package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"testing"
	"time"

	"easyzfs/internal/db"
)

func nuevoManager(t *testing.T) *Manager {
	t.Helper()
	d, err := db.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if err := db.Migrate(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	_, _ = d.Exec(`INSERT OR IGNORE INTO users(user, role, pass_hash) VALUES ('admin','admin','x')`)
	_, _ = d.Exec(`INSERT OR IGNORE INTO users(user, role, pass_hash) VALUES ('user1','user','x')`)
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	return &Manager{db: d, secret: secret}
}

func parseCookieToken(v string) (token string) {
	token, _, _ = strings.Cut(v, "|")
	return token
}

func TestCreateAndValidateSession(t *testing.T) {
	m := nuevoManager(t)
	cookie, err := m.CreateSession(context.Background(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	if !cookie.HttpOnly {
		t.Error("cookie debería ser HttpOnly")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite esperado Lax (%d), got %d", http.SameSiteLaxMode, cookie.SameSite)
	}

	user, role, ok := m.Validate(context.Background(), cookie.Value)
	if !ok {
		t.Fatal("sesión válida rechazada")
	}
	if user != "admin" || role != "admin" {
		t.Fatalf("esperado admin/admin, got %s/%s", user, role)
	}

	// El viewer también recupera su rol
	c2, _ := m.CreateSession(context.Background(), "user1")
	_, role2, ok2 := m.Validate(context.Background(), c2.Value)
	if !ok2 || role2 != "user" {
		t.Fatalf("esperado user, got %s ok=%v", role2, ok2)
	}
}

func TestValidateRejectsTamperedSignature(t *testing.T) {
	m := nuevoManager(t)
	cookie, _ := m.CreateSession(context.Background(), "admin")
	tampered := cookie.Value[:len(cookie.Value)-4] + "xxxx"
	_, _, ok := m.Validate(context.Background(), tampered)
	if ok {
		t.Error("firma alterada debería rechazarse")
	}
}

func TestValidateRejectsExpiredSession(t *testing.T) {
	m := nuevoManager(t)
	cookie, err := m.CreateSession(context.Background(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	token := parseCookieToken(cookie.Value)
	_, _ = m.db.Exec("UPDATE sessions SET expires_at=? WHERE token=?",
		time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), token)
	_, _, ok := m.Validate(context.Background(), cookie.Value)
	if ok {
		t.Error("sesión expirada debería rechazarse")
	}
}

func TestValidateRejectsBogusCookie(t *testing.T) {
	m := nuevoManager(t)
	for _, v := range []string{"", "sinpipe", "|", "token|mal"} {
		_, _, ok := m.Validate(context.Background(), v)
		if ok {
			t.Errorf("cookie %q debería rechazarse", v)
		}
	}
}

func TestRoleFromContext(t *testing.T) {
	if RoleFromContext(context.Background()) != "" {
		t.Error("contexto vacío debería devolver ''")
	}
	ctx := context.WithValue(context.Background(), ctxRole, "admin")
	if RoleFromContext(ctx) != "admin" {
		t.Error("context con role debería devolver 'admin'")
	}
}

func TestDestroySession(t *testing.T) {
	m := nuevoManager(t)
	cookie, _ := m.CreateSession(context.Background(), "admin")
	m.DestroySession(context.Background(), cookie.Value)
	_, _, ok := m.Validate(context.Background(), cookie.Value)
	if ok {
		t.Error("sesión destruida debería rechazarse")
	}
}

func TestDestroyUserSessions(t *testing.T) {
	m := nuevoManager(t)
	c1, _ := m.CreateSession(context.Background(), "admin")
	c2, _ := m.CreateSession(context.Background(), "admin")
	t2 := parseCookieToken(c2.Value)

	if err := m.DestroyUserSessions(context.Background(), "admin", t2); err != nil {
		t.Fatal(err)
	}
	_, _, ok := m.Validate(context.Background(), c1.Value)
	if ok {
		t.Error("c1 debería estar destruida")
	}
	_, _, ok = m.Validate(context.Background(), c2.Value)
	if !ok {
		t.Error("c2 debería seguir viva")
	}
}

func TestExpiredCookie(t *testing.T) {
	m := nuevoManager(t)
	c := m.ExpiredCookie()
	if c.MaxAge != -1 {
		t.Errorf("ExpiredCookie MaxAge esperado -1, got %d", c.MaxAge)
	}
}

func TestPurgeExpired(t *testing.T) {
	m := nuevoManager(t)
	c1, _ := m.CreateSession(context.Background(), "admin")
	token := parseCookieToken(c1.Value)
	_, _ = m.db.Exec("UPDATE sessions SET expires_at=? WHERE token=?",
		time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), token)
	if err := m.PurgeExpired(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, _, ok := m.Validate(context.Background(), c1.Value)
	if ok {
		t.Error("sesión expirada debería haberse purgado")
	}
}

func TestSignDeterministic(t *testing.T) {
	m := nuevoManager(t)
	s1 := m.sign("abc")
	s2 := m.sign("abc")
	if s1 != s2 {
		t.Error("sign debería ser determinista")
	}
	if s1 == m.sign("abcd") {
		t.Error("tokens distintos deberían tener firmas distintas")
	}
	if len(s1) != 64 {
		t.Errorf("firma esperada 64 hex chars, got %d", len(s1))
	}
	if _, err := hex.DecodeString(s1); err != nil {
		t.Errorf("firma no es hex válido: %v", err)
	}
}

func TestTokenRandomness(t *testing.T) {
	m := nuevoManager(t)
	c1, _ := m.CreateSession(context.Background(), "admin")
	c2, _ := m.CreateSession(context.Background(), "user1")
	t1 := parseCookieToken(c1.Value)
	t2 := parseCookieToken(c2.Value)
	if t1 == t2 {
		t.Error("tokens deberían ser únicos")
	}
	if len(t1) != 64 {
		t.Errorf("token esperado 64 hex chars, got %d", len(t1))
	}
}
