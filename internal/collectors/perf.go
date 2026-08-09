// perf.go — colector de rendimiento (tick 60 s): ARC (tamaño + hit %) y
// throughput por pool ('zpool iostat -Hpy 1 1', una muestra de 1 s que ya
// viene como tasa media — el flag -y descarta la muestra desde el arranque).
//
// ARC: PREFIERE /proc/spl/kstat/zfs/arcstats (estable entre versiones, sin
// ejecutar nada). Si no existe, cae a zarcsummary (≥2.4) o arc_summary
// parseando el texto. Si no hay ninguna fuente, Arc queda nil y la UI oculta
// la tarjeta de rendimiento.
package collectors

import (
	"context"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"easyzfs/internal/executil"
	"easyzfs/internal/model"
)

const perfInterval = 60 * time.Second

// PerfCollector — caché de ARC + iostat por pool.
type PerfCollector struct {
	mu   sync.RWMutex
	perf model.Performance
}

// NewPerfCollector crea el colector de rendimiento.
func NewPerfCollector() *PerfCollector {
	return &PerfCollector{perf: model.Performance{Pools: []model.PoolPerf{}}}
}

// Name implementa Collector.
func (c *PerfCollector) Name() string { return "perf" }

// Run — bucle con ticker (patrón del skill).
func (c *PerfCollector) Run(ctx context.Context) {
	c.collect(ctx)
	t := time.NewTicker(perfInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.collect(ctx)
		}
	}
}

// Performance implementa PerfProvider (copia defensiva).
func (c *PerfCollector) Performance() model.Performance {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := model.Performance{Pools: make([]model.PoolPerf, len(c.perf.Pools))}
	copy(out.Pools, c.perf.Pools)
	if c.perf.Arc != nil {
		arc := *c.perf.Arc
		out.Arc = &arc
	}
	return out
}

// collect — una pasada: ARC + iostat. Los fallos degradan (no matan el tick).
func (c *PerfCollector) collect(ctx context.Context) {
	arc := readARC(ctx)
	pools := c.iostat(ctx)
	c.mu.Lock()
	c.perf = model.Performance{Arc: arc, Pools: pools}
	c.mu.Unlock()
}

// iostat — 'zpool iostat -Hpy 1 1': una muestra de 1 segundo (ya es tasa,
// con -y se descarta la muestra acumulada desde el arranque).
func (c *PerfCollector) iostat(ctx context.Context) []model.PoolPerf {
	out, err := executil.Run(ctx, 15*time.Second, "zpool", "iostat", "-Hpy", "1", "1")
	if err != nil {
		log.Printf("perf iostat: %v", err)
		return nil // conservar la caché anterior
	}
	return parseIostat(string(out))
}

// parseIostat — salida tabular -Hp: 'name alloc free r_ops w_ops r_bw w_bw'.
// Sin -v solo hay líneas de pool (una por pool). -p da bytes crudos.
func parseIostat(out string) []model.PoolPerf {
	pools := []model.PoolPerf{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 7 {
			continue
		}
		r, err1 := strconv.ParseFloat(f[5], 64)
		w, err2 := strconv.ParseFloat(f[6], 64)
		if err1 != nil || err2 != nil {
			continue
		}
		pools = append(pools, model.PoolPerf{Name: f[0], ReadBps: r, WriteBps: w})
	}
	return pools
}

// --- ARC ---

// readARC — tamaño y hit % del ARC: /proc primero, texto de (z)arc_summary
// como respaldo. nil = sin fuente disponible en este sistema.
func readARC(ctx context.Context) *model.ArcStats {
	if b, err := os.ReadFile("/proc/spl/kstat/zfs/arcstats"); err == nil {
		if st, ok := parseArcstats(string(b)); ok {
			return st
		}
	}
	// Fallback CLI: zarcsummary (≥2.4) o arc_summary (versiones viejas).
	for _, name := range []string{"zarcsummary", "arc_summary"} {
		if out, err := executil.Run(ctx, 10*time.Second, name); err == nil {
			if st, ok := parseArcSummary(string(out)); ok {
				return st
			}
		}
	}
	return nil
}

// parseArcstats — /proc/spl/kstat/zfs/arcstats: líneas 'name type data'.
// Campos: size (bytes actuales del ARC), hits, misses (acumulados).
func parseArcstats(content string) (*model.ArcStats, bool) {
	var size, hits, misses uint64
	var seenSize, seenHits, seenMisses bool
	for _, line := range strings.Split(content, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		v, err := strconv.ParseUint(f[2], 10, 64)
		if err != nil {
			continue
		}
		switch f[0] {
		case "size":
			size, seenSize = v, true
		case "hits":
			hits, seenHits = v, true
		case "misses":
			misses, seenMisses = v, true
		}
	}
	if !seenSize || !seenHits || !seenMisses {
		return nil, false
	}
	hitPct := 0.0
	if total := hits + misses; total > 0 {
		hitPct = float64(hits) * 100 / float64(total)
	}
	return &model.ArcStats{SizeBytes: size, HitPct: hitPct}, true
}

// arcSizeRe / arcHitRe — campos del texto de arc_summary/zarcsummary:
// 'ARC size (current):                    25.4 %   3.99 GiB' y
// 'ARC hit ratio:    92.6%   ...' (los formatos varían; mejor esfuerzo).
var (
	arcSizeRe = regexp.MustCompile(`(?i)ARC size \(current\):\s+[\d.]+\s*%\s+([\d.]+)\s+([KMGTPE]?i?B)`)
	arcHitRe  = regexp.MustCompile(`(?i)ARC (?:total )?hit ratio:\s+([\d.]+)\s*%`)
)

// parseArcSummary — texto de arc_summary/zarcsummary (respaldo sin /proc).
func parseArcSummary(out string) (*model.ArcStats, bool) {
	sm := arcSizeRe.FindStringSubmatch(out)
	hm := arcHitRe.FindStringSubmatch(out)
	if sm == nil && hm == nil {
		return nil, false
	}
	st := &model.ArcStats{}
	if sm != nil {
		unit := strings.TrimSuffix(strings.ToUpper(sm[2]), "IB") // "GiB" → "G" (parseHumanSize espera sufijo simple)
		if sz, ok := parseHumanSize(sm[1] + unit); ok {
			st.SizeBytes = sz
		}
	}
	if hm != nil {
		st.HitPct, _ = strconv.ParseFloat(hm[1], 64)
	}
	return st, true
}
