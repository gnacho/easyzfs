// TwoFAPanel — gestión de la verificación en dos pasos (TOTP) desde Mi perfil.
// Estados: carga → (off | setup con QR → confirm → activo con recovery codes).
import { useCallback, useEffect, useState } from 'react';
import { useApp } from '../ui/store';
import { getProvider } from '../data';
import { errorMessage } from '../ui/store';
import type { TwoFARecovery, TwoFASetup } from '../data/types';
import { Badge } from './ui';
import { IconCheck, IconRefresh, IconShield } from './icons';

type Phase = 'loading' | 'off' | 'setup' | 'confirm' | 'active';

export function TwoFAPanel() {
  const { t } = useApp();
  const [phase, setPhase] = useState<Phase>('loading');
  const [setup, setSetup] = useState<TwoFASetup | null>(null);
  const [recovery, setRecovery] = useState<TwoFARecovery | null>(null);
  const [codes, setCodes] = useState<string[]>([]);
  const [code, setCode] = useState('');
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState('');
  const [err, setErr] = useState('');
  const [copied, setCopied] = useState(false);

  const loadStatus = useCallback(async () => {
    setErr('');
    try {
      const st = await getProvider().get2FAStatus();
      setPhase(st.enabled ? 'active' : 'off');
      if (st.enabled) {
        try {
          const c = await getProvider().regenerateRecoveryCodes();
          setCodes(c.codes);
        } catch { /* sin códigos: no bloquear el estado activo */ }
      }
    } catch {
      setPhase('off');
    }
  }, []);

  useEffect(() => { void loadStatus(); }, [loadStatus]);

  const startSetup = async () => {
    setBusy(true); setErr('');
    try {
      const s = await getProvider().setup2FA();
      setSetup(s);
      setPhase('confirm');
      setMsg(t('s_2fa_setup_d'));
    } catch (e) { setErr(errorMessage(e, t)); }
    setBusy(false);
  };

  const confirmSetup = async (ev: React.FormEvent) => {
    ev.preventDefault();
    setBusy(true); setErr('');
    try {
      const rec = await getProvider().confirm2FA(code.trim());
      setRecovery(rec);
      setCodes(rec.codes);
      setPhase('active');
      setMsg('');
      setCode('');
    } catch (e) { setErr(errorMessage(e, t)); }
    setBusy(false);
  };

  const disable = async (ev: React.FormEvent) => {
    ev.preventDefault();
    setBusy(true); setErr('');
    try {
      await getProvider().disable2FA(code.trim());
      setPhase('off'); setCode(''); setSetup(null); setRecovery(null); setCodes([]);
      setMsg('');
    } catch (e) { setErr(errorMessage(e, t)); }
    setBusy(false);
  };

  const regen = async () => {
    if (!window.confirm(t('s_2fa_regen_warn'))) return;
    setBusy(true); setErr('');
    try {
      const rec = await getProvider().regenerateRecoveryCodes();
      setRecovery(rec);
      setCodes(rec.codes);
    } catch (e) { setErr(errorMessage(e, t)); }
    setBusy(false);
  };

  const copySecret = async () => {
    if (!setup) return;
    try {
      await navigator.clipboard.writeText(setup.secret);
      setCopied(true);
      setTimeout(() => setCopied(false), 1600);
    } catch { /* clipboard no disponible (http): el usuario puede copiar a mano */ }
  };

  return (
    <div>
      <p className="muted">{t('s_2fa_d')}</p>

      {phase === 'loading' && <p className="muted">{t('s_2fa_loading')}</p>}

      {phase === 'off' && (
        <div style={{ display: 'flex', gap: 9, alignItems: 'center', flexWrap: 'wrap', marginTop: 10 }}>
          <Badge tone="info" dot={false}>{t('s_2fa_off')}</Badge>
          <button className="btn primary sm" disabled={busy} onClick={() => void startSetup()}>
            <IconShield size={15} aria-hidden="true" /> {t('s_2fa_setup')}
          </button>
        </div>
      )}

      {phase === 'confirm' && setup && (
        <div style={{ marginTop: 12, display: 'flex', flexDirection: 'column', gap: 10 }}>
          {setup.qr ? (
            <img src={setup.qr} alt={t('s_2fa_setup')}
              style={{ width: 200, height: 200, borderRadius: 10, alignSelf: 'center', background: '#fff', padding: 8 }} />
          ) : (
            <p className="muted">{t('s_2fa_setup_d')}</p>
          )}
          <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
            <span className="muted" style={{ fontSize: 12.5 }}>{t('s_2fa_secret_lbl')}:</span>
            <code style={{ fontSize: 12.5, wordBreak: 'break-all' }}>{setup.secret}</code>
            <button type="button" className="btn sm" onClick={() => void copySecret()}
              title={t('s_2fa_copy')} aria-label={t('s_2fa_copy')}>
              {copied ? <IconCheck size={14} /> : <span>{t('s_2fa_copy')}</span>}
            </button>
          </div>
          {copied && <span style={{ fontSize: 12, color: 'var(--ok)' }}>{t('s_2fa_copied')}</span>}
          <form onSubmit={(e) => void confirmSetup(e)}>
            <label htmlFor="2fa-confirm">{t('s_2fa_confirm')}</label>
            <input id="2fa-confirm" inputMode="numeric" autoComplete="one-time-code" autoFocus
              value={code} onChange={(e) => setCode(e.target.value)} placeholder="000000" required />
            <div className="m-actions">
              <button type="submit" className="btn primary" disabled={busy || code.trim().length < 6}>
                {t('s_2fa_confirm')}
              </button>
            </div>
          </form>
        </div>
      )}

      {phase === 'active' && (
        <div style={{ marginTop: 10, display: 'flex', flexDirection: 'column', gap: 10 }}>
          <div style={{ display: 'flex', gap: 9, alignItems: 'center', flexWrap: 'wrap' }}>
            <Badge tone="ok" dot={false}>{t('s_2fa_on')}</Badge>
            <span className="muted">{t('s_2fa_recovery_d')}</span>
            <button className="btn sm" onClick={() => void regen()} disabled={busy} title={t('s_2fa_regen')}>
              <IconRefresh size={14} aria-hidden="true" /> {t('s_2fa_regen')}
            </button>
          </div>

          <div style={{ borderTop: '1px solid var(--border)', paddingTop: 10 }}>
            <b style={{ fontSize: 13 }}>{t('s_2fa_recovery_title')}</b>
            {codes.length > 0 && (
              <ul style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(150px, 1fr))', gap: 4, margin: '8px 0', padding: 0, listStyle: 'none' }}>
                {codes.map((c) => (
                  <li key={c}><code style={{ fontSize: 12.5 }}>{c}</code></li>
                ))}
              </ul>
            )}
            {recovery && (
              <div className="m-actions" style={{ marginTop: 4 }}>
                <button className="btn" disabled={busy}
                  onClick={() => { setRecovery(null); setCodes([]); }}>
                  {t('s_2fa_recovery_done')}
                </button>
              </div>
            )}
          </div>

          <form onSubmit={(e) => void disable(e)}
            style={{ borderTop: '1px solid var(--border)', paddingTop: 10 }}>
            <p className="muted" style={{ marginBottom: 6 }}>{t('s_2fa_disable_d')}</p>
            <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
              <input inputMode="numeric" autoComplete="one-time-code" value={code}
                onChange={(e) => setCode(e.target.value)} placeholder="000000"
                style={{ maxWidth: 140 }} required />
              <button type="submit" className="btn sm" disabled={busy || code.trim().length < 6}>
                {t('s_2fa_disable')}
              </button>
            </div>
          </form>
        </div>
      )}

      {msg && <p style={{ fontSize: 13, color: 'var(--ok)', fontWeight: 600, marginTop: 10 }} role="status">{msg}</p>}
      {err && <p className="form-err" role="alert" style={{ marginTop: 10 }}>{err}</p>}
    </div>
  );
}
