// Package alerts — generación de alertas por umbrales (capacidad, temperatura,
// SMART, scrub con errores) en la tabla alerts + evento SSE alert.new.
// Dedupe: por (source, kind) si hay kind (el mensaje con contadores volátiles
// se refresca en la alerta activa); por (source, message) en alertas legado.
package alerts

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"easyzfs/internal/hub"
	"easyzfs/internal/model"
	"easyzfs/internal/push"
	"easyzfs/internal/settings"
	"easyzfs/internal/webhook"
)

// Alerter evalúa umbrales y persiste/emite alertas.
type Alerter struct {
	db   *sql.DB
	hub  *hub.Hub
	st   *settings.Store
	push *push.Sender // puede ser nil (push no configurado)
	wh   *webhook.Notifier // puede ser nil (webhook desactivado)
}

// New crea el Alerter.
func New(d *sql.DB, h *hub.Hub, st *settings.Store) *Alerter {
	return &Alerter{db: d, hub: h, st: st}
}

// SetPush conecta el sender Web Push (opcional; nil = sin notificaciones push).
func (a *Alerter) SetPush(s *push.Sender) { a.push = s }

// SetWebhook conecta el notificador de webhook saliente (opcional; nil = sin
// webhook). El notifier ya arranca su worker al crearse; aquí solo se enlaza.
func (a *Alerter) SetWebhook(w *webhook.Notifier) { a.wh = w }

// Raise inserta una alerta sin metadatos estructurados (kind "").
func (a *Alerter) Raise(ctx context.Context, level, source, target, message string) {
	a.RaiseKind(ctx, level, source, target, message, "", nil)
}

// RaiseKind inserta una alerta y la emite por SSE. target es el destino
// navegable en la UI ("pools:tank", "disks:nvme1n1", "tasks", "settings"; ""
// si no aplica). kind+params son los metadatos estructurados (alerts.meta)
// que el sender push usa para componer el texto i18n; message sigue en
// español (compat UI).
//
// Dedupe: con kind != "" se deduplica por (source, kind) — NO por mensaje,
// porque el mensaje lleva contadores volátiles (p. ej. UDMA CRC que crece
// en cada pasada SMART): sin esto, un disco con tormenta CRC generaba una
// alerta + push NUEVA cada 10 min. Si ya existe una alerta activa del mismo
// (source, kind), se REFRESCA su mensaje/ts/meta (sin re-notificar push ni
// SSE: no es un evento nuevo). Con kind "" (legado) se deduplica por
// (source, message) como siempre.
func (a *Alerter) RaiseKind(ctx context.Context, level, source, target, message, kind string, params map[string]any) {
	meta := ""
	if kind != "" {
		if raw, err := json.Marshal(map[string]any{"kind": kind, "params": params}); err == nil {
			meta = string(raw)
		}
	}
	if kind != "" {
		var id int64
		err := a.db.QueryRowContext(ctx,
			"SELECT id FROM alerts WHERE source=? AND kind=? AND acked_at IS NULL LIMIT 1",
			source, kind).Scan(&id)
		if err == nil {
			// Ya activa: refresca mensaje con los contadores actuales.
			if _, err := a.db.ExecContext(ctx,
				"UPDATE alerts SET ts=?, level=?, message=?, meta=? WHERE id=?",
				time.Now().UTC().Format(time.RFC3339), level, message, meta, id); err != nil {
				log.Printf("alerts: refresh: %v", err)
			}
			return
		}
	} else {
		var exists int
		err := a.db.QueryRowContext(ctx,
			"SELECT 1 FROM alerts WHERE source=? AND message=? AND acked_at IS NULL LIMIT 1",
			source, message).Scan(&exists)
		if err == nil {
			return // ya hay una idéntica activa
		}
	}
	now := time.Now().UTC()
	res, err := a.db.ExecContext(ctx,
		"INSERT INTO alerts(ts, level, source, target, message, meta, kind) VALUES (?,?,?,?,?,?,?)",
		now.Format(time.RFC3339), level, source, target, message, meta, kind)
	if err != nil {
		log.Printf("alerts: insert: %v", err)
		return
	}
	id, _ := res.LastInsertId()
	a.hub.Publish("alert.new", map[string]any{
		"alert": model.Alert{ID: id, Ts: now, Level: level, Source: source, Target: target, Message: message},
	})
	// Webhook saliente (async, cola acotada + DLQ): encola y sigue.
	if a.wh != nil {
		a.wh.Notify(webhook.Event{
			ID: id, Ts: now, Level: level, Source: source, Target: target, Message: message,
		})
	}
	// Push (app cerrada): fire-and-forget; el sender decide a quién y nunca falla.
	if a.push != nil && kind != "" {
		go a.push.Notify(context.Background(),
			push.Alert{Level: level, Source: source, Target: target, Kind: kind, Params: params})
	}
}

// EvaluatePools aplica umbrales de capacidad y scrub con errores.
func (a *Alerter) EvaluatePools(ctx context.Context, pools []model.Pool) {
	st, err := a.st.Load(ctx)
	if err != nil {
		st = settings.Defaults()
	}
	for _, p := range pools {
		if p.TotalBytes == 0 {
			continue
		}
		pct := int(p.UsedBytes * 100 / p.TotalBytes)
		switch {
		case pct >= st.CapCritPct:
			a.RaiseKind(ctx, "crit", "pool."+p.Name, "pools:"+p.Name,
				fmt.Sprintf("Pool %s al %d%% de capacidad (crítico ≥ %d%%)", p.Name, pct, st.CapCritPct),
				"pool_capacity", map[string]any{"pool": p.Name, "pct": pct, "threshold": st.CapCritPct})
		case pct >= st.CapWarnPct:
			a.RaiseKind(ctx, "warn", "pool."+p.Name, "pools:"+p.Name,
				fmt.Sprintf("Pool %s al %d%% de capacidad (aviso ≥ %d%%)", p.Name, pct, st.CapWarnPct),
				"pool_capacity", map[string]any{"pool": p.Name, "pct": pct, "threshold": st.CapWarnPct})
		}
		if p.Status == "DEGRADED" {
			a.RaiseKind(ctx, "crit", "pool."+p.Name, "pools:"+p.Name, "Pool "+p.Name+" DEGRADED",
				"pool_status", map[string]any{"pool": p.Name, "status": "DEGRADED"})
		} else if p.Status == "FAULTED" {
			a.RaiseKind(ctx, "crit", "pool."+p.Name, "pools:"+p.Name, "Pool "+p.Name+" FAULTED",
				"pool_status", map[string]any{"pool": p.Name, "status": "FAULTED"})
		}
		// kind "trim" no aplica: sus "errores" no son errores de datos.
		if st.NotifyScrubErrors && p.Scrub.State == "done" && p.Scrub.Errors > 0 && p.Scrub.Kind != "trim" {
			a.RaiseKind(ctx, "warn", "scrub."+p.Name, "pools:"+p.Name,
				fmt.Sprintf("Scrub de %s terminó con %d errores", p.Name, p.Scrub.Errors),
				"scrub_errors", map[string]any{"pool": p.Name, "errors": p.Scrub.Errors})
		}
	}
}

// EvaluateDisks aplica umbrales de temperatura y estado SMART.
func (a *Alerter) EvaluateDisks(ctx context.Context, disks []model.Disk) {
	st, err := a.st.Load(ctx)
	if err != nil {
		st = settings.Defaults()
	}
	for _, d := range disks {
		if d.TempC != nil && int(*d.TempC) >= st.DiskTempC {
			a.RaiseKind(ctx, "warn", "disk."+d.Dev, "disks:"+d.Dev,
				fmt.Sprintf("Disco %s a %.0f °C (umbral %d °C)", d.Dev, *d.TempC, st.DiskTempC),
				"disk_temp", map[string]any{"dev": d.Dev, "temp": int(*d.TempC), "threshold": st.DiskTempC})
		}
		if !st.NotifySmartChange {
			continue
		}
		switch d.Smart {
		case "crit":
			a.RaiseKind(ctx, "crit", "smart."+d.Dev, "disks:"+d.Dev,
				fmt.Sprintf("SMART crítico en %s: %s", d.Dev, d.SmartDetail),
				"smart_status", map[string]any{"dev": d.Dev, "detail": d.SmartDetail})
		case "warn":
			a.RaiseKind(ctx, "warn", "smart."+d.Dev, "disks:"+d.Dev,
				fmt.Sprintf("SMART con avisos en %s: %s", d.Dev, d.SmartDetail),
				"smart_status", map[string]any{"dev": d.Dev, "detail": d.SmartDetail})
		}
	}
}

// List devuelve las últimas alertas (limit), más recientes primero.
func (a *Alerter) List(ctx context.Context, limit int) ([]model.Alert, error) {
	rows, err := a.db.QueryContext(ctx,
		"SELECT id, ts, level, source, target, message, acked_at IS NOT NULL FROM alerts ORDER BY id DESC LIMIT ?",
		limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Alert{}
	for rows.Next() {
		var al model.Alert
		var ts string
		if err := rows.Scan(&al.ID, &ts, &al.Level, &al.Source, &al.Target, &al.Message, &al.Acked); err != nil {
			return nil, err
		}
		al.Ts = parseTS(ts)
		out = append(out, al)
	}
	return out, rows.Err()
}

// Ack marca una alerta como reconocida.
func (a *Alerter) Ack(ctx context.Context, id int64) error {
	_, err := a.db.ExecContext(ctx,
		"UPDATE alerts SET acked_at=? WHERE id=? AND acked_at IS NULL",
		time.Now().UTC().Format(time.RFC3339), id)
	return err
}

// parseTS tolera RFC3339 (desde Go) y 'YYYY-MM-DD HH:MM:SS' (defaults SQLite).
func parseTS(s string) time.Time {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02 15:04:05", s); err == nil {
		return t.UTC()
	}
	return time.Time{}
}
