<div align="center">

<img src="docs/assets/xit-hero.png" alt="XiT / 吸T神功" width="800"/>

# XiT / 吸T神功

**吸T神功：专治 AI CLI 被终端日志撑爆上下文**

适用于所有会调用终端命令的 AI Coding CLI（Kimi · Claude Code · Codex · Cursor 等）。

高噪音命令输出预计压缩 **80–95%**，预计省 **80%+ Token**，原始证据本地留存。

[![npm](https://img.shields.io/npm/v/xitsg?color=56f5a3&label=xitsg&style=flat-square)](https://www.npmjs.com/package/xitsg)
[![Go](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat-square&logo=go)](https://go.dev)
[![License](https://img.shields.io/badge/license-MIT-b8860b?style=flat-square)](LICENSE)
[![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux%20%7C%20Windows-6b7280?style=flat-square)](#支持平台)

</div>

---

## 一行安装

```bash
npm i -g xitsg
```

安装后命令名为 `xit`。无需配置，开箱即用。

---

## 预计收益

<img src="docs/assets/metrics.svg" alt="预计收益" width="800"/>

| 指标 | 预计效果 |
|------|-------:|
| 高噪音命令压缩率 | 80–95% |
| 高命中场景 Token 节省 | 80%+ |
| 单次冗长测试预计节省 | 约 5k–15k Token |
| AI CLI 路由命中率目标 | 90%+ |
| 原始证据 | 本地 raw_log 留存 |
| 数据上传 | 无遥测 / 不上传 |

以上为宣传口径与目标区间，实际效果取决于命令类型、输出规模、仓库大小、AI CLI 是否正确使用 `xit auto`。Token 节省为估算值，计算方式为 `saved_tokens = saved_bytes / 4`，并非 tokenizer 精确计数。

---

## 它解决什么问题

任何 AI Coding CLI 在执行终端命令后，都会把原始输出原封不动塞进上下文。

`go test -v ./...` 一跑，几千到数万行日志全部入帧。  
`docker logs` 一扔，上下文直接爆掉。

吸T神功不是承诺每次都固定省多少 Token，而是把 AI CLI 最容易浪费上下文的高噪音命令输出，变成更短、更可审计、更适合 AI 阅读的摘要。

**运转方式：**

```
AI 说 xit auto go test -v ./...
         ↓
XiT 捕获完整输出 → 过滤噪音 → 输出摘要
         ↓
原始日志本地留存 → .xit/runs/xxx.raw.log
```

AI 读摘要，你留证据。Token 压力归零。

---

## 吸T前后对比

<img src="docs/assets/before-after.svg" alt="吸T前后对比" width="800"/>

---

## 内功运转流程

<img src="docs/assets/workflow.svg" alt="内功运转流程" width="800"/>

---

## 江湖适配图谱

| AI CLI / 门派 | 当前状态 | 说明 |
|--------------|---------|------|
| **Kimi CLI** | ✅ 已打通实战原型 | rules、hook、turn lifecycle、中文状态栏已跑通 |
| **DeepSeek 系 CLI** | 🎯 下一目标 | 调研 DeepCode / DeepSeek-backed CLI |
| **Claude Code** | 🔄 适配中 | 后续做 hook 与命中率验证 |
| **Codex** | 📋 规划中 | 后续适配 |
| **Cursor** | 📋 规划中 | 后续适配 |

---

## 常用招式

| 招式 | 命令 | 效果 |
|------|------|------|
| 运功 | `xit auto <任意命令>` | 捕获 + 压缩 + 输出摘要 |
| 查阅 | `xit history` | 查历史记录与压缩率 |
| 留证 | `xit log show <run-id>` | 查完整原始输出 |
| 清场 | `xit log clean --older-than 7d` | 清理旧 raw_log |

```bash
xit auto go test -v ./...
xit auto git diff HEAD~1
xit auto grep -r "TODO" ./src
xit auto docker logs mycontainer --tail 200
```

---

## Kimi 实战案例

<img src="docs/assets/kimi-toolbar.svg" alt="Kimi 实战状态栏" width="800"/>

Kimi CLI 是 XiT 第一套已跑通的实战适配。XiT 的目标不是绑定某个 AI，而是成为所有 AI Coding CLI 的本地输出压缩层。

- **Kimi CLI**：已打通实战原型
- **DeepSeek 系 CLI**：下一目标
- **Claude Code / Codex / Cursor**：后续适配

在 Kimi rules 文件加一行即可：

```
当你需要运行终端命令时，使用 xit auto <命令> 代替直接运行命令。
```

完整接入文档（rules / hook observe / toolbar patch）：[docs/kimi.md](docs/kimi.md)

---

## 本地 dogfood 参考

以下数据来自 XiT 本仓库的本地 dogfood，仅用于说明工具效果，不代表所有项目固定结果。

| 口径 | 本仓库 dogfood 结果 |
|------|------------------:|
| 历史输出压缩率 | 91.5% |
| 当前会话输出压缩率 | 98.7% |
| 最近窗口命中率 | 100% |
| 累计估算节省 | 约 340k Token |
| 最近单次测试节省 | 约 9k Token（`go test -v ./...`） |

这些数据会随命令类型和工作流变化。README 第一屏采用预计区间，避免把本地 dogfood 误读为通用承诺。Token 均为 `saved_bytes / 4` 估算，非 tokenizer 精确计数。

---

## 安全与隐私

- **零 telemetry**：不收集任何使用数据，不上传日志
- **全程本地**：所有处理在本机完成，不经过任何外部服务器
- **raw_log 留证**：完整原始输出保存在 `.xit/runs/`，随时可查
- **本地统计**：`.xit/history.jsonl` 保存本地压缩统计，不离开本机
- **状态栏 patch**：可选高级功能，可随时回滚
- **Token 节省**：估算值，计算方式为 `saved_tokens = saved_bytes / 4`

详见 [docs/privacy.md](docs/privacy.md)。

---

## 路线图

- [x] `xit auto` 核心压缩引擎
- [x] raw_log 本地留存
- [x] Kimi CLI 完整适配
- [x] npm 全平台分发（macOS / Linux / Windows）
- [ ] DeepSeek CLI 适配
- [ ] Claude Code 深度集成
- [ ] 自定义过滤器 DSL
- [ ] 压缩规则插件系统

---

## npm 包说明

包名 `xitsg`，CLI 命令为 `xit`。

```bash
npm i -g xitsg    # 安装
xit --version     # 验证
xit auto --help   # 查看帮助
```

**支持平台：**

| 平台 | 架构 | 状态 |
|------|------|------|
| macOS | Apple Silicon (arm64) | ✅ |
| macOS | Intel (x64) | ✅ |
| Linux | x64 | ✅ |
| Linux | arm64 | ✅ |
| Windows | x64 | ✅ |

源码：[github.com/stephenywilson/xit](https://github.com/stephenywilson/xit)

---

<div align="center">

*全程本地运功 · 无任何数据离开本机 · raw_log 是你的审计留证*

</div>
