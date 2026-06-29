# XiT 0.2.48 / VS Code 0.0.34 Hotfix

## Fixed
- Fixed VS Code Dashboard / Status Bar becoming stale after repeated AI workflow runs.
- Added a global VS Code bridge mirror event file so events are still visible when an AI agent changes cwd outside the current VS Code workspace.
- Improved VS Code bridge watcher resilience for malformed JSON, partial lines, truncation, rotation, and repeated events.
- Added host-instance corroboration so same-window AI events are accepted even when command cwd differs from workspace root.
- Added bridge diagnostics without exposing raw prompts, commands, terminal output, or full session ids.

## Notes
- This hotfix requires both xitsg 0.2.48 and VS Code extension 0.0.34 for the cross-workspace mirror fix.
