# XiT v0.2.47 / VS Code Extension v0.0.33 — VS Code AI Bridge

## Highlights

- **VS Code AI Bridge for Codex Chat**：Codex 在 VS Code 面板内调用 `xit auto` 时，状态栏与 Dashboard 实时联动。
- **VS Code Claude Code 面板支持**：通过 deny + 推荐 `xit auto` 命令的重试流程接入（Claude Code 协议不支持命令改写，只能 deny/ask/allow）。
- **Dashboard "本轮发功" 四卡片**：本次省 / 本轮共吸 / 降噪率 / 状态，状态值不再提前显示最终数据。
- **状态时序修正**：运行中 → 收功中 → 最终结果。`run.finished`（工具执行完）不再等同于"本轮交互完成"——最终数字只在 AI 给出结束语（或安全 fallback）之后才一次性展示。
- **最终结果停留时间延长**：状态栏 `本次省` 结果停留 ≥20 秒；Dashboard 的最终结果会保留到下一轮开始或 VS Code reload，不再随状态栏一起提前清空。

---

## Added

- VS Code Bridge 事件协议扩展 `turn.started` / `turn.finished`（AI 回合级信号，与原有 `run.started`/`run.finished` 工具级信号区分）。
  - Codex：复用已安装的 `UserPromptSubmit`/`Stop` hook 触发。
  - Claude Code：能力已实现（`xit claude-hook userpromptsubmit` / `stop`），但**未写入默认安装**——VS Code Claude 面板是否稳定触发这两个 hook 尚未验证，详见 `docs/claude.md`。未启用时走保守 fallback（6–8 秒）。
- Dashboard 新状态：`守护中`（AI 思考中，无工具调用）、`收功中`（工具已完成，AI 尚未给出结束语）。

## Changed

- Token 节省统一展示为 `约 X.XXk Token` 格式（CLI 状态栏 / Kimi toolbar / VS Code 一致）。
- VS Code 状态栏文案时序：`正在守护` → `正在吸T` → `神功正在收工` → `本次省 约 X.XXk Token`。
- 修正错别字：Dashboard 状态卡片 `收工中` → `收功中`（产品名为"吸T神功"）。
- Dashboard 运行中/收功中阶段 `本轮共吸` 显示 `统计中`（不再显示 `—`，也不提前显示真实次数）。
- VS Code 扩展描述文案移除未经验证的"支持 Claude/Codex/Gemini/Cursor"表述，改为客观描述功能本身。
- OpenCode 工具结果输出改为更简洁的两行文案，并修正 turn 计数。

## Fixed

- VS Code Bridge 的 run 匹配与 workspace 路径归一化（symlink 解析）问题。
- Codex footer 的回合计数在特定 loop-prevention 场景下的边界修复。

---

## Install

```bash
npm i -g xitsg
```

```bash
xit --version        # 0.2.47
```

VS Code 扩展：通过 VSIX 安装（暂未上架 Marketplace）。

---

## Known Limitations

- Claude Code 在 VS Code 面板内的 `turn.started`/`turn.finished`（Stop hook）触发可靠性尚未经真实面板验证；当前默认依赖 fallback 计时器展示最终结果。
- npm 包 `vendor/` 下的各平台二进制需要在发布前重新交叉编译，确保版本号与 `package.json` 一致（v0.2.43 曾因此发过一次 hotfix）。

---

## Safety

- No telemetry
- raw_log stays local（`.xit/runs/`）
- history stays local（`.xit/history.jsonl`）
- hook events stay local（`~/.xit/*-hooks/events.jsonl`）
- VS Code Bridge 事件不保存 prompt / assistant reply / raw command / raw output / raw cwd / 完整 session id / token / secret，仅保存 schema 字段 + 哈希值
- 所有 token 节省统计是本地估算（`saved_bytes / 4`）
