// channels_handlers_test.go — endpoints de canales de alerta (#134):
// GET /api/channels nunca expone secretos y POST /api/channels/{name}/test
// exige canal configurado y propaga el fallo del destino.
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

// serverChannelsPrueba — servidor con BD migrada, admin y canales inyectados.
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
	srv := NewServer(Deps{Cfg: cfg, DB: d, Auth: am, Users: us, Channels: ch})
	cookie, err := am.CreateSession(context.Background(), "admin")
	if err != nil {
		t.Fatalf("sesión: %v", err)
	}
	return srv.Handler(), cookie
}

// GET /api/channels: estado sin secretos (el token NUNCA sale; el chat id sí).
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
			Detail     string `json:"detail"`
		} `json:"channels"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("body no JSON: %v", err)
	}
	for _, name := range []string{"ntfy", "gotify", "telegram"} {
		if !resp.Channels[name].Configured {
			t.Errorf("%s debería estar configured", name)
		}
	}
	if resp.Channels["syslog"].Configured {
		t.Error("syslog no está configurado")
	}
	if resp.Channels["telegram"].Detail != "-100987654" {
		t.Errorf("detail de telegram = %q, esperado el chat id", resp.Channels["telegram"].Detail)
	}
	if resp.Channels["push"].Configured {
		t.Error("push sin claves VAPID no debe estar configurado")
	}
}

// POST /api/channels/{name}/test: canal configurado → 200 y el destino recibe.
func TestTestChannelDelivers(t *testing.T) {
	var gotTitle string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p map[string]string
		_ = json.NewDecoder(r.Body).Decode(&p)
		gotTitle = p["title"]
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ch := channels.New(channels.Config{NtfyURL: srv.URL})
	h, cookie := serverChannelsPrueba(t, ch)
	w := doReq(t, h, cookie, "POST", "/api/channels/ntfy/test", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if gotTitle == "" {
		t.Fatal("el canal de prueba no recibió el mensaje")
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
