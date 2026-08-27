// UpdateWizard — asistente de actualización multi-paso con barra de progreso
// (UX del actualizador de NetPulse): confirmación → pasos (download → install
// → restart) → reinicio → recarga. Estado en vivo por SSE (/api/update/stream)
// con fallback a polling, y recarga solo cuando responde un proceso distinto
// (uptime_sec menor que el baseline previo al apply).
import { useCallback, useEffect, useRef, useState } from 'react';
import { ModalBox } from './Modal';
import { useApp } from '../ui/store';
import { getProvider } from '../data';
import { IconCheck, IconDownload, IconRefresh } from './icons';
import type { UpdateStatus } from '../data/types';

interface PlanCheck {
  id: string;
  status: string; // "pass" | "warn" | "fail"
  title: string;
  summary: string;
}

interface UpdateWizardProps {
  status: UpdateStatus;
  plan: { canApply: boolean; checks: PlanCheck[] };
  onClose: () => void;
}

/** Pasos visibles del asistente (etapas reales que reporta el backend). */
const STEP_ORDER = ['downloading', 'installing', 'restarting'] as const;

type Phase = 'confirm' | 'progress' | 'restarting' | 'error';

export function UpdateWizard({ status: initialStatus, plan, onClose }: UpdateWizardProps) {
  const { t } = useApp();
  const [phase, setPhase] = useState<Phase>('confirm');
  const [step, setStep] = useState<string>('downloading');
  const [pct, setPct] = useState(0);
  const [errorCode, setErrorCode] = useState<string | null>(null);
  const [ackDown, setAckDown] = useState(false);

  const esRef = useRef<EventSource | null>(null);
  const pollRef = useRef<number | null>(null);
  const uptimeRef = useRef<number>(Number.MAX_SAFE_INTEGER);
  const phaseRef = useRef<Phase>('confirm');
  const seenRef = useRef(false); // ¿hemos visto un apply en curso? (evita el falso final del evento inicial)

  const setPhaseBoth = (p: Phase) => {
    phaseRef.current = p;
    setPhase(p);
  };

  const closeStream = useCallback(() => {
    if (esRef.current) { esRef.current.close(); esRef.current = null; }
    if (pollRef.current !== null) { window.clearInterval(pollRef.current); pollRef.current = null; }
  }, []);

  useEffect(() => closeStream, [closeStream]);

  // Recarga cuando responde un proceso distinto (uptime_sec menor). Tope de
  // 90 s por si el restart se atasca.
  const waitAndReload = useCallback(() => {
    if (phaseRef.current === 'restarting') return;
    setPhaseBoth('restarting');
    const deadline = Date.now() + 90_000;
    const id = window.setInterval(async () => {
      try {
        const v = await getProvider().getVersion();
        if (v.uptime_sec < uptimeRef.current || Date.now() > deadline) {
          window.clearInterval(id);
          window.location.reload();
        }
      } catch {
        /* reiniciando… */
      }
    }, 1500);
  }, []);

  // Reacción central al estado que llega por SSE o polling.
  const handleStatus = useCallback(
    (st: UpdateStatus) => {
      if (st.progress) { setStep(st.progress.step); setPct(st.progress.percentage); }
      if (st.inProgress) {
        seenRef.current = true;
        if (phaseRef.current === 'confirm') setPhaseBoth('progress');
      } else if (phaseRef.current === 'progress' && seenRef.current) {
        // El apply terminó: o reinició (el .path) o falló. El stream muere con
        // el proceso durante el reinicio; si no, cerramos y recargamos.
        closeStream();
        waitAndReload();
      }
    },
    [closeStream, waitAndReload],
  );

  // Fallback a polling si el SSE no está disponible (proxy, etc.).
  const startPolling = useCallback(() => {
    if (pollRef.current !== null) return;
    let fails = 0;
    const poll = async () => {
      try {
        const st = await getProvider().getUpdateStatus();
        if (phaseRef.current !== 'progress' && phaseRef.current !== 'restarting') return;
        handleStatus(st);
      } catch {
        fails += 1;
        // Sin conexión durante el update: casi seguro reiniciando.
        if (fails >= 2 && phaseRef.current === 'progress') { waitAndReload(); }
      }
    };
    void poll();
    pollRef.current = window.setInterval(poll, 2000);
  }, [handleStatus, waitAndReload]);

  const startStream = useCallback(() => {
    try {
      const es = new EventSource('/api/update/stream');
      esRef.current = es;
      es.addEventListener('update', (ev) => {
        try {
          handleStatus(JSON.parse((ev as MessageEvent).data) as UpdateStatus);
        } catch { /* payload corrupto: espera el próximo */ }
      });
      es.onerror = () => {
        // El stream MUERE con el proceso durante el reinicio final.
        if (phaseRef.current === 'restarting') return;
        if (step === 'restarting') {
          closeStream();
          waitAndReload();
          return;
        }
        closeStream();
        startPolling();
      };
    } catch {
      startPolling();
    }
  }, [closeStream, handleStatus, startPolling, waitAndReload, step]);

  const apply = async () => {
    if (!ackDown || !plan.canApply) return;
    setPhaseBoth('progress');
    startStream();
    try {
      const v = await getProvider().getVersion();
      if (typeof v.uptime_sec === 'number') uptimeRef.current = v.uptime_sec;
    } catch { /* sin baseline: el primer uptime menor recarga */ }
    // El apply del servidor descarga+valida de forma síncrona y toca el flag
    // .restart-me; el stream reporta el progreso mientras tanto.
    void getProvider()
      .applyUpdate()
      .catch((e: unknown) => {
        closeStream();
        const code = (e as { status?: number } | null)?.status === 409 ? 'already_updating' : 'update_failed';
        setErrorCode(code);
        setPhaseBoth('error');
      });
  };

  const busy = phase === 'progress' || phase === 'restarting';
  const activeIdx = STEP_ORDER.indexOf(step as (typeof STEP_ORDER)[number]);
  const failChecks = plan.checks.filter((c) => c.status === 'fail');

  // No permitir cerrar mientras el update está en vuelo (Esc/overlay).
  const handleClose = () => { if (!busy) onClose(); };

  return (
    <ModalBox onClose={handleClose} label={t('uz_title')}>
      <h3>{t('uz_title')}</h3>

      {phase === 'confirm' && (
        <>
          <p className="desc">{t('uz_confirm')}</p>
          <div className="uz-versions">
            <span className="uz-ver">{initialStatus.current || ''}</span>
            <span className="uz-arrow" aria-hidden="true">→</span>
            <span className="uz-ver uz-latest">{initialStatus.latest || ''}</span>
          </div>
          {initialStatus.releaseNotes && (
            <p className="uz-notes">{initialStatus.releaseNotes}</p>
          )}
          {failChecks.length > 0 && (
            <div className="uz-checks" role="alert">
              {failChecks.map((c) => (
                <div key={c.id}>✗ {c.summary}</div>
              ))}
            </div>
          )}
          <div className="uz-down">
            <input type="checkbox" id="uz-ack" aria-label={t('uz_down')} checked={ackDown} onChange={(e) => setAckDown(e.target.checked)} />
            <span>{t('uz_down')}</span>
          </div>
          <div className="m-actions">
            <button type="button" className="btn" onClick={onClose}>{t('uz_cancel')}</button>
            <button type="button" className="btn primary" disabled={!ackDown || !plan.canApply} onClick={() => { void apply(); }}>
              <IconDownload size={14} /> {t('uz_start', { v: initialStatus.latest || '' })}
            </button>
          </div>
        </>
      )}

      {phase === 'progress' && (
        <div className="uz-progress" role="status">
          <ul className="uz-steps">
            {STEP_ORDER.map((s) => {
              const idx = STEP_ORDER.indexOf(s);
              const done = activeIdx > idx || step === 'restarting';
              const active = activeIdx === idx && step !== 'restarting';
              return (
                <li key={s} className={done ? 'done' : active ? 'active' : ''}>
                  {done ? <IconCheck size={13} /> : active ? <IconRefresh size={13} className="spin" /> : <span className="uz-step-dot">○</span>}
                  <span>{t(`uz_step_${s}`)}</span>
                </li>
              );
            })}
          </ul>
          <div className="uz-bar-label">{t(`uz_step_${step}`)} · {pct}%</div>
          <div className="uz-bar">
            <div className="uz-bar-fill" style={{ width: `${pct}%` }} />
          </div>
          <p className="uz-hint">{t('uz_close_hint')}</p>
        </div>
      )}

      {phase === 'restarting' && (
        <div className="uz-restarting" role="status">
          <IconRefresh size={30} className="spin" />
          <span className="uz-restart-title">{t('uz_restarting')}</span>
          <span className="uz-hint">{t('uz_reload')}</span>
        </div>
      )}

      {phase === 'error' && (
        <div className="uz-error">
          <div className="uz-error-title">{t('uz_failed')}</div>
          {errorCode && <div className="uz-error-code">{errorCode}</div>}
          <div className="m-actions">
            <button type="button" className="btn" onClick={onClose}>{t('uz_close')}</button>
          </div>
        </div>
      )}
    </ModalBox>
  );
}
