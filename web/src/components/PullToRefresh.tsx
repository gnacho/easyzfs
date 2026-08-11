// Pull-to-refresh móvil (issue #20, patrón Keynest PR #79): al tirar hacia
// abajo desde el top de la página recarga la app, permitiendo coger un deploy
// nuevo sin cerrar y reabrirla. Solo actúa cuando window.scrollY <= 0 y el
// gesto es un pull vertical con un solo dedo. CSS nativo de EasyZFS.
import { useEffect, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { IconRefresh } from './icons';

const THRESHOLD = 70;

export default function PullToRefresh({ children }: { children: ReactNode }) {
  const startY = useRef<number | null>(null);
  const pullRef = useRef(0);
  const [pull, setPull] = useState(0);
  const [refreshing, setRefreshing] = useState(false);

  useEffect(() => {
    const onTouchStart = (e: TouchEvent) => {
      if (window.scrollY <= 0 && e.touches.length === 1) {
        startY.current = e.touches[0].clientY;
      } else {
        startY.current = null;
      }
    };
    const onTouchMove = (e: TouchEvent) => {
      if (startY.current === null) return;
      const dy = e.touches[0].clientY - startY.current;
      if (dy > 0) {
        const v = Math.min(dy * 0.5, 120);
        pullRef.current = v;
        setPull(v);
      } else {
        pullRef.current = 0;
        setPull(0);
      }
    };
    const onTouchEnd = () => {
      if (startY.current === null) return;
      startY.current = null;
      if (pullRef.current >= THRESHOLD) {
        setRefreshing(true);
        window.location.reload();
        return;
      }
      pullRef.current = 0;
      setPull(0);
    };
    window.addEventListener('touchstart', onTouchStart, { passive: true });
    window.addEventListener('touchmove', onTouchMove, { passive: true });
    window.addEventListener('touchend', onTouchEnd, { passive: true });
    return () => {
      window.removeEventListener('touchstart', onTouchStart);
      window.removeEventListener('touchmove', onTouchMove);
      window.removeEventListener('touchend', onTouchEnd);
    };
  }, []);

  return (
    <div className="ptr">
      <div
        className="ptr-ind"
        style={{ height: Math.max(pull, refreshing ? 28 : 0), transition: refreshing ? 'height .2s' : 'none' }}
      >
        <IconRefresh size={18} className={(pull > 0 || refreshing) ? 'ptr-spin' : undefined} />
      </div>
      <div style={{ transform: pull > 0 ? `translateY(${pull}px)` : undefined, transition: pull > 0 ? 'none' : 'transform .2s' }}>
        {children}
      </div>
    </div>
  );
}
