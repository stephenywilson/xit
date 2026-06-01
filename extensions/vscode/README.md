# 吸T神功（XiT）

吸T神功（XiT）是一个帮 AI 编程工作流省 token、减少噪音、提升命中率的 VS Code 插件。

当 `go test -v ./...`、`npm test`、`docker logs`、`tsc`、`eslint` 这类命令输出太长时，吸T神功会先对高噪音输出进行压缩，再交给 Claude、Codex、Gemini、Cursor 等 AI 编程工作流使用，避免上下文被无效输出占满。

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
| `XiT: Open Dashboard` | 显示最近运行记录、Workflow Health、累计节省、活动日志 |
| `XiT: Refresh` | 刷新状态栏 |
| `XiT: Show Gain` | 弹出累计节省摘要 |
| `XiT: Open Latest Raw Log` | 打开工作区 `.xit/runs/` 中最新的原始日志 |
| `XiT: Show Output Channel` | 显示 XiT 扩展调试输出 |
| `XiT: Install Workspace AI Rules` | 在当前 workspace 的 AI 规则文件里幂等写入 XiT 命令输出规则 |
| `XiT: Diagnose AI Workflow` | 输出当前 workspace 的 XiT CLI、最近 run、saved bytes、rules、routing hit rate 报告 |

## 状态栏含义

| 文本 | 含义 |
|------|---------|
| `吸T神功 · 准备就绪` | 空闲 — XiT 已就绪 |
| `吸T神功 · 正在压缩` | 运行中 — 正在压缩命令输出 |
| `吸T神功 · 本次省991B` | 成功 — 本次运行节省了 991 字节 |
| `吸T神功 · 本次省~41KB` | 成功 — 本次运行节省了约 41KB |
| `吸T神功 · 本次未触发压缩` | 错过 — 高输出命令未经过压缩直接运行 |
| `吸T神功 · 未找到 XiT` | 未找到 XiT 二进制 — 请先安装 CLI |

鼠标悬停在状态栏上可查看更多信息，包括最近一次节省、workspace rules 是否已安装，以及最近高噪音命令的 XiT 路由情况。

**边界说明：** 吸T神功**不会读取** AI 聊天内容，也不会读取私有 Webview。它通过本地命令输出、workspace 规则和 `.xit` 运行记录帮助 AI coding workflow 降噪。

## 设置

| 设置 | 默认值 | 说明 |
|---------|---------|-------------|
| `xit.binaryPath` | `""` | `xit` 二进制文件路径。为空时自动检测。 |
| `xit.home` | `""` | XiT 主目录。默认为 `~/.xit`。 |
| `xit.refreshInterval` | `5` | 状态栏刷新间隔（秒）。 |
| `xit.enableStatusBar` | `true` | 显示吸T神功状态栏。 |
| `xit.enableTerminalListener` | `true` | 监听 VS Code 终端命令执行，对高输出命令记录本地元数据并辅助计算 XiT 路由命中率。 |
| `xit.showActiveAiSurface` | `true` | 在 tooltip / Dashboard 显示最近的本地 XiT 工具来源记录。 |

## 隐私

- 无遥测
- 无网络请求
- 只读取本地 `~/.xit` 和工作区 `.xit` 目录
- 原始日志仅在手动触发命令时打开
- 终端监听器只捕获命令元数据（命令行、工作目录、终端名称），**不捕获命令输出或环境变量**
- 只展示本地 XiT 事件与终端记录，**不读取聊天内容、私有 Webview 或当前对话上下文**

## 从 VSIX 安装

如不从应用商店安装：

```bash
npx vsce package
```

然后在 VS Code / Cursor 中：

1. 命令面板 → `Extensions: Install from VSIX...`
2. 选择 `xit-vscode-*.vsix`
3. 重新加载窗口
