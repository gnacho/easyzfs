// Estado global de la app: sesión, ruta, modo demo, idioma y tema.
// Router ligero basado en hash (#/vista).
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react';
import { flushSync } from 'react-dom';
import type { ReactNode } from 'react';
import type { Capabilities, SessionUser } from '../data/types';
import { getProvider, initProvider, enterDemoSession, exitDemoSession } from '../data';
import { syncPushSubscription } from '../data/push';
import { AUTH_EXPIRED_EVENT, connectSSE, disconnectSSE } from '../data/events';
import { ApiError } from '../data/types';
import { initLang, onLangChange, setLangMode, t as translate, getLangMode } from './i18n';
import type { LangMode, I18nKey } from './i18n';
import { applyTheme, applyDensity, applyReduceMotion, onThemeChange, startThemeWatcher, effectiveTheme, setThemeMode, getThemeMode } from './theme';
import type { ThemeMode } from './theme';

export type ViewId = 'dash' | 'pools' | 'data' | 'snaps' | 'tasks' | 'disks' | 'settings';
export const VIEWS: ViewId[] = ['dash', 'pools', 'data', 'snaps', 'tasks', 'disks', 'settings'];

function parseHash(): ViewId {
  const h = location.hash.replace(/^#\/?/, '') as ViewId;
  return VIEWS.includes(h) ? h : 'dash';
}

interface AppCtx {
  ready: boolean;            // provider inicializado
  demo: boolean;
  user: SessionUser | null;
  route: ViewId;
  navigate: (v: ViewId) => void;
  login: (u: string, p: string) => Promise<'ok' | { pending: string }>;
  login2FA: (pending: string, code: string) => Promise<void>;
  logout: () => Promise<void>;
  enterDemo: () => Promise<void>;
  exitDemo: () => void;
  // Sobrecargada: acepta claves del diccionario y también strings genéricos
  // (para helpers como timeAgo/describeSchedule que construyen claves dinámicas)
  t: ((k: I18nKey, vars?: Record<string, string | number>) => string) & ((k: string) => string);
  langMode: LangMode;
  setLang: (m: LangMode) => void;
  themeMode: ThemeMode;
  themeEff: 'light' | 'dark';
  setTheme: (m: ThemeMode) => void;
  isAdmin: boolean;
  // Capacidades de OpenZFS del host (feature-gating; null hasta conocerlas)
  caps: Capabilities | null;
  // Contador para forzar refresco de datos tras mutaciones
  refresh: () => void;
  dataVersion: number;
  // Re-lee /api/me (tras guardar perfil: nombre visible, email…)
  reloadUser: () => void;
}

const Ctx = createContext<AppCtx | null>(null);

export function AppProvider({ children }: { children: ReactNode }) {
  const [ready, setReady] = useState(false);
  const [demo, setDemo] = useState(false);
  const [user, setUser] = useState<SessionUser | null>(null);
  const [route, setRoute] = useState<ViewId>(parseHash());
  const routeRef = useRef<ViewId>(parseHash());
  const [langMode, setLangModeState] = useState<LangMode>(getLangMode());
  const [themeMode, setThemeModeState] = useState<ThemeMode>(getThemeMode());
  const [themeEff, setThemeEff] = useState<'light' | 'dark'>(effectiveTheme());
  const [dataVersion, setDataVersion] = useState(0);
  const [caps, setCaps] = useState<Capabilities | null>(null);

  // Capacidades OpenZFS (feature-gating; silencioso si falla). Se piden en el
  // arranque y se RE-piden al establecer sesión (login / sesión existente /
  // demo): sin sesión el backend responde 401 y los botones gateados
  // (Expandir, Reescribir…) quedarían ocultos hasta recargar la página.
  const fetchCaps = useCallback(() => {
    getProvider().getVersion().then((v) => setCaps(v.capabilities ?? null)).catch(() => {});
  }, []);

  // Arranque: idioma, tema, provider y sesión existente
  useEffect(() => {
    initLang();
    applyTheme(); // aplica también el acento guardado (depende del tema efectivo)
    applyDensity();
    applyReduceMotion();
    startThemeWatcher();
    const offLang = onLangChange(() => setLangModeState(getLangMode()));
    const offTheme = onThemeChange(() => {
      setThemeModeState(getThemeMode());
      setThemeEff(effectiveTheme());
    });
    (async () => {
      const { demo: d } = await initProvider();
      setDemo(d);
      fetchCaps();
      try {
        const me = await getProvider().me();
        setUser(me);
        // Sesión ya activa: reintenta las capabilities (la petición del
        // arranque pudo fallar con 401 si aún no había cookie de sesión)
        fetchCaps();
        // Idioma: la BD es la fuente de verdad (users.language); si difiere
        // del modo guardado en este navegador (caché), manda la BD.
        if (me.language && me.language !== getLangMode()) setLangMode(me.language);
        // Re-sincronización silenciosa de la suscripción push (idioma/origin
        // actuales; upsert por endpoint). Nunca en demo (sin push real).
        if (!d) void syncPushSubscription();
      } catch {
        setUser(null); // sin sesión → pantalla de login
      }
      setReady(true);
    })();
    return () => { offLang(); offTheme(); };
  }, [fetchCaps]);

  // Router por hash
  useEffect(() => {
    const onHash = () => {
      const next = parseHash();
      const prev = routeRef.current;
      routeRef.current = next;
      if (prev !== next) {
        try {
          document.documentElement.dataset.navDir =
            VIEWS.indexOf(next) > VIEWS.indexOf(prev) ? 'forward' : 'back';
        } catch {
          /* sin dataset */
        }
        const apply = () => {
          setRoute(next);
          window.scrollTo({ top: 0 });
        };
        if (typeof document.startViewTransition === 'function') {
          document.startViewTransition(() => flushSync(apply));
        } else {
          apply();
        }
      } else {
        setRoute(next);
      }
    };
    window.addEventListener('hashchange', onHash);
    return () => window.removeEventListener('hashchange', onHash);
  }, []);

  const navigate = useCallback((v: ViewId) => {
    location.hash = `/${v}`;
  }, []);

  const login = useCallback(async (u: string, p: string): Promise<'ok' | { pending: string }> => {
    const res = await getProvider().login(u, p);
    if (res.twofa_required) return { pending: res.pending };
    setUser(res);
    // Re-fetch de capabilities con la sesión recién creada (sin sesión el
    // backend da 401 y los botones gateados no aparecerían hasta recargar)
    fetchCaps();
    // El idioma de la BD manda sobre el caché local del navegador
    if (res.language && res.language !== getLangMode()) setLangMode(res.language);
    connectSSE(); // reabre el stream con la sesión recién creada
    void syncPushSubscription(); // re-sincroniza la suscripción push (silencioso)
    return 'ok';
  }, [fetchCaps]);

  // Segundo factor: completa el login iniciado (requiere el token 'pending'
  // que el backend devolvió en el paso 1 y un código TOTP válido).
  const login2FA = useCallback(async (pending: string, code: string) => {
    const s = await getProvider().login2FA(pending, code);
    setUser(s);
    fetchCaps();
    if (s.language && s.language !== getLangMode()) setLangMode(s.language);
    connectSSE();
    void syncPushSubscription();
  }, [fetchCaps]);

  const logout = useCallback(async () => {
    disconnectSSE(); // corta el stream antes de cerrar la sesión
    try { await getProvider().logout(); } catch { /* la sesión local se cierra igual */ }
    if (demo) { exitDemoSession(); setDemo(false); } // cerrar sesión en demo también sale del demo
    setUser(null);
    location.hash = '/dash';
  }, [demo]);

  // Sesión expirada (401 global en http.ts o 3 fallos 401 del SSE):
  // fuerza logout y vuelta al login. Solo aplica a sesiones reales (no demo)
  // y con sesión activa (evita bucles con el propio logout).
  const stateRef = useRef({ user, demo, logout });
  stateRef.current = { user, demo, logout };
  useEffect(() => {
    const onExpired = () => {
      const s = stateRef.current;
      if (s.demo || !s.user) return;
      void s.logout();
    };
    window.addEventListener(AUTH_EXPIRED_EVENT, onExpired);
    return () => window.removeEventListener(AUTH_EXPIRED_EVENT, onExpired);
  }, []);

  // Sesión demo local (provider mock, usuario "demo"); no llama al backend
  const enterDemo = useCallback(async () => {
    await enterDemoSession();
    setDemo(true);
    try { setUser(await getProvider().me()); } catch { setUser(null); }
    fetchCaps(); // capabilities del provider mock (la petición del arranque fue contra el HTTP y falló)
    setDataVersion((v) => v + 1);
  }, [fetchCaps]);

  // Cierra la sesión demo y vuelve a la pantalla de login (provider HTTP)
  const exitDemo = useCallback(() => {
    exitDemoSession();
    setDemo(false);
    setUser(null);
    location.hash = '/dash';
    setDataVersion((v) => v + 1);
  }, []);

  const t = useCallback(
    ((k: I18nKey, vars?: Record<string, string | number>) => translate(k, vars)) as AppCtx['t'],
    // langMode fuerza nueva identidad al cambiar idioma
    [langMode],
  );

  const setLang = useCallback((m: LangMode) => {
    setLangMode(m);
    // Espejo en BD (fuente de verdad); silencioso si no hay sesión real
    const s = stateRef.current;
    if (s.user && !s.demo) getProvider().setMyLanguage(m).catch(() => {});
  }, []);
  const setTheme = useCallback((m: ThemeMode) => setThemeMode(m), []);
  const refresh = useCallback(() => setDataVersion((v) => v + 1), []);
  const reloadUser = useCallback(() => {
    getProvider().me().then(setUser).catch(() => {});
  }, []);

  const value = useMemo<AppCtx>(() => ({
    ready, demo, user, route, navigate, login, login2FA, logout, enterDemo, exitDemo,
    t, langMode, setLang, themeMode, themeEff, setTheme,
    isAdmin: user?.role === 'admin',
    caps,
    refresh, dataVersion, reloadUser,
  }), [ready, demo, user, route, navigate, login, login2FA, logout, enterDemo, exitDemo, t, langMode, setLang, themeMode, themeEff, setTheme, caps, refresh, dataVersion, reloadUser]);

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useApp(): AppCtx {
  const ctx = useContext(Ctx);
  if (!ctx) throw new Error('useApp fuera de AppProvider');
  return ctx;
}

// Helper: vista destino de una alerta según su campo target del backend
// ("disks:nvme1n1" → Discos, "pools:tank" → Pools, "tasks" → Tareas, "settings" → Ajustes)
export function alertTargetView(target?: string): ViewId | null {
  if (!target) return null;
  const kind = target.split(':')[0] as ViewId;
  return VIEWS.includes(kind) ? kind : null;
}

// Helper: traduce errores de API a mensajes legibles
export function errorMessage(e: unknown, t: AppCtx['t']): string {
  if (e instanceof ApiError) {
    if (e.code === 'confirm_required') return e.message;
    if (e.status === 401) return t('login_error');
    if (e.status === 403) return t('no_permission');
    return e.message;
  }
  return (e as Error)?.message ?? String(e);
}
