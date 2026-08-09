package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"easyzfs/internal/db"
)

func newTestNotifier(t *testing.T, url string, cfg Config) (*Notifier, func()) {
	t.Helper()
	d, err := db.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.Migrate(context.Background(), d); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	n := NewNotifier(cfg, d, func() string { return url })
	return n, func() {
		n.Close()
		d.Close()
	}
}

func ev(id int64) Event {
	return Event{ID: id, Ts: time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC),
		Level: "warn", Source: "pool.tank", Target: "pools:tank", Message: "Pool tank al 95% de capacidad"}
}

// Entrega correcta: el receptor recibe el payload con event_id, timestamp
// RFC3339 y los campos de la alerta; con secret configurado llega la firma
// HMAC-SHA256 correcta (mismo header que el código de producción).
func TestNotifier_EntregaOK_FirmaYEventID(t *testing.T) {
	var mu sync.Mutex
	var gotPayload map[string]any
	var gotSig string
	var rawBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		gotSig = r.Header.Get("X-EasyZFS-Signature")
		rawBody, _ = io.ReadAll(r.Body)
		json.Unmarshal(rawBody, &gotPayload)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	n, cleanup := newTestNotifier(t, srv.URL, Config{
		Secret: "secreta", Timeout: 2 * time.Second, Retries: 2, RetryDelay: 10 * time.Millisecond,
	})
	defer cleanup()

	n.Notify(ev(42))

	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		ready := gotPayload != nil
		mu.Unlock()
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("el receptor no recibió el evento a tiempo")
		}
		time.Sleep(20 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotPayload["event_id"] != "42" {
		t.Errorf("event_id = %v, esperado 42", gotPayload["event_id"])
	}
	if gotPayload["ts"] != "2026-08-09T12:00:00Z" {
		t.Errorf("ts = %v, esperado RFC3339 UTC", gotPayload["ts"])
	}
	if gotPayload["level"] != "warn" || gotPayload["source"] != "pool.tank" || gotPayload["message"] == "" {
		t.Errorf("payload incompleto: %v", gotPayload)
	}
	if gotSig == "" {
		t.Fatal("no llegó firma X-EasyZFS-Signature")
	}
	mac := hmac.New(sha256.New, []byte("secreta"))
	mac.Write(rawBody)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if gotSig != want {
		t.Errorf("firma = %s, esperada %s", gotSig, want)
	}
}

// Fallo permanente (400): no se reintenta y el evento va a la DLQ.
func TestNotifier_4xxVaADLQ(t *testing.T) {
	var hits int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	n, cleanup := newTestNotifier(t, srv.URL, Config{
		Timeout: 2 * time.Second, Retries: 3, RetryDelay: 5 * time.Millisecond,
	})
	defer cleanup()

	n.Notify(ev(7))

	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		got := hits
		mu.Unlock()
		if got > 0 {
			time.Sleep(100 * time.Millisecond) // deja que el worker acabe de guardar DLQ
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("el receptor no recibió el evento a tiempo")
		}
		time.Sleep(20 * time.Millisecond)
	}

	mu.Lock()
	if hits != 1 {
		t.Errorf("hits = %d, esperado 1 (un 400 no se reintenta)", hits)
	}
	mu.Unlock()

	var nDLQ int
	if err := n.db.QueryRow("SELECT COUNT(*) FROM webhook_events WHERE event_id='7'").Scan(&nDLQ); err != nil {
		t.Fatal(err)
	}
	if nDLQ != 1 {
		t.Errorf("webhook_events = %d, esperada 1 (fallo permanente)", nDLQ)
	}
	var errText string
	if err := n.db.QueryRow("SELECT error FROM webhook_events WHERE event_id='7'").Scan(&errText); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errText, "400") {
		t.Errorf("error DLQ = %q, esperado mención del 400", errText)
	}
}

// 5xx persistente: tras agotar reintentos va a DLQ.
func TestNotifier_5xxAgotaReintentosYVaADLQ(t *testing.T) {
	var hits int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	n, cleanup := newTestNotifier(t, srv.URL, Config{
		Timeout: 2 * time.Second, Retries: 2, RetryDelay: 2 * time.Millisecond,
	})
	defer cleanup()

	n.Notify(ev(9))

	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		got := hits
		mu.Unlock()
		if got >= 3 {
			time.Sleep(100 * time.Millisecond)
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no se completaron los reintentos a tiempo")
		}
		time.Sleep(20 * time.Millisecond)
	}

	mu.Lock()
	if hits != 3 {
		t.Errorf("hits = %d, esperado 3 (intento inicial + 2 reintentos)", hits)
	}
	mu.Unlock()

	var nDLQ int
	if err := n.db.QueryRow("SELECT COUNT(*) FROM webhook_events WHERE event_id='9'").Scan(&nDLQ); err != nil {
		t.Fatal(err)
	}
	if nDLQ != 1 {
		t.Errorf("webhook_events = %d, esperada 1 (reintentos agotados)", nDLQ)
	}
}

// Sin URL configurada (getURL → "") el webhook está desactivado: no llama.
func TestNotifier_SinURLNoEnvia(t *testing.T) {
	var hits int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	d, err := db.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	n := NewNotifier(Config{Timeout: 2 * time.Second, Retries: 2}, d, func() string { return "" })
	defer n.Close()

	n.Notify(ev(1))
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if hits != 0 {
		t.Errorf("hits = %d, esperado 0 (URL vacía = webhook desactivado)", hits)
	}
}
