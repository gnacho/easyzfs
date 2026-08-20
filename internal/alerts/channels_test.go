// channels_test.go (alerts) — integración: RaiseKind entrega la alerta a los
// canales ntfy/gotify/syslog configurados (best-effort async).
package alerts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"easyzfs/internal/channels"
	"easyzfs/internal/db"
	"easyzfs/internal/hub"
	"easyzfs/internal/settings"
)

func TestRaiseKind_EnviaCanales(t *testing.T) {
	d, err := db.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := db.Migrate(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	st, err := settings.NewStore(d)
	if err != nil {
		t.Fatal(err)
	}

	// Servidor fake ntfy que captura el payload.
	type recv struct {
		title, msg string
	}
	got := make(chan recv, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p map[string]string
		_ = json.NewDecoder(r.Body).Decode(&p)
		got <- recv{title: p["title"], msg: p["message"]}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := channels.New(srv.URL, "", "", "", "", 0, "udp", 1)
	a := New(d, hub.NewHub(), st)
	a.SetChannels(c)
	a.RaiseKind(context.Background(), "warn", "pool.tank", "pools:tank",
		"Pool tank al 95% de capacidad", "pool_capacity",
		map[string]any{"pool": "tank", "pct": 95, "threshold": 90})

	select {
	case m := <-got:
		if m.title == "" || m.msg == "" {
			t.Fatalf("payload incompleto: %+v", m)
		}
		if !containsFold(m.msg, "tank") {
			t.Errorf("mensaje sin pool: %q", m.msg)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("no llegó alerta al canal ntfy")
	}
}

func containsFold(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if stringsEqualFold(s[i:i+len(sub)], sub) {
				return true
			}
		}
		return false
	})()
}

func stringsEqualFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
