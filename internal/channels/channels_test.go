// channels_test.go — tests de los canales ntfy/gotify/syslog con destinos de
// prueba (httptest + listener UDP) para verificar payloads y cabeceras.
package channels

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ntfy recibe POST en / con Authorization Bearer.
func TestNtfy(t *testing.T) {
	var got map[string]string
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("método %s, esperado POST", r.Method)
		}
		auth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL, "tok123", "", "", "", 0, "udp", 1)
	if !c.Enabled() {
		t.Fatal("con ntfy URL debería estar enabled")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c.Send(ctx, "Título", "Cuerpo")

	if got["title"] != "Título" || got["message"] != "Cuerpo" {
		t.Fatalf("payload ntfy inesperado: %v", got)
	}
	if auth != "Bearer tok123" {
		t.Fatalf("auth %q, esperado Bearer tok123", auth)
	}
}

// gotify recibe POST en /message con X-Gotify-Key.
func TestGotify(t *testing.T) {
	var got map[string]string
	var key string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/message" {
			t.Errorf("path %q, esperado /message", r.URL.Path)
		}
		key = r.Header.Get("X-Gotify-Key")
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New("", "", srv.URL, "apptok", "", 0, "udp", 1)
	if !c.Enabled() {
		t.Fatal("con gotify URL debería estar enabled")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c.Send(ctx, "Título", "Cuerpo")

	if got["message"] != "Cuerpo" {
		t.Fatalf("payload gotify inesperado: %v", got)
	}
	if key != "apptok" {
		t.Fatalf("X-Gotify-Key %q, esperado apptok", key)
	}
}

// syslog UDP: el datagrama llega con PRI y texto.
func TestSyslogUDP(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	addr := pc.LocalAddr().String()
	host, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)

	c := New("", "", "", "", host, port, "udp", 1)
	if !c.Enabled() {
		t.Fatal("con syslog host debería estar enabled")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c.Send(ctx, "Título", "Cuerpo")

	_ = pc.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 2048)
	n, _, err := pc.ReadFrom(buf)
	if err != nil {
		t.Fatal(err)
	}
	msg := string(buf[:n])
	if !strings.HasPrefix(msg, "<9>") { // facility 1*8 + severity 1 = 9
		t.Fatalf("PRI inesperado: %q", msg[:4])
	}
	if !strings.Contains(msg, "Título: Cuerpo") {
		t.Fatalf("contenido inesperado: %q", msg)
	}
}

// sin configuración: Enabled false y Send no rompe.
func TestDisabled(t *testing.T) {
	c := New("", "", "", "", "", 0, "udp", 1)
	if c.Enabled() {
		t.Fatal("sin canales no debería estar enabled")
	}
	c.Send(context.Background(), "t", "b") // no debe panickear
	if c == nil {
		t.Fatal("nil")
	}
	nilClient := (*Client)(nil)
	nilClient.Send(context.Background(), "t", "b") // no debe panickear
}

// fallo del destino: log y sigue, sin panic.
func TestSendErrorNoPanic(t *testing.T) {
	c := New("http://127.0.0.1:1/no", "", "", "", "", 0, "udp", 1) // puerto 1: nada escucha
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c.Send(ctx, "t", "b")
}
