// update_handlers.go — /api/update/*: estado y aplicación de actualizaciones.
// Patrón app-auto-update: GET /api/update/status (admin, consulta GitHub bajo
// demanda) y POST /api/update/apply (admin, descarga+valida y toca el flag para
// que easyzfs-update.path reinicie con el binario nuevo).
package httpapi

import (
	"context"
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

// postUpdateApply — POST /api/update/apply (admin). Descarga+valida el binario
// nuevo y toca el flag; el servicio se reinicia vía easyzfs-update.path.
func (s *Server) postUpdateApply(w http.ResponseWriter, r *http.Request) {
	if s.updater == nil {
		writeErr(w, http.StatusServiceUnavailable, "update_unavailable", "actualizaciones desactivadas (sin DATA_DIR)")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	if err := s.updater.Apply(ctx); err != nil {
		writeErr(w, http.StatusConflict, "update_apply_failed", err.Error())
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
	a.HandleFunc("POST /api/update/apply", s.auth.RequireAdmin(s.postUpdateApply))
}

var _ = updater.Status{} // mantener el import si el updater solo se usa vía wiring
