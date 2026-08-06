// props.go — tabla de propiedades editables de datasets (U3, fase P1).
// Whitelist ESTRICTA de propiedades y valores: NUNCA se interpola un nombre
// o valor en el argv de zfs sin pasar la validación (patrón del proyecto).
// Solo propiedades seguras y útiles; las delicadas (dedup, encryption,
// keyformat…) quedan FUERA. `zfs get all` expone muchas más (read-only y
// user properties) — se listan en GET pero no son editables vía PATCH.
package actions

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"easyzfs/internal/executil"
	"easyzfs/internal/model"
)

// ErrNotLocal — la propiedad no es local, no se puede heredar (409).
var ErrNotLocal = errors.New("la propiedad no es local (no se puede heredar)")

// propKind — tipo de validación de una propiedad editable.
type propKind int

const (
	propBool propKind = iota // "on" | "off"
	propEnum                 // pertenencia exacta a una lista
	propSize                 // "none" (si sizeNone) o número con sufijo K/M/G/T…
	propSizePow2             // potencia de 2 entre min/max (recordsize, volblocksize)
	propPath                 // ruta absoluta o none|legacy
)

// propSpec — definición de una propiedad editable.
type propSpec struct {
	kind     propKind
	enum     []string // propEnum
	sizeNone bool     // propSize: admite "none" (quota, reservation)
	minBytes uint64   // propSizePow2: cota inferior
	maxBytes uint64   // propSizePow2: cota superior
	fsOnly   bool     // solo aplica a filesystem (no volume)
	volOnly  bool     // solo aplica a volume (no filesystem)
}

// propValidators — whitelist de propiedades editables. Cada propiedad nueva
// que se quiera exponer DEBE añadirse aquí con su tipo y validación.
var propValidators = map[string]propSpec{
	"compression":     {kind: propEnum, enum: []string{"lz4", "zstd", "zlib", "gzip", "gzip-1", "gzip-2", "gzip-3", "gzip-4", "gzip-5", "gzip-6", "gzip-7", "gzip-8", "gzip-9", "lzjb", "off"}},
	"recordsize":      {kind: propSizePow2, minBytes: 4 << 10, maxBytes: 16 << 20},
	"atime":           {kind: propBool},
	"relatime":        {kind: propBool},
	"sync":            {kind: propEnum, enum: []string{"standard", "always", "disabled"}},
	"checksum":        {kind: propEnum, enum: []string{"on", "off", "fletcher2", "fletcher4", "sha256"}},
	"copies":          {kind: propEnum, enum: []string{"1", "2", "3"}},
	"xattr":           {kind: propEnum, enum: []string{"on", "off", "sa"}},
	"acltype":         {kind: propEnum, enum: []string{"off", "posix", "nfsv4"}},
	"aclinherit":      {kind: propEnum, enum: []string{"discard", "noallow", "restricted", "passthrough", "passthrough-x"}},
	"primarycache":    {kind: propEnum, enum: []string{"all", "none", "metadata"}},
	"secondarycache":  {kind: propEnum, enum: []string{"all", "none", "metadata"}},
	"logbias":         {kind: propEnum, enum: []string{"latency", "throughput"}},
	"canmount":        {kind: propEnum, enum: []string{"on", "off", "noauto"}},
	"mountpoint":      {kind: propPath, fsOnly: true},
	"exec":            {kind: propBool},
	"setuid":          {kind: propBool},
	"devices":         {kind: propBool},
	"readonly":        {kind: propBool},
	"snapdir":         {kind: propEnum, enum: []string{"hidden", "visible"}},
	"quota":           {kind: propSize, sizeNone: true, fsOnly: true},
	"reservation":     {kind: propSize, sizeNone: true, fsOnly: true},
	"volsize":         {kind: propSize, volOnly: true},
	"volblocksize":    {kind: propSizePow2, minBytes: 512, maxBytes: 128 << 10, volOnly: true},
}

// reSize — número con sufijo opcional K/M/G/T/P/E (o sin él = bytes),
// con la 'i' y 'B' opcionales que zfs acepta ("500G", "1TiB", "128K").
var reSize = regexp.MustCompile(`^[0-9]+([KMGTPE]i?B?)?$`)

// reMountpoint — ruta absoluta simple (whitelist estricta: sin espacios,
// sin ';' ni metacharacteres; también admite none|legacy).
var reMountpoint = regexp.MustCompile(`^/[a-zA-Z0-9_./\-]+$`)

// propSource — agrupa propiedades por origen para la UI (solo lectura vs
// editables vs user properties). Devuelve "" si la propiedad no es editable.
func propSource(name string) string {
	if _, ok := propValidators[name]; ok {
		return "editable"
	}
	if strings.HasPrefix(name, "user:") || strings.HasPrefix(name, "org.openzfs:") {
		return "user"
	}
	return "readonly"
}

// valid — comprueba que el valor es admisible para la propiedad.
func (p propSpec) valid(v string) bool {
	switch p.kind {
	case propBool:
		return v == "on" || v == "off"
	case propEnum:
		for _, e := range p.enum {
			if v == e {
				return true
			}
		}
		return false
	case propSize:
		if p.sizeNone && v == "none" {
			return true
		}
		return reSize.MatchString(v)
	case propSizePow2:
		n, ok := parseSize(v)
		if !ok || n < p.minBytes || n > p.maxBytes {
			return false
		}
		return n&(n-1) == 0 // potencia de 2
	case propPath:
		return v == "none" || v == "legacy" || reMountpoint.MatchString(v)
	}
	return false
}

// appliesTo — la propiedad es aplicable al tipo de dataset.
func (p propSpec) appliesTo(dsType string) bool {
	if p.fsOnly && dsType == "volume" {
		return false
	}
	if p.volOnly && dsType == "fs" {
		return false
	}
	return true
}

// parseSize — convierte un tamaño zfs ("500G", "1TiB", "1024") a bytes.
func parseSize(v string) (uint64, bool) {
	if v == "" {
		return 0, false
	}
	s := v
	// Sufijo opcional "iB" / "B" (p. ej. "1TiB", "500GB").
	for _, suf := range []string{"iB", "B"} {
		if strings.HasSuffix(s, suf) {
			s = strings.TrimSuffix(s, suf)
			break
		}
	}
	mult := uint64(1)
	if len(s) > 0 {
		switch s[len(s)-1] {
		case 'K':
			mult, s = 1<<10, s[:len(s)-1]
		case 'M':
			mult, s = 1<<20, s[:len(s)-1]
		case 'G':
			mult, s = 1<<30, s[:len(s)-1]
		case 'T':
			mult, s = 1<<40, s[:len(s)-1]
		case 'P':
			mult, s = 1<<50, s[:len(s)-1]
		case 'E':
			mult, s = 1<<60, s[:len(s)-1]
		}
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return n * mult, true
}

// describeKind — mensaje legible del tipo de valor esperado (para errores).
func (p propSpec) describeKind() string {
	switch p.kind {
	case propBool:
		return "on|off"
	case propEnum:
		return "uno de: " + strings.Join(p.enum, ", ")
	case propSize:
		return "tamaño (none o número con sufijo K/M/G/T)"
	case propSizePow2:
		return fmt.Sprintf("potencia de 2 entre %d y %d bytes", p.minBytes, p.maxBytes)
	case propPath:
		return "ruta absoluta, none o legacy"
	}
	return "valor válido"
}

// DatasetPropsGet — 'zfs get -H -o name,property,value,source all <ds>'.
// Lista TODAS las propiedades (nativas + user). El front agrupa por
// propSource; PATCH solo acepta las de la whitelist.
func (s *Service) DatasetPropsGet(ctx context.Context, name string) ([]model.DatasetProp, error) {
	if !reDataset.MatchString(name) {
		return nil, ErrInvalidName
	}
	out, err := executil.Run(ctx, 10*time.Second, "zfs",
		"get", "-H", "-o", "name,property,value,source", "all", name)
	if err != nil {
		return nil, fmt.Errorf("zfs get properties: %w", err)
	}
	props := []model.DatasetProp{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 4 {
			continue
		}
		props = append(props, model.DatasetProp{Name: f[1], Value: f[2], Source: f[3]})
	}
	return props, nil
}

// DatasetPropSet — 'zfs set <property>=<value> <ds>' (admin). Whitelist
// estricta de propiedad y valor; la propiedad debe aplicar al tipo.
func (s *Service) DatasetPropSet(ctx context.Context, actor, name, property, value, dsType string) error {
	if !reDataset.MatchString(name) {
		return ErrInvalidName
	}
	spec, ok := propValidators[property]
	if !ok {
		return fmt.Errorf("%w: propiedad no editable (%s)", ErrInvalidInput, property)
	}
	if dsType != "" && !spec.appliesTo(dsType) {
		return fmt.Errorf("%w: %s no aplica a un %s", ErrInvalidInput, property, dsType)
	}
	if !spec.valid(value) {
		return fmt.Errorf("%w: valor inválido para %s (%s)", ErrInvalidInput, property, spec.describeKind())
	}
	s.audit(ctx, actor, "dataset.setprop", name,
		map[string]any{"property": property, "value": value}, false)
	if _, err := executil.Run(ctx, 15*time.Second, "zfs", "set", property+"="+value, name); err != nil {
		return fmt.Errorf("zfs set %s: %w", property, err)
	}
	return nil
}

// DatasetPropInherit — 'zfs inherit <property> <ds>' (admin). Solo para
// propiedades de la whitelist con source == "local" (el handler comprueba
// el source contra la última lectura; si está obsoleta, zfs hace un no-op).
func (s *Service) DatasetPropInherit(ctx context.Context, actor, name, property string) error {
	if !reDataset.MatchString(name) {
		return ErrInvalidName
	}
	if _, ok := propValidators[property]; !ok {
		return fmt.Errorf("%w: propiedad no editable (%s)", ErrInvalidInput, property)
	}
	s.audit(ctx, actor, "dataset.inherit", name, map[string]any{"property": property}, false)
	if _, err := executil.Run(ctx, 15*time.Second, "zfs", "inherit", property, name); err != nil {
		return fmt.Errorf("zfs inherit %s: %w", property, err)
	}
	return nil
}
