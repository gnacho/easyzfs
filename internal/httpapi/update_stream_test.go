// update_stream_test.go — GET /api/update/stream (SSE): requiere admin y
// emite un evento inicial con el estado del update. El stream bloquea hasta
// que el cliente desconecta, así que probamos la lectura del primer evento.
package httpapi

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"easyzfs/internal/auth"
	"easyzfs/internal/config"
	"easyzfs/internal/db"
	"easyzfs/internal/updater"
	"easyzfs/internal/users"
)

// setupUpdateServer levanta un servidor con updater y crea el admin.
func setupUpdateServer(t *testing.T) (http.Handler, *http.Cookie) {
	t.Helper()
	d, err := db.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	if err := db.Migrate(context.Background(), d); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { d.Close() })

	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	us := users.NewStore(d)
	if err := us.Create(context.Background(), "admin", "password123", "admin"); err != nil {
		t.Fatalf("create: %v", err)
	}
	am := auth.NewManager(d, secret, false)

	up := updater.New("2.9.18", t.TempDir(), "")
	srv := NewServer(Deps{
		Cfg:     &config.Config{Mock: true},
		DB:      d,
		Auth:    am,
		Users:   us,
		Updater: up,
	})
	return srv.Handler(), loginOK(t, srv.Handler())
}

// readFirstEvent lee el primer "event: update\ndata: {...}" del body SSE.
func readFirstEvent(t *testing.T, body string) map[string]any {
	t.Helper()
	scanner := bufio.NewScanner(strings.NewReader(body))
	var data string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data:") {
			data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			break
		}
	}
	if data == "" {
		t.Fatalf("no se encontró el primer evento update en: %q", body)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		t.Fatalf("payload no es JSON: %v (%s)", err, data)
	}
	return m
}

func TestUpdateStreamRequiresAdmin(t *testing.T) {
	h, _ := setupUpdateServer(t)
	req, _ := http.NewRequestWithContext(context.Background(), "GET", "/api/update/stream", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code == 200 {
		t.Fatal("sin cookie el stream no debería responder 200")
	}
}

func TestUpdateStreamEmitsInitialEvent(t *testing.T) {
	h, cookie := setupUpdateServer(t)
	ts := httptest.NewServer(h)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/update/stream", nil)
	req.AddCookie(cookie)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("stream request: %v", err)
	}
	defer res.Body.Close()
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type %q, esperaba text/event-stream", ct)
	}
	buf := make([]byte, 4096)
	n, _ := res.Body.Read(buf)
	first := string(buf[:n])
	m := readFirstEvent(t, first)
	if m["current"] != "2.9.18" {
		t.Fatalf("current esperaba 2.9.18, got %v", m["current"])
	}
	if _, ok := m["available"]; !ok {
		t.Fatal("esperaba campo available en el evento inicial")
	}
}
