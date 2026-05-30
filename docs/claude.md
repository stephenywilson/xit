# Claude Code 实战适配

Claude Code 是 XiT 的下一套 AI CLI 适配目标。

## 当前阶段

| 功能 | 状态 |
|------|------|
| project-scope PreToolUse Bash hook | ✅ 已可用 |
| observe mode（记录命中率，不拦截） | ✅ 已启用 |
| CLAUDE.md rules | ✅ 已写入 |
| reroute / strict 强制拦截 | ❌ 暂不启用 |
| shim / takeover | ❌ 暂不启用 |
| 状态栏 | ❌ 暂不启用 |

## 初始化

在项目内安装 Claude hook（project scope）：

```bash
xit init claude --method official_hook --scope project --yes
```

确保 observe 模式（不强制拦截）：

```bash
xit hook disable-reroute claude --yes
```

查看状态：

```bash
xit hook status claude
```

查看统计：

```bash
xit hook stats claude
```

## 推荐用法

高噪音命令（交给 `xit auto`）：

```bash
xit auto go test -v ./...
xit auto git diff
xit auto rg "panic|error|TODO" .
```

短命令直接执行：

```bash
git status
pwd
go version
```

## 技术说明

Claude Code 使用 `~/.claude/settings.json`（全局）或 `.claude/settings.json`（项目级）配置 hooks。

XiT 注入的 hook 格式：

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [{"type": "command", "command": "~/.xit/hooks/claude-pretooluse-bash.sh"}]
      }
    ]
  }
}
```

hook 脚本通过 `exec xit claude-hook pretooluse-bash` 调用 XiT 内置处理器，读取 PreToolUse 事件，记录到 `.xit/claude-hooks/events.jsonl`。

observe 模式：只记录，不阻断。`fail_open: true` 保证任何异常都直接放行。

## 当前边界

Claude Code 第一阶段目标：

1. 让 Claude Code 主动使用 `xit auto`（靠 CLAUDE.md rules）
2. observe hook 在后台记录命中率
3. 用 `xit hook stats claude` 评估 missed / wrapped 情况
4. 确认稳定后，再考虑 reroute

不做：状态栏、强制拦截、TUI takeover、全局 hook 注入。
