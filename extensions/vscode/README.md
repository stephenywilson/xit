# 吸T神功（XiT）

吸T神功（XiT）是一个帮 AI 编程工作流省 token、减少噪音、提升命中率的 VS Code 插件。

当 `go test -v ./...`、`npm test`、`docker logs`、`tsc`、`eslint` 这类命令输出太长时，吸T神功会先对高噪音输出进行压缩，避免 AI 编程工作流的上下文被无效输出占满。

## 核心价值

- **省 token** — 把高噪音命令输出压缩后再交给 AI
- **减少噪音上下文** — 只保留关键信息
- **提升 AI 回答命中率** — 噪音越少，AI 越容易给出准确答案
- **Codex Chat Bridge** — 在 VS Code 内的 Codex 对话框中运行高噪音命令时，可自动路由到 `xit auto`（其余 AI 工具需通过 `XiT: Run Command` 等命令面板手动触发）
- **本地处理** — 终端原始输出、prompt、AI 回复、命令文本、路径全部留在本地，永不上传
- **匿名统计（默认开启，可关闭）** — 仅 adapter 类型、版本、字节数、压缩率、估算节省 Token；遵循 VS Code 全局 `telemetry.telemetryLevel`，关闭即不发送，也可设 `xit.telemetry` 为 `off`

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

| 命令                              | 作用                                                                               |
| --------------------------------- | ---------------------------------------------------------------------------------- |
| `XiT: Run Command`                | 检测命令是否高输出，如果是则用 `xit auto` 运行                                     |
| `XiT: Run with Auto Compression`  | 始终用 `xit auto` 运行命令                                                         |
| `XiT: Open XiT Terminal`          | 打开名为 "XiT" 的专用终端                                                          |
| `XiT: Open Dashboard`             | 打开产品化 Dashboard，首屏展示状态、Latest Saved、Today Saved、Workspace Total、Adapter Health |
| `XiT: Refresh`                    | 刷新状态栏                                                                         |
| `XiT: Show Gain`                  | 弹出累计节省摘要                                                                   |
| `XiT: Open Latest Raw Log`        | 打开工作区 `.xit/runs/` 中最新的原始日志                                           |
| `XiT: Show Output Channel`        | 显示 XiT 扩展调试输出                                                              |
| `XiT: Install Workspace AI Rules` | 在当前 workspace 的 AI 规则文件里幂等写入 XiT 命令输出规则                         |
| `XiT: Diagnose AI Workflow`       | 输出当前 workspace 的 XiT CLI、最近 run、saved bytes、rules、routing hit rate 报告 |
| `XiT: Verify AI Agent Routing`    | 验证本地 AI agent 规则文件和最近高噪音命令是否真的通过 XiT 路由                    |

## 状态栏含义

| 状态栏                   | 含义                            |
| ------------------------ | ------------------------------- |
| `吸T神功 · 准备就绪`     | 插件已启动，XiT CLI 可用，当前没有 VS Code 主动触发的任务 |
| `吸T神功 · 正在吸T`      | 用户通过 VS Code 命令面板主动触发了真实 `xit auto` |
| `吸T神功 · 神功正在收工` | 终端执行结束，正在等待本次 state/history 写入 |
| `吸T神功 · 本次省 约 9.00k Token` | 本次 VS Code 主动任务已匹配到真实完成记录 |
| `吸T神功 · 本次无需发功` | 用户选择直接运行，或本次命令确认无需压缩 |
| `吸T神功 · 执行失败`     | 本次 VS Code 主动任务失败 |
| `吸T神功 · 未接入`       | 没找到本地 XiT CLI |

鼠标悬停在状态栏上可查看更多信息。状态栏只表示 VS Code 主动命令的即时状态，不再用旧 adapter 事件冒充当前任务。

XiT CLI 在运行 `xit auto` 时还会把当前 active run 写到工作区 `.xit/state/current-run.json`（兼容保留 `.xit/state/current.json`）。扩展优先监听这个 state 文件来感知 running / completed 状态，再用 `.xit/history.jsonl` 做完成态与历史回放兜底。

VS Code 扩展是宿主 UI 型集成：状态栏展示当前 VS Code 主动命令，Dashboard 展示本次、今日和累计统计，Output Channel 提供诊断。扩展仍可读取本地 hook metadata 做诊断，但这些旧事件不会驱动状态栏瞬时状态。

## Dashboard 0.0.17

深色工具风 Dashboard，黑金 accent，本地渲染，不读取聊天内容。默认首屏显示：

- 本次发功
- 今日功力
- 功力累计

没有真实 run 时（或 state 文件为空）显示温和空状态，不会展示测试命令。

**边界说明：** 吸T神功**不会读取** AI 聊天内容，也不会读取私有 Webview。它通过本地命令输出、workspace 规则和 `.xit` 运行记录帮助 AI coding workflow 降噪。

## How to verify Codex integration

1. 运行 `XiT: Install Workspace AI Rules`
2. 打开 Codex 面板
3. 让 Codex 正常运行项目测试，例如 “Run the project tests and follow workspace instructions.”
4. 不要手动输入 `./xit auto`
5. 然后运行 `XiT: Verify AI Agent Routing`
6. 只有当最近的高噪音命令是通过 XiT 执行时，才算 PASS

这个测试只验证 Codex 是否遵守 workspace rules，不验证 XiT 是否读取 Codex chat 内容。

## 设置

| 设置                         | 默认值 | 说明                                                                             |
| ---------------------------- | ------ | -------------------------------------------------------------------------------- |
| `xit.binaryPath`             | `""`   | `xit` 二进制文件路径。为空时自动检测。                                           |
| `xit.home`                   | `""`   | XiT 主目录。默认为 `~/.xit`。                                                    |
| `xit.refreshInterval`        | `5`    | 状态栏刷新间隔（秒）。                                                           |
| `xit.enableStatusBar`        | `true` | 显示吸T神功状态栏。                                                              |
| `xit.enableTerminalListener` | `true` | 监听 VS Code 终端命令执行，对高输出命令记录本地元数据并辅助计算 XiT 路由命中率。 |
| `xit.showActiveAiSurface`    | `true` | 在 tooltip / Dashboard 显示最近的本地 XiT 工具来源记录。                         |

## 隐私

- **永不上传**终端原始输出、prompt、AI 回复、命令文本、文件路径、用户名、邮箱、API key 或完整 session id
- **匿名使用统计**：仅 adapter 类型、版本、OS/arch、字节数、压缩率、估算节省 Token、成功/失败状态。默认开启、透明、可关闭
  - 遵循 VS Code 全局 `telemetry.telemetryLevel`：为 `off` 时插件不发送任何统计
  - 也可将设置 `xit.telemetry` 设为 `off`，或环境变量 `XIT_TELEMETRY=off`
  - 默认使用 `https://xit-api.stephenwilson.dev`；可用 `xit.apiBase` 或 `XIT_API_BASE` 覆盖
- 只读取本地 `~/.xit` 和工作区 `.xit` 目录
- 原始日志仅在手动触发命令时打开
- 终端监听器只捕获命令元数据（命令行、工作目录、终端名称），**不捕获命令输出或环境变量**
- 只展示本地 XiT 事件与终端记录，**不读取聊天内容、私有 Webview 或当前对话上下文**

详见仓库 [docs/telemetry.md](https://github.com/stephenywilson/xit/blob/main/docs/telemetry.md)。

## Token 估算说明

- 状态栏、tooltip 和 Dashboard 中的 token 数字默认是**估算值**
- 如果 XiT history / gain 已提供数值字段 `saved_tokens`，插件优先使用该字段
- 如果没有 `saved_tokens`，插件按 `saved_bytes / 4` 或 `(raw_bytes - summary_bytes) / 4` 做保守估算
- 旧记录里的 `saved_tokens_display` 只作为 schema 兼容字段保留，不作为新 UI 的优先来源
- 不同 AI 模型的 tokenizer 不同，所以 `约 10.00k Token` 应理解为本地近似统计，不是某个模型 API 的精确 billing 数字
- 这些 token 统计默认只保留在本地工作区和本地 XiT 数据目录，不会上传

## 从 VSIX 安装

如不从应用商店安装：

```bash
npx vsce package
```

然后在 VS Code / Cursor 中：

1. 命令面板 → `Extensions: Install from VSIX...`
2. 选择 `xit-vscode-*.vsix`
3. 重新加载窗口
