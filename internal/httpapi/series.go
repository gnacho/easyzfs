package httpapi

import (
	"net/http"
	"strconv"

	"easyzfs/internal/series"
)

// getSeries — GET /api/series?source=pool.tank.used_pct&days=7&points=800
// Devuelve la serie muestreada (LTTB) del rango pedido. days 1-365, points
// 50-2000. Rango sin datos → {points: []} con 200 (un hueco es estado normal).
func (s *Server) getSeries(w http.ResponseWriter, r *http.Request) {
	src := r.URL.Query().Get("source")
	if !series.ValidSource(src) {
		writeErr(w, http.StatusBadRequest, "invalid_source",
			"source debe ser pool.<nombre>.used_pct o disk.<dev>.temp")
		return
	}
	days, err := strconv.Atoi(r.URL.Query().Get("days"))
	if err != nil {
		days = 7
	}
	from, to, err := series.ParseDays(days)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_days", err.Error())
		return
	}
	points := 800
	if v := r.URL.Query().Get("points"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 50 && n <= 2000 {
			points = n
		}
	}
	pts, err := series.Range(r.Context(), s.db, src, from, to, points)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	if pts == nil {
		pts = []series.Point{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"source": src, "points": pts})
}
