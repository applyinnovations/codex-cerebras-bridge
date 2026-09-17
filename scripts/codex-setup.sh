#!/usr/bin/env bash
# Points CODEX_HOME/config.toml at the Bifrost Cerebras profile without
# destroying your current codex config.
#
#   - Backs up the existing config.toml to config.toml.bak-<timestamp> (in place,
#     only if config.toml is a regular file)
#   - Writes cerebras.config.toml (idempotent - never clobbers an existing one)
#   - Symlinks config.toml -> cerebras.config.toml
#
# Usage:
#   ./scripts/codex-setup.sh                  # uses default $HOME/.codex
#   CODEX_HOME=/path/to/other ./scripts/codex-setup.sh
#
#   ./scripts/codex-setup.sh switch-default   # point config.toml back at the
#                                             # saved default (and remove the
#                                             # cerebras config)
#
# Optional env:
#   BIFROST_BASE_URL  default http://127.0.0.1:8080/v1
#   CODEX_MODEL       default cerebras/qwen-3.8-27b
set -euo pipefail

CODEX_HOME="${CODEX_HOME:-${HOME}/.codex}"
CONFIG="${CODEX_HOME}/config.toml"
PROFILE="${CODEX_HOME}/cerebras.config.toml"
BASE_URL="${BIFROST_BASE_URL:-http://127.0.0.1:8080/v1}"
MODEL="${CODEX_MODEL:-cerebras/qwen-3.8-27b}"

mkdir -p "${CODEX_HOME}"

case "${1:-}" in
  switch-default)
    if [[ -L "${CONFIG}" ]]; then
      target="$(readlink "${CONFIG}")"
      if [[ "$(basename "${target}")" == "cerebras.config.toml" ]]; then
        bak=""
        for f in "${CODEX_HOME}"/config.toml.bak-*; do [[ -f "${f}" ]] && bak="${f}"; done
        if [[ -n "${bak}" ]]; then
          rm "${CONFIG}"
          cp "${bak}" "${CONFIG}"
          echo "Restored ${CONFIG} from ${bak}"
        else
          echo "No config.toml.bak-* backup found in ${CODEX_HOME}; nothing to restore." >&2
          exit 1
        fi
      else
        echo "config.toml is a symlink to ${target} (not the cerebras profile); leaving it alone." >&2
        exit 1
      fi
    else
      echo "config.toml is not a symlink; nothing to switch." >&2
      exit 1
    fi
    # remove the profile (optional; keeps CODEX_HOME tidy)
    rm -f "${PROFILE}"
    echo "Switched Codex back to the saved default config."
    exit 0
    ;;
  "") ;;
  *) echo "usage: $0 [switch-default]" >&2; exit 2 ;;
esac

# 1. Back up existing config (regular file only; skip symlinks/dirs)
if [[ -e "${CONFIG}" && ! -L "${CONFIG}" && -f "${CONFIG}" ]]; then
  ts="$(date +%Y%m%d-%H%M%S)"
  cp -a "${CONFIG}" "${CONFIG}.bak-${ts}"
  echo "Backed up ${CONFIG} -> ${CONFIG}.bak-${ts}"
elif [[ -L "${CONFIG}" ]]; then
  echo "config.toml is already a symlink -> $(readlink "${CONFIG}"); no backup taken."
fi

# 2. Write profile (never overwrite an existing one)
if [[ -e "${PROFILE}" ]]; then
  echo "Profile already exists at ${PROFILE}; leaving it unchanged."
else
  cat > "${PROFILE}" <<TOML
# Codex via BiFrost -> Cerebras
model = "${MODEL}"
model_provider = "bifrost"
model_context_window = 131072

[model_providers.bifrost]
name = "Bifrost"
base_url = "${BASE_URL}"
wire_api = "responses"
requires_openai_auth = false
experimental_bearer_token = "unused"
http_headers = { "x-bf-store-raw-request-response" = "true" }
TOML
  echo "Wrote ${PROFILE}"
fi

# 3. Symlink in place
ln -sfn cerebras.config.toml "${CONFIG}"
echo "Linked ${CONFIG} -> cerebras.config.toml"
echo
echo "Done. Test with:  codex \"say hi\""
