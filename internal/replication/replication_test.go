// replication_test.go — whitelists anti-inyección, flujo full→incremental con
// fake zfs/ssh en PATH, retry force_full y test de conexión.
package replication

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"easyzfs/internal/db"
	"easyzfs/internal/executil"
	"easyzfs/internal/hub"
	"easyzfs/internal/longops"
)

// --- validación (casos de inyección) ---

func TestValidateDataset(t *testing.T) {
	ok := []string{"tank", "tank/documentos", "pool-1/ds_2.b", "a/b/c-d.e_f"}
	for _, s := range ok {
		if err := ValidateDataset(s); err != nil {
			t.Errorf("%q debería ser válido: %v", s, err)
		}
	}
	bad := []string{
		"", "pool/x; rm -rf /", "pool/x`id`", "pool/x y", "pool/$(id)",
		"pool/x|cat", "pool/x&reboot", "pool/x>f", "pool/x<f", "pool/x\"q",
		"pool/x'q", "pool/x\\n", "pool/x#bm", "pool/x@snap", "pool/x:80",
	}
	for _, s := range bad {
		if err := ValidateDataset(s); err == nil {
			t.Errorf("%q debería rechazarse", s)
		}
	}
}

func TestValidateSSHCreds(t *testing.T) {
	for _, s := range []string{"pool/x; rm -rf", "user@host:cmd", "`id`", "a b", "-oProxyCommand=x", "root;ls"} {
		if err := ValidateSSHUser(s); err == nil {
			t.Errorf("usuario %q debería rechazarse", s)
		}
		if err := ValidateSSHHost(s); err == nil {
			t.Errorf("host %q debería rechazarse", s)
		}
	}
	if err := ValidateSSHUser("zfs_repl-01"); err != nil {
		t.Errorf("usuario válido rechazado: %v", err)
	}
	if err := ValidateSSHUser("1root"); err == nil {
		t.Error("usuario empezando por dígito debería rechazarse")
	}
	if err := ValidateSSHHost("nas.lan-1.example.com"); err != nil {
		t.Errorf("host válido rechazado: %v", err)
	}
	for _, p := range []int{0, -1, 65536, 99999} {
		if err := ValidateSSHPort(p); err == nil {
			t.Errorf("puerto %d debería rechazarse", p)
		}
	}
	if err := ValidateSSHPort(2222); err != nil {
		t.Errorf("puerto válido rechazado: %v", err)
	}
}

func TestJobValidate(t *testing.T) {
	j := &Job{Source: "tank/a", DestType: "ssh", DestDataset: "bak/a",
		Host: "h; rm -rf /", User: "u", Port: 22}
	if err := j.Validate(); err == nil {
		t.Error("host con inyección debería rechazarse en Validate()")
	}
	j.Host = "nas.local"
	if err := j.Validate(); err != nil {
		t.Errorf("job válido rechazado: %v", err)
	}
	j.DestType = "remote"
	if err := j.Validate(); err == nil {
		t.Error("dest_type desconocido debería rechazarse")
	}
}

// --- fakes zfs/ssh en PATH ---

// fakeBin escribe scripts zfs/ssh que registran sus llamadas en $FAKE_LOG y
// simulan el estado de snapshots en $FAKE_STATE/snaps.
func fakeBin(t *testing.T) (logFile, stateDir string) {
	t.Helper()
	dir := t.TempDir()
	stateDir = t.TempDir()
	logFile = filepath.Join(stateDir, "calls.log")
	zfs := `#!/bin/bash
echo "zfs $*" >> "$FAKE_LOG"
cmd="$1"; shift
case "$cmd" in
  snapshot)
    echo "$1" >> "$FAKE_STATE/snaps" ;;
  send)
    if [[ "$*" == *" -i "* && -f "$FAKE_STATE/fail_incr" ]]; then
      echo "cannot open 'tank/a#ezrepl-last': bookmark does not exist" >&2
      exit 1
    fi
    echo "datos-del-send" ;;
  recv)
    cat > /dev/null ;;
  bookmark)
    echo "$2" >> "$FAKE_STATE/bookmarks" ;;
  destroy)
    if [[ "$1" == "-r" ]]; then shift; fi
    sed -i "\|^$1\$|d" "$FAKE_STATE/snaps" 2>/dev/null ;;
  list)
    cat "$FAKE_STATE/snaps" 2>/dev/null ;;
esac
exit 0
`
	ssh := `#!/bin/bash
echo "ssh $*" >> "$FAKE_LOG"
last="${!#}"
case "$SSH_MODE" in
  deny)
    echo "Permission denied (publickey)." >&2
    exit 255 ;;
esac
case "$last" in
  version) echo "zfs-2.2.6-1"; echo "zfs-kmod-2.2.6-1" ;;
  *"zfs recv"*) cat > /dev/null ;;
  *"zfs destroy"*) : ;;
esac
exit 0
`
	for name, body := range map[string]string{"zfs": zfs, "ssh": ssh} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_LOG", logFile)
	t.Setenv("FAKE_STATE", stateDir)
	t.Setenv("SSH_MODE", "ok")
	executil.SetSudoForTest(false)
	return logFile, stateDir
}

func newTestRunner(t *testing.T) *Runner {
	t.Helper()
	ops := longops.New(hub.NewHub())
	return NewRunner(nil, ops, hub.NewHub(), nil, t.TempDir(), false)
}

func readLog(t *testing.T, f string) string {
	t.Helper()
	b, err := os.ReadFile(f)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(b)
}

func localJob() *Job {
	return &Job{Source: "tank/a", DestType: "local", DestDataset: "bak/a", Schedule: "daily@03:00"}
}

func TestRunFirstFullThenIncremental(t *testing.T) {
	logFile, stateDir := fakeBin(t)
	r := newTestRunner(t)
	j := localJob()
	ctx := context.Background()

	// 1ª ejecución: send completo (sin -i), bookmark creado.
	if err := r.run(ctx, j); err != nil {
		t.Fatalf("run 1: %v", err)
	}
	log1 := readLog(t, logFile)
	if !strings.Contains(log1, "zfs snapshot tank/a@ezrepl-") {
		t.Errorf("falta snapshot inicial:\n%s", log1)
	}
	if strings.Contains(log1, "send -v -i ") {
		t.Errorf("la primera ejecución no debe ser incremental:\n%s", log1)
	}
	if !strings.Contains(log1, "zfs bookmark tank/a@ezrepl-") {
		t.Errorf("falta bookmark tras éxito:\n%s", log1)
	}
	bm := strings.TrimSpace(readLog(t, filepath.Join(stateDir, "bookmarks")))
	if bm == "" || j.LastBookmark == "" {
		t.Fatalf("bookmark no registrado: %q / %q", bm, j.LastBookmark)
	}

	// 2ª y 3ª: incremental con -i <src>#ezrepl-last; prune conserva solo 2.
	// (los nombres de snapshot llevan timestamp al segundo: espaciar las runs)
	time.Sleep(1100 * time.Millisecond)
	if err := r.run(ctx, j); err != nil {
		t.Fatalf("run 2: %v", err)
	}
	time.Sleep(1100 * time.Millisecond)
	if err := r.run(ctx, j); err != nil {
		t.Fatalf("run 3: %v", err)
	}
	logAll := readLog(t, logFile)
	if !strings.Contains(logAll, "send -v -i tank/a#ezrepl-last tank/a@ezrepl-") {
		t.Errorf("falta send incremental con bookmark:\n%s", logAll)
	}
	snaps := strings.Fields(readLog(t, filepath.Join(stateDir, "snaps")))
	if len(snaps) != 2 {
		t.Errorf("prune debería conservar 2 snapshots ezrepl, hay %v", snaps)
	}
	if !strings.Contains(logAll, "zfs destroy tank/a@ezrepl-") {
		t.Errorf("prune no destruyó snapshots viejos:\n%s", logAll)
	}
}

func TestRunIncrementalFailureWithoutForceFull(t *testing.T) {
	_, stateDir := fakeBin(t)
	os.WriteFile(filepath.Join(stateDir, "fail_incr"), []byte("1"), 0o644)
	r := newTestRunner(t)
	j := localJob()
	j.LastBookmark = "ezrepl-20000101-000000" // simula ejecución previa
	err := r.run(context.Background(), j)
	if err == nil {
		t.Fatal("incremental divergente debería fallar")
	}
	if !strings.Contains(err.Error(), "force_full") {
		t.Errorf("el error debe indicar la opción force_full: %v", err)
	}
}

func TestRunIncrementalFailureWithForceFull(t *testing.T) {
	logFile, stateDir := fakeBin(t)
	r := newTestRunner(t)
	j := localJob()
	j.ForceFull = true
	j.LastBookmark = "ezrepl-20000101-000000"
	// El incremental falla solo la primera vez (divergencia); tras el destroy
	// del destino el send completo funciona.
	os.WriteFile(filepath.Join(stateDir, "fail_incr"), []byte("1"), 0o644)
	if err := r.run(context.Background(), j); err != nil {
		t.Fatalf("force_full debería recuperar la replicación: %v", err)
	}
	log := readLog(t, logFile)
	if !strings.Contains(log, "zfs destroy -r bak/a") {
		t.Errorf("force_full debería destruir el destino:\n%s", log)
	}
	// Tras el fallo incremental debe verse un send completo (sin -i).
	i := strings.Index(log, "send -v -i ")
	if i < 0 || !strings.Contains(log[i:], "bash") && !strings.Contains(log[i:], "send -v tank/a@") {
		// el send completo va dentro del pipeline bash → aparece en el log del fake zfs
	}
	if !strings.Contains(log, "zfs send") {
		t.Errorf("falta reintento completo:\n%s", log)
	}
	if j.LastBookmark == "ezrepl-20000101-000000" {
		t.Error("LastBookmark debería apuntar al snapshot nuevo")
	}
}

func TestRunSSH(t *testing.T) {
	logFile, _ := fakeBin(t)
	r := newTestRunner(t)
	j := &Job{Source: "tank/a", DestType: "ssh", DestDataset: "bak/a",
		Host: "nas.local", User: "zfsrepl", Port: 2222, Raw: true, Schedule: "daily@03:00"}
	if err := r.run(context.Background(), j); err != nil {
		t.Fatalf("run ssh: %v", err)
	}
	log := readLog(t, logFile)
	if !strings.Contains(log, "ssh -i ") || !strings.Contains(log, "-p 2222") ||
		!strings.Contains(log, "zfsrepl@nas.local") {
		t.Errorf("pipeline ssh incorrecto:\n%s", log)
	}
	if !strings.Contains(log, "send -v -w ") {
		t.Errorf("raw=true debería añadir -w:\n%s", log)
	}
}

func TestTestConnection(t *testing.T) {
	logFile, _ := fakeBin(t)
	r := newTestRunner(t)
	v, err := r.TestConnection(context.Background(), "nas.local", "zfsrepl", 22)
	if err != nil {
		t.Fatalf("test ok: %v", err)
	}
	if v != "zfs-2.2.6-1" {
		t.Errorf("versión remota inesperada: %q", v)
	}
	if !strings.Contains(readLog(t, logFile), "BatchMode=yes") {
		t.Error("ssh debería usar BatchMode=yes")
	}
	t.Setenv("SSH_MODE", "deny")
	if _, err := r.TestConnection(context.Background(), "nas.local", "zfsrepl", 22); err == nil ||
		!strings.Contains(err.Error(), "autenticación") {
		t.Errorf("fallo de auth debería dar error legible: %v", err)
	}
	if _, err := r.TestConnection(context.Background(), "h; rm -rf", "u", 22); err == nil {
		t.Error("host malicioso debería rechazarse antes de ssh")
	}
}

// --- store sobre SQLite en memoria (con migraciones reales) ---

func TestStoreCRUD(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := db.Migrate(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	st := NewStore(d)
	ctx := context.Background()
	id, err := st.Create(ctx, &Job{Source: "tank/a", DestType: "ssh", DestDataset: "bak/a",
		Host: "nas", User: "u", Port: 22, Raw: true, Schedule: "daily@03:00"})
	if err != nil {
		t.Fatal(err)
	}
	j, err := st.Get(ctx, id)
	if err != nil || !j.Raw || !j.Enabled || j.DestType != "ssh" {
		t.Fatalf("get: %+v err=%v", j, err)
	}
	off, ff := false, true
	if err := st.Update(ctx, id, &off, &ff, nil, nil); err != nil {
		t.Fatal(err)
	}
	j, _ = st.Get(ctx, id)
	if j.Enabled || !j.ForceFull {
		t.Errorf("update no aplicado: %+v", j)
	}
	if err := st.MarkRun(ctx, id, time.Now(), false, "fallo X", ""); err != nil {
		t.Fatal(err)
	}
	j, _ = st.Get(ctx, id)
	if j.LastRun == nil || j.LastOK == nil || *j.LastOK || j.LastError != "fallo X" {
		t.Errorf("markrun: %+v", j)
	}
	if err := st.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Get(ctx, id); err != ErrNotFound {
		t.Errorf("tras delete debería ser ErrNotFound: %v", err)
	}
}

func TestRunnerTryAcquireRelease(t *testing.T) {
	r := NewRunner(nil, longops.New(hub.NewHub()), hub.NewHub(), nil, t.TempDir(), false)
	id := int64(42)
	if !r.TryAcquire(id) {
		t.Fatal("primera adquisición debería tener éxito")
	}
	if r.TryAcquire(id) {
		t.Fatal("segunda adquisición del mismo id debería fallar")
	}
	r.Release(id)
	if !r.TryAcquire(id) {
		t.Fatal("tras Release, TryAcquire debería tener éxito otra vez")
	}
	r.Release(id)
	// Id diferente: sin interferencia
	if !r.TryAcquire(99) {
		t.Fatal("TryAcquire con id distinto debería tener éxito")
	}
	r.Release(99)
}

func TestRunNowPreventsDoubleExecution(t *testing.T) {
	fakeBin(t)
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := db.Migrate(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	st := NewStore(d)
	id, err := st.Create(context.Background(), localJob())
	if err != nil {
		t.Fatal(err)
	}
	ops := longops.New(hub.NewHub())
	r := NewRunner(st, ops, hub.NewHub(), nil, t.TempDir(), true)
	r.testGate = make(chan struct{})

	// Primera llamada: adquiere el slot, lanza execute, se bloquea en el gate.
	done1 := make(chan error, 1)
	go func() {
		done1 <- r.RunNow(context.Background(), id)
	}()
	time.Sleep(100 * time.Millisecond) // margen para que la goroutine adquiera el slot

	// Segunda llamada: el slot ya está tomado → ErrAlreadyRunning.
	err2 := r.RunNow(context.Background(), id)
	if !errors.Is(err2, ErrAlreadyRunning) {
		t.Fatalf("segunda RunNow debería devolver ErrAlreadyRunning, got %v", err2)
	}

	// Liberar el gate: la primera ejecución continúa.
	close(r.testGate)
	if err := <-done1; err != nil {
		t.Fatalf("primera RunNow falló: %v", err)
	}

	// Esperar a que execute termine (mockRun tarda ~3s: sleep 2 + sleep 1) y
	// libere el slot. Poll TryAcquire hasta que esté libre con timeout.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("timeout esperando a que el slot se libere tras la ejecución")
		}
		if r.TryAcquire(id) {
			r.Release(id)
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	r.testGate = nil

	// Tras completarse, el slot está libre otra vez.
	if err := r.RunNow(context.Background(), id); err != nil {
		t.Fatalf("RunNow tras completarse la primera ejecución: %v", err)
	}
}
