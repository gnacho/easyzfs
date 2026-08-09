// Package notifier — canal de notificaciones por EMAIL (S5) para alertas.
//
// SMTP configurado por variables de entorno (nunca hardcodeado ni en BD):
// SMTP_HOST, SMTP_PORT, SMTP_USER, SMTP_PASS, SMTP_FROM, SMTP_ENCRYPTION,
// SMTP_TIMEOUT y opcional SMTP_TEST_TO (fuerza un destino de prueba).
// Plantillas ES/EN embebidas (texto + HTML multipart/alternative).
//
// Send NUNCA devuelve error al llamador de la alerta: es best-effort, igual
// que push y webhook (la alerta ya está en BD y emitida por SSE).
package notifier

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"html/template"
	"log"
	"strings"
	"time"

	"github.com/wneessen/go-mail"

	"easyzfs/internal/config"
)

// Alert — datos de la alerta a renderizar en el correo.
type Alert struct {
	Level     string // "info" | "warn" | "crit"
	Source    string // "pool.tank", "disk.sda"…
	Target    string // "pools:tank", "disks:sda"…
	Timestamp time.Time
}

// color de cabecera del HTML según severidad (mismas que la UI).
func colorFor(level string) string {
	switch level {
	case "crit":
		return "#b3261e"
	case "warn":
		return "#9e5f00"
	default:
		return "#0e7a55"
	}
}

// severityLabel — "crítica"/"aviso"/"info" en el idioma del destinatario.
func severityLabel(lang, level string) string {
	switch level {
	case "crit":
		if lang == "es" {
			return "crítica"
		}
		return "critical"
	case "warn":
		if lang == "es" {
			return "aviso"
		}
		return "warning"
	default:
		return "info"
	}
}

type templateData struct {
	Title     string
	Severity  string
	Summary   string
	Target    string
	Timestamp string
	Color     string
}

// Mailer envía correos SMTP reutilizando una conexión.
type Mailer struct {
	from   string
	testTo string
	client *mail.Client
}

// SMTP — configuración validada del canal email.
type SMTP struct {
	Host       string
	Port       int
	User       string
	Pass       string
	From       string
	Encryption string // none | starttls | tls
	Timeout    time.Duration
	TestTo     string
}

// FromConfig extrae la config SMTP del config de la app.
func FromConfig(c *config.Config) SMTP {
	return SMTP{
		Host: c.SMTPHost, Port: c.SMTPPort, User: c.SMTPUser, Pass: c.SMTPPass,
		From: c.SMTPFrom, Encryption: c.SMTPEncryption, Timeout: c.SMTPTimeout,
		TestTo: c.SMTPTestTo,
	}
}

// Validate comprueba que el canal está operativo (host + from).
func (s SMTP) Validate() error {
	if s.Host == "" {
		return fmt.Errorf("SMTP_HOST no definido")
	}
	if s.From == "" {
		return fmt.Errorf("SMTP_FROM no definido")
	}
	return nil
}

func tlsPolicy(enc string) mail.TLSPolicy {
	switch strings.ToLower(enc) {
	case "tls":
		return mail.TLSMandatory
	case "none":
		return mail.NoTLS
	default:
		return mail.TLSOpportunistic
	}
}

// NewMailer crea el cliente SMTP (conexión perezosa al primer envío).
func NewMailer(s SMTP) (*Mailer, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	opts := []mail.Option{
		mail.WithPort(s.Port),
		mail.WithTLSPolicy(tlsPolicy(s.Encryption)),
		mail.WithTimeout(s.Timeout),
	}
	if s.User != "" {
		opts = append(opts,
			mail.WithSMTPAuth(mail.SMTPAuthPlain),
			mail.WithUsername(s.User),
			mail.WithPassword(s.Pass),
		)
	}
	client, err := mail.NewClient(s.Host, opts...)
	if err != nil {
		return nil, err
	}
	return &Mailer{from: s.From, testTo: s.TestTo, client: client}, nil
}

// Close cierra la conexión SMTP reutilizada.
func (m *Mailer) Close() error {
	if m == nil || m.client == nil {
		return nil
	}
	return m.client.Close()
}

// Send renderiza el correo en el idioma pedido y lo envía. Best-effort para
// el llamador (log si falla), como push y webhook.
func (m *Mailer) Send(ctx context.Context, to []string, lang string, a Alert, title, summary string) error {
	if m == nil {
		return nil
	}
	dest := to
	prefix := ""
	if m.testTo != "" {
		dest = []string{m.testTo}
		prefix = "[TEST] "
	}
	htmlBody, textBody, err := render(lang, a, title, summary)
	if err != nil {
		return err
	}
	msg := mail.NewMsg()
	if err := msg.From(m.from); err != nil {
		return err
	}
	for _, d := range dest {
		if err := msg.To(d); err != nil {
			log.Printf("notifier: destinatario inválido %q: %v", d, err)
		}
	}
	msg.Subject(prefix + title)
	msg.SetBodyString(mail.TypeTextPlain, textBody)
	msg.AddAlternativeString(mail.TypeTextHTML, htmlBody)
	return m.client.DialAndSendWithContext(ctx, msg)
}

//go:embed templates/alert-es.html
var alertESHTML string

//go:embed templates/alert-en.html
var alertENHTML string

//go:embed templates/alert-es.txt
var alertESTxt string

//go:embed templates/alert-en.txt
var alertENTxt string

// render compone texto + HTML en el idioma pedido (fallback es).
func render(lang string, a Alert, title, summary string) (html, text string, err error) {
	if lang != "en" {
		lang = "es"
	}
	data := templateData{
		Title:     title,
		Severity:  severityLabel(lang, a.Level),
		Summary:   summary,
		Target:    a.Target,
		Timestamp: a.Timestamp.UTC().Format(time.RFC3339),
		Color:     colorFor(a.Level),
	}
	htmlTmpl, textTmpl := alertENHTML, alertENTxt
	if lang == "es" {
		htmlTmpl, textTmpl = alertESHTML, alertESTxt
	}
	if html, err = exec(htmlTmpl, data); err != nil {
		return "", "", err
	}
	if text, err = exec(textTmpl, data); err != nil {
		return "", "", err
	}
	return html, text, nil
}

func exec(src string, data any) (string, error) {
	t, err := template.New("alert").Parse(src)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}
