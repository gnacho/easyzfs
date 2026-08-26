// disks.go — endpoints de discos. GET desde caché del colector smart.
package httpapi

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"easyzfs/internal/executil"
	"easyzfs/internal/model"
	"easyzfs/internal/recs"
)

// listDisks — GET /api/disks (caché; pool cruzado con vdevs conocidos y
// "en uso" cruzado con puntos de montaje activos).
func (s *Server) listDisks(w http.ResponseWriter, r *http.Request) {
	disks := s.disksEnriched(r.Context())
	writeJSON(w, http.StatusOK, disks)
}

// disksEnriched — caché de discos con el pool cruzado por vdevs y el flag
// "en uso". Lo comparten /api/disks y /api/recommendations (el motor de
// reglas necesita el pool para las guardas de seguridad).
func (s *Server) disksEnriched(ctx context.Context) []model.Disk {
	disks := s.disks.Disks()
	pools := s.pools.Pools()
	names := make([]string, 0, len(pools))
	vdevs := map[string][]string{}
	for _, p := range pools {
		names = append(names, p.Name)
		for _, v := range p.Vdevs {
			vdevs[p.Name] = append(vdevs[p.Name], v.Dev)
			if v.Path != "" {
				vdevs[p.Name] = append(vdevs[p.Name], v.Path)
			}
		}
	}
	inUse := mountedDisks(ctx)
	for i := range disks {
		if disks[i].Pool == "" {
			disks[i].Pool = poolForDisk(names, vdevs, disks[i].Dev, disks[i].ByID)
		}
		disks[i].InUse = inUse[disks[i].Dev]
	}
	return disks
}

// --- discos "en uso": alguna partición montada o swap activo ---

var mountedCache = struct {
	sync.Mutex
	ts time.Time
	m  map[string]bool
}{}

// mountedDisks — mapa dev→true si el disco (o alguna partición) está montado
// o es swap activo. Caché de 15 s (lsblk es barato pero cada petición no).
func mountedDisks(ctx context.Context) map[string]bool {
	mountedCache.Lock()
	defer mountedCache.Unlock()
	if time.Since(mountedCache.ts) < 15*time.Second && mountedCache.m != nil {
		return mountedCache.m
	}
	m := map[string]bool{}
	out, err := executil.Run(ctx, 5*time.Second, "lsblk", "-rno", "NAME,MOUNTPOINTS")
	if err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			f := strings.Fields(line)
			if len(f) < 2 {
				continue // sin punto de montaje
			}
			m[stripPart(f[0])] = true
		}
	}
	mountedCache.ts = time.Now()
	mountedCache.m = m
	return m
}

// powerOff — POST /api/disks/{dev}/poweroff → 202.
// Solo discos libres: vetado si es miembro de un pool o tiene montajes activos.
func (s *Server) powerOff(w http.ResponseWriter, r *http.Request) {
	dev := r.PathValue("dev")
	pools := s.pools.Pools()
	names := make([]string, 0, len(pools))
	vdevs := map[string][]string{}
	for _, p := range pools {
		names = append(names, p.Name)
		for _, v := range p.Vdevs {
			vdevs[p.Name] = append(vdevs[p.Name], v.Dev)
			if v.Path != "" {
				vdevs[p.Name] = append(vdevs[p.Name], v.Path)
			}
		}
	}
	var aliases []string
	for _, d := range s.disks.Disks() {
		if d.Dev == dev || d.ByID == dev {
			if d.ByID != "" {
				aliases = append(aliases, d.ByID)
			}
			break
		}
	}
	if p := poolForDisk(names, vdevs, dev, aliases...); p != "" {
		writeErr(w, http.StatusConflict, "dev_in_use", "el disco pertenece al pool '"+p+"'")
		return
	}
	if mountedDisks(r.Context())[dev] {
		writeErr(w, http.StatusConflict, "dev_mounted", "el disco tiene particiones montadas o swap activo")
		return
	}
	if err := s.act.PowerOff(r.Context(), actor(r), dev); err != nil {
		actionErr(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// listRecommendations — GET /api/recommendations: motor de reglas sobre la
// caché de discos enriquecida (con pool) + contexto de pools (guardas
// resilver/degradado/stripe).
func (s *Server) listRecommendations(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, recs.Evaluate(s.disksEnriched(r.Context()), s.pools.Pools()))
}

// smartTest — POST /api/disks/{dev}/smart-test {type:short|long} → 202.
// Lanza el test; el resultado se observa en el colector smart.
func (s *Server) smartTest(w http.ResponseWriter, r *http.Request) {
	dev := r.PathValue("dev")
	var body struct {
		Type string `json:"type"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := s.act.SmartTest(r.Context(), actor(r), dev, body.Type); err != nil {
		actionErr(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// identifyDisk — POST /api/disks/{dev}/identify → 202.
// Hace parpadear el LED de actividad de la bahía (lectura I/O durante unos
// segundos) para localizar físicamente el disco. Inofensivo: lectura directa,
// sirve también para miembros de pool antes de un replace.
func (s *Server) identifyDisk(w http.ResponseWriter, r *http.Request) {
	dev := r.PathValue("dev")
	if err := s.act.IdentifyDisk(r.Context(), actor(r), dev); err != nil {
		actionErr(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// diskSmart — GET /api/disks/{dev}/smart (U1): detalle SMART completo del
// disco (atributos + info) desde la caché del colector. 404 si no existe;
// discos "unknown" (sin smartctl) → 200 con attributes vacíos.
func (s *Server) diskSmart(w http.ResponseWriter, r *http.Request) {
	dev := r.PathValue("dev")
	for _, d := range s.disks.Disks() {
		if d.Dev != dev {
			continue
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"dev": d.Dev, "model": d.Model, "serial": d.Serial,
			"smart": d.Smart, "smart_detail": d.SmartDetail, "hours": d.Hours,
			"attributes": smartAttrsOrEmpty(d.SmartFull),
		})
		return
	}
	writeErr(w, http.StatusNotFound, "not_found", "disco no encontrado")
}

// diskSmartLog — GET /api/disks/{dev}/smart-log (U1): historial de selftests
// y log de errores desde la caché del colector.
func (s *Server) diskSmartLog(w http.ResponseWriter, r *http.Request) {
	dev := r.PathValue("dev")
	for _, d := range s.disks.Disks() {
		if d.Dev != dev {
			continue
		}
		det := d.SmartFull
		selftests, errLog := []model.SmartSelftest{}, model.SmartErrorLog{}
		if det != nil {
			selftests, errLog = det.Selftests, det.ErrorLog
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"dev": dev, "selftests": selftests, "error_log": errLog,
		})
		return
	}
	writeErr(w, http.StatusNotFound, "not_found", "disco no encontrado")
}

// smartAttrsOrEmpty — atributos del detalle, o lista vacía si no hay detalle
// (disco sin SMART: eMMC, USB sin SAT).
func smartAttrsOrEmpty(det *model.DiskSmartDetail) []model.SmartAttr {
	if det == nil {
		return []model.SmartAttr{}
	}
	return det.Attributes
}
