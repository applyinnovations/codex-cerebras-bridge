# codex-cerebras-bridge

Run [Codex](https://github.com/openai/codex) on **Qwen 3.8 27B via Cerebras**, through a local [Bifrost](https://www.getbifrost.ai) gateway. No Python proxy.

**Terminology:** the plugin in this repo is a **Bifrost** plugin (a Go `.so` loaded by Bifrost's `config.json`). It is not a Codex plugin — `codex plugin` manages Codex's own plugin marketplaces and is not part of this setup. Codex only needs to point at the gateway:

```
Codex  ->  Bifrost :8080 (plugin fixes the request)  ->  Cerebras
```

Codex (in `responses` mode) can place `system`/`developer` instructions anywhere in its input, but Cerebras' Qwen requires exactly **one** `system` message first. A small Go plugin that runs **inside Bifrost** hoists all of those instructions into the top-level `instructions` field for the configured model only, so the wire traffic Cerebras sees is always clean. Everything else passes through untouched.

This repo gives you the plugin source, a Bifrost `config.json`, and helper scripts to wire it all up in a few steps.

---

## Requirements

- Docker
- A [Cerebras API key](https://inference.console.cerebras.ai/)
- Go `1.27` — **only** if you want to rebuild the `.so` yourself. The image ships a pre-built one, so a plain first run doesn't need Go.

## Quick start

```bash
# 1. Clone
git clone git@github.com:applyinnovations/codex-cerebras-bridge.git
cd codex-cerebras-bridge

# 2. Set your Cerebras key (read by the start script; kept out of git)
cp scripts/env.example env
$EDITOR env          # set CEREBRAS_API_KEY=csk-...

# 3. Build the gateway image ONCE (host binary + plugin, one Go toolchain)
./scripts/build-image.sh

# 4. Start Bifrost on http://127.0.0.1:8080 (waits for the health check)
./scripts/bifrost-restart.sh

# 5. Point Codex at it (backs up your config, then symlinks a new one)
./scripts/codex-setup.sh

# 6. Test
codex "say hi"
```

That's it. If `codex "say hi"` streams a reply, you're done.

---

## How Codex is configured

`scripts/codex-setup.sh` writes `~/.codex/cerebras.config.toml` and symlinks
`~/.codex/config.toml` to it, after backing up your existing config to
`config.toml.bak-<timestamp>`. The profile it writes:

```toml
# Codex via BiFrost -> Cerebras
model = "cerebras/qwen-3.8-27b"
model_provider = "bifrost"
model_context_window = 131072

[model_providers.bifrost]
name = "Bifrost"
base_url = "http://127.0.0.1:8080/v1"
wire_api = "responses"
requires_openai_auth = false
experimental_bearer_token = "unused"
http_headers = { "x-bf-store-raw-request-response" = "true" }
```

> `http_headers` just tells Bifrost to store each request/response so you can
> inspect what actually hit Cerebras. It's diagnostic and optional.

Tune it with environment variables before running the script:

| Variable           | Default                  | Meaning                       |
| ------------------ | ------------------------ | ----------------------------- |
| `BIFROST_BASE_URL` | `http://127.0.0.1:8080/v1` | Gateway base URL            |
| `CODEX_MODEL`      | `cerebras/qwen-3.8-27b`  | Model slug (Cerebras via Bifrost) |

### No symlink: `codex -p` profile flag

`codex-setup.sh` already writes `$CODEX_HOME/cerebras.config.toml`, which is exactly what Codex's `-p/--profile` flag looks for. If you prefer not to symlink `config.toml`, just run Codex with the profile:

```bash
codex -p cerebras "say hi"
```

This layers `cerebras.config.toml` on top of your existing user config (no backup needed). Either way — symlink or `-p` — the provider block is identical.

### Remote-control / daemon server

If you run Codex as a **remote-control daemon server**, it reads its **own**
`CODEX_HOME`. Either point the script at that home, or paste the snippet above
into that daemon's `config.toml` directly:

```bash
CODEX_HOME=/path/to/daemon-codex-home ./scripts/codex-setup.sh
```

### Manual setup (no script)

Copy the snippet above into your `~/.codex/config.toml` (or the daemon's
`config.toml`) and make sure Bifrost is running. No symlink needed.

---

## Switching back and forth

`codex-setup.sh` never deletes your original config — it backs it up. To go back:

```bash
./scripts/codex-setup.sh switch-default     # restore the saved default config
```

Re-apply Codex to Cerebras any time with `./scripts/codex-setup.sh` again.

---

## Scripts

| Script                          | What it does |
| ------------------------------- | ------------ |
| `scripts/build-image.sh`        | Builds `bifrost-cerebras:latest` (dynamically-linked host + plugin). One-time. |
| `scripts/bifrost-restart.sh`    | Removes + re-runs the `bifrost` container on `:8080`, waits for health. |
| `scripts/codex-setup.sh`        | Wires `config.toml` to the Cerebras profile (or `switch-default` to revert). |
| `scripts/build-plugin.sh`       | Rebuilds just the `.so` on your host (needs Go 1.27). |

### Build options

`build-image.sh` needs Bifrost's `transports` module source (not vendored here).
By default it clones it from `master`. You can override:

```bash
BIFROST_REF=v2.2.0 ./scripts/build-image.sh                 # pin a ref
BIFROST_TRANSPORTS_DIR=/path/to/transports ./scripts/build-image.sh
```

It verifies that module pins the **same** `bifrost/core` version as
`plugin/go.mod` — host and plugin must share it or the plugin won't link.

---

## Rebuilding the `.so` (optional)

The image already contains a working `codex-cerebras-compat.so`. Only rebuild
if you change the plugin code:

```bash
./scripts/build-plugin.sh            # builds plugin/codex-cerebras-compat.so
```

After a rebuild, the checksum matters — verify the container's copy matches
the host's before and after a restart:

```bash
sha256sum plugin/codex-cerebras-compat.so
docker exec bifrost sha256sum /app/codex-cerebras-compat.so
```

Both must match. The `.so` is git-ignored and never committed.

---

## What the plugin does (in one paragraph)

For the configured model slug (`cerebras/qwen-3.8-27b`, alias `qwen-3.8-27b`),
Bifrost's request interceptor scans the **entire** `input[]` of a Responses
call, pulls out every `system` or `developer` message (string or content-block
form), merges them into the top-level `instructions` field, and removes them
from `input[]`. All other items keep their order. Any `system`/`developer`
content the plugin can't represent is rejected with a clear local `400`
instead of leaking to the provider. Other models and normal
`user`/`assistant` traffic pass through unchanged. It also answers
`GET /v1/models?client_version=...` with the Codex catalog while leaving a
plain `/v1/models` untouched.

---

## Troubleshooting

- **`codex: command not found`** — install Codex or put it on `PATH`.
- **Gateway not healthy** — `docker logs bifrost`, then re-run
  `./scripts/bifrost-restart.sh`.
- **`plugin status ... not active` / ABI errors** — the host binary and `.so`
  must share one core version. Rebuild the image (`./scripts/build-image.sh`)
  so both are compiled together, then restart.
- **"System message must be at the beginning"** from the provider — the plugin
  isn't loaded, or this model's slug doesn't match its config. Confirm the
  active line in `docker logs bifrost`, and that the configured slug is the
  one you're actually requesting.
- **Inspecting wire traffic** — with `store_raw_request_response` on, Bifrost
  logs the request/response it actually sent; check
  `.extra_fields.raw_request.messages` (roles) and `.extra_fields.raw_response`.

---

## Layout

```
config.json                     Bifrost config (provider + plugin)
env.example                     CEREBRAS_API_KEY template (not committed)
plugin/                         Go plugin source (main.go, tests)
plugin/Dockerfile.build         Builds host + plugin in one image
scripts/                        build-image, bifrost-restart, codex-setup, build-plugin
```
