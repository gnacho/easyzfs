// Package actions — operaciones ZFS/SMART reales: exec.CommandContext con
// whitelists de nombres, validación de confirm y registro en audit_log.
// Las operaciones largas (scrub, resilver) se LANZAN aquí y se OBSERVAN
// en el colector correspondiente (patrón del skill).
package actions

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"easyzfs/internal/executil"
	"easyzfs/internal/model"
)

// Errores de dominio mapeados a códigos HTTP en httpapi.
var (
	ErrInvalidName   = errors.New("nombre inválido (solo [a-zA-Z0-9_.-/])")
	ErrInvalidDev    = errors.New("dispositivo inválido")
	ErrInvalidTopo   = errors.New("topología inválida")
	ErrInvalidAction = errors.New("acción inválida")
	ErrInvalidInput  = errors.New("entrada inválida")
	ErrSnapshotNotFound = errors.New("el snapshot no existe; refresca la lista")
)

// Whitelists de nombres (lección 6 + ejecución segura del skill).
var (
	rePool     = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$`)
	reDataset  = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*(/[a-zA-Z0-9][a-zA-Z0-9_.-]*)*$`)
	reSnapName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.:-]{0,127}$`)
	reDev      = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.:-]{0,63}$`) // sdb, nvme0n1, ata-XXX…
	// reDevByIDPath — ruta estable completa '/dev/disk/by-id/<nombre>'.
	// Es la forma preferida para el disco NUEVO de un replace: las letras
	// sdX son inestables entre arranques y movimientos de bahía (issue #65).
	// No se admite '/dev/sdX': solo nombres base o rutas by-id.
	reDevByIDPath = regexp.MustCompile(`^/dev/disk/by-id/[a-zA-Z0-9][a-zA-Z0-9_.:-]{0,127}$`)
)

// validNewDev — el destino de un replace/attach puede ser un nombre base
// ('sda', 'nvme0n1', nombre by-id sin ruta) o una ruta '/dev/disk/by-id/…'.
func validNewDev(dev string) bool {
	return reDev.MatchString(dev) || reDevByIDPath.MatchString(dev)
}

// ValidTopos — topologías admitidas al crear/añadir vdevs.
var ValidTopos = map[string]bool{
	"stripe": true, "mirror": true, "raidz1": true, "raidz2": true, "raidz3": true,
}

// DiffEntry — entrada de 'zfs diff -FHt' (un cambio entre dos snapshots).
type DiffEntry struct {
	Type    string `json:"type"`     // M, +, -, R
	Path    string `json:"path"`
	NewPath string `json:"new_path,omitempty"`
}

// Service ejecuta operaciones contra el sistema y las audita.
type Service struct {
	db *sql.DB
}

// NewService crea el servicio de acciones.
func NewService(d *sql.DB) *Service {
	return &Service{db: d}
}

// audit registra la acción en audit_log (obligatorio en destructivas).
func (s *Service) audit(ctx context.Context, actor, action, target string, params any, confirmed bool) {
	detail, _ := json.Marshal(params)
	conf := 0
	if confirmed {
		conf = 1
	}
	if _, err := s.db.ExecContext(ctx,
		"INSERT INTO audit_log(ts, actor, action, target, detail, confirmed) VALUES (?,?,?,?,?,?)",
		time.Now().UTC().Format(time.RFC3339), actor, action, target, string(detail), conf); err != nil {
		log.Printf("audit: %v", err)
	}
}

// AuditOnly registra en audit_log acciones administrativas que no ejecutan CLI
// (gestión de usuarios, cambios de ajustes…).
func (s *Service) AuditOnly(ctx context.Context, actor, action, target string, params any) {
	s.audit(ctx, actor, action, target, params, false)
}

// --- Pools ---

// PoolCreate — 'zpool create [-o ashift=N] <name> [topo] <disks...>'.
// confirmed debe ser true solo si el handler validó {"confirm":name}.
// ashift: 0 = automático (no se pasa -o); 9-16 = alineación explícita
// (12 para discos 4K, 13 para algunos NVMe; inmutable tras crear).
func (s *Service) PoolCreate(ctx context.Context, actor, name, topo string, disks []string, ashift int, confirmed bool) error {
	if !rePool.MatchString(name) {
		return ErrInvalidName
	}
	if !ValidTopos[topo] {
		return ErrInvalidTopo
	}
	if ashift != 0 && (ashift < 9 || ashift > 16) {
		return fmt.Errorf("%w: ashift debe estar entre 9 y 16 (o 0 = automático)", ErrInvalidInput)
	}
	args, err := vdevArgs(topo, disks)
	if err != nil {
		return err
	}
	cli := []string{"create"}
	if ashift > 0 {
		cli = append(cli, "-o", fmt.Sprintf("ashift=%d", ashift))
	}
	cli = append(cli, name)
	cli = append(cli, args...)
	params := map[string]any{"topo": topo, "disks": disks, "ashift": ashift}
	s.audit(ctx, actor, "pool.create", name, params, confirmed)
	_, err = executil.Run(ctx, 60*time.Second, "zpool", cli...)
	if err != nil {
		return fmt.Errorf("crear pool: %w", err)
	}
	return nil
}

// PoolImportList — pools importables ('zpool import' sin args, parseo mejor esfuerzo).
func (s *Service) PoolImportList(ctx context.Context) ([]string, error) {
	out, err := executil.Run(ctx, 15*time.Second, "zpool", "import")
	if err != nil && len(out) == 0 {
		return nil, err
	}
	names := []string{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "pool:") {
			names = append(names, strings.TrimSpace(strings.TrimPrefix(line, "pool:")))
		}
	}
	return names, nil
}

// PoolImport — 'zpool import <name>'.
func (s *Service) PoolImport(ctx context.Context, actor, name string) error {
	if !rePool.MatchString(name) {
		return ErrInvalidName
	}
	s.audit(ctx, actor, "pool.import", name, nil, false)
	if _, err := executil.Run(ctx, 60*time.Second, "zpool", "import", name); err != nil {
		return fmt.Errorf("importar pool: %w", err)
	}
	return nil
}

// PoolExport — 'zpool export [-f] <name>'; destroy=true → 'zpool destroy' (requiere confirm).
func (s *Service) PoolExport(ctx context.Context, actor, name string, force, destroy bool) error {
	if !rePool.MatchString(name) {
		return ErrInvalidName
	}
	s.audit(ctx, actor, "pool.export", name,
		map[string]any{"force": force, "destroy": destroy}, true)
	if destroy {
		if _, err := executil.Run(ctx, 60*time.Second, "zpool", "destroy", name); err != nil {
			return fmt.Errorf("destruir pool: %w", err)
		}
		return nil
	}
	args := []string{"export"}
	if force {
		args = append(args, "-f")
	}
	args = append(args, name)
	if _, err := executil.Run(ctx, 60*time.Second, "zpool", args...); err != nil {
		return fmt.Errorf("exportar pool: %w", err)
	}
	return nil
}

// Scrub — 'zpool scrub [-p|-s] <pool>'; se observa en el colector zpool.
func (s *Service) Scrub(ctx context.Context, actor, pool, action string) error {
	if !rePool.MatchString(pool) {
		return ErrInvalidName
	}
	args := []string{"scrub"}
	switch action {
	case "start":
	case "pause":
		args = append(args, "-p")
	case "stop":
		args = append(args, "-s")
	default:
		return ErrInvalidAction
	}
	args = append(args, pool)
	s.audit(ctx, actor, "pool.scrub."+action, pool, nil, false)
	if _, err := executil.Run(ctx, 15*time.Second, "zpool", args...); err != nil {
		return fmt.Errorf("scrub %s: %w", action, err)
	}
	return nil
}

// Trim — 'zpool trim <pool>': libera los bloques no usados hacia el SSD.
// No es destructivo ni verifica datos (no confundir con scrub); confirm=false.
func (s *Service) Trim(ctx context.Context, actor, pool string) error {
	if !rePool.MatchString(pool) {
		return ErrInvalidName
	}
	s.audit(ctx, actor, "pool.trim", pool, nil, false)
	if _, err := executil.Run(ctx, 15*time.Second, "zpool", "trim", pool); err != nil {
		return fmt.Errorf("trim: %w", err)
	}
	return nil
}

// SetAutotrim — 'zpool set autotrim=on|off <pool>': activa/desactiva el TRIM
// continuo del pool (solo aplicable a SSD; en HDD no tiene efecto).
func (s *Service) SetAutotrim(ctx context.Context, actor, pool string, enabled bool) error {
	if !rePool.MatchString(pool) {
		return ErrInvalidName
	}
	val := "off"
	if enabled {
		val = "on"
	}
	s.audit(ctx, actor, "pool.autotrim", pool, map[string]any{"enabled": enabled}, false)
	if _, err := executil.Run(ctx, 15*time.Second, "zpool", "set", "autotrim="+val, pool); err != nil {
		return fmt.Errorf("autotrim: %w", err)
	}
	return nil
}

// CheckpointCreate — 'zpool checkpoint <pool>': punto de restauración del pool
// completo (reversible con 'zpool import --rewind-to-checkpoint'). Mientras
// exista bloquea remove/attach/detach y retiene espacio liberado.
func (s *Service) CheckpointCreate(ctx context.Context, actor, pool string) error {
	if !rePool.MatchString(pool) {
		return ErrInvalidName
	}
	s.audit(ctx, actor, "pool.checkpoint.create", pool, nil, true)
	if _, err := executil.Run(ctx, 30*time.Second, "zpool", "checkpoint", pool); err != nil {
		return fmt.Errorf("checkpoint: %w", err)
	}
	return nil
}

// CheckpointDiscard — 'zpool checkpoint -d <pool>': descarta el checkpoint
// (ya no se podrá rebobinar; libera el espacio retenido).
func (s *Service) CheckpointDiscard(ctx context.Context, actor, pool string) error {
	if !rePool.MatchString(pool) {
		return ErrInvalidName
	}
	s.audit(ctx, actor, "pool.checkpoint.discard", pool, nil, true)
	if _, err := executil.Run(ctx, 30*time.Second, "zpool", "checkpoint", "-d", pool); err != nil {
		return fmt.Errorf("descartar checkpoint: %w", err)
	}
	return nil
}

// VdevAdd — 'zpool add <pool> [topo] <disks...>'.
// confirmed debe ser true solo si el handler validó {"confirm":pool}.
func (s *Service) VdevAdd(ctx context.Context, actor, pool, topo string, disks []string, confirmed bool) error {
	if !rePool.MatchString(pool) {
		return ErrInvalidName
	}
	if !ValidTopos[topo] {
		return ErrInvalidTopo
	}
	args, err := vdevArgs(topo, disks)
	if err != nil {
		return err
	}
	s.audit(ctx, actor, "pool.vdev.add", pool,
		map[string]any{"topo": topo, "disks": disks}, confirmed)
	_, err = executil.Run(ctx, 60*time.Second, "zpool",
		append([]string{"add", pool}, args...)...)
	if err != nil {
		return fmt.Errorf("añadir vdev: %w", err)
	}
	return nil
}

// VdevAction — 'zpool offline|online|detach <pool> <dev>'.
// offline/online no son destructivos (detach sí: confirmed debe venir validado).
// Nota: detach solo es válido en mirrors (lo restringe el propio zpool).
func (s *Service) VdevAction(ctx context.Context, actor, pool, dev, action string, confirmed bool) error {
	if !rePool.MatchString(pool) {
		return ErrInvalidName
	}
	if !reDev.MatchString(dev) {
		return ErrInvalidDev
	}
	switch action {
	case "offline", "online", "detach":
	default:
		return ErrInvalidAction
	}
	s.audit(ctx, actor, "pool.vdev."+action, pool, map[string]any{"dev": dev}, confirmed)
	if _, err := executil.Run(ctx, 60*time.Second, "zpool", action, pool, dev); err != nil {
		return fmt.Errorf("%s: %w", action, err)
	}
	return nil
}

// VdevSize — tamaño en bytes de un vdev hoja ('zpool list -Hp -v').
// Devuelve 0 si no se encuentra (el llamador decide tolerarlo).
func (s *Service) VdevSize(ctx context.Context, pool, dev string) (uint64, error) {
	if !rePool.MatchString(pool) {
		return 0, ErrInvalidName
	}
	out, err := executil.Run(ctx, 15*time.Second, "zpool", "list", "-Hp", "-v",
		"-o", "name,size", pool)
	if err != nil {
		return 0, fmt.Errorf("vdev size: %w", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 2 {
			continue
		}
		name := strings.TrimPrefix(f[0], "/dev/")
		if name == dev || f[0] == dev {
			sz, _ := strconv.ParseUint(f[1], 10, 64)
			return sz, nil
		}
	}
	return 0, nil
}

// PowerOff — apaga un disco para extracción: 'udisksctl power-off' y, si no
// está disponible, 'hdparm -y' (standby). El handler debe vetar miembros de
// pool y discos montados.
func (s *Service) PowerOff(ctx context.Context, actor, dev string) error {
	if !reDev.MatchString(dev) {
		return ErrInvalidDev
	}
	s.audit(ctx, actor, "disk.poweroff", dev, nil, false)
	if _, err := executil.Run(ctx, 15*time.Second, "udisksctl", "power-off", "-b", "/dev/"+dev); err == nil {
		return nil
	}
	if _, err := executil.Run(ctx, 15*time.Second, "hdparm", "-y", "/dev/"+dev); err != nil {
		return fmt.Errorf("apagar disco: %w", err)
	}
	return nil
}

// --- tareas del sistema (vía helper root confinado easyzfs-sysd) ---

// sysdHelper — única vía de escritura sobre /etc/cron* y /etc/systemd.
// EASYZFS_SYSD_HELPER permite sobreescribir la ruta (tests).
var sysdHelper = func() string {
	if h := os.Getenv("EASYZFS_SYSD_HELPER"); h != "" {
		return h
	}
	return "/usr/local/libexec/easyzfs-sysd"
}()

var (
	reCronSched = regexp.MustCompile(`^(@(yearly|annually|monthly|weekly|daily|midnight|hourly)|[0-9A-Za-z*,/\-]+( +[0-9A-Za-z*,/\-]+){4})$`)
	reTaskName  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,40}$`)
)

// SysTaskSetSchedule — cambia la periodicidad de una tarea del sistema.
// Cron: schedule de 5 campos o @atajo. Systemd: OnCalendar (valida el helper
// con systemd-analyze). La identidad de la tarea la valida el handler contra
// la caché del colector.
func (s *Service) SysTaskSetSchedule(ctx context.Context, actor string, task model.SysTimer, schedule string) error {
	s.audit(ctx, actor, "systask.schedule", task.Name,
		map[string]any{"source": task.Source, "origin": task.Origin, "line": task.Line, "schedule": schedule}, true)
	if task.Source == "systemd" {
		if !strings.HasSuffix(task.Name, ".timer") {
			return ErrInvalidName
		}
		if _, err := executil.Run(ctx, 30*time.Second, sysdHelper, "timer-set", task.Name, schedule); err != nil {
			return fmt.Errorf("cambiar timer: %w", err)
		}
		return nil
	}
	if !reCronSched.MatchString(schedule) {
		return ErrInvalidInput
	}
	if task.Line < 1 || !strings.HasPrefix(task.Origin, "/etc/") {
		return ErrInvalidInput
	}
	if _, err := executil.Run(ctx, 30*time.Second, sysdHelper, "cron-set",
		task.Origin, strconv.Itoa(task.Line), schedule); err != nil {
		return fmt.Errorf("cambiar cron: %w", err)
	}
	return nil
}

// SysTaskMigrate — migra una entrada cron (fichero /etc) a systemd timer.
func (s *Service) SysTaskMigrate(ctx context.Context, actor string, task model.SysTimer, newName string) error {
	if task.Source != "cron" || task.Line < 1 || !strings.HasPrefix(task.Origin, "/etc/") {
		return ErrInvalidInput
	}
	if !reTaskName.MatchString(newName) {
		return ErrInvalidName
	}
	s.audit(ctx, actor, "systask.migrate", task.Name,
		map[string]any{"origin": task.Origin, "line": task.Line, "unit": "easyzfs-" + newName + ".timer"}, true)
	if _, err := executil.Run(ctx, 30*time.Second, sysdHelper, "cron-to-timer",
		task.Origin, strconv.Itoa(task.Line), newName); err != nil {
		return fmt.Errorf("migrar a systemd: %w", err)
	}
	return nil
}

// Replace — 'zpool replace <pool> <old> <new>'; el resilver se observa en el colector.
// confirmed debe ser true solo si el handler validó {"confirm":pool}.
// newDev debería llegar ya resuelto a la ruta estable '/dev/disk/by-id/…'
// (lo hace el handler con el inventario de discos); se acepta también un
// nombre base como fallback cuando el disco no tiene enlace by-id.
func (s *Service) Replace(ctx context.Context, actor, pool, oldDev, newDev string, confirmed bool) error {
	if !rePool.MatchString(pool) {
		return ErrInvalidName
	}
	if !reDev.MatchString(oldDev) || !validNewDev(newDev) {
		return ErrInvalidDev
	}
	s.audit(ctx, actor, "pool.replace", pool,
		map[string]any{"old_dev": oldDev, "new_dev": newDev}, confirmed)
	if _, err := executil.Run(ctx, 60*time.Second, "zpool", "replace",
		pool, oldDev, newDev); err != nil {
		return fmt.Errorf("replace: %w", err)
	}
	return nil
}

// reRaidzVdev — whitelist del objetivo de RAID-Z expansion ('raidz2-0').
var reRaidzVdev = regexp.MustCompile(`^raidz[123]-\d+$`)

// PoolExpand — 'zpool attach <pool> <vdev-raidz> <disco>' (RAID-Z expansion,
// OpenZFS ≥ 2.3). Añade UN disco a un vdev raidz existente; la relocalización
// de bloques se observa como scan 'expand' en el colector zpool.
// La pertenencia del vdev al pool y la disponibilidad del disco las valida el
// handler contra la caché de colectores (aquí solo whitelists + audit).
func (s *Service) PoolExpand(ctx context.Context, actor, pool, vdev, disk string, confirmed bool) error {
	if !rePool.MatchString(pool) {
		return ErrInvalidName
	}
	if !reRaidzVdev.MatchString(vdev) {
		return fmt.Errorf("%w: el vdev objetivo debe ser raidz[123]-N", ErrInvalidInput)
	}
	if !reDev.MatchString(disk) {
		return ErrInvalidDev
	}
	s.audit(ctx, actor, "pool.expand", pool,
		map[string]any{"vdev": vdev, "disk": disk}, confirmed)
	if _, err := executil.Run(ctx, 60*time.Second, "zpool", "attach", pool, vdev, disk); err != nil {
		return fmt.Errorf("expandir raidz: %w", err)
	}
	return nil
}

// vdevArgs traduce topología + lista de discos a argumentos de zpool.
// stripe: discos sueltos; mirror/raidzN: palabra clave + discos.
func vdevArgs(topo string, disks []string) ([]string, error) {
	if len(disks) == 0 {
		return nil, fmt.Errorf("%w: se requiere al menos 1 disco", ErrInvalidDev)
	}
	args := []string{}
	if topo != "stripe" {
		// mirror|raidz1|raidz2|raidz3 son palabras clave válidas de zpool
		args = append(args, topo)
	}
	for _, d := range disks {
		if !reDev.MatchString(d) {
			return nil, ErrInvalidDev
		}
		args = append(args, d)
	}
	return args, nil
}

// --- Datasets ---

// DatasetCreate — 'zfs create [-p] [-o compression=..] [-o atime=..] [-o quota=..]
// [-V size] [-o encryption=aes-256-gcm -o keyformat=passphrase -o keylocation=prompt] <pool/name>'.
// Con encrypted=true la passphrase va SOLO por stdin (nunca argv/logs/audit) y
// el buffer se limpia antes de volver.
// atime: "" (no tocar, hereda del pool) | "on" | "off" | "relatime" (recomendado).
func (s *Service) DatasetCreate(ctx context.Context, actor, pool, name, typ, compression string,
	quota, volsize uint64, encrypted bool, passphrase, atime string) error {
	if !rePool.MatchString(pool) || !reDataset.MatchString(name) {
		return ErrInvalidName
	}
	full := pool + "/" + name
	if !reDataset.MatchString(full) {
		return ErrInvalidName
	}
	if compression != "lz4" && compression != "zstd" && compression != "off" {
		return fmt.Errorf("compresión inválida (lz4|zstd|off)")
	}
	if atime != "" && atime != "on" && atime != "off" && atime != "relatime" {
		return fmt.Errorf("%w: atime debe ser on, off o relatime", ErrInvalidInput)
	}
	args := []string{"create", "-p", "-o", "compression=" + compression}
	if atime != "" {
		args = append(args, "-o", "atime="+atime)
	}
	if quota > 0 {
		args = append(args, "-o", "quota="+strconv.FormatUint(quota, 10))
	}
	if typ == "volume" {
		if volsize == 0 {
			return fmt.Errorf("volsize_bytes requerido para type=volume")
		}
		args = append(args, "-V", strconv.FormatUint(volsize, 10))
	} else if typ != "fs" {
		return fmt.Errorf("type inválido (fs|volume)")
	}
	var keyBuf []byte
	if encrypted {
		if len(passphrase) < 8 {
			return fmt.Errorf("%w: la passphrase debe tener al menos 8 caracteres", ErrInvalidInput)
		}
		args = append(args, "-o", "encryption=aes-256-gcm",
			"-o", "keyformat=passphrase", "-o", "keylocation=prompt")
		// zfs pide la passphrase DOS veces por prompt (verificación).
		keyBuf = []byte(passphrase + "\n" + passphrase + "\n")
		defer executil.Zero(keyBuf)
	}
	args = append(args, full)
	s.audit(ctx, actor, "dataset.create", full, map[string]any{
		"type": typ, "compression": compression, "atime": atime,
		"quota_bytes": quota, "volsize_bytes": volsize, "encrypted": encrypted, // NUNCA la passphrase
	}, false)
	var err error
	if encrypted {
		_, err = executil.RunStdin(ctx, 30*time.Second, keyBuf, "zfs", args...)
	} else {
		_, err = executil.Run(ctx, 30*time.Second, "zfs", args...)
	}
	if err != nil {
		return fmt.Errorf("crear dataset: %w", err)
	}
	return nil
}

// DatasetLoadKey — 'zfs load-key <name>' con la passphrase por stdin
// (sin -n: carga la clave y monta). Desbloquea datasets cifrados.
func (s *Service) DatasetLoadKey(ctx context.Context, actor, name, passphrase string) error {
	if !reDataset.MatchString(name) {
		return ErrInvalidName
	}
	if passphrase == "" {
		return fmt.Errorf("%w: passphrase requerida", ErrInvalidInput)
	}
	s.audit(ctx, actor, "dataset.unlock", name, nil, false) // SIN la clave
	keyBuf := []byte(passphrase + "\n")
	defer executil.Zero(keyBuf)
	if _, err := executil.RunStdin(ctx, 30*time.Second, keyBuf, "zfs", "load-key", name); err != nil {
		return fmt.Errorf("desbloquear dataset: %w", err)
	}
	return nil
}

// DatasetUnloadKey — 'zfs unload-key <name>': desmonta y retira la clave.
// Si el dataset está ocupado zfs falla y se devuelve el error legible
// (NO se fuerza con -f: sería un desmontaje destructivo de ficheros abiertos).
func (s *Service) DatasetUnloadKey(ctx context.Context, actor, name string) error {
	if !reDataset.MatchString(name) {
		return ErrInvalidName
	}
	s.audit(ctx, actor, "dataset.lock", name, nil, false)
	if _, err := executil.Run(ctx, 30*time.Second, "zfs", "unload-key", name); err != nil {
		return fmt.Errorf("bloquear dataset: %w", err)
	}
	return nil
}

// DatasetChangeKey — 'zfs change-key -o keyformat=passphrase <name>' con la
// NUEVA passphrase por stdin (zfs la pide dos veces; con keyformat=passphrase
// y la clave cargada no pide la actual, por eso currentKey no se usa — el
// handler la exige como confirmación de posesión, documentado en el contrato).
func (s *Service) DatasetChangeKey(ctx context.Context, actor, name, newPassphrase string) error {
	if !reDataset.MatchString(name) {
		return ErrInvalidName
	}
	if len(newPassphrase) < 8 {
		return fmt.Errorf("%w: la passphrase nueva debe tener al menos 8 caracteres", ErrInvalidInput)
	}
	s.audit(ctx, actor, "dataset.change_key", name, nil, false) // SIN claves
	keyBuf := []byte(newPassphrase + "\n" + newPassphrase + "\n")
	defer executil.Zero(keyBuf)
	if _, err := executil.RunStdin(ctx, 30*time.Second, keyBuf, "zfs",
		"change-key", "-o", "keyformat=passphrase", name); err != nil {
		return fmt.Errorf("cambiar clave: %w", err)
	}
	return nil
}

// DatasetPatch — 'zfs set quota=.. compression=.. <name>'.
func (s *Service) DatasetPatch(ctx context.Context, actor, name string,
	quota *uint64, compression *string) error {
	if !reDataset.MatchString(name) {
		return ErrInvalidName
	}
	props := []string{}
	if quota != nil {
		props = append(props, "quota="+strconv.FormatUint(*quota, 10))
	}
	if compression != nil {
		if *compression != "lz4" && *compression != "zstd" && *compression != "off" {
			return fmt.Errorf("compresión inválida (lz4|zstd|off)")
		}
		props = append(props, "compression="+*compression)
	}
	if len(props) == 0 {
		return nil
	}
	s.audit(ctx, actor, "dataset.patch", name, map[string]any{"props": props}, false)
	for _, p := range props {
		if _, err := executil.Run(ctx, 15*time.Second, "zfs", "set", p, name); err != nil {
			return fmt.Errorf("set %s: %w", p, err)
		}
	}
	return nil
}

// DatasetDelete — 'zfs destroy [-r] <name>' (destructiva: confirm obligatorio fuera).
func (s *Service) DatasetDelete(ctx context.Context, actor, name string, recursive bool) error {
	if !reDataset.MatchString(name) {
		return ErrInvalidName
	}
	s.audit(ctx, actor, "dataset.delete", name,
		map[string]any{"recursive": recursive}, true)
	args := []string{"destroy"}
	if recursive {
		args = append(args, "-r")
	}
	args = append(args, name)
	if _, err := executil.Run(ctx, 60*time.Second, "zfs", args...); err != nil {
		return fmt.Errorf("borrar dataset: %w", err)
	}
	return nil
}

// --- Snapshots ---

// SnapshotCreate — 'zfs snapshot [-r] <dataset>@<name>'.
func (s *Service) SnapshotCreate(ctx context.Context, actor, dataset, name string, recursive bool) error {
	if !reDataset.MatchString(dataset) || !reSnapName.MatchString(name) {
		return ErrInvalidName
	}
	full := dataset + "@" + name
	args := []string{"snapshot"}
	if recursive {
		args = append(args, "-r")
	}
	args = append(args, full)
	s.audit(ctx, actor, "snapshot.create", full, map[string]any{"recursive": recursive}, false)
	if _, err := executil.Run(ctx, 30*time.Second, "zfs", args...); err != nil {
		return fmt.Errorf("crear snapshot: %w", err)
	}
	return nil
}

// SnapshotDelete — 'zfs destroy <dataset>@<snap>' (destructiva).
func (s *Service) SnapshotDelete(ctx context.Context, actor, full string) error {
	ds, snap, ok := strings.Cut(full, "@")
	if !ok || !reDataset.MatchString(ds) || !reSnapName.MatchString(snap) {
		return ErrInvalidName
	}
	s.audit(ctx, actor, "snapshot.delete", full, nil, true)
	if _, err := executil.Run(ctx, 30*time.Second, "zfs", "destroy", full); err != nil {
		if strings.Contains(err.Error(), "could not find any snapshots to destroy") {
			return ErrSnapshotNotFound
		}
		return fmt.Errorf("borrar snapshot: %w", err)
	}
	return nil
}

// SnapshotRollback — 'zfs rollback -r <dataset>@<snap>' (destructiva).
func (s *Service) SnapshotRollback(ctx context.Context, actor, full string) error {
	ds, snap, ok := strings.Cut(full, "@")
	if !ok || !reDataset.MatchString(ds) || !reSnapName.MatchString(snap) {
		return ErrInvalidName
	}
	s.audit(ctx, actor, "snapshot.rollback", full, nil, true)
	if _, err := executil.Run(ctx, 60*time.Second, "zfs", "rollback", "-r", full); err != nil {
		if strings.Contains(err.Error(), "dataset does not exist") {
			return ErrSnapshotNotFound
		}
		return fmt.Errorf("rollback: %w", err)
	}
	return nil
}

// SnapshotPrune — borra snapshots automáticos del dataset más viejos que cutoff.
// Devuelve cuántos se han borrado. Usado por el scheduler (retención).
func (s *Service) SnapshotPrune(ctx context.Context, actor, dataset string, cutoff time.Time) (int, error) {
	out, err := executil.Run(ctx, 15*time.Second, "zfs", "list", "-Hp", "-r",
		"-t", "snapshot", "-o", "name,creation", dataset)
	if err != nil {
		return 0, err
	}
	deleted := 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 2 {
			continue
		}
		ds, snap, ok := strings.Cut(f[0], "@")
		// El scheduler crea con -r: podar el dataset y todo su árbol.
		inTree := ds == dataset || strings.HasPrefix(ds, dataset+"/")
		if !ok || !inTree || !strings.HasPrefix(snap, model.AutoSnapPrefix) {
			continue
		}
		epoch, _ := strconv.ParseInt(f[1], 10, 64)
		if time.Unix(epoch, 0).After(cutoff) {
			continue
		}
		if _, err := executil.Run(ctx, 30*time.Second, "zfs", "destroy", f[0]); err != nil {
			log.Printf("prune %s: %v", f[0], err)
			continue
		}
		deleted++
	}
	if deleted > 0 {
		s.audit(ctx, actor, "snapshot.prune", dataset,
			map[string]any{"deleted": deleted, "cutoff": cutoff.Format(time.RFC3339)}, false)
	}
	return deleted, nil
}

// SnapshotDiff — 'zfs diff -FHt <older> <newer>': lista los cambios entre
// dos snapshots de un mismo dataset. Lectura pura (sin audit ni confirm).
func (s *Service) SnapshotDiff(ctx context.Context, older, newer string) ([]DiffEntry, error) {
	oldDS, oldSnap, ok := strings.Cut(older, "@")
	if !ok || !reDataset.MatchString(oldDS) || !reSnapName.MatchString(oldSnap) {
		return nil, ErrInvalidName
	}
	newDS, newSnap, ok := strings.Cut(newer, "@")
	if !ok || !reDataset.MatchString(newDS) || !reSnapName.MatchString(newSnap) {
		return nil, ErrInvalidName
	}
	out, err := executil.Run(ctx, 30*time.Second, "zfs", "diff", "-FHt", older, newer)
	if err != nil {
		return nil, fmt.Errorf("diff entre snapshots: %w", err)
	}
	var entries []DiffEntry
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		f := strings.Split(line, "\t")
		// zfs diff -FHt → ts\ttype\tpath[\tnew_path]
		if len(f) < 3 {
			continue
		}
		e := DiffEntry{Type: f[1], Path: f[2]}
		if e.Type == "R" && len(f) > 3 {
			e.NewPath = f[3]
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// --- SMART ---

// SmartTest — 'smartctl -t short|long /dev/<dev>'; el resultado se observa en el colector.
func (s *Service) SmartTest(ctx context.Context, actor, dev, testType string) error {
	if !reDev.MatchString(dev) {
		return ErrInvalidDev
	}
	if testType != "short" && testType != "long" {
		return fmt.Errorf("type inválido (short|long)")
	}
	s.audit(ctx, actor, "disk.smart_test."+testType, dev, nil, false)
	out, err := executil.RunTolerant(ctx, 15*time.Second, "smartctl",
		"-t", testType, "/dev/"+dev)
	if err != nil {
		outStr := string(out)
		if strings.Contains(outStr, "Testing has begun") ||
			strings.Contains(outStr, "Self-test has begun") {
			return nil
		}
		return fmt.Errorf("smart test: %w", err)
	}
	return nil
}

// --- Clone + promote ---

// SnapshotClone — 'zfs clone [-o mountpoint=..] <snapshot>@<snap> <target>'.
// No destructivo: el clone hereda los bloques del snapshot (copy-on-write)
// y comparte espacio hasta que diverge.
func (s *Service) SnapshotClone(ctx context.Context, actor, snapshotFull, target, mountpoint string) error {
	ds, snap, ok := strings.Cut(snapshotFull, "@")
	if !ok || !reDataset.MatchString(ds) || !reSnapName.MatchString(snap) {
		return ErrInvalidName
	}
	if !reDataset.MatchString(target) {
		return ErrInvalidName
	}
	args := []string{"clone"}
	if mountpoint != "" {
		args = append(args, "-o", "mountpoint="+mountpoint)
	}
	args = append(args, snapshotFull, target)
	s.audit(ctx, actor, "snapshot.clone", target,
		map[string]any{"snapshot": snapshotFull, "mountpoint": mountpoint}, false)
	if _, err := executil.Run(ctx, 30*time.Second, "zfs", args...); err != nil {
		return fmt.Errorf("clonar snapshot: %w", err)
	}
	return nil
}

// DatasetPromote — 'zfs promote <name>'. Invierte la relación de clonación:
// el dataset promocionado se convierte en el origen (padre) y el dataset que
// lo clonó pasa a ser su hijo. No destructivo, pero irreversible.
func (s *Service) DatasetPromote(ctx context.Context, actor, name string) error {
	if !reDataset.MatchString(name) {
		return ErrInvalidName
	}
	s.audit(ctx, actor, "dataset.promote", name, nil, false)
	if _, err := executil.Run(ctx, 30*time.Second, "zfs", "promote", name); err != nil {
		return fmt.Errorf("promocionar dataset: %w", err)
	}
	return nil
}

// DatasetRename — 'zfs rename <old> <new>'. Renombra un dataset y todos sus
// hijos. Irreversible pero no destructivo (no borra datos).
func (s *Service) DatasetRename(ctx context.Context, actor, oldName, newName string) error {
	if !reDataset.MatchString(oldName) || !reDataset.MatchString(newName) {
		return ErrInvalidName
	}
	s.audit(ctx, actor, "dataset.rename", oldName,
		map[string]any{"new": newName}, false)
	if _, err := executil.Run(ctx, 30*time.Second, "zfs", "rename", oldName, newName); err != nil {
		return fmt.Errorf("renombrar dataset: %w", err)
	}
	return nil
}

// DatasetMount — 'zfs mount <name>'. Monta un dataset desmontado.
// Idempotente: si ya está montado, zfs no hace nada.
func (s *Service) DatasetMount(ctx context.Context, actor, name string) error {
	if !reDataset.MatchString(name) {
		return ErrInvalidName
	}
	s.audit(ctx, actor, "dataset.mount", name, nil, false)
	if _, err := executil.Run(ctx, 30*time.Second, "zfs", "mount", name); err != nil {
		return fmt.Errorf("montar dataset: %w", err)
	}
	return nil
}

// DatasetUnmount — 'zfs unmount <name>' (sin -f). Desmonta un dataset.
// Si está ocupado (ficheros abiertos), zfs falla y se devuelve su error
// legible (mismo criterio que unload-key: no forzar desmontaje destructivo).
func (s *Service) DatasetUnmount(ctx context.Context, actor, name string) error {
	if !reDataset.MatchString(name) {
		return ErrInvalidName
	}
	s.audit(ctx, actor, "dataset.unmount", name, nil, false)
	if _, err := executil.Run(ctx, 30*time.Second, "zfs", "unmount", name); err != nil {
		return fmt.Errorf("desmontar dataset: %w", err)
	}
	return nil
}

// PoolClear — 'zpool clear <pool> [dev]'. Limpia errores de un pool o vdev.
// Idempotente: si no hay errores, zpool no falla.
func (s *Service) PoolClear(ctx context.Context, actor, pool, dev string) error {
	if !rePool.MatchString(pool) {
		return ErrInvalidName
	}
	args := []string{"clear", pool}
	if dev != "" {
		if !reDev.MatchString(dev) {
			return ErrInvalidDev
		}
		args = append(args, dev)
	}
	s.audit(ctx, actor, "pool.clear", pool,
		map[string]any{"dev": dev}, false)
	if _, err := executil.Run(ctx, 30*time.Second, "zpool", args...); err != nil {
		return fmt.Errorf("limpiar errores: %w", err)
	}
	return nil
}
