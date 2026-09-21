// push_handlers_test.go — endpoints Web Push: validación de claves (400
// invalid_keys), origin guardado, preferencias y quiet hours del propio usuario.
package httpapi

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	webpush "github.com/SherClockHolmes/webpush-go"

	"easyzfs/internal/auth"
	"easyzfs/internal/config"
	"easyzfs/internal/db"
	"easyzfs/internal/push"
	"easyzfs/internal/users"
)

// serverPushPrueba — servidor real (mux completo) con BD migrada, usuario
// admin y sesión válida; devuelve también la BD para aserciones directas.
func serverPushPrueba(t *testing.T) (http.Handler, *http.Cookie, *sql.DB) {
	t.Helper()
	d, err := db.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.Migrate(context.Background(), d); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	us := users.NewStore(d)
	if err := us.Bootstrap(context.Background(), "adminpass-largo"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	priv, pub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatalf("vapid: %v", err)
	}
	cfg := &config.Config{
		VAPIDPublicKey:  pub,
		VAPIDPrivateKey: priv,
		VAPIDSubject:    "mailto:easyzfs@localhost",
	}
	am := auth.NewManager(d, []byte("secreto-de-prueba-32-bytes-xxxxxxxx"), false)
	srv := NewServer(Deps{
		Cfg: cfg, DB: d, Auth: am, Users: us, Push: push.New(cfg, d, nil),
	})
	cookie, err := am.CreateSession(context.Background(), "admin")
	if err != nil {
		t.Fatalf("sesión: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return srv.Handler(), cookie, d
}

func doReq(t *testing.T, h http.Handler, cookie *http.Cookie, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// b64url — n bytes aleatorios en base64url sin padding (como el navegador).
func b64url(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// (A6) Claves con formato inválido → 400 invalid_keys.
func TestSubscribeInvalidKeys(t *testing.T) {
	h, cookie, _ := serverPushPrueba(t)
	casos := []struct {
		nombre       string
		p256dh, auth string
	}{
		{"p256dh corta", b64url(t, 33), b64url(t, 16)},
		{"auth corta", b64url(t, 65), b64url(t, 12)},
		{"no base64", "!!!no-es-base64!!!", b64url(t, 16)},
		{"vacías", "", ""},
	}
	for _, c := range casos {
		body := `{"endpoint":"https://push.example.com/x","keys":{"p256dh":"` + c.p256dh + `","auth":"` + c.auth + `"}}`
		w := doReq(t, h, cookie, "POST", "/api/push/subscribe", body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, esperado 400", c.nombre, w.Code)
			continue
		}
		var resp map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("%s: body no JSON: %v", c.nombre, err)
		}
		if resp["error"] != "invalid_keys" {
			t.Errorf("%s: error = %q, esperado invalid_keys", c.nombre, resp["error"])
		}
	}
}

// (A5) Subscribe con claves válidas + origin → 204 y la columna origin guardada.
func TestSubscribeConOrigin(t *testing.T) {
	h, cookie, d := serverPushPrueba(t)
	body := `{"endpoint":"https://push.example.com/sub1","keys":{"p256dh":"` + b64url(t, 65) +
		`","auth":"` + b64url(t, 16) + `"},"lang":"en","origin":"https://zfs.example.com:8443"}`
	w := doReq(t, h, cookie, "POST", "/api/push/subscribe", body)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, esperado 204 (body: %s)", w.Code, w.Body.String())
	}
	var origin, lang string
	if err := d.QueryRow(
		"SELECT origin, lang FROM push_subscriptions WHERE endpoint='https://push.example.com/sub1'").
		Scan(&origin, &lang); err != nil {
		t.Fatalf("select: %v", err)
	}
	if origin != "https://zfs.example.com:8443" {
		t.Errorf("origin = %q, esperado https://zfs.example.com:8443", origin)
	}
	if lang != "en" {
		t.Errorf("lang = %q, esperado en", lang)
	}

	// Origin inválido (no http/https) → 400 invalid_origin.
	body = `{"endpoint":"https://push.example.com/sub2","keys":{"p256dh":"` + b64url(t, 65) +
		`","auth":"` + b64url(t, 16) + `"},"origin":"javascript:alert(1)"}`
	w = doReq(t, h, cookie, "POST", "/api/push/subscribe", body)
	if w.Code != http.StatusBadRequest {
		t.Errorf("origin javascript: status = %d, esperado 400", w.Code)
	}
}

// (B10) GET preferences devuelve los 5 tipos (default true); PUT hace upsert;
// tipo desconocido → 400 invalid_tipo.
func TestPreferencesEndpoints(t *testing.T) {
	h, cookie, _ := serverPushPrueba(t)

	w := doReq(t, h, cookie, "GET", "/api/push/preferences", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET preferences: status = %d", w.Code)
	}
	var resp struct {
		Preferences []struct {
			Tipo    string `json:"tipo"`
			Enabled bool   `json:"enabled"`
		} `json:"preferences"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("GET preferences: body no JSON: %v", err)
	}
	if len(resp.Preferences) != 6 {
		t.Fatalf("tipos = %d, esperado 6", len(resp.Preferences))
	}
	for _, p := range resp.Preferences {
		if !p.Enabled {
			t.Errorf("%s: enabled = false por defecto, esperado true", p.Tipo)
		}
	}

	// Desactivar disk_temp → persiste.
	w = doReq(t, h, cookie, "PUT", "/api/push/preferences", `{"tipo":"disk_temp","enabled":false}`)
	if w.Code != http.StatusNoContent {
		t.Fatalf("PUT preferences: status = %d", w.Code)
	}
	w = doReq(t, h, cookie, "GET", "/api/push/preferences", "")
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("GET preferences 2: %v", err)
	}
	for _, p := range resp.Preferences {
		if p.Tipo == "disk_temp" && p.Enabled {
			t.Error("disk_temp sigue enabled tras el PUT")
		}
	}

	// Tipo desconocido → 400 invalid_tipo.
	w = doReq(t, h, cookie, "PUT", "/api/push/preferences", `{"tipo":"no_existe","enabled":true}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("PUT tipo desconocido: status = %d, esperado 400", w.Code)
	}
	var errResp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &errResp); err == nil && errResp["error"] != "invalid_tipo" {
		t.Errorf("error = %q, esperado invalid_tipo", errResp["error"])
	}
}

// (B10) Quiet hours: GET por defecto (desactivado, nulls), validación 0-23 y
// start≠end, y upsert correcto.
func TestQuietHoursEndpoints(t *testing.T) {
	h, cookie, _ := serverPushPrueba(t)

	w := doReq(t, h, cookie, "GET", "/api/push/quiet-hours", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET quiet-hours: status = %d", w.Code)
	}
	var resp struct {
		Enabled bool   `json:"enabled"`
		Start   *int   `json:"start"`
		End     *int   `json:"end"`
		TZ      string `json:"tz"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("GET quiet-hours: body no JSON: %v", err)
	}
	if resp.Enabled || resp.Start != nil || resp.End != nil || resp.TZ != "Europe/Madrid" {
		t.Errorf("quiet-hours por defecto = %+v, esperado desactivado con nulls y tz Europe/Madrid", resp)
	}

	// Validaciones: fuera de rango e iguales → 400 invalid_hours.
	for _, body := range []string{
		`{"enabled":true,"start":24,"end":8}`,
		`{"enabled":true,"start":-1,"end":8}`,
		`{"enabled":true,"start":22,"end":22}`,
	} {
		w = doReq(t, h, cookie, "PUT", "/api/push/quiet-hours", body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("PUT %s: status = %d, esperado 400", body, w.Code)
		}
	}

	// Ventana que cruza medianoche: OK.
	w = doReq(t, h, cookie, "PUT", "/api/push/quiet-hours", `{"enabled":true,"start":22,"end":8}`)
	if w.Code != http.StatusNoContent {
		t.Fatalf("PUT quiet-hours válido: status = %d", w.Code)
	}
	w = doReq(t, h, cookie, "GET", "/api/push/quiet-hours", "")
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("GET quiet-hours 2: %v", err)
	}
	if !resp.Enabled || resp.Start == nil || *resp.Start != 22 || resp.End == nil || *resp.End != 8 {
		t.Errorf("quiet-hours tras PUT = %+v, esperado enabled 22→8", resp)
	}

	// Desactivar → start/end vuelven a null.
	w = doReq(t, h, cookie, "PUT", "/api/push/quiet-hours", `{"enabled":false,"start":22,"end":8}`)
	if w.Code != http.StatusNoContent {
		t.Fatalf("PUT quiet-hours off: status = %d", w.Code)
	}
	w = doReq(t, h, cookie, "GET", "/api/push/quiet-hours", "")
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("GET quiet-hours 3: %v", err)
	}
	if resp.Enabled || resp.Start != nil || resp.End != nil {
		t.Errorf("quiet-hours tras desactivar = %+v, esperado nulls", resp)
	}
}
