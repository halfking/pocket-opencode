# openpocket 后端日志标准（rule 71）

> 对齐 vibe-coding `rules/71-log-standard.md`，字段契约 SSOT：
> `vibe-coding/spec/logging/log-schema.yaml`。
> 本文件是 openpocket 的**落地说明**；规则正文与 SSOT 才是权威。

## 一句话

pocketd 的**所有**日志行（含 880 处标准库 `log.*` 调用）都带同一套规范字段，
AI 侧不必为不同来源写不同解析分支。

## 规范字段（9 个必填）

| 字段 | 类型 | 说明 | 来源 |
|---|---|---|---|
| `timestamp` | string | RFC3339 纳秒 **UTC** | 规范层自动 |
| `severity_text` | string | 大写：TRACE/DEBUG/INFO/WARN/ERROR/FATAL | 规范层自动 |
| `severity_number` | int | OTel 官方数值表（1/5/9/13/17/21） | 规范层自动 |
| `level` | string | **小写**别名（ECS `log.level` 惯例） | 规范层自动 |
| `msg` | string | 人类可读事件描述 | 调用点 |
| `service` | string | 服务标识（`openpocket-api`） | 规范层自动 |
| `version` | string | 服务版本（构建期注入） | 规范层自动 |
| `env` | string | 部署环境（local/dev/test/staging/prod） | 规范层自动（`POCKET_ENV` 归一） |
| `instance_id` | string | hostname-pid-启动时刻 | 规范层自动 |

### 为什么 `level` 小写、`severity_text` 大写

两者是**独立字段**，不是彼此的 alias。

- OTel 的 `severity_text` 惯例是大写；
- ECS 的 `log.level` 惯例是小写，下游 LogQL 常用 `level=~"error|fatal"` 过滤。

大写的 `level` 会让这类过滤**静默漏掉全部错误日志**（实测：AI 日志分析器
正是用这个表达式）。两个名字各取所长、并存互不覆盖。

## 实现落点

| 组件 | 文件 | 职责 |
|---|---|---|
| 规范层 | `backend/internal/logging/logging.go` | 字段注入、级别三件套、时间戳、脱敏 |
| 级别辅助 | `backend/internal/logging/level.go` | `Errorf` / `Warnf` / `Infof` / `Fatalf`（printf 语义 + 真级别） |
| 版本注入 | `backend/internal/version/version.go` | 构建期 `-ldflags -X` 注入 version |
| 接线 | `backend/cmd/pocketd/main.go` | 启动即装规范层 |

参考实现源：`vibe-coding/templates/logging/go/`（复制而非依赖，保持同源）。

## 三个关键决定

### 1. 一次 `logging.Init` 收编全部存量调用点

`logging.Init` 内部调用 `slog.SetDefault`，而 Go 1.21+ 的 `slog.SetDefault`
会把标准库 `log` 包的输出也指到 slog handler 上。因此本仓 **880 处**
`log.Printf` / `log.Println` **不需要逐处改写**，全部变成带必填字段的 JSONL。

这是选 slog 而不是「另起一套封装」的决定——改 880 处调用点的风险，
远高于把输出端接过去。

### 2. 级别要显式化，否则告警会漏

标准库 `log` 没有级别概念：接进 slog 后一律是 INFO。于是
`log.Printf("ERROR: ...")` 这类行在 `level` 字段上仍是 `info`，
下游 `level=~"error|fatal"` 会**漏掉全部错误事件**。

因此本轮把带级别前缀的调用点改成级别化的辅助函数：

| 原写法 | 现写法 | 数量 |
|---|---|---|
| `log.Printf("WARN: ...")` | `logging.Warnf("WARN: ...")` | 87 |
| `log.Printf("ERROR: ...")` | `logging.Errorf("ERROR: ...")` | 25 |
| `log.Fatalf(...)` | `logging.Fatalf(...)` | 64 |

**消息文本一字未改**（前缀也保留），运维 grep 的习惯不用改；
变的只是 `level` / `severity_text` 字段变得可信。

`logging.Fatalf` 与 `log.Fatalf` 的唯一差异是级别为 FATAL——进程即将退出
属于不可恢复事件，必须能被 `level=~"fatal"` 抓到。退出码仍是 1。

### 3. goacos 注册面也走同一条管线

原代码在注册处单起了一个 `slog.NewTextHandler(os.Stderr, ...)`——
纯文本、缺必填字段，同一进程会出现两种格式。现在改用
`slog.Default().Handler()`（即规范层的 handler）。

## 环境归一

| `POCKET_ENV` | 规范 `env` |
|---|---|
| `production` / `prod` | `prod` |
| `staging` / `stage` | `staging` |
| `development` / `dev` | `dev` |
| `testing` / `test` | `test` |
| `local` | `local` |
| 其它 / 空 | `local` |

本仓历史默认值是 `development`，不归一会让 AI 按 `env` 分组时出现
`dev` 与 `development` 两个桶。

## 构建期版本注入

```bash
go build -ldflags "-X github.com/halfking/pocket-opencode/backend/internal/version.BuildVersion=1.2.0" ./cmd/pocketd
```

未注入时回落到 `dev`——合法非空值，可让门禁通过，同时明确表示「这不是发布构建」。

## 自检

```bash
cd backend
go test ./internal/logging/...
```

机器门禁（规范仓）：

```bash
cd vibe-coding
python3 scripts/verify-log-standard.py --service openpocket-api --log-file <日志文件>
```

## 未覆盖的部分

- **前端 / 移动端**（`frontend/`、Android）不产生服务端日志，不在本规范范围。
- 其余没有级别前缀的 `log.Printf` 调用点仍是 INFO 级别——它们本来就没有
  级别语义，按需逐批迁移到 `logging.Infof` / `slog.Info` 即可。
- 日志输出到 **stdout**（容器采集）；落盘轮转参数见规范层 `Options`，
  本仓暂未启用（`LogFile` 留空）。
