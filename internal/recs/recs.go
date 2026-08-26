// Package recs — motor de recomendaciones de sustitución/mantenimiento de
// discos. Reglas PURAS sobre los modelos (testeables sin sistema): ni
// TrueNAS ni OMV tienen esto (solo alertan o pintan semáforos); el valor es
// decir QUÉ disco, POR QUÉ y QUÉ hacer — incluido el matiz "CRC alto =
// revisa cable/puerto, NO el disco", aprendido con el caso real de bigtank.
package recs

import (
	"sort"

	"easyzfs/internal/model"
)

// Umbrales del motor (documentados en la UI vía i18n).
const (
	// ReallocSoon — sectores reasignados a partir de los cuales se recomienda
	// planificar la sustitución (por debajo: "vigilar").
	ReallocSoon = 100
)

// Evaluate aplica las reglas sobre los discos, con contexto de sus pools
// (guardas de seguridad: no sugerir retirar un disco con resilver en curso,
// pool degradado o sin redundancia). Orden del resultado: crit, warn, info.
func Evaluate(disks []model.Disk, pools []model.Pool) []model.Recommendation {
	byPool := map[string]model.Pool{}
	for _, p := range pools {
		byPool[p.Name] = p
	}
	out := []model.Recommendation{}
	for _, d := range disks {
		base := model.Recommendation{
			Dev: d.Dev, Serial: d.Serial, Pool: d.Pool,
			ReallocSectors: d.ReallocSectors, PendingSectors: d.PendingSectors,
			OfflineUncorr: d.OfflineUncorr, CrcErrors: d.CrcErrors,
			CrcRecent: d.CrcRecent,
		}
		add := func(level, kind string) {
			r := base
			r.Level, r.Kind = level, kind
			if kind == model.RecReplaceNow || kind == model.RecReplaceSoon {
				r.Hold, r.HoldReason = holdFor(d, byPool)
			}
			out = append(out, r)
		}
		switch {
		case d.Smart == "crit":
			// SMART overall FAILED: el propio firmware da el disco por muerto.
			add("crit", model.RecReplaceNow)
		case d.PendingSectors > 0 || d.OfflineUncorr > 0:
			// Sectores pendientes/incorregibles: fallo de lectura real, el
			// disco está muriendo (no se recupera solo).
			add("crit", model.RecReplaceNow)
		case d.ReallocSectors >= ReallocSoon:
			add("warn", model.RecReplaceSoon)
		case d.ReallocSectors > 0 || d.NvmeWarn > 0:
			add("info", model.RecWatch)
		}
		// CRC va APARTE: no es salud del medio, es el link SATA (cable/
		// puerto/backplane). Lo accionable es el CRECIMIENTO entre pasadas
		// (link roto AHORA). El acumulado de por vida no se resetea y
		// perseguiría al disco equivocado tras un cambio de bahías (caso
		// real bigtank 4-Ago-2026: disco absuelto marcado como "cable malo"
		// por su histórico mientras el puerto roto azotaba a otro). El CRC
		// histórico estable se consulta en la burbuja info del disco.
		if d.CrcRecent > 0 {
			add("warn", model.RecCheckCable)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return rank(out[i].Level) < rank(out[j].Level) })
	return out
}

// rank ordena por severidad: crit(0) < warn(1) < info(2).
func rank(level string) int {
	switch level {
	case "crit":
		return 0
	case "warn":
		return 1
	default:
		return 2
	}
}

// holdFor decide si la sustitución sugerida debe ESPERAR por seguridad:
// no se retira un disco mientras el pool resilvera, está degradado o no
// tiene redundancia que cubra la retirada.
func holdFor(d model.Disk, byPool map[string]model.Pool) (bool, string) {
	p, ok := byPool[d.Pool]
	if !ok {
		return false, ""
	}
	if p.Scrub.State == "running" && p.Scrub.Kind == "resilver" {
		return true, model.HoldResilver
	}
	if p.Status != "ONLINE" {
		return true, model.HoldPoolDegraded
	}
	if p.Topo == "stripe" {
		return true, model.HoldNoRedundancy
	}
	return false, ""
}
