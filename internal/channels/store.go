// store.go — persistencia de la configuración de canales en SQLite (#134).
// Una fila única ('all') con el Config completo en JSON. Los secretos viven
// aquí y NUNCA se devuelven por la API (solo "tokenSet").
package channels

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// configRowName — clave de la fila única con la config de canales.
const configRowName = "all"

// Store persiste la config de canales.
type Store struct {
	db *sql.DB
}

// NewStore crea el store sobre la BD de la app.
func NewStore(d *sql.DB) *Store {
	return &Store{db: d}
}

// Load lee la config persistida. ok=false si aún no hay fila (primera vez:
// se siembra desde el entorno).
func (s *Store) Load(ctx context.Context) (cfg Config, ok bool, err error) {
	var raw string
	err = s.db.QueryRowContext(ctx,
		"SELECT json FROM channel_configs WHERE name=?", configRowName).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Config{}, false, nil
	}
	if err != nil {
		return Config{}, false, err
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return Config{}, false, err
	}
	return cfg, true, nil
}

// Save persiste la config completa (upsert de la fila única).
func (s *Store) Save(ctx context.Context, cfg Config) error {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO channel_configs(name, json, updated_at) VALUES(?,?,datetime('now'))
		ON CONFLICT(name) DO UPDATE SET json=excluded.json, updated_at=datetime('now')`,
		configRowName, string(raw))
	return err
}
