// pools.go — endpoints de pools. Los GET leen la caché del colector zpool.
package httpapi

import (
	"net/http"
	"strings"
)

// listPools — GET /api/pools (caché; vdevs con temp cruzada con discos).
func (s *Server) listPools(w http.ResponseWriter, r *http.Request) {
	pools := s.pools.Pools()
	temps := map[string]float64{}
	for _, d := range s.disks.Disks() {
		if d.TempC != nil {
			temps[d.Dev] = *d.TempC
			if d.ByID != "" {
				temps[d.ByID] = *d.TempC
			}
		}
	}
	for i := range pools {
		for j := range pools[i].Vdevs {
			key := vdevKey(pools[i].Vdevs[j].Path)
			if key == "" {
				key = vdevKey(pools[i].Vdevs[j].Dev)
			}
			if t, ok := temps[key]; ok {
				pools[i].Vdevs[j].TempC = t
			}
		}
	}
	writeJSON(w, http.StatusOK, pools)
}

// createPool — POST /api/pools {name, topo, disks[], confirm, ashift?} → 202.
func (s *Server) createPool(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name    string   `json:"name"`
		Topo    string   `json:"topo"`
		Disks   []string `json:"disks"`
		Confirm string   `json:"confirm"`
		Ashift  int      `json:"ashift"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if !requireConfirm(w, body.Confirm, body.Name) {
		return
	}
	disks := s.resolveDisks(body.Disks)
	if err := s.act.PoolCreate(r.Context(), actor(r), body.Name, body.Topo, disks, body.Ashift, true); err != nil {
		actionErr(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// importPool — POST /api/pools/import {name?} → lista importables o importa.
func (s *Server) importPool(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Name == "" {
		names, err := s.act.PoolImportList(r.Context())
		if err != nil {
			actionErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"importable": names})
		return
	}
	if err := s.act.PoolImport(r.Context(), actor(r), body.Name); err != nil {
		actionErr(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// scrubPool — POST /api/pools/{name}/scrub {action:start|pause|stop} → 202.
func (s *Server) scrubPool(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body struct {
		Action string `json:"action"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := s.act.Scrub(r.Context(), actor(r), name, body.Action); err != nil {
		actionErr(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// exportPool — POST /api/pools/{name}/export {confirm, force, destroy} → 202.
func (s *Server) exportPool(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body struct {
		Confirm string `json:"confirm"`
		Force   bool   `json:"force"`
		Destroy bool   `json:"destroy"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if !requireConfirm(w, body.Confirm, name) {
		return
	}
	if err := s.act.PoolExport(r.Context(), actor(r), name, body.Force, body.Destroy); err != nil {
		actionErr(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// addVdev — POST /api/pools/{name}/vdev {topo, disks[], confirm} → 202.
func (s *Server) addVdev(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body struct {
		Topo    string   `json:"topo"`
		Disks   []string `json:"disks"`
		Confirm string   `json:"confirm"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if !requireConfirm(w, body.Confirm, name) {
		return
	}
	disks := s.resolveDisks(body.Disks)
	if err := s.act.VdevAdd(r.Context(), actor(r), name, body.Topo, disks, true); err != nil {
		actionErr(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// resolveNewDev — normaliza el disco destino de un replace. Acepta nombre
// base ('sda'), '/dev/sda' o ruta by-id ('/dev/disk/by-id/ata-…') y lo
// resuelve contra el inventario de discos: devuelve la forma canónica
// preferida para zpool ('/dev/disk/by-id/<id>' si el disco tiene enlace,
// si no el nombre base) y el nombre base (para las guardas). Si el disco
// no está en el inventario, devuelve la entrada saneada y ok=false (la
// guarda de tamaño no podrá aplicarse, como ocurría antes) — issue #65.
func (s *Server) resolveNewDev(in string) (canonical, base string, ok bool) {
	in = strings.TrimSpace(in)
	if strings.HasPrefix(in, "/dev/disk/by-id/") {
		id := strings.TrimPrefix(in, "/dev/disk/by-id/")
		for _, d := range s.disks.Disks() {
			if d.ByID != "" && d.ByID == id {
				return "/dev/disk/by-id/" + d.ByID, d.Dev, true
			}
		}
		return in, id, false
	}
	base = stripPart(strings.TrimPrefix(in, "/dev/"))
	for _, d := range s.disks.Disks() {
		if d.Dev == base {
			if d.ByID != "" {
				return "/dev/disk/by-id/" + d.ByID, base, true
			}
			return base, base, true
		}
	}
	return base, base, false
}

// resolveDisks — resuelve cada disco de create/add vdev a su ruta by-id estable
// '/dev/disk/by-id/<id>' cuando existe (issue #107); nombre base como fallback
// (discos sin enlace by-id, p.ej. algunas eMMC). Reusa resolveNewDev.
func (s *Server) resolveDisks(disks []string) []string {
	out := make([]string, len(disks))
	for i, d := range disks {
		canonical, _, _ := s.resolveNewDev(d)
		out[i] = canonical
	}
	return out
}

// vdevKey reduce el nombre o ruta de un vdev a la clave para cruzar con discos:
// '/dev/disk/by-id/ata-XXX' → 'ata-XXX'; '/dev/sda1' → 'sda'; 'nvme0n1' → 'nvme0n1'.
func vdevKey(v string) string {
	v = strings.TrimPrefix(v, "/dev/")
	v = strings.TrimPrefix(v, "disk/by-id/")
	return stripPart(v)
}

// replaceDisk — POST /api/pools/{name}/replace {old_dev, new_dev, confirm} → 202.
// new_dev admite nombre base, '/dev/sdX' o ruta by-id; se resuelve SIEMPRE a
// '/dev/disk/by-id/…' cuando el disco tiene enlace estable (issue #65).
func (s *Server) replaceDisk(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body struct {
		OldDev  string `json:"old_dev"`
		NewDev  string `json:"new_dev"`
		Confirm string `json:"confirm"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if !requireConfirm(w, body.Confirm, name) {
		return
	}
	newCanonical, newBase, _ := s.resolveNewDev(body.NewDev)
	oldBase := stripPart(strings.TrimPrefix(body.OldDev, "/dev/"))
	if body.OldDev == body.NewDev || oldBase == newBase {
		writeErr(w, http.StatusConflict, "same_dev", "el disco nuevo no puede ser el mismo que el sustituido")
		return
	}
	// Guarda: el disco nuevo no puede ser miembro de ningún pool (evita el
	// error críptico de zpool 'is part of active pool').
	for _, p := range s.pools.Pools() {
		for _, v := range p.Vdevs {
			key := stripPart(strings.TrimPrefix(v.Path, "/dev/"))
			if key == "" {
				key = stripPart(v.Dev)
			}
			if key != "" && key == newBase {
				writeErr(w, http.StatusConflict, "dev_in_use",
					"el disco nuevo ya pertenece al pool '"+p.Name+"'")
				return
			}
		}
	}
	// Guarda: el disco nuevo debe ser al menos tan grande como el sustituido.
	if oldSz, err := s.act.VdevSize(r.Context(), name, body.OldDev); err == nil && oldSz > 0 {
		for _, d := range s.disks.Disks() {
			if d.Dev == newBase && d.SizeBytes > 0 && d.SizeBytes < oldSz {
				writeErr(w, http.StatusConflict, "dev_too_small",
					"el disco nuevo es más pequeño que el sustituido")
				return
			}
		}
	}
	if err := s.act.Replace(r.Context(), actor(r), name, body.OldDev, newCanonical, true); err != nil {
		actionErr(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// vdevAction — POST /api/pools/{name}/vdev/action {dev, action, confirm?} → 202.
// offline/online: sin confirmación. detach: exige confirm (destructivo).
func (s *Server) vdevAction(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body struct {
		Dev     string `json:"dev"`
		Action  string `json:"action"`
		Confirm string `json:"confirm"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Action == "detach" && !requireConfirm(w, body.Confirm, name) {
		return
	}
	if err := s.act.VdevAction(r.Context(), actor(r), name, body.Dev, body.Action, body.Action == "detach"); err != nil {
		actionErr(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// setAutotrim — POST /api/pools/{name}/autotrim {enabled} → 204.
func (s *Server) setAutotrim(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if s.cfg.Mock {
		// MOCK=1: sin zpool real; mutación simulada sobre la caché del mock.
		type autotrimSetter interface{ SetAutotrim(string, bool) }
		if m, ok := s.pools.(autotrimSetter); ok {
			m.SetAutotrim(name, body.Enabled)
		}
		s.act.AuditOnly(r.Context(), actor(r), "pool.autotrim", name, map[string]any{"enabled": body.Enabled})
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.act.SetAutotrim(r.Context(), actor(r), name, body.Enabled); err != nil {
		actionErr(w, err)
		return
	}
	// Refresco inmediato del colector: sin él la UI (y el SSE) verían el
	// valor antiguo hasta el próximo tick de 30 s y el toggle "no cambiaría".
	type refresher interface{ RefreshSoon() }
	if rc, ok := s.pools.(refresher); ok {
		rc.RefreshSoon()
	}
	w.WriteHeader(http.StatusNoContent)
}

// poolCheckpoint — POST /api/pools/{name}/checkpoint {action:create|discard,
// confirm:"<pool>"} → 202. Operación delicada: siempre exige confirm.
func (s *Server) poolCheckpoint(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body struct {
		Action  string `json:"action"`
		Confirm string `json:"confirm"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if !requireConfirm(w, body.Confirm, name) {
		return
	}
	if s.cfg.Mock {
		// MOCK=1: checkpoint simulado sobre la caché del mock.
		type ckSetter interface{ SetCheckpoint(string, bool) }
		switch body.Action {
		case "create", "discard":
			if m, ok := s.pools.(ckSetter); ok {
				m.SetCheckpoint(name, body.Action == "create")
			}
			s.act.AuditOnly(r.Context(), actor(r), "pool.checkpoint."+body.Action, name, nil)
			w.WriteHeader(http.StatusAccepted)
		default:
			writeErr(w, http.StatusBadRequest, "invalid_input", "action debe ser create|discard")
		}
		return
	}
	var err error
	switch body.Action {
	case "create":
		err = s.act.CheckpointCreate(r.Context(), actor(r), name)
	case "discard":
		err = s.act.CheckpointDiscard(r.Context(), actor(r), name)
	default:
		writeErr(w, http.StatusBadRequest, "invalid_input", "action debe ser create|discard")
		return
	}
	if err != nil {
		actionErr(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// expandPool — POST /api/pools/{name}/expand {vdev:"raidz2-0", disk:"sdX",
// confirm:"<pool>"} (admin) → 202. RAID-Z expansion (OpenZFS ≥ 2.3): añade UN
// disco a un vdev raidz existente. Gate por capability; el vdev debe ser un
// raidz del pool y el disco un físico libre (no en uso por ningún pool).
func (s *Server) expandPool(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body struct {
		Vdev    string `json:"vdev"`
		Disk    string `json:"disk"`
		Confirm string `json:"confirm"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if !s.caps.Capabilities().RaidzExpansion {
		writeErr(w, http.StatusBadRequest, "not_supported",
			"RAID-Z expansion requiere OpenZFS ≥ 2.3 en este host")
		return
	}
	if !requireConfirm(w, body.Confirm, name) {
		return
	}
	// El pool debe existir y el vdev ser uno de sus raidz (de la caché).
	found := false
	vdevOK := false
	for _, p := range s.pools.Pools() {
		if p.Name != name {
			continue
		}
		found = true
		for _, rv := range p.RaidzVdevs {
			if rv == body.Vdev {
				vdevOK = true
			}
		}
	}
	if !found {
		writeErr(w, http.StatusNotFound, "not_found", "pool no encontrado")
		return
	}
	if !vdevOK {
		writeErr(w, http.StatusBadRequest, "invalid_input",
			"el vdev '"+body.Vdev+"' no es un raidz del pool '"+name+"'")
		return
	}
	// El disco debe ser un físico conocido, libre y no en uso por ningún pool.
	base := stripPart(strings.TrimPrefix(body.Disk, "/dev/"))
	diskOK := false
	for _, d := range s.disks.Disks() {
		if d.Dev == base && d.Pool == "" && !d.InUse {
			diskOK = true
		}
	}
	if !diskOK {
		writeErr(w, http.StatusConflict, "dev_in_use",
			"el disco '"+body.Disk+"' no está libre (en uso o desconocido)")
		return
	}
	for _, p := range s.pools.Pools() {
		for _, v := range p.Vdevs {
			key := stripPart(strings.TrimPrefix(v.Path, "/dev/"))
			if key == "" {
				key = stripPart(v.Dev)
			}
			if key != "" && key == base {
				writeErr(w, http.StatusConflict, "dev_in_use",
					"el disco '"+body.Disk+"' ya pertenece al pool '"+p.Name+"'")
				return
			}
		}
	}
	if s.cfg.Mock {
		// MOCK=1: expansión simulada con progreso en la caché del mock.
		type expander interface {
			Expand(pool, vdev, disk string)
		}
		if m, ok := s.pools.(expander); ok {
			m.Expand(name, body.Vdev, base)
		}
		s.act.AuditOnly(r.Context(), actor(r), "pool.expand", name,
			map[string]any{"vdev": body.Vdev, "disk": base})
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if err := s.act.PoolExpand(r.Context(), actor(r), name, body.Vdev, base, true); err != nil {
		actionErr(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// clearPool — POST /api/pools/{name}/clear {dev?} → 204.
func (s *Server) clearPool(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body struct {
		Dev string `json:"dev"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if s.cfg.Mock {
		s.act.AuditOnly(r.Context(), actor(r), "pool.clear", name,
			map[string]any{"dev": body.Dev})
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.act.PoolClear(r.Context(), actor(r), name, body.Dev); err != nil {
		actionErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// poolHistory — GET /api/pools/{name}/history → lista (caché del colector).
func (s *Server) poolHistory(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.pools.History(r.PathValue("name")))
}

// getPerformance — GET /api/performance → {arc, pools[]} (caché del colector).
func (s *Server) getPerformance(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.perf.Performance())
}

// stripPart quita el sufijo de partición:
// 'sdb1'→'sdb' (solo estilo sdX/vdX/hdX), 'nvme0n1p2'→'nvme0n1', 'mmcblk0p1'→'mmcblk0'.
// OJO: 'nvme0n1' (disco entero) NO debe perder el '1' final.
func stripPart(dev string) string {
	// estilo <base>p<N> (nvme, mmcblk, loop…)
	if i := strings.LastIndex(dev, "p"); i > 0 && allDigits(dev[i+1:]) && !allDigits(dev[:i]) {
		return dev[:i]
	}
	// estilo sdX<N>/vdX<N>/hdX<N>: letra(s) + dígitos finales
	for _, pre := range []string{"xvd", "sd", "vd", "hd"} {
		if strings.HasPrefix(dev, pre) {
			rest := dev[len(pre):]
			j := len(rest)
			for j > 0 && rest[j-1] >= '0' && rest[j-1] <= '9' {
				j--
			}
			if j < len(rest) && j > 0 {
				return pre + rest[:j]
			}
			return dev
		}
	}
	return dev
}

// allDigits — true si s no está vacío y son todo dígitos.
func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// poolForDisk — pool al que pertenece un disco (por vdevs conocidos).
// `aliases` son nombres equivalentes del disco (p.ej. su ByID) para que un
// vdev creado por ruta by-id también se cruce con el disco físico.
func poolForDisk(pools []string, vdevs map[string][]string, dev string, aliases ...string) string {
	keys := []string{stripPart(dev)}
	for _, a := range aliases {
		keys = append(keys, stripPart(a))
	}
	for _, p := range pools {
		for _, v := range vdevs[p] {
			k := vdevKey(v)
			for _, a := range keys {
				if k == a || v == dev {
					return p
				}
			}
		}
	}
	return ""
}
