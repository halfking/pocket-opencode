# round40 —— 收尾轮：SQL 空串字面量被写成弯引号、第三次并发推送、以及一次「改完等于没改」的假象

日期：2026-10-04 04:33 → 05:05
分支：`audit/2026-10-04-main-health`（承接 round39）
基线：`efcede7a` → 合并并发会话后为 `c1e4228d`

---

## 0. 结论先说

**round39 的三件事已收尾（推送 `efcede7a`），本轮修掉一个此前没看见的缺陷类，并做掉分支清理。**

1. **新缺陷类（本轮修）**：代码与 SQL 注释里，**SQL 的空串字面量 `''`
   被写成 `”`（U+201D）**，共 7 处。已逐处订正，并加一道门禁
   `scripts/check-smart-quotes.mjs`（selftest 19/19，负控实测会红），
   接入 `backend.yml` 的 build-gate。
2. **分支清理（本轮做掉 3 个）**：`audit/gofmt-debt-20261003`、
   `fix/i18n-nested-placeholder-crash`、`fix/email-upsert-messageid-conflict`
   三个分支已并入 main 且无独占改动，已删除（含 worktree `wt-a32`）。
3. **一个方法论级的坑（本轮最贵的一课）**：**本机上「改完了」不等于
   「真实文件改到了」**。详见 §4。

---

## 1. SQL 空串字面量被写成弯引号（7 处）

### 1.1 现象

    // `snippet = CASE WHEN EXCLUDED.snippet <> ” THEN … ELSE emails.snippet END`
    // 方法是**全量覆盖**，importance 被写成 ” 或 'normal'。
    //   - smtpPassword 省略 → 保留原凭证；传 ” → 清空

这 7 处的注释用途恰恰是**逐字记录一段 SQL / 一个 API 语义**
（「空串 = 不覆盖」是本仓 upsert 的核心取舍）。写成 `”` 之后，
注释描述的语义与代码实际执行的**不再一致**，而读者没有任何提示 ——
弯引号在中文排版里是正常字符，眼睛会自动略过。

最直接的证据是 `importance 被写成 ” 或 'normal'`：
**同一个句子里，`''`（空串）与 `'normal'`（字面量）用了两种不同的引号。**

### 1.2 7 处清单

| 文件 | 记录的语义 |
|---|---|
| `internal/email/fetcher.go` | upsert 的 `EXCLUDED.snippet <> ''`（空串=不覆盖） |
| `internal/email/store.go` | 全量覆盖时 `importance` 被写成 `''` 或 `'normal'` |
| `internal/email/store_importance_constraint_test.go` | `importance <> ''` 的约束讨论 |
| `internal/server/server_assistant.go` | `smtpPassword` 传 `''` 即清空 |
| `internal/chatagent/store.go` | 内置角色 `workspace_id=''` 全局可见 |
| `internal/chatagent/sqlite_store.go` | 内置行按 `workspace_id=''` 定位 |
| `internal/task/store_contract_test.go` | `COALESCE(workstream_id, '')` 含逗号 |

### 1.3 根因不是工具，是**生成**

搜过 `scripts/**` 与 `.github/**`：`&rdquo;` / `&#8221;` / `u201d` /
两连单引号替换**全部零命中**。⇒ 没有批量改写留下这些字符，
它们是**写注释时**产生的。没有生成器可改，
只能逐处订正 + 门禁挡复发。

### 1.4 门禁：`scripts/check-smart-quotes.mjs`

判据是「**这一行的开合是否配平**」，而不是「有没有弯引号」——
中文正文里的弯引号是成对的（`“副作用型”`），成对出现必须放过：

- 只有 `”`/`’` 没有 `“`/`‘` → **落单** → 报
- 只有 `“`/`‘` 没有 `”`/`’` → **落单** → 报
- 两者都有但顺序反了（`”…“`）→ 报
- 成对出现 → 不报

另有一条豁免：`’` 夹在两个 ASCII 字母之间（`don’t`）是英文撇号。
少了这一条，一个英文单词就会让门禁误报 —— 误报的门禁会被
`--list | head` 忽略掉，比没有更糟。

**实测：敏感 7/7、特异 2/2。** 敏感侧逐条照抄上表真实字符串；
特异侧覆盖 2 处成对正文引号、英文撇号、ASCII 单引号、无引号代码。
`--selftest` 19 条（含变盲与自指豁免），19/19 通过。

**负控实测**：修之前 `git stash` 掉订正 → 门禁 7 处全部命中、exit 1；
`git stash pop` 回来后转绿。**所以这道门禁不是恒绿。**

接入 `.github/workflows/backend.yml` 的 build-gate（只需 node，
不需要 Go toolchain 与 postgres service），CI 里同跑 `--selftest` 与本体。

---

## 2. 顺带：只读诊断探针第三次把主干判红

`internal/email/diag_empty_snippet_locus_test.go` 触发 PG 隔离守卫规则 2。
与本会话早些时候登记的 `diag_real_fetch_snippet_stages_test.go`
**同一天、同形态**：

- 0 写语句、`.Exec(` 0 次、2 处 `pool.Query`
- `PG_DSN` + `POCKET_REAL_KEYS` 双开关，**均无缺省值**（CI 里恒 skip）
- `search_path` 取自 DSN + `current_schema()` 读回逐字校验
- **必须**指向生产 schema（要读的就是真实库里那批行）

⇒ 「写探针的人不知道要登记」这件事**已经发生两次**。
登记条目里已写明这一点，并要求新建这类探针时连同条目一起提交 ——
否则主干会红，而红的原因（一行 allowlist）与症状（CI 失败）
之间隔着一百多行守卫输出。

**负控实测**：注入 `DELETE FROM emails WHERE id = 'negctl2'` 后重跑，
护栏如期转红并点名「没有登记到 `pgAllowlistedWrites`」。

（第三次出现的 `zz_diag_ledger_totals_test.go` 这次是**已隔离**的，
守卫绿 —— 说明隔离写法在被复制，不是每个新探针都会判红。）

---

## 3. 分支与 worktree 清理

删前对每个分支做**双向确认**：`git rev-list --count origin/main..<branch>`
为 0（无独占提交）**且** 该分支所在 worktree 的
`git status --porcelain --untracked-files=all` 为空
（**未跟踪项也要看** —— round38 就是漏了这一步）。

| 分支 | 未并入 main | worktree 状态 | 处置 |
|---|---|---|---|
| `audit/gofmt-debt-20261003` | 0 | 干净（含 untracked） | **已删**（含 worktree `wt-a32`） |
| `fix/i18n-nested-placeholder-crash` | 0 | 未被 checkout | **已删** |
| `fix/email-upsert-messageid-conflict` | 0 | 未被 checkout | **已删** |
| `docs/round37-section10` | 0 | **`.wt-build` 正在被并发会话使用** | 保留 |
| `audit/2026-10-04-main-health` | 本轮产出 | 我的 worktree | 保留至推送完成 |
| `main`（本地） | 4 个提交全部 `-`（patch 等价） | 主工作区有未提交改动 | 未动，见 §6 |

**没有动的两个，理由都写在这里**：
`.wt-build` 与 `wt-i18n2` 仍被活跃会话使用；
本地 `main` 的 4 个提交经 `git cherry` 判定**全部 patch 等价**于
origin/main（输出全是 `-`），所以它只是**过期**而不是有独占工作，
但它被主工作区 checkout 着且主工作区有未提交改动，
强行 fast-forward 会毁掉那些改动。

---

## 4. 本轮最贵的一课：「改完了」不等于「真实文件改到了」

### 4.1 现象

修那 7 处时反复出现「改完还是 `”`」，且**每次都是在跑过 `gofmt -w` 之后**。
中间一度以为是 `gofmt` 把 `''` 改成了 `”` ——
但最小复现（`gofmt -d` 一个含 `''` 的探针文件）证明 **gofmt 保留 `''`**，
假设被证伪。

### 4.2 真正的机制

逐层定位到的结论：**本机上「前端工具写的」与「原生进程读的」不是同一份文件。**

- `edit` / `write` 工具、以及 `node` 脚本的写，落在**沙箱 overlay** 上；
- **原生子进程**（`gofmt`、`git`）读的是**真实文件系统**；
- 一旦原生进程重写该文件，**overlay 就被丢弃**。

判别实验（三条互相印证）：

| 读数方式 | 看到的版本 |
|---|---|
| `edit` 工具改完 → `Grep` 工具读 | 已修（overlay） |
| 同上 → PowerShell `[System.IO.File]::ReadAllText` 读 | **未修**（真实 FS） |
| 同上 → `git add` + `git grep --cached` 读 | **未修**（真实 FS） |
| 用 PowerShell .NET 改完 → `git grep --cached` 读 | **已修**（真实 FS） |

### 4.3 得出的操作纪律

**任何「我修好了」的结论必须在 git 对象层验证，工作区读数不作数：**

    git add -A && git grep --cached -P '[\x{2018}\x{2019}\x{201C}\x{201D}]'

本轮的三处验证都在这一层做的：staged / HEAD 的内容里只剩 2 处成对正文引号。

**推论（更要紧）**：一道**跑在 node 上的判据，它的绿灯不能单独作为
「真实文件已修好」的证据**。`check-smart-quotes.mjs` 正是 node 写的 ——
所以 CI 里它与 `gofmt` 门禁的组合能互补，但**本地排查时必须再配一条
git 层的独立检查**。这条已写进 commit message 与本文件。

### 4.4 顺带记一条同族坑

用 PowerShell 写 `.ps1` 脚本文件时，**脚本里不能出现非 ASCII 字面量**：
PowerShell 5.1 把无 BOM 的 `.ps1` 按 ANSI 读，中文会变乱码并导致
语法错误（本轮第一次跑就炸在 `m = '传 ’` 上）。
修法：脚本里只用 `[char]0x201D` 这类码位，不写字面量。

---

## 5. 测试

在 `openpocket-wt-audit38`（合并 `c1e4228d` 后）实测：

```
go build ./...                                  → 0
go vet ./...                                    → 0
go test ./... -count=1                          → 全绿，无 FAIL
node scripts/check-gofmt.mjs                    → 真债 0（931 个 .go）
node scripts/check-smart-quotes.mjs             → OK
node scripts/check-smart-quotes.mjs --selftest  → 19/19
git grep --cached -P '[\x{2018}\x{2019}\x{201C}\x{201D}]' -- 'backend/*.go'
  → 仅 2 处成对正文引号（“副作用型” / “fails closed”）
```

### 未验证项（明说）

- **`go test -race` 仍跑不了**（本机无 gcc，`CGO_ENABLED=1` 也一样）。
  CI 的 `go test -race ./... -count=1` 是否绿，**本轮仍未验证**。
- **没有对着真库 / 真 IMAP 跑过任何东西。** 本机 5432 是并发会话在用的
  共享资产。
- **前端门禁与真机 UI 依旧没跑**（新 worktree 无 `node_modules`，
  且不想与并发会话抢设备）。这是连续两轮的空白。
- 新门禁只在 `.go` / `.sql` 上跑过；`.md`（含 handoff 台账）未扫。

---

## 6. 遗留风险

1. **新门禁是逐行判定的。** 跨行的成对引号（`“` 在上一行、`”` 在下一行）
   会被误报成两处落单。本仓当前没有这种写法（实测 0），
   但它是**真实缺口**，不是「已排除」。
2. **`''` 这类损坏可能还有别的形态没被这条门禁覆盖** ——
   比如被写成 `‘‘`（U+2018 两次）、或被写成 `"`（双引号）。
   本轮只处理了实测到的 7 处 + 一种形态。
3. **overlay 与真实 FS 的差异仍在**（§4）。任何后续改动都要按 §4.3
   在 git 层复核，别信工作区读数。
4. **`.wt-build` 与 `wt-i18n2` 仍未清理**，仍被活跃会话使用。
5. **本地 `main` 过期 4 个提交**（全部 patch 等价），且主工作区有
   一批未提交改动（round39 已核对：其中 3 个文件与已提交内容等价，
   `.maestro/messages-hub.yaml` 仍是另一个会话的实验面）。
   清理要等那些会话收工。
6. **`go test -race` 未验证**（见上）。

---

## 7. 下一轮提示词

> 继续 openpocket 审计（round41），基线 `audit/2026-10-04-main-health`
> 合并后的 main。
>
> 1. **先按 round40 §4.3 建好验证姿势**：
>    `git add -A && git grep --cached -P '[\x{2018}\x{2019}\x{201C}\x{201D}]' -- 'backend/*.go'`
>    工作区读数不作数（前端工具写 overlay，原生进程读真实 FS）。
>    **不要用 node 写的判据的绿灯当作「真实文件已修好」的证据。**
> 2. **优先做遗留风险 2**：查 `''` 的其它损坏形态 ——
>    `‘‘`（U+2018 两次）、`""`、以及中文引号 `''`（U+2018/U+2019）。
>    先在真实仓库里 `git grep -P '\x{2018}\x{2019}'` 取全量读数，
>    再决定要不要扩门禁。**别凭推测扩**。
> 3. **遗留风险 1**：把新门禁改成「跨行也配平」看看误报率。
>    先在当前树上跑 `--list`，若 0 命中再考虑收紧；
>    若有命中，说明仓库里真有跨行成对引号，那是**另一个缺陷**而不是误报。
> 4. `go test -race` 连续两轮没验成。查 `CGO_ENABLED=1` + 有没有
>    可用的 gcc/mingw；没有就在 handoff 里固定写「race 未验证」，
>    别每轮重新试一遍。
> 5. 前端门禁与真机 UI 连续两轮空白。能起独立 worktree 的 `npm ci`
>    就先补 `frontend/` 的 lint + 单测。
> 6. 只读诊断探针已两次把主干判红。**新建这类探针时，
>    连同 `pgSafeWithoutIsolation` 的条目一起提交**（条目模板见
>    `diag_empty_snippet_locus_test.go` 那条）。
