// Package longops — runner genérico de procesos largos (zfs rewrite y la
// replicación zfs send/recv del lote C).
//
// Diseño (pensando en reuso por replicación):
//   - Start(tipo, target, name, args...) lanza CUALQUIER proceso desacoplado:
//     el contexto es propio de la op (NO el de la request HTTP que lo creó,
//     que moriría al responder), la salida combinada stdout+stderr se captura
//     línea a línea (anillo con las últimas N) y el ciclo de vida se registra
//     en memoria.
//   - Registro en memoria: {id, tipo, target, pid, started, ended, status
//     (running/done/error/canceled), error, lines}. Cancel(id) mata el proceso.
//   - Las entradas terminadas se purgan a la hora (TTL). NO hay persistencia:
//     si el daemon reinicia, las ops en curso quedan huérfanas en el sistema
//     (zfs rewrite sigue su curso; EasyZFS ya no las ve). Decisión deliberada
//     — se documenta en el contrato.
//   - Cada cambio de estado publica el evento SSE "longop.update" {op}.
package longops

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"easyzfs/internal/executil"
	"easyzfs/internal/hub"
)

// Estados del ciclo de vida de una operación larga.
const (
	StatusRunning  = "running"
	StatusDone     = "done"
	StatusError    = "error"
	StatusCanceled = "canceled"
)

const (
	// doneTTL — cuánto se conserva una op terminada en el registro.
	doneTTL = time.Hour
	// maxLines — anillo de salida (últimas N líneas visibles en la UI).
	maxLines = 50
)

// Errores de dominio.
var (
	ErrNotFound   = errors.New("operación no encontrada")
	ErrNotRunning = errors.New("la operación ya no está en curso")
)

// Op — una operación larga (contrato GET /api/longops y evento longop.update).
type Op struct {
	ID      string     `json:"id"`
	Type    string     `json:"type"` // "rewrite", "replication"
	Target  string     `json:"target"`
	PID     int        `json:"pid"`
	Started time.Time  `json:"started"`
	Ended   *time.Time `json:"ended,omitempty"`
	Status  string     `json:"status"` // running | done | error | canceled
	Error   string     `json:"error,omitempty"`
	Lines   []string   `json:"lines"` // últimas líneas de salida (anillo)

	cancel      context.CancelFunc `json:"-"`
	canceledReq bool               `json:"-"` // Cancel() pidió matarla
}

// Manager — registro en memoria + lanzamiento/cancelación de procesos.
type Manager struct {
	h   *hub.Hub
	mu  sync.Mutex
	ops []*Op // más reciente primero
	seq int64
}

// New crea el runner.
func New(h *hub.Hub) *Manager { return &Manager{h: h} }

// Start lanza el proceso (con la política sudo de executil) y devuelve la op
// registrada. El proceso vive con contexto propio: sobrevive a la request.
func (m *Manager) Start(typ, target, name string, args ...string) (*Op, error) {
	ctx, cancel := context.WithCancel(context.Background())
	cmd := executil.NewCommand(ctx, name, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	cmd.Stderr = cmd.Stdout // salida combinada
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("lanzar %s: %w", name, err)
	}
	m.mu.Lock()
	m.seq++
	op := &Op{
		ID:   fmt.Sprintf("op-%d-%d", time.Now().Unix(), m.seq),
		Type: typ, Target: target, PID: cmd.Process.Pid,
		Started: time.Now().UTC(), Status: StatusRunning,
		Lines: []string{}, cancel: cancel,
	}
	m.ops = append([]*Op{op}, m.ops...)
	m.mu.Unlock()
	m.publish(op)

	go m.watch(op, cmd, stdout)
	return op, nil
}

// watch consume la salida línea a línea y cierra el ciclo de vida de la op.
func (m *Manager) watch(op *Op, cmd *exec.Cmd, stdout io.Reader) {
	scanDone := make(chan struct{})
	go func() {
		defer close(scanDone)
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			m.appendLine(op, sc.Text())
		}
	}()
	err := cmd.Wait()
	<-scanDone
	ended := time.Now().UTC()
	m.mu.Lock()
	op.Ended = &ended
	switch {
	case op.canceledReq:
		// La cancelación mata con SIGKILL ("signal: killed"), no con un error
		// de contexto: el estado lo decide la petición de cancelación.
		op.Status = StatusCanceled
	case err != nil:
		op.Status = StatusError
		op.Error = err.Error()
	default:
		op.Status = StatusDone
	}
	op.cancel = nil
	m.mu.Unlock()
	log.Printf("longops: %s %s terminó (%s)", op.Type, op.Target, op.Status)
	m.publish(op)
}

// appendLine añade una línea al anillo de salida (sin publicar SSE por línea:
// la UI refresca con longop.update de cambios de estado y al listar).
func (m *Manager) appendLine(op *Op, line string) {
	m.mu.Lock()
	op.Lines = append(op.Lines, line)
	if len(op.Lines) > maxLines {
		op.Lines = op.Lines[len(op.Lines)-maxLines:]
	}
	m.mu.Unlock()
}

// publish emite longop.update con una copia de la op.
func (m *Manager) publish(op *Op) {
	m.mu.Lock()
	cp := *op
	cp.Lines = append([]string{}, op.Lines...)
	cp.cancel = nil
	m.mu.Unlock()
	m.h.Publish("longop.update", map[string]any{"op": cp})
}

// List devuelve las ops (más reciente primero), purgando terminadas con más
// de doneTTL. Devuelve copias: la UI nunca toca el estado interno.
func (m *Manager) List() []Op {
	m.mu.Lock()
	defer m.mu.Unlock()
	cutoff := time.Now().Add(-doneTTL)
	keep := m.ops[:0]
	for _, op := range m.ops {
		if op.Ended != nil && op.Ended.Before(cutoff) {
			continue
		}
		keep = append(keep, op)
	}
	m.ops = keep
	out := make([]Op, 0, len(m.ops))
	for _, op := range m.ops {
		cp := *op
		cp.Lines = append([]string{}, op.Lines...)
		cp.cancel = nil
		out = append(out, cp)
	}
	return out
}

// Get devuelve una copia de la op por id (para seguir su ciclo de vida desde
// otro componente, p. ej. el runner de replicación que espera el fin del
// pipeline send|recv antes de hacer bookmark/prune).
func (m *Manager) Get(id string) (Op, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, op := range m.ops {
		if op.ID == id {
			cp := *op
			cp.Lines = append([]string{}, op.Lines...)
			cp.cancel = nil
			return cp, nil
		}
	}
	return Op{}, ErrNotFound
}

// Cancel mata el proceso de una op en curso (status → canceled al terminar).
func (m *Manager) Cancel(id string) error {
	m.mu.Lock()
	op := (*Op)(nil)
	for _, o := range m.ops {
		if o.ID == id {
			op = o
			break
		}
	}
	m.mu.Unlock()
	if op == nil {
		return ErrNotFound
	}
	m.mu.Lock()
	cancel := op.cancel
	if cancel != nil {
		op.canceledReq = true
	}
	m.mu.Unlock()
	if cancel == nil {
		return ErrNotRunning
	}
	// Matar el grupo de procesos completo (auditoría 7-Ago-2026):
	// cancel() solo mata el líder (bash/sudo); con Setpgid=true en
	// executil.NewCommand, Kill(-pgid) alcanza a todos los hijos del
	// pipeline (zfs send, ssh, etc.). Ignoramos ESRCH (ya murió solo).
	_ = syscall.Kill(-op.PID, syscall.SIGKILL)
	cancel()
	return nil
}

// RunningFor — ¿hay alguna op en curso sobre target? (indicador en la UI por
// fila; p.ej. un rewrite activo sobre un dataset concreto).
func (m *Manager) RunningFor(target string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, op := range m.ops {
		if op.Status == StatusRunning && op.Target == target {
			return true
		}
	}
	return false
}
