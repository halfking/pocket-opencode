# 2026-11-09 — openpocket 后端接入 rule 71 统一日志标准

> **范围**：`client/openpocket`（后端 pocketd）
> **状态**：已合主干并端到端验证

---

## 1. 为什么要做

目标是让**所有服务的日志可被 AI 自动化监测**。openpocket 是台账里剩下的
P0 项之一：扫描判为「部分合规」，缺 `severity_text` / `severity_number`。

实测证据：

| 事实 | 读数 |
|---|---|
| 标准库 `log` 调用点 | **880** 处（`log.Printf` / `log.Println` / `log.Fatalf`） |
| `slog` 调用点 | 35 处（几乎只在 goacos 注册面） |
| 输出格式 | 纯文本（stdlib log 默认），**无任何结构化字段** |
| goacos 注册面 | 单起 `slog.NewTextHandler(os.Stderr, ...)` → 同进程两种格式 |
| 版本机制 | **无**（`version` 字段无处可取） |

关键后果：标准库 `log` 没有级别概念。`log.Printf("ERROR: ...")` 打出来的行
在下游 `level=~"error|fatal"` 过滤下**一条都抓不到**——而那正是 AI 日志
分析器用的选择器。

---

## 2. 做法

### 2.1 输出端收编，而不是改 880 处调用点

`logging.Init` 内部调用 `slog.SetDefault`，而 Go 1.21+ 的 `slog.SetDefault`
会把标准库 `log` 包的输出也指到 slog handler 上（`log.SetOutput`）。

⇒ 880 处调用点**一行不改**就变成了带 8 个必填字段的 JSONL。

### 2.2 级别显式化（176 处）

输出端收编解决了「有没有字段」，但没解决「级别是什么」——
标准库 log 一律 INFO。所以带级别前缀的调用点要显式改写：

| 原写法 | 现写法 | 数量 |
|---|---|---|
| `log.Printf("WARN: ...")` | `logging.Warnf("WARN: ...")` | 87 |
| `log.Printf("ERROR: ...")` | `logging.Errorf("ERROR: ...")` | 25 |
| `log.Printf("[WARN] ...")` | `logging.Warnf("[WARN] ...")` | 20 |
| `log.Printf("[ERROR] ...")` | `logging.Errorf("[ERROR] ...")` | 8 |
| `log.Fatalf(...)` | `logging.Fatalf(...)` | 64 |

**消息文本一字未改**（前缀也保留）——运维 grep 的习惯不用改。

### 2.3 两个语义决定

- **`logging.Fatalf` 的级别是 FATAL**：进程即将退出属不可恢复事件，
  必须能被 `level=~"fatal"` 抓到。退出码仍是 1，控制流不变。
- **被 recover 的 panic 归 ERROR 而不是 FATAL**：
  `internal/background/background.go` 的兜底 recover 记录的是
  「某个后台任务死了」，进程还活着。按 rule 71 §4.1 的口径
  （FATAL = 进程即将退出/不可恢复），这种事件归 ERROR 才对。
  ⇒ 规范层**不提供** `Panicf`，并把这个理由写进了代码注释，
  免得后来人「补」一个语义错误的 API。

---

## 3. 改动清单

| 文件 | 改动 |
|---|---|
| `backend/internal/logging/logging.go` | 规范层（自模板复制） |
| `backend/internal/logging/level.go` | `Errorf` / `Warnf` / `Infof` / `Fatalf` |
| `backend/internal/logging/logging_test.go` | 16 组用例 |
| `backend/internal/version/version.go` | 构建期版本注入 |
| `backend/cmd/pocketd/main.go` | 启动即装规范层；goacos logger 改走规范 handler |
| 25 个业务文件 | 176 处调用点级别化 + import 调整 |
| `backend/docs/LOGGING-STANDARD.md` | 落地说明 |
| `CHANGELOG.md` / `docs/changelogs/2026-11-09-rule-71-log-standard.md` | rule 36 要求的变更记录与归档 |

---

## 4. 验证

- `go build ./...`、`go vet ./...` 全绿；`gofmt -l` 全仓干净。
- `go test ./...`：仅 2 处失败，**与本次改动无关**——
  `internal/server` 的 `TestMeetingSyncCallSitesShareTheSameID` /
  `TestMeetingSyncIDExtractor_HasTeeth` 在原始 `origin/main` 上同样失败
  （worktree 缺 `frontend/node_modules`，属环境问题）。
  已在独立 worktree 上取基线逐条对照。
- 真实启动 pocketd 采样 40 行 →
  `python3 vibe-coding/scripts/verify-log-standard.py --service openpocket-api --log-file run3.out`
  → `errors=0 warnings=0`。
- 级别分布改造前后：INFO 40 → INFO 34 / WARN 5 / ERROR 1。

### 过程中撞到并修掉的一个真问题

改造后 `internal/repohygiene` 的守卫测试 `TestS949HelperRecoversAndLogs` 变红：
「background.Go 里 recover 之后没有日志」。原因是该守卫用**方法名白名单**
识别日志调用（`Printf`/`Println`/`Error`/`Warn`/`Info`/`Errorf`/`Warnf`/`Infof`），
而我一度用了 `Panicf` —— 白名单里没有 ⇒ 守卫判定「没有日志」。

对照基线确认这是**我引入的回归**（基线该包全绿），据此把 `Panicf` 撤掉、
改用 `Errorf`——而这同时也更符合 rule 71 的级别语义（见 §2.3）。

---

## 5. 剩余缺口

- 没有级别前缀的 `log.Printf` 调用点仍是 INFO 级别（它们本来就没有级别语义）。
- 落盘轮转参数见规范层 `Options`，本仓暂未启用（`LogFile` 留空 → stdout）。
- 前端 / 移动端不产生服务端日志，不在本规范范围。
