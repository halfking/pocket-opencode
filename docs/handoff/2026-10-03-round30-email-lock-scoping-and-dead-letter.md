# round30 — 每日流水线的锁键分片、死信淘汰与报错归因

日期：2026-10-03　基线：`e39ec9de`　范围：`backend/internal/email`

本轮解决两个**互相独立**的缺陷，外加一个复核时发现的既有缺陷。三个都在
真实生产数据上有可观测的表现，不是代码审阅出来的猜测。

---

## 一、锁键按 schema 分片（08:00 整轮被跳过）

### 现象与证据

生产实例今天 08:00 那一轮整轮跳过，日志只有一行：

```
[email/pipeline] 每日定时流水线跨进程锁已被其它实例持有，本轮跳过
```

结果：重要邮件提醒积压，`pending_high` 到 35 封（补跑时降到 0）。

根因不在锁本身，而在**锁的作用域**。本轮实测（重新跑 `logs/cmp-instance-dsn.ps1`，
不引用旧结论）：

| pid | exe | 库 | schema | 端口 |
|---|---|---|---|---|
| 61356 | `logs/pocketd-1528-new.exe` | 127.0.0.1:5432/postgres | `opencode_pocket` | 18099（生产） |
| 69476 | `backend/pocketd-demo.exe` | 127.0.0.1:5432/postgres | `rssdemo_test` | 18190 |

PG 的会话级 advisory lock 作用域是**数据库**，与 `search_path` 无关。两者同库，
于是每天 08:00 互相抢同一把锁——而抢到锁的那个实例跑的是另一个 schema 的数据，
对生产毫无意义。纯跨租户误伤。

### 修法

`pipeline_lock.go`：`dailyPipelineLockKey` 在**取锁那条连接上**现查
`SELECT COALESCE(current_schema(), 'public')`，拼成 `"<schema>:email:daily-pipeline"`。

- 为什么不从配置读：schema 是 `db.New` 用来钉 `search_path` 的，**没有传进**
  各模块的 Store（Store 结构体里没有 schema 字段）。而 `current_schema()` 返回的
  正是这条连接实际生效的 `search_path` 第一项——问「我现在连的是哪个 schema」
  问库最不容易答错。
- 为什么 `COALESCE` 放在 SQL 里：`current_schema()` 只在 `search_path` 为空时返回
  NULL，而 pgx 把 NULL 扫进 `*string` 是**报错**。写成「扫进 Go 再判空」会留下一段
  声称处理了 NULL、实际永远走不到的分支。
- `release` 必须用**当初实际加锁的那个键**：用常量去解锁一个带前缀的键会返回
  false，于是走兜底销毁连接——每轮白毁一条连接，且真实原因（键对不上）被掩盖。

### ⚠ 升级窗口（部署时必须知道）

键里加了 schema 之后，**新旧两版二进制用的是不同的键**。只升级生产、另一个实例
还跑旧版，两边就不再互相排斥——反而会同时跑一轮，造成重复推送（比现状更糟）。
**同一 schema 的实例必须一起换到新二进制**；混用期间不要让它们同时排 08:00。

### 判据与负控

新增 `pipeline_lock_schema_scope_test.go`，三条边界：

| 判据 | 用例 | 负控 → 实测结果 |
|---|---|---|
| 键确实带 schema（**不需要数据库**） | `TestDailyPipelineLock_LockKeyCarriesSchema` | 去前缀 → **转红** |
| 同库不同 schema 互不干扰 | `TestDailyPipelineLock_DifferentSchemasAreIndependent` | 去前缀 → **转红**（`state = busy, want acquired`） |
| 同库同 schema 仍互斥（对照） | `TestDailyPipelineLock_SameSchemaStillExcludes` | 去前缀时**保持绿**（对照组该如此） |

负控做法是把 `dailyPipelineLockKey` 的返回值临时改成常量，跑完立刻改回。

> 一次返工：最初把「两个锁键必须不同」写成夹具自检，结果负控打在自检上而不是
> 行为断言上，读不出「state = busy」。改成自检 **schema** 不同（这才是夹具该保证
> 的事），负控才落在真正的判据上。

既有 5 条锁用例的探针从「直接用常量」改成调生产实现 `dailyPipelineLockKey`。
不能自己拼 `schema + ":" + 常量`——那正是 `mime.go:117` 注释里记的坑的翻版：
测试自己手搓那行字符串，把生产的前缀删掉测试照样全绿。

---

## 二、死信淘汰 + 报错归因

### 现象与证据

`em-10443` / `em-10444`（AWS 账户告警，2026-05-31）两封邮件的原文已被**单独从
服务器删除**——同一账户 uid 10432 / 10424 / 11 取回正常，10443 / 10444 取回失败，
这组对照排除了 UIDVALIDITY 变更的解释（早前那轮已核过）。

后果有三：

1. 每轮都进第 1.5 步的取原文预算（`maxInvoiceBodyFetches=24`），**当天 6 次里占 2 次**；
2. 每轮必然失败，每轮留两条失败记录；
3. 报错文案把整步归因成「IMAP 侧问题」——**对这两封是错的**：不是 IMAP 坏了，
   是这两封信没了。运维照这句话去查 IMAP 会查错方向。

### 修法（`raw_body_dead.go`，新增）

新增两列（`NewStore` 时 `migrateRawBodyDead` 幂等 ALTER）：

```sql
ALTER TABLE emails ADD COLUMN IF NOT EXISTS raw_body_gone_streak INTEGER NOT NULL DEFAULT 0;
ALTER TABLE emails ADD COLUMN IF NOT EXISTS raw_body_dead_at TIMESTAMPTZ;
```

**不能挪用既有列**：`processed_at` 参与同步水位线
（`GREATEST(date, processed_at, created_at)`），`body_purged` 触发「禁止回源」与摘要
守卫。挪用任一个都会改变与本问题无关的行为。

规则：

- **连续 3 轮**观察到「服务端已无此消息」才置死信标记；
- 其它成因（网络/凭据/POP3 无缓存）把 streak **清零**——一次抖动不该累积；
- 标记满 **14 天自动复检**（清标记、重置 streak）。

**为什么不是「一次就淘汰」**：「取回 0 条消息」有两个成因，只有第一个是永久的——
①邮件被 expunge（永久）；②UIDVALIDITY 变了（库里的 uid 整体失效，邮件还在，
只是要用新 uid 取）。一次 0 条就永久淘汰会在场景 ②下把**整批**邮件判死。
14 天复检是这条误判的唯一安全阀：真被误判，代价上界是「保留期内少试两次」，
而不是永久排除在发票建档之外。`TestRawBodyDeadLetter_RetentionWindowIsBounded`
把这个上界钉死（阈值 ≥2、保留期 >0 且 ≤ 90 天回看窗口）。

### 「服务端已无此消息」的可判定入口

`mime.go` 新增哨兵 `ErrRawBodyGone`，并在**两条独立通道都这么说**时才保留：

- go-imap：匹配 0 条（服务端对不存在的 UID 回 OK + 空结果集）；
- textproto 降级通道：成功但一条 BODY literal 都没有（新哨兵 `errNoBodyLiteral`）。

降级通道自己失败（拨号/TLS/登录被拒/超时）**不构成佐证**——只说明它没跑成。
把哨兵留着会把一次网络抖动记成一次死信观察。

> 一次返工：我先断言「textproto 对不存在的 uid 回 OK 且不报错」，于是把佐证条件
> 写成 `rawErr == nil`。但 `mime.go:352` 恰恰是「一条 literal 都没解析到」就返回
> error——那个写法会让死信路径**永远进不去**（判据恒暗）。

### 报错归因（原来一句错话，现在按成因分桶）

```
invoice raw body fetch: N 未建档（服务端已无此消息 A、POP3 无原文且自愈失败 B、
网络/协议/凭据等其他失败 C、本轮被预算顺延 D）；连续 3 轮…会被淘汰，
本轮新淘汰 E 封、累计跳过 F 封
```

「被预算顺延」单列：它不是拉取失败，归进「其他失败」会把预算说成故障。

归因按**错误身份**判定（`classifyRawBodyFetchFailure`：`errors.Is` /
`errors.As`），不按文案。`TestClassifyRawBodyFetchFailure_IdentityNotWording`
里有一条专门钉这件事：一个**文案里含** `matched 0 messages` 但不是 gone 的错误，
必须判为 other——按文案匹配就会误记一次死信观察。

### 判据与负控

新增 `raw_body_dead_test.go`：

| 判据 | 负控 → 实测结果 |
|---|---|
| `TestRawBodyDeadLetter_StreakReachesThresholdOnlyThen` | `MarkRawBodyGone` 忽略成因 → **转红** |
| `TestRawBodyDeadLetter_NonGoneFailureResetsStreak` | 同上 → **转红** |
| `TestRawBodyDeadLetter_ReArmOnlyStale` | 同上 → **转红** |
| `TestPipelineStep15_DeadLetterIsNotProcessed` | 删掉第 1.5 步的 `if deadLetters[e.ID] { continue }` → **转红** |
| `TestPipelineStep15_LiveMessageIsStillArchived`（对照） | 同上 → 保持绿 |
| `TestClassifyRawBodyFetchFailure_IdentityNotWording` | 改成按文案匹配 → 转红（未实测，属设计约束） |

`TestPipelineStep15_*` 复用 `pop3CandidatePipeline` 夹具：同一个 fixture 在没有
标记时**一定**能建档（既有护栏 `TestPipelineStep15_POP3CandidateIsArchivedFromBodyCache`
守着），所以「标记之后建不出台账行」只可能由死信跳过造成。判据是行为，不是
「某个函数被调用过」。

> 一次返工：判据自己抓到我夹具写错——`now() - 1 day` 并不比 `now() - 14 days` 老，
> 复检数出 0 行。改成从 `rawBodyDeadRetryAfter` 常量本身推导要推多久，并加了
> 一条「标记确实落在截止线之前」的夹具自检。

---

## 三、顺带修掉的既有缺陷：顺延的 job 会读到别人的正文

复核 `extractInvoiceCandidates` 时发现的（与上面两项无关，但同一函数内）：

```go
posInKept := make([]int, len(jobs))   // 全零
for k, idx := range keptIdx { posInKept[idx] = k }   // 只回填保留下来的
...
if c.jobAt >= 0 { k := posInKept[c.jobAt]; if k < len(bodies) { b = bodies[k] } }
```

被预算顺延的 job 其 `jobAt` 同样 `>= 0`，而它在 `posInKept` 里是**零值 0**，
于是 `b = bodies[0]`——**另一封邮件**的解析结果。后果是把别人的正文拿来补这封的
开票日期、把别人的附件当成这封的发票落进台账。

修法：`-1` 作哨兵（抽成纯函数 `mapJobsToKeptPositions`，原先内联在 120 行大循环
里、从外部完全测不到）。负控：哨兵改回零值 →
`TestMapJobsToKeptPositions_DeferredJobsDoNotAliasKeptZero` 转红。

---

## 四、本轮**未做**的事（需要显式授权）

以下动作本轮一律**没有执行**，因为问卷是 `automatic_timeout` 自动采纳的
（`explicitUserConfirmation: false`），按既定规矩不算授权：

1. **重启生产实例 18099**（当前 PID 61356）——代码要生效必须重启。
2. **ALTER 生产表 `opencode_pocket.emails`**（加上述两列）——由重启时的
   `migrateRawBodyDead` 触发。形态是 `ADD COLUMN IF NOT EXISTS`，可逆。

因此**生产行为目前仍是旧的**：明天 08:00 锁竞争照旧、死信照旧占预算。

飞书推送环节（`APP_ID` / `APP_SECRET` / `INVOICE_CHAT_ID` / `INVOICE_FOLDER_TOKEN`
四项缺失）本轮同样未动，按需求原文的备选路径收尾：台账 CSV/MD + A4 拼版交付，
并在报告里明确记为「飞书腿未在真实环境验证」。

---

## 五、验证记录

| 项 | 结果 |
|---|---|
| `go build ./...` | 通过 |
| `go vet ./internal/email/` | 通过 |
| `go test ./internal/email/` | **ok 143.956s** |
| `go test ./internal/server/` | ok 51.118s |
| `go test ./internal/db/` | ok 0.623s |
| `go test ./internal/repohygiene/` | 新增 3 文件 `git add` 后重跑：`TestNoCommittedSecrets` **PASS**（扫描 3170/3517 个受跟踪文本文件，含新增 3 个） |

### 一个必须说明的环境坑：`TestNoCorruptGoMod` 在点开头的 worktree 里恒红

本轮在 `.wt-lockdead`（linked worktree）里跑 `repohygiene`，`TestNoCorruptGoMod`
报「只扫描到 0 个 go.mod，walk 范围可能不对」。

**做了对照，不是猜的**：

| 提交 | 目录 | 结果 |
|---|---|---|
| `e39ec9de`（未改动基线） | `C:\workspace\openpocket\.wt-baseline`（点开头） | **红**，同样报 0 个 |
| `e39ec9de`（未改动基线） | `C:\workspace\wt-check-nodot`（非点开头） | **绿** |
| 本轮分支 | `.wt-lockdead`（点开头） | 红（同一原因） |
| 主检出 | `C:\workspace\openpocket` | **绿**（46.596s） |

根因：`stray_go_mod_test.go` 的 walk 跳过「以 `.` 开头的目录」，而 linked worktree
的名字以 `.` 开头，于是**根目录第一下就被 `SkipDir`**，扫到 0 个文件。与代码无关。

顺带查清一件更要紧的事：同一包里的 `TestNoCommittedSecrets` 走 `git ls-files`，
**只枚举已跟踪文件**——新增文件若不 `git add` 就等于没被扫。本轮已显式
`git add` 三个新文件后重跑，确认它们进入了扫描范围（3170 个受跟踪文本文件）。

---

## 六、改动文件清单

| 文件 | 作用 |
|---|---|
| `internal/email/raw_body_dead.go` | **新增**：死信记账（阈值 / 清零 / 复检）+ 归因分类 `classifyRawBodyFetchFailure` |
| `internal/email/raw_body_dead_test.go` | **新增**：记账与归因的判据 + 负控 |
| `internal/email/pipeline_lock_schema_scope_test.go` | **新增**：锁键分片的三条边界 + 负控 |
| `internal/email/pipeline_lock.go` | `dailyPipelineLockKey`（schema 分片）+ release 用实际键 |
| `internal/email/pipeline_lock_test.go` | 探针改调生产实现取键；新增 `testDailyPipelineLockKey` |
| `internal/email/pipeline.go` | 死信跳过、记账调用、报错按成因分桶、新报告字段 `InvoiceBodyDeadLettered`、`mapJobsToKeptPositions` |
| `internal/email/mime.go` | `ErrRawBodyGone` / `errNoBodyLiteral` 两个哨兵 + 双通道佐证 |
| `internal/email/store.go` | `NewStore` 调 `migrateRawBodyDead`（1 行） |

Go 文件全部 CRLF、无 BOM；新增 3 个文件已断言 `bareLF=0`。
