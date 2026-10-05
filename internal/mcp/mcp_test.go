package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"easyzfs/internal/alerts"
	"easyzfs/internal/apikeys"
	"easyzfs/internal/db"
	"easyzfs/internal/hub"
	"easyzfs/internal/model"
	"easyzfs/internal/scheduler"
)

type fakePools struct {
	pools    []model.Pool
	datasets []model.Dataset
	groups   []model.SnapGroup
	history  map[string][]model.HistoryEntry
}

func (f fakePools) Pools() []model.Pool               { return f.pools }
func (f fakePools) Datasets() []model.Dataset         { return f.datasets }
func (f fakePools) SnapshotGroups() []model.SnapGroup { return f.groups }
func (f fakePools) History(name string) []model.HistoryEntry {
	return f.history[name]
}

type fakeDisks struct{ disks []model.Disk }

func (f fakeDisks) Disks() []model.Disk { return f.disks }

type fakePerf struct{ perf model.Performance }

func (f fakePerf) Performance() model.Performance { return f.perf }

type fakeCaps struct{ caps model.Capabilities }

func (f fakeCaps) Capabilities() model.Capabilities { return f.caps }

func testProviders() Deps {
	temp := 40.0
	return Deps{
		Pools: fakePools{
			pools: []model.Pool{
				{
					Name: "tank", Status: "ONLINE", Topo: "mirror",
					UsedBytes: 100, TotalBytes: 200,
					Scrub: model.ScrubInfo{State: "done", Kind: "scrub", Ts: time.Unix(1750000000, 0)},
					Vdevs: []model.Vdev{{Dev: "sdb", Path: "/dev/sdb1", Role: "mirror", Status: "ONLINE"}},
				},
				{
					Name: "backup", Status: "DEGRADED", Topo: "raidz1",
					UsedBytes: 10, TotalBytes: 50,
					Vdevs: []model.Vdev{{Dev: "sdc", Path: "/dev/sdc1", Role: "raidz1", Status: "DEGRADED"}},
				},
			},
			datasets: []model.Dataset{
				{Name: "tank/docs", Type: "fs", UsedBytes: 10, AvailBytes: 90},
				{Name: "tank/media", Type: "fs", UsedBytes: 20, AvailBytes: 80},
				{Name: "other/x", Type: "fs", UsedBytes: 1, AvailBytes: 9},
			},
			groups: []model.SnapGroup{
				{Dataset: "tank/docs", Snaps: []model.Snapshot{
					{Name: "snap-a", Full: "tank/docs@snap-a", Ts: time.Unix(1750000000, 0)},
					{Name: "snap-b", Full: "tank/docs@snap-b", Ts: time.Unix(1750000100, 0)},
				}},
				{Dataset: "other/x", Snaps: []model.Snapshot{
					{Name: "snap-x", Full: "other/x@snap-x", Ts: time.Unix(1750000200, 0)},
				}},
			},
			history: map[string][]model.HistoryEntry{
				"tank": {
					{Ts: time.Unix(1750000000, 0), Command: "zpool scrub tank"},
					{Ts: time.Unix(1749999000, 0), Command: "zfs snapshot tank/docs@snap-a"},
				},
			},
		},
		Disks: fakeDisks{disks: []model.Disk{
			{Dev: "sda", Model: "Disk A", SizeBytes: 200, Smart: "ok", Hours: 100},
			{Dev: "sdb", Model: "Disk B", SizeBytes: 200, TempC: &temp, Smart: "warn", SmartDetail: "reallocated", Hours: 200},
			{Dev: "nvme0n1", Model: "NVMe", SizeBytes: 100, Smart: "crit", SmartDetail: "media errors", Hours: 300},
		}},
		Perf: fakePerf{perf: model.Performance{
			Arc:   &model.ArcStats{SizeBytes: 1024, HitPct: 99},
			Pools: []model.PoolPerf{{Name: "tank", ReadBps: 10, WriteBps: 20}},
		}},
		Caps:    fakeCaps{caps: model.Capabilities{Version: "2.3.2", Rewrite: true, RaidzExpansion: true}},
		Version: "0.0.0-test", Build: "testbuild", ZFSVersion: "2.3.2",
	}
}

// testEnv levanta el handler MCP completo (auth + rate limit) sobre una BD real
// temporal con una API key valida. Devuelve la URL base y la clave cruda.
func testEnv(t *testing.T, mutate func(Deps, *alerts.Alerter), ratePerMin int) (string, string) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	if err := db.Migrate(context.Background(), d); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	keys := apikeys.NewStore(d)
	raw, err := keys.Create(context.Background(), "mcp-test")
	if err != nil {
		t.Fatalf("api key create: %v", err)
	}
	deps := testProviders()
	deps.RatePerMin = ratePerMin
	al := alerts.New(d, hub.NewHub(), nil)
	deps.Alerter = al
	deps.Jobs = scheduler.NewStore(d)
	if mutate != nil {
		mutate(deps, al)
	}
	srv := New(deps)
	mux := http.NewServeMux()
	mux.Handle("/mcp", srv.Handler(keys))
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts.URL, raw
}

// rpc hace una llamada JSON-RPC 2.0 contra /mcp y devuelve el body parseado.
func rpc(t *testing.T, url, token, method string, id int, params any) (int, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": id, "method": method, "params": params,
	})
	req, err := http.NewRequest("POST", url+"/mcp", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("Decode (status %d): %v", resp.StatusCode, err)
	}
	return resp.StatusCode, out
}

func callTool(t *testing.T, url, token, name string, args map[string]any) map[string]any {
	t.Helper()
	_, out := rpc(t, url, token, "tools/call", 2, map[string]any{
		"name":      name,
		"arguments": args,
	})
	return out
}

// structured extrae el contenido JSON de un CallToolResult.
func structured(t *testing.T, out map[string]any) map[string]any {
	t.Helper()
	res, ok := out["result"].(map[string]any)
	if !ok {
		t.Fatalf("sin result en %v", out)
	}
	if res["isError"] == true {
		t.Fatalf("tool error: %v", res)
	}
	content, ok := res["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("sin content en %v", res)
	}
	first, ok := content[0].(map[string]any)
	if !ok {
		t.Fatalf("content[0] no es mapa: %v", content[0])
	}
	text, ok := first["text"].(string)
	if !ok {
		t.Fatalf("content[0].text no es string: %v", first)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("content[0].text no es JSON (%q): %v", text, err)
	}
	return v
}

func TestServerStatus(t *testing.T) {
	url, tok := testEnv(t, nil, 60)

	v := structured(t, callTool(t, url, tok, "server_status", nil))
	if v["name"] != "EasyZFS" || v["version"] != "0.0.0-test" || v["build"] != "testbuild" {
		t.Errorf("identidad = %v", v)
	}
	if v["pools_total"] != float64(2) || v["pools_online"] != float64(1) || v["pools_degraded"] != float64(1) {
		t.Errorf("pools = %v", v)
	}
	if v["cap_used_bytes"] != float64(110) || v["cap_total_bytes"] != float64(250) {
		t.Errorf("capacity = %v", v)
	}
	if v["datasets_total"] != float64(3) || v["snapshots_total"] != float64(3) {
		t.Errorf("datasets/snapshots = %v", v)
	}
	if v["disks_total"] != float64(3) || v["disks_smart_ok"] != float64(1) ||
		v["disks_smart_warn"] != float64(1) || v["disks_smart_crit"] != float64(1) {
		t.Errorf("disks = %v", v)
	}
	arc, ok := v["arc"].(map[string]any)
	if !ok || arc["hit_pct"] != float64(99) {
		t.Errorf("arc = %v", v["arc"])
	}
	caps, ok := v["capabilities"].(map[string]any)
	if !ok || caps["version"] != "2.3.2" || caps["rewrite"] != true {
		t.Errorf("capabilities = %v", v["capabilities"])
	}
	if _, leaked := v["db_path"]; leaked {
		t.Errorf("server_status no debe exponer db_path: %v", v)
	}
}

func TestToolsListAllReadOnly(t *testing.T) {
	url, tok := testEnv(t, nil, 60)

	_, out := rpc(t, url, tok, "tools/list", 1, nil)
	res, ok := out["result"].(map[string]any)
	if !ok {
		t.Fatalf("sin result: %v", out)
	}
	tools, ok := res["tools"].([]any)
	if !ok || len(tools) != 7 {
		t.Fatalf("tools = %v", res["tools"])
	}
	want := map[string]bool{
		"server_status": true, "list_pools": true, "pool_health": true,
		"list_datasets": true, "list_snapshots": true, "list_disks": true,
		"alerts": true,
	}
	for _, raw := range tools {
		tool := raw.(map[string]any)
		name := tool["name"].(string)
		if !want[name] {
			t.Errorf("tool inesperada: %s", name)
		}
		delete(want, name)
		ann, ok := tool["annotations"].(map[string]any)
		if !ok {
			t.Errorf("%s sin annotations", name)
			continue
		}
		if ann["readOnlyHint"] != true {
			t.Errorf("%s readOnlyHint != true: %v", name, ann)
		}
		for _, forbidden := range []string{"destructiveHint", "openWorldHint"} {
			if ann[forbidden] == true {
				t.Errorf("%s no debe declarar %s=true", name, forbidden)
			}
		}
	}
	if len(want) > 0 {
		t.Errorf("tools sin registrar: %v", want)
	}
}

func TestListPoolsAndPoolHealth(t *testing.T) {
	url, tok := testEnv(t, nil, 60)

	v := structured(t, callTool(t, url, tok, "list_pools", nil))
	if v["total"] != float64(2) {
		t.Errorf("total = %v", v)
	}
	pools := v["pools"].([]any)
	tank := pools[0].(map[string]any)
	if tank["name"] != "tank" || tank["status"] != "ONLINE" {
		t.Errorf("tank = %v", tank)
	}
	vdevs := tank["vdevs"].([]any)
	if vdevs[0].(map[string]any)["temp_c"] != float64(40) {
		t.Errorf("temperatura cruzada = %v", vdevs[0])
	}

	h := structured(t, callTool(t, url, tok, "pool_health", map[string]any{"pool_name": "tank"}))
	pool := h["pool"].(map[string]any)
	if pool["name"] != "tank" || pool["topo"] != "mirror" {
		t.Errorf("pool = %v", pool)
	}
	history := h["history"].([]any)
	if len(history) != 2 {
		t.Errorf("history = %v", history)
	}

	out := callTool(t, url, tok, "pool_health", map[string]any{"pool_name": "missing"})
	res := out["result"].(map[string]any)
	if res["isError"] != true {
		t.Errorf("pool desconocido deberia ser isError: %v", out)
	}
}

func TestListDatasetsAndSnapshots(t *testing.T) {
	url, tok := testEnv(t, nil, 60)

	v := structured(t, callTool(t, url, tok, "list_datasets", map[string]any{"pool": "tank"}))
	if v["total"] != float64(2) {
		t.Errorf("datasets tank = %v", v)
	}
	v = structured(t, callTool(t, url, tok, "list_datasets", nil))
	if v["total"] != float64(3) {
		t.Errorf("datasets total = %v", v)
	}

	v = structured(t, callTool(t, url, tok, "list_snapshots", map[string]any{"dataset": "tank/docs"}))
	if v["total"] != float64(2) || len(v["groups"].([]any)) != 1 {
		t.Errorf("snapshots filtrados = %v", v)
	}
	v = structured(t, callTool(t, url, tok, "list_snapshots", nil))
	if v["total"] != float64(3) || len(v["groups"].([]any)) != 2 {
		t.Errorf("snapshots total = %v", v)
	}
}

func TestListDisks(t *testing.T) {
	url, tok := testEnv(t, nil, 60)

	v := structured(t, callTool(t, url, tok, "list_disks", nil))
	if v["total"] != float64(3) {
		t.Errorf("total = %v", v)
	}
	disks := v["disks"].([]any)
	byDev := map[string]map[string]any{}
	for _, raw := range disks {
		d := raw.(map[string]any)
		byDev[d["dev"].(string)] = d
	}
	if byDev["sdb"]["pool"] != "tank" {
		t.Errorf("sdb pool = %v", byDev["sdb"])
	}
	if byDev["nvme0n1"]["smart"] != "crit" {
		t.Errorf("nvme smart = %v", byDev["nvme0n1"])
	}
}

func TestAlertsActiveOnlyAndMissingPools(t *testing.T) {
	url, tok := testEnv(t, func(_ Deps, al *alerts.Alerter) {
		ctx := context.Background()
		al.RaiseKind(ctx, "crit", "pool.tank", "pools:tank", "Pool tank DEGRADED", "pool_status", map[string]any{"pool": "tank"})
		al.RaiseKind(ctx, "warn", "disk.sda", "disks:sda", "Disco sda caliente", "disk_temp", map[string]any{"dev": "sda"})
		list, err := al.List(ctx, 10)
		if err != nil || len(list) < 1 {
			t.Fatalf("seed alerts: %v %v", list, err)
		}
		if err := al.Ack(ctx, list[len(list)-1].ID); err != nil {
			t.Fatalf("ack: %v", err)
		}
	}, 60)

	v := structured(t, callTool(t, url, tok, "alerts", nil))
	if v["total"] != float64(2) || v["unread"] != float64(1) {
		t.Errorf("alerts = %v", v)
	}
	v = structured(t, callTool(t, url, tok, "alerts", map[string]any{"active_only": true}))
	if v["total"] != float64(1) {
		t.Errorf("active_only = %v", v)
	}
	if _, ok := v["missing_pools"].([]any); !ok {
		t.Errorf("missing_pools ausente: %v", v)
	}
}

func TestAuthBearerOnly(t *testing.T) {
	url, tok := testEnv(t, nil, 60)

	status, _ := rpc(t, url, "", "tools/list", 1, nil)
	if status != http.StatusUnauthorized {
		t.Errorf("sin token: status %d", status)
	}
	status, _ = rpc(t, url, "ez_bogus0000000000000000000000000000000000000000000000000000000000", "tools/list", 1, nil)
	if status != http.StatusUnauthorized {
		t.Errorf("token invalido: status %d", status)
	}
	status, _ = rpc(t, url, tok, "tools/list", 1, nil)
	if status != http.StatusOK {
		t.Errorf("token valido: status %d", status)
	}
}

func TestRateLimit(t *testing.T) {
	url, tok := testEnv(t, nil, 2)

	for i := 1; i <= 2; i++ {
		status, _ := rpc(t, url, tok, "tools/list", i, nil)
		if status != http.StatusOK {
			t.Fatalf("peticion %d: status %d", i, status)
		}
	}
	status, body := rpc(t, url, tok, "tools/list", 3, nil)
	if status != http.StatusTooManyRequests {
		t.Errorf("peticion 3: status %d body %v", status, body)
	}
}

func TestHandlerNilKeysFailClosed(t *testing.T) {
	srv := New(testProviders())
	ts := httptest.NewServer(srv.Handler(nil))
	t.Cleanup(ts.Close)

	status, _ := rpc(t, ts.URL, "", "tools/list", 1, nil)
	if status != http.StatusServiceUnavailable {
		t.Errorf("sin key store deberia ser 503 fail-closed, status %d", status)
	}
}

func Example() {
	srv := New(Deps{Version: "2.9.27"})
	_ = srv
	fmt.Println("ok")
	// Output: ok
}
