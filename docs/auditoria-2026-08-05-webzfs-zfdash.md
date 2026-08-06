# EasyZFS — Auditoría funcional y técnica vs WebZFS y ZfDash

Fecha: 5-Ago-2026 · commit base: 75d1435 (HEAD de main)
Comparativa: EasyZFS vs [WebZFS](https://github.com/webzfs/webzfs) (149⭐, MIT) vs [ZfDash](https://github.com/ad4mts/zfdash) (239⭐, GPL-3.0)

**Estado 5-Ago-2026**: todas las funcionalidades P0, S1-S4 y frontend implementadas, verificadas (go build/vet/test 11/11, npm build + tsc limpios) y pusheadas. Pendientes: despliegue en citadel-01/02 + release v2.4.0.

**Estado 6-Ago-2026**: P1 de v2.5 reacotado a U1 (SMART drill-down) + U3 (tabla de propiedades). S5 (email), U2 (sparklines) y U4 (tooltips) movidos a P2. Specs en `docs/specs-p1-v2.5.md`.

**Estado 6-Ago-2026 (cierre)**: P1 **COMPLETADA y desplegada** en la release **v2.5.0** (issue #6, PR #7, merge `65620b4`). La release **v2.6.0** (PR #9, merge `3a73ceb`) actualizó el stack (sqlite v1.56.0, Vite 8, TS 7, Tailwind 4) sin cambios de comportamiento. Siguiente fase: P2 (ver §7).

## 1. Encuadre y metodología

Esta auditoría compara EasyZFS con las dos herramientas de gestión ZFS más cercanas en ecosistema al proyecto propio: WebZFS (Python FastAPI+HTMX, desarrollada por una persona ex-iXsystems/Klara) y ZfDash (Python GUI+Web, desarrollada por un médico como hobby). Quedan fuera TrueNAS CE, OMV y Cockpit ZFS — ya comparados en `easyzfs-vs-plataformas.md`.

**Metodología**: los claims de EasyZFS se verificaron contra el código real (`internal/actions/actions.go`, `internal/collectors/smart.go`, `internal/httpapi/`, `internal/auth/`, `api-contract.md`, vistas `web/src/views/`). Los de los competidores se extrajeron de sus README, documentación y estructura de repos en GitHub. Las celdas de la matriz se marcaron con `✅` (nativo), `✅ UI` (con interfaz), `⚠️` (parcial), `CLI` (solo línea de comandos) o `❌` (ausente).

**Regla de datos**: sin pools, seriales, IPs ni hostnames reales en este documento (repositorio público).

## 2. Inventario funcional verificado de EasyZFS (código real)

### Pools
- Crear (stripe/mirror/raidz1/raidz2/raidz3), importar (listar + importar por nombre), exportar (con force y destroy), scrub (start/pause/stop), trim, autotrim toggle, checkpoint (create/discard), añadir vdev, acciones de vdev (offline/online/detach), replace, RAID-Z expansion (`zpool attach`), historial (`zpool history -i`)
- Topología, estado, bytes usados/totales, compresión, fragmentación, vdevs con temperatura, scrub/resilver/trim/expand unificado con progreso

### Datasets
- Crear (fs/volume, compresión lz4/zstd/off, quota, volsize, cifrado aes-256-gcm con passphrase por stdin), patch (quota, compresión), borrar (recursivo)
- Cifrado: unlock (`zfs load-key`), lock (`zfs unload-key`), change-key
- Rewrite (`zfs rewrite -r -x <mountpoint>`, version-gated)
- **NO**: clone, diff, rename, mount, unmount, promote, inherit, edición de propiedades más allá de quota/compresión

### Snapshots
- Crear (recursivo), borrar, rollback, prune (retención automática)
- **NO**: clone, diff

### Discos y SMART
- Inventario físico (lsblk + whitelist sda/hda/vda/nvme/eMMC, excluye zvols/loop/ram/dm-*)
- SMART: estado PASSED/FAILED, temperatura, Power_On_Hours, reallocated/pending/offline_uncorrectable/UDMA_CRC (ATA), critical_warning (NVMe)
- Tests SMART (short/long) — solo lanzamiento, sin historial de tests ni log de errores
- Apagado/standby de disco (udisksctl power-off, fallback hdparm -y)
- Delta CRC por serial entre pasadas (motor cable/puerto roto)
- **NO**: vista de tabla de atributos completa, historial de self-test, log de errores SMART

### Jobs programados
- Tipos: snapshot, scrub, trim, smart_short, smart_long
- Formato propio: `hourly@:15`, `daily@06:00`, `weekly:sun@03:00`, `monthly:1@02:00`
- Retención configurable por job, historial de ejecuciones

### Tareas del sistema
- Vista read-only de cron + systemd timers. Filtrar relevantes ZFS. Migración cron→systemd vía helper root confinado

### Alertas y push
- Web Push VAPID: suscripción por dispositivo, preferencias por tipo, quiet hours, i18n ES/EN
- Webhook POST JSON básico (sin HMAC ni reintentos)
- Alertas por umbrales (capacidad, temp disco, estado pool, SMART)
- Eventos zed en vivo (`zpool events -f`): mapeo de ereport/sysevent a alertas con target navegable
- **NO**: email/SMTP

### Replicación
- send/recv incremental (local y SSH), bookmarks `ezrepl-*`, force_full, clave ed25519 del daemon
- Test de conectividad SSH (zfs version remoto)
- Jobs con schedule propio, progreso vía longops runner

### Rendimiento
- ARC stats (size, hit%) de `/proc/spl/kstat/zfs/arcstats` (fallback zarcsummary/arc_summary)
- Lectura/escritura por pool en bytes/s (`zpool iostat -Hpy 1 1`)

### Seguridad y multiusuario
- Auth: argon2id (m=64MiB, t=3, p=2), semáforo de memoria, dummy hash anti-timing
- Sesiones: cookie HttpOnly, token|HMAC-SHA256, SameSite=Lax, expiración 7 días, purge
- Roles: admin/viewer. Confirm en destructivas. audit_log en toda mutación
- Rate limiting en login (IP+usuario, 5/min, bloqueo 15 min tras 10 fallos)
- Sudoers limitados con args restringidos (zpool/zfs/smartctl/lsblk/crontab -l/hdparm -y/udisksctl power-off)
- Helper root confinado (3 operaciones validadas sobre /etc/cron* y /etc/systemd)

### Plataforma
- Binario Go único estático (CGO_ENABLED=0, modernc.org/sqlite), ~12 MB
- Un solo archivo, una sola cosa: despliegue = `scp + restart`. RAM: decenas de MB
- Instalador one-liner multi-distro con whiptail, deps ZFS por distro, sha256 obligatorio
- PWA instalable, tema claro/oscuro, i18n ES/EN, modo demo (`DEMO=1`)

## 3. Matriz funcional comparativa

### 3.1 Pools y discos

| Función | EasyZFS | WebZFS | ZfDash |
|---|---|---|---|
| Crear pool (varios vdevs/topologías) | ✅ | ✅ | ✅ |
| Importar pool | ✅ | ✅ | ✅ |
| Exportar pool | ✅ | ✅ | ✅ |
| Destruir pool | ✅ | ❌ (intencional) | ✅ |
| Scrub (start/pause/stop) | ✅ | ✅ | ✅ |
| Clear errors (`zpool clear`) | ❌ | ❌ | ✅ |
| TRIM manual | ✅ | — | — |
| Autotrim toggle | ✅ | — | — |
| Añadir/quitar vdev | ✅ (add/attach/detach) | — | ✅ (add/remove/attach/detach/replace) |
| Reemplazar disco | ✅ | — | ✅ |
| Offline/Online vdev | ✅ | — | ✅ (force option) |
| RAID-Z expansion | ✅ UI | ❌ | ❌ |
| Pool checkpoint | ✅ UI | ❌ | ❌ |
| Historial del pool | ✅ (`zpool history -i`) | ✅ (history + kernel logs + module params) | ❌ |
| Progreso en vivo (scrub/resilver/expand) | ✅ (SSE) | — | ⚠️ (replication progress) |
| SMART de discos | ✅ (básico: PASSED/FAILED + attrs clave) | ✅ (atributos completos + test scheduling + error logs) | ❌ |
| Apagar disco físicamente | ✅ | — | — |
| Recomendaciones de sustitución | ✅ (motor recs) | ❌ | ❌ |

### 3.2 Datasets y snapshots

| Función | EasyZFS | WebZFS | ZfDash |
|---|---|---|---|
| Crear dataset (fs/volume) | ✅ (comp, quota, volsize, cifrado) | ✅ | ✅ |
| Borrar dataset (recursivo) | ✅ | ❌ (intencional) | ✅ |
| Rename dataset | ❌ | ✅ | ✅ |
| Mount/unmount dataset | ❌ | ✅ | ✅ |
| Editar propiedades | ⚠️ (solo quota/compresión) | ✅ | ✅ (tabla completa + inherit + promote) |
| Crear snapshot (recursivo) | ✅ | ✅ | ✅ |
| Borrar snapshot | ✅ | ✅ | ✅ |
| Rollback | ✅ | ✅ | ✅ |
| **Clone de snapshot** | ❌ | ✅ | ✅ |
| **Snapshot diff** | ❌ | ✅ | ❌ |
| Promote (de clone) | ❌ | ✅ | ✅ |
| Cifrado nativo | ✅ (crear, unlock, lock, change-key) | ❌ | ✅ (crear, estado, load/unload/change) |
| Rewrite (`zfs rewrite`) | ✅ UI | ❌ | ❌ |

### 3.3 Replicación y backups

| Función | EasyZFS | WebZFS | ZfDash |
|---|---|---|---|
| send/recv local | ✅ (incremental, bookmarks) | ✅ (Sanoid/Syncoid integrado) | ✅ (local) |
| send/recv SSH | ✅ | ✅ (Sanoid/Syncoid) | ✅ (SSH) |
| send/recv agente-a-agente | ❌ | — | ✅ (TLS) |
| Export a fichero | ❌ | — | ✅ |
| Resume de send | ✅ (bookmark) | — | ✅ (resume) |
| Detección incremental automática | ✅ (GUID vía bookmark) | ✅ (Sanoid/Syncoid) | ✅ (GUID) |
| Backup de la BD de la app | ✅ (VACUUM INTO, scheduler propio) | — | — |

### 3.4 Alertas y notificaciones

| Función | EasyZFS | WebZFS | ZfDash |
|---|---|---|---|
| Web Push al móvil | ✅ (VAPID, i18n ES/EN) | ❌ | ❌ |
| Quiet hours | ✅ | ❌ | ❌ |
| Preferencias por tipo | ✅ | ❌ | ❌ |
| Webhook saliente | ⚠️ (POST básico, sin HMAC) | ❌ | ❌ |
| Email/SMTP | ❌ | ❌ | ❌ |
| Umbrales configurables | ✅ | ❌ | ❌ |
| Eventos zed en vivo | ✅ (`zpool events -f` → SSE) | ✅ (logs ZED en UI) | ❌ |
| Log de eventos/actividad | ✅ (audit_log + actividad en Ajustes) | ✅ (kernel logs, module params) | ⚠️ (command logging opcional) |

### 3.5 Automatización

| Función | EasyZFS | WebZFS | ZfDash |
|---|---|---|---|
| Scheduler de jobs propio | ✅ (snap/scrub/trim/smart + retención) | ❌ (delega en Sanoid/Syncoid/smartd) | ❌ |
| Schedule flexible (hourly/daily/weekly/monthly) | ✅ | — | — |
| Vista de tareas del sistema | ✅ (cron + systemd, read-only) | — | — |
| Migración cron→systemd | ✅ (helper confinado) | — | — |

### 3.6 Seguridad y multiusuario

| Función | EasyZFS | WebZFS | ZfDash |
|---|---|---|---|
| Auth multiusuario | ✅ (argon2id, roles admin/viewer) | ⚠️ (PAM del sistema, sin roles de app) | ✅ (PBKDF2, sin roles) |
| Confirmación en destructivas | ✅ (`{"confirm":"<name>"}`) | ❌ | ⚠️ (three-button modals) |
| Audit log | ✅ (toda mutación) | ❌ | ⚠️ (opcional) |
| Rate limiting | ⚠️ (solo login) | ❌ | ❌ |
| CSRF | ⚠️ (SameSite=Lax, sin Origin check) | ❌ | ❌ |
| Sudoers limitado + args restringidos | ✅ | ✅ (sudo para comandos aprobados) | ✅ (polkit/daemon) |
| Sin credenciales por defecto | ✅ (genera admin password aleatoria) | — (usa PAM, sin bootstrap) | ❌ (`admin`/`admin`) |
| Sesiones con expiración | ✅ (7 días + purge) | — (PAM, sin sesiones propias) | ✅ (Flask-Login) |
| Password vault (creds agentes) | — | — | ✅ (autolock en logout) |

### 3.7 Plataforma y despliegue

| Función | EasyZFS | WebZFS | ZfDash |
|---|---|---|---|
| Tipo | Binario Go estático único | Python FastAPI + HTMX + Tailwind | Python (GUI PySide6 + Web Flask) |
| Tamaño | ~12 MB | ~500+ MB (Python venv + deps) | ~200+ MB (Python venv + deps) |
| RAM típica | 14-40 MB | ~200+ MB | ~150+ MB |
| Instalación | One-liner curl\|bash multi-distro | git clone + script (Linux/FreeBSD/NetBSD) | One-liner curl\|bash + Docker |
| Docker requerido | ❌ | ❌ | ⚠️ (opcional pero --privileged) |
| Plataformas | Linux x86_64, ARM64 | Linux, FreeBSD, NetBSD | Linux, macOS (exp.), FreeBSD (exp.) |
| Multi-host | ❌ (single host) | ✅ (fleet monitoring opcional) | ✅ (agent mode con TLS) |
| Desktop GUI | ❌ | ❌ | ✅ (PySide6) |

### 3.8 Experiencia

| Función | EasyZFS | WebZFS | ZfDash |
|---|---|---|---|
| PWA instalable | ✅ | ❌ (web tradicional) | ❌ (web tradicional) |
| Tema claro/oscuro | ✅ | ❌ | ❌ |
| i18n | ✅ (ES/EN/Auto por usuario) | ❌ (solo EN) | ❌ (solo EN) |
| Modo demo sin tocar discos | ✅ | ❌ | ❌ |
| Móvil responsive | ✅ | ❌ | ❌ |
| Diseño de sistema propio | ✅ (CSS a mano, verde/crema, JetBrains Mono) | ✅ (Tailwind + HTMX) | ✅ (Bootstrap modals) |
| Ayudas contextuales | ❌ | ❌ | ⚠️ (en roadmap) |
| Gráficas/evolución histórica | ❌ (solo tabla perf instantánea) | ⚠️ (real-time, sin histórico) | ❌ |
| Actualización proactiva del SW | ⚠️ (check semanal + ribbon, sin skipWaiting) | — | — |

## 4. Análisis por bloque

### Dónde EasyZFS gana sin discusión

1. **Huella y despliegue**: un binario de 12 MB. Ni WebZFS (Python venv, cientos de MB) ni ZfDash (Python + deps) compiten en un LXC de 256 MB. El one-liner de EasyZFS es el más pulido de los tres (whiptail, distros múltiples, sha256, credenciales aleatorias).

2. **Alertas push**: EasyZFS es la única con Web Push reales al móvil con la app cerrada. WebZFS y ZfDash ni lo mencionan. Es la killer feature para el homelab.

3. **Multiusuario real**: EasyZFS tiene usuarios con roles, argon2id, audit_log y confirm en destructivas. WebZFS depende de PAM (sin roles de app, sin audit propio). ZfDash tiene login PBKDF2 pero arranca con `admin/admin` y sin roles. En un NAS compartido (familia, colegas), EasyZFS es la única opción segura de las tres.

4. **Motor de recomendaciones**: exclusivo de EasyZFS. Ni WebZFS ni ZfDash dicen QUÉ disco cambiar, POR QUÉ ni con qué guardas de seguridad.

5. **Scheduler propio**: los jobs de EasyZFS (snapshot/scrub/trim/SMART con retención y schedule flexible) no existen en los otros. WebZFS delega en Sanoid/Syncoid/smartd (herramientas externas). ZfDash carece por completo.

6. **Operaciones avanzadas ZFS con UI**: `zfs rewrite`, checkpoint de pool, RAID-Z expansion con progreso en vivo — solo EasyZFS las expone en interfaz. Son las herramientas justo para los momentos delicados.

7. **Experiencia general**: PWA instalable, tema, i18n ES/EN real, modo demo, responsive móvil. WebZFS es web tradicional sin tema ni i18n. ZfDash tiene GUI de escritorio + web, ambas solo EN.

### Dónde EasyZFS pierde (hoy)

1. **Clone y diff de snapshots**: ambos competidores tienen clone; WebZFS tiene diff. Son flujos básicos de ZFS que EasyZFS no expone. El clone es el hueco más grande en datasets/snapshots.

2. **Gestión de propiedades**: ZfDash ofrece tabla completa + inherit + promote. EasyZFS solo quota y compresión. Perder la capacidad de ver y editar propiedades desde la UI obliga a la CLI para tareas cotidianas (recordsize, atime, sync…).

3. **Mount/unmount y rename**: operaciones básicas de cualquier dataset. Ambos competidores las tienen; EasyZFS no.

4. **SMART profundo**: WebZFS muestra atributos completos, programa tests y expone logs de error. EasyZFS parsea solo 4 atributos ATA y lanza tests sin historial. Para diagnosticar un disco en detalle, toca CLI.

5. **Multi-host**: WebZFS tiene fleet monitoring; ZfDash tiene agent mode con TLS+discovery. EasyZFS es single-host. Para quien tiene más de un servidor ZFS, esto pesa.

6. **Email**: nadie lo tiene. Es un gap común pero EasyZFS es la que mejor posicionada está para cerrarlo (stack Go con deps mínimas, webhook ya existe como base).

7. **Gráficas históricas**: EasyZFS guarda series de temperatura de disco pero no las pinta. No hay evolución de capacidad ni rendimiento. La tabla de `perf` es instantánea (últimos 60 s). La sensación de "monitorización" frente a "estado" la dan las tendencias.

## 5. Seguridad comparada + recomendaciones

### Estado actual — el más sólido de los tres

EasyZFS parte de la posición más segura:

- Auth con argon2id y parámetros documentados, dummy hash anti-timing y semáforo de memoria (2 verificaciones simultáneas máx).
- Sesiones con token aleatorio + firma HMAC-SHA256 en la cookie. Expiración, purge periódico.
- Rate limiting en login por IP+usuario con bloqueo progresivo.
- Confirmación obligatoria en destructivas con `{"confirm":"<nombre>"}`.
- audit_log en toda mutación.
- Sudoers limitado con args restringidos (no ejecución arbitraria como root).
- Helper root confinado (3 operaciones validadas, solo sobre /etc/cron* y /etc/systemd).
- Sin secretos en el repo (verificado).
- Modo demo aislado (mutaciones 403, datos mock).

### Recomendaciones de seguridad (priorizadas por impacto/esfuerzo)

**P0 — Bajo esfuerzo, alto blindaje:**

| # | Recomendación | Riesgo actual | Implementación |
|---|---|---|---|
| S1 | **SESSION_SECRET siempre generado** | Las sesiones no sobreviven a reinicios si no se define (fallback efímero). El instalador ya lo genera para VAPID: añadir `SESSION_SECRET=$(openssl rand -hex 32)` en install.sh | Modificar `deploy/install.sh`: generar + escribir en `/etc/easyzfs/env` si no existe. El código ya lo soporta (sha256 del valor) |
| S2 | **CSRF: check de Origin/Referer** | Hoy solo SameSite=Lax. Si la app se expone a internet, un ataque desde otro origen podría tener éxito (necesitaría credenciales, pero existe el vector) | Middleware en `httpapi.go`: comparar `r.Header.Get("Origin")` con `r.Host` en POST/PUT/DELETE. Rechazar sin Origin o con mismatch → 403. Opcional vía env `CSRF_CHECK=1` (defecto off en LAN, on con COOKIE_SECURE) |
| S3 | **Webhook con HMAC-SHA256 + reintentos** | El webhook actual es un POST sin autenticación: cualquier persona que conozca la URL puede enviar datos falsos al destino. Sin reintentos ni log de fallos | Añadir `WEBHOOK_SECRET` al env. Firma `HMAC-SHA256(payload, secret)` en cabecera `X-EasyZFS-Signature`. 3 reintentos con backoff exponencial (1s, 5s, 25s). Log en `audit_log` con `action="webhook.delivery"` y resultado. Timeout configurable (`WEBHOOK_TIMEOUT`, def 10s) |

**P1 — Medio esfuerzo, protección adicional:**

| # | Recomendación | Riesgo actual | Implementación |
|---|---|---|---|
| S4 | **Rate-limit en mutaciones autenticadas** | Un cliente con credenciales válidas puede martillear endpoints de mutación (crear/borrar pools, datasets, jobs). Riesgo bajo (necesita auth) pero la protección es barata | Generalizar `loginLimiter` → `actionLimiter` por IP (bucket de 30 acciones/minuto). Middleware `requireActionLimit` en POST/PUT/DELETE. 429 con mensaje legible. Sin estado persistente (memoria, aceptable para LAN) |
| S5 | **Email/SMTP** (funcionalidad, pero también reduce dependencia del webhook inseguro) | Sin notificaciones por correo, el webhook es el único canal externo. Un correo con credenciales SMTP propias es más fiable y ubícuo | `SMTP_HOST/PORT/USER/PASS/FROM` en env. `gopkg.in/gomail.v2` o `net/smtp` nativo (ya en stdlib). Plantillas ES/EN. Rate-limit: máx 1 correo/min por tipo de alerta. Digest horario opcional |

**P2 — Largo plazo:**
- Origin check por defecto cuando `COOKIE_SECURE=1` (TLS activo → exposición externa probable).
- Rotación de `SESSION_SECRET` con periodo de gracia (mantener el anterior 24h para no invalidar sesiones vivas en el reinicio).
- OIDC/LDAP opcional para integrarse con Authentik/SSO (P3 del roadmap anterior).

## 6. UX comparada + recomendaciones

### Fortalezas actuales

- **PWA completa**: instalable, tema claro/oscuro, i18n ES/EN con cambio en caliente, responsive (desktop/móvil con sidebar colapsable, bottomnav).
- **Dashboard coherente**: KPIs, donut de salud, recs, tarjetas de pool con progreso en vivo, rendimiento y actividad.
- **Modo demo**: permite enseñar la app sin acceso a ZFS real.
- **Flujo de confirmación**: `{"confirm":"<name>"}` + audit_log en toda mutación destructiva.

### Puntos de mejora UX (priorizados)

**P0 — Cierran flujos incompletos (mismo esfuerzo que los P0 funcionales):**

| # | Mejora | Problema actual | Diseño |
|---|---|---|---|
| U1 | **Clone en flujo de snapshots** | Ver un snapshot sin poder clonarlo rompe el flujo mental "restauro → clono → promociono" | Acción "Clonar" en la fila de snapshot (Datasets.tsx). Modal: nombre del nuevo dataset, mountpoint opcional. Al crear, redirigir a Datasets. El clone hereda propiedades del snapshot. Sin confirm: no es destructivo |
| U2 | **Diff entre snapshots** | Sin diff, el usuario no sabe qué cambió entre dos snapshots antes de un rollback | Selector de 2 snapshots en Snapshots.tsx → tabla diff (`+` creados, `-` borrados, `M` modificados, `R` renombrados, tipo de fichero). Resultado en modal, sin scroll infinito (solo primeras 200 entradas + aviso si hay más) |
| U3 | **Rename y mount/unmount inline** | Sin poder renombrar ni montar desde la UI, el dataset queda "atrapado" en su nombre inicial | Dataset.tsx: acción "Renombrar" (modal con input + validación de nombre) y toggle "Montado" (zfs mount/unmount sin -f, como el lock/unlock de cifrado) |

**P1 — Valor añadido visible:**

| # | Mejora | Diseño |
|---|---|---|
| U4 | **Gráficas históricas (sparklines)** | PoolCard: mini-gráfico de ocupación 7d/30d debajo de la barra de capacidad. Disco: sparkline de temperatura 24h. Dashboard: tendencia de ARC hit% + bps acumulado. Usar datos de `series` (hoy solo temp; añadir `pool.<name>.used_bytes` y `pool.<name>.read_bps`/`write_bps`). Retención con el patrón `sqlite-timeseries-daemon` (raw 7d → buckets 5min 1 año → daily ∞). LTTB para muestreo visual |
| U5 | **SMART drill-down por disco** | Fila de disco clickeable → panel lateral o vista de detalle con: tabla de atributos completa (nombre, valor raw, umbral, estado), historial de self-tests (tipo, resultado, % completado, duración), log de errores. Endpoints nuevos: `GET /api/disks/{dev}/smart` (atributos completos) y `GET /api/disks/{dev}/smart-log` (selftest + error log) |
| U6 | **Tabla de propiedades del dataset** | Modal o vista de detalle del dataset: tabla completa de propiedades (`zfs get all` → filtrar las modificables: recordsize, atime, sync, dedup, xattr, acltype…). Cada fila: nombre, valor actual, source (local/default/inherited), botón "Editar" + botón "Inherit" (si no es local). Whitelist estricta de propiedades y valores (regex + enum), mismo patrón que el resto de whitelists |
| U7 | **Ayudas contextuales (tooltips)** | En "Crear pool": tooltip por topología ("Un mirror es como un RAID 1: cada disco es una copia exacta. Un raidz2 tolera 2 fallos. Un stripe sin redundancia es rápido pero si falla un disco lo pierdes todo"). En "Añadir vdev": tooltip por tipo ("LOG mejora escrituras síncronas con un SSD rápido. SPECIAL almacena metadatos y bloques pequeños"). i18n ES/EN. Implementación: componente `HelpTip` (icono `?` con hover → tooltip) |

**P2 — Experiencia premium:**

| # | Mejora | Diseño |
|---|---|---|
| U8 | **Onboarding "Primeros pasos"** | Vista descartable que aparece tras el primer login (persiste en `settings`). Checklist: (1) importar pool existente o crear uno nuevo, (2) crear un job de snapshots diario para tu dataset principal, (3) suscribir notificaciones push, (4) configurar alertas. Cada paso es un enlace a la vista correspondiente. Botón "Entendido, empezar" al final |
| U9 | **Update proactivo del SW** | Cache de assets con `skipWaiting` + `clientsClaim` en el service worker. Al detectar nueva versión: toast "Nueva versión instalada — Recargar" (sin recarga forzosa). El ribbon actual de "Nueva versión disponible" ya funciona bien para el aviso. Queda la capa de actualización automática |
| U10 | **Atajos de teclado** | `Ctrl+K` → búsqueda/paleta de comandos (saltar a pool, dataset, vista). Patrón de app madura (GitHub, Linear). Barato de implementar: lista plana de rutas + nombres de recursos |

## 7. Roadmap integrado (función + seguridad + UX)

Orden de ejecución recomendado, combinando las tres dimensiones en una secuencia coherente. Cada fila incluye versión objetivo sugerida (desde el HEAD actual 75d1435), paquete Go afectado y tests nuevos.

### P0 — v2.4.x (~2-3 releases) ✅ COMPLETADO 5-Ago-2026 (commits 5bac5a9, ad35ec4)

Todas las funcionalidades P0 implementadas backend + frontend: F1-F6 (clone, diff, rename, clear, mount/unmount, webhook HMAC), S1-S4 (session secret, CSRF, rate-limit). Verificado: go build/vet/test (11/11), npm build + tsc limpios.

---

### P1 — v2.5.x ✅ COMPLETADA 6-Ago-2026 (release v2.5.0)

| # | Área | Feature | Esfuerzo | Paquetes | Tests sugeridos |
|---|---|---|---|---|---|
| U1 | UX | SMART drill-down + tabla atributos | Medio | `internal/collectors/`, `internal/httpapi/`, `web/src/views/Disks.tsx` | Test parseo de `smartctl -j -a` con todos los atributos; test selftest log vacío; test disco sin SMART (unknown) |
| U3 | UX | Tabla de propiedades + edición | Medio-alto | `actions/`, `httpapi/`, `web/src/views/Datasets.tsx` | Test get all → whitelist; test set valor válido; test set valor inválido (rechazo); test inherit; test propiedad readonly |

Ambas implementadas (backend + front + mock + i18n + tests Go 12/12 + build limpio + E2E demo), issue #6 → PR #7 → merge `65620b4`. Specs y verificación: `docs/specs-p1-v2.5.md`.

### P2 — v2.7+ (largo plazo; reordenado 6-Ago-2026)

Items arrastrados de P1 (decisión 6-Ago-2026: acotar el alcance de v2.5 a las dos features de medio esfuerzo, el resto no se apila en la misma fase):

| # | Área | Feature |
|---|---|---|
| S5 | Seguridad | Email/SMTP (nuevo paquete + plantillas i18n + config) |
| U2 | UX | Gráficas históricas (sparklines + retención) |
| U4 | UX | Ayudas contextuales tooltips |
| F6 | Plataforma | Multi-host / fleet (agentes ligeros SSH) |
| U5 | UX | Onboarding "Primeros pasos" |
| U6 | UX | SW update automático (skipWaiting + clientsClaim) + bundle split |
| U7 | UX | Activity con paginación keyset |
| P1 | Rendimiento | Partir bundle (manualChunks: react-dom, i18n) |
| M1 | Observabilidad | Export métricas Prometheus (`/metrics`) |
| M2 | Auth | OIDC/LDAP opcional para SSO doméstico |

### Sin cambios (decisión deliberada)

- **No shares SMB/NFS/iSCSI**: fuera de alcance por diseño. EasyZFS es gestor ZFS, no servidor de ficheros.
- **No apps/Docker/VMs**: fuera de alcance.
- **Jobs en systemd timers en vez del scheduler propio**: decisión de diseño revisada y mantenida (el scheduler propio es fuente de verdad única con historial/SSE, sin ampliar superficie root).

## 8. Verificación del análisis

Los claims de EasyZFS en este documento se verificaron contra el código:
- **Clone**: 0 referencias en `internal/` (grep `clone` en .go, excluyendo tests) → ausente.
- **Diff**: 0 referencias a `zfs diff` en `internal/` → ausente.
- **Rename**: solo aparece en os.Rename de backup/avatar (no operaciones ZFS) → ausente.
- **Mount/unmount**: mountpoint en model.go, usado en rewrite (longops.go:57-61), pero sin endpoint de montaje explícito → ausente como operación.
- **Clear**: 0 referencias a `zpool clear` → ausente.
- **Promote**: 0 referencias → ausente.
- **Inherit**: 0 referencias → ausente.
- **Clone, diff, rename, promote, mount, unmount, clear, inherit confirmados ausentes del código actual.**

Los claims de WebZFS y ZfDash se extrajeron de sus README y documentación en GitHub a 5-Ago-2026, verificando la versión pública más reciente. Las celdas marcadas `—` indican información no disponible o no documentada en sus README (no implica necesariamente ausencia, sino falta de evidencia pública).

## 9. Fuentes

- EasyZFS: código en `internal/actions/actions.go`, `internal/collectors/smart.go`, `internal/recs/`, `internal/auth/`, `internal/config/`, `internal/httpapi/`, `docs/api-contract.md`, `web/src/views/`.
- WebZFS: [github.com/webzfs/webzfs](https://github.com/webzfs/webzfs) (README, BUILD_AND_RUN.md, estructura de repo).
- ZfDash: [github.com/ad4mts/zfdash](https://github.com/ad4mts/zfdash) (README, ARCHITECTURE.md, changelog v2.0.0).
- Auditoría previa: `docs/auditoria-2026-08-04.md` y `docs/easyzfs-comparativa-roadmap.md`.
- OpenZFS: [github.com/openzfs/zfs/releases](https://github.com/openzfs/zfs/releases), manpages (zfs-rewrite.8, zpool-wait.8, zpool-events.8).
