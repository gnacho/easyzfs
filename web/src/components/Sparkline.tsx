// Sparkline — gráfico de línea SVG sin dependencias (U2).
// Dibuja la serie (epoch ts, value) como polilínea con min/max normalizados;
// 0-1 puntos → no dibuja nada (hueco de datos = estado normal, no error).
import { useId } from 'react';

export function Sparkline({ points, width = 120, height = 32, ariaLabel }: {
  points: { ts: number; value: number }[];
  width?: number;
  height?: number;
  ariaLabel?: string;
}) {
  const uid = useId();
  if (!points || points.length < 2) return <svg width={width} height={height} role="img" aria-label={ariaLabel} />;

  let min = Infinity, max = -Infinity;
  for (const p of points) {
    if (p.value < min) min = p.value;
    if (p.value > max) max = p.value;
  }
  const span = max - min;
  const pad = span === 0 ? 1 : span * 0.08;
  const lo = min - pad, hi = max + pad;

  const x = (ts: number) => ((ts - points[0].ts) / (points[points.length - 1].ts - points[0].ts)) * (width - 2) + 1;
  const y = (v: number) => height - 1 - ((v - lo) / (hi - lo)) * (height - 2);

  const d = points.map((p, i) => `${i === 0 ? 'M' : 'L'}${x(p.ts).toFixed(1)},${y(p.value).toFixed(1)}`).join(' ');

  return (
    <svg width={width} height={height} viewBox={`0 0 ${width} ${height}`} role="img" aria-label={ariaLabel}>
      <defs>
        <linearGradient id={`grad-${uid}`} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor="currentColor" stopOpacity="0.28" />
          <stop offset="100%" stopColor="currentColor" stopOpacity="0" />
        </linearGradient>
      </defs>
      <path d={`${d}L${width - 1},${height}L1,${height}Z`} fill={`url(#grad-${uid})`} />
      <path d={d} fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinejoin="round" strokeLinecap="round" />
    </svg>
  );
}
