// RecLine — una recomendación del motor (acción + disco + motivo + guarda).
// Reutiliza el patrón visual de AlertRow (icono tonal + texto).
import type { Recommendation } from '../data/types';
import { useApp } from '../ui/store';

export function RecLine({ r }: { r: Recommendation }) {
  const { t } = useApp();
  const tone = r.level === 'crit' ? 'err' : r.level === 'warn' ? 'warn' : 'info';
  const ico = r.level === 'crit' ? '!' : r.level === 'warn' ? '⚠' : 'i';
  const reasons: string[] = [];
  if (r.realloc_sectors) reasons.push(t('dk_realloc', { n: r.realloc_sectors }));
  if (r.pending_sectors) reasons.push(t('dk_pending', { n: r.pending_sectors }));
  if (r.offline_uncorr) reasons.push(t('dk_offunc', { n: r.offline_uncorr }));
  if ((r.crc_recent ?? 0) > 0) reasons.push(t('dk_crc_recent', { n: r.crc_recent!, total: r.crc_errors ?? 0 }));
  return (
    <div className="alert">
      <div className="ico" style={{ background: `var(--${tone}-soft)`, color: `var(--${tone})` }}>{ico}</div>
      <div className="grow" style={{ flex: 1, minWidth: 0 }}>
        <b>{t('rec_' + r.kind)}</b>
        {' · '}<span className="mono" style={{ fontWeight: 650 }}>{r.dev}</span>
        {' '}<span className="muted">({r.serial})</span>
        <div className="muted" style={{ marginTop: 2 }}>
          {reasons.join(' · ')}
          {r.pool && r.pool !== '—' ? `${reasons.length ? ' · ' : ''}${r.pool}` : ''}
          {r.hold && r.hold_reason && (
            <span style={{ color: 'var(--warn)', fontWeight: 600 }}> · {t('rec_hold_' + r.hold_reason)}</span>
          )}
        </div>
      </div>
    </div>
  );
}
