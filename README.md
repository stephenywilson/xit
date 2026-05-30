<div align="center">

# XiT / 吸T神功

**Stop wasting AI coding context on noisy terminal output.**

把高噪音命令输出压缩成 AI 可读摘要，同时保留本地 raw\_log 证据。

[![npm](https://img.shields.io/npm/v/xitsg?label=npm%3A%20xitsg&color=56f5a3)](https://www.npmjs.com/package/xitsg)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux%20%7C%20Windows-lightgrey)](https://www.npmjs.com/package/xitsg)
[![No telemetry](https://img.shields.io/badge/telemetry-none-56f5a3)](docs/privacy.md)

```bash
npm i -g xitsg
xit auto go test -v ./...
```

</div>

---

<div align="center">

![XiT Hero](docs/assets/xit-hero.svg)

</div>

---

## Why developers care

AI coding agents have a limited context window. When you run `go test`, `git diff`, `grep`, or `docker logs`, the terminal output floods the AI with noise — repeated log lines, progress bars, irrelevant test details.

**The AI reads thousands of tokens of junk. You pay for it. It slows down reviews.**

But raw evidence still matters. You can't just throw the output away.

XiT compresses command output **locally** into a compact summary, and saves the full raw output to `.xit/runs/` for audit. The AI gets signal. You keep the evidence.

---

## Before / After

<div align="center">

![Before / After](docs/assets/before-after.svg)

</div>

**Before:**

```bash
go test -v ./...
# → 36,000 bytes of verbose test output enters your AI context
# → ~9,000 tokens consumed
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
raw_log:    .xit/runs/20260530-go-test.raw.log

Key facts:
- All tests passed
- No panic / slice bounds error
- 19 packages checked
```

> The AI sees the compact summary. You keep the full raw log.  
> `saved_tokens = saved_bytes / 4` (local estimate, not a tokenizer guarantee)

---

## Install

```bash
npm i -g xitsg
```

> **Why `xitsg`?** The name `xit` is already taken on npm. After install, the command is still `xit`.

Verify:

```bash
xit --version
# xit version 0.2.40
```

---

## Quick start

```bash
# Compress go test output
xit auto go test -v ./...

# Compress git diff
xit auto git diff

# Compress grep
xit auto grep -r "TODO" --include="*.go" .

# Compress npm test
xit auto npm test

# Check environment
xit doctor

# View compression history
xit gain
```

---

## How it works

<div align="center">

![Workflow](docs/assets/workflow.svg)

</div>

1. **Run** — `xit auto <command>` executes your original command unchanged
2. **Capture** — records stdout, stderr, exit code, duration
3. **Save** — full raw output written to `.xit/runs/<timestamp>.raw.log`
4. **Filter** — selects the right filter for the command type, extracts key facts
5. **Output** — prints compact XiT Auto Summary, preserves exit code
6. **Track** — appends to `.xit/history.jsonl`, use `xit gain` for stats

---

## Current AI CLI support

| AI CLI      | Status               | Notes                                          |
|-------------|----------------------|------------------------------------------------|
| Kimi CLI    | Functional prototype | rules, hooks, turn lifecycle, optional toolbar |
| Claude Code | In progress          | hook experiments / validation ongoing          |
| Codex       | Planned              | future adapter                                 |
| Cursor      | Planned              | future adapter                                 |

> Current focus is Kimi CLI. Other integrations are being evaluated.

---

## Kimi CLI integration

<div align="center">

![Kimi Toolbar](docs/assets/kimi-toolbar.svg)

</div>

### Step 1 — Install XiT rules

Teaches Kimi to proactively use `xit auto` for high-output commands:

```bash
xit init kimi --method official_hook --scope user --yes
xit kimi rules install --scope user --yes
```

Restart Kimi. It will now prefer `xit auto go test -v ./...` over raw `go test -v ./...`.

### Step 2 — Verify

```bash
xit kimi rules status --scope user
xit doctor kimi --deep
```

### Step 3 — Optional toolbar patch

Shows XiT status in Kimi's bottom bar (吸T神功 · 准备就绪 → 本次吸T 1次 · 省 ~9k Token):

```bash
xit kimi status-patch install --yes --accept-risk
```

> ⚠️ The toolbar patch modifies your local Kimi Python package. It is **opt-in** and can be rolled back at any time:
>
> ```bash
> xit kimi status-patch uninstall --yes
> ```

Full Kimi docs → [docs/kimi.md](docs/kimi.md)

---

## Supported commands

| Command type | Compression strategy |
|---|---|
| `go test` | exit code + pass/fail count + failed test details |
| `git diff` | changed file count + high-risk files + hunk summary |
| `git log` | one line per commit + total count |
| `git status` | branch + staged/unstaged/untracked counts |
| `grep` / `rg` | grouped by file, max 3 matches per file |
| `npm test` / `pytest` / `cargo test` | pass/fail summary + stack top for failures |
| `tsc` / `eslint` | errors grouped by file |
| `docker logs` | dedup repeated lines, prioritize errors |
| `find` / `ls` | directory aggregate, skips node_modules/.git |

---

## Safety & privacy

- **No telemetry** — nothing is sent anywhere
- **No cloud upload** — all raw logs stay on your machine
- **raw logs stay local** — `.xit/runs/<timestamp>.raw.log`
- **history stays local** — `.xit/history.jsonl`
- **status patch is opt-in** — requires `--yes --accept-risk`, rollback supported
- **fail-open** — if XiT errors, original command output is preserved
- **`saved_tokens = saved_bytes / 4`** — local estimate, not a tokenizer guarantee

→ [docs/privacy.md](docs/privacy.md)

---

## npm package

```
Package:         xitsg
Version:         0.2.40
Install:         npm i -g xitsg
Command:         xit
Platforms:       macOS (arm64 + x64) · Linux (x64 + arm64) · Windows (x64)
```

The `xitsg` npm package ships pre-compiled Go binaries for all platforms. No compilation needed at install time.

---

## Build from source

```bash
git clone https://github.com/stephenywilson/xit
cd xit
go build -o xit ./cmd/xit/main.go
mkdir -p ~/.local/bin
cp ./xit ~/.local/bin/xit
xit --version
```

Requirements: Go 1.21+

---

## Roadmap

- [x] `xit auto` command compression — go test, git diff, grep, npm test, docker logs
- [x] raw\_log local evidence trail
- [x] Kimi CLI — rules mode, hook observe, turn lifecycle, optional toolbar
- [x] Multi-platform npm binary (v0.2.40)
- [ ] Claude Code integration (in progress)
- [ ] Codex adapter (planned)
- [ ] Cursor adapter (planned)
- [ ] Real tokenizer integration (planned)

---

## License

MIT — see [LICENSE](LICENSE)
