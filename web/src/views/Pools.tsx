// Vista Pools: filtro por salud, tarjetas y acciones de creación/importación.
import { useEffect, useState } from 'react';
import { subscribeEvents } from '../data/events';
import { useData } from '../ui/useData';
import { useApp } from '../ui/store';
import { Spinner } from '../components/ui';
import { PoolCard } from '../components/PoolCard';
import { useModal } from '../components/Modal';

type Filter = 'all' | 'ok' | 'warn';

export default function Pools() {
  const { t, isAdmin } = useApp();
  const { openModal } = useModal();
  const { data, loading, reload, setData } = useData((p) => p.getPools());
  const [filter, setFilter] = useState<Filter>('all');

  // Tiempo real: progreso de scrub y temperaturas de vdevs sin recargar todo
  useEffect(() => subscribeEvents((ev) => {
    if (ev.type === 'scrub.progress') {
      setData((cur) => cur?.map((p) => p.name === ev.pool
        ? { ...p, scrub: { ...p.scrub, state: ev.pct >= 100 ? 'done' : 'running', kind: ev.kind ?? p.scrub.kind, pct: ev.pct, eta_sec: ev.eta_sec } }
        : p) ?? cur);
      if (ev.pct >= 100) reload();
    }
    if (ev.type === 'disk.temp') {
      setData((cur) => cur?.map((p) => ({
        ...p, vdevs: p.vdevs.map((v) => v.dev === ev.dev ? { ...v, temp_c: ev.temp_c } : v),
      })) ?? cur);
    }
    if (ev.type === 'pool.status') reload();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }), []);

  const filtered = (data ?? []).filter((p) =>
    filter === 'all' ? true : filter === 'ok' ? p.status === 'ONLINE' : p.status !== 'ONLINE');

  return (
    <div className="view">
      <div className="chips">
        <button className={`chip ${filter === 'all' ? 'on' : ''}`} onClick={() => setFilter('all')}>{t('pools_all')}</button>
        <button className={`chip ${filter === 'ok' ? 'on' : ''}`} onClick={() => setFilter('ok')}>{t('pools_ok')}</button>
        <button className={`chip ${filter === 'warn' ? 'on' : ''}`} onClick={() => setFilter('warn')}>{t('pools_warn')}</button>
      </div>
      {loading && !data && <Spinner label={t('loading')} />}
      <div className="grid pools" style={{ gridTemplateColumns: '1fr' }}>
        {filtered.map((p) => <PoolCard key={p.name} pool={p} onChanged={reload} />)}
      </div>
      {data && filtered.length === 0 && <div className="empty">{t('empty')}</div>}
      <div className="sect">
        <button className="btn primary" disabled={!isAdmin} title={!isAdmin ? t('no_permission') : undefined}
          onClick={() => openModal('newpool')}>{t('pool_create')}</button>
        <button className="btn" style={{ marginLeft: 8 }} disabled={!isAdmin}
          title={!isAdmin ? t('no_permission') : undefined} onClick={() => openModal('importpool')}>{t('pool_import')}</button>
      </div>
    </div>
  );
}
