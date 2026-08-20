package collectors

import (
	"os"
	"path/filepath"
	"testing"
)

// mkByIDDir crea un sandbox que imita el layout real: root/dev/sdX (ficheros)
// y root/dev/disk/by-id/<name> → symlinks relativos '../../sdX'. Los destinos
// EXISTEN para que filepath.EvalSymlinks resuelva (en /dev real los enlaces
// siempre apuntan a dispositivos existentes). Devuelve el dir by-id.
func mkByIDDir(t *testing.T, links map[string]string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "dev", "disk", "by-id")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for name, rel := range links {
		target := filepath.Join(dir, rel)
		if f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			f.Close()
		} else {
			t.Fatalf("target %s: %v", target, err)
		}
		if err := os.Symlink(rel, filepath.Join(dir, name)); err != nil {
			t.Fatalf("symlink %s: %v", name, err)
		}
	}
	return dir
}

func TestByIDMap(t *testing.T) {
	dir := mkByIDDir(t, map[string]string{
		"ata-WDC_WD40EFRX-68N_WD-WCC7K1AAAA01":      "../../sdb",
		"ata-WDC_WD40EFRX-68N_WD-WCC7K1AAAA01-part1": "../../sdb1",
		"wwn-0x5000cca000aaaa01":                     "../../sdb",
		"wwn-0x5000cca000aaaa01-part1":               "../../sdb1",
		"nvme-Samsung_SSD_980_1TB_S649NL0R111111":    "../../nvme0n1",
		"nvme-eui.002538839107fd40":                  "../../nvme0n1",
		"usb-SanDisk_Ultra_4C530001230919102571-0:0": "../../sda",
		"dm-name-vg-lv":                              "../../dm-0",
	})
	got := byIDMap(dir)

	// disco entero con ata- y wwn-: gana ata- (modelo+serial, score 3 > 2)
	if got["sdb"] != "ata-WDC_WD40EFRX-68N_WD-WCC7K1AAAA01" {
		t.Errorf("sdb = %q, esperado el enlace ata-", got["sdb"])
	}
	// las particiones NO se mapean como discos
	if _, ok := got["sdb1"]; ok {
		t.Errorf("sdb1 (partición) no debería aparecer: %q", got["sdb1"])
	}
	// nvme- con modelo gana a nvme-eui. (score 3 > 0)
	if got["nvme0n1"] != "nvme-Samsung_SSD_980_1TB_S649NL0R111111" {
		t.Errorf("nvme0n1 = %q, esperado el enlace nvme- con modelo", got["nvme0n1"])
	}
	// usb- se usa si es lo único que hay
	if got["sda"] != "usb-SanDisk_Ultra_4C530001230919102571-0:0" {
		t.Errorf("sda = %q, esperado el enlace usb-", got["sda"])
	}
	// destinos que no son discos físicos (dm-0) se mapean igualmente; el
	// llamador solo consulta discos de lsblk, así que no estorban.
	if got["dm-0"] != "dm-name-vg-lv" {
		t.Errorf("dm-0 = %q, esperado dm-name-vg-lv", got["dm-0"])
	}
}

func TestByIDMapDirInexistente(t *testing.T) {
	if got := byIDMap(filepath.Join(t.TempDir(), "no-existe")); got != nil {
		t.Errorf("dir inexistente: esperado nil, got %v", got)
	}
}

func TestByIDScore(t *testing.T) {
	cases := map[string]int{
		"ata-WDC_WD40EFRX-68N_WD-WCC7K1AAAA01": 3,
		"nvme-Samsung_SSD_980_1TB_S649NL0R":    3,
		"scsi-36000c29000000000":               3,
		"wwn-0x5000cca000aaaa01":               2,
		"usb-SanDisk_Ultra_XXXX-0:0":           1,
		"nvme-eui.002538839107fd40":            0,
		"dm-name-vg-lv":                        0,
	}
	for name, want := range cases {
		if got := byIDScore(name); got != want {
			t.Errorf("byIDScore(%q)=%d, esperado %d", name, got, want)
		}
	}
}
