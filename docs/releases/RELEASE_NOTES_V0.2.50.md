# XiT 0.2.50 CLI Hotfix

A packaging hotfix for the npm `xitsg` distribution.

## Fixed
- v0.2.50 fixes the npm package binary mismatch in v0.2.49.
- v0.2.49 package metadata was published as 0.2.49, but the bundled CLI binaries still reported 0.2.48.
- v0.2.50 rebuilds all npm vendor binaries and is the correct CLI release for telemetry, version-check, and multi-channel support.

## Notes
- Install `xitsg@0.2.50`; after install, `xit --version` must report `0.2.50`.
- The VS Code extension `0.0.35` is unaffected and does not change in this release.

## Privacy
- No change to privacy behavior. Telemetry remains anonymous and opt-out; raw logs stay local. XiT does not upload terminal output, prompts, AI replies, command text, paths, or full session ids.
