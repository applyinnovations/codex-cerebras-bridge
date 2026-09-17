#!/usr/bin/env bash
# Builds the Bifrost plugin .so for this host's architecture.
#
# The host main binary used with this .so must be the dynamically-linked
# maximhq/bifrost host image build (see README "Rebuilding the .so"): the
# .so is compiled with CGO_ENABLED=1 -buildmode=plugin against the exact
# git commit of github.com/maximhq/bifrost/core listed in plugin/go.mod.
set -euo pipefail

PLUGIN_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../plugin" && pwd)"
cd "${PLUGIN_DIR}"

if [[ ! -s codex-base-instructions.md ]]; then
  echo "error: plugin/codex-base-instructions.md is missing or empty" >&2
  exit 1
fi

echo "Building codex-cerebras-compat.so ..."
CGO_ENABLED=1 go build -buildmode=plugin -trimpath -o codex-cerebras-compat.so .

echo "Built: ${PLUGIN_DIR}/codex-cerebras-compat.so"
if command -v sha256sum >/dev/null 2>&1; then
  echo "Remember to verify the host/container checksums match after restart:"
  sha256sum codex-cerebras-compat.so
fi
