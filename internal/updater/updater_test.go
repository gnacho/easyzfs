package updater

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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
