// Vista Tendencias: evolución histórica de capacidad de pools (used_pct) y
// temperatura de discos (temp) con rangos de 7 días a 5 años. Los rangos
// largos usan agregados diarios del servidor (series_daily, #85).
import { useMemo, useState } from 'react';
import { useApp } from '../ui/store';
import { useData } from '../ui/useData';
import { getProvider } from '../data';
import type { SeriesResp } from '../data/types';

const RANGES: { days: number; label: string }[] = [
  { days: 7, label: '7d' },
  { days: 30, label: '30d' },
  { days: 90, label: '90d' },
  { days: 365, label: '1y' },
  { days: 1825, label: '5y' },
];

export default function Trends() {
  const { t } = useApp();
  const pools = useData((p) => p.getPools());
  const disks = useData((p) => p.getDisks());

  // Fuente seleccionada: pool.<name>.used_pct | disk.<dev>.temp
  const [source, setSource] = useState('');
  const [days, setDays] = useState(30);

  const series = useData(
    (p) => (source ? p.getSeries(source, days) : Promise.resolve({ source, points: [] } as SeriesResp)),
    [source, days],
  );

  // Opciones de fuente: pools primero, luego discos con sensor.
  const options = useMemo(() => {
    const out: { value: string; label: string; group: string }[] = [];
    for (const po of pools.data ?? []) {
      out.push({ value: `pool.${po.name}.used_pct`, label: po.name, group: t('tr_pools') });
    }
    for (const d of disks.data ?? []) {
      if (d.temp_c != null) {
        out.push({ value: `disk.${d.dev}.temp`, label: `${d.dev} · ${d.model}`, group: t('tr_disks') });
      }
    }
    return out;
  }, [pools.data, disks.data, t]);

  // Si no hay fuente y ya hay opciones, preseleccionar la primera.
  if (!source && options.length > 0) {
    setTimeout(() => setSource(options[0].value), 0);
  }

  return (
    <section className="blk">
      <h2 style={{ fontSize: 20, fontWeight: 800, letterSpacing: '-.02em' }}>{t('tr_title')}</h2>
      <p className="muted" style={{ margin: '4px 0 16px' }}>{t('tr_sub')}</p>

      <div className="tr-controls">
        <select className="tr-src" value={source} onChange={(e) => setSource(e.target.value)}
          aria-label={t('tr_source')}>
          {!source && <option value="">{t('tr_select')}</option>}
          {options.map((o: { value: string; label: string; group: string }) => (
            <option key={o.value} value={o.value}>{o.label} ({o.group})</option>
          ))}
        </select>
        <div className="tr-ranges" role="group" aria-label={t('tr_range')}>
          {RANGES.map((r) => (
            <button key={r.days} type="button"
              className={`tr-range${days === r.days ? ' active' : ''}`}
              onClick={() => setDays(r.days)}>
              {r.label}
            </button>
          ))}
        </div>
      </div>

      <div className="card pad tr-card">
        {series.loading && <p className="muted">{t('loading')}</p>}
        {!series.loading && !!series.error && (
          <p className="form-err" role="alert">{t('tr_error')}</p>
        )}
        {!series.loading && !series.error && (
          <TrendChart points={series.data?.points ?? []} />
        )}
      </div>
    </section>
  );
}

// TrendChart — gráfica de área SVG pura (sin librerías): línea + relleno
// degradado + etiquetas de eje Y (máx/mín) y del último valor.
function TrendChart({ points }: { points: SeriesResp['points'] }) {
  const { t } = useApp();
  const W = 760, H = 240, PAD = 12;
  const last = points[points.length - 1];

  if (points.length < 2) {
    return <p className="muted" style={{ padding: 40, textAlign: 'center' }}>{t('tr_empty')}</p>;
  }

  const xs = points.map((p) => p.ts);
  const ys = points.map((p) => p.value);
  const minX = Math.min(...xs), maxX = Math.max(...xs);
  const minY = Math.min(...ys), maxY = Math.max(...ys);
  const rangeX = maxX - minX || 1;
  const rangeY = maxY - minY || 1;
  const X = (ts: number) => PAD + ((ts - minX) / rangeX) * (W - PAD * 2);
  const Y = (v: number) => H - PAD - ((v - minY) / rangeY) * (H - PAD * 2);
  const line = points.map((p, i) => `${i === 0 ? 'M' : 'L'}${X(p.ts).toFixed(1)},${Y(p.value).toFixed(1)}`).join(' ');
  const area = `${line} L${X(maxX).toFixed(1)},${H - PAD} L${X(minX).toFixed(1)},${H - PAD} Z`;

  const fmtY = (v: number) => (v >= 1000 ? `${(v / 1000).toFixed(1)}k` : `${Math.round(v)}`);
  const fmtT = (ts: number) => new Date(ts * 1000).toLocaleDateString();

  return (
    <div className="tr-plot">
      <svg viewBox={`0 0 ${W} ${H}`} role="img" aria-label={t('tr_chart')}
        style={{ width: '100%', height: 'auto', display: 'block' }}>
        <defs>
          <linearGradient id="trFill" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="var(--accent)" stopOpacity="0.35" />
            <stop offset="100%" stopColor="var(--accent)" stopOpacity="0.04" />
          </linearGradient>
        </defs>
        <path d={area} fill="url(#trFill)" />
        <path d={line} fill="none" stroke="var(--accent)" strokeWidth="2"
          strokeLinejoin="round" strokeLinecap="round" />
        <text x={PAD} y={PAD + 4} fontSize="11" fill="var(--muted)" fontWeight={700}>
          {fmtY(maxY)}
        </text>
        <text x={PAD} y={H - PAD - 4} fontSize="11" fill="var(--muted)" fontWeight={700}>
          {fmtY(minY)}
        </text>
        {last && (
          <text x={W - PAD} y={PAD + 4} fontSize="11" fill="var(--accent)" fontWeight={800} textAnchor="end">
            {fmtY(last.value)}
          </text>
        )}
      </svg>
      {last && (
        <div className="muted" style={{ fontSize: 12, marginTop: 4 }}>
          {t('tr_last')}: <b style={{ color: 'var(--text)' }}>{fmtY(last.value)}</b> · {fmtT(last.ts)}
        </div>
      )}
    </div>
  );
}
