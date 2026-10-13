// disks_test.go — guardas del handler powerOff: confirmación escrita para
// discos miembros de pool (hot swap) y veto de montajes para discos libres.
package httpapi

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"easyzfs/internal/actions"
	"easyzfs/internal/config"
	"easyzfs/internal/db"
	"easyzfs/internal/executil"
	"easyzfs/internal/model"
)

func init() {
	// El powerOff ejecuta udisksctl; en tests no usamos sudo.
	executil.SetSudoForTest(false)
}

// fakePoolsVdev — PoolProvider mínimo con un vdev que da de alta al disco sdb.
type fakePoolsVdev struct{}

func (fakePoolsVdev) Pools() []model.Pool {
	return []model.Pool{{
		Name:   "tank",
		Status: "DEGRADED",
		Vdevs:  []model.Vdev{{Dev: "sdb", Role: "raidz2", Status: "ONLINE"}},
	}}
}
func (fakePoolsVdev) Datasets() []model.Dataset           { return nil }
func (fakePoolsVdev) SnapshotGroups() []model.SnapGroup   { return nil }
func (fakePoolsVdev) History(string) []model.HistoryEntry { return nil }

// fakeDisksDev — DiskProvider mínimo con el disco sdb (miembro de tank).
type fakeDisksDev struct{}

func (fakeDisksDev) Disks() []model.Disk {
	return []model.Disk{{Dev: "sdb", ByID: "ata-WDC_WD40EFRX-68N_WD-WCC7K1AAAA01", Pool: "tank"}}
}

func setupPowerOffServer(t *testing.T) *Server {
	t.Helper()
	d, err := db.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	if err := db.Migrate(context.Background(), d); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return &Server{
		cfg:   &config.Config{Mock: true},
		act:   actions.NewService(d),
		pools: fakePoolsVdev{},
		disks: fakeDisksDev{},
	}
}

func postPowerOff(t *testing.T, s *Server, dev, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/disks/"+dev+"/poweroff", strings.NewReader(body))
	req.SetPathValue("dev", dev)
	rec := httptest.NewRecorder()
	s.powerOff(rec, req)
	return rec
}

func TestPowerOffPoolRequiereConfirm(t *testing.T) {
	s := setupPowerOffServer(t)

	// Miembro de pool sin confirm → 400 confirm_required.
	rec := postPowerOff(t, s, "sdb", `{}`)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "confirm_required") {
		t.Fatalf("sin confirm: status %d (%s), esperaba 400 confirm_required", rec.Code, rec.Body.String())
	}

	// Confirm con un valor distinto → 400 confirm_required.
	rec = postPowerOff(t, s, "sdb", `{"confirm":"tank"}`)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "confirm_required") {
		t.Fatalf("confirm 'tank': status %d (%s), esperaba 400 confirm_required", rec.Code, rec.Body.String())
	}

	// Confirm con el nombre del dispositivo → el gate pasa (ejecución puede
	// fallar en CI sin udisksctl, pero NUNCA debe pedir confirm de nuevo).
	rec = postPowerOff(t, s, "sdb", `{"confirm":"sdb"}`)
	if strings.Contains(rec.Body.String(), "confirm_required") {
		t.Fatalf("confirm 'sdb': status %d (%s), no debe pedir confirm_required", rec.Code, rec.Body.String())
	}
}
