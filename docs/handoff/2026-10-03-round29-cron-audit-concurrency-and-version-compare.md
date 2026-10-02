# round29 — cron 审计：并发占用取证、分支盘点、versionLess 非数字方向反转

> 日期：2026-10-03 03:46 – 04:30
> 分支：`main`（推送自本地 main，28 个未发布提交 + 53 项工作区改动）
> 侧分支备份：`audit/round29-cron`（`ea336c77`，基于 `origin/main` = `7e615dbc`）

---

## §1 头号结论：开工时工作区被 3 个活跃会话占用

定时任务要求里有四条会改动**共享状态**的动作：合并到 main、删分支、提交本地修改、推送 main。
开工取证发现这四条的前提已经不成立，因此前 30 分钟只做只读审计与隔离分支上的修复，
共享状态动作全部**先报告、后征询用户**，拿到明确授权后才执行。

### 取证（三条独立量法互相印证，不是推测）

**量法一 —— 会话清单。** `mavis session list` 显示同一工作区 `C:\workspace\openpocket` 下有 3 个
非归档、`status.type = started` 的会话：

| sessionId | 标题 | 启动时刻 |
|---|---|---|
| `mvs_f912ea2c9e5e4fe88beee60a22a2acc3` | openpocket round28 审计与门禁接入 | 03:43:16（**比 cron 早 3 分钟**） |
| `mvs_b9297fbb5ad54001b4406905200e10b8` | Maestro 真机全项目测试与功能修复 | 前一日 18:30 |
| `mvs_a865ae9d53ee4f9998abc71bd4906c2d` | 本会话（cron） | 03:46:01 |

**量法二 —— 文件 mtime。** 03:46 开工时：handoff 主文档 03:42:27（距今 5 分钟）、
`frontend/package.json` 03:40:54、`scripts/verify-edge-route-reach.mjs` 03:39:05。
到 04:16 再测，最新写入已推进到 **04:11:45**（`_part-4.100.md`、`apk_download_test.go`、
`config.go` 等陆续出现）—— **写者全程没停**。

**量法三 —— 进程表。** 03:46:20 出现 `git` + `ssh` 进程对，03:45:55 与 03:46:19 两个 `go` 进程，
03:47 又起 3 个 `node`。不是残留进程。

**量法四（补充，04:16）** —— 工作区改动数从 48 涨到 53，新增
`apk_download_test.go`、`_part-4.99.md`、`_part-4.100.md`、`sweep-api-readonly.mjs`，
且 `backend/internal/config/config.go` 变成已修改。**这一轮提交是「在制品快照」，不是「已验收成果」。**

### 一个自己踩的坑（记录下来）

统计未跟踪文件时我写了 `git status --porcelain | Where-Object { $_ -like '??*' }`。
PowerShell 的 `-like` 里 `?` 是**通配符**（匹配任意单字符），不是字面量问号，
于是这条过滤匹配了**全部 53 行**，`config.go` 一度看起来像「核心文件未被跟踪」的危机。
正确写法是 `$_.Substring(0,2) -eq '??'` 或 `-like '`??*'`（反引号转义）。
**差点把一次显示层假象当成 P0 事故。**

---

## §2 分支盘点（24h 内、不活跃、是否已合并）

`git fetch --all --prune` exit=0。注意：本机 `~/.ssh/config` 里的代理端口已从 7897 漂到
**443 / 25022**，两者 `Test-NetConnection` 都不通；但 fetch 与 push 仍然成功（实测活口仍是 **7897**）。
⇒ **端口会漂，且「config 里写的端口」与「实际能用的端口」可以不一致**，每次推送前都要重新验。

| 分支 | 落后 origin/main | 领先 | 未合入 origin | 已并入 origin | 判定 |
|---|---|---|---|---|---|
| `audit/round26` | 2 | 0 | 0 | 是 | 已完全合入，无内容可捞。**已删分支，保留 worktree 目录** |
| `verify/e2e-20261002-v2` | 0 | 0 | 0 | 是 | 就是 `origin/main` 的 tip 本身。**已删分支** |
| `audit/round28-gates` | 0 | 28 | 27 | 否 | 0 小时前刚建、正在被 round28 会话使用。按任务自身「1 小时前不活跃」口径即已排除。领先的那 27 个其实是本地 main 未推送的提交 |
| `origin/feat/ia-notes-messages-20261003` | 73 | 3 | 3 | 否 | 唯一真正待合入的分支，见 §3。**本轮不处理** |

按任务口径实际命中的两个分支都已 100% 合入 `origin/main` ⇒ **没有需要逐文件抢救的内容**。

### 已预判但尚未发生的冲突：`gates` 单行

round28 会话要往 `frontend/package.json` 的 `gates` 加 `check:runtime-data`；
主工作区的脏 `package.json` 已经往**同一行**加了 `check:callback-routes` 与
`check:edge-route-reach`。`gates` 是 package.json 里的**单行长字符串**，两边各自追加
⇒ 合到一起必然冲突，且冲突点只有一行、上下文极长。
建议把 `gates` 改成数组 + 循环执行，否则每加一个门禁都要手改这一行。

---

## §3 `feat/ia-notes-messages-20261003`：只盘点，本轮不合并

相对 merge-base 38 个文件 / +3713 / −239，是完整的一个特性：

- 新页面：`features/messages/MessagesHubView.vue`（+694）、`features/notes/NotesHubView.vue`（+606）
- 新工具：`utils/relative-time.ts`（+95）+ 134 行用例；`study/learning-streak-view.ts`
- 9 个语言包各 +87 行（新增文案已铺满，不是只加中文）
- i18n 卡口 `check-i18n-keys.mjs` +68；`maestro-run.mjs` +187；`start-local-backend.sh` +200

分支 tip 自述「审计修正——3 个阻塞缺陷 + i18n 模板 key 卡口补强」，作者自己已做过一轮审计。
**落后 73 个提交**，直接合大概率在 `locales/*`、`MoreHubView.vue`、`scripts/maestro-run.mjs`
撞车（这几个文件主工作区当时也正在改）。**已征询用户，决定留给单独一轮。**

---

## §4 真正修掉的缺陷：`versionLess` 非数字分量的方向反了

### 4.1 缺陷本体

`backend/internal/server/app_version_compare.go` 的 `aok != bok` 分支：

```go
case aok != bok:
    // 一边是数字一边不是：数字版本更"新"（1.2 视为新于 1.2-rc1）。
    return aok          // ← 实际效果：a 是数字 ⇒ 返回 true ⇒ a 更旧
```

注释声明「数字分量比非数字的**新**」，而 `return aok` 实现的是「a 是数字 ⇒ a 更**旧**」。
**方向与自己的注释相反。**

### 4.2 实测（探针只打印不判定，不预设结论）

```
versionLess("1.10.0","1.x.0")  = true
versionLess("1.x.0","1.10.0")  = false
versionLess("1.2.0","1.2.x")    = true
versionLess("1.2.x","1.2.0")    = false
hasUpdateAvailable("1.10.0", 5, "1.x.0", 5) = true
```

最后一行是**生产后果**：`latest` 版本号里只要有一个脏字符（手写错一个字母、
CI 读空串再拼后缀），**全体客户端都会收到「有更新」**，去下载一个谁也解析不了的包。
不报错、不告警、nginx 正常、页面正常 —— 与 §4.97 那个 `/callback/` 缺陷是同一种难受。

### 4.3 为什么测试从没红过

配套用例把**反向行为**写成了期望值：

```go
{"数字分量优先于非数字", "1.10.0", "1.x.0", true},   // 名字说"数字优先"，断言却是 1.10.0 更旧
{"数字/非数字顺序必须反对称", "1.x.0", "1.10.0", false},
```

代码与用例彼此自洽 ⇒ 全绿。**错的是方向本身，而两处注释都在说反话。**
本轮最值得记的一条：**用例会绿，说明不了它验证的是对的语义，只说明它验证的是当前的语义。**

### 4.4 改法

- `return aok` → `return !aok`：非数字分量更旧 ⇒ 解析不了的版本**永远不会**被当成升级推出去。
- 修正两条编码了反向行为的用例，改掉误导性的用例名。
- 新增 `TestHasUpdateAvailable_MalformedLatestIsNeverOfferedAsUpgrade`（4 条畸形 latest：
  `1.x.0` / `1.2.x` / `x.10.0` / `1.10.beta0`）。**直接跑 `hasUpdateAvailable` 而非 `versionLess`** ——
  判据必须落在 `handleCheckUpdate` 真正调用的那个函数上。
  用例里 build 号两边取同值以隔离变量；不隔离的话 `||` 的右半边会盖成 true，
  这条判据就永远绿不了也永远红不了，等于没写。

### 4.5 负控

把 `return !aok` 改回 `return aok` 重跑：**7 条子用例转红**（3 条 versionLess + 4 条 malformed-latest），
报错信息正是 §4.2 那几行。改回后恢复全绿。**判据有牙齿。**

### 4.6 顺带确认：这一半是真修复，不是半成品

`server.go:2521` 原本是裸字符串比较 `req.CurrentVersion < latestVersion.Version`，
现已接入 `hasUpdateAvailable(...)`。**调用点确实被接上**，
所以「1.9.0 的设备收不到 1.10.0 推送」是真的被修掉了，不是加了个没人调用的函数。

---

## §5 对工作区在制品改动的审计结论

### 5.1 两个新门禁 `verify-callback-routes.mjs` / `verify-edge-route-reach.mjs`：判定**有效**

`verify-callback-routes.mjs`：凡「有 `location /api/` 且 `location /` 指向**另一个**上游」的 vhost
（= 未知路径回落前端），必须有 `/callback/` 规则，且上游必须与 `/api/` 相同。
`verify-edge-route-reach.mjs`：要求每个这类 vhost 的反代前缀覆盖 `server.go` 注册的**全部 139 条**路由
（按 Go 1.22 ServeMux 语义还原，含 `/ws` 无尾斜杠只精确匹配、`/` 兜底两条易错规则）。

| | 结果 |
|---|---|
| 正向（当前树） | 两个脚本均 **exit=0**，识别出 3 个 SPA 回落型 vhost，前缀均为 `= /healthz /api/ /ws /plugin/ws /callback/ /` |
| 负控（拷 3 份 conf 到临时目录、删掉 `/callback/` 整块） | 3 个 vhost 全部报 `/callback/ -> （缺失）`，**exit=1**；不删则 exit=0 |

两个脚本都写了防空跑断言（扫不到文件、提取不到 100+ 条路由、连 `/api/tasks` 都没提到 ⇒ exit=3/4 拒给结论），
注释里还记着自己第一版因漏排除 `location /` 导致负控全绿的过程。**质量高于本仓平均水平。**

### 5.2 `deploy/edge/*.conf` 补 `/callback/` 规则：判定**正确**

3 份 conf 各补一条 `location /callback/`，`proxy_pass` 指向与 `/api/` 相同的后端上游
（**不是 4175 的前端** —— 写错上游不会被 nginx 拦下，只会静默返回 HTML），并带 `X-Pocket-Upstream` 便于排查。

### 5.3 `frontend/src/features/more/hubItems.ts`：判定**正常**

`applyCapabilityGates` 把「更多」页 9 宫格里 Android 上永远打不开的 `/vault` 入口门控掉，
并明确「`null`（首屏还没探完）按不可用处理，先无后有」。
注释解释了为什么不用 `featureFlags.security.keystore_v1`（那是静态开关，
把关后本地哪天再打开就假装修好了）。

> **一个假警报，记录下来免得下次重复查：** 第一次用 PowerShell `Get-Content` 读它，满屏乱码
> （`銆屾洿澶...`），看起来像文件已损坏。用 `[System.IO.File]::ReadAllBytes` + 显式 UTF-8 解码复核：
> 首 3 字节 `2F 2A 2A`（即 `/**`），无 BOM，**内容完全正常**。
> 乱码来自 PowerShell 5.1 用系统 ANSI 码页（GBK）解码 UTF-8 源文件，**是显示层假象，不是文件缺陷**。
> 同一个坑在 `route-coverage-sweep.mjs` 上又出现一次。
> **判断中文源文件是否损坏，必须验字节，不能凭 `Get-Content` 的输出。**

### 5.4 `scripts/_patch-unlock.mjs`：判定**垃圾文件，已排除在本次提交之外**

**0 字节**，全仓无任何引用（`scripts/*.mjs`、`frontend/package.json`、`frontend/scripts/*.mjs` 均无命中）。
是某次一次性 patch 尝试的残留。**文件仍留在磁盘上，未删除**，需要的话可以单独处置。

### 5.5 `route-coverage-sweep.mjs`(25KB) / `route-usage-crossref.mjs`(50KB)

内容是真实资产：前者把 139 条路由在真跑实例上逐条打一遍做可用性分类
（补上了「63 条页面巡检 + 20 个端点矩阵」都没覆盖的 100 来条）；
后者做「后端注册路由 ↔ 前端实际调用点」对账，其注释里记着一个实证例子：
`/api/assets/sync` 后端已注册、Store 有 `listDirty`、Api 客户端也写了，
**唯独没有任何调用方**，而 types 全绿 —— 正是「死代码但编译通过」那一类。

---

## §6 测试命令与结果

环境：`POCKET_TEST_POSTGRES_DSN=postgresql://postgres@127.0.0.1:5432/postgres?sslmode=disable`
（**必须设**：不设则 PG 集成测试静默 skip，「全绿」是假的 —— round24 §24 已吃过这个亏）

| # | 命令 | `origin/main`=7e615dbc 基线 | 含 §4 修复 |
|---|---|---|---|
| 1 | `go build ./...` | exit=0 | exit=0 |
| 2 | `go test ./... -count=1 -timeout 25m` | exit=0，无 FAIL | 见 §9 |
| 3 | `go test ./internal/server/ -run 'TestVersionLess\|TestHasUpdateAvailable'` | （文件不存在） | 31 条子用例 PASS |
| 4 | 同上，**负控**（退回 `return aok`） | — | **7 条子用例 FAIL** |
| 5 | `node scripts/verify-callback-routes.mjs` | — | exit=0 |
| 6 | 同上，**负控**（删 `/callback/` 块） | — | **exit=1，3 个 vhost 报缺失** |
| 7 | `node scripts/verify-edge-route-reach.mjs` | — | exit=0，139 条路由全覆盖 |

前端 `npm run gates` 的覆盖情况见 §9。

---

## §7 本轮实际执行的动作

1. 新建隔离 worktree `C:\workspace\openpocket-wt-cron29` / 分支 `audit/round29-cron`，
   基于 `origin/main`（`7e615dbc`），提交 `ea336c77` 并推送成功（`[new branch]`，exit=0）。
   —— 这是对「§4 修复 + round27/round28 侧分支上尚未提交的 `app_version_compare.go`」的一次备份。
2. 把 §4 的修复**同步回主工作区**的 `app_version_compare.go` / `_test.go`。
   **这一步是必须的**：主工作区里那份是**未修复版**，若照原样提交推送，等于把已查实的缺陷推上 main。
3. 征询用户后执行：提交并推送本地 main 的 28 个未发布提交 + 53 项工作区改动（排除 0 字节的
   `scripts/_patch-unlock.mjs`）。
4. 删除 `audit/round26`、`verify/e2e-20261002-v2` 两个分支（均已 100% 合入 origin/main），
   **保留 `openpocket-wt-r26` worktree 目录**（避免与 round28 会话的清理计划撞车）。
5. `feat/ia-notes-messages-20261003` 不合并。

---

## §8 遗留风险

1. **本次推送是「在制品快照」，不是「已验收成果」。** 提交时另一个会话仍在写文件
   （最新写入 04:11:45），因此可能捕获到**写到一半的状态**。后端 `go build` + `go test ./...`
   在提交前于主工作区跑过并作为门槛，但**前端未跑完整 gates**（见 §9）。
2. **提交信息无法区分作者。** 这 53 项改动来自至少两个不同会话，
   提交信息里只能如实标注「来自并发会话的在制品」，无法追溯到具体作者。
3. **`gates` 单行冲突已预判但未发生**（§2 末）。本轮推送的 `package.json` 已含
   `check:callback-routes` / `check:edge-route-reach`，round28 的 `check:runtime-data` 合进来必冲突。
4. **`feat/ia-notes-messages-20261003` 仍未合并**，落后 73 个提交，合并窗口只会继续变差。
5. **工作区改动没有任何 stash 兜底。** 本仓有 `git stash -u` 卷走在制品的前科
   （2026-10-01 04:04，11 个新建文件全没）。本轮提交后该风险大幅下降但未归零。

---

## §9 下一轮提示词

> 接着 round29 往下做，先读
> `docs/handoff/2026-10-03-round29-cron-audit-concurrency-and-version-compare.md` 的 §1、§2 末、§8。
>
> 1. **先查并发**：`mavis session list` 过滤非归档 + `status.type=started` 且
>    `workspaceDir` 是 `C:\workspace\openpocket` 的会话；再看已跟踪文件 mtime 与进程表。
>    round29 实测：3 个会话同时在跑，其中一个比 cron 早 3 分钟启动，且**写者全程没停**。
>    有别人在写就不要提交主工作区 —— 先建隔离 worktree。
> 2. **验证上一轮推上去的东西**：round29 提交的是在制品快照（§8 第 1 条）。
>    本轮第一件事是在有 `frontend/node_modules` 的工作区跑一次完整 `npm run gates`，
>    把 round29 欠下的前端覆盖补上。若发现快照是断的，优先修它而不是开新特性。
> 3. **处理 `gates` 单行冲突**（§2 末已预判）：把 `gates` 从单行长字符串改成数组 + 循环执行，
>    否则每加一个门禁都要手改这一行，且必然与 round28 的 `check:runtime-data` 撞车。
> 4. **合并 `origin/feat/ia-notes-messages-20261003`**（§3 已盘点：38 文件 / +3713 / −239，
>    当时落后 73 个提交）。预期冲突点：`locales/*`、`MoreHubView.vue`、`scripts/maestro-run.mjs`。
>    这是特性合并，值得单独一轮，不要塞进 cron。
> 5. **入库并接门禁**：`verify-callback-routes.mjs` / `verify-edge-route-reach.mjs`
>    （§5.1 已负控坐实有牙齿）、`route-coverage-sweep.mjs` / `route-usage-crossref.mjs`。
>    **处置 `scripts/_patch-unlock.mjs`**（0 字节、无引用，round29 已排除在提交外，文件仍在磁盘上）。
> 6. 推送前 `Test-NetConnection 127.0.0.1 -p 7897` 验活代理
>    （round29 实测：ssh config 里写的是 443/25022 且都不通，但 push 仍成功 —— **别照抄 config 里的端口**），
>    大改动加 `GIT_SSH_COMMAND='ssh -o ServerAliveInterval=15 -o ServerAliveCountMax=20'`。
> 7. 读本仓任何中文源文件是否损坏，**必须验字节**（`ReadAllBytes` + 显式 UTF-8），
>    不要凭 PowerShell `Get-Content` 的输出下结论 —— round29 §5.3 踩过一次假警报。
> 8. 用 PowerShell 过滤 `git status --porcelain` 输出时**不要用 `-like '??*'`**：
>    `?` 是通配符会匹配全部行。round29 §1 因此差点把 `config.go` 误报成 P0。
