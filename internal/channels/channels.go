// Package channels — canales de alerta adicionales (#86, #134): ntfy, Gotify,
// Telegram y Syslog. Cada canal es inerte si no está configurado. La
// configuración vive en BD (editable desde Ajustes sin reiniciar) y se lee en
// cada envío; el env solo siembra la primera vez.
//
// Reglas de seguridad (lecciones de NetPulse #773 / NetGrip #298):
//   - Los secretos viajan en la URL de ntfy (el topic) y de Telegram (el bot
//     token): un error de red de *url.Error incluiría la URL completa, así que
//     SIEMPRE se redacta el secreto antes de loguear.
//   - Los 4xx permanentes no se reintentan; 429/5xx y fallos de red sí (1
//     reintento), porque una alerta de pool DEGRADED no puede perderse por un
//     corte puntual.
//   - El texto se trunca por límite de runas (nunca a mitad de UTF-8).
package channels

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrNotConfigured — el canal pedido no tiene configuración mínima.
var ErrNotConfigured = errors.New("canal no configurado")

// Config — configuración de los canales (BD; el env siembra la primera vez).
// Los tokens nunca se exponen en la API.
type Config struct {
	NtfyURL   string `json:"ntfy_url,omitempty"`
	NtfyToken string `json:"ntfy_token,omitempty"`

	GotifyURL   string `json:"gotify_url,omitempty"`
	GotifyToken string `json:"gotify_token,omitempty"`

	TelegramBotToken string `json:"telegram_bot_token,omitempty"`
	TelegramChatID   string `json:"telegram_chat_id,omitempty"`

	SyslogHost     string `json:"syslog_host,omitempty"`
	SyslogPort     int    `json:"syslog_port,omitempty"`
	SyslogProto    string `json:"syslog_proto,omitempty"`
	SyslogFacility int    `json:"syslog_facility,omitempty"`

	// Canal email (SMTP), editable desde Ajustes. La contraseña es write-only.
	SMTPHost       string `json:"smtp_host,omitempty"`
	SMTPPort       int    `json:"smtp_port,omitempty"`
	SMTPUser       string `json:"smtp_user,omitempty"`
	SMTPPass       string `json:"smtp_pass,omitempty"`
	SMTPFrom       string `json:"smtp_from,omitempty"`
	SMTPEncryption string `json:"smtp_encryption,omitempty"`
}

// Client — conjunto de canales. La configuración es dinámica (Apply) para que
// los cambios desde Ajustes entren en vigor sin reiniciar el servicio.
type Client struct {
	mu  sync.RWMutex
	cfg Config

	http         *http.Client
	telegramBase string // base de la Bot API (inyectable en tests)
}

// New construye el cliente con la configuración inicial.
func New(cfg Config) *Client {
	return &Client{
		cfg:          cfg,
		http:         &http.Client{Timeout: 10 * time.Second},
		telegramBase: telegramAPIBase,
	}
}

// Apply reemplaza la configuración en caliente (tras guardar en Ajustes).
func (c *Client) Apply(cfg Config) {
	c.mu.Lock()
	c.cfg = cfg
	c.mu.Unlock()
}

// Config devuelve una copia de la configuración actual.
func (c *Client) Config() Config {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cfg
}

// telegramReady — Telegram necesita token Y chat id.
func telegramReady(cfg Config) bool {
	return cfg.TelegramBotToken != "" && cfg.TelegramChatID != ""
}

// Enabled — ¿hay al menos un canal configurado?
func (c *Client) Enabled() bool {
	if c == nil {
		return false
	}
	cfg := c.Config()
	return cfg.NtfyURL != "" || cfg.GotifyURL != "" || cfg.SyslogHost != "" || telegramReady(cfg)
}

// Configured — ¿el canal indicado tiene configuración mínima? Nombres válidos:
// ntfy, gotify, telegram, syslog. Cualquier otro devuelve false.
func (c *Client) Configured(name string) bool {
	if c == nil {
		return false
	}
	cfg := c.Config()
	switch name {
	case "ntfy":
		return cfg.NtfyURL != ""
	case "gotify":
		return cfg.GotifyURL != ""
	case "telegram":
		return telegramReady(cfg)
	case "syslog":
		return cfg.SyslogHost != ""
	case "email":
		// El email no lo envía este paquete (lo hace el alerter con su
		// Mailer), pero la UI consulta su estado aquí.
		return cfg.SMTPHost != "" && cfg.SMTPFrom != ""
	default:
		return false
	}
}

// Send entrega la alerta a todos los canales configurados (best-effort).
// El contexto lleva timeout; cada canal se envía en serie con su propio
// límite. Los fallos se loguean (redactados) y no propagan.
func (c *Client) Send(ctx context.Context, title, body string) {
	if c == nil {
		return
	}
	cfg := c.Config()
	if err := c.sendNtfy(ctx, cfg, title, body); err != nil {
		log.Printf("channels: ntfy: %v", err)
	}
	if err := c.sendGotify(ctx, cfg, title, body); err != nil {
		log.Printf("channels: gotify: %v", err)
	}
	if err := c.sendTelegram(ctx, cfg, title, body); err != nil {
		log.Printf("channels: telegram: %v", err)
	}
	if err := c.sendSyslog(ctx, cfg, title, body); err != nil {
		log.Printf("channels: syslog: %v", err)
	}
}

// Test envía un mensaje de prueba por el canal indicado y devuelve el error
// real (sin loguear) para que el endpoint HTTP pueda informar.
func (c *Client) Test(ctx context.Context, name, title, body string) error {
	if c == nil {
		return ErrNotConfigured
	}
	cfg := c.Config()
	switch name {
	case "ntfy":
		return c.sendNtfy(ctx, cfg, title, body)
	case "gotify":
		return c.sendGotify(ctx, cfg, title, body)
	case "telegram":
		return c.sendTelegram(ctx, cfg, title, body)
	case "syslog":
		return c.sendSyslog(ctx, cfg, title, body)
	default:
		return fmt.Errorf("canal desconocido: %q", name)
	}
}

// --- ntfy ---

// sendNtfy — POST JSON a la URL del topic con Authorization Bearer si hay token.
func (c *Client) sendNtfy(ctx context.Context, cfg Config, title, body string) error {
	if cfg.NtfyURL == "" {
		return nil
	}
	payload, err := json.Marshal(map[string]string{
		"title":   title,
		"message": truncateRunes(body, maxMessageRunes),
	})
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.NtfyURL, bytes.NewReader(payload))
	if err != nil {
		return redactErr(err, topicOf(cfg.NtfyURL))
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.NtfyToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.NtfyToken)
	}
	// El topic (secreto) va en la URL: redactarlo en cualquier error.
	return c.doRetry(req, topicOf(cfg.NtfyURL))
}

// --- gotify ---

// sendGotify — POST JSON a <url>/message con X-Gotify-Key.
func (c *Client) sendGotify(ctx context.Context, cfg Config, title, body string) error {
	if cfg.GotifyURL == "" {
		return nil
	}
	payload, err := json.Marshal(map[string]string{
		"title":    title,
		"message":  truncateRunes(body, maxMessageRunes),
		"priority": "5",
	})
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	// El token viaja en cabecera, no en la URL: sin secreto que redactar.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(cfg.GotifyURL, "/")+"/message", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Gotify-Key", cfg.GotifyToken)
	return c.doRetry(req, "")
}

// --- telegram ---

// telegramAPIBase — API pública de Telegram (Bot API).
const telegramAPIBase = "https://api.telegram.org"

// maxMessageRunes — límite de la Bot API (sendMessage: 4096 caracteres) y de
// ntfy.sh por defecto; se trunca por runas para no partir UTF-8.
const maxMessageRunes = 4096

// sendTelegram — POST a /bot<token>/sendMessage con chat_id y texto.
// El token va en la URL: se redacta en cualquier error.
func (c *Client) sendTelegram(ctx context.Context, cfg Config, title, body string) error {
	if !telegramReady(cfg) {
		return nil
	}
	text := truncateRunes(strings.TrimSpace(title+"\n"+body), maxMessageRunes)
	payload, err := json.Marshal(map[string]any{
		"chat_id":                  cfg.TelegramChatID,
		"text":                     text,
		"disable_web_page_preview": true,
	})
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	endpoint := c.telegramBase + "/bot" + cfg.TelegramBotToken + "/sendMessage"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return redactErr(err, cfg.TelegramBotToken)
	}
	req.Header.Set("Content-Type", "application/json")
	return c.doRetry(req, cfg.TelegramBotToken)
}

// --- syslog ---

// sendSyslog — datagrama RFC 3164 (PRI + timestamp + host + texto) por UDP/TCP.
func (c *Client) sendSyslog(ctx context.Context, cfg Config, title, body string) error {
	if cfg.SyslogHost == "" {
		return nil
	}
	// facility*8 + severity(1=notice); fallback 14 (1*8+1=9 → user.notice).
	pri := cfg.SyslogFacility*8 + 1
	msg := fmt.Sprintf("<%d>%s EasyZFS[%d]: %s: %s",
		pri, time.Now().Format("Jan _2 15:04:05"), 0, title, truncateRunes(body, maxMessageRunes))
	addr := net.JoinHostPort(cfg.SyslogHost, strconv.Itoa(cfg.SyslogPort))
	if cfg.SyslogProto == "tcp" {
		var d net.Dialer
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return err
		}
		defer conn.Close()
		if _, err := conn.Write([]byte(msg + "\n")); err != nil {
			return err
		}
		return nil
	}
	// UDP: dial y close por envío (sin conexión persistente).
	conn, err := net.Dial("udp", addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.Write([]byte(msg + "\n")); err != nil {
		return err
	}
	return nil
}

// --- entrega con reintento acotado ---

// maxIntentos — 1 envío + 1 reintento (solo para fallos transitorios).
const maxIntentos = 2

// statusError — respuesta HTTP no-2xx (código + cuerpo acotado).
type statusError struct {
	code int
	body string
}

func (e *statusError) Error() string {
	if e.body == "" {
		return fmt.Sprintf("status %d", e.code)
	}
	return fmt.Sprintf("status %d: %s", e.code, e.body)
}

// doRetry ejecuta la petición; reintenta UNA vez si el fallo es transitorio
// (429, 5xx o error de red) y el contexto sigue vivo. El secreto se redacta
// en el error devuelto.
func (c *Client) doRetry(req *http.Request, secret string) error {
	backoff := 1 * time.Second
	var lastErr error
	for intento := 1; ; intento++ {
		lastErr = c.do(req, secret)
		if lastErr == nil || intento >= maxIntentos || !retryable(lastErr) {
			return lastErr
		}
		// Reusar el cuerpo en el reintento (NewRequest rellena GetBody para
		// bytes.Reader, así que se puede recomponer).
		if req.GetBody != nil {
			body, err := req.GetBody()
			if err != nil {
				return lastErr
			}
			req.Body = body
		}
		select {
		case <-req.Context().Done():
			return lastErr
		case <-time.After(backoff):
		}
		backoff *= 2
	}
}

// retryable — ¿merece la pena reintentar? 429/5xx y fallos de red sí; 4xx
// permanentes y contexto cancelado/agotado no.
func retryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var se *statusError
	if errors.As(err, &se) {
		return se.code == http.StatusTooManyRequests || se.code >= 500
	}
	return true // error de transporte (timeout, DNS, conexión): transitorio
}

// do ejecuta la petición y verifica 2xx; el cuerpo se cierra siempre. El
// secreto se usa para redactar los errores (nunca se loguea la URL cruda).
func (c *Client) do(req *http.Request, secret string) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return redactErr(err, secret)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		// Cuerpo acotado: en Telegram trae la "description" del fallo
		// (chat not found, Unauthorized…), útil para el botón Probar.
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return redactErr(&statusError{code: resp.StatusCode, body: strings.TrimSpace(string(b))}, secret)
	}
	return nil
}

// redactErr — sustituye el secreto por "***" en errores de red y de status.
// Los *url.Error de net/http incluyen la URL completa (topic de ntfy, token de
// Telegram): sin esto, el secreto acabaría en el log.
func redactErr(err error, secret string) error {
	if err == nil || secret == "" {
		return err
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		clean := *ue
		clean.URL = strings.ReplaceAll(ue.URL, secret, "***")
		if ue.Err != nil {
			clean.Err = errors.New(strings.ReplaceAll(ue.Err.Error(), secret, "***"))
		}
		return &clean
	}
	var se *statusError
	if errors.As(err, &se) {
		return &statusError{code: se.code, body: strings.ReplaceAll(se.body, secret, "***")}
	}
	if strings.Contains(err.Error(), secret) {
		return errors.New(strings.ReplaceAll(err.Error(), secret, "***"))
	}
	return err
}

// topicOf — último segmento del path de una URL de ntfy (el topic), para
// redactarlo sin ocultar el servidor. Vacío si no se puede derivar.
func topicOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	t := path.Base(u.Path)
	if t == "/" || t == "." {
		return ""
	}
	return t
}

// NtfyServer — servidor de una URL de ntfy sin el topic (para la API: el topic
// es la contraseña y no se expone).
func NtfyServer(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// truncateRunes — recorta a max runas (no bytes) y añade elipsis.
func truncateRunes(s string, max int) string {
	if max <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}
