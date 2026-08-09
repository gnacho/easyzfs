// Tarjeta de pool compartida entre Panel y Pools (réplica del mockup)
import { getProvider } from '../data';
import type { Disk, Pool, Topo } from '../data/types';
import { errorMessage, useApp } from '../ui/store';
import { t } from '../ui/i18n';
import { fmtBytes, fmtBytesPair, fmtPct, fmtRatio, timeAgo } from '../ui/format';
import { statusLabel } from '../ui/labels';
import { Badge, InfoBubble, Meter, Switch } from './ui';
import { Sparkline } from './Sparkline';
import { useModal } from './Modal';
import { useEffect, useState } from 'react';

// topoTipKey — clave i18n que explica la topología (ayuda contextual U4).
const topoTipKey = (topo: Topo) => {
  switch (topo) {
    case 'mirror': return 'topo_mirror';
    case 'raidz1': return 'topo_raidz1';
    case 'raidz2': return 'topo_raidz2';
    default: return 'topo_stripe';
  }
};

// topoBase — topología base de la cadena descriptiva de pool.topo
// ("raidz2 (4×1,86 TB NVMe)" → "raidz2").
const topoBase = (s: string): Topo | null => {
  const m = s.match(/^(mirror|raidz1|raidz2|stripe)/);
  return m ? (m[1] as Topo) : null;
};

// TopoHelp — burbuja que explica la topología del pool según la base de la
// cadena; si no se reconoce, no muestra nada.
function TopoHelp({ topo }: { topo: string }) {
  const base = topoBase(topo);
  if (!base) return null;
  return <InfoBubble title={topo}>{t(topoTipKey(base))}</InfoBubble>;
}

// shortDev — nombre corto de vdev: ruta base si la hay; UUID acortado si no.
const shortDev = (v: { dev: string; path?: string }) =>
  v.path ? v.path.replace('/dev/', '') : (v.dev.length > 13 ? v.dev.slice(0, 8) + '…' : v.dev);

// fmtEta — "~45 min" / "~13 h 23 m" según magnitud.
const fmtEta = (sec: number): string => {
  const min = Math.max(1, Math.round(sec / 60));
  if (min < 90) return `~${min} min`;
  return `~${Math.floor(min / 60)} h ${min % 60} m`;
};

export function PoolCard({ pool, onChanged }: { pool: Pool; onChanged: () => void }) {
  const { t, isAdmin, caps } = useApp();
  const { openModal } = useModal();
  const [err, setErr] = useState('');
  const [disks, setDisks] = useState<Disk[]>([]);
  const [trimOverride, setTrimOverride] = useState<boolean | null>(null);
  const [hist, setHist] = useState<{ days: number; points: { ts: number; value: number }[] }[]>([]);
  const pct = pool.total_bytes > 0 ? (pool.used_bytes / pool.total_bytes) * 100 : 0;
  const cap = fmtBytesPair(pool.used_bytes, pool.total_bytes);
  const running = pool.scrub.state === 'running';
  const resilvering = running && pool.scrub.kind === 'resilver';
  const expanding = running && pool.scrub.kind === 'expand';
  const ok = pool.status === 'ONLINE';

  useEffect(() => {
    let alive = true;
    getProvider().getDisks().then((d) => { if (alive) setDisks(d); }).catch(() => {});
    const src = `pool.${pool.name}.used_pct`;
    getProvider().getSeries(src, 7, 120)
      .then((r) => { if (alive) setHist((cur) => [...cur.filter((h) => h.days !== 7), { days: 7, points: r.points }]); })
      .catch(() => {});
    getProvider().getSeries(src, 30, 160)
      .then((r) => { if (alive) setHist((cur) => [...cur.filter((h) => h.days !== 30), { days: 30, points: r.points }]); })
      .catch(() => {});
    return () => { alive = false; };
  }, [pool]);

  // Cuando el dato real alcanza el valor pedido, suelta el override optimista.
  useEffect(() => {
    setTrimOverride((cur) => (cur !== null && cur === pool.autotrim ? null : cur));
  }, [pool.autotrim]);

  const scrub = async (action: 'start' | 'pause' | 'stop') => {
    setErr('');
    try {
      await getProvider().scrubAction(pool.name, action);
      onChanged();
    } catch (e) { setErr(errorMessage(e, t)); }
  };

  const vdevAct = async (dev: string, action: 'offline' | 'online') => {
    setErr('');
    try {
      await getProvider().vdevAction(pool.name, dev, action);
      onChanged();
    } catch (e) { setErr(errorMessage(e, t)); }
  };

  const toggleAutotrim = async () => {
    setErr('');
    // Estado optimista: el Switch es controlado y la caché del backend puede
    // tardar unos segundos en reflejar el cambio; sin override local el
    // interruptor "vuelve solo" y parece que no hace nada. Se limpia cuando
    // la prop pool.autotrim alcanza el valor pedido.
    const next = !(trimOverride ?? pool.autotrim);
    setTrimOverride(next);
    try {
      await getProvider().setAutotrim(pool.name, next);
      onChanged();
    } catch (e) { setTrimOverride(null); setErr(errorMessage(e, t)); }
  };

  const faulted = pool.vdevs.find((v) => v.status !== 'ONLINE' && !v.replacing);
  const free = disks.filter((d) => (d.pool === '—' || d.pool === '') && !d.in_use);
  const isMirror = pool.topo.startsWith('mirror');
  // RAID-Z expansion: solo con capability, vdev raidz detectado y discos libres.
  const canExpand = isAdmin && !!caps?.raidz_expansion &&
    (pool.raidz_vdevs ?? []).length > 0 && free.length > 0 && !expanding;

  return (
    <div className="card">
      <div className="poolhead">
        <div className="grow">
          <div className="t1" style={{ fontSize: 16, fontWeight: 700, display: 'flex', gap: 9, alignItems: 'center' }}>
            {pool.name} <Badge tone={ok ? 'ok' : 'warn'}>{statusLabel(pool.status, t)}</Badge>
            {pool.checkpoint && <Badge tone="info">{t('ck_badge')}</Badge>}
          </div>
          <div className="t2">{pool.topo}</div>
          <TopoHelp topo={pool.topo} />
          <Meter pct={pct} />
          {hist.length > 0 && (
            <div className="sparks" aria-label={t('pool_history_series')}>
              {hist.sort((a, b) => a.days - b.days).map((h) => (
                <span key={h.days} className="spark" title={t('pool_history_days', { days: h.days })}>
                  <Sparkline points={h.points} width={72} height={26}
                    ariaLabel={t('pool_history_days', { days: h.days })} />
                  <em>{h.days}d</em>
                </span>
              ))}
            </div>
          )}
        </div>
        <div style={{ textAlign: 'right' }}>
          <div style={{ fontWeight: 700, fontSize: 17 }}>
            {cap.used}
            <span className="muted" style={{ fontWeight: 500 }}>
              {' '}{t('pool_of')} {cap.total}
            </span>
          </div>
          <div style={{ fontSize: 12, color: 'var(--text2)' }}>{fmtPct(pct)} {t('pool_used')}</div>
        </div>
      </div>

      <div className="poolmeta">
        <span>{t('pool_comp')} <b>{fmtRatio(pool.comp_ratio)}</b></span>
        <span>{t('pool_frag')} <b>{fmtPct(pool.frag_pct)}</b></span>
        <span>
          {t('pool_last_scrub')}{' '}
          <b>{running ? t('pool_in_progress') : timeAgo(pool.scrub.ts, t)}</b>
          {' '}· <b>{pool.scrub.errors} {t('pool_errors')}</b>
        </span>
        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 7 }}>
          <Switch checked={trimOverride ?? pool.autotrim} onChange={() => { void toggleAutotrim(); }}
            disabled={!isAdmin} ariaLabel={t('pool_autotrim')} />
          <b>{t('pool_autotrim')}</b>
          <InfoBubble title={t('pool_autotrim')}>{t('pool_autotrim_hint')}</InfoBubble>
        </span>
      </div>

      {running && (<>
        <div style={{ padding: '0 16px 4px', fontSize: 12.5, color: 'var(--info)', fontWeight: 600 }}>
          {expanding ? t('pool_expand_running') : resilvering ? t('pool_resilvering') : pool.scrub.kind === 'trim' ? t('pool_trim_running') : t('pool_scrub_running')}
          {' '}· {Math.round(pool.scrub.pct)}%
          {pool.scrub.eta_sec > 0 && <> · {fmtEta(pool.scrub.eta_sec)}</>}
          {(pool.scrub.bytes_done ?? 0) > 0 && (pool.scrub.bytes_total ?? 0) > 0 && (
            <> · {t('pool_scan_bytes', { done: fmtBytes(pool.scrub.bytes_done ?? 0), total: fmtBytes(pool.scrub.bytes_total ?? 0) })}</>
          )}
        </div>
        <div className="scrubbar" role="progressbar" aria-valuenow={Math.round(pool.scrub.pct)} aria-valuemin={0} aria-valuemax={100}>
          <i style={{ width: `${pool.scrub.pct}%` }} />
        </div>
      </>)}

      {isAdmin && !running && faulted && free.length > 0 && (
        <div className="rebuildbar">
          <span style={{ flex: 1, minWidth: 220 }}>
            {t('pool_rebuild', {
              dev: free[0].dev,
              size: fmtBytes(free[0].size_bytes),
              old: shortDev(faulted),
            })}
          </span>
          <button className="btn sm warn" title={t('pool_rebuild_hint')}
            onClick={() => openModal('replace', { pool: pool.name, oldDev: faulted.dev, newDev: free[0].dev })}>
            {t('pool_rebuild_btn')}
          </button>
        </div>
      )}

      <div className="vdevs">
        {[...pool.vdevs]
          .sort((a, b) => Number(a.replacing && a.status !== 'ONLINE') - Number(b.replacing && b.status !== 'ONLINE'))
          .map((v) => {
          // Hijo saliente de un replacing-N: no es un error, es el disco viejo
          // que desaparece solo al terminar la reconstrucción.
          if (v.replacing && v.status !== 'ONLINE') {
            return (
              <div className="vdev" key={v.dev} style={{ opacity: 0.55 }}>
                <span className="badge info" style={{ padding: '2px 7px' }}>{t('vdev_outgoing')}</span>
                <span className="dname" title={v.dev}>{shortDev(v)}</span>
                <span style={{ fontSize: 12, color: 'var(--text2)' }}>{t('vdev_outgoing_hint')}</span>
              </div>
            );
          }
          return (
          <div className="vdev" key={v.dev} style={v.replacing ? { flexWrap: 'wrap' } : undefined}>
            <span className={`badge ${v.status === 'ONLINE' ? 'ok' : 'err'}`} style={{ padding: '2px 7px' }}>{statusLabel(v.status, t)}</span>
            <span className="dname" title={v.dev}>{shortDev(v)}</span>
            {v.replacing && (
              <span className="badge info" style={{ padding: '1px 7px' }} title={t('vdev_new_hint')}>{t('vdev_new')}</span>
            )}
            <span>{v.role !== '—' ? v.role : ''}</span>
            <span style={{ marginLeft: 'auto' }}>{v.temp_c}°C</span>
            {v.replacing ? (
              <span className="vdevjoin" title={t('vdev_joining_hint')}>
                {t('vdev_joining')} · {Math.round(pool.scrub.pct)}%
              </span>
            ) : (<>
            <button className="btn sm" disabled={!isAdmin}
              title={!isAdmin ? t('no_permission') : t('pool_replace_disk', { dev: shortDev(v) })}
              onClick={() => openModal('replace', { pool: pool.name, oldDev: v.dev })}>{t('pool_replace')}</button>
            {v.status === 'ONLINE' && (
              <button className="btn sm" disabled={!isAdmin} title={!isAdmin ? t('no_permission') : t('vdev_offline_hint')}
                onClick={() => vdevAct(v.dev, 'offline')}>{t('vdev_offline')}</button>
            )}
            {v.status === 'OFFLINE' && (
              <button className="btn sm" disabled={!isAdmin} title={!isAdmin ? t('no_permission') : undefined}
                onClick={() => vdevAct(v.dev, 'online')}>{t('vdev_online')}</button>
            )}
            {isMirror && (
              <button className="btn sm danger" disabled={!isAdmin} title={!isAdmin ? t('no_permission') : t('vdev_detach_hint')}
                onClick={() => openModal('detach', { pool: pool.name, dev: v.dev, path: v.path })}>{t('vdev_detach')}</button>
            )}
            </>)}
            {v.replacing && (
              <div className={`vdevbar ${pool.scrub.pct <= 30 ? 'low' : pool.scrub.pct <= 90 ? 'mid' : 'high'}`}>
                <i style={{ width: `${Math.max(1, pool.scrub.pct)}%` }} />
              </div>
            )}
          </div>
          );
        })}
      </div>

      {err && <p className="form-err" style={{ padding: '0 16px' }} role="alert">{err}</p>}

      <div style={{ display: 'flex', gap: 7, padding: '0 16px 15px', flexWrap: 'wrap' }}>
        {!resilvering && !expanding && (
          <button className="btn sm" title={running ? t('pool_scrub_pause_hint') : t('pool_scrub_hint')}
            onClick={() => scrub(running ? 'pause' : 'start')}>
            {running ? t('pool_scrub_pause') : t('pool_scrub_now')}
          </button>
        )}
        {running && !resilvering && !expanding && <button className="btn sm" onClick={() => scrub('stop')}>{t('pool_scrub_stop')}</button>}
        <button className="btn sm" title={t('pool_history_hint')}
          onClick={() => openModal('history', { pool: pool.name })}>{t('pool_history')}</button>
        <button className="btn sm" disabled={!isAdmin} title={!isAdmin ? t('no_permission') : t('ck_title')}
          onClick={() => openModal('checkpoint', { pool: pool.name, active: pool.checkpoint })}>{t('pool_checkpoint')}</button>
        <button className="btn sm" disabled={!isAdmin} title={!isAdmin ? t('no_permission') : t('pool_add_vdev_hint')}
          onClick={() => openModal('addvdev', { pool: pool.name })}>{t('pool_add_vdev')}</button>
        {canExpand && (
          <button className="btn sm" title={t('xpd_hint')}
            onClick={() => openModal('expand', { pool })}>{t('xpd_btn')}</button>
        )}
        <button className="btn sm" disabled={!isAdmin} title={!isAdmin ? t('no_permission') : t('pool_export_hint')}
          onClick={() => openModal('export', { pool: pool.name })}>
          {t('pool_export')}
        </button>
        <button className="btn sm" disabled={!isAdmin} title={t('pool_clear_hint')}
          onClick={() => getProvider().clearPool(pool.name).then(onChanged).catch((e: Error) => setErr(e.message))}>
          {t('pool_clear')}
        </button>
      </div>
    </div>
  );
}
