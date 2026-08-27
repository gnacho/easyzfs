// Implementación real del provider contra el backend Go en /api.
import type { DataProvider } from './provider';
import { ApiError } from './types';
import { notifyAuthExpired } from './events';
import type {
  ActivityItem, Alert, APIKeyCreated, APIKeyInfo, BackupFile, BackupStatus, CreateDatasetReq, CreateJobReq, CreatePoolReq, CreateReplicationReq, CreateSnapshotReq, CreateUserReq,
  Dataset, DatasetProp, DatasetPropsResp, DiffEntry, Disk, DiskSmartLogResp, DiskSmartResp, Job, JobHistoryItem, Lang, LoginResult, LongOp, Overview, Performance, Pool, PoolHistoryEntry, PushAlertTipo, PushPreference, PushQuietHours, PushSubscriptionJSON,
  Recommendation, ReplicationJob, ReplicationSSHKey, ReplicationTestResult, SessionUser, Settings, SeriesResp,
  SnapshotGroup, SystemTimer, SystemTimersResp, TwoFARecovery, TwoFASetup, TwoFAStatus, UpdateJobReq, UpdateReplicationReq, UpdateStatus, UserInfo, VersionInfo,
} from './types';

const BASE = '/api';

// Estado público del demo (GET /api/public/demo, sin sesión): lo consulta el
// login para mostrar u ocultar el botón "Entrar como demo" y main.tsx para
// resolver el idioma por defecto. Fuera del provider porque se llama ANTES de
// autenticarse. Single-flight: main.tsx y Login comparten la misma petición.
export interface PublicDemo {
  enabled: boolean; // ajuste del admin: se ofrece el botón de demo
  server: boolean;  // este servidor ES el despliegue demo (DEMO=1)
}

let publicDemoPromise: Promise<PublicDemo> | null = null;

export function fetchPublicDemo(): Promise<PublicDemo> {
  if (!publicDemoPromise) {
    publicDemoPromise = (async () => {
      try {
        const res = await fetch(`${BASE}/public/demo`, { credentials: 'same-origin' });
        if (!res.ok) return { enabled: true, server: false }; // sin respuesta: botón visible, servidor no-demo
        const j = await res.json();
        return { enabled: j?.demo_enabled !== false, server: j?.demo_server === true };
      } catch {
        return { enabled: true, server: false }; // sin red: botón visible, servidor no-demo
      }
    })();
  }
  return publicDemoPromise;
}

async function req<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(BASE + path, {
    method,
    credentials: 'same-origin',
    headers: body !== undefined ? { 'Content-Type': 'application/json' } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  if (res.status === 204) return undefined as T;
  const text = await res.text();
  let json: unknown = undefined;
  try { json = text ? JSON.parse(text) : undefined; } catch { /* respuesta no JSON */ }
  if (!res.ok) {
    // Sesión expirada: cualquier 401 fuera del propio login/logout fuerza
    // volver a la pantalla de login (lo gestiona el store). Se excluyen
    // /login (credenciales incorrectas no son sesión expirada) y /logout
    // (evita bucles al cerrar una sesión ya caducada).
    if (res.status === 401 && path !== '/login' && path !== '/logout') notifyAuthExpired();
    const e = json as { error?: string; message?: string } | undefined;
    throw new ApiError(res.status, e?.error ?? 'http_error', e?.message ?? `HTTP ${res.status}`);
  }
  return json as T;
}

const get = <T>(p: string) => req<T>('GET', p);
const post = <T>(p: string, b?: unknown) => req<T>('POST', p, b);
const patch = <T>(p: string, b?: unknown) => req<T>('PATCH', p, b);
const put = <T>(p: string, b?: unknown) => req<T>('PUT', p, b);
const del = <T>(p: string, b?: unknown) => req<T>('DELETE', p, b);
const enc = encodeURIComponent;

export class HttpProvider implements DataProvider {
  readonly isMock = false;

  getVersion = () => get<VersionInfo>('/version');
  getUpdateStatus = () => get<UpdateStatus>('/update/status');
  checkUpdate = () => post<UpdateStatus>('/update/check');
  getUpdatePlan = () => get<{canApply: boolean; checks: {id:string;status:string;title:string;summary:string}[]}>('/update/plan');
  applyUpdate = async () => { await post<UpdateStatus>('/update/apply'); };
  getSettings = () => get<Settings>('/settings');
  putSettings = (s: Settings) => put<void>('/settings', s);
  getAlerts = () => get<Alert[]>('/alerts');
  ackAlert = (id: number) => post<void>(`/alerts/${id}/ack`);
  getOverview = () => get<Overview>('/overview');
  getActivity = (limit?: number) =>
    get<ActivityItem[]>(limit ? `/activity?limit=${limit}` : '/activity');

  getBackupStatus = () => get<BackupStatus>('/backup/status');
  runBackup = () => post<BackupFile>('/backup/run');
  importBackup = async (file: File): Promise<void> => {
    // Body crudo (no JSON): el server verifica magic + quick_check y, si es
    // válido, hace swap y reinicia el proceso (202).
    const res = await fetch(`${BASE}/backup/import`, {
      method: 'POST', credentials: 'same-origin',
      headers: { 'Content-Type': 'application/octet-stream' },
      body: file,
    });
    if (!res.ok) {
      const j = await res.json().catch(() => undefined);
      throw new ApiError(res.status, j?.error ?? 'http_error', j?.message ?? `HTTP ${res.status}`);
    }
  };

  login = (user: string, password: string) => post<LoginResult>('/login', { user, password });
  login2FA = (pending: string, code: string) => post<SessionUser>('/login/2fa', { pending, code });
  logout = () => post<void>('/logout');
  me = () => get<SessionUser>('/me');
  setMyPassword = (current: string, next: string) => post<void>('/me/password', { current, new: next });
  setMyLanguage = (language: Lang) => put<void>('/me/language', { language });

  updateMyProfile = (display_name: string, email: string) =>
    put<void>('/me/profile', { display_name, email });

  get2FAStatus = () => get<TwoFAStatus>('/me/2fa');
  setup2FA = () => post<TwoFASetup>('/me/2fa/setup');
  confirm2FA = (code: string) => post<TwoFARecovery>('/me/2fa/confirm', { code });
  disable2FA = (code: string) => post<void>('/me/2fa/disable', { code });
  regenerateRecoveryCodes = () => get<TwoFARecovery>('/me/2fa/recovery');

  // Avatar: el blob ya viene recortado y re-codificado (webp/jpeg) del
  // diálogo de recorte; se manda crudo como importBackup.
  setMyAvatar = async (blob: Blob): Promise<void> => {
    const res = await fetch(`${BASE}/me/avatar`, {
      method: 'PUT', credentials: 'same-origin',
      headers: { 'Content-Type': blob.type || 'application/octet-stream' },
      body: blob,
    });
    if (!res.ok) {
      const j = await res.json().catch(() => undefined);
      throw new ApiError(res.status, j?.error ?? 'http_error', j?.message ?? `HTTP ${res.status}`);
    }
  };
  deleteMyAvatar = () => del<void>('/me/avatar');
  // La URL existe siempre; el componente solo la usa si user.avatar != ''
  // (evita 404s en el <img>). no-cache en el server: se revalida al cambiar.
  avatarUrl = (name: string) => `${BASE}/avatars/${enc(name)}`;

  getUsers = () => get<UserInfo[]>('/users');
  createUser = (r: CreateUserReq) => post<void>('/users', r);
  deleteUser = (name: string, confirm: string) => del<void>(`/users/${enc(name)}`, { confirm });
  setUserPassword = (name: string, next: string, closeSessions: boolean) =>
    post<void>(`/users/${enc(name)}/password`, { new: next, close_sessions: closeSessions });
  setUserLanguage = (name: string, language: Lang) =>
    put<void>(`/users/${enc(name)}/language`, { language });

  getAPIKeys = () => get<{ keys: APIKeyInfo[] }>('/keys').then((r) => r.keys);
  createAPIKey = (name: string) => post<APIKeyCreated>('/keys', { name });
  deleteAPIKey = (id: number) => del<void>(`/keys/${id}`);

  getPools = () => get<Pool[]>('/pools');
  createPool = (r: CreatePoolReq) => post<void>('/pools', r);
  importPool = async (name?: string): Promise<string[]> => {
    const r = await post<{ importable?: string[] } | string[]>('/pools/import', name ? { name } : {});
    return Array.isArray(r) ? r : (r.importable ?? []);
  };
  scrubAction = (pool: string, action: 'start' | 'pause' | 'stop') =>
    post<void>(`/pools/${enc(pool)}/scrub`, { action });
  exportPool = (name: string, confirm: string, force: boolean, destroy: boolean) =>
    post<void>(`/pools/${enc(name)}/export`, { confirm, force, destroy });
  addVdev = (pool: string, topo: string, disks: string[], confirm: string) =>
    post<void>(`/pools/${enc(pool)}/vdev`, { topo, disks, confirm });
  replaceDisk = (pool: string, oldDev: string, newDev: string, confirm: string) =>
    post<void>(`/pools/${enc(pool)}/replace`, { old_dev: oldDev, new_dev: newDev, confirm });
  vdevAction = (pool: string, dev: string, action: 'offline' | 'online' | 'detach', confirm?: string) =>
    post<void>(`/pools/${enc(pool)}/vdev/action`, { dev, action, confirm });
  setAutotrim = (pool: string, enabled: boolean) =>
    post<void>(`/pools/${enc(pool)}/autotrim`, { enabled });
  checkpointPool = (pool: string, action: 'create' | 'discard', confirm: string) =>
    post<void>(`/pools/${enc(pool)}/checkpoint`, { action, confirm });
  getPoolHistory = (pool: string) => get<PoolHistoryEntry[]>(`/pools/${enc(pool)}/history`);
  getPerformance = () => get<Performance>('/performance');
  getSeries = (source: string, days: number, points = 800) =>
    get<SeriesResp>(`/series?source=${enc(source)}&days=${days}&points=${points}`);

  getDatasets = () => get<Dataset[]>('/datasets');
  createDataset = (r: CreateDatasetReq) => post<void>('/datasets', r);
  updateDataset = (name: string, p: { quota_bytes?: number; compression?: string }) =>
    patch<void>(`/datasets/${enc(name)}`, p);
  deleteDataset = (name: string, confirm: string, recursive: boolean) =>
    del<void>(`/datasets/${enc(name)}`, { confirm, recursive });
  rewriteDataset = (name: string, confirm: string) =>
    post<{ op_id: string }>(`/datasets/${enc(name)}/rewrite`, { confirm });
  unlockDataset = (name: string, key: string) =>
    post<void>(`/datasets/${enc(name)}/unlock`, { key });
  lockDataset = (name: string) =>
    post<void>(`/datasets/${enc(name)}/lock`, {});
  changeDatasetKey = (name: string, currentKey: string, newKey: string) =>
    post<void>(`/datasets/${enc(name)}/change-key`, { current_key: currentKey, new_key: newKey });
  getDatasetProps = (name: string) => get<DatasetPropsResp>(`/datasets/${enc(name)}/properties`);
  setDatasetProp = (name: string, property: string, value: string) =>
    patch<void>(`/datasets/${enc(name)}/properties`, { property, value });
  inheritDatasetProp = (name: string, property: string) =>
    post<void>(`/datasets/${enc(name)}/properties/${enc(property)}/inherit`);
  getDiskSmart = (dev: string) => get<DiskSmartResp>(`/disks/${enc(dev)}/smart`);
  getDiskSmartLog = (dev: string) => get<DiskSmartLogResp>(`/disks/${enc(dev)}/smart-log`);
  expandPool = (pool: string, vdev: string, disk: string, confirm: string) =>
    post<void>(`/pools/${enc(pool)}/expand`, { vdev, disk, confirm });

  getLongOps = () => get<LongOp[]>('/longops');
  cancelLongOp = (id: string) => post<void>(`/longops/${enc(id)}/cancel`);

  getSnapshots = (dataset?: string) =>
    get<SnapshotGroup[]>('/snapshots' + (dataset ? `?dataset=${enc(dataset)}` : ''));
  createSnapshot = (r: CreateSnapshotReq) => post<void>('/snapshots', r);
  deleteSnapshot = (full: string, confirm: string) => del<void>(`/snapshots/${enc(full)}`, { confirm });
  rollback = (full: string, confirm: string) => post<void>(`/snapshots/${enc(full)}/rollback`, { confirm });

  getJobs = () => get<Job[]>('/jobs');
  createJob = (r: CreateJobReq) => post<void>('/jobs', r);
  updateJob = (id: number, r: UpdateJobReq) => patch<void>(`/jobs/${id}`, r);
  deleteJob = (id: number, confirm: string) => del<void>(`/jobs/${id}`, { confirm });
  runJob = (id: number) => post<void>(`/jobs/${id}/run`);
  getJobHistory = () => get<JobHistoryItem[]>('/jobs/history');

  getReplicationJobs = () => get<ReplicationJob[]>('/replication');
  createReplicationJob = (r: CreateReplicationReq) => post<void>('/replication', r);
  updateReplicationJob = (id: number, r: UpdateReplicationReq) => patch<void>(`/replication/${id}`, r);
  deleteReplicationJob = (id: number, confirm: string) => del<void>(`/replication/${id}`, { confirm });
  runReplicationJob = (id: number) => post<void>(`/replication/${id}/run`);
  getReplicationSSHKey = () => get<ReplicationSSHKey>('/replication/sshkey');
  testReplication = (host: string, user: string, port: number) =>
    post<ReplicationTestResult>('/replication/test', { host, user, port });
  getSystemTimers = () => get<SystemTimersResp>('/system-timers');
  setSystemTimerSchedule = (t: SystemTimer, schedule: string) =>
    post<void>('/system-timers/schedule', { source: t.source, name: t.name, origin: t.origin ?? '', line: t.line ?? 0, schedule });
  migrateSystemTimer = (t: SystemTimer, newName: string) =>
    post<void>('/system-timers/migrate', { source: t.source, name: t.name, origin: t.origin ?? '', line: t.line ?? 0, new_name: newName });

  getDisks = () => get<Disk[]>('/disks');
  getRecommendations = () => get<Recommendation[]>('/recommendations');
  smartTest = (dev: string, type: 'short' | 'long') => post<void>(`/disks/${enc(dev)}/smart-test`, { type });
  poweroffDisk = (dev: string) => post<void>(`/disks/${enc(dev)}/poweroff`, {});
  identifyDisk = (dev: string) => post<void>(`/disks/${enc(dev)}/identify`, {});

  getPushVapidKey = () => get<{ publicKey: string }>('/push/vapid-public-key');
  pushSubscribe = (sub: PushSubscriptionJSON, lang: 'es' | 'en') =>
    post<void>('/push/subscribe', {
      endpoint: sub.endpoint, keys: sub.keys, lang,
      origin: window.location.origin, // para notification.navigate absoluta
    });
  pushUnsubscribe = (endpoint: string) => del<void>('/push/unsubscribe', { endpoint });

  getPushPreferences = () =>
    get<{ preferences: PushPreference[] }>('/push/preferences');
  putPushPreference = (tipo: PushAlertTipo, enabled: boolean) =>
    put<void>('/push/preferences', { tipo, enabled });
  getPushQuietHours = () => get<PushQuietHours>('/push/quiet-hours');
  putPushQuietHours = (q: { enabled: boolean; start: number; end: number }) =>
    put<void>('/push/quiet-hours', q);

  // --- Nuevos: snapshot, dataset, pool ---
  cloneSnapshot = (full: string, target: string, mountpoint?: string) =>
    post<{ name: string }>(`/snapshots/${enc(full)}/clone`, { target, mountpoint });
  snapshotDiff = (from: string, to: string) =>
    get<DiffEntry[]>(`/snapshots/diff?from=${enc(from)}&to=${enc(to)}`);
  renameDataset = (name: string, newName: string) =>
    patch<void>(`/datasets/${enc(name)}/rename`, { new_name: newName });
  mountDataset = (name: string) => post<void>(`/datasets/${enc(name)}/mount`, {});
  unmountDataset = (name: string) => post<void>(`/datasets/${enc(name)}/unmount`, {});
  promoteDataset = (name: string) => post<void>(`/datasets/${enc(name)}/promote`, {});
  clearPool = (pool: string, dev?: string) =>
    post<void>(`/pools/${enc(pool)}/clear`, dev ? { dev } : {});
}
