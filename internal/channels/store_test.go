// store_test.go — persistencia de la config de canales (#134) y aplicación en
// caliente (sin reiniciar).
package channels

import (
	"context"
	"testing"

	"easyzfs/internal/db"
)

// Roundtrip: Save/Load conserva todos los campos (incluidos secretos) y el
// primer Load (sin fila) lo indica con ok=false.
func TestStoreRoundtrip(t *testing.T) {
	d, err := db.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := db.Migrate(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	s := NewStore(d)

	if _, ok, err := s.Load(context.Background()); err != nil || ok {
		t.Fatalf("sin fila: ok=%v err=%v (esperado ok=false, err=nil)", ok, err)
	}

	in := Config{
		NtfyURL:          "https://ntfy.sh/topic-secreto",
		NtfyToken:        "tok-ntfy",
		GotifyURL:        "https://gotify.example.com",
		GotifyToken:      "tok-gotify",
		TelegramBotToken: "123:ABC",
		TelegramChatID:   "-1009",
		SyslogHost:       "127.0.0.1",
		SyslogPort:       514,
		SyslogProto:      "udp",
		SyslogFacility:   1,
	}
	if err := s.Save(context.Background(), in); err != nil {
		t.Fatalf("save: %v", err)
	}
	out, ok, err := s.Load(context.Background())
	if err != nil || !ok {
		t.Fatalf("load: ok=%v err=%v", ok, err)
	}
	if out != in {
		t.Fatalf("roundtrip distinto:\n in=%+v\nout=%+v", in, out)
	}
}

// Apply en caliente: un cliente vacío pasa a tener canal sin reiniciar.
func TestApplyLive(t *testing.T) {
	c := New(Config{})
	if c.Enabled() || c.Configured("ntfy") {
		t.Fatal("sin config no debe estar habilitado")
	}
	c.Apply(Config{NtfyURL: "https://ntfy.sh/x"})
	if !c.Enabled() || !c.Configured("ntfy") {
		t.Fatal("Apply debe activar el canal en caliente")
	}
	c.Apply(Config{})
	if c.Enabled() {
		t.Fatal("Apply vacío debe desactivar")
	}
}

// NtfyServer oculta el topic (secreto) y conserva el servidor.
func TestNtfyServer(t *testing.T) {
	if got := NtfyServer("https://ntfy.sh/mi-topic-secreto"); got != "https://ntfy.sh" {
		t.Fatalf("NtfyServer = %q", got)
	}
	if got := NtfyServer(""); got != "" {
		t.Fatalf("NtfyServer vacío = %q", got)
	}
}
