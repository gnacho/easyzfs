# EasyZFS

<p align="center">
  <a href="README.md">English</a> |
  <a href="README.es.md">Español</a>
</p>

<p align="center">
  <a href="https://easyzfs.cloudless.club"><img alt="Website" src="https://img.shields.io/badge/Website-easyzfs.cloudless.club-blue"></a>
  <a href="https://demo.easyzfs.cloudless.club"><img alt="Live demo" src="https://img.shields.io/badge/Live%20demo-demo.easyzfs.cloudless.club-blue"></a>
  <a href="https://github.com/gnacho/easyzfs/releases"><img alt="Release" src="https://img.shields.io/github/v/release/gnacho/easyzfs"></a>
  <a href="https://github.com/gnacho/easyzfs/actions/workflows/release.yml"><img alt="CI" src="https://img.shields.io/github/actions/workflow/status/gnacho/easyzfs/release.yml?branch=main"></a>
  <a href="LICENSE"><img alt="License" src="https://img.shields.io/github/license/gnacho/easyzfs"></a>
  <a href="https://ko-fi.com/gnacho"><img alt="Support on Ko-fi" src="https://img.shields.io/badge/Ko--fi-Donate-ff5e5b?logo=ko-fi&logoColor=white"></a>
</p>

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/hero-en-dark.png">
    <source media="(prefers-color-scheme: light)" srcset="assets/hero-en-light.png">
    <img alt="EasyZFS dashboard showing two pools (raidz1 tank degraded with a rebuild offer, ssd mirror rebuilding at 53%), capacity bars, disk states and scrub status" src="assets/hero-en-light.png" width="800">
  </picture>
</p>

EasyZFS is a ZFS management app that lives **on the NAS itself**: a single
static Go binary that wraps the system commands (`zpool`, `zfs`, `smartctl`,
hwmon sensors), exposes a REST + SSE API and serves an embedded PWA. It runs
24/7 with a minimal footprint and deploys on a Debian LXC + systemd. No
Docker, no appliance OS, no heavy stack.

## Why does this exist?

After years with commercial NAS boxes (first Synology, then QNAP), I ended
up with captive ecosystems running on hardware that aged fast and couldn't
be upgraded. So I moved to an HP mini server running Proxmox, unifying NAS
and homelab in one box. Proxmox has no friendly RAID management UI, so I
tried a TrueNAS VM. Spoiler: it was a mistake. Everything worked, but every
reboot meant containers waiting on a slow TrueNAS boot, it ate RAM, needed
its own maintenance, and orchestrating it with the rest of the stack became
a source of stress. The obvious fix won: drop TrueNAS and import the pools
straight into Proxmox. Managing ZFS by hand taught me the commands, but I
kept missing a small, friendly, minimal web UI for the day to day. I
couldn't find one I liked, so I built it.

## EasyZFS vs TrueNAS CE vs OpenMediaVault

Framing matters: TrueNAS and OMV are complete NAS systems (shares, apps,
VMs). EasyZFS is not trying to be one. It answers a narrower question: what
do you use to manage your ZFS pools? In that category, this is the picture
(full analysis in [docs/easyzfs-vs-plataformas.md](docs/easyzfs-vs-plataformas.md)):

| | EasyZFS | TrueNAS CE | OMV 8 |
|---|:---:|:---:|:---:|
| RAM needed | **tens of MB** | 8 GB (16 recommended) | ~1 GB |
| Runs on | any Linux, VM, 256 MB LXC | its own appliance only | dedicated Debian |
| ZFS is | the whole product | the product | a third-party plugin |
| `zfs rewrite` (real defrag) with UI | ✅ | CLI only | ❌ |
| Pool checkpoint with UI | ✅ | CLI only | ❌ |
| RAID-Z expansion with live progress | ✅ | ✅ | ❌ |
| Disk replacement recommendations (which disk, why, safety holds) | ✅ | ❌ | ❌ |
| Push alerts to your phone, zero third parties | ✅ (Web Push) | email/webhooks | email |
| Live zed events + long-op progress (SSE) | ✅ | partial | ❌ |
| SMB/NFS shares, apps, VMs | ❌ (by design) | ✅ | ✅ |

Three points close the case:

1. **It runs where the others cannot.** A 256 MB LXC on any distro is a
   full EasyZFS server. TrueNAS needs 8 GB and the whole machine; OMV needs
   a dedicated Debian.
2. **Alerts that reach your pocket.** Real Web Push to your phone with the
   app closed: quiet hours, severities, ES/EN, no third-party service in
   between. A "degraded disk" notification in 5 seconds beats a historical
   report.
3. **No pivot risk.** EasyZFS does one thing and will keep doing it.
   TrueNAS changed course three times in three years and retired CORE; OMV
   keeps ZFS alive through an extras plugin.

The honest reverse side: no SMB/NFS/iSCSI shares, no apps or VMs, and
TrueNAS wins on granular RBAC, enterprise auditing, replication maturity
and community. If you need a full NAS, use TrueNAS. If you need to control
ZFS on any Linux with 50 MB of RAM and one binary, EasyZFS is the only
tool in its category.

## Why this stack?

- **Go, single static binary**: a 24/7 daemon on a small LXC: ~12 MB,
  minimal RSS, no runtime to maintain, an upgrade is swapping one file.
- **`modernc.org/sqlite` (pure Go)**: keeps `CGO_ENABLED=0` so the binary
  is fully static and needs no C toolchain on the NAS. Only 2 Go
  dependencies, on purpose.
- **Collectors + cache, never CLI from HTTP**: collectors poll
  `zpool`/`smartctl`/sensors into an in-memory cache and publish SSE;
  handlers only read the cache. The UI stays instant no matter how slow
  the disks are.
- **systemd + limited sudoers, no Docker**: the service runs unprivileged
  and elevates only the whitelisted binaries (`zpool`, `zfs`, `smartctl`…).
  Docker would add a layer without adding anything here.
- **React + Vite embedded PWA**: the UI shell (theme, i18n, settings) is
  shared with my other apps, so fixes land everywhere at once.

## Features

- Pools, datasets, snapshots, disks (SMART), temperatures, live over SSE
- Scheduled jobs: snapshots, scrubs, SMART tests (`easyzfs-auto-*` with
  per-job retention)
- System tasks view: systemd timers + cron (read-only)
- Physical-disk inventory filter: zvols, loop, eMMC boot partitions and
  other pseudo-devices are hidden; eMMC/USB without SAT report SMART as
  "unknown"
- Multi-user auth (argon2id, roles admin/viewer), HMAC-SHA256 session
  cookies
- Audit log for every mutating action; destructive ops require
  `{"confirm":"<name>"}`
- Web Push alerts (pool capacity, DEGRADED/FAULTED, scrub errors, disk
  temperature, SMART warnings); see [Push notifications](#push-notifications)
- Demo mode (`DEMO=1`): realistic mock data, all mutations return 403, so
  it's safe to show off
- PWA: installable, dark/light theme, i18n es/en/auto
- Embedded SQLite (WAL) for users, sessions, settings, alerts, series and
  job history

## Screenshots

**Pools:** topology, health, rebuild offer when a free disk is detected**

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/screenshot-pools-en-dark.png">
  <source media="(prefers-color-scheme: light)" srcset="assets/screenshot-pools-en-light.png">
  <img alt="Pools view showing the raidz1 tank pool degraded with a free-disk rebuild banner, member disks with temperatures, and the ssd mirror rebuilding" src="assets/screenshot-pools-en-light.png" width="800">
</picture>

**Disks:** SMART health, temperatures and power-on hours per physical disk**

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/screenshot-disks-en-dark.png">
  <source media="(prefers-color-scheme: light)" srcset="assets/screenshot-disks-en-light.png">
  <img alt="Disks table listing model, serial, size, temperature, SMART health and pool for each physical disk, with one disk warning about reallocated sectors" src="assets/screenshot-disks-en-light.png" width="800">
</picture>

**Settings:** theme, accent, users, push notifications and backups**

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/screenshot-settings-en-dark.png">
  <source media="(prefers-color-scheme: light)" srcset="assets/screenshot-settings-en-light.png">
  <img alt="Settings page with appearance options, accent color, density, profile and language cards" src="assets/screenshot-settings-en-light.png" width="800">
</picture>

## What to expect

EasyZFS is a personal project, built for my own homelab and released as
free software (AGPL-3.0). It is and will always be free. I can't dedicate
full time to it: it evolves at my pace, following my own needs first, and I
may be slow to answer issues. With contributions or support it might grow
faster, but I can't promise anything. It works, it runs 24/7 in production
at my home, and every release is tested there first: if I'm not using it
myself, it doesn't ship.

## Installation

Requirements: Linux (x86_64 or arm64) with systemd and ZFS, root access.

Interactive installer (downloads the latest release binary for your arch
and installs everything):

```bash
curl -fsSL https://raw.githubusercontent.com/gnacho/easyzfs/main/deploy/install.sh | bash   # (recommended)
```

`deploy/install.sh` automates everything: detects the distro, installs ZFS
+ smartmontools if missing, creates the service account (or root mode with
`--root-mode`), writes `/etc/easyzfs/env` and the systemd unit, and verifies
startup. Supports `--binary`, `--source`, `--port`, `--yes`
(non-interactive), `--uninstall` and `DRY_RUN=1` for a no-changes
rehearsal. [Read it before running](deploy/install.sh): it's plain shell.

```bash
bash deploy/install.sh --binary ./easyzfs --yes
```

<details>
<summary><strong>Manual installation</strong></summary>

```bash
install -m 0755 easyzfs /usr/local/bin/easyzfs
useradd -r -s /usr/sbin/nologin easyzfs || true
install -d -o easyzfs -g easyzfs /var/lib/easyzfs
install -m 0644 deploy/easyzfs.service /etc/systemd/system/easyzfs.service
install -m 0440 -o root -g root deploy/easyzfs.sudoers /etc/sudoers.d/easyzfs
visudo -cf /etc/sudoers.d/easyzfs   # validate syntax

# /etc/easyzfs/env (chmod 600, root:easyzfs 640):
#   SESSION_SECRET=<long-random-string>
#   ADMIN_PASSWORD=<first boot only; if unset, one is generated and logged once>
#   LISTEN_ADDR=127.0.0.1:8080   # recommended if Nginx Proxy Manager lives on the same host
#   DB_PATH=/var/lib/easyzfs/app.db
#   COOKIE_SECURE=1        # once NPM serves SSL (Secure cookie)

systemctl daemon-reload && systemctl enable --now easyzfs
journalctl -u easyzfs -f   # first boot: note the bootstrap password if generated
```

</details>

### Privileges: limited sudoers (recommended) or conscious root

The backend needs to run a handful of binaries as root (`zpool`, `zfs`,
`smartctl`, `lsblk`, `crontab` (only to read root's crontab
for the Tasks view), plus `udisksctl`/`hdparm` to power down free disks).
Editing system task schedules and migrating cron entries to systemd timers
goes through a confined root helper (`/usr/local/libexec/easyzfs-sysd`)
that only accepts three validated operations (`cron-set`, `timer-set`,
`cron-to-timer`) on whitelisted files. The service never gets free write
access to `/etc/cron*` or `/etc/systemd`. `executil` decides automatically:
if the process does **not** run as root, it prepends `sudo -n` to every
command; as root it runs them directly. Override with `EASYZFS_SUDO=0|1`
(default: auto). Two deployment options:

**Option A: `easyzfs` user + limited sudoers (recommended).** The service
runs unprivileged and can only elevate those binaries. This is the bundled
`easyzfs.service` setup (which is why it does **not** set
`NoNewPrivileges=yes`: sudo needs the setuid bit). Install
`deploy/easyzfs.sudoers`:

```
easyzfs ALL=(root) NOPASSWD: /usr/sbin/zpool, /usr/sbin/zfs, /usr/sbin/smartctl, /usr/bin/lsblk, /usr/bin/crontab -l, /usr/sbin/hdparm -y /dev/*, /usr/bin/udisksctl power-off -b /dev/*, /usr/local/libexec/easyzfs-sysd
```

`crontab`, `hdparm` and `udisksctl` are pinned to the exact arguments the
code uses (read-only crontab listing; disk standby/power-off), so they
cannot be abused as a root code-execution path.

**Option B: conscious root.** Change `User=easyzfs`/`Group=easyzfs` to
`User=root` in the unit (or set `EASYZFS_SUDO=0` with another sufficiently
privileged user). A conscious, documented choice for an appliance whose
purpose is administering the system, but it grants far more than option A.

If a proxy sits in front (NPM/Caddy), SSE already sends
`X-Accel-Buffering: no`; in nginx also add `proxy_buffering off` for
`/api/events`.

> **Nginx Proxy Manager on the same host**: use
> `LISTEN_ADDR=127.0.0.1:8080` so the backend is only reachable through
> NPM, and `COOKIE_SECURE=1` once NPM serves SSL.

## Configuration

The service reads `/etc/easyzfs/env`:

| Var | Default | Description |
|---|---|---|
| `LISTEN_ADDR` | `:8080` | Listen address |
| `DB_PATH` | `/var/lib/easyzfs/app.db` | SQLite DB path |
| `SESSION_SECRET` | *(ephemeral)* | HMAC secret for sessions (set it in production) |
| `ADMIN_PASSWORD` | *(generated)* | First admin password (bootstrap) |
| `DEMO` | - | `1` = demo mode (mock + mutations blocked) |
| `MOCK` | - | `1` = mock collectors |
| `COOKIE_SECURE` | - | `1` = Secure cookie (behind TLS proxy) |
| `EASYZFS_SUDO` | auto | `1`/`0` forces or disables `sudo -n` on zpool/zfs/smartctl/lsblk/crontab |
| `RETENTION_DAYS` | `30` | Series retention (daily purge 03:30) |
| `VAPID_PUBLIC_KEY` | - | Web Push public key (installer-generated) |
| `VAPID_PRIVATE_KEY` | - | Web Push private key (server only; push disabled if missing) |
| `VAPID_SUBJECT` | `mailto:easyzfs@localhost` | VAPID contact (`mailto:`, required by Safari) |

Restart after changes: `sudo systemctl restart easyzfs`.

## Demo and mock modes

- `DEMO=1`:realistic mock data (pools `tank`/`ssd`, 7 disks, a
  live-progressing scrub over SSE) and **all mutations return 403
  `demo_mode`**.
- `MOCK=1`:same mock data but mutations try to run the real commands
  (they will fail without ZFS). For frontend development without a ZFS
  host.

## Push notifications

EasyZFS can send **Web Push** alerts to your phone/browser **with the app
closed** (pool capacity, DEGRADED/FAULTED pools, scrubs with errors, disk
temperature, SMART warnings). With the app open you already get them live
over SSE. Push is only for closed-app devices; critical alerts
always notify.

- **VAPID keys**: the installer generates them automatically on first
  install (`easyzfs -generate-vapid`) and stores `VAPID_PUBLIC_KEY`,
  `VAPID_PRIVATE_KEY` and `VAPID_SUBJECT` in `/etc/easyzfs/env`. Reinstalls
  keep the existing keys (regenerating them would invalidate every
  subscription). Without keys the server simply starts with push disabled.
- **HTTPS required**: Web Push only works on secure contexts. `localhost`
  works as-is; for remote access put EasyZFS behind Nginx Proxy Manager
  with SSL.
- **iOS/iPadOS**: push requires the PWA installed on the Home Screen
  (Share → "Add to Home Screen"), then enable alerts from Settings.
- Enable them per device in **Settings → Push notifications**.

## Job schedule format

`hourly@:15` · `daily@06:00` · `weekly:sun@03:00` · `monthly:1@02:00`
(NAS local time; monthly accepts days 1-28).

## Architecture

```
main.go                 wiring: config → db → collectors → scheduler → hub → HTTP
internal/
  config/               env → validated struct
  db/                   SQLite (modernc.org/sqlite, WAL, busy_timeout) + migrations
  settings/             settings (single JSON row) and alert thresholds
  users/                multi-user, argon2id passwords, admin bootstrap
  auth/                 HttpOnly cookie sessions token|HMAC-SHA256 + role middleware
  collectors/           zpool / smart / sensors / schedsys / maintenance / mock (in-memory cache)
  actions/              real ZFS operations (whitelists, confirm, audit_log)
  scheduler/            snapshot/scrub/smart jobs with custom schedule format
  alerts/               thresholds (capacity, temp, SMART, scrub) → alerts table + SSE
  hub/                  SSE broker (25s heartbeat, X-Accel-Buffering: no)
  httpapi/              REST handlers (read cache, NEVER run CLI)
  model/                API contract types
  executil/             defensive exec.CommandContext with timeout (auto sudo -n if not root)
```

HTTP handlers **read the collectors' cache**; they never run system
commands. Long operations (scrub, resilver, SMART tests) are launched as
actions and their progress is observed via the corresponding collector,
which publishes SSE events.

## API contract

See [`docs/api-contract.md`](docs/api-contract.md). Summary:
`easyzfs_session` cookie auth; errors `{"error","message"}`; destructive
ops need `{"confirm":"<name>"}` and are recorded in `audit_log`; under
`DEMO=1` mutations return 403 `demo_mode`.

## Build

Requirements: Go 1.23+, Node 20+ (only if rebuilding the front).

```bash
go mod tidy        # go.sum is committed; tidy only if deps change
make build         # = web (vite) + CGO_ENABLED=0 go build -o easyzfs .
```

Go dependencies (kept to 2 on purpose):

- `modernc.org/sqlite`:pure-Go SQLite driver: enables `CGO_ENABLED=0`
  (static binary, no C toolchain needed on the NAS).
- `golang.org/x/crypto`:`argon2.IDKey` for password hashes (stdlib has
  no argon2).

## Docs

- [EasyZFS vs TrueNAS CE vs OpenMediaVault](docs/easyzfs-vs-plataformas.md): an honest comparison of ZFS control features.
- [ZFS defrag (`zfs rewrite`) and roadmap comparison](docs/easyzfs-comparativa-roadmap.md).
- [API contract](docs/api-contract.md).

## Changelog

### v2.9.5

- **About card links (#50)**: the four tiles now link to their destination (GitHub, the project website, Ko-fi and the Cloudless Club) instead of being static boxes.

### v2.9.4

- **Pull-to-refresh on mobile (#20)**: pull down from the top of any view to reload the app and pick up a newly deployed build without closing and reopening it.

### v2.9.3

- **Settings page rebuilt (Deltos pattern, #44)**: profile card as a compact horizontal bar (avatar, editable name, email, language, password, notifications), Appearance with theme tiles filling half the card plus accent/animation/density controls, a single-row admin bar (update check, backups, users, demo toggle), and a 3-state theme pill in the topbar (system/light/dark).
- **Settings polish (#49)**: Data & thresholds now show save feedback and highlight modified fields in accent until saved; the update-check widget no longer duplicates its label while idle; the demo toggle reads "Activar modo demo"; and the About card follows the Keynest layout (logo, description, low link tiles, one-line version/license/runtime, action buttons).

### v2.9.2

- **Shell layout**: main content constrained to 1400px, user block moved to the topbar (avatar + name, avatar only on mobile), theme button next to the bell, simplified sidebar footer.

### v2.9.1

- **Updater: live progress**: the in-app update now shows download/install/restart progress in real time.

### v2.9.0

- **Updater: readiness checks, update history and rollback**: the update pipeline validates readiness before applying, keeps an update history, and can roll back to the previous binary on failure.

### v2.8.7

- **Updater: weekly auto-check and confirmation toast**: automatic weekly update check and a toast confirming the deployed version.

### v2.8.6

- **Robust SMART test scheduling (#35)**: the scheduler now handles transient `smartctl -t` failures gracefully by checking the command output before declaring an error (e.g. exit code 4 = test already running, which is benign). Disks without SMART support are also skipped in "all" target jobs as defense in depth.

### v2.8.5

- **Release notes in the update ribbon**: when a new version is available, the ribbon now shows a summary of what changed (~600 chars from the GitHub release body) alongside the version number and action buttons. The backend `GET /api/update/status` response also includes `releaseNotes` and `releaseUrl`.

### v2.8.4

- **Historical sparklines (U2)**: pool cards now show a small capacity chart for the last 7 and 30 days under the meter, and each disk row shows a 24h temperature chart. Backed by a new `GET /api/series` endpoint that reads the `series` table with server-side LTTB downsampling, so long ranges render smoothly without shipping thousands of points to the browser.

### v2.8.3

- **Email alert channel (S5)**: alerts can now be delivered by email alongside Web Push and the outgoing webhook. SMTP is configured via environment variables (`SMTP_HOST`, `SMTP_PORT`, `SMTP_USER`, `SMTP_PASS`, `SMTP_FROM`, `SMTP_ENCRYPTION`, `SMTP_TIMEOUT`, optional `SMTP_TEST_TO`). Users with an email set and the alert type enabled receive a message in their language (ES/EN templates, plain text + HTML). The channel stays inert until SMTP is configured.

### v2.8.2

- **Contextual topology help (U4)**: a help bubble next to the topology selector in Create pool and Add vdev explains the selected layout in plain language (mirror, raidz1, raidz2, stripe, what it tolerates and how usable capacity works). The pool card now also shows the same explanation for its layout.

### v2.8.1

- **Outgoing webhook hardened (issue #18)**: alerts are now delivered through a dedicated worker with a bounded queue (cap 64) instead of spawning a goroutine per alert, so a burst queues instead of fanning out N parallel POSTs. Events whose retries are exhausted land in a new `webhook_events` dead-letter table, the payload carries an `event_id` (the alert id) for receiver-side dedup, and response bodies are capped at 64 KiB. The signing secret, timeout and retry count are read once from env at startup, while the URL stays in settings so changes apply without restart.
- **New replication store lookup**: the replication store now fetches a job by id with a direct query instead of listing and filtering (`O(1)` vs `O(n)`).
- **More tests**: 7 concurrency tests for the hub and 11 tests for auth signatures, tampering, expiry, roles and purge, all run under `go test -race`.
- **CI pipeline**: `go vet`, `go test -race`, staticcheck, TypeScript check and the frontend build now run on every push and PR.

### v2.8.0

- **Replication TOCTOU fix**: an atomic job slot (`TryAcquire`/`Release`) prevents double execution of replication jobs. Before, a concurrent tick or manual `RunNow` during the snapshot window could launch a second `zfs send | zfs recv` pipeline against the same destination.
- **Cancel kills the whole pipeline**: replication cancel now kills the entire process group (`Setpgid` + `Kill(-pgid)`), not just the `bash` leader. Children of the pipeline (`zfs send`, `ssh`) are no longer left behind consuming I/O.
- **Option injection blocked**: pool, dataset, snapshot, device and SSH host regexes now anchor to `^[a-zA-Z0-9]`. Names starting with `-` or `.` are rejected — they were interpreted as flags of `zpool`/`zfs`/`ssh`. Not exploitable as shell injection, but the whitelist was incorrect.
- **Two new concurrency tests** in the replication package run under `go test -race`.
- External security audit, 7-Aug-2026.

### v2.7.0

- **Security audit release**: HTTP security headers on every response
  (`X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, CSP with
  `default-src 'self'`, HSTS, Referrer-Policy, Permissions-Policy). The CSP
  still allows the passive update check against `api.github.com`.
- Roadmap: new phase P3 (security & robustness audit) in
  `docs/auditoria-2026-08-05-webzfs-zfdash.md`.

### v2.6.0

- **Stack updated**: Go deps to date (`modernc.org/sqlite` v1.56.0, `golang.org/x/crypto` v0.54.0) and frontend toolchain (Vite 8, TypeScript 7, Tailwind 4). No API or behavior changes.

### v2.5.0

- **SMART drill-down**: click a disk row to open its full SMART detail (complete attribute table: id, value, worst, threshold, raw, status; self-test history; error log), for ATA and NVMe. Data comes from the collector cache, never `smartctl` on request.
- **Dataset properties**: full property table per dataset (`zfs get all`) with edit and inherit actions. Strict backend whitelist of editable properties and values (`recordsize`, `atime`, `sync`, `quota`, `mountpoint`, etc.), only safe and useful ones: `dedup`/`encryption` stay out.
- New endpoints: `GET /api/disks/{dev}/smart`, `GET /api/disks/{dev}/smart-log`, `GET /api/datasets/{name}/properties`, `PATCH /api/datasets/{name}/properties`, `POST /api/datasets/{name}/properties/{prop}/inherit`. See [API contract](docs/api-contract.md).

## License

[AGPL-3.0](LICENSE)
