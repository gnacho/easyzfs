// Package updater — actualización de EasyZFS desde releases semver del repo
// público gnacho/easyzfs (patrón app-auto-update).
//
// Arquitectura (la misma que NetPulse, ya validada):
//   - El proceso corre como el usuario 'easyzfs' SIN permisos de escritura
//     sobre /usr/local/bin/easyzfs (root).
//   - El updater solo DETECTA (semver vs la release más reciente) y DESCARG A+
//     VALIDA el binario nuevo a $DATA_DIR/update/easyzfs.new (escribible por el
//     servicio), tocando después el flag $DATA_DIR/update/.restart-me.
//   - Una unit systemd 'easyzfs-update.path' (root) vigila el flag: instala el
//     binario nuevo sobre /usr/local/bin/easyzfs y reinicia el servicio.
//
// La validación de la descarga la hace creativeprojects/go-selfupdate con
// ChecksumValidator{UniqueFilename: "checksums.txt"} (el checksums.txt único
// del release, que goreleaser/el workflow publica).
package updater

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	selfupdate "github.com/creativeprojects/go-selfupdate"
)

const (
	repoSlug     = "gnacho/easyzfs"
	checksumsFile = "checksums.txt" // fichero único con los hashes de TODOS los assets
)

// Status — respuesta de GET /api/update/status.
type Status struct {
	Current    string `json:"current"`
	Latest     string `json:"latest"`
	Available  bool   `json:"available"`
	InProgress bool   `json:"inProgress,omitempty"`
}

// Updater gestiona el chequeo y la descarga de actualizaciones.
type Updater struct {
	current string   // versión local (inyectada por ldflags), sin 'v'
	dataDir string   // DATA_DIR; el binario nuevo y el flag viven en dataDir/update/

	mu            sync.Mutex
	currentLatest string // última versión detectada (cache del último Check)
	inProgress    bool
}

// New crea el updater. current es la versión del binario (main.version).
func New(current, dataDir string) *Updater {
	return &Updater{current: stripV(current), dataDir: dataDir}
}

func stripV(v string) string {
	if len(v) > 0 && v[0] == 'v' {
		return v[1:]
	}
	return v
}

// updateDir — directorio escribible por el servicio para el binario nuevo y el flag.
func (u *Updater) updateDir() string { return filepath.Join(u.dataDir, "update") }

// RestartFlag — ruta del flag que vigila easyzfs-update.path.
func (u *Updater) RestartFlag() string { return filepath.Join(u.updateDir(), ".restart-me") }

// NewBinary — ruta donde el updater deja el binario nuevo descargado y validado.
func (u *Updater) NewBinary() string { return filepath.Join(u.updateDir(), "easyzfs.new") }

// Status devuelve el estado actual (último resultado del Check; sin red).
func (u *Updater) Status() Status {
	u.mu.Lock()
	defer u.mu.Unlock()
	return Status{Current: u.current, Latest: u.currentLatest, InProgress: u.inProgress}
}

// Check consulta GitHub y devuelve si hay una release semver más reciente.
// No aplica nada. Se puede llamar bajo demanda (botón) o en un ticker.
func (u *Updater) Check(ctx context.Context) (Status, error) {
	u.mu.Lock()
	u.inProgress = false // un check no es un apply
	u.mu.Unlock()

	up, err := selfupdate.NewUpdater(selfupdate.Config{
		Validator: &selfupdate.ChecksumValidator{UniqueFilename: checksumsFile},
	})
	if err != nil {
		return Status{Current: u.current}, fmt.Errorf("updater: config: %w", err)
	}

	latest, found, err := up.DetectLatest(ctx, selfupdate.ParseSlug(repoSlug))
	if err != nil {
		return Status{Current: u.current}, fmt.Errorf("updater: detect: %w", err)
	}
	if !found {
		return Status{Current: u.current, Latest: u.current, Available: false}, nil
	}

	latestV := stripV(latest.Version())
	available := latestV != "" && latestV != u.current && latest.GreaterThan(u.current)

	u.mu.Lock()
	u.currentLatest = latestV
	u.mu.Unlock()

	return Status{Current: u.current, Latest: latestV, Available: available}, nil
}

// Apply descarga+valida el binario nuevo a $DATA_DIR/update/easyzfs.new y toca
// el flag .restart-me. Devuelve error si ya hay un apply en curso. El proceso
// NO se reinicia a sí mismo: la unit systemd .path hace install+restart.
func (u *Updater) Apply(ctx context.Context) error {
	u.mu.Lock()
	if u.inProgress {
		u.mu.Unlock()
		return errors.New("updater: apply ya en curso")
	}
	u.inProgress = true
	u.mu.Unlock()
	defer func() {
		u.mu.Lock()
		u.inProgress = false
		u.mu.Unlock()
	}()

	up, err := selfupdate.NewUpdater(selfupdate.Config{
		Validator: &selfupdate.ChecksumValidator{UniqueFilename: checksumsFile},
	})
	if err != nil {
		return fmt.Errorf("updater: config: %w", err)
	}
	latest, found, err := up.DetectLatest(ctx, selfupdate.ParseSlug(repoSlug))
	if err != nil {
		return fmt.Errorf("updater: detect: %w", err)
	}
	if !found || latest.LessOrEqual(u.current) {
		return errors.New("updater: no hay versión más reciente")
	}

	// Descarga + validación + descompresión a un path temporal, y lo movemos al
	// destino escribible por el servicio. UpdateTo espera el path del ejecutable
	// final; le pasamos el destino real y descargamos a un fichero del updateDir.
	if err := os.MkdirAll(u.updateDir(), 0o750); err != nil {
		return fmt.Errorf("updater: mkdir: %w", err)
	}
	// UpdateTo aplica sobre el path indicado. Como el proceso no es root, el
	// destino NO puede ser /usr/local/bin/easyzfs: usamos dataDir/update y que
	// la unit .path haga el install.
	if err := up.UpdateTo(ctx, latest, u.NewBinary()); err != nil {
		return fmt.Errorf("updater: apply: %w", err)
	}
	if err := os.Chmod(u.NewBinary(), 0o755); err != nil {
		return fmt.Errorf("updater: chmod: %w", err)
	}

	flag := u.RestartFlag()
	if err := os.WriteFile(flag, []byte(time.Now().Format(time.RFC3339)), 0o644); err != nil {
		return fmt.Errorf("updater: flag: %w", err)
	}
	log.Printf("[easyzfs] actualización %s descargada y validada → %s (flag .restart-me)", stripV(latest.Version()), u.NewBinary())
	return nil
}
