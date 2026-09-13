// channels_handlers_test.go — endpoints de canales de alerta (#134):
// estado sin secretos, configuración en caliente y prueba de envío.
package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"easyzfs/internal/auth"
	"easyzfs/internal/channels"
	"easyzfs/internal/config"
	"easyzfs/internal/db"
	"easyzfs/internal/users"
)

// serverChannelsPrueba — servidor con BD migrada, admin, canales y su store.
func serverChannelsPrueba(t *testing.T, ch *channels.Client) (http.Handler, *http.Cookie) {
	t.Helper()
	d, err := db.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	if err := db.Migrate(context.Background(), d); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	us := users.NewStore(d)
	if err := us.Bootstrap(context.Background(), "adminpass-largo"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	cfg := &config.Config{}
	am := auth.NewManager(d, []byte("secreto-de-prueba-32-bytes-xxxxxxxx"), false)
	srv := NewServer(Deps{
		Cfg: cfg, DB: d, Auth: am, Users: us,
		Channels: ch, ChannelStore: channels.NewStore(d),
	})
	cookie, err := am.CreateSession(context.Background(), "admin")
	if err != nil {
		t.Fatalf("sesión: %v", err)
	}
	return srv.Handler(), cookie
}

// GET /api/channels: estado sin secretos (el token y el topic NUNCA salen; el
// chat id y el servidor sí, no son credenciales).
func TestGetChannelsNoSecrets(t *testing.T) {
	ch := channels.New(channels.Config{
		NtfyURL:          "https://ntfy.sh/topic-secreto",
		NtfyToken:        "ntfy-token-secreto",
		GotifyURL:        "https://gotify.example.com",
		GotifyToken:      "gotify-token-secreto",
		TelegramBotToken: "123:TELEGRAM-SECRETO",
		TelegramChatID:   "-100987654",
	})
	h, cookie := serverChannelsPrueba(t, ch)
	w := doReq(t, h, cookie, "GET", "/api/channels", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, secret := range []string{"topic-secreto", "ntfy-token-secreto", "gotify-token-secreto", "TELEGRAM-SECRETO"} {
		if strings.Contains(body, secret) {
			t.Fatalf("la respuesta filtra un secreto (%q): %s", secret, body)
		}
	}
	var resp struct {
		Channels map[string]struct {
			Configured bool   `json:"configured"`
			Server     string `json:"server"`
			ChatID     string `json:"chat_id"`
			TokenSet   bool   `json:"token_set"`
			TopicSet   bool   `json:"topic_set"`
			Editable   bool   `json:"editable"`
		} `json:"channels"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("body no JSON: %v", err)
	}
	if !resp.Channels["ntfy"].Configured || !resp.Channels["ntfy"].TopicSet || !resp.Channels["ntfy"].TokenSet {
		t.Errorf("ntfy: %+v", resp.Channels["ntfy"])
	}
	if resp.Channels["ntfy"].Server != "https://ntfy.sh" {
		t.Errorf("ntfy server = %q, esperado sin topic", resp.Channels["ntfy"].Server)
	}
	if !resp.Channels["gotify"].Configured || !resp.Channels["gotify"].TokenSet {
		t.Errorf("gotify: %+v", resp.Channels["gotify"])
	}
	if !resp.Channels["telegram"].Configured || resp.Channels["telegram"].ChatID != "-100987654" {
		t.Errorf("telegram: %+v", resp.Channels["telegram"])
	}
	if resp.Channels["syslog"].Configured {
		t.Error("syslog no está configurado")
	}
	if !resp.Channels["telegram"].Editable || resp.Channels["email"].Editable {
		t.Error("editable mal marcado (telegram sí, email no)")
	}
}

// PUT /api/channels/{name}: guarda en caliente, token write-only (vacío
// conserva, clear_token borra) y DELETE desactiva.
func TestPutAndDeleteChannel(t *testing.T) {
	var gotTitle string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p map[string]string
		_ = json.NewDecoder(r.Body).Decode(&p)
		gotTitle = p["title"]
		w.WriteHeader(http.StatusOK)
	}))
	defer fake.Close()

	ch := channels.New(channels.Config{})
	h, cookie := serverChannelsPrueba(t, ch)

	// Configurar ntfy apuntando al fake.
	w := doReq(t, h, cookie, "PUT", "/api/channels/ntfy",
		`{"url":"`+fake.URL+`/topic-secreto","token":"tok-123"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT ntfy status %d: %s", w.Code, w.Body.String())
	}
	if !ch.Configured("ntfy") {
		t.Fatal("el canal debe estar activo sin reiniciar")
	}
	// El topic no se expone; el token tampoco (solo token_set).
	gw := doReq(t, h, cookie, "GET", "/api/channels", "")
	if strings.Contains(gw.Body.String(), "topic-secreto") || strings.Contains(gw.Body.String(), "tok-123") {
		t.Fatalf("GET filtra secretos: %s", gw.Body.String())
	}
	if !strings.Contains(gw.Body.String(), `"token_set":true`) {
		t.Fatalf("GET debería marcar token_set: %s", gw.Body.String())
	}

	// Probar entrega end-to-end contra el fake (sin reiniciar).
	tw := doReq(t, h, cookie, "POST", "/api/channels/ntfy/test", "")
	if tw.Code != http.StatusOK {
		t.Fatalf("test status %d: %s", tw.Code, tw.Body.String())
	}
	if gotTitle == "" {
		t.Fatal("el fake no recibió la notificación")
	}

	// Token vacío (ausente) conserva el existente.
	w = doReq(t, h, cookie, "PUT", "/api/channels/ntfy", `{"url":"`+fake.URL+`/topic-secreto"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT sin token status %d: %s", w.Code, w.Body.String())
	}
	if ch.Config().NtfyToken != "tok-123" {
		t.Fatalf("token vacío debe conservar, quedó %q", ch.Config().NtfyToken)
	}

	// clear_token borra el token (ntfy sigue activo: el token es opcional).
	w = doReq(t, h, cookie, "PUT", "/api/channels/ntfy", `{"clear_token":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT clear_token status %d: %s", w.Code, w.Body.String())
	}
	if ch.Config().NtfyToken != "" || !ch.Configured("ntfy") {
		t.Fatalf("clear_token debe borrar el token y mantener el canal: %+v", ch.Config())
	}

	// DELETE desactiva.
	w = doReq(t, h, cookie, "DELETE", "/api/channels/ntfy", "")
	if w.Code != http.StatusOK {
		t.Fatalf("DELETE status %d: %s", w.Code, w.Body.String())
	}
	if ch.Configured("ntfy") {
		t.Fatal("DELETE debe desactivar el canal")
	}
}

// PUT inválido: validaciones por canal (sin persistir).
func TestPutChannelValidation(t *testing.T) {
	ch := channels.New(channels.Config{})
	h, cookie := serverChannelsPrueba(t, ch)
	casos := []struct {
		canal, body, errCode string
	}{
		{"ntfy", `{"url":"https://ntfy.sh"}`, "invalid_topic"},
		{"ntfy", `{"url":"ftp://ntfy.sh/x"}`, "invalid_url"},
		{"telegram", `{"token":"123:AAAAAAAAAA"}`, "incomplete"},
		{"telegram", `{"token":"malo","chat_id":"1"}`, "invalid_token"},
		{"syslog", `{"host":"127.0.0.1","port":70000}`, "invalid_port"},
		{"syslog", `{"host":"127.0.0.1","proto":"sctp"}`, "invalid_proto"},
	}
	for _, c := range casos {
		w := doReq(t, h, cookie, "PUT", "/api/channels/"+c.canal, c.body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s %s: status %d, esperado 400", c.canal, c.body, w.Code)
			continue
		}
		var resp map[string]string
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if resp["error"] != c.errCode {
			t.Errorf("%s %s: error %q, esperado %q", c.canal, c.body, resp["error"], c.errCode)
		}
	}
	if ch.Enabled() {
		t.Fatal("una config inválida no debe persistir")
	}
}

// Canal no configurado: 400 channel_not_configured (no se finge éxito).
func TestTestChannelNotConfigured(t *testing.T) {
	ch := channels.New(channels.Config{})
	h, cookie := serverChannelsPrueba(t, ch)
	w := doReq(t, h, cookie, "POST", "/api/channels/telegram/test", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, esperado 400", w.Code)
	}
	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("body no JSON: %v", err)
	}
	if resp["error"] != "channel_not_configured" {
		t.Fatalf("error = %q, esperado channel_not_configured", resp["error"])
	}
}

// Canal desconocido: 404.
func TestTestChannelUnknown(t *testing.T) {
	h, cookie := serverChannelsPrueba(t, channels.New(channels.Config{NtfyURL: "https://ntfy.sh/x"}))
	w := doReq(t, h, cookie, "POST", "/api/channels/palomitas/test", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d, esperado 404", w.Code)
	}
}
