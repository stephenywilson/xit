# XiT v0.2.53 Release Notes — Integrity Update

## Summary

XiT v0.2.53 is an integrity and privacy foundation release focusing on telemetry correctness, privacy-by-default guarantees, metric precision, and local-state hardening.

## Highlights & Fixes

- **Telemetry Defaults to OFF**: Telemetry is now disabled by default on clean installations. Explicit opt-in consent is strictly required before any metrics can be transmitted (`xit telemetry on`).
- **Explicit Opt-In Required**: Anonymous Install IDs are created lazily only after explicit user consent is given; unconsented installations never generate or persist an install ID.
- **Read-Only Telemetry Status**: `xit telemetry status` is strictly read-only and no longer mutates local disk state or fabricates install IDs when checking status.
- **Local Telemetry State Permissions Hardened**: The XiT home directory (`~/.xit`) is enforced at `0700` permissions. Telemetry state (`telemetry.json`) and offline queue (`telemetry-queue.jsonl`) are strictly enforced at `0600` permissions via atomic write workflows across all creation and rewrite operations.
- **Offline Retry Reliability**: Fixed partial failure handling in the offline retry queue (`telemetry-queue.jsonl`), ensuring queued events are preserved upon delivery errors instead of silently dropping data.
- **Fail-Open with Explicit Persistence Failure**: The server-side metrics endpoint rejects failed D1 database writes with a `503 Service Unavailable` response rather than falsely reporting success.
- **Accurate Run Statistics**: Telemetry event classification (`event` column) cleanly separates `run.finished` execution events from `extension.activated` and internal development telemetry. Non-run events no longer count as XiT execution runs in aggregate statistics.
- **Weighted Compression Ratio**: Dashboard and backend metrics compute weighted compression ratios based on aggregate input and saved bytes, preventing skew from short runs.
- **Development & Production API Base Isolation**: Development source trees default to an empty API base (`internal/apibase.Default = ""`), ensuring local and development builds do not leak events to the production API. Production endpoints are injected only at release build time.
