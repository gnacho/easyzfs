package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"easyzfs/internal/apikeys"
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

func TestSignPendingRoundTrip(t *testing.T) {
	m := nuevoManager(t)
	pending, err := m.SignPending("admin")
	if err != nil {
		t.Fatal(err)
	}
	user, ok := m.VerifyPending(pending)
	if !ok || user != "admin" {
		t.Fatalf("pending válido rechazado: user=%q ok=%v", user, ok)
	}
}

func TestVerifyPendingRejectsTampered(t *testing.T) {
	m := nuevoManager(t)
	pending, err := m.SignPending("admin")
	if err != nil {
		t.Fatal(err)
	}
	// Cambia un carácter del payload: la firma no cuadra.
	bad := "usuario" + pending[len("admin"):]
	if _, ok := m.VerifyPending(bad); ok {
		t.Error("pending manipulado aceptado")
	}
}

func TestVerifyPendingRejectsExpired(t *testing.T) {
	m := nuevoManager(t)
	pending, err := m.SignPending("admin")
	if err != nil {
		t.Fatal(err)
	}
	// Verificar con el tiempo congelado tras la expiración: recomponer el
	// token con una expiración pasada usando la misma firma.
	_, rest, _ := strings.Cut(pending, "|")
	_, sig, _ := strings.Cut(rest, "|")
	expired := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	bad := "admin|" + expired + "|" + sig
	if _, ok := m.VerifyPending(bad); ok {
		t.Error("pending caducado aceptado")
	}
}

func TestVerifyPendingRejectsBogus(t *testing.T) {
	m := nuevoManager(t)
	if _, ok := m.VerifyPending(""); ok {
		t.Error("pending vacío aceptado")
	}
	if _, ok := m.VerifyPending("solo-un-campo"); ok {
		t.Error("pending malformado aceptado")
	}
}

// --- API keys de solo lectura (#87) ---

// Con una API key válida: GET pasa con rol user; POST/PUT dan 403.
func TestMiddlewareAPIKeyReadOnly(t *testing.T) {
	m := nuevoManager(t)
	ks := apikeys.NewStore(m.db)
	key, err := ks.Create(context.Background(), "monitoring")
	if err != nil {
		t.Fatal(err)
	}
	m.SetAPIKeys(ks)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, role := UserFromContext(r.Context()), RoleFromContext(r.Context())
		w.Header().Set("X-User", user)
		w.Header().Set("X-Role", role)
		w.WriteHeader(http.StatusOK)
	})
	h := m.Middleware(inner)

	// GET con Bearer → 200, user apikey:monitoring, role user.
	req := httptest.NewRequest(http.MethodGet, "/api/pools", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET con key: %d, esperado 200", rec.Code)
	}
	if rec.Header().Get("X-User") != "apikey:monitoring" || rec.Header().Get("X-Role") != "user" {
		t.Fatalf("ctx inyectado: user=%q role=%q", rec.Header().Get("X-User"), rec.Header().Get("X-Role"))
	}

	// POST con Bearer → 403 (solo lectura).
	req = httptest.NewRequest(http.MethodPost, "/api/pools", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+key)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("POST con key: %d, esperado 403", rec.Code)
	}

	// Sin credenciales → 401.
	req = httptest.NewRequest(http.MethodGet, "/api/pools", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("sin credenciales: %d, esperado 401", rec.Code)
	}
}
