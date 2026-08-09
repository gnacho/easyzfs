package series

import (
	"context"
	"testing"

	"easyzfs/internal/db"
)

// LTTB reduce manteniendo el primer y último punto, y nunca devuelve más de
// threshold puntos ni más que los originales.
func TestLTTB_Reduce(t *testing.T) {
	data := make([]Point, 100)
	for i := range data {
		data[i] = Point{Ts: int64(i), Value: float64(i*i % 37)}
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
func TestRange_FiltraYDowsample(t *testing.T) {
	d, err := db.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := db.Migrate(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	// 200 muestras de pool.tank.used_pct (una por "minuto" en epoch)
	for i := 0; i < 200; i++ {
		ts := int64(1_700_000_000 + i*60)
		if _, err := d.Exec(
			"INSERT INTO series(source, ts, value) VALUES (?, datetime(?, 'unixepoch'), ?)",
			"pool.tank.used_pct", ts, float64(i)); err != nil {
			t.Fatal(err)
		}
	}
	// una muestra de otra fuente que NO debe colarse
	d.Exec("INSERT INTO series(source, ts, value) VALUES ('disk.sda.temp', datetime(1700000060, 'unixepoch'), 42)")

	from, to := int64(1_700_000_000), int64(1_700_000_000+199*60)
	got, err := Range(context.Background(), d, "pool.tank.used_pct", from, to, 50)
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
	for _, p := range got {
		if p.Value == 42 && p.Ts == 1700000060 {
			t.Errorf("se coló una muestra de otra fuente")
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
	got, err := Range(context.Background(), d, "pool.tank.used_pct", 100, 200, 50)
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
	if _, _, err := ParseDays(400); err == nil {
		t.Error("days=400 debería fallar")
	}
}

func TestValidSource(t *testing.T) {
	for _, ok := range []struct{ src string; want bool }{
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
