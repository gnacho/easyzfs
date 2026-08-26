// Controles básicos del sistema visual: Badge, Meter, Seg, Switch, Spinner
import { useEffect, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { fmtPct } from '../ui/format';
import { IconChev } from './icons';

export type Tone = 'ok' | 'warn' | 'err' | 'info';

// Select — desplegable propio (listbox). El <select> nativo pinta su popup con
// los colores del SO/navegador (LibreWolf RFP, Chrome/Linux), ignorando el tema
// de la app; este va con los tokens y se ve igual en todas partes.
export function Select<T extends string>({ options, value, onChange, ariaLabel }: {
  options: { v: T; label: string }[];
  value: T;
  onChange: (v: T) => void;
  ariaLabel?: string;
}) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  const current = options.find((o) => o.v === value) ?? options[0];

  useEffect(() => {
    if (!open) return;
    const onDoc = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') setOpen(false); };
    document.addEventListener('mousedown', onDoc);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDoc);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);

  const move = (dir: 1 | -1) => {
    const i = options.findIndex((o) => o.v === value);
    const next = options[(i + dir + options.length) % options.length];
    onChange(next.v);
  };

  return (
    <div className="sel" ref={ref}>
      <button type="button" className="sel-btn" aria-label={ariaLabel}
        aria-haspopup="listbox" aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
        onKeyDown={(e) => {
          if (e.key === 'ArrowDown') { e.preventDefault(); open ? move(1) : setOpen(true); }
          if (e.key === 'ArrowUp') { e.preventDefault(); open ? move(-1) : setOpen(true); }
        }}>
        <span>{current.label}</span>
        <span className={`sel-chev${open ? ' up' : ''}`}><IconChev /></span>
      </button>
      {open && (
        <div className="sel-pop" role="listbox" aria-label={ariaLabel}>
          {options.map((o) => (
            <button key={o.v} type="button" role="option" aria-selected={o.v === value}
              className={`sel-opt${o.v === value ? ' on' : ''}`}
              onClick={() => { onChange(o.v); setOpen(false); }}>
              {o.label}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

export function Badge({ tone, children, dot = true, style }: {
  tone: Tone; children: ReactNode; dot?: boolean; style?: React.CSSProperties;
}) {
  return (
    <span className={`badge ${tone}`} style={style}>
      {dot && <span className="dot" />}
      {children}
    </span>
  );
}

// Barra de capacidad con umbrales aviso/crítico (80/90 por defecto)
export function Meter({ pct, warnAt = 80, critAt = 90 }: { pct: number; warnAt?: number; critAt?: number }) {
  const cls = pct >= critAt ? 'crit' : pct >= warnAt ? 'warn' : '';
  return (
    <div className={`meter ${cls}`} role="progressbar" aria-valuenow={Math.round(pct)} aria-valuemin={0} aria-valuemax={100}>
      <i style={{ width: `${Math.min(100, Math.max(0, pct))}%` }} />
    </div>
  );
}

// Grupo segmentado (topologías, frecuencias…)
export function Seg<T extends string>({ options, value, onChange, ariaLabel }: {
  options: { v: T; label: string }[];
  value: T;
  onChange: (v: T) => void;
  ariaLabel?: string;
}) {
  return (
    <div className="seg" role="group" aria-label={ariaLabel}>
      {options.map((o) => (
        <button key={o.v} type="button" className={o.v === value ? 'on' : ''}
          aria-pressed={o.v === value} onClick={() => onChange(o.v)}>
          {o.label}
        </button>
      ))}
    </div>
  );
}

// Interruptor on/off accesible
export function Switch({ checked, onChange, ariaLabel, disabled }: {
  checked: boolean; onChange: (v: boolean) => void; ariaLabel: string; disabled?: boolean;
}) {
  return (
    <label className="switch">
      <input type="checkbox" checked={checked} disabled={disabled}
        aria-label={ariaLabel} onChange={(e) => onChange(e.target.checked)} />
      <span className="track" />
    </label>
  );
}

export function Spinner({ label }: { label: string }) {
  return <div className="empty" role="status">{label}</div>;
}

// InfoBubble — burbuja explicativa al pasar el ratón (tokens del tema) y al
// tocar en móvil (tabIndex → :focus). glyph: '?' por defecto, 'i' informativo.
// El popover se posiciona con position:fixed desde JS: los ancestros con
// overflow (p.ej. .tblwrap) no pueden recortarlo. Si no cabe arriba (primera
// fila de una tabla) se voltea automáticamente hacia abajo, y el left queda
// sujeto a los bordes del viewport.
export function InfoBubble({ title, glyph = '?', children }: { title?: string; glyph?: '?' | 'i'; children: React.ReactNode }) {
  const btnRef = useRef<HTMLSpanElement>(null);
  const popRef = useRef<HTMLSpanElement>(null);
  const [open, setOpen] = useState(false);
  const [pos, setPos] = useState<{ top: number; left: number } | null>(null);
  const [dir, setDir] = useState<'up' | 'down'>('up');

  const place = () => {
    const btn = btnRef.current, pop = popRef.current;
    if (!btn || !pop) return;
    const r = btn.getBoundingClientRect();
    const pw = pop.offsetWidth, ph = pop.offsetHeight;
    const pad = 8, vw = window.innerWidth, vh = window.innerHeight;
    // Por defecto arriba; voltea abajo si se cortaría con el borde superior
    const up = r.top - ph - pad >= 4;
    const top = up ? r.top - ph - pad : Math.min(r.bottom + pad, vh - ph - 4);
    const left = Math.max(4, Math.min(r.left + r.width / 2 - pw / 2, vw - pw - 4));
    setDir(up ? 'up' : 'down');
    setPos({ top, left });
  };

  useEffect(() => {
    if (!open) { setPos(null); return; }
    // El popover ya está display:block (clase .open) pero opacity:0: medible
    // sin parpadeo. place() calcula y setPos revela (clase .placed) en el
    // mismo ciclo de render tras el primer paint invisible.
    place();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  useEffect(() => {
    if (!open) return;
    const close = () => setOpen(false);
    // al hacer scroll (incl. scroll horizontal de .tblwrap) la posición fixed
    // queda desincronizada del trigger: cerrar es lo más simple y seguro
    window.addEventListener('scroll', close, true);
    window.addEventListener('resize', close);
    return () => {
      window.removeEventListener('scroll', close, true);
      window.removeEventListener('resize', close);
    };
  }, [open]);

  return (
    <span className="infobubble" tabIndex={0} aria-label={title} ref={btnRef}
      onMouseEnter={() => setOpen(true)} onMouseLeave={() => setOpen(false)}
      onFocus={() => setOpen(true)} onBlur={() => setOpen(false)}
      onTouchStart={() => setOpen((o) => !o)}
      onKeyDown={(e) => { if (e.key === 'Escape') setOpen(false); }}>
      {glyph}
      <span className={`infobubble-pop${open ? ' open' : ''}${pos ? ' placed' : ''} ${dir}`} role="tooltip" ref={popRef}
        style={pos ? { top: pos.top, left: pos.left } : undefined}>
        {title && (
          <div className="ib-title">
            <span className="ib-dot" aria-hidden="true" />
            <b>{title}</b>
          </div>
        )}
        {children}
      </span>
    </span>
  );
}

// Pie de tarjeta KPI con valor opcional de porcentaje
export function KpiCard({ label, value, small, foot, meter }: {
  label: string; value: ReactNode; small?: string; foot?: ReactNode; meter?: number;
}) {
  return (
    <div className="card kpi">
      <div className="lbl">{label}</div>
      <div className="val">{value}{small ? <> <small>{small}</small></> : null}</div>
      {meter !== undefined && <Meter pct={meter} />}
      {foot && <div className="foot">{foot}</div>}
    </div>
  );
}

export function pctLabel(used: number, total: number): string {
  return fmtPct(total > 0 ? (used / total) * 100 : 0);
}
