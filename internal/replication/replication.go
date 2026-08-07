// Package replication — replicación ZFS send/recv (local y SSH), lote C.
//
// Modelo incremental con bookmarks:
//   - Cada ejecución crea un snapshot ezrepl-YYYYMMDD-HHMMSS en el origen.
//   - Primera vez: send completo. Siguientes: 'zfs send -i <src>#ezrepl-last'.
//   - Tras el éxito: el bookmark 'ezrepl-last' pasa a apuntar al snapshot nuevo
//     y se podan los snapshots ezrepl-* viejos (se conservan los 2 más recientes).
//   - Si el incremental falla (divergencia): con force_full se destruye el
//     destino y se reintenta completo UNA vez; sin force_full, error claro.
//
// Seguridad: todo lo que se interpola en el pipeline de shell (datasets, host,
// usuario, puerto) pasa por whitelist estricta (Validate*) antes de lanzarse.
package replication

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"time"
)

// ErrNotFound — job de replicación inexistente.
var ErrNotFound = errors.New("job de replicación no encontrado")

// Job — contrato GET /api/replication.
type Job struct {
	ID           int64      `json:"id"`
	Source       string     `json:"source"`
	DestType     string     `json:"dest_type"` // "local" | "ssh"
	DestDataset  string     `json:"dest_dataset"`
	Host         string     `json:"host"`
	User         string     `json:"user"`
	Port         int        `json:"port"`
	Raw          bool       `json:"raw"`        // zfs send -w (cifrados)
	ForceFull    bool       `json:"force_full"` // permite destruir destino al divergir
	Schedule     string     `json:"schedule"`
	Enabled      bool       `json:"enabled"`
	LastBookmark string     `json:"last_bookmark"`
	LastRun      *time.Time `json:"last_run"`
	LastOK       *bool      `json:"last_ok"`
	LastError    string     `json:"last_error"`
	NextRun      *time.Time `json:"next_run,omitempty"`
	CreatedAt    time.Time  `json:"-"`
}

// Whitelists estrictas: lo que toca el shell del pipeline send|recv.
// Cualquier cosa fuera del patrón se rechaza ANTES de construir el comando.
var (
	reDataset = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.\-/]*$`)
	reSSHUser = regexp.MustCompile(`^[a-z_][a-z0-9_-]*$`)
	reSSHHost = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.\-]*$`)
)

// ValidateDataset — nombre de dataset/pool (sin espacios ni metacaracteres).
func ValidateDataset(name string) error {
	if name == "" || len(name) > 256 || !reDataset.MatchString(name) {
		return fmt.Errorf("nombre de dataset inválido %q", name)
	}
	return nil
}

// ValidateSSHUser — usuario SSH (sintaxis de login Unix).
func ValidateSSHUser(u string) error {
	if u == "" || len(u) > 32 || !reSSHUser.MatchString(u) {
		return fmt.Errorf("usuario SSH inválido %q", u)
	}
	return nil
}

// ValidateSSHHost — host SSH (FQDN o IP literal; sin espacios ni opciones).
func ValidateSSHHost(h string) error {
	if h == "" || len(h) > 253 || !reSSHHost.MatchString(h) {
		return fmt.Errorf("host SSH inválido %q", h)
	}
	return nil
}

// ValidateSSHPort — puerto TCP 1-65535.
func ValidateSSHPort(p int) error {
	if p < 1 || p > 65535 {
		return fmt.Errorf("puerto SSH inválido %d", p)
	}
	return nil
}

// Validate — valida todos los campos del job antes de crearlo/ejecutarlo.
func (j *Job) Validate() error {
	if err := ValidateDataset(j.Source); err != nil {
		return err
	}
	if err := ValidateDataset(j.DestDataset); err != nil {
		return err
	}
	switch j.DestType {
	case "local":
		// sin más campos
	case "ssh":
		if err := ValidateSSHHost(j.Host); err != nil {
			return err
		}
		if err := ValidateSSHUser(j.User); err != nil {
			return err
		}
		if err := ValidateSSHPort(j.Port); err != nil {
			return err
		}
	default:
		return fmt.Errorf("dest_type inválido %q (local|ssh)", j.DestType)
	}
	return nil
}

// Store — persistencia de replication_jobs.
type Store struct {
	db *sql.DB
}

// NewStore crea el store.
func NewStore(d *sql.DB) *Store { return &Store{db: d} }

// List devuelve todos los jobs de replicación.
func (st *Store) List(ctx context.Context) ([]Job, error) {
	rows, err := st.db.QueryContext(ctx,
		`SELECT id, source, dest_type, dest_dataset, host, user, port, raw, force_full,
		        schedule, enabled, last_bookmark, last_run, last_ok, last_error, created_at
		 FROM replication_jobs ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Job{}
	for rows.Next() {
		var j Job
		var raw, ff, en int
		var lastRun, lastOK sql.NullString
		var created string
		if err := rows.Scan(&j.ID, &j.Source, &j.DestType, &j.DestDataset, &j.Host, &j.User,
			&j.Port, &raw, &ff, &j.Schedule, &en, &j.LastBookmark, &lastRun, &lastOK,
			&j.LastError, &created); err != nil {
			return nil, err
		}
		j.Raw = raw != 0
		j.ForceFull = ff != 0
		j.Enabled = en != 0
		if lastRun.Valid && lastRun.String != "" {
			t := parseTS(lastRun.String)
			j.LastRun = &t
		}
		if lastOK.Valid && lastOK.String != "" {
			ok := lastOK.String == "1"
			j.LastOK = &ok
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
		`INSERT INTO replication_jobs(source, dest_type, dest_dataset, host, user, port, raw, force_full, schedule, enabled)
		 VALUES (?,?,?,?,?,?,?,?,?,1)`,
		j.Source, j.DestType, j.DestDataset, j.Host, j.User, j.Port,
		boolInt(j.Raw), boolInt(j.ForceFull), j.Schedule)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Update aplica cambios parciales (enabled/schedule/force_full/raw).
func (st *Store) Update(ctx context.Context, id int64, enabled, forceFull, raw *bool, schedule *string) error {
	if enabled != nil {
		if _, err := st.db.ExecContext(ctx,
			"UPDATE replication_jobs SET enabled=? WHERE id=?", boolInt(*enabled), id); err != nil {
			return err
		}
	}
	if forceFull != nil {
		if _, err := st.db.ExecContext(ctx,
			"UPDATE replication_jobs SET force_full=? WHERE id=?", boolInt(*forceFull), id); err != nil {
			return err
		}
	}
	if raw != nil {
		if _, err := st.db.ExecContext(ctx,
			"UPDATE replication_jobs SET raw=? WHERE id=?", boolInt(*raw), id); err != nil {
			return err
		}
	}
	if schedule != nil {
		if _, err := st.db.ExecContext(ctx,
			"UPDATE replication_jobs SET schedule=? WHERE id=?", *schedule, id); err != nil {
			return err
		}
	}
	return nil
}

// Delete elimina un job (solo la definición: no toca snapshots ni el destino).
func (st *Store) Delete(ctx context.Context, id int64) error {
	res, err := st.db.ExecContext(ctx, "DELETE FROM replication_jobs WHERE id=?", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkRun registra el resultado de la última ejecución. bookmark = snapshot al
// que apunta 'ezrepl-last' tras el éxito (vacío si no aplica).
func (st *Store) MarkRun(ctx context.Context, id int64, ts time.Time, ok bool, errMsg, bookmark string) error {
	_, err := st.db.ExecContext(ctx,
		"UPDATE replication_jobs SET last_run=?, last_ok=?, last_error=?, last_bookmark=? WHERE id=?",
		ts.UTC().Format(time.RFC3339), boolInt(ok), errMsg, bookmark, id)
	return err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
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
