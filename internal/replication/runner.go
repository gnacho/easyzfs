// runner.go — ejecución de jobs de replicación vía el runner genérico
// internal/longops: pipeline 'zfs send | [ssh] zfs recv' como proceso
// desacoplado; el bookmark/prune posterior se hace aquí al terminar la op.
package replication

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"easyzfs/internal/executil"
	"easyzfs/internal/hub"
	"easyzfs/internal/longops"
	"easyzfs/internal/scheduler"
)

// SnapPrefix — prefijo de los snapshots creados por la replicación.
const SnapPrefix = "ezrepl-"

// BookmarkName — bookmark que marca la base del próximo incremental.
const BookmarkName = "ezrepl-last"

// Historial de jobs (scheduler.Store). Interfaz para no acoplar al tipo.
type HistoryRecorder interface {
	RecordHistory(ctx context.Context, jobID int64, tipo, target string, ok bool, detail string) error
}

// Runner — planificador + ejecutor de replication_jobs.
type Runner struct {
	store   *Store
	ops     *longops.Manager
	h       *hub.Hub
	hist    HistoryRecorder
	dataDir string // dir de datos del daemon (para ssh/)
	mock    bool   // MOCK=1: sin zfs/ssh reales (ops simuladas)

	inFlight   map[int64]struct{}
	inFlightMu sync.Mutex
	testGate   chan struct{} // solo tests de concurrencia: execute bloquea aquí si != nil
}

// NewRunner crea el runner de replicación.
func NewRunner(st *Store, ops *longops.Manager, h *hub.Hub, hist HistoryRecorder, dataDir string, mock bool) *Runner {
	return &Runner{store: st, ops: ops, h: h, hist: hist, dataDir: dataDir, mock: mock, inFlight: map[int64]struct{}{}}
}

// Store expone el store (handlers HTTP).
func (r *Runner) Store() *Store { return r.store }

// Target — etiqueta legible de la op en longops / historial.
func Target(j *Job) string {
	if j.DestType == "ssh" {
		return fmt.Sprintf("%s → %s@%s:%s", j.Source, j.User, j.Host, j.DestDataset)
	}
	return fmt.Sprintf("%s → %s", j.Source, j.DestDataset)
}

// ErrAlreadyRunning — ya hay una ejecución en curso de ese job (HTTP 409).
var ErrAlreadyRunning = errors.New("ya hay una replicación en curso para este job")

// TryAcquire reserva el slot del job de forma atómica. Si el slot ya está tomado
// devuelve false; si está libre lo toma y devuelve true. Release() lo libera.
// Cierra la ventana TOCTOU entre Running() y ops.Start() (auditoría 7-Ago-2026).
func (r *Runner) TryAcquire(id int64) bool {
	r.inFlightMu.Lock()
	defer r.inFlightMu.Unlock()
	if _, ok := r.inFlight[id]; ok {
		return false
	}
	r.inFlight[id] = struct{}{}
	return true
}

// Release libera el slot del job (se llama en defer desde execute).
func (r *Runner) Release(id int64) {
	r.inFlightMu.Lock()
	delete(r.inFlight, id)
	r.inFlightMu.Unlock()
}

// Running — ¿hay una op de replicación en curso para este job?
// (lectura informativa del registro de longops; la prevención de doble
// ejecución la hace TryAcquire, que es atómica).
func (r *Runner) Running(j *Job) bool {
	t := Target(j)
	for _, op := range r.ops.List() {
		if op.Type == "replication" && op.Target == t && op.Status == longops.StatusRunning {
			return true
		}
	}
	return false
}

// RunNow lanza la ejecución en segundo plano (POST /api/replication/{id}/run).
func (r *Runner) RunNow(ctx context.Context, id int64) error {
	j, err := r.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if !r.TryAcquire(j.ID) {
		return ErrAlreadyRunning
	}
	go r.execute(context.Background(), *j)
	return nil
}

// Run — tick cada 30 s: ejecuta los jobs habilitados cuya hora ya pasó
// (misma lógica de NextRun que internal/scheduler).
func (r *Runner) Run(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.check(ctx)
		}
	}
}

// check busca jobs vencidos y los lanza.
func (r *Runner) check(ctx context.Context) {
	jobs, err := r.store.List(ctx)
	if err != nil {
		log.Printf("replication: %v", err)
		return
	}
	now := time.Now()
	for _, j := range jobs {
		if !j.Enabled {
			continue
		}
		base := j.CreatedAt
		if j.LastRun != nil {
			base = *j.LastRun
		}
		if base.IsZero() {
			base = now
		}
		next, err := scheduler.NextRun(j.Schedule, base)
		if err != nil {
			log.Printf("replication: job %d schedule inválido: %v", j.ID, err)
			continue
		}
		if next.After(now) {
			continue
		}
		if !r.TryAcquire(j.ID) {
			continue
		}
		go r.execute(context.Background(), j)
	}
}

// execute — una ejecución completa: snapshot → pipeline send|recv (longop) →
// bookmark + prune (éxito) o retry completo / error claro (fallo incremental).
func (r *Runner) execute(ctx context.Context, j Job) {
	defer r.Release(j.ID)
	if r.testGate != nil {
		<-r.testGate
	}
	ok := false
	detail := ""
	bookmark := j.LastBookmark
	if err := r.run(ctx, &j); err != nil {
		detail = err.Error()
		log.Printf("replication: job %d (%s): %v", j.ID, Target(&j), err)
	} else {
		ok = true
		detail = "ok"
		bookmark = j.LastBookmark // run() la actualiza al snapshot nuevo
	}
	now := time.Now().UTC()
	if err := r.store.MarkRun(ctx, j.ID, now, ok, errMsg(ok, detail), bookmark); err != nil {
		log.Printf("replication: mark run: %v", err)
	}
	if r.hist != nil {
		if err := r.hist.RecordHistory(ctx, j.ID, "replication", Target(&j), ok, detail); err != nil {
			log.Printf("replication: history: %v", err)
		}
	}
	r.h.Publish("replication.finished", map[string]any{"id": j.ID, "ok": ok, "detail": detail})
	r.h.Publish("job.finished", map[string]any{"id": j.ID, "ok": ok, "detail": detail})
}

func errMsg(ok bool, detail string) string {
	if ok {
		return ""
	}
	return detail
}

// run — cuerpo de la ejecución; actualiza j.LastBookmark al snapshot nuevo en
// caso de éxito.
func (r *Runner) run(ctx context.Context, j *Job) error {
	if err := j.Validate(); err != nil {
		return err
	}
	if r.mock {
		return r.mockRun(ctx, j)
	}
	snap := SnapPrefix + time.Now().Format("20060102-150405")
	fullSnap := j.Source + "@" + snap
	if _, err := executil.Run(ctx, 60*time.Second, "zfs", "snapshot", fullSnap); err != nil {
		return fmt.Errorf("snapshot %s: %w", fullSnap, err)
	}
	incremental := j.LastBookmark != ""
	op, err := r.ops.Start("replication", Target(j), "bash", "-c", r.pipeline(j, fullSnap, incremental))
	if err != nil {
		return fmt.Errorf("lanzar replicación: %w", err)
	}
	res := r.waitOp(ctx, op.ID)
	if res.Status == longops.StatusDone {
		return r.postSuccess(ctx, j, snap)
	}
	if res.Status == longops.StatusCanceled {
		return errors.New("cancelada por el usuario")
	}
	sendErr := opError(res)
	if !incremental {
		return fmt.Errorf("envío completo falló: %s", sendErr)
	}
	// Incremental falló: posible divergencia (bookmark/snapshot ausente o
	// destino modificado). Sin force_full: error claro, sin tocar el destino.
	if !j.ForceFull {
		return fmt.Errorf("incremental falló (posible divergencia origen/destino); "+
			"activa force_full en el job para reiniciar con un envío completo destruyendo el destino. Detalle: %s", sendErr)
	}
	// Con force_full: destruir destino y reintentar completo UNA vez.
	log.Printf("replication: job %d: incremental falló (%s); reintentando completo con force_full", j.ID, sendErr)
	if err := r.destroyDest(ctx, j); err != nil {
		return fmt.Errorf("force_full: no se pudo destruir el destino: %w", err)
	}
	op2, err := r.ops.Start("replication", Target(j), "bash", "-c", r.pipeline(j, fullSnap, false))
	if err != nil {
		return fmt.Errorf("force_full: lanzar envío completo: %w", err)
	}
	res2 := r.waitOp(ctx, op2.ID)
	if res2.Status != longops.StatusDone {
		return fmt.Errorf("force_full: el envío completo también falló: %s", opError(res2))
	}
	return r.postSuccess(ctx, j, snap)
}

// postSuccess — tras un send|recv correcto: mover el bookmark ezrepl-last al
// snapshot nuevo y podar snapshots ezrepl-* viejos (conservar los 2 últimos).
func (r *Runner) postSuccess(ctx context.Context, j *Job, snap string) error {
	fullSnap := j.Source + "@" + snap
	bm := j.Source + "#" + BookmarkName
	// El bookmark anterior apunta a un snapshot distinto: se borra primero
	// (zfs bookmark no sobrescribe). Error ignorado: puede no existir aún.
	_, _ = executil.Run(ctx, 30*time.Second, "zfs", "destroy", bm)
	if _, err := executil.Run(ctx, 30*time.Second, "zfs", "bookmark", fullSnap, bm); err != nil {
		return fmt.Errorf("bookmark %s: %w", bm, err)
	}
	j.LastBookmark = snap
	r.prune(ctx, j)
	return nil
}

// prune conserva solo los 2 snapshots ezrepl-* más recientes del origen.
// Fallo no fatal: se registra y se sigue (la próxima ejecución lo reintenta).
func (r *Runner) prune(ctx context.Context, j *Job) {
	out, err := executil.Run(ctx, 30*time.Second, "zfs", "list", "-H", "-t", "snapshot", "-o", "name", j.Source)
	if err != nil {
		log.Printf("replication: prune list %s: %v", j.Source, err)
		return
	}
	prefix := j.Source + "@" + SnapPrefix
	var snaps []string
	for _, l := range strings.Split(string(out), "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, prefix) {
			snaps = append(snaps, l)
		}
	}
	sort.Strings(snaps) // el sufijo timestamp ordena cronológicamente
	for _, s := range snaps[:max(0, len(snaps)-2)] {
		if _, err := executil.Run(ctx, 60*time.Second, "zfs", "destroy", s); err != nil {
			log.Printf("replication: prune destroy %s: %v", s, err)
		}
	}
}

// destroyDest — 'zfs destroy -r' del destino (local o vía SSH) para force_full.
func (r *Runner) destroyDest(ctx context.Context, j *Job) error {
	if j.DestType == "ssh" {
		args := append(r.sshArgs(j), j.User+"@"+j.Host, "zfs", "destroy", "-r", j.DestDataset)
		_, err := executil.Run(ctx, 120*time.Second, "ssh", args...)
		return err
	}
	_, err := executil.Run(ctx, 120*time.Second, "zfs", "destroy", "-r", j.DestDataset)
	return err
}

// pipeline — 'set -o pipefail; zfs send -v … | [ssh …] zfs recv -s <dest>'.
// Todos los interpolados pasaron por las whitelists de Validate().
func (r *Runner) pipeline(j *Job, fullSnap string, incremental bool) string {
	send := "zfs send -v"
	if j.Raw {
		send += " -w"
	}
	if incremental {
		send += " -i " + j.Source + "#" + BookmarkName
	}
	send += " " + fullSnap
	recv := "zfs recv -s " + j.DestDataset
	if j.DestType == "ssh" {
		recv = "ssh " + strings.Join(r.sshArgs(j), " ") + " " +
			j.User + "@" + j.Host + " 'zfs recv -s " + j.DestDataset + "'"
	}
	return "set -o pipefail; " + send + " | " + recv
}

// waitOp espera a que la op termine (sondeo ligero; longops no tiene wait).
func (r *Runner) waitOp(ctx context.Context, id string) longops.Op {
	for {
		select {
		case <-ctx.Done():
			_ = r.ops.Cancel(id)
		case <-time.After(300 * time.Millisecond):
		}
		op, err := r.ops.Get(id)
		if err == nil && op.Status != longops.StatusRunning {
			return op
		}
	}
}

// opError — mensaje legible de una op fallida: exit status + última línea.
func opError(op longops.Op) string {
	msg := op.Error
	if n := len(op.Lines); n > 0 {
		last := strings.TrimSpace(op.Lines[n-1])
		if last != "" {
			msg = last + " (" + op.Error + ")"
		}
	}
	return msg
}

// mockRun — MOCK=1: sin zfs/ssh reales. Local: progreso y éxito; SSH: fallo
// de autenticación legible (permite capturas y smoke del camino de error).
func (r *Runner) mockRun(ctx context.Context, j *Job) error {
	snap := SnapPrefix + time.Now().Format("20060102-150405")
	var script string
	fail := false
	if j.DestType == "ssh" {
		fail = true
		script = fmt.Sprintf("echo '%s@%s: Permission denied (publickey).' ; sleep 2; exit 1", j.User, j.Host)
	} else {
		script = "echo 'send: 412 MiB / 1,2 GiB (34 %)…'; sleep 2; echo 'send: 1,2 GiB / 1,2 GiB (100 %)…'; sleep 1"
	}
	op, err := r.ops.Start("replication", Target(j), "bash", "-c", script)
	if err != nil {
		return err
	}
	res := r.waitOp(ctx, op.ID)
	if fail {
		return fmt.Errorf("ssh: Permission denied (publickey) — instala la clave pública del servidor en el destino (GET /api/replication/sshkey)")
	}
	if res.Status != longops.StatusDone {
		return fmt.Errorf("%s", opError(res))
	}
	j.LastBookmark = snap
	return nil
}
