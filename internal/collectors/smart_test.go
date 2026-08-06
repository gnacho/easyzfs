package collectors

import (
	"os"
	"strings"
	"testing"

	"easyzfs/internal/model"
)

// Caso real reportado: la lista cruda de lsblk incluía zd0 (zvol) y
// mmcblk0boot0/boot1 (particiones hardware eMMC), que no deben mostrarse.
func TestIsPhysicalDisk(t *testing.T) {
	yes := []string{
		"sda", "sdaa", "hdb", "vda", "xvdc",
		"nvme0n1", "nvme1n1", "nvme2n1", "nvme3n1",
		"mmcblk0", "mmcblk1",
	}
	no := []string{
		"zd0", "zd16", // zvols ZFS
		"loop0", "loop7", "ram0", "dm-0", "dm-1", "sr0", "fd0",
		"mmcblk0boot0", "mmcblk0boot1", "mmcblk0rpmb", // particiones hardware eMMC
		"sda1", "nvme0n1p1", "mmcblk0p1", // particiones (lsblk type=part, doble seguro)
		"", "md0", "nbd0", "zram0",
	}
	for _, n := range yes {
		if !isPhysicalDisk(n) {
			t.Errorf("%s debería ser disco físico", n)
		}
	}
	for _, n := range no {
		if isPhysicalDisk(n) {
			t.Errorf("%s NO debería ser disco físico", n)
		}
	}
}

// Regresión del bug "disco muriendo aparece como SMART no disponible":
// smartctl sale con exit 192 (self-test log con errores) pero emite JSON
// válido; antes se descartaba la salida y el disco quedaba "unknown".
// Fixture = salida real de /dev/sdb (TEST0001, 2528 realloc + 184 pending)
// de un host de producción (anonimizado).
func TestParseSmartJSON_DiscoMuriendoExit192(t *testing.T) {
	out, err := os.ReadFile("testdata/smart_sdb_exit192.json")
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	d := model.Disk{Dev: "sdb", Smart: "unknown", SmartDetail: "no disponible"}
	if err := parseSmartJSON(out, &d); err != nil {
		t.Fatalf("parseSmartJSON: %v", err)
	}
	if d.Smart != "warn" {
		t.Errorf("Smart = %q, esperado warn", d.Smart)
	}
	if d.ReallocSectors != 2528 || d.PendingSectors != 184 || d.OfflineUncorr != 184 {
		t.Errorf("contadores = realloc:%d pending:%d offunc:%d, esperados 2528/184/184",
			d.ReallocSectors, d.PendingSectors, d.OfflineUncorr)
	}
	if d.Hours == 0 {
		t.Errorf("horas de uso no parseadas")
	}
}

// Tormenta de link SATA (caso real sdc TEST0002: >1,1M UDMA CRC): el parser
// rellena CrcErrors pero NO decide por el acumulado (es de por vida y no se
// resetea); el warn llega por los realloc=48. El juicio del CRC es del delta
// (applyCrcDelta, testeado aparte).
func TestParseSmartJSON_TormentaCRC(t *testing.T) {
	out := []byte(`{
		"model_name": "ST12000NM0127", "serial_number": "TEST0002",
		"smart_status": {"passed": true},
		"power_on_time": {"hours": 9194},
		"ata_smart_attributes": {"table": [
			{"name": "Reallocated_Sector_Ct", "raw": {"value": 48}},
			{"name": "Current_Pending_Sector", "raw": {"value": 0}},
			{"name": "Offline_Uncorrectable", "raw": {"value": 0}},
			{"name": "UDMA_CRC_Error_Count", "raw": {"value": 1184752}}
		]}
	}`)
	d := model.Disk{Dev: "sdc", Smart: "unknown", SmartDetail: "no disponible"}
	if err := parseSmartJSON(out, &d); err != nil {
		t.Fatalf("parseSmartJSON: %v", err)
	}
	if d.Smart != "warn" {
		t.Errorf("Smart = %q, esperado warn (por realloc=48)", d.Smart)
	}
	if d.CrcErrors != 1184752 {
		t.Errorf("CrcErrors = %d, esperado 1184752", d.CrcErrors)
	}
	if strings.Contains(d.SmartDetail, "crc=") {
		t.Errorf("SmartDetail %q no debe juzgar el CRC absoluto (eso es el delta)", d.SmartDetail)
	}
}

// applyCrcDelta — la regla del 4-Ago-2026: lo accionable es el CRECIMIENTO.
// Tormenta activa → warn + "+N nuevos"; histórico alto congelado → contexto
// "estable" sin elevar warn; primera pasada (sin previo) → delta 0.
func TestApplyCrcDelta(t *testing.T) {
	casos := []struct {
		nombre     string
		crc, prev  int64
		ok         bool
		smart      string
		wantSmart  string
		wantRecent int64
		wantDetail string
	}{
		{"tormenta activa", 417, 284, true, "ok", "warn", 133, "+133 nuevos"},
		{"histórico congelado", 1196981, 1196981, true, "ok", "ok", 0, "histórico, estable"},
		{"primera pasada", 417, 0, false, "ok", "ok", 0, "histórico, estable"},
		{"sano", 2, 2, true, "ok", "ok", 0, "PASSED"},
		{"contador no crece", 500, 600, true, "ok", "ok", 0, "histórico, estable"},
	}
	for _, c := range casos {
		d := model.Disk{Dev: "sdx", Smart: c.smart, SmartDetail: "PASSED", CrcErrors: c.crc}
		applyCrcDelta(&d, c.prev, c.ok)
		if d.Smart != c.wantSmart {
			t.Errorf("%s: Smart = %q, esperado %q", c.nombre, d.Smart, c.wantSmart)
		}
		if d.CrcRecent != c.wantRecent {
			t.Errorf("%s: CrcRecent = %d, esperado %d", c.nombre, d.CrcRecent, c.wantRecent)
		}
		if !strings.Contains(d.SmartDetail, c.wantDetail) {
			t.Errorf("%s: SmartDetail %q sin %q", c.nombre, d.SmartDetail, c.wantDetail)
		}
	}
}

// Dispositivo sin SMART (eMMC sin SAT de un host real): smartctl emite JSON de error
// sin sección smart_status. Debe quedar "unknown", NO "crit" (regresión del
// parseo tolerante: smart_status ausente se deserializaba como passed=false).
func TestParseSmartJSON_SinSmart(t *testing.T) {
	out, err := os.ReadFile("testdata/smart_mmcblk0_nosmart.json")
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	d := model.Disk{Dev: "mmcblk0", Smart: "unknown", SmartDetail: "no disponible"}
	if err := parseSmartJSON(out, &d); err != nil {
		t.Fatalf("parseSmartJSON: %v", err)
	}
	if d.Smart != "unknown" {
		t.Errorf("Smart = %q, esperado unknown", d.Smart)
	}
	if d.SmartDetail != "no disponible" {
		t.Errorf("SmartDetail = %q, esperado 'no disponible'", d.SmartDetail)
	}
}

// Disco sano con un CRC histórico suelto: no debe avisar por CRC.
func TestParseSmartJSON_SanoConCRCHistorico(t *testing.T) {
	out := []byte(`{
		"model_name": "ST12000NM0127", "serial_number": "TEST0004",
		"smart_status": {"passed": true},
		"power_on_time": {"hours": 9519},
		"ata_smart_attributes": {"table": [
			{"name": "Reallocated_Sector_Ct", "raw": {"value": 0}},
			{"name": "Current_Pending_Sector", "raw": {"value": 0}},
			{"name": "Offline_Uncorrectable", "raw": {"value": 0}},
			{"name": "UDMA_CRC_Error_Count", "raw": {"value": 3}}
		]}
	}`)
	d := model.Disk{Dev: "sdd", Smart: "unknown", SmartDetail: "no disponible"}
	if err := parseSmartJSON(out, &d); err != nil {
		t.Fatalf("parseSmartJSON: %v", err)
	}
	if d.Smart != "ok" {
		t.Errorf("Smart = %q, esperado ok", d.Smart)
	}
}

// U1 — detalle SMART completo desde un fixture ATA real (atributos con
// id/value/worst/thresh/when_failed + selftest log + error log).
func TestParseSmartDetail_ATA(t *testing.T) {
	out := []byte(`{
		"model_name": "ST12000NM0127", "serial_number": "TEST0005",
		"smart_status": {"passed": true},
		"power_on_time": {"hours": 9519},
		"ata_smart_attributes": {"table": [
			{"id": 1, "name": "Raw_Read_Error_Rate", "value": 100, "worst": 16, "thresh": 6, "raw": {"value": 0, "string": "0"}, "when_failed": "-"},
			{"id": 5, "name": "Reallocated_Sector_Ct", "value": 100, "worst": 100, "thresh": 36, "raw": {"value": 48, "string": "48"}, "when_failed": "-"},
			{"id": 197, "name": "Current_Pending_Sector", "value": 99, "worst": 99, "thresh": 0, "raw": {"value": 4, "string": "4"}, "when_failed": "Past"}
		]},
		"ata_smart_selftest_log": {"standard": {"table": [
			{"type": {"string": "Short self-test"}, "status": {"string": "Completed without error"}, "lifetime_hours": 9519, "percent": 100},
			{"type": {"string": "Extended self-test"}, "status": {"string": "Completed with errors"}, "lifetime_hours": 9500, "percent": 90}
		]}},
		"ata_error_log": {"summary": {"count": 2, "table": [
			{"error_type": {"string": "NCQ"}, "lba": {"string": "1048576"}},
			{"error_type": {"string": "ABRT"}, "lba": {"string": "0"}}
		]}}
	}`)
	d := model.Disk{Dev: "sde", Smart: "unknown", SmartDetail: "no disponible"}
	if err := parseSmartJSON(out, &d); err != nil {
		t.Fatalf("parseSmartJSON: %v", err)
	}
	if d.SmartFull == nil {
		t.Fatal("SmartFull no se rellenó para un disco ATA")
	}
	if d.SmartFull.Protocol != "ata" {
		t.Errorf("Protocol = %q, esperado ata", d.SmartFull.Protocol)
	}
	if len(d.SmartFull.Attributes) != 3 {
		t.Fatalf("len(Attributes) = %d, esperado 3", len(d.SmartFull.Attributes))
	}
	pend := d.SmartFull.Attributes[2]
	if pend.Name != "Current_Pending_Sector" || pend.ID != 197 || pend.Raw != "4" || pend.WhenFailed != "Past" {
		t.Errorf("attr 197 = %+v", pend)
	}
	if len(d.SmartFull.Selftests) != 2 {
		t.Fatalf("len(Selftests) = %d, esperado 2", len(d.SmartFull.Selftests))
	}
	if d.SmartFull.Selftests[0].Type != "Short self-test" || d.SmartFull.Selftests[0].Percent != 100 {
		t.Errorf("selftest[0] = %+v", d.SmartFull.Selftests[0])
	}
	if d.SmartFull.ErrorLog.Count != 2 || len(d.SmartFull.ErrorLog.Entries) != 2 {
		t.Errorf("error log = %+v", d.SmartFull.ErrorLog)
	}
}

// U1 — NVMe: el health log se mapea a atributos y el self-test a selftests.
func TestParseSmartDetail_NVMe(t *testing.T) {
	out := []byte(`{
		"model_name": "Samsung SSD 990 PRO", "serial_number": "TEST0006",
		"smart_status": {"passed": true},
		"power_on_time": {"hours": 123},
		"nvme_smart_health_information_log": {"temperature": 42, "available_spare": 100, "critical_warning": 0},
		"nvme_self_test_log_1": {"self_test": [
			{"type": {"string": "Short self-test"}, "status": {"string": "Completed without error"}, "power_on_hours": 120, "completion_percent": 100}
		]}
	}`)
	d := model.Disk{Dev: "nvme0n1", Smart: "unknown", SmartDetail: "no disponible"}
	if err := parseSmartJSON(out, &d); err != nil {
		t.Fatalf("parseSmartJSON: %v", err)
	}
	if d.SmartFull == nil {
		t.Fatal("SmartFull no se rellenó para NVMe")
	}
	if d.SmartFull.Protocol != "nvme" {
		t.Errorf("Protocol = %q, esperado nvme", d.SmartFull.Protocol)
	}
	if len(d.SmartFull.Attributes) != 3 {
		t.Fatalf("len(Attributes) = %d, esperado 3 (health log)", len(d.SmartFull.Attributes))
	}
	if d.SmartFull.Attributes[0].Name != "temperature" || d.SmartFull.Attributes[0].Raw != "42" {
		t.Errorf("attr temp = %+v", d.SmartFull.Attributes[0])
	}
	if len(d.SmartFull.Selftests) != 1 {
		t.Fatalf("len(Selftests) = %d, esperado 1", len(d.SmartFull.Selftests))
	}
}

// U1 — disco sin SMART (eMMC/USB): SmartFull debe quedar nil y no fallar.
func TestParseSmartDetail_SinSmart(t *testing.T) {
	out, err := os.ReadFile("testdata/smart_mmcblk0_nosmart.json")
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	d := model.Disk{Dev: "mmcblk0", Smart: "unknown", SmartDetail: "no disponible"}
	if err := parseSmartJSON(out, &d); err != nil {
		t.Fatalf("parseSmartJSON: %v", err)
	}
	if d.SmartFull != nil {
		t.Error("SmartFull debería ser nil para un disco sin SMART")
	}
}
