// apikeys_test.go — store de API keys: creación con clave única mostrada una
// vez, hash almacenado (nunca en claro), validación y borrado.
package apikeys

import (
	"context"
	"strings"
	"testing"

	"easyzfs/internal/db"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	d, err := db.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return NewStore(d)
}

func TestCreateYValidate(t *testing.T) {
	s := newStore(t)
	key, err := s.Create(context.Background(), "monitoring")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(key, "ez_") || len(key) != 67 { // ez_ + 64 hex
		t.Fatalf("clave con formato inesperado: %q (len %d)", key, len(key))
	}
	name, ok := s.Validate(context.Background(), key)
	if !ok || name != "monitoring" {
		t.Fatalf("validate: ok=%v name=%q", ok, name)
	}
	// Clave inválida → no válida.
	if _, ok := s.Validate(context.Background(), "ez_"+strings.Repeat("0", 64)); ok {
		t.Fatal("clave inventada aceptada")
	}
}

func TestHashNuncaEnClaro(t *testing.T) {
	s := newStore(t)
	key, err := s.Create(context.Background(), "monitoring")
	if err != nil {
		t.Fatal(err)
	}
	var hash string
	if err := s.db.QueryRow("SELECT key_hash FROM api_keys").Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(hash, key) || hash == key {
		t.Fatal("la clave en claro está en la BD")
	}
}

func TestDelete(t *testing.T) {
	s := newStore(t)
	key, err := s.Create(context.Background(), "monitoring")
	if err != nil {
		t.Fatal(err)
	}
	keys, err := s.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0].Name != "monitoring" {
		t.Fatalf("lista inesperada: %+v", keys)
	}
	id := keys[0].ID
	if err := s.Delete(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Validate(context.Background(), key); ok {
		t.Fatal("clave válida tras borrarla")
	}
	if _, err := s.List(context.Background()); err != nil {
		t.Fatal(err)
	}
	keys, _ = s.List(context.Background())
	if len(keys) != 0 {
		t.Fatalf("tras borrar quedan claves: %+v", keys)
	}
}
