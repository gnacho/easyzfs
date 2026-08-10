// Package scheduler — jobs programados (snapshot/scrub/trim/smart_short/smart_long):
// cálculo de next_run, ejecución y historial en tabla.
package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"

	"easyzfs/internal/actions"
	"easyzfs/internal/hub"
	"easyzfs/internal/model"
)

// Job — contrato GET /api/jobs (next_run se calcula en el handler).
type Job struct {
	ID         int64      `json:"id"`
	Tipo       string     `json:"tipo"` // "snapshot" | "scrub" | "trim" | "smart_short" | "smart_long"
	Target     string     `json:"target"`
	Schedule   string     `json:"schedule"`
	Retention  string     `json:"retention"`
	Enabled    bool       `json:"enabled"`
	LastRun    *time.Time `json:"last_run"`
	LastResult string     `json:"last_result"`
	NextRun    *time.Time `json:"next_run,omitempty"`
	CreatedAt  time.Time  `json:"-"` // base de planificación si nunca se ejecutó
}

// HistoryEntry — contrato GET /api/jobs/history.
type HistoryEntry struct {
	Ts     time.Time `json:"ts"`
	Tipo   string    `json:"tipo"`
	Target string    `json:"target"`
	OK     bool      `json:"ok"`
	Detail string    `json:"detail"`
}

// ValidTipos — tipos de job admitidos.
var ValidTipos = map[string]bool{
	"snapshot": true, "scrub": true, "trim": true, "smart_short": true, "smart_long": true,
}

// ErrNotFound — job inexistente.
var ErrNotFound = errors.New("job no encontrado")

// Store — persistencia de jobs e historial.
type Store struct {
	db *sql.DB
}

// NewStore crea el store de jobs.
func NewStore(d *sql.DB) *Store {
	return &Store{db: d}
}

// List devuelve todos los jobs.
func (st *Store) List(ctx context.Context) ([]Job, error) {
	rows, err := st.db.QueryContext(ctx,
		"SELECT id, tipo, target, schedule, COALESCE(retention,''), enabled, last_run, COALESCE(last_result,''), created_at FROM jobs ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Job{}
	for rows.Next() {
		var j Job
		var en int
		var last sql.NullString
		var created string
		if err := rows.Scan(&j.ID, &j.Tipo, &j.Target, &j.Schedule, &j.Retention, &en, &last, &j.LastResult, &created); err != nil {
			return nil, err
		}
		j.Enabled = en != 0
		if last.Valid && last.String != "" {
			t := parseTS(last.String)
			j.LastRun = &t
		}
		j.CreatedAt = parseTS(created)
		out = append(out, j)
	}
	return out, rows.Err()
}

// Get devuelve un job por id.
func (st *Store) Get(ctx context.Context, id int64) (*Job, error) {
	jobs, err := st.List(ctx)
	if err != nil {
		return nil, err
	}
	for i := range jobs {
		if jobs[i].ID == id {
			return &jobs[i], nil
		}
	}
	return nil, ErrNotFound
}

// Create inserta un job y devuelve su id.
func (st *Store) Create(ctx context.Context, j *Job) (int64, error) {
	res, err := st.db.ExecContext(ctx,
		"INSERT INTO jobs(tipo, target, schedule, retention, enabled) VALUES (?,?,?,?,1)",
		j.Tipo, j.Target, j.Schedule, j.Retention)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Update aplica cambios parciales (enabled/schedule/retention).
func (st *Store) Update(ctx context.Context, id int64, enabled *bool, schedule, retention *string) error {
	if enabled != nil {
		en := 0
		if *enabled {
			en = 1
		}
		if _, err := st.db.ExecContext(ctx, "UPDATE jobs SET enabled=? WHERE id=?", en, id); err != nil {
			return err
		}
	}
	if schedule != nil {
		if _, err := ParseSchedule(*schedule); err != nil {
			return err
		}
		if _, err := st.db.ExecContext(ctx, "UPDATE jobs SET schedule=? WHERE id=?", *schedule, id); err != nil {
			return err
		}
	}
	if retention != nil {
		if _, err := st.db.ExecContext(ctx, "UPDATE jobs SET retention=? WHERE id=?", *retention, id); err != nil {
			return err
		}
	}
	return nil
}

// Delete elimina un job.
func (st *Store) Delete(ctx context.Context, id int64) error {
	res, err := st.db.ExecContext(ctx, "DELETE FROM jobs WHERE id=?", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkRun registra el resultado de la última ejecución.
func (st *Store) MarkRun(ctx context.Context, id int64, ts time.Time, result string) error {
	_, err := st.db.ExecContext(ctx,
		"UPDATE jobs SET last_run=?, last_result=? WHERE id=?",
		ts.UTC().Format(time.RFC3339), result, id)
	return err
}

// RecordHistory añade una entrada al historial.
func (st *Store) RecordHistory(ctx context.Context, jobID int64, tipo, target string, ok bool, detail string) error {
	okInt := 0
	if ok {
		okInt = 1
	}
	_, err := st.db.ExecContext(ctx,
		"INSERT INTO job_history(ts, job_id, tipo, target, ok, detail) VALUES (?,?,?,?,?,?)",
		time.Now().UTC().Format(time.RFC3339), jobID, tipo, target, okInt, detail)
	return err
}

// History devuelve las últimas entradas del historial.
func (st *Store) History(ctx context.Context, limit int) ([]HistoryEntry, error) {
	rows, err := st.db.QueryContext(ctx,
		"SELECT ts, tipo, target, ok, COALESCE(detail,'') FROM job_history ORDER BY id DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HistoryEntry{}
	for rows.Next() {
		var e HistoryEntry
		var ts string
		var ok int
		if err := rows.Scan(&ts, &e.Tipo, &e.Target, &ok, &e.Detail); err != nil {
			return nil, err
		}
		e.Ts = parseTS(ts)
		e.OK = ok != 0
		out = append(out, e)
	}
	return out, rows.Err()
}

// Scheduler ejecuta los jobs cuando toca.
type Scheduler struct {
	store   *Store
	actions *actions.Service
	h       *hub.Hub
	disks   func() []model.Disk // caché de discos para smart_* con target 'all'
}

// New crea el scheduler.
func New(st *Store, act *actions.Service, h *hub.Hub, disks func() []model.Disk) *Scheduler {
	return &Scheduler{store: st, actions: act, h: h, disks: disks}
}

// Run — tick cada 30 s: ejecuta los jobs habilitados cuya hora ya pasó.
func (s *Scheduler) Run(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.check(ctx)
		}
	}
}

// check busca jobs vencidos y los lanza.
func (s *Scheduler) check(ctx context.Context) {
	jobs, err := s.store.List(ctx)
	if err != nil {
		log.Printf("scheduler: %v", err)
		return
	}
	now := time.Now()
	for _, j := range jobs {
		if !j.Enabled {
			continue
		}
		// Base: última ejecución, o creación si nunca se ejecutó.
		base := j.CreatedAt
		if j.LastRun != nil {
			base = *j.LastRun
		}
		if base.IsZero() {
			base = now
		}
		next, err := NextRun(j.Schedule, base)
		if err != nil {
			log.Printf("scheduler: job %d schedule inválido: %v", j.ID, err)
			continue
		}
		if next.After(now) {
			continue // aún no toca
		}
		s.runJob(ctx, j)
	}
}

// RunNow ejecuta un job bajo demanda (POST /api/jobs/{id}/run) en segundo plano.
func (s *Scheduler) RunNow(ctx context.Context, id int64) error {
	j, err := s.store.Get(ctx, id)
	if err != nil {
		return err
	}
	go s.runJob(context.Background(), *j)
	return nil
}

// runJob ejecuta el job según su tipo y registra resultado + historial + SSE.
func (s *Scheduler) runJob(ctx context.Context, j Job) {
	ok := true
	detail := "ok"
	if err := s.execute(ctx, j); err != nil {
		ok = false
		detail = err.Error()
		log.Printf("scheduler: job %d (%s %s): %v", j.ID, j.Tipo, j.Target, err)
	}
	now := time.Now().UTC()
	result := "ok"
	if !ok {
		result = "error: " + detail
	}
	if err := s.store.MarkRun(ctx, j.ID, now, result); err != nil {
		log.Printf("scheduler: mark run: %v", err)
	}
	if err := s.store.RecordHistory(ctx, j.ID, j.Tipo, j.Target, ok, detail); err != nil {
		log.Printf("scheduler: history: %v", err)
	}
	s.h.Publish("job.finished", map[string]any{"id": j.ID, "ok": ok, "detail": detail})
}

// execute — la acción concreta de cada tipo de job.
func (s *Scheduler) execute(ctx context.Context, j Job) error {
	switch j.Tipo {
	case "snapshot":
		name := model.AutoSnapPrefix + time.Now().Format("20060102-1504")
		if err := s.actions.SnapshotCreate(ctx, "scheduler", j.Target, name, true); err != nil {
			return err
		}
		if j.Retention != "" {
			dur, err := ParseRetention(j.Retention)
			if err != nil {
				return err
			}
			n, err := s.actions.SnapshotPrune(ctx, "scheduler", j.Target, time.Now().Add(-dur))
			if err != nil {
				return fmt.Errorf("prune: %w", err)
			}
			if n > 0 {
				log.Printf("scheduler: %d snapshots viejos purgados de %s", n, j.Target)
			}
		}
		return nil
	case "scrub":
		return s.actions.Scrub(ctx, "scheduler", j.Target, "start")
	case "trim":
		return s.actions.Trim(ctx, "scheduler", j.Target)
	case "smart_short", "smart_long":
		testType := "short"
		if j.Tipo == "smart_long" {
			testType = "long"
		}
		if j.Target == "all" {
			if s.disks == nil {
				return fmt.Errorf("sin caché de discos disponible")
			}
			var firstErr error
			for _, d := range s.disks() {
				if d.Smart == "unknown" {
					continue
				}
				if err := s.actions.SmartTest(ctx, "scheduler", d.Dev, testType); err != nil && firstErr == nil {
					firstErr = err
				}
			}
			return firstErr
		}
		return s.actions.SmartTest(ctx, "scheduler", j.Target, testType)
	}
	return fmt.Errorf("tipo de job desconocido %q", j.Tipo)
}

// parseTS tolera RFC3339 y formato SQLite.
func parseTS(s string) time.Time {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02 15:04:05", s); err == nil {
		return t.UTC()
	}
	return time.Time{}
}
