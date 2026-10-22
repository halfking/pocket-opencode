# round51 · 2026-10-22 · 读 CI 真实读数 ⇒ main 长期全红；修 5 处 CI 真红 + Windows 26 个前端红文件（4 个根因族）；A/B 对照脚本入库

> 上一轮 handoff：`docs/handoff/2026-10-22-round50-96h-audit-and-branch-merge.md`
> 本轮基线：`origin/main` = `cdbf8422`（并发会话的 §962）→ 本轮合并后 = 本轮提交
> ⚠ **并发提示**：本轮开工时 `origin/main` 已被并发会话推进到 `cdbf8422`，
> 且它**占了 §962 与简报第 188 条** ⇒ 本轮顺延用 **§963 / 简报第 189 条**，
> 并在自己的章节里登记并存关系（**没有改对方的标题**）。

---

## 1. 一句话结论

上一轮记的「CI 全绿」是**局部读数**（只跑了门禁链，没看 workflow 的**测试 job**）。
本轮读 CI 的**真实结论**才发现：**main 上最近 12/12 次 run 全红**。
⇒ 39 条门禁在 Linux 上**是绿的**，**红的是测试 job**。
⇒ 本轮定位并修掉 **4 条 Go 真红**（含 1 条**测试自身**的真 data race）＋ **2 条前端真红**；
另把 **Windows 上 26 个前端失败文件**归成 **4 个根因族**逐个修完（62 条红 → 0）。

---

## 2. ⭐ CI 的**权威读数**（修完之后，横向对照 15 个 run × 4 个关键 job）

| sha | frontend-lint | backend-test | gates-parity | e2e-web |
|---|---|---|---|---|
| `7ce60119` / `6dfbc907` / `21a393e2` / `cdbf8422`（**修之前 4 个 run**） | **failure** | **failure** | success | — |
| `640ca85a`（本轮第 2 笔） | ✅ **success** | ✅ **success**（×3 job） | ❌ failure（gofmt） | ❌ failure |
| `c6e9acfe`（本轮第 3 笔，修 gofmt） | 见 §2.1 | 见 §2.1 | 见 §2.1 | 见 §2.1 |

⇒ ★ **本轮把「长期红」的前端与后端两个 job 全部修成了绿**：

- **frontend-lint** ⇒ **success** ⇒ 12 次连续红断在这一笔。
- **backend-test**（`test` / `test-pg`）⇒ **success**（3 个 job 全绿）
  ⇒ ★ **特别是 Go#3（真 data race）**：CI 跑的是 `go test -race ./... -count=1`
  （`.github/workflows/backend.yml:118`）⇒ **本机跑不了的那条，CI 跑通了**
  ⇒ ⇒ **§5 里「Go#3 本地未验证」那条登记可以销账**。
  ⇒ ★ `test-pg` 也绿 ⇒ **Go#2 的 `pgTimeEqual` 在真 PG 上生效**。
- **gates-parity** ⇒ `640ca85a` 那次红在**第 20 项 `check:gofmt`**
  ⇒ 成因是**本轮第 2 笔自引入的**（`transcript_test.go` 新增 doc comment 的
  缩进是 Go 1.19 之前的形态）⇒ 第 3 笔用 `node scripts/check-gofmt.mjs --fix` 修掉
  ⇒ ⚠ **门禁自己的口径**会先剔掉 CRLF 行尾噪声
  （本机裸 `gofmt -l` 报 **816**，其中 **815 是纯行尾伪债**，真债只有 **1**）
  ⇒ 拿 `gofmt -l` 当判据会得到「800+ 个文件不 gofmt」的**假结论**。
- **e2e-web** ⇒ 报错原文 `connect ECONNREFUSED 127.0.0.1:8088`
  ⇒ ⚠ **真因已查实：不是「后端没起来」，是「端口写死 8088 vs CI 把后端起在 8090」**
  （见 §6 第 6 条）⇒ **固有的配置不一致** ⇒ 与本轮改动零相关。
  ⇒ ⚠ 但它**是长期红**：翻 14 次 run（2026-10-17 → 2026-10-22）⇒ **14/14 全红**
  ⇒ ⇒ 它与「12/12 全红」**不是同一个成因** ⇒ 别把两者混成一句「CI 都是红的」。

### 2.1 ⏳ 待补：`c6e9acfe` 这一笔的最终读数

⇒ **下一轮先读这一行**（GitHub REST API 的 run → job 结论）：
`c6e9acfe` 的四个 job 结论 ⇒ 若 `gates-parity` 转绿 ⇒ main 上四个 job 只剩
`e2e-web`（环境性）⇒ **长期红的成因已全部销账**。

---

## 3. 读数：CI 的真实结论（工具：GitHub REST API，公开仓免鉴权）

| 项 | 读数 |
|---|---|
| main 最近 run 数 | 12 |
| 结论 | **12/12 全红**（backend + frontend 双 job failure，每次红在同一个 step） |
| backend job | `test` step「Run tests」failure |
| frontend job | 「Run notes/meetings/permission helper tests」failure |
| `gates parity` job | ✅ success（**39 条门禁在 Linux 上是绿的**） |
| `android assembleDebug` | ✅ success |
| `backend build gate` | ✅ success |

失败点（**读 job 日志**，不是猜）：

- backend 恰好 4 条 `--- FAIL:` ⇒ `TestIsFutureInvoiceDate` /
  `TestS943TombstoneGuardRefusesToMoveAnotherScopesTombstone` /
  `TestPGUpdateMetaScopedLeavesTranscriptUntouched` /
  `TestDynamicGatewayStreamChainVisitsEachCandidateOnce`
- frontend 恰好 1 条 `not ok` ⇒ `note-voice 来源的提醒 expr 带真实 due…`
  （`expected: 29857860` / `actual: 29857380` ⇒ 差 **480 分钟 = 8 小时 = 一个时区偏移**）

⚠ **注意遮蔽机制**（§960.7.1）：frontend job 在「notes/meetings/permission helper tests」就红
⇒ **后面的 `npm run test:all` 从没跑到过** ⇒ 它里面的红一直**被遮住**。
本轮修完前一处后，**下一个被揭出来的 CI 阻塞点**就是
`meeting-todo-readback-day.test.mjs`（时区基准在 TZ=UTC 下不成立）—— 本轮一并修掉。

---

## 3. 本轮改了哪些文件（2 笔提交）

### 3.1 第 1 笔 `64c31e26`（+ 合并 `origin/main`）：CI 真红 5 处

| # | 文件 | 根因 | 修法 |
|---|---|---|---|
| Go#1 | `backend/internal/email/invoice.go:911` | `time.Parse`（固定 UTC）vs `now.Location()` ⇒ **结论随时区变** | `ParseInLocation` + `!t.Before`；新增 4 时区 × 5 用例判据 |
| Go#2 | `backend/internal/meeting/transcript_test.go`、`store_tombstone_scope_test.go` | PG `timestamptz` 只存**微秒**、Go 是**纳秒** ⇒ 往返值 `Equal` 恒红 | 新增 `pgTimeEqual`（容差 **1µs = PG 精度单位**）＋ `TestPGTimeEqualTolerance`；**内存版保持严格相等** |
| Go#3 | `backend/internal/server/llmbff_provider_adapters_test.go:358` | `seen` map 被 handler goroutine 写、主 goroutine 读 ⇒ **真 data race** | 加 `sync.Mutex`（**生产代码一行没动**） |
| 前端#1 | `frontend/src/features/notes/note-todo-persist.test.ts`、`frontend/src/features/meetings/meeting-due-plan.test.ts` | expr 是「本地墙钟 + 固定 +08」，`parsed.at` 是设备本地时区 ⇒ **不是同一瞬间** | 判据改为**与时区无关**（墙上钟字段 ＋ 声明的 `+08:00` ＋ epoch 级双带偏移判据） |
| 前端#2 | `frontend/src/features/notes/note-todo-persist.test.ts` | 负控锚点是 **LF 形态**跨行串，Windows 检出命中不了 ⇒ **负控静默失效** | **读入口**归一 `.replace(/\r\n/g, '\n')` |

### 3.2 第 2 笔（本轮）：Windows 26 个前端红文件（4 个根因族）

| 族 | 根因 | 修法 | 文件数 |
|---|---|---|---|
| A | Windows 绝对路径**裸插**进 JS/TS 字符串字面量 ⇒ `\w` `\p` `\f` `\n` 被当转义符 ⇒ esbuild 解析不到 ⇒ `before` 钩子抛错 ⇒ 整组 `cancelledByParent` | 新增 `asModulePath(p)`（正斜杠化 + `JSON.stringify`），所有写入口走它 | 5 |
| B | `new URL(...).pathname` 在 Windows 上是 `/C:/...` ⇒ 拼出 `C:\C:\...`；`path.relative()` 给反斜杠 | `fileURLToPath(new URL(...))`；`relative(...).replace(/\\/g,'/')` | 6 |
| C | 变异锚点**正则里写死 `\n`** ⇒ CRLF 下匹配不上 ⇒ 报「判据恒真」而**真因是「变异没发生」** | **读入口**归一（仓内先例：`account-read-path-normalization.test.mjs:46-48`） | 9 |
| D | 判据产出的**绝对路径** vs 清单的**正斜杠相对路径**；ESM `import()` 收 Windows 绝对路径 | 产出侧正斜杠化；`pathToFileURL(...).href` | 6 |

外加 **`meeting-todo-readback-day.test.mjs`**：把进程时区**钉死**成 `Asia/Shanghai`
（它在 TZ=UTC 下**数学上不可能成立** ⇒ 是 `test:all` 的真实 CI 阻塞点）。

### 3.3 工具：`scripts/tools/ab-crlf-lf-gates.mjs`（**工具，不是门禁**）

A/B 对照脚本从 `.scratch` 一次性脚本 → **可复用的仓内工具**。
参数：`--root` / `--wt` / `--out` / `--keep-wt` / `--no-gates` ＋ 位置参数 `ref`。
⚠ 为什么不是门禁：它要建 worktree + 跑两遍全量门禁（单遍 249.5s）。
⚠ 保留的三个 Windows 坑：① 不能用 `cmd /c "... > log 2>&1"`（正斜杠被当开关分隔符）
⇒ 改成**纯管道捕获 + node 自己落盘**（顺带绕开 PowerShell `>` 的 UTF-16LE 陷阱）；
② LF worktree 必须 `git -c core.autocrlf=false worktree add` ＋ `git ls-files --eol` **自证 `w/lf`**
（否则对照两棵 CRLF 树 ⇒ 结论恒等 ⇒ **假绿**）；
③ **`--no-gates` 这条早退路径也必须收掉自建的 worktree** —— 上一版在自证后直接 `exit`
⇒ 残留两棵（还带着指向主工作区的 junction）⇒ **本轮已修**：`cleanupWt()` 提到早退分支之前，
实测 `--no-gates` 跑完**目录零残留**、`git worktree list` 只剩主工作区。
⚠ **清理说明已按负控订正**：旧说法「不拆 junction 的话 `git worktree remove` 会把主工作区的
`node_modules` 一起带走」⇒ **实测不成立**（目标目录 50 个文件一个没少）
⇒ 真实理由是**带着 junction 删不干净**（git 会把 junction 与它上面的目录原地留下）。

---

## 4. 判据纪律：每处修复都做了**变异负控**，且**实测转红**

| 组 | 负控条数 | 结果 |
|---|---|---|
| Go#1 | 2（改回 `time.Parse` / 改回 `t.After`） | 全转红 ✔ |
| Go#2 | 3（容差→1ms / 改回严格 `Equal` / 容差→1ns） | 全转红 ✔ |
| 前端 | 5（退回 epoch 直比 ×3 时区 / 去掉 CRLF 归一） | 全转红 ✔（含 **TZ=+08 仍绿** 的正向对照） |
| readback 钉 TZ | 3（把 `deviceTimeZone()` 变异成 `'UTC'` × 3 时区） | 全转红 ✔ ⇒ **钉 TZ 不是放宽，是修复** |

⚠ **负控脚本的纪律**：判「转红」必须**同时看退出码与 `--- FAIL` 行** ——
变异写错会给出「退出码≠0 但没有 FAIL 行」（= 构建失败），那**不是**「判据有牙」。
本轮踩到一次：只改 `return` 却留下未使用变量 ⇒ 编译失败 ⇒ 误以为负控通过。

---

## 5. 读数（本轮实测）

```
go test ./internal/email/ ./internal/meeting/ ./internal/server/  （目标用例）  ⇒ ok
node --test <两个前端文件>  TZ=UTC / TZ=Asia/Shanghai  ⇒ 33/33 绿
node --test src/features/meetings/__tests__/meeting-todo-readback-day.test.mjs
        TZ=Asia/Shanghai / UTC / America/New_York  ⇒ 8/8 绿（钉 TZ 后）
前端全量（374 个测试文件，Windows）
        TZ=Asia/Shanghai ⇒ tests=4722 pass=4720 fail=0，失败文件 0
        TZ=UTC           ⇒ 同上（钉 TZ 后）
```

⚠ **未实测（如实登记）**：

- **Linux 上全量前端到底绿不绿** —— 本机无 docker / 无 Linux ⇒
  「26 个红在 Linux 上是绿的」是**基于根因分析**（每个根因都逐条核到
  「该形态只在 Windows 出现」的代码位置），**不是**「在 Linux 上实测过」。
  ⇒ ★ **但 CI 的 `npm run test:all` 在 Linux 上跑了全量 374 个文件且通过**
  （frontend-lint job ✅ success，§2）⇒ **这条已由 CI 间接验证**。
- **Go#3 的 `-race`** —— 本机 `CGO_ENABLED=0` 且**无 C 编译器**
  ⇒ `go test -race` 直接报 `go: -race requires cgo`
  ⇒ ★ **但 CI 的 backend job 跑的就是 `go test -race ./... -count=1` 且 ✅ success**
  （§2）⇒ **这条已由 CI 验证**（本机跑不了的那条，CI 跑通了）。
- **`c6e9acfe` 的最终读数** —— 交接时它还在跑 ⇒ 见 §2.1。

---

## 6. 遗留风险（下一轮的入口）

1. ✅ **Go#3 的 `-race` 验证** —— **已由 CI 完成**：backend job ✅ success
   （CI 跑的是 `go test -race ./... -count=1`）⇒ 那个 data race 真的修好了。
   ⇒ 原 §5 里「本条本地未验证」的登记**可以销账**。
2. ⏳ **「设备本地 vs 固定 +08」**的产品口径待拍板（简报 189 ①）。
   ⇒ 整链只在 +08 设备上自洽；本轮**只改判据层**，产品口径一个字没动。
3. ⏳ **新门形态待设计**（简报 189 ②）：`check:crlf-needles` 只认**直接写在 `.replace()` 里**的
   字面量 ⇒ **看不见「变量/数组里存 needle 再传进去」**（族 A 正是如此）⇒ 该门有覆盖盲区。
4. ⏳ **本机要不要装 C 工具链**（简报 189 ③）—— 决定 `-race` 类问题能不能在提交前看见。
5. ✅ **§948–§957 的审计回填** —— 本轮**已做**：**39 笔（不是 38）成果全部还在 main 上，无一丢失**。
   ⇒ 判据落在「成果」上（文件/符号是否还在），不落在「那一行有没有一个字没动」上
   （第一版逐行匹配报出 17 笔「疑似丢失」，第一处就是误报：
   `run_all.py` 的 BATCHES 列表只是换了行，s948–s957 全在）
   ⇒ 报「有缺失」的 2 笔全是同一个符号 `§803.4`，而那**正是 §955 自己修掉的死引用**
   ⇒ ⇒ **不判为丢失**。详见主文档 §963.7.1。
6. ⏳ **e2e-web 的长期红**（2026-10-17 起连续红）—— 报错 `connect ECONNREFUSED 127.0.0.1:8088`
   ⇒ ⚠ **真因已查实（读 job 日志原文）**：**CI 把后端起在 8090**（`POCKET_HTTP_PORT` ＋
     healthz 探针都在 8090，vite 代理默认也是 8090），而 `e2e/web/helpers/tokenAuth.ts:125` 的
     `assertTokenWorks(apiBase = 'http://127.0.0.1:8088')` **把端口写死 8088**，
     且 `e2e/web/specs/authenticated-shell.spec.ts:54` 调它**不传参** ⇒ 探针必然 ECONNREFUSED
     ⇒ **`beforeAll` 挂掉 ⇒ 整个 spec 全是噪音**（那份 spec 的注释自己写着「若这一步红了，后面全是噪音」）
     ⇒ ⇒ **长期红的真因是「e2e 侧端口写死 vs CI 侧端口」的配置不一致，不是偶发环境问题**。
     ⚠ 本机跑不了（要起后端 + Playwright）⇒ **修法很小但本轮没改**（改完会让一套从未真跑过的用例
     第一次真跑 ⇒ 影响面本机量不了）⇒ 登记为待拍板（简报 189 ⑤）。
   ⇒ ⇒ 改完只能靠 CI 读数；**先量影响面再动手**。
   ⇒ 📌 **本轮复测的准确读数**：**最近 12 次 e2e-web run 全部 failure（success 0 次）**，
     最近一次在 `c6e9acfe`；⚠ **它最近 5 笔没出 run，不是「跑过」，是「按设计没触发」** ——
     `.github/workflows/e2e-web.yml` 的 `on.push.paths` 只认 `e2e/web/**`·`frontend/src/**`·
     `frontend/vite.config.ts`·`backend/**`·它自己 ⇒ 那 5 笔只动 `docs/**` 与 `scripts/tools/**`
     ⇒ **一条都没匹配上** ⇒ **下一次它真跑，是有人改了 `frontend/src/**` 或 `backend/**` 的时候**。
7. ✅ **本机残留 worktree：已清理完毕**（2026-10-22 本轮末，**不再是遗留**）：
   - `C:/workspace/_po-base-main` —— detached `0fdda422` ⇒ **已删 + 已注销**
   - `C:/workspace/_po-lf-main` —— detached `cdbf8422` ⇒ **已删 + 已注销**
   ⇒ `git worktree list` 现在**只剩主工作区**（`C:/workspace/pocket-opencode 1ac89236 [main]`）。
   ⇒ ⚠ **旧说法被负控证伪**：「先拆 junction，否则 `git worktree remove` 会把主工作区的
     `node_modules` 一起带走」⇒ **实测不成立**（夹具负控：目标目录 50 个文件一个没少）
     ⇒ 真实理由是**带着 junction 删不干净**（git 会把 junction 与它上面的目录原地留下）。
   ⇒ 正解顺序：① `fsutil reparsepoint delete <junction>`（摘成**普通空目录**，目标不动）
     ② `git worktree remove --force <wt>` ③ **复验**主工作区读数。
   ⇒ **复验读数**：`node_modules` **9070 文件 / 227.3 MB**、`dist` **220 文件 / 7.1 MB**
     ⇒ 与删除前基准**逐项一致**；另跑 `vue-tsc --noEmit` exit=0（工具链功能完好）。
8. ✅ **A/B 全量对照已在 `b6942ea7` 上复跑完毕**（本轮末）：
   - CRLF 工作树 **✅ 全部 39 项通过**（退出码 0）
   - LF 检出树 **✅ 全部 39 项通过**（退出码 0）
   - **跑过的门列表逐条一致** ✓
   ⇒ **当前 commit 上没有「仍在依赖检出形态」的门禁**（此前这条结论只属 `cdbf8422` 时期）
   ⇒ ★ 工具的**完整路径首次端到端跑通**（建树 → 两遍全量门禁 → 自动拆 junction → 自动删树），
     收尾**零残留**；跑完两遍门禁后主工作区工作区**仍干净**（门禁不弄脏树）。
   ⇒ ⚠ 副作用已确认：跑对照期间**本机负载升高**（两遍各约 250s），别与别的重活并行。

---

## 7. 下一轮提示词

> 请按本交接单 §6 的遗留风险逐条推进：
> ① **先读 `c6e9acfe` 那一笔的 e2e-web 结论**（本交接时它还在跑）
>    —— 前面三个 job（frontend-lint / backend-test / gates-parity）**已经全绿**，
>    只剩 e2e-web。若它也红了，**先看它红在哪个 step + 报错原文**：
>    `640ca85a` 那次的报错是 `connect ECONNREFUSED 127.0.0.1:8088`
>    ⇒ ⚠ **真因已查实：端口写死 8088 vs CI 起在 8090**（见 §6 第 6 条）
>    ⇒ **不是偶发环境问题**（⇒ 判断依据：读日志原文，别猜 —— 本轮就是靠日志原文才发现不是「后端没起来」）。
> ② 处理简报 189 的三件待拍板（**不代拍**）：产品口径 / 新门形态 / 要不要装 C 工具链。
>    ⚠ 特别提醒：**本轮「门禁 39/39 绿」已经不是 CI 绿不绿的问题了**
>    —— 三个 job 真的绿了 ⇒ 下一轮起，「CI 红」必须**逐个 job 读结论**才算数，
>    不能再用本地门禁读数代替。
> ③ ~~清理本机两个残留 worktree~~ ⇒ ✅ **2026-10-22 已清理完毕**（两棵都删 + 都注销，
>    `git worktree list` 只剩主工作区；主工作区 `node_modules`/`dist` 读数与删除前逐项一致）
>    ⇒ **本项不再是遗留**。⚠ 但「留一棵当 A/B 对照载体」这个用法**要改**：
>    `scripts/tools/ab-crlf-lf-gates.mjs` 现在**自己会建、自己会清**（`--no-gates` 早退路径也收尾）
>    ⇒ **不需要预先留一棵**（且留着会带着指向主工作区的 junction ⇒ 自己变成风险面）。
> ④ 收尾：同步主文档 / 简报 / 新交接单，提交、合并到 main、推送。
> ⑤ 若要再修 e2e-web：⚠ **本机跑不了 e2e**（要起后端服务 127.0.0.1:8088 + Playwright），
>    ⇒ 改完只能靠 CI 读数 ⇒ **先量影响面再动手**。
>    ⚠ **触发面要知道**：`e2e-web.yml` 的 `on.push.paths` 只认 `e2e/web/**`·`frontend/src/**`·
>    `frontend/vite.config.ts`·`backend/**`·它自己 ⇒ **只改 `docs/**` / `scripts/**` 不会触发它**
>    ⇒ ⇒ **要验 e2e-web，改动就得落到 `frontend/src/**` 或 `backend/**` 上**。
>
> **纪律沿用（本轮的实测教训）**：
> ⇒ 📌 **完整清单在主文档 §963.9.1（读数纪律 R1–R10）与 §963.9.2（合并纪律 M1–M10）**
>   —— 那是**可操作清单**（每条都是「做什么 / 不做什么」），下面只是摘要。
> - **先量影响面再动手** —— 本轮第一版前端改法被 2×2 矩阵（HEAD vs 工作树 × TZ）实测推翻并回退。
> - **判据要有牙** —— 每处修复都做变异负控且**实测转红**，绿了不算数。
> - **负控判「转红」要同时看退出码与 FAIL 行** —— 构建失败 ≠ 用例转红（本轮踩到）。
> - **归一在产出口/读入口**，不改断言形态（断言里的字面量是有意义的仓内约定）。
> - **审计「成果还在吗」要落在「文件/符号还在吗」**，不落在「那一行有没有一个字没动」。
> - **拿门禁自己的口径当判据** —— 本机裸 `gofmt -l` 报 816 个文件，
>   而门禁（剔掉 CRLF 噪声后）只报 1 个 ⇒ 拿错口径会得到「800+ 个文件不 gofmt」的假结论。
> - **「本机绿」的三类（真 race / 时区 / CRLF）本机永远看不到** ⇒ 必须靠 CI 读数收口。

---

## 8. ⭐ 并入的外部分支：`fix/acc-runtime-service-jwt`（ACC workstream）

**为什么会有这一节**：属主「请提交代码并合并到主分支中推送」时，本会话的工作
**早已提交推送完毕**（工作区干净、`main == origin/main`）⇒ 仓库里**唯一**没落地的东西
是**另一个会话的分支** ⇒ 那一问的实际对象就是它。

### 8.1 合并前的四道查证（缺一道都不该合）

1. **它是不是真的「没并入 main」** ⇒ merge-base = `1134e751` = main tip
   ⇒ 它是 main 的**严格后代** ⇒ 合并无冲突，**合并结果树 == 分支 tip 树**
   （`44b5c8f9…`）⇒ main 现有内容**一字不丢**。
2. ⭐ **它此前有没有被 CI 验过** ⇒ **没有**。`backend.yml` / `frontend.yml` /
   `backend-pg.yml` 的 `push.branches` 是 `[main, feat/**, …]`，而它在 `fix/**` 下
   ⇒ **只有无 branches 过滤的 `e2e-web` 跑过**（4 次全红 = 已知环境性红）
   ⇒ ⇒ **合并是这 45 个文件（含 13 个 Go 文件）第一次进 CI** ⇒ 必须先本机验。
3. **密钥扫描**（公开仓 ⇒ 必做）⇒ `scripts/seed_llm_gateway.sh` 只从 env/envs 读 key
   且不写入仓库；新增行里的字面量密钥全是测试夹具（`static-token-abc` /
   `signed.jwt.value` / `control-secret-abc`）⇒ **无泄漏**。
4. **隔离 worktree 上按 CI 同口径跑**（`_po-acc-eval`；合并结果树 == 被验树 ⇒ 读数直接可用）：
   - `run-gates.mjs --ci` ⇒ **39/39 全过**（99.7s）
   - `npm run test:all` ⇒ **374/374 个测试文件全部实跑**
   - `go build ./...` / `go vet ./...` ⇒ exit 0
   - `go test ./... -count=1` ⇒ **连绿 3 轮**（另加 1 轮基线对照，共 4 次全绿）

### 8.2 合并前抓到并修掉的 2 处门禁真债（都是分支自带的）

| 门 | 现象 | 修法 |
|---|---|---|
| `check:gofmt`（第 20 项） | `backend/cmd/pocketd/main.go` 的 `accjwt.Options{...}` 新增 `ServiceIdentity: true`（键名更短）却没重排冒号对齐 | 门禁自己的 `--fix`（6 增 6 删，**纯对齐**，EOL 未变） |
| `check:env-example`（第 23 项） | 分支新增的 `POCKET_ACC_RUNTIME_URL` / `POCKET_ACC_CONTROL_SECRET` 被 backend 读取却没登记进 `.env.example` | 按代码默认值（空串 = 不启用）补登记 |

⇒ ★ **第二处是被「失败即停」遮住的**：第 20 项红 ⇒ 第 21+ 项**从没跑过**
⇒ 修好第一处，第二处才**第一次露出来** ⇒ §963.9.1 **R10 的又一例**。

### 8.3 一个差点误判的读数（记下来免得下次再踩）

门禁输出里有 4 行 `FAIL appId 应为 com.kaixuan.opencode.pocket，实际 com.example.wrong`
等 ⇒ 看着像 maestro 流有问题 ⇒ **实为 `check-maestro-flows` 的自检故意错值**
（`scripts/check-maestro-flows.mjs` 敏感度 3：`yamlOf('com.example.wrong', …)`）
⇒ 那道门实际 **exit 0 / 结果 OK** ⇒ ★ **门禁输出里的 `FAIL` 行 ≠ 门禁红**
⇒ 要同时看**退出码**与**那道门的结论行**（§963.9.1 R4 的同族）。

### 8.4 合并方式与结果

- `git merge --no-ff`（**不做快进**）⇒ 万一 CI 翻红可 `git revert -m 1` **非破坏性**回退。
- 合并提交 `227a5a96`；推送后 `origin/main == 227a5a96`。
- 合并结果树 = 被验树 = `837c82f5…`（`git rev-parse HEAD^{tree}`）
  ⇒ **推上去的就是验过的那棵树**。
- CI 读数：`227a5a96`（合并提交）⇒ **三个关键 job 全绿**：`backend #1031` success（`build gate` ✅ ＋ `test` = `go test -race ./... -count=1` ✅）／`backend-pg #328` success（`test-pg` ✅）／`frontend #1016` success（`typecheck + build + node tests` ✅ ＋ `android assembleDebug` ✅ ＋ `gates parity` ✅）⇒ **这批内容第一次进 CI 就是绿的**；❌ 只剩 `e2e-web #463` failure（`connect ECONNREFUSED 127.0.0.1:8088`）

### 8.5 本轮新增的两件待拍板（**不代拍**）

1. ⚠ **要不要把 `fix/**` 加进 `backend.yml` / `frontend.yml` / `backend-pg.yml` 的 `push.branches`？**
   —— 现在这三个 workflow 只认 `[main, feat/**, …]` ⇒ **任何 `fix/*` 分支的工作在合并前都进不了 CI**
   ⇒ 本次这两处门禁真债正是「合并前才发现」的例子（若这批内容直接进了 main 而没被发现，
   下游会带着两处门禁红）。代价：每个 `fix/*` 分支的 push 都会多跑 3 个 job（CI 分钟）。
   ⇒ ⇒ 登记在简报 189 的 ④。
2. ⚠ **e2e-web 的长期红按「真因」修不修**？—— 真因已查实（见 §6 第 6 条）：
   **e2e helper 把端口写死 8088，而 CI 把后端起在 8090** ⇒ 探针必然被拒 ⇒ `beforeAll` 挂掉。
   ⇒ 修法很小，但**改完会让一套从未真跑过的用例第一次真跑** ⇒ **影响面本机量不了** ⇒ 不代拍。
   ⇒ ⇒ 登记在简报 189 的 ⑤。

### 8.6 远端分支处置：**保留**（并发会话仍在活跃）

- `origin/fix/acc-runtime-service-jwt`：**保留未删**。
  ⇒ ⚠ **理由**：属主那个会话**仍在活跃** —— 判据是它的工作目录 `C:/workspace/acc-wc-test`
  **2026-10-22 22:24:05 还在写入**（本轮合并前 6 分钟）⇒ 按并发纪律
  **不删别人在途分支的远端引用**。
- ⇒ ★ **但内容已经全部在 main 里**（合并提交 `227a5a96`）⇒ 删的只是引用，不是成果。
- ⚠ **一个必须说清的后果**：我合的是它**截至 `f897b7df`（14:00）**的那一批；
  它此后若再推新提交，**那些提交不会自动进 main** ⇒ 需要它自己再合一次，
  或下一轮由我合（届时同样先做「四道查证」）。
