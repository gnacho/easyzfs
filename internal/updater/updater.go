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
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	selfupdate "github.com/creativeprojects/go-selfupdate"
	"github.com/Masterminds/semver/v3"
)

const (
	repoSlug     = "gnacho/easyzfs"
	checksumsFile = "checksums.txt" // fichero único con los hashes de TODOS los assets
	notesMax     = 600
)

var (
	rxHeading = regexp.MustCompile(`(?m)^#{1,4}\s+.*$`)
	rxBold    = regexp.MustCompile(`\*\*`)
)

// Status — respuesta de GET /api/update/status.
type Status struct {
	Current      string `json:"current"`
	Latest       string `json:"latest"`
	Available    bool   `json:"available"`
	InProgress   bool   `json:"inProgress,omitempty"`
	ReleaseNotes string `json:"releaseNotes,omitempty"`
	ReleaseURL   string `json:"releaseUrl,omitempty"`
}

// Updater gestiona el chequeo y la descarga de actualizaciones.
type Updater struct {
	current string   // versión local (inyectada por ldflags), sin 'v'
	dataDir string   // DATA_DIR; el binario nuevo y el flag viven en dataDir/update/

	mu               sync.Mutex
	currentLatest    string // última versión detectada (cache del último Check)
	currentNotes     string
	currentURL       string
	inProgress       bool
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

func truncateReleaseNotes(body string) string {
	if body == "" {
		return ""
	}
	cleaned := rxHeading.ReplaceAllString(body, "")
	cleaned = rxBold.ReplaceAllString(cleaned, "")
	cleaned = strings.TrimSpace(cleaned)
	if len(cleaned) <= notesMax {
		return cleaned
	}
	cut := cleaned[:notesMax]
	if idx := strings.LastIndexAny(cut, " \n\r\t"); idx > 0 {
		cut = cut[:idx]
	}
	return cut + "…"
}

// updateDir — directorio escribible por el servicio para el binario nuevo y el flag.
func (u *Updater) updateDir() string { return filepath.Join(u.dataDir, "update") }

// RestartFlag — ruta del flag que vigila easyzfs-update.path.
func (u *Updater) RestartFlag() string { return filepath.Join(u.updateDir(), ".restart-me") }

// PendingApply — registro efímero de la actualización en curso para confirmación
// post-reinicio. Se escribe antes de Apply y se consume en el siguiente arranque.
type PendingApply struct {
	FromVersion string `json:"fromVersion"`
	ToVersion   string `json:"toVersion"`
	StartedAt   int64  `json:"startedAt"`
}

func (u *Updater) pendingFile() string { return filepath.Join(u.updateDir(), ".pending-apply") }

// WritePendingApply guarda el registro antes de aplicar. Si el apply falla, el
// archivo se borra; si triunfa, el siguiente arranque lo consume.
func (u *Updater) WritePendingApply(from, to string) error {
	if err := os.MkdirAll(u.updateDir(), 0o750); err != nil {
		return err
	}
	b, _ := json.Marshal(PendingApply{FromVersion: from, ToVersion: to, StartedAt: time.Now().UnixMilli()})
	return os.WriteFile(u.pendingFile(), b, 0o644)
}

// ConsumePendingApply lee el registro pendiente y, si la versión actual es
// distinta de la registrada (el update triunfó), devuelve los datos y borra
// el archivo. Si la versión NO cambió (falló), borra el archivo y devuelve nil.
// Si no hay archivo, devuelve nil.
func (u *Updater) ConsumePendingApply() *PendingApply {
	data, err := os.ReadFile(u.pendingFile())
	if err != nil {
		return nil
	}
	var p PendingApply
	if err := json.Unmarshal(data, &p); err != nil {
		os.Remove(u.pendingFile())
		return nil
	}
	if p.FromVersion != u.current {
		// El update triunfó: confirmar y borrar
		os.Remove(u.pendingFile())
		return &p
	}
	// La versión no cambió (falló o rollback): borrar sin confirmar
	os.Remove(u.pendingFile())
	return nil
}

// ClearPendingApply borra el archivo de pending (usado cuando el apply falla).
func (u *Updater) ClearPendingApply() {
	os.Remove(u.pendingFile())
}

// NewBinary — ruta donde el updater deja el binario nuevo descargado y validado.
func (u *Updater) NewBinary() string { return filepath.Join(u.updateDir(), "easyzfs.new") }

// Status devuelve el estado actual (último resultado del Check; sin red).
func (u *Updater) Status() Status {
	u.mu.Lock()
	defer u.mu.Unlock()
	return Status{Current: u.current, Latest: u.currentLatest, InProgress: u.inProgress, ReleaseNotes: u.currentNotes, ReleaseURL: u.currentURL}
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
	notes := truncateReleaseNotes(latest.ReleaseNotes)
	releaseURL := latest.URL

	// Comparación semver defensiva: si la versión local no es semver válido
	// (p.ej. 'dev' o un build local), se considera al día solo si coincide la
	// cadena; en caso contrario hay versión nueva.
	available := false
	cur, curErr := semver.NewVersion(u.current)
	lat, latErr := semver.NewVersion(latestV)
	if curErr != nil {
		available = latErr == nil && latestV != "" && latestV != u.current
	} else if latErr == nil {
		available = lat.GreaterThan(cur)
	} else {
		available = latestV != "" && latestV != u.current
	}

	u.mu.Lock()
	u.currentLatest = latestV
	u.currentNotes = notes
	u.currentURL = releaseURL
	u.mu.Unlock()

	return Status{Current: u.current, Latest: latestV, Available: available, ReleaseNotes: notes, ReleaseURL: releaseURL}, nil
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
	if !found {
		return errors.New("updater: no hay versión más reciente")
	}
	// ¿Es realmente más reciente? (comparación semver defensiva, sin panic).
	latestV := stripV(latest.Version())
	cur, curErr := semver.NewVersion(u.current)
	lat, latErr := semver.NewVersion(latestV)
	newer := false
	if curErr != nil {
		newer = latErr == nil && latestV != "" && latestV != u.current
	} else if latErr == nil {
		newer = lat.GreaterThan(cur)
	} else {
		newer = latestV != "" && latestV != u.current
	}
	if !newer {
		return errors.New("updater: no hay versión más reciente")
	}

	// Registrar pending apply para confirmación post-reinicio
	if err := u.WritePendingApply(u.current, latestV); err != nil {
		log.Printf("updater: no se pudo escribir pending-apply: %v", err)
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
	//
	// La librería (update.Apply) hace rename(target, target.old) antes de mover
	// el nuevo: con un target inexistente el rename falla (ENOENT). Creamos un
	// placeholder para que el swap funcione en el primer apply.
	if err := os.WriteFile(u.NewBinary(), nil, 0o755); err != nil {
		return fmt.Errorf("updater: placeholder: %w", err)
	}
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

// ---- readiness checks (#30) ----

// Plan — resultado de la comprobación pre-vuelo antes de aplicar.
type Plan struct {
	CanApply bool    `json:"canApply"`
	Checks   []Check `json:"checks"`
}

// Check — una comprobación individual con estado y descripción.
type Check struct {
	ID      string `json:"id"`
	Status  string `json:"status"` // "pass" | "warn" | "fail"
	Title   string `json:"title"`
	Summary string `json:"summary"`
}

// Plan — comprobaciones pre-vuelo locales (espacio, permisos, concurrencia).
func (u *Updater) Plan() Plan {
	u.mu.Lock()
	inProgress := u.inProgress
	u.mu.Unlock()

	checks := []Check{
		{ID: "disk_space", Status: "pass", Title: "Disk space", Summary: "Enough space for download and backup."},
		{ID: "write_perms", Status: "pass", Title: "Write permissions", Summary: "Data directory is writable."},
		{ID: "no_concurrent", Status: "pass", Title: "No update in progress", Summary: "No other update is running."},
	}

	var st syscall.Statfs_t
	if err := syscall.Statfs(u.dataDir, &st); err == nil {
		freeMB := st.Bavail * uint64(st.Bsize) / 1024 / 1024
		if freeMB < 50 {
			checks[0].Status = "fail"
			checks[0].Summary = fmt.Sprintf("Only %d MB free in %s (need at least 50 MB).", freeMB, u.dataDir)
		}
	}

	if err := os.MkdirAll(u.updateDir(), 0o750); err != nil {
		checks[1].Status = "fail"
		checks[1].Summary = fmt.Sprintf("Cannot write to %s: %v", u.updateDir(), err)
	}

	if inProgress {
		checks[2].Status = "fail"
		checks[2].Summary = "An update is already in progress."
	}

	canApply := true
	for _, c := range checks {
		if c.Status == "fail" {
			canApply = false
			break
		}
	}
	return Plan{CanApply: canApply, Checks: checks}
}

// ---- rollback (#29) ----

// Rollback restaura el binario anterior (.old) y toca el flag para que la unit
// systemd lo instale y reinicie.
func (u *Updater) Rollback() error {
	u.mu.Lock()
	if u.inProgress {
		u.mu.Unlock()
		return errors.New("rollback: operation already in progress")
	}
	u.inProgress = true
	u.mu.Unlock()
	defer func() {
		u.mu.Lock()
		u.inProgress = false
		u.mu.Unlock()
	}()

	backup := u.NewBinary() + ".old"
	if _, err := os.Stat(backup); os.IsNotExist(err) {
		return errors.New("rollback: no backup available (.old not found)")
	}
	if err := os.Rename(backup, u.NewBinary()); err != nil {
		return fmt.Errorf("rollback: rename: %w", err)
	}
	if err := os.Chmod(u.NewBinary(), 0o755); err != nil {
		return fmt.Errorf("rollback: chmod: %w", err)
	}
	flag := u.RestartFlag()
	if err := os.WriteFile(flag, []byte(time.Now().Format(time.RFC3339)), 0o644); err != nil {
		return fmt.Errorf("rollback: flag: %w", err)
	}
	log.Printf("[easyzfs] rollback: restored %s → pending install", backup)
	return nil
}
