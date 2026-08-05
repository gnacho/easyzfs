/* EasyZFS landing — interactividad: idioma, tema, slider, contadores, reveal, copiar */
(function () {
  'use strict';

  const LANG_KEY = 'easyzfs-lang';
  const THEME_KEY = 'easyzfs-theme';
  const reduceMotion = window.matchMedia('(prefers-reduced-motion: reduce)').matches;
  const root = document.documentElement;

  /* Capturas: orden de las vistas + alt por idioma */
  const SLIDES = ['pools', 'disks', 'settings'];
  const SHOT_ALT = {
    pools: { es: 'Vista de pools: raidz1 tank degradado con oferta de rebuild y mirror ssd resilverizando', en: 'Pools view: degraded raidz1 tank with a rebuild offer and the ssd mirror resilvering' },
    disks: { es: 'Tabla de discos físicos con modelo, serie, tamaño, temperatura, salud SMART y pool', en: 'Physical disks table with model, serial, size, temperature, SMART health and pool' },
    settings: { es: 'Página de ajustes con apariencia, acento, densidad, perfil, idioma y notificaciones push', en: 'Settings page with appearance, accent, density, profile, language and push notifications' }
  };

  /* ---------- Idioma ---------- */
  function applyLang(lang) {
    const dict = I18N[lang] || I18N.es;
    document.querySelectorAll('[data-i18n]').forEach(function (el) {
      const key = el.getAttribute('data-i18n');
      if (dict[key]) el.textContent = dict[key];
    });
    document.querySelectorAll('[data-i18n-aria]').forEach(function (el) {
      const key = el.getAttribute('data-i18n-aria');
      if (dict[key]) el.setAttribute('aria-label', dict[key]);
    });
    root.lang = lang;
    const sel = document.getElementById('langSelect');
    if (sel) sel.value = lang;
    try { localStorage.setItem(LANG_KEY, lang); } catch (e) { /* noop */ }
    applyShots();
  }

  const langSelect = document.getElementById('langSelect');
  if (langSelect) {
    langSelect.addEventListener('change', function () { applyLang(this.value); });
  }

  function initialLang() {
    try {
      const saved = localStorage.getItem(LANG_KEY);
      if (saved && I18N[saved]) return saved;
    } catch (e) { /* noop */ }
    return (navigator.language || '').toLowerCase().indexOf('es') === 0 ? 'es' : 'en';
  }

  /* ---------- Tema ---------- */
  const themeBtn = document.getElementById('themeBtn');
  const themeIcon = document.getElementById('themeIcon');

  function iconPath(theme) {
    if (theme === 'dark') {
      return '<path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/>';
    }
    return '<circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M4.93 19.07l1.41-1.41M17.66 6.34l1.41-1.41"/>';
  }

  function applyTheme(theme) {
    root.setAttribute('data-theme', theme);
    if (themeIcon) themeIcon.innerHTML = iconPath(theme);
    const meta = document.querySelector('meta[name="theme-color"]');
    if (meta) meta.setAttribute('content', theme === 'dark' ? '#0d110f' : '#ffffff');
    try { localStorage.setItem(THEME_KEY, theme); } catch (e) { /* noop */ }
    applyShots();
  }

  if (themeBtn) {
    themeBtn.addEventListener('click', function () {
      const next = root.getAttribute('data-theme') === 'dark' ? 'light' : 'dark';
      applyTheme(next);
    });
  }

  function themeNow() { return root.getAttribute('data-theme') || 'light'; }
  function langNow() { return root.lang || 'es'; }
  function shotUrl(view) { return 'assets/screenshot-' + view + '-' + langNow() + '-' + themeNow() + '.webp'; }

  /* ---------- Slider de capturas ---------- */
  const shotImg = document.getElementById('shotImg');
  const shotCaption = document.getElementById('shotCaption');
  let shotIndex = 0;

  function applyShots() {
    // miniaturas
    document.querySelectorAll('.thumb img').forEach(function (img) {
      img.src = shotUrl(img.dataset.view);
    });
    // imagen principal del slider
    renderShot(shotIndex);
  }

  function renderShot(i) {
    shotIndex = (i + SLIDES.length) % SLIDES.length;
    const view = SLIDES[shotIndex];
    shotImg.src = shotUrl(view);
    shotImg.alt = SHOT_ALT[view][langNow()];
    const dict = I18N[langNow()] || I18N.es;
    shotCaption.textContent = dict['shots.s' + (shotIndex + 1)] || '';
    document.querySelectorAll('.thumb').forEach(function (th, idx) {
      th.classList.toggle('active', idx === shotIndex);
    });
  }

  const shotPrev = document.getElementById('shotPrev');
  const shotNext = document.getElementById('shotNext');
  if (shotPrev) shotPrev.addEventListener('click', function () { renderShot(shotIndex - 1); });
  if (shotNext) shotNext.addEventListener('click', function () { renderShot(shotIndex + 1); });
  document.querySelectorAll('.thumb').forEach(function (th) {
    th.addEventListener('click', function () { renderShot(parseInt(th.dataset.slide, 10)); });
  });

  /* ---------- Contadores ---------- */
  function animateCount(el) {
    const target = parseFloat(el.getAttribute('data-count')) || 0;
    const decimals = parseInt(el.getAttribute('data-decimals') || '0', 10);
    const suffix = el.getAttribute('data-suffix') || '';
    if (reduceMotion) {
      el.textContent = (decimals ? target.toFixed(decimals) : String(target)) + suffix;
      return;
    }
    const dur = 900;
    const start = performance.now();
    function tick(now) {
      const t = Math.min((now - start) / dur, 1);
      const eased = 1 - Math.pow(1 - t, 3);
      const v = target * eased;
      el.textContent = (decimals ? v.toFixed(decimals) : String(Math.round(v))) + suffix;
      if (t < 1) requestAnimationFrame(tick);
    }
    requestAnimationFrame(tick);
  }

  const counters = Array.prototype.slice.call(document.querySelectorAll('.mini-stat [data-count]'));
  if ('IntersectionObserver' in window) {
    const co = new IntersectionObserver(function (entries) {
      entries.forEach(function (entry) {
        if (entry.isIntersecting) {
          animateCount(entry.target);
          co.unobserve(entry.target);
        }
      });
    }, { threshold: 0.4 });
    counters.forEach(function (c) { co.observe(c); });
  } else {
    counters.forEach(animateCount);
  }

  /* ---------- Reveal al hacer scroll ---------- */
  const reveals = Array.prototype.slice.call(document.querySelectorAll('.reveal'));
  if ('IntersectionObserver' in window && !reduceMotion) {
    const ro = new IntersectionObserver(function (entries) {
      entries.forEach(function (entry) {
        if (entry.isIntersecting) {
          entry.target.classList.add('in');
          ro.unobserve(entry.target);
        }
      });
    }, { threshold: 0.08, rootMargin: '0px 0px -40px 0px' });
    reveals.forEach(function (r) { ro.observe(r); });
  } else {
    reveals.forEach(function (r) { r.classList.add('in'); });
  }

  /* ---------- Copiar comando ---------- */
  const copyBtn = document.getElementById('copyBtn');
  const installCmd = document.getElementById('installCmd');
  if (copyBtn && installCmd) {
    copyBtn.addEventListener('click', function () {
      const text = installCmd.textContent.trim();
      const done = function () {
        const dict = I18N[root.lang] || I18N.es;
        const orig = copyBtn.textContent;
        copyBtn.textContent = dict['misc.copied'] || (root.lang === 'es' ? 'Copiado ✓' : 'Copied ✓');
        setTimeout(function () { copyBtn.textContent = orig; }, 1600);
      };
      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(text).then(done);
      } else {
        // contexto no seguro (http): fallback legacy
        const ta = document.createElement('textarea');
        ta.value = text;
        ta.setAttribute('readonly', '');
        ta.style.position = 'fixed';
        ta.style.opacity = '0';
        document.body.appendChild(ta);
        ta.select();
        try { document.execCommand('copy'); done(); } catch (e) { /* noop */ }
        document.body.removeChild(ta);
      }
    });
  }

  /* ---------- Arranque ---------- */
  applyTheme(initialTheme());
  applyLang(initialLang());
  renderShot(0);

  function initialTheme() {
    try {
      const saved = localStorage.getItem(THEME_KEY);
      if (saved === 'light' || saved === 'dark') return saved;
    } catch (e) { /* noop */ }
    return 'light';
  }
})();
