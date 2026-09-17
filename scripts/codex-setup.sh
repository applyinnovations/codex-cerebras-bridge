#!/usr/bin/env bash
# Manage Codex's config for the Bifrost Cerebras profile.
#
#   setup    (default) save the existing config.toml as default.config.toml,
#                      write cerebras.config.toml, symlink config.toml to it
#   default  symlink config.toml back to default.config.toml
#   status   print config state (always exits 0)
#
# CODEX_HOME defaults to ~/.codex
set -euo pipefail

CODEX_HOME="${CODEX_HOME:-${HOME}/.codex}"
CONFIG="${CODEX_HOME}/config.toml"
DEFAULT_CFG="${CODEX_HOME}/default.config.toml"
PROFILE="${CODEX_HOME}/cerebras.config.toml"
BASE_URL="${BIFROST_BASE_URL:-http://127.0.0.1:8080/v1}"
MODEL="${CODEX_MODEL:-cerebras/qwen-3.8-27b}"

die() { printf 'error: %s\n' "$*" >&2; exit 1; }

cmd_setup() {
  mkdir -p "${CODEX_HOME}"

  if [[ -L "${CONFIG}" ]]; then
    case "$(basename "$(readlink "${CONFIG}")")" in
      cerebras.config.toml) echo "config.toml already points at the cerebras profile" ;;
      default.config.toml)  echo "config.toml points at saved default; re-pointing to cerebras" ;;
      *) die "config.toml is a symlink to '$(basename "$(readlink "${CONFIG}")")', not managed by these scripts. Move or rename it, then re-run" ;;
    esac
  elif [[ -e "${CONFIG}" ]]; then
    [[ -f "${CONFIG}" ]] || die "config.toml exists but is not a regular file; inspect ${CODEX_HOME}, then re-run"
    if [[ -e "${DEFAULT_CFG}" ]]; then
      die "config.toml is a regular file and ${DEFAULT_CFG} already exists. Refusing to overwrite the saved default - rename one of them, then re-run"
    fi
    mv "${CONFIG}" "${DEFAULT_CFG}"
    echo "saved existing config: config.toml -> default.config.toml"
  fi

  if [[ -e "${PROFILE}" ]]; then
    echo "profile already exists: ${PROFILE} (left unchanged)"
  else
    cat > "${PROFILE}" <<TOML
# Codex via Bifrost -> Cerebras
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
    echo "wrote ${PROFILE}"
  fi

  ln -sfn cerebras.config.toml "${CONFIG}"
  echo "linked config.toml -> cerebras.config.toml"
  if command -v codex >/dev/null 2>&1; then
    echo "test: codex \"say hi\""
  fi
}

cmd_default() {
  if [[ -L "${CONFIG}" ]]; then
    local target
    target="$(basename "$(readlink "${CONFIG}")")"
    case "${target}" in
      default.config.toml)
        echo "config.toml already points at default.config.toml"
        return 0
        ;;
      cerebras.config.toml) : ;;
      *) die "config.toml is a symlink to '${target}', not managed by these scripts" ;;
    esac
  elif [[ -e "${CONFIG}" ]]; then
    die "config.toml is a regular file, not the managed symlink. Manage it manually."
  fi

  [[ -e "${DEFAULT_CFG}" ]] || die "no ${DEFAULT_CFG} found (nothing was saved at setup time). Write it manually, then re-run."

  rm -f "${CONFIG}"
  ln -sfn default.config.toml "${CONFIG}"
  echo "linked config.toml -> default.config.toml"
}

cmd_status() {
  if [[ -L "${CONFIG}" ]]; then
    echo "config.toml:        symlink -> $(readlink "${CONFIG}")"
  elif [[ -e "${CONFIG}" ]]; then
    echo "config.toml:        regular file (not managed by these scripts)"
  else
    echo "config.toml:        missing"
  fi
  [[ -e "${DEFAULT_CFG}" ]] && echo "default.config.toml: present" || echo "default.config.toml: missing"
  [[ -e "${PROFILE}" ]]     && echo "cerebras.config.toml: present"    || echo "cerebras.config.toml: missing"
  return 0
}

case "${1:-setup}" in
  setup)   cmd_setup ;;
  default) cmd_default ;;
  status)  cmd_status ;;
  help|-h|--help)
    cat <<USAGE
Usage: $0 [setup|default|status]
  setup    save config.toml as default.config.toml, link config.toml -> cerebras.config.toml
  default  link config.toml -> default.config.toml (switch back)
  status   print config state
Env: CODEX_HOME, BIFROST_BASE_URL, CODEX_MODEL
USAGE
    ;;
  *) printf 'usage: %s [setup|default|status]\n' "$0" >&2; exit 2 ;;
esac
