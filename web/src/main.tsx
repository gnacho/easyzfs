// Punto de entrada de la SPA
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import App from './App';
import { hasDemoSession } from './data';
import { fetchPublicDemo } from './data/http';
import { setDemoServer } from './ui/i18n';
// Fuentes self-hosted (offline/LAN): Space Grotesk = voz UI, JetBrains Mono = datos
import '@fontsource/space-grotesk/400.css';
import '@fontsource/space-grotesk/500.css';
import '@fontsource/space-grotesk/600.css';
import '@fontsource/space-grotesk/700.css';
import '@fontsource/jetbrains-mono/400.css';
import '@fontsource/jetbrains-mono/500.css';
import '@fontsource/jetbrains-mono/600.css';
import './index.css';

// En el despliegue demo (DEMO=1) el idioma automático se resuelve a inglés
// ANTES del primer render para evitar un destello del idioma del navegador.
// Timeout corto: si el flag no llega a tiempo se renderiza igualmente.
function render(): void {
  createRoot(document.getElementById('root')!).render(
    <StrictMode>
      <App />
    </StrictMode>,
  );

  // Service worker (Web Push): solo si el navegador lo soporta y la sesión NO
  // es demo (en demo no hay push real; ver skill web-push-alerts).
  if ('serviceWorker' in navigator && !hasDemoSession()) {
    navigator.serviceWorker.register('/sw.js').catch(() => { /* push no disponible */ });
  }
}

const flag = fetchPublicDemo().then((d) => { if (d.server) setDemoServer(true); });
const timeout = new Promise<void>((resolve) => { setTimeout(resolve, 400); });
Promise.race([flag, timeout]).finally(render);
