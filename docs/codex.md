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

## 最终回答 footer

一个用户 prompt 是一轮。同一轮里可以执行多次 `xit auto`。

当本轮至少执行过一次 Codex `xit auto` 时，最终 assistant 回答末尾追加一次：

```text
吸T神功 · Codex · 守护你的T
本次省 约 18.53k Token · 本轮共吸 2次
```

小于 1000 Token 时不使用 k：

```text
吸T神功 · Codex · 守护你的T
本次省 841 Token · 本轮共吸 1次
```

未执行 `xit auto` 的轮次不显示 footer。

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
状态在 footer 确认出现或 fail-open 后清理，陈旧状态会自动过期。

## Hook 行为

`UserPromptSubmit` 初始化新 turn；同一 `session_id + turn_id` 的重复事件不清空已有累计。
Stop continuation 使用 `[XIT_CODEX_FOOTER_CONTINUATION]` marker，避免把内部续写当成新 turn。

`PreToolUse` 在 Bash 命令包含 `xit auto` 或可安全重写为 `xit auto` 时注入：

```bash
XIT_ADAPTER=codex XIT_CODEX_SESSION_ID='...' XIT_CODEX_TURN_ID='...'
```

`PostToolUse` 只记录生命周期事件，stdout 保持为空；不会返回
`hookSpecificOutput.additionalContext`，避免 Codex UI 在工具卡片后显示 hook context。

`Stop` 在最终回答缺 footer 时只 block 一次，要求 Codex 续写同一回答的 footer；如果
`stop_hook_active=true` 或本 turn 已经续写过，则 fail-open，避免循环。
