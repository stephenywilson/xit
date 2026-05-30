<div align="center">

<img src="docs/assets/xit-hero.png" alt="XiT / 吸T神功" width="800"/>

# XiT / 吸T神功

**吸T神功：专治 AI CLI 被终端日志撑爆上下文**

适用于所有会调用终端命令的 AI Coding CLI（Kimi · Claude Code · Codex · Cursor 等）。

`go test -v ./...` 输出 35,629 字节 → 吸T后 318 字节 · 压缩率 **99%** · 省 ~9k Token

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

## 实战战绩

> 数据来自本仓库 dogfood，Token 为 saved_bytes / 4 估算，非精确 tokenizer。

<img src="docs/assets/metrics.svg" alt="实战战绩" width="800"/>

| 指标 | 数据 |
|------|------|
| 历史输出压缩率 | **91.5%** |
| 当前会话输出压缩率 | **98.7%** |
| 估算已省 Token | **~340k Token**（saved_bytes / 4） |
| 最近命中率 | **100%** |
| 摘要完整性 | **100%** |
| 最近单次测试节省 | **~9k Token**（`go test -v ./...`） |

---

## 它解决什么问题

任何 AI Coding CLI 在执行终端命令后，都会把原始输出原封不动塞进上下文。

`go test -v ./...` 一跑，9000 个 Token 没了。  
`docker logs` 一扔，上下文直接爆掉。

**吸T神功的解法：**

```
AI 说 xit auto go test -v ./...
         ↓
XiT 捕获完整输出 → 过滤噪音 → 输出摘要（318 字节）
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

| AI CLI | 状态 | 接入方式 |
|--------|------|----------|
| **Kimi CLI** | ✅ 已打通 | rules 模式 + hook observe + toolbar patch |
| **Claude Code** | 🔄 适配中 | `xit auto <cmd>` 直接调用 |
| **DeepSeek CLI** | 🎯 下一目标 | 调研中 |
| **Codex CLI** | 📋 规划中 | — |
| **Cursor** | 📋 规划中 | — |

---

## 常用招式

| 招式 | 命令 | 效果 |
|------|------|------|
| 运功 | `xit auto <任意命令>` | 捕获 + 压缩 + 输出摘要 |
| 查阅 | `xit history` | 查历史战绩与压缩率 |
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

Kimi CLI 是第一套已跑通实战的接入案例。在 Kimi rules 文件加一行即可：

```
当你需要运行终端命令时，使用 xit auto <命令> 代替直接运行命令。
```

完整接入文档（rules / hook observe / toolbar patch）：[docs/kimi.md](docs/kimi.md)

---

## 下一站：DeepSeek 系 AI CLI

DeepSeek CLI 正在调研接入方案，目标通过 `xit auto` 无缝接入，实现 90%+ 压缩率。进展同步于 [Releases](https://github.com/stephenywilson/xit/releases)。

---

## 安全与隐私

- **零 telemetry**：不收集任何使用数据
- **全程本地**：所有处理在本机完成，不经过任何外部服务器
- **raw_log 留证**：完整原始输出保存在 `.xit/runs/`，随时可查

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
