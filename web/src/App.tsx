// Shell principal: sidebar colapsable (desktop) / bottom-nav (móvil), header
// con alertas y tema, barras de modo demo y de versión nueva, y vistas con
// code-splitting tolerante a despliegues (lazyRetry + ErrorBoundary).
import { Suspense, useEffect, useRef, useState } from 'react';
import type { ComponentType } from 'react';
import { AppProvider, useApp, alertTargetView } from './ui/store';
import type { ViewId } from './ui/store';
import { ModalProvider, ModalHost } from './components/ModalHost';
import { Logo, IconHome, IconPool, IconData, IconSnap, IconTask, IconDisk, IconGear, IconBell, IconMoon, IconSun, IconMonitor, IconChev, IconFoldLeft, IconFoldRight, IconUser } from './components/icons';
import { Spinner } from './components/ui';
import ErrorBoundary from './components/ErrorBoundary';
import PullToRefresh from './components/PullToRefresh';
import { getProvider } from './data';
import { subscribeEvents } from './data/events';
import { timeAgo } from './ui/format';
import { lazyRetry } from './ui/lazyRetry';
import { useUpdateAvailable } from './ui/updatecheck';
import { useReleaseCheck, getReleaseDismissed, dismissRelease } from './ui/releasecheck';
import type { Alert } from './data/types';
import Login from './views/Login';

// Code-splitting por vista (lazyRetry: si el chunk ya no existe tras un
// despliegue, recarga una vez en vez de quedarse en pantalla negra)
const Dashboard = lazyRetry(() => import('./views/Dashboard'));
const Pools = lazyRetry(() => import('./views/Pools'));
const Datasets = lazyRetry(() => import('./views/Datasets'));
const Snapshots = lazyRetry(() => import('./views/Snapshots'));
const Tasks = lazyRetry(() => import('./views/Tasks'));
const Disks = lazyRetry(() => import('./views/Disks'));
const Settings = lazyRetry(() => import('./views/Settings'));

// Nav principal (Ajustes NO va aquí: vive al pie del sidebar, patrón
// webapp-shell; en la bottom-nav móvil sí es un item más).
const NAV: { id: ViewId; icon: ComponentType<{ size?: number }> }[] = [
  { id: 'dash', icon: IconHome },
  { id: 'pools', icon: IconPool },
  { id: 'data', icon: IconData },
  { id: 'disks', icon: IconDisk },
  { id: 'tasks', icon: IconTask },
  { id: 'snaps', icon: IconSnap },
];

const COLLAPSED_KEY = 'easyzfs-sidebar-collapsed';

// Panel desplegable de alertas (campanita)
function AlertsPanel({ onClose }: { onClose: () => void }) {
  const { t, navigate } = useApp();
  const [alerts, setAlerts] = useState<Alert[]>([]);
  const ref = useRef<HTMLDivElement>(null);

  const load = () => getProvider().getAlerts().then(setAlerts).catch(() => {});
  useEffect(() => {
    load();
    // Nuevas alertas en tiempo real
    return subscribeEvents((ev) => { if (ev.type === 'alert.new') load(); });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    const onDoc = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) onClose();
    };
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose(); };
    document.addEventListener('mousedown', onDoc);
    document.addEventListener('keydown', onKey);
    return () => { document.removeEventListener('mousedown', onDoc); document.removeEventListener('keydown', onKey); };
  }, [onClose]);

  const ack = async (id: number) => {
    await getProvider().ackAlert(id).catch(() => {});
    setAlerts((cur) => cur.map((a) => (a.id === id ? { ...a, acked: true } : a)));
  };
  const ackAll = async () => {
    for (const a of alerts.filter((x) => !x.acked)) await getProvider().ackAlert(a.id).catch(() => {});
    load();
  };

  const pending = alerts.filter((a) => !a.acked);
  return (
    <div className="alertpanel" ref={ref} role="dialog" aria-label={t('al_title')}>
      <div className="rowitem" style={{ padding: '11px 16px' }}>
        <b style={{ fontSize: 14 }}>{t('al_title')}</b>
        {pending.length > 0 && (
          <button className="btn sm" style={{ marginLeft: 'auto' }} onClick={ackAll}>{t('al_ack_all')}</button>
        )}
      </div>
      {pending.length === 0 && <div className="empty">{t('al_none')}</div>}
      <div style={{ maxHeight: 320, overflowY: 'auto' }}>
        {pending.map((a) => {
          const tone = a.level === 'crit' ? 'err' : a.level === 'warn' ? 'warn' : 'info';
          const view = alertTargetView(a.target);
          const go = view ? () => { navigate(view); onClose(); } : undefined;
          return (
            <div className={`alert${view ? ' clickable' : ''}`} key={a.id}
              role={view ? 'link' : undefined} tabIndex={view ? 0 : undefined}
              title={view ? t('al_goto') : undefined}
              onClick={go}
              onKeyDown={go ? (e) => { if (e.key === 'Enter') go(); } : undefined}>
              <div className="ico" style={{ background: `var(--${tone}-soft)`, color: `var(--${tone})` }}>
                {a.level === 'crit' ? '!' : a.level === 'warn' ? '⚠' : 'i'}
              </div>
              <div style={{ flex: 1, minWidth: 0 }}>
                <b style={{ fontSize: 13 }}>{a.message}</b>
                <div style={{ fontSize: 12, color: 'var(--text2)', marginTop: 2 }}>
                  {a.source} · {timeAgo(a.ts, t)}
                </div>
              </div>
              <button className="btn sm" onClick={(e) => { e.stopPropagation(); ack(a.id); }}>{t('al_ack')}</button>
              {view && <span className="chev"><IconChev /></span>}
            </div>
          );
        })}
      </div>
    </div>
  );
}

function Shell() {
  const { t, route, navigate, demo, exitDemo, user, isAdmin, ready, themeMode, setTheme } = useApp();
  const [showAlerts, setShowAlerts] = useState(false);
  const [hasPending, setHasPending] = useState(false);
  const [collapsed, setCollapsed] = useState(() => localStorage.getItem(COLLAPSED_KEY) === '1');
  const updateAvailable = useUpdateAvailable(ready && !!user && !demo);
  // Ribbon "nueva release en GitHub" (check pasivo 1/semana, descartable por
  // versión, solo admin: actualizar es un acto de administración). El botón
  // "Actualizar" aplica vía el servidor (/api/update/apply); "Ver novedades"
  // enlaza a la release.
  const [version, setVersion] = useState('');
  const [relDismissed, setRelDismissed] = useState(getReleaseDismissed());
  const [applyingRel, setApplyingRel] = useState(false);
  const [updateToast, setUpdateToast] = useState('');
  const rel = useReleaseCheck(version || undefined, ready && !!user && !demo && isAdmin);
  useEffect(() => {
    if (!ready || !user || demo) return;
    getProvider().getVersion().then((v) => {
      setVersion(v.version);
      if (v.pendingUpdate?.to) {
        setUpdateToast(t('upd_toast_updated', { v: v.pendingUpdate.to }));
        setTimeout(() => setUpdateToast(''), 5000);
      }
    }).catch(() => {});
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ready, user, demo]);

  const applyRel = async () => {
    setApplyingRel(true);
    try { await getProvider().applyUpdate(); } catch { /* error silencioso: recarga en el reinicio */ }
    finally { setApplyingRel(false); }
  };

  const toggleCollapsed = () => {
    setCollapsed((c) => {
      const next = !c;
      if (next) localStorage.setItem(COLLAPSED_KEY, '1');
      else localStorage.removeItem(COLLAPSED_KEY);
      return next;
    });
  };

  // Punto indicador si hay alertas sin leer (espera a que el provider esté listo)
  useEffect(() => {
    if (!ready || !user) return;
    let alive = true;
    getProvider().getAlerts()
      .then((a) => alive && setHasPending(a.some((x) => !x.acked)))
      .catch(() => {});
    return subscribeEvents((ev) => {
      if (ev.type === 'alert.new') setHasPending(true);
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ready, user]);

  if (!ready) {
    return <div className="splash"><Logo size={42} /><div>{t('login_checking')}</div></div>;
  }
  if (!user) return <Login />;

  const active = route;

  const view = (() => {
    switch (active) {
      case 'dash': return <Dashboard />;
      case 'pools': return <Pools />;
      case 'data': return <Datasets />;
      case 'snaps': return <Snapshots />;
      case 'tasks': return <Tasks />;
      case 'disks': return <Disks />;
      case 'settings': return <Settings />;
    }
  })();

  return (
    <>
      <div className="app-shell">
        <aside className={`sidebar${collapsed ? ' collapsed' : ''}`}>
          <a className="brand" href="#/dash" aria-label={t('brand_home')} title={collapsed ? 'EasyZFS' : undefined}>
            <Logo size={30} />
            {!collapsed && (
              <div>
                <div style={{ fontWeight: 800, fontSize: 16, letterSpacing: '-.02em' }}>EasyZFS</div>
                <div style={{ fontSize: 11, color: 'var(--text2)' }}>{t('brand_sub')}</div>
              </div>
            )}
          </a>
          <nav>
            {NAV.map((n) => {
              const Ico = n.icon;
              return (
                <a key={n.id} href={`#/${n.id}`} className={n.id === active ? 'active' : ''}
                  aria-current={n.id === active ? 'page' : undefined}
                  title={collapsed ? t(n.id as never) : undefined}>
                  <Ico /><span className="nlbl">{t(n.id as never)}</span>
                </a>
              );
            })}
          </nav>
          <div className="sidefoot">
            <div className="sideactions">
              <a href="#/settings" className={active === 'settings' ? 'active' : ''}
                aria-current={active === 'settings' ? 'page' : undefined}
                title={collapsed ? t('settings' as never) : undefined}>
                <IconGear /><span className="nlbl">{t('settings' as never)}</span>
              </a>
              <button type="button" className="iconbtn fold" onClick={toggleCollapsed}
                aria-label={collapsed ? t('a11y_expand') : t('a11y_collapse')}
                title={collapsed ? t('a11y_expand') : t('a11y_collapse')}>
                {collapsed ? <IconFoldRight /> : <IconFoldLeft />}
              </button>
            </div>
          </div>
          {!collapsed && version && (
            <div style={{ fontSize: 10, color: 'var(--text3)', padding: '4px 12px 8px', textAlign: 'left' }}>
              v{version}
            </div>
          )}
        </aside>

        <main className="main">
          <PullToRefresh>
          {updateToast && (
            <div className="updatebar" role="status">
              <span>{updateToast}</span>
            </div>
          )}
          {updateAvailable && (
            <div className="updatebar" role="status">
              <span>{t('upd_banner')}</span>
              <button className="btn sm primary" style={{ marginLeft: 'auto' }} onClick={() => location.reload()}>
                {t('upd_btn')}
              </button>
            </div>
          )}
          {rel.kind === 'available' && relDismissed !== rel.version && (
            <div className="relbar" role="status">
              <div className="relbar-body">
                <span className="relbar-title">{t('upd_rel_banner', { v: rel.version })}</span>
                {rel.notes && <span className="relbar-notes">{rel.notes}</span>}
                <div className="relbar-actions">
                  <a href={rel.url} target="_blank" rel="noreferrer" className="btn sm">{t('ab_upd_new')}</a>
                  <button className="btn sm primary" disabled={applyingRel}
                    onClick={() => { void applyRel(); }}>
                    {applyingRel ? t('ab_updapp') : t('upd_rel_upd')}
                  </button>
                  <button className="btn sm"
                    onClick={() => { dismissRelease(rel.version); setRelDismissed(rel.version); }}>
                    {t('upd_rel_dismiss')}
                  </button>
                </div>
              </div>
            </div>
          )}
          {demo && (
            <div className="demobar" role="status">
              <span className="dot" />
              <span>{t('demobar')}</span>
              <button className="btn sm" style={{ marginLeft: 'auto' }} onClick={exitDemo}>
                {t('demobar_exit')}
              </button>
            </div>
          )}
          <header className="top">
            <div>
              <h1>{t(active as never)}</h1>
              <div className="sub">
                {active === 'dash' && user.display_name
                  ? t('dash_hello', { name: user.display_name })
                  : t(`sub_${active}` as never)}
              </div>
            </div>
            <div className="head-actions">
              <button className="iconbtn" title={t('a11y_alerts')} aria-label={t('a11y_alerts')}
                style={{ position: 'relative' }} onClick={() => { setShowAlerts((v) => !v); setHasPending(false); }}>
                <IconBell />
                {hasPending && <span className="ping" />}
              </button>
              <div className="theme-pill" role="radiogroup" aria-label={t('s_theme')}>
                {([['auto', IconMonitor], ['light', IconSun], ['dark', IconMoon]] as const).map(([m, Icon]) => (
                  <button key={m} type="button" role="radio" aria-checked={themeMode === m}
                    className={themeMode === m ? 'active' : ''}
                    title={t(`s_theme_${m}` as never)}
                    onClick={() => setTheme(m)}>
                    <Icon size={14} />
                    <span className="plbl">{t(`s_theme_${m}` as never)}</span>
                  </button>
                ))}
              </div>
              <button type="button" className={`topuser${active === 'settings' ? ' active' : ''}`}
                onClick={() => navigate('settings')}
                aria-label={user.display_name || user.user}
                title={t('settings' as never)}>
                <span className="avatar" aria-hidden="true">
                  {user.avatar ? <img src={getProvider().avatarUrl(user.avatar)} alt="" /> : <IconUser size={16} />}
                </span>
                <span className="tulbl">
                  <b>{user.display_name || user.user}</b>
                  <span>{isAdmin ? t('mu_r_admin') : t('mu_r_user')}</span>
                </span>
              </button>
              {showAlerts && <AlertsPanel onClose={() => setShowAlerts(false)} />}
            </div>
          </header>

          <ErrorBoundary>
            <Suspense fallback={<Spinner label={t('loading')} />}>
              {view}
            </Suspense>
          </ErrorBoundary>
          </PullToRefresh>
        </main>
      </div>

      <nav className="bottomnav" aria-label={t('a11y_mainnav')}>
        {[...NAV, { id: 'settings' as ViewId, icon: IconGear }].map((n) => {
          const Ico = n.icon;
          return (
            <button key={n.id} className={n.id === active ? 'active' : ''} onClick={() => navigate(n.id)}>
              <Ico size={22} />
              <span>{t(n.id as never)}</span>
            </button>
          );
        })}
      </nav>

      <ModalHost />
    </>
  );
}

export default function App() {
  return (
    <AppProvider>
      <ModalProvider>
        <Shell />
      </ModalProvider>
    </AppProvider>
  );
}
