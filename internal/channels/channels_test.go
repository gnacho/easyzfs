// channels_test.go — tests de los canales ntfy/gotify/telegram/syslog con
// destinos de prueba (httptest + listener UDP): payloads, cabeceras, límites
// y reglas de seguridad (redacción de secretos, reintentos).
package channels

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
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

	c := New(Config{NtfyURL: srv.URL, NtfyToken: "tok123"})
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

	c := New(Config{GotifyURL: srv.URL, GotifyToken: "apptok"})
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

// telegram: POST a /bot<token>/sendMessage con chat_id y texto.
func TestTelegram(t *testing.T) {
	var got map[string]any
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(Config{TelegramBotToken: "123:ABC", TelegramChatID: "-1009"})
	c.telegramBase = srv.URL
	if !c.Enabled() {
		t.Fatal("con Telegram configurado debería estar enabled")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c.Send(ctx, "Pool DEGRADED", "TheZBox")

	if path != "/bot123:ABC/sendMessage" {
		t.Fatalf("path %q, esperado /bot123:ABC/sendMessage", path)
	}
	if got["chat_id"] != "-1009" {
		t.Fatalf("chat_id %v, esperado -1009", got["chat_id"])
	}
	if txt, _ := got["text"].(string); txt != "Pool DEGRADED\nTheZBox" {
		t.Fatalf("text %q inesperado", txt)
	}
	if v, ok := got["disable_web_page_preview"]; !ok || v != true {
		t.Fatalf("disable_web_page_preview ausente/falso: %v", got)
	}
}

// telegram incompleto (solo token o solo chat) = canal desactivado.
func TestTelegramIncomplete(t *testing.T) {
	onlyToken := New(Config{TelegramBotToken: "123:ABC"})
	if onlyToken.Enabled() || onlyToken.Configured("telegram") {
		t.Fatal("solo token no debe activar Telegram")
	}
	onlyChat := New(Config{TelegramChatID: "-1009"})
	if onlyChat.Enabled() || onlyChat.Configured("telegram") {
		t.Fatal("solo chat id no debe activar Telegram")
	}
	if err := onlyChat.Test(context.Background(), "telegram", "t", "b"); err != nil {
		t.Fatalf("canal incompleto debe ser no-op, no error: %v", err)
	}
}

// Telegram limita sendMessage a 4096 caracteres: se trunca por runas.
func TestTelegramTruncate(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(Config{TelegramBotToken: "tok", TelegramChatID: "1"})
	c.telegramBase = srv.URL
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c.Send(ctx, "Título", strings.Repeat("á", 5000))

	txt, _ := got["text"].(string)
	runes := []rune(txt)
	if len(runes) != maxMessageRunes {
		t.Fatalf("longitud %d runas, esperado %d", len(runes), maxMessageRunes)
	}
	if runes[len(runes)-1] != '…' {
		t.Fatalf("el truncado debe acabar en elipsis: %q", string(runes[len(runes)-3:]))
	}
}

// Redacción de secretos: un fallo de red en ntfy no puede filtrar el topic.
func TestRedactTopicOnNetworkError(t *testing.T) {
	c := New(Config{NtfyURL: "http://127.0.0.1:1/mi-topic-secreto"})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := c.Test(ctx, "ntfy", "t", "b")
	if err == nil {
		t.Fatal("se esperaba error de red")
	}
	if strings.Contains(err.Error(), "mi-topic-secreto") {
		t.Fatalf("el error filtra el topic: %v", err)
	}
	if !strings.Contains(err.Error(), "***") {
		t.Fatalf("el error debería redactar el topic con ***: %v", err)
	}
}

// Redacción del bot token de Telegram en fallos de red.
func TestRedactTelegramTokenOnNetworkError(t *testing.T) {
	c := New(Config{TelegramBotToken: "123:SUPERSECRETO", TelegramChatID: "1"})
	c.telegramBase = "http://127.0.0.1:1"
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := c.Test(ctx, "telegram", "t", "b")
	if err == nil {
		t.Fatal("se esperaba error de red")
	}
	if strings.Contains(err.Error(), "SUPERSECRETO") {
		t.Fatalf("el error filtra el bot token: %v", err)
	}
	if !strings.Contains(err.Error(), "***") {
		t.Fatalf("el error debería redactar el token con ***: %v", err)
	}
}

// 5xx es transitorio: se reintenta una vez y el segundo intento entrega.
func TestRetryOn5xx(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(Config{NtfyURL: srv.URL})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Test(ctx, "ntfy", "t", "b"); err != nil {
		t.Fatalf("debería entregar tras reintento: %v", err)
	}
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Fatalf("peticiones %d, esperado 2", n)
	}
}

// 4xx permanente no se reintenta (una sola petición).
func TestNoRetry4xx(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		http.Error(w, `{"ok":false,"description":"chat not found"}`, http.StatusBadRequest)
	}))
	defer srv.Close()

	c := New(Config{NtfyURL: srv.URL})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := c.Test(ctx, "ntfy", "t", "b")
	if err == nil {
		t.Fatal("se esperaba error 400")
	}
	if !strings.Contains(err.Error(), "chat not found") {
		t.Fatalf("el error debe incluir el cuerpo acotado del destino: %v", err)
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("peticiones %d, esperado 1 (sin reintento en 4xx)", n)
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

	c := New(Config{SyslogHost: host, SyslogPort: port, SyslogProto: "udp", SyslogFacility: 1})
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
	c := New(Config{})
	if c.Enabled() {
		t.Fatal("sin canales no debería estar enabled")
	}
	for _, name := range []string{"ntfy", "gotify", "telegram", "syslog"} {
		if c.Configured(name) {
			t.Fatalf("%s no debería estar configurado", name)
		}
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
	c := New(Config{NtfyURL: "http://127.0.0.1:1/no"})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c.Send(ctx, "t", "b")
}

// Configured — cada canal responde a su configuración mínima.
func TestConfigured(t *testing.T) {
	c := New(Config{NtfyURL: "https://ntfy.sh/x", GotifyURL: "https://g", TelegramBotToken: "t", TelegramChatID: "1", SyslogHost: "127.0.0.1"})
	for _, name := range []string{"ntfy", "gotify", "telegram", "syslog"} {
		if !c.Configured(name) {
			t.Fatalf("%s debería estar configurado", name)
		}
	}
	if c.Configured("email") {
		t.Fatal("email no es un canal del paquete")
	}
	if c.TelegramChatID() != "1" {
		t.Fatalf("chat id %q", c.TelegramChatID())
	}
}

// Test exige canal configurado: no-op silencioso en los canales inertes.
func TestChannelTestOnDisabledIsNoop(t *testing.T) {
	c := New(Config{})
	for _, name := range []string{"ntfy", "gotify", "telegram", "syslog"} {
		if err := c.Test(context.Background(), name, "t", "b"); err != nil {
			t.Fatalf("%s desactivado debe ser no-op: %v", name, err)
		}
	}
}
