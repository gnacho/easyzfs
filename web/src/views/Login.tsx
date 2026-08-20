// Pantalla de login. El formulario hace SIEMPRE login real (POST /api/login);
// si el usuario tiene 2FA activo, el backend responde twofa_required y se
// muestra el campo de código TOTP (segundo paso, POST /api/login/2fa).
// El modo demo es una sesión de entrada aparte (botón secundario, sin backend).
// El botón demo solo aparece si el admin lo tiene habilitado
// (GET /api/public/demo → settings.demo_enabled).
// El form usa method="post" + name/autocomplete para que el navegador pueda
// guardar las credenciales; el checkbox "Recordar contraseña" activa o
// desactiva el autocompletado.
import { useEffect, useState } from 'react';
import { useApp } from '../ui/store';
import { ApiError } from '../data/types';
import { fetchPublicDemo } from '../data/http';
import { Logo } from '../components/icons';

export default function Login() {
  const { t, login, login2FA, enterDemo } = useApp();
  const [user, setUser] = useState('');
  const [pass, setPass] = useState('');
  const [remember, setRemember] = useState(true);
  const [pending, setPending] = useState<string | null>(null);
  const [code, setCode] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');
  const [demoOk, setDemoOk] = useState(false);

  useEffect(() => {
    let alive = true;
    void fetchPublicDemo().then((d) => { if (alive) setDemoOk(d.enabled); });
    return () => { alive = false; };
  }, []);

  const storeCredential = (u: string, p: string) => {
    // Ofrecer al gestor de contraseñas guardar la credencial (regla
    // webapp-shell: el login SIEMPRE lo ofrece). En SPA el submit no
    // recarga, así que se fuerza con la Credential Management API.
    // (PasswordCredential no está en los tipos DOM de este TS → cast)
    const nav = navigator as Navigator & {
      credentials?: { store?: (c: unknown) => Promise<void> };
    };
    const PC = (window as unknown as { PasswordCredential?: new (d: { id: string; password: string; name: string }) => unknown }).PasswordCredential;
    if (remember && nav.credentials?.store && PC) {
      const cred = new PC({ id: u, password: p, name: u });
      nav.credentials.store(cred).catch(() => { /* el usuario canceló o no soporta */ });
    }
  };

  const submit = async (e: React.FormEvent) => {
    e.preventDefault(); // el POST real lo hace la app vía fetch
    setBusy(true); setErr('');
    try {
      const step = await login(user.trim(), pass);
      if (typeof step === 'object') {
        // Paso 1 correcto: pedir el código del autenticador.
        setPending(step.pending);
        setBusy(false);
        return;
      }
      storeCredential(user.trim(), pass);
    } catch (ex) {
      // 401 → credenciales incorrectas; cualquier otro fallo (red, 5xx…) → sin conexión
      setErr(ex instanceof ApiError && ex.status === 401 ? t('login_error') : t('login_no_conn'));
      setBusy(false);
    }
  };

  const submitCode = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!pending) return;
    setBusy(true); setErr('');
    try {
      await login2FA(pending, code.trim());
      storeCredential(user.trim(), pass);
    } catch (ex) {
      setErr(ex instanceof ApiError && ex.status === 401 ? t('login_code_error') : t('login_no_conn'));
      setBusy(false);
    }
  };

  const backToPassword = () => {
    setPending(null);
    setCode('');
    setErr('');
  };

  const demo = async () => {
    setBusy(true); setErr('');
    try {
      await enterDemo();
    } catch {
      setBusy(false);
    }
  };

  return (
    <div className="login-wrap">
      <div className="login-card">
        <div className="card pad" style={{ padding: 26 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 18 }}>
            <Logo size={40} />
            <div>
              <div style={{ fontWeight: 800, fontSize: 20, letterSpacing: '-.02em' }}>EasyZFS</div>
              <div className="muted">{t('login_sub')}</div>
            </div>
          </div>
          {!pending ? (
            <form method="post" onSubmit={submit}>
              <h3 style={{ fontSize: 16, fontWeight: 700, marginBottom: 4 }}>{t('login_title')}</h3>
              <label htmlFor="lg-user">{t('login_user')}</label>
              <input id="lg-user" name="username" autoComplete={remember ? 'username' : 'off'}
                value={user} autoFocus onChange={(e) => setUser(e.target.value)} required />
              <label htmlFor="lg-pass">{t('login_pass')}</label>
              <input id="lg-pass" name="password" type="password"
                autoComplete={remember ? 'current-password' : 'off'} value={pass}
                onChange={(e) => setPass(e.target.value)} required />
              <label className="checklabel" style={{ marginTop: 14 }}>
                <input type="checkbox" checked={remember}
                  onChange={(e) => setRemember(e.target.checked)} />
                {t('login_remember')}
              </label>
              {err && <p className="form-err" role="alert">{err}</p>}
              <div className="m-actions" style={{ justifyContent: 'stretch', marginTop: 16 }}>
                <button type="submit" className="btn primary" style={{ flex: 1, justifyContent: 'center' }}
                  disabled={busy || !user.trim() || !pass}>
                  {busy ? '…' : t('login_btn')}
                </button>
              </div>
            </form>
          ) : (
            <form method="post" onSubmit={submitCode}>
              <h3 style={{ fontSize: 16, fontWeight: 700, marginBottom: 4 }}>{t('login_code_title')}</h3>
              <p className="muted" style={{ margin: '4px 0 12px' }}>{t('login_code_sub', { user })}</p>
              <label htmlFor="lg-code">{t('login_code')}</label>
              <input id="lg-code" name="code" inputMode="numeric" autoComplete="one-time-code"
                autoFocus value={code} onChange={(e) => setCode(e.target.value)}
                placeholder="000000" required />
              {err && <p className="form-err" role="alert">{err}</p>}
              <div className="m-actions" style={{ justifyContent: 'stretch', marginTop: 16 }}>
                <button type="submit" className="btn primary" style={{ flex: 1, justifyContent: 'center' }}
                  disabled={busy || code.trim().length < 6}>
                  {busy ? '…' : t('login_code_btn')}
                </button>
              </div>
              <button type="button" className="btn" style={{ width: '100%', justifyContent: 'center', marginTop: 8 }}
                onClick={backToPassword} disabled={busy}>
                {t('login_back')}
              </button>
            </form>
          )}
          {!pending && demoOk && (
            <>
              <div className="login-or" aria-hidden="true"><span>{t('login_or')}</span></div>
              <button type="button" className="btn" style={{ width: '100%', justifyContent: 'center' }}
                onClick={demo} disabled={busy}>
                {t('login_demo_btn')}
              </button>
            </>
          )}
        </div>
      </div>
    </div>
  );
}
