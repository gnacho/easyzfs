package actions

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestPoolCreateAceptaByID(t *testing.T) {
	svc, logFile := newTestService(t)
	if err := svc.PoolCreate(context.Background(), "tester", "tank", "mirror",
		[]string{"/dev/disk/by-id/ata-WDC_WD40EFRX_WD-WCC4E1234567", "/dev/disk/by-id/ata-WDC_WD40EFRX_WD-WCC4E7654321"},
		12, true); err != nil {
		t.Fatalf("PoolCreate con by-id: %v", err)
	}
	out, _ := os.ReadFile(logFile)
	if got := strings.TrimSpace(string(out)); got != "create -o ashift=12 tank mirror /dev/disk/by-id/ata-WDC_WD40EFRX_WD-WCC4E1234567 /dev/disk/by-id/ata-WDC_WD40EFRX_WD-WCC4E7654321" {
		t.Fatalf("argv zpool = %q", got)
	}
}

func TestVdevAddAceptaByID(t *testing.T) {
	svc, logFile := newTestService(t)
	if err := svc.VdevAdd(context.Background(), "tester", "tank", "mirror",
		[]string{"/dev/disk/by-id/ata-WDC_WD40EFRX_WD-WCC4E1234567", "/dev/disk/by-id/ata-WDC_WD40EFRX_WD-WCC4E7654321"},
		true); err != nil {
		t.Fatalf("VdevAdd con by-id: %v", err)
	}
	out, _ := os.ReadFile(logFile)
	if got := strings.TrimSpace(string(out)); got != "add tank mirror /dev/disk/by-id/ata-WDC_WD40EFRX_WD-WCC4E1234567 /dev/disk/by-id/ata-WDC_WD40EFRX_WD-WCC4E7654321" {
		t.Fatalf("argv zpool = %q", got)
	}
}

func TestVdevArgsRechazaDiscoInvalido(t *testing.T) {
	svc, _ := newTestService(t)
	for _, bad := range []string{"/dev/sda", "sda;echo x", "../sda", ""} {
		if err := svc.VdevAdd(context.Background(), "tester", "tank", "mirror",
			[]string{bad, "sdb"}, true); !errors.Is(err, ErrInvalidDev) {
			t.Errorf("VdevAdd(%q) = %v, esperaba ErrInvalidDev", bad, err)
		}
	}
}
