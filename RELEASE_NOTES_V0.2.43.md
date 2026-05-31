# XiT v0.2.43 — NPM Vendor Binary Hotfix

## Root Cause

v0.2.42 package metadata was published, but the bundled vendor binaries still reported `xit version 0.2.41`.

- `npm view xitsg version` returned `0.2.42` (metadata correct)
- `npx xitsg@latest --version` returned `xit version 0.2.41` (binary stale)

## Fix

Regenerate all vendor binaries for the npm package so the bundled CLI version matches the package version.

## Vendor Binaries

All five platform targets rebuilt:

- `vendor/darwin-amd64/xit`
- `vendor/darwin-arm64/xit`
- `vendor/linux-amd64/xit`
- `vendor/linux-arm64/xit`
- `vendor/win32-amd64/xit.exe`

## Behavior

No functional behavior changes from v0.2.42.

---

## Install

```bash
npm i -g xitsg
```

```bash
xit --version        # 0.2.43
```

## Safety

- No telemetry
- raw_log stays local
- history stays local
- hook events stay local
