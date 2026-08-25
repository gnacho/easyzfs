// datasets.go — endpoints de datasets y volúmenes (incl. cifrado nativo, lote D).
package httpapi

import (
	"net/http"

	"easyzfs/internal/model"
)

// keyMutator — mutaciones simuladas de cifrado sobre la caché del mock (MOCK=1).
type keyMutator interface {
	SetKeyStatus(name, status string)
}

// listDatasets — GET /api/datasets (caché del colector).
func (s *Server) listDatasets(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.pools.Datasets())
}

// createDataset — POST /api/datasets {pool, name, type, compression, atime?,
// quota_bytes, volsize_bytes?, encryption?, passphrase?} → 201.
// encryption:true crea con cifrado nativo AES-256-GCM; la passphrase viaja
// solo en el body JSON de esta request (NUNCA en URL, logs ni audit_log).
// atime (opcional): "" (heredar del pool) | "on" | "off" | "relatime".
func (s *Server) createDataset(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Pool         string `json:"pool"`
		Name         string `json:"name"`
		Type         string `json:"type"`
		Compression  string `json:"compression"`
		Atime        string `json:"atime"`
		QuotaBytes   uint64 `json:"quota_bytes"`
		VolsizeBytes uint64 `json:"volsize_bytes"`
		Encryption   bool   `json:"encryption"`
		Passphrase   string `json:"passphrase"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Type == "" {
		body.Type = "fs"
	}
	if body.Compression == "" {
		body.Compression = "lz4"
	}
	if body.Encryption && len(body.Passphrase) < 8 {
		writeErr(w, http.StatusBadRequest, "invalid_input",
			"la passphrase debe tener al menos 8 caracteres")
		return
	}
	if s.cfg.Mock {
		// MOCK=1: sin zfs real; el dataset aparece en la caché del mock.
		type dsAdder interface {
			AddDataset(name, typ, compression string, encrypted bool)
		}
		if m, ok := s.pools.(dsAdder); ok {
			m.AddDataset(body.Pool+"/"+body.Name, body.Type, body.Compression, body.Encryption)
		}
		s.act.AuditOnly(r.Context(), actor(r), "dataset.create", body.Pool+"/"+body.Name,
			map[string]any{"type": body.Type, "compression": body.Compression, "encrypted": body.Encryption})
		writeJSON(w, http.StatusCreated, map[string]string{"name": body.Pool + "/" + body.Name})
		return
	}
	if err := s.act.DatasetCreate(r.Context(), actor(r), body.Pool, body.Name,
		body.Type, body.Compression, body.QuotaBytes, body.VolsizeBytes,
		body.Encryption, body.Passphrase, body.Atime); err != nil {
		actionErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"name": body.Pool + "/" + body.Name})
}

// findDataset — dataset de la caché del colector (nil si no existe).
func (s *Server) findDataset(name string) *model.Dataset {
	for _, d := range s.pools.Datasets() {
		if d.Name == name {
			dd := d
			return &dd
		}
	}
	return nil
}

// requireEncrypted — valida que el dataset existe y tiene cifrado nativo.
func (s *Server) requireEncrypted(w http.ResponseWriter, name string) bool {
	d := s.findDataset(name)
	if d == nil {
		writeErr(w, http.StatusNotFound, "not_found", "dataset no encontrado")
		return false
	}
	if d.Encryption == "" || d.Encryption == "off" || d.Encryption == "-" {
		writeErr(w, http.StatusBadRequest, "invalid_input", "el dataset no está cifrado")
		return false
	}
	return true
}

// unlockDataset — POST /api/datasets/{name}/unlock {key} (admin) → 204.
// La passphrase va solo en el body JSON (memoria de la request; jamás en
// URL, logs ni audit_log).
func (s *Server) unlockDataset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body struct {
		Key string `json:"key"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Key == "" {
		writeErr(w, http.StatusBadRequest, "invalid_input", "se requiere la passphrase")
		return
	}
	if !s.requireEncrypted(w, name) {
		return
	}
	if s.cfg.Mock {
		if m, ok := s.pools.(keyMutator); ok {
			m.SetKeyStatus(name, "available")
		}
		s.act.AuditOnly(r.Context(), actor(r), "dataset.unlock", name, nil)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.act.DatasetLoadKey(r.Context(), actor(r), name, body.Key); err != nil {
		actionErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// lockDataset — POST /api/datasets/{name}/lock (admin) → 204. Desmonta y
// retira la clave; si el dataset está ocupado devuelve el error legible de zfs
// (NO se fuerza con -f).
func (s *Server) lockDataset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !s.requireEncrypted(w, name) {
		return
	}
	if s.cfg.Mock {
		if m, ok := s.pools.(keyMutator); ok {
			m.SetKeyStatus(name, "unavailable")
		}
		s.act.AuditOnly(r.Context(), actor(r), "dataset.lock", name, nil)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.act.DatasetUnloadKey(r.Context(), actor(r), name); err != nil {
		actionErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// changeKeyDataset — POST /api/datasets/{name}/change-key {current_key, new_key}
// (admin) → 204. Con keyformat=passphrase y la clave cargada, zfs solo pide la
// nueva (dos veces, por stdin); current_key se exige como confirmación de
// posesión pero no se envía al CLI (documentado en docs/api-contract.md).
func (s *Server) changeKeyDataset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body struct {
		CurrentKey string `json:"current_key"`
		NewKey     string `json:"new_key"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.CurrentKey == "" {
		writeErr(w, http.StatusBadRequest, "invalid_input", "se requiere la passphrase actual")
		return
	}
	if len(body.NewKey) < 8 {
		writeErr(w, http.StatusBadRequest, "invalid_input",
			"la passphrase nueva debe tener al menos 8 caracteres")
		return
	}
	if !s.requireEncrypted(w, name) {
		return
	}
	if s.cfg.Mock {
		s.act.AuditOnly(r.Context(), actor(r), "dataset.change_key", name, nil)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.act.DatasetChangeKey(r.Context(), actor(r), name, body.NewKey); err != nil {
		actionErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// patchDataset — PATCH /api/datasets/{name} {quota_bytes?, compression?} → 204.
// {name} puede venir URL-encoded con '/' (tank%2Fdocs): el mux lo decodifica por segmento.
func (s *Server) patchDataset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body struct {
		QuotaBytes  *uint64 `json:"quota_bytes"`
		Compression *string `json:"compression"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := s.act.DatasetPatch(r.Context(), actor(r), name, body.QuotaBytes, body.Compression); err != nil {
		actionErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// promoteDataset — POST /api/datasets/{name}/promote → 204.
func (s *Server) promoteDataset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if s.cfg.Mock {
		s.act.AuditOnly(r.Context(), actor(r), "dataset.promote", name, nil)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.act.DatasetPromote(r.Context(), actor(r), name); err != nil {
		actionErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// renameDataset — PATCH /api/datasets/{name}/rename {new_name} → 204.
func (s *Server) renameDataset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body struct {
		NewName string `json:"new_name"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.NewName == "" {
		writeErr(w, http.StatusBadRequest, "invalid_input", "new_name requerido")
		return
	}
	if err := s.act.DatasetRename(r.Context(), actor(r), name, body.NewName); err != nil {
		actionErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// mountDataset — POST /api/datasets/{name}/mount → 204.
func (s *Server) mountDataset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if s.cfg.Mock {
		s.act.AuditOnly(r.Context(), actor(r), "dataset.mount", name, nil)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.act.DatasetMount(r.Context(), actor(r), name); err != nil {
		actionErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// unmountDataset — POST /api/datasets/{name}/unmount → 204.
func (s *Server) unmountDataset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if s.cfg.Mock {
		s.act.AuditOnly(r.Context(), actor(r), "dataset.unmount", name, nil)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.act.DatasetUnmount(r.Context(), actor(r), name); err != nil {
		actionErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// deleteDataset — DELETE /api/datasets/{name} {confirm, recursive} → 202.
func (s *Server) deleteDataset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body struct {
		Confirm   string `json:"confirm"`
		Recursive bool   `json:"recursive"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if !requireConfirm(w, body.Confirm, name) {
		return
	}
	if err := s.act.DatasetDelete(r.Context(), actor(r), name, body.Recursive); err != nil {
		actionErr(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
