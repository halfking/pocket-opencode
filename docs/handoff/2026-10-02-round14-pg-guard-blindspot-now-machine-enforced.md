# round14：把 pgSafeWithoutIsolation 的盲区从「注释声明」变成「机器强制」

日期：2026-10-02
分支：main（无新分支）
上一轮：round13（`7c82b594`）——移除 monitor.aliyun.com 域名后门 + 重写那条护栏
本轮承接：round13 §5 遗留风险第 2 条「`pgSafeWithoutIsolation` 豁免是真盲区」

---

## 0. 本轮最重要的一条

**「有写语句」此前只是一次性观察，现在是不变式。**

round11 用负控证明了旧护栏的盲区：往 `diag_credential_health_test.go` 注入

```go
pool.Exec(ctx, "DELETE FROM email_accounts")
```

护栏**依然绿**。当时给出的结论是「列入本表后护栏就完全跳过该文件，
改这个文件的人必须自己保证不引入写语句」——并把这写进注释，当作已知的
不可消除的代价。

本轮证明它**可以**消除，而且代价极小。

---

## 1. 根因：豁免一次性放行了全部检查

旧护栏的判定顺序是：

```go
if reason, ok := pgSafeWithoutIsolation[rel]; ok {
    t.Logf("allowlist: %s — %s", rel, reason)
    return nil          // ← 一次性跳过规则 1/2/3 的后续检查
}
```

`pgSafeWithoutIsolation` 回答的是**一个**问题——「不隔离 schema 为什么安全」——
但 `return nil` 顺带放行了**所有**与「写」相关的检查。语义被悄悄放大了：
「我豁免隔离」变成了「我豁免一切」。

---

## 2. 改动

### 2.1 新增规则 4（独立于规则 2 生效）

文件：`backend/internal/server/pg_test_isolation_guard_test.go`

```go
if _, exempt := pgSafeWithoutIsolation[rel]; exempt && hasSQLWrite(code) {
    if reason, ok := pgAllowlistedWrites[rel]; ok {
        t.Logf("allowlist(含写语句): %s — %s", rel, reason)
    } else {
        t.Errorf(...)
    }
}
```

新增 `pgAllowlistedWrites` map，逐个登记「在豁免表里、且确实含写语句」的文件
及可核查的理由。两个 map **独立**，意味着豁免隔离与豁免写是分开裁定的。

### 2.2 作用域限定在豁免表内（这是踩过坑才定下来的）

第一版把规则 4 写成**全仓扫描**，结果判红 **77 个文件**。逐个查证后确认
绝大多数是假阳性，分两类：

- **已正确隔离**的文件（`internal/task/store_test.go` 等自建 `*_test_` schema
  的助手）——它们本来就知道自己写在隔离库里，登记「我写了什么」纯属仪式；
- **Go 标识符撞上 SQL 动词**：
  - `time.Now().UTC().Truncate(time.Second)` ← `TRUNCATE` 撞 Go 方法名
  - `t.Fatal("oversized response/status update missing")` ← `UPDATE\s+\w+` 撞普通英文短语
  - `grant := computeGrant(user)` ← `GRANT` 撞 Go 标识符

结论是**收窄作用域**而不是给正则加限定词。理由是代价不对称：
加限定词必然让某些真 SQL 漏掉，而漏掉的代价是**在生产库上删表**；
误报的代价是补一行理由。而盲区从来只存在于豁免表内——表外的文件
要么被规则 2 强制隔离，要么根本不开 PG 连接。所以收窄是**准确的**，不是妥协。

---

## 3. 普查判据本身曾经恒假（第二个要点）

为了写规则 4，我先做了一次全表普查。判据写的是：

```js
/\b(INSERT\s+INTO|UPDATE\s+[A-Za-z_]|DELETE\s+FROM|...)\b/gi
```

普查结果把 `internal/email/diag_snippet_leak_test.go` 报成 **0 写语句**——
而它的 L133 明确写着：

```go
tag, err := pool.Exec(ctx,
    `UPDATE email_accounts SET last_synced_uid = 0, last_synced_at = 0
```

这与 allowlist 里手写的「全文只有 1 条写语句」**直接矛盾**。

### 根因

`[A-Za-z_]` 只吃**一个**字符 `'e'`，而尾部的 `\b` 要求 `'e'` 后面是**非单词字符**
——实际是 `"email_accounts"` 里 `"mail_accounts"` 的 `'m'`，属于单词字符，
于是 `\b` 恒不成立。**该分支恒假，且不报任何错。**
同族的 `INSERT INTO` / `DELETE FROM` / `CREATE (TABLE|SCHEMA|INDEX)` 一并恒假。

实测对照：

| 正则 | 命中 |
|---|---|
| `\bUPDATE\s+[A-Za-z_]\b` | **0** |
| `\bUPDATE\s+[A-Za-z_]`（去掉尾部 `\b`） | 1 |
| `\bUPDATE\s+email_accounts\b` | 1 |

### 为什么没被立刻发现

因为**两侧看起来都是对的**：手写注释说「1 条写语句」，机器判据说「0 条」，
但机器判据从不与注释交叉核对。而「恒假」在"干净"的输入上看起来与
「正确」完全一样。

**教训：交替分支里的 `[A-Za-z_]` 后面接 `\b` 是个静默陷阱。**
正确写法是 `\w+`。

---

## 4. 护栏的自检与负控

### 4.1 新增 `TestCensusSQLWriteReIsNotVacuous`

钉住 `sqlWriteRe` 的**每一条**分支都有承重能力：12 个正向用例
（UPDATE / INSERT / DELETE / DROP×3 / CREATE×3 / TRUNCATE / ALTER / GRANT），
加上反向用例（只读语句、注释里的动词、`COALESCE(deleted_at,0)=0` 读条件）。

同时把「已知且接受的假阳性」写成断言——`time.Truncate`、
`"status update missing"`、`grant :=` 这三条**必须**命中。
它们红不是 bug，是把取舍钉住，防止有人「优化」判据时顺手改坏。

> 这里也有一个我自己的失误值得记：我一度把 `TestCaptureTruncatesLongSummary`
> 当成假阳性样例写进断言，测试立刻转红。实测才发现 `"Truncates"` 的词尾 `s`
> 挡住了 `\bTRUNCATE\b`，它**不**命中。**假阳性样例必须实测，不能凭印象写**——
> 否则钉住的取舍是假的。

### 4.2 负控复测（旧盲区的直接证明）

往 `diag_credential_health_test.go` 注入**与 round11 完全相同**的那段：

```go
_, _ = pool.Exec(ctx, "DELETE FROM email_accounts")
```

| | round11（旧护栏） | round14（新护栏） |
|---|---|---|
| 结果 | **绿** | **红** |
| 信息 | 无 | 点名文件 + 给出两条修法 |

```
internal/email/diag_credential_health_test.go: 在 pgSafeWithoutIsolation 里被豁免，
且出现了 SQL 写语句，但没有登记到 pgAllowlistedWrites。
```

注入已撤销。**这证明规则 4 有承重能力。**

---

## 5. 更新了 4 条豁免注释

`diag_credential_health_test.go` / `diag_real_invoice_extract_test.go` /
`diag_real_invoice_gate_test.go` 三条的理由字符串都写着
「本条目使护栏完全跳过该文件，写语句无机器守护」——**这句已过期**。
已改为「规则 4 会在本文件出现写语句时判红」。

同时在 `pgAllowlistedWrites` 里登记了 5 个确实含写语句的豁免文件：
`fetcher_greenmail_test.go`（build tag greenmail）、`diag_snippet_leak_test.go`
（三重开关 + 写路径拒绝 schema 缺省）、`pgscope_test.go`（隔离助手）、
`diag_merge_exec_test.go`（三道闸门，UPDATE 是 t.Logf 回滚语句）、
`diag_schema_present_test.go`（CREATE SCHEMA 只出现在提示文本里）、
`third_party/identity-go/shadow/dao_test.go`（vendored）。

---

## 6. 验证

| 命令 | 结果 |
|---|---|
| `cd backend; go vet ./...` | **exit 0** |
| `cd backend; go test ./... -count=1` | **exit 0** — ok **53** / FAIL **0** / 无测试 **18** |
| 定向：`TestPGTestsNeverTargetTheProductionSchema` / `TestCensusSQLWriteReIsNotVacuous` / `TestStripGoComments` | **exit 0** |
| 负控（注入 DELETE） | **exit 1**，准确点名 |

**`go test -race` 仍未跑**（需 w64devkit 的 `CC`，`$env:CC` 仅单次调用有效）。

---

## 7. 遗留风险

1. **规则 4 只保证「写语句被登记」，不保证「登记的理由仍然成立」。**
   理由仍是手写的散文。机器能守住「不许偷偷加 DELETE」，守不住
   「三重开关今天还拦得住吗」。后者仍需人核对。
2. **豁免表本身不检查 schema 隔离是否真的到位**——一个文件可以登记
   「我只在 POCKET_DIAG_RESET_ACCOUNT 时写」，但那个开关的判断逻辑
   不在护栏视野内。
3. **`sqlWriteRe` 的假阳性方向是已知的、有意的**（见 §2.2）。
   若要收窄精度，代价是可能漏掉真 SQL。这条取舍已写成断言。
4. **并发会话持续活动**：本轮我在 `pg_test_isolation_guard_test.go` 上工作时，
   对方正在改 `config_writer.go` / `llm-gateway*` / `scripts/gw-*` 等文件。
   提交前须重新核实 `git status`。
5. **`.scratch-sttdev/` 下的普查脚本**（`write-census.cjs` 等）是本轮的
   一次性工具，**JS 正则与 Go RE2 行为不同**（`(?i)` 内联标志 JS 不支持），
   结论以 Go 护栏为准。

---

## 8. 下一轮提示词

```
接手 openpocket（仓库 C:\workspace\openpocket，Go module 在 backend/），
继续 24 小时修正任务的审计与完善。上一轮是 round14
（docs/handoff/2026-10-02-round14-pg-guard-blindspot-now-machine-enforced.md）：
给 PG 隔离护栏补了规则 4——豁免「不隔离 schema」不再等于豁免「写」，
allowlist 内的文件出现 SQL 写语句必须登记进 pgAllowlistedWrites，否则判红。
负控复测（注入 pool.Exec(ctx, "DELETE FROM email_accounts")）：旧护栏绿、新护栏红。

本轮请按序做：

1. 【并发前置，务必先做】这个仓库有并发会话长期共用同一 main 工作区。
   动手前先跑：
     git worktree list
     git log --oneline -5
     git status --porcelain
   再看目标文件的 mtime。**禁止**对别人正在编辑的文件跑 `git checkout HEAD --`
   （round11 的失实提交 0728aa11 就是这么来的，已记入 round12 §1）。
   自己的改动放独立 worktree，或先确认文件不在别人的在途集合里。

2. 【round14 遗留，优先级最高】规则 4 只保证「写语句被登记」，不保证
   「登记的理由仍然成立」。具体缺口：
     - diag_snippet_leak_test.go 的理由是「三重开关 + 写路径拒绝 schema 缺省值」。
       请核对 POCKET_DIAG_RESET_ACCOUNT / POCKET_DIAG_ALLOW_RESET 与
       「schema 缺省 != opencode_pocket」这个拒绝条件今天是否还拦得住。
     - diag_merge_exec_test.go 的理由是「备份表不存在或为空时 t.Fatal」。
       核对该 t.Fatal 是否仍在写操作之前。
   这是散文，机器守不住，只能人核。核完把结论写进 pgAllowlistedWrites 的理由串。

3. go test -race 至今没在 HEAD 上跑成过（round12 §2.1）。要跑必须：
     $env:CC='C:\tools\w64devkit\w64devkit\bin\gcc.exe'; $env:CGO_ENABLED='1'
     go test -p 2 -race -count=1 ./...
   $env:CC 只在**单次 bash 调用内**有效，漏设会退化成「-race 跑不了」的假象。
   连推含大二进制文件的提交时也要用它（send-pack unexpected disconnect）。

4. 机械普查尚未闭合的缺陷类别：
   - **恒假判据**（round14 刚踩过，见下）：交替分支里的 [A-Za-z_] 后面接 \b
     是静默陷阱——[A-Za-z_] 只吃一个字符，尾部 \b 几乎恒不成立。
     正确写法是 \w+。普查报「0 命中」时，先拿一个**已知存在**的样本过一遍
     同一个判据，验证判据本身会命中。
   - **恒真判据**：sfnt.GlyphIndex(buf, r) 对「缺失」返回 (0, nil)，
     只判 err == nil 会恒真——必须连零值一起判并加必然缺失的对照样本。
   - 不实日期：「实测 / 审计 / 审计记录 / 审计实测」几种措辞都要查。
   - 空 catch / 吞错。
   - RE2 vs JS 引擎差异：Go 正则无回溯。判据必须在最终运行的引擎里验。

5. 【已知的假阳性方向，不要「顺手优化」】sqlWriteRe 的 TRUNCATE / GRANT
   无限定词，UPDATE\s+\w+ 会撞普通英文短语。首次把规则 4 写成全仓扫描时
   判红了 77 个文件，收窄作用域才落地。这条取舍已写成断言
   （TestCensusSQLWriteReIsNotVacuous 末尾三条），改判据前先读那段。
   假阳性样例必须实测，不能凭印象写——我曾把 TestCaptureTruncatesLongSummary
   当样例，实际它不命中（词尾 s 挡住 \b）。

6. 收尾：更新对应 handoff，提交（git diff --cached --stat 逐条核对文件列表与
   提交信息相符），推送前 git fetch 确认无并发新提交，推送时用
     $env:GIT_SSH_COMMAND='ssh -o ServerAliveInterval=15 -o ServerAliveCountMax=20 -o TCPKeepAlive=yes'
```
