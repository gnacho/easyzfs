// Package collectors — un colector por fuente de datos, con caché en memoria.
// Los handlers HTTP leen la caché (Snapshot), NUNCA ejecutan comandos.
package collectors

import (
	"context"
	"database/sql"

	"easyzfs/internal/alerts"
	"easyzfs/internal/config"
	"easyzfs/internal/hub"
	"easyzfs/internal/model"
)

// Collector — patrón del skill: bucle con ticker que sale al cancelar ctx.
type Collector interface {
	Name() string
	Run(ctx context.Context)
}

// PoolProvider — caché de pools/datasets/snapshots (lo que leen los handlers).
type PoolProvider interface {
	Pools() []model.Pool
	Datasets() []model.Dataset
	SnapshotGroups() []model.SnapGroup
	History(name string) []model.HistoryEntry // historial del pool, más reciente primero
}

// DiskProvider — caché de discos.
type DiskProvider interface {
	Disks() []model.Disk
}

// SysTimerProvider — caché de tareas del sistema (cron + systemd timers).
type SysTimerProvider interface {
	SysTimers() []model.SysTimer
	SystemdAvailable() bool // systemd operativo como init (botón "Cambiar" en UI)
}

// PerfProvider — caché de rendimiento (ARC + iostat por pool).
type PerfProvider interface {
	Performance() model.Performance
}

// CapProvider — capacidades de OpenZFS del host (feature-gating).
type CapProvider interface {
	Capabilities() model.Capabilities
}

// Providers agrupa las cachés que consume httpapi.
type Providers struct {
	Pools     PoolProvider
	Disks     DiskProvider
	SysTimers SysTimerProvider
	Perf      PerfProvider
	Caps      CapProvider
}

// Build construye colectores reales o el mock (MOCK=1 / DEMO=1).
func Build(cfg *config.Config, d *sql.DB, h *hub.Hub, al *alerts.Alerter) (*Providers, []Collector) {
	mant := NewMantenimiento(d, cfg.RetentionDays)
	if cfg.Mock {
		m := NewMock(h, al)
		return &Providers{Pools: m, Disks: m, SysTimers: m, Perf: m, Caps: m}, []Collector{m, mant}
	}
	zc := NewZpoolCollector(d, h, al, cfg.ZpoolInterval)
	sc := NewSensorsCollector(h)
	smc := NewSmartCollector(d, h, al, sc)
	ssc := NewSchedSysCollector()
	pc := NewPerfCollector()
	cc := NewCapsCollector()
	// Eventos ZFS en tiempo real ('zpool events -f'); si no está disponible se
	// desactiva solo tras el log y el polling queda como red de seguridad.
	ec := NewEventsCollector(al)
	return &Providers{Pools: zc, Disks: smc, SysTimers: ssc, Perf: pc, Caps: cc},
		[]Collector{zc, sc, smc, ssc, pc, cc, ec, mant}
}

// baseName normaliza un dev de vdev ('/dev/sdb1', 'sdb1', 'ata-XXX-part1') a
// nombre base comparable con lsblk ('sdb', 'nvme0n1'). Mejor esfuerzo.
func baseName(dev string) string {
	// quita prefijo /dev/
	for len(dev) > 0 && dev[0] == '/' {
		dev = dev[1:]
		if len(dev) >= 4 && dev[:3] == "dev" {
			dev = dev[3:]
		}
	}
	return dev
}
