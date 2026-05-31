# XiT Status

Active XiT command runner, status dashboard, and terminal listener for VS Code and Cursor.

## What it does

XiT Status is a companion extension for the [XiT](https://github.com/stephenywilson/xit) CLI. It helps you:

- **Run commands with compression**: `XiT: Run Command` detects high-output commands and runs them with `xit auto`
- **Status bar**: live states (idle / running / saved / missed), saved tokens, estimated reduction
- **Dashboard**: latest run, workspace gain, adapter activity, recent events
- **Terminal listener** (opt-in): detects high-output commands in VS Code terminal and suggests `xit auto`
- **Quick access**: open XiT Terminal, latest raw log, refresh status

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
| `XiT: Run Command` | Detect high-output and run with `xit auto` |
| `XiT: Run with Auto Compression` | Always run with `xit auto` |
| `XiT: Open XiT Terminal` | Open a dedicated XiT terminal |
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
| `xit.enableTerminalListener` | `false` | Listen to VS Code terminal shell executions and suggest `xit auto` for high-output commands. |

## Privacy

- No telemetry
- No network requests
- Only reads local `~/.xit` and workspace `.xit` directories
- Raw logs are opened only when you explicitly trigger the command
