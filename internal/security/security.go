// Package security — cabeceras de seguridad HTTP obligatorias (middleware
// global). Patrón de NetPulse (server-go/internal/security): mismas cabeceras
// en TODAS las respuestas. La CSP permite el check de actualizaciones pasivo
// contra api.github.com (web/src/ui/releasecheck.ts).
package security

import "net/http"

// Headers es la lista de cabeceras de seguridad aplicadas a toda respuesta.
var Headers = []struct{ K, V string }{
	{"X-Content-Type-Options", "nosniff"},
	{"X-Frame-Options", "DENY"},
	{"Referrer-Policy", "strict-origin-when-cross-origin"},
	{"Permissions-Policy", "geolocation=(), microphone=(), camera=()"},
	{"Strict-Transport-Security", "max-age=31536000; includeSubDomains"},
	{"Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data:; connect-src 'self' https://api.github.com"},
}

// Middleware aplica los headers a todas las respuestas.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, h := range Headers {
			w.Header().Set(h.K, h.V)
		}
		next.ServeHTTP(w, r)
	})
}
