#!/usr/bin/env bash
# =============================================================================
# install.sh — Instalador interactivo de EasyZFS (estilo ProxMenux)
#
# Despliega el binario Go de gestión ZFS en cualquier servidor Linux con
# systemd: detecta la distro, instala ZFS + smartmontools, crea la cuenta de
# servicio (o modo root), escribe /etc/easyzfs/env y la unit de systemd.
#
# Uso:
#   bash install.sh [opciones]
#   curl -fsSL https://raw.githubusercontent.com/gnacho/easyzfs/main/deploy/install.sh | bash
#   DRY_RUN=1 bash install.sh --binary ./easyzfs --yes   # ensayo sin cambios
#
# Opciones:
#   --binary <ruta>   Binario local (defecto: ./easyzfs si existe)
#   --url <url>       URL de release; acepta {arch} (x86_64|aarch64).
#                     También vía EASYZFS_RELEASE_URL. Defecto: releases
#                     oficiales de github.com/gnacho/easyzfs.
#   --source <dir>    Compila desde el repo fuente (requiere go y make;
#                     node/npm solo si existe web/ en el fuente)
#   --port <n>        Puerto de escucha (defecto: 8080)
#   --root-mode       El servicio corre como root (sin usuario easyzfs/sudoers)
#   --uninstall       Desinstala unit, binario y sudoers (pregunta por datos)
#   --yes, -y         No interactivo: acepta todos los valores por defecto
#   --help, -h        Muestra la ayuda
#
# Entorno:
#   DRY_RUN=1              Imprime los comandos sin ejecutarlos (pruebas)
#   EASYZFS_RELEASE_URL     Equivalente a --url
#   NO_COLOR=1             Desactiva los colores
# =============================================================================
set -euo pipefail

# ---- Constantes de despliegue (rutas y nombres fijos de la app) ----
readonly APP="easyzfs"
readonly SCRIPT_VERSION="1.0.0"
readonly INSTALL_BIN="/usr/local/bin/easyzfs"
readonly SYSD_HELPER="/usr/local/libexec/easyzfs-sysd"
readonly UNIT_PATH="/etc/systemd/system/easyzfs.service"
readonly SUDOERS_PATH="/etc/sudoers.d/easyzfs"
readonly ENV_DIR="/etc/easyzfs"
readonly ENV_FILE="${ENV_DIR}/env"
readonly DATA_DIR="/var/lib/easyzfs"
readonly SVC_USER="easyzfs"

# URL oficial de releases (one-liner curl|bash). {arch} = x86_64|aarch64.
readonly DEFAULT_RELEASE_URL="https://github.com/gnacho/easyzfs/releases/latest/download/easyzfs-linux-{arch}"

# ---- Opciones (flags / variables de entorno) ----
OPT_BINARY=""
OPT_URL="${EASYZFS_RELEASE_URL:-$DEFAULT_RELEASE_URL}"
OPT_SOURCE=""
OPT_PORT="8080"
PORT_FROM_FLAG=0
OPT_ROOT_MODE=0
OPT_DEMO=0
OPT_UNINSTALL=0
OPT_YES=0
DRY_RUN="${DRY_RUN:-0}"

# ---- Estado global rellenado por las funciones de detección ----
DISTRO_ID="unknown"
DISTRO_FAMILY="unknown"
DISTRO_PRETTY="desconocida"
ARCH="unknown"
USE_WHIPTAIL=0
SUDO=()
BIN_MODE=""
GENERATED_ADMIN=""

# ---- Colores (solo si stdout es un TTY; NO_COLOR los desactiva) ----
if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
  C_RESET=$'\033[0m'; C_BOLD=$'\033[1m'
  C_RED=$'\033[31m'; C_GREEN=$'\033[32m'; C_YELLOW=$'\033[33m'
  C_BLUE=$'\033[34m'; C_CYAN=$'\033[36m'; C_GRAY=$'\033[90m'
else
  C_RESET=""; C_BOLD=""; C_RED=""; C_GREEN=""; C_YELLOW=""
  C_BLUE=""; C_CYAN=""; C_GRAY=""
fi

# ---- Mensajes con símbolos (${C_*:-} para poder extraer funciones con sed) ----
info() { printf '%s\n' "${C_CYAN:-}»${C_RESET:-} $*"; }
ok()   { printf '%s\n' "${C_GREEN:-}✔${C_RESET:-} $*"; }
warn() { printf '%s\n' "${C_YELLOW:-}⚠${C_RESET:-} $*" >&2; }
err()  { printf '%s\n' "${C_RED:-}✖${C_RESET:-} $*" >&2; }
die()  { err "$*"; exit 1; }
step() { printf '\n%s\n' "${C_BOLD:-}${C_BLUE:-}══ $* ══${C_RESET:-}"; }

# ---- run: ejecuta (o imprime en DRY-RUN) un comando simple ----
run() {
  if [ "$DRY_RUN" = "1" ]; then
    printf '%s\n' "${C_GRAY:-}[DRY-RUN]${C_RESET:-} $*" >&2
    return 0
  fi
  "$@"
}

# ---- write_root_file RUTA MODO: escribe stdin como root con permisos MODO ----
write_root_file() {
  local path="$1" mode="$2"
  if [ "$DRY_RUN" = "1" ]; then
    cat > /dev/null # consume stdin
    printf '%s\n' "${C_GRAY:-}[DRY-RUN]${C_RESET:-} escribir ${path} (modo ${mode})" >&2
    return 0
  fi
  "${SUDO[@]}" install -m "$mode" /dev/stdin "$path"
}

# ---- Banner ASCII ----
banner() {
  printf '%s\n' "${C_BLUE:-}${C_BOLD:-}"
  cat <<'EOF'
███████╗ █████╗ ███████╗██╗   ██╗███████╗███████╗███████╗
██╔════╝██╔══██╗██╔════╝╚██╗ ██╔╝╚══███╔╝██╔════╝██╔════╝
█████╗  ███████║███████╗ ╚████╔╝   ███╔╝ █████╗  ███████╗
██╔══╝  ██╔══██║╚════██║  ╚██╔╝   ███╔╝  ██╔══╝  ╚════██║
███████╗██║  ██║███████║   ██║   ███████╗██║     ███████║
╚══════╝╚═╝  ╚═╝╚══════╝   ╚═╝   ╚══════╝╚═╝     ╚══════╝
EOF
  printf '%s\n' "${C_RESET:-}  Instalador de ${APP} v${SCRIPT_VERSION} — gestión ZFS para tu NAS"
  if [ "$DRY_RUN" = "1" ]; then
    printf '%s\n\n' "${C_YELLOW:-}  *** MODO DRY-RUN: no se aplicará ningún cambio real ***${C_RESET:-}"
  else
    printf '\n'
  fi
}

usage() {
  cat <<'EOF'
Instalador de EasyZFS — despliegue en cualquier servidor Linux con systemd.

Uso:
  bash install.sh [opciones]
  curl -fsSL <url>/install.sh | bash -s -- --yes
  DRY_RUN=1 bash install.sh --binary ./easyzfs --yes   # ensayo sin cambios

Opciones:
  --binary <ruta>   Binario local (defecto: ./easyzfs si existe)
  --url <url>       URL de release; acepta {arch} (x86_64|aarch64). Si la URL
                    no apunta a un fichero, se asume <base>/easyzfs-linux-<arch>.
                    También vía EASYZFS_RELEASE_URL. Defecto: releases gnacho/easyzfs.
  --source <dir>    Compila desde el repo fuente (go + make; node/npm si hay web/)
  --port <n>        Puerto de escucha (defecto: 8080)
  --demo            Arranca en modo demo (DEMO=1: datos de muestra, mutaciones 403)
  --root-mode       El servicio corre como root (sin usuario easyzfs ni sudoers)
  --uninstall       Desinstala unit, binario y sudoers (pregunta por los datos)
  --yes, -y         No interactivo: todo por defecto
  --help, -h        Muestra esta ayuda

Entorno:
  DRY_RUN=1              Imprime los comandos sin ejecutarlos
  EASYZFS_RELEASE_URL     Equivalente a --url
  NO_COLOR=1             Desactiva los colores
EOF
}

# =============================================================================
# UI: whiptail si hay TTY + whiptail; si no, prompts de texto elegantes.
# En modo pipe (`bash <(curl ...)`) los prompts usan /dev/tty cuando existe.
# =============================================================================

setup_ui() {
  USE_WHIPTAIL=0
  if [ "$OPT_YES" = "0" ] && [ -t 0 ] && [ -t 1 ] \
     && [ "${TERM:-dumb}" != "dumb" ] && command -v whiptail >/dev/null 2>&1; then
    USE_WHIPTAIL=1
  fi
}

# tty_ok — ¿hay terminal de control usable? (-r/-w no bastan: ENXIO sin ctty)
tty_ok() { (exec 3<>/dev/tty) 2>/dev/null; }

# Imprime un prompt (%b: interpreta \n) en /dev/tty si existe; si no, en stderr.
_prompt_out() {
  if tty_ok; then
    printf '%b' "$*" > /dev/tty
  else
    printf '%b' "$*" >&2
  fi
}

# _read_line VAR [secreto:0|1] — lee de /dev/tty (modo pipe) o de stdin.
_read_line() {
  local __var="$1" __secret="${2:-0}" __val=""
  if tty_ok; then
    if [ "$__secret" = "1" ]; then
      IFS= read -rs __val < /dev/tty || true
      printf '\n' > /dev/tty
    else
      IFS= read -r __val < /dev/tty || true
    fi
  else
    if [ "$__secret" = "1" ]; then
      IFS= read -rs __val || true
      printf '\n' >&2
    else
      IFS= read -r __val || true
    fi
  fi
  printf -v "$__var" '%s' "$__val"
}

# confirm "pregunta" [defecto: 0=no, 1=sí] → devuelve 0 (sí) o 1 (no)
confirm() {
  local text="$1" def="${2:-0}" reply="" hint="s/N"
  if [ "$OPT_YES" = "1" ]; then return "$def"; fi
  if [ "$USE_WHIPTAIL" = "1" ]; then
    if [ "$def" = "1" ]; then
      whiptail --title "$APP" --yesno "$text" 10 68 --defaultyes
    else
      whiptail --title "$APP" --yesno "$text" 10 68
    fi
    return $?
  fi
  [ "$def" = "1" ] && hint="S/n"
  while true; do
    _prompt_out "${text} [${hint}] "
    _read_line reply
    case "${reply,,}" in
      "") return "$def" ;;
      s|si|sí|y|yes) return 0 ;;
      n|no) return 1 ;;
      *) _prompt_out "Responde 's' o 'n'.\n" ;;
    esac
  done
}

# prompt VAR "texto" "defecto" — entrada de texto con valor por defecto.
prompt() {
  local __var="$1" text="$2" def="${3:-}" reply=""
  if [ "$OPT_YES" = "1" ]; then printf -v "$__var" '%s' "$def"; return 0; fi
  if [ "$USE_WHIPTAIL" = "1" ]; then
    reply=$(whiptail --title "$APP" --inputbox "$text" 10 68 "$def" 3>&1 1>&2 2>&3) || reply="$def"
    printf -v "$__var" '%s' "${reply:-$def}"
    return 0
  fi
  _prompt_out "${text} [${def}]: "
  _read_line reply
  printf -v "$__var" '%s' "${reply:-$def}"
}

# prompt_password VAR "texto" — entrada oculta (vacío = cancelar/generar).
prompt_password() {
  local __var="$1" text="$2" reply=""
  if [ "$OPT_YES" = "1" ]; then printf -v "$__var" '%s' ""; return 0; fi
  if [ "$USE_WHIPTAIL" = "1" ]; then
    reply=$(whiptail --title "$APP" --passwordbox "$text" 10 68 3>&1 1>&2 2>&3) || reply=""
  else
    _prompt_out "${text} (oculta; vacío = generar aleatoria): "
    _read_line reply 1
  fi
  printf -v "$__var" '%s' "$reply"
}

# menu VAR "título" "texto" tag1 desc1 tag2 desc2 ... → VAR=tag elegido (""=cancelar)
menu() {
  # OJO: la variable de resultado interna usa prefijo __ para no colisionar
  # (ámbito dinámico de bash) con el nombre que pida el llamador (p. ej. "choice").
  local __var="$1" title="$2" text="$3" __choice=""
  shift 3
  if [ "$USE_WHIPTAIL" = "1" ]; then
    __choice=$(whiptail --title "$title" --menu "$text" 17 72 7 "$@" 3>&1 1>&2 2>&3) || __choice=""
  else
    local i=1 n=""
    local tags=()
    _prompt_out "\n${C_BOLD:-}${text}${C_RESET:-}\n"
    while [ $# -ge 2 ]; do
      tags+=("$1")
      _prompt_out "  ${i}) $1 — $2\n"
      shift 2
      i=$((i + 1))
    done
    _prompt_out "Elige [1-${#tags[@]}] (vacío = cancelar): "
    _read_line n
    if [[ "$n" =~ ^[0-9]+$ ]] && [ "$n" -ge 1 ] && [ "$n" -le "${#tags[@]}" ]; then
      __choice="${tags[$((n - 1))]}"
    fi
  fi
  printf -v "$__var" '%s' "$__choice"
}

# =============================================================================
# Detección del sistema
# =============================================================================

# detect_distro — /etc/os-release → DISTRO_ID / DISTRO_FAMILY / DISTRO_PRETTY.
# Familias: debian | arch | rhel | suse | alpine | unknown.
detect_distro() {
  DISTRO_ID="unknown"; DISTRO_FAMILY="unknown"
  DISTRO_PRETTY="desconocida"
  if [ -r /etc/os-release ]; then
    local ID="" ID_LIKE="" PRETTY_NAME=""
    # shellcheck disable=SC1091  # /etc/os-release es un fichero de datos estándar
    . /etc/os-release
    DISTRO_ID="${ID:-unknown}"
    DISTRO_PRETTY="${PRETTY_NAME:-$DISTRO_ID}"
    local tokens=" ${DISTRO_ID} ${ID_LIKE:-} "
    case "$tokens" in
      *" debian "*|*" ubuntu "*) DISTRO_FAMILY="debian" ;;
      *" arch "*|*" manjaro "*|*" endeavouros "*) DISTRO_FAMILY="arch" ;;
      *" fedora "*|*" rhel "*|*" centos "*|*" almalinux "*|*" rocky "*|*" ol "*) DISTRO_FAMILY="rhel" ;;
      *" suse "*|*" opensuse "*|*" sled "*|*" sles "*) DISTRO_FAMILY="suse" ;;
      *" alpine "*) DISTRO_FAMILY="alpine" ;;
      *) DISTRO_FAMILY="unknown" ;;
    esac
  fi
}

# detect_arch — uname -m → ARCH (x86_64 | aarch64 | unknown).
detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64) ARCH="x86_64" ;;
    aarch64|arm64) ARCH="aarch64" ;;
    *) ARCH="unknown" ;;
  esac
}

# check_root — exige root o sudo; en DRY-RUN solo avisa y simula con 'sudo'.
check_root() {
  SUDO=()
  if [ "$(id -u)" -eq 0 ]; then
    ok "Ejecutando como root."
    return 0
  fi
  if [ "$DRY_RUN" = "1" ]; then
    warn "DRY-RUN sin root: los comandos se mostrarán prefijados con 'sudo'."
    SUDO=(sudo)
    return 0
  fi
  if command -v sudo >/dev/null 2>&1; then
    SUDO=(sudo)
    if sudo -v; then
      ok "Sin root; se usará sudo para los pasos privilegiados."
      return 0
    fi
  fi
  die "Se necesita root (o sudo con contraseña validable). Reejecuta como root o con sudo."
}

# check_systemd — requiere systemd en ejecución (PID 1). Alpine/OpenRC: no soportado.
check_systemd() {
  if [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1; then
    ok "systemd detectado y en ejecución."
    return 0
  fi
  if [ "$DISTRO_FAMILY" = "alpine" ]; then
    if [ "$DRY_RUN" = "1" ]; then
      warn "Alpine usa OpenRC: aún no soportado (DRY-RUN: continúo el ensayo)."
      return 0
    fi
    die "Alpine usa OpenRC y este instalador aún no soporta servicios OpenRC (solo systemd)."
  fi
  if [ "$DRY_RUN" = "1" ]; then
    warn "systemd no está activo en este entorno (DRY-RUN: continúo el ensayo)."
    return 0
  fi
  die "No se detectó systemd en ejecución (PID 1). Este instalador requiere systemd."
}

# check_resources — pre-flight de disco y RAM (bloquea solo si el disco es crítico).
check_resources() {
  local avail=""
  avail="$(df -Pm / 2>/dev/null | awk 'NR==2 {print $4}')"
  if [ -n "$avail" ]; then
    if [ "$avail" -lt 300 ]; then
      die "Espacio en disco insuficiente: ${avail} MB libres (mínimo 300 MB para ZFS + EasyZFS)."
    elif [ "$avail" -lt 600 ]; then
      warn "Poco espacio en disco: ${avail} MB libres (recomendado 600+ MB)."
    else
      ok "Espacio en disco: ${avail} MB libres."
    fi
  fi
  local mem=""
  mem="$(awk '/^MemAvailable:/ {print int($2/1024)}' /proc/meminfo 2>/dev/null)"
  if [ -n "$mem" ] && [ "$mem" -lt 512 ]; then
    warn "RAM disponible baja: ${mem} MB. ZFS rinde mejor con 512+ MB libres."
  fi
}

# port_in_use N — ¿hay algo escuchando en el puerto N? Si no hay herramienta
# de red (ss/netstat), se asume libre.
port_in_use() {
  local p="$1"
  if command -v ss >/dev/null 2>&1; then
    ss -tln 2>/dev/null | awk '{print $4}' | grep -qE "[:.]${p}\$"
  elif command -v netstat >/dev/null 2>&1; then
    netstat -tln 2>/dev/null | awk '{print $4}' | grep -qE "[:.]${p}\$"
  else
    return 1
  fi
}

# next_free_port BASE → imprime el primer puerto libre en (BASE, BASE+20].
next_free_port() {
  local p=$(( $1 + 1 )) end=$(( $1 + 21 ))
  while [ "$p" -le "$end" ]; do
    if ! port_in_use "$p"; then printf '%s' "$p"; return 0; fi
    p=$((p + 1))
  done
  return 1
}

# =============================================================================
# Dependencias: ZFS + herramientas, con mapeo de paquetes por familia
# =============================================================================

deps_debian() {
  info "Instalando paquetes con apt…"
  run "${SUDO[@]}" apt-get update -qq
  run "${SUDO[@]}" env DEBIAN_FRONTEND=noninteractive apt-get install -y \
      zfsutils-linux smartmontools util-linux curl ca-certificates \
    || die "apt-get falló instalando zfsutils-linux / smartmontools."
  # Headers del kernel para DKMS (puede no aplicar en LXC/contenedores: solo aviso)
  if ! run "${SUDO[@]}" env DEBIAN_FRONTEND=noninteractive apt-get install -y \
      "linux-headers-$(uname -r)"; then
    warn "No se pudieron instalar los headers del kernel ($(uname -r)); normal en LXC/contenedores."
    warn "Si el módulo ZFS no carga, instala los headers correctos y ejecuta: dkms autoinstall"
  fi
}

deps_arch() {
  info "Instalando paquetes con pacman…"
  if run "${SUDO[@]}" pacman -Sy --needed --noconfirm zfs-utils smartmontools util-linux curl; then
    return 0
  fi
  warn "pacman no pudo instalar 'zfs-utils': no está en los repos oficiales de Arch."
  cat >&2 <<'EOF'
  Opciones para ZFS en Arch/Manjaro/EndeavourOS:
    1) Repo [archzfs] — añade a /etc/pacman.conf:
         [archzfs]
         Server = https://archzfs.com/$repo/$arch
       Importa y firma la clave:
         pacman-key -r DDF7DB817396A49B2A2723F7403BD972F75D9D76
         pacman-key --lsign-key DDF7DB817396A49B2A2723F7403BD972F75D9D76
       Luego: pacman -Sy zfs-utils
    2) AUR (DKMS): yay -S zfs-dkms zfs-utils   (requiere base-devel y headers)
EOF
  confirm "¿Ya tienes zfs-utils instalado por otra vía y quieres continuar?" 0 \
    || die "Instala ZFS (archzfs o AUR) y reintenta."
}

deps_rhel() {
  local pm="dnf"
  command -v dnf >/dev/null 2>&1 || pm="yum"
  local dist=""
  dist="$(rpm --eval '%{dist}' 2>/dev/null || true)"
  local base="fedora"
  [ "$DISTRO_ID" = "fedora" ] || base="epel"
  if [ -n "$dist" ]; then
    local repo_url="https://zfsonlinux.org/${base}/zfs-release-2-3${dist}.noarch.rpm"
    info "Añadiendo el repo ZFS on Linux: ${repo_url}"
    if ! run "${SUDO[@]}" "$pm" install -y "$repo_url"; then
      warn "No se pudo instalar zfs-release (¿no hay build para ${DISTRO_PRETTY}?)."
      warn "Descarga el RPM correcto de https://zfsonlinux.org/ e instálalo a mano."
    fi
  else
    warn "No se pudo evaluar %%{dist}; instala el repo zfs-release a mano si falla 'zfs'."
  fi
  run "${SUDO[@]}" "$pm" install -y zfs smartmontools util-linux curl \
    || warn "La instalación de paquetes falló; revisa el repo ZFS on Linux."
  # kernel-devel para DKMS (solo aviso si no está)
  run "${SUDO[@]}" "$pm" install -y kernel-devel \
    || warn "kernel-devel no disponible: DKMS podría no compilar el módulo ZFS."
}

deps_suse() {
  info "Instalando paquetes con zypper…"
  if run "${SUDO[@]}" zypper --non-interactive install --no-recommends \
      zfs smartmontools util-linux curl; then
    return 0
  fi
  warn "zypper falló: 'zfs' puede requerir el repo 'filesystems' de openSUSE."
  cat >&2 <<'EOF'
  Añade el repo (sustituye <VERSION>, p. ej. openSUSE_Leap_15.6):
    zypper ar -f https://download.opensuse.org/repositories/filesystems/<VERSION>/ filesystems
    zypper ref && zypper install zfs
EOF
  confirm "¿Continuar asumiendo que ZFS ya está instalado?" 0 \
    || die "Instala ZFS (repo filesystems) y reintenta."
}

deps_alpine() {
  warn "Alpine: asegúrate de tener el repo 'community' habilitado en /etc/apk/repositories."
  run "${SUDO[@]}" apk add zfs smartmontools util-linux curl \
    || die "apk falló (¿está habilitado el repo community?)."
}

# verify_zfs_stack — modprobe + comprobación real de zpool y smartctl.
verify_zfs_stack() {
  info "Cargando el módulo ZFS y verificando herramientas…"
  run "${SUDO[@]}" modprobe zfs || true
  if [ "$DRY_RUN" = "1" ]; then
    info "[DRY-RUN] verificaría: zpool version && smartctl --version"
    return 0
  fi
  if ! zpool version >/dev/null 2>&1; then
    err "El módulo ZFS no está disponible ('zpool version' falló tras modprobe)."
    cat >&2 <<'EOF'
  Pistas:
    • DKMS sin headers del kernel: instala linux-headers / kernel-devel
      y ejecuta: dkms autoinstall && modprobe zfs
    • Secure Boot: un módulo sin firmar no carga; fírmalo con mokutil
      o desactiva Secure Boot en la UEFI
    • Contenedor LXC: el host debe cargar ZFS y pasar el módulo/dispositivos
EOF
    exit 1
  fi
  ok "ZFS operativo: $(zpool version 2>/dev/null | head -1)"
  if smartctl --version >/dev/null 2>&1; then
    ok "smartmontools: $(smartctl --version 2>/dev/null | head -1)"
  else
    warn "smartctl no disponible: las funciones SMART de EasyZFS no funcionarán."
  fi
}

install_dependencies() {
  step "Dependencias del sistema (${DISTRO_FAMILY})"
  case "$DISTRO_FAMILY" in
    debian) deps_debian ;;
    arch)   deps_arch ;;
    rhel)   deps_rhel ;;
    suse)   deps_suse ;;
    alpine) deps_alpine ;;
    *)
      warn "Distribución no reconocida (${DISTRO_PRETTY}): no hay mapeo automático de paquetes."
      warn "Instala manualmente: zfs (utils), smartmontools, util-linux, curl, ca-certificates."
      confirm "¿Continuar en modo manual (asumo dependencias ya instaladas)?" 0 \
        || die "Instalación cancelada. Instala las dependencias y reintenta."
      ;;
  esac
  verify_zfs_stack
}

# =============================================================================
# Origen e instalación del binario EasyZFS
# =============================================================================

# resolve_asset_url — sustituye {arch}; si la URL no apunta a un fichero,
# asume patrón de GitHub releases: <base>/easyzfs-linux-<arch>.
resolve_asset_url() {
  local url="$1"
  if [[ "$url" == *"{arch}"* ]]; then
    url="${url//\{arch\}/${ARCH}}"
  elif [[ "$url" != *.tar.gz && "$url" != */easyzfs* ]]; then
    url="${url%/}/easyzfs-linux-${ARCH}"
  fi
  printf '%s\n' "$url"
}

# select_binary_source — prioridad: --binary > --url > --source > ./easyzfs;
# en interactivo ofrece menú (compilar solo si hay go + make + Makefile).
select_binary_source() {
  if [ -n "$OPT_BINARY" ]; then BIN_MODE="local"; return 0; fi
  if [ -n "$OPT_URL" ];    then BIN_MODE="download"; return 0; fi
  if [ -n "$OPT_SOURCE" ]; then BIN_MODE="build"; return 0; fi

  local src_dir="${EASYZFS_SOURCE_DIR:-.}"
  local can_build=0
  if command -v go >/dev/null 2>&1 && command -v make >/dev/null 2>&1 \
     && [ -f "${src_dir}/Makefile" ]; then
    can_build=1
  fi

  if [ "$OPT_YES" = "1" ]; then
    if [ -f "./easyzfs" ]; then OPT_BINARY="./easyzfs"; BIN_MODE="local"; return 0; fi
    if [ "$can_build" = "1" ]; then OPT_SOURCE="$src_dir"; BIN_MODE="build"; return 0; fi
    die "No hay origen para el binario: usa --binary, --url o coloca ./easyzfs junto al script."
  fi

  local choice=""
  if [ "$can_build" = "1" ]; then
    menu choice "EasyZFS — origen del binario" "¿Cómo quieres obtener el binario?" \
      local "Usar un binario local (p. ej. ./easyzfs)" \
      download "Descargar desde una URL de release" \
      build "Compilar desde el código fuente (go + make)"
  else
    menu choice "EasyZFS — origen del binario" "¿Cómo quieres obtener el binario?" \
      local "Usar un binario local (p. ej. ./easyzfs)" \
      download "Descargar desde una URL de release"
  fi
  case "$choice" in
    local)
      BIN_MODE="local"
      prompt OPT_BINARY "Ruta del binario easyzfs" "./easyzfs"
      ;;
    download)
      BIN_MODE="download"
      prompt OPT_URL "URL de release (acepta {arch})" \
        "https://github.com/gnacho/easyzfs/releases/latest/download/easyzfs-linux-{arch}"
      ;;
    build)
      BIN_MODE="build"
      prompt OPT_SOURCE "Directorio del repo fuente" "$src_dir"
      ;;
    *)
      die "No se eligió origen del binario; instalación cancelada."
      ;;
  esac
}

install_binary() {
  step "Instalación del binario (${BIN_MODE})"
  case "$BIN_MODE" in
    local)
      if [ ! -f "$OPT_BINARY" ] && [ "$DRY_RUN" != "1" ]; then
        die "No existe el binario: ${OPT_BINARY}"
      fi
      run "${SUDO[@]}" install -m 0755 "$OPT_BINARY" "$INSTALL_BIN"
      ;;
    download)
      [ "$ARCH" != "unknown" ] || die "Arquitectura '$(uname -m)' no soportada para descarga (x86_64/aarch64)."
      local url=""
      url="$(resolve_asset_url "$OPT_URL")"
      info "Descargando: ${url}"
      if [ "$DRY_RUN" = "1" ]; then
        info "[DRY-RUN] curl -fsSL '${url}' → ${INSTALL_BIN} (extrayendo si es .tar.gz)"
      else
        local tmp="" bin=""
        tmp="$(mktemp -d)"
        curl -fsSL "$url" -o "${tmp}/asset" || die "La descarga falló: ${url}"
        # Verificación sha256 contra el checksums de la misma release.
        # Desde v2.1.3 hay un fichero por arch (checksums-<arch>.txt); las
        # releases anteriores publicaban un checksums.txt único.
        local sums_url="${url%/*}/checksums-${ARCH}.txt"
        if ! curl -fsSL "$sums_url" -o "${tmp}/checksums.txt" 2>/dev/null; then
          sums_url="${url%/*}/checksums.txt"
          curl -fsSL "$sums_url" -o "${tmp}/checksums.txt" 2>/dev/null || rm -f "${tmp}/checksums.txt"
        fi
        if [ -s "${tmp}/checksums.txt" ]; then
          local want="" got=""
          want="$(grep "easyzfs-linux-${ARCH}\$" "${tmp}/checksums.txt" | awk '{print $1}' | head -1)"
          got="$(sha256sum "${tmp}/asset" | awk '{print $1}')"
          [ -n "$want" ] || die "checksums no lista easyzfs-linux-${ARCH} (¿release sin asset para esta arch?)."
          [ "$want" = "$got" ] || die "sha256 NO COINCIDE para easyzfs-linux-${ARCH} — descarga corrupta o manipulada."
          ok "sha256 verificado contra $(basename "$sums_url")."
        else
          warn "La release no publica checksums (${sums_url}): descarga SIN verificar."
          confirm "¿Continuar sin verificación de integridad?" 1 || die "Instalación cancelada por seguridad."
        fi
        bin="${tmp}/asset"
        # Si el asset es un .tar.gz, se extrae y se localiza el binario dentro
        if file "${tmp}/asset" 2>/dev/null | grep -qi 'gzip compressed'; then
          tar -xzf "${tmp}/asset" -C "$tmp" || die "No se pudo descomprimir el asset."
          bin="$(find "$tmp" -type f -name easyzfs -print -quit)"
          [ -n "$bin" ] || die "El asset descargado no contiene un binario 'easyzfs'."
        fi
        "${SUDO[@]}" install -m 0755 "$bin" "$INSTALL_BIN"
        rm -rf "$tmp"
      fi
      ;;
    build)
      [ -d "$OPT_SOURCE" ] || die "No existe el directorio fuente: ${OPT_SOURCE}"
      if [ "$DRY_RUN" != "1" ]; then
        command -v go   >/dev/null 2>&1 || die "Falta 'go' para compilar (instálalo o usa --binary/--url)."
        command -v make >/dev/null 2>&1 || die "Falta 'make' para compilar."
        if [ -d "${OPT_SOURCE}/web" ] && ! command -v npm >/dev/null 2>&1; then
          die "Existe ${OPT_SOURCE}/web pero falta 'npm' para compilar el front (Node 20+)."
        fi
        info "Compilando con make (web + binario estático)…"
        make -C "$OPT_SOURCE" build || die "La compilación falló."
        [ -f "${OPT_SOURCE}/easyzfs" ] || die "make no produjo ${OPT_SOURCE}/easyzfs."
        "${SUDO[@]}" install -m 0755 "${OPT_SOURCE}/easyzfs" "$INSTALL_BIN"
      else
        info "[DRY-RUN] make -C ${OPT_SOURCE} build && install easyzfs → ${INSTALL_BIN}"
      fi
      ;;
    *)
      die "Modo de instalación del binario desconocido: ${BIN_MODE}"
      ;;
  esac
  ok "Binario instalado en ${INSTALL_BIN}"
}

# install_sysd_helper — helper root confinado (edición/migración de tareas del
# sistema): local del repo si existe; si no, desde el repo público.
install_sysd_helper() {
  step "Helper de tareas del sistema (easyzfs-sysd)"
  run "${SUDO[@]}" mkdir -p "$(dirname "$SYSD_HELPER")" \
    || die "No se pudo crear $(dirname "$SYSD_HELPER")"
  local src=""
  local script_dir; script_dir="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd || echo .)"
  if [ -f "${script_dir}/easyzfs-sysd" ]; then
    src="${script_dir}/easyzfs-sysd"
  elif [ -n "$OPT_BINARY" ] && [ -f "$(dirname "$OPT_BINARY")/easyzfs-sysd" ]; then
    src="$(dirname "$OPT_BINARY")/easyzfs-sysd"
  fi
  if [ -n "$src" ]; then
    run "${SUDO[@]}" install -m 0755 "$src" "$SYSD_HELPER" \
      || die "No se pudo instalar el helper en $SYSD_HELPER"
  else
    local url="https://raw.githubusercontent.com/gnacho/easyzfs/main/deploy/easyzfs-sysd"
    info "Descargando helper: $url"
    local tmp; tmp="$(mktemp)"
    curl -fsSL "$url" -o "$tmp" || die "No se pudo descargar el helper."
    run "${SUDO[@]}" install -m 0755 "$tmp" "$SYSD_HELPER" \
      || die "No se pudo instalar el helper en $SYSD_HELPER"
    rm -f "$tmp"
  fi
  ok "Helper instalado en ${SYSD_HELPER}"
}

# =============================================================================
# Cuenta de servicio y sudoers limitado
# =============================================================================

setup_user_and_sudoers() {
  step "Cuenta de servicio y privilegios"
  if [ "$OPT_ROOT_MODE" = "0" ] && [ "$OPT_YES" = "0" ]; then
    local choice=""
    menu choice "EasyZFS — privilegios" "¿Con qué usuario debe correr el servicio?" \
      easyzfs "Usuario de sistema 'easyzfs' + sudoers limitado (recomendado)" \
      root "root — administración completa sin sudoers (decisión consciente)"
    [ "$choice" = "root" ] && OPT_ROOT_MODE=1
  fi
  if [ "$OPT_ROOT_MODE" = "1" ]; then
    warn "Modo root: el servicio correrá como root (appliance de administración; sin sudoers)."
    return 0
  fi
  if id "$SVC_USER" >/dev/null 2>&1; then
    ok "El usuario de sistema '${SVC_USER}' ya existe."
  else
    run "${SUDO[@]}" useradd --system --shell /usr/sbin/nologin \
        --home-dir "$DATA_DIR" --comment "EasyZFS service" "$SVC_USER" \
      || run "${SUDO[@]}" useradd -r -s /usr/sbin/nologin -d "$DATA_DIR" "$SVC_USER" \
      || die "No se pudo crear el usuario de sistema '${SVC_USER}'."
    ok "Usuario de sistema '${SVC_USER}' creado."
  fi
  write_sudoers
}

# write_sudoers — NOPASSWD solo para zpool/zfs/smartctl/lsblk y, con
# argumentos restringidos al uso real del código: crontab -l (lectura del
# crontab de root en la vista Tareas), hdparm -y /dev/* (standby de disco),
# udisksctl power-off -b /dev/* (apagado de disco). Valida con visudo -cf.
write_sudoers() {
  local zpool_path zfs_path smartctl_path lsblk_path crontab_path udisksctl_path hdparm_path content
  zpool_path="$(command -v zpool 2>/dev/null || echo /usr/sbin/zpool)"
  zfs_path="$(command -v zfs 2>/dev/null || echo /usr/sbin/zfs)"
  smartctl_path="$(command -v smartctl 2>/dev/null || echo /usr/sbin/smartctl)"
  lsblk_path="$(command -v lsblk 2>/dev/null || echo /usr/bin/lsblk)"
  crontab_path="$(command -v crontab 2>/dev/null || echo /usr/bin/crontab)"
  udisksctl_path="$(command -v udisksctl 2>/dev/null || echo /usr/bin/udisksctl)"
  hdparm_path="$(command -v hdparm 2>/dev/null || echo /usr/sbin/hdparm)"
  content="${SVC_USER} ALL=(root) NOPASSWD: ${zpool_path}, ${zfs_path}, ${smartctl_path}, ${lsblk_path}, ${crontab_path} -l, ${hdparm_path} -y /dev/*, ${udisksctl_path} power-off -b /dev/*, ${SYSD_HELPER}"
  if [ "$DRY_RUN" = "1" ]; then
    info "[DRY-RUN] escribiría ${SUDOERS_PATH} (0440) y validaría con visudo -cf:"
    printf '    %s\n' "$content"
    return 0
  fi
  local tmp=""
  tmp="$(mktemp)"
  printf '%s\n' "$content" > "$tmp"
  if command -v visudo >/dev/null 2>&1; then
    if ! visudo -cf "$tmp" >/dev/null; then
      rm -f "$tmp"
      die "visudo rechazó el fichero sudoers generado (no se instala)."
    fi
  fi
  "${SUDO[@]}" install -m 0440 "$tmp" "$SUDOERS_PATH"
  rm -f "$tmp"
  ok "Sudoers limitado instalado: ${content}"
}

# =============================================================================
# Directorios, fichero env y unit de systemd
# =============================================================================

setup_dirs() {
  step "Directorios"
  local owner="root" group="root"
  if [ "$OPT_ROOT_MODE" = "0" ]; then owner="$SVC_USER"; group="$SVC_USER"; fi
  run "${SUDO[@]}" install -d -m 0755 -o "$owner" -g "$group" "$DATA_DIR"
  run "${SUDO[@]}" install -d -m 0750 "$ENV_DIR"
  ok "${DATA_DIR} (datos) y ${ENV_DIR} (config) listos."
}

# random_password — contraseña aleatoria de 20 caracteres alfanuméricos.
random_password() {
  if [ "$DRY_RUN" = "1" ]; then printf '%s' "DRYRUN-password-0000"; return 0; fi
  openssl rand -base64 24 | tr -dc 'A-Za-z0-9' | cut -c1-20
}

# configure_env — /etc/easyzfs/env (0600) con las vars exactas que acepta el
# binario (internal/config): LISTEN_ADDR, DB_PATH, SESSION_SECRET, ADMIN_PASSWORD.
# Idempotente: en reinstalaciones reutiliza los secretos y el puerto ya existentes.
configure_env() {
  step "Configuración (${ENV_FILE})"

  # Reutilizar secretos y puerto existentes (idempotencia en reinstalaciones)
  local existing_secret="" existing_admin="" existing_port=""
  local existing_vapid_pub="" existing_vapid_priv="" existing_vapid_sub=""
  if [ "$DRY_RUN" != "1" ] && [ -r "$ENV_FILE" ]; then
    existing_secret="$(sed -n 's/^SESSION_SECRET=//p' "$ENV_FILE" | head -1)"
    existing_admin="$(sed -n 's/^ADMIN_PASSWORD=//p' "$ENV_FILE" | head -1)"
    existing_port="$(sed -n 's/^LISTEN_ADDR=://p' "$ENV_FILE" | head -1)"
    existing_vapid_pub="$(sed -n 's/^VAPID_PUBLIC_KEY=//p' "$ENV_FILE" | head -1)"
    existing_vapid_priv="$(sed -n 's/^VAPID_PRIVATE_KEY=//p' "$ENV_FILE" | head -1)"
    existing_vapid_sub="$(sed -n 's/^VAPID_SUBJECT=//p' "$ENV_FILE" | head -1)"
    existing_wbhook="$(sed -n 's/^WEBHOOK_SECRET=//p' "$ENV_FILE" | head -1)"
  fi
  # Prioridad del puerto: --port > puerto del env existente > defecto (8080)
  if [ "$PORT_FROM_FLAG" = "0" ] && [ -n "$existing_port" ]; then
    OPT_PORT="$existing_port"
  fi

  if [ "$OPT_YES" = "0" ] && [ "$PORT_FROM_FLAG" = "0" ]; then
    prompt OPT_PORT "Puerto de escucha de la interfaz web" "$OPT_PORT"
  fi
  if ! [[ "$OPT_PORT" =~ ^[0-9]+$ ]] || [ "$OPT_PORT" -lt 1 ] || [ "$OPT_PORT" -gt 65535 ]; then
    die "Puerto inválido: ${OPT_PORT}"
  fi

  # Puerto ocupado: si coincide con el del env existente es NUESTRO propio
  # servicio corriendo (reinstalación) y no hay conflicto. Si es otro proceso:
  # --port explícito aborta; interactivo pregunta (sugiere el siguiente libre);
  # no interactivo usa el siguiente libre con aviso.
  if [ "$DRY_RUN" != "1" ] && [ "$OPT_PORT" != "$existing_port" ] && port_in_use "$OPT_PORT"; then
    if [ "$PORT_FROM_FLAG" = "1" ]; then
      die "El puerto ${OPT_PORT} ya está en uso (se pidió con --port). Elige otro: ss -tlnp | grep :${OPT_PORT}"
    fi
    local next=""
    next="$(next_free_port "$OPT_PORT")" || next=""
    if [ "$OPT_YES" = "0" ] && tty_ok; then
      local elegido=""
      while true; do
        prompt elegido "El puerto ${OPT_PORT} está ocupado. ¿En qué puerto escucha EasyZFS?" "${next:-}"
        if ! [[ "$elegido" =~ ^[0-9]+$ ]] || [ "$elegido" -lt 1 ] || [ "$elegido" -gt 65535 ]; then
          warn "Puerto inválido: ${elegido} (1-65535)."
          continue
        fi
        if port_in_use "$elegido"; then
          warn "El puerto ${elegido} también está en uso."
          continue
        fi
        OPT_PORT="$elegido"
        break
      done
    elif [ -n "$next" ]; then
      warn "El puerto ${OPT_PORT} está en uso por otro proceso; EasyZFS escuchará en ${next}."
      OPT_PORT="$next"
    else
      die "Puerto ${OPT_PORT} ocupado y ninguno libre entre $((OPT_PORT + 1)) y $((OPT_PORT + 21))."
    fi
  fi

  local secret="$existing_secret"
  if [ -z "$secret" ]; then
    if [ "$DRY_RUN" = "1" ]; then
      secret="dry-run-session-secret"
    else
      secret="$(openssl rand -hex 32)"
    fi
  fi

  local admin="$existing_admin"
  if [ -z "$admin" ]; then
    local typed=""
    prompt_password typed "Contraseña del usuario 'admin'"
    if [ -n "$typed" ]; then
      admin="$typed"
    else
      admin="$(random_password)"
      GENERATED_ADMIN="$admin"
    fi
  fi

  # Modo demo: --demo, o pregunta en instalaciones nuevas interactivas.
  if [ "$OPT_DEMO" = "0" ] && [ ! -r "$ENV_FILE" ] && [ "$OPT_YES" = "0" ] && [ "$DRY_RUN" != "1" ]; then
    if confirm "¿Arrancar en MODO DEMO? (pools/discos de muestra para explorar; tus discos no se tocan)" 0; then
      OPT_DEMO=1
    fi
  fi

  # Claves VAPID (notificaciones Web Push): se generan UNA vez con el binario
  # recién instalado (-generate-vapid). Idempotente: si ya existen en el env
  # se conservan — regenerarlas invalidaría todas las suscripciones push.
  local vapid_pub="$existing_vapid_pub" vapid_priv="$existing_vapid_priv"
  local vapid_sub="$existing_vapid_sub"
  [ -z "$vapid_sub" ] && vapid_sub="mailto:easyzfs@localhost"
  if [ -z "$vapid_priv" ]; then
    if [ "$DRY_RUN" = "1" ]; then
      vapid_pub="dry-run-vapid-public"
      vapid_priv="dry-run-vapid-private"
    else
      local vapid_keys=""
      if vapid_keys="$("${SUDO[@]}" "$INSTALL_BIN" -generate-vapid 2>/dev/null)"; then
        vapid_pub="$(printf '%s\n' "$vapid_keys" | sed -n 's/^VAPID_PUBLIC_KEY=//p' | head -1)"
        vapid_priv="$(printf '%s\n' "$vapid_keys" | sed -n 's/^VAPID_PRIVATE_KEY=//p' | head -1)"
      fi
      if [ -z "$vapid_priv" ]; then
        warn "No se pudieron generar las claves VAPID: push desactivado (el servicio arrancará igual; añade VAPID_* a ${ENV_FILE} a mano)."
      fi
    fi
  else
    info "Claves VAPID ya presentes en ${ENV_FILE}: se conservan."
  fi

  # WEBHOOK_SECRET: firma HMAC para webhooks salientes. Generar UNA vez;
  # idempotente: en upgrades se conserva.
  local wbhook="$existing_wbhook"
  if [ -z "$wbhook" ]; then
    if [ "$DRY_RUN" = "1" ]; then
      wbhook="dry-run-webhook-secret"
    else
      wbhook="$(openssl rand -hex 32)"
    fi
  fi

  # OJO: $(...) elimina los \n finales; por eso el contenido se compone con
  # saltos de línea literales y se escribe con un único printf '%s\n'.
  local env_content="LISTEN_ADDR=:${OPT_PORT}
DB_PATH=${DATA_DIR}/app.db
SESSION_SECRET=${secret}
ADMIN_PASSWORD=${admin}
WEBHOOK_SECRET=${wbhook}"
  if [ -n "$vapid_priv" ]; then
    env_content+="
VAPID_PUBLIC_KEY=${vapid_pub}
VAPID_PRIVATE_KEY=${vapid_priv}
VAPID_SUBJECT=${vapid_sub}"
  fi
  if [ "$OPT_DEMO" = "1" ]; then
    env_content+="
DEMO=1"
  fi

  if [ "$DRY_RUN" = "1" ]; then
    info "[DRY-RUN] escribiría ${ENV_FILE} (modo 0600):"
    printf '    %s\n' "LISTEN_ADDR=:${OPT_PORT}" "DB_PATH=${DATA_DIR}/app.db" \
      "SESSION_SECRET=***" "ADMIN_PASSWORD=***" \
      "WEBHOOK_SECRET=***"
      "VAPID_PUBLIC_KEY=***" "VAPID_PRIVATE_KEY=***" "VAPID_SUBJECT=${vapid_sub}" \
      "$(if [ "$OPT_DEMO" = "1" ]; then echo 'DEMO=1'; fi)"
  else
    printf '%s\n' "$env_content" | write_root_file "$ENV_FILE" 0600
  fi
  ok "Configuración escrita en ${ENV_FILE} (modo 600)."
  if [ "$OPT_DEMO" = "1" ]; then
    info "MODO DEMO activado (DEMO=1): datos de muestra; las mutaciones responden 403 demo_mode."
    info "Para pasar a producción: quita DEMO=1 de ${ENV_FILE} y reinicia el servicio."
  else
    info "Opcionales que puedes añadir: COOKIE_SECURE=1 (tras proxy TLS), RETENTION_DAYS=30, DEMO=1, MOCK=1."
  fi
}

# write_unit — unit basada en deploy/easyzfs.service del repo, con el usuario elegido.
write_unit() {
  step "Servicio systemd"
  local user="root" group="root"
  local nota="modo root: administración completa (decisión consciente, ver README)"
  local nnp="NoNewPrivileges=yes"
  if [ "$OPT_ROOT_MODE" = "0" ]; then
    user="$SVC_USER"; group="$SVC_USER"
    nota="zpool/zfs/smartctl/lsblk/crontab vía sudoers limitado: ${SUDOERS_PATH}"
    # NoNewPrivileges=yes bloquea el bit setuid de sudo: solo se puede poner
    # en modo root (sin sudo). En modo usuario+sudoers tiene que ir fuera.
    nnp="# NoNewPrivileges=yes (incompatible con sudo setuid; superficie root limitada por sudoers)"
  fi
  local unit=""
  unit="$(cat <<EOF
[Unit]
Description=EasyZFS — gestión ZFS del NAS (colector + PWA)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${user}
Group=${group}
EnvironmentFile=${ENV_FILE}
ExecStart=${INSTALL_BIN}
Restart=on-failure
RestartSec=5

# Huella y longevidad
MemoryMax=256M
LimitNOFILE=4096

# Hardening (${nota})
${nnp}
ProtectSystem=full
ReadWritePaths=${DATA_DIR} /etc/cron.d /etc/crontab /etc/systemd/system
ProtectHome=yes
PrivateTmp=yes

# Solo si escucha en puerto <1024 (preferir puerto alto + proxy):
# AmbientCapabilities=CAP_NET_BIND_SERVICE

[Install]
WantedBy=multi-user.target
EOF
)"
  if [ "$DRY_RUN" = "1" ]; then
    info "[DRY-RUN] escribiría ${UNIT_PATH} (modo 0644):"
    printf '%s\n' "$unit" | sed 's/^/    /'
  else
    printf '%s\n' "$unit" | write_root_file "$UNIT_PATH" 0644
  fi
  # daemon-reload + enable + restart: reinstalar actualiza el binario y reinicia
  run "${SUDO[@]}" systemctl daemon-reload
  run "${SUDO[@]}" systemctl enable easyzfs.service
  run "${SUDO[@]}" systemctl restart easyzfs.service
  ok "Servicio habilitado y (re)iniciado."
}

# =============================================================================
# Verificación final y resumen
# =============================================================================

verify_service() {
  step "Verificación"
  if [ "$DRY_RUN" = "1" ]; then
    info "[DRY-RUN] comprobaría: systemctl is-active easyzfs y HTTP en 127.0.0.1:${OPT_PORT}"
    return 0
  fi
  local i
  for i in $(seq 1 10); do
    if systemctl is-active --quiet easyzfs; then break; fi
    sleep 1
  done
  if ! systemctl is-active --quiet easyzfs; then
    err "El servicio no arrancó. Revisa: journalctl -u easyzfs -n 50 --no-pager"
    return 1
  fi
  ok "Servicio activo (systemd)."
  # /api/version: 200 si responde, 401 si exige login — ambos prueban que escucha.
  # OJO: curl imprime "000" con -w incluso al fallar la conexión; no concatenar
  # otro "000" con `|| echo 000` (salía "000000"). Reintenta unos segundos:
  # el servicio puede tardar un poco en bindear tras el restart.
  local code="000" i
  for i in $(seq 1 10); do
    code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 \
        "http://127.0.0.1:${OPT_PORT}/api/version" 2>/dev/null)" || true
    [ "$code" != "000" ] && break
    sleep 1
  done
  if [ "$code" = "000" ]; then
    warn "Sin respuesta HTTP en 127.0.0.1:${OPT_PORT} (¿firewall o arranque lento?)."
    warn "Comprueba: systemctl status easyzfs && journalctl -u easyzfs -n 50"
  else
    ok "HTTP escuchando en 127.0.0.1:${OPT_PORT} (/api/version → código ${code})."
  fi
}

summary() {
  local ip=""
  ip="$(hostname -I 2>/dev/null | awk '{print $1}')"
  ip="${ip:-127.0.0.1}"
  step "Instalación completada"
  cat <<EOF
  URL:      http://${ip}:${OPT_PORT}
  Usuario:  admin
EOF
  if [ -n "$GENERATED_ADMIN" ]; then
    printf '  Clave:    %s  %s\n' "$GENERATED_ADMIN" \
      "${C_YELLOW:-}(guárdala: solo se muestra esta vez)${C_RESET:-}"
  else
    printf '  Clave:    la de ADMIN_PASSWORD en %s\n' "$ENV_FILE"
  fi
  cat <<EOF

  Comandos útiles:
    systemctl status easyzfs
    journalctl -u easyzfs -f
    systemctl restart easyzfs
  Config: ${ENV_FILE}  ·  Datos: ${DATA_DIR}
EOF
  if [ "$OPT_DEMO" = "1" ]; then
    cat <<EOF
  MODO DEMO activo: pools y discos de muestra; nada se modifica (403 demo_mode).
  Para usar tus discos reales: quita DEMO=1 de ${ENV_FILE} y reinicia.
EOF
  else
    cat <<EOF
  Para explorar primero con datos de muestra: añade DEMO=1 a ${ENV_FILE} y reinicia.
EOF
  fi
  cat <<EOF
  Desinstalar: curl -fsSL https://raw.githubusercontent.com/gnacho/easyzfs/main/deploy/install.sh | bash -s -- --uninstall
EOF
}

# =============================================================================
# Desinstalación
# =============================================================================

do_uninstall() {
  step "Desinstalación de ${APP}"
  local found=0
  [ -e "$UNIT_PATH" ] && found=1
  [ -e "$INSTALL_BIN" ] && found=1
  [ -e "$SUDOERS_PATH" ] && found=1
  [ -d "$ENV_DIR" ] && found=1
  [ -d "$DATA_DIR" ] && found=1
  if [ "$found" = "0" ]; then
    ok "No hay nada instalado de ${APP}; nada que hacer."
    return 0
  fi
  check_root
  if command -v systemctl >/dev/null 2>&1; then
    run "${SUDO[@]}" systemctl stop easyzfs.service || true
    run "${SUDO[@]}" systemctl disable easyzfs.service || true
  fi
  [ -e "$UNIT_PATH" ] && { run "${SUDO[@]}" rm -f "$UNIT_PATH"; ok "Unit eliminada: ${UNIT_PATH}"; }
  if command -v systemctl >/dev/null 2>&1; then
    run "${SUDO[@]}" systemctl daemon-reload || true
  fi
  [ -e "$INSTALL_BIN" ] && { run "${SUDO[@]}" rm -f "$INSTALL_BIN"; ok "Binario eliminado: ${INSTALL_BIN}"; }
  [ -e "$SYSD_HELPER" ] && { run "${SUDO[@]}" rm -f "$SYSD_HELPER"; ok "Helper eliminado: ${SYSD_HELPER}"; }
  [ -e "$SUDOERS_PATH" ] && { run "${SUDO[@]}" rm -f "$SUDOERS_PATH"; ok "Sudoers eliminado: ${SUDOERS_PATH}"; }

  if [ -d "$DATA_DIR" ] || [ -d "$ENV_DIR" ]; then
    if [ "$OPT_YES" = "1" ]; then
      warn "Se conservan los datos (${DATA_DIR}) y la config (${ENV_DIR})."
      warn "Para borrarlos: rm -rf ${DATA_DIR} ${ENV_DIR}"
    elif confirm "¿Borrar también los datos y la configuración (${DATA_DIR}, ${ENV_DIR})?" 0; then
      run "${SUDO[@]}" rm -rf "$DATA_DIR" "$ENV_DIR"
      ok "Datos y configuración eliminados."
    fi
  fi
  if id "$SVC_USER" >/dev/null 2>&1; then
    if [ "$OPT_YES" = "0" ] && confirm "¿Eliminar también el usuario de sistema '${SVC_USER}'?" 0; then
      run "${SUDO[@]}" userdel "$SVC_USER" || true
      ok "Usuario '${SVC_USER}' eliminado."
    fi
  fi
  ok "Desinstalación completada."
}

# =============================================================================
# Argumentos y flujo principal
# =============================================================================

parse_args() {
  while [ $# -gt 0 ]; do
    case "$1" in
      --binary)   [ $# -ge 2 ] || die "--binary requiere un valor"; OPT_BINARY="$2"; shift 2 ;;
      --binary=*)  OPT_BINARY="${1#*=}"; shift ;;
      --url)      [ $# -ge 2 ] || die "--url requiere un valor"; OPT_URL="$2"; shift 2 ;;
      --url=*)     OPT_URL="${1#*=}"; shift ;;
      --source)   [ $# -ge 2 ] || die "--source requiere un valor"; OPT_SOURCE="$2"; shift 2 ;;
      --source=*)  OPT_SOURCE="${1#*=}"; shift ;;
      --port)     [ $# -ge 2 ] || die "--port requiere un valor"; OPT_PORT="$2"; PORT_FROM_FLAG=1; shift 2 ;;
      --port=*)    OPT_PORT="${1#*=}"; PORT_FROM_FLAG=1; shift ;;
      --demo)      OPT_DEMO=1; shift ;;
      --root-mode) OPT_ROOT_MODE=1; shift ;;
      --uninstall) OPT_UNINSTALL=1; shift ;;
      --yes|-y)    OPT_YES=1; shift ;;
      --help|-h)   usage; exit 0 ;;
      *) die "Opción desconocida: $1 (usa --help)" ;;
    esac
  done
}

main() {
  parse_args "$@"
  banner
  setup_ui
  if [ "$DRY_RUN" = "1" ]; then
    warn "MODO DRY-RUN: se imprimirán los comandos sin ejecutarlos."
  fi

  if [ "$OPT_UNINSTALL" = "1" ]; then
    do_uninstall
    exit 0
  fi

  detect_arch
  if [ "$ARCH" = "unknown" ]; then
    warn "Arquitectura no reconocida ($(uname -m)); solo x86_64 y aarch64 tienen assets de release."
  fi
  detect_distro
  ok "Sistema: ${DISTRO_PRETTY}  [familia=${DISTRO_FAMILY}, arch=${ARCH}]"
  if [ "$DISTRO_FAMILY" = "unknown" ]; then
    warn "Distribución desconocida: se ofrecerá continuar en modo manual en el paso de dependencias."
  fi

  check_root
  check_systemd
  check_resources
  install_dependencies
  select_binary_source
  install_binary
  install_sysd_helper
  setup_user_and_sudoers
  setup_dirs
  configure_env
  write_unit
  verify_service
  summary
}

main "$@"
