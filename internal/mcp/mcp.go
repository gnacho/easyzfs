// Package mcp expone un servidor MCP (Model Context Protocol) embebido en
// EasyZFS (#146). Corre en el mismo binario y proceso, montado en /mcp con
// transporte streamable-HTTP de mcp-go. Auth: SOLO API key Bearer
// (internal/apikeys), nunca cookie de sesion. Activacion: EASYZFS_MCP_ENABLED=1
// (config.MCPEnabled); desactivado por defecto.
//
// Primera tanda de tools: SOLO LECTURA sobre las caches en memoria de los
// colectores y el alerter. Nunca ejecutan comandos ZFS ni mutan estado. Todos
// declaran readOnlyHint desde el dia uno; los writes futuros deberan declarar
// destructiveHint.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"easyzfs/internal/alerts"
	"easyzfs/internal/collectors"
	"easyzfs/internal/model"
	"easyzfs/internal/scheduler"
)

// Deps son las fuentes de datos de los tools. Todo lectura: caches en memoria,
// alerter y store de jobs. Los providers se inyectan desde main y son los
// mismos que consume httpapi, sin copiar estado sensible del servidor.
type Deps struct {
	Pools      collectors.PoolProvider
	Disks      collectors.DiskProvider
	Perf       collectors.PerfProvider
	Caps       collectors.CapProvider
	Alerter    *alerts.Alerter
	Jobs       *scheduler.Store
	Version    string
	Build      string
	ZFSVersion string
	Demo       bool
	RatePerMin int
}

// Server envuelve el MCPServer de mcp-go con las fuentes de EasyZFS y la
// auditoria de invocaciones.
type Server struct {
	pools      collectors.PoolProvider
	disks      collectors.DiskProvider
	perf       collectors.PerfProvider
	caps       collectors.CapProvider
	alerter    *alerts.Alerter
	jobs       *scheduler.Store
	version    string
	build      string
	zfsVersion string
	demo       bool
	started    time.Time
	http       *server.StreamableHTTPServer
	ratePerMin int
}

// New construye el servidor MCP con la primera tanda de tools (read-only).
func New(d Deps) *Server {
	s := &Server{
		pools: d.Pools, disks: d.Disks, perf: d.Perf, caps: d.Caps,
		alerter: d.Alerter, jobs: d.Jobs,
		version: d.Version, build: d.Build, zfsVersion: d.ZFSVersion, demo: d.Demo,
		started: time.Now(), ratePerMin: d.RatePerMin,
	}
	if s.ratePerMin <= 0 {
		s.ratePerMin = 60
	}
	mcpServer := server.NewMCPServer(
		"easyzfs",
		d.Version,
		server.WithToolCapabilities(false),
	)
	s.addTools(mcpServer)
	s.http = server.NewStreamableHTTPServer(mcpServer, server.WithStateLess(true))
	return s
}

// readOnly es la anotacion comun de TODA la primera tanda: los clientes AI
// pueden ejecutarlas libremente sin pedir confirmacion al usuario. mcp-go
// defaulta destructiveHint/openWorldHint a TRUE, asi que hay que apagarlos
// explicitamente en los tools de solo lectura.
var readOnly = []mcp.ToolOption{
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
}

// tool arma un tool con descripcion + opciones propias + las anotaciones
// read-only comunes. Helper porque Go no permite mezclar argumentos sueltos y
// spread (s...) en una misma llamada variadica.
func tool(name, description string, opts ...mcp.ToolOption) mcp.Tool {
	all := make([]mcp.ToolOption, 0, len(opts)+len(readOnly)+1)
	all = append(all, mcp.WithDescription(description))
	all = append(all, opts...)
	all = append(all, readOnly...)
	return mcp.NewTool(name, all...)
}

// addTools registra los siete tools de solo lectura.
func (s *Server) addTools(mcpServer *server.MCPServer) {
	mcpServer.AddTool(tool("server_status",
		"EasyZFS service status: version, uptime, pool counts and capacity, dataset and snapshot totals, disk health counts, ARC and performance summary, enabled jobs and recent alerts. Read-only.",
	), s.run("server_status", s.toolServerStatus))

	mcpServer.AddTool(tool("list_pools",
		"ZFS pools from the latest collector cache, including topology, capacity, scrub state, vdevs and known pools that are currently missing. Read-only.",
	), s.run("list_pools", s.toolListPools))

	mcpServer.AddTool(tool("pool_health",
		"Health and recent history of one ZFS pool from the latest collector cache. Read-only.",
		mcp.WithString("pool_name",
			mcp.Required(),
			mcp.Description("Pool name as listed by list_pools"),
		),
	), s.run("pool_health", s.toolPoolHealth))

	mcpServer.AddTool(tool("list_datasets",
		"ZFS datasets and volumes from the latest collector cache, optionally filtered by pool. Read-only.",
		mcp.WithString("pool",
			mcp.Description("Optional pool name filter, for example tank"),
		),
	), s.run("list_datasets", s.toolListDatasets))

	mcpServer.AddTool(tool("list_snapshots",
		"ZFS snapshots grouped by dataset from the latest collector cache, optionally filtered by dataset. Read-only.",
		mcp.WithString("dataset",
			mcp.Description("Optional exact dataset name, for example tank/docs"),
		),
	), s.run("list_snapshots", s.toolListSnapshots))

	mcpServer.AddTool(tool("list_disks",
		"Physical disks from the latest collector cache with SMART summary, temperature, capacity and pool membership when known. Read-only.",
	), s.run("list_disks", s.toolListDisks))

	mcpServer.AddTool(tool("alerts",
		"Recent EasyZFS alerts from the alert store. Pass active_only=true to list only unacknowledged alerts. Read-only.",
		mcp.WithBoolean("active_only",
			mcp.Description("Only return unacknowledged alerts (default false = all recent alerts)"),
		),
	), s.run("alerts", s.toolAlerts))
}

// toolFunc produce el resultado de un tool a partir de las caches actuales y
// los argumentos de la peticion.
type toolFunc func(ctx context.Context, req mcp.CallToolRequest) (any, error)

// run envuelve cada tool con auditoria. Los errores de negocio se devuelven
// como ToolResultError (isError), no como error Go: el protocolo lo trata como
// resultado valido con error dentro.
func (s *Server) run(name string, fn toolFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()
		v, err := fn(ctx, req)
		audit(name, req, err, time.Since(start))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultJSON(v)
	}
}

// audit registra cada invocacion de tool: nombre, argumentos, exito y duracion.
// Es la traza de auditoria de qué asistente tocó qué y cuándo.
func audit(name string, req mcp.CallToolRequest, err error, dur time.Duration) {
	args, _ := json.Marshal(req.GetArguments())
	if err != nil {
		log.Printf("[easyzfs] mcp: tool=%s args=%s error=%q dur=%s", name, args, err.Error(), dur)
		return
	}
	log.Printf("[easyzfs] mcp: tool=%s args=%s ok dur=%s", name, args, dur)
}

func (s *Server) capabilities() model.Capabilities {
	if s.caps != nil {
		if c := s.caps.Capabilities(); c.Version != "" {
			return c
		}
	}
	return model.Capabilities{Version: s.zfsVersion}
}

func (s *Server) poolsWithTemps() []model.Pool {
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
	return pools
}

func (s *Server) missingPools(ctx context.Context) ([]model.MissingPool, error) {
	if s.alerter == nil {
		return []model.MissingPool{}, nil
	}
	m, err := s.alerter.MissingPools(ctx)
	if err != nil {
		return nil, fmt.Errorf("list missing pools: %w", err)
	}
	if m == nil {
		m = []model.MissingPool{}
	}
	return m, nil
}

// serverStatus resume el estado del servicio sin exponer rutas internas ni
// otros datos sensibles de /api/version.
type serverStatus struct {
	Name            string             `json:"name"`
	Version         string             `json:"version"`
	Build           string             `json:"build,omitempty"`
	UptimeSec       int64              `json:"uptime_sec"`
	ZFSVersion      string             `json:"zfs_version"`
	Capabilities    model.Capabilities `json:"capabilities"`
	Demo            bool               `json:"demo"`
	PoolsTotal      int                `json:"pools_total"`
	PoolsOnline     int                `json:"pools_online"`
	PoolsDegraded   int                `json:"pools_degraded"`
	CapUsedBytes    uint64             `json:"cap_used_bytes"`
	CapTotalBytes   uint64             `json:"cap_total_bytes"`
	DatasetsTotal   int                `json:"datasets_total"`
	SnapshotsTotal  int                `json:"snapshots_total"`
	DisksTotal      int                `json:"disks_total"`
	DisksSmartOK    int                `json:"disks_smart_ok"`
	DisksSmartWarn  int                `json:"disks_smart_warn"`
	DisksSmartCrit  int                `json:"disks_smart_crit"`
	JobsEnabled     int                `json:"jobs_enabled"`
	Arc             *model.ArcStats    `json:"arc,omitempty"`
	PoolPerformance []model.PoolPerf   `json:"pool_performance,omitempty"`
	AlertsTotal     int                `json:"alerts_total"`
	UnreadAlerts    int                `json:"unread_alerts"`
}

func (s *Server) toolServerStatus(ctx context.Context, _ mcp.CallToolRequest) (any, error) {
	if s.pools == nil || s.disks == nil || s.perf == nil {
		return nil, fmt.Errorf("collector caches are not available yet")
	}
	pools := s.pools.Pools()
	groups := s.pools.SnapshotGroups()
	disks := s.disks.Disks()
	out := serverStatus{
		Name:         "EasyZFS",
		Version:      s.version,
		Build:        s.build,
		UptimeSec:    int64(time.Since(s.started).Seconds()),
		ZFSVersion:   s.capabilities().Version,
		Capabilities: s.capabilities(),
		Demo:         s.demo,
		PoolsTotal:   len(pools),
		DisksTotal:   len(disks),
	}
	for _, p := range pools {
		switch p.Status {
		case "ONLINE":
			out.PoolsOnline++
		case "DEGRADED", "FAULTED":
			out.PoolsDegraded++
		}
		out.CapUsedBytes += p.UsedBytes
		out.CapTotalBytes += p.TotalBytes
	}
	out.DatasetsTotal = len(s.pools.Datasets())
	for _, g := range groups {
		out.SnapshotsTotal += len(g.Snaps)
	}
	for _, d := range disks {
		switch d.Smart {
		case "ok":
			out.DisksSmartOK++
		case "warn":
			out.DisksSmartWarn++
		case "crit":
			out.DisksSmartCrit++
		}
	}
	if s.jobs != nil {
		jobs, err := s.jobs.List(ctx)
		if err != nil {
			return nil, fmt.Errorf("list scheduled jobs: %w", err)
		}
		for _, j := range jobs {
			if j.Enabled {
				out.JobsEnabled++
			}
		}
	}
	perf := s.perf.Performance()
	out.Arc = perf.Arc
	out.PoolPerformance = perf.Pools
	if s.alerter != nil {
		list, err := s.alerter.List(ctx, 100)
		if err != nil {
			return nil, fmt.Errorf("list alerts: %w", err)
		}
		out.AlertsTotal = len(list)
		for _, a := range list {
			if !a.Acked {
				out.UnreadAlerts++
			}
		}
	}
	return out, nil
}

func (s *Server) toolListPools(ctx context.Context, _ mcp.CallToolRequest) (any, error) {
	if s.pools == nil || s.disks == nil {
		return nil, fmt.Errorf("collector caches are not available yet")
	}
	pools := s.poolsWithTemps()
	missing, err := s.missingPools(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"total":         len(pools),
		"pools":         pools,
		"missing_pools": missing,
	}, nil
}

type poolHealth struct {
	Pool    model.Pool           `json:"pool"`
	History []model.HistoryEntry `json:"history"`
}

func (s *Server) toolPoolHealth(_ context.Context, req mcp.CallToolRequest) (any, error) {
	if s.pools == nil || s.disks == nil {
		return nil, fmt.Errorf("collector caches are not available yet")
	}
	name, err := req.RequireString("pool_name")
	if err != nil {
		return nil, err
	}
	pools := s.poolsWithTemps()
	for _, p := range pools {
		if p.Name == name {
			history := s.pools.History(name)
			if history == nil {
				history = []model.HistoryEntry{}
			}
			return poolHealth{Pool: p, History: history}, nil
		}
	}
	known := make([]string, 0, len(pools))
	for _, p := range pools {
		known = append(known, p.Name)
	}
	return nil, fmt.Errorf("pool %q not found in the latest collector cache; known pools: %v", name, known)
}

func (s *Server) toolListDatasets(_ context.Context, req mcp.CallToolRequest) (any, error) {
	if s.pools == nil {
		return nil, fmt.Errorf("dataset cache is not available yet")
	}
	pool := strings.TrimSpace(req.GetString("pool", ""))
	out := make([]model.Dataset, 0)
	for _, d := range s.pools.Datasets() {
		if pool == "" || d.Name == pool || strings.HasPrefix(d.Name, pool+"/") {
			out = append(out, d)
		}
	}
	return map[string]any{"total": len(out), "datasets": out}, nil
}

func (s *Server) toolListSnapshots(_ context.Context, req mcp.CallToolRequest) (any, error) {
	if s.pools == nil {
		return nil, fmt.Errorf("snapshot cache is not available yet")
	}
	dataset := strings.TrimSpace(req.GetString("dataset", ""))
	groups := s.pools.SnapshotGroups()
	total := 0
	if dataset == "" {
		for _, g := range groups {
			total += len(g.Snaps)
		}
		return map[string]any{"total": total, "groups": groups}, nil
	}
	filtered := make([]model.SnapGroup, 0, 1)
	for _, g := range groups {
		if g.Dataset == dataset {
			filtered = append(filtered, g)
			total += len(g.Snaps)
		}
	}
	return map[string]any{"total": total, "groups": filtered}, nil
}

func (s *Server) toolListDisks(_ context.Context, _ mcp.CallToolRequest) (any, error) {
	if s.pools == nil || s.disks == nil {
		return nil, fmt.Errorf("disk cache is not available yet")
	}
	disks := s.disks.Disks()
	pools := s.pools.Pools()
	poolNames := make([]string, 0, len(pools))
	vdevs := map[string][]string{}
	for _, p := range pools {
		poolNames = append(poolNames, p.Name)
		for _, v := range p.Vdevs {
			vdevs[p.Name] = append(vdevs[p.Name], v.Dev)
			if v.Path != "" {
				vdevs[p.Name] = append(vdevs[p.Name], v.Path)
			}
		}
	}
	for i := range disks {
		if disks[i].Pool == "" {
			disks[i].Pool = poolForDisk(poolNames, vdevs, disks[i].Dev, disks[i].ByID)
		}
	}
	return map[string]any{"total": len(disks), "disks": disks}, nil
}

func (s *Server) toolAlerts(ctx context.Context, req mcp.CallToolRequest) (any, error) {
	if s.alerter == nil {
		return nil, fmt.Errorf("alert store is not available yet")
	}
	activeOnly := req.GetBool("active_only", false)
	list, err := s.alerter.List(ctx, 100)
	if err != nil {
		return nil, fmt.Errorf("list alerts: %w", err)
	}
	out := make([]model.Alert, 0, len(list))
	unread := 0
	for _, a := range list {
		if a.Acked {
			if activeOnly {
				continue
			}
		} else {
			unread++
		}
		out = append(out, a)
	}
	missing, err := s.missingPools(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"total":         len(out),
		"unread":        unread,
		"alerts":        out,
		"missing_pools": missing,
	}, nil
}

// vdevKey reduce un nombre o ruta de vdev a la clave para cruzar con discos.
// Espejo de httpapi.vdevKey: el MCP no importa httpapi para no acoplar paquetes.
func vdevKey(v string) string {
	v = strings.TrimPrefix(v, "/dev/")
	v = strings.TrimPrefix(v, "disk/by-id/")
	return stripPart(v)
}

// stripPart quita el sufijo de particion: sdb1 -> sdb, nvme0n1p2 -> nvme0n1.
func stripPart(dev string) string {
	if i := strings.LastIndex(dev, "p"); i > 0 && allDigits(dev[i+1:]) && !allDigits(dev[:i]) {
		return dev[:i]
	}
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

// poolForDisk cruza un disco con los vdevs conocidos. Espejo del helper de
// httpapi, necesario aqui porque list_disks no debe ejecutar lsblk ni depender
// del paquete HTTP.
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
