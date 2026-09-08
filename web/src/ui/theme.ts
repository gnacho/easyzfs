// Apariencia: tema (familia), modo claro/oscuro/sistema, acento, densidad y
// reducción de animaciones.
// - Tema (familia): 'classic' | 'modern' | 'phosphor' | 'brutalist'.
// - Modo: 'light' | 'dark' | 'system' (= sigue prefers-color-scheme del SO).
//   Aplica a classic y modern; phosphor y brutalist son fijos (ignoran el modo).
// - Acento: 4 colores con valores distintos para claro/oscuro; se aplican
//   como variables CSS (--accent, --accent-soft) en <html>. Disponible para
//   todos los temas (en no-classic se persiste por familia).
// - Densidad: 'cozy' | 'compact' (compacta = html font-size 13.5px + zoom).
// - Reduce-motion: clase .reduce-motion en <html> (además de la media query
//   prefers-reduced-motion del SO, ya cubierta en index.css).
// Persistencia canónica webapp-shell (<slug>-*): easyzfs-theme (familia),
// easyzfs-theme-mode (modo), easyzfs-accent, easyzfs-density,
// easyzfs-reduce-motion. Las claves legacy zfc-* (era zfsctl) migran una vez.
export type ThemeFamily = 'classic' | 'modern' | 'phosphor' | 'brutalist';
export type ThemeMode = 'light' | 'dark' | 'system';
export type AccentId = 'cyan' | 'steel' | 'emerald' | 'amber';
export type Density = 'cozy' | 'compact';

export const FAMILIES: readonly ThemeFamily[] = ['classic', 'modern', 'phosphor', 'brutalist'];
// Fondo de cada data-theme para el meta theme-color / anti-FOUC.
export const SKIN_BG: Record<string, string> = {
  'modern-dark': '#0d0e12',
  'modern-light': '#f4f5f8',
  phosphor: '#050810',
  brutalist: '#f2ede1',
};

const THEME_KEY = 'easyzfs-theme';       // familia
const THEMODE_KEY = 'easyzfs-theme-mode'; // modo (clave legacy reutilizada)
const ACCENT_KEY = 'easyzfs-accent';
const DENSITY_KEY = 'easyzfs-density';
const RM_KEY = 'easyzfs-reduce-motion';
// Legacy (pre-rebrand zfsctl): leer una vez y migrar.
const LEGACY: Record<string, string> = {
  [THEMODE_KEY]: 'zfc-theme',
  [ACCENT_KEY]: 'zfc-accent',
  [DENSITY_KEY]: 'zfc-density',
};

// getKey lee la clave canónica; si no existe pero hay legacy, la migra.
function getKey(key: string): string | null {
  const v = localStorage.getItem(key);
  if (v !== null) return v;
  const legacy = LEGACY[key];
  if (legacy) {
    const old = localStorage.getItem(legacy);
    if (old !== null) {
      localStorage.setItem(key, old);
      localStorage.removeItem(legacy);
      return old;
    }
  }
  return null;
}

// Migración un-time desde el modelo combinado anterior (easyzfs-theme-mode
// guardaba 'auto'|'light'|'dark'|'modern-dark'|'modern-light'|'phosphor'|
// 'brutalist'). Una vez migrado, easyzfs-theme = familia y
// easyzfs-theme-mode = modo limpio.
function migrateLegacy(): void {
  if (localStorage.getItem(THEME_KEY) !== null) return;
  const legacy = getKey(THEMODE_KEY);
  let family: ThemeFamily = 'modern';
  let mode: ThemeMode = 'system';
  if (legacy === 'light' || legacy === 'dark' || legacy === 'auto') {
    family = 'classic'; mode = legacy === 'auto' ? 'system' : legacy;
  } else if (legacy === 'modern-dark') { family = 'modern'; mode = 'dark'; }
  else if (legacy === 'modern-light') { family = 'modern'; mode = 'light'; }
  else if (legacy === 'phosphor' || legacy === 'brutalist') { family = legacy; mode = 'system'; }
  localStorage.setItem(THEME_KEY, family);
  localStorage.setItem(THEMODE_KEY, mode);
}

const subs = new Set<() => void>();

// [color, soft] por tema. emerald = verde original de la app.
export const ACCENTS: Record<AccentId, { light: [string, string]; dark: [string, string] }> = {
  cyan:    { light: ['#0e7c93', '#dff0f4'], dark: ['#4cc3d9', '#13292f'] },
  steel:   { light: ['#3a6ea5', '#e2ebf4'], dark: ['#7ba7d9', '#1b2634'] },
  emerald: { light: ['#2f7d5f', '#e3f0e9'], dark: ['#5cb893', '#1d2f27'] },
  amber:   { light: ['#a8741f', '#f6ecd9'], dark: ['#d9a84e', '#33291a'] },
};

// Acento por familia: cada tema ofrece su propia paleta con un acento por
// defecto (su identidad). Para 'classic' se reutilizan ACCENTS. El label es
// una clave i18n que Settings resuelve con t().
export interface AccentOption { v: string; light: [string, string]; dark: [string, string]; labelKey: string }
export const FAMILY_ACCENTS: Record<ThemeFamily, { default: string; options: AccentOption[] }> = {
  classic: {
    default: 'emerald',
    options: [
      { v: 'cyan', light: ACCENTS.cyan.light, dark: ACCENTS.cyan.dark, labelKey: 'acc_cyan' },
      { v: 'steel', light: ACCENTS.steel.light, dark: ACCENTS.steel.dark, labelKey: 'acc_steel' },
      { v: 'emerald', light: ACCENTS.emerald.light, dark: ACCENTS.emerald.dark, labelKey: 'acc_emerald' },
      { v: 'amber', light: ACCENTS.amber.light, dark: ACCENTS.amber.dark, labelKey: 'acc_amber' },
    ],
  },
  modern: {
    default: 'violet',
    options: [
      { v: 'violet', light: ['#6d3df5', 'rgba(109,61,245,.11)'], dark: ['#7c5cfc', 'rgba(124,92,252,.15)'], labelKey: 'acc_violet' },
      { v: 'emerald', light: ACCENTS.emerald.light, dark: ACCENTS.emerald.dark, labelKey: 'acc_emerald' },
      { v: 'cyan', light: ACCENTS.cyan.light, dark: ACCENTS.cyan.dark, labelKey: 'acc_cyan' },
      { v: 'amber', light: ACCENTS.amber.light, dark: ACCENTS.amber.dark, labelKey: 'acc_amber' },
    ],
  },
  phosphor: {
    default: 'green',
    options: [
      { v: 'green', light: ['#00f48e', 'rgba(0,244,142,.08)'], dark: ['#00f48e', 'rgba(0,244,142,.08)'], labelKey: 'acc_green' },
      { v: 'amber', light: ['#ffb000', 'rgba(255,176,0,.12)'], dark: ['#ffb000', 'rgba(255,176,0,.12)'], labelKey: 'acc_amber' },
      { v: 'cyan', light: ['#22d3ee', 'rgba(34,211,238,.12)'], dark: ['#22d3ee', 'rgba(34,211,238,.12)'], labelKey: 'acc_cyan' },
    ],
  },
  brutalist: {
    default: 'blue',
    options: [
      { v: 'blue', light: ['#2b59ff', 'rgba(43,89,255,.12)'], dark: ['#2b59ff', 'rgba(43,89,255,.12)'], labelKey: 'acc_blue' },
      { v: 'yellow', light: ['#ffd23f', 'rgba(255,210,63,.22)'], dark: ['#ffd23f', 'rgba(255,210,63,.22)'], labelKey: 'acc_yellow' },
      { v: 'green', light: ['#0a8f4d', 'rgba(10,143,77,.14)'], dark: ['#0a8f4d', 'rgba(10,143,77,.14)'], labelKey: 'acc_green' },
    ],
  },
};

// ---- Tema (familia) ----
// DEFAULT sin preferencia guardada: Modern + modo system (sigue al SO).
export function getFamily(): ThemeFamily {
  migrateLegacy();
  const f = localStorage.getItem(THEME_KEY) as ThemeFamily | null;
  return f && FAMILIES.includes(f) ? f : 'modern';
}

export function getMode(): ThemeMode {
  migrateLegacy();
  const m = localStorage.getItem(THEMODE_KEY);
  return m === 'light' || m === 'dark' ? m : 'system';
}

export function systemPrefersDark(): boolean {
  return window.matchMedia('(prefers-color-scheme: dark)').matches;
}

// ¿El modo efectivo es oscuro? (system = sigue al SO).
export function isDarkMode(mode: ThemeMode = getMode()): boolean {
  if (mode === 'system') return systemPrefersDark();
  return mode === 'dark';
}

// Luminosidad efectiva (para pares claro/oscuro de acentos, iconos sol/luna).
export function effectiveTheme(): 'light' | 'dark' {
  const family = getFamily();
  if (family === 'modern' || family === 'classic') return isDarkMode() ? 'dark' : 'light';
  return family === 'brutalist' ? 'light' : 'dark'; // phosphor fijo oscuro
}

export function applyTheme(): void {
  const family = getFamily();
  const mode = getMode();
  const el = document.documentElement;
  const meta = document.querySelector('meta[name="theme-color"]');
  let dt: string;
  if (family === 'classic') dt = isDarkMode(mode) ? 'dark' : '';
  else if (family === 'modern') dt = isDarkMode(mode) ? 'modern-dark' : 'modern-light';
  else dt = family; // phosphor | brutalist (fijos)
  el.dataset.theme = dt;
  if (meta) meta.setAttribute('content', SKIN_BG[dt] ?? (dt === 'dark' ? '#0e1210' : '#f6f6f3'));
  applyAccent(); // acento en todos los temas
}

export function setThemeFamily(f: ThemeFamily): void {
  localStorage.setItem(THEME_KEY, f);
  applyTheme();
  subs.forEach((fn) => fn());
}

export function setThemeMode(m: ThemeMode): void {
  localStorage.setItem(THEMODE_KEY, m);
  applyTheme();
  subs.forEach((fn) => fn());
}

// Botón del header: alterna claro/oscuro manualmente (override sobre system)
export function toggleTheme(): void {
  setThemeMode(isDarkMode() ? 'light' : 'dark');
}

export function onThemeChange(fn: () => void): () => void {
  subs.add(fn);
  return () => subs.delete(fn);
}

// Re-aplica el tema cuando cambia prefers-color-scheme en modo system.
export function startThemeWatcher(): void {
  const mq = window.matchMedia('(prefers-color-scheme: dark)');
  const onChange = () => {
    if (getMode() === 'system') {
      applyTheme();
      subs.forEach((fn) => fn());
    }
  };
  if (mq.addEventListener) mq.addEventListener('change', onChange);
  else mq.addListener(onChange); // Safari antiguo
}

// ---- Acento ----
export function getAccent(): AccentId {
  const v = getKey(ACCENT_KEY) as AccentId | null;
  return v && ACCENTS[v] ? v : 'emerald';
}

// Acento activo de una familia: override persistido (easyzfs-accent-<familia>)
// o, por defecto, el acento identidad de la familia.
export function getFamilyAccent(family: ThemeFamily): string | null {
  const v = localStorage.getItem('easyzfs-accent-' + family);
  return v && FAMILY_ACCENTS[family].options.some((o) => o.v === v) ? v : null;
}

export function currentFamilyAccentId(family: ThemeFamily): string {
  return getFamilyAccent(family) ?? FAMILY_ACCENTS[family].default;
}

// Devuelve el par [color, soft] del acento efectivo para la luminosidad actual.
function accentColors(): [string, string] {
  const family = getFamily();
  const eff = effectiveTheme();
  if (family === 'classic') return ACCENTS[getAccent()][eff];
  const opts = FAMILY_ACCENTS[family];
  const id = currentFamilyAccentId(family);
  const opt = opts.options.find((o) => o.v === id) ?? opts.options[0];
  return eff === 'dark' ? opt.dark : opt.light;
}

export function applyAccent(): void {
  const [accent, soft] = accentColors();
  const st = document.documentElement.style;
  st.setProperty('--accent', accent);
  st.setProperty('--accent-soft', soft);
  // --ok NO sigue al acento: es semántico (salud OK). Si ok=accent, con el
  // acento amber un estado sano y un aviso serían indistinguibles.
}

export function setAccent(id: string): void {
  const family = getFamily();
  if (family !== 'classic') localStorage.setItem('easyzfs-accent-' + family, id);
  else localStorage.setItem(ACCENT_KEY, id);
  applyAccent();
}

// ---- Densidad ----
export function getDensity(): Density {
  return getKey(DENSITY_KEY) === 'compact' ? 'compact' : 'cozy';
}

export function applyDensity(): void {
  const el = document.documentElement;
  if (getDensity() === 'compact') {
    el.dataset.density = 'compact';
    el.style.fontSize = '13.5px';
  } else {
    el.removeAttribute('data-density');
    el.style.fontSize = '';
  }
}

export function setDensity(d: Density): void {
  localStorage.setItem(DENSITY_KEY, d);
  applyDensity();
}

// ---- Reducir animaciones ----
export function getReduceMotion(): boolean {
  return getKey(RM_KEY) === '1';
}

export function applyReduceMotion(): void {
  document.documentElement.classList.toggle('reduce-motion', getReduceMotion());
}

export function setReduceMotion(on: boolean): void {
  if (on) localStorage.setItem(RM_KEY, '1');
  else localStorage.removeItem(RM_KEY);
  applyReduceMotion();
}
