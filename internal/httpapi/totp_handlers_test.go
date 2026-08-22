// totp_handlers_test.go — flujo 2FA end-to-end: login en dos pasos, setup,
// confirmación con QR, recovery codes y desactivación (con servidor real).
package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"easyzfs/internal/auth"
	"easyzfs/internal/config"
	"easyzfs/internal/db"
	"easyzfs/internal/users"
)

// setup2FAServer levanta un servidor completo (Handler con middleware) y crea
// el usuario admin con contraseña 'password123'. Devuelve el handler.
func setup2FAServer(t *testing.T) http.Handler {
	t.Helper()
	d, err := db.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	if err := db.Migrate(context.Background(), d); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	us := users.NewStore(d)
	if err := us.Create(context.Background(), "admin", "password123", "admin"); err != nil {
		t.Fatalf("create: %v", err)
	}
	am := auth.NewManager(d, secret, false)
	srv := NewServer(Deps{
		Cfg:   &config.Config{Mock: true},
		DB:    d,
		Auth:  am,
		Users: us,
	})
	return srv.Handler()
}

// doReq hace una petición autenticada (con cookie) al handler del servidor.
func do2FAReq(t *testing.T, h http.Handler, cookie *http.Cookie, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	r.RemoteAddr = "127.0.0.1:1234"
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// loginReal hace POST /api/login y devuelve la respuesta + cookie si la hay.
func loginReal(t *testing.T, h http.Handler, body string) (*httptest.ResponseRecorder, *http.Cookie) {
	t.Helper()
	rec := do2FAReq(t, h, nil, "POST", "/api/login", body)
	var c *http.Cookie
	if cs := rec.Result().Cookies(); len(cs) > 0 {
		c = cs[0]
	}
	return rec, c
}

func loginOK(t *testing.T, h http.Handler) *http.Cookie {
	t.Helper()
	rec, cookie := loginReal(t, h, `{"user":"admin","password":"password123"}`)
	if rec.Code != 200 {
		t.Fatalf("login status %d (%s)", rec.Code, rec.Body.String())
	}
	return cookie
}

// enable2FA activa 2FA para el usuario admin usando una cookie de sesión
// previa (la sesión sigue siendo válida tras activarlo). Devuelve el secreto.
func enable2FA(t *testing.T, h http.Handler, cookie *http.Cookie) string {
	t.Helper()
	rec := do2FAReq(t, h, cookie, "POST", "/api/me/2fa/setup", `{}`)
	if rec.Code != 200 {
		t.Fatalf("setup status %d (%s)", rec.Code, rec.Body.String())
	}
	var setup map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &setup); err != nil {
		t.Fatalf("setup body: %v", err)
	}
	secret := setup["secret"]
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	rec = do2FAReq(t, h, cookie, "POST", "/api/me/2fa/confirm", `{"code":"`+code+`"}`)
	if rec.Code != 200 {
		t.Fatalf("confirm status %d (%s)", rec.Code, rec.Body.String())
	}
	return secret
}

// --- Tests ---

func TestLoginSin2FACreaSesion(t *testing.T) {
	h := setup2FAServer(t)
	rec, cookie := loginReal(t, h, `{"user":"admin","password":"password123"}`)
	if rec.Code != 200 {
		t.Fatalf("status %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"twofa_required":true`) {
		t.Fatal("2FA no estaba activo pero se pidió segundo factor")
	}
	if cookie == nil {
		t.Fatal("no se fijó cookie de sesión")
	}
}

func TestLogin2FARequiereSegundoFactor(t *testing.T) {
	h := setup2FAServer(t)
	cookie := loginOK(t, h)
	enable2FA(t, h, cookie)
	rec, cookie2 := loginReal(t, h, `{"user":"admin","password":"password123"}`)
	if rec.Code != 200 {
		t.Fatalf("status %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m["twofa_required"] != true {
		t.Fatal("con 2FA activo se esperaba twofa_required=true")
	}
	if m["pending"] == "" {
		t.Fatal("falta el token pending")
	}
	if cookie2 != nil {
		t.Fatal("no debería fijarse cookie hasta el segundo factor")
	}
}

func TestLogin2FASegundoFactorValido(t *testing.T) {
	h := setup2FAServer(t)
	cookie := loginOK(t, h)
	secret := enable2FA(t, h, cookie)
	// Paso 1.
	rec, _ := loginReal(t, h, `{"user":"admin","password":"password123"}`)
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	pending := m["pending"].(string)
	// Paso 2 con código válido.
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	rec2 := do2FAReq(t, h, nil, "POST", "/api/login/2fa", `{"pending":"`+pending+`","code":"`+code+`"}`)
	if rec2.Code != 200 {
		t.Fatalf("status %d, want 200 (%s)", rec2.Code, rec2.Body.String())
	}
	if len(rec2.Result().Cookies()) == 0 {
		t.Fatal("el segundo factor válido debería fijar la cookie de sesión")
	}
}

func TestLogin2FASegundoFactorInvalido(t *testing.T) {
	h := setup2FAServer(t)
	cookie := loginOK(t, h)
	enable2FA(t, h, cookie)
	rec, _ := loginReal(t, h, `{"user":"admin","password":"password123"}`)
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	pending := m["pending"].(string)
	rec2 := do2FAReq(t, h, nil, "POST", "/api/login/2fa", `{"pending":"`+pending+`","code":"000000"}`)
	if rec2.Code != 401 {
		t.Fatalf("status %d, want 401", rec2.Code)
	}
}

func TestMy2FASetupYConfirm(t *testing.T) {
	h := setup2FAServer(t)
	cookie := loginOK(t, h)
	rec := do2FAReq(t, h, cookie, "POST", "/api/me/2fa/setup", `{}`)
	if rec.Code != 200 {
		t.Fatalf("setup status %d (%s)", rec.Code, rec.Body.String())
	}
	var m map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	secret := m["secret"]
	if secret == "" {
		t.Fatal("setup sin secreto")
	}
	if !strings.HasPrefix(m["otpauth"], "otpauth://totp/") {
		t.Fatalf("otpauth inesperado %q", m["otpauth"])
	}
	if !strings.HasPrefix(m["qr"], "data:image/png;base64,") {
		t.Fatal("el QR no es data:image/png")
	}
	// confirm con código válido
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	rec = do2FAReq(t, h, cookie, "POST", "/api/me/2fa/confirm", `{"code":"`+code+`"}`)
	if rec.Code != 200 {
		t.Fatalf("confirm status %d (%s)", rec.Code, rec.Body.String())
	}
	var m2 map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m2); err != nil {
		t.Fatal(err)
	}
	codes, _ := m2["codes"].([]any)
	if len(codes) != 10 {
		t.Fatalf("se esperaban 10 recovery codes, got %d", len(codes))
	}
	// El login ahora exige segundo factor.
	recLogin, cookieLogin := loginReal(t, h, `{"user":"admin","password":"password123"}`)
	var m3 map[string]any
	if err := json.Unmarshal(recLogin.Body.Bytes(), &m3); err != nil {
		t.Fatal(err)
	}
	if m3["twofa_required"] != true {
		t.Fatal("2FA no quedó activo tras confirm")
	}
	if cookieLogin != nil {
		t.Fatal("con 2FA activo no debe haber cookie de sesión en el paso 1")
	}
}

func TestMy2FAConfirmCodeInvalido(t *testing.T) {
	h := setup2FAServer(t)
	cookie := loginOK(t, h)
	rec := do2FAReq(t, h, cookie, "POST", "/api/me/2fa/setup", `{}`)
	if rec.Code != 200 {
		t.Fatalf("setup status %d (%s)", rec.Code, rec.Body.String())
	}
	rec = do2FAReq(t, h, cookie, "POST", "/api/me/2fa/confirm", `{"code":"000000"}`)
	if rec.Code != 401 {
		t.Fatalf("status %d, want 401", rec.Code)
	}
	// 2FA no debe haberse activado: login sigue sin segundo factor.
	recLogin, cookieLogin := loginReal(t, h, `{"user":"admin","password":"password123"}`)
	if strings.Contains(recLogin.Body.String(), `"twofa_required":true`) {
		t.Fatal("2FA no debería activarse con código inválido")
	}
	if cookieLogin == nil {
		t.Fatal("el login sin 2FA debería dar cookie")
	}
}

func TestMy2FADisableConCodigo(t *testing.T) {
	h := setup2FAServer(t)
	cookie := loginOK(t, h)
	secret := enable2FA(t, h, cookie)
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	rec := do2FAReq(t, h, cookie, "POST", "/api/me/2fa/disable", `{"code":"`+code+`"}`)
	if rec.Code != 204 {
		t.Fatalf("status %d, want 204 (%s)", rec.Code, rec.Body.String())
	}
	recLogin, cookieLogin := loginReal(t, h, `{"user":"admin","password":"password123"}`)
	if strings.Contains(recLogin.Body.String(), `"twofa_required":true`) {
		t.Fatal("2FA debería quedar desactivado")
	}
	if cookieLogin == nil {
		t.Fatal("login tras desactivar debería dar cookie")
	}
}

func TestMy2FADisableSinCodigoFallido(t *testing.T) {
	h := setup2FAServer(t)
	cookie := loginOK(t, h)
	enable2FA(t, h, cookie)
	rec := do2FAReq(t, h, cookie, "POST", "/api/me/2fa/disable", `{"code":"000000"}`)
	if rec.Code != 401 {
		t.Fatalf("status %d, want 401", rec.Code)
	}
	// Sigue activo.
	recLogin, _ := loginReal(t, h, `{"user":"admin","password":"password123"}`)
	if !strings.Contains(recLogin.Body.String(), `"twofa_required":true`) {
		t.Fatal("2FA no debería desactivarse con código inválido")
	}
}

func TestLoginConRecoveryCode(t *testing.T) {
	h := setup2FAServer(t)
	cookie := loginOK(t, h)
	enable2FA(t, h, cookie)
	// Generar recovery codes.
	rec := do2FAReq(t, h, cookie, "GET", "/api/me/2fa/recovery", "")
	if rec.Code != 200 {
		t.Fatalf("recovery status %d (%s)", rec.Code, rec.Body.String())
	}
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	codes, _ := m["codes"].([]any)
	if len(codes) != 10 {
		t.Fatalf("se esperaban 10 codes, got %d", len(codes))
	}
	rc := codes[0].(string)
	// Login con el recovery code.
	recLogin, _ := loginReal(t, h, `{"user":"admin","password":"password123"}`)
	var m2 map[string]any
	if err := json.Unmarshal(recLogin.Body.Bytes(), &m2); err != nil {
		t.Fatal(err)
	}
	pending := m2["pending"].(string)
	rec2 := do2FAReq(t, h, nil, "POST", "/api/login/2fa", `{"pending":"`+pending+`","code":"`+rc+`"}`)
	if rec2.Code != 200 {
		t.Fatalf("status %d, want 200 (%s)", rec2.Code, rec2.Body.String())
	}
	// El mismo code ya está gastado.
	recLogin2, _ := loginReal(t, h, `{"user":"admin","password":"password123"}`)
	var m3 map[string]any
	if err := json.Unmarshal(recLogin2.Body.Bytes(), &m3); err != nil {
		t.Fatal(err)
	}
	pending2 := m3["pending"].(string)
	rec3 := do2FAReq(t, h, nil, "POST", "/api/login/2fa", `{"pending":"`+pending2+`","code":"`+rc+`"}`)
	if rec3.Code != 401 {
		t.Fatalf("segundo uso del recovery code: status %d, want 401", rec3.Code)
	}
}

func TestAdminReset2FA(t *testing.T) {
	h := setup2FAServer(t)
	cookie := loginOK(t, h)
	enable2FA(t, h, cookie)
	rec := do2FAReq(t, h, cookie, "DELETE", "/api/users/admin/2fa", "")
	if rec.Code != 204 {
		t.Fatalf("status %d, want 204 (%s)", rec.Code, rec.Body.String())
	}
	recLogin, cookieLogin := loginReal(t, h, `{"user":"admin","password":"password123"}`)
	if strings.Contains(recLogin.Body.String(), `"twofa_required":true`) {
		t.Fatal("el reset admin debería desactivar el 2FA")
	}
	if cookieLogin == nil {
		t.Fatal("login tras reset debería dar cookie")
	}
}
