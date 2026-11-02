# Model runtime API for management dashboards

Status: requirement, not implemented
Date: 2026-11-02
Consumer: `ai_training/llama_dashboard` (management panel)

## Background

The management panel shows, for every model: full-resident VRAM, model path,
context size, GPU layers, KV cache types, parallelism, file size and whether
the running command still matches the registered model dossier.

Today it can only read those values from `/running`, which lists running
processes only. Unloaded models therefore show "parameters unknown" even
though llama-swap already knows their configuration. The panel also cannot
detect that a model's path or resource flags changed while it is unloaded,
so it may admit a load using an old VRAM budget and only notice the mismatch
after the model is loaded.

`/v1/models` is OpenAI-compatible and is consumed by other programs. Do not
add management fields there; they would pay for data they never read.

## Decision

Add a management-only API family:

- `GET /api/models` — all configured models with runtime info.
- `GET /api/models/{id}` — one model, resolved through the normal alias
  lookup.

Leave `/v1/models`, `/running` and `/api/events` unchanged.

No runtime-state cache is needed. `cfg.Models[id].Cmd` is available as soon
as the config is loaded, whether or not the model has ever run. Parse that
configured command. Do not start a model or read its weights to answer the
request.

Only models declared with `proxy:` (llama-swap does not spawn them) have no
`cmd`. For those, llama-swap genuinely cannot know the upstream arguments.
Return `source: "proxy"` and omit unknown fields. Do not guess.

## API contract

### `GET /api/models`

```json
{
  "models": [
    {
      "id": "Qwen3.8-27B-News-Understanding-Q4_K_M",
      "name": "",
      "description": "",
      "state": "stopped",
      "aliases": [],
      "context_length": 32768,
      "runtime": {
        "source": "cmd",
        "model_path": "E:\\models\\news-understanding\\qlora27b_full_20261025_0\\news-understanding-Q4_K_M.gguf",
        "ctx_size": 32768,
        "gpu_layers": "all",
        "cache_type_k": "q8_0",
        "cache_type_v": "q8_0",
        "parallel": 1,
        "batch_size": 1024,
        "ubatch_size": 256,
        "fit": "off",
        "fit_target": null,
        "file_exists": true,
        "file_size": 16810714240,
        "file_mtime": "2026-10-25T12:34:56Z"
      }
    }
  ]
}
```

`GET /api/models/{id}` returns the same object for one model and `404` when
the name is unknown. Accept aliases; return the resolved real `id`.

The list endpoint exists because the panel snapshots every model every few
seconds. Fetching `/api/models/{id}` once per model would work but scales as
N+1 requests as the model library grows.

### Fields

| Field | Type | Source | Notes |
| --- | --- | --- | --- |
| `id` | string | config key | Real model ID |
| `name` | string | `name` | May be empty |
| `description` | string | `description` | May be empty |
| `state` | string | running state | `ready`, `starting`, `stopping`, `stopped` |
| `aliases` | array | `aliases` | Omit or empty when none |
| `context_length` | int | `capabilities.context` or discovered | Omit when unknown |
| `runtime.source` | string | derived | `cmd`, `proxy`, or `unknown` |
| `runtime.model_path` | string | `--model` / `-m` | Same resolution as process launch |
| `runtime.ctx_size` | int | `--ctx-size` / `-c` | Omit when absent; do not invent a default |
| `runtime.gpu_layers` | string | `--n-gpu-layers` / `--gpu-layers` / `-ngl` | `all`, `auto`, or a number |
| `runtime.cache_type_k` | string | `--cache-type-k` / `-ctk` | Omit when absent |
| `runtime.cache_type_v` | string | `--cache-type-v` / `-ctv` | Omit when absent |
| `runtime.parallel` | int | `--parallel` / `-np` | Omit when absent |
| `runtime.batch_size` | int | `--batch-size` / `-b` | Omit when absent |
| `runtime.ubatch_size` | int | `--ubatch-size` / `-ub` | Omit when absent |
| `runtime.fit` | string | `--fit` | `on`, `off`, or the configured value |
| `runtime.fit_target` | int | `--fit-target` | Omit when absent |
| `runtime.file_exists` | bool | `stat(model_path)` | `false` when the path cannot be resolved or stat fails |
| `runtime.file_size` | int64 | `stat(model_path)` | Omit when `file_exists` is false |
| `runtime.file_mtime` | string | `stat(model_path)` | RFC3339 UTC; omit when `file_exists` is false |

Rules:

- Support both `--flag value` and `--flag=value`.
- Support the short flags listed above, normalized to the long field name.
- If a flag appears more than once, use the last occurrence (CLI behaviour).
- Omit fields rather than returning zero values; the panel must be able to
  distinguish "not configured" from "set to 0".
- `gpu_layers` stays a string because `all` and `auto` are valid values.
- `file_mtime` uses UTC and RFC3339 with a `Z` suffix.
- `runtime` is always present for local models. For `proxy:` models it is
  `{"source": "proxy"}` unless something else is genuinely known.

## Security

- Never return the raw `cmd`. Only the allowlisted fields above.
- Never return `--api-key`, `--hf-token`, `env`, proxy credentials, macros,
  or any other token from the command line.
- The new endpoints are management-facing. Keep them behind the existing API
  key authentication. Do not add local paths or file sizes to `/v1/models`.
- Do not spawn processes, load models, or open model weights to answer the
  request.

## Edge cases

- **Never started locally, has `cmd`**: full `runtime` is available from the
  config. This is the main case the panel needs.
- **`proxy:` model**: return `{"source": "proxy"}` only. Do not invent a path
  or resource flags.
- **Wrapper command** (`cmd` starts a wrapper that chooses the model later):
  return the fields that can be parsed, set `source: "cmd"`, and omit the
  rest. Never guess.
- **Relative `--model` path**: resolve it the same way the process launcher
  does. If resolution fails, return the configured string, `file_exists:
  false`, and omit size/mtime.
- **Path is a network share or temporarily unavailable**: `stat` may be slow
  or fail. A failed stat must not fail the endpoint; return `file_exists:
  false` and omit size/mtime.
- **Model is `unlisted: true`**: the management endpoint should still list
  it; `unlisted` only affects the OpenAI-compatible listing and UI.
- **Config reload**: values must reflect the current config, not a previous
  process. Re-parse `cfg.Models[id].Cmd`; do not cache across reloads.
- **Alias requested**: `GET /api/models/{id}` resolves aliases and returns
  the real model ID.

## Caching

No runtime-state cache is required. Parsing one configured command is cheap.

Optional, only if measurements show a problem: cache `stat` results for a
short TTL (5–10 seconds) keyed by resolved path, and clear the cache on
config reload. Do not cache process state as a substitute for parsing the
configured command.

## Out of scope

- GGUF header facts (`architecture`, `parameter_count`, `quantization`) and
  `sha256`. These are not needed to unblock loading. A later change can add:
  - `GET /api/models/{id}/gguf` for header fields, and
  - `GET /api/models/{id}/artifact?hash=1` for the SHA-256.
  Hashing a multi-GiB file must be on demand, never in the model listing.
- VRAM estimation. llama-swap is a router, not a memory estimator. The panel
  computes or asks the operator for the budget.
- Changes to `/v1/models`, `/running` or `/api/events`.

## Tests

Add tests under `internal/server` following the existing naming convention:

- `TestAPI_Models_RuntimeFromConfigForUnloadedModel`: configured but never
  started; assert path, ctx, GPU layers, KV types, parallel and file size.
- `TestAPI_Models_RuntimeEqualsRunningModel`: loaded and unloaded entries
  report the same configured values.
- `TestAPI_Models_RuntimeShortFlags`: `-m`, `-c`, `-ngl`, `-np`, `-b`, `-ub`,
  `-ctk`, `-ctv`.
- `TestAPI_Models_RuntimeProxyModel`: `source: proxy`, no invented fields.
- `TestAPI_Models_RuntimeHidesSecrets`: a command containing `--api-key` and
  an env token never appears in the response.
- `TestAPI_Models_RuntimeMissingFile`: `file_exists: false`, no size or
  mtime, request still succeeds.
- `TestAPI_Models_RuntimeFlagEqualsForm`: `--ctx-size=32768` and
  `--model=...`.
- `TestAPI_ModelByID_ResolvesAlias` and `TestAPI_ModelByID_NotFound`.

Run `go test -v -run 'TestAPI_Models|TestAPI_ModelByID'` while developing,
and `make test-dev` before finishing.

## Acceptance criteria

1. A model that has never been started still reports its configured path,
   context, GPU layers, KV types, parallel and file size.
2. No model is started and no weights are read to satisfy the request.
3. A `proxy:` model reports `source: proxy` and no fabricated fields.
4. The raw command and any credentials never appear in the response.
5. `/v1/models` is byte-for-byte unchanged for existing consumers.
6. The management panel can detect a changed path or resource flag while the
   model is unloaded, before admitting a load.

## Panel-side follow-up (separate repo)

Agreed 2026-11-02:

1. Unknown model handling
   - The panel automatically creates a `provisional` dossier from
     `GET /api/models/{id}`.
   - A provisional model can be loaded when the operator sets a machine
     `minimum_free_mib` or supplies a one-off conservative estimate. GPU
     telemetry and the VRAM gate still apply.
   - If the endpoint returns no useful information (for example a `proxy:`
     model), the panel opens a dialog asking for the required fields. If the
     operator provides neither values nor a VRAM basis, loading is not
     supported.
2. Dossier simplification
   - Auto-fill `artifact_path`, `context_length`, `gpu_layers`,
     `cache_type_k`, `cache_type_v`, `parallel`, `batch_size`,
     `ubatch_size`, `fit` and `file_size` from `runtime`.
   - Require only a display name and a VRAM budget (or the machine's minimum
     free value) for first registration.
3. Artifact identity
   - `artifact_sha256` is mandatory only for replace, rename and import.
   - First registration and first load do not require a hash; the panel uses
     the configured path plus `file_size`/`file_mtime` for drift detection.
4. Drift detection
   - Compare `runtime` with the dossier while the model is unloaded, so a
     changed path or flag is reported before the load instead of after it.
5. Experiments and prototypes live in a new `exploration` area; they must not
   import experimental code from `modules`.
