// auth_handlers.go — login/logout/me/cambio de contraseña propio.
package httpapi

import (
	"context"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/skip2/go-qrcode"

	"easyzfs/internal/auth"
	"easyzfs/internal/totp"
	"easyzfs/internal/users"
)

// --- Rate limiting de /api/login ---
// Limiter en memoria por IP+usuario: máx. 5 intentos/min y bloqueo de 15 min
// tras 10 fallos consecutivos. Respuesta: 429 {"error":"rate_limited"}.
const (
	loginMaxPerMinute  = 5
	loginBlockAfter    = 10
	loginWindow        = time.Minute
	loginBlockDuration = 15 * time.Minute
)

// loginAttempt — estado del limiter para una clave IP+usuario.
type loginAttempt struct {
	mu        sync.Mutex
	window    []time.Time // timestamps de intentos en la ventana deslizante
	failures  int         // fallos consecutivos
	blockedTo time.Time   // bloqueado hasta
}

// loginLimiter agrupa los intentos por clave "IP|usuario".
type loginLimiter struct {
	mu  sync.Mutex
	att map[string]*loginAttempt
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{att: map[string]*loginAttempt{}}
}

// allow registra un intento y dice si puede proceder (y cuándo reintentar).
func (l *loginLimiter) allow(key string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	a, ok := l.att[key]
	if !ok {
		a = &loginAttempt{}
		l.att[key] = a
	}
	// higiene best-effort: purga de claves sin actividad reciente
	if len(l.att) > 1024 {
		for k, v := range l.att {
			v.mu.Lock()
			stale := now.Sub(v.lastSeen()) > loginBlockDuration &&
				len(v.window) == 0 && v.failures == 0
			v.mu.Unlock()
			if stale {
				delete(l.att, k)
			}
		}
	}
	l.mu.Unlock()

	a.mu.Lock()
	defer a.mu.Unlock()
	if now.Before(a.blockedTo) {
		return false, time.Until(a.blockedTo)
	}
	// ventana deslizante de 1 minuto
	cut := now.Add(-loginWindow)
	kept := a.window[:0]
	for _, t := range a.window {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	a.window = kept
	if len(a.window) >= loginMaxPerMinute {
		return false, loginWindow - now.Sub(a.window[0])
	}
	a.window = append(a.window, now)
	return true, 0
}

// lastSeen devuelve el último intento registrado (llamar con a.mu tomado).
func (a *loginAttempt) lastSeen() time.Time {
	if n := len(a.window); n > 0 {
		return a.window[n-1]
	}
	return a.blockedTo
}

// success resetea los fallos consecutivos de la clave.
func (l *loginLimiter) success(key string) {
	l.mu.Lock()
	a, ok := l.att[key]
	l.mu.Unlock()
	if !ok {
		return
	}
	a.mu.Lock()
	a.failures = 0
	a.blockedTo = time.Time{}
	a.mu.Unlock()
}

// failure anota un fallo; tras loginBlockAfter consecutivos, bloquea 15 min.
func (l *loginLimiter) failure(key string, now time.Time) {
	l.mu.Lock()
	a, ok := l.att[key]
	l.mu.Unlock()
	if !ok {
		return
	}
	a.mu.Lock()
	a.failures++
	if a.failures >= loginBlockAfter {
		a.blockedTo = now.Add(loginBlockDuration)
		a.failures = 0 // tras el bloqueo, la cuenta vuelve a cero
	}
	a.mu.Unlock()
}

// argonSem limita las verificaciones argon2 concurrentes (cada Verify usa
// ~64 MiB; el unit tiene MemoryMax=256M). Máximo 2 en vuelo.
var argonSem = make(chan struct{}, 2)

// loginKey — clave del limiter: IP del cliente + usuario.
func loginKey(r *http.Request, user string) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return host + "|" + user
}

// login — POST /api/login {user, password} → {user, role} + cookie.
// Si el usuario tiene 2FA activo, NO crea sesión: responde 200 con
// {twofa_required:true, pending:"<token firmado>"} y el front pide el código.
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		User     string `json:"user"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	key := loginKey(r, body.User)
	now := time.Now()
	if ok, retry := s.loginLimiter.allow(key, now); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
		writeErr(w, http.StatusTooManyRequests, "rate_limited", "demasiados intentos de login; inténtalo más tarde")
		return
	}
	// Semáforo argon2: serializar verificaciones para acotar la memoria.
	argonSem <- struct{}{}
	role, err := s.users.Verify(r.Context(), body.User, body.Password)
	<-argonSem
	if err != nil {
		s.loginLimiter.failure(key, now)
		writeErr(w, http.StatusUnauthorized, "bad_credentials", "usuario o contraseña incorrectos")
		return
	}
	// 2FA activo → no crear sesión aún; entregar un token pendiente firmado.
	if s.require2FA(r.Context(), body.User) {
		pending, perr := s.auth.SignPending(body.User)
		if perr != nil {
			writeErr(w, http.StatusInternalServerError, "session_error", "no se pudo preparar el segundo factor")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"user":           body.User,
			"role":           role,
			"twofa_required": true,
			"pending":        pending,
		})
		return
	}
	s.loginLimiter.success(key)
	cookie, err := s.auth.CreateSession(r.Context(), body.User)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "session_error", "no se pudo crear la sesión")
		return
	}
	http.SetCookie(w, cookie)
	writeJSON(w, http.StatusOK, map[string]string{"user": body.User, "role": role})
}

// login2FA — POST /api/login/2fa {pending, code} → {user, role} + cookie.
// Valida el token pendiente firmado y el código TOTP o recovery, y crea la
// sesión. Un código TOTP erróneo no consume el pending (se puede reintentar);
// un recovery code válido SÍ se gasta.
func (s *Server) login2FA(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Pending string `json:"pending"`
		Code    string `json:"code"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	user, ok := s.auth.VerifyPending(body.Pending)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "pending_expired", "la sesión de segundo factor ha caducado; vuelve a introducir tu contraseña")
		return
	}
	key := loginKey(r, user)
	now := time.Now()
	if ok, retry := s.loginLimiter.allow(key, now); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
		writeErr(w, http.StatusTooManyRequests, "rate_limited", "demasiados intentos; inténtalo más tarde")
		return
	}
	// Código TOTP, o recovery code como alternativa.
	secret, err := s.users.TOTPSecret(r.Context(), user)
	if err != nil || secret == "" {
		writeErr(w, http.StatusUnauthorized, "bad_code", "código incorrecto")
		return
	}
	if totp.Validate(strings.TrimSpace(body.Code), secret, now) {
		s.loginLimiter.success(key)
		s.finishLogin2FA(w, r, user)
		return
	}
	// Recovery code: hash y marcar gastado si existe.
	if s.useRecoveryCode(r.Context(), user, body.Code) {
		s.loginLimiter.success(key)
		s.finishLogin2FA(w, r, user)
		return
	}
	s.loginLimiter.failure(key, now)
	writeErr(w, http.StatusUnauthorized, "bad_code", "código incorrecto")
}

// finishLogin2FA crea la sesión y responde (paso común del login 2FA).
func (s *Server) finishLogin2FA(w http.ResponseWriter, r *http.Request, user string) {
	cookie, err := s.auth.CreateSession(r.Context(), user)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "session_error", "no se pudo crear la sesión")
		return
	}
	role, _ := s.users.RoleOf(r.Context(), user)
	http.SetCookie(w, cookie)
	writeJSON(w, http.StatusOK, map[string]string{"user": user, "role": role})
}

// require2FA — ¿el usuario tiene 2FA activo? (los que no existen → false).
func (s *Server) require2FA(ctx context.Context, user string) bool {
	enabled, err := s.users.TOTPEnabled(ctx, user)
	return err == nil && enabled
}

// useRecoveryCode — verifica y gasta un recovery code (hash SHA-256).
func (s *Server) useRecoveryCode(ctx context.Context, user, code string) bool {
	hash := totp.RecoveryHash(strings.TrimSpace(code))
	used, err := s.users.UseRecoveryCode(ctx, user, hash)
	return err == nil && used
}

// logout — POST /api/logout → 204. Invalida la sesión.
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(auth.CookieName); err == nil {
		s.auth.DestroySession(r.Context(), c.Value)
	}
	http.SetCookie(w, s.auth.ExpiredCookie())
	w.WriteHeader(http.StatusNoContent)
}

// me — GET /api/me → {user, role, language, display_name, email, avatar}
// (401 ya lo da el middleware).
func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	lang := "auto"
	displayName, email, avatar := "", "", ""
	if u, err := s.users.Get(r.Context(), auth.UserFromContext(r.Context())); err == nil {
		lang = u.Language
		displayName = u.DisplayName
		email = u.Email
		avatar = u.Avatar
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"user":         auth.UserFromContext(r.Context()),
		"role":         auth.RoleFromContext(r.Context()),
		"language":     lang,
		"display_name": displayName,
		"email":        email,
		"avatar":       avatar,
	})
}

// putMyProfile — PUT /api/me/profile {display_name, email} → 204.
func (s *Server) putMyProfile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DisplayName string `json:"display_name"`
		Email       string `json:"email"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	user := auth.UserFromContext(r.Context())
	if err := s.users.SetProfile(r.Context(), user, body.DisplayName, body.Email); err != nil {
		if errors.Is(err, users.ErrInvalidEmail) {
			writeErr(w, http.StatusBadRequest, "invalid_email", err.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// putMyLanguage — PUT /api/me/language {language} → 204.
// El idioma del usuario vive en BD (fuente de verdad); el front lo espeja.
func (s *Server) putMyLanguage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Language string `json:"language"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	user := auth.UserFromContext(r.Context())
	if err := s.users.SetLanguage(r.Context(), user, body.Language); err != nil {
		if errors.Is(err, users.ErrInvalidLang) {
			writeErr(w, http.StatusBadRequest, "invalid_language", err.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// changeMyPassword — POST /api/me/password {current, new} → 204.
// Cierra el resto de sesiones del usuario (la actual sobrevive).
func (s *Server) changeMyPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	user := auth.UserFromContext(r.Context())
	// Mismo semáforo argon2 que en login: acota la memoria en verificaciones.
	argonSem <- struct{}{}
	_, err := s.users.Verify(r.Context(), user, body.Current)
	<-argonSem
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "bad_credentials", "la contraseña actual no es correcta")
		return
	}
	if err := s.users.SetPassword(r.Context(), user, body.New); err != nil {
		if errors.Is(err, users.ErrWeakPassword) {
			writeErr(w, http.StatusBadRequest, "weak_password", err.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	// cerrar el resto de sesiones (mantener la actual)
	except := ""
	if c, err := r.Cookie(auth.CookieName); err == nil {
		if token, _, found := strings.Cut(c.Value, "|"); found {
			except = token
		}
	}
	if err := s.auth.DestroyUserSessions(r.Context(), user, except); err != nil {
		writeErr(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// my2FAStatus — GET /api/me/2fa → {enabled}.
func (s *Server) my2FAStatus(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	enabled, err := s.users.TOTPEnabled(r.Context(), user)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": enabled})
}

// my2FASetup — POST /api/me/2fa/setup → {secret, otpauth, qr}.
// Genera un secreto NUEVO (invalida cualquier setup previo sin confirmar),
// lo guarda como provisional (totp_enabled=0) y devuelve el QR.
func (s *Server) my2FASetup(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	secret, err := totp.Secret()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "totp_error", err.Error())
		return
	}
	if err := s.users.SetTOTPSecret(r.Context(), user, secret); err != nil {
		writeErr(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	uri, err := totp.URI(secret, user)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "totp_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"secret":  secret,
		"otpauth": uri,
		"qr":      qrDataURL(uri),
	})
}

// my2FAConfirm — POST /api/me/2fa/confirm {code} → {recovery_codes}.
// Activa el 2FA si el código coincide con el secreto provisional y genera los
// recovery codes (entregados UNA vez). Si ya estaba activo, no cambia nada.
func (s *Server) my2FAConfirm(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code string `json:"code"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	user := auth.UserFromContext(r.Context())
	enabled, err := s.users.TOTPEnabled(r.Context(), user)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	if enabled {
		writeErr(w, http.StatusConflict, "already_enabled", "la verificación en dos pasos ya está activa")
		return
	}
	secret, err := s.users.TOTPSecret(r.Context(), user)
	if err != nil || secret == "" {
		writeErr(w, http.StatusBadRequest, "no_setup", "inicia primero la configuración (setup)")
		return
	}
	if !totp.Validate(strings.TrimSpace(body.Code), secret, time.Now()) {
		writeErr(w, http.StatusUnauthorized, "bad_code", "código incorrecto")
		return
	}
	if err := s.users.TOTPActivate(r.Context(), user); err != nil {
		writeErr(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	// Generar y guardar recovery codes (10), devolverlos en claro una vez.
	codes := []string{}
	if err := s.users.ClearRecoveryCodes(r.Context(), user); err != nil {
		writeErr(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	for i := 0; i < 10; i++ {
		code, err := totp.RecoveryCode()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "totp_error", err.Error())
			return
		}
		if err := s.users.AddRecoveryCode(r.Context(), user, totp.RecoveryHash(code)); err != nil {
			writeErr(w, http.StatusInternalServerError, "db_error", err.Error())
			return
		}
		codes = append(codes, code)
	}
	writeJSON(w, http.StatusOK, map[string]any{"codes": codes})
}

// my2FADisable — POST /api/me/2fa/disable {code} → 204.
// Requiere un código TOTP válido actual (o un recovery code) para desactivar.
func (s *Server) my2FADisable(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code string `json:"code"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	user := auth.UserFromContext(r.Context())
	enabled, err := s.users.TOTPEnabled(r.Context(), user)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	if !enabled {
		writeErr(w, http.StatusBadRequest, "not_enabled", "la verificación en dos pasos no está activa")
		return
	}
	secret, _ := s.users.TOTPSecret(r.Context(), user)
	ok := totp.Validate(strings.TrimSpace(body.Code), secret, time.Now())
	if !ok && s.useRecoveryCode(r.Context(), user, body.Code) {
		ok = true
	}
	if !ok {
		writeErr(w, http.StatusUnauthorized, "bad_code", "código incorrecto")
		return
	}
	if err := s.users.TOTPDisable(r.Context(), user); err != nil {
		writeErr(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// my2FARecovery — GET /api/me/2fa/recovery → {codes}.
// Regenera los recovery codes (solo con 2FA activo). Los anteriores se borran.
func (s *Server) my2FARecovery(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	enabled, err := s.users.TOTPEnabled(r.Context(), user)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	if !enabled {
		writeErr(w, http.StatusBadRequest, "not_enabled", "la verificación en dos pasos no está activa")
		return
	}
	if err := s.users.ClearRecoveryCodes(r.Context(), user); err != nil {
		writeErr(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	codes := []string{}
	for i := 0; i < 10; i++ {
		code, err := totp.RecoveryCode()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "totp_error", err.Error())
			return
		}
		if err := s.users.AddRecoveryCode(r.Context(), user, totp.RecoveryHash(code)); err != nil {
			writeErr(w, http.StatusInternalServerError, "db_error", err.Error())
			return
		}
		codes = append(codes, code)
	}
	writeJSON(w, http.StatusOK, map[string]any{"codes": codes})
}

// admin2FADisable — DELETE /api/users/{name}/2fa → 204 (admin).
// Desactiva el 2FA de otro usuario sin necesidad de su código (reset).
func (s *Server) admin2FADisable(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := s.users.TOTPDisable(r.Context(), name); err != nil {
		if errors.Is(err, users.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", err.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// qrDataURL genera un QR en data:image/png;base64 para el autenticador.
func qrDataURL(otpauth string) string {
	png, err := qrcode.Encode(otpauth, qrcode.Medium, 240)
	if err != nil {
		return "" // sin QR: el front ofrece pegar la clave otpauth manualmente
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
}
