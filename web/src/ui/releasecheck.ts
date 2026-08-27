// releasecheck.ts — aviso pasivo de releases nuevas. La fuente de verdad es el
// servidor (/api/update/status, que cachea el resultado 1 vez al día y usa un
// token GitHub si se configura), así no se consulta GitHub desde el navegador
// ni se depende de una caché larga en localStorage. El ribbon se refresca al
// montar y cada 30 min (barato: el servidor no llama a GitHub en cada GET).
import { useEffect, useState } from 'react';
import { getProvider } from '../data';

const REPO = 'gnacho/easyzfs';
export const RELEASES_URL = `https://github.com/${REPO}/releases`;
const DISMISS_KEY = 'easyzfs-release-dismissed';
const REFRESH_MS = 30 * 60 * 1000;

export type ReleaseState =
  | { kind: 'unknown' }
  | { kind: 'uptodate' }
  | { kind: 'available'; version: string; url: string; notes: string };

// Notifica a los suscritos (ribbon / badge) que hay un resultado nuevo.
type Listener = () => void;
const listeners = new Set<Listener>();
function notify(): void { listeners.forEach((l) => l()); }

// refreshUpdateState — fuerza una re-consulta inmediata de todos los suscriptores
// (lo usa el botón "Comprobar actualizaciones" de Ajustes tras su check).
export function refreshUpdateState(): void { notify(); }

// useReleaseCheck — estado de release respecto al servidor. Solo actúa con
// enabled=true (sesión admin real). Re-consulta al montar y cada 30 min; no
// conserva resultados en localStorage para no quedarse "atascado".
export function useReleaseCheck(_currentVersion: string | undefined, enabled: boolean): ReleaseState {
  const [state, setState] = useState<ReleaseState>({ kind: 'unknown' });

  useEffect(() => {
    if (!enabled) return;
    let alive = true;
    const refresh = () => {
      getProvider()
        .getUpdateStatus()
        .then((st) => {
          if (!alive) return;
          if (st && st.available && st.latest) {
            setState({
              kind: 'available',
              version: st.latest.replace(/^v/, ''),
              url: st.releaseUrl || RELEASES_URL,
              notes: st.releaseNotes || '',
            });
          } else {
            setState({ kind: 'uptodate' });
          }
        })
        .catch(() => {
          // Sin sesión/red: conserva el estado (available) o marca desconocido.
          if (alive) setState((p) => (p.kind === 'available' ? p : { kind: 'unknown' }));
        });
    };
    refresh();
    const onNotify = () => refresh();
    listeners.add(onNotify);
    const timer = window.setInterval(refresh, REFRESH_MS);
    return () => { alive = false; listeners.delete(onNotify); window.clearInterval(timer); };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [enabled]);

  return state;
}

// Dismissal del ribbon por versión (vuelve a salir si aparece otra más nueva).
export function getReleaseDismissed(): string {
  return localStorage.getItem(DISMISS_KEY) ?? '';
}
export function dismissRelease(version: string): void {
  try { localStorage.setItem(DISMISS_KEY, version); } catch { /* sin storage */ }
}
