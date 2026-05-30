# XiT / 吸T神功

**吸走废 Token，炼成有效上下文。**

XiT 是一个本地 AI CLI 输出压缩工具，把 `go test`、`grep`、`git diff`、日志等高输出命令压缩成短摘要，同时保留 raw_log 证据，减少 AI coding CLI 的上下文消耗。

当前版本：**v0.2.40**

---

## 安装

```bash
npm i -g xitsg
```

> **为什么是 xitsg？** npm 上 `xit` 包名已被他人占用，因此发布为 `xitsg`（XiT Stephen G.）。安装后的命令仍然是 `xit`。

安装后验证：

```bash
xit --version
# => xit version 0.2.40
```

---

## 快速开始

```bash
# 压缩 go test 输出
xit auto go test -v ./...

# 压缩 git diff
xit auto git diff

# 压缩 grep 输出
xit auto grep -r "func" --include="*.go" .

# 查看诊断
xit doctor

# 查看历史统计
xit gain
```

---

## 为什么需要 XiT

AI coding agent 的上下文窗口有限且昂贵。运行 `git diff`、`npm test`、`docker logs` 等命令时，终端输出往往包含大量噪音（空行、进度条、重复日志、格式化符号等）。

XiT 在本地实时压缩这些输出，保留关键事实、风险文件、失败项和结构化摘要，让 AI 获得更高密度的有效上下文，同时原始日志完整保存在本地，随时可查。

Token 估算方式：`saved_tokens = saved_bytes / 4`（本地估算，非真实 tokenizer）。

---

## 当前 AI CLI 支持状态

| AI CLI      | 状态                          |
|-------------|-------------------------------|
| Kimi CLI    | 已适配 / functional prototype  |
| Claude Code | 适配中                         |
| Codex       | 规划中                         |
| Cursor      | 规划中                         |

> **注意**：当前重点适配 Kimi CLI。Claude Code hook 有初步集成，其他 AI CLI 正在规划和适配中，尚未全部完成。

---

## Kimi CLI 使用

### 初始化

```bash
# 安装 XiT rules（让 Kimi 主动使用 xit auto）
xit init kimi --method official_hook --scope user --yes
xit kimi rules install --scope user --yes
```

### 可选：Kimi 状态栏（实验性）

```bash
# 安装底部状态栏 patch（会修改本地 Kimi package）
xit kimi status-patch install --yes --accept-risk

# 卸载 patch，恢复原始 Kimi
xit kimi status-patch uninstall --yes
```

> **注意**：status patch 是可选高级功能，因为它会修改本地 Kimi package 文件。可通过 `uninstall` 回滚。

### 查看 Kimi 集成状态

```bash
xit doctor kimi --deep
# 或
xit kimi doctor
```

### 查看压缩统计

```bash
xit kimi benchmark
```

---

## 已完成能力

- `xit auto` 命令输出压缩（支持 go test、git diff、grep、npm test 等）
- raw_log 本地留证（`.xit/runs/<timestamp>.raw.log`）
- Kimi rules 安装（skill 注入系统提示）
- Kimi hook observe 模式（PreToolUse 事件记录）
- Kimi turn lifecycle 追踪
- Kimi 中文状态栏（实验性 monkey patch）
- routing hitrate / context impact / session metrics CLI 诊断
- Token 估算：`saved_tokens = saved_bytes / 4` estimate

---

## 常用命令

```bash
# 基础压缩
xit auto go test -v ./...
xit auto git diff
xit auto git log --oneline -20
xit auto grep -r "func" --include="*.go" .
xit auto npm test
xit auto docker logs <container>

# 查看历史统计
xit gain

# 诊断
xit doctor
xit doctor kimi --deep

# Kimi rules
xit kimi rules install --scope user --yes
xit kimi rules status --scope user
xit kimi rules uninstall --scope user --yes

# Kimi hook
xit hook status kimi --scope user
xit hook stats kimi

# Kimi status patch
xit kimi status-patch status
xit kimi status-patch dry-run
xit kimi status-patch install --yes --accept-risk
xit kimi status-patch uninstall --yes

# 压缩基准测试
xit bench compression
xit kimi benchmark
```

---

## 工作原理

1. **执行** — `xit auto <command>` 解析并运行你的原始命令
2. **捕获** — 记录 stdout、stderr、exit code、duration
3. **保存** — 原始输出完整写入 `.xit/runs/<timestamp>-<slug>.raw.log`
4. **过滤** — 根据命令类型选择专用 filter，提取关键信息
5. **输出** — 打印结构化 XiT Auto Summary，保留原命令 exit code
6. **统计** — 追加记录到 `.xit/history.jsonl`，用 `xit gain` 查看节省量

---

## 支持的命令类型

| 命令类型 | 压缩策略 |
|---------|---------|
| `go test` | exit code + pass/fail 统计 + 失败详情 |
| `git diff` | 变更文件数 / 高风险文件 / hunk 摘要 |
| `git log` | 最近 N 条 commit 一行一个 |
| `git status` | branch / staged-unstaged 统计 / 关键文件 |
| `grep` / `rg` | 按文件聚合 / 每文件最多 3 条 |
| `npm test` / `pytest` / `cargo test` | pass/fail 摘要 + 失败 stack top |
| `tsc` / `eslint` | 按文件聚合错误 |
| `docker logs` | 重复行去重 + 关键错误优先 |
| `find` / `ls` | 目录聚合 / 忽略 node_modules .git |

---

## Kimi CLI 详细文档

### Kimi Rules Mode（推荐）

Rules mode 直接教 Kimi 主动使用 `xit auto`，无需等被拦截：

```bash
# 安装（用户范围，跨所有项目生效）
xit kimi rules install --scope user --yes

# 检查状态
xit kimi rules status --scope user

# 验证规则是否生效（copy-paste 提示词）
xit kimi rules dogfood

# 卸载
xit kimi rules uninstall --scope user --yes
```

### Kimi Hook Observe Mode

```bash
# 安装 observe hook（只记录事件，不阻塞）
xit init kimi --method official_hook --scope user --yes

# 查看状态
xit hook status kimi --scope user

# 查看统计
xit hook stats kimi

# 卸载
xit uninstall kimi --method official_hook --scope user --yes
```

### Kimi Safe Reroute（可选）

```bash
# 启用 reroute（高输出命令 deny + 推荐 xit auto）
xit hook enable-reroute kimi --yes

# 禁用 reroute
xit hook disable-reroute kimi --yes
```

> **说明**：Kimi blocking reroute 会将 deny 显示为 Shell tool ERROR。Kimi 可能不会自动重跑 `xit auto <command>`，这由 Kimi 的决策逻辑决定。Rules mode 是更优雅的方案。

### Kimi Bottom Toolbar Patch（实验性）

直接修改本地 Kimi Python package，在底部状态栏显示 XiT 状态（吸T ON / 吸T神功 ON）。

```bash
# 检查可用性（只读）
xit kimi status-patch status

# 查看 patch 计划（不修改文件）
xit kimi status-patch dry-run

# 验证语法（temp copy，不改真实 Kimi）
xit kimi status-patch validate

# 安装（必须同时提供 --yes 和 --accept-risk）
xit kimi status-patch install --yes --accept-risk

# 卸载，从备份恢复
xit kimi status-patch uninstall --yes
```

> **警告**：这是 experimental monkey patch，会修改本机 Kimi 安装文件。Kimi 更新后可能失效。默认不启用。推荐路线是 rules mode。

---

## Claude Code 初步集成

v0.2.x 为 Claude Code 提供 observe mode hook（实验性），safe reroute 有初步实现。**Claude Code 集成仍在积极开发中，非稳定功能。**

```bash
# Claude hook（实验性）
xit init claude --method official_hook --scope project --yes
xit hook status claude
```

---

## 安全与隐私

- **无 telemetry**：不发送任何数据到云端
- **raw_log 本地**：所有原始日志保存在 `.xit/runs/`，不上传
- **history 本地**：统计保存在 `.xit/history.jsonl`，不上传
- **status patch opt-in**：明确需要 `--yes --accept-risk`，可随时 `uninstall` 回滚
- **所有压缩统计是本地估算**：`saved_tokens = saved_bytes / 4`，非真实 tokenizer
- **fail-open**：XiT 出错时不阻塞原始命令

---

## npm 包信息

```
npm 包名：xitsg
安装命令：npm i -g xitsg
安装后命令：xit
版本：0.2.40
```

> npm 包名 `xit` 已被 npmjs.org 上的其他项目占用，因此发布为 `xitsg`。安装后的 CLI 命令仍然是 `xit`，用户使用体验不变。

---

## 从源码构建

```bash
git clone https://github.com/stephenywilson/xit
cd xit
go build -o xit ./cmd/xit/main.go
mkdir -p ~/.local/bin
cp ./xit ~/.local/bin/xit
export PATH="$HOME/.local/bin:$PATH"
xit --version
```

---

## Roadmap

- [x] v0.2：Session Mode — PTY 包装 + 会话历史追踪
- [x] v0.2.6：Claude Code official hook observe mode
- [x] v0.2.7：Claude Safe Reroute
- [x] v0.2.9：Kimi Hooks Beta Observe Mode
- [x] v0.2.14：Kimi Safe Reroute
- [x] v0.2.18：Kimi Rules Mode（skill 注入）
- [x] v0.2.21：Kimi Bottom Toolbar Monkey Patch
- [x] v0.2.26：Compression Quality Benchmark + Filter Policy Hardening
- [x] v0.2.40：npm 发布（xitsg），多平台 binary
- [ ] v0.3.0：Codex AGENTS.md / XIT.md generator
- [ ] v0.3.1：Cursor hooks.json support
- [ ] v0.3.x：更多命令 filter + 真实 tokenizer 集成

---

## License

MIT
