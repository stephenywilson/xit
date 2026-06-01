# XiT Token Saver

XiT Token Saver（吸T神功）helps AI coding tools save tokens and hit the right answer faster.

When commands like `go test -v ./...`, `npm test`, `docker logs`, `tsc`, or `eslint` produce huge terminal output, XiT compresses the noise before it reaches your AI assistant.

## Supported workflows

- Claude Code
- Codex
- Cursor
- Gemini Code Assist
- VS Code Chat
- Terminal-based AI coding agents

## Core value

- **Save tokens** — shrink noisy command output before it enters the AI context window
- **Reduce noisy context** — keep only what matters
- **Improve AI answer hit rate** — less noise means better answers
- **Keep all processing local** — no cloud, no upload
- **No telemetry**
- **No network requests**

## What this extension does

- **Run commands with compression**: `XiT: Run Command` detects high-output commands and wraps them with `xit auto`
- **Run with auto compression**: `XiT: Run with Auto Compression` always runs with `xit auto`
- **Dedicated XiT Terminal**: Open a terminal optimized for XiT workflows
- **Status bar**: Live state indicators show what XiT is doing and which AI tool is active
- **Dashboard**: Latest run details, workspace gain stats, and recent events
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
| `吸T神功 · 已连接 Claude · 准备就绪` | Idle — Claude Code is the active AI surface |
| `吸T神功 · 正在压缩` | Running — a command is being compressed |
| `吸T神功 · Claude · 正在压缩` | Running — compressing for Claude Code |
| `吸T神功 · 本次省991B` | Success — current run saved 991 bytes |
| `吸T神功 · Claude · 本次省~41KB` | Success — saved ~41KB for Claude Code |
| `吸T神功 · 本次未触发压缩` | Missed — a high-output command ran without compression |
| `吸T神功 · 未找到 XiT` | XiT binary not found — install the CLI |

Hover over the status bar for more details, including historical cumulative savings.

**Privacy note:** XiT detects the active AI surface from VS Code UI metadata (tab labels, terminal names) and recent XiT adapter events. It never reads chat content or conversations.

## Settings

| Setting | Default | Description |
|---------|---------|-------------|
| `xit.binaryPath` | `""` | Path to `xit` binary. Auto-detected if empty. |
| `xit.home` | `""` | XiT home directory. Defaults to `~/.xit`. |
| `xit.refreshInterval` | `10` | Status bar refresh interval in seconds. |
| `xit.enableStatusBar` | `true` | Show XiT status bar item. |
| `xit.enableTerminalListener` | `false` | Listen to VS Code terminal shell executions and suggest `xit auto` for high-output commands. |
| `xit.showActiveAiSurface` | `true` | Show the detected active AI tool in the status bar. |

## Privacy

- No telemetry
- No network requests
- Only reads local `~/.xit` and workspace `.xit` directories
- Raw logs are opened only when you explicitly trigger the command
- Terminal listener captures only command metadata (command line, cwd, terminal name), never command output or environment variables
- Active AI surface detection uses only VS Code UI metadata and XiT adapter events, never chat content

## Install from VSIX

If not installing from the Marketplace:

```bash
npx vsce package
```

Then in VS Code / Cursor:

1. Command Palette → `Extensions: Install from VSIX...`
2. Choose `xit-vscode-*.vsix`
3. Reload window
