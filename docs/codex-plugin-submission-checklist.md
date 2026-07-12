# Codex Plugin Submission Checklist

This tracks what XiT has and still needs before it could be submitted to
OpenAI's official, publicly-searchable Codex **Plugin Directory** — as
opposed to the **local personal marketplace** set up in this repo (see
`plugins/xit/README.md`), which only makes XiT visible to this machine.

**Local personal marketplace ≠ public Plugin Directory.** Installing XiT via
`~/.agents/plugins/marketplace.json` makes it show up under Personal /
"Created by me" in the ChatGPT desktop app on this machine only. To appear
in the official, all-users-searchable Plugin Directory, XiT must go through
OpenAI's plugin submission/review process — that has **not** been started,
and this document does not claim otherwise.

## What XiT has today

- [x] `plugins/xit/.codex-plugin/plugin.json` — manifest with `name`,
      `version`, `description`, `author`, `homepage`, `repository`,
      `license`, `keywords`, `skills`, and an `interface` block
      (`displayName`, `shortDescription`, `longDescription`, `developerName`,
      `category`, `websiteURL`, `privacyPolicyURL`, `defaultPrompt`,
      `brandColor`, `composerIcon`, `logo`).
- [x] `assets/icon.png`, `assets/logo.png` — existing XiT brand icon (dark
      background, 128×128, ~33KB), reused from `extensions/vscode/media/icon.png`.
- [x] `homepage` / `repository` — `https://github.com/stephenywilson/xit`.
- [x] `privacyPolicyURL` — points at `docs/privacy.md` in this repo.
- [x] Install/data-collection description — the SKILL.md and README both
      spell out exactly what is (and is never) sent to telemetry.
- [x] Hook review/audit notes — see "Hook duplication" below.

## What's missing or unconfirmed for public submission

- [ ] **`interface.category` full enum list is not publicly documented.**
      The only confirmed-valid example in OpenAI's docs is `"Productivity"`,
      which is what this manifest uses. `"Developer Tools"` (a more accurate
      fit) was **not** confirmed as valid and was deliberately not used —
      verify the real enum before public submission and switch if a better
      category exists.
- [ ] **`interface.capabilities` schema is not publicly documented.** The
      only example shown in OpenAI's docs is `["Read", "Write"]`, which reads
      like an access-scope enum, not free-text capability descriptions. This
      manifest omits `capabilities` entirely rather than guess a value that
      might fail validation.
- [ ] **`screenshots`** — not included. The repo's existing marketing assets
      (`docs/assets/xit-hero.png`, `docs/assets/metrics.png`) are ~2.3MB each,
      1672×941, and depict the VS Code Dashboard — not the Codex/ChatGPT
      Desktop plugin experience itself. A real screenshot of XiT actually
      running inside Codex mode (ChatGPT Desktop or Codex CLI) doesn't exist
      yet and would need to be captured and appropriately compressed.
- [ ] **`termsOfServiceURL`** — not set; XiT (MIT-licensed, no ToS today)
      may not need one, but this should be confirmed against the actual
      submission requirements.
- [ ] **Support email / support contact** — not currently published anywhere
      in the repo beyond the GitHub repo itself (issues).
- [ ] **Icon/asset size and format requirements** for the actual submission
      pipeline are not stated in the fetched docs (only that manifest paths
      must be relative and `./`-prefixed, under `./assets/`). The current
      128×128 PNG should be re-checked against the real submission tool's
      requirements once available.
- [ ] **OpenAI plugin submission/review itself has not been started.** No
      form has been filled out, no review requested.

## Hook duplication — why this plugin ships Skill-only in stage 1

Per Codex's official hooks documentation: *"Multiple matching command hooks
for the same event are launched concurrently, so one hook can't prevent
another matching hook from starting."* Plugin hooks are also documented to
load *alongside* — not replace — project/user hooks.

XiT already installs a canonical, project-scoped Codex hook at
`.codex/hooks.json` (via `xit hook install codex`, or the `chatgpt` alias —
same hook, see `internal/codexhook`). If this plugin additionally shipped a
`hooks/hooks.json` registering the same `PreToolUse`/Bash lifecycle event,
any project that already has XiT's canonical hook installed would run
**both** hooks concurrently for every Bash command — double-processing, not
graceful de-duplication. There is no documented mechanism for a plugin hook
to detect "an equivalent hook is already installed elsewhere" and skip
registering itself.

Given that, this stage of the plugin intentionally:

- Ships **Skill + diagnostics only** (`skills/xit/SKILL.md`).
- Does **not** include a `hooks/` directory.
- Does **not** reference `hooks` in `plugin.json` at all.

A future stage that wants to ship hooks via the plugin itself would need to
first either (a) migrate the canonical hook installation to be plugin-hook
based everywhere (retiring the standalone `xit hook install codex` path), or
(b) get confirmation from Codex that a project-level hook and a plugin hook
can be reconciled without double-firing. Neither is true today, so hooks
stay out of the plugin rather than risk shipping something that silently
double-compresses.

## Local marketplace details (already done, for reference)

- Personal marketplace file: `~/.agents/plugins/marketplace.json`
  (`{"name":"xit-local","interface":{"displayName":"XiT Local"},"plugins":[{"name":"xit","source":{"source":"local","path":"./.codex/plugins/xit"},"policy":{"installation":"AVAILABLE","authentication":"ON_INSTALL"},"category":"Productivity"}]}`).
- Plugin content copied to `~/.codex/plugins/xit/` (outside this repo, so
  the repo itself is never referenced as a live runtime install path).
- Registered via `codex plugin marketplace add ~` — verified with
  `codex plugin marketplace list` (shows `xit-local`) and `codex plugin list`
  (shows `xit@xit-local`).
- Installed via `codex plugin add xit@xit-local` — verified installed at
  `~/.codex/plugins/cache/xit-local/xit/0.2.51/` with all expected files.
- Rollback: `codex plugin remove xit@xit-local` then
  `codex plugin marketplace remove xit-local`, then delete
  `~/.codex/plugins/xit/` and `~/.agents/plugins/marketplace.json` (nothing
  else existed there before this session — confirmed empty beforehand).

## Bottom line

- **Public Plugin Directory submission status: NOT submitted.**
- Local/personal visibility (ChatGPT Desktop, this machine only): set up
  and CLI-verified; GUI confirmation is a manual step for the user (see the
  main report).
