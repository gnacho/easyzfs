// Todos los modales de la app. Cada uno gestiona su estado y llama al provider;
// al terminar con éxito: cierra y refresca los datos de las vistas.
import { useCallback, useEffect, useMemo, useState } from 'react';
import { getProvider } from '../data';
import { errorMessage, useApp } from '../ui/store';
import { fmtBytes, fmtDateTime, fmtDuration, parseSize } from '../ui/format';
import { statusLabel } from '../ui/labels';
import { ModalBox, useModal } from './Modal';
import { Badge, Seg, InfoBubble, Spinner } from './ui';
import { SYS_SCHED_DEFAULT, buildSysSchedule, parseSysSchedule } from '../ui/syssched';
import type { SysSchedState } from '../ui/syssched';
import type { Dataset, DatasetProp, Disk, DiskSmartLogResp, DiskSmartResp, Job, Pool, PropGroup, ReplicationJob, SystemTimer, Topo } from '../data/types';

// ---------- utilidades comunes ----------
function useLoad<T>(fn: () => Promise<T>, deps: unknown[] = []) {  const [data, setData] = useState<T | null>(null);
  useEffect(() => {
    let alive = true;
    fn().then((d) => { if (alive) setData(d); }).catch(() => { if (alive) setData(null); });
    return () => { alive = false; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps);
  return data;
}

function todayStamp(): string {
  const d = new Date();
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
}

// topoTipKey — clave i18n que explica la topología (ayuda contextual U4).
const topoTipKey = (topo: Topo) => {
  switch (topo) {
    case 'mirror': return 'topo_mirror';
    case 'raidz1': return 'topo_raidz1';
    case 'raidz2': return 'topo_raidz2';
    default: return 'topo_stripe';
  }
};
// topoLabel — nombre corto de la topología para el título del tooltip.
const topoLabel = (topo: Topo) => topo.toUpperCase();

// Botón de envío con estado de carga
function SubmitBtn({ label, busy, disabled, danger }: {
  label: string; busy: boolean; disabled?: boolean; danger?: boolean;
}) {
  return (
    <button type="submit" className={`btn ${danger ? 'solid-danger' : 'primary'}`} disabled={busy || disabled}>
      {busy ? '…' : label}
    </button>
  );
}

// ---------- host: renderiza el modal activo ----------
export function ModalHost() {
  const { modal, closeModal } = useModal();
  if (!modal) return null;
  const p = modal.props ?? {};
  switch (modal.name) {
    case 'newsnap': return <SnapshotModal preset={p.dataset as string | undefined} onClose={closeModal} />;
    case 'newpool': return <NewPoolModal onClose={closeModal} />;
    case 'newds': return <NewDatasetModal vol={!!p.vol} onClose={closeModal} />;
    case 'editds': return <EditDatasetModal ds={p.ds as Dataset} onClose={closeModal} />;
    case 'propsds': return <DatasetPropsModal ds={p.ds as Dataset} onClose={closeModal} />;
    case 'diskdetail': return <DiskDetailModal disk={p.disk as Disk} onClose={closeModal} />;
    case 'delds': return <DeleteDatasetModal name={p.name as string} onClose={closeModal} />;
    case 'renameds': return <RenameDatasetModal name={p.name as string} onClose={closeModal} />;
    case 'rewrite': return <RewriteModal ds={p.ds as Dataset} onClose={closeModal} />;
    case 'unlockds': return <UnlockDatasetModal ds={p.ds as Dataset} onClose={closeModal} />;
    case 'lockds': return <LockDatasetModal ds={p.ds as Dataset} onClose={closeModal} />;
    case 'changekey': return <ChangeKeyModal ds={p.ds as Dataset} onClose={closeModal} />;
    case 'expand': return <ExpandModal pool={p.pool as Pool} onClose={closeModal} />;
    case 'newtask': return <NewTaskModal onClose={closeModal} />;
    case 'sched': return <EditScheduleModal job={p.job as Job} onClose={closeModal} />;
    case 'newrepl': return <NewReplModal onClose={closeModal} />;
    case 'editrepl': return <EditReplModal job={p.job as ReplicationJob} onClose={closeModal} />;
    case 'export': return <ExportPoolModal pool={p.pool as string} onClose={closeModal} />;
    case 'addvdev': return <PoolDiskModal pool={p.pool as string} mode="vdev" onClose={closeModal} />;
    case 'replace': return <PoolDiskModal pool={p.pool as string} mode="replace" presetOld={p.oldDev as string | undefined} presetNew={p.newDev as string | undefined} onClose={closeModal} />;
    case 'detach': return <DetachModal pool={p.pool as string} dev={p.dev as string} path={p.path as string | undefined} onClose={closeModal} />;
    case 'history': return <HistoryModal pool={p.pool as string} onClose={closeModal} />;
    case 'checkpoint': return <CheckpointModal pool={p.pool as string} active={!!p.active} onClose={closeModal} />;
    case 'syssched': return <SysSchedModal task={p.task as SystemTimer} onClose={closeModal} />;
    case 'sysmigrate': return <SysMigrateModal task={p.task as SystemTimer} onClose={closeModal} />;
    case 'newuser': return <NewUserModal onClose={closeModal} />;
    case 'mypass': return <MyPasswdModal onClose={closeModal} />;
    case 'passwd': return <PasswdModal user={p.user as string} onClose={closeModal} />;
    case 'deluser': return <DeleteUserModal user={p.user as string} onClose={closeModal} />;
    case 'rollback': return <RollbackModal full={p.full as string} onClose={closeModal} />;
    case 'delsnap': return <DeleteSnapModal full={p.full as string} onClose={closeModal} />;
    default: return null;
  }
}

// ---------- crear snapshot ----------
function SnapshotModal({ preset, onClose }: { preset?: string; onClose: () => void }) {
  const { t, refresh, notify } = useApp();
  const datasets = useLoad(() => getProvider().getDatasets());
  const pools = useLoad(() => getProvider().getPools());
  const [target, setTarget] = useState(preset ?? '');
  const [name, setName] = useState(`manual-${todayStamp()}`);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');

  useEffect(() => {
    if (!target && datasets?.length) setTarget(datasets[0].name);
  }, [datasets, target]);

  const isPoolTarget = pools?.some((pl) => pl.name === target) ?? false;

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true); setErr('');
    try {
      await getProvider().createSnapshot({ dataset: target, name, recursive: isPoolTarget });
      refresh(); onClose();
      notify(t('toast_snap_created'), 'ok');
    } catch (ex) { const msg = errorMessage(ex, t); setErr(msg); notify(msg, 'err'); setBusy(false); }
  };

  return (
    <ModalBox onClose={onClose} label={t('nsn_title')}>
      <form onSubmit={submit}>
        <h3>{t('nsn_title')}</h3>
        <p className="desc">{t('nsn_desc')}</p>
        <label htmlFor="sn-target">{t('nsn_dataset')}</label>
        <select id="sn-target" value={target} onChange={(e) => setTarget(e.target.value)}>
          {(datasets ?? []).map((d) => <option key={d.name} value={d.name}>{d.name}</option>)}
          {(pools ?? []).map((pl) => <option key={pl.name} value={pl.name}>{pl.name} {t('nsn_recursive')}</option>)}
        </select>
        <label htmlFor="sn-name">{t('nsn_name')}</label>
        <input id="sn-name" value={name} onChange={(e) => setName(e.target.value)} required />
        {err && <p className="form-err" role="alert">{err}</p>}
        <div className="m-actions">
          <button type="button" className="btn" onClick={onClose}>{t('cancel')}</button>
          <SubmitBtn label={t('nsn_create')} busy={busy} disabled={!target || !name.trim()} />
        </div>
      </form>
    </ModalBox>
  );
}

// ---------- historial del pool ----------
function HistoryModal({ pool, onClose }: { pool: string; onClose: () => void }) {
  const { t } = useApp();
  const entries = useLoad(() => getProvider().getPoolHistory(pool), [pool]);

  // Duración: <60 s con decimal; si no, fmtDuration ("4 h 12 min")
  const fmtDur = (sec?: number) => {
    if (!sec) return '—';
    if (sec < 60) return `${(Math.round(sec * 100) / 100).toLocaleString()} s`;
    return fmtDuration(sec);
  };

  return (
    <ModalBox onClose={onClose} wide label={t('hist_title')}>
      <h3>{t('hist_title')} · {pool}</h3>
      <p className="desc">{t('hist_desc', { pool })}</p>
      {!entries && <div className="empty" role="status">{t('loading')}</div>}
      {entries && entries.length === 0 && <div className="empty">{t('hist_empty')}</div>}
      {entries && entries.length > 0 && (
        <div className="tblwrap" style={{ maxHeight: 'min(55vh, 420px)', overflowY: 'auto' }}>
          <table className="data">
            <thead>
              <tr><th>{t('hist_date')}</th><th className="slack">{t('hist_cmd')}</th><th className="num">{t('hist_dur')}</th></tr>
            </thead>
            <tbody>
              {entries.map((e, i) => (
                <tr key={i}>
                  <td style={{ whiteSpace: 'nowrap' }}>{fmtDateTime(e.ts)}</td>
                  <td style={{ whiteSpace: 'normal', fontFamily: 'var(--font-mono)', fontSize: 12.5 }}>{e.command}</td>
                  <td className="num">{fmtDur(e.duration_sec)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <div className="m-actions">
        <button type="button" className="btn" onClick={onClose}>{t('close')}</button>
      </div>
    </ModalBox>
  );
}

// ---------- checkpoint del pool ----------
function CheckpointModal({ pool, active, onClose }: { pool: string; active: boolean; onClose: () => void }) {
  const { t, refresh, notify } = useApp();
  const [confirm, setConfirm] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true); setErr('');
    try {
      await getProvider().checkpointPool(pool, active ? 'discard' : 'create', confirm);
      refresh(); onClose();
      notify(t('toast_checkpoint'), 'ok');
    } catch (ex) { const msg = errorMessage(ex, t); setErr(msg); notify(msg, 'err'); setBusy(false); }
  };

  return (
    <ModalBox onClose={onClose} label={t('ck_title')}>
      <form onSubmit={submit}>
        <h3>{t('ck_title')} · {pool}</h3>
        <p className="desc">{t('ck_desc')}</p>
        <ul className="desc" style={{ paddingLeft: 18, marginTop: 0 }}>
          <li>{t('ck_note1')}</li>
          <li>{t('ck_note2')}</li>
        </ul>
        <p className="desc">{active ? t('ck_state_on') : t('ck_state_off')}</p>
        <label htmlFor="ck-confirm">{t('ck_confirm_lbl')}</label>
        <input id="ck-confirm" value={confirm} onChange={(e) => setConfirm(e.target.value)}
          placeholder={pool} autoComplete="off" required />
        {err && <p className="form-err" role="alert">{err}</p>}
        <div className="m-actions">
          <button type="button" className="btn" onClick={onClose}>{t('cancel')}</button>
          <SubmitBtn label={active ? t('ck_discard') : t('ck_create')} busy={busy}
            disabled={confirm !== pool} danger={active} />
        </div>
      </form>
    </ModalBox>
  );
}

// ---------- crear pool (asistente 2 pasos) ----------
const TOPO_MIN: Record<Topo, number> = { stripe: 1, mirror: 2, raidz1: 3, raidz2: 4, raidz3: 5 };

function NewPoolModal({ onClose }: { onClose: () => void }) {
  const { t, refresh, notify } = useApp();
  const disks = useLoad(() => getProvider().getDisks());
  const [step, setStep] = useState<1 | 2>(1);
  const [name, setName] = useState('');
  const [topo, setTopo] = useState<Topo>('mirror');
  const [ashift, setAshift] = useState(0);
  const [sel, setSel] = useState<Set<string>>(new Set());
  const [confirm, setConfirm] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');

  const free = useMemo(() => (disks ?? []).filter((d) => d.pool === '—' || d.pool === ''), [disks]);
  const toggle = (dev: string) => setSel((s) => {
    const n = new Set(s);
    if (n.has(dev)) n.delete(dev); else n.add(dev);
    return n;
  });

  // Capacidad útil estimada según topología
  const usable = useMemo(() => {
    const chosen = free.filter((d) => sel.has(d.dev));
    if (!chosen.length) return 0;
    const total = chosen.reduce((n, d) => n + d.size_bytes, 0);
    if (topo === 'stripe') return total;
    if (topo === 'mirror') return Math.min(...chosen.map((d) => d.size_bytes));
    const parity = topo === 'raidz1' ? 1 : topo === 'raidz2' ? 2 : 3;
    return Math.min(...chosen.map((d) => d.size_bytes)) * Math.max(0, chosen.length - parity);
  }, [free, sel, topo]);

  const minDisks = TOPO_MIN[topo];
  const canNext = name.trim().length > 0;
  const canCreate = sel.size >= minDisks && confirm.trim() === name.trim();

  const submit = async () => {
    setBusy(true); setErr('');
    try {
      await getProvider().createPool({ name: name.trim(), topo, disks: [...sel], confirm: confirm.trim(), ashift: ashift || undefined });
      refresh(); onClose();
      notify(t('toast_pool_created'), 'ok');
    } catch (ex) { const msg = errorMessage(ex, t); setErr(msg); notify(msg, 'err'); setBusy(false); }
  };

  return (
    <ModalBox onClose={onClose} label={t('np_title')}>
      <h3>{t('np_title')}</h3>
      <p className="desc">{t('np_desc')}</p>
      <div className="chips" style={{ marginTop: 12 }}>
        <span className={`chip ${step === 1 ? 'on' : ''}`}>{t('np_step1')}</span>
        <span className={`chip ${step === 2 ? 'on' : ''}`}>{t('np_step2')}</span>
      </div>

      {step === 1 && (<>
        <label htmlFor="np-name">{t('np_name')}</label>
        <input id="np-name" placeholder={t('np_name_ph')} value={name}
          onChange={(e) => setName(e.target.value)} />
        <label style={{ display: 'inline-flex', alignItems: 'center', gap: 7 }}>{t('np_topo')}
          <InfoBubble title={topoLabel(topo)}>{t(topoTipKey(topo))}</InfoBubble>
        </label>
        <Seg<Topo> ariaLabel={t('np_topo')} value={topo} onChange={setTopo}
          options={[
            { v: 'mirror', label: 'Mirror' }, { v: 'raidz1', label: 'RaidZ1' },
            { v: 'raidz2', label: 'RaidZ2' }, { v: 'stripe', label: 'Stripe' },
          ]} />
        <label htmlFor="np-ashift" style={{ display: 'inline-flex', alignItems: 'center', gap: 7 }}>{t('np_ashift')}
          <InfoBubble title={t('np_ashift_hint')}>{t('np_ashift_hint')}</InfoBubble>
        </label>
        <select id="np-ashift" value={ashift} onChange={(e) => setAshift(Number(e.target.value))}>
          <option value={0}>{t('np_ashift_auto')}</option>
          <option value={12}>{t('np_ashift_12')}</option>
          <option value={13}>{t('np_ashift_13')}</option>
          <option value={9}>{t('np_ashift_9')}</option>
        </select>
        {!canNext && <p className="form-err">{t('np_need_name')}</p>}
        <div className="m-actions">
          <button type="button" className="btn" onClick={onClose}>{t('cancel')}</button>
          <button type="button" className="btn primary" disabled={!canNext} onClick={() => setStep(2)}>{t('np_next')}</button>
        </div>
      </>)}

      {step === 2 && (<>
        <label>{t('np_disks')}</label>
        {free.length === 0 && <p className="desc" style={{ marginTop: 8 }}>{t('np_no_disks')}</p>}
        {free.map((d: Disk) => (
          <div key={d.dev} className={`diskpick ${sel.has(d.dev) ? 'sel' : ''}`} role="checkbox"
            aria-checked={sel.has(d.dev)} tabIndex={0}
            onClick={() => toggle(d.dev)}
            onKeyDown={(e) => { if (e.key === ' ' || e.key === 'Enter') { e.preventDefault(); toggle(d.dev); } }}>
            <span className="mono">{d.dev}</span>
            <span className="muted" style={{ flex: 1 }}>{d.model} · {fmtBytes(d.size_bytes)}</span>
            <span className="badge info">{t('np_free')}</span>
          </div>
        ))}
        <p className="desc" style={{ marginTop: 14 }}>
          ⚠️ {t('np_warn')} {usable > 0 && <>{t('np_usable')}: <b>~{fmtBytes(usable)}</b>.</>}
        </p>
        {sel.size < minDisks && <p className="form-err">{t('np_need_disks', { n: minDisks })}</p>}
        <label htmlFor="np-confirm">{t('ex_confirm_lbl_pool')}</label>
        <input id="np-confirm" placeholder={name.trim()} value={confirm}
          onChange={(e) => setConfirm(e.target.value)} autoComplete="off" />
        {err && <p className="form-err" role="alert">{err}</p>}
        <div className="m-actions">
          <button type="button" className="btn" onClick={() => setStep(1)}>{t('np_back')}</button>
          <button type="button" className="btn primary" disabled={!canCreate || busy} onClick={submit}>
            {busy ? '…' : t('np_create')}
          </button>
        </div>
      </>)}
    </ModalBox>
  );
}

// ---------- nuevo dataset / zvol ----------
function NewDatasetModal({ vol, onClose }: { vol: boolean; onClose: () => void }) {
  const { t, refresh, notify } = useApp();
  const pools = useLoad(() => getProvider().getPools());
  const [pool, setPool] = useState('');
  const [name, setName] = useState('');
  const [comp, setComp] = useState<'lz4' | 'zstd' | 'off'>('lz4');
  const [atime, setAtime] = useState<'on' | 'off' | 'relatime'>('relatime');
  const [quota, setQuota] = useState('');
  const [volsize, setVolsize] = useState('');
  const [enc, setEnc] = useState(false);
  const [pass1, setPass1] = useState('');
  const [pass2, setPass2] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');

  useEffect(() => { if (!pool && pools?.length) setPool(pools[0].name); }, [pools, pool]);

  const passOk = !enc || (pass1.length >= 8 && pass1 === pass2);
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true); setErr('');
    try {
      await getProvider().createDataset({
        pool, name: name.trim(), type: vol ? 'volume' : 'fs', compression: comp, atime,
        quota_bytes: parseSize(quota), volsize_bytes: vol ? parseSize(volsize) : undefined,
        encryption: enc || undefined, passphrase: enc ? pass1 : undefined,
      });
      refresh(); onClose();
      notify(t('toast_ds_created'), 'ok');
    } catch (ex) { const msg = errorMessage(ex, t); setErr(msg); notify(msg, 'err'); setBusy(false); }
  };

  return (
    <ModalBox onClose={onClose} label={vol ? t('nds_title_vol') : t('nds_title_fs')}>
      <form onSubmit={submit}>
        <h3>{vol ? t('nds_title_vol') : t('nds_title_fs')}</h3>
        <p className="desc">{t('nds_desc')}</p>
        <label htmlFor="nd-pool">{t('nds_pool')}</label>
        <select id="nd-pool" value={pool} onChange={(e) => setPool(e.target.value)}>
          {(pools ?? []).map((p: Pool) => <option key={p.name} value={p.name}>{p.name}</option>)}
        </select>
        <label htmlFor="nd-name">{t('nds_name')}</label>
        <input id="nd-name" placeholder={t('nds_name_ph')} value={name} onChange={(e) => setName(e.target.value)} required />
        <label htmlFor="nd-comp">{t('nds_comp')}</label>
        <select id="nd-comp" value={comp} onChange={(e) => setComp(e.target.value as 'lz4' | 'zstd' | 'off')}>
          <option value="lz4">{t('nds_comp_rec')}</option>
          <option value="zstd">zstd</option>
          <option value="off">{t('nds_comp_off')}</option>
        </select>
        <label htmlFor="nd-atime">{t('nds_atime')}</label>
        <select id="nd-atime" value={atime} onChange={(e) => setAtime(e.target.value as 'on' | 'off' | 'relatime')}>
          <option value="relatime">{t('nds_atime_relatime')}</option>
          <option value="on">{t('nds_atime_on')}</option>
          <option value="off">{t('nds_atime_off')}</option>
        </select>
        {vol ? (<>
          <label htmlFor="nd-volsize">{t('nds_volsize')}</label>
          <input id="nd-volsize" placeholder={t('nds_volsize_ph')} value={volsize} onChange={(e) => setVolsize(e.target.value)} required />
        </>) : (<>
          <label htmlFor="nd-quota">{t('nds_quota')}</label>
          <input id="nd-quota" placeholder={t('nds_quota_ph')} value={quota} onChange={(e) => setQuota(e.target.value)} />
        </>)}
        <label className="checklabel" style={{ marginTop: 14 }}>
          <input type="checkbox" checked={enc} onChange={(e) => setEnc(e.target.checked)} />
          {t('nds_enc')}
        </label>
        {enc && (<>
          <p className="desc" style={{ marginTop: 6 }}>{t('nds_enc_hint')}</p>
          <label htmlFor="nd-pass1">{t('nds_pass')}</label>
          <input id="nd-pass1" type="password" value={pass1} onChange={(e) => setPass1(e.target.value)}
            autoComplete="new-password" required minLength={8} />
          <label htmlFor="nd-pass2">{t('nds_pass2')}</label>
          <input id="nd-pass2" type="password" value={pass2} onChange={(e) => setPass2(e.target.value)}
            autoComplete="new-password" required minLength={8} />
          {pass1.length > 0 && pass1.length < 8 && <p className="form-err">{t('nds_pass_short')}</p>}
          {pass1.length >= 8 && pass2 !== '' && pass1 !== pass2 && <p className="form-err">{t('nds_pass_mismatch')}</p>}
        </>)}
        {err && <p className="form-err" role="alert">{err}</p>}
        <div className="m-actions">
          <button type="button" className="btn" onClick={onClose}>{t('cancel')}</button>
          <SubmitBtn label={t('create')} busy={busy} disabled={!name.trim() || !pool || (vol && !volsize.trim()) || !passOk} />
        </div>
      </form>
    </ModalBox>
  );
}

// ---------- editar dataset (cuota / compresión) ----------
function EditDatasetModal({ ds, onClose }: { ds: Dataset; onClose: () => void }) {
  const { t, refresh, notify } = useApp();
  const [comp, setComp] = useState(ds.compression);
  const [quota, setQuota] = useState(ds.quota_bytes ? fmtBytes(ds.quota_bytes).replace('iB', '').replace('B', '') : '');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true); setErr('');
    try {
      await getProvider().updateDataset(ds.name, { compression: comp, quota_bytes: parseSize(quota) });
      refresh(); onClose();
      notify(t('toast_ds_updated'), 'ok');
    } catch (ex) { const msg = errorMessage(ex, t); setErr(msg); notify(msg, 'err'); setBusy(false); }
  };

  return (
    <ModalBox onClose={onClose} label={t('eds_title')}>
      <form onSubmit={submit}>
        <h3>{t('eds_title')}</h3>
        <p className="desc mono">{ds.name}</p>
        <label htmlFor="ed-comp">{t('nds_comp')}</label>
        <select id="ed-comp" value={comp} onChange={(e) => setComp(e.target.value)}>
          <option value="lz4">lz4</option>
          <option value="zstd">zstd</option>
          <option value="off">{t('nds_comp_off')}</option>
        </select>
        {ds.type === 'fs' && (<>
          <label htmlFor="ed-quota">{t('nds_quota')}</label>
          <input id="ed-quota" placeholder={t('nds_quota_ph')} value={quota} onChange={(e) => setQuota(e.target.value)} />
        </>)}
        {err && <p className="form-err" role="alert">{err}</p>}
        <div className="m-actions">
          <button type="button" className="btn" onClick={onClose}>{t('cancel')}</button>
          <SubmitBtn label={t('save')} busy={busy} />
        </div>
      </form>
    </ModalBox>
  );
}

// ---------- propiedades del dataset (U3): tabla completa + editar/inherit ----------
function DatasetPropsModal({ ds, onClose }: { ds: Dataset; onClose: () => void }) {
  const { t, refresh, notify } = useApp();
  const [props, setProps] = useState<DatasetProp[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');
  const [editing, setEditing] = useState<string | null>(null); // nombre de la propiedad en edición
  const [draft, setDraft] = useState('');
  // Contraseña de edición por propiedad: enum/bool → select; resto → input libre
  const [mode, setMode] = useState<'enum' | 'bool' | 'free' | null>(null);

  const load = useCallback(async () => {
    setErr('');
    try {
      setProps((await getProvider().getDatasetProps(ds.name)).properties);
    } catch (e) { setErr(errorMessage(e, t)); }
  }, [ds.name, t]);

  useEffect(() => { load(); }, [load]);

  const group = (name: string): PropGroup => {
    if (name.startsWith('user:') || name.startsWith('org.openzfs:')) return 'user';
    // Whitelist aproximada del front para pintar el grupo; el backend valida
    // de verdad en PATCH (propiedades fuera devuelven invalid_property).
    const known: Record<string, true> = {
      compression: true, recordsize: true, atime: true, relatime: true, sync: true,
      checksum: true, copies: true, xattr: true, acltype: true, aclinherit: true,
      primarycache: true, secondarycache: true, logbias: true, canmount: true,
      mountpoint: true, exec: true, setuid: true, devices: true, readonly: true,
      snapdir: true, quota: true, reservation: true, volsize: true, volblocksize: true,
    };
    return known[name] ? 'editable' : 'readonly';
  };

  const srcLabel = (s: string) => {
    switch (s) {
      case 'local': return t('prp_src_local');
      case 'default': return t('prp_src_default');
      case 'inherited': return t('prp_src_inherited');
      case 'received': return t('prp_src_received');
      case 'temporary': return t('prp_src_temporary');
      default: return s;
    }
  };

  const startEdit = (p: DatasetProp) => {
    setEditing(p.name);
    setDraft(p.value);
    if (p.name === 'atime' || p.name === 'relatime' || p.name === 'exec' ||
        p.name === 'setuid' || p.name === 'devices' || p.name === 'readonly') {
      setMode('bool');
    } else if (p.name === 'compression' || p.name === 'sync' || p.name === 'checksum' ||
        p.name === 'copies' || p.name === 'xattr' || p.name === 'acltype' ||
        p.name === 'aclinherit' || p.name === 'primarycache' || p.name === 'secondarycache' ||
        p.name === 'logbias' || p.name === 'canmount' || p.name === 'snapdir') {
      setMode('enum');
    } else {
      setMode('free');
    }
  };

  const save = async (p: DatasetProp) => {
    setBusy(true); setErr('');
    try {
      await getProvider().setDatasetProp(ds.name, p.name, draft.trim());
      await load();
      setEditing(null);
      refresh();
      notify(t('toast_saved'), 'ok');
    } catch (e) { const msg = errorMessage(e, t); setErr(msg); notify(msg, 'err'); setBusy(false); }
  };

  const inherit = async (p: DatasetProp) => {
    setBusy(true); setErr('');
    try {
      await getProvider().inheritDatasetProp(ds.name, p.name);
      await load();
      refresh();
      notify(t('toast_prop_inherited'), 'ok');
    } catch (e) { const msg = errorMessage(e, t); setErr(msg); notify(msg, 'err'); setBusy(false); }
  };

  const enumValues = (name: string): string[] => {
    switch (name) {
      case 'compression': return ['lz4', 'zstd', 'zlib', 'gzip', 'gzip-1', 'gzip-2', 'gzip-3', 'gzip-4', 'gzip-5', 'gzip-6', 'gzip-7', 'gzip-8', 'gzip-9', 'lzjb', 'off'];
      case 'sync': return ['standard', 'always', 'disabled'];
      case 'checksum': return ['on', 'off', 'fletcher2', 'fletcher4', 'sha256'];
      case 'copies': return ['1', '2', '3'];
      case 'xattr': return ['on', 'off', 'sa'];
      case 'acltype': return ['off', 'posix', 'nfsv4'];
      case 'aclinherit': return ['discard', 'noallow', 'restricted', 'passthrough', 'passthrough-x'];
      case 'primarycache': case 'secondarycache': return ['all', 'none', 'metadata'];
      case 'logbias': return ['latency', 'throughput'];
      case 'canmount': return ['on', 'off', 'noauto'];
      case 'snapdir': return ['hidden', 'visible'];
      default: return [];
    }
  };

  if (!props) {
    return (
      <ModalBox onClose={onClose} wide label={t('prp_title')}>
        <h3>{t('prp_title')}</h3>
        <p className="desc mono">{ds.name}</p>
        <div style={{ padding: '24px 0', textAlign: 'center' }}><Spinner label={t('prp_loaded')} /></div>
      </ModalBox>
    );
  }

  const rows = (g: PropGroup) => props.filter((p) => group(p.name) === g);

  const renderRow = (p: DatasetProp, g: PropGroup) => {
    const isEdit = editing === p.name;
    const editable = g === 'editable';
    return (
      <tr key={p.name}>
        <td className="mono" style={{ fontWeight: 600 }}>{p.name}</td>
        <td className="mono" style={{ color: 'var(--text2)', wordBreak: 'break-all' }}>{p.value}</td>
        <td><Badge tone={p.source === 'local' ? 'info' : 'ok'} dot={false}>{srcLabel(p.source)}</Badge></td>
        <td className="actions">
          {isEdit ? (
            <span style={{ display: 'inline-flex', gap: 6, alignItems: 'center' }}>
              {mode === 'bool' ? (
                <select value={draft} onChange={(e) => setDraft(e.target.value)}>
                  <option value="on">on</option><option value="off">off</option>
                </select>
              ) : mode === 'enum' ? (
                <select value={draft} onChange={(e) => setDraft(e.target.value)}>
                  {enumValues(p.name).map((v) => <option key={v} value={v}>{v}</option>)}
                </select>
              ) : (
                <input value={draft} onChange={(e) => setDraft(e.target.value)} style={{ width: 140 }} />
              )}
              <button className="btn sm" disabled={busy} onClick={() => save(p)}>{t('prp_save')}</button>
              <button className="btn sm" onClick={() => setEditing(null)}>{t('prp_cancel')}</button>
            </span>
          ) : editable ? (
            <span style={{ display: 'inline-flex', gap: 6 }}>
              <button className="btn sm" title={t('prp_edit_hint')} onClick={() => startEdit(p)}>{t('prp_edit')}</button>
              {p.source === 'local' && (
                <button className="btn sm" title={t('prp_inherit_hint')} disabled={busy} onClick={() => inherit(p)}>{t('prp_inherit')}</button>
              )}
            </span>
          ) : null}
        </td>
      </tr>
    );
  };

  return (
    <ModalBox onClose={onClose} wide label={t('prp_title')}>
      <h3>{t('prp_title')}</h3>
      <p className="desc mono">{ds.name}</p>
      <p className="desc" style={{ fontSize: 12, marginBottom: 10 }}>{t('prp_hint')}</p>
      {err && <p className="form-err" role="alert">{err}</p>}
      {props.length === 0 && <div className="empty">{t('prp_empty')}</div>}
      {rows('editable').length > 0 && (
        <h4 style={{ margin: '12px 0 6px', fontSize: 13 }}>{t('prp_group_editable')}</h4>
      )}
      <div className="tblwrap" style={{ maxHeight: '45vh', overflowY: 'auto' }}>
        <table className="data">
          <thead><tr><th>{t('prp_prop')}</th><th>{t('prp_value')}</th><th>{t('prp_source')}</th><th /></tr></thead>
          <tbody>
            {rows('editable').map((p) => renderRow(p, 'editable'))}
            {rows('readonly').map((p) => renderRow(p, 'readonly'))}
          </tbody>
        </table>
      </div>
      {rows('user').length > 0 && (
        <>
          <h4 style={{ margin: '14px 0 6px', fontSize: 13 }}>{t('prp_group_user')}</h4>
          <div className="tblwrap" style={{ maxHeight: '30vh', overflowY: 'auto' }}>
            <table className="data">
              <thead><tr><th>{t('prp_prop')}</th><th>{t('prp_value')}</th><th>{t('prp_source')}</th></tr></thead>
              <tbody>{rows('user').map((p) => renderRow(p, 'user'))}</tbody>
            </table>
          </div>
        </>
      )}
      <div className="m-actions">
        <button type="button" className="btn" onClick={onClose}>{t('cancel')}</button>
      </div>
    </ModalBox>
  );
}

// ---------- detalle SMART del disco (U1): atributos + selftests + errores ----------
function DiskDetailModal({ disk, onClose }: { disk: Disk; onClose: () => void }) {
  const { t } = useApp();
  const [tab, setTab] = useState<'attrs' | 'tests' | 'errors'>('attrs');
  const [smart, setSmart] = useState<DiskSmartResp | null>(null);
  const [log, setLog] = useState<DiskSmartLogResp | null>(null);
  const [err, setErr] = useState('');

  useEffect(() => {
    let alive = true;
    (async () => {
      try {
        const [s, l] = await Promise.all([
          getProvider().getDiskSmart(disk.dev),
          getProvider().getDiskSmartLog(disk.dev),
        ]);
        if (alive) { setSmart(s); setLog(l); }
      } catch (e) { if (alive) setErr(errorMessage(e, t)); }
    })();
    return () => { alive = false; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [disk.dev]);

  const srcLabel = (s: string) => (s === 'Past' ? t('dsm_attr_past') : t('dsm_attr_ok'));

  return (
    <ModalBox onClose={onClose} wide label={t('dsm_title')}>
      <h3>{t('dsm_title')}</h3>
      <p className="desc mono">{disk.dev} · {disk.model}</p>
      <div role="tablist" aria-label={t('dsm_title')} style={{ display: 'inline-flex', gap: 6, marginBottom: 10 }}>
        <button className={`btn sm ${tab === 'attrs' ? 'primary' : ''}`} role="tab" aria-selected={tab === 'attrs'}
          onClick={() => setTab('attrs')}>{t('dsm_tab_attrs')}</button>
        <button className={`btn sm ${tab === 'tests' ? 'primary' : ''}`} role="tab" aria-selected={tab === 'tests'}
          onClick={() => setTab('tests')}>{t('dsm_tab_tests')}</button>
        <button className={`btn sm ${tab === 'errors' ? 'primary' : ''}`} role="tab" aria-selected={tab === 'errors'}
          onClick={() => setTab('errors')}>{t('dsm_tab_errors')}</button>
      </div>
      {err && <p className="form-err" role="alert">{err}</p>}
      {!smart && !err && <div style={{ padding: '24px 0', textAlign: 'center' }}><Spinner label={t('dsm_loading')} /></div>}

      {tab === 'attrs' && smart && (
        <div className="tblwrap" style={{ maxHeight: '45vh', overflowY: 'auto' }}>
          <table className="data">
            <thead><tr>
              <th className="num">{t('dsm_attr_id')}</th><th>{t('dsm_attr_name')}</th>
              <th className="num">{t('dsm_attr_value')}</th><th className="num hide-md">{t('dsm_attr_worst')}</th>
              <th className="num hide-md">{t('dsm_attr_thresh')}</th><th className="mono">{t('dsm_attr_raw')}</th>
              <th>{t('dsm_attr_state')}</th>
            </tr></thead>
            <tbody>
              {(smart.attributes ?? []).map((a) => (
                <tr key={a.id + '-' + a.name}>
                  <td className="num mono">{a.id || '—'}</td>
                  <td style={{ fontWeight: 600 }}>{a.name}</td>
                  <td className="num">{a.value}</td>
                  <td className="num hide-md">{a.worst}</td>
                  <td className="num hide-md">{a.thresh}</td>
                  <td className="mono" style={{ color: 'var(--text2)' }}>{a.raw}</td>
                  <td>
                    {a.when_failed === 'Past' || a.when_failed === 'In the past'
                      ? <Badge tone="warn" dot>{t('dsm_attr_past')}</Badge>
                      : <Badge tone="ok" dot={false}>{t('dsm_attr_ok')}</Badge>}
                  </td>
                </tr>
              ))}
              {smart.attributes?.length === 0 && (
                <tr><td colSpan={7} style={{ textAlign: 'center' }}>{t('dsm_no_attrs')}</td></tr>
              )}
            </tbody>
          </table>
        </div>
      )}

      {tab === 'tests' && log && (
        <div className="tblwrap" style={{ maxHeight: '45vh', overflowY: 'auto' }}>
          <table className="data">
            <thead><tr>
              <th>{t('dsm_test_type')}</th><th>{t('dsm_test_status')}</th>
              <th className="num hide-md">{t('dsm_test_hours')}</th><th className="num">{t('dsm_test_pct')}</th>
            </tr></thead>
            <tbody>
              {(log.selftests ?? []).map((s, i) => (
                <tr key={i}>
                  <td style={{ fontWeight: 600 }}>{s.type}</td>
                  <td>{s.status}</td>
                  <td className="num hide-md">{s.lifetime_hours}</td>
                  <td className="num">{s.percent}%</td>
                </tr>
              ))}
              {log.selftests?.length === 0 && (
                <tr><td colSpan={4} style={{ textAlign: 'center' }}>{t('dsm_no_tests')}</td></tr>
              )}
            </tbody>
          </table>
        </div>
      )}

      {tab === 'errors' && log && (
        <div>
          <p className="desc" style={{ marginBottom: 8 }}>
            {log.error_log.count > 0 ? t('dsm_err_count', { n: log.error_log.count }) : t('dsm_no_errors')}
          </p>
          {(log.error_log.entries ?? []).length > 0 && (
            <div className="tblwrap" style={{ maxHeight: '45vh', overflowY: 'auto' }}>
              <table className="data">
                <thead><tr>
                  <th className="num hide-md">{t('dsm_err_hours')}</th><th>{t('dsm_err_type')}</th><th>{t('dsm_err_detail')}</th>
                </tr></thead>
                <tbody>
                  {(log.error_log.entries ?? []).map((e, i) => (
                    <tr key={i}>
                      <td className="num hide-md">{e.lifetime_hours ?? '—'}</td>
                      <td className="mono">{e.error_type ?? '—'}</td>
                      <td className="mono" style={{ color: 'var(--text2)', wordBreak: 'break-all' }}>{e.detail ?? '—'}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}

      <div className="m-actions">
        <button type="button" className="btn" onClick={onClose}>{t('cancel')}</button>
      </div>
    </ModalBox>
  );
}

// ---------- eliminar dataset (confirmación escrita) ----------
function DeleteDatasetModal({ name, onClose }: { name: string; onClose: () => void }) {
  const { t, refresh, isAdmin, notify } = useApp();
  const [confirm, setConfirm] = useState('');
  const [recursive, setRecursive] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true); setErr('');
    try {
      await getProvider().deleteDataset(name, confirm.trim(), recursive);
      refresh(); onClose();
      notify(t('toast_ds_deleted'), 'ok');
    } catch (ex) { const msg = errorMessage(ex, t); setErr(msg); notify(msg, 'err'); setBusy(false); }
  };

  return (
    <ModalBox onClose={onClose} label={t('dds_title')}>
      <form onSubmit={submit}>
        <h3>{t('dds_title')}</h3>
        <p className="desc">{t('dds_desc')}</p>
        <p className="desc mono" style={{ marginTop: 8 }}>{name}</p>
        <label className="checklabel" style={{ marginTop: 16 }}>
          <input type="checkbox" checked={recursive} onChange={(e) => setRecursive(e.target.checked)} />
          {t('dds_recursive')}
        </label>
        <label htmlFor="dd-confirm">{t('ex_confirm_lbl_dataset')}</label>
        <input id="dd-confirm" placeholder={name} value={confirm} onChange={(e) => setConfirm(e.target.value)} />
        {err && <p className="form-err" role="alert">{err}</p>}
        <div className="m-actions">
          <button type="button" className="btn" onClick={onClose}>{t('cancel')}</button>
          <SubmitBtn label={t('delete')} busy={busy} danger disabled={!isAdmin || confirm.trim() !== name} />
        </div>
      </form>
    </ModalBox>
  );
}

// ---------- reescribir datos del dataset (zfs rewrite; operación larga) ----------
function RewriteModal({ ds, onClose }: { ds: Dataset; onClose: () => void }) {
  const { t, refresh, isAdmin, notify } = useApp();
  const [confirm, setConfirm] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true); setErr('');
    try {
      await getProvider().rewriteDataset(ds.name, confirm.trim());
      refresh(); onClose();
      notify(t('toast_rewrite_started'), 'ok');
    } catch (ex) { const msg = errorMessage(ex, t); setErr(msg); notify(msg, 'err'); setBusy(false); }
  };

  return (
    <ModalBox onClose={onClose} label={t('rw_title')}>
      <form onSubmit={submit}>
        <h3>{t('rw_title')} · <span className="mono">{ds.name}</span></h3>
        <p className="desc">{t('rw_desc', { ds: ds.name })}</p>
        <ul className="desc" style={{ paddingLeft: 18, marginTop: 0 }}>
          <li>{t('rw_note1')}</li>
          <li>{t('rw_note2')}</li>
          <li>{t('rw_note3')}</li>
        </ul>
        <label htmlFor="rw-confirm">{t('rw_confirm_lbl')}</label>
        <input id="rw-confirm" placeholder={ds.name} value={confirm}
          onChange={(e) => setConfirm(e.target.value)} autoComplete="off" />
        {err && <p className="form-err" role="alert">{err}</p>}
        <div className="m-actions">
          <button type="button" className="btn" onClick={onClose}>{t('cancel')}</button>
          <SubmitBtn label={t('rw_btn')} busy={busy} danger disabled={!isAdmin || confirm.trim() !== ds.name} />
        </div>
      </form>
    </ModalBox>
  );
}

// ---------- desbloquear dataset cifrado (zfs load-key) ----------
function UnlockDatasetModal({ ds, onClose }: { ds: Dataset; onClose: () => void }) {
  const { t, refresh, isAdmin, notify } = useApp();
  const [key, setKey] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true); setErr('');
    try {
      await getProvider().unlockDataset(ds.name, key);
      refresh(); onClose();
      notify(t('toast_ds_unlocked'), 'ok');
    } catch (ex) { const msg = errorMessage(ex, t); setErr(msg); notify(msg, 'err'); setBusy(false); }
  };

  return (
    <ModalBox onClose={onClose} label={t('unl_title')}>
      <form onSubmit={submit}>
        <h3>{t('unl_title')} · <span className="mono">{ds.name}</span></h3>
        <p className="desc">{t('unl_desc')}</p>
        <label htmlFor="unl-key">{t('unl_key')}</label>
        <input id="unl-key" type="password" value={key} onChange={(e) => setKey(e.target.value)}
          autoComplete="off" required autoFocus />
        {err && <p className="form-err" role="alert">{err}</p>}
        <div className="m-actions">
          <button type="button" className="btn" onClick={onClose}>{t('cancel')}</button>
          <SubmitBtn label={t('ds_unlock')} busy={busy} disabled={!isAdmin || !key} />
        </div>
      </form>
    </ModalBox>
  );
}

// ---------- bloquear dataset cifrado (zfs unload-key) ----------
function LockDatasetModal({ ds, onClose }: { ds: Dataset; onClose: () => void }) {
  const { t, refresh, isAdmin, notify } = useApp();
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true); setErr('');
    try {
      await getProvider().lockDataset(ds.name);
      refresh(); onClose();
      notify(t('toast_ds_locked'), 'ok');
    } catch (ex) { const msg = errorMessage(ex, t); setErr(msg); notify(msg, 'err'); setBusy(false); }
  };

  return (
    <ModalBox onClose={onClose} label={t('lck_title')}>
      <form onSubmit={submit}>
        <h3>{t('lck_title')} · <span className="mono">{ds.name}</span></h3>
        <p className="desc">{t('lck_desc')}</p>
        {err && <p className="form-err" role="alert">{err}</p>}
        <div className="m-actions">
          <button type="button" className="btn" onClick={onClose}>{t('cancel')}</button>
          <SubmitBtn label={t('ds_lock')} busy={busy} disabled={!isAdmin} danger />
        </div>
      </form>
    </ModalBox>
  );
}

// ---------- cambiar la passphrase de un dataset cifrado (zfs change-key) ----------
function ChangeKeyModal({ ds, onClose }: { ds: Dataset; onClose: () => void }) {
  const { t, refresh, isAdmin, notify } = useApp();
  const [cur, setCur] = useState('');
  const [n1, setN1] = useState('');
  const [n2, setN2] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');

  const ok = !!cur && n1.length >= 8 && n1 === n2;
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true); setErr('');
    try {
      await getProvider().changeDatasetKey(ds.name, cur, n1);
      refresh(); onClose();
      notify(t('toast_key_changed'), 'ok');
    } catch (ex) { const msg = errorMessage(ex, t); setErr(msg); notify(msg, 'err'); setBusy(false); }
  };

  return (
    <ModalBox onClose={onClose} label={t('ckk_title')}>
      <form onSubmit={submit}>
        <h3>{t('ckk_title')} · <span className="mono">{ds.name}</span></h3>
        <p className="desc">{t('ckk_desc')}</p>
        <label htmlFor="ckk-cur">{t('ckk_current')}</label>
        <input id="ckk-cur" type="password" value={cur} onChange={(e) => setCur(e.target.value)}
          autoComplete="off" required />
        <label htmlFor="ckk-n1">{t('ckk_new')}</label>
        <input id="ckk-n1" type="password" value={n1} onChange={(e) => setN1(e.target.value)}
          autoComplete="new-password" required minLength={8} />
        <label htmlFor="ckk-n2">{t('nds_pass2')}</label>
        <input id="ckk-n2" type="password" value={n2} onChange={(e) => setN2(e.target.value)}
          autoComplete="new-password" required minLength={8} />
        {n1.length > 0 && n1.length < 8 && <p className="form-err">{t('nds_pass_short')}</p>}
        {n1.length >= 8 && n2 !== '' && n1 !== n2 && <p className="form-err">{t('nds_pass_mismatch')}</p>}
        {err && <p className="form-err" role="alert">{err}</p>}
        <div className="m-actions">
          <button type="button" className="btn" onClick={onClose}>{t('cancel')}</button>
          <SubmitBtn label={t('ckk_btn')} busy={busy} disabled={!isAdmin || !ok} />
        </div>
      </form>
    </ModalBox>
  );
}

// ---------- expandir un vdev raidz (RAID-Z expansion, OpenZFS ≥ 2.3) ----------
function ExpandModal({ pool, onClose }: { pool: Pool; onClose: () => void }) {
  const { t, refresh, isAdmin, caps, notify } = useApp();
  const disks = useLoad(() => getProvider().getDisks());
  const vdevs = pool.raidz_vdevs ?? [];
  const [vdev, setVdev] = useState(vdevs[0] ?? '');
  const [disk, setDisk] = useState('');
  const [confirm, setConfirm] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');

  const free = useMemo(() => (disks ?? []).filter((d) => (d.pool === '—' || d.pool === '') && !d.in_use), [disks]);
  useEffect(() => { if (!disk && free.length) setDisk(free[0].dev); }, [free, disk]);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true); setErr('');
    try {
      await getProvider().expandPool(pool.name, vdev, disk, confirm.trim());
      refresh(); onClose();
      notify(t('toast_expand_started'), 'ok');
    } catch (ex) { const msg = errorMessage(ex, t); setErr(msg); notify(msg, 'err'); setBusy(false); }
  };

  return (
    <ModalBox onClose={onClose} label={t('xpd_title')}>
      <form onSubmit={submit}>
        <h3>{t('xpd_title')} · <span className="mono">{pool.name}</span></h3>
        <p className="desc">
          {t('xpd_desc', { vdev, disk: disk || '…' })}
          {' '}<InfoBubble title={t('xpd_title')}>
            <ul style={{ margin: 0, paddingLeft: 16 }}>
              <li>{t('xpd_note1')}</li>
              <li>{t('xpd_note2')}{caps?.rewrite ? ` ${t('xpd_note2b')}` : ''}</li>
              <li>{t('xpd_note3')}</li>
              <li>{t('xpd_note4')}</li>
            </ul>
          </InfoBubble>
        </p>
        <ul className="desc" style={{ paddingLeft: 18, marginTop: 0, color: 'var(--warn)' }}>
          <li>{t('xpd_note1')}</li>
          <li>{t('xpd_note2')}{caps?.rewrite ? ` ${t('xpd_note2b')}` : ''}</li>
          <li>{t('xpd_note3')}</li>
          <li>{t('xpd_note4')}</li>
        </ul>
        <label htmlFor="xpd-vdev">{t('xpd_vdev')}</label>
        <select id="xpd-vdev" value={vdev} onChange={(e) => setVdev(e.target.value)}>
          {vdevs.map((v) => <option key={v} value={v}>{v}</option>)}
        </select>
        <label htmlFor="xpd-disk">{t('xpd_disk')}</label>
        {free.length === 0 && <p className="desc" style={{ marginTop: 8 }}>{t('np_no_disks')}</p>}
        {free.length > 0 && (
          <select id="xpd-disk" value={disk} onChange={(e) => setDisk(e.target.value)}>
            {free.map((d) => (
              <option key={d.dev} value={d.dev}>{d.dev} · {d.model} · {fmtBytes(d.size_bytes)}</option>
            ))}
          </select>
        )}
        <label htmlFor="xpd-confirm">{t('ex_confirm_lbl_pool')}</label>
        <input id="xpd-confirm" placeholder={pool.name} value={confirm}
          onChange={(e) => setConfirm(e.target.value)} autoComplete="off" />
        {err && <p className="form-err" role="alert">{err}</p>}
        <div className="m-actions">
          <button type="button" className="btn" onClick={onClose}>{t('cancel')}</button>
          <SubmitBtn label={t('xpd_btn')} busy={busy} danger
            disabled={!isAdmin || !vdev || !disk || confirm.trim() !== pool.name} />
        </div>
      </form>
    </ModalBox>
  );
}

// ---------- exportar pool ----------
function ExportPoolModal({ pool, onClose }: { pool: string; onClose: () => void }) {
  const { t, refresh, isAdmin, notify } = useApp();
  const [force, setForce] = useState(false);
  const [destroy, setDestroy] = useState(false);
  const [confirm, setConfirm] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true); setErr('');
    try {
      await getProvider().exportPool(pool, confirm.trim(), force, destroy);
      refresh(); onClose();
      notify(t('toast_pool_exported'), 'ok');
    } catch (ex) { const msg = errorMessage(ex, t); setErr(msg); notify(msg, 'err'); setBusy(false); }
  };

  return (
    <ModalBox onClose={onClose} label={t('ex_title')}>
      <form onSubmit={submit}>
        <h3>{t('ex_title')}</h3>
        <p className="desc">{t('ex_desc')} <b className="mono">{pool}</b></p>
        <label className="checklabel" style={{ marginTop: 16 }}>
          <input type="checkbox" checked={force} onChange={(e) => setForce(e.target.checked)} />
          {t('ex_force')}
        </label>
        <label className="checklabel" style={{ color: 'var(--err)' }}>
          <input type="checkbox" checked={destroy} onChange={(e) => setDestroy(e.target.checked)} />
          {t('ex_destroy')}
        </label>
        <label htmlFor="ex-confirm">{t('ex_confirm_lbl_pool')}</label>
        <input id="ex-confirm" placeholder={pool} value={confirm} onChange={(e) => setConfirm(e.target.value)} />
        {err && <p className="form-err" role="alert">{err}</p>}
        <div className="m-actions">
          <button type="button" className="btn" onClick={onClose}>{t('cancel')}</button>
          <SubmitBtn label={t('ex_btn')} busy={busy} danger disabled={!isAdmin || confirm.trim() !== pool} />
        </div>
      </form>
    </ModalBox>
  );
}

// devBase — nombre base del dispositivo ('/dev/sda1'→'sda', 'nvme0n1p2'→'nvme0n1').
function devBase(dev: string): string {
  const d = dev.replace(/^\/dev\//, '');
  const pi = d.lastIndexOf('p');
  if (pi > 0 && /^\d+$/.test(d.slice(pi + 1)) && !/^\d+$/.test(d.slice(0, pi))) return d.slice(0, pi);
  const m = d.match(/^(xvd|sd|vd|hd)([a-z]+)\d+$/);
  if (m) return m[1] + m[2];
  return d;
}

// ---------- añadir vdev / sustituir disco (confirmación escrita) ----------
// Modal genérico para las dos operaciones destructivas de pool: pide el
// nombre del pool para confirmar y envía {confirm} al backend.
function PoolDiskModal({ pool, mode, presetOld, presetNew, onClose }: { pool: string; mode: 'vdev' | 'replace'; presetOld?: string; presetNew?: string; onClose: () => void }) {
  const { t, refresh, isAdmin, notify } = useApp();
  const disks = useLoad(() => getProvider().getDisks());
  const pools = useLoad(() => getProvider().getPools());
  const [topo, setTopo] = useState<Topo>('mirror');
  const [sel, setSel] = useState<Set<string>>(new Set());
  const [oldDev, setOldDev] = useState(presetOld ?? '');
  const [newDev, setNewDev] = useState(presetNew ?? '');
  const [confirm, setConfirm] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');

  const free = useMemo(() => (disks ?? []).filter((d) => (d.pool === '—' || d.pool === '') && !d.in_use), [disks]);
  const current = useMemo(() => pools?.find((pl) => pl.name === pool)?.vdevs ?? [], [pools, pool]);
  const [showAll, setShowAll] = useState(false);
  // tamaño del vdev a sustituir (si el disco viejo sigue visible): el nuevo debe ser >=
  const oldVdev = current.find((v) => v.dev === oldDev);
  const oldDisk = (disks ?? []).find((d) => oldVdev?.path ? d.dev === devBase(oldVdev.path) : d.dev === oldDev);
  const newDisk = (disks ?? []).find((d) => d.dev === newDev);
  const oldSize = oldDisk?.size_bytes ?? 0;
  const suitable = useMemo(() => free.filter((d) => oldSize === 0 || d.size_bytes >= oldSize), [free, oldSize]);
  const hidden = free.length - suitable.length;
  const toggle = (dev: string) => setSel((s) => {
    const n = new Set(s);
    if (n.has(dev)) n.delete(dev); else n.add(dev);
    return n;
  });

  useEffect(() => {
    if (mode === 'replace') {
      if (!oldDev && current.length) {
        const bad = current.find((v) => v.status !== 'ONLINE');
        setOldDev((bad ?? current[0]).dev);
      }
      if (!newDev && suitable.length) setNewDev(suitable[0].dev);
    }
  }, [mode, current, suitable, oldDev, newDev]);

  const confirmed = confirm.trim() === pool;
  const minDisks = TOPO_MIN[topo];
  const valid = mode === 'vdev'
    ? sel.size >= minDisks
    : !!oldDev && !!newDev && oldDev !== newDev;

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true); setErr('');
    try {
      if (mode === 'vdev') await getProvider().addVdev(pool, topo, [...sel], confirm.trim());
      else {
        // by-id si existe: las letras sdX son inestables entre arranques (#65)
        const outDev = newDisk?.by_id ? '/dev/disk/by-id/' + newDisk.by_id : newDev;
        await getProvider().replaceDisk(pool, oldDev, outDev, confirm.trim());
      }
      refresh(); onClose();
      notify(t(mode === 'vdev' ? 'toast_vdev_added' : 'toast_disk_replacing'), 'ok');
    } catch (ex) { const msg = errorMessage(ex, t); setErr(msg); notify(msg, 'err'); setBusy(false); }
  };

  return (
    <ModalBox onClose={onClose} label={mode === 'vdev' ? t('av_title') : t('rp_title')}>
      <form onSubmit={submit}>
        <h3>{mode === 'vdev' ? t('av_title') : t('rp_title')}</h3>
        <p className="desc">
          {t(mode === 'vdev' ? 'av_desc' : 'rp_desc')} <b className="mono">{pool}</b>
        </p>

        {mode === 'vdev' && (<>
          <label style={{ display: 'inline-flex', alignItems: 'center', gap: 7 }}>{t('np_topo')}
            <InfoBubble title={topoLabel(topo)}>{t(topoTipKey(topo))}</InfoBubble>
          </label>
          <Seg<Topo> ariaLabel={t('np_topo')} value={topo} onChange={setTopo}
            options={[
              { v: 'mirror', label: 'Mirror' }, { v: 'raidz1', label: 'RaidZ1' },
              { v: 'raidz2', label: 'RaidZ2' }, { v: 'stripe', label: 'Stripe' },
            ]} />
          <label>{t('np_disks')}</label>
          {free.length === 0 && <p className="desc" style={{ marginTop: 8 }}>{t('np_no_disks')}</p>}
          {free.map((d: Disk) => (
            <div key={d.dev} className={`diskpick ${sel.has(d.dev) ? 'sel' : ''}`} role="checkbox"
              aria-checked={sel.has(d.dev)} tabIndex={0}
              onClick={() => toggle(d.dev)}
              onKeyDown={(e) => { if (e.key === ' ' || e.key === 'Enter') { e.preventDefault(); toggle(d.dev); } }}>
              <span className="mono">{d.dev}</span>
              <span className="muted" style={{ flex: 1 }}>{d.model} · {fmtBytes(d.size_bytes)}</span>
              <span className="badge info">{t('np_free')}</span>
            </div>
          ))}
          {sel.size < minDisks && <p className="form-err">{t('np_need_disks', { n: minDisks })}</p>}
        </>)}

        {mode === 'replace' && (<>
          <p className="desc" style={{ marginTop: 0, color: 'var(--text2)' }}>{t('rp_proc')}</p>
          <label htmlFor="rp-old">{t('rp_old')}</label>
          <select id="rp-old" value={oldDev} onChange={(e) => setOldDev(e.target.value)} disabled={!!presetOld}>
            {current.filter((v) => !v.replacing).map((v) => (
              <option key={v.dev} value={v.dev}>
                {v.path ? v.path.replace('/dev/', '') : v.dev} ({v.role}){v.status !== 'ONLINE' ? ` · ${statusLabel(v.status, t)}` : ''}
              </option>
            ))}
          </select>
          <label htmlFor="rp-new">{t('rp_new')}</label>
          {(() => {
            const list = showAll ? free : suitable;
            return (<>
              {list.length === 0 && (
                <p className="desc" style={{ marginTop: 8 }}>
                  {t('np_no_disks')}{hidden > 0 && ` ${t('rp_small_hidden', { n: hidden })}`}
                </p>
              )}
              {list.length > 0 && (
                <select id="rp-new" value={newDev} onChange={(e) => setNewDev(e.target.value)} disabled={!!presetNew}>
                  {list.map((d) => {
                    const small = oldSize > 0 && d.size_bytes < oldSize;
                    return (
                      <option key={d.dev} value={d.dev} disabled={small && !showAll}>
                        {d.dev} · {d.model} · {fmtBytes(d.size_bytes)}{small ? ` (${t('rp_small')})` : ''}
                      </option>
                    );
                  })}
                </select>
              )}
              {hidden > 0 && (
                <label className="checklabel" style={{ gap: 7, marginTop: 6, fontSize: 12.5, color: 'var(--text2)', cursor: 'pointer' }}>
                  <input type="checkbox" checked={showAll} onChange={(e) => setShowAll(e.target.checked)} />
                  {t('rp_show_all', { n: hidden })}
                </label>
              )}
             </>);
          })()}

          {/* #65: resumen explícito origen/destino con modelo+serial antes de confirmar */}
          <div className="rp-summary">
            <div>
              <span className="lbl">{t('rp_old')}</span>
              <span className="mono">
                {oldVdev?.path ? oldVdev.path.replace('/dev/', '') : oldDev}
                {oldDisk ? ` · ${oldDisk.model} · S/N ${oldDisk.serial}` : ''}
              </span>
            </div>
            <div>
              <span className="lbl">{t('rp_new')}</span>
              <span className="mono">
                {newDev}
                {newDisk ? ` · ${newDisk.model} · S/N ${newDisk.serial}` : ''}
              </span>
            </div>
            {newDisk?.by_id && (
              <div>
                <span className="lbl">{t('rp_byid')}</span>
                <span className="mono wrap">/dev/disk/by-id/{newDisk.by_id}</span>
              </div>
            )}
          </div>
          {/* #65: aviso fuerte si el origen es un miembro ONLINE sano */}
          {oldVdev?.status === 'ONLINE' && (
            <p className="rp-warn" role="alert">{t('rp_warn_online')}</p>
          )}
        </>)}

        <label htmlFor="pd-confirm">{t('ex_confirm_lbl_pool')}</label>
        <input id="pd-confirm" placeholder={pool} value={confirm}
          onChange={(e) => setConfirm(e.target.value)} autoComplete="off" />
        {err && <p className="form-err" role="alert">{err}</p>}
        <div className="m-actions">
          <button type="button" className="btn" onClick={onClose}>{t('cancel')}</button>
          <SubmitBtn label={mode === 'vdev' ? t('av_btn') : t('rp_btn')} busy={busy} danger
            disabled={!isAdmin || !confirmed || !valid} />
        </div>
      </form>
    </ModalBox>
  );
}

// ---------- editar periodicidad de una tarea del sistema ----------
// Modo fácil por defecto: preset (cada hora/diario/semanal/mensual) que se
// convierte EN CLIENTE a la sintaxis del origen (cron u OnCalendar). El modo
// avanzado muestra el input en crudo. Si la schedule actual no encaja en un
// preset simple, se abre directamente en avanzado.
function SysSchedModal({ task, onClose }: { task: SystemTimer; onClose: () => void }) {
  const { t, refresh, isAdmin, notify } = useApp();
  const isCron = task.source === 'cron';
  const [initial] = useState(() => parseSysSchedule(task.schedule ?? '', task.source));
  const [advanced, setAdvanced] = useState(initial === null);
  const [preset, setPreset] = useState<SysSchedState>(() => initial ?? SYS_SCHED_DEFAULT);
  const [raw, setRaw] = useState(task.schedule ?? '');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');

  const generated = buildSysSchedule(preset, task.source);
  const sched = advanced ? raw.trim() : generated;

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true); setErr('');
    try {
      await getProvider().setSystemTimerSchedule(task, sched);
      refresh(); onClose();
      notify(t('toast_saved'), 'ok');
    } catch (ex) { const msg = errorMessage(ex, t); setErr(msg); notify(msg, 'err'); setBusy(false); }
  };

  return (
    <ModalBox onClose={onClose} label={t('ss_title')}>
      <form onSubmit={submit}>
        <h3>{t('ss_title')}</h3>
        <p className="desc">
          <b className="mono">{task.name}</b> · <span className={`badge ${isCron ? 'warn' : 'info'}`} style={{ padding: '1px 7px' }}>{task.source}</span>
        </p>
        <p className="desc mono" style={{ fontSize: 12 }}>{task.command}</p>
        <label>{t('ss_mode')}</label>
        <Seg value={advanced ? 'adv' : 'easy'} onChange={(v) => setAdvanced(v === 'adv')} ariaLabel={t('ss_mode')}
          options={[
            { v: 'easy', label: t('ss_mode_easy') },
            { v: 'adv', label: t('ss_mode_adv') },
          ]} />
        {!advanced && (<>
          <ScheduleFields s={preset} set={setPreset} showRetention={false}
            retention="" setRetention={() => { /* sin retención en tareas del sistema */ }} t={t as never} />
          <label htmlFor="ss-result">{t('ss_result')}</label>
          <input id="ss-result" readOnly value={generated} className="mono" />
        </>)}
        {advanced && (<>
          <label htmlFor="ss-sched">{t('ss_lbl')}</label>
          <input id="ss-sched" value={raw} onChange={(e) => setRaw(e.target.value)}
            placeholder={isCron ? '0 3 * * *' : 'daily'} autoComplete="off" className="mono" />
          <p className="desc" style={{ marginTop: 6, fontSize: 12, color: 'var(--text2)' }}>
            {isCron ? t('ss_hint_cron') : t('ss_hint_sysd')}
          </p>
        </>)}
        {err && <p className="form-err" role="alert">{err}</p>}
        <div className="m-actions">
          <button type="button" className="btn" onClick={onClose}>{t('cancel')}</button>
          <SubmitBtn label={t('save')} busy={busy} disabled={!isAdmin || !sched} />
        </div>
      </form>
    </ModalBox>
  );
}

// ---------- cambiar cron → systemd timer ----------
function SysMigrateModal({ task, onClose }: { task: SystemTimer; onClose: () => void }) {
  const { t, refresh, isAdmin, notify } = useApp();
  const [name, setName] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true); setErr('');
    try {
      await getProvider().migrateSystemTimer(task, name.trim());
      refresh(); onClose();
      notify(t('toast_timer_migrated'), 'ok');
    } catch (ex) { const msg = errorMessage(ex, t); setErr(msg); notify(msg, 'err'); setBusy(false); }
  };

  return (
    <ModalBox onClose={onClose} label={t('sm_title')}>
      <form onSubmit={submit}>
        <h3>{t('sm_title')}</h3>
        <p className="desc">{t('sm_desc')}</p>
        <p className="desc mono" style={{ fontSize: 12 }}>
          {task.schedule} · {task.command}
        </p>
        <p className="desc" style={{ fontSize: 12, color: 'var(--text2)' }}>{t('sm_note')}</p>
        <label htmlFor="sm-name">{t('sm_name_lbl')}</label>
        <input id="sm-name" value={name} onChange={(e) => setName(e.target.value)}
          placeholder={t('sm_name_ph')} autoComplete="off" className="mono" pattern="[a-z0-9][a-z0-9\-]*" />
        <p className="desc" style={{ marginTop: 6, fontSize: 12, color: 'var(--text2)' }}>
          easyzfs-<b>{name || '…'}</b>.timer
        </p>
        {err && <p className="form-err" role="alert">{err}</p>}
        <div className="m-actions">
          <button type="button" className="btn" onClick={onClose}>{t('cancel')}</button>
          <SubmitBtn label={t('sm_btn')} busy={busy} disabled={!isAdmin || !/^[a-z0-9][a-z0-9-]*$/.test(name)} />
        </div>
      </form>
    </ModalBox>
  );
}

// ---------- retirar disco de un mirror (confirmación escrita) ----------
function DetachModal({ pool, dev, path, onClose }: { pool: string; dev: string; path?: string; onClose: () => void }) {
  const { t, refresh, isAdmin, notify } = useApp();
  const [confirm, setConfirm] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');
  const label = path ? path.replace('/dev/', '') : dev;

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true); setErr('');
    try {
      await getProvider().vdevAction(pool, dev, 'detach', confirm.trim());
      refresh(); onClose();
      notify(t('toast_action_done'), 'ok');
    } catch (ex) { const msg = errorMessage(ex, t); setErr(msg); notify(msg, 'err'); setBusy(false); }
  };

  return (
    <ModalBox onClose={onClose} label={t('dt_title')}>
      <form onSubmit={submit}>
        <h3>{t('dt_title')}</h3>
        <p className="desc">{t('dt_desc', { dev: label, pool })}</p>
        <label htmlFor="dt-confirm">{t('ex_confirm_lbl_pool')}</label>
        <input id="dt-confirm" placeholder={pool} value={confirm} onChange={(e) => setConfirm(e.target.value)} autoComplete="off" />
        {err && <p className="form-err" role="alert">{err}</p>}
        <div className="m-actions">
          <button type="button" className="btn" onClick={onClose}>{t('cancel')}</button>
          <SubmitBtn label={t('vdev_detach')} busy={busy} danger
            disabled={!isAdmin || confirm.trim() !== pool} />
        </div>
      </form>
    </ModalBox>
  );
}

// ---------- rollback de snapshot ----------
function RollbackModal({ full, onClose }: { full: string; onClose: () => void }) {
  const { t, refresh, isAdmin, notify } = useApp();
  const [confirm, setConfirm] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');
  const [ds, snap] = full.split('@');
  // Se acepta el nombre corto (dataset) o la ruta completa que muestra el modal
  const rbConfirmOk = [ds, full].includes(confirm.trim());

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true); setErr('');
    try {
      // El backend exige confirm == ruta completa; la UI pide el dataset
      await getProvider().rollback(full, confirm.trim() === ds ? full : confirm.trim());
      refresh(); onClose();
      notify(t('toast_rollback'), 'ok');
    } catch (ex) { const msg = errorMessage(ex, t); setErr(msg); notify(msg, 'err'); setBusy(false); }
  };

  return (
    <ModalBox onClose={onClose} label={t('rb_title')}>
      <form onSubmit={submit}>
        <h3>{t('rb_title')}</h3>
        <p className="desc">{t('rb_desc1')} <b className="mono">{ds}</b> {t('rb_desc2')} <b className="mono">{snap}</b>.</p>
        <p className="desc" style={{ marginTop: 10, color: 'var(--err)' }}>⚠️ {t('rb_warn')}</p>
        <label htmlFor="rb-confirm">{t('rb_confirm_lbl')}</label>
        <input id="rb-confirm" placeholder={ds} value={confirm} onChange={(e) => setConfirm(e.target.value)} />
        {err && <p className="form-err" role="alert">{err}</p>}
        <div className="m-actions">
          <button type="button" className="btn" onClick={onClose}>{t('cancel')}</button>
          <SubmitBtn label={t('rb_btn')} busy={busy} danger disabled={!isAdmin || !rbConfirmOk} />
        </div>
      </form>
    </ModalBox>
  );
}

// ---------- eliminar snapshot ----------
function DeleteSnapModal({ full, onClose }: { full: string; onClose: () => void }) {
  const { t, refresh, isAdmin, notify } = useApp();
  const [confirm, setConfirm] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');
  const [, snap] = full.split('@');
  // Se acepta el nombre corto (tras la @) o la ruta completa que muestra el modal
  const dsnConfirmOk = [snap, full].includes(confirm.trim());

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true); setErr('');
    try {
      // El backend exige confirm == ruta completa; la UI pide el nombre corto
      await getProvider().deleteSnapshot(full, confirm.trim() === snap ? full : confirm.trim());
      refresh(); onClose();
      notify(t('toast_snap_deleted'), 'ok');
    } catch (ex) { const msg = errorMessage(ex, t); setErr(msg); notify(msg, 'err'); setBusy(false); }
  };

  return (
    <ModalBox onClose={onClose} label={t('dsn_title')}>
      <form onSubmit={submit}>
        <h3>{t('dsn_title')}</h3>
        <p className="desc">{t('dsn_desc')}</p>
        <p className="desc mono" style={{ marginTop: 8 }}>{full}</p>
        <label htmlFor="ds-confirm">{t('dsn_confirm_lbl')}</label>
        <input id="ds-confirm" placeholder={snap} value={confirm} onChange={(e) => setConfirm(e.target.value)} />
        {err && <p className="form-err" role="alert">{err}</p>}
        <div className="m-actions">
          <button type="button" className="btn" onClick={onClose}>{t('cancel')}</button>
          <SubmitBtn label={t('delete')} busy={busy} danger disabled={!isAdmin || !dsnConfirmOk} />
        </div>
      </form>
    </ModalBox>
  );
}

// ---------- tareas programadas ----------
type Freq = 'hourly' | 'daily' | 'weekly' | 'monthly';
const WD_KEYS = ['mon', 'tue', 'wed', 'thu', 'fri', 'sat', 'sun'] as const;
type Wd = (typeof WD_KEYS)[number];

interface SchedState { freq: Freq; minute: string; time: string; weekday: Wd; monthday: number }

// Construye la cadena schedule del contrato: hourly@:15 | daily@06:00 | weekly:sun@03:00 | monthly:1@02:00
export function buildSchedule(s: SchedState): string {
  switch (s.freq) {
    case 'hourly': return `hourly@:${s.minute.padStart(2, '0')}`;
    case 'daily': return `daily@${s.time}`;
    case 'weekly': return `weekly:${s.weekday}@${s.time}`;
    case 'monthly': return `monthly:${s.monthday}@${s.time}`;
  }
}

// Parsea una cadena schedule al estado del editor
export function parseSchedule(sc: string): SchedState {
  const def: SchedState = { freq: 'daily', minute: '15', time: '06:00', weekday: 'sun', monthday: 1 };
  try {
    if (sc.startsWith('hourly@:')) return { ...def, freq: 'hourly', minute: sc.slice(8) || '00' };
    if (sc.startsWith('daily@')) return { ...def, freq: 'daily', time: sc.slice(6) || '06:00' };
    const w = /^weekly:(\w+)@(.+)$/.exec(sc);
    if (w) return { ...def, freq: 'weekly', weekday: (WD_KEYS.includes(w[1] as Wd) ? w[1] : 'sun') as Wd, time: w[2] };
    const m = /^monthly:(\d+)@(.+)$/.exec(sc);
    if (m) return { ...def, freq: 'monthly', monthday: parseInt(m[1], 10) || 1, time: m[2] };
  } catch { /* formato desconocido: valores por defecto */ }
  return def;
}

// Describe una schedule en lenguaje natural (para la lista de tareas)
export function describeSchedule(sc: string, t: (k: never, vars?: Record<string, string | number>) => string): string {
  const s = parseSchedule(sc);
  const T = t as (k: string, vars?: Record<string, string | number>) => string;
  switch (s.freq) {
    case 'hourly': return `${T('freq_hourly')} · :${s.minute.padStart(2, '0')}`;
    case 'daily': return `${T('freq_daily')} · ${s.time}`;
    case 'weekly': return `${T('freq_weekly')} · ${T(`wdl_${s.weekday}`)} ${s.time}`;
    case 'monthly': return `${T('freq_monthly')} · ${s.monthday} · ${s.time}`;
  }
}

// Campos de frecuencia compartidos entre "nueva tarea" y "editar programación"
function ScheduleFields({ s, set, showRetention, retention, setRetention, t }: {
  s: SchedState; set: (v: SchedState) => void;
  showRetention: boolean; retention: string; setRetention: (v: string) => void;
  t: (k: never, vars?: Record<string, string | number>) => string;
}) {
  const T = t as (k: string) => string;
  return (<>
    <label>{T('nt_freq')}</label>
    <Seg<Freq> value={s.freq} onChange={(freq) => set({ ...s, freq })} ariaLabel={T('nt_freq')}
      options={[
        { v: 'hourly', label: T('nt_hourly') }, { v: 'daily', label: T('nt_daily') },
        { v: 'weekly', label: T('nt_weekly') }, { v: 'monthly', label: T('nt_monthly') },
      ]} />
    {s.freq === 'hourly' && (<>
      <label htmlFor="sch-min">{T('nt_minute')}</label>
      <input id="sch-min" type="number" min={0} max={59} value={s.minute}
        onChange={(e) => set({ ...s, minute: e.target.value })} />
    </>)}
    {s.freq === 'weekly' && (<>
      <label>{T('nt_weekday')}</label>
      <Seg<Wd> value={s.weekday} onChange={(weekday) => set({ ...s, weekday })} ariaLabel={T('nt_weekday')}
        options={WD_KEYS.map((d) => ({ v: d, label: T(`wd_${d}`) }))} />
    </>)}
    {s.freq === 'monthly' && (<>
      <label htmlFor="sch-md">{T('nt_monthday')}</label>
      <input id="sch-md" type="number" min={1} max={28} value={s.monthday}
        onChange={(e) => set({ ...s, monthday: parseInt(e.target.value, 10) || 1 })} />
    </>)}
    {s.freq !== 'hourly' && (<>
      <label htmlFor="sch-time">{T('nt_time')}</label>
      <input id="sch-time" type="time" value={s.time} onChange={(e) => set({ ...s, time: e.target.value })} />
    </>)}
    {showRetention && (<>
      <label htmlFor="sch-ret">{T('nt_ret')}</label>
      <select id="sch-ret" value={retention} onChange={(e) => setRetention(e.target.value)}>
        <option value="1w">{T('nt_ret_1w')}</option>
        <option value="1m">{T('nt_ret_1m')}</option>
        <option value="3m">{T('nt_ret_3m')}</option>
        <option value="1y">{T('nt_ret_1y')}</option>
      </select>
    </>)}
  </>);
}

function NewTaskModal({ onClose }: { onClose: () => void }) {
  const { t, refresh, notify } = useApp();
  const datasets = useLoad(() => getProvider().getDatasets());
  const pools = useLoad(() => getProvider().getPools());
  const [tipo, setTipo] = useState<'snapshot' | 'scrub' | 'trim' | 'smart'>('snapshot');
  const [target, setTarget] = useState('');
  const [sched, setSched] = useState<SchedState>({ freq: 'daily', minute: '15', time: '06:00', weekday: 'sun', monthday: 1 });
  const [retention, setRetention] = useState('1m');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');

  useEffect(() => { if (!target && datasets?.length) setTarget(datasets[0].name); }, [datasets, target]);
  useEffect(() => {
    // Ajusta el objetivo por defecto según el tipo de tarea
    if (tipo === 'smart') setTarget('all');
    else if ((tipo === 'scrub' || tipo === 'trim') && pools?.length) setTarget(pools[0].name);
    else if (tipo === 'snapshot' && datasets?.length) setTarget(datasets[0].name);
  }, [tipo, pools, datasets]);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true); setErr('');
    const jobType = tipo === 'smart' ? 'smart_short' : tipo;
    try {
      await getProvider().createJob({
        tipo: jobType, target, schedule: buildSchedule(sched),
        retention: tipo === 'snapshot' ? retention : undefined,
      });
      refresh(); onClose();
      notify(t('toast_task_created'), 'ok');
    } catch (ex) { const msg = errorMessage(ex, t); setErr(msg); notify(msg, 'err'); setBusy(false); }
  };

  return (
    <ModalBox onClose={onClose} label={t('nt_title')}>
      <form onSubmit={submit}>
        <h3>{t('nt_title')}</h3>
        <p className="desc">{t('nt_desc')}</p>
        <label>{t('nt_type')}</label>
        <Seg value={tipo} onChange={setTipo} ariaLabel={t('nt_type')}
          options={[
            { v: 'snapshot', label: t('tk_type_snapshot') },
            { v: 'scrub', label: t('tk_type_scrub') },
            { v: 'trim', label: t('tk_type_trim') },
            { v: 'smart', label: t('tk_type_smart') },
          ]} />
        {tipo === 'trim' && (
          <p className="desc" style={{ marginTop: 8, fontSize: 12, color: 'var(--text2)' }}>{t('nt_trim_desc')}</p>
        )}
        <label htmlFor="nt-target">{t('nt_target')}</label>
        <select id="nt-target" value={target} onChange={(e) => setTarget(e.target.value)}>
          {tipo === 'smart' && <option value="all">{t('nt_all_disks')}</option>}
          {(tipo === 'scrub' || tipo === 'trim') && (pools ?? []).map((p) => <option key={p.name} value={p.name}>{p.name} {t('nt_pool_full')}</option>)}
          {tipo === 'snapshot' && (<>
            {(datasets ?? []).map((d) => <option key={d.name} value={d.name}>{d.name}</option>)}
            {(pools ?? []).map((p) => <option key={p.name} value={p.name}>{p.name} {t('nt_pool_full')}</option>)}
          </>)}
        </select>
        <ScheduleFields s={sched} set={setSched} showRetention={tipo === 'snapshot'}
          retention={retention} setRetention={setRetention} t={t as never} />
        {err && <p className="form-err" role="alert">{err}</p>}
        <div className="m-actions">
          <button type="button" className="btn" onClick={onClose}>{t('cancel')}</button>
          <SubmitBtn label={t('nt_create')} busy={busy} disabled={!target} />
        </div>
      </form>
    </ModalBox>
  );
}

function EditScheduleModal({ job, onClose }: { job: Job; onClose: () => void }) {
  const { t, refresh, isAdmin, notify } = useApp();
  const [sched, setSched] = useState<SchedState>(() => parseSchedule(job.schedule));
  const [retention, setRetention] = useState(job.retention || '1m');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true); setErr('');
    try {
      await getProvider().updateJob(job.id, {
        schedule: buildSchedule(sched),
        retention: job.tipo === 'snapshot' ? retention : undefined,
      });
      refresh(); onClose();
      notify(t('toast_saved'), 'ok');
    } catch (ex) { const msg = errorMessage(ex, t); setErr(msg); notify(msg, 'err'); setBusy(false); }
  };

  const remove = async () => {
    setBusy(true); setErr('');
    try {
      // El backend exige confirm == target del job (no su id)
      await getProvider().deleteJob(job.id, job.target);
      refresh(); onClose();
      notify(t('toast_task_deleted'), 'ok');
    } catch (ex) { const msg = errorMessage(ex, t); setErr(msg); notify(msg, 'err'); setBusy(false); }
  };

  return (
    <ModalBox onClose={onClose} label={t('et_title')}>
      <form onSubmit={submit}>
        <h3>{t('et_title')}</h3>
        <p className="desc">{t('et_job')}: <b className="mono">{job.tipo} · {job.target}</b></p>
        <ScheduleFields s={sched} set={setSched} showRetention={job.tipo === 'snapshot'}
          retention={retention} setRetention={setRetention} t={t as never} />
        <label className="checklabel" style={{ marginTop: 16 }}>
          <input type="checkbox" defaultChecked /> {t('et_notify')}
        </label>
        {err && <p className="form-err" role="alert">{err}</p>}
        <div className="m-actions">
          {isAdmin && <button type="button" className="btn danger" style={{ marginRight: 'auto' }} onClick={remove} disabled={busy}>{t('et_delete')}</button>}
          <button type="button" className="btn" onClick={onClose}>{t('cancel')}</button>
          <SubmitBtn label={t('save')} busy={busy} />
        </div>
      </form>
    </ModalBox>
  );
}

// ---------- replicación ZFS (send/recv local y SSH) ----------
function NewReplModal({ onClose }: { onClose: () => void }) {
  const { t, refresh, notify } = useApp();
  const datasets = useLoad(() => getProvider().getDatasets());
  const [source, setSource] = useState('');
  const [destType, setDestType] = useState<'local' | 'ssh'>('local');
  const [dest, setDest] = useState('');
  const [host, setHost] = useState('');
  const [user, setUser] = useState('');
  const [port, setPort] = useState('22');
  const [raw, setRaw] = useState(false);
  const [force, setForce] = useState(false);
  const [sched, setSched] = useState<SchedState>({ freq: 'daily', minute: '15', time: '03:00', weekday: 'sun', monthday: 1 });
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');
  const [testState, setTestState] = useState<'idle' | 'busy' | 'ok' | 'fail'>('idle');
  const [testMsg, setTestMsg] = useState('');
  // Clave pública del daemon: solo se pide cuando el destino es SSH
  const sshkey = useLoad(
    () => (destType === 'ssh' ? getProvider().getReplicationSSHKey() : Promise.resolve(null)),
    [destType]);

  useEffect(() => { if (!source && datasets?.length) setSource(datasets[0].name); }, [datasets, source]);
  useEffect(() => { setTestState('idle'); setTestMsg(''); }, [host, user, port]);

  const runTest = async () => {
    setTestState('busy'); setTestMsg('');
    try {
      const r = await getProvider().testReplication(host.trim(), user.trim(), parseInt(port, 10) || 22);
      if (r.ok) { setTestState('ok'); setTestMsg(r.remote_version ?? ''); }
      else { setTestState('fail'); setTestMsg(r.error ?? ''); }
    } catch (ex) { setTestState('fail'); setTestMsg(errorMessage(ex, t)); }
  };

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true); setErr('');
    try {
      await getProvider().createReplicationJob({
        source, dest_type: destType, dest_dataset: dest.trim(),
        host: destType === 'ssh' ? host.trim() : undefined,
        user: destType === 'ssh' ? user.trim() : undefined,
        port: destType === 'ssh' ? (parseInt(port, 10) || 22) : undefined,
        raw, force_full: force, schedule: buildSchedule(sched),
      });
      refresh(); onClose();
      notify(t('toast_repl_created'), 'ok');
    } catch (ex) { const msg = errorMessage(ex, t); setErr(msg); notify(msg, 'err'); setBusy(false); }
  };

  const sshOk = destType !== 'ssh' || (host.trim() && user.trim() && +port >= 1 && +port <= 65535);
  return (
    <ModalBox onClose={onClose} wide label={t('repl_create_title')}>
      <form onSubmit={submit}>
        <h3>{t('repl_create_title')}</h3>
        <p className="desc">{t('repl_create_desc')}</p>
        <label htmlFor="rp-source">{t('repl_source')}</label>
        <select id="rp-source" value={source} onChange={(e) => setSource(e.target.value)}>
          {(datasets ?? []).map((d) => <option key={d.name} value={d.name}>{d.name}</option>)}
        </select>
        <label>{t('repl_dest_type')}</label>
        <Seg<'local' | 'ssh'> value={destType} onChange={setDestType} ariaLabel={t('repl_dest_type')}
          options={[{ v: 'local', label: t('repl_local') }, { v: 'ssh', label: t('repl_ssh') }]} />
        <label htmlFor="rp-dest">{t('repl_dest_dataset')}</label>
        <input id="rp-dest" placeholder={t('repl_dest_dataset_ph')} value={dest}
          onChange={(e) => setDest(e.target.value)} required />
        {destType === 'ssh' && (<>
          <div style={{ display: 'grid', gridTemplateColumns: '2fr 1.5fr 0.7fr', gap: '0 10px' }}>
            <div>
              <label htmlFor="rp-host">{t('repl_host')}</label>
              <input id="rp-host" placeholder={t('repl_host_ph')} value={host} onChange={(e) => setHost(e.target.value)} required />
            </div>
            <div>
              <label htmlFor="rp-user">{t('repl_user')}</label>
              <input id="rp-user" placeholder={t('repl_user_ph')} value={user} onChange={(e) => setUser(e.target.value)} required />
            </div>
            <div>
              <label htmlFor="rp-port">{t('repl_port')}</label>
              <input id="rp-port" type="number" min={1} max={65535} value={port} onChange={(e) => setPort(e.target.value)} required />
            </div>
          </div>
          <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginTop: 4 }}>
            <button type="button" className="btn sm" onClick={runTest}
              disabled={testState === 'busy' || !host.trim() || !user.trim()}>
              {testState === 'busy' ? t('repl_testing') : t('repl_test')}
            </button>
            {testState === 'ok' && <span style={{ fontSize: 12.5, color: 'var(--ok)' }}>✓ {t('repl_test_ok')} <b className="mono">{testMsg}</b></span>}
            {testState === 'fail' && <span className="form-err" style={{ margin: 0, fontSize: 12.5 }}>{testMsg}</span>}
          </div>
          {sshkey && (
            <div className="card" style={{ marginTop: 10, padding: '10px 14px' }}>
              <div style={{ fontSize: 12.5, fontWeight: 650, marginBottom: 4 }}>{t('repl_sshkey_title')}</div>
              <code className="mono" style={{ display: 'block', fontSize: 11.5, wordBreak: 'break-all', color: 'var(--text2)' }}>
                {sshkey.public_key}
              </code>
              <p className="desc" style={{ marginTop: 6, fontSize: 12 }}>{t('repl_sshkey_hint')}</p>
              <p className="desc mono" style={{ marginTop: 2, fontSize: 11.5 }}>{sshkey.instructions}</p>
            </div>
          )}
        </>)}
        <label className="checklabel" style={{ marginTop: 12 }}>
          <input type="checkbox" checked={raw} onChange={(e) => setRaw(e.target.checked)} /> {t('repl_raw')}
        </label>
        <p className="desc" style={{ marginTop: 2, fontSize: 12 }}>{t('repl_raw_hint')}</p>
        <label className="checklabel" style={{ marginTop: 8 }}>
          <input type="checkbox" checked={force} onChange={(e) => setForce(e.target.checked)} /> {t('repl_force')}
        </label>
        <p className="desc" style={{ marginTop: 2, fontSize: 12 }}>{t('repl_force_hint')}</p>
        <ScheduleFields s={sched} set={setSched} showRetention={false}
          retention="" setRetention={() => { }} t={t as never} />
        {err && <p className="form-err" role="alert">{err}</p>}
        <div className="m-actions">
          <button type="button" className="btn" onClick={onClose}>{t('cancel')}</button>
          <SubmitBtn label={t('repl_create')} busy={busy} disabled={!source || !dest.trim() || !sshOk} />
        </div>
      </form>
    </ModalBox>
  );
}

function EditReplModal({ job, onClose }: { job: ReplicationJob; onClose: () => void }) {
  const { t, refresh, isAdmin, notify } = useApp();
  const [sched, setSched] = useState<SchedState>(() => parseSchedule(job.schedule));
  const [raw, setRaw] = useState(job.raw);
  const [force, setForce] = useState(job.force_full);
  const [confirm, setConfirm] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true); setErr('');
    try {
      await getProvider().updateReplicationJob(job.id, {
        schedule: buildSchedule(sched), raw, force_full: force,
      });
      refresh(); onClose();
      notify(t('toast_saved'), 'ok');
    } catch (ex) { const msg = errorMessage(ex, t); setErr(msg); notify(msg, 'err'); setBusy(false); }
  };
  const remove = async () => {
    setBusy(true); setErr('');
    try {
      await getProvider().deleteReplicationJob(job.id, confirm.trim());
      refresh(); onClose();
      notify(t('toast_repl_deleted'), 'ok');
    } catch (ex) { const msg = errorMessage(ex, t); setErr(msg); notify(msg, 'err'); setBusy(false); }
  };

  return (
    <ModalBox onClose={onClose} label={t('repl_edit_title')}>
      <form onSubmit={submit}>
        <h3>{t('repl_edit_title')}</h3>
        <p className="desc mono" style={{ fontSize: 12.5 }}>
          {job.source} → {job.dest_type === 'ssh' ? `${job.user}@${job.host}:${job.dest_dataset}` : job.dest_dataset}
        </p>
        <ScheduleFields s={sched} set={setSched} showRetention={false}
          retention="" setRetention={() => { }} t={t as never} />
        <label className="checklabel" style={{ marginTop: 12 }}>
          <input type="checkbox" checked={raw} onChange={(e) => setRaw(e.target.checked)} /> {t('repl_raw')}
        </label>
        <label className="checklabel" style={{ marginTop: 8 }}>
          <input type="checkbox" checked={force} onChange={(e) => setForce(e.target.checked)} /> {t('repl_force')}
        </label>
        <p className="desc" style={{ marginTop: 2, fontSize: 12 }}>{t('repl_force_hint')}</p>
        {isAdmin && (<>
          <p className="desc" style={{ marginTop: 14 }}>{t('repl_del_desc')}</p>
          <label htmlFor="rpe-confirm">{t('repl_del_confirm_lbl')}</label>
          <input id="rpe-confirm" placeholder={job.source} value={confirm}
            onChange={(e) => setConfirm(e.target.value)} autoComplete="off" />
        </>)}
        {err && <p className="form-err" role="alert">{err}</p>}
        <div className="m-actions">
          {isAdmin && <button type="button" className="btn danger" style={{ marginRight: 'auto' }}
            onClick={remove} disabled={busy || confirm.trim() !== job.source}>{t('repl_delete')}</button>}
          <button type="button" className="btn" onClick={onClose}>{t('cancel')}</button>
          <SubmitBtn label={t('save')} busy={busy} />
        </div>
      </form>
    </ModalBox>
  );
}

// ---------- usuarios ----------
function NewUserModal({ onClose }: { onClose: () => void }) {
  const { t, refresh, notify } = useApp();
  const [user, setUser] = useState('');
  const [pass, setPass] = useState('');
  const [role, setRole] = useState<'admin' | 'user'>('user');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true); setErr('');
    try {
      await getProvider().createUser({ user: user.trim(), password: pass, role });
      refresh(); onClose();
      notify(t('toast_user_created'), 'ok');
    } catch (ex) { const msg = errorMessage(ex, t); setErr(msg); notify(msg, 'err'); setBusy(false); }
  };

  return (
    <ModalBox onClose={onClose} label={t('mu_title')}>
      <form onSubmit={submit}>
        <h3>{t('mu_title')}</h3>
        <p className="desc">{t('mu_d')}</p>
        <label htmlFor="mu-name">{t('mu_name')}</label>
        <input id="mu-name" placeholder={t('mu_name_ph')} value={user} onChange={(e) => setUser(e.target.value)} required />
        <label htmlFor="mu-pass">{t('mu_pass')}</label>
        <input id="mu-pass" type="password" placeholder={t('mu_pass_ph')} value={pass}
          onChange={(e) => setPass(e.target.value)} minLength={8} required />
        <label>{t('mu_role')}</label>
        <Seg value={role} onChange={setRole} ariaLabel={t('mu_role')}
          options={[{ v: 'user', label: t('mu_r_user') }, { v: 'admin', label: t('mu_r_admin') }]} />
        <p className="desc" style={{ marginTop: 12 }}>{t('mu_roles_d')}</p>
        {err && <p className="form-err" role="alert">{err}</p>}
        <div className="m-actions">
          <button type="button" className="btn" onClick={onClose}>{t('cancel')}</button>
          <SubmitBtn label={t('mu_create')} busy={busy} disabled={!user.trim() || pass.length < 8} />
        </div>
      </form>
    </ModalBox>
  );
}

// Cambiar la contraseña de la sesión actual (desde Ajustes > Mi sesión)
function MyPasswdModal({ onClose }: { onClose: () => void }) {
  const { t, notify } = useApp();
  const [cur, setCur] = useState('');
  const [p1, setP1] = useState('');
  const [p2, setP2] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (p1 !== p2) { setErr(t('s_mypass_mismatch')); return; }
    setBusy(true); setErr('');
    try {
      await getProvider().setMyPassword(cur, p1);
      onClose();
      notify(t('toast_pwd_changed'), 'ok');
    } catch (ex) { const msg = errorMessage(ex, t); setErr(msg); notify(msg, 'err'); setBusy(false); }
  };

  return (
    <ModalBox onClose={onClose} label={t('s_mypass')}>
      <form onSubmit={submit}>
        <h3>{t('s_mypass')}</h3>
        <label htmlFor="myp-cur">{t('s_mypass_cur')}</label>
        <input id="myp-cur" type="password" autoComplete="current-password" value={cur}
          onChange={(e) => setCur(e.target.value)} required />
        <label htmlFor="myp-p1">{t('mp_new')}</label>
        <input id="myp-p1" type="password" placeholder={t('mu_pass_ph')} autoComplete="new-password"
          value={p1} onChange={(e) => setP1(e.target.value)} minLength={8} required />
        <label htmlFor="myp-p2">{t('s_mypass2')}</label>
        <input id="myp-p2" type="password" autoComplete="new-password" value={p2}
          onChange={(e) => setP2(e.target.value)} minLength={8} required />
        {err && <p className="form-err" role="alert">{err}</p>}
        <div className="m-actions">
          <button type="button" className="btn" onClick={onClose}>{t('cancel')}</button>
          <SubmitBtn label={t('update')} busy={busy} disabled={!cur || p1.length < 8 || p1 !== p2} />
        </div>
      </form>
    </ModalBox>
  );
}

function PasswdModal({ user, onClose }: { user: string; onClose: () => void }) {
  const { t, notify } = useApp();
  const [p1, setP1] = useState('');
  const [p2, setP2] = useState('');
  const [closeSess, setCloseSess] = useState(true);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (p1 !== p2) { setErr(t('s_mypass_mismatch')); return; }
    setBusy(true); setErr('');
    try {
      await getProvider().setUserPassword(user, p1, closeSess);
      onClose();
      notify(t('toast_pwd_changed'), 'ok');
    } catch (ex) { const msg = errorMessage(ex, t); setErr(msg); notify(msg, 'err'); setBusy(false); }
  };

  return (
    <ModalBox onClose={onClose} label={t('mp_title')}>
      <form onSubmit={submit}>
        <h3>{t('mp_title')}</h3>
        <p className="desc">{t('mp_user')}: <b className="mono">{user}</b></p>
        <label htmlFor="mp-p1">{t('mp_new')}</label>
        <input id="mp-p1" type="password" placeholder={t('mu_pass_ph')} value={p1}
          onChange={(e) => setP1(e.target.value)} minLength={8} required />
        <label htmlFor="mp-p2">{t('mp_new2')}</label>
        <input id="mp-p2" type="password" value={p2} onChange={(e) => setP2(e.target.value)} minLength={8} required />
        <label className="checklabel" style={{ marginTop: 16 }}>
          <input type="checkbox" checked={closeSess} onChange={(e) => setCloseSess(e.target.checked)} />
          {t('mp_close')}
        </label>
        {err && <p className="form-err" role="alert">{err}</p>}
        <div className="m-actions">
          <button type="button" className="btn" onClick={onClose}>{t('cancel')}</button>
          <SubmitBtn label={t('update')} busy={busy} disabled={p1.length < 8 || p1 !== p2} />
        </div>
      </form>
    </ModalBox>
  );
}

function DeleteUserModal({ user, onClose }: { user: string; onClose: () => void }) {
  const { t, refresh, notify } = useApp();
  const [confirm, setConfirm] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true); setErr('');
    try {
      await getProvider().deleteUser(user, confirm.trim());
      refresh(); onClose();
      notify(t('toast_user_deleted'), 'ok');
    } catch (ex) { const msg = errorMessage(ex, t); setErr(msg); notify(msg, 'err'); setBusy(false); }
  };

  return (
    <ModalBox onClose={onClose} label={t('du_title')}>
      <form onSubmit={submit}>
        <h3>{t('du_title')}</h3>
        <p className="desc">{t('du_desc')}</p>
        <p className="desc mono" style={{ marginTop: 8 }}>{user}</p>
        <label htmlFor="du-confirm">{t('du_confirm_lbl')}</label>
        <input id="du-confirm" placeholder={user} value={confirm} onChange={(e) => setConfirm(e.target.value)} />
        {err && <p className="form-err" role="alert">{err}</p>}
        <div className="m-actions">
          <button type="button" className="btn" onClick={onClose}>{t('cancel')}</button>
          <SubmitBtn label={t('delete')} busy={busy} danger disabled={confirm.trim() !== user} />
        </div>
      </form>
    </ModalBox>
  );
}

// ---------- Rename dataset ----------
function RenameDatasetModal({ name, onClose }: { name: string; onClose: () => void }) {
  const { t, refresh, notify } = useApp();
  const [newName, setNewName] = useState(name);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (newName === name || !newName.trim()) return;
    setBusy(true); setErr('');
    try {
      await getProvider().renameDataset(name, newName.trim());
      refresh(); onClose();
      notify(t('toast_ds_updated'), 'ok');
    } catch (ex) { const msg = errorMessage(ex, t); setErr(msg); notify(msg, 'err'); setBusy(false); }
  };

  return (
    <ModalBox onClose={onClose} label={t('ds_rename')}>
      <form onSubmit={submit}>
        <h3>{t('ds_rename')}</h3>
        <p className="desc mono" style={{ marginBottom: 10 }}>{name}</p>
        <input placeholder={t('ds_rename_ph')} value={newName}
          onChange={(e) => setNewName(e.target.value)} autoFocus />
        {err && <p className="form-err" role="alert">{err}</p>}
        <div className="m-actions">
          <button type="button" className="btn" onClick={onClose}>{t('cancel')}</button>
          <SubmitBtn label={t('save')} busy={busy} />
        </div>
      </form>
    </ModalBox>
  );
}
