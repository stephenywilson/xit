# Codex CLI 实战适配

Codex CLI 是 XiT 的下一套 AI CLI 适配对象。

当前阶段：

- `AGENTS.md` rules：已建立
- Codex hook：暂未启用
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

Codex Phase D2 只使用 `AGENTS.md` rules，不启用 hooks。

后续会继续审计 Codex hooks，重点确认是否存在 shell-command 前置事件。如果 hook 只能看到 `SessionStart`、`Stop`、`UserPromptSubmit`，则不能用于命令命中率统计。
