// Package model — tipos de dominio compartidos (contrato JSON del API).
// Paquete neutro: lo importan collectors, alerts, actions, scheduler y httpapi
// para evitar ciclos de dependencias.
package model

import "time"

// ScrubInfo — estado del scan en curso/último de un pool (contrato: pools[].scrub).
// Unifica scrub, resilver y trim (todos son "scans" con progreso en OpenZFS).
type ScrubInfo struct {
	State  string    `json:"state"` // "none" | "running" | "done"
	Kind   string    `json:"kind"`  // "scrub" | "resilver" | "trim" | "expand" (vacío si none)
	Pct    float64   `json:"pct"`
	EtaSec int64     `json:"eta_sec"`
	Ts     time.Time `json:"ts"`
	Errors int64     `json:"errors"`
	// Progreso en bytes (mejor esfuerzo: scan_stats examined/to_examine en
	// JSON; "scanned/issued"/"trimmed" en el fallback texto; 0 si no se sabe).
	BytesDone  uint64 `json:"bytes_done,omitempty"`
	BytesTotal uint64 `json:"bytes_total,omitempty"`
}

// Vdev — dispositivo de un pool (contrato: pools[].vdevs[]).
type Vdev struct {
	Dev       string  `json:"dev"`
	Path      string  `json:"path,omitempty"` // ruta real resuelta ('/dev/sda1'); "" si no resoluble (p.ej. disco retirado)
	Role      string  `json:"role"`           // "stripe" | "mirror" | "raidz1" | "raidz2" | "raidz3" | "spare" | "log" | "cache"
	Status    string  `json:"status"`
	TempC     float64 `json:"temp_c"`
	Replacing bool    `json:"replacing,omitempty"` // hijo de un vdev 'replacing-N' (sustitución en curso)
}

// Pool — contrato GET /api/pools.
type Pool struct {
	Name       string    `json:"name"`
	Status     string    `json:"status"` // "ONLINE" | "DEGRADED" | "FAULTED"
	Topo       string    `json:"topo"`   // "stripe" | "mirror" | "raidz1" | "raidz2" | "raidz3"
	UsedBytes  uint64    `json:"used_bytes"`
	TotalBytes uint64    `json:"total_bytes"`
	FragPct    float64   `json:"frag_pct"`
	CompRatio  float64   `json:"comp_ratio"`
	Scrub      ScrubInfo `json:"scrub"`
	Vdevs      []Vdev    `json:"vdevs"`
	Autotrim   bool      `json:"autotrim"`   // propiedad autotrim del pool (TRIM continuo en SSD)
	Checkpoint bool      `json:"checkpoint"` // el pool tiene un checkpoint activo (zpool checkpoint)
	// RaidzVdevs — nombres de los vdevs raidz del pool ("raidz2-0"…), objetivo
	// válido de 'zpool attach <pool> <vdev> <disco>' (RAID-Z expansion, lote D).
	RaidzVdevs []string `json:"raidz_vdevs,omitempty"`
}

// Capabilities — capacidades derivadas de la versión de OpenZFS del host
// (contrato: GET /api/version → capabilities).
type Capabilities struct {
	Rewrite        bool   `json:"rewrite"`         // zfs rewrite (Linux ≥ 2.3.4)
	RaidzExpansion bool   `json:"raidz_expansion"` // expansión raidz en caliente (≥ 2.3)
	ScrubAll       bool   `json:"scrub_all"`       // zpool scrub -a (≥ 2.4)
	ScrubRange     bool   `json:"scrub_range"`     // zpool scrub -S/-E (≥ 2.4)
	ZarcNames      bool   `json:"zarc_names"`      // zarcsummary/zarcstat (≥ 2.4; si no, arc_summary/arcstat)
	JSONOutput     bool   `json:"json_output"`     // salida --json de zpool/zfs (≥ 2.3)
	Version        string `json:"version"`         // "2.3.2" ("desconocida" si no se pudo sondear)
}

// HistoryEntry — línea de 'zpool history -i <pool>' (contrato:
// GET /api/pools/{name}/history, más reciente primero).
type HistoryEntry struct {
	Ts          time.Time `json:"ts"`
	Command     string    `json:"command"`
	DurationSec float64   `json:"duration_sec,omitempty"` // 0 = sin duración registrada
}

// ArcStats — tamaño del ARC y tasa de aciertos (contrato: GET /api/performance).
type ArcStats struct {
	SizeBytes uint64  `json:"size_bytes"`
	HitPct    float64 `json:"hit_pct"`
}

// PoolPerf — throughput actual de un pool en bytes/s.
type PoolPerf struct {
	Name     string  `json:"name"`
	ReadBps  float64 `json:"read_bps"`
	WriteBps float64 `json:"write_bps"`
}

// Performance — contrato GET /api/performance. Arc es nil cuando no hay
// fuente de estadísticas ARC en el sistema (la UI oculta la tarjeta).
type Performance struct {
	Arc   *ArcStats  `json:"arc"`
	Pools []PoolPerf `json:"pools"`
}

// Dataset — contrato GET /api/datasets.
type Dataset struct {
	Name        string `json:"name"`
	Type        string `json:"type"` // "fs" | "volume"
	Compression string `json:"compression"`
	UsedBytes   uint64 `json:"used_bytes"`
	AvailBytes  uint64 `json:"avail_bytes"`
	QuotaBytes  uint64 `json:"quota_bytes"`
	Mountpoint  string `json:"mountpoint"`
	// Encryption — valor efectivo de la propiedad encryption ("off" si no hay
	// cifrado; "aes-256-gcm"… si cifrado, heredado o propio).
	Encryption string `json:"encryption"`
	// KeyStatus — "available" | "unavailable" | "-" (sin cifrado).
	KeyStatus string `json:"keystatus"`
}

// DatasetProp — una propiedad de un dataset (GET /api/datasets/{name}/properties).
type DatasetProp struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Source string `json:"source"` // local | default | inherited | received | temporary | "-"
}

// Snapshot — contrato GET /api/snapshots (dentro de SnapGroup).
type Snapshot struct {
	Name      string    `json:"name"`
	Full      string    `json:"full"` // "tank/docs@snap"
	Ts        time.Time `json:"ts"`
	UsedBytes uint64    `json:"used_bytes"`
	Kind      string    `json:"kind"` // "auto" | "manual"
}

// SnapGroup — snapshots agrupados por dataset.
type SnapGroup struct {
	Dataset string     `json:"dataset"`
	Snaps   []Snapshot `json:"snaps"`
}

// Disk — contrato GET /api/disks.
type Disk struct {
	Dev       string `json:"dev"`
	Model     string `json:"model"`
	Serial    string `json:"serial"`
	SizeBytes uint64 `json:"size_bytes"`
	// ByID — nombre del enlace estable /dev/disk/by-id/<ByID> (modelo+serial
	// o WWN). Vacío si el disco no tiene enlace by-id (p.ej. algunas eMMC).
	// Las operaciones destructivas (replace) deben usarlo en vez de Dev:
	// las letras sdX son inestables entre arranques (issue #65).
	ByID string `json:"by_id,omitempty"`
	// TempC es nil (JSON null) cuando no hay lectura (eMMC, USB sin SAT,
	// smartctl no disponible): "sin dato" no es lo mismo que 0 °C.
	TempC       *float64 `json:"temp_c"`
	Smart       string   `json:"smart"` // "ok" | "warn" | "crit" | "unknown" (sin smartctl: eMMC, USB sin SAT)
	SmartDetail string   `json:"smart_detail"`
	// Contadores SMART estructurados (la UI los traduce a texto humano;
	// SmartDetail queda como forma cruda de respaldo).
	ReallocSectors int64  `json:"realloc_sectors,omitempty"`
	PendingSectors int64  `json:"pending_sectors,omitempty"`
	OfflineUncorr  int64  `json:"offline_uncorr,omitempty"` // attr 198: sectores incorregibles offline
	CrcErrors      int64  `json:"crc_errors,omitempty"`     // attr 199: errores UDMA CRC (cable/puerto SATA)
	// CrcRecent — crecimiento de CrcErrors desde la pasada anterior del
	// colector (mismo disco, por serial). Lo accionable es ESTE delta: el
	// acumulado de por vida no se resetea y persigue al disco equivocado
	// tras un cambio de bahías (caso real bigtank, 4-Ago-2026).
	CrcRecent      int64  `json:"crc_recent,omitempty"`
	NvmeWarn       int    `json:"nvme_warn,omitempty"`
	Pool           string `json:"pool"`
	InUse          bool   `json:"in_use,omitempty"` // particiones montadas o swap activo (no elegible como "libre")
	Hours          uint64 `json:"hours"`
	// SmartFull — detalle SMART completo (U1, fase P1): tabla de atributos,
	// selftests y error log parseados del mismo `smartctl -j -a` de la pasada
	// de 10 min. nil si el disco no habla smartctl o aún no se ha parseado.
	SmartFull *DiskSmartDetail `json:"smart_full,omitempty"`
}

// SmartAttr — una fila de la tabla de atributos SMART.
type SmartAttr struct {
	ID         int      `json:"id"`
	Name       string   `json:"name"`
	Value      int64    `json:"value"`
	Worst      int64    `json:"worst"`
	Thresh     int64    `json:"thresh"`
	Raw        string   `json:"raw"`
	Flags      []string `json:"flags,omitempty"`
	WhenFailed string   `json:"when_failed"` // "-" = dentro de umbral; "Past"/"In the past" = superado
}

// SmartSelftest — una entrada del historial de self-tests SMART.
type SmartSelftest struct {
	Type          string `json:"type"`
	Status        string `json:"status"`
	LifetimeHours int64  `json:"lifetime_hours"`
	Percent       int    `json:"percent"`
}

// SmartErrorEntry — una entrada del log de errores SMART (últimas 5).
type SmartErrorEntry struct {
	LifetimeHours int64  `json:"lifetime_hours,omitempty"`
	ErrorType     string `json:"error_type,omitempty"`
	Detail        string `json:"detail,omitempty"`
}

// SmartErrorLog — resumen del log de errores.
type SmartErrorLog struct {
	Count   int                `json:"count"`
	Entries []SmartErrorEntry  `json:"entries,omitempty"`
}

// DiskSmartDetail — detalle SMART completo de un disco (GET /smart y /smart-log).
type DiskSmartDetail struct {
	Protocol   string          `json:"protocol"` // "ata" | "nvme" | ""
	Attributes []SmartAttr     `json:"attributes"`
	Selftests  []SmartSelftest `json:"selftests"`
	ErrorLog   SmartErrorLog   `json:"error_log"`
}

// Kinds de Recommendation (constantes para que front y tests no usen strings).
const (
	RecReplaceNow  = "replace_now"  // sustituir cuanto antes (crit)
	RecReplaceSoon = "replace_soon" // planificar sustitución (warn)
	RecWatch       = "watch"        // vigilar evolución (info)
	RecCheckCable  = "check_cable"  // revisar cable/puerto/backplane, NO el disco (warn, CRC creciendo)
	RecCrcHistory  = "crc_history"  // CRC alto pero ESTABLE: contexto histórico, sin acción (info)
)

// Razones de Hold (la acción sugerida debe esperar).
const (
	HoldResilver     = "resilver"      // resilver en curso: esperar a que termine
	HoldPoolDegraded = "pool_degraded" // pool degradado: sin margen para retirar discos
	HoldNoRedundancy = "no_redundancy" // pool stripe: sin redundancia
)

// Recommendation — contrato GET /api/recommendations. La UI traduce Kind y
// HoldReason con i18n; los contadores van crudos para mostrar el motivo.
type Recommendation struct {
	Level  string `json:"level"` // "crit" | "warn" | "info"
	Kind   string `json:"kind"`  // RecReplaceNow | RecReplaceSoon | RecWatch | RecCheckCable
	Dev    string `json:"dev"`
	Serial string `json:"serial"`
	Pool   string `json:"pool"`
	// Contadores que motivan la recomendación (0 si no aplican).
	ReallocSectors int64 `json:"realloc_sectors,omitempty"`
	PendingSectors int64 `json:"pending_sectors,omitempty"`
	OfflineUncorr  int64 `json:"offline_uncorr,omitempty"`
	CrcErrors      int64 `json:"crc_errors,omitempty"`
	CrcRecent      int64 `json:"crc_recent,omitempty"`
	// Hold: la acción es correcta pero debe ESPERAR (ver HoldReason).
	Hold       bool   `json:"hold,omitempty"`
	HoldReason string `json:"hold_reason,omitempty"`
}

// SysTimer — contrato GET /api/system-timers: temporizadores que YA existen
// en el sistema (systemd timers y cron), solo lectura. next_run/last_run son
// cadenas de visualización ("" si el sistema no las conoce: cron no las tiene).
type SysTimer struct {
	Source   string `json:"source"`             // "systemd" | "cron"
	Name     string `json:"name"`               // unidad .timer o nombre derivado del comando
	Schedule string `json:"schedule"`           // expr. cron ("0 2 * * *", "@daily") o "" si no se conoce
	NextRun  string `json:"next_run"`           // systemd: NEXT; cron: ""
	LastRun  string `json:"last_run"`           // systemd: LAST; cron: ""
	Command  string `json:"command"`            // unidad activada (systemd) o comando (cron)
	Origin   string `json:"origin"`             // "systemctl list-timers", "crontab", "/etc/crontab", "/etc/cron.d/<f>", "/etc/cron.daily"…
	Line     int    `json:"line,omitempty"`     // cron: nº de línea (1-based) en el fichero origen
	Editable bool   `json:"editable,omitempty"` // cron de fichero (/etc) o timer systemd → se puede editar/migrar
}

// Alert — contrato GET /api/alerts y evento SSE alert.new.
type Alert struct {
	ID      int64     `json:"id"`
	Ts      time.Time `json:"ts"`
	Level   string    `json:"level"`  // "info" | "warn" | "crit"
	Source  string    `json:"source"` // origen lógico: "pool.tank", "disk.sda", "smart.sda", "scrub.tank"…
	Target  string    `json:"target"` // destino navegable en la UI: "pools:<name>", "disks:<dev>", "tasks", "settings" ("" = sin destino)
	Message string    `json:"message"`
	Acked   bool      `json:"acked"`
}

// AutoSnapPrefix — prefijo de los snapshots creados por el scheduler.
const AutoSnapPrefix = "easyzfs-auto-"
