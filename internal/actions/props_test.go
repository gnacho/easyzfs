// props_test.go — whitelist de propiedades (U3): validadores, get/set/inherit
// con un fake zfs que emite fixtures y registra sus argumentos.
package actions

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newPropsTestService — como newTestService pero con un fake `zfs` en el PATH
// que: en "get" emite el fixture de propiedades, y en "set"/"inherit" anota
// los args (tras quitar flags tipo -H/-o). Cada invocación de set/inherit
// además se registra en el mismo log para verificar audit y ejecución.
func newPropsTestService(t *testing.T) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	logFile := filepath.Join(dir, "zfs-args.log")

	// zfs falso: get → emite el fixture; set/inherit → anota y sale 0.
	zfs := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  get) cat <<'EOF'\n" + fixtureProps + "EOF\n exit 0 ;;\n" +
		"  set|inherit) shift 1 ; echo \"$@\" >> \"$ZFS_LOG\" ; exit 0 ;;\n" +
		"  *) exit 1 ;;\n" +
		"esac\n"
	sudo := "#!/bin/sh\nwhile [ $# -gt 0 ]; do case \"$1\" in -*) shift;; *) break;; esac; done\nexec \"$@\"\n"
	for name, body := range map[string]string{"zfs": zfs, "sudo": sudo} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ZFS_LOG", logFile)

	svc, _ := newTestService(t)
	return svc, logFile
}

// fixtureProps — propiedades típicas de 'zfs get -H -o name,property,value,source all tank/docs'.
const fixtureProps = "tank/docs\tcompression\tlz4\tlocal\n" +
	"tank/docs\trecordsize\t128K\tlocal\n" +
	"tank/docs\tatime\ton\tdefault\n" +
	"tank/docs\tquota\tnone\tdefault\n" +
	"tank/docs\tmountpoint\t/mnt/docs\tlocal\n" +
	"tank/docs\texec\ton\tdefault\n" +
	"tank/docs\tencryption\toff\tdefault\n" +
	"tank/docs\tused\t1610612736\t-\n" +
	"tank/docs\tuser:backup\ttrue\tlocal\n"

func TestPropsGet(t *testing.T) {
	svc, _ := newPropsTestService(t)

	props, err := svc.DatasetPropsGet(context.Background(), "tank/docs")
	if err != nil {
		t.Fatalf("DatasetPropsGet: %v", err)
	}
	if len(props) != 9 {
		t.Fatalf("len(props) = %d, esperaba 9", len(props))
	}
	if props[0].Name != "compression" || props[0].Value != "lz4" || props[0].Source != "local" {
		t.Fatalf("props[0] = %+v", props[0])
	}
	// Orden preservado del fixture.
	if props[8].Name != "user:backup" {
		t.Fatalf("user prop no está al final: %s", props[8].Name)
	}
}

func TestPropsGetNombreInvalido(t *testing.T) {
	svc, _ := newPropsTestService(t)
	_, err := svc.DatasetPropsGet(context.Background(), "tank@docs")
	if !errors.Is(err, ErrInvalidName) {
		t.Fatalf("err = %v, esperaba ErrInvalidName", err)
	}
}

func TestPropValidators(t *testing.T) {
	cases := []struct {
		prop, val string
		ok        bool
	}{
		{"compression", "lz4", true},
		{"compression", "zstd", true},
		{"compression", "none", false}, // none no es compresión válida
		{"atime", "on", true},
		{"atime", "off", true},
		{"atime", "1", false},
		{"recordsize", "128K", true},
		{"recordsize", "64K", true},
		{"recordsize", "100K", false}, // no potencia de 2
		{"recordsize", "16M", true},
		{"recordsize", "32M", false}, // > 16M
		{"recordsize", "1T", false},  // > 16M
		{"sync", "always", true},
		{"sync", "on", false},
		{"quota", "none", true},
		{"quota", "1T", true},
		{"quota", "1.5T", false}, // no numérico
		{"quota", "500G", true},
		{"mountpoint", "/tank/docs", true},
		{"mountpoint", "none", true},
		{"mountpoint", "legacy", true},
		{"mountpoint", "../etc/passwd", false},
		{"mountpoint", "/tmp/x; rm -rf /", false},
		{"volblocksize", "512", true},
		{"volblocksize", "128K", true},
		{"volblocksize", "64M", false}, // > 128K
		{"copies", "2", true},
		{"copies", "4", false},
	}
	for _, c := range cases {
		spec, ok := propValidators[c.prop]
		if !ok {
			t.Fatalf("propiedad %s no está en la whitelist", c.prop)
		}
		if got := spec.valid(c.val); got != c.ok {
			t.Errorf("propValidators[%s].valid(%q) = %v, esperaba %v", c.prop, c.val, got, c.ok)
		}
	}
}

func TestPropAppliesTo(t *testing.T) {
	if propValidators["mountpoint"].appliesTo("volume") {
		t.Error("mountpoint no debería aplicar a volume")
	}
	if !propValidators["mountpoint"].appliesTo("fs") {
		t.Error("mountpoint debería aplicar a fs")
	}
	if propValidators["volsize"].appliesTo("fs") {
		t.Error("volsize no debería aplicar a fs")
	}
	if !propValidators["volsize"].appliesTo("volume") {
		t.Error("volsize debería aplicar a volume")
	}
	if !propValidators["atime"].appliesTo("fs") || !propValidators["atime"].appliesTo("volume") {
		t.Error("atime debería aplicar a ambos tipos")
	}
}

func TestPropSetValido(t *testing.T) {
	svc, logFile := newPropsTestService(t)

	if err := svc.DatasetPropSet(context.Background(), "tester", "tank/docs", "recordsize", "64K", "fs"); err != nil {
		t.Fatalf("DatasetPropSet: %v", err)
	}
	out, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("el fake zfs no registró la llamada: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "recordsize=64K tank/docs" {
		t.Fatalf("args de zfs = %q, esperaba %q", got, "recordsize=64K tank/docs")
	}
}

func TestPropSetInvalido(t *testing.T) {
	svc, logFile := newPropsTestService(t)

	// Valor no válido → no debe llegar a zfs.
	if err := svc.DatasetPropSet(context.Background(), "tester", "tank/docs", "recordsize", "100K", "fs"); err == nil {
		t.Fatal("set de recordsize=100K debería fallar")
	}
	// Propiedad fuera de whitelist.
	if err := svc.DatasetPropSet(context.Background(), "tester", "tank/docs", "dedup", "on", "fs"); err == nil {
		t.Fatal("set de dedup debería fallar (fuera de whitelist)")
	}
	// Propiedad no aplicable al tipo.
	if err := svc.DatasetPropSet(context.Background(), "tester", "tank/docs", "mountpoint", "/x", "volume"); err == nil {
		t.Fatal("set de mountpoint en volume debería fallar")
	}
	if b, _ := os.ReadFile(logFile); len(b) != 0 {
		t.Fatalf("zfs no debería haberse llamado para sets inválidos: %q", b)
	}
}

func TestPropInherit(t *testing.T) {
	svc, logFile := newPropsTestService(t)

	if err := svc.DatasetPropInherit(context.Background(), "tester", "tank/docs", "recordsize"); err != nil {
		t.Fatalf("DatasetPropInherit: %v", err)
	}
	out, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("el fake zfs no registró la llamada: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "recordsize tank/docs" {
		t.Fatalf("args de zfs = %q, esperaba %q", got, "recordsize tank/docs")
	}
}

func TestPropInheritInvalida(t *testing.T) {
	svc, logFile := newPropsTestService(t)
	if err := svc.DatasetPropInherit(context.Background(), "tester", "tank/docs", "dedup"); err == nil {
		t.Fatal("inherit de dedup debería fallar (fuera de whitelist)")
	}
	if b, _ := os.ReadFile(logFile); len(b) != 0 {
		t.Fatalf("zfs no debería haberse llamado: %q", b)
	}
}
