// admin-bar.tsx — Tarjeta de administración horizontal con secciones
// desplegables, portada del asset canónico de la skill webapp-shell
// (Deltos v1.9.2). Estructura y clases EXACTAS del asset; cada app aporta
// sus secciones (paneles/endpoints). Iconos propios de EasyZFS.
import { useState, type ReactNode } from 'react';
import { IconChev, IconShield } from './icons';

/** Contenedor con borde izquierdo de acento y fondo tintado muy sutil. */
export function AdminCard({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <section className={`rounded-2xl border border-border border-l-4 border-l-accent bg-accent/[0.03] p-4 md:p-5 ${className ?? ''}`}>
      {children}
    </section>
  );
}

/** Botón desplegable de la barra. Estado activo con acento. */
export function AdminButton({
  active, icon, label, onClick,
}: {
  active: boolean;
  icon?: ReactNode;
  label: string;
  onClick: () => void;
}) {
  const base = 'inline-flex h-9 shrink-0 items-center gap-1.5 rounded-xl border px-3 text-[13px] font-medium transition-colors';
  const tone = active ? 'border-accent bg-accent-soft text-accent' : 'border-border bg-elevated text-text-secondary hover:bg-hover hover:text-text-primary';
  return (
    <button type="button" aria-expanded={active} onClick={onClick} className={`${base} ${tone}`}>
      {icon}
      <span className="hidden sm:inline">{label}</span>
      <IconChev className={`h-3.5 w-3.5 shrink-0 transition-transform ${active ? 'rotate-180' : ''}`} />
    </button>
  );
}

/** Panel que aparece debajo de la barra cuando su sección está abierta. */
export function AdminPanel({ children }: { children: ReactNode }) {
  return <div className="mt-4 border-t border-border pt-4">{children}</div>;
}

export interface AdminSection {
  id: string;
  label?: string;
  icon?: ReactNode;
  panel?: ReactNode;
  content?: ReactNode;
  align?: 'left' | 'right';
}

export interface AdminBarProps {
  title: string;
  sections: AdminSection[];
}

/** Barra de administración horizontal. Cada sección es un botón desplegable
 *  o un widget inline; las `align="right"` se alinean a la derecha. */
export function AdminBar({ title, sections }: AdminBarProps) {
  const [open, setOpen] = useState<Record<string, boolean>>({});

  const left = sections.filter((s) => s.align !== 'right');
  const right = sections.filter((s) => s.align === 'right');

  const toggle = (id: string) => setOpen((prev) => ({ ...prev, [id]: !prev[id] }));

  const renderSection = (s: AdminSection) => {
    if (s.content) return <div key={s.id}>{s.content}</div>;
    return (
      <AdminButton
        key={s.id}
        active={!!open[s.id]}
        icon={s.icon}
        label={s.label ?? s.id}
        onClick={() => toggle(s.id)}
      />
    );
  };

  return (
    <AdminCard>
      <div className="flex flex-wrap items-start gap-3 sm:gap-4">
        <div className="flex h-9 shrink-0 items-center gap-2">
          <IconShield size={20} className="text-accent" />
          <h2 className="font-display text-[15px] font-semibold">{title}</h2>
        </div>
        <div className="hidden h-6 w-px bg-border sm:block" />

        {left.map(renderSection)}

        {right.map((s) => (
          <div key={s.id} className={left.length > 0 ? 'ml-auto' : ''}>
            {renderSection(s)}
          </div>
        ))}
      </div>

      {sections.map(
        (s) =>
          open[s.id] && s.panel != null && <AdminPanel key={s.id}>{s.panel}</AdminPanel>,
      )}
    </AdminCard>
  );
}
