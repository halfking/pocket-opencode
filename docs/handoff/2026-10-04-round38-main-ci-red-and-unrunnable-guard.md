# round38 —— 24h 修正审计：main 的 CI 是红的，且一个真库护栏在 CI 里从不执行

日期：2026-10-04 00:46 → 01:20
分支：`audit/2026-10-04-main-health`
基线：`742252f8` → 合并并发会话后为 `8a504202`

---

## 0. 结论先说

**发现 1 个真缺陷，且它同时命中三件事：把 main 的 CI 判红、让一个针对真实线上事故的护栏在 CI 里恒 skip、并且被登记成一条永远读不到的「已豁免」配置。**

`backend/internal/email/store_upsert_messageid_test.go`
（`705b22b0` 引入，`de252b65` 改过隔离策略）违反本仓自己的 PG 隔离守卫
规则 2，而 `.github/workflows/backend.yml` 跑的就是
`go test -race ./... -count=1` —— **主干自 `de252b65` 起就是红的。**

已修。修完 `go build` / `go vet` / `go test ./... -count=1` 全绿。

---

## 1. 根因：一个测试文件按错了隔离约定

### 1.1 缺陷本体（不是这次引入的，是 2026-10-03 引入的）

初版把 schema 隔离寄托在**运维手工指定** `POCKET_DIAG_SCHEMA` 上：

```go
schema := os.Getenv("POCKET_DIAG_SCHEMA")
if schema == "" { schema = "opencode_pocket" }   // ← 初版缺省即生产库
```

`de252b65` 已经把「缺省即生产库」改成「缺省即 t.Skip、显式点名
`opencode_pocket` 即 t.Fatalf」。**危险的那一半修掉了，但没有修完** ——
于是落进一个更尴尬的状态：**安全但永远不执行**。

三个可证伪的后果：

1. **main 的 CI 红的。** 规则 2 判
   `TestPGTestsNeverTargetTheProductionSchema` 失败。
2. **CI 里恒 skip。** 闸门是 `POCKET_DIAG_SCHEMA` 非空，而
   `backend.yml` 只设 `POCKET_TEST_POSTGRES_DSN`。
   ⇒ 本地/真库上「跑过」，CI 上从不跑。
3. **它结构上跑不起来。** 初版借真实账户
   （`SELECT id FROM email_accounts LIMIT 1`）解外键，而干净的自建 schema 里
   `email_accounts` 是空的 ⇒ 落到 `t.Skipf("库里没有可用账户")`。
   **能让它真正断言的场景，恰好是它显式拒绝的那个（生产库）。**

第 3 条是关键：这不是「忘了配环境变量」，是这个用例的**成功条件与它的
安全条件互斥**。所以「跑一次绿了」这种本地证据毫无意义。

### 1.2 为什么它能一路混过 review：一条永远读不到的登记

`de252b65` 同时往 `pg_test_isolation_guard_test.go` 加了一条：

```go
"internal/email/store_upsert_messageid_test.go": "会真写 emails 行…",
```

加在 **`pgAllowlistedWrites`** 表里。但守卫的规则 4（`:833`）是：

```go
if _, exempt := pgSafeWithoutIsolation[rel]; exempt && hasSQLWrite(code) {
    if reason, ok := pgAllowlistedWrites[rel]; ok { ... }
```

**作用域限定在 `pgSafeWithoutIsolation` 之内**，而这个文件从来不在那张表里。
⇒ 这条登记从写下那天起就**永远读不到**，规则 2 照常判红。

这比「没登记」更糟：它让读代码的人以为「已豁免、已处理」，
实际是**装饰性配置**，把冲突变沉默了。（守卫自己在 `:849` 写过这句警告：
「不要靠把 pgSafeWithoutIsolation 的理由写宽松来绕过——那是让冲突变沉默」，
但这条是同一个病的另一个方向。）

---

## 2. 修法

`store_upsert_messageid_test.go` 改为**在本文件内自建隔离 schema**，
形态照抄本包 `fetcher_greenmail_test.go`（守卫注释里点名的「已正确隔离」范例）：

- `CREATE SCHEMA email_upsertguard_test_<hex>`
- `newScopedPool` 把 `search_path` 钉上去
- `NewStore(pool)` 跑真迁移 ⇒ **两条唯一约束是真的**（被测行为需要）
- `seedGuardAccount` 自建账户解外键（不再借真实账户）
- 收尾整条 schema `DROP CASCADE`

⇒ 不再需要 `POCKET_REAL_MAIL_DSN` / `POCKET_DIAG_SCHEMA`，
不再需要在任何豁免表登记，**CI 里会真的执行**。

同时删掉 `pgAllowlistedWrites` 里那条失效登记，并把它为什么失效写在原地
（否则下一个人会照着再加一次）。

新增 `requireIsolatedSchema`：`SELECT current_schema()` 与本用例自建的
schema 名逐字比对。词法判据只能证明「文件里写了自建 schema」，
证明不了「连接真的用它」；而本用例会真的 INSERT/DELETE `emails` 行。

---

## 3. 判据的牙齿：两个负控（都实测过）

按本仓规矩，**绿灯不算数，除非先见过它红**。

| 场景 | 期望 | 实测 |
|---|---|---|
| 修复前（main 原样） | 红 | ✅ 红，`pg_test_isolation_guard_test.go:864` |
| 修复后 | 绿 | ✅ 绿 |
| 负控 A：去掉 `*_test_` schema 字面量（保留真·直连池 + `DELETE FROM emails`） | 红 | ✅ 红，同一行 864 |
| 负控 B：改用 `newWorkspaceTestStore` 复用 helper | **红** | ⚠️ **绿 —— 见 §4** |

### 3.1 一个必须记录的坑：go test 缓存让负控 A 一度「假绿」

守卫是**词法扫描其它包的文件**，而 `go test` 的缓存只跟踪本包的输入。
第一次跑负控时我看到「ok」，实际是**上一轮绿灯的缓存**。

⇒ **跑这条守卫必须带 `-count=1`**。
好在 CI 的 `go test -race ./... -count=1` 已经带了，本地排查别忘。

---

## 4. 副产物：守卫本身有一个可复现的假绿（**未修，已记录**）

负控 B 是本轮最有价值的发现。

把两个 PG 用例改成复用本包既有的 `newWorkspaceTestStore(t)`
（它确实做了完整隔离：自建 `email_ws_test_<hex>` + RuntimeParams 钉
search_path + 真迁移）之后，**守卫是绿的**。于是我往那个版本里塞了一段
真正不隔离的代码：

```go
func negCtrlOpen(t *testing.T) {
    pool, _ := pgxpool.New(context.Background(), os.Getenv("POCKET_TEST_POSTGRES_DSN"))
    defer pool.Close()
    _, _ = pool.Exec(context.Background(), "DELETE FROM emails WHERE id = 'x'")
}
```

**守卫依然绿。**

### 根因

规则 2 的放行条件是：

```go
if isolatedSchemaRe.MatchString(code) { return nil }
var isolatedSchemaRe = regexp.MustCompile(`"(\w*_test_)`)
```

`\w*` **可以匹配零个字符**，所以我那行
`strings.Contains(cur, "_test_")` 里的字符串字面量 `"_test_"`
**本身就满足** `"(\w*_test_)` ⇒ 规则 2 在整个文件上被短路。

### 危害

`isolatedSchemaRe` 认的是「文件里出现过一个长得像 schema 前缀的字符串」，
不是「这个文件自建并钉住了 schema」。任何一个**用途无关**的
`"_test_"` 字面量（一句 `strings.Contains`、一个错误消息、一段注释里的
示例）都会把这条判据整个关掉。

⇒ **「守卫绿」与「文件确实隔离」不是同一件事。** 在本文件上我一度就是
这个形态。这与本仓已经吃过多次的「判据被无关字面量满足」是同一类。

### 为什么这次没顺手修

把 `\w*` 收紧成 `\w+`（要求 `_test_` 前至少有一个词字符）**看起来**能修，
但守卫的注释明写它已经因为「太宽」被收窄过两轮
（`pg_test_isolation_guard_test.go:770-786`：第一版判红 6 个全是误报，
第二次又判红 4 个）。**再动这条正则有把 6~10 个已正确隔离的文件重新判红的
风险**，且那几个 worktree 正被另外三个会话使用、无法在本轮安全验证。

⇒ 留作独立一轮。**这一条不要在别的改动里顺手改**——它需要自己的
负控矩阵（把每个已隔离文件跑一遍，确认收紧后仍绿）。

---

## 5. 分支审计（24h 内）

`git log origin/main..<branch>` 全部为空 ⇒ 三个分支**已完全并入 main**，
无可合并内容：

| 分支 | 状态 |
|---|---|
| `audit/gofmt-debt-20261003` | 已并入（远端已 gone） |
| `fix/i18n-nested-placeholder-crash` | 已并入 |
| `fix/email-upsert-messageid-conflict` | 已并入 |

**本轮没有删除任何分支或 worktree。** 原因见 §6。

---

## 6. 并发实况（这一条决定了本轮能做什么、不能做什么）

开工时实测到**除本会话外还有 3 个 openpocket 会话在跑**
（`session list` + 文件 mtime 双重取证）：

| 会话 | 标题 | 状态 |
|---|---|---|
| `mvs_9ba2f519…` | 对齐主分支并用 Maestro 真机测试修复功能 | started |
| `mvs_8a0f6bf8…` | 完善邮件定时收取与发票处理需求 | started |
| `mvs_1fd1f45a…` | 修复 fetcher.go 缺失 BODY[] 断言 | idle |

主工作区 `C:\workspace\openpocket` 当时有 7 个已改文件 + 6 个未跟踪文件，
且**在我两次 `git status` 之间又长出 2 个新文件**
（`diag-master-pw-dom.mjs` 0:46:54、`diag-master-pw-dialog.mjs` 0:48:02）。
四个 worktree（`.wt-build` / `wt-a32` / `wt-i18n` / `wt-i18n2`）**全部**在
被写入（`.wt-build` 最新一个文件落在我发起检查后 36 秒）。

⇒ 因此本轮**全部工作在独立 worktree `openpocket-wt-audit38` 完成**，
主工作区一个字节都没碰。

**并且没有做这两件事**（任务要求了，但会毁掉别人的在制品）：

1. **删分支 / 删 worktree** —— 三个分支虽已并入 main，但它们**仍被 checkout
   在正在使用的 worktree 里**（`wt-a32`、`wt-i18n` 各自还有未跟踪文件：
   `frontend/scripts/probe-i18n-err-locus.mjs`）。删 worktree 会连带删掉这些。
2. **提交/推送主工作区那批未提交改动** —— 那是并发会话的**在制品**
   （master password 弹窗诊断、snippet 整封回退、invoice link 接线），
   不是本轮的产出。代为提交会把别人做到一半的实验固化成主干。

⇒ 合并 origin/main 时也确实撞上了并发推送：基线 `742252f8` 在我工作期间
被推成 `8a504202`（`c357a178` harness preflight + 1 个 merge）。
已核对**新提交不碰我改的两个文件**，`git merge origin/main` 干净合并。

---

## 7. 测试

在 `openpocket-wt-audit38`（`8a504202` 基线）实测：

```
go build ./...                          → 0
go vet ./...                            → 0
go test ./... -count=1                  → 全绿，无 FAIL
node scripts/check-gofmt.mjs            → 0（921 个 .go，归一化后真债 0）
```

关键那条守卫：

```
go test ./internal/server/ -run TestPGTestsNeverTargetTheProductionSchema -count=1
  修复前：FAIL (:864)
  修复后：PASS
  负控 A：FAIL (:864)   ← 证明它有牙齿
```

### 未验证项（明说，不假装覆盖）

- **两个 PG 用例没有对着真库跑过绿。** 本机 5432 上有 Postgres，但那是
  **另外三个会话正在用的库**；本轮刻意没有用 `POCKET_TEST_POSTGRES_DSN`
  指过去（那会在生产库上 CREATE/DROP schema）。本机无 Docker，起不了
  一次性实例。⇒ 只验证到「编译通过 + 无 DSN 时干净 skip」。
  **它们在 CI 里会真的执行**（`backend.yml` 设了
  `POCKET_TEST_POSTGRES_DSN`），首次真跑请看 CI。
- 前端门禁没跑：新 worktree 无 `node_modules`，且不想与并发会话抢端口/设备。
  `check:gofmt` 不依赖 node_modules，已跑。

---

## 8. 遗留风险

1. **§4 的守卫假绿未修。** 任何文件只要有一个无关的 `"*_test_"` 字面量，
   PG 隔离规则 2 就对它失效。需要独立一轮 + 完整负控矩阵。
2. **本机 5432 的 Postgres 是共享资产。** 三个并发会话 + 生产数据。
   任何 PG 集成测试都不应在未确认 DSN 归属时指过去。
3. **三个分支与四个 worktree 仍原样保留**（见 §6）。清理要等并发会话
   收工后单独做，删前先 `git rev-list --count origin/main..<branch>`
   与 `git status` 双向确认。
4. **守卫受 go test 缓存影响**：本地必须 `-count=1`（CI 已带）。
