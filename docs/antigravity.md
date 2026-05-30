# Antigravity CLI 实战适配

Antigravity CLI 是 XiT 的 AI CLI 适配对象之一。

当前阶段：

- Antigravity CLI 版本：1.0.3
- 命令：agy
- --print / headless 模式：已验证
- rules dogfood：已跑通
- hook：暂未发现官方入口，暂不启用
- statusLine：✅ 官方原型已实现
- plugin：已发现 plugin 子命令，暂不接入
- reroute / strict：暂不启用
- shim / takeover：暂不启用

## 环境确认

```bash
which agy
agy --version
agy --help
```

正确命中应为：

```text
/Users/dongjiayang/.local/bin/agy
1.0.3
```

注意：如果本机同时安装了 Antigravity Electron IDE，可能存在另一个 agy launcher。必须确保 $HOME/.local/bin 优先于 Electron IDE 路径。

## XiT 规则

Antigravity CLI 已能理解本项目规则：

- 短命令直接执行
- 高噪音命令使用 xit auto
- 不粘贴 raw output
- 保留 exit_code、reduction、saved_tokens、raw_log

## 已验证 dogfood

短命令：

```bash
git status
```

结果：未使用 xit auto，符合预期。

高噪音命令：

```bash
go test -v ./...
```

结果：Antigravity 使用了：

```bash
xit auto go test -v ./...
```

并返回：

- exit_code: 0
- reduction: 99%
- raw_log: .xit/runs/...raw.log
- rules compliance: none missed

## 推荐测试

```bash
agy -p '请严格按本项目规则执行一次 XiT / 吸T神功 Antigravity dogfood 测试。

要求：
1. 只读操作，不要修改任何文件。
2. 先执行短命令 git status，不要使用 xit auto。
3. 然后执行 go test -v ./...，这是高噪音命令，必须使用 xit auto。
4. 不要粘贴 raw output。
5. 最终报告不超过 20 行。
6. 报告必须包含每个命令是否使用 xit auto、exit_code、reduction、saved_tokens、raw_log、key facts。
7. 如果你没有遵守规则，请明确说明 missed。
' --print-timeout 5m
```

## 官方 statusLine

Antigravity CLI 支持官方 `statusLine` API，在底部状态栏显示自定义文本。

输出示例：
```
吸T神功 · Antigravity · 命中率100%
吸T神功 · 本次省9k · 命中率100%
吸T神功 · Antigravity · 准备就绪
```

颜色：暗金 `\033[38;5;178m`（256-color gold）

### 安装

```bash
xit antigravity statusline install --yes
```

写入 `~/.gemini/antigravity-cli/settings.json`：

```json
{
  "statusLine": {
    "type": "command",
    "command": "xit antigravity statusline",
    "padding": 0
  }
}
```

### 命令

```bash
xit antigravity statusline                              # 输出一行状态文本（带颜色）
NO_COLOR=1 xit antigravity statusline                  # 无颜色纯文本
xit antigravity statusline --json                       # JSON 格式
xit antigravity statusline install --yes               # 安装 statusLine
xit antigravity statusline status                       # 查看配置状态
xit antigravity statusline uninstall --yes             # 移除 statusLine
```

### 状态优先级

| 条件 | 输出 |
|------|------|
| 最近 10min 有 xit auto history + verdict pass | `吸T神功 · 本次省Xk · 命中率Y%` |
| 最近 10min 有 xit auto history | `吸T神功 · Antigravity · 命中率Y%` |
| 无任何数据 | `吸T神功 · Antigravity · 待观测` |

fail-open：任何异常均输出 `待观测`，不 panic，不输出多行。

## 当前边界

Antigravity 当前只确认 rules / headless dogfood / statusLine prototype 跑通。

暂不声称：

- hook 已适配
- statusLine 已适配
- plugin 已适配
- reroute 已启用
- strict 已启用
