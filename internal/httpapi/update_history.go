// update_history.go — historial de actualizaciones (issue #28).
// GET /api/updates/history y registro en cada apply.
package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
)

func newEventID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// getUpdateHistory — GET /api/updates/history (admin).
func (s *Server) getUpdateHistory(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(
		"SELECT event_id, timestamp, action, channel, version_from, version_to, initiated_by, status, duration_ms, notes FROM update_history ORDER BY timestamp DESC LIMIT 30")
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"entries": []any{}})
		return
	}
	defer rows.Close()
	var entries []map[string]any
	for rows.Next() {
		var eid, ts, action, channel, vfrom, vto, status string
		var initiatedBy, notes *string
		var durationMs *int64
		if err := rows.Scan(&eid, &ts, &action, &channel, &vfrom, &vto, &initiatedBy, &status, &durationMs, &notes); err != nil {
			continue
		}
		entry := map[string]any{
			"event_id": eid, "timestamp": ts, "action": action, "channel": channel,
			"version_from": vfrom, "version_to": vto, "status": status,
		}
		if initiatedBy != nil {
			entry["initiated_by"] = *initiatedBy
		}
		if durationMs != nil {
			entry["duration_ms"] = *durationMs
		}
		if notes != nil {
			entry["notes"] = *notes
		}
		entries = append(entries, entry)
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

// recordUpdateStart escribe en update_history antes de aplicar.
func (s *Server) recordUpdateStart(user string, fromVer, toVer string) string {
	eid := newEventID()
	s.db.Exec(
		"INSERT INTO update_history (event_id, timestamp, action, channel, version_from, version_to, initiated_by, status) VALUES (?, datetime('now'), 'update', 'stable', ?, ?, ?, 'started')",
		eid, fromVer, toVer, user)
	return eid
}

// recordUpdateResult actualiza la entrada tras el apply.
func (s *Server) recordUpdateResult(eid string, success bool, durationMs int64) {
	status := "applied"
	if !success {
		status = "failed"
	}
	msg := ""
	if !success {
		msg = fmt.Sprintf("apply failed after %d ms", durationMs)
	}
	s.db.Exec(
		"UPDATE update_history SET status = ?, duration_ms = ?, notes = ? WHERE event_id = ?",
		status, durationMs, msg, eid)
}
