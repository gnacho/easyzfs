// Package db — apertura SQLite (modernc.org/sqlite), migraciones embebidas y purgas.
package db

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"os"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaV1 string

// migrations — SQL por versión, en orden. Aditivas siempre.
var migrations = []string{
	schemaV1,
	// v2: alertas con objetivo navegable (la UI enlaza a la vista afectada).
	`ALTER TABLE alerts ADD COLUMN target TEXT NOT NULL DEFAULT '';`,
	// v3: suscripciones Web Push (una fila por dispositivo/navegador; el
	// endpoint es una capability URL = secreto, UNIQUE para upsert).
	// users tiene PK TEXT (user), de ahí el tipo de user_id.
	`CREATE TABLE IF NOT EXISTS push_subscriptions (
	  id         INTEGER PRIMARY KEY AUTOINCREMENT,
	  user_id    TEXT NOT NULL REFERENCES users(user) ON DELETE CASCADE,
	  endpoint   TEXT NOT NULL UNIQUE,
	  p256dh     TEXT NOT NULL,
	  auth       TEXT NOT NULL,
	  lang       TEXT NOT NULL DEFAULT 'es',
	  user_agent TEXT,
	  created_at TEXT NOT NULL DEFAULT (datetime('now')),
	  updated_at TEXT NOT NULL DEFAULT (datetime('now'))
	);`,
	// v4: índice por usuario para los envíos y el borrado en cascada.
	`CREATE INDEX IF NOT EXISTS idx_push_subscriptions_user ON push_subscriptions(user_id);`,
	// v5: alertas con metadatos estructurados (JSON {"kind":"...","params":{...}})
	// para el catálogo i18n del sender push; message sigue en español (compat UI).
	`ALTER TABLE alerts ADD COLUMN meta TEXT NOT NULL DEFAULT '';`,
	// v6: origin del navegador al suscribirse (window.location.origin). El
	// sender compone notification.navigate/url absolutas con él (Declarative
	// Web Push las exige); vacío = fallback a relativa.
	`ALTER TABLE push_subscriptions ADD COLUMN origin TEXT NOT NULL DEFAULT '';`,
	// v7: preferencias de notificación por usuario y tipo de alerta.
	// Sin fila para (user_id, tipo) = habilitado (default true).
	`CREATE TABLE IF NOT EXISTS notification_preferences (
	  user_id    TEXT NOT NULL REFERENCES users(user) ON DELETE CASCADE,
	  tipo       TEXT NOT NULL,
	  enabled    INTEGER NOT NULL DEFAULT 1,
	  created_at TEXT NOT NULL DEFAULT (datetime('now')),
	  updated_at TEXT NOT NULL DEFAULT (datetime('now')),
	  PRIMARY KEY (user_id, tipo)
	);`,
	// v8: quiet hours (horario silencioso) por usuario. NULL en quiet_start/
	// quiet_end = desactivado; la ventana es hora local en tz y puede cruzar
	// medianoche. Las críticas la atraviesan siempre.
	`CREATE TABLE IF NOT EXISTS notification_quiet_hours (
	  user_id     TEXT PRIMARY KEY REFERENCES users(user) ON DELETE CASCADE,
	  quiet_start INTEGER,
	  quiet_end   INTEGER,
	  tz          TEXT NOT NULL DEFAULT 'Europe/Madrid',
	  updated_at  TEXT NOT NULL DEFAULT (datetime('now'))
	);`,
	// v9: cola de entrega diferida: las alertas no críticas encoladas durante
	// quiet hours; el ticker del sender las envía al terminar la ventana.
	`CREATE TABLE IF NOT EXISTS notification_queue (
	  id         INTEGER PRIMARY KEY AUTOINCREMENT,
	  user_id    TEXT NOT NULL REFERENCES users(user) ON DELETE CASCADE,
	  tipo       TEXT NOT NULL,
	  severity   TEXT NOT NULL DEFAULT 'normal',
	  datos_json TEXT NOT NULL DEFAULT '{}',
	  created_at TEXT NOT NULL DEFAULT (datetime('now'))
	);`,
	// v10: índice para el vaciado de la cola por usuario.
	`CREATE INDEX IF NOT EXISTS idx_notification_queue_user ON notification_queue(user_id, tipo);`,
	// v11: idioma por usuario (skill webapp-shell: users.language = fuente de
	// verdad; localStorage del navegador solo es caché). 'auto' = navegador.
	`ALTER TABLE users ADD COLUMN language TEXT NOT NULL DEFAULT 'auto';`,
	// v12: replicación ZFS (send/recv local y SSH, lote C). Incremental con
	// bookmark 'ezrepl-last' (last_bookmark = snapshot al que apunta); raw=1
	// fuerza 'zfs send -w' (obligatorio en datasets cifrados); force_full=1
	// permite reiniciar la replicación destruyendo el destino si el
	// incremental diverge. schedule usa el formato de jobs (daily@06:00…).
	`CREATE TABLE IF NOT EXISTS replication_jobs (
	  id            INTEGER PRIMARY KEY,
	  source        TEXT NOT NULL,
	  dest_type     TEXT NOT NULL CHECK(dest_type IN ('local','ssh')),
	  dest_dataset  TEXT NOT NULL,
	  host          TEXT NOT NULL DEFAULT '',
	  user          TEXT NOT NULL DEFAULT '',
	  port          INTEGER NOT NULL DEFAULT 22,
	  raw           INTEGER NOT NULL DEFAULT 0,
	  force_full    INTEGER NOT NULL DEFAULT 0,
	  schedule      TEXT NOT NULL,
	  enabled       INTEGER NOT NULL DEFAULT 1,
	  last_bookmark TEXT NOT NULL DEFAULT '',
	  last_run      TEXT,
	  last_ok       INTEGER,
	  last_error    TEXT NOT NULL DEFAULT '',
	  created_at    TEXT NOT NULL DEFAULT (datetime('now'))
	);`,
	// v13: nombre visible del usuario (saludos en la app; el login sigue
	// siendo 'user'). Vacío = mostrar el username.
	`ALTER TABLE users ADD COLUMN display_name TEXT NOT NULL DEFAULT '';`,
	// v14: email opcional del usuario (perfil; no se usa para envíos).
	`ALTER TABLE users ADD COLUMN email TEXT NOT NULL DEFAULT '';`,
	// v15: columna kind en alerts para deduplicar por (source, kind) y no por
	// mensaje exacto — los contadores volátiles (p. ej. CRC que crece cada
	// pasada) generaban una alerta+push nueva cada 10 min (bug 3-Ago-2026).
	`ALTER TABLE alerts ADD COLUMN kind TEXT NOT NULL DEFAULT '';`,
	// v16: avatar del usuario (foto de perfil). Nombre del fichero dentro de
	// <datadir>/avatars/ (p. ej. 'nacho.webp'); vacío = sin foto.
	`ALTER TABLE users ADD COLUMN avatar TEXT NOT NULL DEFAULT '';`,
	// v17: DLQ del webhook saliente (issue #18): eventos no entregados tras
	// agotar reintentos. event_id = id de la alerta (clave de idempotencia).
	`CREATE TABLE IF NOT EXISTS webhook_events (
	  event_id  TEXT PRIMARY KEY,
	  payload   TEXT NOT NULL,
	  sent_at   TEXT NOT NULL DEFAULT (datetime('now')),
	  error     TEXT NOT NULL
	);`,
	// v18: historial de actualizaciones (#28). Registra cada apply con
	// versión origen/destino, quién y estado.
	`CREATE TABLE IF NOT EXISTS update_history (
	  event_id     TEXT PRIMARY KEY,
	  timestamp    TEXT NOT NULL DEFAULT (datetime('now')),
	  action       TEXT NOT NULL DEFAULT 'update',
	  channel      TEXT NOT NULL DEFAULT 'stable',
	  version_from TEXT NOT NULL,
	  version_to   TEXT NOT NULL,
	  initiated_by TEXT,
	  status       TEXT NOT NULL DEFAULT 'started',
	  duration_ms  INTEGER,
	  notes        TEXT
	);
	CREATE INDEX IF NOT EXISTS idx_update_hist_ts ON update_history(timestamp);`,
	// v19: TOTP 2FA (#84). totp_secret es base32 del servidor (inicialmente el
	// secreto provisional al hacer setup, promovido a activo al confirmar);
	// totp_enabled=1 tras confirmar el primer código. Los recovery codes se
	// guardan hasheados (RecoveryHash), nunca en claro.
	`ALTER TABLE users ADD COLUMN totp_secret TEXT NOT NULL DEFAULT '';
	ALTER TABLE users ADD COLUMN totp_enabled INTEGER NOT NULL DEFAULT 0;
	CREATE TABLE IF NOT EXISTS totp_recovery (
	  user       TEXT NOT NULL REFERENCES users(user) ON DELETE CASCADE,
	  code_hash  TEXT NOT NULL,
	  used       INTEGER NOT NULL DEFAULT 0,
	  created_at TEXT NOT NULL DEFAULT (datetime('now')),
	  PRIMARY KEY (user, code_hash)
	);`,
	// v21: API keys de solo lectura (#87). key_hash es SHA-256 hex; la clave
	// en claro (ez_...) se muestra UNA vez al crearla y nunca se guarda.
	`CREATE TABLE IF NOT EXISTS api_keys (
	  id         INTEGER PRIMARY KEY AUTOINCREMENT,
	  name       TEXT NOT NULL,
	  key_hash   TEXT NOT NULL UNIQUE,
	  created_at TEXT NOT NULL DEFAULT (datetime('now')),
	  last_used  TEXT
	);`,
}

// Open abre la BD con WAL, busy_timeout y una sola conexión escritora.
func Open(path string) (*sql.DB, error) {
	dsn := "file:" + path +
		"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)" +
		"&_pragma=synchronous(NORMAL)&_pragma=cache_size(-8192)"
	d, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	d.SetMaxOpenConns(1) // escrituras serializadas; lecturas rápidas en WAL
	if err := d.Ping(); err != nil {
		d.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	return d, nil
}

// Migrate aplica las migraciones pendientes en orden (cada una en su tx).
func Migrate(ctx context.Context, d *sql.DB) error {
	var version int
	err := d.QueryRowContext(ctx,
		"SELECT COALESCE(MAX(version),0) FROM migrations").Scan(&version)
	if err != nil {
		// La tabla migrations puede no existir todavía: la crea la migración 1.
		version = 0
	}
	for i, m := range migrations {
		v := i + 1
		if v <= version {
			continue
		}
		tx, err := d.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, m); err != nil {
			tx.Rollback()
			return fmt.Errorf("migración %d: %w", v, err)
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO migrations(version) VALUES (?)", v); err != nil {
			tx.Rollback()
			return fmt.Errorf("migración %d (registro): %w", v, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migración %d (commit): %w", v, err)
		}
	}
	return nil
}

// SizeBytes devuelve el tamaño total de la BD (main + WAL + SHM) para /api/version.
func SizeBytes(path string) int64 {
	var total int64
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if st, err := os.Stat(p); err == nil {
			total += st.Size()
		}
	}
	return total
}

// PurgeSeries borra series más viejas que retentionDays.
func PurgeSeries(ctx context.Context, d *sql.DB, retentionDays int) (int64, error) {
	res, err := d.ExecContext(ctx,
		"DELETE FROM series WHERE ts < datetime('now', '-' || ? || ' days')", retentionDays)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// PurgeSessions borra sesiones expiradas.
func PurgeSessions(ctx context.Context, d *sql.DB) error {
	_, err := d.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at < datetime('now')")
	return err
}

// PurgeAlerts borra alertas reconocidas más viejas que days días.
func PurgeAlerts(ctx context.Context, d *sql.DB, days int) error {
	_, err := d.ExecContext(ctx,
		"DELETE FROM alerts WHERE acked_at IS NOT NULL AND ts < datetime('now', '-' || ? || ' days')", days)
	return err
}

// PurgeJobHistory borra historial de jobs más viejo que days días.
func PurgeJobHistory(ctx context.Context, d *sql.DB, days int) error {
	_, err := d.ExecContext(ctx,
		"DELETE FROM job_history WHERE ts < datetime('now', '-' || ? || ' days')", days)
	return err
}

// Checkpoint hace wal_checkpoint(TRUNCATE) (mantenimiento semanal).
func Checkpoint(ctx context.Context, d *sql.DB) error {
	_, err := d.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
	return err
}
