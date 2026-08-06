# EasyZFS — Contrato API (front ↔ back)

Base: `/api`. Auth: cookie de sesión HttpOnly (`easyzfs_session`), login devuelve el usuario.
Todas las respuestas de error: `{"error":"código","message":"texto legible"}` con HTTP 4xx/5xx.
Acciones destructivas exigen `{"confirm":"<nombre exacto del objetivo>"}` en el body → si falta o no coincide: 400 `confirm_required`.

**Modo demo**: es una sesión mock 100% en cliente (botón "Entrar en modo demo" en el login, sin llamada al backend). Las credenciales reales autenticadas muestran SIEMPRE datos reales: no existe fallback a mock. Opcionalmente, el backend acepta `DEMO=1` para desplegar una instancia pública de demostración completa (colectores mock + mutaciones 403 `{"error":"demo_mode"}`), pero es un modo de despliegue, no de sesión.
Números: bytes en enteros (el front formatea a TiB/GiB con coma es-ES). Fechas: RFC3339 UTC.

## Auth y sesión
- `POST /api/login` `{user, password}` → `{user:"admin", role:"admin"}` + cookie. 401 si credenciales mal. 429 `rate_limited` si se supera el límite (5 intentos/min por IP+usuario; bloqueo 15 min tras 10 fallos consecutivos).
- `POST /api/logout` → 204. Invalida la sesión.
- `GET /api/me` → `{user, role}` o 401.
- `POST /api/me/password` `{current, new}` → 204. Cierra el resto de sesiones del usuario.

## Usuarios (solo admin)
- `GET /api/users` → `[{user, role:"admin"|"user", last_login, sessions}]`
- `POST /api/users` `{user, password, role}` → 201. 409 si existe.
- `DELETE /api/users/{name}` `{confirm}` → 204. No puede borrarse a sí mismo ni al último admin.
- `POST /api/users/{name}/password` `{new, close_sessions?}` → 204 (admin). `close_sessions` por defecto `true` si no se envía.

## Sistema
- `GET /api/version` → `{name:"EasyZFS", version, build, go, os_arch, uptime_sec, rss_bytes, db_bytes, db_path, zfs_version, demo, capabilities:{rewrite, raidz_expansion, scrub_all, scrub_range, zarc_names, json_output, version}}`
  - `capabilities` — feature-gating derivado de la versión de OpenZFS del host (sondeo al arranque y cada hora): `rewrite` (zfs rewrite, Linux ≥ 2.3.4), `raidz_expansion` (≥ 2.3), `scrub_all`/`scrub_range` (scrub -a y -S/-E, ≥ 2.4), `zarc_names` (zarcsummary/zarcstat, ≥ 2.4; si no, arc_summary/arcstat), `json_output` (--json, ≥ 2.3).
- `GET /api/performance` → `{arc:{size_bytes, hit_pct}|null, pools:[{name, read_bps, write_bps}]}`
  - Caché del colector `perf` (tick 60 s). ARC de `/proc/spl/kstat/zfs/arcstats` (respaldo: zarcsummary/arc_summary); `arc:null` = sin fuente en el sistema (la UI oculta la tarjeta). `read_bps`/`write_bps` = bytes/s de `zpool iostat -Hpy 1 1` (muestra de 1 s).
- `GET /api/settings` → `{lang:"auto"|"es"|"en", cap_warn_pct, cap_crit_pct, disk_temp_c, webhook, notify_scrub_errors, notify_smart_change}`
- `PUT /api/settings` (admin) mismo body → 204. 400 `invalid_input` si `cap_warn_pct`/`cap_crit_pct` fuera de 1-100, `warn >= crit` o `disk_temp_c` fuera de 20-90.
- `GET /api/alerts` → `[{id, ts, level:"info"|"warn"|"crit", source, target, message, acked}]`
  - `target` — destino navegable en la UI según la fuente de la alerta: `"pools:<pool>"` (capacidad, DEGRADED/FAULTED, scrub con errores), `"disks:<dev>"` (temperatura, SMART), `"tasks"`, `"settings"`; `""` = sin destino.
- `POST /api/alerts/{id}/ack` → 204.

## Tareas del sistema (cron + systemd timers, solo lectura)
- `GET /api/system-timers` → `{timers:[{source:"systemd"|"cron", name, schedule, next_run, last_run, command, origin}], systemd_available:bool}`
  - Lo que YA existe en el sistema (colector `schedsys`, refresco 5 min; sin systemd/cron devuelve `[]`, nunca error). `systemd_available` = systemctl en PATH + `/run/systemd/system` (la UI oculta el cambio cron→systemd si es false; `POST /api/system-timers/migrate` responde 400 `systemd_unavailable`).
  - systemd: `name` = unidad `.timer`, `command` = unidad activada, `next_run`/`last_run` tal como los devuelve `systemctl list-timers`, `schedule` = `""` (list-timers no expone OnCalendar), `origin` = `"systemctl list-timers"`.
  - cron: `schedule` = expresión cron (`"30 3 * * *"`) o `"@daily"`/`"@hourly"`/… (entradas de `/etc/cron.{hourly,daily,…}`), `command` = comando, `next_run`/`last_run` = `""`, `origin` = `"crontab"`, `"crontab (root)"`, `"/etc/crontab"`, `"/etc/cron.d/<fichero>"`, `"/etc/cron.daily"`…

## Overview (dashboard)
- `GET /api/overview` → `{pools_total, pools_online, cap_used_bytes, cap_total_bytes, snapshots_total, jobs_active, last_scrub:{pool, ts, errors}, alerts:[…últimas 3], activity:[{ts, text, detail}…últimas 10]}`

## Pools
- `GET /api/pools` → `[{name, status:"ONLINE"|"DEGRADED"|"FAULTED", topo, used_bytes, total_bytes, frag_pct, comp_ratio, autotrim, checkpoint, scrub:{state:"none"|"running"|"done", kind:"scrub"|"resilver"|"trim", pct, eta_sec, ts, errors, bytes_done?, bytes_total?}, vdevs:[{dev, role, status, temp_c}]}]`
  - `scrub` (lote B) unifica scrub/resilver/**trim**: el progreso de trim se obtiene con `zpool status -t` cada tick (un scrub/resilver en curso tiene prioridad en la representación unificada). `bytes_done`/`bytes_total` son mejor esfuerzo (`scan_stats.examined/to_examine` en `--json`; `scanned`/`issued`/`trimmed` en texto). Cadencia = tick del colector (~30 s): la barra de progreso de la UI salta en cada tick (aceptable, documentado).
  - `autotrim` — propiedad autotrim del pool (TRIM continuo en SSD). `checkpoint` — el pool tiene un checkpoint activo.
- `POST /api/pools` `{name, topo:"stripe"|"mirror"|"raidz1"|"raidz2"|"raidz3", disks:[…], confirm}` → 202 (job). `confirm` = nombre del pool.
- `POST /api/pools/import` `{name?}` → lista importables si sin name; con name importa.
- `POST /api/pools/{name}/scrub` `{action:"start"|"pause"|"stop"}` → 202
- `POST /api/pools/{name}/export` `{confirm, force, destroy}` → 202
- `POST /api/pools/{name}/vdev` `{topo, disks:[…], confirm}` → 202 (añadir vdev). `confirm` = nombre del pool.
- `POST /api/pools/{name}/replace` `{old_dev, new_dev, confirm}` → 202. `confirm` = nombre del pool.
- `POST /api/pools/{name}/autotrim` (admin) `{enabled:bool}` → 204 (`zpool set autotrim=on|off`).
- `POST /api/pools/{name}/checkpoint` (admin) `{action:"create"|"discard", confirm}` → 202. `confirm` = nombre del pool (operación delicada: el checkpoint bloquea remove/attach/detach y retiene espacio; revertir con `zpool import --rewind-to-checkpoint`).
- `GET /api/pools/{name}/history` → `[{ts, command, duration_sec?}]` más reciente primero (últimas ~100 líneas de `zpool history -i`, cacheadas por el colector zpool).
- `POST /api/pools/{name}/expand` (admin) `{vdev:"raidz2-0", disk:"sdX", confirm:"<pool>"}` → 202. **RAID-Z expansion** (lote D; `zpool attach <pool> <vdev> <disco>`, OpenZFS ≥ 2.3): añade UN disco a un vdev raidz. Errores: 400 `not_supported` (sin capability `raidz_expansion`), 400 `confirm_required`, 400 `invalid_input` (vdev no raidz del pool), 404 `not_found` (pool), 409 `dev_in_use` (disco no libre o ya en un pool). Audit `pool.expand`.
  - `pools[].raidz_vdevs` (`["raidz2-0"…]`) — vdevs raidz detectados en `zpool status`; objetivo válido del endpoint. El progreso de la expansión aparece en `pools[].scrub` con `kind:"expand"` (pct/eta_sec/bytes_done) y en el SSE `scrub.progress {kind:"expand"}`.

## Datasets
- `GET /api/datasets` → `[{name, type:"fs"|"volume", compression, used_bytes, avail_bytes, quota_bytes, mountpoint, encryption, keystatus}]`
  - `encryption` (lote D) — valor efectivo de la propiedad (`"off"` sin cifrado; `"aes-256-gcm"`… cifrado, propio o heredado). `keystatus` — `"available"` (clave cargada, desbloqueado) | `"unavailable"` (bloqueado) | `"-"` (sin cifrado).
- `POST /api/datasets` `{pool, name, type:"fs"|"volume", compression:"lz4"|"zstd"|"off", quota_bytes, volsize_bytes?, encryption?, passphrase?}` → 201. Con `encryption:true` crea con cifrado nativo AES-256-GCM (`-o encryption=aes-256-gcm -o keyformat=passphrase -o keylocation=prompt`); la passphrase (mín. 8) viaja SOLO en el body y se pasa a zfs por stdin — jamás en URL, argv, logs ni audit_log.
- `PATCH /api/datasets/{name}` `{quota_bytes?, compression?}` → 204
- **Propiedades (U3, fase P1)**: tabla completa + edición con whitelist estricta.
  - `GET /api/datasets/{name}/properties` → `{name, properties:[{name, value, source}]}` (admin y viewer).
    - `source` ∈ `local`/`default`/`inherited`/`received`/`temporary`/`-`. Lista TODAS (nativas + user props); el front agrupa por editabilidad.
    - Lectura bajo demanda con caché TTL 30 s por dataset (excepción puntual a la caché de collectors, documentada en `docs/specs-p1-v2.5.md`). 404 `not_found`.
  - `PATCH /api/datasets/{name}/properties` (admin) `{property, value}` → 204.
    - Whitelist estricta en `internal/actions/props.go` (compression, recordsize, atime, relatime, sync, checksum, copies, xattr, acltype, aclinherit, primarycache, secondarycache, logbias, canmount, mountpoint, exec, setuid, devices, readonly, snapdir, quota, reservation, volsize, volblocksize). 400 `invalid_property` / 400 `invalid_value` (nunca llega a zfs). Propiedades no aplicables al tipo (mountpoint en volume…) → 400.
  - `POST /api/datasets/{name}/properties/{prop}/inherit` (admin) → 204. Solo propiedades de la whitelist con `source == "local"`; 400 `invalid_property`, 409 `not_local`. Audit `dataset.setprop`/`dataset.inherit`.
- `DELETE /api/datasets/{name}` `{confirm, recursive}` → 202
- **Cifrado nativo** (lote D; admin; audit sin claves NUNCA):
  - `POST /api/datasets/{name}/unlock` `{key}` → 204 (`zfs load-key`, clave por stdin; monta). 400 `invalid_input` (sin clave o dataset sin cifrar), 404 `not_found`.
  - `POST /api/datasets/{name}/lock` → 204 (`zfs unload-key`, sin `-f`: si el dataset está ocupado se devuelve el error legible de zfs).
  - `POST /api/datasets/{name}/change-key` `{current_key, new_key}` → 204 (`zfs change-key -o keyformat=passphrase`; la nueva va por stdin dos veces). Con keyformat=passphrase y la clave cargada, zfs no pide la actual: `current_key` se exige como confirmación de posesión pero no se envía al CLI (limitación conocida: el backend no verifica la passphrase actual; quien la tenga mal obtendrá éxito igualmente — la nueva clave se aplica). `new_key` mín. 8.

## Snapshots
- `GET /api/snapshots?dataset=` → agrupado: `[{dataset, snaps:[{name, full, ts, used_bytes, kind:"auto"|"manual"}]}]`
- `POST /api/snapshots` `{dataset, name, recursive}` → 201
- `DELETE /api/snapshots/{full}` `{confirm}` → 204 (`full` = `tank/docs@snap`, URL-encoded)
- `POST /api/snapshots/{full}/rollback` `{confirm}` → 202

## Jobs (tareas programadas)
- `GET /api/jobs` → `[{id, tipo:"snapshot"|"scrub"|"trim"|"smart_short"|"smart_long", target, schedule, retention, enabled, last_run, last_result, next_run}]`
  - `schedule` formato propio: `hourly@:15`, `daily@06:00`, `weekly:sun@03:00`, `monthly:1@02:00`
- `POST /api/jobs` `{tipo, target, schedule, retention?}` → 201
- `PATCH /api/jobs/{id}` `{enabled?, schedule?, retention?}` → 204
- `DELETE /api/jobs/{id}` `{confirm}` → 204
- `POST /api/jobs/{id}/run` → 202
- `GET /api/jobs/history` → `[{ts, tipo, target, ok, detail}]`

## Discos
- `GET /api/disks` → `[{dev, model, serial, size_bytes, temp_c:number|null, smart:"ok"|"warn"|"crit"|"unknown", smart_detail, pool, hours}]`
  - Solo dispositivos físicos: whitelist `sd[a-z]+`, `hd[a-z]+`, `vd[a-z]+`, `xvd[a-z]+`, `nvmeNnM`, `mmcblkN`. Excluidos siempre: `zd*` (zvols), `loop*`, `ram*`, `dm-*`, `sr*`, `fd*`, `mmcblk*boot*`, `mmcblk*rpmb`.
  - `temp_c: null` = sin lectura (eMMC, USB sin SAT, smartctl no disponible); `null` no es lo mismo que `0`. El front muestra "—".
  - `smart:"unknown"` + `smart_detail:"no disponible"` cuando el disco no habla smartctl: no es un error.
- `POST /api/disks/{dev}/smart-test` `{type:"short"|"long"}` → 202
- **SMART drill-down (U1, fase P1)** — datos de la caché del colector (hasta 10 min de antigüedad; NUNCA smartctl bajo demanda).
  - `GET /api/disks/{dev}/smart` → `{dev, model, serial, smart, smart_detail, hours, attributes:[{id, name, value, worst, thresh, raw, when_failed}]}` (admin y viewer). 404 `not_found`. Discos `unknown` (sin smartctl: eMMC, USB sin SAT) → 200 con `attributes:[]`.
  - `GET /api/disks/{dev}/smart-log` → `{dev, selftests:[{type, status, lifetime_hours, percent}], error_log:{count, entries:[{error_type, detail}]}}`. Listas vacías si el disco no expone logs.
  - El detalle se parsea en la pasada de 10 min del colector del mismo `smartctl -j -a`; protocolo ATA/NVMe detectado automáticamente.

## Notificaciones push (Web Push)
Los endpoints de suscripción devuelven 503 `push_not_configured` si el servidor no tiene claves VAPID. El texto de las notificaciones se compone server-side (ES/EN) según el `lang` guardado en la suscripción.
- `GET /api/push/vapid-public-key` → `{publicKey}` para `pushManager.subscribe()`.
- `POST /api/push/subscribe` `{endpoint, keys:{p256dh, auth}, lang?, origin?}` → 204. Upsert por `endpoint` (re-suscripciones y rotaciones actualizan la fila y reasignan `user_id`).
  - `lang` — `"es"|"en"`; ausente/desconocido no pisa el guardado.
  - `origin` — `window.location.origin` del navegador (p. ej. `https://zfs.example.com`). El servidor compone con él `url` y `notification.navigate` ABSOLUTAS en el payload (Declarative Web Push las exige); vacío = fallback a relativa. El upsert lo actualiza.
  - 400 `invalid_endpoint` (no https://), 400 `invalid_keys` (p256dh debe ser base64url de 65 bytes y auth base64url de 16 bytes), 400 `invalid_origin` (no http(s)://).
- `DELETE /api/push/unsubscribe` `{endpoint}` → 204. Solo borra suscripciones del propio usuario.
- `GET /api/push/preferences` → `{preferences:[{tipo, enabled}]}`. Los 5 tipos del catálogo (`pool_capacity`, `pool_status`, `scrub_errors`, `disk_temp`, `smart_status`), siempre presentes; `enabled` por defecto `true` si no hay fila guardada.
- `PUT /api/push/preferences` `{tipo, enabled}` → 204. Upsert por `(usuario, tipo)`. 400 `invalid_tipo` si el tipo es desconocido.
- `GET /api/push/quiet-hours` → `{enabled, start, end, tz}`. `start`/`end` son `null` cuando `enabled:false`. `tz` fija de momento: `Europe/Madrid`.
- `PUT /api/push/quiet-hours` `{enabled, start, end}` → 204. `start`/`end`: hora local 0-23; la ventana puede cruzar medianoche (22 → 8). 400 `invalid_hours` si fuera de 0-23 o `start == end`.
  - Efecto en el envío: las críticas atraviesan el horario silencioso SIEMPRE; warn/info dentro de la ventana se encolan (`notification_queue`) y se entregan al terminar (ticker de 60 s).

## SSE (tiempo real)
- `GET /api/events` — stream `text/event-stream`, heartbeat `:ping` cada 25 s, cabecera `X-Accel-Buffering: no`.
  Eventos (`event:` / `data:` JSON):
  - `pool.status` `{name, status}` · `scrub.progress` `{pool, pct, eta_sec}`
  - `disk.temp` `{dev, temp_c}` · `alert.new` `{alert}` · `job.finished` `{id, ok, detail}`
  - `longop.update` `{op}` (lote B: ciclo de vida de operaciones largas)
  - `replication.finished` `{id, ok, detail}` (lote C: fin de una replicación)
  - `overview` (cambios agregados para refrescar KPIs)

## Operaciones largas (lote B; runner `internal/longops`)
Procesos desacoplados monitorizados (hoy `zfs rewrite`; el runner es genérico
para reusarlo en la replicación del lote C: `Start(tipo, target, cmd, args…)`
con contexto propio, NO el de la request). El registro es **solo memoria**
(decisión deliberada): si el daemon reinicia, las ops en curso quedan
huérfanas (el proceso sigue en el SO pero EasyZFS ya no las ve). Las
terminadas se conservan 1 h (TTL). Cada cambio de estado publica
`longop.update` `{op}` por SSE.

`LongOp = { id, type ("rewrite"|"replication"), target, pid, started, ended?, status ("running"|"done"|"error"|"canceled"), error?, lines[] }` — `lines` es un anillo con las últimas 50 líneas de salida combinada.

- `GET /api/longops` → `[LongOp]` (más reciente primero).
- `POST /api/longops/{id}/cancel` (admin) → 204 · 404 `not_found` · 409 `not_running`.
- `POST /api/datasets/{name}/rewrite` (admin) `{confirm:"<dataset>"}` → 202 `{op_id}`. Errores: 400 `not_supported` (sin capability `rewrite`, OpenZFS ≥ 2.3.4), 400 `confirm_required`, 400 `invalid_input` (dataset inexistente, no filesystem o no montado), 409 `already_running`. Lanza `zfs rewrite -r -x <mountpoint>` y registra `dataset.rewrite` en audit_log.

## Replicación ZFS send/recv (lote C; `internal/replication`)
Copia incremental de datasets, local o vía SSH. Tabla propia
`replication_jobs` (migración v12). Modelo: cada ejecución crea un snapshot
`ezrepl-YYYYMMDD-HHMMSS` en el origen; la primera vez envía completo y las
siguientes `zfs send -i <origen>#ezrepl-last`; tras el éxito el bookmark
`ezrepl-last` pasa al snapshot nuevo y se podan los snapshots `ezrepl-*`
(se conservan los 2 últimos). Si el incremental falla (divergencia): con
`force_full` se destruye el destino y se reintenta completo UNA vez; sin él,
`last_error` lo explica y no se toca el destino. La ejecución va por el runner
longops (`type:"replication"`, pipeline `bash -c 'set -o pipefail; zfs send -v
… | [ssh …] zfs recv -s …'`) y el resultado queda en el job (`last_run`,
`last_ok`, `last_error`, `last_bookmark`) y en `job_history` (tipo
`replication`). Planificación con el mismo formato `schedule` y tick de 30 s
que los jobs. **Todo** lo interpolado en el pipeline pasa whitelists estrictas
(dataset `[a-zA-Z0-9_.\-/]+`, usuario `[a-z_][a-z0-9_-]*`, host
`[a-zA-Z0-9.\-]+`, puerto 1-65535).

`ReplicationJob = { id, source, dest_type ("local"|"ssh"), dest_dataset, host, user, port, raw, force_full, schedule, enabled, last_bookmark, last_run, last_ok, last_error, next_run? }`

- `GET /api/replication` → `[ReplicationJob]` (con `next_run` calculado).
- `POST /api/replication` (admin) `{source, dest_type, dest_dataset, host?, user?, port?, raw?, force_full?, schedule}` → 201 `{id}` · 400 `invalid_input` / `invalid_schedule`.
- `PATCH /api/replication/{id}` (admin) `{enabled?, schedule?, force_full?, raw?}` → 204 · 404 `not_found`.
- `DELETE /api/replication/{id}` (admin) `{confirm:"<source>"}` → 204 (solo la definición: no toca snapshots, bookmark ni destino).
- `POST /api/replication/{id}/run` (admin) → 202 · 404 `not_found` · 409 `already_running` (una ejecución por job).
- `GET /api/replication/sshkey` → `{public_key, instructions}`: clave pública ed25519 del daemon (se genera al primer uso en `<datadir>/ssh/id_ed25519`, 0600; la privada jamás sale del servidor). Las conexiones usan `BatchMode=yes`, `StrictHostKeyChecking=accept-new` y `UserKnownHostsFile=<datadir>/ssh/known_hosts`.
- `POST /api/replication/test` (admin) `{host, user, port}` → 200 `{ok:true, remote_version}` (salida de `zfs version` remoto) o 200 `{ok:false, error}` con mensaje legible clasificado (autenticación / red / zfs ausente).

Evento SSE nuevo: `replication.finished` `{id, ok, detail}` (además de
`job.finished` para refrescar el historial). En MOCK=1: los jobs locales
completan con progreso simulado y los SSH fallan con error de autenticación
legible.

## Alertas en tiempo real (eventos ZFS; lote B)
Colector `events`: proceso persistente `zpool events -f` (sin timeout;
reconexión con backoff 5 s→2 min; si no está disponible —permisos, zfs
ausente— se desactiva solo tras log y el polling queda como red de seguridad).
Mapea bloques a alertas con `source = "zed.<class>"` (distinto del polling,
para que el dedupe source+message no colisione):

- `ereport.fs.zfs.io|checksum|data` → **crit**, target `disks:<dev>` (o `pools:<pool>`)
- `ereport.fs.zfs.deadman|delay` → **warn**
- `sysevent.fs.zfs.resilver_start|resilver_finish` → **info**
- `sysevent.fs.zfs.scrub_finish` con errores > 0 → **warn**
- `sysevent.fs.zfs.vdev_statechange` a FAULTED/DEGRADED → **crit**
- `config_sync`, `trim_start/finish`, `scrub_start` → sin alerta (solo log debug)

Kinds nuevos en el catálogo push (ES/EN): `zfs_io_error`, `zfs_checksum_error`,
`zfs_data_error`, `zfs_deadman`, `zfs_io_delay`, `vdev_state`,
`resilver_start`, `resilver_finish`.
