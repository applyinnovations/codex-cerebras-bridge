#!/usr/bin/env bash
# Starts or recreates the Bifrost Docker container serving the
# codex-cerebras-compat plugin on http://127.0.0.1:8080.
#
# BIFROST_IMAGE defaults to bifrost-cerebras:latest, which you build ONCE
# with ./scripts/build-image.sh. That image's /app/main and
# /app/codex-cerebras-compat.so share one Go toolchain; the stock
# maximhq/bifrost image is CGO-disabled and cannot plugin.Open.
#
# CEREBRAS_API_KEY is picked up in this order:
#   1. $CEREBRAS_API_KEY already in the environment
#   2. ./env file next to this script (create: cp env.example env, then edit)
#   3. Secret prompt (read -s), never echoed
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONTAINER_NAME="${BIFROST_CONTAINER:-bifrost}"
HOST_PORT="${BIFROST_PORT:-8080}"
IMAGE="${BIFROST_IMAGE:-bifrost-cerebras:latest}"

[[ -f "${REPO_ROOT}/config.json" ]] || {
  echo "error: config.json not found in ${REPO_ROOT}" >&2
  exit 1
}

if [[ -z "${CEREBRAS_API_KEY:-}" && -f "${REPO_ROOT}/env" ]]; then
  set -a
  # shellcheck disable=SC1091
  . "${REPO_ROOT}/env"
  set +a
fi

if [[ -z "${CEREBRAS_API_KEY:-}" ]]; then
  echo -n "CEREBRAS_API_KEY: "
  IFS= read -r -s CEREBRAS_API_KEY
  echo
  if [[ -z "${CEREBRAS_API_KEY}" ]]; then
    echo "error: no Cerebras API key provided. Set CEREBRAS_API_KEY or create ${REPO_ROOT}/env" >&2
    exit 1
  fi
fi

docker rm -f "${CONTAINER_NAME}" 2>/dev/null || true

docker run -d \
  --name "${CONTAINER_NAME}" \
  --restart unless-stopped \
  -p "${HOST_PORT}:8080" \
  -v "${REPO_ROOT}:/app/data" \
  -e "CEREBRAS_API_KEY=${CEREBRAS_API_KEY}" \
  -e LOG_LEVEL=info -e LOG_STYLE=json \
  "${IMAGE}"\
  /app/main -app-dir /app/data -port 8080 -host 0.0.0.0 -log-level info -log-style json

echo "Waiting for Bifrost health check..."
for _ in $(seq 1 30); do
  if curl -sf "http://127.0.0.1:${HOST_PORT}/health" >/dev/null 2>&1; then
    echo "Bifrost is up: http://127.0.0.1:${HOST_PORT}"
    exit 0
  fi
  sleep 1
done

echo "Bifrost did not become healthy in time. Check: docker logs ${CONTAINER_NAME}" >&2
exit 1
