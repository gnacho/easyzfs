// zpool.go — colector ZFS: pools (list + status con JSON/fallback), datasets,
// snapshots. Intervalo 30 s. Publica pool.status / scrub.progress solo en cambios.
package collectors

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"easyzfs/internal/alerts"
	"easyzfs/internal/executil"
	"easyzfs/internal/hub"
	"easyzfs/internal/model"
)

const (
	zpoolIntervalDef = 60 * time.Second
	zpoolMaxBackoff  = 5 * time.Minute
	seriesInterval   = 10 * time.Minute // persistir series con esta cadencia minima
	historyTTL       = 10 * time.Minute // re-leer 'zpool history' como maximo con esta cadencia
	historyTimeout   = 90 * time.Second // historiales grandes (bigtank: ~20 s / 275 MB)
	propTTL          = 5 * time.Minute // propiedades estables: autotrim, checkpoint, compressratio
	trimTTL          = 2 * time.Minute // estado TRIM no cambia tan rapido; reduce llamadas -t
)

// ZpoolCollector — caché de pools, datasets y snapshots.
type ZpoolCollector struct {
	db *sql.DB
	h  *hub.Hub
	al *alerts.Alerter

	mu        sync.RWMutex
	pools     []model.Pool
	datasets  []model.Dataset
	snaps     []model.Snapshot
	history   map[string][]model.HistoryEntry
	historyAt map[string]time.Time

	fails      int
	stale      bool
	prevStatus map[string]string
	prevPct    map[string]int
	lastSeries map[string]time.Time

	// Intervalo entre recolectas periodicas (configurable; #124).
	interval time.Duration

	// Cache de propiedades estables y de trim para no repetir comandos en
	// cada tick del colector.
	lastPropsAt map[string]time.Time

	// refreshCh despierta el bucle Run tras una mutacion (autotrim, trim...):
	// sin el la UI veria el valor antiguo hasta el proximo tick.
	// Buffer 1 = debounce: una rafaga de mutaciones produce UNA recolecta.
	refreshCh chan struct{}
}

// NewZpoolCollector crea el colector. interval=0 usa el default de 60 s.
func NewZpoolCollector(d *sql.DB, h *hub.Hub, al *alerts.Alerter, interval time.Duration) *ZpoolCollector {
	if interval <= 0 {
		interval = zpoolIntervalDef
	}
	return &ZpoolCollector{
		db:          d,
		h:           h,
		al:          al,
		interval:    interval,
		prevStatus:  map[string]string{},
		prevPct:     map[string]int{},
		lastSeries:  map[string]time.Time{},
		lastPropsAt: map[string]time.Time{},
		history:     map[string][]model.HistoryEntry{},
		historyAt:   map[string]time.Time{},
		refreshCh:   make(chan struct{}, 1),
	}
}

// Name implementa Collector.
func (c *ZpoolCollector) Name() string { return "zpool" }

// RefreshSoon pide una recolecta inmediata al bucle Run (tras una mutación
// como autotrim). No bloquea; si ya hay una petición pendiente, se descarta
// (debounce). La recolecta la ejecuta la propia goroutine de Run, así que no
// hay concurrencia sobre la caché.
func (c *ZpoolCollector) RefreshSoon() {
	select {
	case c.refreshCh <- struct{}{}:
	default:
	}
}

// Run — bucle con ticker, backoff tras 3 fallos seguidos (patrón del skill).
func (c *ZpoolCollector) Run(ctx context.Context) {
	interval := c.interval
	t := time.NewTicker(interval)
	defer t.Stop()
	if err := c.collectOnce(ctx); err != nil {
		log.Printf("zpool: %v", err)
		c.fails++
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.refreshCh:
			// Refresco bajo demanda (mutación reciente): recolecta y
			// reinicia el tick periódico desde este momento.
			if err := c.collectOnce(ctx); err != nil {
				log.Printf("zpool refresh: %v", err)
			}
			t.Reset(interval)
		case <-t.C:
			if err := c.collectOnce(ctx); err != nil {
				log.Printf("zpool: %v", err)
				c.fails++
			} else {
				c.fails = 0
			}
			if c.fails >= 3 {
				if !c.stale {
					log.Printf("zpool: fuente stale tras %d fallos; backoff", c.fails)
				}
				c.stale = true
				interval = min(2*interval, zpoolMaxBackoff)
				t.Reset(interval)
			} else if interval != c.interval {
				c.stale = false
				interval = c.interval
				t.Reset(interval)
			}
		}
	}
}

// Pools — caché de pools (copia defensiva).
func (c *ZpoolCollector) Pools() []model.Pool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]model.Pool, len(c.pools))
	copy(out, c.pools)
	return out
}

// Datasets — caché de datasets.
func (c *ZpoolCollector) Datasets() []model.Dataset {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]model.Dataset, len(c.datasets))
	copy(out, c.datasets)
	return out
}

// SnapshotGroups — snapshots agrupados por dataset, más recientes primero.
func (c *ZpoolCollector) SnapshotGroups() []model.SnapGroup {
	c.mu.RLock()
	defer c.mu.RUnlock()
	byDS := map[string][]model.Snapshot{}
	order := []string{}
	for _, s := range c.snaps {
		ds, _, _ := strings.Cut(s.Full, "@")
		if _, ok := byDS[ds]; !ok {
			order = append(order, ds)
		}
		byDS[ds] = append(byDS[ds], s)
	}
	sort.Strings(order)
	out := make([]model.SnapGroup, 0, len(order))
	for _, ds := range order {
		snaps := byDS[ds]
		sort.Slice(snaps, func(i, j int) bool { return snaps[i].Ts.After(snaps[j].Ts) })
		out = append(out, model.SnapGroup{Dataset: ds, Snaps: snaps})
	}
	return out
}

// collectOnce — una pasada completa: list → status por pool → datasets → snapshots.
func (c *ZpoolCollector) collectOnce(ctx context.Context) error {
	pools, err := c.listPools(ctx)
	if err != nil {
		return err
	}
	now := time.Now()
	for i := range pools {
		c.fillStatus(ctx, &pools[i]) // tolerante: degrada, no falla la pasada
		c.fillTrim(ctx, &pools[i], now)
		c.fillCompressRatio(ctx, &pools[i], now)
		c.fillPoolProps(ctx, &pools[i], now)
		c.resolveVdevPaths(ctx, &pools[i])
	}
	history := map[string][]model.HistoryEntry{}
	for i := range pools {
		// TTL por pool: el historial solo cambia cuando alguien ejecuta
		// comandos, y en pools grandes la lectura cuesta segundos/cientos
		// de MB de salida — no tiene sentido en cada tick de 30 s.
		if time.Since(c.historyAt[pools[i].Name]) < historyTTL {
			continue
		}
		if h := c.fetchHistory(ctx, pools[i].Name); h != nil {
			history[pools[i].Name] = h
			c.historyAt[pools[i].Name] = time.Now()
		}
	}
	datasets, err := c.listDatasets(ctx)
	if err != nil {
		return err
	}
	snaps, err := c.listSnapshots(ctx)
	if err != nil {
		return err
	}

	c.mu.Lock()
	c.pools = pools
	c.datasets = datasets
	c.snaps = snaps
	for name, h := range history {
		c.history[name] = h
	}
	// Olvida historiales de pools que ya no existen
	for name := range c.history {
		found := false
		for i := range pools {
			if pools[i].Name == name {
				found = true
				break
			}
		}
		if !found {
			delete(c.history, name)
		}
	}
	c.mu.Unlock()

	c.publishChanges(pools)
	c.al.EvaluatePools(ctx, pools)
	c.persistSeries(ctx, pools)
	return nil
}

// listPools — 'zpool list -Hp' con columnas explícitas por nombre.
func (c *ZpoolCollector) listPools(ctx context.Context) ([]model.Pool, error) {
	out, err := executil.Run(ctx, 10*time.Second, "zpool", "list", "-Hp",
		"-o", "name,size,alloc,fragmentation,health")
	if err != nil {
		return nil, err
	}
	pools := []model.Pool{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 5 {
			log.Printf("zpool list: línea con %d campos (esperaba 5): %q", len(f), line)
			continue
		}
		p := model.Pool{
			Name:       f[0],
			TotalBytes: parseUint(f[1]),
			UsedBytes:  parseUint(f[2]),
			FragPct:    parsePct(f[3]),
			Status:     f[4],
			Scrub:      model.ScrubInfo{State: "none"},
			Vdevs:      []model.Vdev{},
		}
		pools = append(pools, p)
	}
	return pools, nil
}

// fillPoolProps — propiedades del pool autotrim y checkpoint
// ('zpool get -Hp -o property,value autotrim,checkpoint <pool>').
// checkpoint vale "-" cuando no hay checkpoint activo.
// Estas propiedades cambian muy poco: se cachean con TTL para reducir sudo.
func (c *ZpoolCollector) fillPoolProps(ctx context.Context, p *model.Pool, now time.Time) {
	key := "props:" + p.Name
	if time.Since(c.lastPropsAt[key]) < propTTL {
		return
	}
	out, err := executil.Run(ctx, 5*time.Second, "zpool", "get", "-Hp",
		"-o", "property,value", "autotrim,checkpoint", p.Name)
	if err != nil {
		return
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case "autotrim":
			p.Autotrim = f[1] == "on"
		case "checkpoint":
			p.Checkpoint = f[1] != "-" && f[1] != ""
		}
	}
	c.lastPropsAt[key] = now
}

// fetchHistory — 'zpool history -i <pool>' parseado EN STREAMING (nil si
// falla; se conserva la caché anterior). La salida puede ser enorme (275 MB
// en bigtank): nunca se carga entera en memoria — pipe + ring buffer de
// historyKeep entradas. Timeout generoso porque el kernel tarda ~20 s en
// volcar historiales grandes; si expira, nil y se reintenta en otro tick.
func (c *ZpoolCollector) fetchHistory(ctx context.Context, pool string) []model.HistoryEntry {
	cctx, cancel := context.WithTimeout(ctx, historyTimeout)
	defer cancel()
	cmd := executil.NewCommand(cctx, "zpool", "history", "-i", pool)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil
	}
	if err := cmd.Start(); err != nil {
		return nil
	}
	entries := parseHistoryStream(stdout)
	_ = cmd.Wait()
	if cctx.Err() != nil {
		return nil
	}
	return entries
}

// History — caché del historial del pool (más reciente primero).
func (c *ZpoolCollector) History(name string) []model.HistoryEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	src := c.history[name]
	out := make([]model.HistoryEntry, 0, len(src))
	for i := len(src) - 1; i >= 0; i-- {
		out = append(out, src[i])
	}
	return out
}

// fillCompressRatio — compressratio del dataset raíz del pool como ratio del pool.
// Propiedad estable: se cachea con TTL para reducir llamadas a zfs get.
func (c *ZpoolCollector) fillCompressRatio(ctx context.Context, p *model.Pool, now time.Time) {
	key := "compress:" + p.Name
	if time.Since(c.lastPropsAt[key]) < propTTL {
		return
	}
	out, err := executil.Run(ctx, 5*time.Second, "zfs", "get", "-Hp", "-o", "value",
		"compressratio", p.Name)
	if err != nil {
		return
	}
	v := strings.TrimSuffix(strings.TrimSpace(string(out)), "x")
	if n, err := strconv.ParseFloat(v, 64); err == nil {
		p.CompRatio = n
	}
	c.lastPropsAt[key] = now
}

// --- zpool status: JSON (OpenZFS ≥2.2) con fallback a texto ---

type zpoolStatusJSON struct {
	Pools map[string]struct {
		Name      string              `json:"name"`
		State     string              `json:"state"`
		Vdevs     map[string]jsonVdev `json:"vdevs"`
		ScanStats *struct {
			Function      string  `json:"function"` // "SCRUB" | "RESILVER"
			State         string  `json:"state"`
			Percentage    float64 `json:"percentage"` // puede faltar en resilver
			ToExamine     string  `json:"to_examine"`
			Examined      string  `json:"examined"`
			PassStart     flexInt `json:"pass_start"` // epoch
			TotalSecsLeft flexInt `json:"total_secs_left"`
			Errors        flexInt `json:"errors"`
			EndTime       string  `json:"end_time"`
		} `json:"scan_stats"`
	} `json:"pools"`
}

type jsonVdev struct {
	Name     string              `json:"name"`
	VdevType string              `json:"vdev_type"`
	State    string              `json:"state"`
	Vdevs    map[string]jsonVdev `json:"vdevs"`
}

// flexInt tolera números JSON como número o string ("0").
type flexInt int64

// UnmarshalJSON implementa json.Unmarshaler.
func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		*f = 0
		return nil // tolerante
	}
	*f = flexInt(n)
	return nil
}

// fillStatus rellena vdevs y scrub; intenta --json y cae a texto plano.
func (c *ZpoolCollector) fillStatus(ctx context.Context, p *model.Pool) {
	out, err := executil.Run(ctx, 15*time.Second, "zpool", "status", "--json", p.Name)
	if err == nil {
		if c.parseStatusJSON(out, p) {
			return
		}
	}
	out, err = executil.Run(ctx, 15*time.Second, "zpool", "status", p.Name)
	if err != nil {
		return
	}
	c.parseStatusText(string(out), p)
}

// parseStatusJSON — vía primaria. Devuelve false si el JSON no es reconocible.
func (c *ZpoolCollector) parseStatusJSON(out []byte, p *model.Pool) bool {
	var st zpoolStatusJSON
	if err := json.Unmarshal(out, &st); err != nil {
		return false
	}
	pj, ok := st.Pools[p.Name]
	if !ok {
		return false
	}
	if pj.State != "" {
		p.Status = pj.State
	}
	roles := map[string]bool{}
	p.RaidzVdevs = nil
	for _, root := range pj.Vdevs {
		c.walkVdev(root, "stripe", p, roles, false)
	}
	p.Topo = topoFromRoles(roles)
	if ss := pj.ScanStats; ss != nil {
		kind := "scrub"
		if ss.Function == "RESILVER" {
			kind = "resilver"
		}
		if strings.Contains(strings.ToUpper(ss.Function), "EXPAND") {
			kind = "expand" // RAID-Z expansion (lote D)
		}
		switch ss.State {
		case "DSS_SCANNING", "SCANNING":
			pct := ss.Percentage
			if pct == 0 {
				// resilver sin percentage: examined/to_examine ("34.0G"/"35.2T")
				if tot, ok := parseHumanSize(ss.ToExamine); ok && tot > 0 {
					if ex, ok2 := parseHumanSize(ss.Examined); ok2 {
						pct = float64(ex) * 100 / float64(tot)
					}
				}
			}
			eta := int64(ss.TotalSecsLeft)
			if eta == 0 && pct > 0.05 && ss.PassStart > 0 {
				// ETA por tasa media desde el inicio del pase
				elapsed := time.Now().Unix() - int64(ss.PassStart)
				if elapsed > 0 {
					eta = int64(float64(elapsed) * (100 - pct) / pct)
				}
			}
			info := model.ScrubInfo{State: "running", Kind: kind, Pct: pct,
				EtaSec: eta, Ts: time.Now().UTC(), Errors: int64(ss.Errors)}
			if n, ok := parseHumanSize(ss.Examined); ok {
				info.BytesDone = n
			}
			if n, ok := parseHumanSize(ss.ToExamine); ok {
				info.BytesTotal = n
			}
			p.Scrub = info
		case "DSS_FINISHED", "FINISHED":
			p.Scrub = model.ScrubInfo{State: "done", Kind: kind, Pct: 100,
				Ts: parseZfsTime(ss.EndTime), Errors: int64(ss.Errors)}
		}
	}
	return true
}

// parseHumanSize — tamaños de zpool status ("35.2T", "34.0G", "0B", "748K").
func parseHumanSize(s string) (uint64, bool) {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" {
		return 0, false
	}
	mult := uint64(1)
	last := s[len(s)-1]
	if last < '0' || last > '9' {
		s = s[:len(s)-1]
		switch last {
		case 'B': // "0B": la letra es solo unidad
			mult = 1
			if len(s) > 0 && (s[len(s)-1] < '0' || s[len(s)-1] > '9') {
				return 0, false
			}
		case 'K':
			mult = 1 << 10
		case 'M':
			mult = 1 << 20
		case 'G':
			mult = 1 << 30
		case 'T':
			mult = 1 << 40
		case 'P':
			mult = 1 << 50
		default:
			return 0, false
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return uint64(f * float64(mult)), true
}

// walkVdev recorre el árbol JSON de vdevs recogiendo discos hoja y roles.
// Los hijos de un contenedor 'replacing-N' se marcan Replacing: son la pareja
// viejo+nuevo de una sustitución en curso (el viejo desaparece al terminar).
func (c *ZpoolCollector) walkVdev(v jsonVdev, role string, p *model.Pool, roles map[string]bool, replacing bool) {
	t := vdevRole(v.Name, v.VdevType)
	if t != "" {
		role = t
		roles[t] = true
		// Contenedor raidz ('raidz2-0'): objetivo de RAID-Z expansion.
		if strings.HasPrefix(t, "raidz") && reRaidzName.MatchString(v.Name) {
			p.RaidzVdevs = append(p.RaidzVdevs, v.Name)
		}
	}
	if strings.HasPrefix(v.Name, "replacing-") {
		replacing = true
	}
	if len(v.Vdevs) == 0 {
		if v.Name != p.Name && v.VdevType != "root" {
			p.Vdevs = append(p.Vdevs, model.Vdev{
				Dev:       baseName(v.Name),
				Role:      role,
				Status:    v.State,
				Replacing: replacing,
			})
		}
		return
	}
	// orden estable para la UI
	names := make([]string, 0, len(v.Vdevs))
	for n := range v.Vdevs {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		child := v.Vdevs[n]
		c.walkVdev(child, role, p, roles, replacing)
	}
}

// vdevRole clasifica un vdev contenedor por nombre/tipo.
func vdevRole(name, vtype string) string {
	s := name + " " + vtype
	switch {
	case strings.Contains(s, "raidz3"):
		return "raidz3"
	case strings.Contains(s, "raidz2"):
		return "raidz2"
	case strings.Contains(s, "raidz"):
		return "raidz1"
	case strings.Contains(s, "mirror"):
		return "mirror"
	case strings.Contains(s, "spare"):
		return "spare"
	case strings.Contains(s, "log"):
		return "log"
	case strings.Contains(s, "cache"):
		return "cache"
	}
	return ""
}

// topoFromRoles — la topología del pool = el rol de datos "más fuerte".
func topoFromRoles(roles map[string]bool) string {
	for _, t := range []string{"raidz3", "raidz2", "raidz1", "mirror"} {
		if roles[t] {
			return t
		}
	}
	return "stripe"
}

// parseZfsTime — formato de fechas de zpool status ('Thu Jun  6 05:12:33 2024').
func parseZfsTime(s string) time.Time {
	if t, err := time.Parse("Mon Jan _2 15:04:05 2006", s); err == nil {
		return t.UTC()
	}
	return time.Now().UTC()
}

// --- Fallback texto (OpenZFS <2.2, sin --json). Mejor esfuerzo documentado. ---

// reRaidzName — nombre de vdev contenedor raidz ('raidz2-0').
var reRaidzName = regexp.MustCompile(`^raidz[123]-\d+$`)

var (
	vdevLineRe  = regexp.MustCompile(`^(\s+)(\S+)\s+(ONLINE|DEGRADED|FAULTED|UNAVAIL|OFFLINE|REMOVED)\s+\d+`)
	copiedRe    = regexp.MustCompile(`([\d.]+[KMGTPE]B?|\d+B)\s+copied`)
	scrubDoneRe = regexp.MustCompile(`scrub .* with (\d+) errors on (.+)$`)
	scrubPctRe  = regexp.MustCompile(`(\d+(?:\.\d+)?)%\s+done`)
	scrubEtaRe  = regexp.MustCompile(`(\d+):(\d{2}):(\d{2}) to go`)
	scannedRe   = regexp.MustCompile(`([\d.]+[KMGTPE]B?|\d+B)\s+scanned`)
	issuedRe    = regexp.MustCompile(`([\d.]+[KMGTPE]B?|\d+B)\s+issued`)
	trimDoneRe  = regexp.MustCompile(`trimmed .* with (\d+) errors on (.+)$`)
	trimAmtRe   = regexp.MustCompile(`([\d.]+[KMGTPE]B?|\d+B)\s+trimmed`)
)

// parseStatusText — parseo defensivo del formato clásico de 'zpool status'.
func (c *ZpoolCollector) parseStatusText(out string, p *model.Pool) {
	roles := map[string]bool{}
	curRole := "stripe"
	inConfig := false
	replIndent := -1 // indentación del contenedor 'replacing-N' activo (-1 = no)
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "config:") {
			inConfig = true
			continue
		}
		if inConfig {
			m := vdevLineRe.FindStringSubmatch(line)
			if m != nil {
				indent, name, state := len(m[1]), m[2], m[3]
				if strings.HasPrefix(name, "replacing-") {
					replIndent = indent
					continue
				}
				replacing := replIndent >= 0 && indent > replIndent
				if replIndent >= 0 && indent <= replIndent {
					replIndent = -1
				}
				if r := vdevRole(name, ""); r != "" {
					curRole = r
					roles[r] = true
					if strings.HasPrefix(r, "raidz") && reRaidzName.MatchString(name) {
						p.RaidzVdevs = append(p.RaidzVdevs, name)
					}
					continue
				}
				if name == p.Name {
					continue
				}
				p.Vdevs = append(p.Vdevs, model.Vdev{
					Dev:       baseName(name),
					Role:      curRole,
					Status:    state,
					Replacing: replacing,
				})
			}
		}
		if strings.HasPrefix(strings.TrimSpace(line), "scan:") {
			c.parseScanLine(line, p)
		} else if p.Scrub.State == "running" {
			// la línea de progreso va separada del 'scan:' en algunos formatos
			if m := scrubPctRe.FindStringSubmatch(line); m != nil {
				p.Scrub.Pct, _ = strconv.ParseFloat(m[1], 64)
			}
			if m := scrubEtaRe.FindStringSubmatch(line); m != nil {
				h, _ := strconv.Atoi(m[1])
				mi, _ := strconv.Atoi(m[2])
				se, _ := strconv.Atoi(m[3])
				p.Scrub.EtaSec = int64(h*3600 + mi*60 + se)
			}
			if m := scannedRe.FindStringSubmatch(line); m != nil {
				if n, ok := parseHumanSize(m[1]); ok {
					p.Scrub.BytesDone = n
				}
			}
			if m := issuedRe.FindStringSubmatch(line); m != nil {
				if n, ok := parseHumanSize(m[1]); ok {
					p.Scrub.BytesTotal = n
				}
			}
			// expansión: "1.23T copied at 100M/s, 45.67% done, 0:30:15 to go"
			if m := copiedRe.FindStringSubmatch(line); m != nil {
				if n, ok := parseHumanSize(m[1]); ok {
					p.Scrub.BytesDone = n
				}
			}
		}
	}
	p.Topo = topoFromRoles(roles)
}

// fillTrim — progreso de TRIM ('zpool status -t <pool>'; la salida normal no
// lo muestra). Solo rellena Scrub si el pool no tiene scrub/resilver en curso
// (el scan de datos manda sobre el trim en la representación unificada).
// Se ejecuta con TTL: reduce llamadas sudo cuando no hay trim activo.
func (c *ZpoolCollector) fillTrim(ctx context.Context, p *model.Pool, now time.Time) {
	key := "trim:" + p.Name
	if time.Since(c.lastPropsAt[key]) < trimTTL {
		return
	}
	out, err := executil.Run(ctx, 15*time.Second, "zpool", "status", "-t", p.Name)
	if err != nil {
		return
	}
	c.parseTrimStatus(string(out), p)
	c.lastPropsAt[key] = now
}

// parseTrimStatus — líneas 'scan:' de 'zpool status -t':
//
//	scan: trim in progress since Wed Aug 13 02:00:00 2025
//	        1.23T trimmed at 100M/s, 45.2% done, 0:30:15 to go
//	scan: trimmed 2.34T in 0 days 00:30:15 with 0 errors on Wed ...
func (c *ZpoolCollector) parseTrimStatus(out string, p *model.Pool) {
	trimRunning := false
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "scan:") {
			trimRunning = false
			switch {
			case strings.Contains(line, "trim in progress"):
				if p.Scrub.State == "running" {
					continue // hay scrub/resilver en curso: manda
				}
				p.Scrub = model.ScrubInfo{State: "running", Kind: "trim", Ts: time.Now().UTC()}
				trimRunning = true
			case strings.Contains(line, "trimmed"):
				if p.Scrub.State == "running" {
					continue
				}
				st := model.ScrubInfo{State: "done", Kind: "trim", Pct: 100, Ts: time.Now().UTC()}
				if m := trimDoneRe.FindStringSubmatch(line); m != nil {
					st.Errors, _ = strconv.ParseInt(m[1], 10, 64)
					st.Ts = parseZfsTime(m[2])
				}
				p.Scrub = st
			}
			continue
		}
		if !trimRunning {
			continue
		}
		if m := trimAmtRe.FindStringSubmatch(line); m != nil {
			if n, ok := parseHumanSize(m[1]); ok {
				p.Scrub.BytesDone = n
			}
		}
		if m := scrubPctRe.FindStringSubmatch(line); m != nil {
			p.Scrub.Pct, _ = strconv.ParseFloat(m[1], 64)
		}
		if m := scrubEtaRe.FindStringSubmatch(line); m != nil {
			h, _ := strconv.Atoi(m[1])
			mi, _ := strconv.Atoi(m[2])
			se, _ := strconv.Atoi(m[3])
			p.Scrub.EtaSec = int64(h*3600 + mi*60 + se)
		}
	}
}

// parseScanLine interpreta la línea 'scan:' del estado.
func (c *ZpoolCollector) parseScanLine(line string, p *model.Pool) {
	switch {
	case strings.Contains(line, "expansion in progress"):
		// RAID-Z expansion (OpenZFS ≥ 2.3): la relocalización de bloques
		// avanza como un scan con % y ETA (misma representación unificada).
		p.Scrub = model.ScrubInfo{State: "running", Kind: "expand", Ts: time.Now().UTC()}
		if m := scrubPctRe.FindStringSubmatch(line); m != nil {
			p.Scrub.Pct, _ = strconv.ParseFloat(m[1], 64)
		}
		if m := scrubEtaRe.FindStringSubmatch(line); m != nil {
			h, _ := strconv.Atoi(m[1])
			mi, _ := strconv.Atoi(m[2])
			se, _ := strconv.Atoi(m[3])
			p.Scrub.EtaSec = int64(h*3600 + mi*60 + se)
		}
	case strings.Contains(line, "expansion completed") || strings.Contains(line, "expanded "):
		p.Scrub = model.ScrubInfo{State: "done", Kind: "expand", Pct: 100, Ts: time.Now().UTC()}
	case strings.Contains(line, "scrub in progress") || strings.Contains(line, "resilver in progress"):
		kind := "scrub"
		if strings.Contains(line, "resilver") {
			kind = "resilver"
		}
		p.Scrub = model.ScrubInfo{State: "running", Kind: kind, Ts: time.Now().UTC()}
		if m := scrubPctRe.FindStringSubmatch(line); m != nil {
			p.Scrub.Pct, _ = strconv.ParseFloat(m[1], 64)
		}
		if m := scrubEtaRe.FindStringSubmatch(line); m != nil {
			h, _ := strconv.Atoi(m[1])
			mi, _ := strconv.Atoi(m[2])
			se, _ := strconv.Atoi(m[3])
			p.Scrub.EtaSec = int64(h*3600 + mi*60 + se)
		}
	case strings.Contains(line, "scrub repaired") || strings.Contains(line, "scrub resilvered") || strings.Contains(line, "resilvered "):
		kind := "scrub"
		if strings.Contains(line, "resilver") {
			kind = "resilver"
		}
		st := model.ScrubInfo{State: "done", Kind: kind, Pct: 100, Ts: time.Now().UTC()}
		if m := scrubDoneRe.FindStringSubmatch(line); m != nil {
			st.Errors, _ = strconv.ParseInt(m[1], 10, 64)
			st.Ts = parseZfsTime(m[2])
		}
		p.Scrub = st
	case strings.Contains(line, "none requested"):
		p.Scrub = model.ScrubInfo{State: "none"}
	}
}

// --- resolución de vdevs a dispositivos reales ---

// reUUID — nombres de vdev que son UUID (pools heredados que usan PARTUUID
// como nombre, p.ej. venidos de TrueNAS: zpool status muestra el UUID en vez
// de la ruta del dispositivo).
var reUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// resolveVdevPaths rellena Vdev.Path con la ruta real del dispositivo:
//   - nombre normal ('sdb1') → '/dev/sdb1' si existe.
//   - nombre UUID (pools heredados que usan PARTUUID, p.ej. TrueNAS) →
//     symlink /dev/disk/by-partuuid/<uuid>.
//   - nombre by-id ('nvme-XXXX', 'ata-XXXX-part1') → symlink /dev/disk/by-id/<nombre>.
//
// Si nada resuelve (disco retirado), Path queda "".
func (c *ZpoolCollector) resolveVdevPaths(_ context.Context, p *model.Pool) {
	for j := range p.Vdevs {
		p.Vdevs[j].Path = resolveDevPath(p.Vdevs[j].Dev)
	}
}

// resolveDevPath traduce el nombre de un vdev a su ruta /dev real ("" si no).
func resolveDevPath(dev string) string {
	candidates := []string{"/dev/" + dev}
	if reUUID.MatchString(dev) {
		candidates = append([]string{"/dev/disk/by-partuuid/" + strings.ToLower(dev)}, candidates...)
	} else {
		candidates = append(candidates, "/dev/disk/by-id/"+dev)
	}
	for _, cand := range candidates {
		if target, err := filepath.EvalSymlinks(cand); err == nil {
			if st, err := os.Stat(target); err == nil && st.Mode()&os.ModeDevice != 0 {
				return target
			}
		}
	}
	return ""
}

// --- datasets y snapshots ---

// listDatasets — 'zfs list -Hp -t filesystem,volume' con columnas por nombre.
// encryption es el valor EFECTIVO (heredado resuelto por zfs list); keystatus
// es available/unavailable/"-" (lote D: cifrado nativo por dataset).
func (c *ZpoolCollector) listDatasets(ctx context.Context) ([]model.Dataset, error) {
	out, err := executil.Run(ctx, 10*time.Second, "zfs", "list", "-Hp",
		"-t", "filesystem,volume",
		"-o", "name,type,compression,used,avail,quota,mountpoint,encryption,keystatus")
	if err != nil {
		return nil, err
	}
	datasets := []model.Dataset{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 9 {
			log.Printf("zfs list: línea con %d campos (esperaba 9): %q", len(f), line)
			continue
		}
		typ := "fs"
		if f[1] == "volume" {
			typ = "volume"
		}
		datasets = append(datasets, model.Dataset{
			Name:        f[0],
			Type:        typ,
			Compression: f[2],
			UsedBytes:   parseUint(f[3]),
			AvailBytes:  parseUint(f[4]),
			QuotaBytes:  parseUint(f[5]), // '-' → 0 (sin cuota)
			Mountpoint:  f[6],
			Encryption:  f[7],
			KeyStatus:   f[8],
		})
	}
	return datasets, nil
}

// listSnapshots — 'zfs list -Hp -t snapshot' (creation en epoch con -p).
func (c *ZpoolCollector) listSnapshots(ctx context.Context) ([]model.Snapshot, error) {
	out, err := executil.Run(ctx, 10*time.Second, "zfs", "list", "-Hp",
		"-t", "snapshot", "-o", "name,creation,used")
	if err != nil {
		return nil, err
	}
	snaps := []model.Snapshot{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 3 {
			continue
		}
		_, snapName, _ := strings.Cut(f[0], "@")
		kind := "manual"
		if strings.HasPrefix(snapName, model.AutoSnapPrefix) {
			kind = "auto"
		}
		snaps = append(snaps, model.Snapshot{
			Name:      snapName,
			Full:      f[0],
			Ts:        time.Unix(int64(parseUint(f[1])), 0).UTC(),
			UsedBytes: parseUint(f[2]),
			Kind:      kind,
		})
	}
	return snaps, nil
}

// --- eventos SSE y series ---

// publishChanges emite pool.status y scrub.progress solo cuando cambian.
func (c *ZpoolCollector) publishChanges(pools []model.Pool) {
	changed := false
	for _, p := range pools {
		if c.prevStatus[p.Name] != "" && c.prevStatus[p.Name] != p.Status {
			c.h.Publish("pool.status", map[string]any{"name": p.Name, "status": p.Status})
			changed = true
		}
		c.prevStatus[p.Name] = p.Status

		pct := int(p.Scrub.Pct)
		if p.Scrub.State == "running" && c.prevPct[p.Name] != pct {
			c.h.Publish("scrub.progress", map[string]any{
				"pool": p.Name, "pct": p.Scrub.Pct, "eta_sec": p.Scrub.EtaSec, "kind": p.Scrub.Kind,
			})
			c.prevPct[p.Name] = pct
		}
		if p.Scrub.State == "done" && c.prevPct[p.Name] != 100 {
			c.h.Publish("scrub.progress", map[string]any{
				"pool": p.Name, "pct": 100.0, "eta_sec": int64(0), "kind": p.Scrub.Kind,
			})
			c.prevPct[p.Name] = 100
			changed = true
		}
	}
	if changed {
		c.h.Publish("overview", map[string]any{"reason": "pool.status"})
	}
}

// persistSeries guarda pool.<name>.used_pct cada seriesInterval (con retención).
func (c *ZpoolCollector) persistSeries(ctx context.Context, pools []model.Pool) {
	now := time.Now()
	for _, p := range pools {
		key := "pool." + p.Name + ".used_pct"
		if last, ok := c.lastSeries[key]; ok && now.Sub(last) < seriesInterval {
			continue
		}
		if p.TotalBytes == 0 {
			continue
		}
		pct := float64(p.UsedBytes) * 100 / float64(p.TotalBytes)
		if _, err := c.db.ExecContext(ctx,
			"INSERT INTO series(source, ts, value) VALUES (?,?,?)",
			key, now.UTC().Format(time.RFC3339), pct); err != nil {
			log.Printf("zpool series: %v", err)
			continue
		}
		c.lastSeries[key] = now
	}
}

// --- helpers de parseo ---

// parseUint convierte un campo numérico de salida -p ('12345' o '-').
func parseUint(s string) uint64 {
	if s == "-" || s == "" {
		return 0
	}
	n, _ := strconv.ParseUint(s, 10, 64)
	return n
}

// parsePct convierte '12' o '12%' a float64.
func parsePct(s string) float64 {
	s = strings.TrimSuffix(s, "%")
	n, _ := strconv.ParseFloat(s, 64)
	return n
}
