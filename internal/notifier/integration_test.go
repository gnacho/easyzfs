package notifier

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// smtpFake — mini servidor SMTP de prueba (sin TLS, sin auth) que captura los
// correos enviados. Suficiente para el flujo DialAndSend de go-mail.
type smtpFake struct {
	ln   net.Listener
	msgs chan string
	wg   sync.WaitGroup
}

func newSMTPFake(t *testing.T) *smtpFake {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &smtpFake{ln: ln, msgs: make(chan string, 8)}
	s.wg.Add(1)
	go s.serve()
	t.Cleanup(func() {
		ln.Close()
		s.wg.Wait()
	})
	return s
}

func (s *smtpFake) addr() string { return s.ln.Addr().String() }

func (s *smtpFake) serve() {
	defer s.wg.Done()
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
	reply := func(code int, msg string) {
		fmt.Fprintf(c, "%d %s\r\n", code, msg)
	}
	reply(220, "fake ESMTP")
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
		case strings.HasPrefix(up, "MAIL"):
			reply(250, "ok")
		case strings.HasPrefix(up, "RCPT"):
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

// Envío real end-to-end: el Mailer dial al servidor fake y entrega el correo
// con el destino de prueba (SMTP_TEST_TO) y el asunto prefijado [TEST].
func TestMailer_EnvioReal(t *testing.T) {
	fake := newSMTPFake(t)
	m, err := NewMailer(SMTP{
		Host: "127.0.0.1", Port: portOf(t, fake.addr()), From: "easyzfs@example.com",
		Encryption: "none", Timeout: 5 * time.Second, TestTo: "test@example.com",
	})
	if err != nil {
		t.Fatalf("NewMailer: %v", err)
	}
	defer m.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = m.Send(ctx, []string{"real@example.com"}, "es", testAlert(),
		"Capacidad de pool", "El pool tank está al 95% de capacidad.")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	select {
	case msg := <-fake.msgs:
		// Asunto y destinatario van sin codificar en las cabeceras.
		for _, want := range []string{"Subject: [TEST] Capacidad de pool", "To: <test@example.com>"} {
			if !strings.Contains(msg, want) {
				t.Errorf("correo sin %q:\n%s", want, msg)
			}
		}
		// El cuerpo multipart va en quoted-printable: verificar el flujo real
		// comprobando que el correo incluye al menos el contenido (los '=' son
		// saltos de línea codificados, no deben aparecer dentro de las frases).
		if !strings.Contains(msg, "pools:tank") {
			t.Errorf("correo sin pool target:\n%s", msg)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("no llegó correo al servidor fake")
	}
}

func portOf(t *testing.T, addr string) int {
	t.Helper()
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if _, err := fmt.Sscanf(port, "%d", &n); err != nil {
		t.Fatal(err)
	}
	return n
}
