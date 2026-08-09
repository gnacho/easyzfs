// i18n.go — catálogo ES/EN de notificaciones push, compuesto server-side.
// El idioma se lee de push_subscriptions.lang (idioma del dispositivo al
// suscribirse). Las variables ({pool}, {pct}…) se interpolan con params.
package push

import (
	"fmt"
	"strings"
)

// textos — título corto + cuerpo con placeholders {param} por kind de alerta.
type textos struct {
	title string
	body  string
}

var catalogo = map[string]map[string]textos{
	"es": {
		"pool_capacity": {"Capacidad de pool", "El pool {pool} está al {pct}% de capacidad (umbral {threshold}%)."},
		"pool_status":   {"Estado de pool", "El pool {pool} está {status}."},
		"scrub_errors":  {"Scrub con errores", "El scrub de {pool} terminó con {errors} errores."},
		"disk_temp":     {"Disco caliente", "El disco {dev} está a {temp} °C (umbral {threshold} °C)."},
		"smart_status":  {"Aviso SMART", "{dev}: {detail}."},
		// Eventos ZFS en tiempo real (colector events, source "zed.<class>")
		"zfs_io_error":       {"Errores de E/S", "{vdev}: errores de E/S en el pool {pool}."},
		"zfs_checksum_error": {"Errores de checksum", "{vdev}: errores de checksum en el pool {pool}."},
		"zfs_data_error":     {"Errores de datos", "{vdev}: errores de datos en el pool {pool}."},
		"zfs_deadman":        {"E/S colgada", "{vdev}: operación de E/S colgada (deadman) en el pool {pool}."},
		"zfs_io_delay":       {"E/S lenta", "{vdev}: E/S lenta (delay) en el pool {pool}."},
		"vdev_state":         {"Estado de vdev", "{vdev} pasó a {state} en el pool {pool}."},
		"resilver_start":     {"Resilver iniciado", "El pool {pool} ha empezado a reconstruirse."},
		"resilver_finish":    {"Resilver terminado", "El pool {pool} ha terminado de reconstruirse."},
		"generic":            {"EasyZFS", "Tienes una alerta nueva."},
	},
	"en": {
		"pool_capacity": {"Pool capacity", "Pool {pool} is at {pct}% capacity (threshold {threshold}%)."},
		"pool_status":   {"Pool status", "Pool {pool} is {status}."},
		"scrub_errors":  {"Scrub errors", "Scrub of {pool} finished with {errors} errors."},
		"disk_temp":     {"Hot disk", "Disk {dev} is at {temp} °C (threshold {threshold} °C)."},
		"smart_status":  {"SMART warning", "{dev}: {detail}."},
		// Real-time ZFS events (events collector, source "zed.<class>")
		"zfs_io_error":       {"I/O errors", "{vdev}: I/O errors on pool {pool}."},
		"zfs_checksum_error": {"Checksum errors", "{vdev}: checksum errors on pool {pool}."},
		"zfs_data_error":     {"Data errors", "{vdev}: data errors on pool {pool}."},
		"zfs_deadman":        {"Hung I/O", "{vdev}: hung I/O operation (deadman) on pool {pool}."},
		"zfs_io_delay":       {"Slow I/O", "{vdev}: slow I/O (delay) on pool {pool}."},
		"vdev_state":         {"Vdev state", "{vdev} changed to {state} on pool {pool}."},
		"resilver_start":     {"Resilver started", "Pool {pool} has started rebuilding."},
		"resilver_finish":    {"Resilver finished", "Pool {pool} has finished rebuilding."},
		"generic":            {"EasyZFS", "You have a new alert."},
	},
}

// estadosES — estados de pool traducidos en el catálogo ES. EN los deja tal
// cual (DEGRADED/FAULTED son los términos de zpool).
var estadosES = map[string]string{
	"DEGRADED": "degradado",
	"FAULTED":  "fallado",
}

// Compose devuelve título y cuerpo interpolados para (lang, kind, params),
// reutilizado por los canales email/webhook además del push. Idiomas/kind
// desconocidos → fallback ('es' / 'generic'), igual que catalog.
func Compose(lang, kind string, params map[string]any) (title, body string) {
	return catalog(lang, kind, params)
}

// catalog devuelve título y cuerpo interpolados para (lang, kind). Idioma o
// kind desconocido → fallback ('es' / 'generic').
func catalog(lang, kind string, params map[string]any) (title, body string) {
	dict, ok := catalogo[lang]
	if !ok {
		dict = catalogo["es"]
	}
	tx, ok := dict[kind]
	if !ok {
		tx = dict["generic"]
	}
	// ES traduce los estados de pool (DEGRADED→degradado, FAULTED→fallado);
	// EN los deja tal cual. (lang≠"en" siempre resuelve al diccionario ES.)
	if lang != "en" && kind == "pool_status" {
		if traducido, ok2 := estadosES[fmt.Sprint(params["status"])]; ok2 {
			p := make(map[string]any, len(params))
			for k, v := range params {
				p[k] = v
			}
			p["status"] = traducido
			params = p
		}
	}
	return interp(tx.title, params), interp(tx.body, params)
}

// interp sustituye {clave} por el valor de params (fmt.Sprint).
func interp(s string, params map[string]any) string {
	for k, v := range params {
		s = strings.ReplaceAll(s, "{"+k+"}", fmt.Sprint(v))
	}
	return s
}
