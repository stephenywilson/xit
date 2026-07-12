---
name: XiT
description: Automatic terminal output compression for Codex — high-output commands are rerouted through xit auto by a hook, not by you choosing this skill.
---

# XiT — Automatic Terminal Output Compression for Codex

XiT (吸T神功) compresses noisy terminal output into a short, AI-readable
summary, while keeping the full raw output in a local log file. This applies
to Codex mode inside the **ChatGPT desktop app**, **Codex CLI**, and **Codex
IDE** — they all share the same underlying Codex hook/config, so nothing
here is specific to one of them. This skill does **not** apply to, and does
not claim to support, ChatGPT's general Chat or Work modes.

## Automatic mode — you do not need to do anything

**When automatic mode is set up for a project** (via `xit chatgpt setup
--auto`, or already installed by whoever set up this repo), XiT's Codex hook
inspects every Bash tool call BEFORE it runs, and transparently reroutes
high-output commands through `xit auto` for you. You do not need to:

- prepend `xit auto` to the command yourself,
- mention XiT in your response,
- wait for the user to ask for XiT,
- select this skill manually before running a command.

Just run the command you would normally run (e.g. `go test -v ./...`). If
automatic mode is active, the hook has already rewritten it before it
executes; the tool result you get back is the already-compressed summary,
often followed by a short `XiT · auto · ...` status line showing byte counts
and estimated token savings for that one command — that line is how the
hook, not the skill, tells you (and the user) it ran.

## Checking whether automatic mode is actually active

Before assuming automatic mode is on, you can check:

```
command -v xit
xit --version
xit telemetry status
xit chatgpt status
```

`xit chatgpt status` reports `Automatic mode: enabled/disabled` for the
current project, plus whether the hook is trusted and whether a duplicate
hook exists. If it reports `disabled`, or XiT is not installed at all, fall
back to the manual behavior below.

## Manual fallback — only if automatic mode is NOT active

If `command -v xit` finds nothing, XiT is not installed. Tell the user and
suggest:

```
npm install -g xitsg@latest
```

Do not attempt to install it yourself unless the user explicitly asks you to.

If XiT is installed but `xit chatgpt status` shows automatic mode disabled
(no hook, or a duplicate-hook conflict), you can still manually wrap a
command that is likely to produce a large amount of output:

- `go test -v ./...`, `pytest`, `npm test`, `cargo test`
- `git diff`, `git log --stat`
- `grep -r` / `rg` across a repository
- `find` / directory tree scans
- `docker logs`, `kubectl logs`
- `tsc`, `eslint`, `ruff`, or other build/lint output

Example: `xit auto go test -v ./...` instead of `go test -v ./...` directly.

Do not wrap commands that are already short, structured, interactive, or
sensitive: `git status`, `git branch`, version-check commands, anything with
`--json`/`--porcelain`, interactive commands (live stdin, a REPL, a pager),
install/uninstall commands (unless asked), or anything already short. Never
wrap a command that already starts with `xit` itself — that would recurse.

## If the wrapped command fails

Never hide a real failure — whether it was wrapped automatically by the hook
or manually by you. Preserve and report:

- the exit code
- the error message / key failure evidence from the compressed summary
- the path to the full raw log (XiT always keeps one locally)

A non-zero exit code from a command run through `xit auto` is still a real
failure — report it exactly as you would an unwrapped command's failure.

## Privacy — what must never be sent anywhere

XiT's compression, its per-command status line, and its optional anonymous
telemetry never include, and you must never attempt to make them include:

- prompt text or AI reply text
- the literal command text being run
- the current working directory or any file path
- repository name, username, or email
- API keys, tokens, or any other secret

Telemetry (if enabled) is anonymous, aggregate-only (adapter, version,
byte counts, compression ratio, success/error status), and can be disabled
entirely with `xit telemetry off` or `XIT_TELEMETRY=off`.

## Scope of this skill

This skill and the underlying hook are shared identically across:

- Codex mode inside the ChatGPT desktop app (`chatgpt_desktop_codex`)
- Codex CLI (`codex_cli`)
- Codex IDE / VS Code extension (`codex_ide`)

There is exactly one canonical Codex hook per project (this is what actually
performs the automatic rerouting — this Skill is documentation and a
diagnostic aid, not the mechanism itself). Installing or enabling XiT for
one of the three surfaces above does not require, and must not create, a
second hook for the others.
