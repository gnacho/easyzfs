// actions_test.go — acciones sobre pools con binarios falsos en PATH
// (fake zpool registra sus argumentos; fake sudo los pasa tal cual).
package actions

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"easyzfs/internal/db"
)

// newTestService crea el servicio con SQLite temporal (audit_log real) y un
// directorio de binarios falsos al frente del PATH. Devuelve el servicio y la
// ruta del log donde el fake zpool anota cada invocación.
func newTestService(t *testing.T) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	logFile := filepath.Join(dir, "zpool-args.log")

	// zpool falso: anota los args y sale 0.
	zpool := "#!/bin/sh\necho \"$@\" >> " + logFile + "\nexit 0\n"
	// sudo falso: executil antepone 'sudo -n' cuando no somos root; lo ignora.
	sudo := "#!/bin/sh\nwhile [ $# -gt 0 ]; do case \"$1\" in -*) shift;; *) break;; esac; done\nexec \"$@\"\n"
	// dd falso: anota los args y sale 0 (IdentifyDisk).
	dd := "#!/bin/sh\necho \"$@\" >> " + logFile + "\nexit 0\n"
	for name, body := range map[string]string{"zpool": zpool, "sudo": sudo, "dd": dd} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	d, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if err := db.Migrate(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	return NewService(d), logFile
}

func TestTrim(t *testing.T) {
	svc, logFile := newTestService(t)

	if err := svc.Trim(context.Background(), "tester", "tank"); err != nil {
		t.Fatalf("Trim: %v", err)
	}
	out, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("el fake zpool no registró la llamada: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "trim tank" {
		t.Fatalf("args de zpool = %q, esperaba %q", got, "trim tank")
	}

	// Auditoría: acción pool.trim con actor, sin confirm (no destructiva).
	var action, actor string
	var confirmed int
	err = svc.db.QueryRow(
		"SELECT action, actor, confirmed FROM audit_log WHERE target='tank'").Scan(&action, &actor, &confirmed)
	if err != nil {
		t.Fatalf("audit_log: %v", err)
	}
	if action != "pool.trim" || actor != "tester" || confirmed != 0 {
		t.Fatalf("audit = (%q,%q,%d), esperaba (pool.trim,tester,0)", action, actor, confirmed)
	}
}

func TestTrimNombreInvalido(t *testing.T) {
	svc, _ := newTestService(t)
	for _, bad := range []string{"", "tan k", "tank;rm -rf /", "../etc", strings.Repeat("a", 65)} {
		if err := svc.Trim(context.Background(), "tester", bad); !errors.Is(err, ErrInvalidName) {
			t.Errorf("Trim(%q) = %v, esperaba ErrInvalidName", bad, err)
		}
	}
}

func TestPoolCreateAshift(t *testing.T) {
	svc, logFile := newTestService(t)
	if err := svc.PoolCreate(context.Background(), "tester", "tank", "mirror",
		[]string{"sda", "sdb"}, 12, true); err != nil {
		t.Fatalf("PoolCreate: %v", err)
	}
	out, _ := os.ReadFile(logFile)
	if got := strings.TrimSpace(string(out)); got != "create -o ashift=12 tank mirror sda sdb" {
		t.Fatalf("argv zpool = %q, esperaba %q", got, "create -o ashift=12 tank mirror sda sdb")
	}
	var action string
	var confirmed int
	err := svc.db.QueryRow(
		"SELECT action, confirmed FROM audit_log WHERE target='tank'").Scan(&action, &confirmed)
	if err != nil {
		t.Fatalf("audit_log: %v", err)
	}
	if action != "pool.create" || confirmed != 1 {
		t.Fatalf("audit = (%q,%d), esperaba (pool.create,1)", action, confirmed)
	}
}

func TestPoolCreateAshiftAuto(t *testing.T) {
	svc, logFile := newTestService(t)
	if err := svc.PoolCreate(context.Background(), "tester", "tank", "mirror",
		[]string{"sda", "sdb"}, 0, true); err != nil {
		t.Fatalf("PoolCreate: %v", err)
	}
	out, _ := os.ReadFile(logFile)
	if got := strings.TrimSpace(string(out)); got != "create tank mirror sda sdb" {
		t.Fatalf("argv zpool = %q, esperaba %q", got, "create tank mirror sda sdb")
	}
}

func TestPoolCreateAshiftInvalido(t *testing.T) {
	svc, _ := newTestService(t)
	if err := svc.PoolCreate(context.Background(), "tester", "tank", "mirror",
		[]string{"sda", "sdb"}, 5, true); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("ashift=5 = %v, esperaba ErrInvalidInput", err)
	}
	if err := svc.PoolCreate(context.Background(), "tester", "tank", "mirror",
		[]string{"sda", "sdb"}, 17, true); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("ashift=17 = %v, esperaba ErrInvalidInput", err)
	}
}

func TestIdentifyDisk(t *testing.T) {
	svc, logFile := newTestService(t)

	if err := svc.IdentifyDisk(context.Background(), "tester", "sda"); err != nil {
		t.Fatalf("IdentifyDisk: %v", err)
	}
	out, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("el fake dd no registró la llamada: %v", err)
	}
	want := "if=/dev/sda of=/dev/null bs=1M count=2048"
	if got := strings.TrimSpace(string(out)); got != want {
		t.Fatalf("argv de dd = %q, esperaba %q", got, want)
	}

	// Auditoría: acción disk.identify, sin confirm (no destructiva).
	var action, actor string
	var confirmed int
	err = svc.db.QueryRow(
		"SELECT action, actor, confirmed FROM audit_log WHERE target='sda'").Scan(&action, &actor, &confirmed)
	if err != nil {
		t.Fatalf("audit_log: %v", err)
	}
	if action != "disk.identify" || actor != "tester" || confirmed != 0 {
		t.Fatalf("audit = (%q,%q,%d), esperaba (disk.identify,tester,0)", action, actor, confirmed)
	}
}

func TestIdentifyDiskDevInvalido(t *testing.T) {
	svc, _ := newTestService(t)
	for _, bad := range []string{"", "/dev/sda", "sda;rm", "s d", "../sda", "sda/part1"} {
		if err := svc.IdentifyDisk(context.Background(), "tester", bad); !errors.Is(err, ErrInvalidDev) {
			t.Errorf("IdentifyDisk(%q) = %v, esperaba ErrInvalidDev", bad, err)
		}
	}
}
