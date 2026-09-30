# 交付后审计：发现的问题、已修的与待修的

> 日期：2026-09-30（交付后复审）　范围：学习域 + 任务域 + 前端图标层
> 状态：**2 条高危已修并加守卫**；其余已分级登记为 backlog，**未修**。
> 方法：两个只读探查代理分域审计 + **逐条人工复核**后才动手改。

---

## 0. 审计方法与一条纪律

审计由两个 `explore` 代理并行做（学习域 / 任务域），产出 40+ 条候选问题。
**代理的结论没有直接采信**——每条要动手改的，都自己打开代码核对过。
实际核对了其中 4 条（学习域 2、任务域 2），结论见下。

复核立刻证明了这条纪律是必要的：代理对 `TaskUpdate.Status` 的判断是
「字段被丢弃」，实际是**故意的**——`CompleteTaskScoped` 自己写
`status = 'completed'`，处理它会产生重复赋值。差点改出一个新 bug。

---

## 1. 已修（2 条，均为高危）

### 高-1 · 完成任务的写路径静默丢弃全部工作项字段

**位置**：`internal/task/store.go` `taskUpdateSets`，调用点 `CompleteTaskScoped`

**根因**：`taskUpdateSets` 是完成路径专用的 SET 构造器，但只处理了 4 个遗留字段
（title/description/priority/workstream_id），**不认识** `TaskUpdate` 里的 8 个工作项字段
（type/ownerId/assignees/dueAt/remindAt/parentId/tags/visibility）。

**为什么这条特别糟**：handler 在进入 store **之前**已经跑完 `validateReparent`
（完整的环检测）并放行，然后 store 在事务里把 `parentId` 直接丢掉。
**环检测白做了，客户端还收到 200，响应里回显的是旧值**——没有任何错误、日志或信号。

**修复**：补齐 8 个字段的赋值，列名与编码严格对齐 `updateTask`
（`assignees`/`tags` 走 `encodeStringList`，客户端传 `[]` 才能清空）。

**防复发**：新增 `TestTaskUpdateSetsCoverEveryField` —— 反射遍历 `TaskUpdate`
的全部指针字段填非 nil，断言渲染出的赋值条数 = 字段数 + 1（`updated_at`），
并**按列名逐一断言 8 个工作项列都在**。
`Status` 作为**显式例外**跳过并在注释里写明原因（调用方拥有该列），
不是默默放过。配套 `TestTaskUpdateSetsPlaceholderOrderIsSequential`
断言第 N 条赋值引用 `$N`，防止 SET 子句与实参错位。

> 这条守卫在第一次跑时就抓出了 `Status`（见 §0）——说明它是有效的，
> 不是「写完就绿」的摆设。

### 高-2 · `ClaimMilestone` 的合成主键不含 workspace，跨 workspace 必然 23505

**位置**：`internal/learning/store.go` `ClaimMilestone`

**根因**：`id` 拼成 `"ms-" + key + "-" + userID`，不含 workspace。
而 `ON CONFLICT (workspace_id, user_id, kind, item_id)` **只覆盖唯一索引，不覆盖主键**。

**后果**：同一用户在第二个 workspace claim 同一里程碑时，撞的是**主键**而非唯一索引 →
`23505 duplicate key`，而那条 `ON CONFLICT DO NOTHING` 管不到 →
`ClaimMilestone` 返回 error，**第二个 workspace 的里程碑永远播报不了**，
每次 digest 都刷一条错误日志。

**修复**：`id` 改为 `"ms-" + workspace + "-" + key + "-" + userID`。

**向后兼容**：已有行的 id 不含 workspace，但**同一 workspace 内**的重复 claim
仍由唯一索引兜住，行为不变。

**早已有测试却没抓到**：`store_pg_test.go` 里本来就有「different workspace」用例——
它只会在真库上失败，而真库从未启动过。已在该用例旁补注释说明这条因果。

---

## 2. 已修（前端，1 组）

### 高-3 · 图标注册表的「类型保护」只覆盖 3/10 处，且有一条假注释

- `DYNAMIC_ICON_NAMES` 是**死代码**（导出后从未被 import），
  而它的注释写着「构建脚本与两个门禁脚本都从**这个数组**读」——
  **这句是假的**，三个脚本实际都是正则解析 `ICON` 对象块。已删除该导出并订正注释。
- `IconName` 的注释宣称「数据表写错名字会在 vue-tsc 阶段就报错」，
  但当时只有 `SessionStatusBar`/`ToolCallCard`/`StudyHubView` 三个组件套了类型，
  其余 6 处数据表仍是裸字符串。**注释夸大了覆盖范围**。

**修复**：把 `IconName` 真正套到全部 6 处数据表
（`MoreHubView.HubItem`、`SettingsView.themeOptions`、`BottomNav.NavItem`、
`SettingsMenuDrawer.MenuItem`、`useSessionDrafts.QuickCommand`、
`FlashcardEditView.templateOptions`），并把 `IconName` 的注释改成准确描述。

**注入实测**：把 `MoreHubView` 的 `'forum'` 改成 `'not_a_real_icon'`，
`vue-tsc` 精确报 `MoreHubView.vue(90,22): error TS2769`。
**类型保护是真的**，不是注释里说说。已还原。

---

## 3. 未修 backlog（已复核，按优先级）

以下**本轮未修**。列在这里是为了不丢失，不是因为它们不重要。

### P0 · 可被直接利用的越权

| # | 位置 | 问题 |
|---|---|---|
| B-1 | `learning/sources/resolver.go:91` | 邮件来源的租户校验是**死代码**：`email.Store.GetEmailByID` 的 SELECT 里没有 `workspace_id`，`msg.WorkspaceID` 恒为 `""`，判断永远为假。任意登录用户可把**他人邮件**收进自己的学习条目（跨租户数据泄露）。文件里声称「enforce the boundary here」的注释是错的 |
| B-2 | `server.go:1305-1358` | `POST /api/tasks` **完全不校验 `parentId`**（`validateReparent` 只挂在 PATCH）。可写入自环行与跨租户父指针 |
| B-3 | `task_hierarchy_handler.go:40-55` | `GET /children` 只对**父**任务做可见性检查，不查子项的 `visibility` → 读一次父任务即可拿走 `private` 子项的完整字段 |
| B-4 | `executors/workitem_reminder.go:133-136` | 租户边界取自 payload 且**永远优先**于任务自身的 workspace。已认证用户可让执行器**跨租户写库并发通知**。文件头声称「客户端无法扩大扫描范围」是错的 |

### P1 · 数据静默损坏

| # | 位置 | 问题 |
|---|---|---|
| B-5 | `learning/types.go:194` + `store.go:61` | `sourceKind=manual` 且 `sourceId` 为空时，同一用户**所有手工条目塌缩成一行**（都等于 `(ws,user,'manual','')`）。第二条会覆盖第一条并返回 200「已存在」，历史条目静默丢失 |
| B-6 | `task/store.go:419` | `UpsertTask` 违背自己声明的时间戳契约：实参写 `now, now`，丢掉了算好的 `task.CreatedAt`。keyset 分页排序键漂移，可能漏行或重复 |
| B-7 | `learning/store.go:86` | 提醒唯一索引不含 `card_id`，`itemId` 为空的同类提醒互相覆盖——为两张卡各建一条 `spaced_review` 提醒，第二条静默改写第一条 |
| B-8 | `learning/store.go:299` | 提醒 upsert 的 `DO UPDATE SET` 不含 `state`/`last_sent_at`/`snoozed_until` → 用户重新保存已 ack 的提醒是静默 no-op，前端却乐观显示已生效 |
| B-9 | `server.go:1365` | `tasks.assignees` 与 `work_item_participants` 是**两套永不同步的名单**。被指派者不在 participants → private 项下她**打不开任务**，也不会收到任何通知 |
| B-10 | `notify.go:60,64` | `NotifyStatusChanged` / `NotifyCompleted` 是**死代码**——没有任何生产者。任务完成/状态变更**永远不发协作通知**（与原始需求「包括通知」直接冲突） |

### P2 · 溢出与时区

| # | 位置 | 问题 |
|---|---|---|
| B-11 | `task/quiet.go:69,73` | 免打扰用 **UTC** 日界算「本地午夜」。UTC+8 部署下 23:50 的提醒**照常推送**。整条免打扰链路只在服务器时区恰为 UTC 时正确——这与 P4 evidence 里「免打扰顺延已验证」的结论**冲突**，那个结论只在 UTC 下成立 |
| B-12 | `learning/service.go:382` | `nextDailyOccurrence` 用 UTC 零点解释 `ruleValue`，与「本地时间 HH:MM」契约矛盾 → 每日回顾从第二天起固定漂移 `tz_offset` 小时 |
| B-13 | `learning/service.go:371`、`server.go:255`、`scheduler.go:172` | 三处 `int64` 乘法无上界：间隔规则、`snooze minutes`、FSRS 间隔参数均可溢出成过去时间戳 → 提醒**每轮都重新到期并循环** |
| B-14 | `executors/learning_digest.go:198` | 里程碑播报硬编码 `tzOffsetSec=0`，与 HTTP 路径的 `tz_offset` 用不同日界线。而 `ClaimMilestone` 是**一次性不可撤销**的，会在与界面不同的日子上被消耗 |
| B-15 | `workitem_reminder.go:164` | 顺延终点按**原始 `remind_at`** 算而非按 `now`，会把提醒改写成过去时间 → 下一 tick 走 stale 分支**从未通知就被退役** |
| B-16 | `workitem_reminder.go:137` | payload 的 `limit` 无上限覆盖 batch 限流（客户端可把单 tick 放大到 200） |

### P3 · 性能与一致性

| # | 位置 | 问题 |
|---|---|---|
| B-17 | `learning/store.go:440` | `ActiveDayTimestamps` 无可用索引也无 LIMIT，活跃用户每次打开界面都全表扫 |
| B-18 | `task_from_source_handler.go:130` | 会议幂等靠**全量拉取整个 workspace 的任务**构造 `seen` map；且读与写之间无锁无事务 → 并发转换同一会议会产生重复任务（表上也无唯一约束兜底） |
| B-19 | `learning/store.go:191` | `destLearningItem` 把 `tags` 扫进 `new(string)` 丢弃 → 接口恒返回 `"tags": null`，而库里已存 |
| B-20 | `learning/store.go:154` | `importance == 0` 被静默改成 3，但契约明确允许 0 → 用户主动设 0 变成 3 |
| B-21 | `learning/store.go:381` | `MarkReminderSent` 永远写 `state='pending'`，`'sent'` 从不落库 → `state='sent'` 的查询恒空 |
| B-22 | `executors/learning_digest.go:124-160` | 先提交重排、后发通知，通知失败时提醒状态已被改写；`once` 提醒在重试时已查不到 |
| B-23 | `workitem.go:193` | `AppendEvent` 不校验 `ev.WorkspaceID` 与 `ev.TaskID` 归属一致，是租户隔离里唯一没做 JOIN 的写路径（当前调用方都传对了，属潜在） |
| B-24 | `server/task.go:34-35` | `DueAt`/`RemindAt` 带 `omitempty`，int64 零值被省略 → 客户端无法区分「没有 dueAt」和「字段缺失」 |

### 需要产品决策，不单点改码可解

- **学习域的到期提醒到底由谁投递**：审计发现 `learning_digest.go` 对每条 due reminder
  调 `Advance`（内部 `AckReminder` 或 `MarkReminderSent`），
  但全仓库**没有任何地方再读 `learning_reminders` 发内容**。
  即：提醒被「消费」并销毁，用户从未收到过 deadline 通知。
  `daily_digest` 在 `summary.Empty()` 时同样只标记不推送。
  **这是 P0 功能缺口，需要先定「投递责任方」再改码。**

---

## 4. 复核为「无发现」的方向

这些是审计问到、且**逐条核对后确认干净**的，记下来避免下次重复怀疑：

- **SQL 与 scan 四面一致**：`taskColumns` 25 列 ↔ `scanTask` 25 个 dest 逐位对齐；
  `taskInsertColumns` 22 列 ↔ 22 个占位符；两个动态拼参器 `updateTask` / `listTasksCursor`
  的 argIdx 递推无跳号重号。**表结构与列清单本身没有错位**，风险全在写路径与横切规则上。
- **store 层租户隔离**：`internal/learning/store.go` 全部 12 条语句都带
  `workspace_id` + `user_id` 并用 `normalizeWorkspace` 归一。跨租户问题出在 resolver 层（B-1）。
- **`WouldCreateCycle` 的 fail-closed 语义正确**：预算耗尽返回 `true`，
  `validateReparent` 翻译成 400，**不会**把「检测失败」当「没问题」。
- **`ComputeStreak` 本身自洽**：去重、丢弃未来日、从 anchor 向下走的逻辑没有问题。
- **进度只聚合一层不会重复计算**：每层各自只数直接子项，代价是深链目标永远 0%（设计取舍，已在文档写明）。

---

## 5. 本轮新增的机器守卫

| 守卫 | 抓什么 | 验证方式 |
|---|---|---|
| `TestTaskUpdateSetsCoverEveryField` | 新增 `TaskUpdate` 字段却没进完成写路径 | 首次运行就抓出 `Status` 遗漏 |
| `TestTaskUpdateSetsPlaceholderOrderIsSequential` | SET 子句与实参错位 | 绿（`$N` 逐条对齐） |
| 前端 `IconName` 类型 | 数据表图标名写错 | 注入 `'not_a_real_icon'` → `vue-tsc` 报 `(90,22) TS2769` |
| 既有 `store_pg_test.go` 跨 workspace claim | 主键/唯一索引混用 | 修前必然失败，修后逻辑自洽（**真库上未跑**） |
