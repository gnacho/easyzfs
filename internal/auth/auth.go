// Package auth — sesiones con cookie HttpOnly token|HMAC-SHA256 en tabla sessions.
// Middleware de autenticación y de rol (admin requerido en usuarios/ajustes/destructivas).
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"
)

// CookieName — nombre de la cookie de sesión (contrato).
const CookieName = "easyzfs_session"

// sessionTTL — duración fija de la sesión.
const sessionTTL = 7 * 24 * time.Hour

// contextKey para el usuario/rol en el request.
type contextKey string

const (
	ctxUser contextKey = "easyzfs.user"
	ctxRole contextKey = "easyzfs.role"
)

// Manager gestiona sesiones y middleware.
type Manager struct {
	db     *sql.DB
	secret []byte
	secure bool
}

// NewManager crea el gestor de sesiones.
func NewManager(d *sql.DB, secret []byte, secureCookies bool) *Manager {
	return &Manager{db: d, secret: secret, secure: secureCookies}
}

// CreateSession crea una sesión para el usuario y devuelve la cookie lista.
func (m *Manager) CreateSession(ctx context.Context, user string) (*http.Cookie, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(raw)
	expires := time.Now().Add(sessionTTL).UTC()
	_, err := m.db.ExecContext(ctx,
		"INSERT INTO sessions(token, user, expires_at) VALUES (?,?,?)",
		token, user, expires.Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	return &http.Cookie{
		Name:     CookieName,
		Value:    token + "|" + m.sign(token),
		Path:     "/",
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  expires,
		MaxAge:   int(sessionTTL.Seconds()),
	}, nil
}

// Validate comprueba firma HMAC y expiración; devuelve usuario y rol.
func (m *Manager) Validate(ctx context.Context, cookieValue string) (user, role string, ok bool) {
	token, sig, found := strings.Cut(cookieValue, "|")
	if !found || token == "" || sig == "" {
		return "", "", false
	}
	expected := m.sign(token)
	if subtle.ConstantTimeCompare([]byte(sig), []byte(expected)) != 1 {
		return "", "", false
	}
	var expires string
	err := m.db.QueryRowContext(ctx, `
		SELECT s.user, u.role, s.expires_at
		FROM sessions s JOIN users u ON u.user = s.user
		WHERE s.token = ?`, token).Scan(&user, &role, &expires)
	if err != nil {
		return "", "", false
	}
	exp, err := time.Parse(time.RFC3339, expires)
	if err != nil || time.Now().After(exp) {
		return "", "", false
	}
	return user, role, true
}

// DestroySession invalida la sesión de la cookie dada.
func (m *Manager) DestroySession(ctx context.Context, cookieValue string) {
	token, _, found := strings.Cut(cookieValue, "|")
	if !found {
		return
	}
	_, _ = m.db.ExecContext(ctx, "DELETE FROM sessions WHERE token=?", token)
}

// DestroyUserSessions cierra las sesiones de un usuario (exceptToken puede ser "").
func (m *Manager) DestroyUserSessions(ctx context.Context, user, exceptToken string) error {
	if exceptToken == "" {
		_, err := m.db.ExecContext(ctx, "DELETE FROM sessions WHERE user=?", user)
		return err
	}
	_, err := m.db.ExecContext(ctx,
		"DELETE FROM sessions WHERE user=? AND token<>?", user, exceptToken)
	return err
}

// PurgeExpired borra sesiones caducadas (colector de mantenimiento).
func (m *Manager) PurgeExpired(ctx context.Context) error {
	_, err := m.db.ExecContext(ctx,
		"DELETE FROM sessions WHERE expires_at < ?", time.Now().UTC().Format(time.RFC3339))
	return err
}

// sign calcula HMAC-SHA256(secret, token) en hex.
func (m *Manager) sign(token string) string {
	mac := hmac.New(sha256.New, m.secret)
	mac.Write([]byte(token))
	return hex.EncodeToString(mac.Sum(nil))
}

// pendingTTL — ventana del segundo paso de login 2FA.
const pendingTTL = 5 * time.Minute

// SignPending emite un token firmado que prueba que la contraseña ya se
// verificó (paso 1 del login 2FA). Formato: <user>|<expiryRFC3339>|<sig>.
// Se re-firma con la misma clave HMAC de sesiones; la expiración va dentro.
func (m *Manager) SignPending(user string) (string, error) {
	expires := time.Now().Add(pendingTTL).UTC().Format(time.RFC3339)
	payload := user + "|" + expires
	return payload + "|" + m.sign("pending:"+payload), nil
}

// VerifyPending valida un token firmado y devuelve el usuario. Rechaza tokens
// caducados o con firma inválida (no filtra si el usuario existe).
func (m *Manager) VerifyPending(token string) (string, bool) {
	user, rest, ok := strings.Cut(token, "|")
	if !ok {
		return "", false
	}
	expires, sig, ok := strings.Cut(rest, "|")
	if !ok {
		return "", false
	}
	expected := m.sign("pending:" + user + "|" + expires)
	if subtle.ConstantTimeCompare([]byte(sig), []byte(expected)) != 1 {
		return "", false
	}
	exp, err := time.Parse(time.RFC3339, expires)
	if err != nil || time.Now().After(exp) {
		return "", false
	}
	return user, true
}

// Middleware exige sesión válida en todo lo que envuelve; inyecta user+role en ctx.
// Las rutas públicas (login) se registran FUERA de este middleware.
func (m *Manager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(CookieName)
		if err != nil {
			writeAuthErr(w, http.StatusUnauthorized, "unauthorized", "sesión requerida")
			return
		}
		user, role, ok := m.Validate(r.Context(), c.Value)
		if !ok {
			writeAuthErr(w, http.StatusUnauthorized, "unauthorized", "sesión inválida o expirada")
			return
		}
		ctx := context.WithValue(r.Context(), ctxUser, user)
		ctx = context.WithValue(ctx, ctxRole, role)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireAdmin envuelve un handler exigiendo rol admin.
func (m *Manager) RequireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if RoleFromContext(r.Context()) != "admin" {
			writeAuthErr(w, http.StatusForbidden, "forbidden", "se requiere rol admin")
			return
		}
		next(w, r)
	}
}

// UserFromContext devuelve el usuario autenticado ("" si no hay).
func UserFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ctxUser).(string); ok {
		return v
	}
	return ""
}

// RoleFromContext devuelve el rol autenticado ("" si no hay).
func RoleFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ctxRole).(string); ok {
		return v
	}
	return ""
}

// ExpiredCookie devuelve una cookie que borra la sesión en el navegador.
func (m *Manager) ExpiredCookie() *http.Cookie {
	return &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	}
}

// writeAuthErr — mismo formato de error que el resto del API (duplicado aquí
// para no crear dependencia auth → httpapi).
func writeAuthErr(w http.ResponseWriter, code int, errCode, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(`{"error":"` + errCode + `","message":"` + msg + `"}`))
}

// ErrNoSession — error interno para handlers que esperan usuario en ctx.
var ErrNoSession = errors.New("no hay sesión en el contexto")
