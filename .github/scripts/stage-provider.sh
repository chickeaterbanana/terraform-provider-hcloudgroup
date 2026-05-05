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

# goreleaser names amd64 dist dirs <name>_<goos>_amd64_v1 (with the
# goamd64 level suffix) but arm64 gets <name>_<goos>_arm64_v8.0
# (goarm64 level). Glob over both so a single GOOS/GOARCH pair finds
# its dir regardless of the suffix.
DIR=""
for d in "$DIST_DIR"/terraform-provider-hcloudgroup_${GOOS}_${GOARCH}*; do
  [ -d "$d" ] || continue
  DIR="$d"
  break
done
[ -n "$DIR" ] || { echo "no dist dir matching ${GOOS}_${GOARCH}* under $DIST_DIR" >&2; exit 2; }

BIN=""
for f in "$DIR"/terraform-provider-hcloudgroup_v*; do
  [ -e "$f" ] || break
  BIN="$f"
  break
done
[ -n "$BIN" ] || { echo "no goreleaser binary in $DIR" >&2; exit 2; }

chmod +x "$BIN"
(cd "$DIR" && ln -sf "$(basename "$BIN")" terraform-provider-hcloudgroup)
# `direct` must explicitly exclude the dev-overridden provider, otherwise
# OpenTofu's `init` still issues a registry lookup for the override
# target and fails with "provider registry ... does not have a provider
# named ...". Terraform handles dev_overrides during init without the
# exclude, but matching both is harmless.
printf 'provider_installation {\n  dev_overrides { "chickeaterbanana/hcloudgroup" = "%s" }\n  direct { exclude = ["chickeaterbanana/hcloudgroup"] }\n}\n' \
  "$WORKSPACE/$DIR" > "$DIST_DIR/dev_overrides.tfrc"
