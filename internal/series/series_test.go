package series

import (
	"context"
	"testing"
	"time"

	"easyzfs/internal/db"
)

// LTTB reduce manteniendo el primer y último punto, y nunca devuelve más de
// threshold puntos ni más que los originales.
func TestLTTB_Reduce(t *testing.T) {
	data := make([]Point, 100)
	for i := range data {
		data[i] = Point{Ts: int64(i), Value: float64(i * i % 37)}
	}
	got := LTTB(data, 10)
	if len(got) != 10 {
		t.Fatalf("LTTB(100,10) = %d puntos, esperado 10", len(got))
	}
	if got[0] != data[0] {
		t.Errorf("primer punto cambiado: %+v != %+v", got[0], data[0])
	}
	if got[len(got)-1] != data[len(data)-1] {
		t.Errorf("último punto cambiado")
	}
}

// LTTB con threshold >= len devuelve la serie intacta.
func TestLTTB_NoReduce(t *testing.T) {
	data := []Point{{1, 2}, {2, 3}}
	if got := LTTB(data, 5); len(got) != 2 {
		t.Fatalf("LTTB pequeño = %d, esperado 2", len(got))
	}
	if got := LTTB(data, 0); len(got) != 2 {
		t.Fatalf("LTTB threshold<=0 = %d, esperado 2", len(got))
	}
}

// Range filtra por fuente y ventana de tiempo y aplica LTTB.
// Las muestras usan ts RELATIVOS al presente (dentro de la retención raw),
// porque fuera de ella Range consulta series_daily (test aparte).
func TestRange_FiltraYDowsample(t *testing.T) {
	d, err := db.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := db.Migrate(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	base := now.Add(-200 * time.Minute)
	// 200 muestras de pool.tank.used_pct (una por "minuto")
	for i := 0; i < 200; i++ {
		ts := base.Add(time.Duration(i) * time.Minute).Unix()
		if _, err := d.Exec(
			"INSERT INTO series(source, ts, value) VALUES (?, datetime(?, 'unixepoch'), ?)",
			"pool.tank.used_pct", ts, float64(i)); err != nil {
			t.Fatal(err)
		}
	}
	// una muestra de otra fuente que NO debe colarse
	d.Exec("INSERT INTO series(source, ts, value) VALUES ('disk.sda.temp', datetime(?, 'unixepoch'), 42)", base.Add(10*time.Minute).Unix())

	from, to := base.Unix(), now.Unix()
	got, err := Range(context.Background(), d, "pool.tank.used_pct", from, to, 50, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("serie vacía con datos presentes")
	}
	if len(got) > 50 {
		t.Fatalf("downsampling no aplicó: %d > 50", len(got))
	}
	// el rango excluye la muestra de otra fuente (solo pool.tank.used_pct)
	intruderTS := base.Add(10 * time.Minute).Unix()
	for _, p := range got {
		if p.Ts == intruderTS {
			t.Errorf("se coló una muestra de otra fuente (ts=%d)", intruderTS)
		}
	}
}

// Rango sin datos o con to<=from devuelve slice vacío, no error.
func TestRange_Vacio(t *testing.T) {
	d, err := db.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := db.Migrate(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	got, err := Range(context.Background(), d, "pool.tank.used_pct", 100, 200, 50, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("esperado vacío, %d puntos", len(got))
	}
}

// ParseDays valida el rango.
func TestParseDays(t *testing.T) {
	from, to, err := ParseDays(7)
	if err != nil {
		t.Fatal(err)
	}
	if to <= from || to < 0 {
		t.Errorf("rango inválido: from=%d to=%d", from, to)
	}
	if _, _, err := ParseDays(0); err == nil {
		t.Error("days=0 debería fallar")
	}
	if _, _, err := ParseDays(2000); err == nil {
		t.Error("days=2000 debería fallar (máx 1825)")
	}
	if _, _, err := ParseDays(1825); err != nil {
		t.Errorf("days=1825 debería ser válido: %v", err)
	}
}

func TestValidSource(t *testing.T) {
	for _, ok := range []struct {
		src  string
		want bool
	}{
		{"pool.tank.used_pct", true},
		{"disk.sda.temp", true},
		{"pool..bad", false},
		{"other", false},
		{"", false},
		{"disk..bad", false},
	} {
		if got := ValidSource(ok.src); got != ok.want {
			t.Errorf("ValidSource(%q) = %v, esperado %v", ok.src, got, ok.want)
		}
	}
}

// RollupSeriesToDaily agrega a series_daily las crudas viejas y las borra.
func TestRollupYRangeDiario(t *testing.T) {
	d, err := db.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := db.Migrate(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	// 40 días atrás: 4 muestras del mismo día (deben agregarse a daily).
	old := now.AddDate(0, 0, -40)
	for i, v := range []float64{10, 20, 30, 40} {
		ts := time.Date(old.Year(), old.Month(), old.Day(), 1+i, 0, 0, 0, time.UTC)
		if _, err := d.Exec(
			"INSERT INTO series(source, ts, value) VALUES (?, datetime(?, 'unixepoch'), ?)",
			"pool.tank.used_pct", ts.Unix(), v); err != nil {
			t.Fatal(err)
		}
	}
	// 1 día atrás: cruda que NO debe agregarse (dentro de retención 30d).
	recent := now.AddDate(0, 0, -1)
	if _, err := d.Exec(
		"INSERT INTO series(source, ts, value) VALUES (?, datetime(?, 'unixepoch'), 55)",
		"pool.tank.used_pct", recent.Unix()); err != nil {
		t.Fatal(err)
	}

	if _, err := db.RollupSeriesToDaily(context.Background(), d, 30); err != nil {
		t.Fatal(err)
	}

	// La cruda vieja se borró; la reciente sigue.
	var nRecent int
	d.QueryRow("SELECT COUNT(*) FROM series WHERE source='pool.tank.used_pct'").Scan(&nRecent)
	if nRecent != 1 {
		t.Fatalf("quedan %d crudas, esperado 1 (solo la reciente)", nRecent)
	}
	// series_daily tiene 1 día con avg 25, min 10, max 40.
	var avg, min, max float64
	if err := d.QueryRow(
		"SELECT avg, min, max FROM series_daily WHERE source='pool.tank.used_pct'").Scan(&avg, &min, &max); err != nil {
		t.Fatal(err)
	}
	if avg != 25 || min != 10 || max != 40 {
		t.Fatalf("agregado %v/%v/%v, esperado 25/10/40", avg, min, max)
	}

	// Range con rango largo (50 días) combina daily + raw y no rompe.
	from := now.AddDate(0, 0, -50).Unix()
	to := now.Unix()
	pts, err := Range(context.Background(), d, "pool.tank.used_pct", from, to, 100, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) < 2 {
		t.Fatalf("rango largo con %d puntos, esperado >= 2 (daily + raw)", len(pts))
	}
	// El punto diario es el mediodía UTC: ts ~ old-day 12:00.
	foundDaily := false
	for _, p := range pts {
		if p.Value == 25 {
			foundDaily = true
		}
	}
	if !foundDaily {
		t.Error("no aparece el agregado diario (25) en el rango largo")
	}
}

// Range con rango corto (dentro de retención) no toca series_daily.
func TestRangeCortoNoUsaDaily(t *testing.T) {
	d, err := db.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := db.Migrate(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	// Cruda reciente.
	if _, err := d.Exec(
		"INSERT INTO series(source, ts, value) VALUES ('pool.tank.used_pct', datetime(?, 'unixepoch'), 42)",
		now.Add(-time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	// Un daily de una fuente distinta que no debe colarse.
	if _, err := d.Exec(
		"INSERT INTO series_daily(source, day, avg, min, max, count) VALUES ('disk.sda.temp', ?, 99, 99, 99, 1)",
		now.AddDate(0, 0, -60).Format("2006-01-02")); err != nil {
		t.Fatal(err)
	}
	pts, err := Range(context.Background(), d, "pool.tank.used_pct", now.Add(-2*24*time.Hour).Unix(), now.Unix(), 100, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) != 1 || pts[0].Value != 42 {
		t.Fatalf("rango corto devolvió %v, esperado solo la cruda 42", pts)
	}
}
