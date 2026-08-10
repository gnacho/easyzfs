// releasecheck.ts — aviso pasivo de releases nuevas en GitHub. Comprueba la
// última release UNA vez a la semana por navegador (caché en localStorage)
// con una GET anónima: el servidor no hace phone-home. Si hay versión más
// nueva, la shell muestra un ribbon superior (descartable por versión, con
// botón Actualizar) y Ajustes → Acerca de ofrece "Comprobar actualizaciones"
// (solo admin; fuerza la consulta al momento). Incluye las release notes
// (primeros ~600 caracteres del body) para mostrar un resumen de novedades.
import { useEffect, useState } from 'react';

const REPO = 'gnacho/easyzfs';
export const RELEASES_URL = `https://github.com/${REPO}/releases`;
const CACHE_KEY = 'easyzfs-release-check';
const DISMISS_KEY = 'easyzfs-release-dismissed';
const WEEK_MS = 7 * 24 * 60 * 60 * 1000;
const NOTES_MAX = 600;

export type ReleaseState =
  | { kind: 'unknown' }
  | { kind: 'uptodate' }
  | { kind: 'available'; version: string; url: string; notes: string };

interface Cache { ts: number; tag: string; url: string; notes: string }

// Comparación semver numérica ('v' opcional): 1.10.0 > 1.9.0.
export function compareSemver(a: string, b: string): number {
  const pa = a.replace(/^v/, '').split('.').map((x) => parseInt(x, 10) || 0);
  const pb = b.replace(/^v/, '').split('.').map((x) => parseInt(x, 10) || 0);
  for (let i = 0; i < Math.max(pa.length, pb.length); i++) {
    const d = (pa[i] ?? 0) - (pb[i] ?? 0);
    if (d !== 0) return d;
  }
  return 0;
}

function readCache(): Cache | null {
  try {
    const raw = localStorage.getItem(CACHE_KEY);
    if (!raw) return null;
    const c = JSON.parse(raw) as Cache;
    return typeof c?.ts === 'number' && typeof c?.tag === 'string' ? c : null;
  } catch { return null; }
}

function truncateNotes(body: string): string {
  if (!body) return '';
  const cleaned = body.replace(/^#{1,4}\s+.*$/gm, '').replace(/\*\*/g, '').trim();
  if (cleaned.length <= NOTES_MAX) return cleaned;
  return cleaned.slice(0, NOTES_MAX).replace(/\s+\S*$/, '') + '…';
}

async function fetchLatest(): Promise<Cache> {
  let res = await fetch(`https://api.github.com/repos/${REPO}/releases/latest`);
  let tag = '';
  let url = RELEASES_URL;
  let notes = '';
  if (res.ok) {
    const j = await res.json();
    tag = j.tag_name ?? '';
    url = j.html_url ?? url;
    notes = truncateNotes(j.body ?? '');
  } else if (res.status === 404) {
    // Sin release publicada: fallback al último tag
    res = await fetch(`https://api.github.com/repos/${REPO}/tags?per_page=1`);
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const tags = await res.json();
    tag = tags?.[0]?.name ?? '';
    url = `${RELEASES_URL}/tag/${tag}`;
  } else {
    throw new Error(`HTTP ${res.status}`); // 403 = rate-limit 60/h por IP
  }
  const c: Cache = { ts: Date.now(), tag, url, notes };
  try { localStorage.setItem(CACHE_KEY, JSON.stringify(c)); } catch { /* sin storage */ }
  return c;
}

function toState(c: Cache | null, currentVersion: string | undefined): ReleaseState {
  if (!c || !c.tag || !currentVersion) return { kind: 'unknown' };
  return compareSemver(c.tag, currentVersion) > 0
    ? { kind: 'available', version: c.tag.replace(/^v/, ''), url: c.url, notes: c.notes }
    : { kind: 'uptodate' };
}

// Notifica a los suscritos (ribbon de la shell) que hay un resultado nuevo.
type Listener = () => void;
const listeners = new Set<Listener>();
function notify(): void { listeners.forEach((l) => l()); }

// checkReleaseNow — comprobación manual e inmediata (botón "Comprobar
// actualizaciones" de Ajustes). Ignora la caducidad semanal; devuelve el
// estado resultante. Si la red falla, conserva la caché anterior y relanza.
export async function checkReleaseNow(currentVersion: string | undefined): Promise<ReleaseState> {
  try {
    const c = await fetchLatest();
    notify();
    return toState(c, currentVersion);
  } catch (e) {
    notify();
    throw e;
  }
}

// useReleaseCheck — estado de release respecto a currentVersion. Solo actúa
// con enabled=true (sesión real, no demo). Pasivo: como mucho 1 fetch por
// semana por navegador; se re-suscribe a los cambios de checkReleaseNow.
export function useReleaseCheck(currentVersion: string | undefined, enabled: boolean): ReleaseState {
  const [cache, setCache] = useState<Cache | null>(() => readCache());

  useEffect(() => {
    const refresh = () => setCache(readCache());
    listeners.add(refresh);
    return () => { listeners.delete(refresh); };
  }, []);

  useEffect(() => {
    if (!enabled) return;
    const c = readCache();
    if (c && Date.now() - c.ts < WEEK_MS) { setCache(c); return; }
    let alive = true;
    fetchLatest().then((n) => { if (alive) setCache(n); }).catch(() => {
      // Sin red/rate-limit: conserva la caché anterior (si la hay)
      if (alive) setCache(readCache());
    });
    return () => { alive = false; };
  }, [enabled]);

  return toState(cache, currentVersion);
}

// Dismissal del ribbon por versión (vuelve a salir si aparece otra más nueva).
export function getReleaseDismissed(): string {
  return localStorage.getItem(DISMISS_KEY) ?? '';
}
export function dismissRelease(version: string): void {
  try { localStorage.setItem(DISMISS_KEY, version); } catch { /* sin storage */ }
}
