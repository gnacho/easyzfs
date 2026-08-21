// Vista Snapshots: árbol por dataset con acciones de restaurar/eliminar/clonar/diff.
import { useEffect, useMemo, useState } from 'react';
import { useData } from '../ui/useData';
import { useApp, errorMessage } from '../ui/store';
import { fmtBytes, timeAgo } from '../ui/format';
import { Badge, Spinner } from '../components/ui';
import { IconChev } from '../components/icons';
import { useModal } from '../components/Modal';
import { subscribeEvents } from '../data/events';
import { getProvider } from '../data';

export default function Snapshots() {
  const { t, isAdmin, notify } = useApp();
  const { openModal } = useModal();
  const pools = useData((p) => p.getPools());
  const [poolFilter, setPoolFilter] = useState<string>('');
  const { data, loading, reload } = useData((p) => p.getSnapshots());
  const [open, setOpen] = useState<Set<string>>(new Set());
  const [err, setErr] = useState('');

  useEffect(() => { if (!poolFilter && pools.data?.length) setPoolFilter(pools.data[0].name); }, [pools.data, poolFilter]);

  useEffect(() => subscribeEvents((ev) => {
    if (ev.type === 'job.finished' || ev.type === 'overview') reload();
  }), []);

  const groups = useMemo(
    () => (data ?? []).filter((g) => !poolFilter || g.dataset === poolFilter || g.dataset.startsWith(poolFilter + '/')),
    [data, poolFilter]);

  const toggle = (ds: string) => setOpen((s) => {
    const n = new Set(s);
    if (n.has(ds)) n.delete(ds); else n.add(ds);
    return n;
  });

  const cloneSnap = async (full: string) => { setErr('');
    try {
      const target = prompt(t('clone_target_ph'));
      if (!target) return;
      const mount = prompt(t('clone_mount_ph'));
      await getProvider().cloneSnapshot(full, target, mount || undefined);
      reload();
      notify(t('toast_clone_created'), 'ok');
    } catch (e) { const m = errorMessage(e, t); setErr(m); notify(m, 'err'); }
  };

  return (
    <div className="view">
      <div className="chips">
        {(pools.data ?? []).map((p) => (
          <button key={p.name} className={`chip ${poolFilter === p.name ? 'on' : ''}`}
            onClick={() => setPoolFilter(p.name)}>{p.name}</button>
        ))}
        <button className="chip" style={{ marginLeft: 'auto' }} onClick={() => openModal('newsnap', {})}>
          {t('sn_now')}
        </button>
      </div>
      {loading && !data && <Spinner label={t('loading')} />}
      {err && <p className="form-err" role="alert">{err}</p>}
      <div className="grid snaps">
        {groups.map((g) => (
          <div className={`card snapgroup ${open.has(g.dataset) ? 'open' : ''}`} key={g.dataset}>
            <div className="snapg-head" role="button" tabIndex={0} aria-expanded={open.has(g.dataset)}
              onClick={() => toggle(g.dataset)}
              onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); toggle(g.dataset); } }}>
              <span className="chev" style={{ display: 'inline-flex' }}><IconChev /></span>
              <span className="mono" style={{ fontWeight: 650, fontSize: 13.5 }}>{g.dataset}</span>
              <span className="badge info" style={{ marginLeft: 'auto' }}>{g.snaps.length}</span>
            </div>
            <div className="snaplist">
              {g.snaps.map((s) => (
                <div className="snap" key={s.full}>
                  <Badge tone={s.kind === 'manual' ? 'warn' : 'info'} dot={false} style={{ padding: '2px 8px' }}>{s.kind}</Badge>
                  <span className="mono">{s.name}</span>
                  <span style={{ color: 'var(--text2)', fontSize: 12 }}>
                    {fmtBytes(s.used_bytes)} · {timeAgo(s.ts, t)}
                  </span>
                  <span className="actions" style={{ marginLeft: 'auto' }}>
                    <button className="btn sm" disabled={!isAdmin} title={!isAdmin ? t('no_permission') : undefined}
                      onClick={() => openModal('rollback', { full: s.full })}>{t('snap_restore')}</button>
                    <button className="btn sm danger" disabled={!isAdmin} title={!isAdmin ? t('no_permission') : undefined}
                      onClick={() => openModal('delsnap', { full: s.full })}>{t('snap_delete')}</button>
                    <button className="btn sm" disabled={!isAdmin}
                      onClick={() => cloneSnap(s.full)}>{t('snap_clone')}</button>
                  </span>
                </div>
              ))}
            </div>
          </div>
        ))}
        {data && groups.length === 0 && <div className="card"><div className="empty">{t('empty')}</div></div>}
      </div>
    </div>
  );
}
