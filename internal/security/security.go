// Package security — cabeceras de seguridad HTTP obligatorias (middleware
// global). Patrón de NetPulse (server-go/internal/security): mismas cabeceras
// en TODAS las respuestas. La CSP permite el check de actualizaciones pasivo
// contra api.github.com (web/src/ui/releasecheck.ts). GC_ORIGIN (solo demo
// pública, issue #103) añade un origen extra (GoatCounter) a script-src y
// connect-src; las instalaciones normales no lo definen y la CSP queda intacta.
package security

import (
	"net/http"
	"os"
	"strings"
)

// cspValue construye la CSP. Lee GC_ORIGIN en cada llamada para que los tests
// puedan ejercitar ambos caminos; en producción el env no cambia tras arrancar.
func cspValue() string {
	v := "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data:; connect-src 'self' https://api.github.com"
	if o := os.Getenv("GC_ORIGIN"); o != "" {
		v = strings.Replace(v, "script-src 'self' 'unsafe-inline'", "script-src 'self' 'unsafe-inline' "+o, 1)
		v = strings.Replace(v, "connect-src 'self' https://api.github.com", "connect-src 'self' https://api.github.com "+o, 1)
	}
	return v
}

// Headers es la lista de cabeceras de seguridad aplicadas a toda respuesta.
// La CSP se fija por petición en el middleware (cspValue) porque depende del
// entorno; el resto son constantes.
var Headers = []struct{ K, V string }{
	{"X-Content-Type-Options", "nosniff"},
	{"X-Frame-Options", "DENY"},
	{"Referrer-Policy", "strict-origin-when-cross-origin"},
	{"Permissions-Policy", "geolocation=(), microphone=(), camera=()"},
	{"Strict-Transport-Security", "max-age=31536000; includeSubDomains"},
}

// Middleware aplica los headers a todas las respuestas.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, h := range Headers {
			w.Header().Set(h.K, h.V)
		}
		w.Header().Set("Content-Security-Policy", cspValue())
		next.ServeHTTP(w, r)
	})
}
