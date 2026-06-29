# XiT 0.2.48 / VS Code 0.0.34 Hotfix

A reliability hotfix for the VS Code extension.

## Fixed
- Dashboard and Status Bar now update reliably across repeated AI workflow runs.
- Events remain visible when an AI agent changes directories during a session, including when the working directory moves outside the current VS Code workspace.
- More resilient event handling, so transient or partial events no longer leave the Dashboard stale.

## Privacy
- No telemetry. Raw logs stay local. XiT does not upload terminal output, prompts, AI replies, or full session ids.

## Notes
- Upgrade both `xitsg@0.2.48` and the VS Code extension `0.0.34` for the fix to take effect.
