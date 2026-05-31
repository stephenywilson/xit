# XiT / 吸T神功 Aider CLI 适配

## 当前支持状态

Aider 适配为 **rules-only**。

| 能力 | 状态 | 说明 |
|------|------|------|
| Rules | 支持 | `.aider.conf.yml` + `XIT_AIDER.md` |
| Hooks | 不支持 | Aider 无 PreToolUse / observe hook 机制 |
| StatusLine | 不支持 | Aider 无 command-backed bottom statusLine 接口 |
| Hitrate | 不支持 | 无 hook 则无法统计 |

## 安装前提

Aider 需要独立安装。推荐通过 pipx + Python 3.12：

```bash
pipx install aider-chat --python /opt/homebrew/opt/python@3.12/bin/python3.12
```

验证：

```bash
aider --version
```

## XiT 安装命令

在当前项目安装 XiT Aider rules：

```bash
xit aider rules preview
xit aider rules install --scope project --yes
xit aider rules status --scope project
```

卸载：

```bash
xit aider rules uninstall --scope project --yes
```

## 安装产物

- `XIT_AIDER.md`：XiT 规则文件，告诉 Aider 何时使用 `xit auto`
- `.aider.conf.yml`：Aider 配置文件，包含 `read: [XIT_AIDER.md]`

`.aider.conf.yml` 采用 managed block 方式 merge，不会覆盖用户已有的 model / api / lint / test 配置。

## Dogfood 验证

安装后，在 Aider 会话中测试：

1. 让 Aider 执行 `git status`
   - 预期：Aider 直接执行，不使用 `xit auto`

2. 让 Aider 执行 `go test -v ./...`
   - 预期：Aider 使用 `xit auto go test -v ./...`

如果 Aider 没有使用 `xit auto`，检查：

```bash
xit aider rules status --scope project
```

确认 `installed: true` 且 `read_configured: true`。

## 限制说明

- Aider 不支持 command-backed statusLine
- Aider 不支持 PreToolUse hook
- XiT 的 Aider adapter 是纯粹的 rules-only 集成
- 本轮仅支持 project scope，不支持 user scope
