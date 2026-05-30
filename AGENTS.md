# XiT / 吸T神功 Codex 使用规则

XiT 是本项目的本地终端输出压缩层，用来防止 AI CLI 被高噪音命令撑爆上下文。

本项目中，Codex 执行终端命令时必须遵守以下规则。

## 必须使用 xit auto 的命令

下面这些命令输出通常很长，必须用 `xit auto` 包裹：

- `go test -v ./...`
- `npm test` / `pnpm test` / `yarn test`
- `pytest`
- `cargo test`
- `git diff`
- `git log --stat`
- `grep` / `rg` 大范围搜索
- `find` / `tree` 大范围目录扫描
- `docker logs`
- `kubectl logs`
- `tsc` / `eslint` / `build` / `lint` 的长输出

示例：

```bash
xit auto go test -v ./...
xit auto git diff
xit auto rg "TODO|panic|error" .
xit auto docker logs <container>
```

## 不要使用 xit auto 的命令

下面这些命令通常输出短，直接执行即可：

- `git status`
- `git branch`
- `pwd`
- `whoami`
- `go version`
- `node --version`
- `npm --version`
- `ls` 单层目录
- `cat` 很短的配置文件
- 带 `--json` / `--porcelain` 的结构化短输出
- `npm install` / `pnpm install` / `go mod tidy` 等会改动依赖或文件的命令，除非用户明确要求

## 报告规则

使用 `xit auto` 后，最终报告必须 compact：

- 不要粘贴 raw output
- 不要重复长表格
- 不要超过用户要求的行数
- 必须保留 `exit_code`
- 必须保留 `reduction`
- 必须保留 `saved_tokens`
- 必须保留 `raw_log` 路径
- 如果失败，必须保留失败摘要和关键错误

推荐报告格式：

```text
command:
exit_code:
status:
reduction:
saved_tokens:
raw_log:
key facts:
```

## 安全规则

- 不要上传 `raw_log`
- 不要把 `raw_log` 全文贴进对话
- `raw_log` 是本地审计证据，路径通常在 `.xit/runs/`
- 不要修改文件，除非用户明确要求
- 如果用户要求只读操作，只能执行只读命令
- 如果不确定命令是否高噪音，优先使用 `xit auto`
