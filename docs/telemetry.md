# XiT — Version Check & Anonymous Telemetry

XiT has two small network features, both **fail-open** and **privacy-first**:

1. **Version check** — so XiT can tell you when a newer version is available, and
   (only in severe cases) refuse to drive high-risk paths on a very old build.
2. **Anonymous usage metrics** — so the project can understand how XiT is used
   (which adapters, how much output is compressed, how many tokens are saved).

Both features use the production XiT API by default
(`https://xit-api.stephenwilson.dev`). You can override it with `XIT_API_BASE`,
or with the `xit.apiBase` VS Code setting. With no endpoint in a dev build,
XiT performs **no** version check and sends **no** metrics.

---

## Anonymous usage metrics

### Default & how to disable

Anonymous metrics are **enabled by default**, transparent, and easy to disable:

```bash
xit telemetry status      # show current state, install id, and what is/ isn't sent
xit telemetry off         # disable
xit telemetry on          # enable
```

Environment variable (highest priority):

```bash
XIT_TELEMETRY=off    # or: on
```

XiT also honors the `DO_NOT_TRACK=1` convention (treated as off).

In **VS Code**, telemetry additionally follows the editor's global
`telemetry.telemetryLevel`. If that is `off`, the XiT extension sends nothing —
regardless of the `xit.telemetry` setting.

Resolution order (first match wins):

1. VS Code global telemetry off (extension only) → **off**
2. `XIT_TELEMETRY` env (`off`/`on`)
3. `DO_NOT_TRACK=1` → **off**
4. local state file `~/.xit/telemetry.json` (`xit telemetry on|off`)
5. default → **on**

### What is collected

A single event, schema `xit.metrics.v1`:

```json
{
  "schema": "xit.metrics.v1",
  "event": "run.finished",
  "anonymous_install_id": "random-local-id",
  "timestamp": "2026-06-29T00:00:00Z",
  "cli_version": "0.2.51",
  "vscode_extension_version": "0.0.36",
  "adapter": "codex | claude | kimi | opencode | cursor | vscode | unknown",
  "surface": "cli | hook | vscode | bridge | codex_cli | codex_ide | chatgpt_desktop_codex | codex_shared",
  "os": "darwin | linux | windows",
  "arch": "arm64 | amd64",
  "input_bytes": 123456,
  "summary_bytes": 1234,
  "saved_bytes": 122222,
  "estimated_saved_tokens": 30555,
  "compression_ratio": 0.92,
  "run_count": 1,
  "status": "success | error",
  "error_kind": "none | timeout | command_failed | parse_failed | unknown"
}
```

`anonymous_install_id` is a random local id (`~/.xit/telemetry.json`). It is not
derived from your username, email, hostname, or any path.

### What is NEVER collected

The schema has no field for, and XiT never sends:

```
raw_output, raw_log, prompt, ai_reply, command, cwd, path, file_name,
repo_name, username, email, api_key, token, full_session_id,
full_host_instance_hash, full_workspace_hash, channel_id, run_id, turn_id,
event_id, full_channel_id, full_run_id, full_turn_id
```

This is enforced structurally (the event struct cannot carry those fields) and
asserted by tests in both the CLI (`internal/telemetry`) and the backend
(`server/src/validate.js`), which **rejects** any event containing a forbidden
key.

### Delivery

- Asynchronous, with a **1s** timeout per attempt.
- Never retried aggressively; failure never affects `xit auto`.
- On failure, events spool to a local queue capped at **100** entries
  (`~/.xit/telemetry-queue.jsonl`) and are flushed on the next success.

---

## Version check

The CLI / extension fetch `GET {XIT_API_BASE}/v1/version`:

```json
{
  "latest_cli": "0.2.51",
  "min_cli": "0.2.50",
  "latest_vscode": "0.0.36",
  "min_vscode": "0.0.35",
  "severity": "info | recommended | required | blocked",
  "message": "Please upgrade XiT.",
  "npm_command": "npm install -g xitsg@latest",
  "vscode_marketplace_url": "https://marketplace.visualstudio.com/items?itemName=XiT.xit-vscode"
}
```

### Behavior

- The result is cached locally for **24h** (`~/.xit/version-check.json`).
- Network failure is **fail-open**: XiT keeps working with no nag and no block.
- `xit upgrade` shows the latest version and the **exact** commands to run. XiT
  never runs `npm install` / `vsce` for you — it only prints them.
- `xit doctor` shows the current/latest version and telemetry status.

### Severity ladder

| Severity      | Effect |
|---------------|--------|
| `info`        | No action; a newer version merely exists. |
| `recommended` | Suggest upgrading. |
| `required`    | Strongly urge upgrade; high-risk paths (hooks / VS Code bridge) may refuse. |
| `blocked`     | Strictly below `min_cli` / `min_vscode` (current < minimum); also blocks the core path (`xit auto`). |

`blocked` means `current < minimum` and nothing else — **equality is never
"below"**. When running below the declared minimum, XiT escalates locally to
`blocked` even if the server was conservative. Conversely, when
`current >= min_cli` (including exact equality), a server-declared
`severity: "blocked"` is downgraded to `required` — a stale or overly broad
server flag can never lock out a version that already satisfies the minimum.
(This is the fix shipped in 0.2.51 / 0.0.36; see
`docs/releases/RELEASE_NOTES_V0.2.51.md`.)

### What is never blocked

Regardless of severity, these always work so you can recover:

```bash
xit --version
xit doctor
xit upgrade
xit telemetry on | off | status
```

`xit auto` itself is **never** blocked — your command always runs and compresses.
Only XiT's own high-risk machinery (the VS Code bridge) is disabled on a
blocked/required version.

> npm distributors may additionally deprecate very old versions with, e.g.,
> `npm deprecate xitsg@"<0.2.48" "Please upgrade XiT"`. XiT never issues npm
> commands itself.

---

## Backend

The reference backend is a Cloudflare Worker (`server/`) exposing `/v1/version`,
`/v1/metrics`, and `/v1/health`, backed by D1. It validates and sanitizes every
event, stores only anonymous aggregate-friendly fields, keeps no IP in the
business table, and stores no raw request body. See [../server/README.md](../server/README.md).
