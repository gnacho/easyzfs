package notifier

import (
	"strings"
	"testing"
	"time"
)

func testAlert() Alert {
	return Alert{
		Level:     "warn",
		Source:    "pool.tank",
		Target:    "pools:tank",
		Timestamp: time.Date(2026, 8, 9, 14, 30, 0, 0, time.UTC),
	}
}

// render ES: título, severidad traducida, pool y timestamp en el HTML+texto.
func TestRender_Espanol(t *testing.T) {
	a := testAlert()
	html, text, err := render("es", a, "Capacidad de pool", "El pool tank está al 95% de capacidad.")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"aviso", "Capacidad de pool", "El pool tank está al 95% de capacidad.", "pools:tank", "2026-08-09T14:30:00Z"} {
		if !strings.Contains(html, want) {
			t.Errorf("HTML sin %q:\n%s", want, html)
		}
		if !strings.Contains(text, want) {
			t.Errorf("TXT sin %q:\n%s", want, text)
		}
	}
	if !strings.Contains(text, "Severidad:") {
		t.Errorf("TXT sin cabecera de severidad:\n%s", text)
	}
}

// render EN: severidad en inglés y pie en inglés.
func TestRender_Ingles(t *testing.T) {
	a := testAlert()
	html, text, err := render("en", a, "Pool capacity", "Pool tank is at 95% capacity.")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"warning", "Pool capacity", "2026-08-09T14:30:00Z"} {
		if !strings.Contains(html, want) {
			t.Errorf("HTML sin %q", want)
		}
		if !strings.Contains(text, want) {
			t.Errorf("TXT sin %q", want)
		}
	}
	if !strings.Contains(text, "Severity:") || !strings.Contains(html, `lang="en"`) {
		t.Errorf("EN mal: text=%q html=%q", text, html)
	}
}

// Idiomas desconocidos caen a es (fallback).
func TestRender_FallbackES(t *testing.T) {
	_, text, err := render("de", testAlert(), "X", "Y")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "Severidad:") {
		t.Errorf("fallback no fue ES:\n%s", text)
	}
}

// severidad crit → "crítica"/"critical" según idioma.
func TestSeverityLabel(t *testing.T) {
	if severityLabel("es", "crit") != "crítica" {
		t.Errorf("severityLabel es crit = %q", severityLabel("es", "crit"))
	}
	if severityLabel("en", "crit") != "critical" {
		t.Errorf("severityLabel en crit = %q", severityLabel("en", "crit"))
	}
	if severityLabel("es", "info") != "info" {
		t.Errorf("severityLabel info = %q", severityLabel("es", "info"))
	}
}

// tlsPolicy mapea none/tls/starttls.
func TestTLSPolicy(t *testing.T) {
	if tlsPolicy("none").String() == "" {
		t.Error("policy none vacía")
	}
	if tlsPolicy("tls").String() == "" {
		t.Error("policy tls vacía")
	}
	if tlsPolicy("starttls").String() == "" {
		t.Error("policy starttls vacía")
	}
}

// NewMailer valida: sin host, sin from o sin puerto/timeout → error.
func TestNewMailer_Validate(t *testing.T) {
	if _, err := NewMailer(SMTP{}); err == nil {
		t.Error("SMTP vacío debería fallar (sin host)")
	}
	if _, err := NewMailer(SMTP{Host: "smtp.example.com"}); err == nil {
		t.Error("sin from debería fallar")
	}
	if _, err := NewMailer(SMTP{Host: "smtp.example.com", From: "easyzfs@example.com", Port: 587}); err == nil {
		t.Error("sin timeout debería fallar")
	}
	ok := SMTP{Host: "smtp.example.com", Port: 587, From: "easyzfs@example.com", Timeout: 10}
	if _, err := NewMailer(ok); err != nil {
		t.Errorf("config mínima válida debería pasar: %v", err)
	}
}
