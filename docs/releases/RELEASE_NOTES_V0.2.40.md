# XiT v0.2.40 — Kimi CLI 适配、npm 安装、本地上下文压缩

## Highlights

- **npm 一行安装**：`npm i -g xitsg`，安装后命令是 `xit`
- **多平台 binary**：darwin-arm64 / darwin-amd64 / linux-amd64 / linux-arm64 / win32-amd64
- **Kimi CLI functional prototype 已完成**：rules mode + hook observe + status patch
- **`xit auto` 命令输出压缩**：支持 go test、git diff、grep、npm test 等
- **raw_log 本地留证**：每次命令完整保存到 `.xit/runs/`
- **Kimi rules / hook observe / turn lifecycle** 全链路支持
- **Kimi 中文状态栏**（实验性 monkey patch，opt-in）
- **Token 展示**：`saved_tokens = saved_bytes / 4` estimate（本地估算）
- **routing hitrate / context impact / session metrics** CLI 诊断

---

## Install

```bash
npm i -g xitsg
```

> npm 包名 `xitsg`，因为 `xit` 在 npmjs.org 上已被他人占用。安装后的命令仍然是 `xit`。

---

## Usage

```bash
xit --version
xit auto go test -v ./...
xit auto git diff
xit gain
xit doctor
```

---

## Kimi Setup

```bash
xit init kimi --method official_hook --scope user --yes
xit kimi rules install --scope user --yes
```

### Optional Kimi Toolbar

```bash
xit kimi status-patch install --yes --accept-risk
```

---

## Current AI CLI Support

| AI CLI      | 状态                          |
|-------------|-------------------------------|
| Kimi CLI    | 已适配 / functional prototype  |
| Claude Code | 适配中                         |
| Codex       | 规划中                         |
| Cursor      | 规划中                         |

---

## Safety

- No telemetry
- raw_log stays local（`.xit/runs/`）
- history stays local（`.xit/history.jsonl`）
- status patch is opt-in（`--yes --accept-risk`），支持 `uninstall` 回滚
- 所有 token 节省统计是本地估算（`saved_bytes / 4`）

---

## Notes

- npm 包名是 `xitsg`，因为 npm 上 `xit` 已被占用
- 安装后的命令仍然是 `xit`，用户使用体验不变
- Kimi bottom toolbar 是 experimental monkey patch，Kimi 更新后可能失效
- Claude Code / Codex / Cursor 适配正在推进中，尚未完成
