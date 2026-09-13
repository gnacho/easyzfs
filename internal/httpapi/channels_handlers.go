// channels_handlers.go — canales de alerta (#134): estado, configuración y
// prueba de envío. Los secretos (bot token de Telegram, tokens de ntfy/Gotify)
// NUNCA salen en la respuesta: solo un booleano "token_set"/"topic_set".
// La configuración se guarda en BD y entra en vigor sin reiniciar.
package httpapi

import (
	"context"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"easyzfs/internal/auth"
	"easyzfs/internal/channels"
)

// channelInfo — estado saneado de un canal para la UI.
type channelInfo struct {
	Configured bool   `json:"configured"`
	Server     string `json:"server,omitempty"`  // ntfy: servidor sin topic
	URL        string `json:"url,omitempty"`     // gotify
	ChatID     string `json:"chat_id,omitempty"` // telegram
	Host       string `json:"host,omitempty"`    // syslog
	Port       int    `json:"port,omitempty"`
	Proto      string `json:"proto,omitempty"`
	Facility   int    `json:"facility,omitempty"`
	TokenSet   bool   `json:"token_set,omitempty"`
	TopicSet   bool   `json:"topic_set,omitempty"`
	Editable   bool   `json:"editable"`
}

// channelPatch — campos editables (punteros: ausente = no tocar).
// token vacío o ausente = conservar; clear_token = borrar.
type channelPatch struct {
	URL        *string `json:"url"`
	Token      *string `json:"token"`
	ClearToken bool    `json:"clear_token"`
	ChatID     *string `json:"chat_id"`
	Host       *string `json:"host"`
	Port       *int    `json:"port"`
	Proto      *string `json:"proto"`
	Facility   *int    `json:"facility"`
}

// telegramTokenRe — formato del token de @BotFather: <id>:<secreto>.
var telegramTokenRe = regexp.MustCompile(`^\d+:[A-Za-z0-9_-]{10,}$`)

// testableChannel — canales que admiten prueba de envío y edición.
func testableChannel(name string) bool {
	switch name {
	case "ntfy", "gotify", "telegram", "syslog":
		return true
	}
	return false
}

// getChannels — GET /api/channels (admin): estado de cada canal. Incluye los
// canales de infraestructura (editables) y email/webhook/push (solo estado:
// se configuran por entorno o en otras secciones). Nunca expone secretos.
func (s *Server) getChannels(w http.ResponseWriter, r *http.Request) {
	cfg := s.channels.Config()
	out := map[string]channelInfo{
		"ntfy": {
			Configured: cfg.NtfyURL != "",
			Server:     channels.NtfyServer(cfg.NtfyURL),
			TopicSet:   cfg.NtfyURL != "",
			TokenSet:   cfg.NtfyToken != "",
			Editable:   true,
		},
		"gotify": {
			Configured: cfg.GotifyURL != "",
			URL:        cfg.GotifyURL,
			TokenSet:   cfg.GotifyToken != "",
			Editable:   true,
		},
		"telegram": {
			Configured: cfg.TelegramBotToken != "" && cfg.TelegramChatID != "",
			ChatID:     cfg.TelegramChatID,
			TokenSet:   cfg.TelegramBotToken != "",
			Editable:   true,
		},
		"syslog": {
			Configured: cfg.SyslogHost != "",
			Host:       cfg.SyslogHost,
			Port:       cfg.SyslogPort,
			Proto:      cfg.SyslogProto,
			Facility:   cfg.SyslogFacility,
			Editable:   true,
		},
	}
	// Email: operativo con SMTP_HOST + SMTP_FROM (env; sin edición aquí).
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

// putChannel — PUT /api/channels/{name} (admin): guarda la config del canal y
// la aplica en caliente (sin reiniciar). Valida antes de persistir.
func (s *Server) putChannel(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !testableChannel(name) {
		writeErr(w, http.StatusNotFound, "unknown_channel", "canal desconocido: "+name)
		return
	}
	var p channelPatch
	if !decodeJSON(w, r, &p) {
		return
	}
	cfg := s.channels.Config()
	switch name {
	case "ntfy":
		// La URL incluye el topic (secreto): write-only, vacío conserva.
		// clear_token solo afecta al token, nunca a la URL.
		applyValue(&cfg.NtfyURL, p.URL, false)
		applyValue(&cfg.NtfyToken, p.Token, p.ClearToken)
	case "gotify":
		if p.URL != nil {
			cfg.GotifyURL = strings.TrimSpace(*p.URL)
		}
		applyValue(&cfg.GotifyToken, p.Token, p.ClearToken)
	case "telegram":
		applyValue(&cfg.TelegramBotToken, p.Token, p.ClearToken)
		if p.ChatID != nil {
			cfg.TelegramChatID = strings.TrimSpace(*p.ChatID)
		}
	case "syslog":
		if p.Host != nil {
			cfg.SyslogHost = strings.TrimSpace(*p.Host)
		}
		if p.Port != nil {
			cfg.SyslogPort = *p.Port
		}
		if p.Proto != nil {
			cfg.SyslogProto = strings.TrimSpace(*p.Proto)
		}
		if p.Facility != nil {
			cfg.SyslogFacility = *p.Facility
		}
		// Defaults al activar el canal sin especificarlos.
		if cfg.SyslogHost != "" {
			if cfg.SyslogPort == 0 {
				cfg.SyslogPort = 514
			}
			if cfg.SyslogProto == "" {
				cfg.SyslogProto = "udp"
			}
		}
	}
	if msg, errCode := validateChannel(name, cfg); errCode != "" {
		writeErr(w, http.StatusBadRequest, errCode, msg)
		return
	}
	if err := s.channelStore.Save(r.Context(), cfg); err != nil {
		log.Printf("channels: guardar %s: %v", name, err)
		writeErr(w, http.StatusInternalServerError, "save_failed", "no se pudo guardar la configuración")
		return
	}
	s.channels.Apply(cfg)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "channel": name})
}

// deleteChannel — DELETE /api/channels/{name} (admin): desactiva el canal.
func (s *Server) deleteChannel(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !testableChannel(name) {
		writeErr(w, http.StatusNotFound, "unknown_channel", "canal desconocido: "+name)
		return
	}
	cfg := s.channels.Config()
	switch name {
	case "ntfy":
		cfg.NtfyURL, cfg.NtfyToken = "", ""
	case "gotify":
		cfg.GotifyURL, cfg.GotifyToken = "", ""
	case "telegram":
		cfg.TelegramBotToken, cfg.TelegramChatID = "", ""
	case "syslog":
		cfg.SyslogHost, cfg.SyslogPort, cfg.SyslogProto, cfg.SyslogFacility = "", 0, "", 0
	}
	if err := s.channelStore.Save(r.Context(), cfg); err != nil {
		log.Printf("channels: desactivar %s: %v", name, err)
		writeErr(w, http.StatusInternalServerError, "save_failed", "no se pudo guardar la configuración")
		return
	}
	s.channels.Apply(cfg)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "channel": name})
}

// applyValue — campo write-only: valor vacío/ausente conserva el actual;
// clear lo borra. (El valor es un puntero para distinguir "ausente" de "vacío".)
func applyValue(dst *string, val *string, clear bool) {
	if clear {
		*dst = ""
		return
	}
	if val != nil && strings.TrimSpace(*val) != "" {
		*dst = strings.TrimSpace(*val)
	}
}

// validateChannel — valida la config resultante del canal. Devuelve mensaje y
// código de error (vacíos si es válida).
func validateChannel(name string, cfg channels.Config) (string, string) {
	switch name {
	case "ntfy":
		if cfg.NtfyURL == "" {
			return "", "" // desactivado
		}
		u, err := url.Parse(cfg.NtfyURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return "la URL de ntfy debe ser http(s)://servidor/topic", "invalid_url"
		}
		if strings.Trim(u.Path, "/") == "" {
			return "la URL de ntfy debe incluir el topic (p.ej. https://ntfy.sh/mi-topic)", "invalid_topic"
		}
	case "gotify":
		if cfg.GotifyURL == "" {
			return "", ""
		}
		u, err := url.Parse(cfg.GotifyURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return "la URL de Gotify debe ser http(s)://servidor", "invalid_url"
		}
	case "telegram":
		token, chat := cfg.TelegramBotToken != "", cfg.TelegramChatID != ""
		if !token && !chat {
			return "", "" // desactivado
		}
		if token != chat {
			return "Telegram requiere bot token Y chat id (o ninguno de los dos)", "incomplete"
		}
		if !telegramTokenRe.MatchString(cfg.TelegramBotToken) {
			return "el bot token no tiene el formato de @BotFather (<id>:<secreto>)", "invalid_token"
		}
	case "syslog":
		if cfg.SyslogHost == "" {
			return "", "" // desactivado
		}
		if cfg.SyslogPort < 1 || cfg.SyslogPort > 65535 {
			return "el puerto de syslog debe estar entre 1 y 65535", "invalid_port"
		}
		if cfg.SyslogProto != "udp" && cfg.SyslogProto != "tcp" {
			return "el protocolo de syslog debe ser udp o tcp", "invalid_proto"
		}
		if cfg.SyslogFacility < 0 || cfg.SyslogFacility > 23 {
			return "la facility de syslog debe estar entre 0 y 23", "invalid_facility"
		}
	}
	return "", ""
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
			"el canal "+name+" no está configurado")
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
