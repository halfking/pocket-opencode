# round29 — cron 审计：并发占用取证、分支盘点、versionLess 非数字方向反转

> 日期：2026-10-03 03:46 起
> 分支：`audit/round29-cron`（基于 `origin/main` = `7e615dbc`）
> 性质：定时审计任务。**本轮没有合并到 main、没有推送 main、没有删任何分支** —— 理由见 §1。

---

## §1 头号结论：工作区当时被 3 个活跃会话占用，共享状态动作全部不能自动做

定时任务的要求里有四条会改动**共享状态**的动作：合并到 main、删分支、提交本地修改、推送 main。
开工取证发现这四条的前提已经不成立，因此本轮把它们全部降级为「报告 + 征询」，只做只读审计与隔离分支上的修复。

### 取证（不是推测，是三条独立量法互相印证）

**量法一 —— 会话清单。** `mavis session list` 显示同一工作区 `C:\workspace\openpocket` 下有 3 个
非归档、`status.type = started` 的会话：

| sessionId | 标题 | 启动时刻 | 状态 |
|---|---|---|---|
| `mvs_f912ea2c9e5e4fe88beee60a22a2acc3` | openpocket round28 审计与门禁接入 | 03:43:16 | started（**比我早 3 分钟**） |
| `mvs_b9297fbb5ad54001b4406905200e10b8` | Maestro 真机全项目测试与功能修复 | 前一日 18:30 | started |
| `mvs_a865ae9d53ee4f9998abc71bd4906c2d` | 本会话（cron） | 03:46:01 | started |

**量法二 —— 文件 mtime。** 工作区已跟踪文件的最新写入：`docs/handoff/2026-09-30-android-e2e-bug-d-e-f.md`
于 **03:42:27** 被改（我 03:46 开工时距今 5 分钟），`frontend/package.json` 03:40:54，
`scripts/verify-edge-route-reach.mjs` 03:39:05。

**量法三 —— 进程表。** 03:46:20 出现 `git` + `ssh` 进程对，03:45:55 与 03:46:19 两个 `go` 进程，
03:47 又起 3 个 `node`。这不是残留进程，是**正在跑的东西**。

### 为什么必须停手

- 主工作区有 **48 项未提交改动**（36 已跟踪 + 17 未跟踪，起始时 53 项，过程中还在变）。
  这些属于上述两个会话，不属于本轮。`git add -A` 提交它们 = 把别人的在制品当成自己的成果签收。
- 本地 `main` 在我开工后**自己往前走了一个提交**（`722cd2ad` → `c224bd89`），
  说明有人正在对 main 提交。此时再往 main 上合并/推送，是在抢同一把锁。
- round28 会话正在对 `frontend/package.json` 的 `gates` 字段做**同一处**修改（它要加
  `check:runtime-data`），而主工作区的脏 `package.json` 已经往同一行加了
  `check:callback-routes` 与 `check:edge-route-reach`。**`gates` 是 package.json 里的单行长字符串**，
  两边各自追加 ⇒ 合到一起必然冲突，且冲突点只有一行、上下文极长，人工解很费劲。
  这是本轮**预判**（尚未发生），提前记录以免下一轮以为是随机故障。

### 本轮采取的隔离

新建独立 worktree `C:\workspace\openpocket-wt-cron29`，分支 `audit/round29-cron`，
基线 `origin/main`（`7e615dbc`）。所有写入都在自己 worktree 内完成，未触碰主工作区。

---

## §2 分支盘点（24h 内、不活跃、是否已合并）

`git fetch --all --prune` 成功。注意：本机 `~/.ssh/config` 里的代理端口已从 7897 漂到 **443 / 25022**，
两者 `Test-NetConnection` 都不通；但 fetch 仍然 exit=0（实测活口仍是 **7897**）。
⇒ **端口会漂，且「config 里写的端口」与「实际能用的端口」可以不一致**，每次推送前都要重新验。

| 分支 | 落后 origin/main | 领先 | 未合入 origin 的提交 | 已并入 origin | 判定 |
|---|---|---|---|---|---|
| `audit/round26` | 2 | 0 | 0 | 是 | **已完全合入**，无内容可捞；仅落后 2 个提交。分支可删（其 worktree `openpocket-wt-r26` 的清理已由 round28 排在计划里） |
| `verify/e2e-20261002-v2` | 0 | 0 | 0 | 是 | **就是 `origin/main` 的 tip 本身**（`7e615dbc`）。已收尾，分支可删 |
| `audit/round28-gates` | 0 | 28 | 27 | 否 | **0 小时前刚建、正在被 round28 会话使用**。按任务自身「1 小时前不活跃」的口径即已排除；领先的那 27 个其实是本地 main 未推送的提交，不是它自己的产出 |
| `origin/feat/ia-notes-messages-20261003` | 73 | 3 | 3 | 否 | **唯一真正待合入的分支**，见 §3 |

按任务口径（「1 小时前所有没有合并的不活跃的子分支」）实际命中的只有前两个，两者都已 100% 合入
`origin/main` ⇒ **没有需要逐文件抢救的内容，也没有任何分支值得合入后再删**。
唯一有独立价值的 `feat/ia-notes-messages-20261003` 是 **2 小时前**、落后 73 个提交，
不在「不活跃」口径内，且是 3713 行 insertions 的在途特性分支 —— 合并它是一次独立的工程决策，
不该由一条 cron 顺手做掉。

---

## §3 `feat/ia-notes-messages-20261003` 是什么（只盘点，未合并）

相对 merge-base 38 个文件 / +3713 / −239。形态是完整的一个特性：

- 新页面：`features/messages/MessagesHubView.vue`（+694）、`features/notes/NotesHubView.vue`（+606）
- 新工具：`utils/relative-time.ts`（+95）+ 134 行用例；`study/learning-streak-view.ts`
- 9 个语言包各 +87 行（新增文案已铺满，不是只加中文）
- i18n 卡口 `check-i18n-keys.mjs` 加强 +68
- 运维：`scripts/start-local-backend.sh`（+200）、`maestro-run.mjs`（+187）

分支 tip 提交信息自述为「审计修正——3 个阻塞缺陷 + i18n 模板 key 卡口补强」，
说明作者自己已经做过一轮审计。**落后 73 个提交**，直接合大概率在 `locales/*`、
`MoreHubView.vue`、`scripts/maestro-run.mjs` 上撞车（这几个文件主工作区当前也正在改）。
建议单独开一轮处理，不要塞进 cron。

---

## §4 真正修掉的缺陷：`versionLess` 非数字分量的方向反了

### 4.1 缺陷本体

`backend/internal/server/app_version_compare.go` 的 `aok != bok` 分支：

```go
case aok != bok:
    // 一边是数字一边不是：数字版本更"新"（1.2 视为新于 1.2-rc1）。
    return aok          // ← 实际效果：a 是数字 ⇒ 返回 true ⇒ a 更旧
```

注释声明的规则是「数字分量比非数字的**新**」，而 `return aok` 实现的是「a 是数字 ⇒ a 更**旧**」。
**方向与自己的注释相反。**

### 4.2 实测（不是读代码得出的）

先写了一个只打印不判定的探针，直接问实现：

```
versionLess("1.10.0","1.x.0") = true
versionLess("1.x.0","1.10.0") = false
versionLess("1.2.0","1.2.x")   = true
versionLess("1.2.x","1.2.0")   = false
hasUpdateAvailable(current=1.10.0 build=5, latest=1.x.0 build=5) = true
```

最后一行是**生产后果**：`latest` 版本号里只要有一个脏字符（手写错一个字母、
CI 读空串再拼后缀），**全体客户端都会收到「有更新」**，去下载一个谁也解析不了的包。
不报错、不告警、nginx 正常、页面正常 —— 与 §4.97 那个 `/callback/` 缺陷是同一种难受。

### 4.3 为什么测试从没红过

配套的 `app_version_compare_test.go` 里两条用例把**反向行为**写成了期望值：

```go
{"数字分量优先于非数字", "1.10.0", "1.x.0", true},   // 名字说"数字优先"，断言却是 1.10.0 更旧
{"数字/非数字顺序必须反对称", "1.x.0", "1.10.0", false},
```

代码与用例彼此自洽 ⇒ 全绿。**错的是方向本身，而两处注释都在说反话。**
这是本轮最值得记的一条：一个用例会绿，说明不了它验证的是**对的**语义，只说明它验证的是**当前的**语义。

### 4.4 改法

- `return aok` → `return !aok`（非数字分量更旧 ⇒ 解析不了的版本**永远不会**被当成升级推出去）。
- 修正两条编码了反向行为的用例，并改掉误导性的用例名。
- 新增 `TestHasUpdateAvailable_MalformedLatestIsNeverOfferedAsUpgrade`：4 条畸形 latest
  （`1.x.0` / `1.2.x` / `x.10.0` / `1.10.beta0`）。**直接跑 `hasUpdateAvailable` 而不是 `versionLess`** ——
  判据必须落在 `handleCheckUpdate` 真正调用的那个函数上，否则把调用点换掉它照样绿。
  用例里 build 号两边取同值，把变量隔离到版本字符串；不隔离的话 `||` 的右半边会盖成 true，
  这条判据就永远绿不了也永远红不了，等于没写。

### 4.5 负控

把 `return !aok` 改回 `return aok` 重跑：**7 条子用例转红**（3 条 versionLess + 4 条 malformed-latest），
报错信息正是 §4.2 那几行。改回后恢复全绿。判据有牙齿。

### 4.6 顺带确认的一件事（结论：这一半是真修复，不是半成品）

新文件是否真的接进了生产路径：`server.go:2521` 原本是
`req.CurrentVersion < latestVersion.Version`（裸字符串比较），
现已改为调用 `hasUpdateAvailable(...)`。**调用点已接入**，
所以「1.9.0 的设备收不到 1.10.0 推送」这个原缺陷是真的被修掉了，不是只加了一个没人调用的函数。

---

## §5 对主工作区未提交改动的审计结论（只读，未改动）

### 5.1 两个新门禁 `verify-callback-routes.mjs` / `verify-edge-route-reach.mjs`：判定为**有效**

`verify-callback-routes.mjs`：凡「有 `location /api/` 且 `location /` 指向**另一个**上游」的 vhost
（= 未知路径回落前端），必须有 `/callback/` 规则，且上游必须与 `/api/` 相同。
`verify-edge-route-reach.mjs`：更进一步，要求每个这类 vhost 的反代前缀覆盖
`server.go` 注册的**全部 139 条**路由（按 Go 1.22 ServeMux 语义还原，含
`/ws` 无尾斜杠只精确匹配、`/` 兜底两条易错规则）。

**正向：** 当前树上两个脚本都 exit=0，识别出 3 个 SPA 回落型 vhost，前缀均为
`= /healthz /api/ /ws /plugin/ws /callback/ /`。

**负控：** 把 3 份 conf 拷到临时目录、删掉 `/callback/` 整块后重跑 ——
3 个 vhost 全部报 `/callback/ -> （缺失）`，**exit=1**；不删则 exit=0。判据有牙齿。

两个脚本都写了防空跑断言（扫不到文件、提取不到 100+ 条路由、连 `/api/tasks` 都没提到 ⇒ exit=3/4 拒给结论），
并且注释里记着自己第一版因漏排除 `location /` 导致负控全绿的过程。**质量高于本仓平均水平，建议入库。**

### 5.2 `deploy/edge/*.conf` 补 `/callback/` 规则：判定为**正确**

3 份 conf 各补一条 `location /callback/`，`proxy_pass` 指向与 `/api/` 相同的后端上游（非 4175 前端），
并带 `X-Pocket-Upstream` 便于排查。与 §5.1 的门禁互为验证。

### 5.3 `frontend/src/features/more/hubItems.ts`：判定为**正常**

`applyCapabilityGates` 把「更多」页 9 宫格里 Android 上永远打不开的 `/vault` 入口门控掉，
并明确「`null`（首屏还没探完）按不可用处理，先无后有」。注释里解释了为什么不用
`featureFlags.security.keystore_v1`（那是静态开关，把关后本地哪天再打开就假装修好了）。

> **一个假警报，记录下来免得下次重复查：** 我第一次用 PowerShell `Get-Content` 读它，满屏乱码
> （`銆屾洿澶...`），看起来像文件已损坏。用 `[System.IO.File]::ReadAllBytes` + 显式 UTF-8 解码复核：
> 首 3 字节 `2F 2A 2A`（即 `/**`），无 BOM，**内容完全正常**。
> 乱码来自 PowerShell 5.1 用系统 ANSI 码页（GBK）解码 UTF-8 源文件，**是显示层假象，不是文件缺陷**。
> 同一个坑在 `route-coverage-sweep.mjs` 上又出现一次。**判断中文源文件是否损坏，必须验字节，不能凭 `Get-Content` 的输出。**

### 5.4 `scripts/_patch-unlock.mjs`：判定为**垃圾文件，不应入库**

**0 字节**，全仓无任何引用（`scripts/*.mjs`、`frontend/package.json`、`frontend/scripts/*.mjs` 均无命中）。
建议直接丢弃，不要 `git add -A` 顺手带进去。

### 5.5 `scripts/route-coverage-sweep.mjs`(25KB) / `route-usage-crossref.mjs`(50KB)

内容是真实资产：前者把 139 条路由在真跑实例上逐条打一遍做可用性分类（补上了「63 条页面巡检 +
20 个端点矩阵」都没覆盖的 100 来条）；后者做「后端注册路由 ↔ 前端实际调用点」对账，
其注释里就记着一个实证例子：`/api/assets/sync` 后端已注册、Store 有 `listDirty`、Api 客户端也写了，
**唯独没有任何调用方**，而 types 全绿 —— 「死代码但编译通过」的那一类。
两者合计 75KB，属入库候选而非垃圾，但建议连同 §5.1 的门禁一起在专门一轮里入库并接门禁，
不要混在 cron 里。

---

## §6 测试命令与结果

基线与修复后各跑一遍，**同样的命令、同样的环境**，用来区分「本轮引入的回归」与「既有问题」。

环境：`POCKET_TEST_POSTGRES_DSN=postgresql://postgres@127.0.0.1:5432/postgres?sslmode=disable`
（**必须设**：不设则 PG 集成测试静默 skip，「全绿」是假的 —— round24 §24 已吃过这个亏）
worktree：`C:\workspace\openpocket-wt-cron29`

| # | 命令 | 基线 `origin/main`=7e615dbc | 本分支（含 §4 修复） |
|---|---|---|---|
| 1 | `go build ./...` | **exit=0** | **exit=0** |
| 2 | `go test ./... -count=1 -timeout 25m` | **exit=0，无 FAIL** | **exit=0，无 FAIL** |
| 3 | `go test ./internal/server/ -run 'TestVersionLess\|TestHasUpdateAvailable'` | （文件不存在） | **全绿，31 条子用例 PASS** |
| 4 | 同上，**负控**（实现退回 `return aok`） | — | **7 条子用例 FAIL** |
| 5 | `node scripts/verify-callback-routes.mjs` | — | **exit=0** |
| 6 | 同上，**负控**（删掉 `/callback/` 块） | — | **exit=1，3 个 vhost 报缺失** |
| 7 | `node scripts/verify-edge-route-reach.mjs` | — | **exit=0，139 条路由全覆盖** |

前端 `npm run gates` **本轮未跑**：隔离 worktree 里没有 `frontend/node_modules`，
而主工作区正在被两个会话改动（跑它会读到别人在制品的结果，既不可信又会污染共享目录）。
**这是本轮明确的未覆盖面，不是「通过了」。**

---

## §7 遗留风险

1. **共享状态动作全部未执行**（合并 main / 删分支 / 推 main）。定时任务要求的这四条需要人在场决策。
2. **主工作区 48 项未提交改动仍然只存在于工作区**，没有 commit、没有 stash 兜底。
   本仓有 `git stash -u` 卷走在制品的前科（2026-10-01 04:04，11 个新建文件全没）。
   任何一次并发会话的 `stash -u` / `checkout .` 都会真丢。
3. **`gates` 单行冲突已预判但未发生**：round28 的 `check:runtime-data` 与主工作区的
   `check:callback-routes` / `check:edge-route-reach` 会撞在同一行。
4. **`feat/ia-notes-messages-20261003` 未合并**，落后 73 个提交，合并窗口只会继续变差。
5. **前端门禁未验证**（见 §6 第 7 条）。
6. 本轮修复的基座文件（`app_version_compare.go` / `_test.go`）**原本是另一个会话的未提交产物**。
   本分支把它们连同修复一起提交，等于给那部分工作做了一次侧分支备份（原文件仍在主工作区，未被改动）。
   若那个会话随后自行提交，两个版本会在合并时冲突（差异只有 §4 那几行）。

---

## §8 下一轮提示词

> 接着 round29 往下做，先读
> `docs/handoff/2026-10-03-round29-cron-audit-concurrency-and-version-compare.md` 的 §1 与 §7。
>
> 1. **先查并发**：`mavis session list` 过滤非归档 + `status.type=started` 且
>    `workspaceDir` 是 `C:\workspace\openpocket` 的会话；再看主工作区已跟踪文件 mtime 与进程表。
>    round29 实测：3 个会话同时在跑，其中一个比 cron 早 3 分钟启动。
>    **只要还有别人在写，就不要合并 main、不要删分支、不要 `git add -A` 提交主工作区。**
> 2. **合并 `origin/feat/ia-notes-messages-20261003`**（round29 §3 已盘点：38 文件 / +3713 / −239，
>    落后 73 个提交）。预期冲突点：`locales/*`、`MoreHubView.vue`、`scripts/maestro-run.mjs`。
>    这是特性合并，值得单独一轮，不要塞进 cron。
> 3. **处理 `gates` 单行冲突**（round29 §1 已预判）：建议把 `gates` 从单行长字符串改成
>    数组 + 循环执行，或至少让每个 check 独立成行，否则每加一个门禁都要手改这一行。
> 4. **入库 `verify-callback-routes.mjs` / `verify-edge-route-reach.mjs`**（round29 §5.1 已负控坐实有牙齿），
>    接进 `npm run gates`；一并处置 `route-coverage-sweep.mjs` / `route-usage-crossref.mjs`。
>    **丢弃 `scripts/_patch-unlock.mjs`**（0 字节、无引用，round29 §5.4）。
> 5. **在有 `frontend/node_modules` 的工作区跑一次完整 `npm run gates`**，补上 round29 §6 第 7 条的未覆盖面。
> 6. 推送前 `Test-NetConnection 127.0.0.1 -p 7897` 验活代理（round29 实测：ssh config 里写的是
>    443/25022 且都不通，但 fetch 仍成功 —— **别照抄 config 里的端口**），
>    大改动加 `GIT_SSH_COMMAND='ssh -o ServerAliveInterval=15 -o ServerAliveCountMax=20'`。
> 7. 读本仓任何中文源文件是否损坏，**必须验字节**（`ReadAllBytes` + 显式 UTF-8），
>    不要凭 PowerShell `Get-Content` 的输出下结论 —— round29 §5.3 踩过一次假警报。
