// Package channels — canales de alerta adicionales (#86): ntfy, Gotify y
// Syslog. Cada canal es inerte si no está configurado (env). Envíos
// best-effort con timeout acotado; los fallos se loguean y no rompen nada.
package channels

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"
)

// Client — conjunto de canales configurados. Los nil no envían.
type Client struct {
	ntfyURL   string
	ntfyToken string

	gotifyURL   string
	gotifyToken string

	syslogHost     string
	syslogPort     int
	syslogProto    string
	syslogFacility int

	http *http.Client
}

// New construye el cliente a partir de la configuración. Solo registra los
// canales con los datos mínimos; el resto queda nil (inactivo).
func New(ntfyURL, ntfyToken, gotifyURL, gotifyToken string,
	syslogHost string, syslogPort int, syslogProto string, syslogFacility int) *Client {
	return &Client{
		ntfyURL:        ntfyURL,
		ntfyToken:      ntfyToken,
		gotifyURL:      gotifyURL,
		gotifyToken:    gotifyToken,
		syslogHost:     syslogHost,
		syslogPort:     syslogPort,
		syslogProto:    syslogProto,
		syslogFacility: syslogFacility,
		http:           &http.Client{Timeout: 10 * time.Second},
	}
}

// Enabled — ¿hay al menos un canal configurado?
func (c *Client) Enabled() bool {
	return c != nil && (c.ntfyURL != "" || c.gotifyURL != "" || c.syslogHost != "")
}

// Send entrega la alerta a todos los canales configurados (best-effort).
// El contexto lleva timeout; cada canal se envía en serie con su propio
// límite. Compose recibe el texto ya compuesto (título + cuerpo).
func (c *Client) Send(ctx context.Context, title, body string) {
	if c == nil {
		return
	}
	if c.ntfyURL != "" {
		c.sendNtfy(ctx, title, body)
	}
	if c.gotifyURL != "" {
		c.sendGotify(ctx, title, body)
	}
	if c.syslogHost != "" {
		c.sendSyslog(ctx, title, body)
	}
}

// sendNtfy — POST JSON a NTFY_URL con Authorization Bearer si hay token.
// Formato: {"topic":…} se envía a la URL base del topic directamente:
// la URL configurada es el topic completo (p.ej. https://ntfy.sh/mialerta).
func (c *Client) sendNtfy(ctx context.Context, title, body string) {
	payload, err := json.Marshal(map[string]string{
		"title":   title,
		"message": body,
	})
	if err != nil {
		log.Printf("channels: ntfy: marshal: %v", err)
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.ntfyURL, bytes.NewReader(payload))
	if err != nil {
		log.Printf("channels: ntfy: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if c.ntfyToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.ntfyToken)
	}
	if err := c.do(req); err != nil {
		log.Printf("channels: ntfy: %v", err)
	}
}

// sendGotify — POST JSON a GOTIFY_URL/message con X-Gotify-Key.
func (c *Client) sendGotify(ctx context.Context, title, body string) {
	payload, err := json.Marshal(map[string]string{
		"title":    title,
		"message":  body,
		"priority": "5",
	})
	if err != nil {
		log.Printf("channels: gotify: marshal: %v", err)
		return
	}
	url := c.gotifyURL + "/message"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		log.Printf("channels: gotify: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Gotify-Key", c.gotifyToken)
	if err := c.do(req); err != nil {
		log.Printf("channels: gotify: %v", err)
	}
}

// sendSyslog — datagrama RFC 3164 (PRI + timestamp + host + texto) por UDP/TCP.
func (c *Client) sendSyslog(ctx context.Context, title, body string) {
	// facility*8 + severity(1=notice); fallback 14 (1*8+1=9 → user.notice).
	pri := c.syslogFacility*8 + 1
	msg := fmt.Sprintf("<%d>%s EasyZFS[%d]: %s: %s",
		pri, time.Now().Format("Jan _2 15:04:05"), 0, title, body)
	addr := fmt.Sprintf("%s:%d", c.syslogHost, c.syslogPort)
	if c.syslogProto == "tcp" {
		var d net.Dialer
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			log.Printf("channels: syslog tcp: %v", err)
			return
		}
		defer conn.Close()
		if _, err := conn.Write([]byte(msg + "\n")); err != nil {
			log.Printf("channels: syslog tcp: %v", err)
		}
		return
	}
	// UDP: dial y close por envío (sin conexión persistente).
	conn, err := net.Dial("udp", addr)
	if err != nil {
		log.Printf("channels: syslog udp: %v", err)
		return
	}
	defer conn.Close()
	if _, err := conn.Write([]byte(msg + "\n")); err != nil {
		log.Printf("channels: syslog udp: %v", err)
	}
}

// do ejecuta la petición y verifica 2xx; el cuerpo se cierra siempre.
func (c *Client) do(req *http.Request) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}
