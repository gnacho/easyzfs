// smart.go — colector SMART: lsblk -J para inventario + smartctl -j por disco.
// smartctl -a es MUY lento: intervalo 10 min y timeout 60 s por disco.
// Las temperaturas se complementan con el colector de sensores (30 s).
package collectors

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	"easyzfs/internal/alerts"
	"easyzfs/internal/executil"
	"easyzfs/internal/hub"
	"easyzfs/internal/model"
)

// Solo dispositivos físicos: la whitelist admite discos SATA/SAS/IDE/virtio/
// Xen/NVMe/eMMC y la blacklist excluye explícitamente seudo-dispositivos
// (zvols ZFS, loop, ram, device-mapper, ópticos, floppy) y las particiones
// hardware de eMMC (boot0/boot1/rpmb), que no son discos usables.
var (
	physDiskRe     = regexp.MustCompile(`^(sd[a-z]+|hd[a-z]+|vd[a-z]+|xvd[a-z]+|nvme\d+n\d+|mmcblk\d+)$`)
	excludedDiskRe = regexp.MustCompile(`^(zd\d+|loop\d+|ram\d+|dm-\d+|sr\d+|fd\d+|mmcblk\d+boot\d+|mmcblk\d+rpmb)$`)
)

// isPhysicalDisk decide si un nombre de dispositivo de lsblk es un disco
// físico gestionable (true) o ruido del sistema (false).
func isPhysicalDisk(name string) bool {
	if excludedDiskRe.MatchString(name) {
		return false
	}
	return physDiskRe.MatchString(name)
}

const (
	smartInterval   = 10 * time.Minute
	smartMaxBackoff = 30 * time.Minute
)

// SmartCollector — caché de discos con estado SMART.
type SmartCollector struct {
	db      *sql.DB
	h       *hub.Hub
	al      *alerts.Alerter
	sensors *SensorsCollector

	mu         sync.RWMutex
	disks      []model.Disk
	fails      int
	stale      bool
	lastSeries map[string]time.Time
	// prevSmart recuerda el último estado publicado por disco para emitir
	// disk.smart solo en cambios (las pestañas abiertas se actualizan por
	// SSE; sin esto una pestaña vieja mostraba SMART obsoleto hasta recargar).
	prevSmart map[string]string
	// prevCRC recuerda el UDMA CRC de la pasada anterior POR SERIAL (el
	// sdX puede cambiar al mover bahías). El delta es lo accionable: el
	// acumulado de por vida no se resetea y persigue al disco equivocado
	// tras un cambio de bahías (caso real bigtank 4-Ago-2026).
	prevCRC map[string]int64
}

// NewSmartCollector crea el colector SMART.
func NewSmartCollector(d *sql.DB, h *hub.Hub, al *alerts.Alerter, sensors *SensorsCollector) *SmartCollector {
	return &SmartCollector{
		db:         d,
		h:          h,
		al:         al,
		sensors:    sensors,
		lastSeries: map[string]time.Time{},
		prevSmart:  map[string]string{},
		prevCRC:    map[string]int64{},
	}
}

// Name implementa Collector.
func (c *SmartCollector) Name() string { return "smart" }

// Run — bucle con ticker y backoff (patrón del skill).
func (c *SmartCollector) Run(ctx context.Context) {
	interval := smartInterval
	t := time.NewTicker(interval)
	defer t.Stop()
	if err := c.collectOnce(ctx); err != nil {
		log.Printf("smart: %v", err)
		c.fails++
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := c.collectOnce(ctx); err != nil {
				log.Printf("smart: %v", err)
				c.fails++
			} else {
				c.fails = 0
			}
			if c.fails >= 3 {
				if !c.stale {
					log.Printf("smart: fuente stale tras %d fallos; backoff", c.fails)
				}
				c.stale = true
				interval = min(2*interval, smartMaxBackoff)
				t.Reset(interval)
			} else if interval != smartInterval {
				c.stale = false
				interval = smartInterval
				t.Reset(interval)
			}
		}
	}
}

// Disks — caché de discos (copia defensiva).
func (c *SmartCollector) Disks() []model.Disk {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]model.Disk, len(c.disks))
	copy(out, c.disks)
	return out
}

// lsblkJSON — salida de 'lsblk -J -b -o NAME,MODEL,SERIAL,SIZE,TYPE'.
type lsblkJSON struct {
	BlockDevices []struct {
		Name   string `json:"name"`
		Model  string `json:"model"`
		Serial string `json:"serial"`
		Size   uint64 `json:"size"`
		Type   string `json:"type"`
	} `json:"blockdevices"`
}

// smartJSON — subconjunto tolerante de 'smartctl -j -a'.
type smartJSON struct {
	ModelName    string `json:"model_name"`
	ModelFamily  string `json:"model_family"`
	SerialNumber string `json:"serial_number"`
	Temperature  struct {
		Current float64 `json:"current"`
	} `json:"temperature"`
	SmartStatus *struct {
		Passed bool `json:"passed"`
	} `json:"smart_status"`
	PowerOnTime struct {
		Hours uint64 `json:"hours"`
	} `json:"power_on_time"`
	UserCapacity struct {
		Bytes uint64 `json:"bytes"`
	} `json:"user_capacity"`
	NVMeSmartHealthLog *struct {
		Temperature       float64 `json:"temperature"`
		AvailableSparePct float64 `json:"available_spare"`
		CriticalWarning   int     `json:"critical_warning"`
	} `json:"nvme_smart_health_information_log"`
	AtaAttrs *struct {
		Table []struct {
			Name   string `json:"name"`
			Raw    struct {
				Value int64  `json:"value"`
				Str   string `json:"string"`
			} `json:"raw"`
		ID         int      `json:"id"`
		Value      int64    `json:"value"`
		Worst      int64    `json:"worst"`
		Thresh     int64    `json:"thresh"`
		WhenFailed string   `json:"when_failed"`
	} `json:"table"`
	} `json:"ata_smart_attributes"`
	// Selftest log (ATA) y error log (ATA) — U1: drill-down SMART.
	AtaSelftestLog *struct {
		Standard *struct {
			Table []struct {
				Type   struct{ String string `json:"string"` } `json:"type"`
				Status struct{ String string `json:"string"` } `json:"status"`
				LifetimeHours int64 `json:"lifetime_hours"`
				Percent int    `json:"percent"`
			} `json:"table"`
		} `json:"standard"`
	} `json:"ata_smart_selftest_log"`
	AtaErrorLog *struct {
		Summary *struct {
			Count int `json:"count"`
			Table []struct {
				ErrorType struct{ String string `json:"string"` } `json:"error_type"`
				Lba      struct{ Str string `json:"string"` } `json:"lba"`
			} `json:"table"`
		} `json:"summary"`
	} `json:"ata_error_log"`
	// NVMe self-test (U1). Nota de incertidumbre: nombre de sección a
	// confirmar con un smartctl -j -a real sobre NVMe; ausente → lista vacía.
	NVMeSelfTest *struct {
		SelfTest []struct {
			Type   struct{ String string `json:"string"` } `json:"type"`
			Status struct{ String string `json:"string"` } `json:"status"`
			PowerOnHours int64 `json:"power_on_hours"`
			CompletionPercent int `json:"completion_percent"`
		} `json:"self_test"`
	} `json:"nvme_self_test_log_1"`
}

// collectOnce — inventario lsblk y SMART por disco (un fallo de un disco no
// falla la pasada; solo falla si el inventario no se puede leer).
func (c *SmartCollector) collectOnce(ctx context.Context) error {
	out, err := executil.Run(ctx, 10*time.Second, "lsblk", "-J", "-b",
		"-o", "NAME,MODEL,SERIAL,SIZE,TYPE")
	if err != nil {
		return err
	}
	var inv lsblkJSON
	if err := json.Unmarshal(out, &inv); err != nil {
		return fmt.Errorf("lsblk JSON: %w", err)
	}
	disks := []model.Disk{}
	crcSeen := map[string]bool{}
	for _, bd := range inv.BlockDevices {
		if bd.Type != "disk" || !isPhysicalDisk(bd.Name) {
			continue
		}
		d := model.Disk{
			Dev:       bd.Name,
			Model:     strings.TrimSpace(bd.Model),
			Serial:    bd.Serial,
			SizeBytes: bd.Size,
			// Por defecto "unknown": eMMC / USB sin SAT no hablan smartctl
			// y no deben aparecer como error ni como "ok".
			Smart:       "unknown",
			SmartDetail: "no disponible",
		}
		c.fillSmart(ctx, &d)
		// Delta CRC por serial (sobrevive a cambios de bahía/sdX).
		key := d.Serial
		if key == "" {
			key = d.Dev
		}
		prev, ok := c.prevCRC[key]
		applyCrcDelta(&d, prev, ok)
		c.prevCRC[key] = d.CrcErrors
		crcSeen[key] = true
		if t, ok := c.sensors.Temp(bd.Name); ok && t > 0 {
			d.TempC = &t // sensores (30 s) tienen preferencia sobre smartctl (10 min)
		}
		disks = append(disks, d)
	}
	// Podar seriales de discos retirados.
	for key := range c.prevCRC {
		if !crcSeen[key] {
			delete(c.prevCRC, key)
		}
	}
	c.mu.Lock()
	c.disks = disks
	c.mu.Unlock()
	c.al.EvaluateDisks(ctx, disks)
	c.publishSmartChanges(disks)
	c.persistSeries(ctx, disks)
	return nil
}

// publishSmartChanges emite disk.smart por SSE cuando cambia el estado o el
// detalle SMART de un disco (p. ej. un disco pasa a warn entre dos pasadas
// de 10 min). La primera pasada solo siembra el mapa (sin tormenta de
// eventos al arrancar; las pestañas nuevas ya traen el dato fresco del GET).
func (c *SmartCollector) publishSmartChanges(disks []model.Disk) {
	c.mu.Lock()
	defer c.mu.Unlock()
	seen := map[string]bool{}
	for _, d := range disks {
		seen[d.Dev] = true
		key := d.Smart + "|" + d.SmartDetail
		prev, ok := c.prevSmart[d.Dev]
		c.prevSmart[d.Dev] = key
		if !ok || prev == key {
			continue
		}
		c.h.Publish("disk.smart", map[string]any{
			"dev":             d.Dev,
			"smart":           d.Smart,
			"smart_detail":    d.SmartDetail,
			"realloc_sectors": d.ReallocSectors,
			"pending_sectors": d.PendingSectors,
			"offline_uncorr":  d.OfflineUncorr,
			"crc_errors":      d.CrcErrors,
			"crc_recent":      d.CrcRecent,
			"nvme_warn":       d.NvmeWarn,
		})
	}
	// Podar discos que ya no existen (extraídos) para no filtrar memoria.
	for dev := range c.prevSmart {
		if !seen[dev] {
			delete(c.prevSmart, dev)
		}
	}
}

// fillSmart rellena estado SMART de un disco; tolerante ante fallos.
func (c *SmartCollector) fillSmart(ctx context.Context, d *model.Disk) {
	// RunTolerant: smartctl devuelve !=0 como bitfield de avisos (p. ej.
	// self-test log con errores en un disco muriendo) pero su JSON es válido.
	out, err := executil.RunTolerant(ctx, 60*time.Second, "smartctl", "-j", "-a", "/dev/"+d.Dev)
	if err != nil && len(out) == 0 {
		d.SmartDetail = "smartctl no disponible"
		return
	}
	if err := parseSmartJSON(out, d); err != nil {
		d.SmartDetail = "salida smartctl no parseable"
		return
	}
}

// parseSmartJSON aplica la salida JSON de 'smartctl -j -a' sobre d.
// Función pura (testeable con fixtures reales).
func parseSmartJSON(out []byte, d *model.Disk) error {
	var sj smartJSON
	if err := json.Unmarshal(out, &sj); err != nil {
		return err
	}
	if sj.ModelName != "" {
		d.Model = sj.ModelName
	}
	if sj.SerialNumber != "" {
		d.Serial = sj.SerialNumber
	}
	if sj.Temperature.Current > 0 {
		t := sj.Temperature.Current
		d.TempC = &t
	}
	if sj.PowerOnTime.Hours > 0 {
		d.Hours = sj.PowerOnTime.Hours
	}
	if sj.UserCapacity.Bytes > 0 {
		d.SizeBytes = sj.UserCapacity.Bytes
	}
	if sj.SmartStatus == nil {
		// Sin sección smart_status (eMMC, USB sin SAT: smartctl emite JSON de
		// error "Unable to detect device type" con exit != 0): no es un FALLO
		// del disco, es que el dispositivo no habla SMART.
		d.Smart = "unknown"
		d.SmartDetail = "no disponible"
	} else {
		d.Smart = "ok"
		d.SmartDetail = "PASSED"
		if !sj.SmartStatus.Passed {
			d.Smart = "crit"
			d.SmartDetail = "FAILED"
		}
	}
	// Avisos ATA: sectores reasignados, pendientes, incorregibles offline
	// y errores CRC de link SATA (firma de cable/puerto, no del disco).
	if sj.AtaAttrs != nil {
		var realloc, pending, offunc, crc int64
		for _, a := range sj.AtaAttrs.Table {
			switch a.Name {
			case "Reallocated_Sector_Ct":
				realloc = a.Raw.Value
			case "Current_Pending_Sector":
				pending = a.Raw.Value
			case "Offline_Uncorrectable":
				offunc = a.Raw.Value
			case "UDMA_CRC_Error_Count":
				crc = a.Raw.Value
			}
		}
		d.ReallocSectors = realloc
		d.PendingSectors = pending
		d.OfflineUncorr = offunc
		d.CrcErrors = crc
		if realloc > 0 || pending > 0 || offunc > 0 {
			if d.Smart == "ok" {
				d.Smart = "warn"
			}
			d.SmartDetail = fmt.Sprintf("%s (realloc=%d pending=%d offunc=%d)", d.SmartDetail, realloc, pending, offunc)
		}
		// CRC aislado NO eleva a warn aquí: es contador de por vida (no se
		// resetea) y lo accionable es su crecimiento — lo evalúa
		// applyCrcDelta en collectOnce con el valor de la pasada anterior.
	}
	// NVMe: temperatura y avisos críticos viven en otro log.
	if sj.NVMeSmartHealthLog != nil {
		if sj.NVMeSmartHealthLog.Temperature > 0 {
			t := sj.NVMeSmartHealthLog.Temperature
			d.TempC = &t
		}
		if sj.NVMeSmartHealthLog.CriticalWarning > 0 {
			if d.Smart == "ok" {
				d.Smart = "warn"
			}
			d.NvmeWarn = sj.NVMeSmartHealthLog.CriticalWarning
			d.SmartDetail = fmt.Sprintf("%s (nvme warning=%d)", d.SmartDetail, sj.NVMeSmartHealthLog.CriticalWarning)
		}
	}
	// Detalle completo (U1): atributos, selftests y error log del mismo JSON.
	// Solo se construye si hay algo que mostrar (ATA o NVMe).
	d.SmartFull = buildSmartDetail(sj)
	return nil
}

// buildSmartDetail — construye el detalle completo (atributos + selftests +
// error log) a partir del JSON de smartctl. Función pura (testeable).
func buildSmartDetail(sj smartJSON) *model.DiskSmartDetail {
	det := &model.DiskSmartDetail{}
	if sj.AtaAttrs != nil {
		det.Protocol = "ata"
		for _, a := range sj.AtaAttrs.Table {
			det.Attributes = append(det.Attributes, model.SmartAttr{
				ID: a.ID, Name: a.Name, Value: a.Value, Worst: a.Worst,
				Thresh: a.Thresh, Raw: a.Raw.Str, WhenFailed: a.WhenFailed,
			})
		}
		if sj.AtaSelftestLog != nil && sj.AtaSelftestLog.Standard != nil {
			for _, s := range sj.AtaSelftestLog.Standard.Table {
				det.Selftests = append(det.Selftests, model.SmartSelftest{
					Type: s.Type.String, Status: s.Status.String,
					LifetimeHours: s.LifetimeHours, Percent: s.Percent,
				})
			}
		}
		if sj.AtaErrorLog != nil && sj.AtaErrorLog.Summary != nil {
			det.ErrorLog.Count = sj.AtaErrorLog.Summary.Count
			for _, e := range sj.AtaErrorLog.Summary.Table {
				ent := model.SmartErrorEntry{ErrorType: e.ErrorType.String, Detail: e.Lba.Str}
				det.ErrorLog.Entries = append(det.ErrorLog.Entries, ent)
			}
		}
	} else if sj.NVMeSmartHealthLog != nil || sj.NVMeSelfTest != nil {
		det.Protocol = "nvme"
		if sj.NVMeSmartHealthLog != nil {
			det.Attributes = append(det.Attributes,
				model.SmartAttr{Name: "temperature", Value: int64(sj.NVMeSmartHealthLog.Temperature), Raw: fmt.Sprintf("%.0f", sj.NVMeSmartHealthLog.Temperature), WhenFailed: "-"},
				model.SmartAttr{Name: "available_spare", Value: int64(sj.NVMeSmartHealthLog.AvailableSparePct), Raw: fmt.Sprintf("%.0f%%", sj.NVMeSmartHealthLog.AvailableSparePct), WhenFailed: "-"},
				model.SmartAttr{Name: "critical_warning", Value: int64(sj.NVMeSmartHealthLog.CriticalWarning), Raw: fmt.Sprintf("%d", sj.NVMeSmartHealthLog.CriticalWarning), WhenFailed: "-"},
			)
		}
		if sj.NVMeSelfTest != nil {
			for _, s := range sj.NVMeSelfTest.SelfTest {
				det.Selftests = append(det.Selftests, model.SmartSelftest{
					Type: s.Type.String, Status: s.Status.String,
					LifetimeHours: s.PowerOnHours, Percent: s.CompletionPercent,
				})
			}
		}
	}
	if len(det.Attributes) == 0 && len(det.Selftests) == 0 && det.ErrorLog.Count == 0 {
		return nil // sin detalle que ofrecer (p. ej. disco sin SMART)
	}
	return det
}

// crcHistoryWarn — acumulado de por vida a partir del cual el CRC se menciona
// como contexto ("histórico, estable") aunque no esté creciendo.
const crcHistoryWarn = 100

// applyCrcDelta calcula el crecimiento de UDMA CRC desde la pasada anterior
// (prev/ok = valor previo) y ajusta estado/detalle. Lo que importa es el
// CRECIMIENTO (link roto AHORA): el acumulado de por vida no se resetea y
// perseguiría al disco equivocado tras un cambio de bahías (caso real
// bigtank 4-Ago-2026: disco absuelto marcado "cable malo" por su histórico
// mientras el puerto roto azotaba a otro disco).
func applyCrcDelta(d *model.Disk, prev int64, ok bool) {
	d.CrcRecent = 0
	if ok && d.CrcErrors > prev {
		d.CrcRecent = d.CrcErrors - prev
	}
	switch {
	case d.CrcRecent > 0:
		if d.Smart == "ok" {
			d.Smart = "warn"
		}
		d.SmartDetail = fmt.Sprintf("%s (crc=%d, +%d nuevos)", d.SmartDetail, d.CrcErrors, d.CrcRecent)
	case d.CrcErrors >= crcHistoryWarn:
		// Histórico estable: contexto, no alarma (el estado no cambia).
		d.SmartDetail = fmt.Sprintf("%s (crc=%d histórico, estable)", d.SmartDetail, d.CrcErrors)
	}
}

// persistSeries guarda disk.<dev>.temp cada seriesInterval (con retención).
func (c *SmartCollector) persistSeries(ctx context.Context, disks []model.Disk) {
	now := time.Now()
	for _, d := range disks {
		if d.TempC == nil || *d.TempC <= 0 {
			continue
		}
		key := "disk." + d.Dev + ".temp"
		if last, ok := c.lastSeries[key]; ok && now.Sub(last) < seriesInterval {
			continue
		}
		if _, err := c.db.ExecContext(ctx,
			"INSERT INTO series(source, ts, value) VALUES (?,?,?)",
			key, now.UTC().Format(time.RFC3339), *d.TempC); err != nil {
			log.Printf("smart series: %v", err)
			continue
		}
		c.lastSeries[key] = now
	}
}
