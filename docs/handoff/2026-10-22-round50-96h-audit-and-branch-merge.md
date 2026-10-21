# round50 —— 96 小时窗口审计与分支并合：上一轮合并**静默丢了两道门**，main 一直是红的

日期：2026-10-22
基线：`0fdda422`（`Merge branch 'feat/app-icon-standard' into main` —— **本轮从这个红点出发**）
承接：`docs/handoff/2026-10-20-round49-round48-debts-closed-and-which-first-dead-branch.md`
本轮主线：主文档 `docs/design/2026-10-06-recording-quality-fixes.md` **§960**

---

## 一句话结论

本轮不是「新功能轮」，是**审计轮**，挖到 **10 件真事**，其中 **3 件是上一轮的合并亲手带进 main 的**：

| # | 缺陷 | 谁带进来的 | 读数 |
|---|---|---|---|
| **D1** | `frontend/gates.json` 的冲突被按「一侧整块」处置 ⇒ **两道门连同 script 一起掉了** | 上一轮合并 `0fdda422` | `run-gates.mjs --ci` ⇒ **EXIT 2** |
| **D2** | `internal/notes` 的 skip 台账过期 | §940（`99442d20`） | `go test ./internal/notes` ⇒ **FAIL** |
| **D3** | `internal/server` 的 skip 台账漏登记一条跳过点 | §907 加的守卫，§936 只登记进 census | `go test ./internal/server` ⇒ **FAIL** |
| **D4** | 主文档 **9 个严格小节指针**同时指向两段互不相干的内容 | 上一轮合并 `0fdda422` | `check:doc-ref-integrity` ⇒ **绿**（它只判死引用） |
| **D5** | 三处**只在本机红**的门（路径分隔符 / CRLF） | Windows 检出形态 | 3 个包 ⇒ **FAIL**（CI/Linux 绿） |
| **D6** | `check:local-todo-dedupe` 的抽取器**假设行尾是 LF** | 仓内既有（基线就有） | 自检 **5 条红** / 体检 EXIT 1 |
| **D7** | `check:dead-features` 的 `rel()` **假设分隔符是 `/`** | 仓内既有（基线就有） | 11 条「新增未接线」⇒ EXIT 1 |
| **D8** | `check:ci-trigger` 的 workflow 解析器**假设行尾是 LF** | 仓内既有（基线就有） | 13 条「新增」⇒ EXIT 1 |
| **D9** | ESM `import()` 加载 Windows 绝对路径 ⇒ **一条证据臂从未跑过** | 仓内既有（基线就有） | `check:router-parity` EXIT 2；`check:marketplace-fix` **绿但降级** |
| **D10** | `check:doc-encoding` 的豁免 Map key **是反斜杠** ⇒ 登记一条都查不到 | 仓内既有（基线就有） | 23 处问题 ⇒ EXIT 1 |

★ **最要紧的一条**：D1 ⇒ **main 从 `0fdda422` 起门禁 runner 就退出码 2**，
而 round49 交接单里「全部 36 项通过」的读数取自**合并之前**的那个 HEAD
⇒ ⇒⇒ ⇒ **那条读数不能用来证明合并后的 main 是绿的**。

★ **第二条最要紧的**：D1 修好之后，`run-gates.mjs --ci` 才**第一次真正跑起来** ——
此前它一路在**规则-4 早退** ⇒ **`ciRuns` 的 39 条门禁一条都没跑过**
⇒ ⇒⇒ ⇒ 于是 D6–D10 才**依次**冒出来（它们原本就红着，只是从没有机会被看见）。
⇒ ⇒⇒ ⇒ ⇒ **推论（下一轮必须记住）**：「基线也是红的」这句话**只对 runner 自检成立**；
⇒ ⇒⇒ ⇒ ⇒ 对第 1 步之后的每一道门，**基线读数是不存在的** ⇒ 必须**单独跑那一道门**去取。

---

## 1. 根因（每条一句话）

1. **D1**：合并冲突**被按「main 侧整块」判掉**，分支侧的两条门没有并进来；
   而 `package.json` 里的 script 留下了 ⇒ `run-gates.mjs` 规则 4 必然触发。
   ⇒ **合并时对「名单型文件」必须逐条核对两侧条目，不能整块取一侧。**
2. **D2**：§940 把跳过点收进 helper，而 `skipaudit` 的 census 与**本包台账**
   **只数「测试函数体内」的 `t.Skip(`** ⇒ 读数 0 ⇒ 台账 6 条判「过期」+
   量具自证失败 ⇒ **§940 把一条绿门换成了两条红门**。
3. **D3**：§936 把 §907 的跳过点登记进了 **census 基线**，却漏登记进
   **server 包自己的台账** ⇒ 同一件事登记在两张表里，只登记了一张。
4. **D4**：`check:doc-ref-integrity` 判的是**死引用**（`§NNN` 没有实体）
   ⇒ 重号**能解析** ⇒ 门全程绿。⇒ **「引用能解析」≠「引用是唯一的」**。
5. **D5**：豁免清单是**入库文件**（正斜杠），而 key 用 `filepath.Separator` 拼
   ⇒ Windows 上是反斜杠 ⇒ **每一条豁免都匹配不上**；跨语言 fixture 在
   前端目录 ⇒ `.gitattributes` 只声明 `*.go` ⇒ Windows 检出即 CRLF。
6. **D6–D10 共享同一个形状**：**门禁的结论依赖了不该依赖的「检出形态」**
   —— 行尾（CRLF）、路径分隔符（`/` vs `\`）、路径形态（绝对路径 vs ESM URL）。
   ⇒ ⇒⇒ ⇒ 这些是**检出的属性**，不是**代码的属性**
   ⇒ ⇒⇒ ⇒ 一道门若对它敏感，它在 Linux 上的绿就是**运气**，不是结论。
   ⇒ ⇒⇒ ⇒ 五处的「被依赖属性 → 依赖它的判据 → 修法」对照表见主文档 §960.7.8。

---

## 1b. D6–D10 逐条（一句话根因）

| # | 被依赖的环境属性 | 依赖它的判据 | 修法 | 修前 → 修后 |
|---|---|---|---|---|
| D6 | 行尾 CRLF | 循环头正则 `\{[ \t]*$`（尾部是 `{\r`）+ 「重拼」反推 `{` 绝对位置时少算每个 `\r` | 读入口归一 | 自检 5 红 → **16/16 绿** |
| D7 | 分隔符 `\` | `rel()` = `f.replace(`${ROOT}/`, '')` ⇒ 模板成了**混合分隔符** ⇒ 永不匹配 | 先归一再剥前缀 | 11 条假「新增」→ **棘轮通过** |
| D8 | 行尾 CRLF | `/^(\s+)run:\s*(.*)$` —— JS 的 `.` **不匹配 `\r`**、`$` 只匹配输入末尾 ⇒ 61 条 `run:` 行只认出 6 条 | 读入口归一 | 13 条假「新增」→ **基线 0 条** |
| D9 | 路径形态 | `await import(<Windows 绝对路径>)` ⇒ Node 当成协议 `c:` 的 URL | `pathToFileURL(x).href` | 退出码 2 / 证据臂死 → **全绿、臂复活** |
| D10 | 分隔符 `\` | `relToFile` 的 key（`path.relative()`）vs 登记表里的 `file`（正斜杠） | `relOf()` 归一（**产生处与消费处同一个函数**） | 23 处 → **0 处** |

⇒ ⇒⇒ ⇒ **五处全部**：**没有改任何一份登记表 / 豁免 / 台账** ——
⇒ ⇒⇒ ⇒ 错的从来不是登记，是**键的归一**。
⇒ ⇒⇒ ⇒ ⇒ 这也是本轮最省事的地方：**基线一直是对的**。

⇒ ⇒⇒ ⇒ ★ **本轮的门禁逮住了本轮自己的回归**：第 20 项 `check:gofmt` 报出
⇒ ⇒⇒ ⇒ `backend/internal/testaudit/skip_census_test.go` 真债（修 D2 时留了个空行）
⇒ ⇒⇒ ⇒ ⇒ **自己刚写进去的债，被自己修好的门禁逮住** —— 这是「把门禁真跑起来」本身的价值。

---

## 2. 改动文件与关键行为

### 2.1 本轮**新写**

| 文件 | 作用 |
|---|---|
| `scripts/check-doc-section-number-uniqueness.mjs` | **新门**：主文档节号/小节号不许重号。规则 A（`## §NNN` 非 D 形态）+ 规则 B（`### §NNN.M`，**字母后缀续写约定放行**）；豁免清单双向严格相等 + **只许变少**；量具下限哨兵（标题数 < 300 ⇒ exit 2「量具坏了」而不是 1） |
| `scripts/doc-section-number-waivers.json` | 存量 3 条重号的豁免（`§426` / `§497.2` / `§497.3`），每条带 why |

### 2.2 本轮**改**

| 文件 | 改动 | 关键行为 |
|---|---|---|
| `frontend/gates.json` | **恢复**被上一轮合并吞掉的两道门 + 接入新门 | `gates` 47→**50**、`ciRuns` 36→**39**；**两侧增量全保** |
| `frontend/package.json` | 新增 `check:doc-section-number-uniqueness` | 规则 4 的「孤儿门禁」不许再出现 |
| `backend/internal/notes/store_test.go` | 抽 `testPGDSN()`；6 个 PG 用例**体内**补同义跳过 | helper 里那条保留为运行时保证（§940 的成果不动） |
| `backend/internal/testaudit/skip_census_test.go` | 恢复 `./internal/notes: 6` 基线 + 记 §960 的取舍 | 棘轮回到 §936 时的 194/194 |
| `backend/internal/server/skip_ledger_test.go` | 补登 `TestRefineRejectedCarriesItsReason`；`CatPremiseFailed` 计数 12→**13** | 「漏登记」消失 |
| `backend/internal/repohygiene/truncate_byteslice_test.go` | `relToRepo` 统一 `filepath.ToSlash` | Windows 上豁免重新匹配得上 |
| `backend/internal/stt/full_wire_contract_test.go` | 比对 fixture 前归一 CRLF | 同一份内容在 Windows/Linux 结论一致 |
| `backend/internal/server/meeting_response_contract_test.go` | 同上（2 处） | 同上 |
| `scripts/check-local-todo-dedupe.mjs` | 新增 `normalizeEol()`；读入口归一 CRLF；导出的 `reminderFollowsDedupe` 自己也归一 | D6：自检 5 红 → **16/16 绿**、体检 EXIT 0 |
| `frontend/scripts/audit-dead-features.mjs` | `rel()` 改成「先归一分隔符再剥前缀」 | D7：11 条假「新增」→ **棘轮通过**（基线一个字没改） |
| `frontend/scripts/check-ci-trigger-surface.mjs` | 新增 `readTextNormalized()`；两处读 workflow 归一 CRLF | D8：13 条假「新增」→ **基线 0 条**（driver 被正确认出） |
| `scripts/check-router-runtime-parity.mjs` | 2 处 `import()` 改 `pathToFileURL(...).href` | D9a：退出码 2 → **EXIT 0**、自检 4/4 |
| `scripts/verify-marketplace-fix.mjs` | 同上 2 处 | D9b：「运行期独立证词」由「未验成 ⬜」→ **「通过」**（一条死掉的证据臂复活） |
| `scripts/audit-doc-encoding.mjs` | 新增 `relOf()`；Map key 与查询键**用同一个函数** | D10：23 处 → **0 处**（11 条登记全部命中） |
| `docs/design/2026-10-06-recording-quality-fixes.md` | **字节级门侧 §895/§896 → §958/§959**；追加 **§960**（含 §960.7 的五处平台假红 + 同族总表） | 9 个歧义指针消失；本轮审计留档 |
| `docs/design/2026-10-11-pending-decisions-brief.md` | 待拍板项 **118/119 → 182/183**；追加 **第 184 / 185 条** | 简报条目不再重号 |
| `frontend/src/utils/__tests__/base64-null-result.test.mjs` | 注释里的 §895/§896 → §958/§959 | 代码注释与主文档同号 |
| `handoff/2026-10-17-48h-audit-and-fixes.md` | 同上 + 「简报 119」→「简报 183」 | 同上 |
| `docs/handoff/2026-10-20-round49-...md` | 「简报 119」→「简报 183」 | 同上 |

### 2.3 合并进来的（**不是本轮写的，是分支带进 main 的**）

`origin/feat/app-icon-standard` 的 **38 个提交（§948–§957）**，180 个文件：
promise 形态的「拒绝被就地消化」不许无声（92 站台账 + 5 处真缺陷修复）、
后台 goroutine 的 panic 不许打死整个进程（77 处裸 `go` 收进 `background` 包）、
出站 HTTP 必须有上界（33 处裸 `http.Client` 收进 `outbound` 包 + 4 处真缺陷）、
读一个不知道多大的东西必须自带上界（`sizedread` 包）、
webm 解码从「整场/累计」换成「逐簇」、原生链的「整场重转」从盘上分片流式供给、
会话时间轴（`native-part-timeline.ts`）、切设备后的「切换空隙」与「真丢音频」必须分得开。

---

## 3. 测试命令与结果

### 3.1 本轮新增/改动门禁

| 命令 | 结果 |
|---|---|
| **`node frontend/scripts/run-gates.mjs --ci`** | **EXIT 0 —— 全部 39 项通过（99.9s）** ⇒ ★ **这是本仓自 `0fdda422` 起第一次全量 CI 门禁跑绿**（此前一路在规则-4 早退，一条门都没真跑过） |
| `node scripts/check-doc-section-number-uniqueness.mjs --selftest` | **EXIT 0** —— 量具自证 **8/8** |
| `node scripts/check-doc-section-number-uniqueness.mjs` | **EXIT 0** —— 实测重号 3 条 / 豁免 3 条 / **新增 0 / 过期 0** |
| **负控**：注入 `## §957` 重号 | **EXIT 1** ⇒ 点名 `section:957` ✅ 转红 |
| **负控**：再注入 `### §957.1` 重号 | **EXIT 1** ⇒ 点名 `sub:957.1` ✅ 转红 |
| 还原核对（读**被改的那个路径**） | md5 `5efc45ee…` 前后一致 ✅ 逐字节相同；还原后门禁 **EXIT 0** |
| `node scripts/check-doc-ref-integrity.mjs` | **EXIT 0** —— 9 条已知缺口全部登记在案，**无新增、无过期** |
| `node frontend/scripts/run-gates.mjs --list` | **EXIT 0** —— CI 39 条 / workflow 11 条；规则 2/4/5 预检全过 |

### 3.1b 五处平台假红的**对照读数**（每处都带负控）

| 门 | 修复前（Windows） | 修复后 | 负控 |
|---|---|---|---|
| `check:local-todo-dedupe` | 自检 **5 条红** / 体检 EXIT 1 | 自检 **16/16 绿** / 体检 **EXIT 0** | `lf`/`crlf` 两棵树：干净都绿；**注入第 4 处裸 INSERT 都红且点名** |
| `check:dead-features` | 11 条假「新增」⇒ EXIT 1 | **棘轮通过 / EXIT 0**（基线一个字没改） | A 干净树绿 / B 注入新死导出**红且点名** / C 删掉基线登记的一条⇒绿且点出「本次未检出」 |
| `check:ci-trigger` | 13 条假「新增」⇒ EXIT 1 | **基线 0 条 / EXIT 0**（driver 被正确认出） | A 干净 CRLF 绿 / B 干净 LF 绿且**输出逐字相同** / C 删 driver⇒**红且点名 driver** / D 删 typecheck 兑现路径⇒**红且点名豁免** |
| `check:router-parity` / `check:marketplace-fix` | 前者 **EXIT 2**；后者**绿但降级**（「运行期独立证词」= 未验成 ⬜） | 前者自检 **4/4**、文本 **85** / 运行期 **85**；后者该臂**「通过」**、结论不变 | 两道门**自带**变异（前者 4 条、后者含「可构造变异体」臂） |
| `check:doc-encoding` | **23 处** ⇒ EXIT 1 | **0 处**（11 条登记全部命中，另有 23 个 U+FFFD 属豁免）⇒ EXIT 0 | 登记**一条没改**即可全部命中 ⇒ 证明错的只是 key 归一 |

⇒ ⇒⇒ ⇒ ★ **每一处都验证了「Linux 口径下输出逐字不变 / 恒等变换」** ⇒ CI 读数不受影响。
⇒ ⇒⇒ ⇒ ★ **五处全部没有改任何一份登记表 / 豁免 / 台账**。

### 3.2 Go 侧（**基线对照**：同一负载在 `0fdda422` 上先跑一遍）

| 包 | 合并前（`0fdda422` 基线） | 本轮修复后 |
|---|---|---|
| `internal/notes` | **FAIL**（量具失效 + 台账过期） | **PASS** |
| `internal/server` | **FAIL**（漏登记 + 契约门 CRLF） | **PASS** |
| `internal/repohygiene` | **FAIL**（豁免 key 路径分隔符） | **PASS** |
| `internal/stt` | **FAIL**（fixture CRLF） | **PASS** |
| `internal/testaudit` | — | **PASS**（census 棘轮回到 194/194） |
| `go build ./...` | — | **EXIT 0** |
| **`go test ./...`（全量）** | — | **EXIT 0 —— 全部 ok，无 FAIL** |

⚠ **本轮门禁逮住了本轮自己的回归**（值得单列）：
第 20 项 `check:gofmt` 报出 `backend/internal/testaudit/skip_census_test.go` 真债
—— 修 D2 时改 census 基线 `./internal/notes: 6`，**在 map 字面量末尾留了一个空行**。
⇒ 修法：删掉那个空行 ⇒ `gofmt -l` 为空、`go test ./internal/testaudit/...` 仍 ok。
⇒ ★ **在 D1 修好之前，这个回归会被第 20 项之前那道门挡住，一路带到 main。**

### 3.3 前端（**基线对照**：同一负载在 `0fdda422` 上先跑一遍）

| 命令 | 基线 `0fdda422` | 本轮（合并后） | 结论 |
|---|---|---|---|
| `npm run test:all` | **30 个测试文件红**（含 `relative-time` / `safe-url` / `base64-null-result` …） | **同样 30 个文件红，逐条相同** | ⇒ **合并零新增失败**；这 30 个是 **Windows 专属**的既有假红（`new URL(...).pathname` 在 Windows 上得到 `/C:/...` ⇒ 拼出 `C:\C:\...`） |

---

## 4. 分支并合（96 小时窗口：2026-10-18 02:09 → 2026-10-22 02:09，且「1 小时前」不活跃）

| 分支 | 窗口内 | 判定 | 处置 |
|---|---|---|---|
| `feat/app-icon-standard` | ✅ 末次 2026-10-21 23:32，**审计期间又推进到 2026-10-22 02:57** | **有用**：38 提交 / 180 文件 | **已并入 main** ⇒ 后来追加的 2 个提交（变异 harness 加固）**另见 §4.1** ⇒ 也已并入（重编号 §961）⇒ 删 |
| `fix/maestro-login-assertions` | ❌ 2026-10-12 | **已并入**：`APP_NOT_RUNNING` / `cdpCall` 都在 main 的 `scripts/maestro-run.mjs` 里 ⇒ ⇒⇒ ⇒ **逐文件核对确认连 `optional: true` 那 6 条流的修复也都在** | 删 |
| `backup/pre-rebase-49e6a802` | ❌ 2026-10-17 | **已并入**：16 提交的内容全在 main（21 个冲突块 **18 块归一后逐字相同**，另 3 块是 main 的更正版；代码侧 `best-effort.ts` 逐字节相同）⇒ 合并后 `git diff HEAD --stat` **为空** | 删 |
| 其余 9 条 | ❌ | 早已合入（ahead=0） | 不动 |

### 4.1 ★ 分支在**审计期间**又推进了 ⇒ 合并之后才冒出 2 个提交（差点下错结论）

⚠ **这一节的方法比结论更值得留**（主文档 §960.9 有完整版）。

**事实**：`feat/app-icon-standard` 在我合并之后**又推进了 2 个提交**
（`b616c4cb` 2026-10-22 00:52、`a8d0249a` 2026-10-22 02:57）
⇒ ⇒⇒ ⇒ 「合并后 ahead 仍 > 0」**不等于「我漏合了」** ⇒ ⇒⇒ ⇒ **先查时间线，再判合没合**。

**判据（三重法，从粗到细，每级都有对照）**：

| 级别 | 判据 | 读数 | 可信度 |
|---|---|---|---|
| ① | `git rev-list --count origin/main..origin/<branch>` | 40 / 6 / 16 | **只看哈希** ⇒ 会把「内容已并入但提交不同」误判成未合 |
| ② | `git cherry`（**patch-id**） | feat 0/2、maestro 5/1、backup 0/0 | **patch-id 仍会因上下文漂移而误判** |
| ③ | **逐文件**反向 apply | 精确到文件 | ★ 最可信，但**失败仍不等于「内容缺失」** |

★ **本例的实测**：`fix/maestro-login-assertions` 的 `f20c4e74` 被 ② 判成「未吸收」，
③ 逐文件拆开后：**5 个 `.maestro/*.yaml` + `scripts/maestro-run.mjs` 全部已吸收**，
只剩 `settings-llm-gateway.yaml` 反向 apply 失败 ⇒ **打开那个文件逐行读**
⇒ **修复已在**（74–79 行已是 `optional: true`）⇒ 失败只因该文件上方后来多了内容
⇒ **hunk 上下文漂移** ⇒ ⇒⇒ ⇒ **结论：已完全吸收，可删**。

⇒ ⇒⇒ ⇒ ★ **这是「用哈希/指纹判断有没有被合入」这条教训的第三次现身**
⇒ ⇒⇒ ⇒ ⇒ **真判据是「逐文件打开目标路径，比内容」**。

**处置（两侧增量全保）**：

| | 分支侧 | main 侧（已占用） | 处置 |
|---|---|---|---|
| 主文档 | `## §958` + `§958.0–§958.9` | `## §958` / `## §959` = 字节级门 | 分支侧 ⇒ **§961.0–§961.9**（块内 14 处一并改） |
| 简报 | 第 **182** 条 + `\| §958 \|` 索引行 | 第 **182 / 183** 条 = 字节级门两条 | 分支侧 ⇒ **第 186 条** + `\| §961 \|` 索引行 |
| 代码 | `_harness.py` / `run_all.py` / `README.md` | — | **原样合，一个字没改** |

⚠ **顺带修掉 round50 自己漏改的两处**（这次冲突把它顶出来了）：
① 简报**索引表**缺 §958 / §959 / §960 三行 ⇒ 补上（连同 §961 共 4 行）；
② 简报**计数行**仍写「累计 **181** 条 / 最新五条 181…」⇒ 已过期（实际已到 185）
⇒ 改到 **186**，最新五条重排为 186 / 185 / 184 / 183 / 182。
⇒ ⇒⇒ ⇒ ⇒ 若不是这次冲突，这两处漏改会一直留在 main 上。

⚠ **本轮踩到的两个测量坑（都记在 §960.5 / §960.6）**：
1. 用「指纹在不在 main」判断「有没有被合入」⇒ **错**（指纹带节号，而 main 重编号过）
   ⇒ **真判据是「节号归一后逐字比对」**；
2. 扩展 `skipaudit.ScanDir` 让它归因 helper 里的跳过点 ⇒ **实测 8/11 个台账包被打爆**
   （server 实际点数被放大到 406 vs 台账 39）⇒ **撤回**，改走窄路径。

---

## 5. 遗留风险（如实登记）

1. ⛔ **Windows 上 30 个前端测试文件红**（既有，非本轮引入）：根因是
   `new URL('...', import.meta.url).pathname` 在 Windows 上得到 `/C:/...`
   ⇒ 修它要改 30 个文件 ⇒ 属**另一件独立的工作** ⇒ **本轮只登记**。
   ⇒ CI（Linux）不受影响 ⇒ **不影响线上**，只影响「Windows 上能不能本地自证」。
   ⇒ ⚠ **但本轮已把 5 处「门禁侧」的平台缺陷修掉 ⇒ 两边现在不对称了**：
   ⇒ ⇒⇒ ⇒ **门禁**在 Windows 上能真跑、能真自证；**测试**侧那 30 个文件仍红。
   ⇒ ⇒⇒ ⇒ ⇒ 修掉测试侧才能在本机得到**完整**自证能力（见简报 184 ① / 185 ②）。
2. ⛔ **「门禁对检出形态免疫」的静态扫描没做完**（简报 185 ①）：
   本轮修了**已爆的 5 处**（D6–D10）⇒ ⇒⇒ ⇒ 仓里**可能还有尚未被跑到的同型门**
   ⇒ 本轮做过一次粗扫（`split('\n')` 100+ 处、动态 `import()` 十几处），
   ⇒ ⇒⇒ ⇒ **但没有逐个判它们是否敏感** ⇒ ⇒⇒ ⇒ 建议下一轮做成一门。
3. ⛔ **CI（Linux）上的真实读数仍未验证**：本轮全部读数都是**本机（Windows）**的。
   ⇒ 每处改动都验证过「Linux 口径输出逐字不变 / 恒等变换」，
   ⇒ ⇒⇒ ⇒ 但**没有在 Linux 上实跑过** ⇒ ⇒⇒ ⇒ 下一轮必须读一次 CI 的实际读数。
4. ⛔ **census / skipaudit 的扫描口径没扩到 helper 层**（§940.7 登记的那件）：
   本轮实测该扩展会放大 8 个包的点数 ⇒ 必须连同其它包的台账一起处理
   ⇒ **不代拍**（见简报 184 ②）。
5. ⛔ **存量 3 条重号**（`§426` / `§497.2` / `§497.3`）未改号：各有入向引用
   ⇒ 登记在豁免清单里，**只能变少**（见简报 184 ③）。
6. ⛔ **round49 交接单的「go test -race ./... ⇒ EXIT 0 / FAIL 0」那条读数不可信**：
   它取自 `51814ed7`（§940 之前），而 §940 之后 main 的 notes/server 两包是红的。
   ⇒ 本轮交接单里的读数**附了基线对照**，可以照此要求下一轮。
7. ⚠ **race 仍然是「一次快照」**（沿用 round17 §5.1 的口径）：本轮**没有**跑 `-race`
   ⇒ 新增并发代码（`background` 包）需在下一轮补 `-race` 读数。
8. ⚠ 本轮**没有**动 CI workflow：新门走 `gates.json` 的 `ciRuns` 数据驱动，CI 侧无需改
   ⇒ 但**新门在 CI 上必须真跑** ⇒ 下一轮要读一次 CI 的实际读数。
9. ⚠ **本机残留一个基线 worktree**：`C:/workspace/_po-base-main`（detached 于 `0fdda422`，
   `node_modules` / `dist` 用 junction 软链到主工作区）—— 本轮用它取各处基线读数。
   ⇒ **故意没有删**：该目录里有指向主工作区的 junction ⇒ 递归删除有误伤
   ⇒ `node_modules` / `dist` 的风险 ⇒ ⇒⇒ ⇒ 留给下一轮或属主手工处置。

---

## 6. 下一轮提示词（可直接粘贴）

> 继续 pocket-opencode 的 96 小时滚动审计轮。
>
> 起点：`docs/handoff/2026-10-22-round50-96h-audit-and-branch-merge.md`；
> 主线文档 `docs/design/2026-10-06-recording-quality-fixes.md` **§960（尤其 §960.7）**；
> 待拍板项见简报 **第 184 / 185 条**（**不代拍**）。
>
> 本轮已销账：① 上一轮合并静默丢的两道门（`check:doc-ref-integrity` /
> `check:fetch-bounds`）已恢复，`gates` 47→50、`ciRuns` 36→39；
> ② `internal/notes` 与 `internal/server` 两个包的 skip 台账已修好（Go 侧 CI 不会再红）；
> ③ 主文档 9 处歧义小节指针已修（字节级门侧 §895/§896 → §958/§959），
> 并新建 `check:doc-section-number-uniqueness`（负控验牙过）；
> ④ 三条分支已并入/删除；
> ⑤ ★ **五处「门禁把检出形态当成代码属性」的平台假红已修**（D6–D10，
> 详见主文档 §960.7.8 的同族总表）⇒ **`run-gates.mjs --ci` 首次 39 项全绿**。
>
> 本轮请做（按优先级）：
> 1. **读一次 CI 的实际读数**（`backend.yml` / `frontend.yml`）⇒ 确认 39 条在 Linux 上
>    **真跑且绿**。⚠ 本轮全部读数都是**本机（Windows）**的；每处改动都验证过
>    「Linux 口径输出逐字不变 / 恒等变换」，但**没有在 Linux 上实跑过**。
> 2. **把「门禁对检出形态免疫」做成一道门**（简报 185 ①）：
>    静态扫「读文本文件后 `split('\n')` 且判据用 `(...)$` 锚」与「`import()` 传绝对路径」
>    ⇒ 判定哪几处**真的**敏感。⚠ 本轮只修了**已爆的 5 处** ⇒ **这个扫描没做完**。
> 3. **Windows 上那 30 个前端测试文件红**要不要现在修（简报 184 ① / 185 ②）：
>    ⚠ 门禁侧已修好 ⇒ **两边现在不对称**了 ⇒ 修掉测试侧才能在本机得到完整自证能力。
>    若做，**逐文件**改并逐文件验证，**禁止一次性批量替换**。
> 4. **补 `-race` 快照**：`cd backend && go test -race -count=1 ./...`
>    （§949 新增了 `internal/background` 包 ⇒ 上一次 race 读数在它之前）
>    ⇒ 并用 NEGCTRL 探针证明 detector 武装。
> 5. **§948–§957 那 38 个提交的审计回填**：本轮只验了「它们不引入回归」
>    （基线对照），**没有逐条审它们的判据本身** ⇒ 下一轮按 §958 的读法
>    （行为门 + 接线扫描 + 变异）逐节核。
> 6. **合并纪律固化**（本轮最贵的教训）：合并**名单型文件**（`gates.json`、
>    `package.json`、豁免清单、台账）时，必须**逐条核对两侧条目**，
>    并在合并后跑一次 `git diff HEAD --stat` 看是否「零内容变化」——
>    本轮的两个分支分别是「带进 180 文件」与「带进 0 行」，**两种都要说清**。
> 7. ★ **读数纪律（本轮新增，最容易再犯）**：`run-gates.mjs`「失败即停」+
>    规则-4 早退 ⇒ **一道门红在前面 ⇒ 后面 38 条门的问题全被遮住**
>    ⇒ 「基线也是红的」这句话**只对 runner 自检成立**；
>    ⇒ 对第 1 步之后的每一道门，**必须单独跑那一道门**去取基线。
