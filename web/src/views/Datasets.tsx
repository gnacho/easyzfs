// Vista Datasets: tabla de datasets/zvols con acciones por fila.
import { useEffect, useState } from 'react';
import { useData } from '../ui/useData';
import { useApp, errorMessage } from '../ui/store';
import { fmtBytes } from '../ui/format';
import { Badge, Spinner } from '../components/ui';
import { IconLock, IconUnlock } from '../components/icons';
import { useModal } from '../components/Modal';
import { subscribeEvents } from '../data/events';
import { getProvider } from '../data';

export default function Datasets() {
  const { t, isAdmin, caps } = useApp();
  const { openModal } = useModal();
  const { data, loading } = useData((p) => p.getDatasets());
  const ops = useData((p) => p.getLongOps());

  // Operaciones largas en vivo (rewrite…): refresca el indicador por fila
  useEffect(() => subscribeEvents((ev) => {
    if (ev.type === 'longop.update') ops.reload();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }), []);

  const running = new Set((ops.data ?? []).filter((o) => o.status === 'running').map((o) => o.target));

  const [err, setErr] = useState('');
  const dsAct = async (fn: () => Promise<void>) => {
    setErr('');
    try { await fn(); } catch (e) { setErr(errorMessage(e, t)); }
  };

  return (
    <div className="view">
      {loading && !data && <Spinner label={t('loading')} />}
      <div className="card tblwrap">
        <table className="data">
          <thead>
            <tr>
              <th className="slack">{t('ds_name')}</th><th>{t('ds_type')}</th><th>{t('ds_comp')}</th>
              <th className="num">{t('ds_used')}</th><th className="num">{t('ds_avail')}</th><th className="num">{t('ds_quota')}</th><th />
            </tr>
          </thead>
          <tbody>
            {(data ?? []).map((d) => {
              const rewriting = running.has(d.name);
              const canRewrite = isAdmin && !!caps?.rewrite && d.type === 'fs' &&
                !!d.mountpoint && d.mountpoint !== '—' && d.mountpoint !== '-' && d.mountpoint !== 'none' && d.mountpoint !== 'legacy';
              const encrypted = !!d.encryption && d.encryption !== 'off' && d.encryption !== '-';
              const unlocked = d.keystatus === 'available';
              return (
              <tr className="clickable" key={d.name}
                onClick={() => openModal('propsds', { ds: d })}>
                <td className="mono" style={{ fontWeight: 600 }}>
                  {encrypted && (
                    <span style={{ display: 'inline-flex', verticalAlign: '-3px', marginRight: 6,
                      color: unlocked ? 'var(--ok)' : 'var(--err)' }}
                      title={unlocked ? t('ds_unlocked') : t('ds_locked')}
                      aria-label={unlocked ? t('ds_unlocked') : t('ds_locked')}>
                      {unlocked ? <IconUnlock size={15} /> : <IconLock size={15} />}
                    </span>
                  )}
                  {d.name}
                  {encrypted && (
                    <Badge tone={unlocked ? 'ok' : 'warn'} style={{ marginLeft: 8 }}>{t('ds_encrypted')}</Badge>
                  )}
                  {rewriting && (
                    <Badge tone="info" style={{ marginLeft: 8 }}>{t('ds_rewrite_running')}</Badge>
                  )}
                </td>
                <td style={{ color: 'var(--text2)' }}>{d.type === 'volume' ? t('ds_vol') : t('ds_fs')}</td>
                <td>{d.compression}</td>
                <td className="num">{fmtBytes(d.used_bytes)}</td>
                <td className="num">{fmtBytes(d.avail_bytes)}</td>
                <td className="num">{d.quota_bytes ? fmtBytes(d.quota_bytes) : '—'}</td>
                <td style={{ whiteSpace: 'nowrap' }}>
                  {encrypted && isAdmin && (<>
                    {!unlocked && (
                      <button className="btn sm" title={t('ds_unlock_hint')}
                        onClick={(e) => { e.stopPropagation(); openModal('unlockds', { ds: d }); }}>
                        {t('ds_unlock')}
                      </button>
                    )}{' '}
                    {unlocked && (
                      <button className="btn sm" title={t('ds_lock_hint')}
                        onClick={(e) => { e.stopPropagation(); openModal('lockds', { ds: d }); }}>
                        {t('ds_lock')}
                      </button>
                    )}{' '}
                    <button className="btn sm" title={t('ds_changekey_hint')}
                      onClick={(e) => { e.stopPropagation(); openModal('changekey', { ds: d }); }}>
                      {t('ds_changekey')}
                    </button>{' '}
                  </>)}
                  <button className="btn sm" onClick={(e) => { e.stopPropagation(); openModal('newsnap', { dataset: d.name }); }}>
                    {t('ds_snapshot')}
                  </button>{' '}
                  {canRewrite && !rewriting && (
                    <button className="btn sm" title={t('ds_rewrite_hint')}
                      onClick={(e) => { e.stopPropagation(); openModal('rewrite', { ds: d }); }}>
                      {t('ds_rewrite')}
                    </button>
                  )}{' '}
                  {isAdmin && (
                    <button className="btn sm danger" onClick={(e) => { e.stopPropagation(); openModal('delds', { name: d.name }); }}>
                      {t('delete')}
                    </button>
                  )}
                  {isAdmin && (
                    <button className="btn sm" title={t('ds_mount')}
                      onClick={(e) => { e.stopPropagation(); dsAct(() => getProvider().mountDataset(d.name)); }}>
                      {t('ds_mount')}
                    </button>
                  )}
                  {isAdmin && (
                    <button className="btn sm" title={t('ds_unmount')}
                      onClick={(e) => { e.stopPropagation(); dsAct(() => getProvider().unmountDataset(d.name)); }}>
                      {t('ds_unmount')}
                    </button>
                  )}
                  {isAdmin && (
                    <button className="btn sm" title={t('ds_rename')}
                      onClick={(e) => { e.stopPropagation(); openModal('renameds', { name: d.name }); }}>
                      {t('ds_rename')}
                    </button>
                  )}
                  {isAdmin && (
                    <button className="btn sm" title={t('ds_promote_hint')}
                      onClick={(e) => { e.stopPropagation(); dsAct(() => getProvider().promoteDataset(d.name)); }}>
                      {t('ds_promote')}
                    </button>
                  )}
                </td>
              </tr>
              );
            })}
          </tbody>
        </table>
        {data && data.length === 0 && <div className="empty">{t('empty')}</div>}
      </div>
      <div className="sect">
        <button className="btn primary" onClick={() => openModal('newds', { vol: false })}>{t('ds_new')}</button>
        <button className="btn" style={{ marginLeft: 8 }} onClick={() => openModal('newds', { vol: true })}>{t('ds_newvol')}</button>
      </div>
    </div>
  );
}
