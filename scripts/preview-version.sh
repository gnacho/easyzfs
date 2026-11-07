#!/bin/sh
# preview-version.sh - versión para un binario de validación (#155).
#
# Política: prerelease de la SIGUIENTE versión (2.9.31-alpha.<sha>).
#   - Queda por ENCIMA de la última release: el updater no pisa la validación.
#   - Queda por DEBAJO de la próxima: al cortarla, el updater la ofrece y el
#     preview se sustituye solo.
# Nunca más '2.9.99-*': semver las deja por encima de toda release y el
# updater quedaba mudo para siempre diciendo "estás al día".
set -eu

last=$(git describe --tags --abbrev=0 --match 'v[0-9]*' 2>/dev/null || echo v0.0.0)
base=${last#v}
major=${base%%.*}
rest=${base#*.}
minor=${rest%%.*}
patch=${rest#*.}
patch=${patch%%[!0-9]*}
[ -n "$patch" ] || patch=0
sha=$(git rev-parse --short HEAD)
printf '%s.%s.%s-alpha.%s\n' "$major" "$minor" "$((patch + 1))" "$sha"
