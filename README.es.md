# EasyZFS

<p align="center">
  <a href="README.md">English</a> |
  <a href="README.es.md">Español</a>
</p>

<p align="center">
  <a href="https://easyzfs.cloudless.club"><img alt="Sitio web" src="https://img.shields.io/badge/Website-easyzfs.cloudless.club-blue"></a>
  <a href="https://demo.easyzfs.cloudless.club"><img alt="Demo en vivo" src="https://img.shields.io/badge/Live%20demo-demo.easyzfs.cloudless.club-blue"></a>
  <a href="https://github.com/gnacho/easyzfs/releases"><img alt="Release" src="https://img.shields.io/github/v/release/gnacho/easyzfs"></a>
  <a href="https://github.com/gnacho/easyzfs/actions/workflows/release.yml"><img alt="CI" src="https://img.shields.io/github/actions/workflow/status/gnacho/easyzfs/release.yml?branch=main"></a>
  <a href="LICENSE"><img alt="Licencia" src="https://img.shields.io/github/license/gnacho/easyzfs"></a>
  <a href="https://ko-fi.com/gnacho"><img alt="Apóyame en Ko-fi" src="https://img.shields.io/badge/Ko--fi-Donate-ff5e5b?logo=ko-fi&logoColor=white"></a>
</p>

<p align="center"><a href="https://demo.easyzfs.cloudless.club"><strong>Prueba la demo en vivo</strong></a> en <code>demo.easyzfs.cloudless.club</code></p>

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/hero-es-dark.png">
    <source media="(prefers-color-scheme: light)" srcset="assets/hero-es-light.png">
    <img alt="Panel de EasyZFS mostrando dos pools (tank raidz1 degradado con oferta de reconstrucción, mirror ssd reconstruyéndose al 53%), barras de capacidad, estado de discos y de scrub" src="assets/hero-es-light.png" width="800">
  </picture>
</p>

EasyZFS es una app de gestión ZFS que vive **en el propio NAS**: un único
binario Go estático que envuelve los comandos del sistema (`zpool`, `zfs`,
`smartctl`, sensores hwmon), expone una API REST + SSE y sirve una PWA
embebida. Corre 24/7 con una huella mínima y se despliega en un LXC Debian
+ systemd. Sin Docker, sin sistema operativo de appliance, sin stack
pesado.

> **Prueba la demo en vivo**
>
> Mírala en funcionamiento sin instalar nada. Entra en **[demo.easyzfs.cloudless.club](https://demo.easyzfs.cloudless.club)** - dos pools de ejemplo con datos mock realistas, sin registro. En modo demo las acciones destructivas están bloqueadas, así que puedes explorar sin riesgo.

## ¿Por qué existe?

Tras años con NAS comerciales (primero Synology, luego QNAP) acabé con
ecosistemas cautivos sobre hardware que envejece rápido y no se puede
actualizar. Así que pasé a un HP mini con Proxmox, unificando NAS y homelab
en una sola caja. Proxmox no tiene una interfaz amigable para gestionar el
RAID, así que probé una MV con TrueNAS. Spoiler: fue un error. Todo
funcionaba, pero cada reinicio dejaba a los contenedores esperando a un
arranque lento de TrueNAS, consumía mucha RAM, pedía mantenimiento aparte y
orquestarlo con el resto del stack acabó siendo una fuente de estrés. Ganó
lo obvio: quitar TrueNAS e importar los pools directamente en Proxmox.
Gestionar ZFS a mano me enseñó los comandos, pero seguía echando de menos
una UI web pequeña, amigable y minimalista para el día a día. No encontré
ninguna que me convenciera, así que la construí.

## EasyZFS vs TrueNAS CE vs OpenMediaVault

El encuadre importa: TrueNAS y OMV son sistemas NAS completos (shares,
apps, VMs). EasyZFS no intenta serlo. Responde a una pregunta más
estrecha: ¿con qué gestionas tus pools ZFS? En esa categoría, este es el
panorama (análisis completo en [docs/easyzfs-vs-plataformas.md](docs/easyzfs-vs-plataformas.md)):

| | EasyZFS | TrueNAS CE | OMV 8 |
|---|:---:|:---:|:---:|
| RAM necesaria | **decenas de MB** | 8 GB (16 recomendados) | ~1 GB |
| Corre en | cualquier Linux, VM, LXC de 256 MB | solo su appliance | una Debian dedicada |
| ZFS es | el producto entero | el producto | un plugin de terceros |
| `zfs rewrite` (el defrag real) con UI | ✅ | solo CLI | ❌ |
| Checkpoint de pool con UI | ✅ | solo CLI | ❌ |
| Expansión RAID-Z con progreso en vivo | ✅ | ✅ | ❌ |
| Recomendaciones de sustitución de discos (qué disco, por qué, guardas) | ✅ | ❌ | ❌ |
| Avisos push al móvil, cero terceros | ✅ (Web Push) | email/webhooks | email |
| Eventos zed en vivo + progreso de operaciones (SSE) | ✅ | parcial | ❌ |
| Shares SMB/NFS, apps, VMs | ❌ (por diseño) | ✅ | ✅ |

Tres puntos cierran el caso:

1. **Corre donde las otras no caben.** Un LXC de 256 MB con cualquier
   distro ya es un servidor EasyZFS completo. TrueNAS exige 8 GB y la
   máquina entera; OMV exige una Debian dedicada.
2. **Avisos que llegan al bolsillo.** Web Push real al móvil con la app
   cerrada: horas de silencio, severidades, ES/EN, sin ningún servicio de
   terceros por medio. El aviso de "disco degradado" en 5 segundos vale
   más que un informe histórico.
3. **Cero riesgo de pivote.** EasyZFS hace una cosa y la seguirá haciendo.
   TrueNAS ha cambiado de rumbo tres veces en tres años y abandonó CORE;
   OMV mantiene ZFS vivo a través de un plugin de extras.

La cara honesta: no hay shares SMB/NFS/iSCSI, ni apps ni VMs, y TrueNAS
gana en RBAC granular, auditoría empresarial, madurez de la replicación y
comunidad. Si necesitas un NAS completo, usa TrueNAS. Si necesitas
controlar ZFS en cualquier Linux con 50 MB de RAM y un binario, EasyZFS es
la única herramienta de su categoría.

## ¿Por qué este stack?

- **Go, un único binario estático**: un daemon 24/7 en un LXC pequeño:
  ~12 MB, RSS mínima, sin runtime que mantener; una actualización es
  cambiar un fichero.
- **`modernc.org/sqlite` (Go puro)**: mantiene `CGO_ENABLED=0`, así el
  binario es totalmente estático y no necesita toolchain C en el NAS. Solo
  2 dependencias Go, a propósito.
- **Colectores + caché, nunca CLI desde HTTP**: los colectores sondean
  `zpool`/`smartctl`/sensores a una caché en memoria y publican SSE; los
  handlers solo leen la caché. La UI responde al instante por lentos que
  sean los discos.
- **systemd + sudoers limitado, sin Docker**: el servicio corre sin
  privilegios y solo eleva los binarios en whitelist (`zpool`, `zfs`,
  `smartctl`…). Docker añadiría una capa sin aportar nada aquí.
- **PWA embebida React + Vite**: la shell de UI (tema, i18n, ajustes) es
  compartida con mis otras apps, así que un arreglo llega a todas a la vez.

## Características

- Pools, datasets, snapshots, discos (SMART), temperaturas, en vivo por SSE
- Tareas programadas: snapshots, scrubs, tests SMART (`easyzfs-auto-*` con
  retención por tarea)
- Vista de tareas del sistema: timers de systemd + cron (solo lectura)
- Filtro de inventario de discos físicos: zvols, loop, particiones eMMC de
  boot y otros pseudo-dispositivos quedan ocultos; eMMC/USB sin SAT
  reportan SMART como "desconocido"
- Auth multiusuario (argon2id, roles admin/viewer), cookies de sesión
  HMAC-SHA256
- Registro de auditoría de toda acción mutante; las operaciones destructivas
  requieren `{"confirm":"<nombre>"}`
- Alertas Web Push (capacidad de pool, DEGRADED/FAULTED, scrubs con
  errores, temperatura de disco, avisos SMART); ver
  [Notificaciones push](#notificaciones-push)
- Modo demo (`DEMO=1`): datos mock realistas, toda mutación devuelve 403,
  así que es seguro para enseñar
- PWA: instalable, tema claro/oscuro, i18n es/en/auto
- SQLite embebido (WAL) para usuarios, sesiones, ajustes, alertas, series e
  historial de tareas

## Capturas

**Pools:** topología, salud y oferta de reconstrucción al detectar un disco libre**

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/screenshot-pools-es-dark.png">
  <source media="(prefers-color-scheme: light)" srcset="assets/screenshot-pools-es-light.png">
  <img alt="Vista de pools mostrando el pool raidz1 tank degradado con banner de reconstrucción por disco libre, discos miembros con temperaturas y el mirror ssd reconstruyéndose" src="assets/screenshot-pools-es-light.png" width="800">
</picture>

**Discos:** salud SMART, temperaturas y horas encendido por disco físico**

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/screenshot-disks-es-dark.png">
  <source media="(prefers-color-scheme: light)" srcset="assets/screenshot-disks-es-light.png">
  <img alt="Tabla de discos con modelo, serie, tamaño, temperatura, salud SMART y pool de cada disco físico, con un disco avisando de sectores reasignados" src="assets/screenshot-disks-es-light.png" width="800">
</picture>

**Ajustes:** tema, acento, usuarios, notificaciones push y backups**

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/screenshot-settings-es-dark.png">
  <source media="(prefers-color-scheme: light)" srcset="assets/screenshot-settings-es-light.png">
  <img alt="Página de ajustes con opciones de apariencia, color de acento, densidad, perfil e idioma" src="assets/screenshot-settings-es-light.png" width="800">
</picture>

## Qué debes esperar

EasyZFS es un proyecto personal, construido para mi propio homelab y
publicado como software libre (AGPL-3.0). Es y será siempre libre. No
puedo dedicarle jornada completa: evoluciona a mi ritmo, siguiendo primero
mis propias necesidades, y puedo tardar en responder a los issues. Con
colaboraciones o apoyo quizás podría crecer más rápido, pero no puedo
prometer nada. Funciona, corre 24/7 en producción en mi casa, y cada
versión se prueba ahí primero: si no la uso yo, no se publica.

## Instalación

Requisitos: Linux (x86_64 o arm64) con systemd y ZFS, acceso root.

Instalador interactivo (descarga el binario de la última release para tu
arquitectura e instala todo):

```bash
curl -fsSL https://raw.githubusercontent.com/gnacho/easyzfs/main/deploy/install.sh | bash   # (recomendado)
```

`deploy/install.sh` lo automatiza todo: detecta la distro, instala ZFS +
smartmontools si faltan, crea la cuenta de servicio (o modo root con
`--root-mode`), escribe `/etc/easyzfs/env` y la unit systemd, y verifica el
arranque. Soporta `--binary`, `--source`, `--port`, `--yes`
(no interactivo), `--uninstall` y `DRY_RUN=1` para un ensayo sin cambios.
[Léelo antes de ejecutarlo](deploy/install.sh): es shell plano.

```bash
bash deploy/install.sh --binary ./easyzfs --yes
```

<details>
<summary><strong>Instalación manual</strong></summary>

```bash
install -m 0755 easyzfs /usr/local/bin/easyzfs
useradd -r -s /usr/sbin/nologin easyzfs || true
install -d -o easyzfs -g easyzfs /var/lib/easyzfs
install -m 0644 deploy/easyzfs.service /etc/systemd/system/easyzfs.service
install -m 0440 -o root -g root deploy/easyzfs.sudoers /etc/sudoers.d/easyzfs
visudo -cf /etc/sudoers.d/easyzfs   # valida la sintaxis

# /etc/easyzfs/env (chmod 600, root:easyzfs 640):
#   SESSION_SECRET=<cadena-aleatoria-larga>
#   ADMIN_PASSWORD=<solo primer boot; si no se define, se genera una y se loguea una vez>
#   LISTEN_ADDR=127.0.0.1:8080   # recomendado si Nginx Proxy Manager vive en el mismo host
#   DB_PATH=/var/lib/easyzfs/app.db
#   COOKIE_SECURE=1        # cuando NPM sirva SSL (cookie Secure)

systemctl daemon-reload && systemctl enable --now easyzfs
journalctl -u easyzfs -f   # primer boot: anota la contraseña bootstrap si se genera
```

</details>

### Privilegios: sudoers limitado (recomendado) o root consciente

El backend necesita ejecutar un puñado de binarios como root (`zpool`,
`zfs`, `smartctl`, `lsblk`, `crontab` (solo para leer el
crontab de root en la vista Tareas), más `udisksctl`/`hdparm` para apagar
discos libres). Editar programaciones de tareas del sistema y migrar
entradas de cron a timers de systemd pasa por un helper root confinado
(`/usr/local/libexec/easyzfs-sysd`) que solo acepta tres operaciones
validadas (`cron-set`, `timer-set`, `cron-to-timer`) sobre ficheros en
whitelist. El servicio nunca tiene escritura libre en `/etc/cron*` ni
`/etc/systemd`. `executil` decide automáticamente: si el proceso **no**
corre como root, antepone `sudo -n` a cada comando; como root los ejecuta
directamente. Anulable con `EASYZFS_SUDO=0|1` (default: auto). Dos opciones
de despliegue:

**Opción A: usuario `easyzfs` + sudoers limitado (recomendado).** El
servicio corre sin privilegios y solo puede elevar esos binarios. Es la
configuración del `easyzfs.service` incluido (por eso **no** lleva
`NoNewPrivileges=yes`: sudo necesita el bit setuid). Instala
`deploy/easyzfs.sudoers`:

```
easyzfs ALL=(root) NOPASSWD: /usr/sbin/zpool, /usr/sbin/zfs, /usr/sbin/smartctl, /usr/bin/lsblk, /usr/bin/crontab -l, /usr/sbin/hdparm -y /dev/*, /usr/bin/udisksctl power-off -b /dev/*, /usr/local/libexec/easyzfs-sysd
```

`crontab`, `hdparm` y `udisksctl` quedan fijados a los argumentos exactos
que usa el código (lectura del crontab; standby/apagado de disco), de modo
que no puedan usarse como vía de ejecución de código como root.

**Opción B: root consciente.** Cambia `User=easyzfs`/`Group=easyzfs` a
`User=root` en la unit (o define `EASYZFS_SUDO=0` con otro usuario lo
bastante privilegiado). Una elección consciente y documentada para un
appliance cuyo propósito es administrar el sistema, pero concede mucho más
que la opción A.

Si hay un proxy delante (NPM/Caddy), SSE ya envía
`X-Accel-Buffering: no`; en nginx añade también `proxy_buffering off` para
`/api/events`.

> **Nginx Proxy Manager en el mismo host**: usa
> `LISTEN_ADDR=127.0.0.1:8080` para que el backend solo sea alcanzable a
> través de NPM, y `COOKIE_SECURE=1` cuando NPM sirva SSL.

## Configuración

El servicio lee `/etc/easyzfs/env`:

| Var | Default | Descripción |
|---|---|---|
| `LISTEN_ADDR` | `:8080` | Dirección de escucha |
| `DB_PATH` | `/var/lib/easyzfs/app.db` | Ruta de la BD SQLite |
| `SESSION_SECRET` | *(efímero)* | Secreto HMAC de sesiones (defínelo en producción) |
| `ADMIN_PASSWORD` | *(generada)* | Primera contraseña de admin (bootstrap) |
| `DEMO` | - | `1` = modo demo (mock + mutaciones bloqueadas) |
| `MOCK` | - | `1` = colectores mock |
| `COOKIE_SECURE` | - | `1` = cookie Secure (tras proxy TLS) |
| `EASYZFS_SUDO` | auto | `1`/`0` fuerza o desactiva `sudo -n` en zpool/zfs/smartctl/lsblk/crontab |
| `RETENTION_DAYS` | `30` | Retención de series (purga diaria 03:30) |
| `VAPID_PUBLIC_KEY` | - | Clave pública Web Push (generada por el instalador) |
| `VAPID_PRIVATE_KEY` | - | Clave privada Web Push (solo servidor; push desactivado si falta) |
| `VAPID_SUBJECT` | `mailto:easyzfs@localhost` | Contacto VAPID (`mailto:`, requerido por Safari) |

Reinicia tras cambios: `sudo systemctl restart easyzfs`.

## Modos demo y mock

- `DEMO=1`:datos mock realistas (pools `tank`/`ssd`, 7 discos, un scrub
  progresando en vivo por SSE) y **toda mutación devuelve 403
  `demo_mode`**.
- `MOCK=1`:mismos datos mock pero las mutaciones intentan ejecutar los
  comandos reales (fallarán sin ZFS). Para desarrollo de frontend sin un
  host ZFS.

## Notificaciones push

EasyZFS puede enviar alertas **Web Push** a tu móvil/navegador **con la app
cerrada** (capacidad de pool, pools DEGRADED/FAULTED, scrubs con errores,
temperatura de disco, avisos SMART). Con la app abierta ya las recibes en
vivo por SSE. El push es solo para dispositivos con la app cerrada; las
alertas críticas siempre notifican.

- **Claves VAPID**: el instalador las genera automáticamente en la primera
  instalación (`easyzfs -generate-vapid`) y guarda `VAPID_PUBLIC_KEY`,
  `VAPID_PRIVATE_KEY` y `VAPID_SUBJECT` en `/etc/easyzfs/env`. Las
  reinstalaciones conservan las claves existentes (regenerarlas invalidaría
  todas las suscripciones). Sin claves el servidor arranca con el push
  desactivado.
- **HTTPS obligatorio**: Web Push solo funciona en contextos seguros.
  `localhost` vale tal cual; para acceso remoto pon EasyZFS tras Nginx
  Proxy Manager con SSL.
- **iOS/iPadOS**: el push requiere la PWA instalada en la pantalla de
  inicio (Compartir → "Añadir a pantalla de inicio"), y luego activar las
  alertas desde Ajustes.
- Actívalas por dispositivo en **Ajustes → Notificaciones push**.

## Formato de programación de tareas

`hourly@:15` · `daily@06:00` · `weekly:sun@03:00` · `monthly:1@02:00`
(hora local del NAS; monthly acepta días 1-28).

## Arquitectura

```
main.go                 wiring: config → db → collectors → scheduler → hub → HTTP
internal/
  config/               env → struct validado
  db/                   SQLite (modernc.org/sqlite, WAL, busy_timeout) + migraciones
  settings/             ajustes (única fila JSON) y umbrales de alerta
  users/                multiusuario, contraseñas argon2id, bootstrap de admin
  auth/                 sesiones cookie HttpOnly token|HMAC-SHA256 + middleware de roles
  collectors/           zpool / smart / sensors / schedsys / maintenance / mock (caché en memoria)
  actions/              operaciones ZFS reales (whitelists, confirm, audit_log)
  scheduler/            tareas snapshot/scrub/smart con formato de programación propio
  alerts/               umbrales (capacidad, temp, SMART, scrub) → tabla alerts + SSE
  hub/                  broker SSE (heartbeat 25s, X-Accel-Buffering: no)
  httpapi/              handlers REST (leen caché, NUNCA ejecutan CLI)
  model/                tipos del contrato API
  executil/             exec.CommandContext defensivo con timeout (sudo -n auto si no es root)
```

Los handlers HTTP **leen la caché de los colectores**; nunca ejecutan
comandos del sistema. Las operaciones largas (scrub, resilver, tests
SMART) se lanzan como acciones y su progreso se observa vía el colector
correspondiente, que publica eventos SSE.

## Contrato API

Ver [`docs/api-contract.md`](docs/api-contract.md). Resumen: auth por
cookie `easyzfs_session`; errores `{"error","message"}`; las operaciones
destructivas requieren `{"confirm":"<nombre>"}` y quedan en `audit_log`;
bajo `DEMO=1` las mutaciones devuelven 403 `demo_mode`.

## Compilación

Requisitos: Go 1.23+, Node 20+ (solo si recompilas el front).

```bash
go mod tidy        # go.sum está commiteado; tidy solo si cambian deps
make build         # = web (vite) + CGO_ENABLED=0 go build -o easyzfs .
```

Dependencias Go (mantenidas a 2 a propósito):

- `modernc.org/sqlite`:driver SQLite en Go puro: permite `CGO_ENABLED=0`
  (binario estático, sin toolchain C en el NAS).
- `golang.org/x/crypto`:`argon2.IDKey` para los hashes de contraseña (la
  stdlib no trae argon2).

## Documentos

- [EasyZFS vs TrueNAS CE vs OpenMediaVault](docs/easyzfs-vs-plataformas.md): comparativa honesta de control ZFS.
- ["Defrag" en ZFS (`zfs rewrite`) y comparativa de roadmap](docs/easyzfs-comparativa-roadmap.md).
- [Contrato API](docs/api-contract.md).

## Registro de cambios

### v2.9.18

- **Identificar un disco físico haciendo parpadear el LED de actividad de su bahía (#81)**: una acción "Identificar" ejecuta una ráfaga de I/O de solo lectura contra el disco (`dd` a /dev/null, unos 8-9 segundos), lo que hace parpadear el LED de actividad de la bahía y deja claro qué ranura ocupa el dispositivo antes de extraerlo, por ejemplo justo antes de un replace. Funciona en hardware sin LED de localización controlable por software (sin encierro SES/SGPIO), no requiere confirmación y es segura también para miembros de pool (es una lectura directa). Disponible en cada fila de discos, en el modal de detalle SMART y en el diálogo de replace, junto al disco que se va a sustituir. La lista sudoers restringida ganó una regla `dd` de un solo uso.

### v2.9.17

- **Crear un pool con la alineación elegida (ashift) (#105)**: `POST /api/pools` acepta un `ashift` opcional (9-16; 0 u omitido = automático) y pasa `-o ashift=N` a `zpool create`. El asistente de crear pool lo expone como selector (Auto, 12 para discos 4K, 13 para NVMe 8K, 9 para sectores 512B). La alineación es inmutable tras crear el pool, así que elegirla al crearlo evita que el pool herede en silencio un tamaño de sector mal detectado.
- **Crear datasets eligiendo el registro de acceso (#106)**: `POST /api/datasets` acepta un `atime` opcional (`on`/`off`/`relatime`) y pasa `-o atime=` cuando se indica. El formulario de nuevo dataset usa `relatime` por defecto (la recomendación de OpenZFS), minimizando escrituras sin perder compatibilidad.
- **Pools y vdevs creados con rutas `/dev/disk/by-id` estables (#107)**: crear pool y añadir vdev resuelve cada disco a su ruta estable `/dev/disk/by-id/<id>` cuando existe (nombre base como fallback), igual que el flujo de replace (#65), y evita pools que dependen de letras `sdX` inestables entre reinicios. El cruce disco-pool y la unión de temperaturas ahora también casan por ByID, lo que arregla además la misma laguna en los replaces por by-id.
### v2.9.16

- **La actualización desde la app detecta units de reinicio ausentes (#66)**: `GET /api/update/status` ahora devuelve `restartConfigured`, y `GET /api/update/plan` incluye la comprobación `restart_ready`. Si el host se desplegó antes de que existieran las units systemd de auto-update, la UI deshabilita el botón Actualizar y muestra un aviso claro en vez de descargar el binario nuevo en silencio y quedarse bloqueada.
### v2.9.15

- **Toasts en todas las acciones mutantes restantes (#97)**: checkpoint/export/scrub/acciones vdev de pools, crear/editar/renombrar/cifrar/rewrite de datasets, crear/editar/ejecutar/cancelar tareas, jobs de replicación, crear/borrar usuarios, cambios de contraseña, backups, claves API, tests SMART y apagado de discos, y guardados de ajustes muestran ahora un toast de éxito o de error. Los interruptores solo muestran error; el propio control es el feedback de éxito.
- **Aceptar la ruta completa en la confirmación de borrar/rollback de snapshots (#99)**: los modales de confirmación muestran la ruta completa `dataset@snapshot`, así que copiar esa ruta en el campo de confirmación activa el botón en vez de dejarlo deshabilitado. El nombre corto (tras la `@`) sigue funcionando.
- **Error claro y refresco de caché cuando un snapshot ya no existe (#100)**: si un proceso externo (por ejemplo, una poda automática de snapshots) elimina un snapshot entre la actualización de la caché de la UI y el click de borrar/rollback, ZFS devuelve el críptico mensaje en inglés "could not find any snapshots to destroy". La UI ahora muestra "El snapshot no existe; refresca la lista" y refresca la caché de snapshots inmediatamente tras cualquier mutación, incluidos los borrados fallidos, para que las entradas obsoletas desaparezcan enseguida.

### v2.9.14

- **Avisos toast al completar acciones (#94)**: las acciones que modifican datos ahora se confirman con un pequeño aviso abajo a la derecha, por encima de cualquier modal abierto. Crear/borrar snapshots y datasets y el rollback muestran un aviso de éxito, y los fallos aparecen como aviso de error aunque ya hayas cambiado de vista. Cierre automático a los pocos segundos (los errores duran más), cierre manual, anuncio a lectores de pantalla y respeto del movimiento reducido. Sin dependencias nuevas. Portado del fork comunitario coruhoorhan/easyzfs-truenas (AGPL-3.0), gracias coruhoorhan.
- **Fix: las acciones destructivas no pasaban la confirmación (#95)**: borrar un snapshot, hacer rollback y borrar una tarea desde la UI siempre fallaban contra un backend real con 400 confirm_required, porque la UI mandaba el nombre corto (o el id del job) y el backend exige la ruta completa del snapshot (o el target del job). El mock de la demo aceptaba los valores cortos y escondía el bug. La UI ahora envía el valor que el backend espera.

### v2.9.13

- **Tendencias de capacidad a largo plazo (#85)**: nueva vista Tendencias en la navegación con selector de fuente (uso de pools en %, temperatura de discos), botones de rango de 7 días a 5 años y gráfica de área SVG sin dependencias. Las muestras crudas se agregan en resúmenes diarios (media/mín/máx por fuente y día) antes de salir de la retención, así los rangos largos siguen siendo rápidos sin guardar todo el detalle.
- **Canales de alerta ntfy, Gotify y syslog (#86)**: además de Web Push, email y webhook, las alertas pueden llegar a ntfy, Gotify y un syslog local. Cada canal es opcional e inerte salvo que se configure por entorno (`NTFY_URL`, `GOTIFY_URL`, `SYSLOG_HOST`…), siguiendo el mismo patrón que SMTP.
- **API keys de solo lectura (#87)**: los administradores pueden generar claves revocables para integraciones externas (monitorización, scripts) desde Ajustes. La clave se muestra una sola vez al crearla y se guarda con hash; autentica con `Authorization: Bearer ez_…` y solo puede leer (GET/HEAD), nunca mutar. Cualquier admin puede revocar una clave en cualquier momento.
- **Landing: detección SMART profunda (#88)**: la landing y la tabla comparativa destacan ahora que EasyZFS marca los discos desde contadores de sector (reasignados, pendientes, incorregibles) y el historial de errores CRC, no solo el autodiagnóstico del propio disco.

### v2.9.12

- **Verificación en dos pasos TOTP opcional (#84)**: cualquier cuenta puede activar el 2FA desde Ajustes → Mi perfil. La activación muestra un código QR y la clave secreta para cualquier app de autenticación (Google Authenticator, Aegis, 1Password…); al confirmar con un código válido se generan 10 códigos de recuperación de un solo uso (almacenados con hash, nunca en claro). Con el 2FA activo, el login pasa a ser de dos pasos: primero la contraseña y luego un código de 6 dígitos (o un código de recuperación). Los administradores pueden resetear el 2FA de otro usuario desde la gestión de usuarios, y el límite de intentos de login también aplica al segundo factor.

### v2.9.11

- **Demo pública en inglés por defecto (#73)**: la demo alojada resuelve ahora el idioma automático a inglés antes del primer render, así los visitantes ven la interfaz en inglés sea cual sea el idioma de su navegador. La elección explícita de idioma y las instalaciones normales mantienen su comportamiento.
- **Corregida una alerta de la demo que daba mal consejo ZFS (#74)**: el panel de la demo mostraba una alerta de ejemplo que decía que un 12% de fragmentación es alto y sugería un scrub. Un 12% de fragmentación del espacio libre es sano, y los scrubs verifican checksums sin reescribir datos, así que nunca reducen la fragmentación. La alerta pasa a ser el mensaje crítico "Pool tank DEGRADED" que la app emite de verdad, coherente con el estado real del pool de la demo.
- **CTA del hero de la landing renombrado (#75)**: el botón principal pasa de "Instalar en tu NAS" a "Instalar EasyZFS" en ambos idiomas, ya que la app funciona en cualquier host Linux.
- **Icono de idioma en Ajustes (#76)**: el selector de idioma de Ajustes lleva ahora el icono "languages" de lucide; sustituye al icono genérico de monitor en el disparador móvil y aparece junto al select en escritorio.

### v2.9.10

- **Sustitución de discos segura con rutas by-id estables (#65)**: el disco destino de un replace se resuelve ahora a su ruta estable `/dev/disk/by-id/` siempre que exista, ya que las letras sdX del kernel pueden cambiar entre arranques o movimientos de bahía. El diálogo de sustitución muestra un resumen de origen y destino con modelo y número de serie de ambos discos más la ruta estable que se usará, y avisa con fuerza cuando el origen elegido es un miembro ONLINE y sano, porque sustituirlo dispara un resiliver innecesario. Las guardas del servidor (mismo disco, disco ya presente en un pool, destino más pequeño) comparan el dispositivo resuelto y detectan también formas mezcladas base/by-id.


### v2.9.9

- **Transición de deslizamiento móvil entre vistas (#60)**: al cambiar de vista con la navegación inferior en móvil, el contenido se desliza en la dirección de avance (adelante/atrás) mientras la barra inferior permanece fija, mediante la View Transitions API con respaldo a la navegación normal donde no está disponible. Respeta `prefers-reduced-motion` y el ajuste de reducir animaciones.

### v2.9.8

- **Toggle binario de tema en móvil (#58)**: en pantallas de menos de 640px el control de tema del header pasa a ser un único botón sol/luna que alterna claro/oscuro, en sustitución de los tres botones con solo icono (sistema, claro, oscuro). En pantallas anchas se mantiene el selector de tres opciones con etiquetas.

### v2.9.7

- **Orientación PWA (#56, #57)**: el manifest declara `orientation: any`, así que la app rota con libertad en tablets.

### v2.9.6

- **Título de pestaña (#53)**: el título de la pestaña y el nombre de la PWA usan un guión simple en vez de una raya larga.

### v2.9.5

- **Enlaces en "Acerca de" (#50)**: los cuatro tiles enlazan ahora a su destino (GitHub, la web del proyecto, Ko-fi y el Club Cloudless) en vez de ser cajas estáticas.

### v2.9.4

- **Pull-to-refresh en móvil (#20)**: tira hacia abajo desde el top de cualquier vista para recargar la app y coger un despliegue nuevo sin cerrarla y reabrirla.

### v2.9.3

- **Página de Ajustes reconstruida (patrón Deltos, #44)**: tarjeta de perfil como barra horizontal compacta (avatar, nombre editable, email, idioma, contraseña, notificaciones), Apariencia con los tiles de tema ocupando media tarjeta más controles de acento/animaciones/densidad, barra de administración en una fila (comprobar actualizaciones, respaldos, usuarios, modo demo) y un selector de tema de 3 estados en la barra superior (sistema/claro/oscuro).
- **Pulido de Ajustes (#49)**: Datos y umbrales ahora muestran confirmación al guardar y resaltan en acento los campos modificados hasta guardar; el widget de comprobar actualizaciones ya no duplica su texto en reposo; el interruptor de demo pasa a «Activar modo demo»; y la tarjeta Acerca de sigue el diseño de Keynest (logo, descripción, tiles de enlaces bajos, versión/licencia/runtime en una línea, botones de acción).

### v2.9.2

- **Layout del shell**: contenido principal limitado a 1400px, bloque de usuario movido a la barra superior (avatar + nombre, solo avatar en móvil), botón de tema junto a la campana, pie del sidebar simplificado.

### v2.9.1

- **Updater: progreso en vivo**: la actualización desde la app ahora muestra el progreso de descarga/instalación/reinicio en tiempo real.

### v2.9.0

- **Updater: comprobaciones de preparación, historial y rollback**: el pipeline de actualización valida la preparación antes de aplicar, guarda un historial de actualizaciones y puede volver al binario anterior si algo falla.

### v2.8.7

- **Updater: comprobación semanal automática y aviso de confirmación**: comprobación semanal automática y un aviso que confirma la versión desplegada.

### v2.8.6

- **Tests SMART programados más robustos (#35)**: el scheduler ahora maneja fallos transitorios de `smartctl -t` comprobando la salida antes de declarar error (p. ej. exit code 4 = test ya en curso, que es benigno). También se saltan los discos sin soporte SMART en trabajos con target "all" como defensa en profundidad.

### v2.8.5

- **Resumen de novedades en el aviso de actualización**: cuando hay una versión nueva, el ribbon ahora muestra un resumen de lo que cambió (~600 caracteres del cuerpo de la release de GitHub) junto al número de versión y los botones de acción. La respuesta de `GET /api/update/status` también incluye `releaseNotes` y `releaseUrl`.

### v2.8.4

- **Gráficas históricas (U2)**: las tarjetas de pool ahora muestran una mini-gráfica de ocupación de los últimos 7 y 30 días bajo la barra de capacidad, y cada disco muestra una gráfica de temperatura de 24 h. Lo respalda un nuevo endpoint `GET /api/series` que lee la tabla `series` con downsampling LTTB en el servidor, para que los rangos largos se dibujen con fluidez sin enviar miles de puntos al navegador.

### v2.8.3

- **Canal de alertas por email (S5)**: las alertas ahora pueden llegar por correo, además del Web Push y el webhook saliente. El SMTP se configura con variables de entorno (`SMTP_HOST`, `SMTP_PORT`, `SMTP_USER`, `SMTP_PASS`, `SMTP_FROM`, `SMTP_ENCRYPTION`, `SMTP_TIMEOUT`, y opcional `SMTP_TEST_TO`). Los usuarios con email configurado y el tipo de alerta habilitado reciben el mensaje en su idioma (plantillas ES/EN, texto plano + HTML). El canal queda inerte hasta que el SMTP esté configurado.

### v2.8.2

- **Ayuda contextual de topologías (U4)**: una burbuja de ayuda junto al selector de topología en Crear pool y Añadir vdev explica el esquema seleccionado en lenguaje claro (mirror, raidz1, raidz2, stripe, qué tolera y cómo queda la capacidad útil). La tarjeta de pool muestra también esa explicación para su distribución.

### v2.8.1

- **Webhook saliente endurecido (issue #18)**: las alertas ahora se entregan mediante un worker con cola acotada (cap 64) en vez de lanzar una goroutine por alerta, así una ráfaga se encola en lugar de abrir N POST paralelos. Los eventos cuyos reintentos se agotan van a una nueva tabla dead-letter `webhook_events`, el payload lleva un `event_id` (el id de la alerta) para deduplicar en el receptor, y las respuestas se limitan a 64 KiB. El secreto de firma, el timeout y los reintentos se leen una vez de env al arrancar; la URL sigue en ajustes, así los cambios aplican sin reiniciar.
- **Búsqueda nueva en el store de replicación**: ahora obtiene un job por id con una consulta directa en vez de listar y filtrar (`O(1)` en vez de `O(n)`).
- **Más tests**: 7 tests de concurrencia del hub y 11 de auth (firmas, manipulación, expiración, roles y purga), todos bajo `go test -race`.
- **Pipeline de CI**: `go vet`, `go test -race`, staticcheck, comprobación de TypeScript y el build del frontend se ejecutan en cada push y PR.

### v2.8.0

- **Fix TOCTOU en replicación**: un slot atómico (`TryAcquire`/`Release`) evita la doble ejecución de jobs de replicación. Antes, un tick concurrente o un `RunNow` manual durante la ventana del snapshot podía lanzar un segundo pipeline `zfs send | zfs recv` contra el mismo destino.
- **Cancelar mata el pipeline completo**: la cancelación de replicación ahora mata el grupo de procesos entero (`Setpgid` + `Kill(-pgid)`), no solo el líder `bash`. Los hijos del pipeline (`zfs send`, `ssh`) ya no quedan huérfanos consumiendo I/O.
- **Option injection bloqueada**: los regex de pool, dataset, snapshot, dispositivo y host SSH ahora anclan a `^[a-zA-Z0-9]`. Los nombres que empiezan por `-` o `.` se rechazan, porque se interpretaban como flags de `zpool`/`zfs`/`ssh`. No explotable como inyección de shell, pero la whitelist era incorrecta.
- **Dos nuevos tests de concurrencia** en el paquete de replicación, ejecutados bajo `go test -race`.
- Auditoría externa de seguridad, 7-Ago-2026.

### v2.7.0

- **Release de auditoría de seguridad**: cabeceras de seguridad HTTP en todas
  las respuestas (`X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`,
  CSP con `default-src 'self'`, HSTS, Referrer-Policy, Permissions-Policy).
  La CSP sigue permitiendo el check de actualizaciones pasivo contra
  `api.github.com`.
- Roadmap: nueva fase P3 (auditoría de seguridad y robustez) en
  `docs/auditoria-2026-08-05-webzfs-zfdash.md`.

### v2.6.0

- **Stack actualizado**: deps Go al día (`modernc.org/sqlite` v1.56.0, `golang.org/x/crypto` v0.54.0) y toolchain del frontend (Vite 8, TypeScript 7, Tailwind 4). Sin cambios de API ni de comportamiento.

### v2.5.0

- **Detalle SMART**: clic en la fila de un disco abre su detalle SMART completo (tabla de atributos: id, valor, peor, umbral, raw, estado; historial de self-tests; log de errores), para ATA y NVMe. Los datos salen de la caché del colector, nunca `smartctl` bajo demanda.
- **Propiedades de dataset**: tabla completa de propiedades por dataset (`zfs get all`) con acciones editar y heredar. Whitelist estricta de propiedades y valores editables en el backend (`recordsize`, `atime`, `sync`, `quota`, `mountpoint`, etc.), solo las seguras y útiles: `dedup`/`encryption` quedan fuera.
- Endpoints nuevos: `GET /api/disks/{dev}/smart`, `GET /api/disks/{dev}/smart-log`, `GET /api/datasets/{name}/properties`, `PATCH /api/datasets/{name}/properties`, `POST /api/datasets/{name}/properties/{prop}/inherit`. Ver [Contrato API](docs/api-contract.md).

## Licencia

[AGPL-3.0](LICENSE)
