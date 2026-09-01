// zpool_test.go — parseo de status y resolucion de vdevs UUID→dispositivo.
package collectors

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"easyzfs/internal/hub"
	"easyzfs/internal/model"
)

func TestParseStatusJSONVdevs(t *testing.T) {
	out := []byte(`{"pools":{"tank":{"name":"tank","state":"DEGRADED","vdevs":{
		"tank":{"name":"tank","vdev_type":"root","state":"DEGRADED","vdevs":{
			"raidz1-0":{"name":"raidz1-0","vdev_type":"raidz1","state":"DEGRADED","vdevs":{
				"sdb1":{"name":"sdb1","vdev_type":"disk","state":"ONLINE"},
				"11111111-2222-3333-4444-555555555555":{"name":"11111111-2222-3333-4444-555555555555","vdev_type":"disk","state":"FAULTED"}
			}}
		}}
	}}}}`)
	p := &model.Pool{Name: "tank", Vdevs: []model.Vdev{}}
	c := &ZpoolCollector{lastPropsAt: map[string]time.Time{}}
	if !c.parseStatusJSON(out, p) {
		t.Fatal("parseStatusJSON devolvio false")
	}
	if len(p.Vdevs) != 2 {
		t.Fatalf("vdevs=%d, esperaba 2", len(p.Vdevs))
	}
	if p.Topo != "raidz1" {
		t.Fatalf("topo=%q, esperaba raidz1", p.Topo)
	}
	if p.Status != "DEGRADED" {
		t.Fatalf("status=%q, esperaba DEGRADED", p.Status)
	}
}

func TestResolveVdevPaths(t *testing.T) {
	c := &ZpoolCollector{lastPropsAt: map[string]time.Time{}}
	p := &model.Pool{Name: "tank", Vdevs: []model.Vdev{
		{Dev: "sdb1", Status: "ONLINE"},
		{Dev: "nvme0n1p2", Status: "ONLINE"},
		{Dev: "11111111-2222-3333-4444-555555555555", Status: "FAULTED"},
		{Dev: "nvme-ORICO_FAKE_123", Status: "ONLINE"},
	}}
	c.resolveVdevPaths(context.Background(), p)
	for _, v := range p.Vdevs {
		// Path es "" (no existe en este equipo) o una ruta real bajo /dev;
		// nunca una ruta inventada con el nombre crudo (uuid / by-id).
		if v.Path == "" {
			continue
		}
		if !strings.HasPrefix(v.Path, "/dev/") {
			t.Fatalf("path inesperado %q para %q", v.Path, v.Dev)
		}
		if strings.Contains(v.Path, "ORICO_FAKE") || reUUID.MatchString(v.Path[5:]) {
			t.Fatalf("path sin resolver: %q", v.Path)
		}
	}
}

func TestParseStatusJSONResilver(t *testing.T) {
	out := []byte(`{"pools":{"tank":{"name":"tank","state":"DEGRADED","vdevs":{
		"tank":{"name":"tank","vdev_type":"root","state":"DEGRADED","vdevs":{
			"raidz1-0":{"name":"raidz1-0","vdev_type":"raidz1","state":"DEGRADED","vdevs":{
				"sdb1":{"name":"sdb1","vdev_type":"disk","state":"ONLINE"}
			}}
		}}
	},
	"scan_stats":{"function":"RESILVER","state":"SCANNING","to_examine":"35.2T","examined":"3.52T","pass_start":"1785606676","errors":"0"}}}}`)
	p := &model.Pool{Name: "tank", Vdevs: []model.Vdev{}}
	c := &ZpoolCollector{lastPropsAt: map[string]time.Time{}}
	if !c.parseStatusJSON(out, p) {
		t.Fatal("parseStatusJSON devolvio false")
	}
	if p.Scrub.State != "running" || p.Scrub.Kind != "resilver" {
		t.Fatalf("scrub=%+v, esperaba running resilver", p.Scrub)
	}
	if p.Scrub.Pct < 9.9 || p.Scrub.Pct > 10.1 {
		t.Fatalf("pct=%v, esperaba ~10", p.Scrub.Pct)
	}
	if p.Scrub.EtaSec <= 0 {
		t.Fatalf("eta=%v, esperaba >0 (calculada por tasa)", p.Scrub.EtaSec)
	}
}

func TestParseHumanSize(t *testing.T) {
	cases := map[string]uint64{"35.2T": 38702809297715, "0B": 0, "748K": 765952, "34.0G": 36507222016}
	for in, want := range cases {
		got, ok := parseHumanSize(in)
		if !ok || got != want {
			t.Errorf("parseHumanSize(%q)=%d,%v esperaba %d", in, got, ok, want)
		}
	}
	if _, ok := parseHumanSize("-"); ok {
		t.Error("'-' no deberia parsear")
	}
}

func TestReUUID(t *testing.T) {
	if !reUUID.MatchString("11111111-2222-3333-4444-555555555555") {
		t.Fatal("UUID no reconocido")
	}
	for _, no := range []string{"sdb1", "ata-ST12000-part1", "gptid/abcd"} {
		if reUUID.MatchString(no) {
			t.Fatalf("falso positivo UUID: %q", no)
		}
	}
}

// fakePoolBin crea un zpool/zfs falso que cuenta invocaciones en un log.
func fakePoolBin(t *testing.T) (dir, logFile string) {
	t.Helper()
	dir = t.TempDir()
	logFile = filepath.Join(dir, "calls.log")

	// zpool falso: anota los args y sale 0.
	zpool := "#!/bin/sh\necho \"$@\" >> " + logFile + "\nexit 0\n"
	// zfs falso: anota los args y sale 0.
	zfs := "#!/bin/sh\necho \"$@\" >> " + logFile + "\nexit 0\n"
	// sudo falso: executil antepone 'sudo -n'; lo ignoramos.
	sudo := "#!/bin/sh\nwhile [ $# -gt 0 ]; do case \"$1\" in -*) shift;; *) break;; esac; done\nexec \"$@\"\n"
	for name, body := range map[string]string{"zpool": zpool, "zfs": zfs, "sudo": sudo} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir, logFile
}

func readCalls(t *testing.T, logFile string) []string {
	t.Helper()
	b, err := os.ReadFile(logFile)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func TestFillPoolPropsTTL(t *testing.T) {
	_, logFile := fakePoolBin(t)
	c := &ZpoolCollector{lastPropsAt: map[string]time.Time{}}
	p := &model.Pool{Name: "tank"}
	now := time.Now()

	c.fillPoolProps(context.Background(), p, now)
	if len(readCalls(t, logFile)) != 1 {
		t.Fatalf("esperaba 1 llamada, hay %d", len(readCalls(t, logFile)))
	}

	// Segunda llamada inmediata: no deberia ejecutar zpool por TTL.
	c.fillPoolProps(context.Background(), p, now)
	if calls := readCalls(t, logFile); len(calls) != 1 {
		t.Fatalf("esperaba 1 llamada tras TTL, hay %d", len(calls))
	}

	// Tras TTL: deberia volver a llamar.
	c.lastPropsAt["props:tank"] = now.Add(-propTTL - time.Second)
	c.fillPoolProps(context.Background(), p, now.Add(propTTL+time.Second))
	if calls := readCalls(t, logFile); len(calls) != 2 {
		t.Fatalf("esperaba 2 llamadas tras expirar TTL, hay %d", len(calls))
	}
}

func TestFillCompressRatioTTL(t *testing.T) {
	_, logFile := fakePoolBin(t)
	c := &ZpoolCollector{lastPropsAt: map[string]time.Time{}}
	p := &model.Pool{Name: "tank"}
	now := time.Now()

	c.fillCompressRatio(context.Background(), p, now)
	if len(readCalls(t, logFile)) != 1 {
		t.Fatalf("esperaba 1 llamada, hay %d", len(readCalls(t, logFile)))
	}

	c.fillCompressRatio(context.Background(), p, now)
	if calls := readCalls(t, logFile); len(calls) != 1 {
		t.Fatalf("esperaba 1 llamada tras TTL, hay %d", len(calls))
	}

	c.lastPropsAt["compress:tank"] = now.Add(-propTTL - time.Second)
	c.fillCompressRatio(context.Background(), p, now.Add(propTTL+time.Second))
	if calls := readCalls(t, logFile); len(calls) != 2 {
		t.Fatalf("esperaba 2 llamadas tras expirar TTL, hay %d", len(calls))
	}
}

func TestFillTrimTTL(t *testing.T) {
	_, logFile := fakePoolBin(t)
	c := &ZpoolCollector{lastPropsAt: map[string]time.Time{}}
	p := &model.Pool{Name: "ssd"}
	now := time.Now()

	c.fillTrim(context.Background(), p, now)
	if len(readCalls(t, logFile)) != 1 {
		t.Fatalf("esperaba 1 llamada, hay %d", len(readCalls(t, logFile)))
	}

	c.fillTrim(context.Background(), p, now)
	if calls := readCalls(t, logFile); len(calls) != 1 {
		t.Fatalf("esperaba 1 llamada tras TTL, hay %d", len(calls))
	}

	c.lastPropsAt["trim:ssd"] = now.Add(-trimTTL - time.Second)
	c.fillTrim(context.Background(), p, now.Add(trimTTL+time.Second))
	if calls := readCalls(t, logFile); len(calls) != 2 {
		t.Fatalf("esperaba 2 llamadas tras expirar TTL, hay %d", len(calls))
	}
}

// fakePoolWithData crea zpool/zfs falsos que devuelven un pool simple para
// poder probar lightCollect/fullCollect sin depender del host real.
func fakePoolWithData(t *testing.T) (dir, logFile string) {
	t.Helper()
	dir = t.TempDir()
	logFile = filepath.Join(dir, "calls.log")

	zpool := `#!/bin/sh
echo "$@" >> ` + logFile + `
case "$1" in
list)
  printf 'tank\t123456789\t12345678\t0\tONLINE\n'
  ;;
status)
  printf '{"pools":{"tank":{"name":"tank","state":"ONLINE","vdevs":{"tank":{"name":"tank","vdev_type":"root","state":"ONLINE","vdevs":{}}}}}}'
  ;;
esac
exit 0
`
	zfs := `#!/bin/sh
echo "$@" >> ` + logFile + `
exit 0
`
	sudo := `#!/bin/sh
while [ $# -gt 0 ]; do case "$1" in -*) shift;; *) break;; esac; done
exec "$@"
`
	for name, body := range map[string]string{"zpool": zpool, "zfs": zfs, "sudo": sudo} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir, logFile
}

func TestLightCollectFewerCommandsThanFull(t *testing.T) {
	_, logFile := fakePoolWithData(t)

	c := NewZpoolCollector(nil, hub.NewHub(), nil, 0, 0, 0, func() int { return 0 })
	ctx := context.Background()

	if err := c.lightCollect(ctx); err != nil {
		t.Fatalf("lightCollect: %v", err)
	}
	lightCalls := len(readCalls(t, logFile))
	if lightCalls == 0 {
		t.Fatal("lightCollect no genero llamadas")
	}

	if err := c.fullCollect(ctx); err != nil {
		t.Fatalf("fullCollect: %v", err)
	}
	fullCalls := len(readCalls(t, logFile))

	if fullCalls <= lightCalls {
		t.Fatalf("fullCollect deberia generar mas llamadas que lightCollect (full=%d light=%d)", fullCalls, lightCalls)
	}
}

func TestNextInterval(t *testing.T) {
	c := NewZpoolCollector(nil, nil, nil,
		100*time.Millisecond, 200*time.Millisecond, 5*time.Second,
		func() int { return 0 })

	if got := c.nextInterval(true); got != 100*time.Millisecond {
		t.Fatalf("active: esperaba 100ms, got %v", got)
	}
	if got := c.nextInterval(false); got != 200*time.Millisecond {
		t.Fatalf("idle: esperaba 200ms, got %v", got)
	}
}
