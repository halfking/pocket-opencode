# round27 — 08:00 重要邮件提醒的多实例竞态（已查实并定量，**未修**）

日期：2026-10-03 01:48（距当日 08:00 流水线 6 小时）
分支：`main`，本文档为本地提交，未推
**只读调查。未改任何产品代码、未停任何进程、未写任何数据。**

---

## 0. 一句话

08:00 那 34 条积压提醒，真正的问题不是「推不推」，而是**可能被推 3 份**：
三个 pocketd 进程连同一个生产 schema、在同一时刻各自跑一遍流水线，
而流水线上**没有任何跨进程互斥**，落库那层也**没有任何去重**。

---

## 1. 三个实例，同库，同一时刻

| 实例 | 端口 | 二进制来源 | schema | 排期日志 |
|---|---|---|---|---|
| 主 | 18099 | `logs\pocketd-invoicenan-fix.exe` | `opencode_pocket` | `pipeline scheduled at 2026-10-03T08:00:00+08:00` |
| 验证 A | 18077 | `openpocket-wt-a20\backend\.verify-bin\pocketd.exe` | **`opencode_pocket`** | `pipeline scheduled at 2026-10-03T08:00:00+08:00` |
| 验证 B | 18100 | `.wt-e2e\backend\.verify-bin\pocketd.exe` | **`opencode_pocket`** | `pipeline scheduled at 2026-10-03T08:00:00+08:00` |
| 隔离 | 18101 | `.wt-e2e\backend\.verify-bin\pocketd.exe` | `opencode_pocket_verify` | 已装载，但**不参与**（隔离库） |

证据取自各实例自己的启动日志，不是推测：

```
openpocket-wt-a20\logs\audit-18077-20261003-000213.err.log
  [email/scheduler] daily pipeline runner injected (hour=8)
  Postgres pool initialized (schema="opencode_pocket")
  [email/scheduler] pipeline scheduled at 2026-10-03T08:00:00+08:00
```

**18101 连的是 `opencode_pocket_verify`，不参与生产。** 参与的是 3 个，不是 4 个。
（我第一版按进程名数出 4 个，是把隔离实例也算进去了；逐个查 schema 日志后更正。）

## 2. 流水线没有任何跨进程互斥

| 机制 | 位置 | 作用域 |
|---|---|---|
| `s.pipelineOnce`（`sync.Once`） | `scheduler.go:208` | 进程内 |
| `emailPipelineMu`（`sync.Mutex`） | `server_email_pipeline.go:274` | 进程内（`*Server` 的字段） |
| `pipelineLoop` | `scheduler.go:750-790` | 纯进程内 `time.After` + 直接调用 |

`pipelineLoop` 里没有任何「今天是不是已经跑过」的认领：

```go
case <-time.After(delay):
}
runCtx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
rep := runner.RunEmailPipeline(runCtx)      // scheduler.go:783 —— 直接跑
```

**本仓库本来就有现成的跨进程原语**，只是没用在流水线上：

- `store.go:1306` `pg_advisory_xact_lock` —— 假期/收件人并发领取串行化
- `llm_gateway_store.go:139` `pg_advisory_xact_lock` —— 网关配置串行化

## 3. 竞态窗口 = 整个推送循环

`pipeline.go` 的 `notifyImportant`：

```
:1036  NotifyImportantEmail(ctx, e)      ← 在循环里逐条推
:1044  MarkEmailsNotified(ctx, ids, …)   ← 循环**全部跑完**才写标记
```

「读到 34 条」到「标记已通知」之间隔着全部 34 次推送，不是瞬间。
三个进程只要都在第一个进程写标记之前完成读取，就各推 34 条。

## 4. 落库那层没有兜底

`notifycenter/service.go:157-160` 是**无去重的裸 INSERT**，每次新 ID：

```sql
INSERT INTO notifications (id, workspace_id, user_id, source, kind, title, body, payload, priority, read_at, created_at)
VALUES ($1, $2, …)
```

`notifications` 表上唯一的约束是主键 `id`，另两个索引
（`idx_notif_unread`、`idx_notif_ws_time`）都不是唯一的。
**没有任何唯一约束能挡住重复。**

## 5. 定量后果

```
当前基线（2026-10-03 01:4x）：
  notifications 共 24 行，全部 source=email / kind=email.important，23 条未读

单实例跑 08:00： 24 + 34      =  58 行
三实例并发：    24 + 34 × 3  = 126 行   ← 上限（若三者都在首个标记落库前完成读取）
```

附带：三个进程会**同时对同 5 个邮箱跑 IMAP 同步**——那是另一个共享外部资源的竞争。

## 6. 我为什么没有直接改代码

根治方案是给 `runEmailPipeline` 加跨进程锁。**本轮刻意没做**，三个理由：

1. `pg_advisory_xact_lock` 是**事务级**的。为它开一个贯穿 30 分钟流水线的事务会
   **占住一条连接**，而流水线自身还要用连接池跑几十条查询——有把池子耗尽、
   **死锁自己**的真实风险。要安全就得用 `pg_try_advisory_lock` + 单独
   `pool.Acquire()` 一条连接持有整轮，那是需要单独设计的一轮工作。
2. `pipeline.go` / `server_email_pipeline.go` 是共享文件，且并发会话正在同仓推进。
3. 距 08:00 只有 6 小时，**无法在这个窗口里做出多进程级验证**。锁写错的失败模式是
   「08:00 那轮整轮不跑」，**比重复推送更糟**。

## 7. 建议

**零代码止血（推荐在 08:00 前做）**：停掉 18077 与 18100，只留 18099。
不改任何代码即消除竞态，且可验证。这两个是并发会话的验证环境，需人工决定。

**根治（另开一轮）**：`pg_try_advisory_lock` + 专用连接，未取到则**记日志并跳过本轮**，
而不是排队等待。跳过必须是显式可见的（报告里要有字段），否则「本轮没跑」会变成
又一个无法解释的 0——这与 `ClassifySkip` / `RemindersUnclassified` 是同一类教训。

## 8. 与 34 条积压提醒的关系

34 条这个数是**按代码谓词独立重算**的（`logs/backlog0800.sql`，逐行注释了对应 Go 位置），
与并发会话的数字吻合。但那个数**假设只有一次流水线运行**；按本文的发现，
实际落地条数取决于几个进程谁先落标记，所以 **34 是下界不是定值**。

34 条里另有 **2 封是 e2e 测试夹具**（`[urgent-e2e]`，`em-pop3-` 前缀，
来自真实邮箱 `56551681@qq.com`，2026-09-07 08:29/08:43 注入），
它们会以「生产环境告警」的名义推送，需要单独清理。
