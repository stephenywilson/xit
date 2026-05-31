# XiT Status

Local XiT status, saved tokens, hitrate, and raw logs for VS Code and Cursor.

## What it does

XiT Status is a companion extension for the [XiT](https://github.com/stephenywilson/xit) CLI. It shows:

- **Status bar**: saved tokens, estimated reduction, and condensed command count
- **Dashboard**: top commands, adapter activity, recent events
- **Quick access**: open latest raw log, refresh status

All data stays local. No telemetry, no network requests.

## Requirements

- XiT CLI installed (`npm i -g xitsg` or download from GitHub Releases)

## Local install from VSIX

```bash
npx vsce package
```

Then in VS Code / Cursor:

1. Command Palette → `Extensions: Install from VSIX...`
2. Choose `xit-vscode-*.vsix`
3. Reload window

## Commands

| Command | Title |
|---------|-------|
| `XiT: Open Dashboard` | Open XiT Dashboard |
| `XiT: Refresh` | Refresh status bar |
| `XiT: Show Gain` | Show gain summary |
| `XiT: Open Latest Raw Log` | Open the newest raw log from workspace `.xit/runs/` |
| `XiT: Show Output Channel` | Show XiT output for debugging |

## Settings

| Setting | Default | Description |
|---------|---------|-------------|
| `xit.binaryPath` | `""` | Path to `xit` binary. Auto-detected if empty. |
| `xit.home` | `""` | XiT home directory. Defaults to `~/.xit`. |
| `xit.refreshInterval` | `10` | Status bar refresh interval in seconds. |
| `xit.enableStatusBar` | `true` | Show XiT status bar item. |

## Privacy

- No telemetry
- No network requests
- Only reads local `~/.xit` and workspace `.xit` directories
- Raw logs are opened only when you explicitly trigger the command
