// apikeys_handlers.go — gestión de API keys de solo lectura (admin, #87).
// La clave en claro solo se devuelve en la respuesta de creación.
package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"easyzfs/internal/apikeys"
)

// listAPIKeys — GET /api/keys → [{id, name, created_at, last_used}].
func (s *Server) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := s.apiKeys.List(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
}

// createAPIKey — POST /api/keys {name} → {id, name, key}.
// La clave en claro (ez_…) se devuelve ÚNICAMENTE aquí.
func (s *Server) createAPIKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if len(body.Name) == 0 || len(body.Name) > 32 {
		writeErr(w, http.StatusBadRequest, "invalid_name", "nombre de clave requerido (máx. 32 caracteres)")
		return
	}
	key, err := s.apiKeys.Create(r.Context(), body.Name)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": body.Name, "key": key})
}

// deleteAPIKey — DELETE /api/keys/{id} → 204.
func (s *Server) deleteAPIKey(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "id inválido")
		return
	}
	if err := s.apiKeys.Delete(r.Context(), id); err != nil {
		if errors.Is(err, apikeys.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", err.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
