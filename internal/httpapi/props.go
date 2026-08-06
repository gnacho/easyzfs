// props.go — endpoints de propiedades de datasets (U3, fase P1).
// GET lista todas (nativas + user); PATCH y POST (admin) solo aceptan las
// de la whitelist estricta de internal/actions/props.go.
//
// Excepción puntual a "los handlers leen caché de collectors": `zfs get all`
// por dataset solo se ejecuta cuando alguien abre el modal de propiedades
// (TTL 30 s). Documentado en docs/specs-p1-v2.5.md.
package httpapi

import (
	"net/http"
	"sync"
	"time"

	"easyzfs/internal/model"
)

// propMutator — mutaciones simuladas de propiedades sobre la caché del mock.
type propMutator interface {
	SetDatasetProp(name, property, value string)
	InheritDatasetProp(name, property string)
}

// mockDatasetProps — propiedades ficticias para MOCK=1 (coherentes con el
// inventario demo: un fs con valores locales y una heredada, sin user props).
func mockDatasetProps() []model.DatasetProp {
	return []model.DatasetProp{
		{Name: "compression", Value: "lz4", Source: "local"},
		{Name: "recordsize", Value: "128K", Source: "local"},
		{Name: "atime", Value: "on", Source: "default"},
		{Name: "sync", Value: "standard", Source: "default"},
		{Name: "quota", Value: "none", Source: "local"},
		{Name: "mountpoint", Value: "/tank/demo", Source: "local"},
		{Name: "exec", Value: "on", Source: "default"},
		{Name: "readonly", Value: "off", Source: "default"},
		{Name: "encryption", Value: "off", Source: "default"},
		{Name: "used", Value: "1.5T", Source: "-"},
	}
}

// propsCache — caché TTL 30 s de propiedades por dataset.
type propsCacheEntry struct {
	ts    time.Time
	props []model.DatasetProp
}

var propsCache = struct {
	sync.Mutex
	m map[string]propsCacheEntry
}{m: map[string]propsCacheEntry{}}

func cachedProps(name string, load func() ([]model.DatasetProp, error)) ([]model.DatasetProp, error) {
	propsCache.Lock()
	defer propsCache.Unlock()
	if e, ok := propsCache.m[name]; ok && time.Since(e.ts) < 30*time.Second {
		return e.props, nil
	}
	props, err := load()
	if err != nil {
		return nil, err
	}
	propsCache.m[name] = propsCacheEntry{ts: time.Now(), props: props}
	return props, nil
}

// listDatasetProps — GET /api/datasets/{name}/properties (admin y viewer).
func (s *Server) listDatasetProps(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if s.cfg.Mock {
		writeJSON(w, http.StatusOK, map[string]any{"name": name, "properties": mockDatasetProps()})
		return
	}
	if s.findDataset(name) == nil {
		writeErr(w, http.StatusNotFound, "not_found", "dataset no encontrado")
		return
	}
	props, err := cachedProps(name, func() ([]model.DatasetProp, error) {
		return s.act.DatasetPropsGet(r.Context(), name)
	})
	if err != nil {
		actionErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "properties": props})
}

// patchDatasetProps — PATCH /api/datasets/{name}/properties {property, value}
// (admin) → 204. Whitelist estricta de propiedad y valor.
func (s *Server) patchDatasetProps(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body struct {
		Property string `json:"property"`
		Value    string `json:"value"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Property == "" {
		writeErr(w, http.StatusBadRequest, "invalid_input", "property requerida")
		return
	}
	if s.cfg.Mock {
		if m, ok := s.pools.(propMutator); ok {
			m.SetDatasetProp(name, body.Property, body.Value)
		}
		s.act.AuditOnly(r.Context(), actor(r), "dataset.setprop", name,
			map[string]any{"property": body.Property, "value": body.Value})
		w.WriteHeader(http.StatusNoContent)
		return
	}
	ds := s.findDataset(name)
	if ds == nil {
		writeErr(w, http.StatusNotFound, "not_found", "dataset no encontrado")
		return
	}
	if err := s.act.DatasetPropSet(r.Context(), actor(r), name, body.Property, body.Value, ds.Type); err != nil {
		actionErr(w, err)
		return
	}
	propsCache.Lock()
	delete(propsCache.m, name) // invalidar tras mutación
	propsCache.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// inheritDatasetProp — POST /api/datasets/{name}/properties/{prop}/inherit
// (admin) → 204. Solo si la propiedad es de la whitelist y source == local.
func (s *Server) inheritDatasetProp(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	prop := r.PathValue("prop")
	if s.cfg.Mock {
		if m, ok := s.pools.(propMutator); ok {
			m.InheritDatasetProp(name, prop)
		}
		s.act.AuditOnly(r.Context(), actor(r), "dataset.inherit", name,
			map[string]any{"property": prop})
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if s.findDataset(name) == nil {
		writeErr(w, http.StatusNotFound, "not_found", "dataset no encontrado")
		return
	}
	// Comprobar source == local contra la última lectura (no-op inocuo si
	// está obsoleta; zfs inherit sobre no-local no hace nada).
	props, err := s.act.DatasetPropsGet(r.Context(), name)
	if err != nil {
		actionErr(w, err)
		return
	}
	found := false
	for _, p := range props {
		if p.Name == prop {
			found = true
			if p.Source != "local" {
				writeErr(w, http.StatusConflict, "not_local",
					"la propiedad no es local, no se puede heredar")
				return
			}
			break
		}
	}
	if !found {
		writeErr(w, http.StatusBadRequest, "invalid_property", "propiedad no encontrada")
		return
	}
	if err := s.act.DatasetPropInherit(r.Context(), actor(r), name, prop); err != nil {
		actionErr(w, err)
		return
	}
	propsCache.Lock()
	delete(propsCache.m, name)
	propsCache.Unlock()
	w.WriteHeader(http.StatusNoContent)
}
