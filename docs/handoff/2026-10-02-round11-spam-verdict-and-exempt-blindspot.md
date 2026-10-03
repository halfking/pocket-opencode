# round11：退订口径人工拍板落地 + 护栏豁免盲区的负控

日期：2026-10-02
分支：main（无新分支）
上一轮：round10（`b54a1ad8`）

## 0. 本轮最重要的一条：**「护栏豁免」不等于「护栏在看着」**

接手时 `email-pipeline-snapshot-2026-10-01`（161 提交）已被另一个并发会话
合并进 main，分支已删。我按惯例准备重做合并，到 `git merge-base` 才发现在
`git worktree list` 里的合并 worktree 已经消失、主工作区 HEAD 前进到
`ba8cf8c2`（比 `origin/main` 多 5 个提交，其中 `7c7e60da` 提交信息直接写着
「合并已执行并全绿」）。

**教训**：动大动作前先 `git worktree list` + `git log --oneline -5`，
不要凭上一轮的印象就开工。重复做一遍别人的活不只是浪费时间——
会在对方的成果上叠加第二批冲突解法，把本来干净的合并搅浑。

核实结果：合并质量是好的，而且比我打算做的更好（见 §2）。

## 1. 人工拍板：退订特征的判定口径

`LooksLikeSpam` 对「退订 / 取消订阅 / 拒收」的处理，两侧规则**直接矛盾**，
且两侧都有真实样本撑着：

| | 查哪里 | 命中后 | 阿里云月刊的结局 |
|---|---|---|---|
| main 旧口径 | 只查主题 | 70 分（需叠加到 100） | 非垃圾 |
| 分支新口径 | 主题 + 摘要 | 直接 100 分 | 垃圾 |

合并时选了一个和稀泥的做法：**把 `monitor.aliyun.com` 加进域名白名单**，
`spam.go:80` 一行。2026-10-02 由人拍板取**分支口径**（带退订头即判营销列表），
并据此移除该白名单条目。

**但这行白名单比「和稀泥」更糟**，值得单独记一笔：白名单在
`LooksLikeSpam` 里是**评分之前**就 `return SpamVerdict{}`（L149），
于是退订规则根本没机会执行，实测得到的是 `score=0 why=""`——
**看起来「判成非垃圾」，实际是「压根没判」**。判据（阿里云样本
`spam:false`）绿了，实现却是空转。用豁免掩盖规则冲突，会把冲突变成沉默。

对应改动：
- `backend/internal/email/spam.go` —— 删除 `monitor.aliyun.com` 白名单条目，
  注释里写明拍板结论、代价、以及可判定的收回点。
- `backend/internal/email/spam_samples_test.go` —— `aliyun-product-monthly`
  期望改为 `spam:true, minScore:100`，并把旧的「near-miss」论证替换为
  这次的取舍说明（含明确代价：阿里云服务月刊会被判垃圾）。

**这条取舍的已知代价**：真实服务通知 + 正文带退订链接这一类兜不住。
误伤由 `invoiceCandidate` / `important` 短路和出票/账单类域名白名单兜住。

## 2. 合并质量核实（并发会话做的，比我的方案更根本）

我在隔离 worktree 里试合并时发现的三个缺陷，对照当前 main：

| 我发现的缺陷 | main 现状 |
|---|---|
| CSV 合计行金额落在 `cells[7]`（文件名列） | 已引入 `invoiceSummaryHeader` 作为**唯一列定义源**，合计行按表头定位「金额」列——消除了整类列位漂移，比逐个改数字更根本 |
| `assertNoLabelSegment` 的正则 `-\d+\.\d{2}-\d{4}-\d{2}-\d{2}\.pdf$` 恒假 | 已改为**按段判定**（金额 `^\d+\.\d{2}$`、日期拆三段），顺带绕开了 Go RE2 无回溯的问题 |
| 分支侧测试夹具缺 `Status` / `FilePath` | 已补全 |

顺带固化一个本轮踩到的坑（已写进记忆）：

**Go 的 RE2 没有回溯**，`\d+` 后面跟字面量时无法退让。
`-...\d{2}-\d+\.pdf$` 在真实发票号（20 位）上必然匹配失败，
而同一模式在 JavaScript 的回溯引擎下是匹配的。跨语言校验正则会得出
相反结论——**判据必须在最终运行的引擎里验**。

## 3. 本轮修的第二个缺陷：`pgSafeWithoutIsolation` 豁免是真盲区

`TestPGTestsNeverTargetTheProductionSchema` 报 `diag_credential_health_test.go`
连了 PG 却没钉 `search_path`。该文件确实 0 写语句、且有
`POCKET_REAL_MAIL_DSN` 开关，符合豁免条件。

但我在写豁免理由时意识到：**一旦列入豁免表，护栏就完全跳过该文件**，
是「不看」而不是「检查后放行」。于是做了负控——往该文件注入一段真实的
`pool.Exec(ctx, "DELETE FROM email_accounts")`，护栏**依然绿**。

证实后把这条写进豁免注释和理由字符串本身：

> 「0 写语句」是一次性观察，不是机器维持的不变式。改这个文件的人
> 必须自己保证不引入写语句，并在同一次改动里跑一遍本护栏。

中途与并发会话撞了同一个 map key（`duplicate key` 编译错），
保留了对方那条更详尽的条目，把负控结论并入其注释。

## 4. 不实日期：同一类缺陷的不同措辞变体

早先提交 `b3cdd819` 修过「实测（2026-10-04）」。本轮普查发现
**措辞换成「审计」就漏掉了 6 处**，分布在：

- `backend/internal/llmgateway/client.go`
- `backend/internal/server/server_assistant.go`
- `frontend/src/api/email.ts`
- `frontend/src/features/email/email-job-runtime.ts`
- `frontend/src/features/email/__tests__/email-job-runtime-singleton.test.mjs`
- `frontend/src/features/email/__tests__/email-long-request-budget.test.mjs`

全部改为 2026-10-02。**教训**：按关键词普查某一类缺陷时，
同一类可能有多种措辞变体（「实测 / 审计 / 审计记录 / 审计实测」），
只匹配一种会把剩余的报成「已清干净」。

（另：`docs/**` 里的 `2026-11/12` 是证书到期日，`quiet_timezone_test.go`
里的 `2026-11-01` 是 DST 结束日，均为合法内容，未改。）

## 5. 验证

```
# 后端
cd backend && go vet ./...                              → exit 0
cd backend && go test ./... -count=1                    → exit 0（53 ok / 0 FAIL / 18 无测试）

# 前端
cd frontend && npm.cmd run gates                        → GATES_EXIT=0
  （typecheck + build + 19 个门禁脚本；孤儿测试 0，覆盖 170/170）
```

注意：本机 PowerShell 执行策略禁止 `npm.ps1`，必须用 `npm.cmd`。

## 6. 本轮开始时的环境异常（已处理）

- 接手时工作区有一个被截断的 `backend/internal/email/fetcher.go`
  （函数体被吞、文件语法损坏），`origin/main` 上是完好的——
  判定为上一轮会话崩在半截写入的残骸，非有意改动，已还原。
- 同理 `docs/handoff/2026-10-02-round8-cleanup-apk-and-device-outage.md`
  是一份**双重编码损坏**（UTF-8 被按 GBK 解读后再存回）的残缺副本，
  章节与远端一一对应且远端更完整，已删本地副本改用远端版。
- 全程有并发会话在写同一仓库（观察到文件 mtime 在两次检查之间变化、
  `zz_diag_credential_health_test.go` 被改名为 `diag_credential_health_test.go`、
  本轮中途出现 map 重复 key）。**动手前先看 `git status` 与文件 mtime。**

## 7. 遗留与建议

1. **退订口径的代价需要真实信箱复验**：拍板后阿里云月刊会被判垃圾。
   下次连真实信箱时，看 `spamdryprobe`（`cb1faabc` 新增）在真实样本上的
   表现，确认误伤面是否可接受。
2. **`pgSafeWithoutIsolation` 是一张会腐烂的表**：每一条都靠人写理由维持。
   若要根治，应让护栏在跳过时仍做**只读扫描**（禁止写语句）而不是全跳过。
   本轮只做了记录，没有改护栏语义。
3. **合并 worktree 已被并发会话删除**，`wt-apkbuild` / `wt-parked` 仍在，
   清理时机交给下一轮判断。

## 8. 下一轮提示词

- 起步先 `git worktree list` + `git log --oneline -5` + `git status`，
  确认没有并发会话在动同一个仓库再动手。
- 复验退订口径的真实代价：用 `spamdryprobe` 在真实信箱样本上跑，
  看 `monitor.aliyun.com` 之外还有哪些「服务通知 + 退订链接」被误伤。
- 考虑把 `pgSafeWithoutIsolation` 的豁免改成「仍扫写语句、只放行 schema
  隔离要求」——现在这张表里的每条理由都无机器守护，已在本轮注明。
- 清理残留 worktree：`wt-apkbuild`（detached d95b4a68）、
  `wt-parked`（audit/2026-10-02-pending-human-decisions）。
