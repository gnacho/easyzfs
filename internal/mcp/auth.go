package mcp

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"easyzfs/internal/apikeys"
)

// Handler devuelve el handler HTTP de /mcp: rate limit por IP -> auth Bearer
// por API key -> servidor MCP streamable-HTTP. La cadena va en este orden para
// que el brute-force de claves tambien quede limitado.
//
// SOLO acepta API keys de solo lectura (internal/apikeys): nunca la cookie de
// sesion, para no mezclar superficies CSRF con clientes AI. Las claves ez_...
// ya son read-only por diseno, igual que los tools de esta primera tanda.
func (s *Server) Handler(keys *apikeys.Store) http.Handler {
	// Sin key store el endpoint NO se sirve sin auth: fail-closed (main solo
	// construye el MCP con el store existente, pero la defensa va aqui, en la
	// ultima capa antes del handler).
	if keys == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"mcp_auth_unavailable"}`))
		})
	}
	return rateLimit(s.ratePerMin, bearerOnly(keys, http.Handler(s.http)))
}

// bearerOnly exige Authorization: Bearer <api key> valida. Sin clave, con
// clave mal formada o invalida -> 401 {error: unauthorized}.
func bearerOnly(keys *apikeys.Store, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := bearerToken(r)
		if raw == "" {
			unauthorized(w)
			return
		}
		if _, ok := keys.Validate(r.Context(), raw); !ok {
			unauthorized(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
}

func bearerToken(r *http.Request) string {
	v := r.Header.Get("Authorization")
	if !strings.HasPrefix(v, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(v, "Bearer ")
}

// rlBucket es la ventana fija de un cliente (por IP).
type rlBucket struct {
	window time.Time
	count  int
}

// rateLimit limita /mcp por IP (ventana fija de 1 minuto). Sin estado externo:
// brute-force de claves queda acotado a ratePerMin intentos/min.
func rateLimit(perMin int, next http.Handler) http.Handler {
	var mu sync.Mutex
	buckets := map[string]*rlBucket{}
	window := time.Minute
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		now := time.Now()
		mu.Lock()
		b := buckets[ip]
		if b == nil || now.Sub(b.window) >= window {
			b = &rlBucket{window: now}
			buckets[ip] = b
		}
		b.count++
		full := b.count > perMin
		mu.Unlock()
		if full {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "rate_limited"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP usa RemoteAddr. EasyZFS no tiene modo trusted proxy global; sin esa
// configuracion, X-Forwarded-For no es fiable y no se debe usar para limitar.
func clientIP(r *http.Request) string {
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	return host
}
