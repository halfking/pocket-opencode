# 04 · 数据模型与 API 契约

**日期**：2026-09-30
**实施状态**：P0 部分为本仓库已落地代码（标 ✅ 已实现），其余为规划（标 📐 规划）。
所有 DDL 沿用仓库既有范式：写在包内 `migrate()` / `EnsureSchema()`，用
`CREATE TABLE IF NOT EXISTS` + `ALTER TABLE ... ADD COLUMN IF NOT EXISTS`，**不引入迁移框架**。

---

## 1. 任务域 DDL（✅ 已实现，落在 `backend/internal/task/store.go:migrate()`）

```sql
-- 工作=任务合一：分类 / 责任人 / 到期 / 层级 / 溯源 / 可见性
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS type        TEXT NOT NULL DEFAULT 'other';
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS owner_id    TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS assignees   JSONB NOT NULL DEFAULT '[]';
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS due_at      BIGINT NOT NULL DEFAULT 0;
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS remind_at   BIGINT NOT NULL DEFAULT 0;
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS parent_id   TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS origin_kind TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS origin_ref  TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS tags        JSONB NOT NULL DEFAULT '[]';
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS visibility  TEXT NOT NULL DEFAULT 'private';
CREATE INDEX IF NOT EXISTS idx_tasks_due    ON tasks(workspace_id, due_at)    WHERE status <> 'completed';
CREATE INDEX IF NOT EXISTS idx_tasks_type   ON tasks(workspace_id, type);

-- 协作：参与者 + 活动流
CREATE TABLE IF NOT EXISTS work_item_participants (
    workspace_id TEXT NOT NULL,
    task_id      TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    user_id      TEXT NOT NULL,
    role         TEXT NOT NULL,          -- owner | assignee | watcher
    created_at   BIGINT NOT NULL,
    PRIMARY KEY (workspace_id, task_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_work_item_participants_user
    ON work_item_participants(workspace_id, user_id, role);

CREATE TABLE IF NOT EXISTS work_item_events (
    workspace_id   TEXT NOT NULL,
    task_id        TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    event_type     TEXT NOT NULL,
    actor_user_id  TEXT NOT NULL DEFAULT '',
    payload        JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at     BIGINT NOT NULL,
    PRIMARY KEY (workspace_id, task_id, created_at)
);
CREATE INDEX IF NOT EXISTS idx_work_item_events_type
    ON work_item_events(workspace_id, event_type, created_at);
```

### 1.1 任务类型枚举（✅ `backend/internal/task/task.go`）

```
work     : dev ops project meeting doc comms admin
life     : errand family finance health
learning : study research review
other    : other
```

`TypeGroup(type) string` 为纯函数，非法 type 返回 `""`；写入侧（POST/PATCH）非法值 → HTTP 400。

### 1.2 任务 API

| 方法 | 路径 | 说明 | 状态 |
|---|---|---|---|
| GET | `/api/tasks?type=&group=&due=overdue\|today\|week&ownerId=` | 列表过滤 | 📐 Phase 1 |
| GET | `/api/tasks?type=learning` | 学习相关任务 | 📐 Phase 1 |
| POST | `/api/tasks` | 扩展字段：`type` `ownerId` `assignees` `dueAt` `remindAt` `parentId` `tags` `origin{kind,ref}` | ✅ 已支持写入 |
| PATCH | `/api/tasks/{id}` | 同上，可增量更新 | ✅ 已支持写入 |
| POST | `/api/tasks/from-source` `{sourceKind,sourceId,type?,title?}` | 笔记/邮件/RSS/会议 → 任务；会议按 action item 展开（忽略 `title`），按 `(originRef,title)` 幂等 | ✅ 已实现（P2） |
| GET | `/api/tasks/{id}/events` | **ACC 运行事件**投影（既有路由，勿占用） | ✅ 已存在 |
| GET | `/api/tasks/{id}/participants` | 协作参与者名单；非参与者读私有工作项返回 **403** | ✅ 已实现（P3） |
| PUT | `/api/tasks/{id}/participants` `{participants:[{userId,role}]}` | **整份替换**（PUT 语义），至多一个 `owner`；只写名单不发通知 | ✅ 已实现（P3） |
| GET | `/api/tasks/{id}/activity?limit=` | 协作活动流（新名字，避开上面的 `/events`） | ✅ 已实现（P3） |
| POST | `/api/tasks/{id}/activity` `{comment,eventId?}` | 追加评论事件；`eventId` 需 `client-` 前缀，用于移动端重试幂等 | ✅ 已实现（P3） |
| POST | `/api/tasks/{id}/delegate` `{userId,role}` | 委派：落 participants + `owner_id` + 写 `assigned` 事件 + 发通知 | ✅ 已实现（P3） |
| GET | `/api/tasks/{id}/approvals` | 审批投影**只读**视图 + `pending` 计数（回复路径归 agent 上游） | ✅ 已实现（P3） |
| GET | `/api/tasks/{id}/children` | 直接子任务 + 派生进度 `{children, progress}` | ✅ 已实现（P3） |
| POST | `/api/tasks/{id}/subtasks` `{title,type?,dueAt?,description?,assignees?}` | 在该工作项下建子任务；类型默认继承父级；写 `child_added` 事件（不发通知） | ✅ 已实现（P3） |

`parent_id` 变更（`PATCH /api/tasks/{id}`）在**写入前**校验：父级必须存在于同一 workspace、
不得自指、不得成环（`WouldCreateCycle`，走查预算 64 层，耗尽即 fail-closed）。

**进度语义**：`progress = {parentId, total, done, percent, blocked}`，由子任务状态**派生不落库**。
`total === 0` 时 `percent` 为 **0 而非 100**；百分比**向下取整**；
`completed` / `accepted` 计入 `done`，`blocked` 单独计入 `blocked` 不算进度。
只聚合**直接子任务**，不递归折进孙任务。

> **两处路径是被既有路由逼出来的，不是随手起的名**：
> `GET /api/tasks/{id}/events` 早已是 ACC 运行事件投影，所以协作活动流叫 `/activity`；
> `POST /api/tasks/delegate`（无 id）早已是「经 ACC 建任务」，所以委派到人走 `{id}/delegate`。
> `task_collaboration_route_test.go` 专门锁了「`/events` 没被劫持」这条。


请求/响应字段（camelCase，与既有 `/api/tasks` 一致）：

```jsonc
{
  "id": "t_1", "title": "接入 FSRS 服务端调度",
  "type": "dev", "typeGroup": "work",
  "status": "active", "priority": "normal",
  "ownerId": "u_1", "assignees": ["u_2"],
  "dueAt": 1790000000, "remindAt": 1789000000,
  "parentId": "", "visibility": "shared",
  "originKind": "note", "originRef": "n_9",
  "tags": ["p0", "learning-core"]
}
```

---

## 2. 学习域 DDL（✅ 已实现，落在 `backend/internal/learning/store.go:EnsureSchema()`）

```sql
-- 学习条目：笔记 / 邮件 / RSS / 会议 / 聊天 / 手工，统一收口
CREATE TABLE IF NOT EXISTS learning_items (
    id           TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    user_id      TEXT NOT NULL,
    source_kind  TEXT NOT NULL,       -- note|email|rss|meeting|chat|manual
    source_id    TEXT NOT NULL DEFAULT '',
    title        TEXT NOT NULL,
    summary      TEXT NOT NULL DEFAULT '',
    deck_id      TEXT NOT NULL DEFAULT '',
    stage        TEXT NOT NULL DEFAULT 'inbox',   -- inbox|learning|review|mastered|archived
    importance   INT  NOT NULL DEFAULT 3,
    captured_at  BIGINT NOT NULL,
    updated_at   BIGINT NOT NULL,
    deleted_at   BIGINT NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_learning_items_source
    ON learning_items(workspace_id, user_id, source_kind, source_id)
    WHERE deleted_at = 0;
CREATE INDEX IF NOT EXISTS idx_learning_items_stage
    ON learning_items(workspace_id, user_id, stage, captured_at DESC);

-- 学习提醒：定时提醒中枢（幂等唯一键防刷屏）
CREATE TABLE IF NOT EXISTS learning_reminders (
    id            TEXT PRIMARY KEY,
    workspace_id  TEXT NOT NULL,
    user_id       TEXT NOT NULL,
    kind          TEXT NOT NULL,     -- daily_digest|spaced_review|deadline|streak
    item_id       TEXT NOT NULL DEFAULT '',
    card_id       TEXT NOT NULL DEFAULT '',
    rule_kind     TEXT NOT NULL,     -- daily|interval|once
    rule_value    TEXT NOT NULL DEFAULT '',
    next_due_at   BIGINT NOT NULL,
    state         TEXT NOT NULL DEFAULT 'pending', -- pending|sent|acked|snoozed|done
    last_sent_at  BIGINT NOT NULL DEFAULT 0,
    snoozed_until BIGINT NOT NULL DEFAULT 0,
    created_at    BIGINT NOT NULL,
    updated_at    BIGINT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_learning_reminders_idem
    ON learning_reminders(workspace_id, user_id, kind, item_id);
CREATE INDEX IF NOT EXISTS idx_learning_reminders_due
    ON learning_reminders(workspace_id, user_id, state, next_due_at);
```

### 2.1 服务端调度器（✅ `backend/internal/learning/scheduler.go`）

```go
// ScheduleInput / ScheduleOutput 是纯函数 Schedule 的入参与出参。
type ScheduleInput struct {
    State            int     // 0=new 1=learning 2=review 3=relearning
    Stability        float64 // FSRS S
    Difficulty       float64 // FSRS D
    Rating           int     // 1=Again 2=Hard 3=Good 4=Easy
    ElapsedDays      float64
    LearningStepMin  int
    DesiredRetention float64 // 0.9 默认
    Now              int64   // unix 秒
}
type ScheduleOutput struct {
    State, Reps, Lapses int
    Stability, Difficulty float64
    IntervalDays float64
    Due          int64  // unix 秒
    Relearning   bool
}
```

- 纯函数、零 IO、**有单测**（`scheduler_test.go`）：覆盖 4 种评分 × 4 种状态、留存率方向性、
  难度夹取到 `[1,10]`、Again 必进 relearning 并计 lapse、间隔增长、脏输入兜底、确定性。
- 公式与参考实现对齐（对照物：仓库前端在用的 `ts-fsrs` 5.4.2），
  含 `hard_penalty = w15` / `easy_bound = w16` 的真实语义，详见 `03-架构方案.md` §3.2。
- 权重用 FSRS-5 17 维默认向量，与 `flashcards/types.go:57-60` 声明的同一向量
  （该 DDL 列为可空无 DB 默认）。

### 2.2 学习 API（✅ 已实现，路由 `/api/learning/*`）

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/learning/items` | 收集学习条目（`sourceKind/sourceId/title/deckId/importance`）；同源重复 → 返回已存在条目（幂等） |
| GET | `/api/learning/items?stage=&sourceKind=&limit=` | 列表（JWT user 权威） |
| GET | `/api/learning/items/due` | **今日到期摘要**：`{dueCards, inbox, reviewItems, dueTasks}` |
| POST | `/api/learning/reminders` | 建提醒（`kind/ruleKind/ruleValue/nextDueAt`）；幂等 upsert |
| GET | `/api/learning/reminders?state=` | 提醒列表 |
| POST | `/api/learning/reminders/{id}/snooze` | `{minutes}` 延后 |
| POST | `/api/learning/reminders/{id}/ack` | 确认（state=acked，停止重复推） |
| POST | `/api/learning/schedule` | **服务端权威调度计算**：入参 `ScheduleInput`，返回 `ScheduleOutput` |
| GET | `/api/learning/streak?tz_offset=` | **连续学习天数**：`{streak:{current,longest,lastActiveDay,activeToday}, milestone, next, today}` |

### 2.3 连续学习天数语义（P4c）

写在这里是因为它**最容易被误实现**：

- **派生不落库**，每次从活动日期重算（730 天窗口）。
  活动来源有**两个**：`learning_items` 的 `captured_at`/`updated_at`（收集 + 阶段推进）
  **与** `flashcard_revlog.reviewed_at`（闪卡复习）。
  **两者合并成一条连续记录**，不是分开统计：周一复习、周二收集，对用户是「连续两天」。
  只刷闪卡、不收集材料的用户同样能积累连续天数。
  计数器在崩溃 / 时区变更 / 漏 tick 之后需要修复，重算是自愈的。
- 连续记录**活过「最后一次学习日的次日」**：昨晚学了、今早还没打开 App，连续天数**仍是完整的**。
  朴素实现会在午夜清零，于是每天早上都给一个昨晚刚学过的人显示 0——而那恰恰是连续记录
  最该发力的时刻。
- 晚于 `today` 的时间戳（客户端时钟不准、节点跑太快）会被丢弃，否则未来的一天能满足
  「今天已学习」并虚增连续。
- 输入的日期**不需要预先排序或去重**，服务端会归一化。
- `tz_offset` 是**东八区秒偏移**（UTC+8 = `28800`；注意前端 `Date.getTimezoneOffset()`
  的符号是反的，`services/learning.ts` 里的 `localUtcOffsetSeconds()` 负责取负），
  缺省按 UTC 计算。界面上「0 天」与「查不到」必须是两件事：接口失败时整块不渲染。

**里程碑 exactly-once**：复用 `learning_reminders` 的唯一索引
`(workspace_id, user_id, kind, item_id)`，以 `kind='streak'`、`item_id='milestone:N'`
存一行作为「已播报」凭据——去重由 PostgreSQL 保证，**本轮刻意没有新增表**。
推送顺序是**先 claim 再发送**：推送失败只错过一次祝贺，而不是每天重复祝贺直到用户静音。

请求示例：

```bash
# 1) 把一封邮件收进学习
POST /api/learning/items
{"sourceKind":"email","sourceId":"em_42","title":"Go 1.25 GC 变更要点","importance":4}

# 2) 每天 20:30 回顾
POST /api/learning/reminders
{"kind":"daily_digest","ruleKind":"daily","ruleValue":"20:30","nextDueAt":1790000000}

# 3) 到点提醒推送的幂等键
#    (workspace_id, user_id, kind, item_id) 唯一 → 重复 POST 不会刷屏
```

### 2.3 提醒执行器（✅ `backend/internal/scheduledtask/executors/learning_digest.go`）

- 新增 `scheduledtask.KindLearningDigest Kind = "learning_digest"`。
- `Execute` 读当日摘要；**四项全 0 时不推送**（沿用 `flashcard_review` 既有"无事不打扰"行为）。
- 推送走 `notifycenter.Dispatch{Source:"learning", Kind:"learning.digest.daily"}`。

---

## 3. 通知契约扩展（📐 Phase 1 写入默认规则，Phase 4 落地 APNs/FCM）

| source | kind | priority 默认 | 收件人 |
|---|---|---|---|
| `work_item` | `assigned` | high | 被指派人 |
| `work_item` | `due_soon` | normal | 参与者 |
| `work_item` | `comment` | normal | 除发言者外参与者 |
| `work_item` | `completed` | low | 参与者 |
| `learning` | `review.due` | normal | 条目所有者 |
| `learning` | `digest.daily` | normal | 条目所有者 |

免打扰：`notification_rules.quiet_start_min=1350`（22:30）/ `quiet_end_min=450`（07:30），
静默期到达的提醒顺延到静默结束（由 executor 计算 `next_due_at` 时处理）。

---

## 4. 前端契约同步（📐 Phase 1）

| 文件 | 变更 |
|---|---|
| `frontend/src/api/client.ts` | 删除幻觉字段 `category?` / `owner?`，改用 `type` / `ownerId` / `assignees` / `dueAt` |
| `frontend/src/types/`（新增 `learning.ts`） | `LearningItem` `LearningReminder` `LearningDueSummary` `ScheduleInput/Output` |
| `frontend/src/services/learning.ts` | 对应 REST 封装（与 `services/flashcards.ts` 同风格） |
| `frontend/src/features/study/StudyHubView.vue` | 升级为学习中心：今日回顾 / 到期复习 / inbox / 来源跳转 |
| `frontend/src/features/tasks/TasksView.vue` | 按 `type_group` 折叠 + 协作面板 |
