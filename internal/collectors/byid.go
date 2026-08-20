// byid.go — resolución de discos kernel (sdX, nvme0n1) a su enlace estable
// /dev/disk/by-id/<nombre>. Las letras sdX son inestables entre arranques y
// movimientos de bahía; el by-id (modelo+serial o WWN) identifica el disco
// FÍSICO. Las operaciones destructivas (zpool replace) deben usar by-id
// siempre que exista (issue #65: replace lanzado contra 'sda', que podía ser
// otro disco tras un reboot).
package collectors

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// rePartLink — enlaces de partición creados por udev ('…-part1' → 'sda1'):
// su destino no es un disco entero, se ignoran.
var rePartLink = regexp.MustCompile(`-part[0-9]+$`)

// byIDMap escanea dir (normalmente /dev/disk/by-id) y devuelve el mapa
// nombre-kernel-base ('sda') → mejor nombre by-id ('ata-WDC_WD40…').
// Los enlaces rotos (disco desaparecido a mitad de escaneo) se saltan.
// Determinista: os.ReadDir devuelve las entradas ordenadas y el desempate
// conserva la primera.
func byIDMap(dir string) map[string]string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	type cand struct {
		name  string
		score int
	}
	best := map[string]cand{}
	for _, e := range entries {
		name := e.Name()
		if rePartLink.MatchString(name) {
			continue
		}
		target, err := filepath.EvalSymlinks(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		dev := filepath.Base(target)
		score := byIDScore(name)
		if prev, ok := best[dev]; !ok || score > prev.score {
			best[dev] = cand{name: name, score: score}
		}
	}
	out := make(map[string]string, len(best))
	for dev, c := range best {
		out[dev] = c.name
	}
	return out
}

// byIDScore — preferencia de estabilidad/legibilidad del enlace by-id:
// ata-/nvme-/scsi- (llevan modelo+serial) > wwn- > usb- > resto (p.ej.
// 'nvme-eui.…', lvm, dm-name…). Determinista.
func byIDScore(name string) int {
	switch {
	case strings.HasPrefix(name, "ata-"),
		strings.HasPrefix(name, "nvme-") && !strings.HasPrefix(name, "nvme-eui."),
		strings.HasPrefix(name, "scsi-"):
		return 3
	case strings.HasPrefix(name, "wwn-"):
		return 2
	case strings.HasPrefix(name, "usb-"):
		return 1
	default:
		return 0
	}
}
