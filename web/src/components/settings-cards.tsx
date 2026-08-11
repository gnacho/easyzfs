// settings-cards.tsx — Tarjetas de Ajustes portadas del asset canónico de la
// skill webapp-shell (Deltos v1.9.2): Card, CheckToggle, AppearanceCard y
// ProfileCard. Estructura y clases EXACTAS del asset; se adaptan los iconos,
// el i18n (useApp().t) y el tema (useApp + ui/theme) a las convenciones de
// EasyZFS. No usar framer-motion: entradas sin animación.
import { useEffect, useState, type ReactNode } from 'react';
import { useApp } from '../ui/store';
import { ACCENTS, getAccent, setAccent, getDensity, setDensity, getReduceMotion, setReduceMotion } from '../ui/theme';
import type { AccentId, Density, ThemeMode } from '../ui/theme';
import { IconBell, IconCheck, IconLock, IconLogout, IconMail, IconMonitor, IconMoon, IconPencil, IconSun, IconUser, IconX } from './icons';

// APP_SLUG: prefijo de localStorage (coherente con el resto de EasyZFS)
export const APP_SLUG = 'easyzfs';

// Perfil de usuario mínimo que consume ProfileCard (slice de SessionUser)
export interface ProfileUser {
  username: string;
  display_name?: string | null;
  email?: string | null;
  language?: string | null;
  role?: string;
}

/* ---------- CheckToggle (check en esquina o switch deslizante) ---------- */
export interface CheckToggleProps {
  checked: boolean;
  onChange: (checked: boolean) => void;
  label: string;
  icon?: ReactNode;
  disabled?: boolean;
  className?: string;
  size?: 'sm' | 'md';
  variant?: 'check' | 'switch';
}

export function CheckToggle({ checked, onChange, label, icon, disabled, className, size = 'md', variant = 'check' }: CheckToggleProps) {
  const height = size === 'sm' ? 'h-8' : 'h-9';
  const base =
    'inline-flex items-center gap-2 rounded-xl border px-3 transition-colors shrink-0 ' + height;
  const tone = checked
    ? 'border-accent bg-accent-soft text-accent'
    : 'border-border bg-elevated text-text-secondary hover:bg-hover hover:text-text-primary';
  const cls = base + ' ' + tone + (disabled ? ' opacity-50 cursor-not-allowed' : '') + (className ? ' ' + className : '');

  if (variant === 'switch') {
    return (
      <button type="button" role="switch" aria-checked={checked} aria-label={label} disabled={disabled}
        onClick={() => onChange(!checked)} className={cls}>
        {icon}
        <span className="text-label font-medium">{label}</span>
        <span className={`relative ml-1 w-11 h-6 rounded-full shrink-0 transition-colors ${checked ? 'bg-accent' : 'bg-canvas border border-border'}`}>
          <span className={`absolute top-0.5 left-0.5 h-5 w-5 rounded-full bg-canvas shadow transition-transform ${checked ? 'translate-x-[22px]' : 'translate-x-0.5'}`} />
        </span>
      </button>
    );
  }

  return (
    <button type="button" role="switch" aria-checked={checked} aria-label={label} disabled={disabled}
      onClick={() => onChange(!checked)} className={cls}>
      {icon}
      <span className="text-label font-medium">{label}</span>
      {checked && (
        <span className="absolute -right-1 -top-1 flex h-4 w-4 items-center justify-center rounded-full bg-accent text-canvas shadow">
          <IconCheck size={12} />
        </span>
      )}
    </button>
  );
}

/* ---------- Tarjeta base ---------- */
export function Card({ title, children, className }: { title: string; children: ReactNode; className?: string }) {
  return (
    <section className={`rounded-2xl border border-border bg-surface p-4 md:p-6 ${className ?? ''}`}>
      <h2 className="font-display text-h2 text-text-primary">{title}</h2>
      <div className="mt-4 space-y-5">{children}</div>
    </section>
  );
}

/* ---------- Preview de tema con tokens reales ----------
   Reutiliza la estructura del asset: bloques pintados con las variables CSS
   reales de EasyZFS (--bg / --surface / --surface2 / --accent) scopeadas. */
function PreviewBlock({ useLight }: { useLight: boolean }) {
  return (
    <div className="flex w-1/2 flex-col p-1.5" style={{ backgroundColor: useLight ? '#f6f6f3' : '#0e1210' }}>
      <div className="mb-1 h-1.5 w-full rounded" style={{ backgroundColor: useLight ? '#e3e3dc' : '#2a332d' }} />
      <div className="flex flex-1 gap-1">
        <div className="w-1/4 rounded" style={{ backgroundColor: useLight ? '#e3e3dc' : '#2a332d' }} />
        <div className="flex flex-1 flex-col gap-1">
          <div className="h-2 w-3/4 rounded" style={{ backgroundColor: 'var(--accent)', opacity: 0.6 }} />
          <div className="h-4 flex-1 rounded" style={{ backgroundColor: useLight ? '#ffffff' : '#161b18' }} />
        </div>
      </div>
    </div>
  );
}

function ThemePreview({ variant }: { variant: ThemeMode }) {
  if (variant === 'auto') {
    return (
      <div className="flex h-[80px] w-full overflow-hidden rounded-lg border border-border">
        <PreviewBlock useLight={false} />
        <PreviewBlock useLight />
      </div>
    );
  }
  const light = variant === 'light';
  return (
    <div className="flex h-[80px] w-full flex-col rounded-lg border border-border p-1.5" style={{ backgroundColor: light ? '#f6f6f3' : '#0e1210' }}>
      <div className="mb-1 h-1.5 w-full rounded" style={{ backgroundColor: light ? '#e3e3dc' : '#2a332d' }} />
      <div className="flex flex-1 gap-1">
        <div className="w-1/4 rounded" style={{ backgroundColor: light ? '#e3e3dc' : '#2a332d' }} />
        <div className="flex flex-1 flex-col gap-1">
          <div className="h-2 w-3/4 rounded" style={{ backgroundColor: 'var(--accent)', opacity: 0.6 }} />
          <div className="h-4 flex-1 rounded" style={{ backgroundColor: light ? '#ffffff' : '#161b18' }} />
        </div>
      </div>
    </div>
  );
}

const THEME_OPTIONS: { value: ThemeMode; labelKey: string; icon: ReactNode }[] = [
  { value: 'dark', labelKey: 's_theme_dark', icon: <IconMoon size={14} /> },
  { value: 'light', labelKey: 's_theme_light', icon: <IconSun size={14} /> },
  { value: 'auto', labelKey: 's_theme_auto', icon: <IconMonitor size={14} /> },
];

/* ---------- Tarjeta Apariencia (canónica 10-Ago-2026) ----------
   Layout horizontal (≥sm): tiles de tema a la izquierda (50%), controles a
   la derecha (flex-1). Acento + Animaciones en la misma fila. */
export function AppearanceCard() {
  const { t, themeMode, setTheme } = useApp();
  const [accent, setAccentState] = useState<AccentId>(getAccent());
  const [density, setDensityState] = useState<Density>(getDensity());
  const [reduceMotion, setReduceMotionState] = useState(getReduceMotion());

  return (
    <Card title={t('s_appear')}>
      <div className="flex flex-col gap-4 sm:flex-row sm:gap-6">
        {/* Tiles de tema (máx 50% ancho) */}
        <div role="radiogroup" aria-label={t('s_theme')} className="grid grid-cols-3 gap-2 sm:w-1/2 sm:flex-shrink-0">
          {THEME_OPTIONS.map((opt) => {
            const active = themeMode === opt.value;
            return (
              <button key={opt.value} type="button" role="radio" aria-checked={active}
                onClick={() => setTheme(opt.value)}
                className={`group relative flex flex-col gap-2 rounded-xl border-2 p-2 transition-all ${active ? 'border-accent bg-accent/5' : 'border-border hover:border-accent/30'}`}>
                <ThemePreview variant={opt.value} />
                <div className="flex items-center justify-center gap-1.5">
                  <span className={active ? 'text-accent' : 'text-text-secondary'}>{opt.icon}</span>
                  <span className={`text-xs font-medium ${active ? 'text-accent' : 'text-text-secondary'}`}>{t(opt.labelKey as never)}</span>
                </div>
                {active && (
                  <span className="absolute -right-1 -top-1 flex h-5 w-5 items-center justify-center rounded-full bg-accent text-canvas">
                    <IconCheck size={12} />
                  </span>
                )}
              </button>
            );
          })}
        </div>

        {/* Controles: acento + animaciones en línea, densidad debajo */}
        <div className="flex flex-col gap-3 sm:flex-1">
          <div>
            <p className="mb-1.5 text-[11px] font-medium uppercase tracking-[0.08em] text-text-secondary">{t('s_accent')}</p>
            <div className="flex items-center gap-3">
              <div role="radiogroup" aria-label={t('s_accent')} className="flex items-center gap-2">
                {(Object.keys(ACCENTS) as AccentId[]).map((id) => {
                  const active = accent === id;
                  const [color, soft] = ACCENTS[id].light;
                  return (
                    <button key={id} type="button" role="radio" aria-checked={active}
                      aria-label={t(`acc_${id}` as never)} title={t(`acc_${id}` as never)}
                      onClick={() => { setAccent(id); setAccentState(id); }}
                      className={`relative flex h-7 w-7 items-center justify-center rounded-full transition-transform hover:scale-110 ${active ? 'ring-2 ring-accent ring-offset-2 ring-offset-[var(--surface)]' : ''}`}
                      style={{ backgroundColor: color }}>
                      {active && <IconCheck size={12} className="text-white" />}
                    </button>
                  );
                })}
              </div>
              <div className="ml-auto flex items-center gap-2">
                <span className="text-[13px] text-text-secondary">{t('s_rm')}</span>
                <button type="button" role="switch" aria-checked={!reduceMotion} onClick={() => { setReduceMotion(!reduceMotion); setReduceMotionState(!reduceMotion); }}
                  className={`relative h-5 w-9 rounded-full transition-colors shrink-0 ${!reduceMotion ? 'bg-accent' : 'bg-hover'}`}>
                  <span className={`absolute top-0.5 h-4 w-4 rounded-full bg-white shadow transition-transform ${!reduceMotion ? 'translate-x-[18px]' : 'translate-x-0.5'}`} />
                </button>
              </div>
            </div>
          </div>

          {/* Densidad */}
          <div>
            <p className="mb-1.5 text-[11px] font-medium uppercase tracking-[0.08em] text-text-secondary">{t('s_density')}</p>
            <div role="radiogroup" aria-label={t('s_density')} className="flex rounded-xl border border-border bg-elevated p-0.5">
              {(['cozy', 'compact'] as const).map((d) => (
                <button key={d} type="button" role="radio" aria-checked={density === d} onClick={() => { setDensity(d); setDensityState(d); }}
                  className={`h-8 flex-1 rounded-lg text-[13px] transition-colors ${density === d ? 'bg-surface shadow-soft font-semibold text-text-primary' : 'text-text-secondary hover:text-text-primary'}`}>
                  {t(d === 'cozy' ? 's_density_cozy' : 's_density_compact')}
                </button>
              ))}
            </div>
          </div>
        </div>
      </div>
    </Card>
  );
}

/* ---------- Tarjeta "Mi perfil" (canónica) ----------
   Barra horizontal compacta SIN título: avatar → nombre editable + rol →
   email (botón circular) + idioma + contraseña + notificaciones → logout
   rojo (ml-auto). Texto de botones oculto en móvil (solo icono). */
export interface ProfileCardProps {
  user: ProfileUser;
  isDemo?: boolean;
  avatar?: ReactNode;
  roleLabel?: string;
  onUpdateProfile: (changes: { display_name?: string | null; email?: string | null; language?: string | null }) => Promise<ProfileUser>;
  onLogout: () => void;
  onLanguageChange?: (lang: string) => void;
  passwordForm?: ReactNode;
  notifications?: ReactNode;
  className?: string;
}

export function ProfileCard({
  user,
  isDemo = false,
  avatar,
  roleLabel,
  onUpdateProfile,
  onLogout,
  onLanguageChange,
  passwordForm,
  notifications,
  className,
}: ProfileCardProps) {
  const { t } = useApp();
  const [showPwd, setShowPwd] = useState(false);
  const [showNotif, setShowNotif] = useState(false);
  const [langError, setLangError] = useState<string | null>(null);

  const [editingName, setEditingName] = useState(false);
  const [nameDraft, setNameDraft] = useState(user.display_name || user.username);
  const [nameBusy, setNameBusy] = useState(false);
  const [nameError, setNameError] = useState<string | null>(null);

  const [editingEmail, setEditingEmail] = useState(false);
  const [emailDraft, setEmailDraft] = useState(user.email || '');
  const [emailBusy, setEmailBusy] = useState(false);
  const [emailError, setEmailError] = useState<string | null>(null);

  useEffect(() => { if (!editingName) setNameDraft(user.display_name || user.username); }, [user.display_name, user.username, editingName]);
  useEffect(() => { if (!editingEmail) setEmailDraft(user.email || ''); }, [user.email, editingEmail]);

  const displayName = user.display_name || user.username;
  const fallbackRole = roleLabel ?? (user.role ? t(`mu_r_${user.role}` as never) : undefined);

  const changeLanguage = async (lang: string) => {
    setLangError(null);
    onLanguageChange?.(lang);
    try {
      await onUpdateProfile({ language: lang });
    } catch {
      setLangError(t('s_email_d'));
    }
  };

  const saveName = async () => {
    const value = nameDraft.trim();
    if (value === (user.display_name || '')) { setEditingName(false); return; }
    setNameBusy(true); setNameError(null);
    try {
      await onUpdateProfile({ display_name: value || null });
      setEditingName(false);
    } catch {
      setNameError(t('s_displayname_d'));
    } finally {
      setNameBusy(false);
    }
  };

  const saveEmail = async () => {
    const value = emailDraft.trim();
    if (value === (user.email || '')) { setEditingEmail(false); return; }
    setEmailBusy(true); setEmailError(null);
    try {
      await onUpdateProfile({ email: value || null });
      setEditingEmail(false);
    } catch {
      setEmailError(t('s_email_d'));
    } finally {
      setEmailBusy(false);
    }
  };

  const cancelName = () => { setNameDraft(user.display_name || user.username); setNameError(null); setEditingName(false); };
  const cancelEmail = () => { setEmailDraft(user.email || ''); setEmailError(null); setEditingEmail(false); };

  const actionBtnCls =
    'inline-flex h-9 items-center gap-1.5 rounded-lg border border-border bg-elevated px-2.5 sm:px-3 text-body font-medium text-text-secondary transition-colors hover:bg-surface hover:text-text-primary';
  const actionTextCls = 'hidden sm:inline';

  return (
    <section className={`rounded-2xl border border-border bg-surface p-5 ${className ?? ''}`}>
      <div className="flex flex-wrap items-center gap-3 sm:gap-4 min-w-0">
        {/* Avatar */}
        {avatar ?? (
          <div className="flex h-11 w-11 shrink-0 items-center justify-center rounded-full bg-amber-500/12 text-amber-500">
            <IconUser size={20} aria-hidden="true" />
          </div>
        )}

        {/* Nombre + privilegios */}
        <div className="min-w-0 flex-1">
          {editingName ? (
            <div className="flex items-center gap-2">
              <input
                type="text" value={nameDraft}
                onChange={(e) => setNameDraft(e.target.value)}
                onKeyDown={(e) => { if (e.key === 'Enter') void saveName(); if (e.key === 'Escape') cancelName(); }}
                disabled={nameBusy}
                className="h-9 w-full rounded-xl border border-border bg-elevated px-3 py-1 text-body outline-none focus:border-accent"
                placeholder={user.username} autoFocus
              />
              <button type="button" onClick={() => void saveName()} disabled={nameBusy} aria-label={t('save')}
                className="inline-flex h-9 w-9 items-center justify-center rounded-lg bg-accent text-canvas transition-colors hover:brightness-110 disabled:opacity-50">
                <IconCheck size={16} />
              </button>
              <button type="button" onClick={cancelName} disabled={nameBusy} aria-label={t('cancel')}
                className="inline-flex h-9 w-9 items-center justify-center rounded-lg border border-border bg-elevated text-text-secondary transition-colors hover:bg-surface hover:text-text-primary">
                <IconX size={16} />
              </button>
            </div>
          ) : (
            <button type="button" onClick={() => setEditingName(true)} className="group flex items-center gap-1.5 min-w-0" title={t('s_displayname')}>
              <p className="text-base font-semibold leading-tight truncate">{displayName}</p>
              <IconPencil size={14} className="text-text-muted opacity-0 group-hover:opacity-100 transition-opacity" aria-hidden="true" />
            </button>
          )}
          {fallbackRole && <p className="text-label text-text-muted leading-tight mt-0.5">{fallbackRole}</p>}
          {nameError && <p role="alert" className="text-label text-danger mt-1">{nameError}</p>}
        </div>

        {/* Email + acciones agrupadas */}
        <div className="flex flex-wrap items-center gap-2 sm:gap-3 min-w-0">
          {editingEmail ? (
            <div className="flex items-center gap-2">
              <input
                type="email" value={emailDraft}
                onChange={(e) => setEmailDraft(e.target.value)}
                onKeyDown={(e) => { if (e.key === 'Enter') void saveEmail(); if (e.key === 'Escape') cancelEmail(); }}
                disabled={emailBusy}
                className="h-9 w-[180px] sm:w-[220px] rounded-xl border border-border bg-elevated px-3 py-1 text-body outline-none focus:border-accent"
                placeholder={t('s_email')} autoFocus
              />
              <button type="button" onClick={() => void saveEmail()} disabled={emailBusy} aria-label={t('save')}
                className="inline-flex h-9 w-9 items-center justify-center rounded-lg bg-accent text-canvas transition-colors hover:brightness-110 disabled:opacity-50">
                <IconCheck size={16} />
              </button>
              <button type="button" onClick={cancelEmail} disabled={emailBusy} aria-label={t('cancel')}
                className="inline-flex h-9 w-9 items-center justify-center rounded-lg border border-border bg-elevated text-text-secondary transition-colors hover:bg-surface hover:text-text-primary">
                <IconX size={16} />
              </button>
            </div>
          ) : (
            <button type="button" onClick={() => setEditingEmail(true)}
              className={`inline-flex h-9 w-9 items-center justify-center rounded-full transition-colors ${user.email ? 'bg-amber-500/10 text-amber-500 hover:bg-amber-500/20' : 'border border-border bg-elevated text-text-muted hover:bg-surface hover:text-text-primary'}`}
              title={user.email || t('s_email')} aria-label={user.email ? t('s_email') : t('s_email_d')}>
              <IconMail size={16} />
            </button>
          )}

          {/* Idioma — icono en móvil, select completo en desktop */}
          <select
            id="profile-lang"
            value={user.language ?? 'auto'}
            onChange={(e) => void changeLanguage(e.target.value)}
            className="hidden sm:inline h-9 w-[120px] shrink-0 rounded-lg border border-border bg-elevated px-2 text-body text-text-primary outline-none focus:border-accent">
            <option value="auto">🌐 {t('s_lang_auto')}</option>
            <option value="es">🇪🇸 Español</option>
            <option value="en">🇬🇧 English</option>
          </select>

          {/* Contraseña */}
          {!isDemo && passwordForm && (
            <button type="button" aria-expanded={showPwd} onClick={() => setShowPwd((v) => !v)} className={actionBtnCls} title={t('s_mypass')}>
              <IconLock size={16} aria-hidden="true" />
              <span className={actionTextCls}>{t('s_mypass')}</span>
            </button>
          )}

          {/* Notificaciones */}
          {notifications && (
            <button type="button" aria-expanded={showNotif} onClick={() => setShowNotif((v) => !v)} className={actionBtnCls} title={t('s_notifs')}>
              <IconBell size={16} aria-hidden="true" />
              <span className={actionTextCls}>{t('s_notifs')}</span>
            </button>
          )}
        </div>

        {/* Cerrar sesión — siempre a la derecha, rojo; texto solo en ≥sm */}
        <button type="button" onClick={() => void onLogout()}
          className="ml-auto inline-flex h-9 shrink-0 items-center gap-1.5 rounded-lg border border-danger/30 bg-danger/10 px-2.5 sm:px-3 text-body font-medium text-danger transition-colors hover:bg-danger/15">
          <IconLogout size={16} aria-hidden="true" />
          <span className={actionTextCls}>{isDemo ? t('demobar_exit') : t('logout')}</span>
        </button>
      </div>

      {emailError && <p role="alert" className="text-body font-medium text-danger mt-3">{emailError}</p>}
      {langError && <p role="alert" className="text-body font-medium text-danger mt-3">{langError}</p>}

      {showPwd && !isDemo && passwordForm && (
        <div className="mt-4 border-t border-border pt-4">{passwordForm}</div>
      )}

      {showNotif && notifications && (
        <div className="mt-4 border-t border-border pt-4">{notifications}</div>
      )}
    </section>
  );
}

