// Vista Panel: KPIs, tarjetas de pool, alertas recientes y actividad.
import { useEffect } from 'react';
import { subscribeEvents } from '../data/events';
import { useData } from '../ui/useData';
import { useApp, alertTargetView } from '../ui/store';
import { fmtBytes, fmtBytesPair, fmtDateTime, fmtInt, fmtPct, timeAgo } from '../ui/format';
import { KpiCard, Spinner } from '../components/ui';
import { PoolCard } from '../components/PoolCard';
import { Donut } from '../components/Donut';
import { RecLine } from '../components/RecLine';
import { IconChev } from '../components/icons';
import type { Alert } from '../data/types';

function AlertRow({ a }: { a: Alert }) {
  const { t, navigate } = useApp();
  const tone = a.level === 'crit' ? 'err' : a.level === 'warn' ? 'warn' : 'info';
  const ico = a.level === 'crit' ? '!' : a.level === 'warn' ? '⚠' : 'i';
  const view = alertTargetView(a.target);
  const go = view ? () => navigate(view) : undefined;
  return (
    <div className={`alert${view ? ' clickable' : ''}`}
      role={view ? 'link' : undefined} tabIndex={view ? 0 : undefined}
      title={view ? t('al_goto') : undefined}
      onClick={go}
      onKeyDown={go ? (e) => { if (e.key === 'Enter') go(); } : undefined}>
      <div className="ico" style={{ background: `var(--${tone}-soft)`, color: `var(--${tone})` }}>{ico}</div>
      <div className="grow" style={{ flex: 1, minWidth: 0 }}>
        <b>{a.message}</b>
        <div className="muted" style={{ marginTop: 2 }}>{a.source}</div>
      </div>
      <span style={{ fontSize: 11.5, color: 'var(--text2)', whiteSpace: 'nowrap' }}>{timeAgo(a.ts, t)}</span>
      {view && <span className="chev"><IconChev /></span>}
    </div>
  );
}

export default function Dashboard() {
  const { t, navigate } = useApp();
  const ov = useData((p) => p.getOverview());
  const pools = useData((p) => p.getPools());
  const perf = useData((p) => p.getPerformance());
  const disks = useData((p) => p.getDisks());
  const recs = useData((p) => p.getRecommendations());

  // Suscripción a eventos en tiempo real: refresca KPIs y progreso de scrub
  useEffect(() => subscribeEvents((ev) => {
    if (ev.type === 'overview' || ev.type === 'alert.new' || ev.type === 'pool.status') ov.reload();
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
    // Salud SMART cambia en vivo: refresca donut de discos y recomendaciones.
    if (ev.type === 'disk.smart') { disks.reload(); recs.reload(); }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }), []);

  const o = ov.data;
  const pct = o && o.cap_total_bytes > 0 ? (o.cap_used_bytes / o.cap_total_bytes) * 100 : 0;
  const cap = o ? fmtBytesPair(o.cap_used_bytes, o.cap_total_bytes) : null;

  return (
    <div className="view">
      {!o && <Spinner label={t('loading')} />}
      {o && (<>
        <div className="grid kpis">
          <KpiCard label={t('kpi_health')} led={o.pools_online === o.pools_total ? 'g' : 'a'}
            value={`${o.pools_total} pools`}
            foot={o.pools_online === o.pools_total ? t('kpi_health_ok') : t('kpi_health_warn')} />
          <KpiCard label={t('kpi_cap')} led={pct >= 90 ? 'r' : pct >= 80 ? 'a' : 'c'}
            value={cap!.used}
            small={`${t('pool_of')} ${cap!.total}`}
            foot={`${fmtPct(pct)} ${t('kpi_cap_used')}`} meter={pct} />
          <KpiCard label={t('kpi_snaps')} led="c" value={fmtInt(o.snapshots_total)}
            foot={`${o.jobs_active} ${t('kpi_snaps_foot')}`} />
          <KpiCard label={t('kpi_scrub')} led={o.last_scrub.errors === 0 ? 'g' : 'r'}
            value={o.last_scrub.errors === 0 ? 'OK' : String(o.last_scrub.errors)}
            foot={`${o.last_scrub.pool} · ${o.last_scrub.errors} ${t('kpi_scrub_errors')} · ${timeAgo(o.last_scrub.ts, t)}`} />
        </div>

        {/* Salud de discos: donut de distribución + recomendaciones del motor
            (acción + disco + motivo + guardas de seguridad) */}
        {disks.data && disks.data.length > 0 && (
          <div className="sect">
            <h2>{t('rec_title')}</h2>
            <div className="card" style={{ padding: '14px 16px', display: 'flex', gap: 26, flexWrap: 'wrap', alignItems: 'center' }}>
              <Donut
                centerValue={String(disks.data.length)}
                centerLabel={t('rec_disks_label')}
                parts={[
                  { value: disks.data.filter((d) => d.smart === 'ok').length, color: 'var(--ok)', label: t('rec_legend_ok') },
                  { value: disks.data.filter((d) => d.smart === 'warn').length, color: 'var(--warn)', label: t('rec_legend_warn') },
                  { value: disks.data.filter((d) => d.smart === 'crit').length, color: 'var(--err)', label: t('rec_legend_crit') },
                  { value: disks.data.filter((d) => d.smart === 'unknown').length, color: 'var(--info)', label: t('rec_legend_unknown') },
                ]} />
              <div style={{ flex: 1, minWidth: 280 }}>
                {(recs.data ?? []).length === 0
                  ? <div className="empty">{t('rec_empty')}</div>
                  : (recs.data ?? []).map((r) => <RecLine key={`${r.dev}:${r.kind}`} r={r} />)}
              </div>
            </div>
          </div>
        )}

        <div className="sect">
          <h2>{t('dash_pools')}
            <span className="actions">
              <button className="btn sm" onClick={() => navigate('pools')}>{t('dash_see_all')}</button>
            </span>
          </h2>
          <div className="grid pools" style={{ gridTemplateColumns: '1fr' }}>
            {(pools.data ?? []).map((p) => (
              <PoolCard key={p.name} pool={p} onChanged={() => { pools.reload(); ov.reload(); }} />
            ))}
          </div>
        </div>

        {/* Rendimiento: solo si hay fuente de stats ARC en el sistema */}
        {perf.data?.arc && (
          <div className="sect">
            <h2>{t('perf_title')}</h2>
            <div className="card" style={{ padding: '14px 16px' }}>
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
            </div>
          </div>
        )}

        <div className="sect">
          <h2>{t('dash_alerts')}</h2>
          <div className="card">
            {o.alerts.length === 0 && <div className="empty">{t('dash_no_alerts')}</div>}
            {o.alerts.map((a) => <AlertRow key={a.id} a={a} />)}
          </div>
        </div>

        {/* grid2 del mockup: temperaturas de disco (LED + minibarra) y
            actividad reciente con formato de registro de eventos */}
        <div className="dash-cols">
        <div className="sect">
          <h2>{t('dash_temps')}</h2>
          <div className="card" style={{ padding: '10px 16px 12px' }}>
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
                  <span className="minibar" style={{ width: 90 }}><i style={{ width: `${w}%` }} /></span>
                </div>
              );
            })}
          </div>
        </div>

        <div className="sect">
          <h2>{t('dash_events')}</h2>
          <div className="card evt-card">
            <div className="evt-log">
              {o.activity.map((a, i) => (
                <div className="evt" key={i}>
                  <span className="ts">{fmtDateTime(a.ts)}</span>
                  <span className="msg">{a.text}{a.detail ? <span className="dim"> · {a.detail}</span> : null}</span>
                </div>
              ))}
              {o.activity.length === 0 && <div className="empty">{t('empty')}</div>}
            </div>
          </div>
        </div>
        </div>
      </>)}
    </div>
  );
}
