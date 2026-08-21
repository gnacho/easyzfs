// snapshots.go — endpoints de snapshots.
package httpapi

import (
	"net/http"
	"strings"
)

type snapshotRefresher interface{ RefreshSoon() }

func refreshSnapshots(pools any) {
	if r, ok := pools.(snapshotRefresher); ok {
		r.RefreshSoon()
	}
}

// listSnapshots — GET /api/snapshots?dataset= (agrupado por dataset).
func (s *Server) listSnapshots(w http.ResponseWriter, r *http.Request) {
	groups := s.pools.SnapshotGroups()
	if ds := r.URL.Query().Get("dataset"); ds != "" {
		filtered := groups[:0]
		for _, g := range groups {
			if g.Dataset == ds {
				filtered = append(filtered, g)
			}
		}
		writeJSON(w, http.StatusOK, filtered)
		return
	}
	writeJSON(w, http.StatusOK, groups)
}

// diffSnapshots — GET /api/snapshots/diff?from=<full>&to=<full> → [DiffEntry].
func (s *Server) diffSnapshots(w http.ResponseWriter, r *http.Request) {
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")
	if from == "" || to == "" {
		writeErr(w, http.StatusBadRequest, "invalid_input", "from y to son requeridos (dataset@snapshot)")
		return
	}
	entries, err := s.act.SnapshotDiff(r.Context(), from, to)
	if err != nil {
		actionErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

// createSnapshot — POST /api/snapshots {dataset, name, recursive} → 201.
func (s *Server) createSnapshot(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Dataset   string `json:"dataset"`
		Name      string `json:"name"`
		Recursive bool   `json:"recursive"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Name == "" {
		writeErr(w, http.StatusBadRequest, "invalid_input", "name requerido")
		return
	}
	if err := s.act.SnapshotCreate(r.Context(), actor(r), body.Dataset, body.Name, body.Recursive); err != nil {
		actionErr(w, err)
		return
	}
	refreshSnapshots(s.pools)
	writeJSON(w, http.StatusCreated, map[string]string{"full": body.Dataset + "@" + body.Name})
}

// deleteSnapshot — DELETE /api/snapshots/{full} {confirm} → 204.
// full = 'tank/docs@snap' URL-encoded (%2F se decodifica por segmento).
func (s *Server) deleteSnapshot(w http.ResponseWriter, r *http.Request) {
	full := r.PathValue("full")
	var body struct {
		Confirm string `json:"confirm"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if !strings.Contains(full, "@") {
		writeErr(w, http.StatusBadRequest, "invalid_input", "se esperaba 'dataset@snapshot'")
		return
	}
	if !requireConfirm(w, body.Confirm, full) {
		return
	}
	if err := s.act.SnapshotDelete(r.Context(), actor(r), full); err != nil {
		refreshSnapshots(s.pools)
		actionErr(w, err)
		return
	}
	refreshSnapshots(s.pools)
	w.WriteHeader(http.StatusNoContent)
}

// cloneSnapshot — POST /api/snapshots/{full}/clone {target, mountpoint?} → 201.
func (s *Server) cloneSnapshot(w http.ResponseWriter, r *http.Request) {
	full := r.PathValue("full")
	var body struct {
		Target     string `json:"target"`
		Mountpoint string `json:"mountpoint"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Target == "" {
		writeErr(w, http.StatusBadRequest, "invalid_input", "target requerido (nombre completo del nuevo dataset)")
		return
	}
	if s.cfg.Mock {
		// MOCK=1: sin zfs real; simulado sobre la caché.
		type cloner interface{ Clone(snapshot, target string) }
		if m, ok := s.pools.(cloner); ok {
			m.Clone(full, body.Target)
		}
		s.act.AuditOnly(r.Context(), actor(r), "snapshot.clone", body.Target,
			map[string]any{"snapshot": full, "mountpoint": body.Mountpoint})
		writeJSON(w, http.StatusCreated, map[string]string{"name": body.Target})
		return
	}
	if err := s.act.SnapshotClone(r.Context(), actor(r), full, body.Target, body.Mountpoint); err != nil {
		actionErr(w, err)
		return
	}
	refreshSnapshots(s.pools)
	writeJSON(w, http.StatusCreated, map[string]string{"name": body.Target})
}

// rollbackSnapshot — POST /api/snapshots/{full}/rollback {confirm} → 202.
func (s *Server) rollbackSnapshot(w http.ResponseWriter, r *http.Request) {
	full := r.PathValue("full")
	var body struct {
		Confirm string `json:"confirm"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if !requireConfirm(w, body.Confirm, full) {
		return
	}
	if err := s.act.SnapshotRollback(r.Context(), actor(r), full); err != nil {
		refreshSnapshots(s.pools)
		actionErr(w, err)
		return
	}
	refreshSnapshots(s.pools)
	w.WriteHeader(http.StatusAccepted)
}
