package alerts

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"easyzfs/internal/db"
	"easyzfs/internal/hub"
	"easyzfs/internal/notifier"
	"easyzfs/internal/settings"
)

// smtpFake — mini servidor SMTP (sin TLS/auth) que captura correos.
type smtpFake struct {
	ln   net.Listener
	msgs chan string
}

func newSMTPFake(t *testing.T) *smtpFake {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &smtpFake{ln: ln, msgs: make(chan string, 4)}
	go s.serve()
	t.Cleanup(func() { ln.Close() })
	return s
}

func (s *smtpFake) serve() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(c)
	}
}

func (s *smtpFake) handle(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	reply := func(code int, msg string) { fmt.Fprintf(c, "%d %s\r\n", code, msg) }
	reply(220, "fake")
	var data []string
	inData := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if inData {
			if line == "." {
				inData = false
				s.msgs <- strings.Join(data, "\n")
				data = nil
				reply(250, "queued")
			} else {
				data = append(data, line)
			}
			continue
		}
		up := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(up, "EHLO") || strings.HasPrefix(up, "HELO"):
			reply(250, "fake")
		case strings.HasPrefix(up, "MAIL") || strings.HasPrefix(up, "RCPT"):
			reply(250, "ok")
		case strings.HasPrefix(up, "DATA"):
			reply(354, "go")
			inData = true
		case strings.HasPrefix(up, "QUIT"):
			reply(221, "bye")
			return
		default:
			reply(250, "ok")
		}
	}
}

// RaiseKind con un usuario con email y el tipo habilitado debe entregar el
// correo al servidor SMTP fake (flujo end-to-end alerts → notifier).
func TestRaiseKind_EnviaEmail(t *testing.T) {
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
	// usuario con email (el idioma 'es' y el tipo habilitado por defecto)
	if _, err := d.Exec(
		"INSERT INTO users(user, pass_hash, role, language, email) VALUES ('alice','x','user','es','alice@example.com')"); err != nil {
		t.Fatal(err)
	}
	// usuario sin email: no debe recibir
	if _, err := d.Exec(
		"INSERT INTO users(user, pass_hash, role, language, email) VALUES ('bob','x','user','en','')"); err != nil {
		t.Fatal(err)
	}

	fake := newSMTPFake(t)
	_, port, _ := net.SplitHostPort(fake.ln.Addr().String())
	var p int
	fmt.Sscanf(port, "%d", &p)
	m, err := notifier.NewMailer(notifier.SMTP{
		Host: "127.0.0.1", Port: p, From: "easyzfs@example.com", Encryption: "none",
		Timeout: 5 * time.Second, TestTo: "alice@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	a := New(d, hub.NewHub(), st)
	a.SetEmail(m)
	a.RaiseKind(context.Background(), "warn", "pool.tank", "pools:tank",
		"Pool tank al 95% de capacidad", "pool_capacity",
		map[string]any{"pool": "tank", "pct": 95, "threshold": 90})

	select {
	case msg := <-fake.msgs:
		// alice tiene email y tipo habilitado → recibe; asunto traducido al ES.
		if !strings.Contains(msg, "To: <alice@example.com>") {
			t.Errorf("destinatario no es alice: %s", msg)
		}
		if !strings.Contains(msg, "pools:tank") {
			t.Errorf("correo sin target:\n%s", msg)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("no llegó correo pese a haber usuario con email habilitado")
	}

	// Asegurar que no hubo segundo correo (bob no tiene email; solo alice)
	select {
	case extra := <-fake.msgs:
		t.Errorf("correo extra inesperado:\n%s", extra)
	case <-time.After(300 * time.Millisecond):
	}
}

// Si el tipo está deshabilitado en notification_preferences, no se envía.
func TestRaiseKind_EmailTipoDeshabilitado(t *testing.T) {
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
	if _, err := d.Exec(
		"INSERT INTO users(user, pass_hash, role, language, email) VALUES ('alice','x','user','es','alice@example.com')"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(
		"INSERT INTO notification_preferences(user_id, tipo, enabled) VALUES ('alice','pool_capacity',0)"); err != nil {
		t.Fatal(err)
	}

	fake := newSMTPFake(t)
	_, port, _ := net.SplitHostPort(fake.ln.Addr().String())
	var p int
	fmt.Sscanf(port, "%d", &p)
	m, err := notifier.NewMailer(notifier.SMTP{
		Host: "127.0.0.1", Port: p, From: "easyzfs@example.com", Encryption: "none",
		Timeout: 5 * time.Second, TestTo: "alice@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	a := New(d, hub.NewHub(), st)
	a.SetEmail(m)
	a.RaiseKind(context.Background(), "warn", "pool.tank", "pools:tank",
		"Pool tank al 95%", "pool_capacity", map[string]any{"pool": "tank", "pct": 95, "threshold": 90})

	select {
	case msg := <-fake.msgs:
		t.Errorf("email enviado pese a tipo deshabilitado:\n%s", msg)
	case <-time.After(600 * time.Millisecond):
	}
}
