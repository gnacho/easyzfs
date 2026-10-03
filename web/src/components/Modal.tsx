// Sistema de modales: contexto + contenedor accesible.
// - Escape cierra, clic en el overlay cierra, foco atrapado dentro del modal.
import { createContext, useCallback, useContext, useEffect, useRef, useState } from 'react';
import type { ReactNode } from 'react';

export interface ModalState {
  name: string;
  props?: Record<string, unknown>;
}

interface ModalCtx {
  modal: ModalState | null;
  openModal: (name: string, props?: Record<string, unknown>) => void;
  closeModal: () => void;
}

const Ctx = createContext<ModalCtx | null>(null);

export function ModalProvider({ children }: { children: ReactNode }) {
  const [modal, setModal] = useState<ModalState | null>(null);
  const openModal = useCallback((name: string, props?: Record<string, unknown>) => setModal({ name, props }), []);
  const closeModal = useCallback(() => setModal(null), []);
  return <Ctx.Provider value={{ modal, openModal, closeModal }}>{children}</Ctx.Provider>;
}

export function useModal(): ModalCtx {
  const ctx = useContext(Ctx);
  if (!ctx) throw new Error('useModal fuera de ModalProvider');
  return ctx;
}

const FOCUSABLE = 'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])';

// Contenedor visual del modal con gestión de foco y teclado.
// label = título del diálogo (el mismo texto del h3): nombre accesible del role="dialog".
// xlarge = diálogo tipo Pulse en desktop (800x600, columna flex con scroll
// interno); en móvil sigue siendo la hoja anclada abajo (ver .modal-xlarge en CSS).
export function ModalBox({ children, onClose, wide, xlarge, label }: {
  children: ReactNode; onClose: () => void; wide?: boolean; xlarge?: boolean; label: string;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const prevFocus = useRef<Element | null>(null);

  useEffect(() => {
    prevFocus.current = document.activeElement;
    const el = ref.current;
    if (!el) return;
    // Foco inicial al primer campo/botón
    const first = el.querySelector<HTMLElement>(FOCUSABLE);
    (first ?? el).focus();

    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') { e.stopPropagation(); onClose(); return; }
      if (e.key !== 'Tab' || !ref.current) return;
      // Foco atrapado: cicla entre los elementos enfocables del modal
      const items = Array.from(ref.current.querySelectorAll<HTMLElement>(FOCUSABLE))
        .filter((x) => !x.hasAttribute('disabled'));
      if (items.length === 0) return;
      const idx = items.indexOf(document.activeElement as HTMLElement);
      if (e.shiftKey && (idx <= 0)) { items[items.length - 1].focus(); e.preventDefault(); }
      else if (!e.shiftKey && (idx === items.length - 1 || idx === -1)) { items[0].focus(); e.preventDefault(); }
    };
    document.addEventListener('keydown', onKey, true);
    return () => {
      document.removeEventListener('keydown', onKey, true);
      (prevFocus.current as HTMLElement | null)?.focus?.();
    };
  }, [onClose]);

  return (
    <div className="overlay" onClick={(e) => { if (e.target === e.currentTarget) onClose(); }}>
      <div className={xlarge ? 'modal modal-xlarge' : 'modal'} role="dialog" aria-modal="true" ref={ref} tabIndex={-1}
        style={wide ? { maxWidth: 620 } : undefined}
        aria-label={label}>
        {children}
      </div>
    </div>
  );
}
