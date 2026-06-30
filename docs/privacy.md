# XiT — Safety & Privacy

## What XiT does locally

| Item | Location | Uploaded? |
|------|----------|-----------|
| Raw command output | `.xit/runs/<timestamp>.raw.log` | Never |
| Compression history | `.xit/history.jsonl` | Never |
| Kimi hook events | `.xit/kimi-hooks/events.jsonl` | Never |
| XiT config | `~/.xit/config.json` | Never |
| Kimi skill file | `~/.kimi/skills/xit/SKILL.md` | Never |

## Telemetry — anonymous usage metrics

XiT may send **anonymous usage metrics** such as adapter type, version, OS/arch,
byte counts, compression ratio, estimated token savings, and success/error
status. It is enabled by default, transparent, and easy to disable.

XiT **never** uploads raw terminal output, prompts, AI replies, command text,
file paths, usernames, emails, API keys, or full session ids. The wire schema
physically has no field for any of those.

Disable any time:

```bash
xit telemetry off          # or: XIT_TELEMETRY=off
```

In VS Code, telemetry also follows the editor's global `telemetry.telemetryLevel`
— if that is `off`, the XiT extension sends nothing.

See [telemetry.md](telemetry.md) for the exact fields, the opt-out, and the
backend design.

## Token estimate

`saved_tokens = saved_bytes / 4`

This is a rough estimate based on average bytes-per-token. It is not produced by a real tokenizer. The actual token savings depend on the tokenizer used by your AI CLI.

## Toolbar patch

The optional Kimi toolbar patch modifies a local file in your Kimi Python package installation. It reads from `.xit/history.jsonl` and `.xit/kimi-hooks/events.jsonl` to generate the status text. It does not write any new files or make network requests.

A backup is created before install. Uninstall restores the original file from backup.

## Fail-open

If XiT encounters an error during compression, it outputs the original command's stdout/stderr unchanged and notes `filter_error` in the summary. It never blocks or modifies the original command's behavior.
