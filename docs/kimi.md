# XiT — Kimi CLI Integration Guide

## Overview

XiT's Kimi CLI integration is a **functional prototype**. It works today via rules mode and hook observe. The optional toolbar patch adds a visible status bar inside Kimi.

---

## Installation

### 1. Rules mode (recommended)

Installs a skill file at `~/.kimi/skills/xit/SKILL.md`. Kimi discovers it at startup and proactively uses `xit auto` for high-output commands.

```bash
xit init kimi --method official_hook --scope user --yes
xit kimi rules install --scope user --yes
```

Restart Kimi. Verify:

```bash
xit kimi rules status --scope user
```

### 2. Verify Kimi uses XiT

Paste this into Kimi:

```
Run the tests and show me a summary
```

Kimi should respond with `xit auto go test -v ./...` instead of raw `go test -v ./...`.

To get the exact dogfood prompt:

```bash
xit kimi rules dogfood
```

---

## Hook observe mode

Records Kimi's tool calls to `.xit/kimi-hooks/events.jsonl` without blocking anything.

```bash
xit hook status kimi --scope user
xit hook stats kimi
```

---

## Safe reroute (optional)

When enabled, XiT returns a `deny` response to Kimi's PreToolUse hook for high-output commands, recommending `xit auto <original>` instead.

```bash
xit hook enable-reroute kimi --yes
xit hook disable-reroute kimi --yes
```

> **Note:** Kimi shows the deny as a Shell tool ERROR, not a soft suggestion. Kimi may not automatically re-run `xit auto <command>`. Rules mode is the preferred path.

---

## Optional toolbar patch

Modifies Kimi's local Python package to show XiT status in the bottom bar.

Status rotates every 15 seconds:
- `吸T神功 · 准备就绪`
- `吸T神功 · 正在吸T中`
- `本次吸T 1次 · 省 ~9k Token`
- `XiT ON · raw_log 留证中`

```bash
# Check compatibility (read-only)
xit kimi status-patch status

# Preview patch plan (no file changes)
xit kimi status-patch dry-run

# Validate syntax on temp copy
xit kimi status-patch validate

# Install (requires --yes --accept-risk)
xit kimi status-patch install --yes --accept-risk

# Uninstall and restore from backup
xit kimi status-patch uninstall --yes
```

> ⚠️ The toolbar patch is experimental. It modifies Kimi's `ui/shell/prompt.py`. Kimi updates may break it. Backup is created automatically before install.

---

## Full health check

```bash
xit doctor kimi --deep
# or
xit kimi doctor
```

## Compression stats

```bash
xit kimi benchmark
xit gain
```

## Uninstall everything

```bash
xit kimi rules uninstall --scope user --yes
xit uninstall kimi --method official_hook --scope user --yes
xit kimi status-patch uninstall --yes
```
