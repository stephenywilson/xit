# XiT Backend

A tiny, privacy-first backend for XiT, designed to run on **Cloudflare Workers + D1**.
It serves two things:

1. **Version check** — `GET /v1/version`, so the CLI / VS Code extension can suggest upgrades.
2. **Anonymous usage metrics** — `POST /v1/metrics`, validated and sanitized before storage.

> Nothing here is deployed automatically. The commands below are for you to run.

## Endpoints

| Method | Path          | Purpose                                            |
|--------|---------------|----------------------------------------------------|
| GET    | `/v1/health`  | Liveness probe (`{"status":"ok"}`)                 |
| GET    | `/v1/version` | Latest/min versions + severity + upgrade commands  |
| POST   | `/v1/metrics` | Ingest one anonymous metrics event (`xit.metrics.v1`) |
| GET    | `/v1/stats`   | **Aggregate-only** dashboard roll-ups (no per-user detail) |

### `GET /v1/stats`

Returns aggregate roll-ups for an internal dashboard: `total_runs`,
`total_saved_tokens`, `success_rate` / `error_rate`, `by_adapter`,
`by_version`, and a 30-day `daily_trend`. Every query is a `GROUP BY` / `SUM` /
`COUNT` (see `src/stats.js`); none selects `anonymous_install_id` or any
per-channel/run/turn id, so the dashboard can only ever show aggregates — never
a single user's or install's detail. Without a `METRICS_DB` binding it returns
a zeroed-but-valid shape.

### `GET /v1/version`

```json
{
  "latest_cli": "0.2.49",
  "min_cli": "0.2.48",
  "latest_vscode": "0.0.35",
  "min_vscode": "0.0.34",
  "severity": "info | recommended | required | blocked",
  "message": "Please upgrade XiT.",
  "npm_command": "npm install -g xitsg@latest",
  "vscode_marketplace_url": "https://marketplace.visualstudio.com/items?itemName=XiT.xit-vscode"
}
```

### `POST /v1/metrics`

Accepts a single `xit.metrics.v1` event. The server:

- enforces a **4 KiB body-size limit** (a metrics event is tiny);
- requires `content-type: application/json`;
- **rejects** any event that carries a forbidden field (see below) with `400`;
- validates the closed enums (`adapter`, `surface`, `status`, `error_kind`);
- stores **only** the allow-listed, sanitized fields — never the raw request body.

Responses: `202 accepted`, `400` (validation), `413` (too large), `415` (wrong content-type), `429` (rate limited).

## Privacy / security design

- **No IP in the business table.** Request IPs are never written to `metrics_events`.
  An optional KV-based rate limiter keys on `anonymous_install_id`, not IP, and is never joined to metrics.
- **No raw body storage.** Only the sanitized allow-list from `validateMetrics()` is persisted.
- **Schema validation** rejects unknown/forbidden shapes; extra fields are stripped, not stored.
- **Body size limit**: `MAX_BODY_BYTES = 4 KiB`.
- **CORS**: minimal. The metrics POST is called by the CLI (no browser origin) and needs no
  credentials, so no extra origins are allowed. Add a narrow `Access-Control-Allow-Origin`
  only if you later build a browser dashboard against a separate read API.
- **Rate limit** (optional): bind a `RATE_LIMIT_KV` namespace to cap writes per install id
  per minute (default 120/min). Fail-open if KV errors.

### Forbidden fields (rejected on ingest)

```
raw_output, raw_log, prompt, ai_reply, command, cwd, path, file_name,
repo_name, username, email, api_key, token, full_session_id,
full_host_instance_hash, full_workspace_hash,
channel_id, run_id, turn_id, event_id
```

## Local development

```bash
cd server
npm install
npm test          # runs validation unit tests (node --test, no build step)
npm run dev       # wrangler dev (local Worker)
```

Without a `METRICS_DB` binding, ingest still validates and returns `202` without storing —
handy for local testing.

## Deploy (manual)

```bash
cd server

# 1. Create the D1 database and apply the schema.
wrangler d1 create xit-metrics
#   -> copy the database_id into wrangler.toml
wrangler d1 execute xit-metrics --file=./schema.sql

# 2. (optional) Create a KV namespace for rate limiting, then uncomment the
#    [[kv_namespaces]] block in wrangler.toml.
wrangler kv namespace create RATE_LIMIT_KV

# 3. Deploy.
wrangler deploy
```

After deploy, point clients at the Worker URL:

```bash
export XIT_API_BASE="https://xit-api.<your-account>.workers.dev"
```

### Default API base for released builds (no manual config for users)

So normal users get version-check + anonymous metrics **without** setting any
env var, release builds ship a baked-in default API base. It is resolved as:

```
xit.apiBase (VS Code setting)  >  XIT_API_BASE (env)  >  built-in default
```

- CLI default: `internal/apibase/apibase.Default` (a `var`, empty in source).
  Inject the production domain at **release build** time, without hardcoding a
  fake domain into the tree:

  ```bash
  go build -ldflags "-X github.com/stephenywilson/xit/internal/apibase.Default=https://xit-api.<domain>" ./cmd/xit
  ```

- VS Code default: `DEFAULT_API_BASE` in `extensions/vscode/src/extension.ts`
  (empty in source; set to the same URL before packaging the `.vsix`).

If **both** the default and the override are empty, XiT performs **no** version
check and sends **no** metrics (silent no-op, fail-open) — so an un-provisioned
build never phones home. A real production domain MUST be chosen before release
(see the release report's `NEED_USER_CONFIRMATION`).

### Cloudflare values you must provide before deploy

| Value                 | Where it goes                          | How to get it |
|-----------------------|----------------------------------------|---------------|
| `account_id`          | `wrangler.toml` (or `CLOUDFLARE_ACCOUNT_ID`) | Cloudflare dashboard → account home |
| D1 `database_id`      | `wrangler.toml` `[[d1_databases]]`     | `wrangler d1 create xit-metrics` |
| worker `name`         | `wrangler.toml` `name` (default `xit-api`) | your choice |
| API domain / route    | the `XIT_API_BASE` / built-in default above | `*.workers.dev` URL, or a custom route via `[[routes]]` + a Cloudflare-managed zone |
| KV namespace id (opt) | `wrangler.toml` `[[kv_namespaces]]`    | `wrangler kv namespace create RATE_LIMIT_KV` |

## Other backends

The storage layer is isolated in `storeEvent()` (D1 today). To target Postgres /
Supabase instead, replace `storeEvent` with an insert against your driver and
keep `validate.js` unchanged — it is the privacy firewall and should not move.

## Aggregation example

```sql
SELECT substr(ts, 1, 10) AS day, adapter,
       COUNT(*)                    AS runs,
       SUM(estimated_saved_tokens) AS saved_tokens
FROM metrics_events
GROUP BY day, adapter
ORDER BY day DESC, saved_tokens DESC;
```
