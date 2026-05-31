# XiT / Cursor Adapter

## Status

- **Hook observe**: supported via `beforeShellExecution`
- **Hitrate**: supported via `xit hook hitrate cursor`
- **Reroute/rewrite**: not enabled (Cursor `beforeShellExecution` does not support `updated_input`)
- **StatusLine**: not supported by Cursor CLI

## Installation

Install the XiT Cursor hook into your user-level Cursor configuration:

```bash
xit hook install cursor --scope user --yes
```

This writes:
- `~/.cursor/hooks.json` — merges XiT hook entry into `beforeShellExecution`
- `~/.xit/hooks/cursor-before-shell-exec.sh` — script that calls `xit cursor-hook before-shell-exec`

After installing, restart Cursor IDE or agent for hooks to take effect.

## Verification

```bash
xit hook status cursor
xit hook stats cursor
xit hook hitrate cursor
```

## Uninstallation

```bash
xit hook uninstall cursor --scope user --yes
```

## How it works

When Cursor runs a shell command, it sends a JSON payload to the XiT hook:

```json
{"command": "go test -v ./...", "cwd": "/path/to/project", "sandbox": false}
```

XiT classifies the command, logs an event to `~/.xit/cursor-hooks/events.jsonl`, and returns:

```json
{"permission": "allow"}
```

XiT never blocks, never rewrites, and never uploads data.

## Dogfood prompt

When working on XiT itself with Cursor enabled:

```
You are helping improve XiT. Use `xit auto` for long commands.
```
