// channels_handlers.go — canales de alerta (#134): estado, configuración y
// prueba de envío. Los secretos (bot token de Telegram, tokens de ntfy/Gotify,
// contraseña SMTP) NUNCA salen en la respuesta: solo un booleano token_set.
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
	"easyzfs/internal/notifier"
)

// channelInfo — estado saneado de un canal para la UI.
type channelInfo struct {
	Configured bool   `json:"configured"`
	Server     string `json:"server,omitempty"`  // ntfy: servidor sin topic
	URL        string `json:"url,omitempty"`     // gotify / webhook
	ChatID     string `json:"chat_id,omitempty"` // telegram
	Host       string `json:"host,omitempty"`    // syslog / email
	Port       int    `json:"port,omitempty"`
	Proto      string `json:"proto,omitempty"`
	Facility   int    `json:"facility,omitempty"`
	User       string `json:"user,omitempty"`       // email
	From       string `json:"from,omitempty"`       // email
	Encryption string `json:"encryption,omitempty"` // email
	TokenSet   bool   `json:"token_set,omitempty"`  // hay secreto guardado
	TopicSet   bool   `json:"topic_set,omitempty"`  // ntfy
	Editable   bool   `json:"editable"`
}

// channelPatch — campos editables (punteros: ausente = no tocar).
// Los secretos (token/pass) y la URL de ntfy son write-only: vacío conserva.
type channelPatch struct {
	URL        *string `json:"url"`
	Token      *string `json:"token"`
	ClearToken bool    `json:"clear_token"`
	ChatID     *string `json:"chat_id"`
	Host       *string `json:"host"`
	Port       *int    `json:"port"`
	Proto      *string `json:"proto"`
	Facility   *int    `json:"facility"`
	User       *string `json:"user"`
	Pass       *string `json:"pass"`
	ClearPass  bool    `json:"clear_pass"`
	From       *string `json:"from"`
	Encryption *string `json:"encryption"`
}

// telegramTokenRe — formato del token de @BotFather: <id>:<secreto>.
var telegramTokenRe = regexp.MustCompile(`^\d+:[A-Za-z0-9_-]{10,}$`)

// testableChannel — canales que admiten prueba de envío. El webhook no: su
// entrega es asíncrona (cola + DLQ) y no tiene un resultado síncrono que mostrar.
func testableChannel(name string) bool {
	switch name {
	case "ntfy", "gotify", "telegram", "syslog", "email":
		return true
	}
	return false
}

// editableChannel — canales configurables desde la UI.
func editableChannel(name string) bool {
	return testableChannel(name) || name == "webhook"
}

// getChannels — GET /api/channels (admin): estado de cada canal. Nunca expone
// secretos (tokens, contraseña SMTP, topic de ntfy).
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
		"email": {
			Configured: cfg.SMTPHost != "" && cfg.SMTPFrom != "",
			Host:       cfg.SMTPHost,
			Port:       cfg.SMTPPort,
			User:       cfg.SMTPUser,
			From:       cfg.SMTPFrom,
			Encryption: cfg.SMTPEncryption,
			TokenSet:   cfg.SMTPPass != "",
			Editable:   true,
		},
	}
	// Webhook saliente: su URL vive en settings (BD).
	whURL := ""
	if s.settings != nil {
		if st, err := s.settings.Load(r.Context()); err == nil {
			whURL = st.Webhook
		}
	}
	out["webhook"] = channelInfo{Configured: whURL != "", URL: whURL, Editable: true}
	out["push"] = channelInfo{Configured: s.cfg.PushEnabled()}
	writeJSON(w, http.StatusOK, map[string]any{"channels": out})
}

// putChannel — PUT /api/channels/{name} (admin): guarda la config del canal y
// la aplica en caliente (sin reiniciar). Valida antes de persistir.
func (s *Server) putChannel(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !editableChannel(name) {
		writeErr(w, http.StatusNotFound, "unknown_channel", "canal desconocido: "+name)
		return
	}
	var p channelPatch
	if !decodeJSON(w, r, &p) {
		return
	}

	// El webhook vive en settings, no en la config de canales.
	if name == "webhook" {
		s.putWebhook(w, r, p)
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
	case "email":
		if p.Host != nil {
			cfg.SMTPHost = strings.TrimSpace(*p.Host)
		}
		if p.Port != nil {
			cfg.SMTPPort = *p.Port
		}
		if p.User != nil {
			cfg.SMTPUser = strings.TrimSpace(*p.User)
		}
		if p.From != nil {
			cfg.SMTPFrom = strings.TrimSpace(*p.From)
		}
		if p.Encryption != nil {
			cfg.SMTPEncryption = strings.TrimSpace(*p.Encryption)
		}
		applyValue(&cfg.SMTPPass, p.Pass, p.ClearPass)
		if cfg.SMTPHost != "" && cfg.SMTPPort == 0 {
			cfg.SMTPPort = 587
		}
		if cfg.SMTPHost != "" && cfg.SMTPEncryption == "" {
			cfg.SMTPEncryption = "starttls"
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
	if name == "email" {
		s.applyMailer(cfg)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "channel": name})
}

// putWebhook — guarda la URL del webhook saliente en settings (aplica sin
// reiniciar: el notifier la lee en cada envío).
func (s *Server) putWebhook(w http.ResponseWriter, r *http.Request, p channelPatch) {
	raw := ""
	if p.URL != nil {
		raw = strings.TrimSpace(*p.URL)
	}
	if raw != "" {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			writeErr(w, http.StatusBadRequest, "invalid_url", "la URL del webhook debe ser http(s)://…")
			return
		}
	}
	st, err := s.settings.Load(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "load_failed", "no se pudieron leer los ajustes")
		return
	}
	st.Webhook = raw
	if err := s.settings.Save(r.Context(), st); err != nil {
		log.Printf("channels: guardar webhook: %v", err)
		writeErr(w, http.StatusInternalServerError, "save_failed", "no se pudo guardar la configuración")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "channel": "webhook"})
}

// applyMailer — recrea el cliente SMTP con la config nueva y lo engancha al
// alerter (nil si el canal queda incompleto). Cierra el anterior.
func (s *Server) applyMailer(cfg channels.Config) {
	if old := s.mailer; old != nil {
		_ = old.Close()
	}
	s.mailer = MailerFromConfig(cfg)
	if s.alerter != nil {
		s.alerter.SetEmail(s.mailer)
	}
}

// mailerFromConfig — construye el cliente SMTP desde la config de canales.
func MailerFromConfig(cfg channels.Config) *notifier.Mailer {
	if cfg.SMTPHost == "" || cfg.SMTPFrom == "" {
		return nil
	}
	m, err := notifier.NewMailer(notifier.SMTP{
		Host: cfg.SMTPHost, Port: cfg.SMTPPort, User: cfg.SMTPUser, Pass: cfg.SMTPPass,
		From: cfg.SMTPFrom, Encryption: cfg.SMTPEncryption, Timeout: 10 * time.Second,
	})
	if err != nil {
		log.Printf("channels: cliente SMTP inválido: %v", err)
		return nil
	}
	return m
}

// deleteChannel — DELETE /api/channels/{name} (admin): desactiva el canal.
func (s *Server) deleteChannel(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !editableChannel(name) {
		writeErr(w, http.StatusNotFound, "unknown_channel", "canal desconocido: "+name)
		return
	}
	if name == "webhook" {
		s.putWebhook(w, r, channelPatch{URL: strPtr("")})
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
	case "email":
		cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPUser, cfg.SMTPPass, cfg.SMTPFrom, cfg.SMTPEncryption = "", 0, "", "", "", ""
	}
	if err := s.channelStore.Save(r.Context(), cfg); err != nil {
		log.Printf("channels: desactivar %s: %v", name, err)
		writeErr(w, http.StatusInternalServerError, "save_failed", "no se pudo guardar la configuración")
		return
	}
	s.channels.Apply(cfg)
	if name == "email" {
		s.applyMailer(cfg)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "channel": name})
}

// strPtr — puntero a string (para reutilizar putWebhook al desactivar).
func strPtr(s string) *string { return &s }

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
	case "email":
		host, from := cfg.SMTPHost != "", cfg.SMTPFrom != ""
		if !host && !from {
			return "", "" // desactivado
		}
		if host != from {
			return "el email requiere servidor SMTP Y remitente (o ninguno de los dos)", "incomplete"
		}
		if cfg.SMTPPort < 1 || cfg.SMTPPort > 65535 {
			return "el puerto SMTP debe estar entre 1 y 65535", "invalid_port"
		}
		switch cfg.SMTPEncryption {
		case "none", "starttls", "tls":
		default:
			return "el cifrado SMTP debe ser none, starttls o tls", "invalid_encryption"
		}
	}
	return "", ""
}

// testChannel — POST /api/channels/{name}/test (admin): envía una notificación
// de prueba por el canal indicado. 400 si el canal no está configurado (no se
// finge un éxito), 502 si el destino falla (mensaje ya redactado).
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

	// Email: la prueba va al correo del admin que la lanza.
	if name == "email" {
		u, err := s.users.Get(r.Context(), auth.UserFromContext(r.Context()))
		if err != nil || u.Email == "" {
			writeErr(w, http.StatusBadRequest, "no_recipient",
				"tu usuario no tiene email configurado en Mi perfil: la prueba necesita un destinatario")
			return
		}
		if s.mailer == nil {
			writeErr(w, http.StatusBadRequest, "channel_not_configured", "el canal email no está configurado")
			return
		}
		if err := s.mailer.Send(ctx, []string{u.Email}, lang,
			notifier.Alert{Level: "info", Source: "test", Target: "settings", Timestamp: time.Now()},
			title, body); err != nil {
			log.Printf("channels: prueba de email falló: %v", err)
			writeErr(w, http.StatusBadGateway, "channel_test_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "channel": name})
		return
	}

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
