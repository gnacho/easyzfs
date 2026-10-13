// Vista Panel: KPIs, tarjetas de pool, alertas recientes y actividad.
import { useEffect } from 'react';
import type { ReactNode } from 'react';
import { subscribeEvents } from '../data/events';
import { useData } from '../ui/useData';
import { useApp, alertTargetView } from '../ui/store';
import { useModal } from '../components/Modal';
import { fmtBytes, fmtBytesPair, fmtDateTime, fmtInt, fmtPct, timeAgo } from '../ui/format';
import { Meter, Spinner } from '../components/ui';
import { PoolCard } from '../components/PoolCard';
import { Donut } from '../components/Donut';
import { RecLine } from '../components/RecLine';
import { IconCheck, IconChev, IconError, IconInfo, IconWarn } from '../components/icons';
import type { Alert } from '../data/types';

function AlertRow({ a }: { a: Alert }) {
  const { t, navigate } = useApp();
  const tone = a.level === 'crit' ? 'err' : a.level === 'warn' ? 'warn' : 'info';
  const Ico = a.level === 'crit' ? IconError : a.level === 'warn' ? IconWarn : IconInfo;
  const view = alertTargetView(a.target);
  const go = view ? () => navigate(view) : undefined;
  return (
    <div className={`alert${view ? ' clickable' : ''}`}
      role={view ? 'link' : undefined} tabIndex={view ? 0 : undefined}
      title={view ? t('al_goto') : undefined}
      onClick={go}
      onKeyDown={go ? (e) => { if (e.key === 'Enter') go(); } : undefined}>
      <div className={`ico ${tone}`}><Ico size={17} /></div>
      <div className="grow" style={{ flex: 1, minWidth: 0 }}>
        <b>{a.message}</b>
        <div className="muted" style={{ marginTop: 2 }}>{a.source}</div>
      </div>
      <span style={{ fontSize: 11.5, color: 'var(--text2)', whiteSpace: 'nowrap' }}>{timeAgo(a.ts, t)}</span>
      {view && <span className="chev"><IconChev /></span>}
    </div>
  );
}

function DashCard({ title, extra, className, flush, children }: {
  title: string; extra?: ReactNode; className?: string; flush?: boolean; children: ReactNode;
}) {
  return (
    <section className={`card dash-card${className ? ' ' + className : ''}`}>
      <header className="dash-card-head">
        <h2>{title}</h2>
        {extra && <div className="dch-extra">{extra}</div>}
      </header>
      <div className={`dash-card-body${flush ? ' flush' : ''}`}>{children}</div>
    </section>
  );
}

export default function Dashboard() {
  const { t, navigate, isAdmin } = useApp();
  const { openModal } = useModal();
  const ov = useData((p) => p.getOverview());
  const pools = useData((p) => p.getPools());
  const perf = useData((p) => p.getPerformance());
  const disks = useData((p) => p.getDisks());
  const recs = useData((p) => p.getRecommendations());
  const missing = useData((p) => p.getMissingPools());

  useEffect(() => subscribeEvents((ev) => {
    if (ev.type === 'overview' || ev.type === 'alert.new' || ev.type === 'pool.status') {
      ov.reload();
      missing.reload();
    }
    if (ev.type === 'scrub.progress') {
      pools.setData((cur) => cur?.map((p) => p.name === ev.pool
        ? { ...p, scrub: { ...p.scrub, state: 'running', kind: ev.kind ?? p.scrub.kind, pct: ev.pct, eta_sec: ev.eta_sec } }
        : p) ?? cur);
      if (ev.pct >= 100) { pools.reload(); ov.reload(); }
    }
    if (ev.type === 'disk.temp') {
      pools.setData((cur) => cur?.map((p) => ({
        ...p, vdevs: p.vdevs.map((v) => v.dev === ev.dev ? { ...v, temp_c: ev.temp_c } : v),
      })) ?? cur);
    }
    if (ev.type === 'disk.smart') { disks.reload(); recs.reload(); }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }), []);

  const o = ov.data;
  const pct = o && o.cap_total_bytes > 0 ? (o.cap_used_bytes / o.cap_total_bytes) * 100 : 0;
  const cap = o ? fmtBytesPair(o.cap_used_bytes, o.cap_total_bytes) : null;
  // Pools: 3 columnas si el número es múltiplo de 3; si no, 2 y el sobrante a fila completa.
  const poolList = pools.data ?? [];
  const poolCols = poolList.length > 0 && poolList.length % 3 === 0 ? 3 : 2;
  const poolSpan = poolCols === 3 ? 'dash-span4' : 'dash-span6';
  const poolLastFull = poolList.length % poolCols === 1;

  return (
    <div className="view">
      {!o && <Spinner label={t('loading')} />}
      {o && (<>
        <div className="card dash-kpis">
          <div className="dash-kpi">
            <div className="klbl"><span className={`led ${o.pools_online === o.pools_total ? 'g' : 'a'}`} />{t('kpi_health')}</div>
            <div className="kval">{o.pools_online}/{o.pools_total}</div>
            <div className="kfoot">{o.pools_online === o.pools_total ? t('kpi_health_ok') : t('kpi_health_warn')}</div>
          </div>
          <div className="dash-kpi">
            <div className="klbl">{t('kpi_cap')}</div>
            <div className="kval">{cap!.used} <small>{t('pool_of')} {cap!.total}</small></div>
            <Meter pct={pct} />
            <div className="kfoot">{fmtPct(pct)} {t('kpi_cap_used')}</div>
          </div>
          <div className="dash-kpi">
            <div className="klbl">{t('kpi_snaps')}</div>
            <div className="kval">{fmtInt(o.snapshots_total)}</div>
            <div className="kfoot">{o.jobs_active} {t('kpi_snaps_foot')}</div>
          </div>
          <div className="dash-kpi">
            <div className="klbl">{t('kpi_scrub')}</div>
            <div className="kval">
              <span style={{ color: o.last_scrub.errors === 0 ? 'var(--ok)' : 'var(--err)', display: 'inline-flex' }}>
                {o.last_scrub.errors === 0 ? <IconCheck size={17} /> : <IconError size={17} />}
              </span>
              {o.last_scrub.errors === 0 ? 'OK' : String(o.last_scrub.errors)}
            </div>
            <div className="kfoot">{o.last_scrub.pool} · {timeAgo(o.last_scrub.ts, t)}</div>
          </div>
        </div>

        <div className="dash-grid">
          {(missing.data?.length ?? 0) > 0 && (
            <div className="card dash-full" role="alert" data-testid="missing-pools-banner">
              {missing.data!.map((m) => {
                const mins = Math.max(1, Math.round((Date.now() - new Date(m.last_seen).getTime()) / 60000));
                return (
                  <div className="alert" key={m.name}>
                    <div className="ico err"><IconError size={17} /></div>
                    <div className="grow" style={{ flex: 1, minWidth: 0 }}>
                      <b>{t('dash_missing_title')}: {m.name}</b>
                      <div className="muted" style={{ marginTop: 2 }}>{t('dash_missing_body', { pools: m.name, mins })}</div>
                    </div>
                    <button className="btn sm" disabled={!isAdmin} title={!isAdmin ? t('no_permission') : undefined}
                      onClick={() => openModal('importpool', { name: m.name })}>{t('dash_missing_import')}</button>
                    <button className="btn sm" onClick={() => navigate('pools')}>{t('dash_see_all')}</button>
                  </div>
                );
              })}
            </div>
          )}

          {disks.data && disks.data.length > 0 && (
            <DashCard className="dash-span6" title={t('rec_title')}>
              <div style={{ display: 'flex', gap: 22, flexWrap: 'wrap', alignItems: 'center' }}>
                <Donut
                  centerValue={String(disks.data.length)}
                  centerLabel={t('rec_disks_label')}
                  parts={[
                    { value: disks.data.filter((d) => d.smart === 'ok').length, color: 'var(--ok)', label: t('rec_legend_ok') },
                    { value: disks.data.filter((d) => d.smart === 'warn').length, color: 'var(--warn)', label: t('rec_legend_warn') },
                    { value: disks.data.filter((d) => d.smart === 'crit').length, color: 'var(--err)', label: t('rec_legend_crit') },
                    { value: disks.data.filter((d) => d.smart === 'unknown').length, color: 'var(--info)', label: t('rec_legend_unknown') },
                  ]} />
                <div style={{ flex: 1, minWidth: 240 }}>
                  {(recs.data ?? []).length === 0
                    ? <div className="empty">{t('rec_empty')}</div>
                    : (recs.data ?? []).map((r) => <RecLine key={`${r.dev}:${r.kind}`} r={r} />)}
                </div>
              </div>
            </DashCard>
          )}

          {perf.data?.arc && (
            <DashCard className="dash-span6" title={t('perf_title')}>
              <div className="poolmeta" style={{ padding: 0, marginBottom: 10 }}>
                <span>{t('perf_arc')}{' '}· {t('perf_arc_size')} <b>{fmtBytes(perf.data.arc.size_bytes)}</b></span>
                <span>{t('perf_arc_hit')} <b>{fmtPct(perf.data.arc.hit_pct)}</b></span>
              </div>
              <div className="tblwrap">
                <table className="data">
                  <thead>
                    <tr><th className="slack">{t('perf_pool')}</th><th className="num">{t('perf_read')}</th><th className="num">{t('perf_write')}</th></tr>
                  </thead>
                  <tbody>
                    {perf.data.pools.map((pp) => (
                      <tr key={pp.name}>
                        <td>{pp.name}</td>
                        <td className="num">{fmtBytes(pp.read_bps)}/s</td>
                        <td className="num">{fmtBytes(pp.write_bps)}/s</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </DashCard>
          )}

          {poolList.map((p, i) => (
            <div className={`dash-cell ${poolLastFull && i === poolList.length - 1 ? 'dash-full' : poolSpan}`} key={p.name}>
              <PoolCard pool={p} onChanged={() => { pools.reload(); ov.reload(); }} />
            </div>
          ))}

          <DashCard className="dash-span4" flush title={t('dash_alerts')} extra={<span>{o.alerts.length}</span>}>
            {o.alerts.length === 0 && <div className="empty" style={{ padding: '12px 14px' }}>{t('dash_no_alerts')}</div>}
            {o.alerts.map((a) => <AlertRow key={a.id} a={a} />)}
          </DashCard>

          <DashCard className="dash-span4" title={t('dash_temps')}>
            {(!disks.data || disks.data.length === 0) && <div className="empty">{t('empty')}</div>}
            {(disks.data ?? []).map((d) => {
              const tC = d.temp_c;
              const w = tC == null ? 0 : Math.min(100, Math.round((tC / 70) * 100));
              return (
                <div className="dev" key={d.dev}>
                  <span className={`led ${d.smart === 'ok' ? 'g' : d.smart === 'warn' ? 'a' : d.smart === 'crit' ? 'r' : 'o'}`} />
                  <span className="dname" title={d.model}>{d.dev.replace('/dev/', '')}</span>
                  <span className="drole">{d.pool && d.pool !== '—' ? d.pool : ''}</span>
                  <span className="temp" style={{ marginLeft: 'auto' }}>{tC == null ? '—' : `${tC}°C`}</span>
                  <span className="minibar" style={{ width: 70 }}><i style={{ width: `${w}%` }} /></span>
                </div>
              );
            })}
          </DashCard>

          <DashCard className="dash-span4" flush title={t('dash_events')}>
            <div className="evt-log" style={{ padding: '6px 14px 10px' }}>
              {o.activity.slice(0, 6).map((a, i) => (
                <div className="evt" key={i}>
                  <span className="ts">{fmtDateTime(a.ts)}</span>
                  <span className="msg">{a.text}{a.detail ? <span className="dim"> · {a.detail}</span> : null}</span>
                </div>
              ))}
              {o.activity.length === 0 && <div className="empty">{t('empty')}</div>}
            </div>
          </DashCard>
        </div>
      </>)}
    </div>
  );
}
