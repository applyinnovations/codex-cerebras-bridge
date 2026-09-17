#!/usr/bin/env bash
# One script to onboard and manage the whole stack:
#   - creates ./env and prompts securely for the Cerebras API key (no echo, confirmed)
#   - checks dependencies (docker daemon, curl, git) with clear errors
#   - builds the gateway image on first run
#   - (re)starts the Bifrost container and waits for the health check
#   - wires Codex's config (backup + symlink)
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}"

ENV_FILE="${REPO_ROOT}/env"
CONTAINER_NAME="${BIFROST_CONTAINER:-bifrost}"
HOST_PORT="${BIFROST_PORT:-8080}"
IMAGE="${BIFROST_IMAGE:-bifrost-cerebras:latest}"

log()  { printf '\n==> %s\n' "$*"; }
warn() { printf 'warn:  %s\n' "$*" >&2; }
die()  { printf 'error: %s\n' "$*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

usage() {
  cat <<'USAGE'
onboard.sh - onboard and manage the Codex -> Bifrost -> Cerebras stack

Usage:
  ./scripts/onboard.sh                 full onboarding (key + image + gateway + codex)
  ./scripts/onboard.sh gateway         (re)start the Bifrost gateway only
  ./scripts/onboard.sh build-image     (re)build the docker image
  ./scripts/onboard.sh codex           wire Codex config only
  ./scripts/onboard.sh default         point Codex back at the saved default config
  ./scripts/onboard.sh status          show gateway + codex config state
  ./scripts/onboard.sh plugins         rebuild the plugin .so on the host (needs Go)
  ./scripts/onboard.sh help            this help

Env overrides:
  BIFROST_CONTAINER=bifrost  BIFROST_PORT=8080  BIFROST_IMAGE=bifrost-cerebras:latest
  BIFROST_REF / BIFROST_TRANSPORTS_DIR   image build source (see scripts/build-image.sh)
  CODEX_HOME=~/.codex  BIFROST_BASE_URL=http://127.0.0.1:8080/v1  CODEX_MODEL=cerebras/qwen-3.8-27b
USAGE
}

require_deps() {
  have docker || die "docker not found. Install Docker (https://docs.docker.com/get-docker/) and re-run"
  if ! docker info >/dev/null 2>&1; then
    die "docker daemon is not running or not accessible. Start Docker and/or add your user to the docker group, then re-run"
  fi
  have curl || die "curl not found (needed for the health check). Install curl and re-run"
}

require_git() {
  if [[ -n "${BIFROST_TRANSPORTS_DIR:-}" ]]; then return 0; fi
  have git || die "git not found (needed to clone Bifrost sources for the image build). Install git, or set BIFROST_TRANSPORTS_DIR to a checkout"
}

# Ensures ${ENV_FILE} exists and holds a key. Order:
#   1. existing env file (only if it actually contains a key)
#   2. $CEREBRAS_API_KEY from the current environment (persisted to the env file)
#   3. secure interactive prompt (never echoed, entered twice)
ensure_env_file() {
  if [[ -s "${ENV_FILE}" ]] && grep -q '^CEREBRAS_API_KEY=.+' "${ENV_FILE}"; then
    log "using existing key file: ${ENV_FILE}"
    return 0
  fi
  if [[ -n "${CEREBRAS_API_KEY:-}" ]]; then
    warn "saving CEREBRAS_API_KEY from the environment to ${ENV_FILE}"
    (umask 077; printf 'CEREBRAS_API_KEY=%s\n' "${CEREBRAS_API_KEY}" > "${ENV_FILE}")
    log "wrote ${ENV_FILE} (mode 600, gitignored)"
    return 0
  fi
  if [[ ! -t 0 ]]; then
    die "CEREBRAS_API_KEY is not set and there is no terminal to prompt in. Export CEREBRAS_API_KEY and re-run, or run interactively"
  fi
  log "creating key file ${ENV_FILE}"
  local k1 k2
  while :; do
    printf 'Cerebras API key (csk-...): '
    IFS= read -r -s k1
    printf '\nconfirm the key: '
    IFS= read -r -s k2
    printf '\n'
    if [[ -z "${k1}" || "${k1}" != "${k2}" ]]; then
      warn "key was empty or entries did not match; try again"
      continue
    fi
    (umask 077; printf 'CEREBRAS_API_KEY=%s\n' "${k1}" > "${ENV_FILE}")
    log "wrote ${ENV_FILE} (mode 600, gitignored)"
    break
  done
}

load_key() {
  if [[ -z "${CEREBRAS_API_KEY:-}" && -f "${ENV_FILE}" ]]; then
    local line
    line="$(grep -E '^CEREBRAS_API_KEY=.+' "${ENV_FILE}" | tail -n 1 || true)"
    CEREBRAS_API_KEY="${line#CEREBRAS_API_KEY=}"
  fi
  if [[ -z "${CEREBRAS_API_KEY:-}" ]]; then
    die "CEREBRAS_API_KEY not found. Run './scripts/onboard.sh' (it prompts once), export CEREBRAS_API_KEY, or create ${ENV_FILE}"
  fi
  if [[ ! "${CEREBRAS_API_KEY}" == csk-* ]]; then
    warn "key does not start with 'csk-' - double-check it is a Cerebras key"
  fi
  export CEREBRAS_API_KEY
}

ensure_image() {
  if docker image inspect "${IMAGE}" >/dev/null 2>&1; then
    log "image present: ${IMAGE}"
    return 0
  fi
  log "image ${IMAGE} not found - building (one-time, takes a few minutes)"
  require_git
  ./scripts/build-image.sh
}

start_gateway() {
  require_deps
  load_key
  ensure_image

  if docker ps --format '{{.Names}}' 2>/dev/null | grep -qx "${CONTAINER_NAME}"; then
    log "removing existing container ${CONTAINER_NAME} (restart)"
  fi
  docker rm -f "${CONTAINER_NAME}" >/dev/null 2>&1 || true

  log "starting ${CONTAINER_NAME} on http://127.0.0.1:${HOST_PORT} ..."
  docker run -d \
    --name "${CONTAINER_NAME}" \
    --restart unless-stopped \
    -p "${HOST_PORT}:8080" \
    -v "${REPO_ROOT}:/app/data" \
    -e "CEREBRAS_API_KEY=${CEREBRAS_API_KEY}" \
    -e LOG_LEVEL=info -e LOG_STYLE=json \
    "${IMAGE}" \
    /app/main -app-dir /app/data -port 8080 -host 0.0.0.0 -log-level info -log-style json \
    || die "docker run failed - port :${HOST_PORT} may already be in use. Inspect: docker logs ${CONTAINER_NAME}"

  log "waiting for health check ..."
  local i
  for i in $(seq 1 30); do
    if curl -sf "http://127.0.0.1:${HOST_PORT}/health" >/dev/null 2>&1; then
      log "gateway healthy: http://127.0.0.1:${HOST_PORT} (${i}s)"
      return 0
    fi
    sleep 1
  done
  echo
  docker logs --tail 20 "${CONTAINER_NAME}" 2>&1 | sed 's/^/  | /'
  die "gateway did not become healthy in 30s - see docker logs above"
}

wire_codex() {
  log "wiring Codex config"
  ./scripts/codex-setup.sh
  if have codex; then
    log "done. Test with:  codex \"say hi\""
  else
    warn "codex not found in PATH - install it (npm install -g @openai/codex), then test:  codex \"say hi\""
  fi
}

show_status() {
  ./scripts/codex-setup.sh status
  echo
  if [[ -f "${ENV_FILE}" ]] && grep -q '^CEREBRAS_API_KEY=.+' "${ENV_FILE}"; then
    log "key file: present (${ENV_FILE})"
  else
    warn "key file: missing (${ENV_FILE})"
  fi
  if ! have docker; then
    warn "gateway: unknown (docker not installed)"
  elif docker ps --format '{{.Names}}' 2>/dev/null | grep -qx "${CONTAINER_NAME}"; then
    if curl -sf "http://127.0.0.1:${HOST_PORT}/health" >/dev/null 2>&1; then
      log "gateway: running and healthy (http://127.0.0.1:${HOST_PORT})"
    else
      warn "gateway: container running but health check failing (docker logs ${CONTAINER_NAME})"
    fi
  else
    warn "gateway: container ${CONTAINER_NAME} not running (./scripts/onboard.sh gateway)"
  fi
}

main() {
  case "${1:-all}" in
    all)             start_gateway; wire_codex ;;
    gateway)         start_gateway ;;
    build-image)     exec ./scripts/build-image.sh "${@:2}" ;;
    codex)           shift; ./scripts/codex-setup.sh "$@" ;;
    default)         ./scripts/codex-setup.sh default ;;
    status)          show_status ;;
    plugins)         exec ./scripts/build-plugin.sh ;;
    help|-h|--help)  usage ;;
    *)               die "unknown command: ${1} (see: ./scripts/onboard.sh help)" ;;
  esac
}

main "$@"
