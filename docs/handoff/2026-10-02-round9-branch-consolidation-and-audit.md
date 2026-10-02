# round9：24 小时分支归并 + 审计 —— 5 处「git 没报错但语义已经错了」的合并缺陷

日期：2026-10-02
分支：`audit/2026-10-02-24h-r9`（本文件随合并进入 main）
范围：`feat/mail-config-deploy` 58 个未合并提交 + 三个 worktree 的未提交改动

## 0. 一句话结论

`feat/mail-config-deploy` 是 24 小时内唯一真正未合并的分支，58 个提交、89 个文件、
+10235/-203。**git 自己只报了 5 个冲突，而真正危险的合并缺陷有 5 处，全部落在
git 判定为「自动合并成功」的区域里**——它们不会让 merge 失败，只会让合并后的
代码在运行期或静默路径上出错。本轮逐处找出并修正，判据见下。

> **教训（可复用）**：merge 冲突数是**上界信号**，不是成本估计，也不是质量估计。
> 「Auto-merging 成功」只说明**文本**三方合并成功，不说明两侧语义合起来仍然正确。
> 本轮 5 处缺陷里有 4 处位于 git 判定为无冲突的文件里。

## 1. 分支盘点与处置

| 分支 | 相对新 main（7eee3669） | 处置 | 依据 |
|---|---|---|---|
| `audit/2026-10-02-24h` | ahead=0 | **删** | 完全已合并 |
| `ci/wire-style-guards-into-gates` | ahead=0 | **删**（worktree 一并删） | 完全已合并 |
| `feat/2026-10-01-stt-service` | ahead=0 | **删**（worktree 一并删） | 完全已合并，14 个有效文件已先抢救 |
| `feat/mail-config-deploy` | ahead=0 | **本轮合并后删** | 是本次 merge 的第二父提交 |
| `email-pipeline-snapshot-2026-10-01` | **ahead=161** | **保留，不删** | 见下 |
| `audit/2026-10-02-pending-human-decisions` | 新建 | **保留** | 见 §5.2，含待批准改动 |

> **`email-pipeline-snapshot-2026-10-01` 差点被误删。** 第一遍盘点时我把它
> 判成「完全已合并」，依据是 `git log main..branch` 输出为空——但那个命令的
> 161 行结果被我看成了下一个分支（`feat/mail-config-deploy`）的。删之前改用
> `git rev-list --count 7eee3669..<branch>` 逐个复核，才发现它领先 161 个提交。
>
> **教训**：盘点分支是否已合并，判据要用**计数**（`rev-list --count`），
> 不要用「`git log` 看起来空不空」——尤其当一次输出里有多个分支时，
> 列表的归属极易串行。删分支是不可逆操作，判据必须是不会看错的那种。

`feat/mail-config-deploy` 曾在 10-02 01:07 被 `5e8d899e` 合入过 25 个提交，
之后分支继续作业，所以本轮是**真增量**（`7eee3669` 相对其父为 89 文件 /
+10235），不是重复合并。

**保留不动的 worktree**：`wt-apkbuild`（detached，持有 round8 判定 dirty=0 的
那份可归因 APK，删了就没产物了）、`wt-mergeprobe`（**有并发会话正在使用**，
本轮全程未触碰）。

## 2. 合并：git 报了 5 个冲突，真正的问题有 5 处

git 报的 5 个冲突：`email/fetcher.go`、`email/store.go`、
`email-classify-run.ts`、`use-email-inbox.ts`、`use-invoice-list.ts`。

### 2.1 `store.go` — `InsertEmail` 的占位符与实参数量对不上（**最严重**）

两侧各自自洽，合起来不自洽：

| | 列数 | 占位符 | 尾部实参 |
|---|---|---|---|
| 分支 | 19 | 19 | `..., time.Now().Unix())` |
| main | **20** | **20** | `..., time.Now().Unix(), time.Now().Unix())` |

main 补了第 20 列 `emails.updated_at`（含 migrate 补列）。而 git 把 SQL 正文
判成「双方一致」放过了，只在**最后一行实参**上报冲突。取分支那一行 ⇒
20 个占位符配 19 个实参，**每一次邮件入库都报**
`bind message supplies 19 parameters, but prepared statement requires 20`。

处置：取 main 的 20 实参 + 分支的 `.Scan(&ins)`，合并后实测
`columns=20 placeholders=20`、`time.Now().Unix()` 出现 2 次。

**这类缺陷测试抓不到**：PG 用例在没设 DSN 时整体 skip，本轮环境即如此。

### 2.2 `store.go` — 两个读路径各漏一半列（**静默失效**）

`GetEmailByID` / `GetEmailByIDScoped` 的 SELECT 列表：

- main 加了 `COALESCE(folder_name,'')`
- 分支加了 `message_id` + `COALESCE(body_purged, FALSE)`

任取一侧都会丢列，而丢列**不产生任何错误信号**——正是分支自己注释里写的
那类缺陷：`message_id` 缺失 ⇒ `emHasReal` 恒 false ⇒ 发票「真实 Message-ID
强确认」判据退化成弱判据；`body_purged` 缺失 ⇒ `summarizeBody` 的
`if em.BodyPurged` 守卫恒不触发 ⇒ 用户软删并清空正文的邮件被重新回源、
喂给 LLM、再把摘要写回已删除的行。

处置：三列取并集，Scan 同步补 `&messageID, &bodyPurged, &folderName`。

### 2.3 `fetcher.go` — `syncBudget` 从包级常量变成了函数内局部变量

分支的 `diag_hard_deadline_test.go` 与 `imap_deadline_test.go` 在**包级**引用
`syncBudget`；main 把它收进了 `Sync` 函数内部。git 两侧都「改过」这个区域，
不构成冲突，但合完 `go vet` 直接红：`undefined: syncBudget`。

这不是「测试写错了」：POP3 兜底能不能跑成完全取决于这个数与 IMAP 实际耗时的差，
测试拿它当**生产常量**核对是对的。

处置：提升回包级 `const syncBudget = 70 * time.Second`，并写明为什么必须是包级
（测试若自己抄一份 70s，抄的那份与生产漂移时不会有任何信号）。

### 2.4 `fetcher.go` — `Sync` 主循环：保住 main 的重构，接住分支的计数修复

两侧改的是同一段循环，但方向相反：

- main 把「取件映射 + 规则评估」统一收敛进 `emailFromMessage` / `applyInlineRules`，
  注释明说此前**另写一份导致产出不兼容的行、重跑时互相覆盖**；
- 分支是重构前的旧内联副本，但它带一个 main 没有的修复：
  `InsertEmailIfNew` —— 只有**真正插入**才计入「新邮件 N」。

整段取分支 ⇒ 回退 main 的去漂移重构；整段取 main ⇒ 丢掉新邮件计数修复，
「整理完成：新邮件 120」会把重复同步全算进去。

处置：保留 main 的结构（含 `highestUID` 的 `imap.UID` 类型），
在 IMAP 与 POP3 两处写入点都改用 `InsertEmailIfNew` + `if isNew { saved++ }`；
`InsertEmail` 保留但降级为委托，新增 `InsertEmailIfNew` 用
`RETURNING (xmax = 0)` 判别（不引入「先查后插」竞态）。

顺带补回分支另一处 main 没有的修复：`ON CONFLICT DO UPDATE` 现在也刷新
`importance` / `action_reason`（`EXCLUDED` 为空 = 该规则没命中，保留旧值），
否则「先收信、后配 rules」这条路是断的。

### 2.5 `use-email-inbox.ts` — main 的判据覆盖不到「一行都没返回」

main 的 `shouldContinueClassify` 有「整批全失败即停」，但条件是
`rowCount > 0 && errorCount === rowCount`。**服务端一行都不返回时
`rowCount === 0`，该条件不成立**，函数返回 `true`——main 自己的用例
（`email-classify-loop.test.mjs:57`）就钉住了这个行为：
`shouldContinueClassify(st({ rowCount: 0, errorCount: 0 })) === true`。

没配 LLM provider 时正是这个形态：`classified` 恒 0、`remaining` 恒等于总数。
后果不是无限循环（轮次上限 20 兜住了），而是**白烧 20 轮请求**，且最终提示说
「达到单次上限 400 封」，把真正的原因（provider 没配）指错了方向。

处置：接入分支的连续零进展判据（`MAX_NO_PROGRESS_PASSES = 2`），停下后复用
main 的 `classifyDoneHint` 并以 `allFailed: allFailed || stalled` 如实报因
（比分支自己那句硬编码「归类未生效（AI 分类服务未配置）」更好：它带 firstError
且经过 `sanitizeFetchHint`）。

## 2.6 `diag_snippet_leak_test.go` — 护栏抓到的**真实生产数据风险**

合入后 `TestPGTestsNeverTargetTheProductionSchema` 转红（这是 8cc01dfd 建的护栏，
本轮第一次真正发挥作用）。它与白名单里其它诊断探针**不是一类**：

- 其它白名单条目全部是「只读真实库探针：无写语句」；
- 这个文件有 1 条 `UPDATE email_accounts SET last_synced_uid = 0,
  last_synced_at = 0`，且 `schema` 在未设 `POCKET_DIAG_SCHEMA` 时**缺省为
  `opencode_pocket`（生产 schema）**，SQL 里还支持 `who = 'ALL'`。

合起来的后果：一条看起来无害的诊断命令
`POCKET_REAL_MAIL_DSN=... POCKET_DIAG_RESET_ACCOUNT=ALL go test ...`
会把**生产库每一个账户**的同步进度归零，触发全量重拉。

处置（先补安全闸，再进白名单，顺序不能反）：

1. 写路径改为**三重开关**：`POCKET_REAL_MAIL_DSN` +
   `POCKET_DIAG_RESET_ACCOUNT` + `POCKET_DIAG_ALLOW_RESET=1`；
2. 写路径**拒绝 schema 缺省值**，必须显式写 `POCKET_DIAG_SCHEMA`——
   让「我要动的是哪个库」出现在命令行里；
3. 再加入 `pgSafeWithoutIsolation`，理由写成可机械核查的形式
   （1 条 UPDATE / 0 条 INSERT|DELETE|DROP|CREATE|TRUNCATE / 三重开关 /
   读路径只读），而不是「应该没事」。

**顺序本身是重点**：先把危险操作堵上，再让护栏放行。反过来做就等于
「为了变绿而登记例外」。

## 3. 护栏冲突：把「判据存在、循环没接」这条保住
分支带了一个源码级接线护栏 `email-classify-loop-wiring.test.ts`，它写死了
`verdict.kind === 'continue'` / `=== 'stalled'`，因此与 §2.5 的合并结果直接冲突
（gates 首次跑就是它红的）。

**没有为了让合并通过而删断言。** 该护栏的理由是成立的：纯函数测绿**不能证明**
调用点真的用了它，而 2026-10-02 那次缺陷的原形态恰恰是「判据存在、循环没接」。
同模块里实际存在**两个各自都有单测**的判据函数
（`shouldContinueClassify` / `classifyRunVerdict`），合并后选哪个是实现选择，
不是缺陷。

处置：改成匹配**任一**判据函数，同时把约束改得更紧——继续标志必须**由判据函数
的返回值赋值**（`/(continueLoop|running)\s*=\s*(shouldContinueClassify|classifyRunVerdict)\s*\(/`），
这样「import 了却没用」仍然会红。

### 3.1 负控：第一次是无效负控，第二次才转红

| 负控 | 做法 | 结果 | 判读 |
|---|---|---|---|
| NC-1 | 在正确代码**旁边**加上 `remaining > 0` 的硬编码终止 | **仍绿 5/5** | **无效负控**——正确接线还在原地，断言当然被满足 |
| NC-2 | 把接线**整段删除**，只留硬编码 `continueLoop = remain > 0` | **红 2/5** | 判据在判，但漏了一条 |
| NC-3 | 同 NC-2 + 收紧零进展断言 | **红 3/5** | 三条断言全部承重 |

NC-1 暴露的是我自己的方法错误：**加**一个缺陷不是负控，**拿掉**接线才是。
NC-2 暴露的是我第一版放宽的断言有洞：它匹配裸常量名 `MAX_NO_PROGRESS_PASSES`，
而该常量在 **import 行**里就出现了，于是「判定整段删掉」照样通过。收紧为要求
**用上**的形态（`noProgressPasses >=` 或 `noProgressPasses,`）——常量名在 import 行
是大写 `MAX_NO_PROGRESS_PASSES`，与代码里的小写驼峰不同，不会被误匹配。

恢复真实代码后 5/5 全绿。**判据必须在两个方向上都验过，只跑绿的不算护栏。**

## 4. Worktree 未提交改动的甄别

### 4.1 `wt-stt`：174 个「改动」里只有 15 个有内容，其中 2 个必须丢弃

`git status` 报 174 个 modified，但加 `--ignore-all-space --ignore-cr-at-eol`
后只剩 15 个文件 / +181-48——**其余 159 个是纯 CRLF 换行抖动**，是
`core.autocrlf=true` 在 Windows 上的产物，不是改动。

必须**丢弃**的两个：

- `frontend/android/capacitor.settings.gradle`
- `frontend/android/capacitor.build.gradle`

它们是被 `mklink /J` 指向主工作区 `node_modules` 后，`cap sync android`
把生成文件里的相对路径改写成绝对路径的产物
（`../node_modules/...` → `../../../openpocket/frontend/node_modules/...`）。
这正是 round8 §2.1 记录的「junction 让 dirty 永远不为 0」的成因。合进去等于
**给每一次后续 APK 构建埋一个污染源**。

**抢救进 main 的 14 个**：`android/{README.md,capacitor.config.ts,package.json}`
（把 `android/` 降级为指针目录，杜绝在两处各建一套 Capacitor 工程导致版本漂移）、
2 个 server 文件的 UTF-8 BOM 去除、一处 gofmt、两处 Vue 的错误提示改走
`useApiError`、3 个测试文件增补、一份 handoff 补写。

### 4.2 `wt-maildeploy`：代码改动已被 main 超越，只抢救了文档

未提交的 `server_auth_extended_test.go` 是一处**高价值安全修复**（测试助手
沿用 DSN 的 `search_path` 而 cleanup 会 `DROP SCHEMA ... CASCADE`，跑一次测试
就可能把生产 schema 连表带数据删光）。但**main 已经有更完整的版本**：无条件生成
一次性 schema + `strings.HasPrefix` 纵深防御。分支的提交版反而是较弱的
`if schema == "" { schema = ... }` 形态。⇒ main 的自动合并结果正确，无需干预。

只抢救了 handoff 文档的未提交部分（+121/-7，第三则更正：「没有任何写入路径」
证据不成立）。

### 4.3 `wt-font`：不并入 main，但**不丢弃**（见 §5.2）

## 5. 待你决定 / 仍阻塞

### 5.1 设备离线（承接 round8 §3.3，未变）

`ping 192.168.31.19` 不通 ⇒ 手机离开网络，宿主侧无解。需要你重开无线调试并回报
新 `IP:端口`。APK 已在 `wt-apkbuild` 构建好且 dirty=0（`29048294`，
sha256 `1E6DA6F5…`），设备回来即可装。

### 5.2 `ResponseHeaderTimeout` 30s → 60s：**未批准，保留在单独分支**

`wt-font` 有一处未提交改动把 `llmgateway` 的 `ResponseHeaderTimeout` 从 30s 调到 60s。
理由成立：本项目网关路由到 glm-5.2 这类推理模型，先花 `reasoning_content` 才回正文
（2026-10-02 实测 63 字总结配了 985 个 reasoning token），于是 `handleNoteSummarize`
的 60s 预算只有前 30 秒真能用，30~60s 是死预算。

**但本轮不并入 main**，两个原因：

1. main 的 `fbdf65b1` 明确写了「先记录不擅自改」「这个取舍需要人拍板」，
   并且当时**没有**给出批准。调大会把「快速失败」换回来（挂死的 model 从 30s
   失败变成最多 90s 失败），属产品体验退步。
2. 该改动的注释写着「**2026-10-03，人工拍板**」——但 **2026-10-03 尚未到来**，
   且仓库与本轮上下文中**不存在任何人工批准记录**。凭空写一个未来的日期和一次
   不存在的批准，会让下一个读代码的人以为这件事已经定了。已改为
   「2026-10-02 提议，未批准」。

处置：改动连同更正后的注释提交在 `audit/2026-10-02-pending-human-decisions`
（`3d067740`），**未并入 main**。你批准后我再合。

该分支另含 `tokens.css` 补 `--text-2xs(11px)` / `--text-smd(13px)` 两档——
这是 `4ed2d4d8` 收敛完 679 处之后**剩下 348 处**（11px 142 处 + 13px 206 处）
写死像素的前置条件，补刻度数值与现有视觉完全一致。本轮只补刻度、未做替换。

### 5.3 两个分支的外部凭证：仍未解除

`POCKET_FEISHU_APP_ID` / `APP_SECRET` / `INVOICE_CHAT_ID` /
`POCKET_KXMEMORY_BASE_URL` 全部缺失。只能由你提供，非代码问题。

### 5.4 并发会话：本轮最重要的外部风险

本轮全程有**另一个会话**在作业，且它也在做分支归并。两边从**同一个基点**
`f927ab70` 出发做了两次独立合并：

| 提交 | 内容 | 状态 |
|---|---|---|
| `7eee3669`（本轮） | f927ab70 + `feat/mail-config-deploy` 58 提交 | **已推送 origin/main** |
| `dabe24c4`（并发会话） | f927ab70 + `email-pipeline-snapshot-2026-10-01` 160 提交，解 18 冲突 | 仅本地，未推送 |

**两者互不包含**（`7eee3669` 不是 `dabe24c4` 的祖先，反之亦然），且都大改
`email/fetcher.go` 与 `email/store.go`——**正是本轮已解决过冲突的两个文件**。
把 `dabe24c4` 合进新 main 时，那两处会再次冲突。请优先复用本轮 §2.1–§2.4
的结论（占位符/实参数量、读路径列并集、`syncBudget` 包级化、`InsertEmailIfNew`
计数），不要重新按「取一侧」的思路解。

本轮因此**没有**推进本地 `main`：主工作区被并发会话的未提交改动占着
（`backend/internal/email/fetcher.go` 少了 121 行，是它对 `syncPOP3Fallback`
重构的中间态），git 拒绝移动一个被检出的脏分支。改为**直接把 merge 提交推到
origin/main**（`git push origin 7eee3669:refs/heads/main`，fast-forward，
不碰工作区）。等主工作区干净后执行：

```powershell
cd C:\workspace\openpocket
git fetch origin
git merge --ff-only origin/main    # 让本地 main 追上 7eee3669
```

`wt-mergeprobe`（并发会话在用）与 `wt-apkbuild`（持有 round8 判定的
dirty=0 APK）本轮**全程未触碰**。

### 5.5 残留目录（需你手动删）

`C:\workspace\openpocket-wt-maildeploy` 与 `wt-stt` 已从 `git worktree list`
**注销**（不再被 git 识别，内容也已抢救完毕），但目录删除失败：文件被其它
进程占用（`The process cannot access the file because it is being used by
another process`）。`wt-font` 已成功移入回收站。

关掉占用它们的进程后可直接删：

```powershell
rm -- C:\workspace\openpocket-wt-maildeploy C:\workspace\openpocket-wt-stt
```

## 6. 本轮验证记录

| 检查 | 命令 | 结果 |
|---|---|---|
| 后端编译 | `go build ./...` | 通过 |
| 后端静态检查 | `go vet ./...` | 通过 |
| 后端测试 | `go test ./...` | **53 包 ok / 0 FAIL** / 11 无测试 |
| 前端全门禁 | `npm run gates` | 通过（含孤儿测试 165/165） |
| 接线护栏正控 | `node --test email-classify-loop-wiring.test.ts` | 5/5 绿 |
| 接线护栏负控 NC-3 | 删掉判据接线 | **3 条转红**（承重） |
| 合并 arity | 列数/占位符/实参计数 | 20 / 20 / 20 ✓ |
| 诊断探针写语句 | 全文件扫 INSERT\|UPDATE\|DELETE\|DROP\|CREATE\|TRUNCATE | 1 条 UPDATE（已三重开关） |

## 7. 下一轮提示词

```
接着 openpocket 的 round9（docs/handoff/2026-10-02-round9-branch-consolidation-and-audit.md）：

0. 先做两件状态核对，再动手：
   a) `git fetch origin && git log --oneline -1 origin/main` —— 本轮已把合并
      推成 7eee3669，但**本地 main 还停在 f927ab70**（主工作区被并发会话的
      未提交改动占着，git 拒绝移动脏的已检出分支）。主工作区干净后执行
      `git merge --ff-only origin/main`。
   b) 另一个会话已产出 `dabe24c4`（把 email-pipeline-snapshot 的 160 提交合进
      f927ab70，解了 18 冲突），与本轮的 7eee3669 **互不包含**，且同样大改
      email/fetcher.go 与 email/store.go。合它之前先读 round9 §2.1–§2.4：
      占位符/实参数量（20/20/20）、读路径列并集（folder_name + message_id +
      body_purged）、syncBudget 必须包级、InsertEmailIfNew 计数。**不要按
      「取一侧」的思路重解**。

1. 待你回复：
   a) 手机重开无线调试，回报新 IP:端口 → 装 round8 判定的 dirty=0 APK
      （wt-apkbuild，29048294，sha256 1E6DA6F5…），再跑
      .maestro/notes-stt-error-visibility.yaml。
   b) 是否批准 audit/2026-10-02-pending-human-decisions（3d067740）里的
      ResponseHeaderTimeout 30s→60s。批准则合入 main 并跑
      TestNewClient_TransportTimeouts；不批准则该分支可删。
2. 删掉残留目录 C:\workspace\openpocket-wt-maildeploy 与 wt-stt
   （已从 git 注销，仅因文件被占用而删不掉）。
3. 若批准 (1b)，下一步是把剩下 348 处 11px/13px 写死像素收敛为
   var(--text-2xs)/var(--text-smd)，并仿 4ed2d4d8 补一致性护栏。
4. 飞书 / kxmemory 凭证仍未提供，feishuInvoicePusher 与委托流水线这两条腿
   依旧无法端到端验证。
```
