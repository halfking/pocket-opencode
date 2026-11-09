# Changelog

本文件记录 openpocket（pocket-opencode）自本轮起的变更。
遵循 [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) + 语义化版本。

---

## [Unreleased] - 2026-11-09

### 🎯 主题：接入 rule 71 统一日志标准 —— 全部日志带规范字段，级别变得可信

**问题**：后端 pocketd 的 880 处日志调用走标准库 `log` 包，输出是纯文本，
既没有 `service`/`version`/`env`/`instance_id` 等必填字段，也没有可信的级别——
`log.Printf("ERROR: ...")` 打出来的行在下游 `level=~"error|fatal"` 过滤下
**一条都抓不到**（标准库 log 没有级别概念，接管后一律是 INFO）。
AI 自动化监测要为本服务写一套专用解析分支。

**方案**：接 vibe-coding `rules/71-log-standard.md` 的规范层，
**不改 880 处调用点**，而是在输出端一次性收编：
`slog.SetDefault` 会把标准库 `log` 包的输出也指到 slog handler 上。

### ✨ Added

- `backend/internal/logging/`：rule 71 规范层（自 `vibe-coding/templates/logging/go/` 复制，保持同源）。
  8 个必填字段自动注入、级别三件套（`severity_text` 大写 / `level` 小写 / `severity_number`）、
  UTC RFC3339 纳秒时间戳、扁平单层 JSONL、键名与值模式脱敏。
- `backend/internal/logging/level.go`：`Errorf` / `Warnf` / `Infof` / `Fatalf`
  —— 按 printf 语义输出**带真实级别**的日志，用于迁移存量 stdlib 调用点。
- `backend/internal/version/version.go`：构建期版本注入（`-ldflags -X`），
  填补本仓此前无版本机制的缺口。
- `backend/docs/LOGGING-STANDARD.md`：落地说明（字段表、实现落点、迁移对照、
  环境归一表、构建期注入方式）。
- `CHANGELOG.md` + `docs/changelogs/2026-11-09-rule-71-log-standard.md`：
  按 rule 36 补齐变更记录与归档结构（本仓此前只有按主题命名的单文件 changelog）。

### 🔄 Changed

- ★★ **176 处日志调用点级别化**（消息文本一字未改，仅级别字段变得可信）：

  | 原写法 | 现写法 | 数量 |
  |---|---|---|
  | `log.Printf("WARN: ...")` | `logging.Warnf("WARN: ...")` | 87 |
  | `log.Printf("ERROR: ...")` | `logging.Errorf("ERROR: ...")` | 25 |
  | `log.Printf("[WARN] ...")` | `logging.Warnf("[WARN] ...")` | 20 |
  | `log.Printf("[ERROR] ...")` | `logging.Errorf("[ERROR] ...")` | 8 |
  | `log.Fatalf(...)` | `logging.Fatalf(...)` | 64 |

  级别前缀保留在消息里 ⇒ 运维 grep 的习惯不变；
  变的是 `level` / `severity_text` 字段不再一律是 info。

- `cmd/pocketd/main.go`：启动即装规范层（在任何日志产生之前）；
  goacos 注册面原先单起一个 `slog.NewTextHandler`（纯文本、缺必填字段，
  同一进程两种格式），改用 `slog.Default().Handler()`。
- 环境名归一：`POCKET_ENV`（历史默认 `development`）→ 规范 `env`（`dev`/`prod`/…），
  避免 AI 按 env 分组时出现 `dev` 与 `development` 两个桶。

### 🐛 Fix

- ★★ **被 recover 的后台 panic 被降级成 INFO**：`internal/background/background.go`
  的兜底 recover 里那条 `[PANIC]` 日志，级别此前是 info ⇒ 进程活着但后台任务已死，
  AI 侧按级别过滤完全看不到。现为 error 级（进程未退出 ⇒ 按 rule 71 §4.1
  应归 ERROR 而非 FATAL；消息里的 `[PANIC]` 前缀保留）。

### Verification

- `go build ./...`、`go vet ./...` 全绿；`gofmt -l` 全仓干净。
- `go test ./...`：仅 2 处失败，且**与本次改动无关**——
  `internal/server` 的 `TestMeetingSyncCallSitesShareTheSameID` /
  `TestMeetingSyncIDExtractor_HasTeeth` 在原始 `origin/main` 上同样失败
  （worktree 缺 `frontend/node_modules`，环境问题），已取基线逐条对照确认。
- 真实启动 pocketd 采样 40 行，`verify-log-standard.py --service openpocket-api`
  → `errors=0 warnings=0`；级别分布 INFO 34 / WARN 5 / ERROR 1（此前 40 行全是 INFO）。

### Known Gaps

- 没有级别前缀的 `log.Printf` 调用点仍是 INFO 级别——它们本来就没有级别语义，
  按需逐批迁移到 `logging.Infof` / `slog.Info` 即可。
- 日志输出到 stdout（容器采集）；落盘轮转参数见规范层 `Options`，本仓暂未启用。
- 前端 / 移动端不产生服务端日志，不在本规范范围。
