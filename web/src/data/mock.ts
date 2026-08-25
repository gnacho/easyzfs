// Provider mock: reproduce los datos del mockup validado y simula
// el progreso del scrub de "ssd" y variaciones de temperatura con eventos.
import type { DataProvider } from './provider';
import { emitEvent } from './events';
import { ApiError } from './types';
import { computeRecommendations } from './recs';
import type {
  Alert, BackupFile, BackupStatus, CreateDatasetReq, CreateJobReq, CreatePoolReq, CreateSnapshotReq, CreateUserReq,
  Dataset, DatasetProp, DatasetPropsResp, Disk, DiskSmartLogResp, DiskSmartResp, Job, JobHistoryItem, Lang, LoginResult, LongOp, Overview, Performance, Pool, PoolHistoryEntry, PushAlertTipo, SeriesPoint, SeriesResp, SessionUser, Settings, Snapshot, SmartSelftest,
  SnapshotGroup, SystemTimer, SystemTimersResp, TwoFARecovery, TwoFASetup, TwoFAStatus, UpdateJobReq, UserInfo, VersionInfo,
  APIKeyCreated, APIKeyInfo,
  CreateReplicationReq, ReplicationJob, ReplicationSSHKey, ReplicationTestResult, UpdateReplicationReq,
} from './types';

const GiB = 1024 ** 3;
const TiB = 1024 ** 4;
const DAY = 86400_000;

// Latencia simulada de red para que la UI muestre estados de carga
const delay = (ms = 160) => new Promise<void>((r) => setTimeout(r, ms));
const iso = (d: Date) => d.toISOString();
const daysAgo = (n: number, h = 6) => {
  const d = new Date(Date.now() - n * DAY);
  d.setHours(h, 0, 0, 0);
  return d;
};

// Genera snapshots automáticos diarios/semanales hacia atrás hasta llegar a `count`
function genSnaps(dataset: string, prefix: string, count: number, stepDays: number, sizeMiB: number): Snapshot[] {
  const out: Snapshot[] = [];
  for (let i = 0; i < count; i++) {
    const d = daysAgo(i * stepDays);
    const stamp = `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}_06-00`;
    out.push({
      name: `${prefix}-${stamp}`,
      full: `${dataset}@${prefix}-${stamp}`,
      ts: iso(d),
      used_bytes: Math.round(sizeMiB * 1024 ** 2 * (0.7 + ((i * 37) % 60) / 100)),
      kind: 'auto',
    });
  }
  return out;
}

export class MockProvider implements DataProvider {
  readonly isMock = true;

  private session: SessionUser | null = null;
  private timers: ReturnType<typeof setInterval>[] = [];
  private alertSeq = 100;

  private version: VersionInfo = {
    name: 'EasyZFS', version: '0.1.0', build: '2026-08-01', go: 'go1.23.4', os_arch: 'linux/amd64',
    uptime_sec: 17 * 86400 + 4 * 3600, rss_bytes: 21 * 1024 ** 2,
    db_bytes: Math.round(8.4 * 1024 ** 2), db_path: '/var/lib/easyzfs/app.db',
    zfs_version: '2.4.1', demo: true,
    capabilities: {
      rewrite: true, raidz_expansion: true, scrub_all: true,
      scrub_range: true, zarc_names: true, json_output: true, version: '2.4.1',
    },
  };

  private settings: Settings = {
    lang: 'auto', cap_warn_pct: 80, cap_crit_pct: 90, disk_temp_c: 45,
    webhook: '', notify_scrub_errors: true, notify_smart_change: true,
    demo_enabled: true,
    backup_enabled: true, backup_freq_hours: 24, backup_retention_days: 3,
  };

  private backupLast: BackupFile | null = {
    file: 'app-20260801-030000.db', ts: iso(daysAgo(1, 3)), bytes: 318 * 1024,
  };

  private users: UserInfo[] = [
    { user: 'admin', role: 'admin', language: 'auto', last_login: iso(daysAgo(0, 8)), sessions: 2 },
    { user: 'maria', role: 'user', language: 'es', last_login: iso(daysAgo(1, 21)), sessions: 1 },
  ];

  private pools: Pool[] = [
    {
      name: 'tank', status: 'DEGRADED', topo: 'raidz2 (4×1,86 TB NVMe)',
      used_bytes: Math.round(4.9 * TiB), total_bytes: Math.round(7.2 * TiB),
      frag_pct: 12, comp_ratio: 1.42,
      scrub: { state: 'done', pct: 100, eta_sec: 0, ts: iso(daysAgo(6, 2)), errors: 0 },
      vdevs: [
        { dev: 'nvme0n1', path: '/dev/nvme0n1', role: 'raidz2', status: 'ONLINE', temp_c: 48 },
        { dev: 'nvme1n1', path: '/dev/nvme1n1', role: 'raidz2', status: 'ONLINE', temp_c: 48 },
        { dev: '11111111-2222-3333-4444-555555555555', role: 'raidz2', status: 'FAULTED', temp_c: 0 },
      ],
      autotrim: false, checkpoint: true,
      raidz_vdevs: ['raidz2-0'], // objetivo del botón Expandir (RAID-Z expansion)
    },
    {
      name: 'ssd', status: 'ONLINE', topo: 'stripe (1×238 GB NVMe)',
      used_bytes: Math.round(0.31 * TiB), total_bytes: Math.round(0.93 * TiB),
      frag_pct: 4, comp_ratio: 1.18,
      scrub: { state: 'running', pct: 62, eta_sec: 12 * 60, ts: iso(daysAgo(0, 5)), errors: 0 },
      vdevs: [{ dev: 'nvme3n1', path: '/dev/nvme3n1', role: '—', status: 'ONLINE', temp_c: 55 }],
      autotrim: true, checkpoint: false,
    },
  ];

  private datasets: Dataset[] = [
    { name: 'tank/documentos', type: 'fs', compression: 'lz4', used_bytes: Math.round(1.2 * TiB), avail_bytes: Math.round(2.3 * TiB), quota_bytes: 0, mountpoint: '/tank/documentos', encryption: 'off', keystatus: '-' },
    { name: 'tank/fotos', type: 'fs', compression: 'lz4', used_bytes: Math.round(2.8 * TiB), avail_bytes: Math.round(2.3 * TiB), quota_bytes: Math.round(4 * TiB), mountpoint: '/tank/fotos', encryption: 'off', keystatus: '-' },
    { name: 'tank/backups', type: 'fs', compression: 'zstd', used_bytes: Math.round(0.9 * TiB), avail_bytes: Math.round(2.3 * TiB), quota_bytes: Math.round(1 * TiB), mountpoint: '/tank/backups', encryption: 'off', keystatus: '-' },
    // Cifrado nativo desbloqueado (clave cargada, montado)
    { name: 'tank/secretos', type: 'fs', compression: 'zstd', used_bytes: Math.round(42 * GiB), avail_bytes: Math.round(2.3 * TiB), quota_bytes: 0, mountpoint: '/tank/secretos', encryption: 'aes-256-gcm', keystatus: 'available' },
    // Cifrado nativo bloqueado (sin clave: no montado; se abre con Desbloquear)
    { name: 'tank/boveda', type: 'fs', compression: 'zstd', used_bytes: Math.round(512 * GiB), avail_bytes: Math.round(2.3 * TiB), quota_bytes: 0, mountpoint: '—', encryption: 'aes-256-gcm', keystatus: 'unavailable' },
    { name: 'ssd/vm-docker', type: 'volume', compression: 'lz4', used_bytes: Math.round(180 * GiB), avail_bytes: Math.round(640 * GiB), quota_bytes: 0, mountpoint: '—', encryption: 'off', keystatus: '-' },
    { name: 'ssd/lxc-cache', type: 'fs', compression: 'lz4', used_bytes: Math.round(42 * GiB), avail_bytes: Math.round(640 * GiB), quota_bytes: 0, mountpoint: '/ssd/lxc-cache', encryption: 'off', keystatus: '-' },
  ];

  // 148 snapshots en total repartidos por dataset
  private snaps: SnapshotGroup[] = [
    { dataset: 'tank/documentos', snaps: genSnaps('tank/documentos', 'auto', 61, 1, 110) },
    { dataset: 'tank/fotos', snaps: genSnaps('tank/fotos', 'auto', 53, 7, 2400) },
    { dataset: 'tank/backups', snaps: genSnaps('tank/backups', 'semanal', 30, 7, 300) },
    { dataset: 'ssd/vm-docker', snaps: genSnaps('ssd/vm-docker', 'auto', 4, 1, 900) },
  ];

  private jobs: Job[] = [
    { id: 1, tipo: 'snapshot', target: 'tank/documentos', schedule: 'daily@06:00', retention: '1m', enabled: true, last_run: iso(daysAgo(0, 6)), last_result: 'OK', next_run: iso(daysAgo(-1, 6)) },
    { id: 2, tipo: 'snapshot', target: 'tank/fotos', schedule: 'weekly:sun@03:00', retention: '3m', enabled: true, last_run: iso(daysAgo(5, 3)), last_result: 'OK', next_run: iso(daysAgo(-2, 3)) },
    { id: 3, tipo: 'snapshot', target: 'tank/backups', schedule: 'weekly:sun@04:00', retention: '1y', enabled: false, last_run: iso(daysAgo(12, 4)), last_result: 'OK', next_run: '' },
    { id: 4, tipo: 'scrub', target: 'tank', schedule: 'monthly:1@02:00', retention: '', enabled: true, last_run: iso(daysAgo(6, 2)), last_result: '0 errores (4h 12m)', next_run: iso(daysAgo(-2, 2)) },
    { id: 5, tipo: 'scrub', target: 'ssd', schedule: 'weekly:sun@05:00', retention: '', enabled: true, last_run: iso(daysAgo(0, 5)), last_result: 'en curso', next_run: iso(daysAgo(-14, 5)) },
    { id: 6, tipo: 'smart_short', target: 'all', schedule: 'weekly:sat@22:00', retention: '', enabled: true, last_run: iso(daysAgo(6, 22)), last_result: 'OK', next_run: iso(daysAgo(0, 22)) },
    { id: 7, tipo: 'smart_long', target: 'all', schedule: 'monthly:1@23:00', retention: '', enabled: true, last_run: iso(daysAgo(31, 23)), last_result: 'OK', next_run: iso(daysAgo(-31, 23)) },
  ];
  private jobSeq = 8;

  private history: JobHistoryItem[] = [
    { ts: iso(daysAgo(6, 2)), tipo: 'scrub', target: 'tank', ok: true, detail: '0 errores · 4h 12m' },
    { ts: iso(daysAgo(0, 6)), tipo: 'snapshot', target: 'tank/fotos', ok: true, detail: '2,4 GiB referenciados' },
    { ts: iso(daysAgo(1, 22)), tipo: 'smart_short', target: 'nvme0n1', ok: true, detail: 'completado sin errores' },
    { ts: iso(daysAgo(14, 5)), tipo: 'scrub', target: 'ssd', ok: false, detail: 'cancelado por el usuario al 31%' },
  ];

  // Discos físicos del caso real (tras filtrar loop/zvols): eMMC sin SMART ni
  // temperatura, un NVMe Samsung de sistema y tres ORICO de datos.
  private disks: Disk[] = [
    { dev: 'mmcblk0', model: 'eMMC 5.1 (64 GB)', serial: '—', size_bytes: Math.round(58.2 * GiB), temp_c: null, smart: 'unknown', smart_detail: '', pool: '—', hours: 0 },
    { dev: 'nvme3n1', by_id: 'nvme-Samsung_MZVLB256HAHQ_S417NB0K402133', model: 'Samsung MZVLB256HAHQ', serial: 'S417NB0K402133', size_bytes: Math.round(238 * GiB), temp_c: 55, smart: 'ok', smart_detail: 'PASSED', pool: 'ssd', hours: 1577 },
    { dev: 'nvme0n1', by_id: 'nvme-ORICO_NVMe_SSD_ORC2024A01', model: 'ORICO NVMe SSD', serial: 'ORC2024A01', size_bytes: Math.round(1.86 * TiB), temp_c: 48, smart: 'ok', smart_detail: 'PASSED', pool: 'tank', hours: 8725 },
    { dev: 'nvme1n1', by_id: 'nvme-ORICO_NVMe_SSD_ORC2024A02', model: 'ORICO NVMe SSD', serial: 'ORC2024A02', size_bytes: Math.round(1.86 * TiB), temp_c: 48, smart: 'ok', smart_detail: 'PASSED', pool: 'tank', hours: 8725 },
    { dev: 'nvme2n1', by_id: 'nvme-ORICO_NVMe_SSD_ORC2024A03', model: 'ORICO NVMe SSD', serial: 'ORC2024A03', size_bytes: Math.round(1.86 * TiB), temp_c: 48, smart: 'ok', smart_detail: 'PASSED', pool: '—', hours: 8725 },
    // Caso real: disco USB montado (en uso) — no debe ofrecerse como libre.
    // realloc 48 (vigilar) + CRC histórico de por vida congelado (se consulta
    // solo en la burbuja info, no genera recomendación).
    { dev: 'sda', by_id: 'usb-Seagate_Expansion_4TB_NAABC123-0:0', model: 'Seagate Expansion 4TB', serial: 'NAABC123', size_bytes: Math.round(3.64 * TiB), temp_c: 38, smart: 'warn', smart_detail: 'PASSED (realloc=48 pending=0)', realloc_sectors: 48, crc_errors: 200, crc_recent: 0, pool: '—', in_use: true, hours: 22100 },
  ];

  private systemTimers: SystemTimer[] = [
    { source: 'systemd', name: 'zfs-scrub-monthly@tank.timer', schedule: 'monthly', next_run: iso(daysAgo(-9, 2)), command: 'zfs-scrub-monthly@tank.service', editable: true },
    { source: 'systemd', name: 'logrotate', schedule: 'logrotate.timer · diario', next_run: iso(daysAgo(-1, 0)), command: '/usr/sbin/logrotate /etc/logrotate.conf' },
    { source: 'systemd', name: 'man-db', schedule: 'man-db.timer · diario', next_run: iso(daysAgo(-1, 3)), command: '/usr/bin/mandb --quiet' },
    { source: 'cron', name: 'Backup nocturno (crontab de root)', schedule: '30 3 * * *', next_run: iso(daysAgo(-1, 3)), command: '/root/bin/backup.sh --to tank/backups' },
    { source: 'cron', name: 'Trim semanal (zfsutils)', schedule: '0 0 * * 0', next_run: iso(daysAgo(-1, 1)), command: '/usr/sbin/zpool trim tank', origin: '/etc/cron.d/zfsutils-linux', line: 7, editable: true },
  ];

  private alerts: Alert[] = [
    { id: 1, ts: iso(daysAgo(2, 14)), level: 'crit', source: 'pool/tank', message: 'Pool tank DEGRADED', acked: false, target: 'pools:tank' },
    { id: 2, ts: iso(new Date()), level: 'info', source: 'scrub/ssd', message: 'Scrub de ssd en curso (62%)', acked: false, target: 'pools:ssd' },
    { id: 4, ts: iso(daysAgo(1, 3)), level: 'warn', source: 'cron/backup', message: 'El backup nocturno terminó con avisos · revisa /var/log/backup.log', acked: false, target: 'tasks' },
    { id: 3, ts: iso(daysAgo(5, 9)), level: 'crit', source: 'smartd/nvme1n1', message: 'smartd: nvme1n1 a 48 °C de forma sostenida · revisar ventilación', acked: false, target: 'disks:nvme1n1' },
  ];

  private activity = [
    { ts: iso(daysAgo(0, 6)), text: 'Snapshot automático creado', detail: 'tank/documentos@auto' },
    { ts: iso(daysAgo(0, 5)), text: 'Scrub iniciado en ssd', detail: 'programación quincenal' },
    { ts: iso(daysAgo(0, 2)), text: 'Inicio de sesión', detail: 'admin' },
    { ts: iso(daysAgo(1, 21)), text: 'Inicio de sesión', detail: 'maria' },
    { ts: iso(daysAgo(1, 19)), text: 'Cuota modificada', detail: 'tank/backups → 1 TiB' },
    { ts: iso(daysAgo(1, 9)), text: 'Respaldos automáticos', detail: 'cada 24 h · retención 3 días' },
    { ts: iso(daysAgo(2, 14)), text: 'Snapshot automático creado', detail: 'tank/documentos@auto' },
    { ts: iso(daysAgo(2, 8)), text: 'Scrub completado en tank', detail: '0 errores' },
    { ts: iso(daysAgo(3, 17)), text: 'Usuario creado', detail: 'maria · rol usuario' },
    { ts: iso(daysAgo(3, 11)), text: 'Contraseña cambiada', detail: 'admin' },
    { ts: iso(daysAgo(4, 3)), text: 'Alerta reconocida', detail: 'smartd/nvme1n1' },
    { ts: iso(daysAgo(5, 22)), text: 'Snapshot automático creado', detail: 'tank/documentos@auto' },
    { ts: iso(daysAgo(6, 15)), text: 'Inicio de sesión', detail: 'admin' },
    { ts: iso(daysAgo(7, 4)), text: 'Respaldo manual de la base de datos', detail: 'app-20260727-040000.db' },
    { ts: iso(daysAgo(8, 12)), text: 'Dataset creado', detail: 'tank/proyectos' },
    { ts: iso(daysAgo(9, 6)), text: 'Scrub completado en tank', detail: '0 errores' },
    { ts: iso(daysAgo(10, 18)), text: 'Inicio de sesión', detail: 'maria' },
    { ts: iso(daysAgo(11, 9)), text: 'Snapshot automático creado', detail: 'tank/documentos@auto' },
    { ts: iso(daysAgo(12, 2)), text: 'Cuota modificada', detail: 'tank/media → 4 TiB' },
    { ts: iso(daysAgo(13, 20)), text: 'Respaldo automático de la base de datos', detail: 'app-20260721-030000.db' },
  ];

  constructor() {
    // Simulación: progreso del scrub de ssd cada 2 s
    this.timers.push(setInterval(() => {
      const ssd = this.pools.find((p) => p.name === 'ssd');
      if (!ssd || ssd.scrub.state !== 'running') return;
      ssd.scrub.pct = Math.min(100, ssd.scrub.pct + 1);
      ssd.scrub.eta_sec = Math.max(0, ssd.scrub.eta_sec - 20);
      emitEvent({ type: 'scrub.progress', pool: 'ssd', pct: ssd.scrub.pct, eta_sec: ssd.scrub.eta_sec });
      if (ssd.scrub.pct >= 100) {
        ssd.scrub = { state: 'done', pct: 100, eta_sec: 0, ts: iso(new Date()), errors: 0 };
        const alert: Alert = {
          id: ++this.alertSeq, ts: iso(new Date()), level: 'info',
          source: 'scrub/ssd', message: 'Scrub de ssd completado · 0 errores', acked: false,
          target: 'pools:ssd',
        };
        this.alerts.unshift(alert);
        this.activity.unshift({ ts: alert.ts, text: 'Scrub completado en ssd', detail: '0 errores' });
        emitEvent({ type: 'alert.new', alert });
        emitEvent({ type: 'overview' });
      }
    }, 2000));

    // Simulación: variaciones leves de temperatura (solo discos con sensor)
    this.timers.push(setInterval(() => {
      const conTemp = this.disks.filter((d): d is Disk & { temp_c: number } => d.temp_c !== null);
      if (conTemp.length === 0) return;
      const d = conTemp[Math.floor(Math.random() * conTemp.length)];
      d.temp_c = Math.max(38, Math.min(56, d.temp_c + (Math.random() > 0.5 ? 1 : -1)));
      const v = this.pools.flatMap((p) => p.vdevs).find((x) => x.dev === d.dev);
      if (v) v.temp_c = d.temp_c;
      emitEvent({ type: 'disk.temp', dev: d.dev, temp_c: d.temp_c });
    }, 8000));

    // Simulación: evento ZFS en tiempo real (colector events / 'zpool events'):
    // a los 12 s llega una alerta zed.* como si hubiera saltado un ereport.
    this.timers.push(setTimeout(() => {
      const alert: Alert = {
        id: ++this.alertSeq, ts: iso(new Date()), level: 'crit',
        source: 'zed.ereport.fs.zfs.checksum',
        message: 'Errores de checksum en nvme1n1 (evento ZFS, pool tank)',
        acked: false, target: 'disks:nvme1n1',
      };
      this.alerts.unshift(alert);
      emitEvent({ type: 'alert.new', alert });
    }, 12000));
  }

  // Libera los temporizadores al salir del modo demo
  dispose() { this.timers.forEach(clearInterval); this.timers = []; }

  private totalSnaps() { return this.snaps.reduce((n, g) => n + g.snaps.length, 0); }

  // ---- Sistema ----
  getVersion = async () => { await delay(); return { ...this.version }; };
  getUpdateStatus = async () => { await delay(); return { current: this.version.version, latest: this.version.version, available: false, restartConfigured: true }; };
  getUpdatePlan = async () => { await delay(); return { canApply: true, checks: [{id:'disk_space',status:'pass',title:'Disk space',summary:'Enough space available.'}] }; };
  applyUpdate = async () => { await delay(); }; // demo: no-op
  getSettings = async () => { await delay(); return { ...this.settings }; };
  putSettings = async (s: Settings) => { await delay(); this.settings = { ...s }; };
  getActivity = async (limit?: number) => {
    await delay();
    return this.activity.slice(0, limit ?? 30).map((a) => ({ ...a }));
  };

  getBackupStatus = async (): Promise<BackupStatus> => {
    await delay();
    const s = this.settings;
    return {
      enabled: s.backup_enabled,
      freq_hours: s.backup_freq_hours,
      retention_days: s.backup_retention_days,
      running: false,
      last: this.backupLast,
      next_run: s.backup_enabled && this.backupLast
        ? iso(new Date(new Date(this.backupLast.ts).getTime() + s.backup_freq_hours * 3600e3))
        : null,
      dir: '/var/lib/easyzfs/backups',
    };
  };
  runBackup = async (): Promise<BackupFile> => {
    await delay(600);
    this.backupLast = {
      file: `app-${new Date().toISOString().slice(0, 10).replaceAll('-', '')}-000000.db`,
      ts: iso(new Date()), bytes: 320 * 1024,
    };
    return { ...this.backupLast };
  };
  importBackup = async (_f: File) => { await delay(800); };
  getAlerts = async () => { await delay(); return this.alerts.map((a) => ({ ...a })); };
  ackAlert = async (id: number) => {
    await delay();
    const a = this.alerts.find((x) => x.id === id);
    if (a) a.acked = true;
  };
  getOverview = async (): Promise<Overview> => {
    await delay();
    const used = this.pools.reduce((n, p) => n + p.used_bytes, 0);
    const total = this.pools.reduce((n, p) => n + p.total_bytes, 0);
    const tank = this.pools[0];
    return {
      pools_total: this.pools.length,
      pools_online: this.pools.filter((p) => p.status === 'ONLINE').length,
      cap_used_bytes: used, cap_total_bytes: total,
      snapshots_total: this.totalSnaps(),
      jobs_active: this.jobs.filter((j) => j.enabled).length,
      last_scrub: { pool: tank.name, ts: tank.scrub.ts, errors: tank.scrub.errors },
      alerts: this.alerts.slice(0, 3).map((a) => ({ ...a })),
      activity: this.activity.slice(0, 10).map((a) => ({ ...a })),
    };
  };

  // ---- Auth ----
  login = async (user: string, _password: string): Promise<LoginResult> => {
    await delay(300);
    // En modo demo entra cualquier credencial; rol según el usuario conocido
    const known = this.users.find((u) => u.user === user);
    this.session = { user: user || 'admin', role: known?.role ?? 'admin' };
    return { ...this.session };
  };
  login2FA = async (_pending: string, _code: string): Promise<SessionUser> => {
    await delay(200);
    if (!this.session) throw new ApiError(401, 'unauthorized', 'Sesión no iniciada');
    return { ...this.session };
  };
  logout = async () => { await delay(80); this.session = null; };
  me = async (): Promise<SessionUser> => {
    await delay(60);
    if (!this.session) throw new ApiError(401, 'unauthorized', 'Sesión no iniciada');
    return { ...this.session };
  };
  setMyPassword = async (_c: string, _n: string) => { await delay(); };
  setMyLanguage = async (_l: Lang) => { await delay(); };
  updateMyProfile = async (d: string, e: string) => {
    await delay();
    if (this.session) this.session = { ...this.session, display_name: d, email: e };
  };

  // 2FA en demo: inerte (no se puede activar sobre el mock; devuelve no activo).
  get2FAStatus = async (): Promise<TwoFAStatus> => { await delay(); return { enabled: false }; };
  setup2FA = async (): Promise<TwoFASetup> => {
    await delay();
    const secret = 'JBSWY3DPEHPK3PXP';
    return { secret, otpauth: `otpauth://totp/EasyZFS:demo?secret=${secret}&issuer=EasyZFS`, qr: '' };
  };
  confirm2FA = async (): Promise<TwoFARecovery> => { await delay(); throw new ApiError(400, 'demo_disabled', 'En modo demo la verificación en dos pasos no está disponible'); };
  disable2FA = async () => { await delay(); };
  regenerateRecoveryCodes = async (): Promise<TwoFARecovery> => { await delay(); throw new ApiError(400, 'demo_disabled', 'En modo demo la verificación en dos pasos no está disponible'); };

  // Avatares en memoria (object URLs): demo sin backend.
  private avatars = new Map<string, string>();
  setMyAvatar = async (blob: Blob) => {
    await delay();
    if (!this.session) throw new ApiError(401, 'unauthorized', 'Sesión no iniciada');
    if (blob.size > 512 * 1024) throw new ApiError(400, 'avatar_too_large', 'Imagen demasiado grande (máx. 512 KB)');
    const name = this.session.user;
    const old = this.avatars.get(name);
    if (old) URL.revokeObjectURL(old);
    this.avatars.set(name, URL.createObjectURL(blob));
    this.session = { ...this.session, avatar: name };
  };
  deleteMyAvatar = async () => {
    await delay();
    if (!this.session) throw new ApiError(401, 'unauthorized', 'Sesión no iniciada');
    const name = this.session.user;
    const old = this.avatars.get(name);
    if (old) { URL.revokeObjectURL(old); this.avatars.delete(name); }
    this.session = { ...this.session, avatar: '' };
  };
  avatarUrl = (name: string) => this.avatars.get(name) ?? '';

  // ---- Usuarios ----
  getUsers = async () => { await delay(); return this.users.map((u) => ({ ...u })); };
  createUser = async (r: CreateUserReq) => {
    await delay();
    if (this.users.some((u) => u.user === r.user)) throw new ApiError(409, 'conflict', 'El usuario ya existe');
    this.users.push({ user: r.user, role: r.role, language: 'auto', last_login: iso(new Date()), sessions: 0 });
  };
  deleteUser = async (name: string, confirm: string) => {
    await delay();
    if (confirm !== name) throw new ApiError(400, 'confirm_required', 'Confirmación incorrecta');
    if (this.session?.user === name) throw new ApiError(400, 'self_delete', 'No puedes eliminarte a ti mismo');
    this.users = this.users.filter((u) => u.user !== name);
  };
  setUserPassword = async (_n: string, _p: string, _c: boolean) => { await delay(); };
  setUserLanguage = async (name: string, language: Lang) => {
    await delay();
    this.users = this.users.map((u) => (u.user === name ? { ...u, language } : u));
  };

  // API keys read-only en demo (inertes)
  private apiKeys: { id: number; name: string; created_at: string }[] = [];
  getAPIKeys = async (): Promise<APIKeyInfo[]> => { await delay(); return this.apiKeys.map((k) => ({ ...k })); };
  createAPIKey = async (name: string): Promise<APIKeyCreated> => {
    await delay();
    const id = this.apiKeys.length + 1;
    this.apiKeys.push({ id, name, created_at: iso(new Date()) });
    return { name, key: 'ez_' + 'a'.repeat(64) };
  };
  deleteAPIKey = async (id: number) => {
    await delay();
    this.apiKeys = this.apiKeys.filter((k) => k.id !== id);
  };

  // ---- Pools ----
  getPools = async () => { await delay(); return this.pools.map((p) => ({ ...p, vdevs: p.vdevs.map((v) => ({ ...v })), scrub: { ...p.scrub } })); };
  createPool = async (r: CreatePoolReq) => {
    await delay(400);
    if (r.confirm !== r.name) throw new ApiError(400, 'confirm_required', `Escribe "${r.name}" para confirmar`);
    const size = this.disks.filter((d) => r.disks.includes(d.dev)).reduce((n, d) => n + d.size_bytes, 0);
    const usable = r.topo === 'mirror' ? size / Math.max(1, r.disks.length) : size;
    this.pools.push({
      name: r.name, status: 'ONLINE', topo: `${r.topo} (${r.disks.length} discos)`,
      used_bytes: Math.round(0.001 * usable), total_bytes: Math.round(usable),
      frag_pct: 1, comp_ratio: 1.0,
      scrub: { state: 'none', pct: 0, eta_sec: 0, ts: iso(new Date()), errors: 0 },
      vdevs: r.disks.map((dev) => ({ dev, role: r.topo === 'stripe' ? '—' : `${r.topo}-0`, status: 'ONLINE', temp_c: 33 })),
      autotrim: false, checkpoint: false,
    });
    this.disks.forEach((d) => { if (r.disks.includes(d.dev)) d.pool = r.name; });
    emitEvent({ type: 'overview' });
  };
  importPool = async (name?: string) => { await delay(); return name ? [] : ['archivo-antiguo']; };
  scrubAction = async (pool: string, action: 'start' | 'pause' | 'stop') => {
    await delay();
    const p = this.pools.find((x) => x.name === pool);
    if (!p) throw new ApiError(404, 'not_found', 'Pool no encontrado');
    if (action === 'start') p.scrub = { state: 'running', pct: 0, eta_sec: 3600, ts: iso(new Date()), errors: 0 };
    if (action === 'pause') p.scrub.state = 'none';
    if (action === 'stop') p.scrub = { state: 'done', pct: p.scrub.pct, eta_sec: 0, ts: iso(new Date()), errors: p.scrub.errors };
  };
  exportPool = async (name: string, confirm: string, _f: boolean, destroy: boolean) => {
    await delay(400);
    if (confirm !== name) throw new ApiError(400, 'confirm_required', `Escribe "${name}" para confirmar`);
    if (destroy) {
      this.pools = this.pools.filter((p) => p.name !== name);
      this.datasets = this.datasets.filter((d) => !d.name.startsWith(name + '/') && d.name !== name);
      this.snaps = this.snaps.filter((g) => !g.dataset.startsWith(name + '/') && g.dataset !== name);
      this.disks.forEach((d) => { if (d.pool === name) d.pool = '—'; });
    }
    emitEvent({ type: 'overview' });
  };
  addVdev = async (pool: string, topo: string, disks: string[], confirm: string) => {
    await delay(300);
    if (confirm !== pool) throw new ApiError(400, 'confirm_required', `Escribe "${pool}" para confirmar`);
    const p = this.pools.find((x) => x.name === pool);
    if (!p) throw new ApiError(404, 'not_found', 'Pool no encontrado');
    const role = topo === 'stripe' ? '—' : `${topo}-${p.vdevs.length}`;
    disks.forEach((dev) => {
      p.vdevs.push({ dev, role, status: 'ONLINE', temp_c: 33 });
      const d = this.disks.find((x) => x.dev === dev);
      if (d) { d.pool = pool; p.total_bytes += d.size_bytes; }
    });
    emitEvent({ type: 'overview' });
  };
  replaceDisk = async (pool: string, oldDev: string, newDev: string, confirm: string) => {
    await delay(300);
    if (confirm !== pool) throw new ApiError(400, 'confirm_required', `Escribe "${pool}" para confirmar`);
    const p = this.pools.find((x) => x.name === pool);
    if (!p) throw new ApiError(404, 'not_found', 'Pool no encontrado');
    const v = p.vdevs.find((x) => x.dev === oldDev);
    if (v) { v.dev = newDev; v.path = '/dev/' + newDev; v.status = 'ONLINE'; }
    const oldD = this.disks.find((x) => x.dev === oldDev);
    if (oldD) oldD.pool = '—';
    const newD = this.disks.find((x) => x.dev === newDev);
    if (newD) newD.pool = pool;
    emitEvent({ type: 'overview' });
  };
  vdevAction = async (pool: string, dev: string, action: 'offline' | 'online' | 'detach', confirm?: string) => {
    await delay(300);
    const p = this.pools.find((x) => x.name === pool);
    if (!p) throw new ApiError(404, 'not_found', 'Pool no encontrado');
    const v = p.vdevs.find((x) => x.dev === dev);
    if (!v) throw new ApiError(404, 'not_found', 'Vdev no encontrado');
    if (action === 'detach') {
      if (confirm !== pool) throw new ApiError(400, 'confirm_required', `Escribe "${pool}" para confirmar`);
      p.vdevs = p.vdevs.filter((x) => x.dev !== dev);
    } else {
      v.status = action === 'offline' ? 'OFFLINE' : 'ONLINE';
    }
    emitEvent({ type: 'overview' });
  };
  setAutotrim = async (pool: string, enabled: boolean) => {
    await delay(250);
    const p = this.pools.find((x) => x.name === pool);
    if (!p) throw new ApiError(404, 'not_found', 'Pool no encontrado');
    p.autotrim = enabled;
    emitEvent({ type: 'overview' });
  };
  checkpointPool = async (pool: string, action: 'create' | 'discard', confirm: string) => {
    await delay(300);
    if (confirm !== pool) throw new ApiError(400, 'confirm_required', `Escribe "${pool}" para confirmar`);
    const p = this.pools.find((x) => x.name === pool);
    if (!p) throw new ApiError(404, 'not_found', 'Pool no encontrado');
    p.checkpoint = action === 'create';
    emitEvent({ type: 'overview' });
  };
  getPoolHistory = async (pool: string): Promise<PoolHistoryEntry[]> => {
    await delay();
    if (pool === 'tank') {
      return [
        { ts: iso(daysAgo(0, 4)), command: 'zfs snapshot -r tank@auto-2026-08-01_06-00', duration_sec: 1.42 },
        { ts: iso(daysAgo(1, 2)), command: 'zpool scrub tank', duration_sec: 14250.8 },
        { ts: iso(daysAgo(3, 11)), command: 'zfs set compression=zstd tank/backups', duration_sec: 0.04 },
        { ts: iso(daysAgo(9, 18)), command: 'zpool checkpoint tank', duration_sec: 0.61 },
        { ts: iso(daysAgo(30, 10)), command: 'zpool create tank mirror nvme0n1 nvme1n1', duration_sec: 2.10 },
      ];
    }
    return [
      { ts: iso(daysAgo(0, 5)), command: 'zpool scrub ssd' },
      { ts: iso(daysAgo(0, 6)), command: 'zfs snapshot -r ssd@auto-2026-08-01_06-00', duration_sec: 0.91 },
      { ts: iso(daysAgo(20, 9)), command: 'zpool set autotrim=on ssd', duration_sec: 0.03 },
      { ts: iso(daysAgo(60, 12)), command: 'zpool create ssd nvme3n1', duration_sec: 1.74 },
    ];
  };
  getPerformance = async (): Promise<Performance> => {
    await delay();
    return {
      arc: { size_bytes: Math.round(3.8 * GiB), hit_pct: 92.4 },
      pools: this.pools.map((p, i) => ({
        name: p.name,
        read_bps: Math.round((41 + i * 180) * 1024 ** 2),
        write_bps: Math.round((12 + i * 84) * 1024 ** 2),
      })),
    };
  };

  // Series históricas sintéticas (U2): onda con ruido, determinista por source.
  getSeries = async (source: string, days: number, points = 120): Promise<SeriesResp> => {
    await delay(120);
    const now = Date.now() / 1000;
    const step = (days * 86400) / points;
    const base = source.startsWith('disk.') ? 42 : 60; // temp vs used_pct
    const amp = source.startsWith('disk.') ? 4 : 15;
    const out: SeriesPoint[] = [];
    for (let i = 0; i < points; i++) {
      const ts = now - (points - i) * step;
      const wave = Math.sin(i / 9) * amp + Math.sin(i / 3) * amp * 0.2;
      const val = Math.max(5, Math.min(100, base + wave + ((i * 7) % 5)));
      out.push({ ts: Math.round(ts), value: Math.round(val * 10) / 10 });
    }
    return { source, points: out };
  };

  // ---- Datasets ----
  getDatasets = async () => { await delay(); return this.datasets.map((d) => ({ ...d })); };
  createDataset = async (r: CreateDatasetReq) => {
    await delay(250);
    if (r.encryption && (r.passphrase ?? '').length < 8) {
      throw new ApiError(400, 'invalid_input', 'La passphrase debe tener al menos 8 caracteres');
    }
    this.datasets.push({
      name: `${r.pool}/${r.name}`, type: r.type, compression: r.compression,
      used_bytes: 1024 ** 2, avail_bytes: Math.round(2 * TiB),
      quota_bytes: r.type === 'volume' ? (r.volsize_bytes ?? 0) : r.quota_bytes,
      mountpoint: r.type === 'fs' ? `/${r.pool}/${r.name}` : '—',
      encryption: r.encryption ? 'aes-256-gcm' : 'off',
      keystatus: r.encryption ? 'available' : '-',
    });
  };

  // ---- Cifrado nativo (lote D; la clave se descarta tras la llamada) ----
  unlockDataset = async (name: string, key: string) => {
    await delay(400);
    const d = this.datasets.find((x) => x.name === name);
    if (!d || d.encryption === 'off' || d.encryption === '-') throw new ApiError(400, 'invalid_input', 'El dataset no está cifrado');
    if (!key) throw new ApiError(400, 'invalid_input', 'Se requiere la passphrase');
    d.keystatus = 'available';
    if (d.type === 'fs' && (!d.mountpoint || d.mountpoint === '—')) d.mountpoint = '/' + name;
    this.activity.unshift({ ts: iso(new Date()), text: 'Dataset desbloqueado', detail: name });
    emitEvent({ type: 'overview' });
  };
  lockDataset = async (name: string) => {
    await delay(300);
    const d = this.datasets.find((x) => x.name === name);
    if (!d || d.encryption === 'off' || d.encryption === '-') throw new ApiError(400, 'invalid_input', 'El dataset no está cifrado');
    d.keystatus = 'unavailable';
    d.mountpoint = '—';
    this.activity.unshift({ ts: iso(new Date()), text: 'Dataset bloqueado', detail: name });
    emitEvent({ type: 'overview' });
  };
  changeDatasetKey = async (name: string, currentKey: string, newKey: string) => {
    await delay(400);
    const d = this.datasets.find((x) => x.name === name);
    if (!d || d.encryption === 'off' || d.encryption === '-') throw new ApiError(400, 'invalid_input', 'El dataset no está cifrado');
    if (!currentKey) throw new ApiError(400, 'invalid_input', 'Se requiere la passphrase actual');
    if (newKey.length < 8) throw new ApiError(400, 'invalid_input', 'La passphrase nueva debe tener al menos 8 caracteres');
    this.activity.unshift({ ts: iso(new Date()), text: 'Clave de cifrado cambiada', detail: name });
  };

  // ---- RAID-Z expansion (lote D; gate capability + disco libre) ----
  expandPool = async (pool: string, vdev: string, disk: string, confirm: string) => {
    await delay(400);
    if (!this.version.capabilities?.raidz_expansion) throw new ApiError(400, 'not_supported', 'RAID-Z expansion requiere OpenZFS ≥ 2.3');
    if (confirm !== pool) throw new ApiError(400, 'confirm_required', `Escribe "${pool}" para confirmar`);
    const p = this.pools.find((x) => x.name === pool);
    if (!p) throw new ApiError(404, 'not_found', 'Pool no encontrado');
    if (!(p.raidz_vdevs ?? []).includes(vdev)) throw new ApiError(400, 'invalid_input', `El vdev '${vdev}' no es un raidz del pool`);
    const d = this.disks.find((x) => x.dev === disk);
    if (!d || (d.pool !== '—' && d.pool !== '') || d.in_use) throw new ApiError(409, 'dev_in_use', `El disco '${disk}' no está libre`);
    const role = vdev.replace(/-\d+$/, '');
    p.vdevs.push({ dev: disk, path: '/dev/' + disk, role, status: 'ONLINE', temp_c: d.temp_c ?? 33 });
    d.pool = pool;
    // Expansión simulada con progreso (~1%/s, evento por tick)
    p.scrub = { state: 'running', kind: 'expand', pct: 0, eta_sec: 100, ts: iso(new Date()), errors: 0 };
    const timer = setInterval(() => {
      if (p.scrub.state !== 'running' || p.scrub.kind !== 'expand') { clearInterval(timer); return; }
      p.scrub.pct = Math.min(100, p.scrub.pct + 2);
      p.scrub.eta_sec = Math.max(0, Math.round((100 - p.scrub.pct) / 2));
      emitEvent({ type: 'scrub.progress', pool, pct: p.scrub.pct, eta_sec: p.scrub.eta_sec, kind: 'expand' });
      if (p.scrub.pct >= 100) {
        p.scrub = { state: 'done', kind: 'expand', pct: 100, eta_sec: 0, ts: iso(new Date()), errors: 0 };
        this.activity.unshift({ ts: iso(new Date()), text: 'Expansión RAID-Z completada', detail: `${pool} · ${vdev} + ${disk}` });
        clearInterval(timer);
        emitEvent({ type: 'overview' });
      }
    }, 1000);
    this.timers.push(timer);
    emitEvent({ type: 'overview' });
  };
  updateDataset = async (name: string, p: { quota_bytes?: number; compression?: string }) => {
    await delay();
    const d = this.datasets.find((x) => x.name === name);
    if (d) Object.assign(d, p);
  };
  // props del mock: un mapa mutable por dataset (set actualiza el valor local).
  private datasetProps: Record<string, DatasetProp[]> = {};
  private propVal = (ds: string, name: string, value: string, source = 'local') =>
    ({ name, value, source }) as DatasetProp;
  getDatasetProps = async (name: string): Promise<DatasetPropsResp> => {
    await delay();
    let props = this.datasetProps[name];
    if (!props) {
      props = [
        this.propVal(name, 'compression', 'lz4'),
        this.propVal(name, 'recordsize', '128K'),
        this.propVal(name, 'atime', 'on', 'default'),
        this.propVal(name, 'relatime', 'on', 'default'),
        this.propVal(name, 'sync', 'standard', 'default'),
        this.propVal(name, 'quota', 'none'),
        this.propVal(name, 'mountpoint', '/mnt/' + name.split('/').pop()),
        this.propVal(name, 'exec', 'on', 'default'),
        this.propVal(name, 'readonly', 'off', 'default'),
        this.propVal(name, 'encryption', 'off', 'default'),
        this.propVal(name, 'used', '1.5T', '-'),
        this.propVal(name, 'avail', '3.2T', '-'),
        this.propVal(name, 'user:backup', 'true'),
      ];
      this.datasetProps[name] = props;
    }
    return { name, properties: props.map((p) => ({ ...p })) };
  };
  setDatasetProp = async (name: string, property: string, value: string) => {
    await delay();
    const props = this.datasetProps[name] ?? (await this.getDatasetProps(name)).properties;
    const p = props.find((x) => x.name === property);
    if (p) p.value = value;
    emitEvent({ type: 'overview' });
  };
  inheritDatasetProp = async (name: string, property: string) => {
    await delay();
    const props = this.datasetProps[name] ?? (await this.getDatasetProps(name)).properties;
    const p = props.find((x) => x.name === property);
    if (p) p.source = 'default';
    emitEvent({ type: 'overview' });
  };

  // ---- U1: SMART drill-down (atributos/selftests/error log ficticios) ----
  getDiskSmart = async (dev: string): Promise<DiskSmartResp> => {
    await delay();
    const d = this.disks.find((x) => x.dev === dev);
    if (!d) throw new ApiError(404, 'not_found', 'Disco no encontrado');
    const proto = dev.startsWith('nvme') ? 'nvme' : 'ata';
    if (d.smart === 'unknown') return { dev, model: d.model, serial: d.serial, smart: 'unknown', smart_detail: 'no disponible', hours: d.hours, attributes: [] };
    const attrs = proto === 'nvme' ? [
      { id: 1, name: 'temperature', value: Math.round(d.temp_c ?? 0), worst: 0, thresh: 0, raw: String(Math.round(d.temp_c ?? 0)), when_failed: '-' },
      { id: 2, name: 'available_spare', value: 100, worst: 100, thresh: 10, raw: '100%', when_failed: '-' },
      { id: 3, name: 'percentage_used', value: 12, worst: 12, thresh: 0, raw: '12%', when_failed: '-' },
    ] : [
      { id: 1, name: 'Raw_Read_Error_Rate', value: 100, worst: 16, thresh: 6, raw: '0', when_failed: '-' },
      { id: 5, name: 'Reallocated_Sector_Ct', value: 100, worst: 100, thresh: 36, raw: String(d.realloc_sectors ?? 0), when_failed: d.realloc_sectors ? 'Past' : '-' },
      { id: 9, name: 'Power_On_Hours', value: 95, worst: 95, thresh: 0, raw: String(d.hours), when_failed: '-' },
      { id: 197, name: 'Current_Pending_Sector', value: 99, worst: 99, thresh: 0, raw: String(d.pending_sectors ?? 0), when_failed: '-' },
      { id: 198, name: 'Offline_Uncorrectable', value: 99, worst: 99, thresh: 0, raw: String(d.offline_uncorr ?? 0), when_failed: '-' },
      { id: 199, name: 'UDMA_CRC_Error_Count', value: 200, worst: 200, thresh: 0, raw: String(d.crc_errors ?? 0), when_failed: '-' },
    ];
    return { dev, model: d.model, serial: d.serial, smart: d.smart, smart_detail: d.smart_detail, hours: d.hours, attributes: attrs };
  };
  getDiskSmartLog = async (dev: string): Promise<DiskSmartLogResp> => {
    await delay();
    const d = this.disks.find((x) => x.dev === dev);
    if (!d) throw new ApiError(404, 'not_found', 'Disco no encontrado');
    if (d.smart === 'unknown') return { dev, selftests: [], error_log: { count: 0, entries: [] } };
    const selftests: SmartSelftest[] = [
      { type: 'Short self-test', status: 'Completed without error', lifetime_hours: d.hours, percent: 100 },
      { type: 'Extended self-test', status: 'Completed without error', lifetime_hours: d.hours - 24, percent: 100 },
    ];
    const entries = d.realloc_sectors || d.crc_errors
      ? [{ error_type: 'NCQ', detail: '1 sectors, LBA 0x0' }]
      : [];
    return { dev, selftests, error_log: { count: entries.length, entries } };
  };
  deleteDataset = async (name: string, confirm: string, _r: boolean) => {
    await delay(300);
    if (confirm !== name) throw new ApiError(400, 'confirm_required', 'Confirmación incorrecta');
    this.datasets = this.datasets.filter((d) => d.name !== name);
    this.snaps = this.snaps.filter((g) => g.dataset !== name);
  };

  // ---- Operaciones largas (zfs rewrite simulado; runner del backend) ----
  private longops: LongOp[] = [
    {
      id: 'op-demo-1', type: 'rewrite', target: 'tank/backups', pid: 4213,
      started: iso(daysAgo(2, 11)), ended: iso(daysAgo(2, 12)), status: 'done',
      lines: ['Reescritura completada'],
    },
  ];
  private longopSeq = 1;

  getLongOps = async (): Promise<LongOp[]> => {
    await delay();
    return this.longops.map((o) => ({ ...o, lines: [...o.lines] }));
  };

  cancelLongOp = async (id: string) => {
    await delay(150);
    const op = this.longops.find((o) => o.id === id);
    if (!op) throw new ApiError(404, 'not_found', 'Operación no encontrada');
    if (op.status !== 'running') throw new ApiError(409, 'not_running', 'La operación ya no está en curso');
    op.status = 'canceled';
    op.ended = iso(new Date());
    op.lines.push('Cancelada por el usuario');
    emitEvent({ type: 'longop.update', op: { ...op } });
  };

  rewriteDataset = async (name: string, confirm: string): Promise<{ op_id: string }> => {
    await delay(300);
    if (!this.version.capabilities?.rewrite) throw new ApiError(400, 'not_supported', 'zfs rewrite requiere OpenZFS ≥ 2.3.4');
    if (confirm !== name) throw new ApiError(400, 'confirm_required', 'Confirmación incorrecta');
    const d = this.datasets.find((x) => x.name === name);
    if (!d || d.type !== 'fs' || !d.mountpoint || d.mountpoint === '—') {
      throw new ApiError(400, 'invalid_input', 'Dataset inexistente, no es filesystem o no está montado');
    }
    if (this.longops.some((o) => o.status === 'running' && o.target === name)) {
      throw new ApiError(409, 'already_running', `Ya hay una operación en curso sobre ${name}`);
    }
    const op: LongOp = {
      id: `op-demo-${++this.longopSeq}`, type: 'rewrite', target: name,
      pid: 4300 + this.longopSeq, started: iso(new Date()), status: 'running',
      lines: ['Reescribiendo bloques de ' + d.mountpoint + '…'],
    };
    this.longops.unshift(op);
    emitEvent({ type: 'longop.update', op: { ...op } });
    // Simulación: completa a los ~9 s con líneas de progreso
    let step = 0;
    const timer = setInterval(() => {
      step++;
      if (op.status !== 'running') { clearInterval(timer); return; }
      if (step >= 3) {
        op.status = 'done';
        op.ended = iso(new Date());
        op.lines.push('Reescritura completada');
        this.activity.unshift({ ts: op.ended, text: 'Reescritura de datos completada', detail: name });
        clearInterval(timer);
      } else {
        op.lines.push(`Procesados ${step * 38} % de los bloques…`);
      }
      emitEvent({ type: 'longop.update', op: { ...op } });
    }, 3000);
    this.timers.push(timer);
    return { op_id: op.id };
  };

  // ---- Snapshots ----
  getSnapshots = async (dataset?: string) => {
    await delay();
    const list = dataset ? this.snaps.filter((g) => g.dataset === dataset) : this.snaps;
    return list.map((g) => ({ dataset: g.dataset, snaps: g.snaps.map((s) => ({ ...s })) }));
  };
  createSnapshot = async (r: CreateSnapshotReq) => {
    await delay(200);
    const stamp = iso(new Date());
    const targets = r.recursive
      ? this.datasets.filter((d) => d.name === r.dataset || d.name.startsWith(r.dataset + '/')).map((d) => d.name)
      : [r.dataset];
    for (const ds of targets) {
      let g = this.snaps.find((x) => x.dataset === ds);
      if (!g) { g = { dataset: ds, snaps: [] }; this.snaps.unshift(g); }
      g.snaps.unshift({ name: r.name, full: `${ds}@${r.name}`, ts: stamp, used_bytes: 0, kind: 'manual' });
    }
    this.activity.unshift({ ts: stamp, text: 'Snapshot manual creado', detail: `${r.dataset}@${r.name}` });
    emitEvent({ type: 'overview' });
  };
  deleteSnapshot = async (full: string, confirm: string) => {
    await delay(200);
    if (confirm !== full.split('@')[1] && confirm !== full)
      throw new ApiError(400, 'confirm_required', 'Confirmación incorrecta');
    const [ds, name] = full.split('@');
    const g = this.snaps.find((x) => x.dataset === ds);
    if (g) g.snaps = g.snaps.filter((s) => s.name !== name);
  };
  rollback = async (full: string, confirm: string) => {
    await delay(300);
    const [ds] = full.split('@');
    if (confirm !== ds && confirm !== full) throw new ApiError(400, 'confirm_required', `Escribe "${ds}" para confirmar`);
    this.activity.unshift({ ts: iso(new Date()), text: 'Rollback ejecutado', detail: full });
  };

  // ---- Replicación (demo: 1 local OK, 1 SSH con error de autenticación) ----
  private replJobs: ReplicationJob[] = [
    {
      id: 1, source: 'tank/fotos', dest_type: 'local', dest_dataset: 'tank/backups/fotos',
      host: '', user: '', port: 22, raw: false, force_full: false,
      schedule: 'daily@06:30', enabled: true,
      last_bookmark: 'ezrepl-20260801-063000', last_run: iso(daysAgo(0, 6)),
      last_ok: true, last_error: '', next_run: iso(daysAgo(-1, 6)),
    },
    {
      id: 2, source: 'tank/documentos', dest_type: 'ssh', dest_dataset: 'bak/documentos',
      host: 'nas-backup.lan', user: 'zfsrepl', port: 22, raw: true, force_full: false,
      schedule: 'hourly@:20', enabled: true,
      last_bookmark: 'ezrepl-20260730-112000', last_run: iso(daysAgo(0, 3)),
      last_ok: false,
      last_error: 'ssh: Permission denied (publickey) — instala la clave pública del servidor en el destino',
      next_run: iso(daysAgo(-1, 3)),
    },
  ];
  private replSeq = 3;

  getReplicationJobs = async () => { await delay(); return this.replJobs.map((j) => ({ ...j })); };
  createReplicationJob = async (r: CreateReplicationReq) => {
    await delay();
    this.replJobs.push({
      id: this.replSeq++, source: r.source, dest_type: r.dest_type, dest_dataset: r.dest_dataset,
      host: r.host ?? '', user: r.user ?? '', port: r.port ?? 22,
      raw: !!r.raw, force_full: !!r.force_full, schedule: r.schedule, enabled: true,
      last_bookmark: '', last_run: null, last_ok: null, last_error: '', next_run: iso(daysAgo(-1)),
    });
  };
  updateReplicationJob = async (id: number, r: UpdateReplicationReq) => {
    await delay();
    const j = this.replJobs.find((x) => x.id === id);
    if (j) Object.assign(j, r);
  };
  deleteReplicationJob = async (id: number, _c: string) => {
    await delay();
    this.replJobs = this.replJobs.filter((j) => j.id !== id);
  };
  runReplicationJob = async (id: number) => {
    await delay(200);
    const j = this.replJobs.find((x) => x.id === id);
    if (!j) throw new ApiError(404, 'not_found', 'Job de replicación no encontrado');
    const target = j.dest_type === 'ssh' ? `${j.source} → ${j.user}@${j.host}:${j.dest_dataset}` : `${j.source} → ${j.dest_dataset}`;
    if (this.longops.some((o) => o.status === 'running' && o.type === 'replication' && o.target === target)) {
      throw new ApiError(409, 'already_running', 'Ya hay una replicación en curso para este job');
    }
    const op: LongOp = {
      id: `op-demo-${++this.longopSeq}`, type: 'replication', target,
      pid: 5100 + this.longopSeq, started: iso(new Date()), status: 'running',
      lines: [`zfs send ${j.raw ? '-w ' : ''}${j.source}@ezrepl-nuevo…`],
    };
    this.longops.unshift(op);
    emitEvent({ type: 'longop.update', op: { ...op } });
    const fail = j.dest_type === 'ssh'; // demo: los ssh fallan con error de auth legible
    let step = 0;
    const timer = setInterval(() => {
      step++;
      if (op.status !== 'running') { clearInterval(timer); return; }
      if (step >= 3) {
        op.status = fail ? 'error' : 'done';
        op.ended = iso(new Date());
        j.last_run = op.ended;
        j.last_ok = !fail;
        if (fail) {
          op.error = 'exit status 255';
          op.lines.push('zfsrepl@' + j.host + ': Permission denied (publickey).');
          j.last_error = 'ssh: Permission denied (publickey) — instala la clave pública del servidor en el destino';
        } else {
          op.lines.push('Replicación completada');
          j.last_error = '';
          j.last_bookmark = 'ezrepl-' + new Date().toISOString().slice(0, 10).replaceAll('-', '') + '-000000';
        }
        this.history.unshift({ ts: op.ended, tipo: 'replication', target, ok: !fail, detail: fail ? j.last_error : 'ok' });
        emitEvent({ type: 'replication.finished', id: j.id, ok: !fail, detail: fail ? j.last_error : 'ok' });
        clearInterval(timer);
      } else {
        op.lines.push(`send: ${step * 410} MiB / 1,2 GiB (${step * 33} %)…`);
      }
      emitEvent({ type: 'longop.update', op: { ...op } });
    }, 1500);
    this.timers.push(timer);
  };
  getReplicationSSHKey = async (): Promise<ReplicationSSHKey> => {
    await delay();
    return {
      public_key: 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIDemoKeyEasyZFSReplication0123456789abcdef easyzfs-replication',
      instructions: 'Añade esta clave a ~/.ssh/authorized_keys del usuario destino. Para no usar root: zfs allow -u <usuario> snapshot,send,receive,destroy,hold,bookmark <pool>',
    };
  };
  testReplication = async (_h: string, _u: string, _p: number): Promise<ReplicationTestResult> => {
    await delay(700);
    return { ok: false, error: 'autenticación fallida (Permission denied): instala la clave pública del servidor en el authorized_keys del usuario destino' };
  };

  // ---- Tareas ----
  getJobs = async () => { await delay(); return this.jobs.map((j) => ({ ...j })); };
  createJob = async (r: CreateJobReq) => {
    await delay();
    this.jobs.push({
      id: this.jobSeq++, tipo: r.tipo, target: r.target, schedule: r.schedule,
      retention: r.retention ?? '', enabled: true, last_run: '', last_result: '—', next_run: iso(daysAgo(-1)),
    });
  };
  updateJob = async (id: number, r: UpdateJobReq) => {
    await delay();
    const j = this.jobs.find((x) => x.id === id);
    if (j) Object.assign(j, r);
  };
  deleteJob = async (id: number, _c: string) => {
    await delay();
    this.jobs = this.jobs.filter((j) => j.id !== id);
  };
  runJob = async (id: number) => {
    await delay(200);
    const j = this.jobs.find((x) => x.id === id);
    if (!j) return;
    j.last_run = iso(new Date());
    j.last_result = 'OK';
    this.history.unshift({ ts: j.last_run, tipo: j.tipo, target: j.target, ok: true, detail: 'ejecutado manualmente' });
    emitEvent({ type: 'job.finished', id, ok: true, detail: 'ejecutado manualmente' });
  };
  getJobHistory = async () => { await delay(); return this.history.map((h) => ({ ...h })); };
  getSystemTimers = async (): Promise<SystemTimersResp> => {
    await delay();
    // Demo: systemd disponible (la UI muestra la opción de cambio a timer).
    return { timers: this.systemTimers.map((s) => ({ ...s })), systemd_available: true };
  };
  setSystemTimerSchedule = async (task: SystemTimer, schedule: string) => {
    await delay(300);
    const t = this.systemTimers.find((x) => x.source === task.source && x.name === task.name && x.line === task.line);
    if (!t || !t.editable) throw new ApiError(404, 'not_found', 'tarea no encontrada o no editable');
    t.schedule = schedule;
  };
  migrateSystemTimer = async (task: SystemTimer, newName: string) => {
    await delay(400);
    const i = this.systemTimers.findIndex((x) => x.source === task.source && x.name === task.name && x.line === task.line);
    if (i < 0 || !this.systemTimers[i].editable || this.systemTimers[i].source !== 'cron') {
      throw new ApiError(404, 'not_found', 'tarea no encontrada o no migrable');
    }
    const old = this.systemTimers[i];
    this.systemTimers.splice(i, 1, {
      source: 'systemd', name: `easyzfs-${newName}.timer`, schedule: old.schedule,
      next_run: old.next_run, command: `easyzfs-${newName}.service`, editable: true,
    });
  };

  // ---- Discos ----
  getDisks = async () => { await delay(); return this.disks.map((d) => ({ ...d })); };
  getRecommendations = async () => { await delay(); return computeRecommendations(this.disks, this.pools); };
  smartTest = async (dev: string, type: 'short' | 'long') => {    await delay(200);
    this.history.unshift({ ts: iso(new Date()), tipo: type === 'short' ? 'smart_short' : 'smart_long', target: dev, ok: true, detail: 'test iniciado' });
  };
  poweroffDisk = async (dev: string) => {
    await delay(300);
    const d = this.disks.find((x) => x.dev === dev);
    if (!d) throw new ApiError(404, 'not_found', 'Disco no encontrado');
    if (d.pool !== '—' && d.pool !== '') throw new ApiError(409, 'dev_in_use', `el disco pertenece al pool '${d.pool}'`);
    if (d.in_use) throw new ApiError(409, 'dev_mounted', 'el disco tiene particiones montadas o swap activo');
    this.history.unshift({ ts: iso(new Date()), tipo: 'poweroff', target: dev, ok: true, detail: 'disco apagado' });
  };

  // ---- Notificaciones push (demo: simula OK SIN push real — regla demo) ----
  getPushVapidKey = async () => { await delay(); return { publicKey: '' }; };
  pushSubscribe = async () => { await delay(); };
  pushUnsubscribe = async () => { await delay(); };
  // La sección de preferencias no se muestra en demo; stubs por el contrato.
  getPushPreferences = async () => {
    await delay();
    return {
      preferences: (['pool_capacity', 'pool_status', 'scrub_errors', 'disk_temp', 'smart_status'] as PushAlertTipo[])
        .map((tipo) => ({ tipo, enabled: true })),
    };
  };
  putPushPreference = async () => { await delay(); };
  getPushQuietHours = async () => {
    await delay();
    return { enabled: false, start: null, end: null, tz: 'Europe/Madrid' };
  };
  putPushQuietHours = async () => { await delay(); };

  // --- Nuevos: snapshot, dataset, pool (stubs demo) ---
  cloneSnapshot = async (full: string, target: string, _mountpoint?: string) => {
    await delay();
    // En demo: simula éxito sin tocar el estado.
    return { name: target };
  };
  snapshotDiff = async (_from: string, _to: string) => { await delay(); return []; };
  renameDataset = async (_name: string, _newName: string) => { await delay(); };
  mountDataset = async (_name: string) => { await delay(); };
  unmountDataset = async (_name: string) => { await delay(); };
  promoteDataset = async (_name: string) => { await delay(); };
  clearPool = async (_pool: string, _dev?: string) => { await delay(); };
}
