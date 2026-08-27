// update_handlers.go — /api/update/*: estado y aplicación de actualizaciones.
// Patrón app-auto-update: GET /api/update/status (admin, consulta GitHub bajo
// demanda), GET /api/update/plan (admin, comprobaciones pre-vuelo) y
// POST /api/update/apply (admin, descarga+valida y toca el flag para
// que easyzfs-update.path reinicie con el binario nuevo).
package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"easyzfs/internal/updater"
)

// getUpdateStatus — GET /api/update/status (admin).
func (s *Server) getUpdateStatus(w http.ResponseWriter, r *http.Request) {
	if s.updater == nil {
		writeErr(w, http.StatusServiceUnavailable, "update_unavailable", "actualizaciones desactivadas (sin DATA_DIR)")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	st, err := s.updater.Check(ctx)
	if err != nil {
		// Sin red / rate-limit: devolver el estado conocido, no 500.
		log.Printf("update: check: %v", err)
		writeJSON(w, http.StatusOK, s.updater.Status())
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// getUpdatePlan — GET /api/update/plan (admin). Comprobaciones pre-vuelo.
func (s *Server) getUpdatePlan(w http.ResponseWriter, r *http.Request) {
	if s.updater == nil {
		writeErr(w, http.StatusServiceUnavailable, "update_unavailable", "actualizaciones desactivadas (sin DATA_DIR)")
		return
	}
	writeJSON(w, http.StatusOK, s.updater.Plan())
}

// postUpdateApply — POST /api/update/apply (admin). Descarga+valida el binario
// nuevo y toca el flag; el servicio se reinicia vía easyzfs-update.path.
func (s *Server) postUpdateApply(w http.ResponseWriter, r *http.Request) {
	if s.updater == nil {
		writeErr(w, http.StatusServiceUnavailable, "update_unavailable", "actualizaciones desactivadas (sin DATA_DIR)")
		return
	}
	st := s.updater.Status()
	eid := s.recordUpdateStart("admin", st.Current, st.Latest)
	start := time.Now()
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	err := s.updater.Apply(ctx)
	s.recordUpdateResult(eid, err == nil, time.Since(start).Milliseconds())
	if err != nil {
		writeErr(w, http.StatusConflict, "update_apply_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "restarting": true})
}

// getUpdateStream — GET /api/update/stream (admin). SSE con el estado del
// update en cada cambio de paso/progreso: evento inicial con el estado
// completo y luego un evento por cambio. Heartbeat de 15 s mantiene la
// conexión viva. El stream MUERE con el proceso en el reinicio final, así
// que el cliente debe tratarlo como fase "restarting" y sondear /api/health.
func (s *Server) getUpdateStream(w http.ResponseWriter, r *http.Request) {
	if s.updater == nil {
		writeErr(w, http.StatusServiceUnavailable, "update_unavailable", "actualizaciones desactivadas (sin DATA_DIR)")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming_unsupported", "SSE no soportado por el servidor")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	ch, cancel := s.updater.Subscribe()
	defer cancel()
	writeEvent := func(st updater.Status) {
		raw, err := json.Marshal(st)
		if err != nil {
			return
		}
		fmt.Fprintf(w, "event: update\ndata: %s\n\n", raw)
		flusher.Flush()
	}
	writeEvent(s.updater.Status())
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			// Comentario SSE: mantiene viva la conexión sin evento.
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case st := <-ch:
			writeEvent(st)
		}
	}
}

// postUpdateRollback — POST /api/update/rollback (admin). Restaura el binario
// anterior (.old) y toca el flag para que easyzfs-update.path reinicie.
func (s *Server) postUpdateRollback(w http.ResponseWriter, r *http.Request) {
	if s.updater == nil {
		writeErr(w, http.StatusServiceUnavailable, "update_unavailable", "actualizaciones desactivadas (sin DATA_DIR)")
		return
	}
	if err := s.updater.Rollback(); err != nil {
		writeErr(w, http.StatusConflict, "update_rollback_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "restarting": true})
}

// wireUpdater registra las rutas de actualización si el updater está disponible.
func (s *Server) wireUpdater(a *http.ServeMux) {
	if s.updater == nil {
		return
	}
	a.HandleFunc("GET /api/update/status", s.auth.RequireAdmin(s.getUpdateStatus))
	a.HandleFunc("GET /api/update/stream", s.auth.RequireAdmin(s.getUpdateStream))
	a.HandleFunc("GET /api/update/plan", s.auth.RequireAdmin(s.getUpdatePlan))
	a.HandleFunc("GET /api/updates/history", s.auth.RequireAdmin(s.getUpdateHistory))
	a.HandleFunc("POST /api/update/apply", s.auth.RequireAdmin(s.postUpdateApply))
	a.HandleFunc("POST /api/update/rollback", s.auth.RequireAdmin(s.postUpdateRollback))
}

var _ = updater.Status{} // mantener el import si el updater solo se usa vía wiring
