// Package webhook — notificador SALIENTE de alertas (paridad con el patrón
// canónico de NetPulse server-go/internal/webhook/webhook.go).
//
// Entrega a la URL configurada con firma HMAC-SHA256 del body, reintentos con
// backoff exponencial + jitter (4xx no se reintenta salvo 429; 429/5xx sí) y
// DLQ en la tabla webhook_events si se agotan los reintentos. El payload lleva
// event_id (id de la alerta) como clave de idempotencia para el receptor.
//
// Notify NUNCA bloquea al llamador: encola el evento y un worker único lo
// drena. Si la cola está llena se descarta (la alerta ya está en /api/alerts).
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"sync"
	"time"
)

const (
	queueCap   = 64
	maxBodyCap = 64 << 10 // 64 KB de respuesta del receptor (cap de seguridad)
)

// Config — parámetros del webhook, leídos una vez al arrancar (env como
// bootstrap; la URL se resuelve dinámicamente desde settings en cada envío).
type Config struct {
	Secret     string
	Timeout    time.Duration
	Retries    int
	RetryDelay time.Duration
}

// Event — alerta encolable para el webhook.
type Event struct {
	ID      int64
	Ts      time.Time
	Level   string
	Source  string
	Target  string
	Message string
}

// payload es el contrato del webhook (firmado): event_id para idempotencia
// del receptor + datos de la alerta.
type payload struct {
	EventID string `json:"event_id"`
	Ts      string `json:"ts"` // RFC3339 UTC
	Level   string `json:"level"`
	Source  string `json:"source"`
	Target  string `json:"target"`
	Message string `json:"message"`
}

// Notifier entrega alertas de forma asíncrona con cola acotada y DLQ.
type Notifier struct {
	cfg    Config
	db     *sql.DB
	getURL func() string // URL actual (settings); "" = webhook desactivado

	client *http.Client

	ch   chan Event
	done chan struct{}
	once sync.Once
	wg   sync.WaitGroup
}

// NewNotifier crea el Notifier y arranca su worker.
func NewNotifier(cfg Config, d *sql.DB, getURL func() string) *Notifier {
	n := &Notifier{
		cfg:    cfg,
		db:     d,
		getURL: getURL,
		client: &http.Client{Timeout: cfg.Timeout},
		ch:     make(chan Event, queueCap),
		done:   make(chan struct{}),
	}
	n.wg.Add(1)
	go n.worker()
	return n
}

// Notify encola el evento y vuelve inmediatamente.
func (n *Notifier) Notify(ev Event) {
	select {
	case <-n.done:
		return
	default:
	}
	select {
	case n.ch <- ev:
	default:
		log.Printf("webhook: cola llena, evento de alerta %d descartado", ev.ID)
	}
}

// Close para el worker (los eventos en curso terminan; la cola restante se
// descarta: la alerta ya está en /api/alerts).
func (n *Notifier) Close() {
	n.once.Do(func() {
		close(n.done)
		n.wg.Wait()
	})
}

func (n *Notifier) worker() {
	defer n.wg.Done()
	for {
		select {
		case <-n.done:
			return
		case ev := <-n.ch:
			n.sendWithRetry(ev)
		}
	}
}

// payloadJSON construye el body firmable de un evento.
func payloadJSON(ev Event) ([]byte, error) {
	return json.Marshal(payload{
		EventID: fmt.Sprintf("%d", ev.ID),
		Ts:      ev.Ts.UTC().Format(time.RFC3339),
		Level:   ev.Level,
		Source:  ev.Source,
		Target:  ev.Target,
		Message: ev.Message,
	})
}

// sign calcula la firma HMAC-SHA256 del body crudo.
func sign(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// sendWithRetry envía con backoff exponencial + jitter. 4xx (salvo 429) no se
// reintenta; 429/5xx sí. Al agotar reintentos guarda en DLQ.
func (n *Notifier) sendWithRetry(ev Event) {
	url := n.getURL()
	if url == "" {
		return // webhook desactivado en settings
	}
	body, err := payloadJSON(ev)
	if err != nil {
		log.Printf("webhook: payload no serializable: %v", err)
		return
	}
	var lastErr error
	for attempt := 0; attempt <= n.cfg.Retries; attempt++ {
		err = n.sendOnce(url, ev.ID, body)
		if err == nil {
			return
		}
		lastErr = err
		var retryable bool
		if httpErr, ok := err.(*httpStatusError); ok {
			// 4xx (salvo 429): error del emisor, no reintentar jamás.
			if httpErr.status >= 400 && httpErr.status < 500 && httpErr.status != http.StatusTooManyRequests {
				log.Printf("webhook: %d permanente en alerta %d, no se reintenta", httpErr.status, ev.ID)
				n.saveDLQ(ev, body, lastErr.Error())
				return
			}
			retryable = true
		}
		if attempt < n.cfg.Retries && retryable {
			jitter := time.Duration(rand.Int63n(int64(n.cfg.RetryDelay)))
			backoff := n.cfg.RetryDelay * time.Duration(1<<attempt)
			time.Sleep(backoff + jitter)
		}
	}
	n.saveDLQ(ev, body, lastErr.Error())
}

// httpStatusError marca la respuesta HTTP para la decisión de reintento.
type httpStatusError struct {
	status int
	body   string
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("webhook status %d: %s", e.status, e.body)
}

func (n *Notifier) sendOnce(url string, eventID int64, body []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), n.cfg.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "EasyZFS-Webhook/2.0")
	if n.cfg.Secret != "" {
		req.Header.Set("X-EasyZFS-Signature", sign(body, n.cfg.Secret))
	}
	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxBodyCap))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return &httpStatusError{status: resp.StatusCode, body: string(respBody)}
}

// saveDLQ persiste el evento no entregado para diagnóstico posterior.
func (n *Notifier) saveDLQ(ev Event, body []byte, reason string) {
	if n.db == nil {
		return
	}
	if _, err := n.db.Exec(
		"INSERT OR REPLACE INTO webhook_events (event_id, payload, sent_at, error) VALUES (?, ?, ?, ?)",
		fmt.Sprintf("%d", ev.ID), string(body), time.Now().UTC().Format(time.RFC3339), reason,
	); err != nil {
		log.Printf("webhook: no se pudo guardar en DLQ: %v", err)
		return
	}
	log.Printf("webhook: alerta %d a DLQ (%s)", ev.ID, reason)
}
