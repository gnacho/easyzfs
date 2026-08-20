// Package users — multiusuario: CRUD, verificación de credenciales y bootstrap.
package users

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"
)

// User — vista pública de un usuario (contrato GET /api/users).
type User struct {
	Name        string     `json:"user"`
	Role        string     `json:"role"`         // "admin" | "user"
	Language    string     `json:"language"`     // "auto" | "es" | "en"
	DisplayName string     `json:"display_name"` // nombre visible (saludos); vacío = username
	Email       string     `json:"email"`        // opcional
	Avatar      string     `json:"avatar"`       // nombre del fichero en <datadir>/avatars/; vacío = sin foto
	LastLogin   *time.Time `json:"last_login"`
	Sessions    int        `json:"sessions"`
}

// Errores de dominio (mapeados a códigos HTTP en httpapi).
var (
	ErrExists        = errors.New("el usuario ya existe")
	ErrNotFound      = errors.New("usuario no encontrado")
	ErrInvalidName   = errors.New("nombre de usuario inválido")
	ErrInvalidRole   = errors.New("rol inválido (admin|user)")
	ErrInvalidLang   = errors.New("idioma inválido (auto|es|en)")
	ErrInvalidEmail  = errors.New("email inválido")
	ErrWeakPassword  = errors.New("la contraseña debe tener al menos 8 caracteres")
	ErrBadCredential = errors.New("credenciales incorrectas")
)

var nameRe = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,32}$`)

// emailRe — validación pragmática: algo@algo.dominio (el email es opcional y
// solo informativo; no se envía correo, no hace falta RFC completa).
var emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]{2,}$`)

// Store — acceso a la tabla users.
type Store struct {
	db *sql.DB
}

// NewStore crea el store de usuarios.
func NewStore(d *sql.DB) *Store {
	return &Store{db: d}
}

// Count devuelve el número total de usuarios.
func (s *Store) Count(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&n)
	return n, err
}

// CountAdmins devuelve cuántos admins quedan (para proteger al último).
func (s *Store) CountAdmins(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE role='admin'").Scan(&n)
	return n, err
}

// Bootstrap crea el primer admin si no hay usuarios. Usa ADMIN_PASSWORD o
// genera una aleatoria y la loguea UNA vez (decisión documentada en README).
func (s *Store) Bootstrap(ctx context.Context, adminPassword string) error {
	n, err := s.Count(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	generated := false
	if adminPassword == "" {
		adminPassword = randomPassword()
		generated = true
	}
	if err := s.Create(ctx, "admin", adminPassword, "admin"); err != nil {
		return fmt.Errorf("bootstrap admin: %w", err)
	}
	if generated {
		log.Printf("BOOTSTRAP: creado usuario 'admin' con contraseña generada: %s (cámbiala tras el primer login)", adminPassword)
	} else {
		log.Println("BOOTSTRAP: creado usuario 'admin' con la contraseña de ADMIN_PASSWORD")
	}
	return nil
}

// Create valida y crea un usuario.
func (s *Store) Create(ctx context.Context, name, password, role string) error {
	if !nameRe.MatchString(name) {
		return ErrInvalidName
	}
	if role != "admin" && role != "user" {
		return ErrInvalidRole
	}
	if len(password) < 8 {
		return ErrWeakPassword
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		"INSERT INTO users(user, pass_hash, role) VALUES (?,?,?)", name, hash, role)
	if err != nil {
		if isUniqueErr(err) {
			return ErrExists
		}
		return err
	}
	return nil
}

// Delete elimina un usuario (las sesiones caen por ON DELETE CASCADE).
func (s *Store) Delete(ctx context.Context, name string) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM users WHERE user=?", name)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// List devuelve todos los usuarios con su nº de sesiones activas.
func (s *Store) List(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT u.user, u.role, u.language, u.display_name, u.email, u.avatar, u.last_login,
		       (SELECT COUNT(*) FROM sessions se WHERE se.user=u.user AND se.expires_at > datetime('now'))
		FROM users u ORDER BY u.user`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		var u User
		var last sql.NullString
		if err := rows.Scan(&u.Name, &u.Role, &u.Language, &u.DisplayName, &u.Email, &u.Avatar, &last, &u.Sessions); err != nil {
			return nil, err
		}
		if last.Valid && last.String != "" {
			t := parseTS(last.String)
			u.LastLogin = &t
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// Get devuelve un usuario por nombre.
func (s *Store) Get(ctx context.Context, name string) (*User, error) {
	var u User
	var last sql.NullString
	err := s.db.QueryRowContext(ctx,
		"SELECT user, role, language, display_name, email, avatar, last_login FROM users WHERE user=?", name).
		Scan(&u.Name, &u.Role, &u.Language, &u.DisplayName, &u.Email, &u.Avatar, &last)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if last.Valid && last.String != "" {
		t := parseTS(last.String)
		u.LastLogin = &t
	}
	return &u, nil
}

// Verify comprueba credenciales y actualiza last_login si son válidas.
func (s *Store) Verify(ctx context.Context, name, password string) (role string, err error) {
	var hash string
	err = s.db.QueryRowContext(ctx,
		"SELECT pass_hash, role FROM users WHERE user=?", name).Scan(&hash, &role)
	if errors.Is(err, sql.ErrNoRows) {
		// Comparación dummy para no filtrar por timing si el usuario existe.
		verifyPassword(password, "$argon2id$v=19$m=65536,t=3,p=2$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
		return "", ErrBadCredential
	}
	if err != nil {
		return "", err
	}
	if !verifyPassword(password, hash) {
		return "", ErrBadCredential
	}
	_, _ = s.db.ExecContext(ctx, "UPDATE users SET last_login=? WHERE user=?",
		time.Now().UTC().Format(time.RFC3339), name)
	return role, nil
}

// SetPassword cambia la contraseña (admin sobre otro usuario o el propio).
func (s *Store) SetPassword(ctx context.Context, name, newPassword string) error {
	if len(newPassword) < 8 {
		return ErrWeakPassword
	}
	hash, err := hashPassword(newPassword)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx,
		"UPDATE users SET pass_hash=? WHERE user=?", hash, name)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// RoleOf devuelve el rol de un usuario (para el middleware de auth).
func (s *Store) RoleOf(ctx context.Context, name string) (string, error) {
	var role string
	err := s.db.QueryRowContext(ctx, "SELECT role FROM users WHERE user=?", name).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return role, err
}

// SetProfile actualiza el nombre visible y el email del usuario.
// displayName: libre, recortado a 64 chars; vacío = mostrar el username.
// email: opcional; si no está vacío debe parecer un email.
func (s *Store) SetProfile(ctx context.Context, name, displayName, email string) error {
	displayName = strings.TrimSpace(displayName)
	email = strings.TrimSpace(email)
	if len(displayName) > 64 {
		displayName = displayName[:64]
	}
	if email != "" && (len(email) > 254 || !emailRe.MatchString(email)) {
		return ErrInvalidEmail
	}
	res, err := s.db.ExecContext(ctx,
		"UPDATE users SET display_name=?, email=? WHERE user=?", displayName, email, name)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetAvatar fija el nombre del fichero de avatar del usuario (dentro de
// <datadir>/avatars/). Vacío = quitar la foto.
func (s *Store) SetAvatar(ctx context.Context, name, filename string) error {
	res, err := s.db.ExecContext(ctx,
		"UPDATE users SET avatar=? WHERE user=?", filename, name)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetLanguage fija el idioma del usuario ('auto'|'es'|'en').
func (s *Store) SetLanguage(ctx context.Context, name, lang string) error {
	if lang != "auto" && lang != "es" && lang != "en" {
		return ErrInvalidLang
	}
	res, err := s.db.ExecContext(ctx,
		"UPDATE users SET language=? WHERE user=?", lang, name)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// TOTPSecret devuelve el secreto TOTP actual del usuario ("" si no tiene).
func (s *Store) TOTPSecret(ctx context.Context, name string) (string, error) {
	var secret string
	err := s.db.QueryRowContext(ctx,
		"SELECT totp_secret FROM users WHERE user=?", name).Scan(&secret)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return secret, err
}

// SetTOTPSecret guarda un secreto TOTP (provisional durante el setup; el
// usuario queda sin activar hasta TOTPEnabled).
func (s *Store) SetTOTPSecret(ctx context.Context, name, secret string) error {
	res, err := s.db.ExecContext(ctx,
		"UPDATE users SET totp_secret=?, totp_enabled=0 WHERE user=?", secret, name)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// TOTPEnabled devuelve si el usuario tiene 2FA activo.
func (s *Store) TOTPEnabled(ctx context.Context, name string) (bool, error) {
	var enabled int
	err := s.db.QueryRowContext(ctx,
		"SELECT totp_enabled FROM users WHERE user=?", name).Scan(&enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	return enabled == 1, err
}

// TOTPActivate marca el 2FA como activo (tras confirmar el primer código).
func (s *Store) TOTPActivate(ctx context.Context, name string) error {
	res, err := s.db.ExecContext(ctx,
		"UPDATE users SET totp_enabled=1 WHERE user=?", name)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// TOTPDisable desactiva el 2FA y borra el secreto.
func (s *Store) TOTPDisable(ctx context.Context, name string) error {
	if _, err := s.db.ExecContext(ctx,
		"DELETE FROM totp_recovery WHERE user=?", name); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx,
		"UPDATE users SET totp_secret='', totp_enabled=0 WHERE user=?", name)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// AddRecoveryCode guarda un recovery code hasheado para un usuario.
func (s *Store) AddRecoveryCode(ctx context.Context, name, codeHash string) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT OR REPLACE INTO totp_recovery(user, code_hash) VALUES (?,?)", name, codeHash)
	return err
}

// ListRecoveryCodes devuelve los hashes de recovery codes (sin gastar) de un usuario.
func (s *Store) ListRecoveryCodes(ctx context.Context, name string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT code_hash FROM totp_recovery WHERE user=? AND used=0 ORDER BY code_hash", name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// UseRecoveryCode marca un recovery code como gastado. Devuelve true si existía.
func (s *Store) UseRecoveryCode(ctx context.Context, name, codeHash string) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		"UPDATE totp_recovery SET used=1 WHERE user=? AND code_hash=? AND used=0", name, codeHash)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ClearRecoveryCodes borra los recovery codes de un usuario.
func (s *Store) ClearRecoveryCodes(ctx context.Context, name string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM totp_recovery WHERE user=?", name)
	return err
}

// randomPassword genera una contraseña aleatoria legible (18 chars base64url).
func randomPassword() string {
	b := make([]byte, 14)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// isUniqueErr detecta violación de UNIQUE en modernc.org/sqlite sin depender del texto exacto.
func isUniqueErr(err error) bool {
	s := err.Error()
	return strings.Contains(s, "UNIQUE constraint failed") || strings.Contains(s, "constraint failed")
}

// parseTS tolera RFC3339 y 'YYYY-MM-DD HH:MM:SS' (defaults SQLite).
func parseTS(s string) time.Time {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02 15:04:05", s); err == nil {
		return t.UTC()
	}
	return time.Time{}
}
