# codex-cerebras-bridge

Run [Codex](https://github.com/openai/codex) on **Qwen 3.8 27B** via [Cerebras](https://cerebras.ai), through a local [Bifrost](https://www.getbifrost.ai) gateway.

```
Codex -> Bifrost :8080 (plugin fixes the request) -> Cerebras
```

Codex sends `system`/`developer` messages at any point in `input[]`; Cerebras' Qwen requires exactly one `system` message first. A Go plugin **inside Bifrost** hoists all of those into top-level `instructions` for the configured model only. Everything else passes through untouched.

This is a **Bifrost** plugin (a `.so` loaded by Bifrost's `config.json`), not a Codex plugin — Codex only needs to point at the gateway.

## Requirements

- Docker (running daemon)
- A [Cerebras API key](https://inference.console.cerebras.ai/)
- Go 1.27 — only if rebuilding the `.so` (the image ships a pre-built one)

## Quick start

```bash
git clone git@github.com:applyinnovations/codex-cerebras-bridge.git
cd codex-cerebras-bridge

./scripts/onboard.sh     # prompts for your Cerebras key, builds the image,
                         # starts the gateway, wires your Codex config
codex "say hi"
```

That's it.

## Switching

```bash
./scripts/onboard.sh default    # config.toml -> default.config.toml (your original)
./scripts/onboard.sh codex      # config.toml -> cerebras.config.toml (this setup)
```

Your original `~/.codex/config.toml` is moved (not copied) to `~/.codex/default.config.toml` on first run; both are symlinked from `config.toml` as needed.

No-symlink alternative:

```bash
codex -p cerebras "say hi"      # layers ~/.codex/cerebras.config.toml onto your config
```

## Dashboard

http://127.0.0.1:8080 — Bifrost UI/logs. Per-request raw records (what actually hit Cerebras): enable `x-bf-store-raw-request-response` (already set in the profile) and check the provider's stored `raw_request` / `raw_response`.

## The Codex profile it writes

`~/.codex/cerebras.config.toml`:

```toml
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

Override at setup time: `BIFROST_BASE_URL`, `CODEX_MODEL`, `CODEX_HOME` (e.g. for a [remote-control daemon server](https://github.com/openai/codex) that uses its own `CODEX_HOME` — set it before running `./scripts/onboard.sh codex`).

## Scripts

| Command | What it does |
|---|---|
| `./scripts/onboard.sh` | Full onboarding: key + image + gateway + Codex config |
| `./scripts/onboard.sh gateway` | (Re)start the Bifrost container, wait for health |
| `./scripts/onboard.sh build-image` | (Re)build the Docker image (see below) |
| `./scripts/onboard.sh codex` | Wire Codex config only |
| `./scripts/onboard.sh default` | Point Codex back at the saved default config |
| `./scripts/onboard.sh status` | Show gateway + Codex config state |
| `./scripts/onboard.sh plugins` | Rebuild the `.so` on the host (needs Go 1.27) |
| `./scripts/build-plugin.sh` | Same as above, direct |

Env overrides: `BIFROST_CONTAINER=bifrost`, `BIFROST_PORT=8080`, `BIFROST_IMAGE=bifrost-cerebras:latest`.

Your key is stored in a gitignored `env` file at the repo root (mode 600); the script reads it on every gateway start. Pre-seed it with `scripts/env.example` if you prefer.

## Rebuilding the `.so`

`./scripts/onboard.sh plugins` (needs Go 1.27). Then verify the container is running what you built:

```bash
sha256sum plugin/codex-cerebras-compat.so
docker exec bifrost sha256sum /app/codex-cerebras-compat.so
```

The two hashes must match.

`build-image.sh` needs Bifrost's `transports` module (not vendored here). By default it clones `github.com/maximhq/bifrost@master`; point it elsewhere with:

- `BIFROST_TRANSPORTS_DIR=/path/to/transports`
- `BIFROST_REF=<ref>`

## Troubleshooting

| Symptom | Fix |
|---|---|
| `System message must be at the beginning` | Plugin not loaded — check `docker logs bifrost` for plugin/ABI errors; `onboard.sh plugins` + rebuild image |
| Plugin loaded but not matching | Match `model` in your config against the `slug`/`aliases` in `config.json` |
| Port conflict | `docker ps`; change `BIFROST_PORT` (Codex profile must match) |
| Gateway unhealthy | `docker logs bifrost`; check `CEREBRAS_API_KEY` |

## Troubleshooting the plugin (developers)

Bifrost logs every request as JSONL in `bifrost-access.jsonl`. For a failed turn, inspect the provider error structurally:

```
.extra_fields.raw_request.messages | map(.role)    # must be: system at 0, none after
.extra_fields.raw_response                                       # Cerebras' actual answer
```

Plugin normalizer events (path, counts, roles before/after, hoisted count — never message contents) are logged under the `codex-cerebras-compat` logger.

## Layout

```
config.json                  Bifrost config (provider key + plugin + model catalog)
plugin/main.go               the plugin (RequestNormalizer + model catalog)
plugin/codex-base-instructions.md  base instructions baked into instructions
plugin/Dockerfile.build      CGO host + plugin image build
scripts/onboard.sh           one-shot onboarding + management
scripts/build-image.sh       Docker image build
scripts/build-plugin.sh      host-side .so build
```
