# round50 —— 96 小时窗口审计与分支并合：上一轮合并**静默丢了两道门**，main 一直是红的

日期：2026-10-22
基线：`0fdda422`（`Merge branch 'feat/app-icon-standard' into main` —— **本轮从这个红点出发**）
承接：`docs/handoff/2026-10-20-round49-round48-debts-closed-and-which-first-dead-branch.md`
本轮主线：主文档 `docs/design/2026-10-06-recording-quality-fixes.md` **§960**

---

## 一句话结论

本轮不是「新功能轮」，是**审计轮**，挖到 **5 件真事**，其中 **3 件是上一轮的合并亲手带进 main 的**：

| # | 缺陷 | 谁带进来的 | 读数 |
|---|---|---|---|
| **D1** | `frontend/gates.json` 的冲突被按「一侧整块」处置 ⇒ **两道门连同 script 一起掉了** | 上一轮合并 `0fdda422` | `run-gates.mjs --ci` ⇒ **EXIT 2** |
| **D2** | `internal/notes` 的 skip 台账过期 | §940（`99442d20`） | `go test ./internal/notes` ⇒ **FAIL** |
| **D3** | `internal/server` 的 skip 台账漏登记一条跳过点 | §907 加的守卫，§936 只登记进 census | `go test ./internal/server` ⇒ **FAIL** |
| **D4** | 主文档 **9 个严格小节指针**同时指向两段互不相干的内容 | 上一轮合并 `0fdda422` | `check:doc-ref-integrity` ⇒ **绿**（它只判死引用） |
| **D5** | 三处**只在本机红**的门（路径分隔符 / CRLF） | Windows 检出形态 | 3 个包 ⇒ **FAIL**（CI/Linux 绿） |

★ **最要紧的一条**：D1 ⇒ **main 从 `0fdda422` 起门禁 runner 就退出码 2**，
而 round49 交接单里「全部 36 项通过」的读数取自**合并之前**的那个 HEAD
⇒ ⇒⇒ ⇒ **那条读数不能用来证明合并后的 main 是绿的**。

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
| `docs/design/2026-10-06-recording-quality-fixes.md` | **字节级门侧 §895/§896 → §958/§959**；追加 **§960** | 9 个歧义指针消失；本轮审计留档 |
| `docs/design/2026-10-11-pending-decisions-brief.md` | 待拍板项 **118/119 → 182/183**；追加 **第 184 条** | 简报条目不再重号 |
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
| `node scripts/check-doc-section-number-uniqueness.mjs --selftest` | **EXIT 0** —— 量具自证 **8/8** |
| `node scripts/check-doc-section-number-uniqueness.mjs` | **EXIT 0** —— 实测重号 3 条 / 豁免 3 条 / **新增 0 / 过期 0** |
| **负控**：注入 `## §957` 重号 | **EXIT 1** ⇒ 点名 `section:957` ✅ 转红 |
| **负控**：再注入 `### §957.1` 重号 | **EXIT 1** ⇒ 点名 `sub:957.1` ✅ 转红 |
| 还原核对（读**被改的那个路径**） | md5 `5efc45ee…` 前后一致 ✅ 逐字节相同；还原后门禁 **EXIT 0** |
| `node scripts/check-doc-ref-integrity.mjs` | **EXIT 0** —— 9 条已知缺口全部登记在案，**无新增、无过期** |
| `node frontend/scripts/run-gates.mjs --list` | **EXIT 0** —— CI 39 条 / workflow 11 条；规则 2/4/5 预检全过 |

### 3.2 Go 侧（**基线对照**：同一负载在 `0fdda422` 上先跑一遍）

| 包 | 合并前（`0fdda422` 基线） | 本轮修复后 |
|---|---|---|
| `internal/notes` | **FAIL**（量具失效 + 台账过期） | **PASS** |
| `internal/server` | **FAIL**（漏登记 + 契约门 CRLF） | **PASS** |
| `internal/repohygiene` | **FAIL**（豁免 key 路径分隔符） | **PASS** |
| `internal/stt` | **FAIL**（fixture CRLF） | **PASS** |
| `internal/testaudit` | — | **PASS**（census 棘轮回到 194/194） |
| `go build ./...` | — | **EXIT 0** |

### 3.3 前端（**基线对照**：同一负载在 `0fdda422` 上先跑一遍）

| 命令 | 基线 `0fdda422` | 本轮（合并后） | 结论 |
|---|---|---|---|
| `npm run test:all` | **30 个测试文件红**（含 `relative-time` / `safe-url` / `base64-null-result` …） | **同样 30 个文件红，逐条相同** | ⇒ **合并零新增失败**；这 30 个是 **Windows 专属**的既有假红（`new URL(...).pathname` 在 Windows 上得到 `/C:/...` ⇒ 拼出 `C:\C:\...`） |

---

## 4. 分支并合（96 小时窗口：2026-10-18 02:09 → 2026-10-22 02:09，且「1 小时前」不活跃）

| 分支 | 窗口内 | 判定 | 处置 |
|---|---|---|---|
| `feat/app-icon-standard` | ✅ 末次 2026-10-21 23:32 | **有用**：38 提交 / 180 文件 | **已并入 main** ⇒ 删 |
| `fix/maestro-login-assertions` | ❌ 2026-10-12 | **已并入**（`APP_NOT_RUNNING` / `cdpCall` 都在 main 的 `scripts/maestro-run.mjs`） | 删 |
| `backup/pre-rebase-49e6a802` | ❌ 2026-10-17 | **已并入**：16 提交的内容全在 main（21 个冲突块 **18 块归一后逐字相同**，另 3 块是 main 的更正版；代码侧 `best-effort.ts` 逐字节相同）⇒ 合并后 `git diff HEAD --stat` **为空** | 删 |
| 其余 9 条 | ❌ | 早已合入（ahead=0） | 不动 |

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
2. ⛔ **census / skipaudit 的扫描口径没扩到 helper 层**（§940.7 登记的那件）：
   本轮实测该扩展会放大 8 个包的点数 ⇒ 必须连同其它包的台账一起处理
   ⇒ **不代拍**（见简报 184 ②）。
3. ⛔ **存量 3 条重号**（`§426` / `§497.2` / `§497.3`）未改号：各有入向引用
   ⇒ 登记在豁免清单里，**只能变少**（见简报 184 ③）。
4. ⛔ **round49 交接单的「go test -race ./... ⇒ EXIT 0 / FAIL 0」那条读数不可信**：
   它取自 `51814ed7`（§940 之前），而 §940 之后 main 的 notes/server 两包是红的。
   ⇒ 本轮交接单里的读数**附了基线对照**，可以照此要求下一轮。
5. ⚠ **race 仍然是「一次快照」**（沿用 round17 §5.1 的口径）：本轮**没有**跑 `-race`
   ⇒ 新增并发代码（`background` 包）需在下一轮补 `-race` 读数。
6. ⚠ 本轮**没有**动 CI workflow：新门走 `gates.json` 的 `ciRuns` 数据驱动，CI 侧无需改
   ⇒ 但**新门在 CI 上必须真跑** ⇒ 下一轮要读一次 CI 的实际读数。

---

## 6. 下一轮提示词（可直接粘贴）

> 继续 pocket-opencode 的 96 小时滚动审计轮。
>
> 起点：`docs/handoff/2026-10-22-round50-96h-audit-and-branch-merge.md`；
> 主线文档 `docs/design/2026-10-06-recording-quality-fixes.md` **§960**；
> 待拍板项见简报 **第 184 条**（4 问，**不代拍**）。
>
> 本轮已销账：① 上一轮合并静默丢的两道门（`check:doc-ref-integrity` /
> `check:fetch-bounds`）已恢复，`gates` 47→50、`ciRuns` 36→39；② `internal/notes`
> 与 `internal/server` 两个包的 skip 台账已修好（Go 侧 CI 不会再红）；
> ③ 主文档 9 处歧义小节指针已修（字节级门侧 §895/§896 → §958/§959），
> 并新建 `check:doc-section-number-uniqueness`（负控验牙过）；
> ④ 三条分支已并入/删除。
>
> 本轮请做（按优先级）：
> 1. **读一次 CI 的实际读数**（`backend.yml` / `frontend.yml`）⇒ 确认新门
>    `check:doc-section-number-uniqueness` 在 Linux 上**真跑且绿**。
> 2. **补 `-race` 快照**：`cd backend && go test -race -count=1 ./...`
>    （§949 新增了 `internal/background` 包 ⇒ 上一次 race 读数在它之前）
>    ⇒ 并用 NEGCTRL 探针证明 detector 武装。
> 3. **Windows 平台缺陷要不要现在做**（简报 184 ①）：30 个前端测试文件的
>    `new URL(...).pathname` 路径形态 ⇒ 若做，**逐文件**改并逐文件验证，
>    禁止一次性批量替换。
> 4. **§948–§957 那 38 个提交的审计回填**：本轮只验了「它们不引入回归」
>    （基线对照），**没有逐条审它们的判据本身** ⇒ 下一轮按 §958 的读法
>    （行为门 + 接线扫描 + 变异）逐节核。
> 5. **合并纪律固化**（本轮最贵的教训）：合并**名单型文件**（`gates.json`、
>    `package.json`、豁免清单、台账）时，必须**逐条核对两侧条目**，
>    并在合并后跑一次 `git diff HEAD --stat` 看是否「零内容变化」——
>    本轮的两个分支分别是「带进 180 文件」与「带进 0 行」，**两种都要说清**。
