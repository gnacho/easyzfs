// Interfaz DataProvider: abstrae el origen de datos (HTTP real o mock demo).
import type {
  ActivityItem, Alert, APIKeyCreated, APIKeyInfo, BackupFile, BackupStatus, CreateDatasetReq, CreateJobReq, CreatePoolReq, CreateReplicationReq, CreateSnapshotReq, CreateUserReq,
  Dataset, DatasetProp, DatasetPropsResp, DiffEntry, Disk, DiskSmartLogResp, DiskSmartResp, Job, JobHistoryItem, Lang, LoginResult, LongOp, Overview, Performance, Pool, PoolHistoryEntry, PushAlertTipo, PushPreference, PushQuietHours, PushSubscriptionJSON, SeriesResp,
  Recommendation, ReplicationJob, ReplicationSSHKey, ReplicationTestResult, SessionUser, Settings,
  SnapshotGroup, SystemTimer, SystemTimersResp, TwoFARecovery, TwoFASetup, TwoFAStatus, UpdateJobReq, UpdateReplicationReq, UpdateStatus, UserInfo, VersionInfo,
} from './types';

export interface DataProvider {
  readonly isMock: boolean;

  // Sistema
  getVersion(): Promise<VersionInfo>;
  getSettings(): Promise<Settings>;
  putSettings(s: Settings): Promise<void>;
  getAlerts(): Promise<Alert[]>;
  ackAlert(id: number): Promise<void>;
  getOverview(): Promise<Overview>;
  getActivity(limit?: number): Promise<ActivityItem[]>;

  // Actualizaciones (admin; el apply reinicia el servicio vía easyzfs-update.path)
  getUpdateStatus(): Promise<UpdateStatus>;
  getUpdatePlan(): Promise<{canApply: boolean; checks: {id:string;status:string;title:string;summary:string}[]}>;
  applyUpdate(): Promise<void>;

  // Copia de seguridad de la BD
  getBackupStatus(): Promise<BackupStatus>;
  runBackup(): Promise<BackupFile>;
  importBackup(file: File): Promise<void>;

  // Auth y sesión
  login(user: string, password: string): Promise<LoginResult>;
  login2FA(pending: string, code: string): Promise<SessionUser>;
  logout(): Promise<void>;
  me(): Promise<SessionUser>;
  setMyPassword(current: string, next: string): Promise<void>;
  setMyLanguage(language: Lang): Promise<void>;
  updateMyProfile(displayName: string, email: string): Promise<void>;
  setMyAvatar(blob: Blob): Promise<void>;
  deleteMyAvatar(): Promise<void>;
  avatarUrl(name: string): string;

  // 2FA (TOTP) del propio usuario
  get2FAStatus(): Promise<TwoFAStatus>;
  setup2FA(): Promise<TwoFASetup>;
  confirm2FA(code: string): Promise<TwoFARecovery>;
  disable2FA(code: string): Promise<void>;
  regenerateRecoveryCodes(): Promise<TwoFARecovery>;

  // Usuarios (admin)
  getUsers(): Promise<UserInfo[]>;
  createUser(r: CreateUserReq): Promise<void>;
  deleteUser(name: string, confirm: string): Promise<void>;
  setUserPassword(name: string, next: string, closeSessions: boolean): Promise<void>;
  setUserLanguage(name: string, language: Lang): Promise<void>;

  // API keys de solo lectura (admin, #87)
  getAPIKeys(): Promise<APIKeyInfo[]>;
  createAPIKey(name: string): Promise<APIKeyCreated>;
  deleteAPIKey(id: number): Promise<void>;

  // Pools
  getPools(): Promise<Pool[]>;
  createPool(r: CreatePoolReq): Promise<void>;
  importPool(name?: string): Promise<string[]>;
  scrubAction(pool: string, action: 'start' | 'pause' | 'stop'): Promise<void>;
  exportPool(name: string, confirm: string, force: boolean, destroy: boolean): Promise<void>;
  addVdev(pool: string, topo: string, disks: string[], confirm: string): Promise<void>;
  replaceDisk(pool: string, oldDev: string, newDev: string, confirm: string): Promise<void>;
  vdevAction(pool: string, dev: string, action: 'offline' | 'online' | 'detach', confirm?: string): Promise<void>;
  setAutotrim(pool: string, enabled: boolean): Promise<void>;
  checkpointPool(pool: string, action: 'create' | 'discard', confirm: string): Promise<void>;
  getPoolHistory(pool: string): Promise<PoolHistoryEntry[]>;
  getPerformance(): Promise<Performance>;
  getSeries(source: string, days: number, points?: number): Promise<SeriesResp>;
  clearPool(pool: string, dev?: string): Promise<void>;
  expandPool(pool: string, vdev: string, disk: string, confirm: string): Promise<void>;

  // Datasets
  getDatasets(): Promise<Dataset[]>;
  createDataset(r: CreateDatasetReq): Promise<void>;
  updateDataset(name: string, patch: { quota_bytes?: number; compression?: string }): Promise<void>;
  deleteDataset(name: string, confirm: string, recursive: boolean): Promise<void>;
  rewriteDataset(name: string, confirm: string): Promise<{ op_id: string }>;
  unlockDataset(name: string, key: string): Promise<void>;
  lockDataset(name: string): Promise<void>;
  changeDatasetKey(name: string, currentKey: string, newKey: string): Promise<void>;
  renameDataset(name: string, newName: string): Promise<void>;
  mountDataset(name: string): Promise<void>;
  unmountDataset(name: string): Promise<void>;
  promoteDataset(name: string): Promise<void>;
  getDatasetProps(name: string): Promise<DatasetPropsResp>;
  setDatasetProp(name: string, property: string, value: string): Promise<void>;
  inheritDatasetProp(name: string, property: string): Promise<void>;
  getDiskSmart(dev: string): Promise<DiskSmartResp>;
  getDiskSmartLog(dev: string): Promise<DiskSmartLogResp>;

  // Operaciones largas
  getLongOps(): Promise<LongOp[]>;
  cancelLongOp(id: string): Promise<void>;

  // Snapshots
  getSnapshots(dataset?: string): Promise<SnapshotGroup[]>;
  createSnapshot(r: CreateSnapshotReq): Promise<void>;
  deleteSnapshot(full: string, confirm: string): Promise<void>;
  rollback(full: string, confirm: string): Promise<void>;
  cloneSnapshot(full: string, target: string, mountpoint?: string): Promise<{ name: string }>;
  snapshotDiff(from: string, to: string): Promise<DiffEntry[]>;

  // Tareas
  getJobs(): Promise<Job[]>;
  createJob(r: CreateJobReq): Promise<void>;
  updateJob(id: number, r: UpdateJobReq): Promise<void>;
  deleteJob(id: number, confirm: string): Promise<void>;
  runJob(id: number): Promise<void>;
  getJobHistory(): Promise<JobHistoryItem[]>;
  getReplicationJobs(): Promise<ReplicationJob[]>;
  createReplicationJob(r: CreateReplicationReq): Promise<void>;
  updateReplicationJob(id: number, r: UpdateReplicationReq): Promise<void>;
  deleteReplicationJob(id: number, confirm: string): Promise<void>;
  runReplicationJob(id: number): Promise<void>;
  getReplicationSSHKey(): Promise<ReplicationSSHKey>;
  testReplication(host: string, user: string, port: number): Promise<ReplicationTestResult>;

  getSystemTimers(): Promise<SystemTimersResp>;
  setSystemTimerSchedule(t: SystemTimer, schedule: string): Promise<void>;
  migrateSystemTimer(t: SystemTimer, newName: string): Promise<void>;

  // Discos
  getDisks(): Promise<Disk[]>;
  getRecommendations(): Promise<Recommendation[]>;
  smartTest(dev: string, type: 'short' | 'long'): Promise<void>;
  poweroffDisk(dev: string): Promise<void>;

  // Notificaciones push
  getPushVapidKey(): Promise<{ publicKey: string }>;
  pushSubscribe(sub: PushSubscriptionJSON, lang: 'es' | 'en'): Promise<void>;
  pushUnsubscribe(endpoint: string): Promise<void>;
  getPushPreferences(): Promise<{ preferences: PushPreference[] }>;
  putPushPreference(tipo: PushAlertTipo, enabled: boolean): Promise<void>;
  getPushQuietHours(): Promise<PushQuietHours>;
  putPushQuietHours(q: { enabled: boolean; start: number; end: number }): Promise<void>;
}
