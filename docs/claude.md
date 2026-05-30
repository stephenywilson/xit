# Claude Code 实战适配

Claude Code 是 XiT 的下一套 AI CLI 适配目标。

## 当前阶段

| 功能 | 状态 |
|------|------|
| project-scope PreToolUse Bash hook | ✅ 已可用 |
| observe mode（记录命中率，不拦截） | ✅ 已启用 |
| CLAUDE.md rules | ✅ 已写入 |
| routing hitrate 审计（`xit hook hitrate claude`） | ✅ 已验证 |
| 官方 statusLine 原型（`xit claude statusline`） | ✅ 已实现 |
| reroute / strict 强制拦截 | ❌ 暂不启用 |
| shim / takeover | ❌ 暂不启用 |

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

## 命中率审计

```bash
xit hook hitrate claude              # 查看最近 2h 命中率
xit hook hitrate claude --last 10m   # 查看最近 10 分钟
xit hook hitrate claude --json       # JSON 格式输出
```

判定标准：

| 指标 | 目标 |
|------|------|
| compress_recall | ≥ 90% |
| passthrough_precision | ≥ 98% |
| verdict | pass |

`correct_wrapped` = 高噪音命令被正确包裹  
`missed` = 漏掉的高噪音命令（需强化 CLAUDE.md rules）  
`correct_passthrough` = 短命令正确直通  
`false_positive` = 短命令被错误包裹

## 官方 statusLine

Claude Code 支持官方 `statusLine` API，在底部状态栏显示自定义文本。

输出示例：
```
吸T神功 · Claude observe · 命中率100%
吸T神功 · 本次省9k · 命中率100%
吸T神功 · Claude observe · 准备就绪
```

颜色：暗金 `\033[38;5;178m`（256-color gold）

### 安装

```bash
xit claude statusline install --scope project-local --yes
```

写入 `.claude/settings.local.json`（gitignored，仅本地生效）：

```json
{
  "statusLine": {
    "type": "command",
    "command": "xit claude statusline",
    "padding": 0
  }
}
```

### 命令

```bash
xit claude statusline                              # 输出一行状态文本（带颜色）
NO_COLOR=1 xit claude statusline                  # 无颜色纯文本
xit claude statusline --json                       # JSON 格式
xit claude statusline install --scope project-local --yes
xit claude statusline status                       # 查看配置状态
xit claude statusline uninstall --yes             # 移除 statusLine
```

### 状态优先级

| 条件 | 输出 |
|------|------|
| 最近 10min 有 hook events + verdict pass | `吸T神功 · Claude observe · 命中率X%` |
| 最近 10min 有 xit auto history | `吸T神功 · 本次省Xk · 命中率Y%` |
| hook installed 但无数据 | `吸T神功 · Claude observe · 准备就绪` |
| 无法判断 | `吸T神功 · Claude observe · 待观测` |

fail-open：任何异常均输出 `待观测`，不 panic，不输出多行。

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

hook 脚本通过 `exec xit claude-hook pretooluse-bash` 调用 XiT 内置处理器，读取 PreToolUse 事件，记录到 `~/.xit/claude-hooks/events.jsonl`。

observe 模式：只记录，不阻断。`fail_open: true` 保证任何异常都直接放行。

statusLine 写入 `.claude/settings.local.json`（project-local），不修改全局 `~/.claude/settings.json`。

## 当前边界

Claude Code 适配进度：

1. ✅ Claude Code 主动使用 `xit auto`（靠 CLAUDE.md rules）
2. ✅ observe hook 在后台记录命中率
3. ✅ `xit hook hitrate claude` 验证路由命中率（100%）
4. ✅ 官方 statusLine 原型（`xit claude statusline`）
5. 下一步：live 验证 statusLine 显示，再考虑 user scope 文档化

不做：强制拦截、TUI takeover、全局 hook 注入。
