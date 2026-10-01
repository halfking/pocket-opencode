# 2026-10-01 审计轮次 rd8 —— 分支归并、合并修正与丢失工作回收

> 承接 §7i（共享工作树事故）。本轮的核心发现不是「新写的代码有 bug」，
> 而是**上一轮宣称完成的工作其实从未落盘**，以及**合并本身引入了编译错误**。

## 0. 一句话结论

三个快照分支合入主线时，`pipeline.go` 里 `splitReminderCandidates` 被**重复定义**，
`go build` 直接失败；同时一次真实的 STT 修复（无 PG 部署下语音转写设置完全不可用）
只存在于 stash 里、从未进过 main。两件都已修复并补上守护测试。

## 1. 合并修正

### 1.1 BUG（合并引入）：`splitReminderCandidates` 重复定义 → 编译失败

`email-pipeline-snapshot-2026-10-01` 合入 main 后 `go build ./...` 报：

```
internal/email/pipeline.go:742:6: splitReminderCandidates redeclared in this block
internal/email/pipeline.go:715:6: other declaration of splitReminderCandidates
```

两个副本**逐字节相同**（25 行），且第二个副本把自己的 doc comment 写成了
`// notifyImportant 对未提醒过的重要邮件派发通知并记录时间。`——也就是说
`notifyImportant` 的文档注释被挤到了 `splitReminderCandidates` 上面，
godoc 会把这个函数描述成「派发通知」，实际它只是拆分候选。

**为什么没被 git 拦住**：该函数在合并前就已经存在于两侧，git 判定为
「两侧都未改动」而直接保留双方内容，冲突标记都不会出现。**git 的无冲突
不等于语义无冲突**——这是本轮最值得记的一条。

处置：删掉重复副本，恢复 `notifyImportant` 的文档注释。

### 1.2 合并冲突 7 处的逐条处置

| 文件 | 冲突 | 处置 | 依据 |
|---|---|---|---|
| `pipeline.go` | 注释措辞 + gofmt 对齐 | 取快照分支（更完整） | 纯注释 |
| handoff 文档 §7i | 6 次 vs 10 次 stash 事故 | 取快照分支（严格超集） | 文档 |
| `package.json` | `^5.11.2` vs `5.11.2` | 取 main 的 caret | 分支的精确 pin 会挡住补丁升级 |
| `api-base.ts` | nginx 哨兵注释 | 取 main | 注释解释了与 nginx.conf 的字面一致性 |
| `database-detect.sh` | nc 平台标志 | 取分支的 Darwin `-G` / Linux `-w`，**丢弃**其 `else` 重复探测 | `/dev/tcp` 已在上方先行，分支的 else 是冗余 |
| `deploy-local.sh` ×3 | `--stop-frontend` 入口 / `--inventory` 容错 / 网关域名 | 取分支入口、**保留** main 的 inventory 容错、**保留** main 的 `llm.kxpms.cn` | 见下 |
| `package-lock.json` ×9 | npmmirror vs npmjs 源 | 全取 main | 本机走国内镜像 |

**网关域名这一处值得单说**：分支写 `llmgo.kxpms.cn`，main 写 `llm.kxpms.cn`。
直觉上「分支更新、应该取分支」是错的——

- `backend/internal/server/llm_gateway_defaults_test.go:13` 断言
  `obsoleteLocalGatewayURL("https://llm.kxpms.cn/v1")` 必须为 **false**；
- `backend/internal/opencode/config_writer.go:11-14` 写明 2026-09-30
  「切回 `llm.kxpms.cn`（用户指定的正式网关）」，此前 09-21 才切到 llmgo。

即 llmgo 是**旧**域，main 是 09-30 的回退。取分支会把已修复的 obsolete 判定
重新打开（设置页填 llm.kxpms.cn 会被打回 llmgo）。

## 2. BUG-AS：STT 在无 PG 部署下完全不可用

### 2.1 根因

`PUT /api/stt/config` 在 `s.userSettings == nil` 时直接
`return fmt.Errorf("user settings store unavailable")` → HTTP 400。
而 pocketd 在没有 `POCKET_POSTGRES_DSN` 时会**正常启动**（remote-only 模式），
此时 `s.userSettings` 恒为 nil。于是这种部署下整个语音转写设置**保存不了**。

### 2.2 为什么 10 个 STT 测试全绿却没抓到

`sttTestServer` 这个构造器**总是**执行 `srv.userSettings = newMemUserSettings()`。
所有 STT 测试都经它构造，于是「有 PG」成了单测的隐含前提，
恰好掩盖了生产里的 nil 路径。**测试构造器与生产初始化路径不一致 = 盲区。**

### 2.3 修复

补一份进程内兜底存储（`sttMemSettings`，懒初始化 + `sync.Mutex`）：
有 PG 走 PG，没有走内存。重启后回默认值——这与「无 PG 部署本身就不持久」
的既有事实一致，**不制造虚假持久化预期**（对比 §7j 里密码明文常量那类
「假装能用」的修法）。

### 2.4 负控实测（关键）

新测试 `TestSttConfigWorksWithoutPostgres` 显式把 `userSettings` 置 nil。
仅看绿灯不算数，故做对照：临时加一个 `OPP_NEGCTL=1` 开关把行为改回
「无 PG 直接 400」，测试如期转红：

```
--- FAIL: TestSttConfigWorksWithoutPostgres
    无 PG 部署下 PUT 应可用，实际 status=400 body={"error":"user settings store unavailable"}
```

去掉开关后转绿。**绿灯 = 修复生效；红→绿 = 测试确实在守这条线。**
负控脚手架已删除，未入库。

## 3. 分支审计

24 小时窗口内、超过 1 小时未活动的分支：

| 分支 | 独有提交 | 处置 |
|---|---|---|
| `email-pipeline-snapshot-2026-10-01` | 0（8 个已并入） | 已合入 main |
| `audit-snapshot-rd7` | 0（`git cherry` 显示 `-`，patch 已等价存在） | 已合入 main |
| `feat/2026-10-01-stt-service` | 0 | 已合入 main |
| `fix/bugz-marketplace-conflict` | 0 | 已合入 main |
| `origin/codex/platform-goal-20260930` | **5** | **合入 main**（见 §4） |
| 其余 10 个远端分支 | 0 | 无需处置 |

四个本地分支的 `git cherry main <branch>` 全部返回 `-`，即内容已通过
其他路径进入 main。**未删除任何分支**——它们是并发会话的工作区锚点
（`wt3` / `wt-stt` 两个 worktree 仍绑定其中两个），删掉会让那些会话的
HEAD 悬空。

## 4. 合入 origin/codex/platform-goal-20260930（部署加固，5 提交）

- `check-databases.py`（242 行新增）：拒绝重复建库、拒绝重复 provision、
  拒绝 `schema` 拼进 SQL（注入）、表/序列权限缺失即失败。
- 启动流程等 PostgreSQL TCP **就绪**（原先只等进程起来）。
- `--stop-frontend` 只停**已核验**的前端实例（原先用 compose down 误伤）。
- 支持纯前端部署（nginx `/healthz` 返回 `frontend ok` 哨兵）。

测试：`database_preflight_test.py` 24/24 通过；`test_frontend_stop.py` 5/5
（需把 Git Bash 放进 PATH，否则 `subprocess` 找不到 `bash`）。

## 5. 遗留问题（未修，已定性）

### 5.1 `test_database_detect.sh` 5 个失败 —— 环境限定，非本轮引入

在**未经改动的分支原样检出**上跑，同样 5 个失败（`PASS: 4 FAIL: 5`），
所以与本轮合并无关。根因是 Windows 平台限制：

```
_db_probe()  →  python3 -c 'subprocess.run([...])'
桩脚本 pg_isready 带 #!/usr/bin/env bash shebang
→ WinError 193 %1 不是有效的 Win32 应用程序
```

`bash` 直接执行该桩正常，只有**经 python3 subprocess 转发**才失败。
Linux CI 上无此问题。**不要在 Windows 上把这个测试的红色当成代码回归。**

### 5.2 仍存在 stash 中的工作（本轮只回收了 STT 那项）

- `DocumentPlugin.java`（238 行，`@CapacitorPlugin(name="Document")`）：
  main 有 TS 契约 `frontend/src/native/document.ts`，但 Android 侧
  `MainActivity.java` 从未注册该插件，**Android 上打开 PDF 目前不可用**。
- `frontend/src/api/vault.ts`（77 行）：main 无任何引用，且 main 刚在
  05aec82 删除了零导入的 vaultApi 门面。**倾向判定为死代码，不回收。**

## 6. 共享工作树再次被并发写入

10:12 有并发会话在本工作树新建了
`frontend/src/features/email/email-cache-heal.ts` 及其测试
（mtime 距我检查仅 2 秒）。本轮**只 `git add` 自己的三个文件**，
未使用 `git add -A`，未触碰对方产物。

> 教训重申：§7i 记的「快照会被污染」在这里表现为「提交会夹带」。
> 在共享工作树里 `git add -A` 等于替别人提交半成品。
