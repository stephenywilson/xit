# XiT 0.2.51

A version-gate bug fix (two rounds — see below), ChatGPT Desktop App (Codex
mode) support with fully automatic high-output command routing, a Dashboard
"By Surface" breakdown, and a Dashboard cutover fix. Supersedes 0.2.50.

## Fixed: version-gate self-block (two related bugs)

### Round 1 — `current == min_cli`

Production briefly served `min_cli=0.2.50`, `latest_cli=0.2.50`,
`severity=blocked` — correct config, since 0.2.50 *is* the minimum. But
`internal/updatecheck.evaluate()` trusted the server's `severity` field
verbatim whenever it wasn't escalating locally, so a v0.2.50 install (exactly
at the minimum) was refused for `xit auto` even though it fully satisfied
`min_cli`.

**Root cause**: `evaluate()` only ever escalated severity *up* toward
`blocked` when `current < min_cli`; it never had a path to escalate *down*
away from a server-declared `blocked` when `current >= min_cli`. `blocked` is
supposed to mean "current is strictly below the minimum" — nothing else.

**Fix** (`internal/updatecheck/updatecheck.go`, `evaluate()`):
- `current < min_cli` → escalate to `blocked` (unchanged).
- `current >= min_cli` (including exact equality) and the server said
  `blocked` → **downgrade to `required`**. A stale or overly-conservative
  server flag can never lock out a version that already satisfies the
  minimum. `required` still surfaces the upgrade message and may refuse
  high-risk paths (hooks / VS Code bridge), but never the core `xit auto`
  path.
- Removed the now-dead `rank()` helper.

Same equality bug, same fix, on the VS Code side: `extensions/vscode/src/telemetry.ts`
gained `evaluateVersionGate(current, minimum, serverSeverity)`, and
`extension.ts`'s `maybeCheckVersion` now uses it instead of trusting
`info.severity` directly against `min_vscode`.

**Tests**: `internal/updatecheck/updatecheck_test.go` — `TestVersionGateMatrix`
(the full CLI current/min/latest matrix from the spec, including the exact
production scenario `0.2.50/0.2.50/0.2.50/severity=blocked`),
`TestEqualToMinimumIsNeverBlocked`, `TestSemverOrderingNotLexicographic`
(`0.10.0 > 0.9.9`), `TestMalformedVersionIsSafe`,
`TestExplicitUpdateCheckRefreshesPastStaleBlockedCache`. VS Code side:
`extensions/vscode/src/test/telemetry.test.ts` — five new `evaluateVersionGate`
cases mirroring the same matrix.

### Round 2 — `current >= latest_cli` still showed `required` and disabled the bridge

Round 1 fixed the `min_cli` equality case, but a CLI already at or ahead of
`latest_cli` (e.g. a fresh local build newer than what the server currently
advertises) could still show `severity: required` if the server kept
declaring it, and `xit auto` would print "this version is out of date;
VS Code bridge disabled" — even though there was nothing to upgrade to.

**Fix**: `evaluate()` gained a dedicated case — `!r.UpgradeNeeded` (i.e.
`current >= latest_cli`) now forces `severity = info` unconditionally,
checked *before* any server-severity pass-through. Also, `ShouldBlockHighRisk()`
was narrowed to only trigger on `severity == blocked` (previously it also
matched `required`, which is what let a stale `required` disable the VS Code
bridge for an otherwise up-to-date install). Only `current < min_cli` can
disable anything now — every other tier is purely advisory.

Mirrored on the VS Code side: `evaluateVersionGate()` gained a `latest`
parameter and the same current>=latest rule; `extension.ts`'s
`maybeCheckVersion` now delegates entirely to it instead of a separate
`compareVersions(...) >= 0` early-return.

**Tests**: `TestCurrentAheadOfLatestIsNeverRequiredOrBlocked`,
`TestFullVersionGateMatrixV2` (both severity and bridge-block gates, full
matrix). VS Code: `evaluateVersionGate` cases for `0.0.36 == min == latest`
and `0.0.37 > latest 0.0.36`.

## Added: ChatGPT Desktop App support (Codex mode only)

OpenAI's ChatGPT desktop app now bundles what was previously the standalone
Codex app (confirmed on-machine: `ChatGPT.app`'s `Info.plist` has
`CFBundleIdentifier=com.openai.codex`, `CFBundleAlternateNames=["Codex"]`;
its embedded Codex agent runs `Contents/Resources/codex ... app-server` as a
direct child of `Contents/MacOS/ChatGPT`).

XiT does **not** claim to support ChatGPT's general Chat or Work modes — only
**Codex mode inside the ChatGPT desktop app**, via the exact same shared Codex
hook Codex CLI and the Codex VS Code extension already use. `adapter` stays
`"codex"` for all three front-ends; only `surface` now distinguishes them:

- `codex_cli` — a plain terminal invocation.
- `codex_ide` — the Codex/"OpenAI ChatGPT" VS Code extension (same `VSCODE_PID`
  ambient signal XiT's VS Code Bridge already relied on).
- `chatgpt_desktop_codex` — ChatGPT Desktop's embedded Codex agent, detected
  via the ambient `__CFBundleIdentifier=com.openai.codex` macOS sets for any
  process descended from that app bundle (confirmed on-machine against a real
  terminal's own bundle id, e.g. `com.apple.Terminal`/`com.microsoft.VSCode`,
  for contrast).
- `codex_shared` — safe fallback when Codex is confirmed but the front-end
  can't be reliably distinguished. Never guessed.

Detection (`internal/codexhook/surface.go`, `DetectSurface()`) uses **only**
ambient environment signals already inherited down the process tree — never
reads prompt text, tool output, file contents, or injects into any process.
`internal/codexhook/chatgpt.go`'s `DetectChatGPTApp()` is a read-only
`plutil`-based check of `/Applications/ChatGPT.app/Contents/Info.plist` —
never launches the app, modifies it, or touches its signature or user data.

### New commands

```
xit hook install chatgpt --scope project --yes   Install the Codex hook shared with ChatGPT Desktop
xit hook status chatgpt                          Show shared Codex hook status (ChatGPT Desktop framing)
xit hook uninstall chatgpt --yes                 Uninstall the shared Codex hook (affects CLI/IDE/Desktop)
xit chatgpt status                               Detect ChatGPT Desktop app + shared Codex hook status
xit chatgpt diagnose                             Deep read-only diagnostics
```

`chatgpt` is an alias over the *same* canonical Codex hook — it never
registers a second hook. Installing via `codex` first, then checking
`xit hook status chatgpt`, reports the identical `.codex/hooks.json`; running
install again (from either name) is idempotent (`AlreadyInstalled: true`,
zero bytes changed). Uninstalling explicitly warns that it removes the one
hook shared by Codex CLI, Codex IDE, and ChatGPT Desktop's Codex mode
together — not just one of them.

**Tests**: `internal/codexhook/surface_test.go` (signal-priority matrix),
`internal/codexhook/chatgpt_test.go` (app detection against fixture bundles,
and `TestChatGPTSharesCanonicalCodexHook` proving no duplicate hook is ever
created).

## Added: fully automatic high-output command routing (no manual Skill needed)

The canonical Codex hook already rewrites a Bash command classified
high-output (`internal/codexhook/rewrite.go`, `RewriteCommandForTurn`) via
Codex's documented `PreToolUse` `hookSpecificOutput.updatedInput.command`
response — **before** Codex ever executes it. This means once the hook is
installed and trusted for a project, high-output commands are routed through
`xit auto` automatically; the user never needs to click the plugin, type
`@XiT`, or ask for XiT by name. This release makes that behavior discoverable
and turnkey rather than an undocumented side effect:

- **`xit chatgpt setup --auto`** — one-shot, non-interactive setup for the
  current project: detects the ChatGPT app / Codex CLI / local plugin,
  **refuses to proceed** (rather than silently double-firing) if XiT's hook
  is already registered in both the project-level and the real Codex
  **user-level** hook layer (`~/.codex/hooks.json` — confirmed via OpenAI's
  hooks docs to be a real, separate layer Codex loads *in addition to*
  project hooks), backs up any existing `hooks.json` with a timestamp before
  touching it, installs/repairs the hook, and re-verifies before reporting
  success.
- **`xit chatgpt status`** rewritten for a non-technical reader: reports
  `Automatic mode`, `Hook provider` (`shared_codex_user_hook`), `Hook
  trusted` (a new read-only check of `~/.codex/config.toml`'s
  `hooks.state` trust records), `Duplicate hook`, plugin install state, and
  telemetry state, in plain language.
- **Visible per-command feedback**: Codex's tool-call output for a
  compressed command now ends with one extra line —
  `XiT · auto · 48.2 KB → 4.7 KB · saved ~10.9k tokens · exit 0` — so the
  user (and the model) can see, per command, that XiT actually ran. Carries
  only aggregate byte/token counts and the exit code; never command text, a
  path, a repo name, a prompt, a reply, or an install id.
- **Coverage additions**: `yarn test`, `kubectl logs`, and `ruff` now
  classify as high-output (`internal/filters/filters.go`) alongside the
  existing `go test`/`npm test`/`docker logs`/etc. — closing a gap in the
  command list XiT already covered.
- **The plugin (`plugins/xit/`, local personal marketplace) stays Skill-only** —
  OpenAI's Codex hooks docs confirm hooks from multiple layers (plugin,
  project, user) fire concurrently rather than replacing each other, so a
  plugin-level hook would double-process every command in any project that
  already has the canonical hook installed, with no documented way to detect
  and skip. The Skill (`plugins/xit/skills/xit/SKILL.md`) now documents
  automatic mode as the primary behavior and manual `xit auto` wrapping as a
  fallback only for when automatic mode isn't active, with an explicit
  recursion guard (never wrap a command that already starts with `xit`). See
  `docs/codex-plugin-submission-checklist.md` for the full reasoning and what
  a future hook-enabled stage would require.

**Tests**: `internal/codexhook/duplicate_test.go` (cross-layer duplicate
detection), `internal/codexhook/trust_test.go` (trust-state parsing),
`TestChatGPTSetupAutoInstallsAndIsIdempotent`,
`TestChatGPTSetupAutoRefusesWhenDuplicateExists`,
`TestFormatCodexAutoStatusLine` (+ privacy/forbidden-substring assertions),
`TestRewriteCommandForTurnVersionCommandNeverRecurses`,
`TestRewriteCommandForTurnReroutesNewHighNoiseCommands`.

## Fixed: Dashboard cutover (`METRICS_PUBLIC_START_AT`)

Public dashboard/stats now exclude pre-cutover internal test data:
`resolvePublicStart`/`laterCutoff` in `server/src/dashboard.js` compute the
effective `ts` filter as the later of the range window and the configured
cutover; a misconfigured `METRICS_PUBLIC_START_AT` fails **closed** (HTTP 500
via `/api/dashboard` and `/v1/stats`) rather than silently leaking
pre-cutover data. A new `/api/dashboard/coverage` endpoint reports aggregate
first/last event timestamps and pre/post-cutover event counts for operator
diagnostics — never a per-event or per-install identifier. npm download
external stats are explicitly unaffected by the cutover. 15 dedicated tests
in `server/test/cutover.test.js`.

## Added: Dashboard "By Surface"

`GET /api/dashboard` gained `by_surface` and `surface_daily_trend`, aggregated
the same way as the existing `by_adapter`/`adapter_daily_trend` (`server/src/dashboard.js`).
The Dashboard page has a new "By Surface" table and a "Runs by surface over
time" chart. Both still respect `METRICS_PUBLIC_START_AT` — pre-cutover
internal test data is excluded exactly like every other roll-up.

Backend validation (`server/src/validate.js`) and the CLI/VS Code telemetry
clients (`internal/telemetry`, `extensions/vscode/src/telemetry.ts`) all
extend `ALLOWED_SURFACES` with the four new values — additively, so older
clients that only ever send `cli`/`hook`/`vscode`/`bridge` keep working
unchanged.

## Versions in this release

- CLI / npm `xitsg`: **0.2.51**
- VS Code extension: **0.0.36** (bumped because `extension.ts`/`telemetry.ts`
  runtime behavior genuinely changed — the version-gate parity fix — not just
  for its own sake)
- Worker/API: no version bump (Workers aren't versioned this way) — the
  Dashboard cutover fix, `by_surface`, and the surface allow-list extension
  all deploy as part of the next Worker deployment, not a version number
  bump
- Codex Plugin (local personal marketplace only): **0.2.51**, matching the
  CLI version. Not submitted to OpenAI's public Plugin Directory this round.

## Privacy

No change to what's collected. The new surface values are still closed-enum,
still contain no prompt/reply/command/path/id data. The new per-command
`XiT · auto · ...` status line is aggregate-only (byte counts, an estimated
token figure, exit code) — see `docs/telemetry.md`.
