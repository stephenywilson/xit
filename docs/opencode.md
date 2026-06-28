# OpenCode CLI 适配

XiT 的 OpenCode 适配使用项目级 `.opencode/plugins/xit.ts`。

## 展示模型

OpenCode 属于工具结果型 CLI。

OpenCode 1.16.2 的 `experimental.text.complete` 不能可靠保证最终 footer 在 TUI
中可见，因此 XiT 不依赖最终 assistant answer 修改，也不使用 continuation。

每次 `xit auto` 工具结果显示：

```text
吸T神功 · OpenCode · 守护你的T
本次省 约 10.24k Token · 本轮共吸 2次
```

小于 1000 Token：

```text
吸T神功 · OpenCode · 守护你的T
本次省 841 Token · 本轮共吸 1次
```

`本次省` 是当前这一次命令的真实 saved token；`本轮共吸` 是当前用户 turn 内真实 `xit auto` 次数。

失败命令先保留真实诊断，再显示两行 XiT：

```text
internal/opencode_test.go:42: expected 3, got 2

吸T神功 · OpenCode · 守护你的T
本次省 约 10.24k Token · 本轮共吸 2次
```

没有可靠 turn key 时，不显示假计数：

```text
吸T神功 · OpenCode · 守护你的T
本次省 841 Token
```

## Turn 边界

OpenCode 1.16.2 的 `tool.execute.before/after` payload 只有 `sessionID` 和
`callID`，不能单独表示用户轮次。XiT 的 turn 边界由插件内状态机维护：

- `chat.message role=user`：打开新的真实用户 turn。
- 同一个 user message 的重复事件：不生成新 key，不重置计数。
- `message.updated role=user`：只在当前 session 没有 active turn 时用于恢复缺失的 turn。
- active turn 已存在时，`message.updated role=user` 不覆盖当前 key，避免同一 prompt 内部消息更新把一轮拆成多轮。
- `session.idle`：关闭插件内 active turn 映射，不删除历史 turn state，不展示 footer，不发送 continuation。

Turn key 使用：

```text
sha256(sessionID + "\x00" + user message id)[0:24]
```

完整 `sessionID` 和 `message.id` 只在插件内存中用于计算 turn key，不写入命令、日志或 turn state。

## 环境注入

插件使用 OpenCode `shell.env` hook 按 `sessionID/callID` 注入：

```text
XIT_ADAPTER=opencode
XIT_OPENCODE_TURN_KEY=<opaque>
```

用户可见命令保持为：

```text
xit auto ...
```

如果没有可靠 active turn key，XiT 不会复用上一轮 key。

## 状态文件

OpenCode turn state 位于：

```text
~/.xit/state/opencode-turns/*.json
```

内容不保存 prompt、assistant reply、原始 OpenCode ID、raw output 或密钥，只保存：

```json
{
  "turn_key": "<opaque>",
  "run_count": 2,
  "saved_tokens_total": 20530,
  "updated_at": "..."
}
```
