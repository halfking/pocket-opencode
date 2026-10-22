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

## 2. 读数：CI 的真实结论（工具：GitHub REST API，公开仓免鉴权）

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
⚠ 保留的两个 Windows 坑：① 不能用 `cmd /c "... > log 2>&1"`（正斜杠被当开关分隔符）
⇒ 改成**纯管道捕获 + node 自己落盘**（顺带绕开 PowerShell `>` 的 UTF-16LE 陷阱）；
② LF worktree 必须 `git -c core.autocrlf=false worktree add` ＋ `git ls-files --eol` **自证 `w/lf`**
（否则对照两棵 CRLF 树 ⇒ 结论恒等 ⇒ **假绿**）。

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
- **Go#3 的 `-race`** —— 本机 `CGO_ENABLED=0` 且**无 C 编译器**
  ⇒ `go test -race` 直接报 `go: -race requires cgo` ⇒ **只能由 CI 验证**。
- **CI 推上去之后是否转绿** —— 等 CI 读数。

---

## 6. 遗留风险（下一轮的入口）

1. ⏳ **Go#3 的 `-race` 验证**：CI 的 `go test -race ./... -count=1`
   （`.github/workflows/backend.yml:118`）—— 推上去后看读数。
2. ⏳ **「设备本地 vs 固定 +08」**的产品口径待拍板（简报 189 ①）。
   ⇒ 整链只在 +08 设备上自洽；本轮**只改判据层**，产品口径一个字没动。
3. ⏳ **新门形态待设计**（简报 189 ②）：`check:crlf-needles` 只认**直接写在 `.replace()` 里**的
   字面量 ⇒ **看不见「变量/数组里存 needle 再传进去」**（族 A 正是如此）⇒ 该门有覆盖盲区。
4. ⏳ **本机要不要装 C 工具链**（简报 189 ③）—— 决定 `-race` 类问题能不能在提交前看见。
5. ⏳ **§948–§957 那 38 个提交的审计回填** —— 本轮**未做**。
6. ⏳ **本机残留 worktree**：`C:/workspace/_po-base-main`（detached `0fdda422`）、
   `C:/workspace/_po-lf-main`（detached `23bc3529`，A/B 对照载体）——
   **都有 junction，清理前先拆 junction**（否则会带走主工作区的 `node_modules`）。

---

## 7. 下一轮提示词

> 请按本交接单 §6 的 6 条遗留风险逐条推进：
> ① 读 CI 的**真实结论**（GitHub REST API：run → job → step 结论，不要只看本地门禁读数），
>    确认本轮推上去之后 backend / frontend 两个 job 是否转绿；
>    ⚠ 若 frontend job 仍红，**先看它红在哪个 step** —— 前面某个 step 红会把后面的遮住
>    （§960.7.1 那个机制本轮又生效了一次：`test:all` 从没跑到过，
>    是本轮修完前一处才第一次露出 `meeting-todo-readback-day` 的 TZ=UTC 红）。
> ② 若 backend job 仍红，**读 job 日志里的 `--- FAIL:` 原文**，逐条定位；
>    ⚠ 注意「本机绿」的三类（真 race / 时区 / CRLF）**本机永远看不到**。
> ③ 处理简报 189 的三件待拍板（**不代拍**）：产品口径 / 新门形态 / 要不要装 C 工具链。
> ④ 做 §948–§957 那 38 个提交的审计回填。
> ⑤ 收尾：清理本机两个残留 worktree（**先拆 junction**），
>    同步主文档 / 简报 / 新交接单，提交、合并到 main、推送。
> 纪律沿用：**先量影响面再动手**（本轮第一版前端改法被 2×2 矩阵实测推翻并回退）、
> **判据要有牙**（每处修复都做变异负控且实测转红，绿了不算数）、
> **负控判「转红」要同时看退出码与 FAIL 行**（构建失败 ≠ 用例转红）、
> **归一在产出口/读入口**（不是改断言形态）。
