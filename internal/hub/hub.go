// Package hub — broker SSE: suscriptores + broadcast + heartbeat 25 s.
// Reglas del skill: heartbeat ":ping" cada 25 s, X-Accel-Buffering: no,
// publicar solo cuando cambia el valor, nunca bloquear al publicador.
package hub

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

// heartbeatInterval — lección 2: sin heartbeat los proxies cortan a los ~60 s.
const heartbeatInterval = 25 * time.Second

// Event — un evento SSE (event: Name, data: JSON de Data).
type Event struct {
	Name string
	Data any
}

// Hub — broker con suscriptores; tolerante a clientes lentos (drop, no bloqueo).
// Cada suscriptor lleva el userID de su sesión (para UserActive: saber si un
// usuario tiene la app abierta y no duplicar avisos SSE + push).
type Hub struct {
	mu     sync.Mutex
	subs   map[chan Event]string // canal → userID ("" si anónimo/desconocido)
	closed bool
}

// NewHub crea el broker.
func NewHub() *Hub {
	return &Hub{subs: make(map[chan Event]string)}
}

// Publish envía el evento a todos los suscriptores sin bloquear (drop si el buffer está lleno).
func (h *Hub) Publish(name string, data any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	ev := Event{Name: name, Data: data}
	for ch := range h.subs {
		select {
		case ch <- ev:
		default: // cliente lento: se pierde el evento, no se bloquea el colector
		}
	}
}

// Subscribe registra un nuevo suscriptor (buffer 32) con su userID (puede ser "").
func (h *Hub) Subscribe(userID string) chan Event {
	ch := make(chan Event, 32)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		close(ch)
		return ch
	}
	h.subs[ch] = userID
	return ch
}

// SubscriberCount devuelve el numero de conexiones SSE activas (UI abierta).
func (h *Hub) SubscriberCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

// UserActive — ¿tiene este usuario alguna conexión SSE abierta (app abierta)?
func (h *Hub) UserActive(userID string) bool {
	if userID == "" {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, uid := range h.subs {
		if uid == userID {
			return true
		}
	}
	return false
}

// Unsubscribe da de baja un suscriptor.
func (h *Hub) Unsubscribe(ch chan Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.subs, ch)
}

// Close drena y cierra todos los clientes (graceful shutdown).
func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	for ch := range h.subs {
		close(ch)
	}
	h.subs = make(map[chan Event]string)
}

// ServeHTTP implementa GET /api/events (stream text/event-stream) sin usuario.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.ServeSSE(w, r, "")
}

// ServeSSE — GET /api/events con el userID de la sesión autenticada (lo
// inyecta el middleware de auth y lo pasa httpapi al montar la ruta).
func (h *Hub) ServeSSE(w http.ResponseWriter, r *http.Request, userID string) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error":"streaming","message":"SSE no soportado"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // por si hay proxy delante

	ch := h.Subscribe(userID)
	defer h.Unsubscribe(ch)

	// Anuncio inicial + flush inmediato para que el cliente confirme el stream.
	fmt.Fprintf(w, ":ok\n\n")
	fl.Flush()

	hb := time.NewTicker(heartbeatInterval)
	defer hb.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-ch:
			if !ok { // hub cerrado: evento de despedida y fin
				fmt.Fprintf(w, "event: bye\ndata: {}\n\n")
				fl.Flush()
				return
			}
			raw, err := json.Marshal(ev.Data)
			if err != nil {
				log.Printf("sse: marshal %s: %v", ev.Name, err)
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Name, raw)
			fl.Flush()
		case <-hb.C:
			fmt.Fprintf(w, ":ping\n\n")
			fl.Flush()
		}
	}
}
