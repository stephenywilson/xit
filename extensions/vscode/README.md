# 吸T神功（XiT）

吸T神功（XiT）是一个帮 AI 编程助手省 token、减少噪音、提升命中率的 VS Code 插件。

当 `go test -v ./...`、`npm test`、`docker logs`、`tsc`、`eslint` 这类命令输出太长时，吸T神功会在内容进入 Claude、Codex、Gemini、Cursor 等 AI 之前先进行压缩，避免上下文被无效输出占满。

## 核心价值

- **省 token** — 把高噪音命令输出压缩后再交给 AI
- **减少噪音上下文** — 只保留关键信息
- **提升 AI 回答命中率** — 噪音越少，AI 越容易给出准确答案
- **支持 Claude Code、Codex、Gemini Code Assist、Cursor、VS Code Chat**
- **本地处理** — 不上传云端
- **无遥测**
- **无网络请求**

## 安装前提

**需要单独安装 XiT CLI。** 本插件不内置 XiT 二进制文件。

```bash
npm install -g xitsg
```

或从 [GitHub Releases](https://github.com/stephenywilson/xit/releases) 下载。

插件会自动从以下位置检测 `xit`：
- 系统 `PATH`
- `~/.local/bin/xit`
- 工作区 `./xit`

也可以通过设置 `xit.binaryPath` 指定自定义路径。

## 命令

| 命令 | 作用 |
|---------|-------------|
| `XiT: Run Command` | 检测命令是否高输出，如果是则用 `xit auto` 运行 |
| `XiT: Run with Auto Compression` | 始终用 `xit auto` 运行命令 |
| `XiT: Open XiT Terminal` | 打开名为 "XiT" 的专用终端 |
| `XiT: Open Dashboard` | 显示最近运行记录、累计节省、活动日志 |
| `XiT: Refresh` | 刷新状态栏 |
| `XiT: Show Gain` | 弹出累计节省摘要 |
| `XiT: Open Latest Raw Log` | 打开工作区 `.xit/runs/` 中最新的原始日志 |
| `XiT: Show Output Channel` | 显示 XiT 扩展调试输出 |

## 状态栏含义

| 文本 | 含义 |
|------|---------|
| `吸T神功 · 准备就绪` | 空闲 — XiT 已就绪 |
| `吸T神功 · 已连接 Claude · 准备就绪` | 空闲 — 检测到 Claude Code 为当前 AI 工作区 |
| `吸T神功 · 正在压缩` | 运行中 — 正在压缩命令输出 |
| `吸T神功 · Claude · 正在压缩` | 运行中 — 正在为 Claude Code 压缩 |
| `吸T神功 · 本次省991B` | 成功 — 本次运行节省了 991 字节 |
| `吸T神功 · Claude · 本次省~41KB` | 成功 — 为 Claude Code 节省了约 41KB |
| `吸T神功 · 本次未触发压缩` | 错过 — 高输出命令未经过压缩直接运行 |
| `吸T神功 · 未找到 XiT` | 未找到 XiT 二进制 — 请先安装 CLI |

鼠标悬停在状态栏上可查看更多信息，包括历史累计节省量。

**隐私说明：** 吸T神功通过 VS Code UI 元数据（标签页标题、终端名称）和最近的 XiT 适配器事件来检测当前使用的 AI 工具。它**不会读取聊天内容或对话**。

## 设置

| 设置 | 默认值 | 说明 |
|---------|---------|-------------|
| `xit.binaryPath` | `""` | `xit` 二进制文件路径。为空时自动检测。 |
| `xit.home` | `""` | XiT 主目录。默认为 `~/.xit`。 |
| `xit.refreshInterval` | `10` | 状态栏刷新间隔（秒）。 |
| `xit.enableStatusBar` | `true` | 显示吸T神功状态栏。 |
| `xit.enableTerminalListener` | `false` | 监听 VS Code 终端命令执行，对高输出命令建议使用 `xit auto`。 |
| `xit.showActiveAiSurface` | `true` | 在状态栏显示检测到的当前 AI 工具。 |

## 隐私

- 无遥测
- 无网络请求
- 只读取本地 `~/.xit` 和工作区 `.xit` 目录
- 原始日志仅在手动触发命令时打开
- 终端监听器只捕获命令元数据（命令行、工作目录、终端名称），**不捕获命令输出或环境变量**
- AI 工具检测仅使用 VS Code UI 元数据和 XiT 适配器事件，**不读取聊天内容**

## 从 VSIX 安装

如不从应用商店安装：

```bash
npx vsce package
```

然后在 VS Code / Cursor 中：

1. 命令面板 → `Extensions: Install from VSIX...`
2. 选择 `xit-vscode-*.vsix`
3. 重新加载窗口
