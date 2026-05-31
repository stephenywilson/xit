# XiT / Cursor Adapter

## Status

- **Hook observe**: supported via `beforeShellExecution`
- **Strict mode (GUI ask)**: supported via `permission: ask` + `user_message`
- **Hitrate**: supported via `xit hook hitrate cursor`
- **Reroute/rewrite**: not enabled (Cursor `beforeShellExecution` does not support `updated_input`)
- **StatusLine**: not supported by Cursor CLI

## Installation

Install the XiT Cursor hook into your user-level Cursor configuration:

```bash
xit hook install cursor --yes
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

## Strict Mode (Visible Feedback)

By default, XiT Cursor hook runs in **observe** mode: it silently records events without interrupting you.

Enable **strict** mode to get GUI-visible feedback when a high-output command is not wrapped with `xit auto`:

```bash
xit hook enable-strict cursor --yes
```

In strict mode, when Cursor tries to run a command like `go test -v ./...` without `xit auto`, the hook returns:

```json
{
  "permission": "ask",
  "user_message": "XiT: high-output command detected. Use: xit auto go test -v ./...",
  "agent_message": "Consider using xit auto go test -v ./... to reduce context noise."
}
```

- `user_message` appears in the Cursor GUI as a visible prompt
- `agent_message` is sent to the LLM alongside the prompt
- The user can still choose to run the command; XiT never blocks permanently

Disable strict mode to return to silent observe:

```bash
xit hook disable-strict cursor --yes
```

## Uninstallation

```bash
xit hook uninstall cursor --yes
```

## How it works

When Cursor runs a shell command, it sends a JSON payload to the XiT hook:

```json
{"command": "go test -v ./...", "cwd": "/path/to/project", "sandbox": false}
```

XiT classifies the command, logs an event to `~/.xit/cursor-hooks/events.jsonl`, and returns a permission response:

- **observe mode**: `{"permission": "allow"}`
- **strict mode + missed compress**: `{"permission": "ask", "user_message": "..."}`
- **strict mode + already wrapped or passthrough**: `{"permission": "allow"}`

Strict ask events include metadata fields:

```json
{
  "action": "ask",
  "strict": true,
  "prompted": true,
  "visible_feedback": true
}
```

These fields power `strict_prompts` and `visible_feedback` counters in `xit hook stats cursor` and `xit hook hitrate cursor`.

XiT never permanently blocks, never rewrites commands, and never uploads data.

## Dogfood prompt

When working on XiT itself with Cursor enabled:

```
You are helping improve XiT. Use `xit auto` for long commands.
```
