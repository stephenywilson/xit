# XiT Token Saver

A VS Code companion for [XiT](https://github.com/stephenywilson/xit) — a local terminal output compression layer that prevents AI coding agents from being overwhelmed by high-noise commands.

## What XiT does

When you run commands like `go test -v ./...`, `git diff`, or `docker logs`, the output can be thousands of lines long. XiT compresses this output locally before it reaches the AI agent's context window, saving tokens and improving response quality.

- **Detect**: Recognizes high-output commands
- **Route**: Runs them with `xit auto` for automatic compression
- **Report**: Shows saved bytes, reduction rate, and raw log paths

All processing happens locally. No data leaves your machine.

## What this extension does

XiT Status brings XiT visibility into VS Code:

- **Run commands with compression**: `XiT: Run Command` detects high-output commands and wraps them with `xit auto`
- **Dedicated XiT Terminal**: Open a terminal optimized for XiT workflows
- **Status bar**: Live state indicators show what XiT is doing
- **Dashboard**: Latest run details, workspace gain stats, adapter activity, and recent events
- **Terminal listener** (opt-in): Detects high-output commands in VS Code terminals and suggests `xit auto`

## Requirements

**XiT CLI must be installed separately.** This extension does not bundle the XiT binary.

### Install XiT CLI

```bash
npm install -g xitsg
```

Or download from [GitHub Releases](https://github.com/stephenywilson/xit/releases).

The extension will auto-detect the `xit` binary from:
- Your `PATH`
- `~/.local/bin/xit`
- Workspace `./xit`

You can also set a custom path via the `xit.binaryPath` setting.

## Commands

| Command | What it does |
|---------|-------------|
| `XiT: Run Command` | Detects if a command is high-output and runs it with `xit auto` |
| `XiT: Run with Auto Compression` | Always runs the command with `xit auto` |
| `XiT: Open XiT Terminal` | Opens a dedicated terminal named "XiT" |
| `XiT: Open Dashboard` | Shows latest run, gain stats, and activity |
| `XiT: Refresh` | Refreshes the status bar |
| `XiT: Show Gain` | Shows a quick gain summary message |
| `XiT: Open Latest Raw Log` | Opens the most recent raw log from workspace `.xit/runs/` |
| `XiT: Show Output Channel` | Shows XiT extension debug output |

## Status bar meanings

| Text | Meaning |
|------|---------|
| `吸T神功 · 准备就绪` | Idle — XiT is ready |
| `吸T神功 · 正在压缩` | Running — a command is being compressed |
| `吸T神功 · 本次省991B` | Success — current run saved 991 bytes |
| `吸T神功 · 本次省~41KB` | Success — current run saved ~41 kilobytes |
| `吸T神功 · 本次未触发压缩` | Missed — a high-output command ran without compression |
| `吸T神功 · 未找到 XiT` | XiT binary not found — install the CLI |

Hover over the status bar for more details, including historical cumulative savings.

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
- Terminal listener captures only command metadata (command line, cwd, terminal name), never command output or environment variables

## Install from VSIX

If not installing from the Marketplace:

```bash
npx vsce package
```

Then in VS Code / Cursor:

1. Command Palette → `Extensions: Install from VSIX...`
2. Choose `xit-vscode-*.vsix`
3. Reload window
