// Host de toasts ligeros (sistema propio, sin dependencias). Cola global de
// AppCtx (store.tsx): notify() encola, dismissToast() descarta. Aparecen
// abajo-derecha, por encima de los modales, con aria-live para lectores de
// pantalla. Port refactorizado del fork coruhoorhan/easyzfs-truenas (AGPL-3.0).
import { useApp } from '../ui/store';
import type { Toast, ToastKind } from '../ui/store';

const KIND_CLASS: Record<ToastKind, string> = {
  ok: 'ok',
  err: 'err',
  warn: 'warn',
  info: 'info',
};

export function ToastHost() {
  const { toasts, dismissToast, t } = useApp();
  if (toasts.length === 0) return null;
  return (
    <div className="toast-host" role="region" aria-live="polite" aria-label={t('toast_dismiss')}>
      {toasts.map((tst: Toast) => (
        <div key={tst.id} className={`toast ${KIND_CLASS[tst.kind]}`} role={tst.kind === 'err' ? 'alert' : 'status'}>
          <span className="toast-msg">{tst.msg}</span>
          <button type="button" className="toast-x" aria-label={t('toast_dismiss')} onClick={() => dismissToast(tst.id)}>×</button>
        </div>
      ))}
    </div>
  );
}
