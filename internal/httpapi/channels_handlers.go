// channels_handlers.go — estado de los canales de alerta y prueba de envío
// (#134). Los secretos (bot token de Telegram, token de ntfy/Gotify) NUNCA
// salen en la respuesta; el chat id de Telegram sí se muestra porque no es una
// credencial (permite al admin confirmar el destino configurado).
package httpapi

import (
	"context"
	"log"
	"net/http"
	"time"

	"easyzfs/internal/auth"
)

// channelInfo — estado de un canal para la UI.
type channelInfo struct {
	Configured bool   `json:"configured"`
	Detail     string `json:"detail,omitempty"`
}

// testableChannel — canales del paquete channels que admiten prueba de envío.
func testableChannel(name string) bool {
	switch name {
	case "ntfy", "gotify", "telegram", "syslog":
		return true
	}
	return false
}

// getChannels — GET /api/channels (admin): estado de cada canal de alerta.
// Incluye los canales de infraestructura (ntfy/Gotify/Telegram/syslog), email,
// webhook y Web Push. Nunca expone secretos.
func (s *Server) getChannels(w http.ResponseWriter, r *http.Request) {
	out := map[string]channelInfo{}
	for _, name := range []string{"ntfy", "gotify", "telegram", "syslog"} {
		info := channelInfo{Configured: s.channels.Configured(name)}
		if name == "telegram" && info.Configured {
			info.Detail = s.channels.TelegramChatID()
		}
		out[name] = info
	}
	// Email: operativo con SMTP_HOST + SMTP_FROM (mismo criterio que el wiring).
	out["email"] = channelInfo{Configured: s.cfg.SMTPHost != "" && s.cfg.SMTPFrom != ""}
	// Webhook saliente: su URL vive en settings (BD), no en env.
	whConfigured := false
	if s.settings != nil {
		if st, err := s.settings.Load(r.Context()); err == nil {
			whConfigured = st.Webhook != ""
		}
	}
	out["webhook"] = channelInfo{Configured: whConfigured}
	out["push"] = channelInfo{Configured: s.cfg.PushEnabled()}
	writeJSON(w, http.StatusOK, map[string]any{"channels": out})
}

// testChannel — POST /api/channels/{name}/test (admin): envía una notificación
// de prueba por el canal indicado. 400 si el canal no está configurado (no se
// finge un éxito), 502 si el destino falla (mensaje ya redactado por el paquete).
func (s *Server) testChannel(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !testableChannel(name) {
		writeErr(w, http.StatusNotFound, "unknown_channel", "canal desconocido: "+name)
		return
	}
	if s.channels == nil || !s.channels.Configured(name) {
		writeErr(w, http.StatusBadRequest, "channel_not_configured",
			"el canal "+name+" no está configurado (define sus variables de entorno y reinicia el servicio)")
		return
	}
	lang := "es"
	if u, err := s.users.Get(r.Context(), auth.UserFromContext(r.Context())); err == nil && u.Language == "en" {
		lang = "en"
	}
	title, body := testMessage(lang)
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	if err := s.channels.Test(ctx, name, title, body); err != nil {
		log.Printf("channels: prueba de %s falló: %v", name, err)
		writeErr(w, http.StatusBadGateway, "channel_test_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "channel": name})
}

// testMessage — texto de la notificación de prueba en el idioma del admin.
func testMessage(lang string) (title, body string) {
	if lang == "en" {
		return "EasyZFS test notification",
			"If you can read this, the alert channel is configured correctly."
	}
	return "Notificación de prueba de EasyZFS",
		"Si lees esto, el canal de alertas está bien configurado."
}
