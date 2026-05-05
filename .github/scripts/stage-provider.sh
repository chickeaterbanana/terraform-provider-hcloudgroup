#!/bin/sh
# Stage the goreleaser-built provider for terraform/tofu dev_overrides.
# Writes <DIST_DIR>/dev_overrides.tfrc; the caller must export
# TF_CLI_CONFIG_FILE pointing at it. Used by `make smoke` and by both
# smoke jobs in CI (POSIX sh so the same script runs on bash on Linux/
# macOS and on the FreeBSD VM's default sh).
#
# Required env: GOOS, GOARCH
# Optional env: DIST_DIR (default: dist), WORKSPACE (default: $PWD)

set -eu

: "${GOOS:?}"
: "${GOARCH:?}"
DIST_DIR="${DIST_DIR:-dist}"
WORKSPACE="${WORKSPACE:-$PWD}"

DIR="$DIST_DIR/terraform-provider-hcloudgroup_${GOOS}_${GOARCH}_v1"
[ -d "$DIR" ] || { echo "missing $DIR" >&2; exit 2; }

BIN=""
for f in "$DIR"/terraform-provider-hcloudgroup_v*; do
  [ -e "$f" ] || break
  BIN="$f"
  break
done
[ -n "$BIN" ] || { echo "no goreleaser binary in $DIR" >&2; exit 2; }

chmod +x "$BIN"
(cd "$DIR" && ln -sf "$(basename "$BIN")" terraform-provider-hcloudgroup)
printf 'provider_installation {\n  dev_overrides { "chickeaterbanana/hcloudgroup" = "%s" }\n  direct {}\n}\n' \
  "$WORKSPACE/$DIR" > "$DIST_DIR/dev_overrides.tfrc"
