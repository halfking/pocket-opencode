# 2026-10-01 审计轮 · 第四轮

> 接 `2026-10-01-audit-round3.md`。本轮按 round3 §9 提示词执行：
> 安全修复 → 预检文档 → §5.2 清理 → learning 两条方案定夺材料 →
> 密钥处置评估。只写有对照证据的部分。

## 0. 本轮最重要的一句话

**P0 越权（同 workspace 普通成员可改删他人 private 工作项）已修并带
route 级测试落进 main；§5.2 三项清理全部闭环，其中 `sk-` 夹具经全历史
核查定性为合成、并已换成无歧义夹具，这个悬了两轮的「留档待查」永久关闭。**

## 1. 开工前置

| 检查 | 结果 |
|---|---|
| fetch / main 状态 | `main == origin/main == 9e8cff6`，工作区干净 ✅ |
| 本工作区双查 | 干净，无并发写入 ✅ |
| **openpocket 克隆三 worktree** | **仍全部活跃**：主区 logs 03:02（pocketd-cls/v5/email19 err）、wt3 maestro 探针 03:00、`.wt-consolidate` 02:51（seed_email_accounts / verify-real-invoice-e2e 在被写）——**按约定本轮一个 worktree 与分支都没动**，只登记 |
| `go build ./...` | EXIT 0（改动前基线） |

## 2. P0 安全修复：handleTaskOperations 写权限校验（`d8237d9`）

- **漏洞**（consolidation §5.1 第三条）：`PATCH / DELETE /api/tasks/{id}`
  只有 workspace 维度 `GetTaskScoped`，同 workspace 普通成员可改/删他人
  private 工作项；与 B-1~B-4 同类。
- **修法**：新增 `Server.workItemWriteGuard`——`GetTaskScoped` 拿任务 +
  `ListParticipants` 拿名单 + `task.CanWriteWorkItem`（owner 或参与者）；
  PATCH/DELETE 共用。参与者名单读不到 → 500 拒绝（**不当成「没有参与者」
  放行**，同 collaborationContext 的既有取舍）。unknown id → 404（先校验
  后 404，不泄露存在性之外的信息）。
- **语义依据**：`task.CanWriteWorkItem` 与注释（「可见 ≠ 可写」）是既有
  规则，子任务/参与者/委派端点已在用；本次是把 HTTP 主写路径对齐到同一
  规则，无新产品语义。POST 指定 `ownerId` 是注释写明的委派场景，不动。
- **route 级测试**（`task_write_guard_route_test.go`，真库门控
  `POCKET_TEST_POSTGRES_DSN`，无 DSN 按仓库约定 SKIP）：
  普通成员 PATCH/DELETE 他人 private 项 → 403 且**行未被写**（GET 复核
  原值）｜owner PATCH → 200｜参与者（非 owner）PATCH → 200｜owner DELETE
  → 200 且 GET 404｜unknown id → 404｜无 store → 503 路由锁（不需库，
  无 DSN 也常绿）。
- **本机验证**：无 DSN 环境 SKIP 是刻意的（`如何验证真实数据库.md`）；
  server+task+email+learning 四包全绿（见 §6）。**PG 用例尚未在真库跑过，
  下次有 DSN 的环境会自动执行。**

## 3. deploy 预检前置文档（`44da73f`）

`deploy/bin/PREFLIGHT.md`（一页）+ README 指引，逐条对照脚本实现写成：

- **依赖表**：python3 / psql / redis-cli / docker compose v2 / `ps -axo` /
  openssl——各自用在哪、缺了会怎样；按 OS 安装命令（macOS libpq keg-only
  路径特判已注明）。
- **修正 round3 提示词一处**：MySQL **没有**登录级预检（check_pg 只查
  PG、check_redis 只查 Redis），`mysql` 客户端不需要装——MySQL 只出现在
  防重复置备的清单/端口检查里。文档按实际实现写，不按 round3 转述。
- **fail-closed**：start.sh 预检失败不切 release；ensure-databases 的
  `--can-create` 门禁（端口占用/已有候选/非空数据目录/已有 DSN 一律拒绝
  置备）；错误信息固定文案、从不打印 DSN/凭据。
- **MSYS 降级路径**：`--dry-run` 的 `--inventory` 失败只打 ⚠️ 不判死；
  `--env-file` 真实预检与 `--can-create` 不降级；Windows 需原生
  python3/psql/redis-cli 并进 PATH。

## 4. §5.2 清理（`8b2d2b5` / `e1ffa19`）

1. **invoice 夹具合成化**：真实公司名/20 位发票号/OSS 链接 → 合成值
   （示例投资有限公司 / 12345678901234567890 / `*.example.com`），触发
   路径与断言不变，3 测试全绿。顺手把 invoice.go / pipeline.go 注释里
   引用的真实发票号与公司名、handoff `2026-09-30-email-pipeline-verify.md`
   里的真实销售方/发票号一并脱敏（沿用「handoff 不留标识符」既有约定）。
   全仓 grep 复核：真实发票号/公司名 0 残留。
2. **`llm_gateway_mask_key_test.go:15` sk- 夹具定性**：`git log -S --all`
   全历史核查——该字面量只在引入它的 `14e35a1`（2026-09-05 掩码测试）
   出现过，从未在配置/证据/脚本中被使用，与租户 key（sk-6tGL…K51YV）
   前后缀均不同 → **判定合成夹具**（`bf15387`）。已换成
   `sk-test-fixture-not-a-real-key-000111` 无歧义合成串（maskKey 纯字符串
   操作，语义不变），并留定性注释。
3. **scripts/ 探针归档**：8 个一次性脚本 → `scripts/archive/2026-09-probes/`
   （probe-invoice-* / gw-audio-probe / cdp-doc-open-* /
   locales-add-fetchhint / **verify-real-invoice-e2e / verify-real-mailbox-readonly**
   ——后两者含真实公司名、发票号、真实邮箱地址，是真邮箱一次性复验脚本）；
   `patch-export-grid-signature.mjs` 按 consolidation §6.4 裁定**删除**；
   email 验证工具箱（email-* / imap-* / inject-fixture-* / verify-email-*）
   成套可复跑、仍在随邮件线演进，**保留原地**；`seed_email_accounts.sh`
   是功能基建（种真实企业邮箱账户），保留，真实地址残留记入 §8 遗留。

## 5. 密钥处置状态与历史重写评估（承接 round3 §5，用户侧）

- **轮换状态未知，仍需用户执行/确认**——这是让旧 key 失效的唯一真动作。
- 轮换完成后的历史重写**评估**（本轮补齐，机器不代做）：
  - **范围已锁定**：密钥只在 `1319229`（2026-08-31 21:56）与 `ab2e71b`
    （2026-09-01 02:06）两个提交进入历史（test-evidence/ 两文件）；
    其后全部提交的树都含它 → `git filter-repo --replace-text` 需覆盖
    2026-08-31 之后约 5 周历史。
  - **做法草案**：`git filter-repo --replace-text`（用户提供完整密钥字面量
    → `==>***REMOVED***`）；filter-repo 自动产出 old→new commit 映射，
    **必须存 docs/**——handoff 文档里数百处 SHA 引用（1319229/ab2e71b/…）
    重写后全部悬空，映射是唯一对账依据。
  - **影响**：force-push `origin/main` + 全部远端分支 + tag；所有协作者
    与 openpocket 克隆（当前 3 个活跃 worktree）必须停机重克隆。
  - **时机**：openpocket 会话全部结束后执行；重写是清洁动作不是安全动作，
    **轮换完成即已安全**，重写可与「全员重克隆」公告同批、不必抢时间。

## 6. 本轮测试证据（合并树实测）

| 套件 | 结果 |
|---|---|
| `go build ./...` | EXIT 0 |
| `go vet ./...` | 干净 |
| `go test ./internal/server/ ./internal/task/ ./internal/learning/` | 全绿（含新 route 守卫测试：无 DSN 下 5 SKIP + 1 PASS） |
| `go test ./internal/email/` | 2 失败 = `TestWriteKeyAtomic_CreatesFileWithCorrectMode`（Windows 权限位 666 vs 0600）+ `TestFetchPOP3MailboxAuthRejected`（本地 TCP wsasend 偶发）——与 consolidation §3 既知清单**同名同因**，非回归；invoice 3 测试全绿 |
| `bash deploy/bin/tests/test_database_preflight.sh` | 24/24 |
| `bash tests/deploy-integration-test.sh` | 15/15 |

## 7. learning 两条产品语义——方案已备，待用户定夺（§5.1 前两条）

两条都**不改代码**，方案与推荐如下，等用户拍板后下轮落地：

### 7.1 `ActiveDayTimestamps` 窗口外旧时间戳混入（store.go:439-461）

- **事实**：SQL `WHERE … AND (captured_at >= $3 OR updated_at >= $3)`，
  Go 侧把两个时间戳都 append。一条「30 天前捕获、今天被更新」的行会把
  **30 天前的 captured_at** 也放进结果 → 连续活跃天数被虚增。
- **方案 A（推荐）**：Go 侧逐值过滤（`capturedAt >= sinceUnix` 才 append），
  SQL 不动。语义保持「捕获或漏斗推进都算活跃」，只修「窗口外混入」这个
  无争议 bug。改动约 2 行 + 单测。
- **方案 B**：SQL `UNNEST` 只吐窗口内时间戳。逻辑集中但重写查询，
  收益与 A 相同。
- **附带产品问题（可一并定，也可不动）**：「活跃」是否该包含 updated_at
  （漏斗推进）？若只算 capture，streak 会更保守。**这是语义变更，未获
  定夺前不动。**

### 7.2 `SnoozeReminder` 基准（store.go:395-405）

- **事实**：`until = now + minutes*60`。24h 后才到期的提醒被顺延 2h →
  变成 **2h 后就响**——snooze 反而把提醒拉早了。
- **方案一（推荐）**：`until = max(now, next_due_at) + minutes*60`
  （「从原定时间起，但不早于现在」）。堵住「顺延反而提前」；对已过期
  提醒行为与现状完全一致；对未到期提醒是真正的「顺延」。唯一入口
  learning_handler.go:264，影响面单点。
- **方案二**：`until = max(now + minutes*60, next_due_at)`（「从现在起，
  但不得早于原定时间」）。最小改动：只堵提前，未到期提醒上 snooze 等于
  无操作。对「顺延」的直觉满足弱于方案一。
- **方案三**：维持现状（「从现在起」，snooze = X 分钟后再响，不管原定），
  补文档。若产品视角认为 snooze 本来就是「几分钟后再叫我」，现状也说得通。

## 8. 遗留（严禁外推）

1. **网关密钥轮换**——用户动作，最高优先级（§5 有重写评估与时机）。
2. learning §7 两条待用户定夺，**方案已备勿再自行取舍**。
3. PG 门控的 route 守卫测试待真库环境执行（SKIP ≠ PASS）。
4. `seed_email_accounts.sh` / `providers.test.mjs` / greenmail 测试注释里
   的真实邮箱地址（`huangxutao@kxpms.cn`）——属邮件线活跃文件，本轮
   不越界；下轮可与邮件线会话协调后脱敏。
5. openpocket 克隆三 worktree 仍活跃（§1），`wip/2026-10-01-audit` /
   `fix/bugz-marketplace-conflict` 与 worktree 清理继续挂起。
6. §5.1 第三条已修（本轮），§5.1 第一、二条转 §7 定夺流程。

## 9. 下一轮提示词

> 接 `2026-10-01-audit-round4.md`。本轮 P0 越权修复、PREFLIGHT 文档、
> §5.2 三项清理、sk- 夹具定性均已进 main（d8237d9 / 44da73f / 8b2d2b5 /
> e1ffa19 / bf15387）。下一轮：
> 1. （用户侧，催）网关密钥轮换；完成后按 round4 §5 评估执行历史重写
>    （含 commit 映射存档与全员重克隆公告），等 openpocket 会话结束。
> 2. 按用户对 round4 §7 两条的定夺落地 learning 修复（方案一 + 方案一
>    为推荐），带单测。
> 3. 有 DSN 环境时跑一次 server 包真库用例，关闭「PG 用例未真跑」条目。
> 4. openpocket 会话若已停：清 worktree 与 `wip/2026-10-01-audit` /
>    `fix/bugz-marketplace-conflict`。
> 开工前照例：fetch、双查工作区、确认并发会话状态。
