package alerts

import (
	"context"
	"strings"
	"testing"

	"easyzfs/internal/db"
	"easyzfs/internal/hub"
	"easyzfs/internal/settings"
)

func newTestAlerter(t *testing.T) (*Alerter, func()) {
	t.Helper()
	d, err := db.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.Migrate(context.Background(), d); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	st, err := settings.NewStore(d)
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	return New(d, hub.NewHub(), st), func() { d.Close() }
}

// Regresión del bug de spam de alertas/push (3-Ago-2026): un contador
// volátil en el mensaje (UDMA CRC creciendo cada pasada SMART de 10 min)
// generaba una alerta NUEVA cada vez porque la deduplicación era por
// (source, message). Con kind la dedupe es por (source, kind) y la alerta
// activa solo REFRESCA su mensaje.
func TestRaiseKind_DedupeContadoresVolatiles(t *testing.T) {
	a, closeDB := newTestAlerter(t)
	defer closeDB()
	ctx := context.Background()

	a.RaiseKind(ctx, "warn", "smart.sdc", "disks:sdc",
		"SMART con avisos en sdc: PASSED (realloc=48 pending=0 offunc=0) (crc=100)",
		"smart_status", map[string]any{"dev": "sdc", "detail": "crc=100"})
	a.RaiseKind(ctx, "warn", "smart.sdc", "disks:sdc",
		"SMART con avisos en sdc: PASSED (realloc=48 pending=0 offunc=0) (crc=200)",
		"smart_status", map[string]any{"dev": "sdc", "detail": "crc=200"})

	var n int
	if err := a.db.QueryRow("SELECT COUNT(*) FROM alerts").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("alertas = %d, esperada 1 (dedupe por source+kind)", n)
	}
	var msg, kind string
	if err := a.db.QueryRow("SELECT message, kind FROM alerts").Scan(&msg, &kind); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "crc=200") {
		t.Errorf("mensaje no refrescado al contador actual: %q", msg)
	}
	if kind != "smart_status" {
		t.Errorf("kind = %q, esperado smart_status", kind)
	}
}

// La vía legado (kind "") sigue deduplicando por (source, message) exacto.
func TestRaise_DedupeLegado(t *testing.T) {
	a, closeDB := newTestAlerter(t)
	defer closeDB()
	ctx := context.Background()

	a.Raise(ctx, "info", "test.legado", "", "mensaje fijo")
	a.Raise(ctx, "info", "test.legado", "", "mensaje fijo")     // duplicada: se ignora
	a.Raise(ctx, "info", "test.legado", "", "mensaje distinto") // nueva: entra

	var n int
	if err := a.db.QueryRow("SELECT COUNT(*) FROM alerts").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("alertas = %d, esperadas 2", n)
	}
}
