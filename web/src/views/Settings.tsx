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
import { fmtBytes, timeAgo } from '../ui/format';
import { Seg, Select, Spinner, Switch, Badge } from '../components/ui';
import { Logo, IconCode, IconList, IconHeart, IconShield, IconCheck, IconUpload, IconCamera, IconChev, IconData, IconUser, IconX, IconTrash, IconLock, IconBell, IconMail, IconPencil, IconLogout, IconLanguages, IconSend } from '../components/icons';
import { useModal } from '../components/Modal';
import { AvatarCropDialog } from '../components/AvatarCropDialog';
import { TwoFAPanel } from '../components/TwoFA';
import { APIKeysPanel } from '../components/APIKeysPanel';
import { usePush } from '../data/push';
import { useReleaseCheck, refreshUpdateState } from '../ui/releasecheck';
import { FAMILY_ACCENTS, getAccent, setAccent, getDensity, setDensity, getReduceMotion, setReduceMotion, currentFamilyAccentId } from '../ui/theme';
import type { Density, ThemeFamily, ThemeMode } from '../ui/theme';
import type { I18nKey } from '../ui/i18n';
import type {
  BackupStatus, ChannelInfo, ChannelName, ChannelPatch, ChannelsStatus, Lang, PushAlertTipo, PushPreference,
  Settings as SettingsData, UpdateStatus,
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

// Fila "Comprobar actualizaciones" (Acerca de, solo admin): consulta al
// servidor (/api/update/status) que detecta releases semver del repo público;
// si hay versión nueva, botón "Actualizar" que aplica (el servidor descarga+
// valida y easyzfs-update.path reinicia con el binario nuevo). Muestra el
// resultado inline: al día, nueva versión, aplicando o error.
function UpdateCheckRow({ version }: { version: string | undefined }) {
  const { t } = useApp();
  const { openModal } = useModal();
  const [state, setState] = useState<'idle' | 'checking' | 'uptodate' | 'available' | 'error'>('idle');
  const [status, setStatus] = useState<UpdateStatus | null>(null);
  const [plan, setPlan] = useState<{ canApply: boolean; checks: { id: string; status: string; title: string; summary: string }[] } | null>(null);

  const check = async () => {
    setState('checking');
    setStatus(null);
    setPlan(null);
    try {
      const [st, plan] = await Promise.all([getProvider().checkUpdate(), getProvider().getUpdatePlan()]);
      setStatus(st);
      setPlan(plan);
      setState(st.available ? 'available' : 'uptodate');
      refreshUpdateState(); // que el ribbon global se actualice tras el check manual
    } catch {
      setState('error');
    }
  };

  return (
    <div className="upd-widget">
      <div className="upd-line">
        <span className="upd-status">
          {state === 'available' ? t('ab_newver', { v: status?.latest ?? '' })
            : state === 'error' ? t('ab_upderr')
            : state === 'uptodate' ? t('ab_uptodate', { v: version ?? '' })
            : null}
        </span>
        <span className="upd-actions">
          {state === 'available' && (
            <button className="btn sm primary" onClick={() => openModal('updatewizard')}>
              {t('upd_rel_upd')}
            </button>
          )}
          <button className="btn sm" disabled={state === 'checking'} onClick={() => { void check(); }}>
            {state === 'checking' ? t('ab_checking') : state === 'error' ? t('ab_retry') : t('ab_checkupd')}
          </button>
        </span>
      </div>
      {plan?.checks && plan.checks.length > 0 && state === 'available' && (
        <div style={{ marginTop: 6 }}>
          {plan.checks.map((c) => (
            <div key={c.id} className="d" style={{ fontSize: 12, opacity: c.status === 'pass' ? .7 : 1, color: c.status === 'fail' ? 'var(--err)' : c.status === 'warn' ? 'var(--warn)' : 'inherit' }}>
              {c.status === 'fail' ? '✗ ' : c.status === 'warn' ? '⚠ ' : '✓ '}{c.summary}
            </div>
          ))}
        </div>
      )}
      {status?.restartConfigured === false && (
        <div className="form-err" role="alert" style={{ marginTop: 8, fontSize: 12.5 }}>
          <b>{t('ab_upd_norestart')}</b><br />
          {t('ab_upd_norestart_d')}
        </div>
      )}
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
  const { t, notify } = useApp();
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
      notify(t('bk_done', { f: f.file }), 'ok');
    } catch (e) { const m = errorMessage(e, t); setErr(m); notify(m, 'err'); }
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
      notify(t('toast_backup_imported'), 'ok');
      setTimeout(() => location.reload(), 4000);
    } catch (e) {
      const m = errorMessage(e, t);
      setErr(m); notify(m, 'err');
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

// Tarjeta "Mi perfil" (barra horizontal compacta, asset webapp-shell):
// sin título, avatar + nombre editable + rol; a la derecha email, idioma,
// contraseña y notificaciones (despliegan inline); cerrar sesión rojo.
function ProfileCard() {
  const { t, user, langMode, setLang, logout, reloadUser, isAdmin, notify } = useApp();
  const [msg, setMsg] = useState('');
  const [err, setErr] = useState('');
  const [busy, setBusy] = useState(false);
  const [showPass, setShowPass] = useState(false);
  const [showNotifs, setShowNotifs] = useState(false);
  const [show2FA, setShow2FA] = useState(false);
  const [cur, setCur] = useState('');
  const [p1, setP1] = useState('');
  const [p2, setP2] = useState('');
  const [cropFile, setCropFile] = useState<File | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);

  const [editingName, setEditingName] = useState(false);
  const [nameDraft, setNameDraft] = useState(user?.display_name ?? '');
  const [editingEmail, setEditingEmail] = useState(false);
  const [emailDraft, setEmailDraft] = useState(user?.email ?? '');

  const displayName = user?.display_name || user?.user || '';
  const avatarUrl = user?.avatar ? getProvider().avatarUrl(user.avatar) : '';
  const initial = (user?.display_name || user?.user || '?').trim().charAt(0).toUpperCase();

  const uploadAvatar = async (blob: Blob) => {
    setMsg(''); setErr('');
    try {
      await getProvider().setMyAvatar(blob);
      reloadUser();
      setMsg(t('saved_ok'));
      notify(t('toast_avatar_updated'), 'ok');
    } catch (e) { const m = errorMessage(e, t); setErr(m); notify(m, 'err'); throw e; }
  };

  const removeAvatar = async () => {
    setMsg(''); setErr('');
    try {
      await getProvider().deleteMyAvatar();
      reloadUser();
      setMsg(t('s_avatar_removed'));
      notify(t('toast_avatar_deleted'), 'ok');
    } catch (e) { const m = errorMessage(e, t); setErr(m); notify(m, 'err'); }
  };

  const saveName = async () => {
    const v = nameDraft.trim();
    if (v === (user?.display_name || '')) { setEditingName(false); return; }
    setBusy(true); setMsg(''); setErr('');
    try {
      await getProvider().updateMyProfile(v, (user?.email ?? '').trim());
      reloadUser();
      setMsg(t('saved_ok'));
      setEditingName(false);
      notify(t('toast_saved'), 'ok');
    } catch (e) { const m = errorMessage(e, t); setErr(m); notify(m, 'err'); }
    setBusy(false);
  };

  const saveEmail = async () => {
    const v = emailDraft.trim();
    if (v === (user?.email || '')) { setEditingEmail(false); return; }
    setBusy(true); setMsg(''); setErr('');
    try {
      await getProvider().updateMyProfile(user?.display_name ?? '', v);
      reloadUser();
      setMsg(t('saved_ok'));
      setEditingEmail(false);
      notify(t('toast_saved'), 'ok');
    } catch (e) { const m = errorMessage(e, t); setErr(m); notify(m, 'err'); }
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
      notify(t('toast_pwd_changed'), 'ok');
    } catch (ex) { const m = errorMessage(ex, t); setErr(m); notify(m, 'err'); }
    setBusy(false);
  };

  // En móvil: icono de idioma que abre el select (oculto). Desktop: select completo.
  const langBlock = (
    <>
      <label className="pact-ico lang-mobile" aria-label={t('s_lang')} title={t('s_lang')}
        onClick={(e) => { e.preventDefault(); const s = document.getElementById('mp-lang'); if (s) { (s as HTMLSelectElement).focus(); (s as HTMLSelectElement).click(); } }}>
        <IconLanguages size={16} />
      </label>
      <span className="lang-desktop">
        <IconLanguages size={16} aria-hidden="true" />
        <select id="mp-lang" className="plang" value={langMode} onChange={(e) => setLang(e.target.value as Lang)}
          aria-label={t('s_lang')} title={t('s_lang')}>
          <option value="auto">{t('s_lang_auto')}</option>
          <option value="es">Español</option>
          <option value="en">English</option>
        </select>
      </span>
    </>
  );

  return (
    <div className="card pad">
      <div className="prow">
        {/* Avatar editable */}
        <button type="button" className="avatar-lg" onClick={() => fileRef.current?.click()}
          aria-label={t('s_avatar_change')} title={t('s_avatar_change')}>
          {avatarUrl ? <img src={avatarUrl} alt="" /> : <span aria-hidden="true">{initial}</span>}
          <span className="cam" aria-hidden="true"><IconCamera size={18} /></span>
          {avatarUrl && (
            <span className="cam-del" onClick={(e) => { e.stopPropagation(); void removeAvatar(); }}
              aria-label={t('s_avatar_remove')} title={t('s_avatar_remove')}><IconTrash size={16} /></span>
          )}
        </button>
        <input ref={fileRef} type="file" accept="image/*" hidden
          onChange={(e) => { const f = e.target.files?.[0]; if (f) setCropFile(f); e.target.value = ''; }} />

        {/* Nombre editable + rol */}
        <div className="pname">
          {editingName ? (
            <div className="p-edit">
              <input type="text" value={nameDraft} maxLength={64} autoFocus autoComplete="nickname"
                placeholder={user?.user}
                onChange={(e) => setNameDraft(e.target.value)}
                onKeyDown={(e) => { if (e.key === 'Enter') void saveName(); if (e.key === 'Escape') { setEditingName(false); setNameDraft(displayName); } }} />
              <button type="button" className="p-ok" disabled={busy} onClick={() => void saveName()}
                aria-label={t('save')} title={t('save')}><IconCheck size={14} /></button>
              <button type="button" className="p-x" onClick={() => { setEditingName(false); setNameDraft(displayName); }}
                aria-label={t('cancel')} title={t('cancel')}><IconX size={14} /></button>
            </div>
          ) : (
            <button type="button" className="pname-btn"
              onClick={() => { setNameDraft(displayName); setEditingName(true); }}
              title={t('s_displayname')}>
              <b>{displayName}</b>
              <IconPencil size={13} aria-hidden="true" />
            </button>
          )}
          <span className="pname-role">
            {isAdmin ? t('mu_r_admin') : t('mu_r_user')} · @{user?.user}
          </span>
        </div>

        {/* Grupo de acciones: email + idioma + contraseña + notificaciones */}
        <div className="pacts">
          {editingEmail ? (
            <div className="p-edit">
              <input type="email" value={emailDraft} autoFocus autoComplete="email"
                placeholder={t('s_email')}
                onChange={(e) => setEmailDraft(e.target.value)}
                onKeyDown={(e) => { if (e.key === 'Enter') void saveEmail(); if (e.key === 'Escape') { setEditingEmail(false); setEmailDraft(user?.email ?? ''); } }} />
              <button type="button" className="p-ok" disabled={busy} onClick={() => void saveEmail()}
                aria-label={t('save')} title={t('save')}><IconCheck size={14} /></button>
              <button type="button" className="p-x" onClick={() => { setEditingEmail(false); setEmailDraft(user?.email ?? ''); }}
                aria-label={t('cancel')} title={t('cancel')}><IconX size={14} /></button>
            </div>
          ) : (
            <button type="button" className={`pact-ico${user?.email ? ' has' : ''}`}
              onClick={() => { setEmailDraft(user?.email ?? ''); setEditingEmail(true); }}
              title={user?.email ? user.email : t('s_email_d')}
              aria-label={user?.email ? t('s_email') : t('s_email_d')}>
              <IconMail size={16} />
            </button>
          )}

          {langBlock}

          <button type="button" className="pact-btn" aria-expanded={showPass}
            onClick={() => setShowPass((v) => !v)} title={t('s_mypass')}>
            <IconLock size={16} aria-hidden="true" />
            <span className="hidden-sm">{t('s_mypass')}</span>
          </button>

          <button type="button" className="pact-btn" aria-expanded={showNotifs}
            onClick={() => setShowNotifs((v) => !v)} title={t('s_notifs')}>
            <IconBell size={16} aria-hidden="true" />
            <span className="hidden-sm">{t('s_notifs')}</span>
          </button>

          <button type="button" className="pact-btn" aria-expanded={show2FA}
            onClick={() => setShow2FA((v) => !v)} title={t('s_2fa')}>
            <IconShield size={16} aria-hidden="true" />
            <span className="hidden-sm">{t('s_2fa')}</span>
          </button>
        </div>

        {/* Cerrar sesión — siempre a la derecha, rojo */}
        <button type="button" className="plogout" onClick={logout} title={t('logout')}>
          <IconLogout size={16} aria-hidden="true" />
          <span className="hidden-sm">{t('logout')}</span>
        </button>
      </div>

      {/* Cambio de contraseña INLINE */}
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

      {/* Notificaciones push INLINE */}
      {showNotifs && (
        <div style={{ marginTop: 14, borderTop: '1px solid var(--border)', paddingTop: 12 }}>
          <PushPanel />
        </div>
      )}

      {/* Verificación en dos pasos INLINE */}
      {show2FA && (
        <div style={{ marginTop: 14, borderTop: '1px solid var(--border)', paddingTop: 12 }}>
          <TwoFAPanel />
        </div>
      )}

      {msg && <p style={{ fontSize: 13, color: 'var(--ok)', fontWeight: 600, marginTop: 10 }} role="status">{msg}</p>}
      {err && <p className="form-err" role="alert" style={{ marginTop: 10 }}>{err}</p>}
    </div>
  );
}

// Etiqueta traducida de cada canal de alerta (exhaustivo sobre ChannelName).
const CHANNEL_LABEL: Record<ChannelName, I18nKey> = {
  ntfy: 's_ch_ntfy',
  gotify: 's_ch_gotify',
  telegram: 's_ch_telegram',
  syslog: 's_ch_syslog',
  email: 's_ch_email',
  webhook: 's_ch_webhook',
  push: 's_ch_push',
};

// Orden de presentación y canales que admiten prueba de envío (los del
// paquete channels; email/webhook/push solo muestran su estado).
const CHANNEL_ORDER: ChannelName[] = ['telegram', 'ntfy', 'gotify', 'syslog', 'email', 'webhook', 'push'];
const CHANNEL_TESTABLE: ChannelName[] = ['telegram', 'ntfy', 'gotify', 'syslog', 'email'];

// Detalle no secreto de la fila de un canal.
function channelDetail(name: ChannelName, info: ChannelInfo, t: (k: I18nKey) => string): string {
  switch (name) {
    case 'ntfy':
      return info.topic_set ? `${info.server ?? ''} · ${t('s_ch_topic_set')}` : (info.server ?? '');
    case 'gotify':
      return info.url ?? '';
    case 'telegram':
      return info.chat_id ? `chat ${info.chat_id}` : '';
    case 'syslog':
      return info.host ? `${info.host}:${info.port ?? 514} (${info.proto ?? 'udp'})` : '';
    case 'email':
      return info.host ? `${info.host}:${info.port ?? 587}` : '';
    case 'webhook':
      return info.url ?? '';
    default:
      return '';
  }
}

// Formulario de configuración de un canal (se despliega bajo su fila). Los
// campos write-only (URL de ntfy, tokens) vacíos conservan el valor actual.
function ChannelForm({ name, info, onDone }: { name: ChannelName; info: ChannelInfo; onDone: () => void }) {
  const { t, notify } = useApp();
  const [url, setUrl] = useState(name === 'gotify' || name === 'webhook' ? (info.url ?? '') : '');
  const [token, setToken] = useState('');
  const [chatId, setChatId] = useState(info.chat_id ?? '');
  const [host, setHost] = useState(info.host ?? '');
  const [port, setPort] = useState(String(info.port ?? (name === 'email' ? 587 : 514)));
  const [proto, setProto] = useState(info.proto ?? 'udp');
  const [facility, setFacility] = useState(String(info.facility ?? 1));
  const [user, setUser] = useState(info.user ?? '');
  const [pass, setPass] = useState('');
  const [from, setFrom] = useState(info.from ?? '');
  const [encryption, setEncryption] = useState(info.encryption ?? 'starttls');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');

  const save = async () => {
    setBusy(true); setErr('');
    try {
      const patch: ChannelPatch = {};
      if (name === 'ntfy') {
        if (url.trim()) patch.url = url.trim();
        if (token.trim()) patch.token = token.trim();
      } else if (name === 'gotify') {
        patch.url = url.trim();
        if (token.trim()) patch.token = token.trim();
      } else if (name === 'telegram') {
        if (token.trim()) patch.token = token.trim();
        patch.chat_id = chatId.trim();
      } else if (name === 'email') {
        patch.host = host.trim();
        patch.port = +port;
        patch.user = user.trim();
        patch.from = from.trim();
        patch.encryption = encryption;
        if (pass.trim()) patch.pass = pass.trim();
      } else if (name === 'webhook') {
        patch.url = url.trim();
      } else {
        patch.host = host.trim();
        patch.port = +port;
        patch.proto = proto;
        patch.facility = +facility;
      }
      await getProvider().putChannel(name, patch);
      notify(t('s_ch_saved'), 'ok');
      onDone();
    } catch (e) { setErr(errorMessage(e, t)); }
    setBusy(false);
  };

  const disable = async () => {
    setBusy(true); setErr('');
    try {
      await getProvider().deleteChannel(name);
      notify(t('s_ch_disabled'), 'ok');
      onDone();
    } catch (e) { setErr(errorMessage(e, t)); }
    setBusy(false);
  };

  return (
    <div className="ch-form">
      {name === 'ntfy' && (
        <>
          <label htmlFor="ch-url">{t('s_ch_ntfy_url')}</label>
          <input id="ch-url" type="text" value={url} autoComplete="off"
            placeholder="https://ntfy.sh/mi-topic" onChange={(e) => setUrl(e.target.value)} />
          <p className="muted" style={{ marginTop: 2 }}>{t('s_ch_ntfy_url_hint')}</p>
          <label htmlFor="ch-tok">{t('s_ch_token_opt')}</label>
          <input id="ch-tok" type="password" value={token} autoComplete="new-password"
            placeholder={info.token_set ? '••••••••' : ''} onChange={(e) => setToken(e.target.value)} />
          <p className="muted" style={{ marginTop: 2 }}>{t('s_ch_token_hint')}</p>
        </>
      )}
      {name === 'gotify' && (
        <>
          <label htmlFor="ch-url">{t('s_ch_gotify_url')}</label>
          <input id="ch-url" type="text" value={url} autoComplete="off"
            placeholder="https://gotify.example.com" onChange={(e) => setUrl(e.target.value)} />
          <label htmlFor="ch-tok">{t('s_ch_token')}</label>
          <input id="ch-tok" type="password" value={token} autoComplete="new-password"
            placeholder={info.token_set ? '••••••••' : ''} onChange={(e) => setToken(e.target.value)} />
          <p className="muted" style={{ marginTop: 2 }}>{t('s_ch_token_hint')}</p>
        </>
      )}
      {name === 'telegram' && (
        <>
          <label htmlFor="ch-tok">{t('s_ch_tg_token')}</label>
          <input id="ch-tok" type="password" value={token} autoComplete="new-password"
            placeholder={info.token_set ? '••••••••' : '123456:ABC…'} onChange={(e) => setToken(e.target.value)} />
          <p className="muted" style={{ marginTop: 2 }}>{t('s_ch_token_hint')}</p>
          <label htmlFor="ch-chat">{t('s_ch_tg_chat')}</label>
          <input id="ch-chat" type="text" value={chatId} autoComplete="off"
            placeholder="-1001234567890" onChange={(e) => setChatId(e.target.value)} />
        </>
      )}
      {name === 'syslog' && (
        <div style={{ display: 'flex', gap: 10, flexWrap: 'wrap', alignItems: 'flex-end' }}>
          <div style={{ minWidth: 150 }}>
            <label htmlFor="ch-host">{t('s_ch_syslog_host')}</label>
            <input id="ch-host" type="text" value={host} placeholder="127.0.0.1" onChange={(e) => setHost(e.target.value)} />
          </div>
          <div style={{ width: 100 }}>
            <label htmlFor="ch-port">{t('s_ch_syslog_port')}</label>
            <input id="ch-port" type="number" min={1} max={65535} value={port} onChange={(e) => setPort(e.target.value)} />
          </div>
          <div style={{ width: 110 }}>
            <label htmlFor="ch-proto">{t('s_ch_syslog_proto')}</label>
            <Select value={proto} ariaLabel={t('s_ch_syslog_proto')}
              options={[{ v: 'udp', label: 'UDP' }, { v: 'tcp', label: 'TCP' }]}
              onChange={setProto} />
          </div>
          <div style={{ width: 100 }}>
            <label htmlFor="ch-fac">{t('s_ch_syslog_facility')}</label>
            <input id="ch-fac" type="number" min={0} max={23} value={facility} onChange={(e) => setFacility(e.target.value)} />
          </div>
        </div>
      )}
      {name === 'email' && (
        <>
          <div style={{ display: 'flex', gap: 10, flexWrap: 'wrap', alignItems: 'flex-end' }}>
            <div style={{ minWidth: 180 }}>
              <label htmlFor="ch-ehost">{t('s_ch_email_host')}</label>
              <input id="ch-ehost" type="text" value={host} placeholder="smtp.example.com" onChange={(e) => setHost(e.target.value)} />
            </div>
            <div style={{ width: 100 }}>
              <label htmlFor="ch-eport">{t('s_ch_email_port')}</label>
              <input id="ch-eport" type="number" min={1} max={65535} value={port} onChange={(e) => setPort(e.target.value)} />
            </div>
            <div style={{ width: 150 }}>
              <label htmlFor="ch-eenc">{t('s_ch_email_enc')}</label>
              <Select value={encryption} ariaLabel={t('s_ch_email_enc')}
                options={[{ v: 'starttls', label: 'STARTTLS' }, { v: 'tls', label: 'TLS' }, { v: 'none', label: 'None' }]}
                onChange={setEncryption} />
            </div>
          </div>
          <label htmlFor="ch-euser">{t('s_ch_email_user')}</label>
          <input id="ch-euser" type="text" value={user} autoComplete="off" onChange={(e) => setUser(e.target.value)} />
          <label htmlFor="ch-epass">{t('s_ch_email_pass')}</label>
          <input id="ch-epass" type="password" value={pass} autoComplete="new-password"
            placeholder={info.token_set ? '••••••••' : ''} onChange={(e) => setPass(e.target.value)} />
          <p className="muted" style={{ marginTop: 2 }}>{t('s_ch_token_hint')}</p>
          <label htmlFor="ch-efrom">{t('s_ch_email_from')}</label>
          <input id="ch-efrom" type="text" value={from} placeholder="EasyZFS <easyzfs@example.com>" onChange={(e) => setFrom(e.target.value)} />
        </>
      )}
      {name === 'webhook' && (
        <>
          <label htmlFor="ch-whurl">{t('s_ch_webhook_url')}</label>
          <input id="ch-whurl" type="text" value={url} autoComplete="off"
            placeholder="https://hooks.example.com/easyzfs" onChange={(e) => setUrl(e.target.value)} />
        </>
      )}
      {err && <p className="form-err" role="alert" style={{ marginTop: 8 }}>{err}</p>}
      <div className="m-actions" style={{ justifyContent: 'flex-start', marginTop: 10 }}>
        <button className="btn sm primary" disabled={busy} onClick={() => { void save(); }}>{t('save')}</button>
        {info.configured && (
          <button className="btn sm danger" disabled={busy} onClick={() => { void disable(); }}>{t('s_ch_disable')}</button>
        )}
        <button className="btn sm" disabled={busy} onClick={onDone}>{t('cancel')}</button>
      </div>
    </div>
  );
}

// Tarjeta "Canales de alerta" (zona admin): estado de cada canal (sin
// secretos), configuración en caliente y botón Probar.
function ChannelsPanel() {
  const { t, notify } = useApp();
  const [ch, setCh] = useState<ChannelsStatus | null>(null);
  const [editing, setEditing] = useState<ChannelName | null>(null);
  const [busy, setBusy] = useState<ChannelName | null>(null);
  const [result, setResult] = useState<{ ok: boolean; text: string } | null>(null);

  const load = () => getProvider().getChannels().then(setCh).catch(() => {});
  useEffect(() => {
    let alive = true;
    getProvider().getChannels().then((c) => alive && setCh(c)).catch(() => {});
    return () => { alive = false; };
  }, []);

  const test = async (name: ChannelName) => {
    setBusy(name);
    setResult(null);
    try {
      await getProvider().testChannel(name);
      setResult({ ok: true, text: t('s_ch_ok') });
      notify(t('s_ch_ok'), 'ok');
    } catch (e) {
      const m = errorMessage(e, t);
      setResult({ ok: false, text: m });
      notify(m, 'err');
    }
    setBusy(null);
  };

  const done = () => { setEditing(null); setResult(null); void load(); };

  if (!ch) return <Spinner label={t('loading')} />;
  return (
    <div className="card pad admin-card">
      <h3 className="cardtitle">{t('s_ch_title')}</h3>
      <p className="muted">{t('s_ch_d')}</p>
      <div>
        {CHANNEL_ORDER.map((name) => {
          const info = ch[name];
          return (
            <div key={name}>
              <div className="rowitem">
                <div className="grow">
                  <div className="t1" style={{ fontSize: 14 }}>{t(CHANNEL_LABEL[name])}</div>
                  <div className="t2">{info.editable ? channelDetail(name, info, t) : t('s_ch_env_only')}</div>
                </div>
                <Badge tone={info.configured ? 'ok' : 'warn'} dot={false}>
                  {info.configured ? t('s_ch_on') : t('s_ch_off')}
                </Badge>
                {CHANNEL_TESTABLE.includes(name) && (
                  <button className="btn sm" disabled={!info.configured || busy === name}
                    onClick={() => { void test(name); }}>
                    {busy === name ? t('s_ch_testing') : t('s_ch_test')}
                  </button>
                )}
                {info.editable && (
                  <button className="btn sm" aria-expanded={editing === name}
                    onClick={() => setEditing(editing === name ? null : name)}>
                    {t('s_ch_edit')}
                  </button>
                )}
              </div>
              {editing === name && <ChannelForm name={name} info={info} onDone={done} />}
            </div>
          );
        })}
      </div>
      {result && (
        <p className={result.ok ? 'thresh-msg' : 'form-err'} role={result.ok ? 'status' : 'alert'}
          style={{ marginTop: 8 }}>{result.text}</p>
      )}
    </div>
  );
}

export default function Settings() {
  const { t, family, mode, themeEff, setFamily, setMode, isAdmin, user, refresh, reloadUser, logout, setLang, notify } = useApp();
  const { openModal } = useModal();
  const [settings, setSettings] = useState<SettingsData | null>(null);
  const [users, setUsers] = useState<Awaited<ReturnType<ReturnType<typeof getProvider>['getUsers']>> | null>(null);
  const [version, setVersion] = useState<Awaited<ReturnType<ReturnType<typeof getProvider>['getVersion']>> | null>(null);
  const [msg, setMsg] = useState('');
  const [err, setErr] = useState('');
  const [accent, setAccentState] = useState<string>(getAccent());
  const [density, setDensityState] = useState<Density>(getDensity());
  const [reduceMotion, setReduceMotionState] = useState(getReduceMotion());
  const [installEvt, setInstallEvt] = useState<BeforeInstallPromptEvent | null>(null);
  const [installed, setInstalled] = useState(isStandalone());
  const [adminPanel, setAdminPanel] = useState<'backup' | 'users' | 'apikeys' | 'channels' | null>(null);
  // Snapshot de los umbrales guardados (para resaltar los campos modificados
  // y limpiar la marca al guardar) + mensaje de feedback local de la tarjeta.
  const [threshSaved, setThreshSaved] = useState<{ cap_warn_pct: number; cap_crit_pct: number; disk_temp_c: number } | null>(null);
  const [threshMsg, setThreshMsg] = useState('');
  const [threshErr, setThreshErr] = useState('');

  // El acento seleccionado depende del tema: en familias no-classic vale el
  // override por familia (easyzfs-accent-<familia>) o ninguno (usa el acento
  // identidad de su CSS); en clásico vale el acento guardado.
  useEffect(() => {
    setAccentState(family !== 'classic' ? currentFamilyAccentId(family) : getAccent());
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [family]);

  useEffect(() => {
    let alive = true;
    getProvider().getSettings().then((s) => {
      alive && setSettings(s);
      // Primera carga: lo que viene del servidor es la base de "guardado".
      setThreshSaved((cur) => cur ?? {
        cap_warn_pct: s.cap_warn_pct, cap_crit_pct: s.cap_crit_pct, disk_temp_c: s.disk_temp_c,
      });
    }).catch(() => {});
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
      notify(t('toast_saved'), 'ok');
    } catch (e) { const m = errorMessage(e, t); setErr(m); notify(m, 'err'); }
  };

  // Guardado de la tarjeta Datos y umbrales: feedback local (mensaje dentro de
  // la tarjeta) y actualiza el snapshot para limpiar la marca de "modificado".
  const saveThresh = async () => {
    if (!settings) return;
    setThreshMsg(''); setThreshErr('');
    try {
      await getProvider().putSettings(settings);
      setThreshSaved({ cap_warn_pct: settings.cap_warn_pct, cap_crit_pct: settings.cap_crit_pct, disk_temp_c: settings.disk_temp_c });
      setThreshMsg(t('saved_ok'));
      notify(t('toast_saved'), 'ok');
    } catch (e) { const m = errorMessage(e, t); setThreshErr(m); notify(m, 'err'); }
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

  const themeOpts: { v: ThemeFamily; label: string }[] = [
    { v: 'classic', label: t('s_theme_clasico') },
    { v: 'modern', label: t('s_theme_modern') },
    { v: 'phosphor', label: t('s_theme_phosphor') },
    { v: 'brutalist', label: t('s_theme_brutalist') },
  ];
  const modeOpts: { v: ThemeMode; label: string }[] = [
    { v: 'system', label: t('s_theme_auto') },
    { v: 'light', label: t('s_theme_light') },
    { v: 'dark', label: t('s_theme_dark') },
  ];
  // El modo (claro/oscuro/sistema) solo aplica a familias con variante.
  const modeApplies = family === 'classic' || family === 'modern';

  return (
    <div className="view">
      {/* ---- Datos y umbrales (solo admin, UNA línea) ---- */}
      {isAdmin && (
        <div className="card pad admin-card">
          <h3 className="cardtitle">{t('s_data_thresh')}</h3>
          <div className="thresh-line">
            <div className="tf">
              <label htmlFor="th-warn">{t('s_cap_warn')}</label>
              <input id="th-warn" type="number" value={settings.cap_warn_pct}
                className={threshSaved && settings.cap_warn_pct !== threshSaved.cap_warn_pct ? 'dirty' : ''}
                onChange={(e) => { setSettings({ ...settings, cap_warn_pct: +e.target.value }); setThreshMsg(''); }} />
            </div>
            <div className="tf">
              <label htmlFor="th-crit">{t('s_cap_crit')}</label>
              <input id="th-crit" type="number" value={settings.cap_crit_pct}
                className={threshSaved && settings.cap_crit_pct !== threshSaved.cap_crit_pct ? 'dirty' : ''}
                onChange={(e) => { setSettings({ ...settings, cap_crit_pct: +e.target.value }); setThreshMsg(''); }} />
            </div>
            <div className="tf">
              <label htmlFor="th-temp">{t('s_temp')}</label>
              <input id="th-temp" type="number" value={settings.disk_temp_c}
                className={threshSaved && settings.disk_temp_c !== threshSaved.disk_temp_c ? 'dirty' : ''}
                onChange={(e) => { setSettings({ ...settings, disk_temp_c: +e.target.value }); setThreshMsg(''); }} />
            </div>
            {!threshOk && <p className="form-err" role="alert">{t('s_thresh_invalid')}</p>}
            <div className="m-actions">
              <button className="btn primary" disabled={!threshOk} onClick={() => { void saveThresh(); }}>{t('save')}</button>
            </div>
          </div>
          {threshMsg && <p className="thresh-msg" role="status">{threshMsg}</p>}
          {threshErr && <p className="form-err" role="alert" style={{ marginTop: 8 }}>{threshErr}</p>}
        </div>
      )}

      {/* ---- Apariencia (horizontal: tema izq + controles der) ---- */}
      <div className="card pad">
        <h3 className="cardtitle">{t('s_appear')}</h3>
        <div className="aprow">
          {/* Tema (familia) */}
          <div className="ap-field ap-theme">
            <span className="lbl">{t('s_theme')}</span>
            <Select value={family} ariaLabel={t('s_theme')}
              onChange={(f) => setFamily(f)}
              options={themeOpts} />
          </div>

          {/* Modo (solo si la familia tiene variante claro/oscuro) */}
          {modeApplies && (
            <div className="ap-field ap-mode">
              <span className="lbl">{t('s_theme_mode')}</span>
              <Seg value={mode} ariaLabel={t('s_theme_mode')}
                onChange={(m) => setMode(m)}
                options={modeOpts} />
            </div>
          )}

          {/* Acento de color — cada familia ofrece su propia paleta */}
          <div className="ap-accent">
            <span className="lbl">{t('s_accent')}</span>
            <div className="swatches" role="group" aria-label={t('s_accent')}>
              {FAMILY_ACCENTS[family].options.map((opt) => (
                <button key={opt.v} type="button" title={t(opt.labelKey as I18nKey)} aria-label={t(opt.labelKey as I18nKey)}
                  className={`swatch${accent === opt.v ? ' sel' : ''}`}
                  aria-pressed={accent === opt.v}
                  onClick={() => { setAccent(opt.v); setAccentState(opt.v); }}>
                  <span style={{ background: opt[themeEff][0] }} />
                </button>
              ))}
            </div>
          </div>

          {/* Activar animaciones */}
          <div className="ap-anim">
            <span className="lbl">{t('s_rm')}</span>
            <Switch checked={!reduceMotion} ariaLabel={t('s_rm')}
              onChange={(v) => { setReduceMotion(!v); setReduceMotionState(!v); }} />
          </div>

          {/* Densidad */}
          <div className="ap-field ap-density">
            <span className="lbl">{t('s_density')}</span>
            <Seg value={density} ariaLabel={t('s_density')}
              onChange={(d) => { setDensity(d); setDensityState(d); }}
              options={[
                { v: 'cozy', label: t('s_density_cozy') },
                { v: 'compact', label: t('s_density_compact') },
              ]} />
          </div>
        </div>
      </div>

      {/* ---- Mi perfil (barra compacta sin título) ---- */}
      <ProfileCard />

      {/* ---- Zona de administración (solo admin) en UNA fila ---- */}
      {isAdmin && (
        <div className="admin-bar">
          <div className="ab-row">
            <div className="ab-title">
              <IconShield size={18} />
              <span>{t('s_admin_zone')}</span>
            </div>
            <span className="ab-sep" />

            {/* 1. Comprobar actualizaciones (widget inline) */}
            <UpdateCheckRow version={version?.version} />

            {/* 2. Respaldos (desplegable) */}
            <button type="button" aria-expanded={adminPanel === 'backup'}
              onClick={() => setAdminPanel(adminPanel === 'backup' ? null : 'backup')}
              className={`ab-btn${adminPanel === 'backup' ? ' on' : ''}`}>
              <IconData size={15} />
              <span className="hidden-sm">{t('bk_title')}</span>
              <IconChev className="chev" />
            </button>

            {/* 3. Usuarios (desplegable) */}
            <button type="button" aria-expanded={adminPanel === 'users'}
              onClick={() => setAdminPanel(adminPanel === 'users' ? null : 'users')}
              className={`ab-btn${adminPanel === 'users' ? ' on' : ''}`}>
              <IconUser size={15} />
              <span className="hidden-sm">{t('s_users')}</span>
              <IconChev className="chev" />
            </button>

            {/* 3b. API keys de solo lectura (desplegable) */}
            <button type="button" aria-expanded={adminPanel === 'apikeys'}
              onClick={() => setAdminPanel(adminPanel === 'apikeys' ? null : 'apikeys')}
              className={`ab-btn${adminPanel === 'apikeys' ? ' on' : ''}`}>
              <IconCode size={15} />
              <span className="hidden-sm">{t('s_apikeys')}</span>
              <IconChev className="chev" />
            </button>

            {/* 3c. Canales de alerta (desplegable, #134) */}
            <button type="button" aria-expanded={adminPanel === 'channels'}
              onClick={() => setAdminPanel(adminPanel === 'channels' ? null : 'channels')}
              className={`ab-btn${adminPanel === 'channels' ? ' on' : ''}`}>
              <IconSend size={15} />
              <span className="hidden-sm">{t('s_ch_title')}</span>
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
                        options={[{ v: 'auto', label: t('s_lang_auto') }, { v: 'es', label: 'Español' }, { v: 'en', label: 'English' }]}
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
          {adminPanel === 'apikeys' && (
            <div className="ab-panel">
              <APIKeysPanel />
            </div>
          )}
          {adminPanel === 'channels' && (
            <div className="ab-panel">
              <ChannelsPanel />
            </div>
          )}
        </div>
      )}

      {/* ---- Acerca de (patrón Keynest: logo+desc izq, tiles bajos der, una línea de versión, botones) ---- */}
      {version && (
        <div className="card pad st-about">
          <h3 className="cardtitle">{t('s_about')}</h3>

          {/* Fila 1: logo + nombre + descripción (izq) · tiles de enlaces bajos (der) */}
          <div className="about-top">
            <div className="about-id">
              <div className="logo"><Logo size={40} /></div>
              <div className="about-idtxt">
                <div style={{ fontWeight: 800, fontSize: 16 }}>{version?.name ?? 'EasyZFS'}</div>
                <div className="muted" style={{ marginTop: 2 }}>{t('s_about_d')}</div>
              </div>
            </div>
            <div className="about-tiles">
              <a className="abouttile" href={REPO_URL} target="_blank" rel="noreferrer">
                <IconCode size={14} /><span>{t('ab_code')}</span>
              </a>
              <a className="abouttile" href="https://easyzfs.cloudless.club" target="_blank" rel="noreferrer">
                <IconList size={14} /><span>{t('ab_chlog')}</span>
              </a>
              <a className="abouttile" href="https://ko-fi.com/gnacho" target="_blank" rel="noreferrer">
                <IconHeart size={14} /><span>{t('ab_kofi')}</span>
              </a>
              <a className="abouttile" href="https://cloudless.club" target="_blank" rel="noreferrer">
                <IconShield size={14} /><span>{t('ab_priv')}</span>
              </a>
            </div>
          </div>

          {/* Fila 2: versión · licencia · runtime en UNA línea sin recuadros */}
          <p className="about-meta mono">v{version.version} · AGPL-3.0 · {version.go} {version.os_arch}</p>

          {/* Botones de acción: instalar PWA + comprobar actualizaciones (admin) */}
          <div className="about-actions">
            {(installed || installEvt || isIOS()) && (
              installed
                ? <Badge tone="ok" dot={false}>{t('ab_installed')}</Badge>
                : isIOS()
                  ? <span className="muted" style={{ fontSize: 12 }}>{t('ab_install_ios')}</span>
                  : installEvt
                    ? <button className="btn sm primary" onClick={() => { void installEvt.prompt(); }}>{t('ab_install_btn')}</button>
                    : null
            )}
            {isAdmin && <UpdateCheckRow version={version?.version} />}
          </div>
        </div>
      )}

      {msg && <p style={{ fontSize: 13, color: 'var(--ok)', fontWeight: 600, marginTop: 12 }} role="status">{msg}</p>}
      {err && <p className="form-err" role="alert">{err}</p>}

    </div>
  );
}
