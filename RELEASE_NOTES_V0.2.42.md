# XiT v0.2.42 — Multi-AI CLI Adaptation Milestone

## Highlights

- **Multi-AI CLI 适配完成**：Kimi、Claude Code、Antigravity CLI、Codex CLI 四路打通
- **Kimi CLI**：status patch + turn lifecycle + toolbar refinements
- **Claude Code**：observe hook + hitrate + command-backed statusLine
- **Antigravity CLI**：rules dogfood + command-backed statusLine + autostate
- **Codex CLI**：AGENTS.md rules + PreToolUse observe hook + hitrate
- **`xit auto` summary 增强**：明确输出 `saved_tokens` 字段
- **Fixed Kimi time-window tests**：消除时间窗口过期导致的测试失败
- **Privacy**：所有 hook 事件仅写入本地，无遥测/不上传

---

## Multi-AI CLI Adaptation Matrix

| AI CLI | Status | Capabilities |
|--------|--------|-------------|
| Kimi CLI | done | status patch, turn lifecycle, toolbar, raw_log, hitrate, session |
| Claude Code | done | CLAUDE.md rules, observe hook, hitrate, command-backed statusLine |
| Antigravity CLI | done | rules dogfood, command-backed statusLine, autostate |
| Codex CLI | done | AGENTS.md rules, PreToolUse hook observe, hook stats, hitrate |

### Known Limitation

- **Codex CLI does not currently support command-backed bottom statusLine**. Codex 的 `/statusline` 为内置功能，暂无通过命令注入底部栏的官方接口。XiT 已启用 PreToolUse hook observe 和 hitrate 审计作为替代。

---

## `xit auto` Summary Polish

`xit auto` 现在会在摘要中明确输出 `saved_tokens`：

```
XiT Auto Summary
command: go test -v ./...
exit_code: 0
estimated_reduction: 99%
saved_tokens: ~9k
raw_log: .xit/runs/...
```

Token 估算方法：`saved_bytes / 4`（本地估算，非 tokenizer 精确计数）。

---

## Codex CLI Hook — Silent Fail-Open

Codex PreToolUse hook 采用静默成功策略：

- exit code 0 + empty stdout = success / continue
- 不输出任何 JSON 到 stdout（避免 Codex UI 报 "invalid pre-tool-use JSON output"）
- 所有事件记录到本地 `~/.xit/codex-hooks/events.jsonl`

---

## Install

```bash
npm i -g xitsg
```

```bash
xit --version        # 0.2.42
xit auto --help
```

---

## Safety

- No telemetry
- raw_log stays local（`.xit/runs/`）
- history stays local（`.xit/history.jsonl`）
- hook events stay local（`~/.xit/*-hooks/events.jsonl`）
- status patch is opt-in，支持 uninstall 回滚
- 所有 token 节省统计是本地估算（`saved_bytes / 4`）
