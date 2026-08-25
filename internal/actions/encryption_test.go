// encryption_test.go — cifrado nativo por dataset (lote D): create cifrado,
// load/unload/change-key con fake zfs que registra argv y stdin por separado.
// Regla de oro verificada aquí: la passphrase viaja SOLO por stdin, JAMÁS en
// argv (visible en ps) ni en audit_log.
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

// newKeyTestService — como newTestService pero el fake 'zfs' anota argv en
// zfs-args.log y lo que reciba por stdin en zfs-stdin.log.
func newKeyTestService(t *testing.T) (*Service, string, string) {
	t.Helper()
	dir := t.TempDir()
	argsLog := filepath.Join(dir, "zfs-args.log")
	stdinLog := filepath.Join(dir, "zfs-stdin.log")

	zfs := "#!/bin/sh\necho \"$@\" >> " + argsLog + "\ncat >> " + stdinLog + "\nexit 0\n"
	zpool := "#!/bin/sh\nexit 0\n"
	sudo := "#!/bin/sh\nwhile [ $# -gt 0 ]; do case \"$1\" in -*) shift;; *) break;; esac; done\nexec \"$@\"\n"
	for name, body := range map[string]string{"zfs": zfs, "zpool": zpool, "sudo": sudo} {
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
	return NewService(d), argsLog, stdinLog
}

const testPass = "cl4ve-sup3r-s3creta"

func TestDatasetCreateCifrado(t *testing.T) {
	svc, argsLog, stdinLog := newKeyTestService(t)

	err := svc.DatasetCreate(context.Background(), "tester", "tank", "secretos",
		"fs", "lz4", 0, 0, true, testPass, "")
	if err != nil {
		t.Fatalf("DatasetCreate cifrado: %v", err)
	}

	args, _ := os.ReadFile(argsLog)
	got := strings.TrimSpace(string(args))
	want := "create -p -o compression=lz4 -o encryption=aes-256-gcm -o keyformat=passphrase -o keylocation=prompt tank/secretos"
	if got != want {
		t.Fatalf("argv = %q, esperaba %q", got, want)
	}
	// La clave NUNCA en argv…
	if strings.Contains(got, testPass) {
		t.Fatalf("la passphrase aparece en argv: %q", got)
	}
	// …y SÍ por stdin (dos veces: verificación de zfs).
	stdin, _ := os.ReadFile(stdinLog)
	if got := string(stdin); got != testPass+"\n"+testPass+"\n" {
		t.Fatalf("stdin = %q, esperaba la passphrase dos veces", got)
	}
	// Audit: registra encrypted=true pero NUNCA la clave.
	var detail string
	if err := svc.db.QueryRow(
		"SELECT detail FROM audit_log WHERE action='dataset.create' AND target='tank/secretos'").Scan(&detail); err != nil {
		t.Fatalf("audit_log: %v", err)
	}
	if !strings.Contains(detail, `"encrypted":true`) {
		t.Errorf("audit detail sin encrypted: %s", detail)
	}
	if strings.Contains(detail, testPass) {
		t.Errorf("la passphrase aparece en audit_log: %s", detail)
	}
}

func TestDatasetCreateSinCifrarNoUsaStdin(t *testing.T) {
	svc, argsLog, stdinLog := newKeyTestService(t)
	if err := svc.DatasetCreate(context.Background(), "tester", "tank", "docs",
		"fs", "zstd", 0, 0, false, "", ""); err != nil {
		t.Fatalf("DatasetCreate: %v", err)
	}
	args, _ := os.ReadFile(argsLog)
	if strings.Contains(string(args), "encryption") {
		t.Errorf("argv con encryption sin pedirlo: %q", args)
	}
	stdin, _ := os.ReadFile(stdinLog)
	if len(stdin) != 0 {
		t.Errorf("stdin no vacío sin cifrado: %q", stdin)
	}
}

func TestDatasetCreatePassphraseCorta(t *testing.T) {
	svc, _, _ := newKeyTestService(t)
	err := svc.DatasetCreate(context.Background(), "tester", "tank", "x",
		"fs", "lz4", 0, 0, true, "corta", "")
	if err == nil {
		t.Fatal("passphrase <8 aceptada")
	}
}

func TestDatasetLoadKey(t *testing.T) {
	svc, argsLog, stdinLog := newKeyTestService(t)
	if err := svc.DatasetLoadKey(context.Background(), "tester", "tank/boveda", testPass); err != nil {
		t.Fatalf("DatasetLoadKey: %v", err)
	}
	args, _ := os.ReadFile(argsLog)
	if got := strings.TrimSpace(string(args)); got != "load-key tank/boveda" {
		t.Fatalf("argv = %q, esperaba 'load-key tank/boveda'", got)
	}
	if strings.Contains(string(args), testPass) {
		t.Fatal("la passphrase aparece en argv")
	}
	stdin, _ := os.ReadFile(stdinLog)
	if got := string(stdin); got != testPass+"\n" {
		t.Fatalf("stdin = %q, esperaba la passphrase", got)
	}
	// audit sin la clave
	var detail string
	if err := svc.db.QueryRow(
		"SELECT detail FROM audit_log WHERE action='dataset.unlock'").Scan(&detail); err != nil {
		t.Fatalf("audit_log: %v", err)
	}
	if strings.Contains(detail, testPass) {
		t.Errorf("la passphrase aparece en audit_log: %s", detail)
	}
}

func TestDatasetUnloadKey(t *testing.T) {
	svc, argsLog, stdinLog := newKeyTestService(t)
	if err := svc.DatasetUnloadKey(context.Background(), "tester", "tank/secretos"); err != nil {
		t.Fatalf("DatasetUnloadKey: %v", err)
	}
	args, _ := os.ReadFile(argsLog)
	// Sin -f por defecto (decisión: el error de zfs se muestra, no se fuerza).
	if got := strings.TrimSpace(string(args)); got != "unload-key tank/secretos" {
		t.Fatalf("argv = %q, esperaba 'unload-key tank/secretos'", got)
	}
	stdin, _ := os.ReadFile(stdinLog)
	if len(stdin) != 0 {
		t.Errorf("stdin no vacío en unload-key: %q", stdin)
	}
}

func TestDatasetChangeKey(t *testing.T) {
	svc, argsLog, stdinLog := newKeyTestService(t)
	nueva := "nu3va-cl4ve-larga"
	if err := svc.DatasetChangeKey(context.Background(), "tester", "tank/secretos", nueva); err != nil {
		t.Fatalf("DatasetChangeKey: %v", err)
	}
	args, _ := os.ReadFile(argsLog)
	got := strings.TrimSpace(string(args))
	if got != "change-key -o keyformat=passphrase tank/secretos" {
		t.Fatalf("argv = %q", got)
	}
	if strings.Contains(got, nueva) {
		t.Fatal("la passphrase nueva aparece en argv")
	}
	stdin, _ := os.ReadFile(stdinLog)
	if s := string(stdin); s != nueva+"\n"+nueva+"\n" {
		t.Fatalf("stdin = %q, esperaba la nueva clave dos veces", s)
	}
	var detail string
	if err := svc.db.QueryRow(
		"SELECT detail FROM audit_log WHERE action='dataset.change_key'").Scan(&detail); err != nil {
		t.Fatalf("audit_log: %v", err)
	}
	if strings.Contains(detail, nueva) {
		t.Errorf("la passphrase aparece en audit_log: %s", detail)
	}
}

func TestPoolExpand(t *testing.T) {
	svc, logFile := newTestService(t)
	if err := svc.PoolExpand(context.Background(), "tester", "tank", "raidz2-0", "sde", true); err != nil {
		t.Fatalf("PoolExpand: %v", err)
	}
	out, _ := os.ReadFile(logFile)
	if got := strings.TrimSpace(string(out)); got != "attach tank raidz2-0 sde" {
		t.Fatalf("argv = %q, esperaba 'attach tank raidz2-0 sde'", got)
	}
	// audit pool.expand con confirmed=1
	var confirmed int
	if err := svc.db.QueryRow(
		"SELECT confirmed FROM audit_log WHERE action='pool.expand' AND target='tank'").Scan(&confirmed); err != nil {
		t.Fatalf("audit_log: %v", err)
	}
	if confirmed != 1 {
		t.Errorf("confirmed=%d, esperaba 1", confirmed)
	}
	// vdev no raidz → inválido
	for _, bad := range []string{"mirror-0", "sdb", "raidz4-0", "raidz2", "raidz2-0;rm"} {
		if err := svc.PoolExpand(context.Background(), "tester", "tank", bad, "sde", true); err == nil {
			t.Errorf("PoolExpand vdev=%q aceptado", bad)
		}
	}
}

func TestDatasetCreateAtime(t *testing.T) {
	svc, argsLog, _ := newKeyTestService(t)

	if err := svc.DatasetCreate(context.Background(), "tester", "tank", "docs",
		"fs", "lz4", 0, 0, false, "", "relatime"); err != nil {
		t.Fatalf("DatasetCreate con atime: %v", err)
	}
	args, _ := os.ReadFile(argsLog)
	if got := strings.TrimSpace(string(args)); got != "create -p -o compression=lz4 -o atime=relatime tank/docs" {
		t.Fatalf("argv = %q, esperaba %q", got, "create -p -o compression=lz4 -o atime=relatime tank/docs")
	}
}

func TestDatasetCreateAtimeVacioNoTocaAtime(t *testing.T) {
	svc, argsLog, _ := newKeyTestService(t)

	if err := svc.DatasetCreate(context.Background(), "tester", "tank", "docs",
		"fs", "lz4", 0, 0, false, "", ""); err != nil {
		t.Fatalf("DatasetCreate: %v", err)
	}
	args, _ := os.ReadFile(argsLog)
	if got := strings.TrimSpace(string(args)); got != "create -p -o compression=lz4 tank/docs" {
		t.Fatalf("argv = %q, esperaba %q", got, "create -p -o compression=lz4 tank/docs")
	}
}

func TestDatasetCreateAtimeInvalido(t *testing.T) {
	svc, _, _ := newKeyTestService(t)
	if err := svc.DatasetCreate(context.Background(), "tester", "tank", "docs",
		"fs", "lz4", 0, 0, false, "", "atime=on;rm -rf"); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("atime inválido = %v, esperaba ErrInvalidInput", err)
	}
}
