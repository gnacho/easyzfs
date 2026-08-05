// Vista Ajustes (layout 3-Ago-2026, spec del usuario):
//   Fila 1: Datos y umbrales (50%, admin) + Apariencia (50%) — misma altura
//   Fila 2: Mi sesión + Alertas push (preset todas/importante/ninguna)
//   Zona admin fila 1: Modo demo | Copia de seguridad | Notificaciones
//   Zona admin fila 2: Usuarios | Actividad (misma altura, "ver más")
//   Acerca de: ancho completo (4 tiles + fila PWA/check-updates admin + sistema)
// "Comprobar actualizaciones": botón manual (admin) + check pasivo semanal
// (ver ui/releasecheck.ts); el ribbon superior lo muestra App.tsx.
import { useEffect, useRef, useState } from 'react';
import { getProvider } from '../data';
import { errorMessage, useApp } from '../ui/store';
import { fmtBytes, fmtDuration, timeAgo } from '../ui/format';
import { Seg, Select, Spinner, Switch, Badge } from '../components/ui';
import { Logo, IconCode, IconList, IconHeart, IconShield, IconDownload, IconCheck, IconSun, IconMoon, IconMonitor, IconUpload, IconCamera, IconTrash, IconLock, IconBell, IconChev, IconData, IconUser } from '../components/icons';
import { useModal, ModalBox } from '../components/Modal';
import { AvatarCropDialog } from '../components/AvatarCropDialog';
import { usePush } from '../data/push';
import { checkReleaseNow, useReleaseCheck } from '../ui/releasecheck';
import {
  ACCENTS, getAccent, setAccent, getDensity, setDensity,
  getReduceMotion, setReduceMotion,
} from '../ui/theme';
import type { AccentId, Density, ThemeMode } from '../ui/theme';
import type { I18nKey } from '../ui/i18n';
import type {
  ActivityItem, BackupStatus, Lang, PushAlertTipo, PushPreference,
  Settings as SettingsData,
} from '../data/types';

// Evento beforeinstallprompt (PWA), no tipado en lib.dom
interface BeforeInstallPromptEvent extends Event {
  prompt: () => Promise<void>;
}

function isIOS(): boolean {
  return /iPad|iPhone|iPod/.test(navigator.userAgent)
    || (navigator.platform === 'MacIntel' && navigator.maxTouchPoints > 1);
}

function isStandalone(): boolean {
  return window.matchMedia('(display-mode: standalone)').matches
    || (navigator as { standalone?: boolean }).standalone === true;
}

// Mini-preview de un tema estilo NetPulse: mini-UI (barra superior, sidebar,
// barra de acento, bloque) pintada con las VARIABLES CSS REALES scopeando
// data-theme en el propio contenedor — cero valores duplicados.
function ThemePreview({ mode }: { mode: ThemeMode }) {
  const half = (
    <div className="tpv-half">
      <div className="tpv-top" />
      <div className="tpv-row">
        <div className="tpv-side" />
        <div className="tpv-main">
          <div className="tpv-accent" />
          <div className="tpv-block" />
        </div>
      </div>
    </div>
  );
  return (
    <div className="tpv" aria-hidden="true">
      {mode !== 'dark' && <div className="tpv-scope" data-theme="light">{half}</div>}
      {mode !== 'light' && <div className="tpv-scope" data-theme="dark">{half}</div>}
    </div>
  );
}

// Etiqueta traducida de cada tipo de alerta (exhaustivo sobre PushAlertTipo).
const TIPO_LABEL: Record<PushAlertTipo, I18nKey> = {
  pool_capacity: 's_pt_pool_capacity',
  pool_status: 's_pt_pool_status',
  scrub_errors: 's_pt_scrub_errors',
  disk_temp: 's_pt_disk_temp',
  smart_status: 's_pt_smart_status',
};

// Opciones de hora 00–23 para el horario silencioso.
const HORAS = Array.from({ length: 24 }, (_, h) => ({
  v: String(h), label: `${String(h).padStart(2, '0')}:00`,
}));

// Repo público de la app (tiles de Acerca de).
const REPO_URL = 'https://github.com/gnacho/easyzfs';

// Icono de estado de releases en la cabecera de la zona admin: la
// comprobación es pasiva (1/día, ver ui/releasecheck.ts); aquí solo se
// refleja el resultado. Sin botón "comprobar" (decisión del usuario).
function ReleaseIcon({ version }: { version: string | undefined }) {
  const { t } = useApp();
  const rel = useReleaseCheck(version, true);
  if (rel.kind === 'available') {
    return (
      <a href={rel.url} target="_blank" rel="noreferrer" className="relico new"
        title={t('ab_newver', { v: rel.version })} aria-label={t('ab_newver', { v: rel.version })}>
        <IconUpload size={14} /><span>v{rel.version}</span>
      </a>
    );
  }
  if (rel.kind === 'uptodate') {
    return (
      <span className="relico ok" title={t('ab_uptodate', { v: version ?? '' })} role="status">
        <IconCheck size={11} />
      </span>
    );
  }
  return null;
}

// Fila "Comprobar actualizaciones" (Acerca de, solo admin): botón manual que
// fuerza la consulta a GitHub al momento (además del check pasivo semanal de
// releasecheck.ts). Muestra el resultado inline: al día, nueva versión
// (enlace a las novedades) o error con reintento.
function UpdateCheckRow({ version }: { version: string | undefined }) {
  const { t } = useApp();
  const rel = useReleaseCheck(version, true);
  const [checking, setChecking] = useState(false);
  const [failed, setFailed] = useState(false);

  const check = async () => {
    setChecking(true); setFailed(false);
    try {
      await checkReleaseNow(version);
    } catch {
      setFailed(true);
    }
    setChecking(false);
  };

  return (
    <div className="installstrip">
      <span className="t-ico" style={{ width: 30, height: 30, borderRadius: 8, background: 'var(--accent-soft)', color: 'var(--accent)', display: 'grid', placeItems: 'center', flexShrink: 0 }}>
        <IconUpload size={16} />
      </span>
      <div className="grow">
        <b>{t('ab_checkupd')}</b>
        <div className="d">
          {checking ? t('ab_checking')
            : failed ? t('ab_upderr')
            : rel.kind === 'available' ? t('ab_newver', { v: rel.version })
            : rel.kind === 'uptodate' ? t('ab_uptodate', { v: version ?? '' })
            : t('s_about_d')}
        </div>
      </div>
      {rel.kind === 'available' && !failed && (
        <a className="btn sm" href={rel.url} target="_blank" rel="noreferrer">{t('ab_viewrel')}</a>
      )}
      <button className="btn sm primary" disabled={checking} onClick={() => { void check(); }}>
        {checking ? t('ab_checking') : failed ? t('ab_retry') : t('ab_checkupd')}
      </button>
    </div>
  );
}

// Tarjeta "Copia de seguridad" (zona admin): patrón ajuste-proceso — estado
// (último + próximo), configuración (switch + frecuencia + retención) y
// acciones (Forzar ahora / Exportar / Importar) en la misma tarjeta.
function BackupCard({ settings, onSave }: {
  settings: SettingsData;
  onSave: (patch: Partial<SettingsData>) => Promise<void>;
}) {
  const { t } = useApp();
  const [st, setSt] = useState<BackupStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState('');
  const [err, setErr] = useState('');
  const [importFile, setImportFile] = useState<File | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);

  const load = () => getProvider().getBackupStatus().then(setSt).catch(() => {});
  useEffect(() => {
    let alive = true;
    getProvider().getBackupStatus().then((s) => alive && setSt(s)).catch(() => {});
    return () => { alive = false; };
  }, []);

  const FREQS = [1, 6, 12, 24, 48, 72].map((h) => ({ v: String(h), label: t('bk_freq_h', { h }) }));

  const runNow = async () => {
    setBusy(true); setMsg(''); setErr('');
    try {
      const f = await getProvider().runBackup();
      setMsg(t('bk_done', { f: f.file }));
      load();
    } catch (e) { setErr(errorMessage(e, t)); }
    setBusy(false);
  };

  const doImport = async () => {
    if (!importFile) return;
    setBusy(true); setErr('');
    try {
      await getProvider().importBackup(importFile);
      // El server hace swap y reinicia el proceso; recargamos al cabo de unos
      // segundos para volver al login con la BD importada.
      setMsg(t('bk_import_restarting'));
      setTimeout(() => location.reload(), 4000);
    } catch (e) {
      setErr(errorMessage(e, t));
      setBusy(false);
      setImportFile(null);
    }
  };

  return (
    <div className="card pad admin-card">
      <h3 className="cardtitle">{t('bk_title')}</h3>

      {/* Estado */}
      <div className="kv"><span>{t('bk_last')}</span>
        <span>{st?.last
          ? <>{st.last.file} · {timeAgo(st.last.ts, t)} · {fmtBytes(st.last.bytes)}</>
          : t('bk_never')}</span>
      </div>
      {st?.next_run && (
        <div className="kv"><span>{t('bk_next')}</span><span>{timeAgo(st.next_run, t)}</span></div>
      )}
      {st?.running && <div className="kv"><span></span><span className="muted">{t('bk_running')}</span></div>}

      {/* Configuración */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 9, marginTop: 14 }}>
        <span style={{ flex: 1, fontSize: 13.5, fontWeight: 600 }}>{t('bk_enable')}</span>
        <Switch checked={settings.backup_enabled} ariaLabel={t('bk_enable')}
          onChange={(v) => { void onSave({ backup_enabled: v }); }} />
      </div>
      <div style={settings.backup_enabled ? undefined : { opacity: 0.4, pointerEvents: 'none' }}>
        <div style={{ display: 'flex', gap: 12, alignItems: 'flex-end', flexWrap: 'wrap' }}>
          <div style={{ minWidth: 150 }}>
            <label>{t('bk_freq')}</label>
            <Select value={String(settings.backup_freq_hours)} ariaLabel={t('bk_freq')}
              options={FREQS}
              onChange={(v) => { void onSave({ backup_freq_hours: +v }); }} />
          </div>
          <div style={{ width: 130 }}>
            <label htmlFor="bk-ret">{t('bk_retention')}</label>
            <input id="bk-ret" type="number" min={1} max={30} value={settings.backup_retention_days}
              onChange={(e) => { void onSave({ backup_retention_days: +e.target.value }); }} />
          </div>
        </div>
      </div>

      {/* Acciones */}
      <div className="m-actions" style={{ justifyContent: 'flex-start' }}>
        <button className="btn" disabled={busy} onClick={() => { void runNow(); }}>{t('bk_run')}</button>
        <a className="btn" href="/api/backup/download" download>{t('bk_export')}</a>
        <button className="btn" disabled={busy} onClick={() => fileRef.current?.click()}>{t('bk_import')}</button>
        <input ref={fileRef} type="file" accept=".db,application/octet-stream" style={{ display: 'none' }}
          onChange={(e) => setImportFile(e.target.files?.[0] ?? null)} />
      </div>

      {/* Importación: confirmación destructiva inline de dos pasos */}
      {importFile && (
        <div className="rebuildbar" style={{ marginTop: 12 }}>
          <span style={{ flex: 1, minWidth: 220 }}>{t('bk_import_q', { f: importFile.name })}</span>
          <button className="btn sm danger" disabled={busy} onClick={() => { void doImport(); }}>
            {t('bk_import_btn')}
          </button>
          <button className="btn sm" disabled={busy} onClick={() => setImportFile(null)}>{t('cancel')}</button>
        </div>
      )}
      {msg && <p style={{ fontSize: 13, color: 'var(--ok)', fontWeight: 600, marginTop: 10 }} role="status">{msg}</p>}
      {err && <p className="form-err" role="alert" style={{ marginTop: 10 }}>{err}</p>}
    </div>
  );
}

// Tarjeta "Actividad" (zona admin): registro de auditoría en la misma fila
// que Usuarios, misma altura. Muestra las primeras entradas y "Ver más"
// (GET /api/activity con límite mayor) si hay más.
function ActivityCard() {
  const { t } = useApp();
  const [items, setItems] = useState<ActivityItem[] | null>(null);
  const [expanded, setExpanded] = useState(false);
  const [loading, setLoading] = useState(false);
  const [err, setErr] = useState('');

  useEffect(() => {
    let alive = true;
    getProvider().getActivity(30)
      .then((a) => alive && setItems(a)).catch(() => {});
    return () => { alive = false; };
  }, []);

  const show = items ?? [];
  const visible = expanded ? show.slice(0, 30) : show.slice(0, 8);

  const loadMore = () => {
    setExpanded(true);
    if (show.length >= 30) {
      setLoading(true); setErr('');
      getProvider().getActivity(200)
        .then((a) => { setItems(a); setLoading(false); })
        .catch((e) => { setErr(errorMessage(e, t)); setLoading(false); });
    }
  };

  return (
    <div className="card pad admin-card">
      <h3 className="cardtitle">{t('s_activity')}</h3>
      <p className="muted">{t('s_activity_d')}</p>
      {items === null && <p className="muted">{t('loading')}</p>}
      {items && items.length === 0 && <div className="empty">{t('empty')}</div>}
      <div>
        {visible.map((a, i) => (
          <div className="rowitem" key={i}>
            <div className="grow">
              <div className="t1" style={{ fontSize: 13.5 }}>{a.text}</div>
              <div className="t2">{a.detail}</div>
            </div>
            <span style={{ fontSize: 11.5, color: 'var(--text2)', whiteSpace: 'nowrap' }}>{timeAgo(a.ts, t)}</span>
          </div>
        ))}
      </div>
      {show.length > 8 && (
        <div style={{ marginTop: 10 }}>
          <button className="btn sm" disabled={loading}
            onClick={() => (expanded ? setExpanded(false) : loadMore())}>
            {expanded ? t('s_activity_less') : (loading ? t('loading') : t('s_activity_more'))}
          </button>
        </div>
      )}
      {err && <p className="form-err" role="alert" style={{ marginTop: 8 }}>{err}</p>}
    </div>
  );
}

// Subsección de configuración de alertas (visible con push activado): preset
// rápido (Todas / Solo importantes / Ninguna) + switches por tipo + horario
// silencioso. Las críticas siempre llegan (texto visible).
//
// Semántica del preset (las crit atraviesan las preferencias en el backend):
//   - Todas: todos los tipos activados.
//   - Solo importantes: solo integridad de datos/salud de hardware
//     (pool_status, scrub_errors, smart_status); se desactivan los avisos
//     de capacidad y temperatura.
//   - Ninguna: todo desactivado — solo llegan las críticas.
const IMPORTANTES: PushAlertTipo[] = ['pool_status', 'scrub_errors', 'smart_status'];
type PushLevel = 'all' | 'important' | 'none';

function deriveLevel(prefs: PushPreference[]): PushLevel | 'custom' {
  const on = new Set(prefs.filter((p) => p.enabled).map((p) => p.tipo));
  if (on.size === prefs.length) return 'all';
  if (on.size === 0) return 'none';
  if (on.size === IMPORTANTES.length && IMPORTANTES.every((k) => on.has(k))) return 'important';
  return 'custom';
}

function PushPrefs() {
  const { t } = useApp();
  const [prefs, setPrefs] = useState<PushPreference[] | null>(null);
  // Estado local del horario silencioso (start/end siempre números; al
  // desactivar se conservan para reactivar con los mismos valores).
  const [quiet, setQuiet] = useState<{ enabled: boolean; start: number; end: number } | null>(null);
  const [err, setErr] = useState('');

  useEffect(() => {
    let alive = true;
    getProvider().getPushPreferences()
      .then((r) => alive && setPrefs(r.preferences)).catch(() => {});
    getProvider().getPushQuietHours()
      .then((q) => alive && setQuiet({ enabled: q.enabled, start: q.start ?? 22, end: q.end ?? 8 }))
      .catch(() => {});
    return () => { alive = false; };
  }, []);

  const toggleTipo = (tipo: PushAlertTipo, enabled: boolean) => {
    setPrefs((cur) => cur?.map((p) => (p.tipo === tipo ? { ...p, enabled } : p)) ?? cur);
    setErr('');
    getProvider().putPushPreference(tipo, enabled).catch((e) => setErr(errorMessage(e, t)));
  };

  // Aplica un preset: un PUT por cada tipo que cambie (optimista).
  const applyLevel = (level: PushLevel) => {
    if (!prefs) return;
    setErr('');
    const next = prefs.map((p) => ({
      ...p,
      enabled: level === 'all' ? true
        : level === 'none' ? false
        : IMPORTANTES.includes(p.tipo),
    }));
    setPrefs(next);
    next.forEach((p, i) => {
      if (p.enabled !== prefs[i].enabled) {
        getProvider().putPushPreference(p.tipo, p.enabled).catch((e) => setErr(errorMessage(e, t)));
      }
    });
  };

  const saveQuiet = (next: { enabled: boolean; start: number; end: number }) => {
    setQuiet(next);
    setErr('');
    if (next.enabled && next.start === next.end) {
      setErr(t('s_quiet_err')); // el servidor también lo valida (400 invalid_hours)
      return;
    }
    getProvider().putPushQuietHours(next).catch((e) => setErr(errorMessage(e, t)));
  };

  if (!prefs && !quiet) return null;
  const level = prefs ? deriveLevel(prefs) : 'all';
  return (
    <div style={{ marginTop: 16 }}>
      {prefs && (
        <>
          {/* Preset rápido: todas / solo importantes / ninguna */}
          <label>{t('s_push_level')}</label>
          <p className="muted" style={{ marginTop: 0 }}>{t('s_push_level_d')}</p>
          <Seg value={(level === 'custom' ? '' : level) as PushLevel} ariaLabel={t('s_push_level')}
            onChange={(v) => applyLevel(v)}
            options={[
              { v: 'all', label: t('s_push_all') },
              { v: 'important', label: t('s_push_important') },
              { v: 'none', label: t('s_push_none') },
            ]} />
          <label style={{ marginTop: 14 }}>{t('s_push_types')}</label>
          <p className="muted" style={{ marginTop: 0 }}>{t('s_push_types_d')}</p>
          {prefs.map((p) => (
            <div key={p.tipo} style={{ display: 'flex', alignItems: 'center', gap: 9, padding: '6px 0' }}>
              <span style={{ flex: 1, fontSize: 13.5 }}>{t(TIPO_LABEL[p.tipo])}</span>
              <Switch checked={p.enabled} ariaLabel={t(TIPO_LABEL[p.tipo])}
                onChange={(v) => toggleTipo(p.tipo, v)} />
            </div>
          ))}
        </>
      )}
      {quiet && (
        <>
          <label style={{ marginTop: 12 }}>{t('s_quiet')}</label>
          <p className="muted" style={{ marginTop: 0 }}>{t('s_quiet_d')}</p>
          <div style={{ display: 'flex', alignItems: 'center', gap: 9, padding: '6px 0' }}>
            <span style={{ flex: 1, fontSize: 13.5 }}>{t('s_quiet_enable')}</span>
            <Switch checked={quiet.enabled} ariaLabel={t('s_quiet_enable')}
              onChange={(v) => saveQuiet({ ...quiet, enabled: v })} />
          </div>
          {quiet.enabled && (
            <div style={{ display: 'flex', gap: 10, alignItems: 'center', flexWrap: 'wrap', marginTop: 4 }}>
              <span className="muted">{t('s_quiet_from')}</span>
              <Select value={String(quiet.start)} ariaLabel={t('s_quiet_from')} options={HORAS}
                onChange={(v) => saveQuiet({ ...quiet, start: +v })} />
              <span className="muted">{t('s_quiet_to')}</span>
              <Select value={String(quiet.end)} ariaLabel={t('s_quiet_to')} options={HORAS}
                onChange={(v) => saveQuiet({ ...quiet, end: +v })} />
            </div>
          )}
        </>
      )}
      {err && <p className="form-err" role="alert" style={{ marginTop: 8 }}>{err}</p>}
    </div>
  );
}

// Contenido de la configuración de notificaciones push (vive en el modal
// "notifs" que abre la tarjeta Mi perfil; canon ajustes.md 5-Ago-2026).
// Tarjeta explicativa ANTES del prompt nativo (qué alertas llegarán y que
// requiere la app cerrada para notar el efecto). El prompt nativo solo sale
// del gesto del botón "Activar alertas" (subscribe). Estados: activadas (con
// desactivar), denied (instrucciones, NO re-pedir), unsupported, iOS sin PWA
// (guía de instalación), demo y sin claves VAPID (nota informativa sin botón).
function PushPanel() {
  const { t } = useApp();
  const { state, error, subscribe, unsubscribe } = usePush();

  return (
    <div>
      <p className="muted">{t('s_push_d')}</p>

        {state === 'unknown' && (
          <p className="muted">{t('loading')}</p>
        )}

        {(state === 'idle' || state === 'subscribing' || state === 'error') && (
          <div style={{ display: 'flex', gap: 9, alignItems: 'center', flexWrap: 'wrap', marginTop: 10 }}>
            <button className="btn primary" disabled={state === 'subscribing'}
              onClick={() => { void subscribe(); }}>
              {state === 'subscribing' ? t('s_push_enabling') : t('s_push_enable')}
            </button>
          </div>
        )}
        {state === 'error' && (
          <p className="form-err" role="alert">{t('s_push_error')}{error ? ` (${error})` : ''}</p>
        )}

        {state === 'subscribed' && (
          <>
            <div style={{ display: 'flex', gap: 9, alignItems: 'center', flexWrap: 'wrap', marginTop: 10 }}>
              <Badge tone="ok" dot={false}>{t('s_push_on')}</Badge>
              <span className="muted">{t('s_push_on_d')}</span>
              <button className="btn sm" onClick={() => { void unsubscribe(); }}>{t('s_push_disable')}</button>
            </div>
            <PushPrefs />
          </>
        )}

        {state === 'denied' && (
          <p style={{ fontSize: 12.5, color: 'var(--warn)', marginTop: 10 }} role="alert">{t('s_push_denied')}</p>
        )}
        {state === 'unsupported' && (
          <p className="muted" style={{ marginTop: 10 }}>{t('s_push_unsupported')}</p>
        )}
        {state === 'needs-ios-install' && (
          <p className="muted" style={{ marginTop: 10 }}>{t('s_push_ios')}</p>
        )}
        {state === 'demo' && (
          <p className="muted" style={{ marginTop: 10 }}>{t('s_push_demo')}</p>
        )}
        {state === 'not-configured' && (
          <p className="muted" style={{ marginTop: 10 }}>{t('s_push_notcfg')}</p>
        )}
    </div>
  );
}

// Tarjeta "Mi perfil" HORIZONTAL (canon ajustes.md 5-Ago-2026): cabecera con
// avatar (subir foto con recorte cuadrado 1:1, EXIF y re-codificación webp)
// + nombre/email visibles; grid de campos: nombre visible, email opcional,
// idioma y fila de notificaciones push (abre modal con PushPanel); cambio de
// contraseña INLINE (estilo NetPulse) + cerrar sesión.
function ProfileCard() {
  const { t, user, langMode, setLang, logout, reloadUser } = useApp();
  const push = usePush();
  const [name, setName] = useState(user?.display_name ?? '');
  const [email, setEmail] = useState(user?.email ?? '');
  const [msg, setMsg] = useState('');
  const [err, setErr] = useState('');
  const [busy, setBusy] = useState(false);
  const [showPass, setShowPass] = useState(false);
  const [cur, setCur] = useState('');
  const [p1, setP1] = useState('');
  const [p2, setP2] = useState('');
  const [cropFile, setCropFile] = useState<File | null>(null);
  const [showNotifs, setShowNotifs] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);

  const avatarUrl = user?.avatar ? getProvider().avatarUrl(user.avatar) : '';
  const initial = (user?.display_name || user?.user || '?').trim().charAt(0).toUpperCase();

  const save = async () => {
    setBusy(true); setMsg(''); setErr('');
    try {
      await getProvider().updateMyProfile(name.trim(), email.trim());
      reloadUser(); // actualiza sidebar/saludo con el nombre nuevo
      setMsg(t('saved_ok'));
    } catch (e) { setErr(errorMessage(e, t)); }
    setBusy(false);
  };

  const changePass = async (e: React.FormEvent) => {
    e.preventDefault();
    if (p1 !== p2) { setErr(t('s_mypass_mismatch')); return; }
    setBusy(true); setMsg(''); setErr('');
    try {
      await getProvider().setMyPassword(cur, p1);
      setShowPass(false); setCur(''); setP1(''); setP2('');
      setMsg(t('saved_ok'));
    } catch (ex) { setErr(errorMessage(ex, t)); }
    setBusy(false);
  };

  const uploadAvatar = async (blob: Blob) => {
    setMsg(''); setErr('');
    try {
      await getProvider().setMyAvatar(blob);
      reloadUser(); // el sidebar y la tarjeta muestran la foto nueva
      setMsg(t('saved_ok'));
    } catch (e) { setErr(errorMessage(e, t)); throw e; }
  };

  const removeAvatar = async () => {
    setMsg(''); setErr('');
    try {
      await getProvider().deleteMyAvatar();
      reloadUser();
      setMsg(t('s_avatar_removed'));
    } catch (e) { setErr(errorMessage(e, t)); }
  };

  return (
    <div className="card pad">
      <h3 className="cardtitle">{t('s_profile')}</h3>

      {/* Cabecera horizontal: avatar editable + nombre/email reales */}
      <div className="profile-head">
        <button type="button" className="avatar-lg" onClick={() => fileRef.current?.click()}
          aria-label={t('s_avatar_change')} title={t('s_avatar_change')}>
          {avatarUrl ? <img src={avatarUrl} alt="" /> : <span aria-hidden="true">{initial}</span>}
          <span className="cam" aria-hidden="true"><IconCamera size={18} /></span>
        </button>
        <input ref={fileRef} type="file" accept="image/*" hidden
          onChange={(e) => { const f = e.target.files?.[0]; if (f) setCropFile(f); e.target.value = ''; }} />
        <div className="ph-lbl">
          <b>{user?.display_name || user?.user}</b>
          <span>@{user?.user}</span>
        </div>
        {avatarUrl && (
          <button type="button" className="iconbtn" onClick={() => { void removeAvatar(); }}
            aria-label={t('s_avatar_remove')} title={t('s_avatar_remove')}><IconTrash size={15} /></button>
        )}
      </div>

      {/* Grid de campos: nombre | email / idioma | notificaciones */}
      <div className="profile-grid">
        <div>
          <label htmlFor="pf-name">{t('s_displayname')}</label>
          <input id="pf-name" value={name} maxLength={64} placeholder={user?.user}
            autoComplete="nickname" onChange={(e) => setName(e.target.value)} />
          <p className="muted" style={{ marginTop: 4 }}>{t('s_displayname_d')}</p>
        </div>
        <div>
          <label htmlFor="pf-email">{t('s_email')}</label>
          <input id="pf-email" type="email" value={email} maxLength={254}
            autoComplete="email" onChange={(e) => setEmail(e.target.value)} />
          <p className="muted" style={{ marginTop: 4 }}>{t('s_email_d')}</p>
        </div>
        <div>
          <label>{t('s_lang')}</label>
          <Select value={langMode} onChange={setLang} ariaLabel={t('s_lang')}
            options={[{ v: 'auto', label: t('s_lang_auto') }, { v: 'es', label: '🇪🇸 Español' }, { v: 'en', label: '🇬🇧 English' }]} />
        </div>
        <div>
          <label>{t('s_notifs')}</label>
          <button type="button" className="notif-row" onClick={() => setShowNotifs(true)}>
            <IconBell size={16} aria-hidden="true" />
            <span className="nr-lbl">{t('s_push')}</span>
            <Badge tone={push.state === 'subscribed' ? 'ok' : 'info'} dot={false}>
              {push.state === 'subscribed' ? t('s_push_on') : t('s_push_off')}
            </Badge>
            <IconChev size={14} aria-hidden="true" />
          </button>
        </div>
      </div>

      <div className="m-actions" style={{ justifyContent: 'flex-start' }}>
        <button className="btn primary" disabled={busy} onClick={() => { void save(); }}>{t('save')}</button>
        <button className="btn" onClick={() => setShowPass((v) => !v)}><IconLock size={14} /> {t('s_mypass')}</button>
        <button className="btn danger" onClick={logout}>{t('logout')}</button>
      </div>
      {showPass && (
        <form onSubmit={(e) => { void changePass(e); }}
          style={{ marginTop: 14, borderTop: '1px solid var(--border)', paddingTop: 12 }}>
          <label htmlFor="pf-cur">{t('s_mypass_cur')}</label>
          <input id="pf-cur" type="password" autoComplete="current-password" value={cur}
            onChange={(e) => setCur(e.target.value)} required />
          <label htmlFor="pf-p1">{t('mp_new')}</label>
          <input id="pf-p1" type="password" autoComplete="new-password" value={p1}
            onChange={(e) => setP1(e.target.value)} minLength={8} required />
          <label htmlFor="pf-p2">{t('s_mypass2')}</label>
          <input id="pf-p2" type="password" autoComplete="new-password" value={p2}
            onChange={(e) => setP2(e.target.value)} minLength={8} required />
          <div className="m-actions">
            <button type="submit" className="btn primary"
              disabled={busy || !cur || p1.length < 8 || p1 !== p2}>{t('update')}</button>
          </div>
        </form>
      )}
      {msg && <p style={{ fontSize: 13, color: 'var(--ok)', fontWeight: 600, marginTop: 10 }} role="status">{msg}</p>}
      {err && <p className="form-err" role="alert" style={{ marginTop: 10 }}>{err}</p>}

      {/* Diálogo de recorte cuadrado para la foto de perfil */}
      {cropFile && (
        <AvatarCropDialog file={cropFile} onClose={() => setCropFile(null)}
          onCrop={(blob) => uploadAvatar(blob)} />
      )}

      {/* Modal de notificaciones push (contenido = PushPanel) */}
      {showNotifs && (
        <ModalBox label={t('s_push')} onClose={() => setShowNotifs(false)}>
          <h3 className="cardtitle">{t('s_push')}</h3>
          <PushPanel />
          <div className="m-actions">
            <button className="btn" onClick={() => setShowNotifs(false)}>{t('close')}</button>
          </div>
        </ModalBox>
      )}
    </div>
  );
}

export default function Settings() {
  const { t, themeMode, themeEff, setTheme, isAdmin, user, refresh } = useApp();
  const { openModal } = useModal();
  const [settings, setSettings] = useState<SettingsData | null>(null);
  const [users, setUsers] = useState<Awaited<ReturnType<ReturnType<typeof getProvider>['getUsers']>> | null>(null);
  const [version, setVersion] = useState<Awaited<ReturnType<ReturnType<typeof getProvider>['getVersion']>> | null>(null);
  const [msg, setMsg] = useState('');
  const [err, setErr] = useState('');
  const [accent, setAccentState] = useState<AccentId>(getAccent());
  const [density, setDensityState] = useState<Density>(getDensity());
  const [reduceMotion, setReduceMotionState] = useState(getReduceMotion());
  const [installEvt, setInstallEvt] = useState<BeforeInstallPromptEvent | null>(null);
  const [installed, setInstalled] = useState(isStandalone());
  const [adminPanel, setAdminPanel] = useState<'backup' | 'users' | null>(null);

  useEffect(() => {
    let alive = true;
    getProvider().getSettings().then((s) => alive && setSettings(s)).catch(() => {});
    getProvider().getVersion().then((v) => alive && setVersion(v)).catch(() => {});
    if (isAdmin) getProvider().getUsers().then((u) => alive && setUsers(u)).catch(() => {});
    return () => { alive = false; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isAdmin, refresh]);

  // Instalación PWA: captura el evento y detecta cuando queda instalada
  useEffect(() => {
    const onPrompt = (e: Event) => { e.preventDefault(); setInstallEvt(e as BeforeInstallPromptEvent); };
    const onInstalled = () => { setInstalled(true); setInstallEvt(null); };
    window.addEventListener('beforeinstallprompt', onPrompt);
    window.addEventListener('appinstalled', onInstalled);
    return () => {
      window.removeEventListener('beforeinstallprompt', onPrompt);
      window.removeEventListener('appinstalled', onInstalled);
    };
  }, []);

  const saveSettings = async (patch: Partial<SettingsData>) => {
    if (!settings) return;
    const next = { ...settings, ...patch };
    setSettings(next);
    setMsg(''); setErr('');
    try {
      await getProvider().putSettings(next);
      setMsg(t('saved_ok'));
    } catch (e) { setErr(errorMessage(e, t)); }
  };

  if (!settings) return <Spinner label={t('loading')} />;

  // Validación de umbrales en cliente: capacidad 1-100 con warn < crit,
  // temperatura entre 20 y 90 °C. Si no cumple, se avisa inline y se
  // deshabilita el botón de guardar umbrales.
  const capOk = Number.isInteger(settings.cap_warn_pct) && Number.isInteger(settings.cap_crit_pct)
    && settings.cap_warn_pct >= 1 && settings.cap_warn_pct <= 100
    && settings.cap_crit_pct >= 1 && settings.cap_crit_pct <= 100
    && settings.cap_warn_pct < settings.cap_crit_pct;
  const tempOk = Number.isInteger(settings.disk_temp_c)
    && settings.disk_temp_c >= 20 && settings.disk_temp_c <= 90;
  const threshOk = capOk && tempOk;

  const themeOpts: { v: ThemeMode; label: string; icon: typeof IconSun }[] = [
    { v: 'light', label: t('s_theme_light'), icon: IconSun },
    { v: 'dark', label: t('s_theme_dark'), icon: IconMoon },
    { v: 'auto', label: t('s_theme_auto'), icon: IconMonitor },
  ];

  return (
    <div className="view">
      {/* ---- Fila 1: Datos y umbrales (50%, admin) + Apariencia (50%) ---- */}
      <div className={`st-row${isAdmin ? '' : ' st-single'}`}>
      {isAdmin && (
        <div className="card pad admin-card">
          <h3 className="cardtitle">{t('s_data_thresh')}</h3>
          <p className="muted">{t('s_data_thresh_d')}</p>
          <label htmlFor="th-warn">{t('s_cap_warn')}</label>
          <input id="th-warn" type="number" value={settings.cap_warn_pct}
            onChange={(e) => setSettings({ ...settings, cap_warn_pct: +e.target.value })} />
          <label htmlFor="th-crit">{t('s_cap_crit')}</label>
          <input id="th-crit" type="number" value={settings.cap_crit_pct}
            onChange={(e) => setSettings({ ...settings, cap_crit_pct: +e.target.value })} />
          <label htmlFor="th-temp">{t('s_temp')}</label>
          <input id="th-temp" type="number" value={settings.disk_temp_c}
            onChange={(e) => setSettings({ ...settings, disk_temp_c: +e.target.value })} />
          {!threshOk && <p className="form-err" role="alert">{t('s_thresh_invalid')}</p>}
          <div className="m-actions">
            <button className="btn primary" disabled={!threshOk} onClick={() => saveSettings({})}>{t('save')}</button>
          </div>
        </div>
      )}

      {/* ---- Apariencia (mitad derecha; la primera con usuario no-admin) ---- */}
      <div className="card pad">
        <h3 className="cardtitle">{t('s_appear')}</h3>
        <label>{t('s_theme')}</label>
        <div className="themegrid" role="radiogroup" aria-label={t('s_theme')}>
          {themeOpts.map((o) => {
            const Ico = o.icon;
            return (
              <button key={o.v} type="button" role="radio" aria-checked={themeMode === o.v}
                className={`themecard${themeMode === o.v ? ' sel' : ''}`}
                onClick={() => setTheme(o.v)}>
                <ThemePreview mode={o.v} />
                <span className="lbl"><Ico size={13} />{o.label}</span>
                {themeMode === o.v && <span className="check"><IconCheck /></span>}
              </button>
            );
          })}
        </div>
        <label>{t('s_accent')}</label>
        <div className="swatches" role="group" aria-label={t('s_accent')}>
          {(Object.keys(ACCENTS) as AccentId[]).map((id) => (
            <button key={id} type="button" title={t(`acc_${id}`)} aria-label={t(`acc_${id}`)}
              className={`swatch${accent === id ? ' sel' : ''}`}
              aria-pressed={accent === id}
              onClick={() => { setAccent(id); setAccentState(id); }}>
              {/* el círculo pinta el color del tema EFECTIVO (el que se aplicará) */}
              <span style={{ background: ACCENTS[id][themeEff][0] }} />
            </button>
          ))}
        </div>
        <label>{t('s_density')}</label>
        <Seg value={density} ariaLabel={t('s_density')}
          onChange={(d) => { setDensity(d); setDensityState(d); }}
          options={[
            { v: 'cozy', label: t('s_density_cozy') },
            { v: 'compact', label: t('s_density_compact') },
          ]} />
        <div style={{ display: 'flex', alignItems: 'center', gap: 9, marginTop: 14 }}>
          <span style={{ flex: 1 }}>
            <span style={{ display: 'block', fontSize: 13.5, fontWeight: 600 }}>{t('s_rm')}</span>
            <span className="muted">{t('s_rm_d')}</span>
          </span>
          <Switch checked={reduceMotion} ariaLabel={t('s_rm')}
            onChange={(v) => { setReduceMotion(v); setReduceMotionState(v); }} />
        </div>
      </div>
      </div>

      {/* ---- Fila 2: Mi perfil (horizontal, ancho completo: avatar + campos + notificaciones) ---- */}
      <ProfileCard />

      {/* ---- Zona de administración (solo admin): AdminBar canónica ---- */}
      {isAdmin && (
      <>
      {/* AdminBar horizontal: Actualizaciones → Respaldos → Usuarios → Modo demo (derecha) */}
      <div className="admin-bar">
        <div className="ab-row">
          <div className="ab-title">
            <IconShield size={18} />
            <span>{t('s_admin_zone')}</span>
            <ReleaseIcon version={version?.version} />
          </div>
          <span className="ab-sep" />

          {/* 1. Comprobar actualizaciones (widget inline) */}
          <UpdateCheckRow version={version?.version} />

          {/* 2. Respaldos (desplegable) */}
          <button
            type="button"
            aria-expanded={adminPanel === 'backup'}
            onClick={() => setAdminPanel(adminPanel === 'backup' ? null : 'backup')}
            className={`ab-btn${adminPanel === 'backup' ? ' on' : ''}`}
          >
            <IconData size={15} />
            <span className="hidden-sm">{t('bk_title')}</span>
            <IconChev className="chev" />
          </button>

          {/* 3. Usuarios (desplegable) */}
          <button
            type="button"
            aria-expanded={adminPanel === 'users'}
            onClick={() => setAdminPanel(adminPanel === 'users' ? null : 'users')}
            className={`ab-btn${adminPanel === 'users' ? ' on' : ''}`}
          >
            <IconUser size={15} />
            <span className="hidden-sm">{t('s_users')}</span>
            <IconChev className="chev" />
          </button>

          {/* 4. Modo demo a la derecha */}
          <div className="ab-right">
            <span>{t('s_demo_enable')}</span>
            <Switch checked={settings.demo_enabled} ariaLabel={t('s_demo_enable')}
              onChange={(v) => { void saveSettings({ demo_enabled: v }); }} />
          </div>
        </div>

        {/* Paneles desplegables */}
        {adminPanel === 'backup' && (
          <div className="ab-panel">
            <BackupCard settings={settings} onSave={saveSettings} />
          </div>
        )}
        {adminPanel === 'users' && (
          <div className="ab-panel">
            <div className="card pad admin-card">
              <h3 className="cardtitle">{t('s_users')}
                <span className="actions" style={{ float: 'right' }}>
                  <button className="btn sm primary" onClick={() => openModal('newuser')}>+ {t('s_newuser')}</button>
                </span>
              </h3>
              <div>
                {(users ?? []).map((u) => (
                  <div className="rowitem" key={u.user}>
                    <div className="grow">
                      <div className="t1" style={{ fontSize: 14 }}>
                        {u.display_name || u.user}
                        {u.user === user?.user && <span style={{ fontSize: 11, color: 'var(--text2)', fontWeight: 500 }}>{t('you')}</span>}
                        <span className={`rolebadge ${u.role}`}>{u.role === 'admin' ? t('mu_r_admin') : t('mu_r_user')}</span>
                      </div>
                      <div className="t2">
                        {u.user} · {t('s_last_login')}: {timeAgo(u.last_login, t)} · {u.sessions}{' '}
                        {u.sessions === 1 ? t('s_session_one') : t('s_sessions')}
                      </div>
                    </div>
                    <Select value={u.language ?? 'auto'} ariaLabel={t('s_lang')}
                      options={[{ v: 'auto', label: t('s_lang_auto') }, { v: 'es', label: '🇪🇸 Español' }, { v: 'en', label: '🇬🇧 English' }]}
                      onChange={(v) => {
                        const lang = v as 'auto' | 'es' | 'en';
                        setUsers((cur) => cur?.map((x) => (x.user === u.user ? { ...x, language: lang } : x)) ?? cur);
                        getProvider().setUserLanguage(u.user, lang).catch(() => {});
                      }} />
                    <button className="btn sm" onClick={() => openModal('passwd', { user: u.user })}>{t('s_passwd')}</button>
                    {u.user !== user?.user && (
                      <button className="btn sm danger" onClick={() => openModal('deluser', { user: u.user })}>{t('s_delete_user')}</button>
                    )}
                  </div>
                ))}
                {users && users.length === 0 && <div className="empty">{t('empty')}</div>}
              </div>
              <p style={{ fontSize: 12, color: 'var(--text2)', marginTop: 8 }}>{t('s_roles_d')}</p>
            </div>
          </div>
        )}
      </div>

      {/* Fila de dominio: Notificaciones webhook | Actividad */}
      <div className="st-row st-users">

      {/* ---- Notificaciones webhook ---- */}
      <div className="card pad admin-card">
        <h3 className="cardtitle">{t('s_notif')}</h3>
        <label htmlFor="nf-hook">{t('s_webhook')}</label>
        <input id="nf-hook" placeholder={t('s_webhook_ph')} value={settings.webhook}
          onChange={(e) => setSettings({ ...settings, webhook: e.target.value })} />
        <label className="checklabel" style={{ marginTop: 16 }}>
          <input type="checkbox" checked={settings.notify_scrub_errors}
            onChange={(e) => setSettings({ ...settings, notify_scrub_errors: e.target.checked })} />
          {t('s_n_scrub')}
        </label>
        <label className="checklabel">
          <input type="checkbox" checked={settings.notify_smart_change}
            onChange={(e) => setSettings({ ...settings, notify_smart_change: e.target.checked })} />
          {t('s_n_smart')}
        </label>
        <div className="m-actions">
          <button className="btn primary" onClick={() => saveSettings({})}>{t('save')}</button>
        </div>
      </div>

      {/* ---- Actividad (audit log, "ver más") ---- */}
      <ActivityCard />
      </div>
      </>
      )}

      {/* ---- Acerca de (ancho completo: 4 tiles + instalar PWA + sistema) ---- */}
      {version && (
        <div className="card pad st-about">
          <h3 className="cardtitle">{t('s_about')}</h3>
          <div className="about">
            <div className="logo"><Logo size={46} /></div>
            <div style={{ flex: 1 }}>
              <div style={{ fontWeight: 800, fontSize: 16 }}>{version?.name ?? 'EasyZFS'}</div>
              {version && (
                <div style={{ fontSize: 12, color: 'var(--accent)', fontWeight: 700 }}>
                  v{version.version} · build {version.build}
                </div>
              )}
              <div className="muted" style={{ marginTop: 2 }}>{t('s_about_d')}</div>
            </div>
          </div>

          <div className="abouttiles">
            <a className="abouttile" href={REPO_URL} target="_blank" rel="noreferrer">
              <span className="t-ico"><IconCode size={16} /></span>
              <b>{t('ab_code')}</b>
              <span>{t('ab_code_d')}</span>
            </a>
            <a className="abouttile" href={`${REPO_URL}/releases`} target="_blank" rel="noreferrer">
              <span className="t-ico"><IconList size={16} /></span>
              <b>{t('ab_chlog')}</b>
              <span>{t('ab_chlog_d')}</span>
            </a>
            <div className="abouttile">
              <span className="t-ico"><IconHeart size={16} /></span>
              <b>{t('ab_kofi')}</b>
              <span>{t('ab_kofi_d')}</span>
            </div>
            <div className="abouttile">
              <span className="t-ico"><IconShield size={16} /></span>
              <b>{t('ab_priv')}</b>
              <span>{t('ab_priv_d')}</span>
            </div>
          </div>

          {/* Instalación PWA + Comprobar actualizaciones (admin) en la misma
              fila. La tira PWA SOLO se renderiza si el navegador lo soporta
              (evento capturado, iOS con instrucciones o ya instalada);
              sin soporte no se renderiza nada (regla webapp-shell) */}
          <div className="aboutrow">
            {(installed || installEvt || isIOS()) && (
              <div className="installstrip">
                <span className="t-ico" style={{ width: 30, height: 30, borderRadius: 8, background: 'var(--accent-soft)', color: 'var(--accent)', display: 'grid', placeItems: 'center', flexShrink: 0 }}>
                  <IconDownload size={16} />
                </span>
                <div className="grow">
                  <b>{t('ab_install')}</b>
                  <div className="d">
                    {installed ? t('ab_installed_d')
                      : isIOS() ? t('ab_install_ios')
                      : t('ab_install_d')}
                  </div>
                </div>
                {installed
                  ? <Badge tone="ok" dot={false}>{t('ab_installed')}</Badge>
                  : installEvt
                    ? <button className="btn sm primary" onClick={() => { void installEvt.prompt(); }}>{t('ab_install_btn')}</button>
                    : null}
              </div>
            )}
            {isAdmin && <UpdateCheckRow version={version?.version} />}
          </div>

          {/* Sistema (datos del servidor, integrados en Acerca de) */}
          <div style={{ marginTop: 16, borderTop: '1px solid var(--border)', paddingTop: 12 }}>
            <div className="kv"><span>{t('ab_rt')}</span><span className="mono">{version.go} {version.os_arch}</span></div>
            <div className="kv"><span>{t('ab_up')}</span><span>{fmtDuration(version.uptime_sec)}</span></div>
            <div className="kv"><span>{t('ab_mem')}</span><span>{fmtBytes(version.rss_bytes)}</span></div>
            <div className="kv"><span>{t('ab_db')}</span><span>{fmtBytes(version.db_bytes)} · {version.db_path}</span></div>
            <div className="kv"><span>ZFS</span><span className="mono">{version.zfs_version}</span></div>
            <div className="kv"><span>{t('ab_lic')}</span><span>AGPL-3.0</span></div>
          </div>

          <div className="aboutfoot mono">
            {version?.name ?? 'EasyZFS'} v{version?.version ?? '0.1.0'} · {version?.zfs_version ?? 'OpenZFS'} · AGPL-3.0
          </div>
        </div>
      )}

      {msg && <p style={{ fontSize: 13, color: 'var(--ok)', fontWeight: 600, marginTop: 12 }} role="status">{msg}</p>}
      {err && <p className="form-err" role="alert">{err}</p>}

    </div>
  );
}
