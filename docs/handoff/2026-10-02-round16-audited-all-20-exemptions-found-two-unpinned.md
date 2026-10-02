# round16：复核 20 条豁免登记——又挖出两个「从不设 search_path」的假结论源

日期：2026-10-02
分支：main（无新分支）
上一轮：round15（`7d3bf2c5`）修掉 `diag_merge_exec_test.go` / `diag_rest_dupes_test.go` 的 DSN 拼接
本轮承接：round15 §7 遗留第 2 条「其余豁免文件的登记理由仍未逐条核对」

---

## 0. 本轮最重要的一条

**「查空库」在这些诊断里不是「没查到」，是「输出不是缺陷」。**

round15 我修完两个文件后写下「其余 16 条仍未核对」。本轮把 20 条全核了一遍
（机器扫 + 人工查证），又挖出**两个从未设置过 `search_path` 的文件**。

其中 `reminder_notified_diag_test.go` 的危害等级和前两个不同：
它不写数据，但它的**判据会在查空库时必然成立**：

```go
if highUnnotified == 0 {
    t.Logf("结论：46 封 high 全部已提醒过 —— remindersSent=0 符合设计，不是缺陷。")
}
```

DSN 的 `search_path` 若指向 `public` 而非生产 schema，它扫到 0 行 →
`highUnnotified` 必然是 0 → 输出「**不是缺陷**」。

**查空库永远「符合设计」。** 一个诊断探针在打错库时会主动否证缺陷，
比它读到脏数据危险得多——脏数据会被人看出来。

---

## 1. 普查方法与结果

写了一个一次性脚本 `.scratch-sttdev/audit-exempt.cjs`，对 20 条豁免逐个扫
三件机器可查的事：

| 项 | 抓什么 |
|---|---|
| env 门控 | 读哪些 `POCKET_*` 变量 |
| DB 调用 | `Exec` vs `Query*` 的次数 |
| search_path | `RuntimeParams[...]` 覆盖 vs 字符串拼接 vs **不设** |

结果直接指向两个文件 `RuntimeParams=0 且拼接=0`：
`reminder_notified_diag_test.go` 与 `realprobe_test.go`。

**逐条核对的结论（其余 18 条均无问题，记录在此以免下轮重查）：**

| 文件 | 结论 |
|---|---|
| `diag_pop3_backfill` / `diag_pop3_invoice` / `diag_backfill_align` / `diag_dup_report` / `diag_merge_plan` | 每条查询带**显式 `FROM \`+schema+\`.` 前缀**，不依赖 search_path，**安全** |
| `fetcher_greenmail_test.go` | `//go:build greenmail` 存在（我第一版普查正则漏了 `//` 前缀，误以为无标签）；且自建 `email_greenmail_test_` schema，**安全** |
| `pgscope_test.go` | 源码里的 `POCKET_POSTGRES_DSN` 全在**注释**里（解释为什么不回退它），代码只读 `POCKET_TEST_POSTGRES_DSN`。护栏剥注释后正确放过 |
| `diag_schema_present_test.go` | L64 **刻意不钉** search_path——它查 `information_schema` 而非业务表，要站「默认视角」。**这是正确的** |
| `diag_snippet_leak_test.go` | 三重开关 + 写路径拒绝 schema 缺省值，**仍成立**（round15 已核） |
| `diag_merge_exec_test.go` | 备份表非空检查在任何写操作之前，**仍成立**（round15 已核并已修 search_path） |
| `diag_credential_health` / `diag_real_invoice_extract` / `diag_real_invoice_gate` / `ledger_realdata_diag` / `spam_realdata` / `diag_kxpms` | 0 写语句 + `RuntimeParams` 覆盖，**均无问题** |
| `third_party/.../dao_test.go` | vendored，需 `-tags=integration`，**无问题** |

---

## 2. 改动

### 2.1 `reminder_notified_diag_test.go`

加 `POCKET_REAL_MAIL_SCHEMA` 门控 + `ParseConfig` + `RuntimeParams` 覆盖
+ 查询前 `current_schema()` 验证。**不验证是不够的**——「以为钉住了」
正是这个缺陷家族的特征。

### 2.2 `realprobe_test.go`

门控只有 `PG_DSN` + `POCKET_REAL_KEYS`，没有第二个变量说明该读哪个 schema。
新增 `dsnSearchPathFromDSN`（放在 `pgscope_test.go`，本包的隔离助手）从 DSN
读出目标 schema，再覆盖设置 + 验证。不引入新门控变量——这个文件的前提
就是「PG_DSN 指向真库」，它已经带了 `search_path`。

### 2.3 `dsnSearchPathFromDSN` + `TestDsnSearchPathFromDSN`

7 个分支：普通单值 / 带其它参数 / search_path 在前 / 逗号分隔取第一段 /
带引号 / **无 search_path 必须报错** / **空值必须报错**。

后两个负向用例是关键：若函数对空值返回空串而不报错，调用方会拿它去
`RuntimeParams["search_path"] = ""`，连接落在默认 search_path——比报错更糟。

**负控**：把 `if sp == "" { return "", err }` 换成 `sp = "public"` →
2 个子用例转红，错误信息是「应报错却返回 "public"——调用方会拿空 schema
去覆盖 search_path」。

### 2.4 护栏新增规则 5

```go
if _, exempt := pgSafeWithoutIsolation[rel]; exempt &&
    dsnSearchPathHelperReUnpinned.MatchString(code) &&   // pgxpool.New(ctx, dsn) 直连
    !searchPathAnyRe.MatchString(code) &&               // 没设置 search_path
    !isolatedSchemaRe.MatchString(code) &&              // 没自建 _test_ schema
    !qualifiedTableRe.MatchString(code) {               // 查询没带显式 schema 前缀
    t.Errorf(...)
}
```

四个条件同时成立才报——那才意味着**完全无从判断目标库**。

---

## 3. 判据收窄：两次都是我的判据太宽

### 收窄 1

第一版只判「直连 + 全文无 `search_path` 字样」→ **判红 6 个文件**。
逐个查证发现**全是误报**：5 个每条查询带显式 `schema.` 前缀，
`fetcher_greenmail` 自建了 `email_greenmail_test_` schema。

### 收窄 2

加上「无 `*_test_` schema」后仍判红 4 个。原因：`isolatedSchemaRe` 要求
双引号字面量 `"(\w*_test_)`，而这几个文件是 `` FROM `+schema+`. `` 拼接，
**没有那个字面量**。补 `qualifiedTableRe` 才彻底收住。

**两次都是判据的问题，不是这些文件有错。** 正确写法的文件被判红，
会逼着人把安全代码改危险——这比漏报更糟。

### 残留盲区（明说，不假装覆盖）

大部分查询带显式前缀、只有一处未限定的文件，判据看不见。
**宁可漏报，不可对正确写法误报。**

---

## 4. 负控记录：判据恒真的一次

`searchPathAnyRe` 第一版写成 `regexp.MustCompile("search_path")`——匹配**字面量**。

做负控时把 `RuntimeParams` 那行删掉，判据**转不了红**（`NEG_EXIT=0`）。
原因：文件里剩下的

```go
t.Fatalf("verify search_path: %v", err)
t.Logf("search_path verified: current_schema() = %q", …)
```

这些**运行时字符串**里照样有那个词。

**「提到 search_path」不等于「设置了 search_path」。** 判据锚在了错误的位置，
于是恒真——而且**不报错**，因为它确实匹配到了东西，只是匹配的是错误的东西。
这与 round14 那条「`[A-Za-z_]` 后面接 `\b` 是静默陷阱」同源。

改成要求赋值形态：

```go
regexp.MustCompile(`(?:\[\s*"search_path"\s*\]\s*=|search_path=|"search_path"\s*:)`)
```

之后负控立刻转红，护栏点名 `reminder_notified_diag_test.go`。

新增 `TestRule5SearchPathJudgesAreNotVacuous` 钉住三个判据，
其中反向用例就是当初让判据恒真的那 5 条输入。
**负控**：把判据改回字面量 → 3 条反向用例转红，精确复现当初的失效。

---

## 5. 改动文件

| 文件 | 变更 |
|---|---|
| `backend/internal/email/reminder_notified_diag_test.go` | 加 schema 门控 + 覆盖式设置 + `current_schema()` 验证 |
| `backend/internal/email/realprobe_test.go` | 同上（目标 schema 从 DSN 读出） |
| `backend/internal/email/pgscope_test.go` | 新增 `dsnSearchPathFromDSN` + `TestDsnSearchPathFromDSN`（7 分支）+ 补 import |
| `backend/internal/server/pg_test_isolation_guard_test.go` | 规则 5 + 3 个判据 + `TestRule5SearchPathJudgesAreNotVacuous`；更新 3 条登记理由 |
| `docs/handoff/2026-10-02-round16-...md` | 本文件 |

---

## 6. 验证

| 命令 | 结果 |
|---|---|
| `go vet ./internal/email/ ./internal/server/` | **exit 0** |
| `go test ./internal/email/ ./internal/server/ -count=1` | **exit 0** |
| `go vet ./...` | **exit 0** |
| `go test ./... -count=1`（排除并发会话的 `internal/wecom`） | 见 §8 |
| 负控 A：`dsnSearchPathFromDSN` 的空值守卫换成 `sp = "public"` | **exit 1**，2 子用例转红 |
| 负控 B：`reminder_notified` 去掉 RuntimeParams 赋值 | **exit 1**，规则 5 点名 |
| 负控 C：`searchPathAnyRe` 改回字面量 | **exit 1**，3 条反向用例转红 |

**`go test -race` 仍未跑**（需 w64devkit 的 `CC`，`$env:CC` 仅单次调用有效）。

---

## 7. 遗留风险

1. **规则 5 的残留盲区**：大部分查询带显式前缀、只有一处未限定的文件，
   判据看不见。要彻底覆盖需要解析 SQL，本轮明确不做。
2. **规则 5/3 的正则可被绕过**（拆字符串、`fmt.Sprintf`、任意命名）。
3. **`current_schema()` 验证只加在 4 个文件**（`diag_snippet_leak`、
   `diag_merge_exec`、`diag_rest_dupes`、本轮两个）。
   其余用 `RuntimeParams` 覆盖但没读回验证。
4. **登记理由仍是散文**——本轮把 20 条全核了一遍并写进 §1，
   但那是一次性快照，下轮改动后需重核。
5. **工作区 CRLF/LF 假 M 状态**：提交前用
   `git diff --ignore-cr-at-eol --stat` 确认真实变更。
6. **并发会话持续活动**（`config.go` / `config_writer.go` / `server.go` /
   `llm-gateway*` / `scripts/gw-*` / `wecom/`），本轮未触碰。
   `internal/wecom/TestDecryptFromNetFixture` 仍是 FAIL，但那是它未提交的新包。

---

## 8. 下一轮提示词

见文末。
