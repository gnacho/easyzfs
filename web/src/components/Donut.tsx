// Donut SVG por segmentos (sin dependencias): círculo con stroke-dasharray
// sobre circunferencia 100. Colores = variables CSS del tema (tokens).
export function Donut({ parts, size = 132, centerValue, centerLabel }: {
  parts: { value: number; color: string; label: string }[];
  size?: number;
  centerValue: string;
  centerLabel: string;
}) {
  const total = parts.reduce((n, p) => n + p.value, 0);
  let acc = 0;
  return (
    <div style={{ display: 'inline-flex', alignItems: 'center', gap: 16 }}>
      <svg width={size} height={size} viewBox="0 0 42 42" role="img" aria-label={centerLabel}>
        {total === 0 ? (
          <circle cx="21" cy="21" r="15.9155" fill="none" stroke="var(--border)" strokeWidth="5" />
        ) : (
          parts.filter((p) => p.value > 0).map((p) => {
            const pct = (p.value / total) * 100;
            // dashoffset 25 = empezar a las 12 en punto; los segmentos se
            // encadenan restando el acumulado.
            const el = (
              <circle key={p.label} cx="21" cy="21" r="15.9155" fill="none"
                stroke={p.color} strokeWidth="5"
                strokeDasharray={`${pct} ${100 - pct}`}
                strokeDashoffset={25 - acc}>
                <title>{`${p.label}: ${p.value}`}</title>
              </circle>
            );
            acc += pct;
            return el;
          })
        )}
        <text x="21" y="20.5" textAnchor="middle" style={{ fontSize: 10, fontWeight: 700, fill: 'var(--text)' }}>{centerValue}</text>
        <text x="21" y="27.5" textAnchor="middle" style={{ fontSize: 3.9, fill: 'var(--text2)' }}>{centerLabel}</text>
      </svg>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 13.5 }}>
        {parts.map((p) => (
          <span key={p.label} style={{ display: 'inline-flex', alignItems: 'center', gap: 6, color: 'var(--text2)' }}>
            <i style={{ width: 9, height: 9, borderRadius: '50%', background: p.color, display: 'inline-block' }} />
            <b style={{ color: 'var(--text)', fontWeight: 650 }}>{p.value}</b> {p.label}
          </span>
        ))}
      </div>
    </div>
  );
}
