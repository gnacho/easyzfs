// Gráfica de ocupación del pool (área, SVG a mano, sin dependencias): muestra
// el valor actual, el delta del periodo y el rango mín-máx de la serie.
import { fmtPct } from '../ui/format';

export function UsageChart({ points, days, threshold = 80, width = 240, height = 58 }: {
  points: { ts: number; value: number }[];
  days: number;
  threshold?: number;
  width?: number;
  height?: number;
}) {
  if (points.length < 2) return null;
  const pad = 3;
  const vals = points.map((p) => p.value);
  const min = Math.min(...vals);
  const max = Math.max(...vals);
  let lo = Math.max(0, min - 2);
  let hi = Math.min(100, max + 2);
  if (hi - lo < 4) { lo = Math.max(0, lo - 2); hi = Math.min(100, hi + 2); }
  const x = (i: number) => pad + (i * (width - 2 * pad)) / (points.length - 1);
  const y = (v: number) => height - pad - ((v - lo) * (height - 2 * pad)) / (hi - lo);
  const line = points.map((p, i) => `${i === 0 ? 'M' : 'L'}${x(i).toFixed(1)} ${y(p.value).toFixed(1)}`).join(' ');
  const area = `${line} L${x(points.length - 1).toFixed(1)} ${(height - pad).toFixed(1)} L${x(0).toFixed(1)} ${(height - pad).toFixed(1)} Z`;
  const showThreshold = threshold > lo && threshold < hi;
  const last = points[points.length - 1].value;
  const first = points[0].value;
  const delta = last - first;
  const up = delta >= 0.05;
  const down = delta <= -0.05;
  return (
    <div className="pu">
      <svg width={width} height={height} viewBox={`0 0 ${width} ${height}`} role="img" aria-hidden="true">
        {showThreshold && (
          <line x1={pad} x2={width - pad} y1={y(threshold)} y2={y(threshold)}
            stroke="var(--warn)" strokeWidth="1" strokeDasharray="3 4" opacity=".7" />
        )}
        <path d={area} fill="var(--accent)" opacity=".12" />
        <path d={line} fill="none" stroke="var(--accent)" strokeWidth="2" strokeLinejoin="round" strokeLinecap="round" />
        <circle cx={x(points.length - 1)} cy={y(last)} r="2.6" fill="var(--accent)" />
      </svg>
      <div className="pu-meta">
        <b>{fmtPct(last)}</b>
        <span className={up ? 'pu-up' : down ? 'pu-down' : ''}>
          {up ? '+' : down ? '−' : ''}{Math.abs(delta).toFixed(1)} pts · {days} d
        </span>
        <span className="pu-range">{fmtPct(min)} – {fmtPct(max)}</span>
      </div>
    </div>
  );
}
