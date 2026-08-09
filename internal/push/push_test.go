// push_test.go — ciclo de vida del sender con un push service falso (httptest):
// envío cifrado, borrado en 410, modo demo sin envío, i18n por dispositivo y
// regla no-duplicar SSE/push.
package push

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"

	"easyzfs/internal/config"
	"easyzfs/internal/db"
)

// clavesSub — par de claves de suscripción válidas (p256dh = punto P256,
// auth = 16 bytes), como las que genera el navegador.
func clavesSub(t *testing.T) (p256dh, auth string) {
	t.Helper()
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ecdh: %v", err)
	}
	secret := make([]byte, 16)
	if _, err := rand.Read(secret); err != nil {
		t.Fatalf("auth secret: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(priv.PublicKey().Bytes()),
		base64.RawURLEncoding.EncodeToString(secret)
}

// hubFalso — sin usuarios activos por defecto (app cerrada).
type hubFalso struct{ activos map[string]bool }

func (h hubFalso) UserActive(userID string) bool { return h.activos[userID] }

// nuevaBD — SQLite en memoria con el esquema completo migrado + un usuario.
func nuevaBD(t *testing.T) *sql.DB {
	t.Helper()
	d, err := db.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.Migrate(context.Background(), d); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := d.Exec("INSERT INTO users(user, pass_hash, role) VALUES ('admin','x','admin')"); err != nil {
		t.Fatalf("usuario: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// cfgConClaves — config con un par VAPID generado en el test.
func cfgConClaves(t *testing.T) *config.Config {
	t.Helper()
	priv, pub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatalf("vapid: %v", err)
	}
	return &config.Config{
		VAPIDPublicKey:  pub,
		VAPIDPrivateKey: priv,
		VAPIDSubject:    "mailto:easyzfs@localhost",
	}
}

func alertaPrueba() Alert {
	return Alert{
		Level: "warn", Source: "pool.tank", Target: "pools:tank",
		Kind:   "pool_capacity",
		Params: map[string]any{"pool": "tank", "pct": 85, "threshold": 80},
	}
}

// (a) Suscripción insertada → Notify envía un POST cifrado al push service.
func TestNotifyEnviaPOSTCifrado(t *testing.T) {
	var recibidas atomic.Int32
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recibidas.Add(1)
		body, _ = io.ReadAll(r.Body)
		if r.Header.Get("Authorization") == "" {
			t.Error("falta cabecera Authorization (VAPID)")
		}
		if r.Header.Get("Content-Encoding") != "aes128gcm" {
			t.Errorf("Content-Encoding = %q, esperado aes128gcm", r.Header.Get("Content-Encoding"))
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	d := nuevaBD(t)
	s := New(cfgConClaves(t), d, hubFalso{})
	p256dh, auth := clavesSub(t)
	if err := s.Subscribe(context.Background(), "admin", srv.URL+"/push/abc", p256dh, auth, "es", "", "test"); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	s.Notify(context.Background(), alertaPrueba())

	if recibidas.Load() != 1 {
		t.Fatalf("peticiones al push service = %d, esperado 1", recibidas.Load())
	}
	if len(body) == 0 {
		t.Fatal("el POST llegó sin cuerpo cifrado")
	}
	// El payload viaja cifrado: el texto plano NO debe aparecer en el cuerpo.
	if string(body) != "" && (contains(string(body), "tank") || contains(string(body), "Capacidad")) {
		t.Error("el cuerpo contiene texto plano: debería ir cifrado (aes128gcm)")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// (b) El push service devuelve 410 → la suscripción se borra en el mismo envío
// y SIN reintentos (una sola petición).
func TestNotify410BorraSuscripcion(t *testing.T) {
	var llamadas atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		llamadas.Add(1)
		w.WriteHeader(http.StatusGone)
	}))
	defer srv.Close()

	d := nuevaBD(t)
	s := New(cfgConClaves(t), d, hubFalso{})
	s.sleep = func(context.Context, time.Duration) {} // sin espera real en tests
	p256dh, auth := clavesSub(t)
	if err := s.Subscribe(context.Background(), "admin", srv.URL+"/push/muerta", p256dh, auth, "es", "", "test"); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	s.Notify(context.Background(), alertaPrueba())

	var n int
	if err := d.QueryRow("SELECT COUNT(*) FROM push_subscriptions").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("suscripciones tras 410 = %d, esperado 0 (borrada)", n)
	}
	if llamadas.Load() != 1 {
		t.Fatalf("peticiones tras 410 = %d, esperado 1 (sin reintento)", llamadas.Load())
	}
}

// (A3) 500 → reintento con backoff y éxito al 2.º intento; Retry-After se
// respeta como espera mínima; se conserva la suscripción.
func TestReintento500ExitoAlSegundoIntento(t *testing.T) {
	var llamadas atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if llamadas.Add(1) == 1 {
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	d := nuevaBD(t)
	s := New(cfgConClaves(t), d, hubFalso{})
	var esperas []time.Duration
	s.sleep = func(_ context.Context, d time.Duration) { esperas = append(esperas, d) }
	p256dh, auth := clavesSub(t)
	if err := s.Subscribe(context.Background(), "admin", srv.URL+"/push/retry", p256dh, auth, "es", "", "test"); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	s.Notify(context.Background(), alertaPrueba())

	if llamadas.Load() != 2 {
		t.Fatalf("peticiones = %d, esperado 2 (fallo + reintento con éxito)", llamadas.Load())
	}
	if len(esperas) != 1 {
		t.Fatalf("esperas entre intentos = %d, esperado 1", len(esperas))
	}
	// Retry-After: 120 manda como espera mínima sobre el backoff (~1 s).
	if esperas[0] < 120*time.Second {
		t.Errorf("espera = %v, esperado ≥ 120 s (Retry-After)", esperas[0])
	}
	var n int
	if err := d.QueryRow("SELECT COUNT(*) FROM push_subscriptions").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("suscripciones = %d, esperado 1 (se conserva)", n)
	}
}

// (A3) 429 persistente → máximo 3 intentos y se conserva la suscripción.
func TestReintento429MaximoTresIntentos(t *testing.T) {
	var llamadas atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		llamadas.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	d := nuevaBD(t)
	s := New(cfgConClaves(t), d, hubFalso{})
	s.sleep = func(context.Context, time.Duration) {}
	p256dh, auth := clavesSub(t)
	if err := s.Subscribe(context.Background(), "admin", srv.URL+"/push/429", p256dh, auth, "es", "", "test"); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	s.Notify(context.Background(), alertaPrueba())

	if llamadas.Load() != maxIntentos {
		t.Fatalf("peticiones = %d, esperado %d (máximo)", llamadas.Load(), maxIntentos)
	}
	var n int
	if err := d.QueryRow("SELECT COUNT(*) FROM push_subscriptions").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("suscripciones = %d, esperado 1 (se conserva)", n)
	}
}

// (c) Modo demo: NUNCA hay envío real.
func TestNotifyDemoNoEnvia(t *testing.T) {
	var recibidas atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recibidas.Add(1)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	d := nuevaBD(t)
	cfg := cfgConClaves(t)
	cfg.Demo = true
	s := New(cfg, d, hubFalso{})
	p256dh, auth := clavesSub(t)
	if err := s.Subscribe(context.Background(), "admin", srv.URL+"/push/demo", p256dh, auth, "es", "", "test"); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	s.Notify(context.Background(), alertaPrueba())
	s.Notify(context.Background(), Alert{Level: "crit", Source: "pool.tank", Target: "pools:tank",
		Kind: "pool_status", Params: map[string]any{"pool": "tank", "status": "DEGRADED"}})

	if recibidas.Load() != 0 {
		t.Fatalf("demo: peticiones al push service = %d, esperado 0", recibidas.Load())
	}
}

// (d) Un dispositivo con lang='en' recibe el texto en inglés (catálogo server-side).
func TestCatalogoIdiomaDispositivo(t *testing.T) {
	a := alertaPrueba()
	_, bodyES := catalog("es", a.Kind, a.Params)
	titleEN, bodyEN := catalog("en", a.Kind, a.Params)
	if titleEN != "Pool capacity" {
		t.Errorf("título EN = %q, esperado %q", titleEN, "Pool capacity")
	}
	if bodyEN != "Pool tank is at 85% capacity (threshold 80%)." {
		t.Errorf("cuerpo EN = %q", bodyEN)
	}
	if bodyES != "El pool tank está al 85% de capacidad (umbral 80%)." {
		t.Errorf("cuerpo ES = %q", bodyES)
	}
	// Idioma desconocido → fallback ES; kind desconocido → genérico.
	if _, b := catalog("fr", a.Kind, a.Params); b != bodyES {
		t.Errorf("fallback de idioma no devolvió ES: %q", b)
	}
	if _, b := catalog("en", "desconocido", nil); b != "You have a new alert." {
		t.Errorf("fallback genérico EN = %q", b)
	}
}

// Regla no-duplicar: warn no se envía a usuarios con SSE activo; crit sí.
func TestNoDuplicarSSEyPush(t *testing.T) {
	var recibidas atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recibidas.Add(1)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	d := nuevaBD(t)
	s := New(cfgConClaves(t), d, hubFalso{activos: map[string]bool{"admin": true}})
	p256dh, auth := clavesSub(t)
	if err := s.Subscribe(context.Background(), "admin", srv.URL+"/push/abierta", p256dh, auth, "es", "", "test"); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	// warn con la app abierta: no push (ya lo ve por SSE).
	s.Notify(context.Background(), alertaPrueba())
	if recibidas.Load() != 0 {
		t.Fatalf("warn con SSE activo: envíos = %d, esperado 0", recibidas.Load())
	}
	// crit con la app abierta: push SIEMPRE.
	s.Notify(context.Background(), Alert{Level: "crit", Source: "pool.tank", Target: "pools:tank",
		Kind: "pool_status", Params: map[string]any{"pool": "tank", "status": "DEGRADED"}})
	if recibidas.Load() != 1 {
		t.Fatalf("crit con SSE activo: envíos = %d, esperado 1", recibidas.Load())
	}
}

// Upsert: re-suscribir el mismo endpoint actualiza la fila (no duplica) y
// reasigna user_id; unsubscribe solo borra lo del propio usuario.
func TestSubscribeUpsertYUnsubscribeMultiuser(t *testing.T) {
	d := nuevaBD(t)
	if _, err := d.Exec("INSERT INTO users(user, pass_hash, role) VALUES ('maria','x','user')"); err != nil {
		t.Fatalf("usuario: %v", err)
	}
	s := New(cfgConClaves(t), d, hubFalso{})
	ctx := context.Background()
	ep := "https://push.example.com/xyz"

	if err := s.Subscribe(ctx, "admin", ep, "k1", "a1", "es", "", "ua1"); err != nil {
		t.Fatalf("subscribe 1: %v", err)
	}
	// Re-suscripción (rotación de claves, otro idioma, OTRO usuario): 1 sola fila.
	if err := s.Subscribe(ctx, "maria", ep, "k2", "a2", "en", "", "ua2"); err != nil {
		t.Fatalf("subscribe 2: %v", err)
	}
	var n int
	var userID, p256dh, lang string
	if err := d.QueryRow("SELECT COUNT(*) FROM push_subscriptions").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("filas tras upsert = %d, esperado 1", n)
	}
	if err := d.QueryRow("SELECT user_id, p256dh, lang FROM push_subscriptions").Scan(&userID, &p256dh, &lang); err != nil {
		t.Fatalf("select: %v", err)
	}
	if userID != "maria" || p256dh != "k2" || lang != "en" {
		t.Errorf("fila tras upsert = (%s,%s,%s), esperado (maria,k2,en)", userID, p256dh, lang)
	}

	// Re-POST sin lang (pushsubscriptionchange del SW): conserva el idioma.
	if err := s.Subscribe(ctx, "maria", ep, "k3", "a3", "", "", "ua3"); err != nil {
		t.Fatalf("subscribe 3 (sin lang): %v", err)
	}
	if err := d.QueryRow("SELECT lang, p256dh FROM push_subscriptions").Scan(&lang, &p256dh); err != nil {
		t.Fatalf("select: %v", err)
	}
	if lang != "en" || p256dh != "k3" {
		t.Errorf("tras re-POST sin lang: (lang,p256dh)=(%s,%s), esperado (en,k3)", lang, p256dh)
	}

	// admin NO puede borrar la suscripción de maria.
	if err := s.Unsubscribe(ctx, "admin", ep); err != nil {
		t.Fatalf("unsubscribe ajeno: %v", err)
	}
	if err := d.QueryRow("SELECT COUNT(*) FROM push_subscriptions").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("unsubscribe ajeno borró la fila (n=%d)", n)
	}
	// maria sí.
	if err := s.Unsubscribe(ctx, "maria", ep); err != nil {
		t.Fatalf("unsubscribe propio: %v", err)
	}
	if err := d.QueryRow("SELECT COUNT(*) FROM push_subscriptions").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("unsubscribe propio: n=%d, esperado 0", n)
	}
}

// urlFor: derivación de destinos navegables.
func TestURLFor(t *testing.T) {
	casos := map[string]string{
		"pools:tank": "/#/pools",
		"disks:sda":  "/#/disks",
		"tasks":      "/#/tasks",
		"settings":   "/#/settings",
		"":           "/",
		"otracosa:x": "/",
		"pools":      "/#/pools",
	}
	for target, want := range casos {
		if got := urlFor(target); got != want {
			t.Errorf("urlFor(%q) = %q, esperado %q", target, got, want)
		}
	}
}
