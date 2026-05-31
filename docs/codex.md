# Codex CLI 实战适配

Codex CLI 是 XiT 的下一套 AI CLI 适配对象。

当前阶段：

- `AGENTS.md` rules：已建立
- Codex hook observe：✅ 已启用
- Codex hitrate 审计：✅ 已可用
- Codex statusLine：暂未发现官方入口
- reroute / strict：暂不启用
- shim / takeover：暂不启用

## 安装 Codex CLI

```bash
npm i -g @openai/codex
```

启动：

```bash
cd /Users/dongjiayang/projects/xit
codex
```

## XiT 规则

Codex 会读取仓库根目录的 `AGENTS.md`。本项目要求：

- 高噪音命令使用 `xit auto`
- 短命令直接执行
- 不粘贴 raw output
- 报告中保留 `exit_code`、`reduction`、`saved_tokens`、`raw_log`

## 安装 Codex Hook

Codex CLI 使用项目级 `.codex/hooks.json` 配置 hooks。XiT 支持 observe 模式（只记录，不拦截）。

```bash
xit hook install codex --scope project --yes
```

查看状态：

```bash
xit hook status codex
```

卸载：

```bash
xit hook uninstall codex --scope project --yes
```

## Hook 统计

```bash
xit hook stats codex
```

输出示例：

```
XiT Codex Hook Stats

events:      12
observed:    10
passthrough: 1
errors:      1
```

事件记录在 `~/.xit/codex-hooks/events.jsonl`。

## 命中率审计

```bash
xit hook hitrate codex              # 最近 2 小时
xit hook hitrate codex --last 10m   # 最近 10 分钟
xit hook hitrate codex --json       # JSON 格式
```

判定标准：

| 指标 | 目标 |
|------|------|
| compress_recall | ≥ 90% |
| passthrough_precision | ≥ 98% |
| verdict | pass |

`correct_wrapped` = 高噪音命令被正确包裹  
`missed` = 漏掉的高噪音命令（需强化 AGENTS.md rules）  
`correct_passthrough` = 短命令正确直通  
`false_positive` = 短命令被错误包裹

## 推荐测试

短命令：

```bash
git status
```

高噪音命令：

```bash
xit auto go test -v ./...
xit auto rg "TODO|FIXME|panic|error" .
```

## 当前边界

Codex Phase D5 启用 hooks，但仅限 observe 模式：

- ✅ PreToolUse Bash hook 已安装到 `.codex/hooks.json`
- ✅ 记录命令分类到 `events.jsonl`
- ✅ `xit hook hitrate codex` 验证命中率
- ❌ 不启用 reroute / deny（Codex 暂无官方 statusLine）
- ❌ 不修改 `~/.codex/config.toml`
- ❌ 不安装全局 hook（仅 project scope）

hook 脚本内容：

```sh
#!/bin/sh
# XiT managed Codex hook
# event: PreToolUse
# matcher: Bash
exec xit codex-hook pretooluse-bash
```

XiT 处理器读取 Codex 发来的 JSON payload，使用 `filters.ClassifyPolicy()` 分类命令，记录事件后返回 `{"decision": "allow"}`（fail-open）。

下一步：持续观察 Codex CLI 更新，待官方支持 statusLine 或持久化底部栏后再评估集成。
