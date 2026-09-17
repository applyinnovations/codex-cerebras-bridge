#!/usr/bin/env bash
# Builds the Bifrost Docker image used by this repo:
#
#   bifrost-cerebras:latest
#
# The stock maximhq/bifrost image is CGO-disabled, so Go plugin loading
# (plugin.Open) fails inside it. This build compiles BOTH the Bifrost
# host binary (dynamically linked) and the codex-cerebras-compat plugin
# .so inside one image, with one Go toolchain, so the plugin loads.
#
# The Bifrost "transports" module source is needed for this build
# (it is NOT vendored in this repo). Provide it with EITHER:
#
#   BIFROST_TRANSPORTS_DIR=/path/to/transports   (a checkout of the
#     "transports" module directory of github.com/maximhq/bifrost)
#
#   or let this script clone it:
#
#   BIFROST_REF=<ref>            (defaults to master)
#
# The build verifies transports/go.mod pins the same
# github.com/maximhq/bifrost/core version as plugin/go.mod requires,
# because host and plugin MUST share the core version to link cleanly.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGE="${BIFROST_IMAGE:-bifrost-cerebras:latest}"
CORE_VERSION="$(grep -oP 'github.com/maximhq/bifrost/core \Kv[\d.]+' "${REPO_ROOT}/plugin/go.mod")"

WORK="$(mktemp -d)"
trap 'rm -rf "${WORK}"' EXIT

mkdir -p "${WORK}/repo-src"

if [[ -n "${BIFROST_TRANSPORTS_DIR:-}" ]]; then
  if [[ ! -f "${BIFROST_TRANSPORTS_DIR}/go.mod" ]]; then
    echo "error: ${BIFROST_TRANSPORTS_DIR} does not look like a Go module (no go.mod)" >&2
    exit 1
  fi
  cp -r "${BIFROST_TRANSPORTS_DIR}" "${WORK}/repo-src/transports"
else
  REF="${BIFROST_REF:-master}"
  echo "Cloning github.com/maximhq/bifrost (${REF}) ..."
  git clone --depth 1 --branch "${REF}" https://github.com/maximhq/bifrost "${WORK}/bifrost"
  if [[ ! -f "${WORK}/bifrost/transports/go.mod" ]]; then
    echo "error: cloned repo has no transports/ module at ref ${REF}" >&2
    exit 1
  fi
  cp -r "${WORK}/bifrost/transports" "${WORK}/repo-src/transports"
fi

if ! grep -q "github.com/maximhq/bifrost/core v${CORE_VERSION}" "${WORK}/repo-src/transports/go.mod"; then
  echo "error: transports/go.mod does not pin github.com/maximhq/bifrost/core v${CORE_VERSION}." >&2
  echo "       The plugin builds against core v${CORE_VERSION}; use a BIFROST_REF or" >&2
  echo "       BIFROST_TRANSPORTS_DIR whose transports/go.mod pins the same core version." >&2
  exit 1
fi

cp -r "${REPO_ROOT}/plugin" "${WORK}/plugin"

echo "Building ${IMAGE} (CGO-enabled host + plugin) ..."
docker build -f "${WORK}/plugin/Dockerfile.build" -t "${IMAGE}" "${WORK}"

echo
echo "Image ready: ${IMAGE} (sha: $(docker images -q "${IMAGE}"))"
echo "Start it with: ./scripts/bifrost-restart.sh"
