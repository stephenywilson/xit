# XiT v0.2.52 Release Notes

## Summary

v0.2.52 finalizes the ChatGPT Desktop / Codex Scheme B behavior: XiT no
longer emits a Stop Hook footer by default, and per-command compressed output
keeps the visible low-noise status line.

## Fixed

- Stop Hook no longer returns `decision:block` / `reason` for the XiT summary
  path, so it does not create a continuation prompt or cause the model to
  repeat the summary.
- Stop Hook no longer returns `systemMessage` by default because real
  ChatGPT/Codex App UI validation showed that this field is not displayed for
  the Stop summary in the current client.
- Internal no-repeat/control phrases are no longer emitted in user-visible
  hook output.
- Turn counters remain unchanged: N compressed commands remain N; Stop closes
  and cleans the turn state without duplicate counting.
- Per-command Codex tool output continues to carry the visible low-noise XiT status
  line, such as `XiT · auto · ... · saved ~... tokens · exit ...`.
- Aggregate metrics remain available through the Dashboard. `xit telemetry
  status` reports whether anonymous aggregate telemetry is enabled or disabled.

## Compatibility

- Codex CLI, ChatGPT Desktop Codex mode, and Codex IDE keep the same shared hook
  lifecycle.
- Legacy `footer_continuation_used` state is still fail-open guarded so older
  in-flight turns cannot loop.
- Telemetry behavior and privacy boundaries are unchanged.

## Release Status

- CLI/npm version bumped to `0.2.52`.
- npm vendor binaries were rebuilt locally from the final Scheme B source.
- Not published.
