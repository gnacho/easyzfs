// Package apikeys — API keys de SOLO LECTURA (#87) para integraciones
// externas. La clave en claro (ez_<64 hex>) se devuelve una única vez al
// crearla; en BD solo vive su hash SHA-256. El middleware de auth valida
// `Authorization: Bearer <clave>` y limita a peticiones GET/HEAD.
package apikeys

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"
)

// KeyInfo — vista pública de una clave (contrato GET /api/keys).
type KeyInfo struct {
	ID        int64      `json:"id"`
	Name      string     `json:"name"`
	CreatedAt *time.Time `json:"created_at"`
	LastUsed  *time.Time `json:"last_used"`
}

// ErrNotFound — la clave o id no existe.
var ErrNotFound = errors.New("clave no encontrada")

// Store — acceso a la tabla api_keys.
type Store struct {
	db *sql.DB
}

// NewStore crea el store de API keys.
func NewStore(d *sql.DB) *Store {
	return &Store{db: d}
}

// Create genera una clave nueva (ez_<64 hex>) para el nombre dado, guarda su
// hash y devuelve la clave en claro UNA vez.
func (s *Store) Create(ctx context.Context, name string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	key := "ez_" + hex.EncodeToString(raw)
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO api_keys(name, key_hash) VALUES (?, ?)", name, hashOf(key))
	if err != nil {
		return "", err
	}
	return key, nil
}

// List devuelve las claves sin su hash (nunca se expone).
func (s *Store) List(ctx context.Context) ([]KeyInfo, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, name, created_at, last_used FROM api_keys ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []KeyInfo{}
	for rows.Next() {
		var k KeyInfo
		var created, last sql.NullString
		if err := rows.Scan(&k.ID, &k.Name, &created, &last); err != nil {
			return nil, err
		}
		if created.Valid {
			if t, err := time.Parse("2006-01-02 15:04:05", created.String); err == nil {
				k.CreatedAt = &t
			}
		}
		if last.Valid && last.String != "" {
			if t, err := time.Parse("2006-01-02 15:04:05", last.String); err == nil {
				k.LastUsed = &t
			}
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// Delete elimina una clave por id.
func (s *Store) Delete(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM api_keys WHERE id=?", id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Validate comprueba una clave en claro contra el hash almacenado y actualiza
// last_used. Devuelve el nombre de la clave si es válida.
func (s *Store) Validate(ctx context.Context, key string) (string, bool) {
	if key == "" {
		return "", false
	}
	var name string
	err := s.db.QueryRowContext(ctx,
		"SELECT name FROM api_keys WHERE key_hash=?", hashOf(key)).Scan(&name)
	if err != nil {
		return "", false
	}
	_, _ = s.db.ExecContext(ctx,
		"UPDATE api_keys SET last_used=? WHERE key_hash=?",
		time.Now().Format("2006-01-02 15:04:05"), hashOf(key))
	return name, true
}

// hashOf — SHA-256 en hex (la clave nunca se almacena en claro).
func hashOf(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}
