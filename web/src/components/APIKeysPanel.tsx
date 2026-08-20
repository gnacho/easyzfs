// APIKeysPanel — gestión de API keys de solo lectura (admin, #87): crear
// (la clave se muestra UNA vez), listar y revocar.
import { useCallback, useEffect, useState } from 'react';
import { useApp } from '../ui/store';
import { getProvider } from '../data';
import { errorMessage } from '../ui/store';
import type { APIKeyInfo } from '../data/types';
import { IconTrash, IconX } from './icons';

export function APIKeysPanel() {
  const { t, notify } = useApp();
  const [keys, setKeys] = useState<APIKeyInfo[] | null>(null);
  const [name, setName] = useState('');
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState('');
  const [err, setErr] = useState('');
  const [newKey, setNewKey] = useState('');

  const load = useCallback(() => {
    getProvider().getAPIKeys().then(setKeys).catch((e) => setErr(errorMessage(e, t)));
  }, [t]);

  useEffect(() => { load(); }, [load]);

  const create = async (ev: React.FormEvent) => {
    ev.preventDefault();
    if (!name.trim()) return;
    setBusy(true); setErr(''); setMsg(''); setNewKey('');
    try {
      const r = await getProvider().createAPIKey(name.trim());
      setNewKey(r.key); // se muestra una sola vez
      setName('');
      load();
      notify(t('toast_apikey_created'), 'ok');
    } catch (e) { const m = errorMessage(e, t); setErr(m); notify(m, 'err'); }
    setBusy(false);
  };

  const remove = async (id: number) => {
    if (!window.confirm(t('s_apikey_del_warn'))) return;
    setBusy(true); setErr(''); setMsg('');
    try {
      await getProvider().deleteAPIKey(id);
      setKeys((cur) => (cur ?? []).filter((k) => k.id !== id));
      notify(t('toast_apikey_deleted'), 'ok');
    } catch (e) { const m = errorMessage(e, t); setErr(m); notify(m, 'err'); }
    setBusy(false);
  };

  return (
    <div className="card pad admin-card">
      <h3 className="cardtitle">{t('s_apikeys')}</h3>
      <p style={{ fontSize: 12, color: 'var(--text2)', margin: '4px 0 10px' }}>{t('s_apikeys_d')}</p>

      {newKey && (
        <div style={{ border: '1px solid var(--accent)', borderRadius: 9, padding: 10, marginBottom: 10, background: 'var(--accent-soft, transparent)' }}>
          <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
            <code style={{ fontSize: 12.5, wordBreak: 'break-all', flex: 1 }}>{newKey}</code>
            <button type="button" className="btn sm" aria-label={t('close')} title={t('close')}
              onClick={() => setNewKey('')}><IconX size={13} /></button>
          </div>
          <p style={{ fontSize: 12, color: 'var(--warn)', fontWeight: 700, marginTop: 6 }}>{t('s_apikey_once')}</p>
        </div>
      )}

      <form onSubmit={(e) => void create(e)} style={{ display: 'flex', gap: 8, marginBottom: 12 }}>
        <input type="text" value={name} placeholder={t('s_apikey_name')}
          onChange={(e) => setName(e.target.value)} maxLength={32} style={{ flex: 1 }} required />
        <button type="submit" className="btn sm primary" disabled={busy || !name.trim()}>+ {t('s_apikey_new')}</button>
      </form>

      {(keys ?? []).map((k) => (
        <div className="rowitem" key={k.id}>
          <div className="grow">
            <div className="t1" style={{ fontSize: 14 }}>{k.name}</div>
            <div className="t2">
              {t('s_apikey_created')}: {k.created_at ? new Date(k.created_at + 'Z').toLocaleString() : '—'}
              {k.last_used ? ` · ${t('s_apikey_last')}: ${new Date(k.last_used + 'Z').toLocaleString()}` : ''}
            </div>
          </div>
          <button className="btn sm danger" disabled={busy} onClick={() => void remove(k.id)}
            aria-label={t('s_apikey_revoke')} title={t('s_apikey_revoke')}>
            <IconTrash size={14} />
          </button>
        </div>
      ))}
      {keys && keys.length === 0 && <div className="empty">{t('empty')}</div>}

      {msg && <p style={{ fontSize: 13, color: 'var(--ok)', fontWeight: 600, marginTop: 8 }} role="status">{msg}</p>}
      {err && <p className="form-err" role="alert" style={{ marginTop: 8 }}>{err}</p>}
    </div>
  );
}
