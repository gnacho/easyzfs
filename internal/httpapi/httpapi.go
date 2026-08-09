// Package httpapi — handlers REST + SSE. Los handlers leen la CACHÉ de los
// colectores, nunca ejecutan comandos del sistema directamente.
// Errores: {"error":"código","message":"texto legible"} con HTTP 4xx/5xx.
package httpapi

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"easyzfs/internal/actions"
	"easyzfs/internal/alerts"
	"easyzfs/internal/auth"
	"easyzfs/internal/backup"
	"easyzfs/internal/collectors"
	"easyzfs/internal/config"
	"easyzfs/internal/hub"
	"easyzfs/internal/longops"
	"easyzfs/internal/push"
	"easyzfs/internal/replication"
	"easyzfs/internal/scheduler"
	"easyzfs/internal/settings"
	"easyzfs/internal/updater"
	"easyzfs/internal/users"
)

// Server — dependencias inyectadas desde main (sin framework de DI).
type Server struct {
	cfg        *config.Config
	db         *sql.DB
	auth       *auth.Manager
	users      *users.Store
	alerter    *alerts.Alerter
	settings   *settings.Store
	pools      collectors.PoolProvider
	disks      collectors.DiskProvider
	sysTimers  collectors.SysTimerProvider
	perf       collectors.PerfProvider
	caps       collectors.CapProvider
	act        *actions.Service
	sched      *scheduler.Scheduler
	jstore     *scheduler.Store
	h          *hub.Hub
	push       *push.Sender
	backup     *backup.Store
	longOps    *longops.Manager
	repl       *replication.Runner
	updater    *updater.Updater
	started    time.Time
	version    string
	build      string
	zfsVersion string

	loginLimiter *loginLimiter // rate limit de /api/login (IP+usuario)
}

// Deps — parámetros del constructor.
type Deps struct {
	Cfg        *config.Config
	DB         *sql.DB
	Auth       *auth.Manager
	Users      *users.Store
	Alerter    *alerts.Alerter
	Settings   *settings.Store
	Pools      collectors.PoolProvider
	Disks      collectors.DiskProvider
	SysTimers  collectors.SysTimerProvider
	Perf       collectors.PerfProvider
	Caps       collectors.CapProvider
	Actions    *actions.Service
	Sched      *scheduler.Scheduler
	Jobs       *scheduler.Store
	Hub        *hub.Hub
	Push       *push.Sender
	Backup     *backup.Store
	LongOps    *longops.Manager
	Repl       *replication.Runner
	Updater    *updater.Updater
	Version    string
	Build      string
	ZFSVersion string
}

// NewServer crea el servidor del API.
func NewServer(d Deps) *Server {
	return &Server{
		cfg: d.Cfg, db: d.DB, auth: d.Auth, users: d.Users,
		alerter: d.Alerter, settings: d.Settings,
		pools: d.Pools, disks: d.Disks, sysTimers: d.SysTimers,
		perf: d.Perf, caps: d.Caps,
		act: d.Actions, sched: d.Sched, jstore: d.Jobs, h: d.Hub, push: d.Push,
		backup: d.Backup, longOps: d.LongOps, repl: d.Repl,
		updater: d.Updater,
		started: time.Now(), version: d.Version, build: d.Build, zfsVersion: d.ZFSVersion,
		loginLimiter: newLoginLimiter(),
	}
}

// Handler monta el árbol de rutas: /api/login público, resto tras auth,
// mutaciones bloqueadas en modo demo.
func (s *Server) Handler() http.Handler {
	root := http.NewServeMux()
	root.HandleFunc("POST /api/login", s.login)
	// Público (sin sesión): el login consulta si el modo demo está habilitado.
	root.HandleFunc("GET /api/public/demo", s.publicDemo)

	a := http.NewServeMux()
	// sesión
	a.HandleFunc("POST /api/logout", s.logout)
	a.HandleFunc("GET /api/me", s.me)
	a.HandleFunc("PUT /api/me/language", s.putMyLanguage)
	a.HandleFunc("PUT /api/me/profile", s.putMyProfile)
	a.HandleFunc("PUT /api/me/avatar", s.putMyAvatar)
	a.HandleFunc("DELETE /api/me/avatar", s.deleteMyAvatar)
	a.HandleFunc("GET /api/avatars/{name}", s.getAvatar)
	a.HandleFunc("POST /api/me/password", s.changeMyPassword)
	// usuarios (admin)
	a.HandleFunc("GET /api/users", s.auth.RequireAdmin(s.listUsers))
	a.HandleFunc("POST /api/users", s.auth.RequireAdmin(s.createUser))
	a.HandleFunc("DELETE /api/users/{name}", s.auth.RequireAdmin(s.deleteUser))
	a.HandleFunc("POST /api/users/{name}/password", s.auth.RequireAdmin(s.setUserPassword))
	a.HandleFunc("PUT /api/users/{name}/language", s.auth.RequireAdmin(s.setUserLanguage))
	// sistema
	a.HandleFunc("GET /api/version", s.getVersion)
	a.HandleFunc("GET /api/settings", s.getSettings)
	a.HandleFunc("PUT /api/settings", s.auth.RequireAdmin(s.putSettings))
	a.HandleFunc("GET /api/activity", s.listActivity)
	a.HandleFunc("GET /api/alerts", s.listAlerts)
	a.HandleFunc("POST /api/alerts/{id}/ack", s.ackAlert)
	a.HandleFunc("GET /api/overview", s.getOverview)
	a.HandleFunc("GET /api/system-timers", s.listSystemTimers)
	a.HandleFunc("POST /api/system-timers/schedule", s.auth.RequireAdmin(s.sysTimerSchedule))
	a.HandleFunc("POST /api/system-timers/migrate", s.auth.RequireAdmin(s.sysTimerMigrate))
	// pools (mutaciones: admin — son potencialmente destructivas)
	a.HandleFunc("GET /api/pools", s.listPools)
	a.HandleFunc("POST /api/pools", s.auth.RequireAdmin(s.createPool))
	a.HandleFunc("POST /api/pools/import", s.auth.RequireAdmin(s.importPool))
	a.HandleFunc("POST /api/pools/{name}/scrub", s.auth.RequireAdmin(s.scrubPool))
	a.HandleFunc("POST /api/pools/{name}/export", s.auth.RequireAdmin(s.exportPool))
	a.HandleFunc("POST /api/pools/{name}/vdev", s.auth.RequireAdmin(s.addVdev))
	a.HandleFunc("POST /api/pools/{name}/vdev/action", s.auth.RequireAdmin(s.vdevAction))
	a.HandleFunc("POST /api/pools/{name}/replace", s.auth.RequireAdmin(s.replaceDisk))
	a.HandleFunc("POST /api/pools/{name}/autotrim", s.auth.RequireAdmin(s.setAutotrim))
	a.HandleFunc("POST /api/pools/{name}/checkpoint", s.auth.RequireAdmin(s.poolCheckpoint))
	a.HandleFunc("POST /api/pools/{name}/expand", s.auth.RequireAdmin(s.expandPool))
	a.HandleFunc("POST /api/pools/{name}/clear", s.auth.RequireAdmin(s.clearPool))
	a.HandleFunc("GET /api/pools/{name}/history", s.poolHistory)
	a.HandleFunc("GET /api/performance", s.getPerformance)
	// series históricas (U2): rangos con downsampling LTTB
	a.HandleFunc("GET /api/series", s.getSeries)
	// datasets
	a.HandleFunc("GET /api/datasets", s.listDatasets)
	a.HandleFunc("POST /api/datasets", s.auth.RequireAdmin(s.createDataset))
	a.HandleFunc("PATCH /api/datasets/{name}", s.auth.RequireAdmin(s.patchDataset))
	a.HandleFunc("PATCH /api/datasets/{name}/properties", s.auth.RequireAdmin(s.patchDatasetProps))
	a.HandleFunc("GET /api/datasets/{name}/properties", s.listDatasetProps)
	a.HandleFunc("PATCH /api/datasets/{name}/rename", s.auth.RequireAdmin(s.renameDataset))
	a.HandleFunc("DELETE /api/datasets/{name}", s.auth.RequireAdmin(s.deleteDataset))
	a.HandleFunc("POST /api/datasets/{name}/promote", s.auth.RequireAdmin(s.promoteDataset))
	a.HandleFunc("POST /api/datasets/{name}/mount", s.auth.RequireAdmin(s.mountDataset))
	a.HandleFunc("POST /api/datasets/{name}/unmount", s.auth.RequireAdmin(s.unmountDataset))
	a.HandleFunc("POST /api/datasets/{name}/rewrite", s.auth.RequireAdmin(s.rewriteDataset))
	a.HandleFunc("POST /api/datasets/{name}/unlock", s.auth.RequireAdmin(s.unlockDataset))
	a.HandleFunc("POST /api/datasets/{name}/lock", s.auth.RequireAdmin(s.lockDataset))
	a.HandleFunc("POST /api/datasets/{name}/change-key", s.auth.RequireAdmin(s.changeKeyDataset))
	a.HandleFunc("POST /api/datasets/{name}/properties/{prop}/inherit", s.auth.RequireAdmin(s.inheritDatasetProp))
	// operaciones largas (runner longops: rewrite, futura replicación)
	a.HandleFunc("GET /api/longops", s.listLongOps)
	a.HandleFunc("POST /api/longops/{id}/cancel", s.auth.RequireAdmin(s.cancelLongOp))
	// snapshots
	a.HandleFunc("GET /api/snapshots", s.listSnapshots)
	a.HandleFunc("GET /api/snapshots/diff", s.diffSnapshots)
	a.HandleFunc("POST /api/snapshots", s.auth.RequireAdmin(s.createSnapshot))
	a.HandleFunc("POST /api/snapshots/{full}/clone", s.auth.RequireAdmin(s.cloneSnapshot))
	a.HandleFunc("DELETE /api/snapshots/{full}", s.auth.RequireAdmin(s.deleteSnapshot))
	a.HandleFunc("POST /api/snapshots/{full}/rollback", s.auth.RequireAdmin(s.rollbackSnapshot))
	// jobs
	a.HandleFunc("GET /api/jobs", s.listJobs)
	a.HandleFunc("POST /api/jobs", s.auth.RequireAdmin(s.createJob))
	a.HandleFunc("GET /api/jobs/history", s.jobsHistory)
	a.HandleFunc("PATCH /api/jobs/{id}", s.auth.RequireAdmin(s.patchJob))
	a.HandleFunc("DELETE /api/jobs/{id}", s.auth.RequireAdmin(s.deleteJob))
	a.HandleFunc("POST /api/jobs/{id}/run", s.auth.RequireAdmin(s.runJob))
	// replicación ZFS send/recv (mutaciones: admin)
	a.HandleFunc("GET /api/replication", s.listReplication)
	a.HandleFunc("POST /api/replication", s.auth.RequireAdmin(s.createReplication))
	a.HandleFunc("PATCH /api/replication/{id}", s.auth.RequireAdmin(s.patchReplication))
	a.HandleFunc("DELETE /api/replication/{id}", s.auth.RequireAdmin(s.deleteReplication))
	a.HandleFunc("POST /api/replication/{id}/run", s.auth.RequireAdmin(s.runReplication))
	a.HandleFunc("GET /api/replication/sshkey", s.getReplicationSSHKey)
	a.HandleFunc("POST /api/replication/test", s.auth.RequireAdmin(s.testReplication))
	// discos
	a.HandleFunc("GET /api/disks", s.listDisks)
	a.HandleFunc("GET /api/recommendations", s.listRecommendations)
	a.HandleFunc("POST /api/disks/{dev}/smart-test", s.auth.RequireAdmin(s.smartTest))
	a.HandleFunc("GET /api/disks/{dev}/smart", s.diskSmart)
	a.HandleFunc("GET /api/disks/{dev}/smart-log", s.diskSmartLog)
	a.HandleFunc("POST /api/disks/{dev}/poweroff", s.auth.RequireAdmin(s.powerOff))
	// notificaciones push (Web Push; 503 push_not_configured sin claves VAPID)
	a.HandleFunc("GET /api/push/vapid-public-key", s.getPushVapidKey)
	a.HandleFunc("POST /api/push/subscribe", s.postPushSubscribe)
	a.HandleFunc("DELETE /api/push/unsubscribe", s.deletePushUnsubscribe)
	// preferencias de notificación (cada usuario gestiona las suyas)
	a.HandleFunc("GET /api/push/preferences", s.getPushPreferences)
	a.HandleFunc("PUT /api/push/preferences", s.putPushPreferences)
	a.HandleFunc("GET /api/push/quiet-hours", s.getPushQuietHours)
	a.HandleFunc("PUT /api/push/quiet-hours", s.putPushQuietHours)

	// Copia de seguridad de la BD (solo admin)
	a.HandleFunc("GET /api/backup/status", s.auth.RequireAdmin(s.backupStatus))
	a.HandleFunc("POST /api/backup/run", s.auth.RequireAdmin(s.backupRun))
	a.HandleFunc("GET /api/backup/download", s.auth.RequireAdmin(s.backupDownload))
	a.HandleFunc("POST /api/backup/import", s.auth.RequireAdmin(s.backupImport))
	// Actualizaciones (solo admin; wireUpdater registra las rutas si hay updater)
	s.wireUpdater(a)
	// SSE (con el usuario de la sesión para la regla no-duplicar push/SSE)
	a.Handle("GET /api/events", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.h.ServeSSE(w, r, actor(r))
	}))

	root.Handle("/api/", s.auth.Middleware(s.rateGuard(s.csrfGuard(s.demoGuard(a)))))
	return root
}

// rateGuard — limita mutaciones a 30 por minuto por IP. Solo lectura (GET/HEAD)
// sin límite. El bucket es en memoria (sin persistencia, aceptable para LAN).
type rateGuardBucket struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func (b *rateGuardBucket) allow(ip string, now time.Time, max int, window time.Duration) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	list := b.hits[ip]
	cutoff := now.Add(-window)
	j := 0
	for ; j < len(list) && list[j].Before(cutoff); j++ {
	}
	list = list[j:]
	if len(list) >= max {
		b.hits[ip] = list
		return false
	}
	list = append(list, now)
	b.hits[ip] = list
	return true
}

var rateGuardGlobal = &rateGuardBucket{hits: map[string][]time.Time{}}

func (s *Server) rateGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		ip := r.RemoteAddr
		if idx := strings.LastIndex(ip, ":"); idx != -1 {
			ip = ip[:idx]
		}
		if !rateGuardGlobal.allow(ip, time.Now(), 30, time.Minute) {
			writeErr(w, http.StatusTooManyRequests, "rate_limited",
				"demasiadas peticiones; inténtalo de nuevo en unos segundos")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// csrfGuard — si CSRF_CHECK=1, valida Origin/Referer contra Host en mutaciones
// (POST/PUT/PATCH/DELETE). Por defecto DESACTIVADO (SameSite=Lax es suficiente en
// LAN); activar solo si se expone a internet con COOKIE_SECURE=1 y TLS.
func (s *Server) csrfGuard(next http.Handler) http.Handler {
	check := os.Getenv("CSRF_CHECK")
	if check != "1" && check != "true" {
		return next // desactivado por defecto → sin sobrecarga
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		origin := r.Header.Get("Origin")
		if origin == "" {
			origin = r.Header.Get("Referer")
		}
		if origin != "" {
			host := r.Host
			if strings.HasPrefix(origin, "https://") {
				origin = origin[8:]
			} else if strings.HasPrefix(origin, "http://") {
				origin = origin[7:]
			}
			if i := strings.Index(origin, "/"); i != -1 {
				origin = origin[:i]
			}
			if i := strings.Index(origin, ":"); i != -1 {
				origin = origin[:i]
			}
			if i := strings.Index(host, ":"); i != -1 {
				host = host[:i]
			}
			if origin != host {
				writeErr(w, http.StatusForbidden, "csrf",
					"petición rechazada: origen '"+origin+"' no coincide con el host '"+host+"'")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// demoGuard — en DEMO=1 las mutaciones devuelven 403 demo_mode
// (excepto logout y ack de alertas, inofensivas y necesarias para la demo).
func (s *Server) demoGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.Demo && r.Method != http.MethodGet && r.Method != http.MethodHead {
			allowed := r.URL.Path == "/api/logout" ||
				(strings.HasPrefix(r.URL.Path, "/api/alerts/") && strings.HasSuffix(r.URL.Path, "/ack"))
			if !allowed {
				writeErr(w, http.StatusForbidden, "demo_mode", "modo demo: las mutaciones están desactivadas")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// --- helpers ---

// writeJSON serializa v con el código dado.
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("httpapi: encode: %v", err)
	}
}

// writeErr — formato de error del contrato.
func writeErr(w http.ResponseWriter, code int, errCode, msg string) {
	writeJSON(w, code, map[string]string{"error": errCode, "message": msg})
}

// decodeJSON decodifica el body; false = ya se escribió el error 400.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(dst); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_json", "body JSON inválido: "+err.Error())
		return false
	}
	return true
}

// requireConfirm valida {"confirm":"<target>"} en destructivas (lección 6).
func requireConfirm(w http.ResponseWriter, confirm, target string) bool {
	if confirm != target || target == "" {
		writeErr(w, http.StatusBadRequest, "confirm_required",
			"se requiere {\"confirm\":\""+target+"\"} para confirmar la operación")
		return false
	}
	return true
}

// actor — usuario autenticado para el audit_log.
func actor(r *http.Request) string {
	return auth.UserFromContext(r.Context())
}

// actionErr traduce errores de actions/scheduler a respuestas HTTP.
func actionErr(w http.ResponseWriter, err error) {
	switch {
	case err == nil:
		return
	case strings.Contains(err.Error(), "inválid"):
		writeErr(w, http.StatusBadRequest, "invalid_input", err.Error())
	default:
		writeErr(w, http.StatusInternalServerError, "exec_error", err.Error())
	}
}

// memRSS — RSS aproximado del proceso vía runtime.ReadMemStats.
func memRSS() uint64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.Sys
}
