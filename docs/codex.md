# Codex CLI 适配

Codex CLI 没有 XiT 可控制的动态底部状态栏。Codex 适配不模仿
Antigravity / Claude 的状态栏，而是使用官方 hooks 做一轮内的统计累计。

## 安装

```bash
xit init codex --method official_hook --yes
```

安装目标是项目级 `.codex/hooks.json`，包含四个 Codex 生命周期 hooks：

- `UserPromptSubmit`
- `PreToolUse`，matcher 为 `^Bash$`
- `PostToolUse`，matcher 为 `^Bash$`
- `Stop`

安装或更新后，Codex 会要求重新 review / trust hooks。进入 Codex 后运行：

```text
/hooks
```

然后 trust XiT 的 `UserPromptSubmit`、`PreToolUse`、`PostToolUse` 和 `Stop`。

## 工具卡片

每次 `xit auto` 的工具输出只显示压缩后的有效诊断内容。

如果没有值得展开的诊断，输出：

```text
命令执行成功，无需展开重复输出。
```

工具卡片不显示 XiT footer、token 统计、raw log 路径或机器字段。

## ChatGPT / Codex App UI limitation

一个用户 prompt 是一轮。同一轮里可以执行多次 `xit auto`。

当前 ChatGPT Desktop / Codex App 的 Stop Hook 没有已验证的“只显示给用户、但不进入
模型 continuation”的 footer 通道：

- `decision:block` + `reason` 能在 UI 中显示，但会作为 continuation prompt 交给模型，
  可能造成最终回复复述摘要，并产生额外 turn。
- `systemMessage` 是官方 Codex Hook common output 字段，但在当前 ChatGPT/Codex App
  Stop Hook UI 验收中没有显示摘要。

因此 XiT 默认不再通过 Stop Hook 输出本轮 footer。每次真正压缩后的工具输出仍保留
低噪音状态行：

```text
XiT · auto · 48.2 KB → 4.7 KB · saved ~10.9k tokens · exit 0
```

本轮累计统计仍保存在 turn state 中，供测试、状态页或后续确认独立 UI 通道后使用；
最终 assistant 回复不复制或改写 XiT 摘要。聚合指标通过 Dashboard 查看；
`xit telemetry status` 用于确认匿名聚合 telemetry 的开关状态。Stop Hook 不负责展示
本轮 footer。

## Turn State

Codex turn state 只保存计数和标识，位置为：

```text
<XIT_HOME>/state/codex-turns/<safe-session-id>/<safe-turn-id>.json
```

内容边界：

```json
{
  "session_id": "...",
  "turn_id": "...",
  "run_count": 2,
  "saved_tokens_total": 18532,
  "footer_continuation_used": false,
  "updated_at": "..."
}
```

不会保存用户完整 prompt、原始命令输出、raw log、transcript 内容或密钥。
状态在 Stop 默认关闭 turn 或 legacy fail-open 后清理，陈旧状态会自动过期。

## Hook 行为

`UserPromptSubmit` 初始化新 turn；同一 `session_id + turn_id` 的重复事件不清空已有累计。
Stop continuation 使用 `[XIT_CODEX_FOOTER_CONTINUATION]` marker，避免把内部续写当成新 turn。
该字段只用于兼容 0.2.51 及更早版本遗留的 `decision:block` 状态；当前 Stop
footer 不再创建 continuation。

`PreToolUse` 在 Bash 命令包含 `xit auto` 或可安全重写为 `xit auto` 时注入：

```bash
XIT_ADAPTER=codex XIT_CODEX_SESSION_ID='...' XIT_CODEX_TURN_ID='...'
```

`PostToolUse` 只记录生命周期事件，stdout 保持为空；不会返回
`hookSpecificOutput.additionalContext`，避免 Codex UI 在工具卡片后显示 hook context。

`Stop` 默认返回：

```json
{}
```

不会返回 `decision:block` 或 `reason`，也不会返回 `systemMessage` 或
`hookSpecificOutput.additionalContext`。这样不会创建 continuation prompt，也不会把
不可见摘要静默吞掉。`stop_hook_active=true` 或旧状态里已经标记过 continuation 的轮次
同样 fail-open 返回 `{}`，避免循环和重复显示。
