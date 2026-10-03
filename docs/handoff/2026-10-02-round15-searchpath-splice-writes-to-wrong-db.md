# round15：复核登记理由时挖出两个真实缺陷——search_path 拼接会「打错库」

日期：2026-10-02
分支：main（无新分支）
上一轮：round14（`47286a27`）给 PG 护栏补了规则 4
本轮承接：round14 §7 遗留风险第 1 条「规则 4 保证写语句被登记，不保证登记的理由今天仍然成立」

---

## 0. 本轮最重要的一条

round14 我写下「规则 4 守住了不许偷偷加 DELETE，但守不住三重开关今天还拦得住吗」
——然后去人工核对那些理由，**第一份就核出了问题**。

复核 `diag_merge_exec_test.go` 的登记理由时发现：它用的
`hasSearchPath` / `appendSearchPath` 正是 **PG 护栏规则 3 明令禁止的 DSN 拼接**。
而护栏当时判它**干净**——因为规则 3 只匹配 `dsn+"&search_path="` 这个**字面**
形态，而拼接被包进了辅助函数。

**护栏的规则 3 和被它批评的那个文件，用的是同一种错误写法。**

---

## 1. 缺陷一：`diag_merge_exec_test.go` 打错库

### 原写法

```go
func hasSearchPath(dsn string) bool { return strings.Contains(dsn, "search_path") }
func appendSearchPath(dsn, schema string) string {
    sep := "?"
    if strings.Contains(dsn, "?") { sep = "&" }
    return dsn + sep + "search_path=" + schema
}
...
if !hasSearchPath(dsn) { dsn = appendSearchPath(dsn, schema) }
pool, err := pgxpool.New(ctx, dsn)
```

### 关键事实（实测，不是记忆）

Go 的 `url.Values.Get` 取**第一个**同名参数，pgx 走这条路径：

```
DSN 形态                                        hasSearchPath  实际 search_path   结果
无 search_path                                   false→追加    opencode_pocket   ✅
?search_path=opencode_pocket（与目标同名）        true→不追加   opencode_pocket   ✅（靠巧合）
?search_path=public（**与目标不同**）             true→不追加   public            ❌ 打错库
?search_path=mytest（**与目标不同**）             true→不追加   mytest            ❌ 打空库
```

护栏规则 3 的注释写「pgx 取第一个」——**这句是对的**，本轮实测确认。
但它对后果的描述不完整。

### 为什么这个文件格外危险

它的两条路径**不一致**：

| 用途 | SQL 形态 | 实际查哪个库 |
|---|---|---|
| 备份检查（`:76`） | `FROM <schema>.emails_merge_backup_...` | `POCKET_REAL_MAIL_SCHEMA` 指定的库 |
| 业务查询（`:91` 起） | `FROM emails`（未限定） | **DSN 里那个** search_path 指向的库 |

于是会出现：**「备份检查通过」与「写操作打在别处」同时发生**。
对一个要在真实库执行合并写操作的文件（`TombstoneDupeEmails`，只打墓碑但仍是写），
这是必须堵死的。

### 修法

```go
cfg, err := pgxpool.ParseConfig(dsn)
cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
pool, err := pgxpool.NewWithConfig(ctx, cfg)
// 关键：当场验证，而不是「以为钉住了」
var resolvedSchema string
pool.QueryRow(ctx, `SELECT current_schema()`).Scan(&resolvedSchema)
if resolvedSchema != schema { t.Fatalf(...) }
```

两个辅助函数**已删除**——保留一个已知有害的辅助函数没有意义，
而且它们的存在本身就会触发新加的护栏判据。

---

## 2. 缺陷二：同一个错误在第二个文件

`diag_rest_dupes_test.go` 用了同一对函数（同包，所以能编译）。
它是**只读**诊断，严重度低一档，但同样会「打到别处输出假结论」
（它查重复候选，打到空库会输出「没有重复」）。

修法相同：`ParseConfig` + `RuntimeParams` 覆盖 + `MaxConns=1` 保留。

> 教训：删函数时 `go vet` 立刻报 `undefined: hasSearchPath`，
> **正是这一点让我发现了第二个使用者**。编译器在这里立了功。

---

## 3. 护栏规则 3 的第二个盲区

原判据只匹配字面拼接。补上辅助函数判据：

```go
var dsnSearchPathHelperRe = regexp.MustCompile(`\b(append|with|set|add|build)\w*SearchPath\s*\(`)
```

新增 `TestSearchPathHelperJudgeIsNotVacuous` 钉住它：5 个正向（各种封装命名）+
4 个反向（**正确的覆盖式设置绝不能被判红**，否则会逼人留着有害写法）。

### 正则的固有局限（必须如实说明）

把字符串拆开再拼、或用 `fmt.Sprintf`，都能绕过它。
**这条规则降低误用概率，不提供保证。** 真正的兜底是代码里那句
「拼好之后用 `SELECT current_schema()` 读回来验证」。

---

## 4. 负控记录（含一次我自己犯的错）

### 负控 1：把 `RuntimeParams` 覆盖换回旧逻辑

`TestDiagMergeExecSearchPathIsPinned` 的 5 个子用例如期转红，错误信息是
「新写法应把 search_path 覆盖为 "opencode_pocket,public"，实际 …」。

### 负控 2（我犯的错）：负控变异没有区分力

第一次给规则 3 的辅助函数判据做负控，我注入的函数名是
`appendSearchPathProbe`。判据**没抓到**，`NEG_EXIT=0`。

差点就此宣布「判据不行」——但实测发现是**我注入的名字不对**：

```
appendSearchPath(dsn, schema)          HIT
appendSearchPathProbe(dsn, schema)     miss   ← 我用的这个
```

`\b(append)\w*SearchPath\s*\(` 要求 `SearchPath` 后**紧跟** `(`，
而 `Probe` 挡在中间。判据本身是对的。

**这正是「负控变异必须是语义 no-op 的反面」那条教训**：转不红时，
先逐格核对变异与判据的对应关系，别急着改判据。改用
`appendSearchPath` 后立刻转红，错误信息是
「调用了疑似『把 search_path 拼进 DSN』的辅助函数」。

### 负控 3：规则 3 新判据本身

注入 `func appendSearchPath(...)` 后护栏如期判红点名文件。注入已撤销。

---

## 5. 改动文件

| 文件 | 变更 |
|---|---|
| `backend/internal/email/diag_merge_exec_test.go` | search_path 改覆盖式设置 + `current_schema()` 验证；删除两个有害辅助函数；新增 `TestDiagMergeExecSearchPathIsPinned` |
| `backend/internal/email/diag_rest_dupes_test.go` | 同一缺陷的修正 |
| `backend/internal/server/pg_test_isolation_guard_test.go` | 新增 `dsnSearchPathHelperRe` + 规则 3 第二段判据 + `TestSearchPathHelperJudgeIsNotVacuous`；更新 `pgAllowlistedWrites` 里 `diag_merge_exec_test.go` 的理由串 |
| `docs/handoff/2026-10-02-round15-...md` | 本文件 |

---

## 6. 验证

| 命令 | 结果 |
|---|---|
| `cd backend; go vet ./internal/email/ ./internal/server/` | **exit 0** |
| `go test ./internal/email/ ./internal/server/ -count=1` | **exit 0** |
| `go vet ./...` | **exit 0** |
| `cd backend; go test ./... -count=1` | **1 FAIL：`internal/wecom`** —— 那是并发会话**未提交的新包**（`git ls-tree HEAD` 里没有），与本轮改动无关，见 §7.6 |
| 同上，**排除该包**：`$pkgs = go list ./... \| ? {$_ -notmatch 'internal/wecom'}; go test $pkgs -count=1` | **exit 0** —— HEAD 范围内全绿 |
| 负控 1（覆盖式→旧逻辑） | **exit 1**，5 个子用例转红 |
| 负控 2（注入 `appendSearchPath`） | **exit 1**，规则 3 点名文件 |

**`go test -race` 仍未跑**（需 w64devkit 的 `CC`，`$env:CC` 仅单次调用有效）。

> **【2026-10-02 round17 订正】这句话是错的，且它被 round13/14/15/16 连续照抄了 4 轮。**
> round17 实际跑了一次：`RACE_EXIT=0`，ok 53 / FAIL 0 / no test files 18，
> 并用「必然触发 data race 的探针」证明 detector 确实启用（报
> `WARNING: DATA RACE`、exit 1），排除了「静默降级成普通模式」的可能。
>
> 本行保留原样，因为它记录的是**当时的状态**；但**不要把它当成限制引用**。
> 教训见 round17 §6：**抄来的限制不是证据**，每轮都该重新试一次。

---

## 7. 遗留风险

1. **规则 3 的辅助函数判据可被绕过**（拆字符串、`fmt.Sprintf`、任意命名）。
   它降低误用概率，不提供保证。真正的兜底是 `current_schema()` 验证——
   **这条验证目前只在 diag_merge_exec_test.go 里，其余文件没有**。
2. **其余豁免文件的登记理由本轮只核了 2 份**
   （`diag_snippet_leak_test.go` 的三重开关 + 拒绝缺省 schema、
   `diag_merge_exec_test.go` 的备份表非空检查）——两份都**仍然成立**。
   但 `fetcher_greenmail_test.go` / `diag_kxpms_test.go` / `spam_realdata_test.go`
   等仍未逐条核对。
3. **同包的辅助函数是共享的**：`hasSearchPath` 被两个文件共用，
   改一个必须查全部。`go vet` 的 `undefined` 是唯一的系统性提示。
4. **工作区 CRLF/LF 假 M 状态**：提交前必须用
   `git diff --ignore-cr-at-eol --stat` 确认真实变更。
5. **并发会话持续活动**（`config.go` / `config_writer.go` / `server.go` /
   `llm-gateway*` / `scripts/gw-*`），本轮未触碰。
6. **`go test ./...` 当前有 1 个 FAIL：`internal/wecom/TestDecryptFromNetFixture`**
   （`PKCS#7 填充字节非法`）。核实结论：**与本轮无关**——
   `git ls-tree -r HEAD | grep wecom` 为空，该包是并发会话正在建的
   **未提交**新包。判定方法：`git ls-tree -r HEAD --name-only | Select-String wecom`
   无输出即证明它不在 HEAD 上。**不要**把这个 FAIL 记成自己引入的回归，
   也不要为了让它变绿去改别人的在途文件。

---

## 8. 下一轮提示词

> **本节已于 2026-10-02 round17 补写。** 最初这里是空的「见文末」——
> 我在最终回复里说了「见文末」却从未写过它。已补，并顺带把本文档
> §6 里那句错误的「`-race` 仍未跑」加了订正批注。
> 教训：**承诺的产物要核对它真的存在**，空标题和缺失内容长得一样。

```
接手 openpocket（仓库 C:\workspace\openpocket，Go module 在 backend/），
继续 24 小时修正任务的审计与完善。上一轮是 round15
（docs/handoff/2026-10-02-round15-searchpath-splice-writes-to-wrong-db.md）：
复核登记理由时发现 diag_merge_exec_test.go / diag_rest_dupes_test.go
用 DSN 拼接设 search_path——正是 PG 护栏规则 3 禁止的形态，而护栏判它
干净（只匹配字面量，拼接被包进辅助函数后整套隐身）。

本轮请按序做：

1. 【并发前置】先跑 git worktree list / git log --oneline -5 /
   git status --porcelain，再看目标文件 mtime。**禁止**对别人正在编辑的
   文件跑 `git checkout HEAD --`（round11 失实提交 0728aa11 的成因）。

2. 【本文档 §7 遗留，round16/17 已部分闭合】
   - 其余豁免文件的登记理由未逐条核对 → **round16 已核完 20 条**，
     并发现 reminder_notified_diag_test.go / realprobe_test.go
     「从不设 search_path」，后者已在 round16 修掉。
   - 规则 3 的辅助函数判据可被绕过（拆字符串、fmt.Sprintf、任意命名）。
     真正的兜底是 current_schema() 验证。
   - go test -race 至今没在 HEAD 上跑成过 → **round17 已推翻**：
     真跑 exit 0，并用必然触发 data race 的探针证明 detector 确实启用。
     抄来的限制不是证据。

3. 机械普查尚未闭合的缺陷类别：
   - **恒假判据**：`\bUPDATE\s+[A-Za-z_]\b` 恒假（[A-Za-z_] 只吃一个字符，
     尾部 \b 几乎永不成立）。正确写法 \w+。普查报「0 命中」时先拿一个
     **已知存在**的样本过一遍同一判据，验证判据本身会命中。
   - **恒真判据**：匹配字面量（而非赋值形态）会被运行时字符串喂成恒真。
     sfnt.GlyphIndex(buf, r) 对「缺失」返回 (0, nil)，只判 err == nil 恒真。
   - 不实日期：「实测 / 审计 / 审计记录 / 审计实测」几种措辞都要查。
   - 空 catch / 吞错。
   - RE2 vs JS 引擎差异：Go 正则无回溯，判据必须在最终运行的引擎里验。

4. 【方法论，务必执行】每轮 handoff 里的「仍未验证 / 跑不了 / 无法执行」
   都应**至少重新试一次**。round12 那条「-race 跑不了」被连续照抄 4 轮，
   实际跑一次就绿了。同理，本文 §6 那句也已加订正批注。

5. 【工程细节】
   - 工作区是 **CRLF**、HEAD 是 LF。批量脚本正则用 \n 结尾会静默 MISS。
     `git status` 显示 M 但 `git diff` 无输出时，用
     `git diff --ignore-cr-at-eol --stat` 看真实变更。
   - 收尾：「下一轮提示词」那节必须真的写内容，写完核对不是空标题。
```
