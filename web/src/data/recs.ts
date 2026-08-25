// Réplica TS del motor de recomendaciones del backend (internal/recs) para
// el modo demo: mismas reglas y umbrales. Si cambia una, cambiar la otra.
import type { Disk, Pool, Recommendation } from './types';

const REALLOC_SOON = 100;

export function computeRecommendations(disks: Disk[], pools: Pool[]): Recommendation[] {
  const byPool = new Map(pools.map((p) => [p.name, p]));
  const out: Recommendation[] = [];
  for (const d of disks) {
    const base = {
      dev: d.dev, serial: d.serial, pool: d.pool,
      realloc_sectors: d.realloc_sectors ?? 0, pending_sectors: d.pending_sectors ?? 0,
      offline_uncorr: d.offline_uncorr ?? 0, crc_errors: d.crc_errors ?? 0,
      crc_recent: d.crc_recent ?? 0,
    };
    const add = (level: Recommendation['level'], kind: Recommendation['kind']) => {
      const r: Recommendation = { ...base, level, kind };
      if (kind === 'replace_now' || kind === 'replace_soon') {
        const p = byPool.get(d.pool);
        if (p) {
          if (p.scrub.state === 'running' && p.scrub.kind === 'resilver') { r.hold = true; r.hold_reason = 'resilver'; }
          else if (p.status !== 'ONLINE') { r.hold = true; r.hold_reason = 'pool_degraded'; }
          else if (p.topo === 'stripe') { r.hold = true; r.hold_reason = 'no_redundancy'; }
        }
      }
      out.push(r);
    };
    if (d.smart === 'crit') add('crit', 'replace_now');
    else if ((d.pending_sectors ?? 0) > 0 || (d.offline_uncorr ?? 0) > 0) add('crit', 'replace_now');
    else if ((d.realloc_sectors ?? 0) >= REALLOC_SOON) add('warn', 'replace_soon');
    else if ((d.realloc_sectors ?? 0) > 0 || (d.nvme_warn ?? 0) > 0) add('info', 'watch');
    // CRC: lo accionable es el CRECIMIENTO (link roto ahora); el acumulado de
    // por vida no se resetea y perseguiría al disco equivocado tras un cambio
    // de bahías (caso real bigtank 4-Ago-2026). El CRC histórico estable se
    // consulta en la burbuja info del disco, no como recomendación.
    if ((d.crc_recent ?? 0) > 0) add('warn', 'check_cable');
  }
  const rank = (l: string) => (l === 'crit' ? 0 : l === 'warn' ? 1 : 2);
  return out.sort((a, b) => rank(a.level) - rank(b.level));
}
