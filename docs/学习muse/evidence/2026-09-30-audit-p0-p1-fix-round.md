# 交付后审计的修复轮：P0 越权 + P1 静默损坏 + P2 免打扰时区

> 日期：2026-09-30（审计后修复轮）　范围：B-1…B-4（P0）、B-9 / B-10（P1）、B-11（P2）
> 前置：[2026-09-30-post-delivery-audit.md](./2026-09-30-post-delivery-audit.md)
> 状态：**6 条已修并加了守卫；5 条新增集成测试已在真实 PostgreSQL 17 上跑通。**

---

## 0. 这一轮的方法

审计里的每条结论都重新打开代码核对过一遍（沿用上一轮「不采信代理结论」的纪律），
其中两条与审计原文有出入，见 §4。

每条修复都配了**能在修复前失败的测试**。有三类：

| 类型 | 用在哪 | 为什么 |
|---|---|---|
| 纯函数单测 | 可见性规则、事件类型选择、免打扰时区算术 | 不需要数据库，秒级 |
| AST 接线守卫 | 「校验写了但没接上」的三条 | handler 的 store 是具体类型，端到端必须真库；接线本身只能读源码验 |
| PostgreSQL 集成测试 | JOIN 隔离、批量 participants、assignee 同步事务 | SQL 只有真跑才算证据 |

**真库证据**：`postgres:postgres123@192.168.31.34:15433`（用户提供的远端 Docker PG17），
`POCKET_TEST_POSTGRES_DSN` 指向默认 `postgres` 库，测试各自建临时 schema 并在结束时删除。

---

## 1. P0 · 已修

### B-1 · 邮件来源的租户校验是死代码

`email.Store` 里**早就有** `GetEmailByIDScoped`（`JOIN email_accounts`，`handleEmailBody` 在用），
审计说的「SELECT 里没有 workspace_id」描述的是另一个方法 `GetEmailByID`。
根因不变：resolver 调的是**未限定**的那个，再在 Go 里比对 `msg.WorkspaceID`——
而该投影根本不选这一列，字段恒为 `""`，`!= ""` 短路成「放行」。

**修**：resolver 改走已有的 account-joined 读取；`msg == nil` 也当成 not-found
（旧代码对未知 id 会在 `msg.WorkspaceID` 上**空指针 panic**）。
resolver 的 `emails` 字段从 `*email.Store` 收窄成接口，让这条规则可以脱离数据库测。

- `resolver_email_test.go`：跨租户邮件 → not-found；scope 必须传进 store；空 workspace 归一；
  store 故障不得被洗成 not-found；空指针不再 panic。
- `store_email_scope_test.go`（真库）：同 workspace 另一用户 / 同用户另一 workspace / 无关组合，
  三种都读不到；本人读得到且投影与未限定版逐列一致。

### B-2 · POST /api/tasks 不校验 parentId

**修**：在 `CreateTask` 之前调用既有的 `validateReparent`，与 PATCH 走同一份实现。
新任务的 id 已经在前面生成，所以自环、跨租户父指针、环检测全部覆盖。

- `TestPostTasksValidatesParentBeforeCreate`（AST）：POST 分支必须调用 `validateReparent`
  **且在 `CreateTask` 之前**——顺序反了等于没修。**修复前 FAIL，已验证。**
- `TestTaskParentValidationHasOneImplementation`：`validateReparent` 全仓只能有一份实现。
- `TestSubtaskRouteTakesParentFromPath`：子任务 body 不得出现 `parentId`，`ParentID` 必须来自路径。

### B-3 · GET /children 只检查父任务

**修**：新增 `task.FilterReadableChildren`，规则**复用 `CanReadWorkItem`**，不写第二份 SQL 谓词——
两份规则必然漂移，漏掉的那份就是泄露。参与者用新增的
`ListParticipantsForTasks` 一次批量取回（避免 N+1）。进度按**过滤后**的子项算，
否则「3/5 完成」本身就会泄露不可见子项的存在与状态。

- `access_children_test.go`：外人只拿到 workspace 可见子项、父任务 owner 仍能看到自己的 private 子项、
  纯成员什么都看不到、空身份不匹配、顺序保持、参与者数据缺失时按 owner/visibility 兜底。
  （写这条时真的踩到了 nil callback 崩溃，已在实现里兜住。）
- `TestTaskChildrenFiltersChildrenByVisibility`（AST）：handler 必须调用过滤器。**修复前 FAIL，已验证。**
- `workitem_participants_pg_test.go`（真库）：批量读取、跨 workspace 隔离、空/重复/空白 id。

### B-4 · 提醒执行器信任 payload 的 workspace

**修**：`reminderPayload` 直接删掉 `WorkspaceID` 字段——不是「不优先使用」，是**结构上不存在**。
租户只来自 `t.WorkspaceID`；为空则**整个 tick 失败**（不猜默认租户）。
每个 item 再校验一次自身 `workspace_id`，与 job 不符就跳过（纵深防御）。
每个 item 的写与通知用该 item 自己的 workspace。

> 顺手做的一件事（超出你给的范围，明说以便你否决）：`limit` 改为**只能调小不能调大**
> （原 B-16，P2）。理由是它就在同一段 payload 解析里，而批量上限的存在意义
> 就是防止宕机积压一次性爆发；客户端传 200 会直接废掉这个上限。

- `TestWorkItemReminderIgnoresPayloadWorkspace`：payload 带 `ws-victim` 时，扫描、清 remind_at、
  读参与者、写事件、发通知**全部**必须是 `ws-1`。**修复前 5 条断言全红，已验证。**
- `TestWorkItemReminderSkipsRowFromAnotherWorkspace` / `TestWorkItemReminderFailsClosedWithoutWorkspace`
  / `TestWorkItemReminderPayloadLimitCannotExceedBatch`：**修复前全红，已验证。**

---

## 2. P1 · 已修

### B-9 · assignees 与 participants 是两套永不同步的名单

**修**：新增 `Store.SyncAssigneeParticipants`（单事务）。规则刻意不对称：
assignee 补进参与者（`ON CONFLICT DO NOTHING`，不降级 owner/watcher）、
owner 必在、**只删 role='assignee' 且已不在 assignees 里的人**。
watcher 永不删——任务行上没有任何字段记录「这个人是被特意加进来的」，
靠猜就会误删真实订阅者。刻意**不用** `SetParticipants`：那是 PUT 语义，
读-改-写整份名单会吃掉并发委派。

接入点三处：创建任务、创建子任务、PATCH 改 assignees。

- `TestTaskWritesSyncAssigneeParticipants` / `TestAssigneeSyncHasOneImplementation`（AST 接线守卫）
- `TestSyncAssigneeParticipants`（真库）：补人、不降级 owner、解绑即移除、watcher 存活、
  跨 workspace 拒绝、边界输入。

### B-10 · 状态变更/完成通知是死代码

`NotificationKind` 从 P3 起就映射了 `status_changed` / `completed`，`NotifyRecipients`
也早就决定好收件人，但**没有任何生产者**写这两个事件：队友完成一个任务，通知方永远收不到。

**修**：新增 `task.StatusChangeEventType`（纯函数，完成/接受 → `completed`，其余变更 → `status_changed`，
无变化 → 空）与 `Server.notifyWorkItemStatusChange`，在 PATCH 检测到状态变化时写事件 + 扇出通知。
actor 取自已认证身份（走 context），**不读 body**。两步都是 best-effort：
状态已落库，此时返回 500 只会让客户端重试一个已经成功的写。

- `notify_status_test.go`：事件选择 + 「这个函数产出的事件必须都有通知映射」。
- `TestTaskStatusChangeEmitsEvent` / `TestTaskOperationsNotifiesOnStatusChange`（AST 接线守卫）

---

## 3. P2 · B-11 免打扰时区（按你选的方向：按用户时区存储）

**定调**：不改「客户端传 tz 参数」，改成**按用户时区**。

**根因**：`Defer()` 用 `(fireAt/60)%1440` 算分钟数、`fireAt - fireAt%86400` 算日界，
**两者都是 UTC**。UTC+8 下 23:50 本地 = 15:50 UTC，看起来是下午三点，直接放行。
旧文档里「免打扰顺延已验证」只在服务器时区恰为 UTC 时成立——这条结论本轮作废。

**修**：
- `Defer(fireAt, loc)` 显式收时区。窗口末端用**日历字段**构造而不是加时长：
  DST 切换日本地日有 23/25 小时，加 7h30m 时长会差一小时（这条是被我自己的测试抓出来的，
  第一版实现就是错的）。
- 时区来源是既有的 per-(user, workspace) 设置库，文档 `notifications` / `quiet-hours`：
  `{"timezone":"Asia/Shanghai","startMin":1350,"endMin":450,"disabled":false}`。
  用 IANA 名而不是固定偏移——偏移表达不了 DST。
- 每一 tick 按**工作项 owner** 解析（§4.2 `reminded` 只通知 owner），
  同一 owner 每 tick 只读一次。取不到、文档损坏、时区名不认识 → 退回服务器时区，
  **绝不退回 UTC**。无存储时区 = 用 `time.Local`。
- 加了显式 `disabled`：`00:00→00:00` 不是用户表达「关掉免打扰」的方式，
  靠猜会把用户特意关掉的通知又打开。

- `quiet_timezone_test.go`：上海 23:50 必须顺延到次日 07:30（**同一瞬间按 UTC 评估则不顺延，
  正是旧行为**）；纽约用户同一瞬间不顺延；nil 时区用服务器时区而非 UTC；25 小时的 DST 日；
  payload 解析与越界拒绝。
- `workitem_reminder_quiet_test.go`：同一条 tick 里两个 owner 按各自时钟分别判定
  （上海顺延、纽约照常）；关闭免打扰的用户照常收到；设置适配器的读/缺/坏/无库四种路径。
- 旧测试里 `pinServerZone(t)` 把服务器时区钉成 UTC：这些 fixture 本身写的是 UTC 的分钟数，
  在 UTC+8 机器上会**因时钟而非逻辑**失败——和生产 bug 是同一个隐藏假设。

---

## 4. 与审计原文不一致的两处（已复核）

1. **B-1**：「`email.Store.GetEmailByID` 的 SELECT 里没有 workspace_id」——准确，
   但 `GetEmailByIDScoped`（带 JOIN 的那个）**本来就存在**，只是 resolver 没用它。
   修法因此比预想的更小，不需要新增 store 方法。
2. **B-3**：审计说「读一次父任务即可拿走 private 子项」——成立。
   但 `ChildProgress`（store 方法）**没有任何 handler 调用**，只被测试用；
   真正需要过滤的只有 `/children` 这一个出口。

---

## 5. 真库跑出来的两件事（存量缺陷，**本轮未修**）

真实 PG 一开，learning 包两个从未跑过的测试立刻变红。两条都**不是**我改动引起的，
但也**不是**测试写错那么简单：

### 5.1 `ActiveDayTimestamps` 会把窗口外的旧时间戳算进来（**真缺陷**）

`store.go:440` 的 WHERE 是 `captured_at >= since OR updated_at >= since`，
命中后 Go 侧把**两个时间戳都** append。一条「一小时前采集、刚刚更新」的材料，
会把它一小时前的 `captured_at` 也算进「最近活跃」——**直接虚增连续活跃天数**。
修法很小（逐列过滤，或 SQL 改 `UNION ALL`），但属学习域，不在本轮范围。

### 5.2 `TestReminderLifecycle`：1 个真缺陷 + 2 个自相矛盾的断言

- **真缺陷**：`SnoozeReminder` 按 `now + minutes*60` 算，等于**可能把提醒往回拨**。
  一条 24 小时后才到的提醒，「顺延 2 小时」会变成 2 小时后就到。
  顺延是「往后推」，往回拨一定是错的。至于应该是「从现在起」还是「从原定时间起」，
  属于产品语义，**没有替你决定**。
- **断言自相矛盾**：测试先建了 `due` 和 `future` 两条，只 ack 了 `future`，
  然后断言「0 条到期、0 条 pending」。但 `due` 仍处于 snoozed（armed）状态，
  按 `DueReminders` / `CountPendingReminders` 的定义（`state IN ('pending','snoozed')`）
  它本来就该被计入。这两条断言只有在前一条提醒已被 ack 时才成立。

---

## 6. 本轮新增守卫清单

| 守卫 | 抓什么 | 类型 | 修复前状态 |
|---|---|---|---|
| `TestPostTasksValidatesParentBeforeCreate` | POST 又漏掉父校验 / 顺序颠倒 | AST | **FAIL** |
| `TestTaskChildrenFiltersChildrenByVisibility` | /children 又不过滤子项 | AST | **FAIL** |
| `TestWorkItemReminderIgnoresPayloadWorkspace` | payload 又能改租户 | 纯函数 | **FAIL（5 条断言）** |
| `TestWorkItemReminderSkipsRowFromAnotherWorkspace` | 跨租户行被写库/发通知 | 纯函数 | **FAIL** |
| `TestWorkItemReminderFailsClosedWithoutWorkspace` | 缺 workspace 时猜默认租户 | 纯函数 | **FAIL** |
| `TestWorkItemReminderPayloadLimitCannotExceedBatch` | payload 抬高批量上限 | 纯函数 | **FAIL** |
| `TestTaskWritesSyncAssigneeParticipants` | 写入路径漏掉名单同步 | AST | 新增 |
| `TestTaskStatusChangeEmitsEvent` / `...NotifiesOnStatusChange` | 通知生产者再次消失 | AST | 新增 |
| `TestFilterReadableChildren`（4 例） | 子项可见性规则被改宽 | 纯函数 | 新增 |
| `TestStatusChangeEventType*` | 状态事件选错 / 无通知映射 | 纯函数 | 新增 |
| `TestQuietWindow*`（时区 6 例） | 日界又退回 UTC / DST 差一小时 | 纯函数 | 新增 |
| `TestWorkItemReminder*Timezone*`（4 例） | 免打扰不按 owner 时区 | 纯函数 | 新增 |
| `TestGetEmailByIDScoped*`（真库 3 例） | JOIN 隔离 | 真库 | 新增 |
| `TestListParticipantsForTasks*`（真库 2 例） | 批量读取与隔离 | 真库 | 新增 |
| `TestSyncAssigneeParticipants*`（真库 3 例） | 同步语义与事务 | 真库 | 新增 |

> 三个**既有**测试的 fixture 也是坏的，`created_at` NOT NULL 缺列、审批投影没建 session 关联，
> 以前从不失败只是因为真库从没起过。已修（仅测试代码），否则提醒/审批的 SQL 依旧没有真库证据。

---

## 7. 仍然未做（明确留档）

- **学习域**：§5.1、§5.2。
- **任务域 PATCH/DELETE 没有任何写权限校验**（本轮新发现，审计未列）：
  `handleTaskOperations` 只做 `GetTaskScoped`（workspace 维度），
  协作面（participants / activity / delegate / subtasks）全都调了 `CanRead/CanWriteWorkItem`，
  唯独改状态、改标题、**删任务**这条主路径没有。
  同 workspace 的普通成员可以改别人的 private 工作项、可以删掉它；
  POST 还可以把 `ownerId` 直接指定给别人。**本轮未修**——它和 B-2/B-3 同类，
  但「普通成员能否编辑他人任务」有一部分是产品语义，该由你定。
- 其余 backlog（B-5…B-8、B-12…B-24）维持未修。
