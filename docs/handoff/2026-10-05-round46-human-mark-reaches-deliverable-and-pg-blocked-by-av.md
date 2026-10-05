# round46 —— 人工标注终于传导到交付物；本机 PG 装回来了但被杀软挡住，DB 层结论仍不可复验

日期：2026-10-05 02:54 → 04:0x（本机时区 +08:00）
范围：定期 48 小时审计（拉取合并 / 分支清理 / 提交总结与批判 / 修正并推送）
基线：`b3527850`（开工时本地 main，与 origin/main 同一个 commit）

---

## 一、结论先行

| # | 事项 | 结论 | 证据强度 |
|---|---|---|---|
| 1 | 开工状态 | 本地 main == origin/main == `b3527850`，工作区干净，无 stash、无并发会话活动 | 强（实跑） |
| 2 | 后端编译/静态/全量测试 | `go build` / `go vet` / `go test ./... -count=1` **全 exit 0** | 强（实跑） |
| 3 | 前端门槛 | `run-gates.mjs` **30/30 通过**，197s | 强（实跑） |
| 4 | 48 小时内未合并的子分支 | **一个都没有**：本地只有 main、远端只有 `origin/main`、`--no-merged` 为空 | 强 |
| 5 | 悬空提交（分支被删但提交没进 main） | 16 个，**全部早于 48 小时**（最新 2026-10-01），且逐个核过：内容要么已在 main、要么是 stash 的 WIP 残留 | 强 |
| 6 | ⚠ 缺陷 A：人工标注没到交付物 | **已修**，并用**真实数字的产物**逐行核对 | 强（真产物 + 负控） |
| 7 | 缺陷 B：`check-marketplace-contract` 把「前置缺失」报成 exit 2 | **已修**（改 exit 3），19 道门禁现在**零模糊退出码** | 强 |
| 8 | 缺陷 C：48h 内 2 个**空提交**，其中一个标题声称「修复」 | **记录，未改历史**（见 §5.3） | 强（tree 哈希比对） |
| 9 | ⚠ 本机 PostgreSQL | **装回来了，但每个 backend 子进程都被杀**（0xC0000142）⇒ **DB 层结论本轮仍不可复验** | 强（见 §2） |
| 10 | 48h 内 313 个提交 | 按主题分组 + 挑高风险项实测审计，方法与**没审什么**写在 §6 | 如实标注 |

### 1b 最重要的一条：PG 回来了，但绿色的测试仍然是「无回归」而不是「跑过了」

round45 留下的第一顺位是「把 PostgreSQL 装回本机」。本轮装成了，**但它跑不起来**：

```
initdb                                     → exit 0（Success）
pg_ctl start（listen 127.0.0.1:5432）      → 监听成功，5432 可连
psql / 任意客户端一连上来                  → backend 立刻死
```

日志里每一条 backend 都是同一句：

```
LOG:  server process (PID 142004) was terminated by exception 0xC0000142
HINT:  See C include file "ntstatus.h" for a description of the hexadecimal value.
LOG:  terminating any other active server processes
...
LOG:  startup process (PID 130540) was terminated by exception 0xC0000142
LOG:  aborting startup due to startup process failure
```

`0xC0000142` = `STATUS_DLL_INIT_FAILED`，**DLL 初始化失败**。逐条排除过的其它解释：

| 假设 | 实测 | 结论 |
|---|---|---|
| 内存/提交额度耗尽（0xC0000142 的另一个常见成因） | 物理空闲 4.76 GB、虚拟空闲 8.47 GB、pagefile 已分配 33.9 GB / 在用 2.3 GB | **排除** |
| VC++ 运行时缺失 | `vcruntime140.dll` / `msvcp140*.dll` 都在，14.50.35719.0 | **排除** |
| 二进制本身坏了 | 直接 `postgres.exe --version` / `psql.exe --version` 都 exit 0 | **排除**（且说明不是「文件缺失/损坏」） |
| PATH 里找不到 pg\bin | 把它加进 PATH 后重试，backend 照样死 | **排除** |
| `pg_ctl` 造成的 job object 限制 | 绕过 pg_ctl 直接 `Start-Process postgres.exe` 起 postmaster，backend 照样死 | **排除** |
| 系统事件日志里有崩溃记录 | Application log 里**没有** postgres 的 faulting module 记录 | 无旁证 |
| WSL 里有 PG 可用 | `wsl -l -q` 无任何发行版 | 备选路径不存在 |

⇒ 剩下的最强嫌疑是**第三方杀软**：本机在册的 AV 有两个，其中一个是

```
displayName      productState
金山毒霸铠甲防御            331776
Windows Defender           397568
```

杀软的进程/DLL 注入防护打断子进程的 DLL 初始化，正是这个症状的典型形态
（postmaster 自己起得来，因为它不是被注入拦截的「子进程」）。

⚠️ **但我没有证成它。** 要证成需要在关掉杀软防护的条件下复跑，那属于改系统安全
状态，不该由我单方面做。**所以下面这句话必须原样转达：本轮 DB 层结论不可复验。**

⇒ 记法与 round45 相同、但原因换了：`go test ./... = 0 FAIL` 在本机当前状态下
仍然**只能算「没有回归」**。上轮是「PG 不存在所以 SKIP」，这轮是
「PG 在但 backend 起不来所以连不上」——**症状不同，账要分开记，别把两轮混成一句「测过了」**。

### 1c 可执行的下一步（一条命令就能验，需要你在杀软里操作）

PG 已经**解压并 initdb 完毕**，只差放行：

```
二进制：C:\workspace\pocket-opencode\.scratch\pgdist\pgsql
数据目录：C:\workspace\pocket-opencode\.scratch\pgdata（trust 认证，用户 postgres）
启动： pg_ctl -D .scratch\pgdata -l pg.log -o "-p 5432 -c listen_addresses=127.0.0.1" -w start
DSN：  postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable
```

在金山毒霸里把 `C:\workspace\pocket-opencode\.scratch\` 加进**信任/白名单目录**，
然后 `$env:POCKET_TEST_POSTGRES_DSN=<上面那串>` 跑
`go test ./internal/email/ ./internal/server/ ./internal/flashcards/ -v -count=1`，
**对比 round43 记的耗时基线**（设 DSN 时 email 172s / server 62s / flashcards 8s）——
耗时落在「不设」那一侧就说明还是没真跑。

> 注：本轮 PG 装在 `.scratch/`（已被 `.gitignore` 忽略）而不是 `D:\pg17`。
> 直接写 `D:\` 被本地硬安全策略拦下了（工作区外的递归写），不是没试。

---

## 二、基线取证（改前 → 改后）

### 2.1 后端

| | 改前 | 改后 |
|---|---|---|
| `go build ./...` | exit 0（31s） | exit 0 |
| `go vet ./...` | exit 0 | exit 0 |
| `go test ./... -count=1` | exit 0（96s） | exit 0（39s） |
| `node scripts/check-gofmt.mjs` | exit 0，966 个 .go、**真债 0** | 同 |

⚠️ 两条口径照旧分清：

- 后端的绿**含 §1b 的水分**（连不上库 ⇒ DB 判据 SKIP），只能算「无回归」。
- 前端的绿是**真跑**（`test:all` / `test:styles` / `test:stt` 等不依赖 PG）。

### 2.2 根目录 19 道 `check-*.mjs`：模糊退出码归零

| | 改前（round45 结束时） | 改后 |
|---|---|---|
| 绿（exit 0） | 14 | **14** |
| 前置缺失（exit 3） | 4 | **5** |
| 门禁自身报错（exit 2） | 1 | **0** |
| 真判红（exit 1） | 0 | 0 |

exit 3 的 5 道：`cdp-pid-strict` / `device-token` / `fts-triggers-device` /
`unlock-focus`（四条都要真机）+ `marketplace-contract`（要隔离后端 18101，本轮新归）。
**零模糊退出码**——一次全量扫描里，「判红」「环境没起」「脚本自己坏了」三者不再同形。

### 2.3 CI 跑什么（决定上面这些数算不算数）

- `.github/workflows/backend.yml`：`go build` / `check-smart-quotes` /
  `go test -race ./... -count=1`（**带 postgres:17 service，设 `POCKET_TEST_POSTGRES_DSN`**）/ `go vet`。
- `.github/workflows/frontend.yml`：`gates-parity` 跑 `node scripts/run-gates.mjs --ci`。

⇒ **CI 上 DB 测试是真跑的**。本机连不上库是本机环境问题，不是仓库问题。
这条要分清，否则会去改不该改的代码。

---

## 三、分支与残留：结论是「没有需要合并的东西」

### 3.1 未合并分支：0 个

```
git branch -r                      → origin/HEAD, origin/main（就这两个）
git for-each-ref refs/heads        → main（唯一）
git branch --no-merged main        → 空
git worktree list                  → C:/workspace/pocket-opencode b3527850 [main]（唯一）
git stash list                     → 空
```

### 3.2 悬空提交：16 个，**没有一个属于这 48 小时**

`git fsck --lost-found` 列出 16 个 dangling commit。逐个查日期与是否已在 main：

```
$ git fsck --lost-found | Select-String 'dangling commit' | % { … git show -s --format='%ad %s' … }
0b07385a 2026-09-30 test(imports): evernote-parser 补 node --test 单测
1428fa93 2026-09-30 fix(deploy): Dockerfile.frontend 钉 nginx stable
e33032df 2026-10-01 fix(server): mask_key 测试的 sk- 夹具定性为合成并更换
…
```

最新的是 **2026-10-01**，全部早于 48 小时窗口。且逐个 `git merge-base --is-ancestor`
确认**没有一条是 48 小时内丢的工作**。

唯一值得单独查的是 `1428fa93`（`Dockerfile.frontend` 钉 nginx stable）——
悬空提交意味着「有人做过但没进 main」的典型形状，所以核了当前 main 的内容：

```
$ Select-String -Path Dockerfile.frontend -Pattern 'nginx'
FROM nginx:1.24-alpine      ← 已钉死，不是 floating mainline
```

⇒ **不丢工作**：同一个修复由别的路径进了 main，悬空的那条是重复。

### 3.3 残留目录

上一轮（round45）清掉了 `openpocket-wt-a31` / `-upsert` / `-i18n2`。
本轮复查：根目录无 `openpocket-wt-*` 残留；本轮新增的 `.scratch/pgdist`、
`.scratch/pgdata`、`.scratch/round46` 都在 `.gitignore` 的 `.scratch/` 之下，
且门禁只扫 `scripts/` `backend/` `frontend/`（已逐个核过扫描根），不会被扫到。

---

## 四、缺陷 A（主项）：人工标注在台账里成立，在**交付物里不存在**

### 4.1 根因

round44 查实的结论本轮直接复现为代码形状：

```
人工标注写在        inv.LastError      （带 【人工标注】 前缀）
导出列定义是        invoiceSummaryHeader = {费用类型,对方单位,金额,币种,
                                             发票号,日期,状态,核验,文件名,来源邮件}
⇒ last_error 不在里面
```

而那两行营销横幅的**机械判据完全看不出问题**：

```go
func InvoiceCountsTowardTotal(inv Invoice) bool {
    return (inv.Status == "downloaded" || inv.Status == "filed") && inv.FilePath != ""
}
```

`status=downloaded`、`FilePath` 非空（落盘件确实是个文件，只是它是 572×140 的宣传图）
⇒ 判「已核验」⇒ 计入合计。**人已经看过并标注了，但这个判断进不了合计口径。**

⇒ 于是同一份数据出现两个数：台账里标着「不是发票」，汇总单里它是 61.1% 的金额。

### 4.2 修法（三处，判据仍只有一处）

| 位置 | 改动 |
|---|---|
| `ledger.go` | 新增 `InvoiceHumanMarkClass` / `InvoiceExcludedByHumanMark` / `InvoiceHumanNote`；`InvoiceCountsTowardTotal` 与 `InvoiceVerifiedLabel` 改为**同时**看人工分类；`LedgerRows` 加「备注」列；`LedgerCellRange` 10 → 11 列 |
| `pipeline.go` | `invoiceSummaryHeader` 加「备注」（**追加在末尾**，既有下标不动）；MD 表格加第 8 列；新增 `mdSafeCell` |
| 判据 | 新增 `invoice_human_mark_export_test.go`；更新 4 个钉住旧列宽的既有用例 |

**为什么不新开一个「标注过滤器」**：本仓已经因为「同一条规则手写三遍」吃过一次大亏
（`ledger.go` 的注释记着：LedgerRows 3,500 vs InvoiceListStats 61,500，差 17.6 倍，
而三处各测各的，任何一处漂移都不会被发现）。所以判据**仍然只有
`InvoiceCountsTowardTotal` 一处**，三条消费路径（LedgerRows / WriteInvoiceSummaryDocs /
InvoiceListStats）自动同口径。

**为什么核验列不复用「未核验」**：

```
未核验            ⇒ 还没核，待会会核 ⇒ 该重试采集
非发票凭证·不计入合计 ⇒ 已经核过了，结论是它不是发票 ⇒ 该追对账/删掉
```

跟进动作不同，用同一个词就是把区别抹掉。

**为什么用分类词表而不是 `strings.Contains(mark, "非发票")`**：
标注是自由文本，「非发票抬头缺失，需要销售方重开」这类句子里也含「非发票」三个字。
用子串匹配判**财务合计**会把不相干的行悄悄剔掉，而合算错时没有任何人会发现。
⇒ 词表外的写法一律按「没有声明」处理（该行仍按机械判据计入）。
**宁可多算也不悄悄少算**；要新增分类必须显式改 `invoiceHumanMarkExcludedClasses`。

### 4.3 判据自测（7 + 1 + 1 组，全部实跑）

| 组 | 覆盖 |
|---|---|
| `TestInvoiceHumanMarkClass_ParsesRealMarkShape` | 7 个子用例，用**真实标注原文**（`【人工标注】非发票凭证（营销横幅）：落盘件为 572×140…`），不是随手编的短串。含两个关键负控：句子里提到「非发票」但分类词不是它 ⇒ 不改口径；缺 `【人工标注】` 前缀 ⇒ 不是人工声明 |
| `TestInvoiceHumanMarkChangesTotal_AndSaysSo` | 口径与标签**同时**改，并断言两者一致（禁止「不计入但标签写已核验」） |
| `TestWriteInvoiceSummaryDocs_HumanMarkReachesDeliverable` | **打开真的 .md/.csv** 逐列核对 |
| `TestLedgerRows_HumanMarkReachesLedger` | 飞书台账那条消费路径同口径 |

#### 负控：把分类短路掉，判据必须转红

```
$（临时把 InvoiceHumanMarkClass 改成恒返回 ""）
--- FAIL: TestInvoiceHumanMarkClass_ParsesRealMarkShape   （3 个子用例）
--- FAIL: TestInvoiceHumanMarkChangesTotal_AndSaysSo
    被人工声明为非发票凭证的行仍计入合计
    核验标签 = "已核验"，want "非发票凭证·不计入合计"
--- FAIL: TestWriteInvoiceSummaryDocs_HumanMarkReachesDeliverable
    合计不是 3500.00
    合计行缺 3500.00：合计,,62500.00,,,,,,,,
    合计行里出现 62500.00 —— 被人工声明的行仍进了合计
```

⇒ 合计从 3,500 变回 **62,500**，正是 round44 记的那个「被横幅主导」的数。
**这证明新判据承重**，不是「跑绿了一次」。负控已撤销（`ledger.go` 里无残留）。

### 4.4 真产物核对（round44 §1d 的教训：只验库内回读不算数）

用 round43/round44 记录的**真实台账数字**复现一份汇总单
（全表 68416.21 / 汇总单口径 10392.21 / 横幅 6071.00 + 283.20 = 6354.20 / 扣除后 4038.01），
落盘后**逐行打开看**（产物在 `.scratch/round46/`，不在仓库里）：

`invoices-summary-20261005-032938.md`：

```
生成时间：2026-10-05 03:29 · 共 5 张（计入合计 2 张）· 合计金额 **4038.01**

| 费用类型 | 对方单位 | 金额 | 发票号 | 日期 | 状态 | 核验 | 备注 |
|---|---|---:|---|---|---|---|---|
| 交通 | 浙江沪杭甬高速公路股份有限公司 | 3500.00 CNY | 25332000000123456789 | 2026-09-28 | downloaded | 已核验 |  |
| 其他 | 杭州开轩科技有限公司 | 6071.00 CNY | …BANNER-1 | 2026-09-20 | downloaded | 非发票凭证·不计入合计 | 非发票凭证（营销横幅）：落盘件为 572×140（长宽比 4.09）平台宣传图，且与另一横幅行字节 SHA256 相同（9ced44f4…）… |
| 其他 | 杭州开轩科技有限公司 | 283.20 CNY | …BANNER-2 | 2026-09-20 | downloaded | 非发票凭证·不计入合计 | 非发票凭证（营销横幅）：… |
| 办公 | 云服务商 | 538.01 CNY | 26332000008261110741 | 2026-09-24 | downloaded | 已核验 |  |
| 其他 | 中国工商银行 | 58000.00 CNY |  | 2026-10-25 | pending | 非发票·不计入合计 | 非发票（信用卡对账单）：金额 58000.00 为**信用额度**、日期 2026-10-25 为**到期还款日**… |
```

`invoices-summary-20261005-032938.csv`：11 列，末列「备注」，合计行 `合计,,4038.01,,,,,,,,`。

**逐条对照 round44 留下的三个数**：

| | round44 记录 | 本轮产物 |
|---|---|---|
| 汇总单口径合计 | 10392.21 | — |
| 汇总单里被标注的部分 | 6354.20（61.1%） | 已剔出，**行仍在表里** |
| **财务若剔除假数据该看到的数** | **4038.01** | **4038.01** ✅ |

三个要点：

1. **合计从 10392.21 变成 4038.01** —— 标注第一次真正影响了口径。
2. **横幅两行仍在明细里**，金额、状态、理由都可见。删掉就再也看不见
   「有一笔 6071 需要人确认」，而这正是 ledger.go 记的原则
   「不计入合计 ≠ 从列表消失」。
3. **工行那行（58000，pending）也带上了标注**。它在改动前就不计入合计
   （status=pending），但此前它在汇总单里显示「未核验」——一个**误导性的**标签：
   「未核验」暗示「还没核」，而它已经被核过、结论是非发票。现在如实写「非发票·不计入合计」。

### 4.5 一个我自己写错又被判据抓到的断言（记下来）

第一版判据写的是「CSV 全文不含 58000.00」，红了。**判据是对的、我的断言是错的**：
被剔除的行**必须**仍列出它的金额。第一版把「剔除」误当成「隐藏」，
会让正确行为被判红。已改成「断言**合计行**不含它」+「反向断言明细里**必须**有它」。

⇒ 这正是「打开产物逐行看」的价值：只看测试全绿会把这个错断言一起提交进去。

### 4.6 顺带记一个**没有改**的既有形状

`ledgerTotalRow` 里「计入 N 张 / 共 M 张」这段文字一直落在**索引 8**，
也就是表头的「文件名」列（合计行的文件名格本来就是空的，所以看不出别扭）。
判据 `invoice_total_parity_test.go:161` 把这个下标钉住了。

本轮**没有动它**：移动它属于改既有契约、不是修本轮缺陷；真要挪得连同表头一起设计，
不能在「加一列」时顺手做（那正是本仓反复栽的「顺手扩范围」）。
已在 `ledger.go` 的函数注释里把这个位置写成显式记录，避免下一个人以为是 bug 去「顺手修」。

---

## 五、缺陷 B / C

### 5.1 缺陷 B：`check-marketplace-contract` 把「前置缺失」报成 exit 2

round45 §6 遗留项 4 的后半段。它有 4 处 `process.exit(2)`，其中：

| 位置 | 情形 | 本轮处置 |
|---|---|---|
| 解析不出 `api.ts` 的 base | **判据自身跑不起来** | **保留 exit 2**（并在原地写清为什么） |
| 隔离后端 18101 不通 | 前置缺失 | **改 exit 3** |
| 登录不通、拿不到 token | 前置缺失 | **改 exit 3** |

为什么这不只是数字洁癖：`run-gates.mjs` 的约定是
**「退出码 ≥3 = 护栏在拒绝给结论（没扫到被检查对象）」**，
而 1 与 2 都不在这个区间。一次全量扫描里，看到「红」的人会先怀疑判据/脚本，
而这条门禁**最常见的失败原因就是隔离后端没起**——把它归到「判据坏了」那一档，
就会有人去改不该改的代码。round45 对三道真机门禁做的正是同一件事，本轮把它补齐。

实测（当前无 18101 后端）：

```
$ node scripts/check-marketplace-contract.mjs ; echo $?
api.ts 的 base = /api/marketplace
抽出 9 条路径
[前置缺失] 隔离后端 http://127.0.0.1:18101 不通（ERR:fetch failed）—— 这次没有跑到被检查对象，
  所以退出码是 3 而不是 1。先起隔离后端再重跑；不要把这一条当成判红去排查。
3
```

全量扫描后：**19 道门禁 = 14 exit 0 / 5 exit 3 / 0 exit 2 / 0 exit 1**。
`check-exit-reflects-verdict.mjs` 自检 7/7、实扫 exit 0。

### 5.2 端口漂移的老教训（这次差点又踩）

记忆里那条「代理端口会漂，先 `Test-NetConnection` 验活」与本轮无关，但同族的
**「本机 PG 端口/tcp 状态会漂」**是真的：本轮 `Test-NetConnection 127.0.0.1 -p 5432`
在 PG 活着时返回 `True`，在它 crash-loop 后返回 `False`。
**「端口通」只说明 postmaster 在监听，不代表 backend 能起来**——
这与「`ls-remote` 失败不等于 push 失败」是同一类：
观测手段成立 ≠ 被观测对象成立。

### 5.3 缺陷 C：48 小时内 2 个**空提交**，其中一个标题声称「修复」

```
$ 对 48h 内每个非 merge 提交比 tree 哈希
EMPTY 42e162eb fix(email): 让配置同步的 LWW 守卫在交互式 UI 路径上真正生效
EMPTY 2a3bc73a docs(handoff): 更正上一提交的说明——内容是 round37 第十五节

$ git rev-parse '42e162eb^{tree}' '42e162eb^^{tree}'
12fd8776c4593c12f2792152402e201cd794418b
12fd8776c4593c12f2792152402e201cd794418b      ← 同一棵树
```

LWW 那个修复的**真正内容在 `c02dfb11`**（前端 4 个文件 + 178 行用例），
`4cdf09ba` 是同一批的文档部分。也就是说：

> **`git log` 不可当作「哪些改动落地了」的索引。**
> 一个 tree 为空、标题写着「让…真正生效」的提交，会让人去 `git show 42e162eb` 核对修复，
> 而那里什么都没有。

**本轮没有改历史**（`main` 上重写提交要 rebase 整个后续链，且本仓有并行会话共用 main，
风险不对等）。也没有为此加门禁——「最近 N 个提交里没有空提交」这种判据会随历史
永远命中，属于 round45 刚清掉的那类「永远红的门禁」，加它就是加债。

⇒ 记在这里，供下一个人核对 LWW 修复时**直接看 `c02dfb11`**。

---

## 六、48 小时内 313 个提交：我怎么审的，以及**没审什么**

```
$ git rev-list --count --since='48 hours ago' origin/main   → 313
按前缀：Merge 83 / fix 79 / docs 72 / test 47 / feat 11 / diag 7 / style 6 / chore 3 / wip 2 / refactor 2
改动最密集：backend/internal 392 文件次 · frontend/src 182 · docs/handoff 131
```

**不假装逐条读完。** 313 个提交里绝大多数是 `docs(handoff)` 与 `fix(email)`
的连续迭代，逐条「批判」只会产出流水账。实际做法：

1. 按主题归类（email 发票链路 / maestro 与 harness / UI 与门禁 / 闪卡 / STT / 文档）；
2. 对**会改变生产行为**的提交，读 diff 找「声明与实现是否一致」；
3. 对**新增护栏**的提交，验它是否真的有区分力（而不是「跑绿了一次」）；
4. 对**已落档但未实现**的决定，当成一等公民去找。

本轮据此定位到的具体问题就是 §4、§5.1、§5.3、§1b。

抽样读过并确认无问题（读的是 diff，不是 commit message）：

| 提交 | 查了什么 | 结论 |
|---|---|---|
| `c02dfb11` LWW 守卫 | 是不是三条同名提交里的重复实现 | 不是重复：代码在 `c02dfb11`，文档在 `4cdf09ba`，`42e162eb` 是空提交（§5.3） |
| `1428fa93` nginx 钉版 | 悬空提交是否意味着丢工作 | 不丢：main 的 `Dockerfile.frontend` 已是 `FROM nginx:1.24-alpine` |
| `8593145b` 真库 upsert 护栏 | 提交说明里「CI 里恒 skip / 把 main 判红」是否还成立 | 已在 main；本机无 DSN 无法复跑，**未验证**，见 §7 |
| `b3527850` / `dfbf2473` 债务棘轮 | 接线与基线是否还在 | `check-fixed-cdp-ports` 148/基线 148/新增 0、`check-dev-pass-sourcing` 26/26/0，全绿 |

**没审的**（如实列，不假装覆盖）：

- `docs(handoff)` 类 72 个提交：只核了「结论与当前代码是否还对得上」里的 3 条，
  没有逐篇复读 40+ 篇 handoff。
- `frontend/src` 182 文件次的 UI/z-index/shell 改动：只验了它们没弄红
  `run-gates.mjs`（含 `check:styles` / `z-index-ladder` / `bottom-chrome`），
  **没有逐个组件核对视觉与交互**。
- maestro 24 条流：只验了 `check-maestro-flows.mjs` exit 0，**没有真机复跑**
  （本机无 Android 设备）。
- 闪卡 / STT / 会议 / vault / identity 等包的 PG 判据：**本机一条都没真跑**（§1b）。

---

## 七、遗留风险（如实记，不藏）

1. **⚠️ 最重要：本机 PostgreSQL 连不上。** PG 已装好并 initdb 完成，但每个 backend
   子进程死于 `0xC0000142`（DLL 初始化失败），最强嫌疑是**金山毒霸铠甲防御**的
   注入防护，**未证成**。⇒ round43/round44 的全部 DB 层结论本轮**仍不可复验**，
   包括本轮修的这条导出口径——**我是用记录下来的真实数字复现产物核对的，
   不是对着真库核对的**。放行杀软后必须对着真库再跑一遍
   （`BuildInvoiceSummaryDocs` → 打开新产出的 `invoices-summary-*.md` 逐行看）。
2. **`check-marketplace-contract` 的 exit 3 路径没有「后端在时」的对照。**
   本轮只验到「后端不在时正确报 3」。起 18101 隔离后端后必须复跑一次，
   确认改动没破坏正常路径（与 round45 对三道真机门禁留的同一类风险）。
3. **`invoiceHumanMarkExcludedClasses` 词表只有 2 个分类。**
   将来出现第三种「人工声明不算发票」的情形，必须显式加进这张表，
   否则该行仍按机械判据计入合计——**这是刻意的失败方向**（宁可多算不悄悄少算），
   但它是一个**需要人记得**的约定。护栏见 §4.3 的负控子用例。
4. **飞书台账的列宽从 10 变 11。** 已核 `PublishLedger` 每次都
   `CreateSpreadsheet` 新建（复用的只是 URL、不重写），所以新表是 11 列。
   ⚠️ 但**已发布过的旧表仍是 10 列**，而它被复用时不会被重写 ⇒ 旧表上看不到「备注」列。
   要不要重建旧表，本轮**未决**（需要用户决定，因为重建会产生一个新链接）。
5. **两个空提交未处理**（§5.3）。`git log` 仍会误导人去 `git show 42e162eb`。
6. **本轮没有真机验证**（无 Android 设备，adb `192.168.31.19:5555 not found`）。
   5 道 exit 3 的门禁只验到「设备/后端不在时正确报 3」。
7. **`ledgerTotalRow` 的张数落在「文件名」列**（§4.6）：已记录、未改。
8. 本轮新增的 `.scratch/pgdist`（约 1 GB）、`.scratch/pgdata`、
   `.scratch/round46` 留在本机。它们在 `.gitignore` 内、不入库；
   要腾空间可自行删除整个 `.scratch`（注意里面有上一轮遗留的 `meetings/` 等）。

---

## 八、下一轮提示词

```
接着 pocket-opencode round46 做，全部要对照证据，不要凭声明：

1. ⚠️ 第一顺位：把 PG 真正跑起来，再验本轮改的导出口径。
   round46 已把 PostgreSQL 17.5 解压并 initdb 到
   C:\workspace\pocket-opencode\.scratch\pgdata（trust 认证），但**每个 backend
   子进程死于 0xC0000142（DLL 初始化失败）**；已排除内存/VC 运行时/二进制损坏/
   PATH/pg_ctl 五种解释，Application 日志无记录，WSL 无发行版。
   最强嫌疑是**金山毒霸铠甲防御**（未证成）。先在杀软里把
   C:\workspace\pocket-opencode\.scratch\ 加白名单，然后：
     $env:POCKET_TEST_POSTGRES_DSN='postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable'
     go test ./internal/email/ ./internal/server/ ./internal/flashcards/ -v -count=1
   **对比 round43 耗时基线**（设 DSN 时 email 172s / server 62s / flashcards 8s）——
   落在「不设」那一侧就说明还是没真跑。
   ⚠️ 注意「Test-NetConnection 5432 = True」**只说明 postmaster 在监听**，
   不代表 backend 能起来（round46 §5.2）。判据要看 SKIP 是否消失。

2. PG 通了之后，对着**真库**重跑一次本轮的验收（round46 用的是记录下来的真实数字，
   不是真库）：
     跑一次 BuildInvoiceSummaryDocs → 打开新产出的 invoices-summary-*.md 逐行核对 →
     确认横幅两行是「非发票凭证·不计入合计」、合计 = 4038.01、工行行是
     「非发票·不计入合计」且仍在明细里。**只验库内回读不算数**（round44 §1d）。

3. round46 遗留待决：飞书台账已发布的旧表仍是 10 列（复用时不重写），
   看不到新增的「备注」列。要不要重建（会产生新链接）需要用户拍板。

4. `invoiceHumanMarkExcludedClasses` 现在只有 {非发票凭证, 非发票}。
   若真库里出现第三种人工声明（且是「不该计入合计」），必须显式加词表——
   词表外的写法会**继续计入合计**（刻意方向：宁可多算不悄悄少算）。
   先查真库里现存标注的分类词，别凭印象加。

5. 起 18101 隔离后端复跑 `node scripts/check-marketplace-contract.mjs`，
   确认 exit 3 的改动没破坏「后端在时」的正常路径（round46 只验了「不在时」）。

6. 接上 Android 设备后复跑 5 道 exit 3 的门禁，确认「设备不在时」的新分类
   没破坏正常判定（round45/46 都只验了缺设备那一侧）。

7. 核对 LWW 修复时**直接看 c02dfb11**，不要看 42e162eb——后者是空提交，
   tree 与父提交完全相同，标题却写着「让…真正生效」（round46 §5.3）。

8. 仍未处理：`ledgerTotalRow` 的「计入 N 张 / 共 M 张」落在表头的「文件名」列
   （索引 8，被 invoice_total_parity_test.go 钉住）。要挪得连同表头一起设计，
   别在加列/改列时顺手做。

红线：main 当前前后端都绿，但**后端那份绿里含大量 SKIP**（连不上库）；
说「测试通过」之前先说清是「无回归」还是「跑过了」。
另：`git log` 不是「哪些改动落地了」的索引——48h 内有 2 个空提交。
```

---

## 九、提交与推送记录

| 时间 | 事件 |
|---|---|
| 02:54 | `git fetch`；本地 main == origin/main == `b3527850`；工作区干净；无 stash；reflog 无并发会话痕迹 |
| 03:0x | 后端 build/vet/test 全 exit 0；前端 `run-gates.mjs` 30/30（197s） |
| 03:0x–03:2x | 分支/悬空提交/残留目录审计（§3） |
| 03:1x | PG 17.5 便携版解压 + initdb 到 `.scratch/pgdata`；起库成功但 backend 全部 0xC0000142（§1b） |
| 03:2x–03:4x | 缺陷 A 修复 + 判据 + 负控 + 真产物核对（§4） |
| 03:4x | 缺陷 B 修复（§5.1）；19 道门禁退出码分布复测 |
| 04:0x | 前端 `run-gates.mjs` 复跑；写本 handoff；提交并推送 |

取消耗时但有据的一次尝试：本想把 PG 装到 `D:\pg17`（你选的那个路径），
被本地硬安全策略拦下（工作区外的递归写），改装到 `.scratch/pgdist`。
**不是没试你说的方案**，是那条路径本轮写不进去。
