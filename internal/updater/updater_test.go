package updater

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Masterminds/semver/v3"
)

func TestIsRestartConfigured(t *testing.T) {
	tmp := t.TempDir()
	pathUnit := filepath.Join(tmp, "easyzfs-update.path")
	svcUnit := filepath.Join(tmp, "easyzfs-update.service")

	u := New("2.9.9", tmp, "")
	u.restartPathUnit = pathUnit
	u.restartServiceUnit = svcUnit

	if u.IsRestartConfigured() {
		t.Error("expected false when neither unit exists")
	}

	if err := os.WriteFile(pathUnit, []byte("[Path]"), 0o644); err != nil {
		t.Fatal(err)
	}
	if u.IsRestartConfigured() {
		t.Error("expected false when only path unit exists")
	}

	if err := os.WriteFile(svcUnit, []byte("[Service]"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !u.IsRestartConfigured() {
		t.Error("expected true when both units exist")
	}
}

func TestPlanRestartReady(t *testing.T) {
	tmp := t.TempDir()
	pathUnit := filepath.Join(tmp, "easyzfs-update.path")
	svcUnit := filepath.Join(tmp, "easyzfs-update.service")

	u := New("2.9.9", tmp, "")
	u.restartPathUnit = pathUnit
	u.restartServiceUnit = svcUnit

	plan := u.Plan()
	if plan.CanApply {
		t.Error("expected CanApply=false when restart units are missing")
	}
	var restartCheck *Check
	for i := range plan.Checks {
		if plan.Checks[i].ID == "restart_ready" {
			restartCheck = &plan.Checks[i]
			break
		}
	}
	if restartCheck == nil {
		t.Fatal("restart_ready check missing")
	}
	if restartCheck.Status != "fail" {
		t.Errorf("expected restart_ready status=fail, got %s", restartCheck.Status)
	}

	if err := os.WriteFile(pathUnit, []byte("[Path]"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(svcUnit, []byte("[Service]"), 0o644); err != nil {
		t.Fatal(err)
	}

	plan = u.Plan()
	if !plan.CanApply {
		t.Error("expected CanApply=true when restart units are present and data dir is writable")
	}
	for i := range plan.Checks {
		if plan.Checks[i].ID == "restart_ready" && plan.Checks[i].Status != "pass" {
			t.Errorf("expected restart_ready status=pass, got %s", plan.Checks[i].Status)
		}
	}
}

func TestStatusIncludesRestartConfigured(t *testing.T) {
	tmp := t.TempDir()
	pathUnit := filepath.Join(tmp, "easyzfs-update.path")
	svcUnit := filepath.Join(tmp, "easyzfs-update.service")

	u := New("2.9.9", tmp, "")
	u.restartPathUnit = pathUnit
	u.restartServiceUnit = svcUnit

	if u.Status().RestartConfigured {
		t.Error("expected RestartConfigured=false when units are missing")
	}

	if err := os.WriteFile(pathUnit, []byte("[Path]"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(svcUnit, []byte("[Service]"), 0o644); err != nil {
		t.Fatal(err)
	}

	if !u.Status().RestartConfigured {
		t.Error("expected RestartConfigured=true when units are present")
	}
}

func TestSubscribeBroadcastProgress(t *testing.T) {
	u := New("2.9.18", t.TempDir(), "")
	ch, cancel := u.Subscribe()
	defer cancel()

	// Apply fija inProgress=true antes de setProgress; replicamos ese estado.
	u.mu.Lock()
	u.inProgress = true
	u.mu.Unlock()

	u.setProgress("downloading", 30)
	select {
	case st := <-ch:
		if !st.InProgress {
			t.Fatal("esperaba inProgress=true al aplicar")
		}
		if st.Progress == nil || st.Progress.Step != "downloading" || st.Progress.Percentage != 30 {
			t.Fatalf("progreso inesperado: %+v", st.Progress)
		}
	case <-time.After(time.Second):
		t.Fatal("no llegó el evento de progreso tras setProgress")
	}

	// Tras cancel no deben llegar más eventos (el suscriptor se retira).
	cancel()
	u.setProgress("installing", 70)
	u.setProgress("restarting", 100)
	select {
	case st := <-ch:
		t.Fatalf("no debería llegar tras cancel: %+v", st)
	default:
	}
}

// #155: una preview nunca debe presentarse como "estás al día".
func TestIsDevBuild(t *testing.T) {
	casos := []struct {
		v   string
		dev bool
	}{
		{"2.9.30", false},             // release limpia
		{"2.9.31-alpha.abc123", true}, // política nueva: prerelease de la siguiente
		{"2.9.99-maina19a014", true},  // previews viejas, por encima de toda release
		{"2.9.99-117revive", true},
		{"2.9.29-3-gabc123", true}, // git describe entre tags
		{"dev", true},              // build local sin versión semver
		{"", true},
	}
	for _, c := range casos {
		if got := isDevBuild(c.v); got != c.dev {
			t.Errorf("isDevBuild(%q) = %v, esperaba %v", c.v, got, c.dev)
		}
	}
}

func TestStatusMarcaDevBuild(t *testing.T) {
	if !New("v2.9.99-maina19a014", t.TempDir(), "").Status().DevBuild {
		t.Error("una preview 2.9.99-* debe venir marcada como devBuild")
	}
	if New("v2.9.30", t.TempDir(), "").Status().DevBuild {
		t.Error("una release limpia no debe venir marcada como devBuild")
	}
}

// #155, política de etiquetado: la preview debe quedar entre la última release
// y la siguiente, nunca por encima de todas como hacían las viejas 2.9.99-*.
func TestPoliticaPreviewsSemver(t *testing.T) {
	preview := semver.MustParse("2.9.31-alpha.abc123")
	if !preview.GreaterThan(semver.MustParse("2.9.30")) {
		t.Error("la preview debe ser mayor que la última release para no ser pisada")
	}
	if !preview.LessThan(semver.MustParse("2.9.31")) {
		t.Error("la preview debe ser menor que la siguiente release para que el updater la ofrezca")
	}
	if !semver.MustParse("2.9.99-maina19a014").GreaterThan(semver.MustParse("2.9.30")) {
		t.Error("este test documenta el problema: las viejas previews quedaban por encima de todo")
	}
}
