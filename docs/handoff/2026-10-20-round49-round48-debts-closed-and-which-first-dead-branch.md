# round49 —— round48 §5 四项遗留全部销账；判据改成「让 gofmt 自己当判据」；顺带挖出 which-first 的 PATH 死分支

日期：2026-10-20
基线：`51814ed7`（`merge: 合入 origin/main（131 提交）`）
承接：`docs/handoff/2026-10-18-round48-race-green-and-doc-comment-negctl-trap.md` §5「遗留（本轮未动，如实登记）」

## 一句话结论

round48 §5 登记的 **4 项遗留全部销账**：

1. **PG 路径实跑** —— 按设计文档 §777.4 的三条安全前置，用**一次性专用空库容器**跑通
   （全程不碰 5432 上那个**别的项目**的生产库）；
2. **`.gitattributes`** —— 补 `*.go text eol=lf`，`git add --renormalize .` 实测 **0 个文件变化**
   （no-op，不制造伪 diff）；
3. **块注释 doc 缺口** —— `check:doc-empty-string-literal` 的判据**整体重写**为
   「JS 找候选 + **真实 gofmt 输出**裁决」，`/* */` 形式已覆盖（round48 §5.3 销账）；
   顺带修掉手写规则与真实 gofmt 的 **4 处不一致**（全是漏报）；
4. **race 新快照** —— `go test -race -count=1 ./...` ⇒ **EXIT 0 / 61 ok / 0 FAIL / 0 DATA RACE**，
   并用 NEGCTRL 探针证明 **detector 确实武装**。

★★ 本轮还挖出并修掉一个**影响全部门禁**的隐性缺陷：
`scripts/lib/which-first.mjs` 的 **PATH 分支恒不成立**
⇒ 本仓门禁此前实际调用的是 `/usr/local/go/bin/gofmt`（**go1.24.5**），
而 CI 是 `setup-go` + `backend/go.mod`（**go1.27.x**）
⇒ **本地门禁与 CI 门禁一直是两套工具**，且这两套对 doc comment 的判定**结论相反**（见 §3.2）。

---

## 1. 遗留逐条销账

### 1.1 遗留①：`internal/server` 的 PG 路径实跑 ✅

§777.4 的三条前置**全部按原文执行**：

| 前置 | 动作 | 读数 |
|---|---|---|
| ① 专用空库 DSN | 起一次性容器 `opencode-pocket-pgtest-r49`（`postgres:17`，user/db/password 均 `pocket_test`，`127.0.0.1:55499`，healthcheck `pg_isready` → healthy） | `current_database=pocket_test`、PostgreSQL **17.11**、schema 仅 `public`、`pg_tables` 计数 **0** ⇒ 空库专用 |
| ② 先跑静态护栏 | `go test ./internal/server -run TestPGTestsNeverTargetTheProductionSchema -count=1 -v` | **PASS（0.71s）**，扫 **709** 个 `_test.go`；护栏把全部 allowlist 条目及其写操作登记逐条打印出来（= 前置③要读的豁免表） |
| ③ 逐条读豁免表 | 读 `pgSafeWithoutIsolation` / `pgAllowlistedWrites` 全表 | 含写语句的条目（`diag_purge_injected_invoices_test.go`、`diag_snippet_leak_test.go`、`fetcher_greenmail_test.go`、`pgscope_test.go`、`diag_claimdue_race_test.go`、`third_party/identity-go/shadow/dao_test.go`）均有**三重开关 / build tag / 自建 schema** 保护 |

实跑（**显式 `unset POCKET_POSTGRES_DSN PG_DSN POCKET_REAL_MAIL_DSN`**，防回落生产）：

```
$ POCKET_TEST_POSTGRES_DSN=postgres://pocket_test:...@127.0.0.1:55499/pocket_test?sslmode=disable \
    go test ./internal/server -count=1 -v
EXIT=0     23 个 SKIP，但 **0 个** skip 的原因是 “POCKET_TEST_POSTGRES_DSN not set”
```

⇒ **PG 路径确实被跑到**（不是「编译了但不执行」）。实证锚点：
`TestPGAuditWriterFlows`、`TestPGAuditHTTPExportPaginationAndTenantIsolation`、
`TestMarketplace_BlobUploadDownloadPG`、`TestPGIsolationNegativeControlMatrix`、
`TestPGTestsNeverTargetTheProductionSchema` 等均实跑 PASS。

**外加一轮带 race 的**（把遗留①与遗留④合起来验，见 §4）：

```
$ POCKET_TEST_POSTGRES_DSN=... go test -race -count=1 -v ./internal/server/
EXIT=0    PASS 770 / FAIL 0 / SKIP 23 / DATA RACE 0
          “POCKET_TEST_POSTGRES_DSN not set” 计数 = 0
```

⚠ **安全边界**：5432 上跑的是 `llm-gateway-pg`（Citus）——**别的项目的生产库**，
§777.4 前置①明令禁止指向它 ⇒ 本轮**全程未向 5432 做过任何写入**，
另起一次性容器，用完即删。

### 1.2 遗留②：`.gitattributes` 缺 `*.go text eol=lf` ✅

round41 §7.1 的原文要求**只有这一行**，作用域刻意最小。

```
$ cat .gitattributes
*.go text eol=lf

$ git check-attr text eol -- backend/cmd/pocketd/main.go
backend/cmd/pocketd/main.go: text: set
backend/cmd/pocketd/main.go: eol: lf
$ git check-attr text eol -- frontend/android/gradlew.bat scripts/maestro-run.mjs
frontend/android/gradlew.bat: text: unspecified      ← 未被波及
scripts/maestro-run.mjs:   text: unspecified      ← 未被波及
```

**renormalize 是 no-op**（这是本条最要紧的读数 —— round41 §7.1 担心的就是「牵动一次 renormalize」）：

```
$ git ls-files --eol '*.go' | awk '{print $1}' | sort -u
i/lf        ← 1133 个 .go 全部已经是 LF
$ git add --renormalize .
$ git diff --cached --stat HEAD   →  只有本轮有意改的 3 个文件
$ git diff --stat                  →  空（工作树 == 暂存区）
```

⚠ **没有采用 broad 的 `* text=auto`**：实测它会 churn **8 个文件**，
其中 `frontend/android/gradlew.bat` 是 Windows 批处理，**必须保持 CRLF**
（`scripts/maestro-run.mjs` 同理，且有 CRLF 脆弱针门盯着它）
⇒ 本次只声明 Go 源码，其余文件保持现状，**不顺手改别人**。

### 1.3 遗留③：本门不覆盖 `/* */` 块注释形式的 doc ✅

这一项没有「补一条规则」了事，而是**把判据整体换掉**。

**为什么必须换**：旧门禁是**手写规则**去模仿 gofmt 的 doc-comment 行为。
2026-10-20 用真实 gofmt 逐例对拍，**手写规则与真实 gofmt 有 4 处不一致，且 4 处全是漏报**：

| 样本 | 手写规则（旧） | 真实 gofmt | 旧门禁 |
|---|---|---|---|
| 整块都是 tab 缩进的 `//` doc | 不报 | **改写** | ❌ 漏报 |
| 整块都是 4 空格缩进的 `//` doc | 不报 | **改写** | ❌ 漏报 |
| 函数体内、列 1 的 `//` 注释 | 不报 | **改写** | ❌ 漏报 |
| 多行 `/* */` 块 doc | 不报 | **改写** | ❌ 漏报（= round48 §5.3 登记的那个缺口） |

根因（一行）：★ **「gofmt 会怎么改写」这件事，真值只有 gofmt 自己知道**；
手写一份规则去模仿它，就是 §8.2 说的「抄一份实现再比一遍」——
Go 的 doc comment 格式化规则**跨版本变过**，手写副本会随 Go 升级**静默腐烂**。

**新判据**（`scripts/check-doc-empty-string-literal.mjs`）：

1. **JS 只负责找候选**：Go 感知的扫描器 `scanCommentRanges` 区分行注释 / 块注释（不嵌套、可跨行）/
   解释字符串 / 原始字符串 / 字符字面量 ⇒ SQL 字符串字面量里的 `''` 天然不入选；
   `findQuotePairOffsets` 只取**注释内**的 `''`，按**不重叠、左到右**配对
   （与 `go/doc/comment/parse.go` 的 `case strings.HasPrefix(t, "''")` 贪心消费一致：
   `''''` 切 2 对、`'''` 切 1 对）。
2. **gofmt 的真实输出裁决**（不是自洽往返）：
   - `C_base` = `gofmt(src)` 里的 `”`（U+201D）个数，`C_src` = 原文里的个数
     ⇒ `C_base > C_src` ⟺ 本文件有 `''` 被改写
     （`”` 的**唯一产地**就是 `case strings.HasPrefix(t, "''")`，且 gofmt 从不删已有的 `”`）；
   - 逐候选把该处 `''` 换成**等长哨兵 `QQ`** 重跑 gofmt ⇒ `”` 计数变少 ⇒ 就是这一处；
     ★ 用 `QQ` 而不是 `""`：后者落在字符串字面量里会提前闭合字符串（实测直接语法错）；
   - ★ **自洽性核对**：命中数必须**等于** `”` 的增量；不等 ⇒ 逐处归因不可靠
     ⇒ 该文件判「**无法判定**」（既不报红、也不放过）。
3. **oracle 锁版本**（见 §3.2）：只认与 `backend/go.mod` 声明的大版本一致的 gofmt。

**验收（三件套，都不是「门绿就算」）**：

| # | 做什么 | 读数 |
|---|---|---|
| ① 正控 | 在**真 doc comment**（`store_select_scan_parity_test.go` L76 `paritySite` 的 doc）注入 `''`，`//` 形态 | 新门禁 **EXIT=1** 且点名 `:76`；**gofmt 独立量具**确认 `''`→`”`（`gofmt <file> \| sed -n '76p'`）⇒ 门与量具一致 |
| ② **差分** | 同一棵被污染的树，跑 `git show HEAD:scripts/check-doc-empty-string-literal.mjs`（**旧门禁**） | 旧门禁 **EXIT=0（漏）**、新门禁 **EXIT=1（红）** ⇒ §5.3 的缺口**真的补上了**，不是「我以为补上了」 |
| ③ 还原 | `git show HEAD:<path> > <path>` 写回**被改的那个路径** | md5 `b0c5e9eb3e8b19bd865ebeaa66a4f17b` 前后一致 + `git diff` 空 |

⚠ **还原纪律**：读备份自己 = 恒真 = 假读数 ⇒ 校验必须读**被改的那个路径**（本轮照此执行）。

`--selftest` **51 例**（9 扫描 / 14 敏感度 / 15 特异度 / 3 变盲 / 2 自指 / 7 oracle / 1 与真实 gofmt 交叉核对）
全绿，`MIN_SELFTEST_CASES` 下限闸**按当前实跑条数棘轮到 51**（语义「只许增不许减」：
删掉任何一条都必须**手改这个常量**才能过 ⇒ 「悄悄少测一条」从此必须是一个显式的决定）。

★ 其中 **5 条 oracle 用例专打「找不到匹配版本 ⇒ 判无法判定」那条失败分支**。
这条分支是「**量具失效时门会不会静默放行**」的唯一防线，而它从前只是一段**从没跑过的代码**
（`resolveAuthoritativeGofmt` 原来把候选表写死在函数体里 ⇒ 无法在不改动生产代码的前提下测它
⇒ 顺手把它拆成纯函数 `pickMatchingGofmt(candidates, declared)`，薄封装 `resolveAuthoritativeGofmt()` 仍读真候选表）。
**变异验牙**（每种坏法各打中不同分支）：

| 变异 | 期望 | 实测 |
|---|---|---|
| M1 去掉大版本校验（`version.minor === declared` → `version`） | 「版本不符的候选必须被拒绝」转红 | ✅ 转红 |
| M2 连 `declared` 守卫一起去掉 | 上一条 + 「声明读不到（null）⇒ 判无法判定」转红 | ✅ 转红 2 条 |

其中「版本不符的候选必须被拒绝」用的是**真二进制 + 伪造 VERSION**：
把真的 gofmt 复制到临时目录、给它写一个 `go1.99.0` 的 `VERSION` 文件
⇒ 它**能跑**，但版本对不上 ⇒ 必须被拒绝（不许「反正它能跑就拿来用」）。
其中「与真实 gofmt 交叉核对」那一条**逐例比较本门裁决与真实 gofmt 的输出**——
Go 的行为若变，会当场报红，而不是继续用一份过期清单自说自话。

### 1.4 遗留④：race 结果是一次快照 ✅

按 round48 的**同款口径**重跑，拿到**当前 HEAD** 的新快照：

```
$ cd backend && go test -race -count=1 ./...
EXIT=0
ok 61 / FAIL 0 / no test files 17 / DATA RACE 0 / panic 0
real 1m25.7s
最慢五包：internal/server 45.605s / agent 37.164s / email 26.317s
          livetest 14.843s / repohygiene 13.605s
```

（round48 那次是 60 ok / 15 无测试文件 —— 其后落了 82 个提交，包数涨了。）

**detector 武装自证**（沿用 round17 §5.1 的形状）：

```
$ go test -race -count=1 -run TestNEGCTRL ./zz_negctrl_race_probe/
WARNING: DATA RACE
WARNING: DATA RACE
--- FAIL: TestNEGCTRLRaceDetectorIsActuallyArmed (0.00s)
PROBE_EXIT=1
```

⇒ detector 确实武装，上面那份全绿是**真在 race 模式下的绿**，不是静默降级跑出来的。
探针是**环境探针不是回归测试**，跑完已删（`mavis-trash`）。

---

## 2. 顺带修掉的一个隐性缺陷：`which-first.mjs` 的 PATH 分支是死代码

```js
// scripts/lib/which-first.mjs（修前）
const r = execFileSync('which', [c], { encoding:'utf8', stdio:['ignore','pipe','ignore'] })
const first = (r.stdout || '').split(/\r?\n/)...       // ← r.stdout 恒 undefined
if (r.status === 0 && first) return first               // ← r.status 恒 undefined
```

**根因（一行）**：★ `execFileSync` 的返回值**就是 stdout 的字符串本身**（不像 `spawnSync` 返回结果对象）
⇒ `r.stdout` / `r.status` 恒为 `undefined` ⇒ 「PATH 命中」这个分支**恒不成立**
⇒ 每次都跳过 PATH、落到后面的**绝对路径**候选。

**后果不是「找不到工具」，而是「静默挑错工具」**：

```
$ node -e "import('./scripts/lib/which-first.mjs').then(m=>console.log(m.whichFirst(m.gofmtCandidates())))"
/usr/local/go/bin/gofmt        ← go1.24.5   （PATH 上明明是 /opt/homebrew/bin/gofmt = go1.27.2）
```

⇒ 本仓的 `check-gofmt.mjs` 与（重写后的）`check:doc-empty-string-literal.mjs`
**此前用的都是 go1.24.5**，而 CI 是 `setup-go` + `backend/go.mod`（**go1.27.x**）
⇒ **本地门禁与 CI 门禁一直是两套工具**。

修法（一行语义更正）：`const stdout = execFileSync(...)` → 直接用这个字符串；
`execFileSync` 在 `which` 未命中时**抛异常**（退出码非 0）⇒ 已被 `catch` 覆盖 ⇒ 顺带下一个候选。

**连带修掉**：`scripts/device-install-preflight.mjs` 里那份**同 bug 的副本**
（2026-10-17 收编 `adb-cdp.mjs` 时漏掉的一处）⇒ 改用 lib 这个**单一来源**，
不再有第二份解析。（`scripts/maestro-run.mjs` 用的是 `spawnSync`，本来就是对的。）

---

## 3. 第二个发现：gofmt 的**版本**会改变结论

### 3.1 现象

同一份源码，两个 gofmt 给出**相反**的答案：

```go
package p
type T struct {
// X has '' for empty
X int
}
```

| gofmt | 结果 |
|---|---|
| `/usr/local/go/bin/gofmt`（**go1.24.5**） | **改写**成 `// X has ” for empty` |
| `/opt/homebrew/bin/gofmt`（**go1.27.2**） | **原样保留** `''` |

### 3.2 根因（读源码，不是推理）

`go/printer/printer.go` 的 `intersperseComments` 里，doc comment 的判定条件：

```go
// go1.24.5
if p.lastTok != token.IMPORT &&                    // do not rewrite cgo's import "C" comments
	p.posFor(p.comment.Pos()).Column == 1 &&
	p.posFor(p.comment.End()+1) == next {

// go1.27.2
if tok != token.IDENT &&                          // ← Go 1.25 新增
	p.lastTok != token.IMPORT &&
	p.posFor(p.comment.Pos()).Column == 1 &&
	p.posFor(p.comment.End()+1) == next {
```

⇒ struct 里**紧跟字段名**（`token.IDENT`）的列 1 注释，**Go 1.25 起不再算 doc comment**。

### 3.3 处置

本仓 CI 是 `setup-go` + `go-version-file: backend/go.mod`（**go 1.27.1**）
⇒ 判据的 oracle 必须是**声明工具链**那一个，否则同一份代码在两台机器上给相反结论
（把 CI 认为干净的文件报红）。

⇒ 新门禁**按 `backend/go.mod` 声明的大版本过滤 gofmt 候选**：
逐个候选探测版本（`GOROOT/VERSION` → `go version -m`），
取第一个大版本一致的；**一个都没有 ⇒「无法判定」exit 1**，不是「通过」
（★ 这条**失败分支已由 5 条 selftest 用例 + 变异验证**，见 §1.3 末段——
它是「量具失效时门会不会静默放行」的唯一防线）。
⇒ 门禁每次都会把 oracle 打印出来（`oracle: /opt/homebrew/bin/gofmt（go1.27.2，backend/go.mod 声明 Go 1.27）`），
**读数可审计**。

### 3.4 ⚠ 自己引入的隐患：两道门可能各用一个 gofmt ⇒ 已收编成**单一来源**

复核时发现一个**由本轮改动自己引入**的隐患：

- 新门禁按**版本**过滤候选；
- 而 `check-gofmt.mjs` 仍用 `whichFirst(gofmtCandidates())`（= PATH 优先）；
- ⇒ 在一台**同时装着多个 Go 版本**的机器上（**本机就装着 1.24.5 与 1.27.2**，
  只差 PATH 指向谁），两道门可能**各用一个 gofmt** ⇒ 各说各话。

实测对照（把 PATH 指向旧的 1.24.5）：

```
$ PATH=/usr/local/go/bin:$PATH which gofmt
/usr/local/go/bin/gofmt                       ← go1.24.5
修之前（whichFirst）  → /usr/local/go/bin/gofmt = go1.24.5   ← check-gofmt 会用它
修之后（版本锁定）    → /opt/homebrew/bin/gofmt = go1.27.2   ← 两道门都用它
```

⇒ 修法：把 oracle 的解析**提成 `scripts/lib/gofmt-oracle.mjs`（单一来源）**，
两道门都从它取；并让 `check-gofmt.mjs` 也**按声明大版本锁定**、
且**把 oracle 打印出来**（两道门的读数可当场对照）：

```
$ node scripts/check-gofmt.mjs
· oracle（判据用的 gofmt）  : /opt/homebrew/bin/gofmt（go1.27.2，backend/go.mod 声明 Go 1.27）
$ node scripts/check-doc-empty-string-literal.mjs
oracle: /opt/homebrew/bin/gofmt（go1.27.2，backend/go.mod 声明 Go 1.27）
```

⚠ `check-gofmt.mjs` 找不到匹配版本时同样 **exit 1「无法判定」**——
这是把它原有的「找不到 gofmt ⇒ exit 1」契约**按同一条纪律**延长，
而不是新增一条更严的规则。

---

## 4. 验证

| 命令 | 结果 |
|---|---|
| `node scripts/check-doc-empty-string-literal.mjs --selftest` | EXIT 0 — 实跑 **51** / 声明 51 |
| 变异 M1（去掉 gofmt 大版本校验） | **EXIT 1** — 「版本不符的候选必须被拒绝」转红 |
| 变异 M2（连 `declared` 守卫一起去掉） | **EXIT 1** — 2 条转红 |
| `node scripts/check-doc-empty-string-literal.mjs` | EXIT 0 — 全仓 **0 命中**（附 oracle 打印） |
| 同上（在 `frontend/` cwd 下跑，验证 ROOT 不依赖 cwd） | EXIT 0 — 0 命中 |
| 正控：`//` 形态真 doc 注入 `''` | **EXIT 1** — 点名 `store_select_scan_parity_test.go:76`；gofmt 独立量具一致 |
| 差分：旧门禁（`git show HEAD:`）跑同一棵污染树 | **EXIT 0（漏）** ⇒ 缺口确实补上 |
| 块注释形态真 doc 注入 `''` | **EXIT 1** — 点名 `:77`；gofmt 独立量具确认改写 |
| 还原核对 | md5 `b0c5e9eb...` 前后一致，`git diff` 空 |
| `go test ./internal/server -run TestPGTestsNeverTargetTheProductionSchema -count=1 -v` | **PASS（0.71s）** — 709 个 `_test.go` |
| `POCKET_TEST_POSTGRES_DSN=… go test ./internal/server -count=1 -v` | **EXIT 0** — 0 处因 DSN 未设而 skip |
| `POCKET_TEST_POSTGRES_DSN=… go test -race -count=1 -v ./internal/server/` | **EXIT 0** — PASS 770 / FAIL 0 / **DATA RACE 0** |
| `go test -race -count=1 ./...` | **EXIT 0** — ok 61 / FAIL 0 / DATA RACE 0（1m25.7s） |
| NEGCTRL 探针 | DATA RACE ×2 / FAIL / exit 1 ⇒ detector 武装（探针已删） |
| `node scripts/check-gofmt.mjs` | EXIT 0 — 1117 个 .go，0 真债（并打印 oracle） |
| **PATH 指向 go1.24.5 时两道门的 oracle** | **同一个**（都是 go1.27.2）⇒ 不会各说各话（对照见 §3.4） |
| `node scripts/run-gates.mjs --ci`（**CI job 的原样命令**） | **EXIT 0 — 全部 36 项通过**（26.6s）⇒ CI 不会被我推红 |
| `npm run gates`（frontend/） | **EXIT 0 — 全部 47 项通过，用时 55.6s** |

---

## 5. 遗留（如实登记）

1. **race 仍然是「一次快照」**（沿用 round17 §5.1 的口径）：它证明的是
   「2026-10-20 在 `51814ed7` 上没有 data race」，不是「这个仓库没有 data race」。
   **新增并发代码需重跑。**
2. **`.gitattributes` 只声明了 `*.go`**：其余文本文件仍依赖各机自己的 `core.autocrlf`
   （本机未设 ⇒ 现状 `i/crlf` 的 `gradlew.bat` / `maestro-run.mjs` /
   `test-evidence/.../*.headers` 保持 CRLF）。这是**刻意**的：broad 的 `* text=auto`
   会 churn 8 个文件且会破坏 `gradlew.bat`。若将来要扩大范围，**单独一轮做**。
3. **48h 审计线的 §8.7 四件事不在本轮范围**（`handoff/2026-10-17-48h-audit-and-fixes.md`）：
   简报 119 三个问待属主拍板、7 道新门的「自洽判据」回扫 ⇒ 需要属主决策，不擅自动。
4. ~~**`check-gofmt.mjs` 本身没有版本锁**~~ ⇒ ✅ **本轮已关掉**（见 §3.4）：
   两道把 gofmt 当判据的门现在**共用同一个 oracle**（`scripts/lib/gofmt-oracle.mjs`），
   都按 `backend/go.mod` 声明的大版本锁定 ⇒ 不可能各说各话，也不可能与 CI 不一致。
5. 本轮**未触碰** 5432 上的 `llm-gateway-pg`（别的项目的生产库），也未改动
   `handoff/2026-10-17-48h-audit-and-fixes.md` 提到的 `ViewSegmentBar.vue` WIP
   （属另一条任务线，仍留在工作树）。
