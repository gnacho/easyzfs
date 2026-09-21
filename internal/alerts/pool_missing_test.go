// pool_missing_test.go — detección de pools conocidos no importados (#136).
package alerts

import (
	"context"
	"strings"
	"testing"
	"time"

	"easyzfs/internal/model"
)

// fresh install: tabla known_pools vacía, un pool presente → se registra y
// NO hay alerta (distingue "nunca visto" de "visto antes y ahora ausente").
func TestTrackPools_FreshInstallNoAlerta(t *testing.T) {
	a, closeDB := newTestAlerter(t)
	defer closeDB()
	a.SetPoolMissingAfter(time.Minute)
	ctx := context.Background()

	a.EvaluatePools(ctx, []model.Pool{
		{Name: "tank", TotalBytes: 100, UsedBytes: 10, Status: "ONLINE"},
	})

	var n int
	if err := a.db.QueryRow("SELECT COUNT(*) FROM alerts").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("alertas = %d, esperadas 0 en instalación fresca", n)
	}
	var lastSeen string
	if err := a.db.QueryRow("SELECT last_seen_at FROM known_pools WHERE name='tank'").Scan(&lastSeen); err != nil {
		t.Fatalf("tank no registrado en known_pools: %v", err)
	}
}

// pool visto antes y ahora ausente de zpool list → alerta crítica pool_missing.
func TestTrackPools_PoolAusenteAlerta(t *testing.T) {
	a, closeDB := newTestAlerter(t)
	defer closeDB()
	a.SetPoolMissingAfter(time.Minute)
	ctx := context.Background()

	stale := time.Now().UTC().Add(-10 * time.Minute).Format(time.RFC3339)
	if _, err := a.db.Exec(
		"INSERT INTO known_pools(name, first_seen_at, last_seen_at) VALUES('tank',?,?)",
		stale, stale); err != nil {
		t.Fatal(err)
	}

	a.EvaluatePools(ctx, nil)

	var level, kind, msg string
	err := a.db.QueryRow("SELECT level, kind, message FROM alerts WHERE source='pool.tank'").
		Scan(&level, &kind, &msg)
	if err != nil {
		t.Fatalf("alerta pool_missing no creada: %v", err)
	}
	if level != "crit" || kind != "pool_missing" {
		t.Errorf("level/kind = %s/%s, esperado crit/pool_missing", level, kind)
	}
	if !strings.Contains(msg, "tank") || !strings.Contains(msg, "no importado") {
		t.Errorf("mensaje inesperado: %q", msg)
	}
}

// ventana de gracia: ausente desde hace menos de PoolMissingAfter → sin alerta.
func TestTrackPools_DentroDeLaVentanaNoAlerta(t *testing.T) {
	a, closeDB := newTestAlerter(t)
	defer closeDB()
	a.SetPoolMissingAfter(5 * time.Minute)
	ctx := context.Background()

	recent := time.Now().UTC().Add(-2 * time.Minute).Format(time.RFC3339)
	if _, err := a.db.Exec(
		"INSERT INTO known_pools(name, first_seen_at, last_seen_at) VALUES('tank',?,?)",
		recent, recent); err != nil {
		t.Fatal(err)
	}

	a.EvaluatePools(ctx, nil)

	var n int
	if err := a.db.QueryRow("SELECT COUNT(*) FROM alerts").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("alertas = %d, esperadas 0 dentro de la ventana", n)
	}
}

// el pool vuelve a importarse → desaparece de MissingPools y la alerta no se
// re-eleva (se refrescaría la existente, no crearía otra).
func TestTrackPools_PoolVuelveSinRealerta(t *testing.T) {
	a, closeDB := newTestAlerter(t)
	defer closeDB()
	a.SetPoolMissingAfter(time.Minute)
	ctx := context.Background()

	stale := time.Now().UTC().Add(-10 * time.Minute).Format(time.RFC3339)
	if _, err := a.db.Exec(
		"INSERT INTO known_pools(name, first_seen_at, last_seen_at) VALUES('tank',?,?)",
		stale, stale); err != nil {
		t.Fatal(err)
	}

	a.EvaluatePools(ctx, nil) // ausente → alerta
	a.EvaluatePools(ctx, []model.Pool{
		{Name: "tank", TotalBytes: 100, UsedBytes: 10, Status: "ONLINE"},
	}) // de vuelta

	m, err := a.MissingPools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 0 {
		t.Fatalf("MissingPools = %v, esperado vacío tras reimportar", m)
	}
	var n int
	if err := a.db.QueryRow("SELECT COUNT(*) FROM alerts WHERE kind='pool_missing'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("alertas pool_missing = %d, esperada 1 (dedupe, no re-alerta)", n)
	}
}

// MissingPools respeta el corte por last_seen (contrato de /api/pools/missing).
func TestMissingPools_Contrato(t *testing.T) {
	a, closeDB := newTestAlerter(t)
	defer closeDB()
	a.SetPoolMissingAfter(time.Minute)
	ctx := context.Background()

	stale := time.Now().UTC().Add(-10 * time.Minute).Format(time.RFC3339)
	recent := time.Now().UTC().Format(time.RFC3339)
	seeds := []struct {
		name    string
		lastSeen string
	}{
		{"tank", stale},
		{"vault", stale},
		{"data", recent},
	}
	for _, s := range seeds {
		if _, err := a.db.Exec(
			"INSERT INTO known_pools(name, first_seen_at, last_seen_at) VALUES(?,?,?)",
			s.name, s.lastSeen, s.lastSeen); err != nil {
			t.Fatal(err)
		}
	}

	m, err := a.MissingPools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 2 {
		t.Fatalf("MissingPools = %d, esperados 2 (tank, vault)", len(m))
	}
	for _, mp := range m {
		if mp.Name == "data" {
			t.Errorf("data no debería figurar (last_seen reciente)")
		}
		if mp.LastSeen.IsZero() {
			t.Errorf("LastSeen vacío para %s", mp.Name)
		}
	}
}
