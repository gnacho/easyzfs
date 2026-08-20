// mock.go — datos realistas del dominio para desarrollo/demo (MOCK=1 o DEMO=1).
// Dominio: pools tank (raidz1, 3×4 TB) y ssd (mirror, 2×1 TB NVMe), 7 discos
// (incl. una eMMC mmcblk0 sin SMART, como en el hardware real reportado),
// scrub de ssd en curso que avanza en cada tick y emite scrub.progress.
package collectors

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"easyzfs/internal/alerts"
	"easyzfs/internal/hub"
	"easyzfs/internal/model"
)

// Mock — implementa PoolProvider y DiskProvider con datos vivos de mentira.
type Mock struct {
	h  *hub.Hub
	al *alerts.Alerter

	mu         sync.RWMutex
	pools      []model.Pool
	datasets   []model.Dataset
	snaps      []model.Snapshot
	disks      []model.Disk
	scrubStart time.Time
	lastPct    int
}

// NewMock construye el escenario realista.
func NewMock(h *hub.Hub, al *alerts.Alerter) *Mock {
	m := &Mock{h: h, al: al, scrubStart: time.Now()}
	m.build()
	return m
}

// Name implementa Collector.
func (m *Mock) Name() string { return "mock" }

// Run — avanza el scrub del pool ssd (~1 % cada 5 s) y republica temperaturas.
func (m *Mock) Run(ctx context.Context) {
	// Evaluación inicial: sdd tiene SMART con avisos → alerta de ejemplo con
	// target navegable ("disks:sdd"), como haría el colector real.
	m.al.EvaluateDisks(ctx, m.Disks())
	m.al.EvaluatePools(ctx, m.Pools())
	// Evento ZFS de ejemplo (colector events): a los pocos segundos llega una
	// alerta "zed.*" como si zpool events hubiera emitido un ereport.
	go func() {
		select {
		case <-ctx.Done():
		case <-time.After(12 * time.Second):
			m.al.RaiseKind(ctx, "crit", "zed.ereport.fs.zfs.checksum", "disks:sdd",
				"Errores de checksum en sdd (evento ZFS, pool tank)",
				"zfs_checksum_error", map[string]any{"pool": "tank", "vdev": "sdd"})
		}
	}()
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			m.tick(now)
		}
	}
}

// tick — evolución del escenario: scrub avanza; al terminar queda 'done'.
func (m *Mock) tick(now time.Time) {
	m.mu.Lock()
	for i := range m.pools {
		p := &m.pools[i]
		if p.Name != "ssd" && p.Scrub.Kind != "expand" {
			continue
		}
		if p.Scrub.Kind == "expand" && p.Scrub.State == "running" {
			// Expansión simulada: ~4 % por tick (~2 min en total).
			p.Scrub.Pct += 4
			if p.Scrub.Pct >= 100 {
				p.Scrub = model.ScrubInfo{State: "done", Kind: "expand", Pct: 100, Ts: now.UTC(), Errors: 0}
			} else {
				p.Scrub.EtaSec = int64((100 - p.Scrub.Pct) * 5 / 4)
				p.Scrub.BytesDone = uint64(p.Scrub.Pct / 100 * float64(p.UsedBytes))
				p.Scrub.BytesTotal = p.UsedBytes
			}
			m.h.Publish("scrub.progress", map[string]any{
				"pool": p.Name, "pct": p.Scrub.Pct, "eta_sec": p.Scrub.EtaSec, "kind": "expand",
			})
			continue
		}
		if p.Name == "ssd" && p.Scrub.State == "running" {
			p.Scrub.Pct += 1.5
			if p.Scrub.Pct >= 100 {
				p.Scrub = model.ScrubInfo{State: "done", Pct: 100, Ts: now.UTC(), Errors: 0}
			} else {
				p.Scrub.EtaSec = int64((100 - p.Scrub.Pct) * 5 / 1.5)
			}
		}
	}
	scrub := model.ScrubInfo{}
	for _, p := range m.pools {
		if p.Name == "ssd" {
			scrub = p.Scrub
		}
	}
	m.mu.Unlock()

	pct := int(scrub.Pct)
	if pct != m.lastPct {
		m.h.Publish("scrub.progress", map[string]any{
			"pool": "ssd", "pct": scrub.Pct, "eta_sec": scrub.EtaSec,
		})
		m.lastPct = pct
		if scrub.State == "done" {
			m.h.Publish("overview", map[string]any{"reason": "scrub.done"})
		}
	}
}

// Pools implementa PoolProvider.
func (m *Mock) Pools() []model.Pool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]model.Pool, len(m.pools))
	copy(out, m.pools)
	return out
}

// Datasets implementa PoolProvider.
func (m *Mock) Datasets() []model.Dataset {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]model.Dataset, len(m.datasets))
	copy(out, m.datasets)
	return out
}

// SnapshotGroups implementa PoolProvider.
func (m *Mock) SnapshotGroups() []model.SnapGroup {
	m.mu.RLock()
	defer m.mu.RUnlock()
	byDS := map[string][]model.Snapshot{}
	order := []string{}
	for _, s := range m.snaps {
		ds, _, _ := strings.Cut(s.Full, "@")
		if _, ok := byDS[ds]; !ok {
			order = append(order, ds)
		}
		byDS[ds] = append(byDS[ds], s)
	}
	sort.Strings(order)
	out := make([]model.SnapGroup, 0, len(order))
	for _, ds := range order {
		snaps := byDS[ds]
		sort.Slice(snaps, func(i, j int) bool { return snaps[i].Ts.After(snaps[j].Ts) })
		out = append(out, model.SnapGroup{Dataset: ds, Snaps: snaps})
	}
	return out
}

// Disks implementa DiskProvider.
func (m *Mock) Disks() []model.Disk {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]model.Disk, len(m.disks))
	copy(out, m.disks)
	return out
}

// History implementa PoolProvider: actividad ZFS reciente de mentira.
func (m *Mock) History(name string) []model.HistoryEntry {
	now := time.Now().UTC()
	switch name {
	case "tank":
		return []model.HistoryEntry{
			{Ts: now.Add(-2 * time.Hour), Command: "zfs snapshot -r tank@easyzfs-auto-20260801-0600", DurationSec: 1.42},
			{Ts: now.Add(-26 * time.Hour), Command: "zpool scrub tank", DurationSec: 14250.8},
			{Ts: now.Add(-3 * 24 * time.Hour), Command: "zfs set compression=zstd tank/backups", DurationSec: 0.04},
			{Ts: now.Add(-9 * 24 * time.Hour), Command: "zpool checkpoint tank", DurationSec: 0.61},
			{Ts: now.Add(-30 * 24 * time.Hour), Command: "zpool create tank raidz sdb sdc sdd", DurationSec: 2.10},
		}
	case "ssd":
		return []model.HistoryEntry{
			{Ts: now.Add(-40 * time.Minute), Command: "zpool replace ssd 13501483247074580929 /dev/disk/by-id/nvme-Samsung_SSD_980_1TB_S649NL0R222222", DurationSec: 0.88},
			{Ts: now.Add(-2 * time.Hour), Command: "zfs snapshot -r ssd@easyzfs-auto-20260801-0600", DurationSec: 0.91},
			{Ts: now.Add(-20 * 24 * time.Hour), Command: "zpool set autotrim=on ssd", DurationSec: 0.03},
			{Ts: now.Add(-60 * 24 * time.Hour), Command: "zpool create ssd mirror nvme0n1 nvme1n1", DurationSec: 1.74},
		}
	}
	return []model.HistoryEntry{}
}

// Performance implementa PerfProvider: ARC y throughput inventados.
func (m *Mock) Performance() model.Performance {
	return model.Performance{
		Arc: &model.ArcStats{SizeBytes: 3900 * (1 << 20), HitPct: 92.4},
		Pools: []model.PoolPerf{
			{Name: "tank", ReadBps: 41.2 * (1 << 20), WriteBps: 12.8 * (1 << 20)},
			{Name: "ssd", ReadBps: 220 * (1 << 20), WriteBps: 96.5 * (1 << 20)},
		},
	}
}

// SetAutotrim — mutación simulada (la llama el handler en MOCK=1 tras validar).
func (m *Mock) SetAutotrim(pool string, enabled bool) {
	m.mu.Lock()
	for i := range m.pools {
		if m.pools[i].Name == pool {
			m.pools[i].Autotrim = enabled
		}
	}
	m.mu.Unlock()
	m.h.Publish("overview", map[string]any{"reason": "pool.autotrim"})
}

// SetCheckpoint — mutación simulada de checkpoint (MOCK=1).
func (m *Mock) SetCheckpoint(pool string, active bool) {
	m.mu.Lock()
	for i := range m.pools {
		if m.pools[i].Name == pool {
			m.pools[i].Checkpoint = active
		}
	}
	m.mu.Unlock()
	m.h.Publish("overview", map[string]any{"reason": "pool.checkpoint"})
}

// SetKeyStatus — unlock/lock simulados (MOCK=1): la clave nunca se pide ni
// guarda; solo cambia el estado visible. Al desbloquear, el dataset "se monta".
func (m *Mock) SetKeyStatus(name, status string) {
	m.mu.Lock()
	for i := range m.datasets {
		if m.datasets[i].Name == name {
			m.datasets[i].KeyStatus = status
			if status == "available" && m.datasets[i].Type == "fs" && m.datasets[i].Mountpoint == "-" {
				m.datasets[i].Mountpoint = "/" + name
			}
			if status == "unavailable" {
				m.datasets[i].Mountpoint = "-"
			}
		}
	}
	m.mu.Unlock()
	m.h.Publish("overview", map[string]any{"reason": "dataset.key"})
}

// AddDataset — alta simulada de dataset (MOCK=1).
func (m *Mock) AddDataset(name, typ, compression string, encrypted bool) {
	m.mu.Lock()
	mount := "-" // volume
	if typ == "fs" {
		mount = "/" + name
	}
	enc, ks := "off", "-"
	if encrypted {
		enc, ks = "aes-256-gcm", "available"
	}
	m.datasets = append(m.datasets, model.Dataset{
		Name: name, Type: typ, Compression: compression,
		UsedBytes: 1 << 20, AvailBytes: 5 * (1 << 40), Mountpoint: mount,
		Encryption: enc, KeyStatus: ks,
	})
	m.mu.Unlock()
	m.h.Publish("overview", map[string]any{"reason": "dataset.create"})
}

// SetDatasetProp — set de propiedad simulado (MOCK=1). Solo afecta a la
// propiedad si ya es "local"; el resto se ignora (no hay tabla de props en
// el mock; el front usa el GET para pintar, que devuelve valores ficticios).
func (m *Mock) SetDatasetProp(name, property, value string) {
	m.mu.Lock()
	for i := range m.datasets {
		if m.datasets[i].Name == name {
			if property == "compression" {
				m.datasets[i].Compression = value
			}
		}
	}
	m.mu.Unlock()
	m.h.Publish("overview", map[string]any{"reason": "dataset.prop"})
}

// InheritDatasetProp — inherit simulado (MOCK=1): no-op (sin tabla de props).
func (m *Mock) InheritDatasetProp(name, property string) {
	m.h.Publish("overview", map[string]any{"reason": "dataset.prop"})
}

// Expand — RAID-Z expansion simulada (MOCK=1): incorpora el disco al pool y
// arranca un scan 'expand' que avanza en cada tick (~2%/tick, ~2,5 min).
func (m *Mock) Expand(pool, vdev, disk string) {
	m.mu.Lock()
	for i := range m.pools {
		if m.pools[i].Name != pool {
			continue
		}
		role := "raidz1"
		if strings.HasPrefix(vdev, "raidz2") {
			role = "raidz2"
		} else if strings.HasPrefix(vdev, "raidz3") {
			role = "raidz3"
		}
		m.pools[i].Vdevs = append(m.pools[i].Vdevs,
			model.Vdev{Dev: disk, Role: role, Status: "ONLINE", TempC: 32})
		m.pools[i].Scrub = model.ScrubInfo{
			State: "running", Kind: "expand", Pct: 0, EtaSec: 150,
			Ts: time.Now().UTC(),
		}
	}
	for i := range m.disks {
		if m.disks[i].Dev == disk {
			m.disks[i].Pool = pool
		}
	}
	m.mu.Unlock()
	m.h.Publish("overview", map[string]any{"reason": "pool.expand"})
}

// Capabilities implementa CapProvider: en demo se anuncia un OpenZFS moderno.
func (m *Mock) Capabilities() model.Capabilities {
	return model.Capabilities{
		Rewrite: true, RaidzExpansion: true, ScrubAll: true,
		ScrubRange: true, ZarcNames: true, JSONOutput: true, Version: "2.4.1",
	}
}

// SysTimers implementa SysTimerProvider: ejemplos de timers systemd y cron
// que un NAS típico ya tendría (la vista Tareas los muestra junto a los jobs).
func (m *Mock) SysTimers() []model.SysTimer {
	now := time.Now().UTC()
	return []model.SysTimer{
		{
			Source: "systemd", Name: "zfs-scrub@tank-monthly.timer", Schedule: "monthly",
			NextRun: now.Add(11 * 24 * time.Hour).Format(time.RFC3339),
			LastRun: now.Add(-19 * 24 * time.Hour).Format(time.RFC3339),
			Command: "zfs-scrub@tank-monthly.service", Origin: "systemctl list-timers",
			Editable: true,
		},
		{
			Source: "systemd", Name: "logrotate.timer", Schedule: "daily",
			NextRun: now.Add(6 * time.Hour).Format(time.RFC3339),
			LastRun: now.Add(-18 * time.Hour).Format(time.RFC3339),
			Command: "logrotate.service", Origin: "systemctl list-timers",
		},
		{
			Source: "systemd", Name: "man-db.timer", Schedule: "daily",
			NextRun: now.Add(9 * time.Hour).Format(time.RFC3339),
			LastRun: now.Add(-15 * time.Hour).Format(time.RFC3339),
			Command: "man-db.service", Origin: "systemctl list-timers",
		},
		{
			Source: "cron", Name: "backup-tank.sh", Schedule: "30 3 * * *",
			Command: "/usr/local/bin/backup-tank.sh --pool tank --dest /mnt/usb",
			Origin:  "/etc/cron.d/backup", Line: 7, Editable: true,
		},
	}
}

// SystemdAvailable implementa SysTimerProvider: en demo se asume systemd
// presente (la UI muestra la opción de cambio a systemd timer). Para pruebas,
// EASYZFS_MOCK_SYSTEMD=0 simula un sistema sin systemd.
func (m *Mock) SystemdAvailable() bool {
	return os.Getenv("EASYZFS_MOCK_SYSTEMD") != "0"
}

// build — el escenario estático inicial.
func (m *Mock) build() {
	const (
		gib = 1 << 30
		tib = 1 << 40
	)
	m.pools = []model.Pool{
		{
			Name:       "tank",
			Status:     "DEGRADED",
			Topo:       "raidz2",
			UsedBytes:  6*uint64(tib) + 420*uint64(gib),
			TotalBytes: 16 * uint64(tib), // 4×4 TB raidz2 ≈ 8 TB útiles; total bruto 16 TB
			FragPct:    12,
			CompRatio:  1.18,
			Scrub:      model.ScrubInfo{State: "done", Pct: 100, Ts: time.Now().Add(-9 * 24 * time.Hour).UTC(), Errors: 0},
			Autotrim:   false, // HDD: no aplica
			Checkpoint: true,  // checkpoint activo de ejemplo (badge en la UI)
			// Vdev raidz2-0: objetivo del botón Expandir (RAID-Z expansion).
			RaidzVdevs: []string{"raidz2-0"},
			Vdevs: []model.Vdev{
				{Dev: "sdb", Role: "raidz2", Status: "ONLINE", TempC: 34},
				{Dev: "sdc", Role: "raidz2", Status: "ONLINE", TempC: 35},
				{Dev: "sdd", Role: "raidz2", Status: "ONLINE", TempC: 36},
				// Caso real (pool heredado): vdev nombrado por PARTUUID y
				// FAULTED; sin Path porque el disco ya no responde.
				{Dev: "11111111-2222-3333-4444-555555555555", Role: "raidz2", Status: "FAULTED"},
			},
		},
		{
			Name:       "ssd",
			Status:     "DEGRADED",
			Topo:       "mirror",
			UsedBytes:  420 * uint64(gib),
			TotalBytes: 2 * uint64(tib),
			FragPct:    4,
			CompRatio:  1.09,
			Scrub:      model.ScrubInfo{State: "running", Kind: "resilver", Pct: 23, EtaSec: 1500, Ts: m.scrubStart.UTC(), Errors: 0},
			Autotrim:   true, // SSD NVMe: TRIM continuo activado
			Vdevs: []model.Vdev{
				{Dev: "nvme0n1", Role: "mirror", Status: "ONLINE", TempC: 41},
				// Pareja replacing- real: viejo saliente (CANT_OPEN) + nuevo ya ONLINE
				{Dev: "13501483247074580929", Role: "mirror", Status: "CANT_OPEN", Replacing: true},
				{Dev: "nvme1n1", Role: "mirror", Status: "ONLINE", TempC: 42, Replacing: true},
			},
		},
	}
	m.datasets = []model.Dataset{
		{Name: "tank", Type: "fs", Compression: "lz4", UsedBytes: 6*uint64(tib) + 420*uint64(gib), AvailBytes: 5 * uint64(tib), QuotaBytes: 0, Mountpoint: "/tank", Encryption: "off", KeyStatus: "-"},
		{Name: "tank/docs", Type: "fs", Compression: "lz4", UsedBytes: 220 * uint64(gib), AvailBytes: 5 * uint64(tib), QuotaBytes: 512 * uint64(gib), Mountpoint: "/tank/docs", Encryption: "off", KeyStatus: "-"},
		{Name: "tank/fotos", Type: "fs", Compression: "lz4", UsedBytes: 3*uint64(tib) + 100*uint64(gib), AvailBytes: 5 * uint64(tib), QuotaBytes: 0, Mountpoint: "/tank/fotos", Encryption: "off", KeyStatus: "-"},
		{Name: "tank/backups", Type: "fs", Compression: "zstd", UsedBytes: 3*uint64(tib) + 40*uint64(gib), AvailBytes: 5 * uint64(tib), QuotaBytes: 4 * uint64(tib), Mountpoint: "/tank/backups", Encryption: "off", KeyStatus: "-"},
		// Cifrado nativo desbloqueado (clave cargada, montado).
		{Name: "tank/secretos", Type: "fs", Compression: "zstd", UsedBytes: 42 * uint64(gib), AvailBytes: 5 * uint64(tib), QuotaBytes: 0, Mountpoint: "/tank/secretos", Encryption: "aes-256-gcm", KeyStatus: "available"},
		// Cifrado nativo bloqueado (sin clave: no montado; se desbloquea con unlock).
		{Name: "tank/boveda", Type: "fs", Compression: "zstd", UsedBytes: 512 * uint64(gib), AvailBytes: 5 * uint64(tib), QuotaBytes: 0, Mountpoint: "-", Encryption: "aes-256-gcm", KeyStatus: "unavailable"},
		{Name: "ssd", Type: "fs", Compression: "lz4", UsedBytes: 420 * uint64(gib), AvailBytes: 1500 * uint64(gib), QuotaBytes: 0, Mountpoint: "/ssd", Encryption: "off", KeyStatus: "-"},
		{Name: "ssd/vm", Type: "volume", Compression: "zstd", UsedBytes: 320 * uint64(gib), AvailBytes: 1500 * uint64(gib), QuotaBytes: 400 * uint64(gib), Mountpoint: "-", Encryption: "off", KeyStatus: "-"},
	}
	now := time.Now().UTC()
	mkSnap := func(ds, name string, age time.Duration, used uint64, kind string) model.Snapshot {
		return model.Snapshot{Name: name, Full: ds + "@" + name, Ts: now.Add(-age), UsedBytes: used, Kind: kind}
	}
	m.snaps = []model.Snapshot{
		mkSnap("tank/docs", "easyzfs-auto-20250101-0600", 48*time.Hour, 1*uint64(gib), "auto"),
		mkSnap("tank/docs", "easyzfs-auto-20250102-0600", 24*time.Hour, 800*(1<<20), "auto"),
		mkSnap("tank/docs", "antes-de-migracion", 30*24*time.Hour, 2*uint64(gib), "manual"),
		mkSnap("tank/fotos", "easyzfs-auto-20250102-0600", 24*time.Hour, 3*uint64(gib), "auto"),
		mkSnap("tank/backups", "easyzfs-auto-20250102-0600", 24*time.Hour, 12*uint64(gib), "auto"),
		mkSnap("ssd/vm", "pre-upgrade", 7*24*time.Hour, 20*uint64(gib), "manual"),
	}
	m.disks = []model.Disk{
		{Dev: "sda", ByID: "ata-CT500MX500SSD1_2034E5A1B2C3", Model: "CT500MX500SSD1", Serial: "2034E5A1B2C3", SizeBytes: 500 * uint64(gib), TempC: f64ptr(33), Smart: "ok", SmartDetail: "PASSED", Pool: "", Hours: 18200, SmartFull: mockSmartDetailATA(0)},
		// Disco libre del mismo tamaño que los miembros de tank: candidato a
		// RAID-Z expansion (lote D) o a sustituir el vdev FAULTED.
		{Dev: "sde", ByID: "ata-WDC_WD40EFRX-68N_WD-WCC7K1AAAA04", Model: "WDC WD40EFRX-68N", Serial: "WD-WCC7K1AAAA04", SizeBytes: 4 * uint64(tib), TempC: f64ptr(31), Smart: "ok", SmartDetail: "PASSED", Pool: "", Hours: 1200, SmartFull: mockSmartDetailATA(0)},
		{Dev: "sdb", ByID: "ata-WDC_WD40EFRX-68N_WD-WCC7K1AAAA01", Model: "WDC WD40EFRX-68N", Serial: "WD-WCC7K1AAAA01", SizeBytes: 4 * uint64(tib), TempC: f64ptr(34), Smart: "ok", SmartDetail: "PASSED", Pool: "tank", Hours: 41230, SmartFull: mockSmartDetailATA(0)},
		{Dev: "sdc", ByID: "ata-WDC_WD40EFRX-68N_WD-WCC7K1AAAA02", Model: "WDC WD40EFRX-68N", Serial: "WD-WCC7K1AAAA02", SizeBytes: 4 * uint64(tib), TempC: f64ptr(35), Smart: "ok", SmartDetail: "PASSED", Pool: "tank", Hours: 41231, SmartFull: mockSmartDetailATA(0)},
		{Dev: "sdd", ByID: "ata-WDC_WD40EFRX-68N_WD-WCC7K1AAAA03", Model: "WDC WD40EFRX-68N", Serial: "WD-WCC7K1AAAA03", SizeBytes: 4 * uint64(tib), TempC: f64ptr(36), Smart: "warn", SmartDetail: "PASSED (realloc=2 pending=0)", ReallocSectors: 2, Pool: "tank", Hours: 42010, SmartFull: mockSmartDetailATA(2)},
		{Dev: "nvme0n1", ByID: "nvme-Samsung_SSD_980_1TB_S649NL0R111111", Model: "Samsung SSD 980 1TB", Serial: "S649NL0R111111", SizeBytes: 1 * uint64(tib), TempC: f64ptr(41), Smart: "ok", SmartDetail: "PASSED", Pool: "ssd", Hours: 9800, SmartFull: mockSmartDetailNVMe()},
		{Dev: "nvme1n1", ByID: "nvme-Samsung_SSD_980_1TB_S649NL0R222222", Model: "Samsung SSD 980 1TB", Serial: "S649NL0R222222", SizeBytes: 1 * uint64(tib), TempC: f64ptr(42), Smart: "ok", SmartDetail: "PASSED", Pool: "ssd", Hours: 9812, SmartFull: mockSmartDetailNVMe()},
		// Caso real: eMMC de placa (smartctl no la soporta → "unknown", sin
		// lectura de temperatura → TempC nil, JSON null). En el sistema también
		// había zd0 y mmcblk0boot0/boot1, pero el filtro de discos físicos los
		// excluye (ver smart.go).
		{Dev: "mmcblk0", Model: "S008G1 eMMC", Serial: "0x2c8f1a3b", SizeBytes: 8 * uint64(gib), TempC: nil, Smart: "unknown", SmartDetail: "no disponible", Pool: "", Hours: 0},
	}
}

// mockSmartDetailATA — detalle SMART ficticio para discos ATA del mock
// (U1). realloc > 0 marca un atributo con when_failed="Past" (disco sdd).
func mockSmartDetailATA(realloc int64) *model.DiskSmartDetail {
	when := "-"
	if realloc > 0 {
		when = "Past"
	}
	return &model.DiskSmartDetail{
		Protocol: "ata",
		Attributes: []model.SmartAttr{
			{ID: 1, Name: "Raw_Read_Error_Rate", Value: 100, Worst: 16, Thresh: 6, Raw: "0", WhenFailed: "-"},
			{ID: 5, Name: "Reallocated_Sector_Ct", Value: 100, Worst: 100, Thresh: 36, Raw: fmt.Sprintf("%d", realloc), WhenFailed: when},
			{ID: 9, Name: "Power_On_Hours", Value: 95, Worst: 95, Thresh: 0, Raw: "41230", WhenFailed: "-"},
			{ID: 197, Name: "Current_Pending_Sector", Value: 99, Worst: 99, Thresh: 0, Raw: "0", WhenFailed: "-"},
			{ID: 198, Name: "Offline_Uncorrectable", Value: 99, Worst: 99, Thresh: 0, Raw: "0", WhenFailed: "-"},
			{ID: 199, Name: "UDMA_CRC_Error_Count", Value: 200, Worst: 200, Thresh: 0, Raw: "0", WhenFailed: "-"},
		},
		Selftests: []model.SmartSelftest{
			{Type: "Short self-test", Status: "Completed without error", LifetimeHours: 41230, Percent: 100},
			{Type: "Extended self-test", Status: "Completed without error", LifetimeHours: 41000, Percent: 100},
		},
		ErrorLog: model.SmartErrorLog{Count: 0},
	}
}

// mockSmartDetailNVMe — detalle SMART ficticio para discos NVMe del mock.
func mockSmartDetailNVMe() *model.DiskSmartDetail {
	return &model.DiskSmartDetail{
		Protocol: "nvme",
		Attributes: []model.SmartAttr{
			{ID: 1, Name: "temperature", Value: 41, Worst: 0, Thresh: 0, Raw: "41", WhenFailed: "-"},
			{ID: 2, Name: "available_spare", Value: 100, Worst: 100, Thresh: 10, Raw: "100%", WhenFailed: "-"},
			{ID: 3, Name: "percentage_used", Value: 12, Worst: 12, Thresh: 0, Raw: "12%", WhenFailed: "-"},
		},
		Selftests: []model.SmartSelftest{
			{Type: "Short self-test", Status: "Completed without error", LifetimeHours: 9800, Percent: 100},
		},
		ErrorLog: model.SmartErrorLog{Count: 0},
	}
}

// f64ptr — helper para literales *float64 del mock (temp_c: number|null).
func f64ptr(v float64) *float64 { return &v }
