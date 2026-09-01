package hub

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestHubSubscribePublish(t *testing.T) {
	h := NewHub()
	ch := h.Subscribe("user1")
	defer h.Close()

	go h.Publish("test", map[string]string{"k": "v"})

	select {
	case ev := <-ch:
		if ev.Name != "test" {
			t.Errorf("evento esperado 'test', got %q", ev.Name)
		}
	case <-time.After(time.Second):
		t.Fatal("no se recibió el evento")
	}
}

func TestHubPublishNoBlock(t *testing.T) {
	h := NewHub()
	ch := h.Subscribe("u1")
	defer h.Close()
	// Buffer 32: Publish nunca bloquea aunque el suscriptor no lea.
	var published atomic.Bool
	go func() {
		for i := 0; i < 64; i++ {
			h.Publish("tick", i)
		}
		published.Store(true)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for !published.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !published.Load() {
		t.Fatal("Publish bloqueó con buffer lleno")
	}
	// Consumir un evento para que no se bloquee el cierre.
	select {
	case <-ch:
	default:
	}
}

func TestHubUnsubscribe(t *testing.T) {
	h := NewHub()
	ch := h.Subscribe("u1")
	h.Unsubscribe(ch)

	done := make(chan struct{})
	go func() {
		defer close(done)
		h.Publish("x", nil)
	}()
	select {
	case <-ch:
		t.Error("evento recibido tras unsubscribe")
	case <-done:
	}
}

func TestHubCloseIdempotent(t *testing.T) {
	h := NewHub()
	h.Close()
	h.Close() // sin panic
}

func TestHubCloseSignalsSubscribers(t *testing.T) {
	h := NewHub()
	ch := h.Subscribe("u1")
	h.Close()

	select {
	case _, ok := <-ch:
		if ok {
			t.Error("canal debería estar cerrado tras Close")
		}
	case <-time.After(time.Second):
		t.Fatal("canal no cerrado")
	}
}

func TestHubSubscriberCount(t *testing.T) {
	h := NewHub()
	defer h.Close()

	if h.SubscriberCount() != 0 {
		t.Fatalf("sin suscriptores esperaba 0, got %d", h.SubscriberCount())
	}
	ch1 := h.Subscribe("u1")
	ch2 := h.Subscribe("u2")
	if h.SubscriberCount() != 2 {
		t.Fatalf("con 2 suscriptores esperaba 2, got %d", h.SubscriberCount())
	}
	h.Unsubscribe(ch1)
	if h.SubscriberCount() != 1 {
		t.Fatalf("tras quitar uno esperaba 1, got %d", h.SubscriberCount())
	}
	h.Unsubscribe(ch2)
	if h.SubscriberCount() != 0 {
		t.Fatalf("sin suscriptores esperaba 0, got %d", h.SubscriberCount())
	}
}

func TestHubUserActive(t *testing.T) {
	h := NewHub()
	defer h.Close()
	if h.UserActive("u1") {
		t.Error("sin suscriptores no debería estar activo")
	}
	ch := h.Subscribe("u1")
	if !h.UserActive("u1") {
		t.Error("u1 debería estar activo")
	}
	h.Unsubscribe(ch)
	if h.UserActive("u1") {
		t.Error("tras unsubscribe no debería estar activo")
	}
}

func TestHubConcurrentSubPublishUnsubClose(t *testing.T) {
	h := NewHub()
	var wg sync.WaitGroup
	const n = 20
	channels := make([]chan Event, n)

	// Suscribir concurrentemente
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			channels[i] = h.Subscribe("")
		}(i)
	}
	wg.Wait()

	// Publicar y desuscribir concurrentemente
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				h.Publish("ev", j)
			}
		}(i)
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			h.Unsubscribe(channels[i])
		}(i)
	}
	wg.Wait()

	h.Close()

	// Drenar canales residuales
	for _, ch := range channels {
		for {
			select {
			case <-ch:
			default:
				goto done
			}
		}
	done:
	}
}
