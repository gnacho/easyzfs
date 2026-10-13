package security

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestMiddlewareCabecerasSeguridad (auditoría P3 #3): toda respuesta lleva
// las cabeceras de seguridad básicas, incluidas las del API y los estáticos.
func TestMiddlewareCabecerasSeguridad(t *testing.T) {
	h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/overview", nil))

	for _, want := range []string{
		"X-Content-Type-Options", "X-Frame-Options", "Referrer-Policy",
		"Permissions-Policy", "Strict-Transport-Security", "Content-Security-Policy",
	} {
		if got := rec.Header().Get(want); got == "" {
			t.Errorf("cabecera %s ausente en la respuesta", want)
		}
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'self'") {
		t.Errorf("CSP sin default-src 'self': %q", csp)
	}
	// La CSP debe permitir el check de actualizaciones pasivo (releasecheck).
	if !strings.Contains(csp, "https://api.github.com") {
		t.Errorf("CSP sin connect-src api.github.com: %q", csp)
	}
	// El front embebe las fuentes (Space Grotesk/JetBrains Mono) como data:.
	if !strings.Contains(csp, "font-src 'self' data:") {
		t.Errorf("CSP sin font-src data: (rompe las fuentes embebidas): %q", csp)
	}
	// X-Content-Type-Options nosniff evita sniffing de tipos.
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, quiero nosniff", got)
	}
}

// TestCSPConGCOrigin (#103): el origen del tracker GoatCounter (solo demo
// pública) se añade a script-src y connect-src; sin env, la CSP base queda
// intacta.
func TestCSPConGCOrigin(t *testing.T) {
	t.Setenv("GC_ORIGIN", "https://stats.example")
	csp := cspValue()
	if !strings.Contains(csp, "script-src 'self' 'unsafe-inline' https://stats.example") {
		t.Errorf("CSP sin el origen en script-src: %q", csp)
	}
	if !strings.Contains(csp, "connect-src 'self' https://api.github.com https://stats.example") {
		t.Errorf("CSP sin el origen en connect-src: %q", csp)
	}

	t.Setenv("GC_ORIGIN", "")
	csp = cspValue()
	if strings.Contains(csp, "stats.example") {
		t.Errorf("la CSP base no debe mencionar el origen del tracker: %q", csp)
	}
	if !strings.Contains(csp, "connect-src 'self' https://api.github.com") {
		t.Errorf("CSP base rota: %q", csp)
	}
}
