# XiT Codex Plugin (Local / Personal Marketplace)

This is the local Codex Plugin packaging of XiT (吸T神功) — a plugin so XiT
appears in the ChatGPT desktop app's Plugins directory (Personal /
"Created by me"), for **Codex mode** inside ChatGPT Desktop, Codex CLI, and
Codex IDE.

## Automatic mode — the plugin is NOT what makes this automatic

**The plugin (Skill + status commands) is not the automation mechanism.**
The actual "no manual Skill selection" automatic behavior comes from XiT's
canonical Codex hook (`.codex/hooks.json`, project-scoped) — a `PreToolUse`
hook that rewrites a high-output Bash command to `xit auto <command>`
*before Codex ever runs it* (see `internal/codexhook/rewrite.go`, `RewriteCommandForTurn`).
That hook is what must be installed and trusted for a project; the plugin
only gives it a discoverable name, an icon, and a couple of diagnostic
commands (`xit chatgpt status`, `xit chatgpt diagnose`, `xit chatgpt setup
--auto`) in the ChatGPT Desktop UI.

Set up automatic mode for a project with:

```
xit chatgpt setup --auto
```

This installs/repairs the project's canonical hook, refuses to proceed if it
would create a duplicate (see below), and prints a final status block. Codex
itself still requires one manual step per project: run `/hooks` once (in
Codex CLI, IDE, or ChatGPT Desktop) to approve/trust the hook — XiT cannot
bypass that approval, and does not try to.

## What this plugin currently includes

- `.codex-plugin/plugin.json` — the plugin manifest.
- `skills/xit/SKILL.md` — documents automatic mode, the manual fallback if
  it isn't active, and what must never be sent to telemetry.
- `assets/icon.png`, `assets/logo.png` — the existing XiT brand icon
  (dark background, "吸" character), reused from
  `extensions/vscode/media/icon.png`.

## What this plugin deliberately does NOT include: hooks

XiT already has a canonical Codex hook, installed per-project at
`.codex/hooks.json` via `xit hook install codex` (or the `chatgpt` alias,
which resolves to the exact same hook — see `internal/codexhook`).

Per Codex's own documented hook semantics ("Multiple matching command hooks
for the same event are launched concurrently, so one hook can't prevent
another matching hook from starting" — official Codex hooks docs), **a
second, plugin-level `hooks/hooks.json` registering the same PreToolUse/Bash
lifecycle event would run concurrently alongside the existing canonical
hook**, not replace or dedupe it. For any project that already has XiT's
canonical hook installed, that would mean every Bash command gets processed
twice.

There is no documented mechanism to detect "a compatible hook is already
installed" from inside a plugin's own `hooks.json` and skip registering.
Given that, this plugin ships **Skill + diagnostics only** in this stage —
correctness over a "complete-looking" plugin. See
`docs/codex-plugin-submission-checklist.md` in the main repo for the
reasoning and what a future hook-enabled stage would require.

## Canonical hook provider

Regardless of whether this plugin is installed, the canonical Codex hook
(project-scoped `.codex/hooks.json`) remains the single source of truth,
managed via:

```
xit hook install codex --scope project --yes
xit hook status codex
xit hook install chatgpt --scope project --yes   # same hook, ChatGPT Desktop framing
xit hook status chatgpt
```

## Not the public Plugin Directory

Installing this via a personal marketplace (`~/.agents/plugins/marketplace.json`)
makes XiT visible **only to this machine**, under Personal / "Created by me"
in the ChatGPT desktop app. It does **not** submit XiT to OpenAI's curated,
publicly-searchable Plugin Directory — that requires a separate submission
and review process. See `docs/codex-plugin-submission-checklist.md`.
