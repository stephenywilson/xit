<div align="center">

# XiT / 吸T神功

**Stop dumping 30k+ bytes of logs into your AI agent.**

吸走废 Token，留下有效上下文。

[![npm](https://img.shields.io/npm/v/xitsg?label=npm%3A%20xitsg&color=56f5a3)](https://www.npmjs.com/package/xitsg)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux%20%7C%20Windows-lightgrey)](https://www.npmjs.com/package/xitsg)
[![No telemetry](https://img.shields.io/badge/telemetry-none-56f5a3)](#safety--privacy)

</div>

```bash
npm i -g xitsg
xit auto go test -v ./...
```

> The npm package is `xitsg` because `xit` is taken on npm. The installed command is `xit`.

<div align="center">

![XiT Hero](docs/assets/xit-hero.svg)

</div>

---

## Real dogfood metrics

| Metric | Result |
|--------|-------:|
| Lifetime output reduction | **91.8%** |
| Current-session reduction | **98.7%** |
| Estimated tokens saved (lifetime) | **~359k Token** |
| Latest `go test -v ./...` turn saving | **~9k Token** |
| Commands compressed (lifetime) | **120** |

> Metrics from local XiT dogfood runs on this repository.  
> Token savings are estimated: `saved_tokens = saved_bytes / 4`. Not a tokenizer guarantee.

<div align="center">

![XiT Metrics](docs/assets/metrics.svg)

</div>

---

## Why this matters

AI coding agents have a limited context window. When your agent runs `go test`, `git diff`, `grep -r`, or `docker logs`, the raw output floods the context with noise — repeated log lines, progress bars, irrelevant pass/fail details.

**The AI reads thousands of tokens of junk. You pay for it. Reviews get slower.**

But you can't just throw the output away — raw evidence still matters for debugging.

XiT compresses command output **locally** into a compact summary and saves the full raw output to `.xit/runs/` for audit. The AI gets signal. You keep the evidence.

---

## Before / After

<div align="center">

![Before / After](docs/assets/before-after.svg)

</div>

**Before:**

```bash
go test -v ./...
# → 35,629 bytes of verbose output enters your AI context
# → ~8,907 tokens consumed (saved_bytes / 4 est.)
```

**After:**

```bash
xit auto go test -v ./...
```

```
XiT Auto Summary
command:    go test -v ./...
exit_code:  0
reduction:  99%
saved:      ~8,907 tokens (saved_bytes / 4 est.)
raw_log:    .xit/runs/20260530-go-test.raw.log  ← local only

Key facts:
- All tests passed · no panic
- 19 packages · 0 failures
- Full raw output preserved locally
```

---

## How it works

<div align="center">

![Workflow](docs/assets/workflow.svg)

</div>

1. **Run** — `xit auto <command>` executes your command unchanged
2. **Capture** — records stdout, stderr, exit code, duration
3. **Save** — full raw output written to `.xit/runs/<timestamp>.raw.log`
4. **Filter** — selects the right compressor for the command type
5. **Output** — prints compact summary, preserves exit code
6. **Track** — appends to `.xit/history.jsonl`, run `xit gain` for stats

---

## Benchmark and hit rate

```bash
xit gain                       # Lifetime compression stats
xit kimi session               # Current session breakdown
xit kimi hitrate --last 10m    # Routing accuracy
xit bench compression          # Filter quality benchmark
```

Sample output from this repository:

```
Lifetime reduction:  91.8%
Session reduction:   98.7%
Commands:            120
Saved tokens:        ~359k  (saved_bytes / 4 est.)
Latest turn:         ~9k tokens saved (go test -v ./...)
```

> Results vary by command type and workflow. Local dogfood sample only.

---

## Current AI CLI support

| AI CLI | Status | Notes |
|--------|--------|-------|
| Kimi CLI | **Functional prototype** | rules, hooks, turn lifecycle, optional toolbar |
| Claude Code | In progress | hook experiments and validation ongoing |
| Codex | Planned | future adapter |
| Cursor | Planned | future adapter |

XiT is currently most useful with **Kimi CLI**. Claude Code, Codex, and Cursor adapters are being developed.

---

## Install

```bash
npm i -g xitsg
```

```bash
xit --version
# xit version 0.2.40
```

Platforms: macOS (arm64 + x64) · Linux (x64 + arm64) · Windows (x64)

Pre-compiled Go binaries are bundled — no compilation needed at install time.

---

## Quick start

```bash
xit auto go test -v ./...    # Compress go test output
xit auto git diff            # Compress git diff
xit auto grep -r "TODO" .    # Compress grep output
xit auto npm test            # Compress npm test
xit auto docker logs <name>  # Compress docker logs

xit gain                     # View lifetime savings
xit doctor                   # Check environment
```

---

## Command coverage

| Command type | Compression strategy |
|---|---|
| `go test` | exit code, pass/fail stats, failure highlights |
| `git diff` | changed files, risky paths, compact hunk summary |
| `git log` | one line per commit, total count |
| `git status` | branch, staged/unstaged counts, key files |
| `grep` / `rg` | grouped by file, capped examples |
| `npm test` / `pytest` / `cargo test` | pass/fail summary, top failures |
| `tsc` / `eslint` | errors grouped by file |
| `docker logs` | dedup repeated lines, surface errors |
| `find` / `ls` | directory aggregation, skips noisy folders |

---

## Kimi CLI integration

<div align="center">

![Kimi Toolbar](docs/assets/kimi-toolbar.svg)

</div>

### Step 1 — Install XiT rules

Teaches Kimi to proactively run `xit auto` for high-output commands:

```bash
xit init kimi --method official_hook --scope user --yes
xit kimi rules install --scope user --yes
```

Restart Kimi. It will now prefer `xit auto go test -v ./...` over raw `go test -v ./...`.

### Step 2 — Verify

```bash
xit kimi rules status --scope user
xit doctor kimi --deep
xit kimi benchmark
```

### Step 3 — Optional toolbar

Shows XiT status in Kimi's bottom bar:

```
吸T神功 · 准备就绪
吸T神功 · 正在吸T中
本次吸T 1次 · 省 ~9k Token
```

```bash
xit kimi status-patch install --yes --accept-risk
```

> ⚠️ The toolbar patch is **opt-in** and experimental. It modifies your local Kimi Python package.
> Roll back at any time: `xit kimi status-patch uninstall --yes`

Full Kimi docs → [docs/kimi.md](docs/kimi.md)

---

## Safety & privacy

- **No telemetry** — nothing is sent anywhere
- **No cloud upload** — all processing is local
- **raw logs stay local** — `.xit/runs/<timestamp>.raw.log`
- **history stays local** — `.xit/history.jsonl`
- **status patch is opt-in** — requires `--yes --accept-risk`, rollback supported
- **fail-open** — if XiT errors, original command output is preserved
- **`saved_tokens = saved_bytes / 4`** — local estimate, not a tokenizer guarantee

→ [docs/privacy.md](docs/privacy.md)

---

## npm package

```
Package:   xitsg
Version:   0.2.40
Install:   npm i -g xitsg
Command:   xit
```

The name `xitsg` is used because `xit` is already taken on npm. The installed CLI command is still `xit`.

---

## Build from source

```bash
git clone https://github.com/stephenywilson/xit
cd xit
go build -o xit ./cmd/xit/main.go
mkdir -p ~/.local/bin && cp xit ~/.local/bin/xit
xit --version
```

Requirements: Go 1.21+

---

## Roadmap

- [x] `xit auto` compression — go test, git diff, grep, npm test, docker logs
- [x] raw\_log local evidence trail
- [x] Kimi CLI — rules mode, hook observe, turn lifecycle, optional toolbar
- [x] Multi-platform npm binary (v0.2.40)
- [ ] Claude Code adapter hardening
- [ ] Codex adapter
- [ ] Cursor adapter
- [ ] Optional real tokenizer support

---

## License

MIT — see [LICENSE](LICENSE)
