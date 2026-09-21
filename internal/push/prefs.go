// prefs.go — preferencias de notificación por usuario: tipos habilitados,
// quiet hours (horario silencioso) y la cola de entrega diferida
// (notification_queue) que vacía el ticker de RunQueue.
//
// Reglas (skill web-push-alerts):
//   - Sin fila en notification_preferences = tipo habilitado (default true).
//   - Quiet hours: ventana horaria local (tz del usuario) que puede cruzar
//     medianoche. Las críticas la atraviesan SIEMPRE; warn/info se encolan.
//   - El ticker (60 s) vacía la cola de cada usuario cuando su ventana termina;
//     el coalescing por tag lo hace el push service.
//   - Modo demo: nunca se envía (la cola tampoco se procesa a envío real).
package push

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"time"
)

// Tipos — catálogo de tipos de alerta configurables por el usuario (casan con
// notification_preferences.tipo y con las claves del catálogo i18n).
var Tipos = []string{"pool_capacity", "pool_status", "pool_missing", "scrub_errors", "disk_temp", "smart_status"}

// TipoValido — ¿tipo de alerta conocido?
func TipoValido(tipo string) bool {
	for _, t := range Tipos {
		if t == tipo {
			return true
		}
	}
	return false
}

// Preference — preferencia de un tipo de alerta para la API.
type Preference struct {
	Tipo    string `json:"tipo"`
	Enabled bool   `json:"enabled"`
}

// Preferences — las 5 preferencias del usuario (default true si no hay fila).
func (s *Sender) Preferences(ctx context.Context, userID string) ([]Preference, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT tipo, enabled FROM notification_preferences WHERE user_id=?", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	guardadas := map[string]bool{}
	for rows.Next() {
		var tipo string
		var enabled int
		if err := rows.Scan(&tipo, &enabled); err != nil {
			return nil, err
		}
		guardadas[tipo] = enabled != 0
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]Preference, 0, len(Tipos))
	for _, t := range Tipos {
		enabled, ok := guardadas[t]
		out = append(out, Preference{Tipo: t, Enabled: !ok || enabled})
	}
	return out, nil
}

// SetPreference — upsert de (user_id, tipo); el handler valida el tipo antes.
func (s *Sender) SetPreference(ctx context.Context, userID, tipo string, enabled bool) error {
	en := 0
	if enabled {
		en = 1
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO notification_preferences(user_id, tipo, enabled, updated_at)
		VALUES (?,?,?,datetime('now'))
		ON CONFLICT(user_id, tipo) DO UPDATE SET enabled=excluded.enabled, updated_at=datetime('now')`,
		userID, tipo, en)
	return err
}

// prefEnabled — ¿el usuario tiene habilitado este tipo? Sin fila = true.
// Ante error de BD se asume habilitado (mejor un push de más que perderlo).
func (s *Sender) prefEnabled(ctx context.Context, userID, tipo string) bool {
	var enabled int
	err := s.db.QueryRowContext(ctx,
		"SELECT enabled FROM notification_preferences WHERE user_id=? AND tipo=?",
		userID, tipo).Scan(&enabled)
	if err != nil {
		return true // sql.ErrNoRows u otro error: default habilitado
	}
	return enabled != 0
}

// QuietHours — horario silencioso del usuario para la API. Enabled=false
// equivale a quiet_start/quiet_end NULL en BD.
type QuietHours struct {
	Enabled bool   `json:"enabled"`
	Start   *int   `json:"start"` // hora local 0-23 (null si desactivado)
	End     *int   `json:"end"`   // puede cruzar medianoche (22 → 8)
	TZ      string `json:"tz"`
}

// QuietHoursDefaultTZ — zona horaria fija de momento (no se expone en la UI).
const QuietHoursDefaultTZ = "Europe/Madrid"

// QuietHours — horario silencioso guardado (o defaults desactivado).
func (s *Sender) QuietHours(ctx context.Context, userID string) (QuietHours, error) {
	var start, end sql.NullInt64
	tz := QuietHoursDefaultTZ
	err := s.db.QueryRowContext(ctx,
		"SELECT quiet_start, quiet_end, tz FROM notification_quiet_hours WHERE user_id=?",
		userID).Scan(&start, &end, &tz)
	if err == sql.ErrNoRows {
		return QuietHours{Enabled: false, TZ: QuietHoursDefaultTZ}, nil
	}
	if err != nil {
		return QuietHours{}, err
	}
	q := QuietHours{Enabled: start.Valid && end.Valid, TZ: tz}
	if q.Enabled {
		sv, ev := int(start.Int64), int(end.Int64)
		q.Start, q.End = &sv, &ev
	}
	return q, nil
}

// SetQuietHours — upsert del horario silencioso; el handler valida el rango.
// Desactivar guarda NULLs (la fila queda pero sin ventana activa).
func (s *Sender) SetQuietHours(ctx context.Context, userID string, enabled bool, start, end int) error {
	var sv, ev any
	if enabled {
		sv, ev = start, end
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO notification_quiet_hours(user_id, quiet_start, quiet_end, tz, updated_at)
		VALUES (?,?,?,?,datetime('now'))
		ON CONFLICT(user_id) DO UPDATE SET
		  quiet_start=excluded.quiet_start, quiet_end=excluded.quiet_end, updated_at=datetime('now')`,
		userID, sv, ev, QuietHoursDefaultTZ)
	return err
}

// inQuietHours — ¿está el usuario ahora mismo dentro de su ventana de silencio?
// Hora local en la tz guardada; la ventana puede cruzar medianoche.
// Ante error de BD se asume FUERA de quiet hours (mejor no silenciar de más).
func (s *Sender) inQuietHours(ctx context.Context, userID string, now time.Time) bool {
	var start, end sql.NullInt64
	var tz string
	err := s.db.QueryRowContext(ctx,
		"SELECT quiet_start, quiet_end, tz FROM notification_quiet_hours WHERE user_id=?",
		userID).Scan(&start, &end, &tz)
	if err != nil || !start.Valid || !end.Valid {
		return false
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.Local
	}
	h := now.In(loc).Hour()
	sv, ev := int(start.Int64), int(end.Int64)
	if sv == ev {
		return false // ventana vacía (la validación de la API lo impide; defensa)
	}
	if sv < ev {
		return h >= sv && h < ev
	}
	return h >= sv || h < ev // cruza medianoche: 22 → 8
}

// queueItem — fila de notification_queue recomponible a una Alert.
type queueItem struct {
	id       int64
	userID   string
	tipo     string
	severity string
	alert    Alert
}

// queueDatos — lo mínimo para recomponer la alerta al vaciar la cola.
type queueDatos struct {
	Kind   string         `json:"kind"`
	Source string         `json:"source,omitempty"`
	Target string         `json:"target"`
	Params map[string]any `json:"params"`
}

// enqueue — guarda la alerta para entrega diferida (quiet hours).
func (s *Sender) enqueue(ctx context.Context, userID string, a Alert) error {
	datos, err := json.Marshal(queueDatos{Kind: a.Kind, Source: a.Source, Target: a.Target, Params: a.Params})
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO notification_queue(user_id, tipo, severity, datos_json)
		VALUES (?,?,?,?)`, userID, a.Kind, a.Level, string(datos))
	return err
}

// RunQueue — ticker (60 s) que vacía notification_queue cuando termina la
// ventana de silencio de cada usuario. Se arranca en main con el Sender.
// En demo o sin VAPID no hace nada (la cola nunca se procesa a envío real).
func (s *Sender) RunQueue(ctx context.Context) {
	if s.cfg.Demo || !s.cfg.PushEnabled() {
		return
	}
	tick := time.NewTicker(60 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.drainQueue(ctx)
		}
	}
}

// drainQueue — un paso del ticker: para cada usuario con alertas encoladas
// cuya ventana YA terminó, envía y borra sus elementos.
func (s *Sender) drainQueue(ctx context.Context) {
	if s.cfg.Demo || !s.cfg.PushEnabled() {
		return
	}
	rows, err := s.db.QueryContext(ctx, "SELECT DISTINCT user_id FROM notification_queue")
	if err != nil {
		log.Printf("push: cola (usuarios): %v", err)
		return
	}
	var users []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			log.Printf("push: cola (scan): %v", err)
			break
		}
		users = append(users, u)
	}
	rows.Close()

	now := time.Now()
	for _, u := range users {
		if s.inQuietHours(ctx, u, now) {
			continue // sigue en horario silencioso: se entrega más tarde
		}
		s.drainUser(ctx, u)
	}
}

// drainUser — envía y borra los elementos encolados de un usuario.
func (s *Sender) drainUser(ctx context.Context, userID string) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, tipo, severity, datos_json FROM notification_queue WHERE user_id=? ORDER BY id",
		userID)
	if err != nil {
		log.Printf("push: cola de %s: %v", userID, err)
		return
	}
	var items []queueItem
	for rows.Next() {
		var it queueItem
		var datos string
		if err := rows.Scan(&it.id, &it.tipo, &it.severity, &datos); err != nil {
			log.Printf("push: cola de %s (scan): %v", userID, err)
			break
		}
		var d queueDatos
		if err := json.Unmarshal([]byte(datos), &d); err != nil {
			log.Printf("push: cola de %s (datos_json inválido, id %d): %v", userID, it.id, err)
			continue
		}
		it.userID = userID
		it.alert = Alert{Level: it.severity, Source: d.Source, Target: d.Target, Kind: d.Kind, Params: d.Params}
		items = append(items, it)
	}
	rows.Close()
	if len(items) == 0 {
		// Nada enviable: limpiar por si quedaron filas corruptas.
		s.clearQueue(ctx, userID)
		return
	}

	subs, err := s.listUser(ctx, userID)
	if err != nil {
		log.Printf("push: suscripciones de %s: %v", userID, err)
		return // reintenta en el próximo tick (no se borra la cola)
	}
	for _, it := range items {
		if !s.prefEnabled(ctx, userID, it.tipo) {
			continue // deshabilitó el tipo mientras estaba en cola: se descarta
		}
		for _, sub := range subs {
			if it.severity != "crit" && s.hub != nil && s.hub.UserActive(userID) {
				break // app abierta: ya lo ve in-app, no duplicar
			}
			s.sendTo(ctx, sub, it.alert)
		}
	}
	s.clearQueue(ctx, userID)
}

// clearQueue — borra los elementos procesados de un usuario.
func (s *Sender) clearQueue(ctx context.Context, userID string) {
	if _, err := s.db.ExecContext(ctx,
		"DELETE FROM notification_queue WHERE user_id=?", userID); err != nil {
		log.Printf("push: vaciar cola de %s: %v", userID, err)
	}
}

// listUser — suscripciones de un solo usuario (para vaciar su cola).
func (s *Sender) listUser(ctx context.Context, userID string) ([]subscription, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, user_id, endpoint, p256dh, auth, lang, origin FROM push_subscriptions WHERE user_id=?",
		userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []subscription
	for rows.Next() {
		var sub subscription
		if err := rows.Scan(&sub.id, &sub.userID, &sub.endpoint, &sub.p256dh, &sub.auth, &sub.lang, &sub.origin); err != nil {
			return nil, err
		}
		out = append(out, sub)
	}
	return out, rows.Err()
}
