// Vista Tareas: próximas ejecuciones, tareas programadas, tareas del
// sistema (systemd/cron, solo lectura) e historial.
import { useEffect } from 'react';
import { getProvider } from '../data';
import { subscribeEvents } from '../data/events';
import type { Job, JobType } from '../data/types';
import { useData } from '../ui/useData';
import { errorMessage, useApp } from '../ui/store';
import { timeAgo } from '../ui/format';
import { Badge, InfoBubble, Spinner, Switch } from '../components/ui';
import { useModal } from '../components/Modal';
import { describeSchedule } from '../components/Modals';
import { useState } from 'react';

const TIPO_CLS: Record<JobType, 'info' | 'ok' | 'warn'> = {
  snapshot: 'info', scrub: 'ok', trim: 'ok', smart_short: 'warn', smart_long: 'warn', poweroff: 'warn', replication: 'info',
};

export default function Tasks() {
  const { t, isAdmin, notify } = useApp();
  const { openModal } = useModal();
  const jobs = useData((p) => p.getJobs());
  const hist = useData((p) => p.getJobHistory());
  const sys = useData((p) => p.getSystemTimers());
  const ops = useData((p) => p.getLongOps());
  const repl = useData((p) => p.getReplicationJobs());
  const [err, setErr] = useState('');

  // Cuando termina una tarea (evento) recargamos lista e historial; las
  // operaciones largas se refrescan con cada cambio de estado (longop.update)
  useEffect(() => subscribeEvents((ev) => {
    if (ev.type === 'job.finished' || ev.type === 'scrub.progress') {
      if (ev.type === 'job.finished') { jobs.reload(); hist.reload(); }
    }
    if (ev.type === 'longop.update') { ops.reload(); repl.reload(); }
    if (ev.type === 'replication.finished') { repl.reload(); hist.reload(); }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }), []);

  const tipoLbl = (j: JobType) =>
    j === 'smart_short' ? t('tk_type_smart_short') : j === 'smart_long' ? t('tk_type_smart_long')
    : j === 'replication' ? t('tk_type_replication')
    : j === 'snapshot' ? t('tk_type_snapshot') : j === 'trim' ? t('tk_type_trim') : t('tk_type_scrub');

  const runRepl = async (id: number) => {
    setErr('');
    try { await getProvider().runReplicationJob(id); repl.reload(); ops.reload(); notify(t('toast_repl_started'), 'ok'); }
    catch (e) { const m = errorMessage(e, t); setErr(m); notify(m, 'err'); }
  };
  const toggleRepl = async (id: number, on: boolean) => {
    setErr('');
    try { await getProvider().updateReplicationJob(id, { enabled: on }); repl.reload(); }
    catch (e) { const m = errorMessage(e, t); setErr(m); notify(m, 'err'); }
  };

  const run = async (id: number) => {
    setErr('');
    try { await getProvider().runJob(id); jobs.reload(); hist.reload(); notify(t('toast_task_started'), 'ok'); }
    catch (e) { const m = errorMessage(e, t); setErr(m); notify(m, 'err'); }
  };
  const toggleEnabled = async (j: Job, on: boolean) => {
    setErr('');
    try { await getProvider().updateJob(j.id, { enabled: on }); jobs.reload(); }
    catch (e) { const m = errorMessage(e, t); setErr(m); notify(m, 'err'); }
  };
  const cancelOp = async (id: string) => {
    setErr('');
    try { await getProvider().cancelLongOp(id); ops.reload(); notify(t('toast_op_cancelled'), 'ok'); }
    catch (e) { const m = errorMessage(e, t); setErr(m); notify(m, 'err'); }
  };

  const list = jobs.data ?? [];
  // systemd disponible como init: condiciona botón "Cambiar", burbuja y texto
  const systemdAvailable = sys.data?.systemd_available ?? false;
  const upcoming = list
    .filter((j) => j.enabled && j.next_run)
    .sort((a, b) => a.next_run.localeCompare(b.next_run))
    .slice(0, 5);

  return (
    <div className="view">
      {jobs.loading && !jobs.data && <Spinner label={t('loading')} />}
      {err && <p className="form-err" role="alert">{err}</p>}

      {/* En pantalla muy ancha (≥1800px): tareas programadas en la columna
          principal y el resto de secciones apiladas a la derecha */}
      <div className="tasks-grid">
      <div className="sect tk-next" style={{ marginTop: 0 }}>
        <h2>{t('tk_next')}</h2>
        <div className="card">
          {upcoming.length === 0 && <div className="empty">{t('empty')}</div>}
          {upcoming.map((j) => (
            <div className="rowitem" style={{ padding: '11px 16px' }} key={j.id}>
              <Badge tone={TIPO_CLS[j.tipo]} dot={false} style={{ padding: '2px 8px' }}>{tipoLbl(j.tipo)}</Badge>
              <div className="grow" style={{ fontSize: 13.5 }}>
                {tipoLbl(j.tipo)} · <span className="mono">{j.target === 'all' ? t('nt_all_disks') : j.target}</span>
              </div>
              <span className="mono" style={{ fontSize: 12, color: 'var(--accent)', fontWeight: 650 }}>
                {timeAgo(j.next_run, t)}
              </span>
            </div>
          ))}
        </div>
      </div>

      <div className="sect tk-jobs">
        <h2>{t('tk_jobs')}
          <span className="actions">
            <button className="btn sm primary" onClick={() => openModal('newtask')}>{t('tk_new')}</button>
          </span>
        </h2>
        <div className="card">
          {list.map((j) => (
            <div className="rowitem" key={j.id}>
              <div className="grow">
                <div className="t1" style={{ fontSize: 13.5 }}>
                  <Badge tone={TIPO_CLS[j.tipo]} dot={false} style={{ padding: '2px 8px' }}>{tipoLbl(j.tipo)}</Badge>
                  <span className="mono">{j.target === 'all' ? t('nt_all_disks') : j.target}</span>
                </div>
                <div className="t2">
                  {describeSchedule(j.schedule, t as never)}
                  {j.retention ? ` · ${j.retention}` : ''}
                  {j.last_run ? ` · ${t('tk_last')}: ${timeAgo(j.last_run, t)} · ${j.last_result}` : ''}
                </div>
                <div className="t2" style={{ color: 'var(--accent)', fontWeight: 600 }}>
                  {t('tk_nextrun')}: {j.enabled && j.next_run ? timeAgo(j.next_run, t) : t('tk_none')}
                </div>
              </div>
              <button className="btn sm" onClick={() => openModal('sched', { job: j })}>{t('edit')}</button>
              <button className="btn sm" onClick={() => run(j.id)}>{t('run_now')}</button>
              <Switch checked={j.enabled} onChange={(on) => toggleEnabled(j, on)}
                ariaLabel={j.enabled ? t('active') : t('paused')} />
            </div>
          ))}
          {jobs.data && list.length === 0 && <div className="empty">{t('empty')}</div>}
        </div>
      </div>

      {/* Replicación ZFS send/recv (local y SSH) */}
      <div className="sect tk-repl">
        <h2>{t('repl_title')}
          <span className="actions">
            {isAdmin && <button className="btn sm primary" onClick={() => openModal('newrepl')}>{t('repl_new')}</button>}
          </span>
        </h2>
        <div className="card">
          {repl.loading && !repl.data && <Spinner label={t('loading')} />}
          {(repl.data ?? []).map((j) => {
            const target = j.dest_type === 'ssh'
              ? `${j.source} → ${j.user}@${j.host}:${j.dest_dataset}`
              : `${j.source} → ${j.dest_dataset}`;
            const running = (ops.data ?? []).find((o) => o.type === 'replication' && o.target === target && o.status === 'running');
            return (
              <div className="rowitem" key={j.id}>
                <Badge tone={j.dest_type === 'ssh' ? 'info' : 'ok'} dot={false} style={{ padding: '2px 8px' }}>
                  {j.dest_type === 'ssh' ? t('repl_ssh') : t('repl_local')}
                </Badge>
                <div className="grow">
                  <div className="t1" style={{ fontSize: 13.5 }}>
                    <span className="mono">{j.source}</span> → <span className="mono">{j.dest_type === 'ssh' ? `${j.user}@${j.host}:${j.dest_dataset}` : j.dest_dataset}</span>
                    {j.raw && <Badge tone="warn" dot={false} style={{ padding: '1px 6px', marginLeft: 6 }}>{t('repl_raw_tag')}</Badge>}
                  </div>
                  <div className="t2">
                    {describeSchedule(j.schedule, t as never)}
                    {j.last_run ? ` · ${t('tk_last')}: ${timeAgo(j.last_run, t)}` : ''}
                    {j.last_ok === true ? ' · OK' : ''}
                    {j.last_ok === false ? ` · ${j.last_error}` : ''}
                    {j.enabled && j.next_run ? ` · ${t('tk_nextrun')}: ${timeAgo(j.next_run, t)}` : ''}
                  </div>
                  {running && running.lines.length > 0 && (
                    <div className="t2" style={{ color: 'var(--accent)', fontWeight: 600 }}>
                      {running.lines[running.lines.length - 1]}
                    </div>
                  )}
                </div>
                {isAdmin && <button className="btn sm" onClick={() => openModal('editrepl', { job: j })}>{t('edit')}</button>}
                {isAdmin && <button className="btn sm" onClick={() => runRepl(j.id)} disabled={!!running}>{t('run_now')}</button>}
                {isAdmin && <Switch checked={j.enabled} onChange={(on) => toggleRepl(j.id, on)}
                  ariaLabel={j.enabled ? t('active') : t('paused')} />}
              </div>
            );
          })}
          {repl.data && repl.data.length === 0 && <div className="empty">{t('repl_empty')}</div>}
        </div>
      </div>

      {/* Tareas del sistema: timers de systemd y cron */}
      <div className="sect tk-sys">
        <h2 style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          {t('tk_system')}
          {/* Sin systemd no tiene sentido la comparativa cron vs systemd */}
          {systemdAvailable && (
            <InfoBubble title={t('tk_cronvs_title')}>
              <b>cron</b>
              <ul>
                <li>{t('tk_cronvs_cron1')}</li>
                <li>{t('tk_cronvs_cron2')}</li>
              </ul>
              <b>systemd timer</b>
              <ul>
                <li>{t('tk_cronvs_sysd1')}</li>
                <li>{t('tk_cronvs_sysd2')}</li>
                <li>{t('tk_cronvs_sysd3')}</li>
              </ul>
            </InfoBubble>
          )}
        </h2>
        <div className="card">
          {sys.loading && !sys.data && <Spinner label={t('loading')} />}
          {(sys.data?.timers ?? []).map((s, i) => (
            <div className="rowitem" key={i}>
              <Badge tone={s.source === 'systemd' ? 'info' : 'warn'} dot={false} style={{ padding: '2px 8px' }}>
                {s.source}
              </Badge>
              <div className="grow">
                <div className="t1" style={{ fontSize: 13.5 }}>{s.name}</div>
                <div className="t2">
                  {s.schedule}
                  {s.next_run ? ` · ${t('tk_nextrun')}: ${timeAgo(s.next_run, t)}` : ''}
                </div>
                <div className="t2 mono">{s.command}</div>
              </div>
              {isAdmin && s.editable && (
                <button className="btn sm" title={t('ss_btn_hint')}
                  onClick={() => openModal('syssched', { task: s })}>{t('edit')}</button>
              )}
              {isAdmin && s.editable && s.source === 'cron' && systemdAvailable && (
                <button className="btn sm" title={t('sm_btn_hint')}
                  onClick={() => openModal('sysmigrate', { task: s })}>{t('sm_btn')}</button>
              )}
            </div>
          ))}
          {sys.data && sys.data.timers.length === 0 && <div className="empty">{t('empty')}</div>}
        </div>
        <p style={{ fontSize: 12, color: 'var(--text2)', marginTop: 8 }}>
          {systemdAvailable ? t('tk_system_d') : t('tk_system_d_nosysd')}
        </p>
      </div>

      {/* Operaciones largas (runner del backend: zfs rewrite; futura replicación) */}
      <div className="sect tk-ops">
        <h2>{t('ops_title')}</h2>
        <div className="card">
          {ops.loading && !ops.data && <Spinner label={t('loading')} />}
          {(ops.data ?? []).map((o) => (
            <div className="rowitem" key={o.id}>
              <Badge tone={o.status === 'running' ? 'info' : o.status === 'done' ? 'ok' : o.status === 'error' ? 'err' : 'warn'}
                dot={false} style={{ padding: '2px 8px' }}>
                {t(`ops_st_${o.status}` as never)}
              </Badge>
              <div className="grow">
                <div className="t1" style={{ fontSize: 13.5 }}>
                  {o.type === 'rewrite' ? t('ops_type_rewrite') : o.type === 'replication' ? t('ops_type_replication') : o.type} · <span className="mono">{o.target}</span>
                </div>
                <div className="t2">
                  {timeAgo(o.started, t)}
                  {o.error ? ` · ${o.error}` : ''}
                  {o.status === 'running' && o.lines.length > 0 ? ` · ${o.lines[o.lines.length - 1]}` : ''}
                </div>
              </div>
              {isAdmin && o.status === 'running' && (
                <button className="btn sm danger" onClick={() => cancelOp(o.id)}>{t('ops_cancel')}</button>
              )}
            </div>
          ))}
          {ops.data && ops.data.length === 0 && <div className="empty">{t('ops_empty')}</div>}
        </div>
        <p style={{ fontSize: 12, color: 'var(--text2)', marginTop: 8 }}>{t('ops_hint')}</p>
      </div>

      <div className="sect tk-hist">
        <h2>{t('tk_history')}</h2>
        <div className="card">
          {(hist.data ?? []).map((h, i) => (
            <div className="rowitem" key={i}>
              <Badge tone={h.ok ? 'ok' : 'err'} dot={false} style={{ padding: '2px 8px' }}>
                {h.ok ? t('hist_ok') : t('hist_warn')}
              </Badge>
              <div className="grow">
                <div className="t1" style={{ fontSize: 13.5 }}>{tipoLbl(h.tipo)} · <span className="mono">{h.target}</span></div>
                <div className="t2">{h.detail}</div>
              </div>
              <span style={{ fontSize: 11.5, color: 'var(--text2)' }}>{timeAgo(h.ts, t)}</span>
            </div>
          ))}
          {hist.data && hist.data.length === 0 && <div className="empty">{t('empty')}</div>}
        </div>
      </div>
      </div>
    </div>
  );
}
