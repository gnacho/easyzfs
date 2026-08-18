// Tipos del contrato API (ver docs/api-contract.md).
// Números en bytes enteros; fechas RFC3339 UTC.

export type Role = 'admin' | 'user';
export type Lang = 'auto' | 'es' | 'en';

export interface SessionUser {
  user: string;
  role: Role;
  language?: Lang; // ausente en respuestas viejas del server
  display_name?: string; // nombre visible (saludos); vacío = username
  email?: string;
  avatar?: string; // nombre del fichero de avatar; vacío = sin foto
}

export interface UserInfo {
  user: string;
  role: Role;
  language?: Lang;
  display_name?: string;
  email?: string;
  avatar?: string; // nombre del fichero de avatar; vacío = sin foto
  last_login: string;
  sessions: number;
}

export interface VersionInfo {
  name?: string; // nombre del producto ("EasyZFS")
  version: string;
  build: string;
  go: string;
  os_arch: string;
  uptime_sec: number;
  rss_bytes: number;
  db_bytes: number;
  db_path: string;
  zfs_version: string;
  demo: boolean;
  capabilities?: Capabilities; // ausente en respuestas viejas del server
  pendingUpdate?: { from: string; to: string } | null;
}

// Estado de actualización (GET /api/update/status). El apply lo ejecuta el
// servidor (descarga+valida y toca el flag para que easyzfs-update.path
// reinicie con el binario nuevo).
export interface UpdateStatus {
  current: string;
  latest: string;
  available: boolean;
  inProgress?: boolean;
  progress?: { step: string; percentage: number };
  restartConfigured?: boolean;
}

// Capacidades derivadas de la versión de OpenZFS (feature-gating, lote A).
export interface Capabilities {
  rewrite: boolean;          // zfs rewrite (Linux ≥ 2.3.4)
  raidz_expansion: boolean;  // ≥ 2.3
  scrub_all: boolean;        // zpool scrub -a (≥ 2.4)
  scrub_range: boolean;      // zpool scrub -S/-E (≥ 2.4)
  zarc_names: boolean;       // zarcsummary/zarcstat (≥ 2.4)
  json_output: boolean;      // --json en zpool/zfs (≥ 2.3)
  version: string;
}

export interface Settings {
  lang: Lang;
  cap_warn_pct: number;
  cap_crit_pct: number;
  disk_temp_c: number;
  webhook: string;
  notify_scrub_errors: boolean;
  notify_smart_change: boolean;
  demo_enabled: boolean;
  backup_enabled: boolean;
  backup_freq_hours: number;
  backup_retention_days: number;
}

// Copia de seguridad de la BD (GET /api/backup/status)
export interface BackupFile {
  file: string;
  ts: string;
  bytes: number;
}
export interface BackupStatus {
  enabled: boolean;
  freq_hours: number;
  retention_days: number;
  running: boolean;
  last: BackupFile | null;
  next_run: string | null;
  dir: string;
}

export type AlertLevel = 'info' | 'warn' | 'crit';
export interface Alert {
  id: number;
  ts: string;
  level: AlertLevel;
  source: string;
  message: string;
  acked: boolean;
  // Vista/recurso causante: "disks:nvme1n1" | "pools:tank" | "tasks" | "settings"
  target?: string;
}

export interface ActivityItem {
  ts: string;
  text: string;
  detail: string;
}

export interface Overview {
  pools_total: number;
  pools_online: number;
  cap_used_bytes: number;
  cap_total_bytes: number;
  snapshots_total: number;
  jobs_active: number;
  last_scrub: { pool: string; ts: string; errors: number };
  alerts: Alert[];
  activity: ActivityItem[];
}

export type PoolStatus = 'ONLINE' | 'DEGRADED' | 'FAULTED';
export type Topo = 'stripe' | 'mirror' | 'raidz1' | 'raidz2' | 'raidz3';
export interface ScrubInfo {
  state: 'none' | 'running' | 'done';
  kind?: 'scrub' | 'resilver' | 'trim' | 'expand' | '';
  pct: number;
  eta_sec: number;
  ts: string;
  errors: number;
  // Progreso en bytes (mejor esfuerzo del colector; ausente/0 si no se sabe)
  bytes_done?: number;
  bytes_total?: number;
}
export interface Vdev {
  dev: string;
  path?: string;
  role: string;
  status: string;
  temp_c: number;
  replacing?: boolean; // hijo de un 'replacing-N' (sustitución en curso)
}
export interface Pool {
  name: string;
  status: PoolStatus;
  topo: string;
  used_bytes: number;
  total_bytes: number;
  frag_pct: number;
  comp_ratio: number;
  scrub: ScrubInfo;
  vdevs: Vdev[];
  autotrim: boolean;    // TRIM continuo activado (SSD)
  checkpoint: boolean;  // checkpoint activo en el pool
  // Vdevs raidz del pool ("raidz2-0"…), objetivo de RAID-Z expansion (lote D)
  raidz_vdevs?: string[];
}

// Entrada de 'zpool history -i' (GET /api/pools/{name}/history).
export interface PoolHistoryEntry {
  ts: string;
  command: string;
  duration_sec?: number;
}

// GET /api/performance
export interface ArcStats {
  size_bytes: number;
  hit_pct: number;
}
export interface PoolPerf {
  name: string;
  read_bps: number;
  write_bps: number;
}
export interface Performance {
  arc: ArcStats | null; // null = sin fuente de stats ARC → ocultar la tarjeta
  pools: PoolPerf[];
}

// GET /api/series?source=&days=&points= (U2: sparklines históricos)
export interface SeriesPoint {
  ts: number; // epoch segundos
  value: number;
}
export interface SeriesResp {
  source: string;
  points: SeriesPoint[];
}

export type DatasetType = 'fs' | 'volume';
export interface Dataset {
  name: string;
  type: DatasetType;
  compression: string;
  used_bytes: number;
  avail_bytes: number;
  quota_bytes: number;
  mountpoint: string;
  encryption: string;  // valor efectivo: "off" | "aes-256-gcm" | …
  keystatus: string;   // "available" | "unavailable" | "-"
}

// Una propiedad de dataset (GET /api/datasets/{name}/properties).
export interface DatasetProp {
  name: string;
  value: string;
  source: string; // local | default | inherited | received | temporary | "-"
}
export interface DatasetPropsResp {
  name: string;
  properties: DatasetProp[];
}
// Agrupación para la UI: editables (whitelist backend), read-only, user props.
export type PropGroup = 'editable' | 'readonly' | 'user';

export interface Snapshot {
  name: string;
  full: string;
  ts: string;
  used_bytes: number;
  kind: 'auto' | 'manual';
}
export interface SnapshotGroup {
  dataset: string;
  snaps: Snapshot[];
}

// Entrada de 'zfs diff -FHt' (GET /api/snapshots/diff?from=&to=)
export interface DiffEntry {
  type: string;    // M, +, -, R
  path: string;
  new_path?: string;
}

export type JobType = 'snapshot' | 'scrub' | 'trim' | 'smart_short' | 'smart_long' | 'poweroff' | 'replication';

// Job de replicación ZFS send/recv (GET /api/replication).
export interface ReplicationJob {
  id: number;
  source: string;
  dest_type: 'local' | 'ssh';
  dest_dataset: string;
  host: string;
  user: string;
  port: number;
  raw: boolean;        // zfs send -w (obligatorio en datasets cifrados)
  force_full: boolean; // reinicia con envío completo destruyendo el destino al divergir
  schedule: string;
  enabled: boolean;
  last_bookmark: string;
  last_run: string | null;
  last_ok: boolean | null;
  last_error: string;
  next_run?: string;
}

export interface CreateReplicationReq {
  source: string;
  dest_type: 'local' | 'ssh';
  dest_dataset: string;
  host?: string;
  user?: string;
  port?: number;
  raw?: boolean;
  force_full?: boolean;
  schedule: string;
}
export interface UpdateReplicationReq {
  enabled?: boolean;
  schedule?: string;
  force_full?: boolean;
  raw?: boolean;
}
export interface ReplicationSSHKey {
  public_key: string;
  instructions: string;
}
export interface ReplicationTestResult {
  ok: boolean;
  remote_version?: string;
  error?: string;
}
export interface Job {
  id: number;
  tipo: JobType;
  target: string;
  schedule: string; // hourly@:15 | daily@06:00 | weekly:sun@03:00 | monthly:1@02:00
  retention: string;
  enabled: boolean;
  last_run: string;
  last_result: string;
  next_run: string;
}
export interface JobHistoryItem {
  ts: string;
  tipo: JobType;
  target: string;
  ok: boolean;
  detail: string;
}

export interface Disk {
  dev: string;
  model: string;
  serial: string;
  size_bytes: number;
  temp_c: number | null; // null = sensor no disponible (p. ej. eMMC)
  smart: 'ok' | 'warn' | 'crit' | 'unknown'; // unknown = SMART no disponible
  smart_detail: string;
  realloc_sectors?: number;
  pending_sectors?: number;
  offline_uncorr?: number; // attr 198: sectores incorregibles offline
  crc_errors?: number; // attr 199: errores UDMA CRC (cable/puerto SATA)
  crc_recent?: number; // crecimiento de crc_errors desde la pasada anterior (lo accionable; el acumulado es de por vida)
  nvme_warn?: number;
  pool: string;
  in_use?: boolean; // particiones montadas o swap activo (no elegible como libre)
  hours: number;
  smart_full?: DiskSmartDetail; // U1: detalle completo (drill-down)
}

// U1 — detalle SMART completo (GET /api/disks/{dev}/smart y /smart-log).
export interface SmartAttr {
  id: number;
  name: string;
  value: number;
  worst: number;
  thresh: number;
  raw: string;
  when_failed: string; // "-" = dentro de umbral; "Past" = superado
}
export interface SmartSelftest {
  type: string;
  status: string;
  lifetime_hours: number;
  percent: number;
}
export interface SmartErrorEntry {
  lifetime_hours?: number;
  error_type?: string;
  detail?: string;
}
export interface SmartErrorLog {
  count: number;
  entries?: SmartErrorEntry[];
}
export interface DiskSmartDetail {
  protocol: 'ata' | 'nvme' | '';
  attributes: SmartAttr[];
  selftests: SmartSelftest[];
  error_log: SmartErrorLog;
}
export interface DiskSmartResp {
  dev: string;
  model: string;
  serial: string;
  smart: Disk['smart'];
  smart_detail: string;
  hours: number;
  attributes: SmartAttr[];
}
export interface DiskSmartLogResp {
  dev: string;
  selftests: SmartSelftest[];
  error_log: SmartErrorLog;
}

// Respuesta de GET /api/system-timers: lista de tareas del sistema + si
// systemd está disponible como init (condiciona el botón "Cambiar" y la
// burbuja comparativa cron vs systemd).
export interface SystemTimersResp {
  timers: SystemTimer[];
  systemd_available: boolean;
}

// Tarea del sistema (GET /api/system-timers): timers de systemd y cron
export interface SystemTimer {
  source: 'systemd' | 'cron';
  name: string;
  schedule: string;
  next_run: string; // RFC3339 UTC; '' si no se conoce
  last_run?: string;
  command: string;
  origin?: string;
  line?: number;
  editable?: boolean;
}

// Operación larga (GET /api/longops y evento SSE longop.update): procesos
// desacoplados monitorizados por el runner del backend (zfs rewrite; en el
// futuro la replicación). El registro es en memoria (TTL 1 h en terminadas).
export type LongOpStatus = 'running' | 'done' | 'error' | 'canceled';
export interface LongOp {
  id: string;
  type: string; // "rewrite", futuro "replication"
  target: string;
  pid: number;
  started: string;
  ended?: string;
  status: LongOpStatus;
  error?: string;
  lines: string[];
}

// --- Peticiones ---
export interface CreatePoolReq { name: string; topo: Topo; disks: string[]; confirm: string }
export interface CreateDatasetReq {
  pool: string; name: string; type: DatasetType;
  compression: 'lz4' | 'zstd' | 'off';
  quota_bytes: number; volsize_bytes?: number;
  encryption?: boolean;   // cifrado nativo AES-256-GCM (lote D)
  passphrase?: string;    // solo si encryption; viaja en el body, jamás en URL
}
export interface CreateSnapshotReq { dataset: string; name: string; recursive: boolean }
export interface CreateJobReq { tipo: JobType; target: string; schedule: string; retention?: string }
export interface UpdateJobReq { enabled?: boolean; schedule?: string; retention?: string }
export interface CreateUserReq { user: string; password: string; role: Role }

// --- Recomendaciones de discos (motor de reglas del backend) ---
export type RecKind = 'replace_now' | 'replace_soon' | 'watch' | 'check_cable' | 'crc_history';
export type RecHoldReason = 'resilver' | 'pool_degraded' | 'no_redundancy';

export interface Recommendation {
  level: 'crit' | 'warn' | 'info';
  kind: RecKind;
  dev: string;
  serial: string;
  pool: string;
  realloc_sectors?: number;
  pending_sectors?: number;
  offline_uncorr?: number;
  crc_errors?: number;
  crc_recent?: number;
  hold?: boolean;
  hold_reason?: RecHoldReason;
}

// --- Eventos SSE ---
export type AppEvent =
  | { type: 'pool.status'; name: string; status: PoolStatus }
  | { type: 'scrub.progress'; pool: string; pct: number; eta_sec: number; kind?: 'scrub' | 'resilver' | 'trim' | 'expand' | '' }
  | { type: 'disk.temp'; dev: string; temp_c: number }
  | { type: 'disk.smart'; dev: string; smart: Disk['smart']; smart_detail: string; realloc_sectors?: number; pending_sectors?: number; offline_uncorr?: number; crc_errors?: number; crc_recent?: number; nvme_warn?: number }
  | { type: 'alert.new'; alert: Alert }
  | { type: 'job.finished'; id: number; ok: boolean; detail: string }
  | { type: 'replication.finished'; id: number; ok: boolean; detail: string }
  | { type: 'longop.update'; op: LongOp }
  | { type: 'overview' };

// --- Notificaciones push (Web Push) ---
// Suscripción tal como la devuelve PushSubscription.toJSON() en el navegador.
export interface PushSubscriptionJSON {
  endpoint: string;
  expirationTime?: number | null;
  keys: { p256dh: string; auth: string };
}

// Tipos de alerta configurables (casan con notification_preferences.tipo y
// con el catálogo i18n del sender).
export type PushAlertTipo = 'pool_capacity' | 'pool_status' | 'scrub_errors' | 'disk_temp' | 'smart_status';

export interface PushPreference {
  tipo: PushAlertTipo;
  enabled: boolean;
}

// Horario silencioso: start/end null cuando está desactivado.
export interface PushQuietHours {
  enabled: boolean;
  start: number | null;
  end: number | null;
  tz: string;
}

export class ApiError extends Error {
  code: string;
  status: number;
  constructor(status: number, code: string, message: string) {
    super(message);
    this.status = status;
    this.code = code;
  }
}
