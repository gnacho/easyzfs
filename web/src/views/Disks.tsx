// Vista Discos: tabla con salud SMART, temperaturas en vivo y tests.
import { useEffect, useState } from 'react';
import { getProvider } from '../data';
import { subscribeEvents } from '../data/events';
import { useData } from '../ui/useData';
import { errorMessage, useApp } from '../ui/store';
import { fmtBytes, fmtInt } from '../ui/format';
import { Badge, InfoBubble, Spinner } from '../components/ui';
import { Sparkline } from '../components/Sparkline';
import type { Disk, SeriesPoint } from '../data/types';
import { useModal } from '../components/Modal';

// DiskTempSpark — sparkline de temperatura 24h bajo el valor actual.
function DiskTempSpark({ dev }: { dev: string }) {
  const { t } = useApp();
  const [pts, setPts] = useState<SeriesPoint[] | null>(null);
  useEffect(() => {
    let alive = true;
    getProvider().getSeries(`disk.${dev}.temp`, 1, 80)
      .then((r) => { if (alive) setPts(r.points); })
      .catch(() => {});
    return () => { alive = false; };
  }, [dev]);
  if (!pts || pts.length < 2) return null;
  return (
    <div style={{ marginTop: 4, color: 'var(--text3)' }}>
      <Sparkline points={pts} width={64} height={18} ariaLabel={t('dk_temp_hist', { dev })} />
    </div>
  );
}

const TIPO_SMART: Record<string, 'ok' | 'warn' | 'err' | 'info'> = {
  ok: 'ok', warn: 'warn', crit: 'err', unknown: 'info',
};

// smartBase — palabra de estado del badge (Correcto/FALLO/no disponible).
function smartBase(d: Disk, t: (k: string) => string): string {
  if (d.smart === 'unknown') return t('dk_smart_na');
  if (d.smart === 'crit') return t('dk_smart_failed');
  return t('dk_smart_ok');
}

// smartParts — contadores como línea aparte bajo el badge (así la píldora
// queda en una línea y los contadores "bajan a dos líneas" según el ancho).
function smartParts(d: Disk, t: (k: string, v?: Record<string, string | number>) => string): string[] {
  if (d.smart === 'unknown' || d.smart === 'crit') return [];
  const parts: string[] = [];
  if ((d.realloc_sectors ?? 0) > 0) parts.push(t('dk_realloc', { n: d.realloc_sectors! }));
  if ((d.pending_sectors ?? 0) > 0) parts.push(t('dk_pending', { n: d.pending_sectors! }));
  if ((d.offline_uncorr ?? 0) > 0) parts.push(t('dk_offunc', { n: d.offline_uncorr! }));
  if ((d.crc_recent ?? 0) > 0) parts.push(t('dk_crc_recent', { n: d.crc_recent!, total: d.crc_errors ?? 0 }));
  else if ((d.crc_errors ?? 0) >= 100) parts.push(t('dk_crc_stable', { n: d.crc_errors! }));
  if ((d.nvme_warn ?? 0) > 0) parts.push(t('dk_nvme_warn', { n: d.nvme_warn! }));
  return parts;
}

export default function Disks() {
  const { t, isAdmin, notify } = useApp();
  const { openModal } = useModal();
  const { data, loading, setData } = useData((p) => p.getDisks());
  const recs = useData((p) => p.getRecommendations());
  const [msg, setMsg] = useState('');
  const [arm, setArm] = useState('');

  // Temperaturas y salud SMART en tiempo real vía eventos (sin disk.smart
  // una pestaña abierta mostraba el SMART obsoleto hasta recargar a mano).
  useEffect(() => subscribeEvents((ev) => {
    if (ev.type === 'disk.temp') {
      setData((cur) => cur?.map((d) => d.dev === ev.dev ? { ...d, temp_c: ev.temp_c } : d) ?? cur);
    }
    if (ev.type === 'disk.smart') {
      setData((cur) => cur?.map((d) => d.dev === ev.dev ? {
        ...d, smart: ev.smart, smart_detail: ev.smart_detail,
        realloc_sectors: ev.realloc_sectors, pending_sectors: ev.pending_sectors,
        offline_uncorr: ev.offline_uncorr, crc_errors: ev.crc_errors,
        crc_recent: ev.crc_recent, nvme_warn: ev.nvme_warn,
      } : d) ?? cur);
      recs.reload();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }), []);

  const test = async (dev: string, type: 'short' | 'long') => {
    setMsg('');
    try {
      await getProvider().smartTest(dev, type);
      setMsg(`${t('dk_test_started')}: ${dev} (${type})`);
      notify(t('toast_smart_started'), 'ok');
    } catch (e) { const m = errorMessage(e, t); setMsg(m); notify(m, 'err'); }
  };

  // Apagar disco: doble clic (1º arma "¿Confirmar?", 2º ejecuta). Se desarma a los 3 s.
  useEffect(() => {
    if (!arm) return;
    const id = setTimeout(() => setArm(''), 3000);
    return () => clearTimeout(id);
  }, [arm]);

  const poweroff = async (dev: string) => {
    if (arm !== dev) { setArm(dev); return; }
    setArm('');
    setMsg('');
    try {
      await getProvider().poweroffDisk(dev);
      setMsg(`${t('dk_powered')}: ${dev}`);
      notify(t('toast_disk_off'), 'ok');
    } catch (e) { const m = errorMessage(e, t); setMsg(m); notify(m, 'err'); }
  };

  return (
    <div className="view">
      {loading && !data && <Spinner label={t('loading')} />}
      {msg && <p className="desc" style={{ marginBottom: 10, fontSize: 13, color: 'var(--info)', fontWeight: 600 }}>{msg}</p>}
      <div className="card tblwrap">
        <table className="data responsive">
          <thead>
            <tr>
              <th>{t('dk_disk')}</th><th className="slack">{t('dk_model')}</th><th className="num hide-md">{t('dk_size')}</th>
              <th className="num">{t('dk_temp')}</th><th>{t('dk_smart')}</th><th className="hide-md">{t('dk_pool')}</th><th />
            </tr>
          </thead>
          <tbody>
            {(data ?? []).map((d) => (
              <tr key={d.dev} className="clickable"
                title={t('dsm_title')}
                onClick={() => openModal('diskdetail', { disk: d })}>
                <td className="mono" style={{ fontWeight: 650 }}>{d.dev}</td>
                <td className="modelcell">
                  <div style={{ fontSize: 13 }}>{d.model}</div>
                  <div style={{ fontSize: 11.5, color: 'var(--text2)' }} className="mono">
                    {d.serial} · {fmtInt(d.hours)} {t('dk_hours')}
                  </div>
                </td>
                <td className="num hide-md" data-l={t('dk_size')}>{fmtBytes(d.size_bytes)}</td>
                <td className="num" data-l={t('dk_temp')}>
                  {d.temp_c === null ? '—' : `${d.temp_c}°C`}
                  <DiskTempSpark dev={d.dev} />
                </td>
                <td className="smartcell">
                  <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
                    <span title={d.smart_detail}>
                      <Badge tone={TIPO_SMART[d.smart] ?? 'info'} dot={d.smart !== 'unknown'}>{smartBase(d, t)}</Badge>
                    </span>
                    {/* Contadores en burbuja (i): compacto; hover en desktop, tap en móvil */}
                    {smartParts(d, t).length > 0 && (
                      <InfoBubble glyph="i" title={t('dk_smart')}>
                        <ul>
                          {smartParts(d, t).map((p) => <li key={p}>{p}</li>)}
                        </ul>
                      </InfoBubble>
                    )}
                  </span>
                  {/* Pista de acción del motor de recomendaciones (la más severa del disco) */}
                  {(recs.data ?? []).filter((r) => r.dev === d.dev).slice(0, 1).map((r) => (
                    <div key={r.kind} style={{ fontSize: 11.5, marginTop: 4, fontWeight: 600,
                      color: `var(--${r.level === 'crit' ? 'err' : r.level === 'warn' ? 'warn' : 'info'})` }}>
                      {t('rec_' + r.kind)}
                      {r.hold && r.hold_reason && <span style={{ color: 'var(--warn)' }}> · {t('rec_hold_' + r.hold_reason)}</span>}
                    </div>
                  ))}
                </td>
                <td className="hide-md" data-l={t('dk_pool')}>
                  {d.pool}
                  {d.in_use && <Badge tone="warn" dot={false}> {t('dk_in_use')}</Badge>}
                </td>
                <td className="actions">
                  <span className="testbtns">
                    <button className="btn sm" disabled={d.smart === 'unknown'} title={t('dk_test_short_hint')} onClick={(e) => { e.stopPropagation(); test(d.dev, 'short'); }}>{t('dk_test_short')}</button>{' '}
                    <button className="btn sm" disabled={d.smart === 'unknown'} title={t('dk_test_long_hint')} onClick={(e) => { e.stopPropagation(); test(d.dev, 'long'); }}>{t('dk_test_long')}</button>
                  </span>{' '}
                  {(d.pool === '—' || d.pool === '') && !d.in_use && (
                    <button className={`btn sm ${arm === d.dev ? 'danger' : ''}`} disabled={!isAdmin}
                      title={!isAdmin ? t('no_permission') : t('dk_poweroff_hint')}
                      onClick={(e) => { e.stopPropagation(); poweroff(d.dev); }}>
                      {arm === d.dev ? t('dk_poweroff_arm') : t('dk_poweroff')}
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        {data && data.length === 0 && <div className="empty">{t('empty')}</div>}
      </div>
    </div>
  );
}
