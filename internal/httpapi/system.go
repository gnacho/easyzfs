// system.go — /api/version, /api/settings, /api/alerts, /api/overview.
package httpapi

import (
	"context"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"easyzfs/internal/db"
	"easyzfs/internal/model"
	"easyzfs/internal/updater"
)

func pendingUpdateJSON(u *updater.Updater) any {
	if u == nil {
		return nil
	}
	p := u.ConsumePendingApply()
	if p == nil {
		return nil
	}
	return map[string]any{
		"from": p.FromVersion,
		"to":   p.ToVersion,
	}
}

// getVersion — GET /api/version → estado del backend y del runtime.
func (s *Server) getVersion(w http.ResponseWriter, r *http.Request) {
	caps := model.Capabilities{Version: s.zfsVersion}
	if s.caps != nil {
		if c := s.caps.Capabilities(); c.Version != "" {
			caps = c
		}
	}
	out := map[string]any{
		"name":          "EasyZFS",
		"version":       s.version,
		"build":         s.build,
		"go":            runtime.Version(),
		"os_arch":       runtime.GOOS + "/" + runtime.GOARCH,
		"uptime_sec":    int64(time.Since(s.started).Seconds()),
		"rss_bytes":     memRSS(),
		"db_bytes":      db.SizeBytes(s.cfg.DBPath),
		"db_path":       s.cfg.DBPath,
		"zfs_version":   caps.Version,
		"capabilities":  caps,
		"demo":          s.cfg.Demo,
		"pendingUpdate": pendingUpdateJSON(s.updater),
	}
	// Métricas del host (best effort: se omiten si no se pueden leer).
	if l1, l5, l15, ok := loadAvg(); ok {
		out["load1"], out["load5"], out["load15"] = l1, l5, l15
	}
	if total, avail, ok := memInfo(); ok {
		out["mem_total_bytes"], out["mem_avail_bytes"] = total, avail
	}
	if hit, size, ok := arcStats(); ok {
		out["arc_hit_pct"], out["arc_size_bytes"] = hit, size
	}
	writeJSON(w, http.StatusOK, out)
}

// loadAvg lee /proc/loadavg (load 1/5/15).
func loadAvg() (l1, l5, l15 float64, ok bool) {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, 0, 0, false
	}
	f := strings.Fields(string(b))
	if len(f) < 3 {
		return 0, 0, 0, false
	}
	l1, err1 := strconv.ParseFloat(f[0], 64)
	l5, err5 := strconv.ParseFloat(f[1], 64)
	l15, err15 := strconv.ParseFloat(f[2], 64)
	if err1 != nil || err5 != nil || err15 != nil {
		return 0, 0, 0, false
	}
	return l1, l5, l15, true
}

// memInfo lee MemTotal/MemAvailable de /proc/meminfo (en bytes).
func memInfo() (total, avail uint64, ok bool) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0, false
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		kb, err := strconv.ParseUint(f[1], 10, 64)
		if err != nil {
			continue
		}
		switch strings.TrimSuffix(f[0], ":") {
		case "MemTotal":
			total = kb * 1024
		case "MemAvailable":
			avail = kb * 1024
		}
	}
	return total, avail, total > 0
}

// arcStats calcula el hit ratio del ARC de ZFS a partir de
// /proc/spl/kstat/zfs/arcstats (hits/(hits+misses)). Devuelve también el
// tamaño actual del ARC en bytes.
func arcStats() (hitPct float64, sizeBytes uint64, ok bool) {
	b, err := os.ReadFile("/proc/spl/kstat/zfs/arcstats")
	if err != nil {
		return 0, 0, false
	}
	var hits, misses, size uint64
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		v, err := strconv.ParseUint(f[2], 10, 64)
		if err != nil {
			continue
		}
		switch f[0] {
		case "hits":
			hits = v
		case "misses":
			misses = v
		case "size":
			size = v
		}
	}
	if hits+misses == 0 {
		return 0, size, false
	}
	return float64(hits) / float64(hits+misses) * 100, size, true
}

// getSettings — GET /api/settings.
func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	st, err := s.settings.Load(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// publicDemo — GET /api/public/demo (sin auth): el login lo consulta para
// mostrar u ocultar el botón "Entrar como demo" según el ajuste del admin.
// demo_server indica que ESTE servidor es el despliegue demo (DEMO=1); el
// frontend lo usa para resolver el idioma por defecto (auto → inglés).
func (s *Server) publicDemo(w http.ResponseWriter, r *http.Request) {
	st, err := s.settings.Load(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{
		"demo_enabled": st.DemoEnabled,
		"demo_server":  s.cfg.Demo,
	})
}

// putSettings — PUT /api/settings (admin) → 204. 400 si los rangos no valen.
func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var st settingsBody
	if !decodeJSON(w, r, &st) {
		return
	}
	if msg := validateSettings(st); msg != "" {
		writeErr(w, http.StatusBadRequest, "invalid_input", msg)
		return
	}
	cur, err := s.settings.Load(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	cur.Lang = st.Lang
	cur.CapWarnPct = st.CapWarnPct
	cur.CapCritPct = st.CapCritPct
	cur.DiskTempC = st.DiskTempC
	cur.Webhook = st.Webhook
	cur.NotifyScrubErrors = st.NotifyScrubErrors
	cur.NotifySmartChange = st.NotifySmartChange
	cur.DemoEnabled = st.DemoEnabled
	cur.BackupEnabled = st.BackupEnabled
	cur.BackupFreqHours = st.BackupFreqHours
	cur.BackupRetentionDays = st.BackupRetentionDays
	if err := s.settings.Save(r.Context(), cur); err != nil {
		writeErr(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	s.act.AuditOnly(r.Context(), actor(r), "settings.update", "settings", st)
	w.WriteHeader(http.StatusNoContent)
}

// validateSettings — rangos duros (400 en vez de corrección silenciosa):
// cap_warn_pct/cap_crit_pct en 1-100, warn < crit, disk_temp_c en 20-90.
// Devuelve "" si todo es válido o el mensaje de error.
func validateSettings(st settingsBody) string {
	if st.CapWarnPct < 1 || st.CapWarnPct > 100 {
		return "cap_warn_pct debe estar entre 1 y 100"
	}
	if st.CapCritPct < 1 || st.CapCritPct > 100 {
		return "cap_crit_pct debe estar entre 1 y 100"
	}
	if st.CapWarnPct >= st.CapCritPct {
		return "cap_warn_pct debe ser menor que cap_crit_pct"
	}
	if st.DiskTempC < 20 || st.DiskTempC > 90 {
		return "disk_temp_c debe estar entre 20 y 90"
	}
	return ""
}

// settingsBody — body de PUT /api/settings (idéntico a settings.Settings).
type settingsBody struct {
	Lang                string `json:"lang"`
	CapWarnPct          int    `json:"cap_warn_pct"`
	CapCritPct          int    `json:"cap_crit_pct"`
	DiskTempC           int    `json:"disk_temp_c"`
	Webhook             string `json:"webhook"`
	NotifyScrubErrors   bool   `json:"notify_scrub_errors"`
	NotifySmartChange   bool   `json:"notify_smart_change"`
	DemoEnabled         bool   `json:"demo_enabled"`
	BackupEnabled       bool   `json:"backup_enabled"`
	BackupFreqHours     int    `json:"backup_freq_hours"`
	BackupRetentionDays int    `json:"backup_retention_days"`
}

// listAlerts — GET /api/alerts → últimas 100.
func (s *Server) listAlerts(w http.ResponseWriter, r *http.Request) {
	list, err := s.alerter.List(r.Context(), 100)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// ackAlert — POST /api/alerts/{id}/ack → 204.
func (s *Server) ackAlert(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "id de alerta inválido")
		return
	}
	if err := s.alerter.Ack(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// listSystemTimers — GET /api/system-timers → {timers, systemd_available}:
// tareas del sistema (cron + systemd timers, solo lectura, desde la caché del
// colector schedsys) y si systemd está disponible como init (la UI oculta la
// opción de cambio cron→systemd cuando no lo está).
func (s *Server) listSystemTimers(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"timers":            s.sysTimers.SysTimers(),
		"systemd_available": s.sysTimers.SystemdAvailable(),
	})
}

// findSysTimer — busca la tarea en la caché del colector por identidad
// (source+name+origin+line): el cliente no puede inventar objetivos.
func (s *Server) findSysTimer(b sysTimerID) (model.SysTimer, bool) {
	for _, t := range s.sysTimers.SysTimers() {
		if t.Source == b.Source && t.Name == b.Name && t.Origin == b.Origin && t.Line == b.Line {
			return t, true
		}
	}
	return model.SysTimer{}, false
}

// sysTimerID — identidad de una tarea del sistema tal como la conoce la UI.
type sysTimerID struct {
	Source string `json:"source"`
	Name   string `json:"name"`
	Origin string `json:"origin"`
	Line   int    `json:"line"`
}

// sysTimerSchedule — POST /api/system-timers/schedule {id…, schedule} → 202.
func (s *Server) sysTimerSchedule(w http.ResponseWriter, r *http.Request) {
	var body struct {
		sysTimerID
		Schedule string `json:"schedule"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	task, ok := s.findSysTimer(body.sysTimerID)
	if !ok || !task.Editable {
		writeErr(w, http.StatusNotFound, "not_found", "tarea no encontrada o no editable")
		return
	}
	if err := s.act.SysTaskSetSchedule(r.Context(), actor(r), task, body.Schedule); err != nil {
		actionErr(w, err)
		return
	}
	s.refreshSysTimers(r.Context())
	w.WriteHeader(http.StatusAccepted)
}

// sysTimerMigrate — POST /api/system-timers/migrate {id…, new_name} → 202.
func (s *Server) sysTimerMigrate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		sysTimerID
		NewName string `json:"new_name"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	task, ok := s.findSysTimer(body.sysTimerID)
	if !ok || !task.Editable || task.Source != "cron" {
		writeErr(w, http.StatusNotFound, "not_found", "tarea no encontrada o no migrable")
		return
	}
	// Defensa en backend (la UI oculta el botón, pero el endpoint es público):
	// sin systemd no se puede crear un timer.
	if !s.sysTimers.SystemdAvailable() {
		writeErr(w, http.StatusBadRequest, "systemd_unavailable",
			"systemd no está disponible en este sistema; no se puede cambiar a systemd timer")
		return
	}
	if err := s.act.SysTaskMigrate(r.Context(), actor(r), task, body.NewName); err != nil {
		actionErr(w, err)
		return
	}
	s.refreshSysTimers(r.Context())
	w.WriteHeader(http.StatusAccepted)
}

// refreshSysTimers — fuerza una pasada del colector si lo soporta (no esperar
// al tick de 5 min tras una edición).
func (s *Server) refreshSysTimers(ctx context.Context) {
	type refresher interface{ Refresh(context.Context) }
	if r, ok := s.sysTimers.(refresher); ok {
		r.Refresh(ctx)
	}
}

// getOverview — GET /api/overview: KPIs agregados desde cachés + BD.
func (s *Server) getOverview(w http.ResponseWriter, r *http.Request) {
	pools := s.pools.Pools()
	snaps := s.pools.SnapshotGroups()

	ov := map[string]any{}
	ov["pools_total"] = len(pools)
	online := 0
	var used, total uint64
	var lastScrub *model.ScrubInfo
	var lastScrubPool string
	snapCount := 0
	for _, p := range pools {
		if p.Status == "ONLINE" {
			online++
		}
		used += p.UsedBytes
		total += p.TotalBytes
		if p.Scrub.State == "done" && (lastScrub == nil || p.Scrub.Ts.After(lastScrub.Ts)) {
			sc := p.Scrub
			lastScrub = &sc
			lastScrubPool = p.Name
		}
	}
	for _, g := range snaps {
		snapCount += len(g.Snaps)
	}
	ov["pools_online"] = online
	ov["cap_used_bytes"] = used
	ov["cap_total_bytes"] = total
	ov["snapshots_total"] = snapCount

	jobs, err := s.jstore.List(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	active := 0
	for _, j := range jobs {
		if j.Enabled {
			active++
		}
	}
	ov["jobs_active"] = active

	ls := map[string]any{"pool": lastScrubPool}
	if lastScrub != nil {
		ls["ts"] = lastScrub.Ts
		ls["errors"] = lastScrub.Errors
	} else {
		ls["pool"] = ""
		ls["ts"] = nil
		ls["errors"] = 0
	}
	ov["last_scrub"] = ls

	alerts, err := s.alerter.List(r.Context(), 3)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	ov["alerts"] = alerts

	activity, err := s.recentActivity(r, 10)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	ov["activity"] = activity

	writeJSON(w, http.StatusOK, ov)
}

// activityEntry — entrada de actividad reciente (audit_log).
type activityEntry struct {
	Ts     string `json:"ts"`
	Text   string `json:"text"`
	Detail string `json:"detail"`
}

// listActivity — GET /api/activity?limit=N → historial auditado completo
// (con paginación simple por límite). Para el "Ver más" de Ajustes → Admin.
func (s *Server) listActivity(w http.ResponseWriter, r *http.Request) {
	limit := 30
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	out, err := s.recentActivity(r, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// recentActivity — últimas acciones auditadas como actividad del dashboard.
func (s *Server) recentActivity(r *http.Request, limit int) ([]activityEntry, error) {
	rows, err := s.db.QueryContext(r.Context(),
		"SELECT ts, action, actor, target FROM audit_log ORDER BY id DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []activityEntry{}
	for rows.Next() {
		var e activityEntry
		var actorName, target string
		if err := rows.Scan(&e.Ts, &e.Text, &actorName, &target); err != nil {
			return nil, err
		}
		if t, err := time.Parse(time.RFC3339, e.Ts); err == nil {
			e.Ts = t.UTC().Format(time.RFC3339)
		}
		e.Detail = target
		if actorName != "" {
			e.Detail = actorName + " · " + target
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
