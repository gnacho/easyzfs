#!/bin/sh
# easyzfs-update-weekly.sh — chequeo y aplicación semanal de actualizaciones.
#
# Ejecutado por easyzfs-update-weekly.timer (systemd, cadencia semanal).
# Descarga la release ESTABLE de GitHub, verifica sha256, hace backup
# del binario actual, instala el nuevo y reinicia el servicio.
#
# A diferencia del apply in-app (POST /api/update/apply → .restart-me flag →
# easyzfs-update.path → easyzfs-update.service), este script es AUTÓNOMO:
# no depende de que el servicio esté corriendo ni de un admin que pulse un botón.
set -eu

APP=easyzfs
REPO=gnacho/easyzfs
INSTALL_BIN=/usr/local/bin/easyzfs
MARKER=/opt/easyzfs/.release-id
TMP_DIR=$(mktemp -d)
trap 'rm -rf "$TMP_DIR"' EXIT INT TERM

log() { logger -t "$APP-update-weekly" "$@"; }

# 1. Detectar última release estable
echo "STEP:detect"
VER=$(curl -fsSL --max-time 20 "https://api.github.com/repos/$REPO/releases/latest" \
  | sed -n 's/.*"tag_name": *"\(v\?[0-9][^"]*\)".*/\1/p' | head -n1)
[ -n "$VER" ] || { log "no se pudo resolver la última release estable"; exit 4; }
VER_NO_V=$(printf '%s' "$VER" | sed 's/^v//')

# ¿Ya instalado?
if [ -f "$MARKER" ] && [ "$(cat "$MARKER" 2>/dev/null || true)" = "$VER_NO_V" ]; then
  log "al día ($VER_NO_V)"; exit 0
fi

# 2. Descargar binario y checksums
echo "STEP:download"
ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
BIN="${APP}_linux_${ARCH}"
BASE="https://github.com/$REPO/releases/download/$VER"
curl -fL --max-time 120 "$BASE/$BIN" -o "$TMP_DIR/$APP"
curl -fL --max-time 30 "$BASE/checksums.txt" -o "$TMP_DIR/checksums.txt"

# 3. Verificar sha256
echo "STEP:verify"
expected=$(awk -v f="$BIN" '$2=="'"$BIN"'" || index($0, "  '"$BIN"'") {print $1; exit}' "$TMP_DIR/checksums.txt" 2>/dev/null || true)
[ -n "$expected" ] || { log "checksums.txt sin entrada para $BIN (¿release sin checksums?)"; exit 5; }
got=$(sha256sum "$TMP_DIR/$APP" | awk '{print $1}')
[ "$expected" = "$got" ] || { log "SHA256 NO coincide para $BIN"; exit 5; }
log "sha256 verificado: $BIN"

# 4. Backup del binario actual
echo "STEP:backup"
if [ -f "$INSTALL_BIN" ]; then
  cp "$INSTALL_BIN" "${INSTALL_BIN}.bak-$(date +%Y%m%d-%H%M%S)"
fi

# 5. Instalar y reiniciar
echo "STEP:install"
install -m 0755 "$TMP_DIR/$APP" "$INSTALL_BIN"
printf '%s\n' "$VER_NO_V" > "$MARKER"
systemctl restart "$APP.service"

log "actualizado a $VER_NO_V"
echo "OK:$VER_NO_V"
