# 06 · 架构决策记录（ADR）

**日期**：2026-09-30 · 状态：定稿

---

## ADR-001 · 工作与任务合为一个实体，用 `type` 分类

**背景**：用户明确「工作与任务应该可能是一起的，只是根据任务类型分类」。
现状 `tasks` 表把用户任务与 `source='opencode'` 的**会话投影**混存（`server.go:1157-1200`），
已有分类诉求但无字段；前端甚至声明了后端不返回的 `category`/`owner`（`api/client.ts:62,67`）。

**决策**：不新建 work 表，**沿用 `tasks` 增量加列**；分类用受控枚举 `type` + 派生 `typeGroup`；
旧数据 `DEFAULT 'other'`，非法值 400 拒绝，不做静默兜底。

**后果**：
- ✅ 零迁移风险、历史行不需回填；
- ✅ 消除前端幻觉字段；
- ⚠️ 会话投影与真实任务同表的问题**仍在**（`type='other'` 无法区分二者），
  缓解手段：`source` 保留三源语义，UI 默认按 `source=local` 过滤（P1 落地）。

**替代方案**：新建 `work_items` 表并双写 → 放弃（两套真源必然不一致）。

---

## ADR-002 · 间隔重复调度下沉到服务端

**背景**：既有契约冻结「FSRS 调度在客户端 via ts-fsrs，后端仅存储」
（`docs/flashcards-contract.md:190-193`，代码 `flashcards/cards.go:17-20` 显式不重算 due/state）。
后果：换端/离线/多端时同一张卡 due 不一致，**提醒算错**。

**决策**：
1. 服务端实现调度器（Go 纯函数，FSRS-5 形态，字段与既有 `flashcard_cards` 对齐）；
2. **P0 不推翻既有契约**：服务端调度先用于「提醒侧到期统计」与 `/api/learning/schedule` 预览；
3. **P4 切写入真相**：`POST /api/flashcards/cards/{id}/review` 改为由服务端计算并返回
   due/state/stability/difficulty，前端 `ts-fsrs` 降级为乐观预览；契约同步增补条款。

**后果**：
- ✅ 多端一致、离线回归后以服务端为准；
- ✅ 提醒（学习模块的核心诉求）不再依赖某台设备是否打开过 App；
- ⚠️ P0–P3 期间存在"前端算 + 服务端算"两份实现，**只用于不同用途**（预览 vs 到期统计），
  不允许两边同时写 `due`（写入真相唯一）。

**替代方案**：把 ts-fsrs 编译成 WASM 在服务端跑 → 放弃（运维与可读性成本高，纯函数 Go 更可测）。

---

## ADR-003 · 学习域只存引用，不复制正文

**背景**：笔记、邮件、RSS 各自有完整存储（`notes` / `emails` / `rss_items`）。
若学习模块复制正文，会出现双写与不一致（邮件正文还有 `body_path` 文件存储）。

**决策**：`learning_items` 只存 `source_kind + source_id + title + summary + stage`，
**内容真源仍在各自主域**，点回原文由前端按 `source_kind` 路由到既有详情页。

**后果**：
- ✅ 单一真源，删除源内容不会留下孤儿正文；
- ⚠️ 源内容删除时 `learning_items` 会指向不存在的引用 → P2 加"源删除钩子"置 `deleted_at`。

---

## ADR-004 · 提醒幂等靠唯一索引，不靠应用层判断

**背景**：轮询型调度器最容易出的事故是**重复提醒刷屏**。

**决策**：
- `learning_reminders` 建唯一索引 `(workspace_id, user_id, kind, item_id)`，
  应用层用 `ON CONFLICT ... DO UPDATE` 做幂等 upsert；
- `learning_items` 建部分唯一索引 `(workspace_id, user_id, source_kind, source_id) WHERE deleted_at=0`；
- 沿用既有 `flashcard_review` 的"**无事不打扰**"行为（`due_count<=0` 不推送）。

**后果**：即使定时任务重复触发、用户重复点按钮，也不会产生第二条提醒。

---

## ADR-005 · 新域不引入迁移框架

**背景**：仓库所有模块的 DDL 都写在包内 `migrate()`/`EnsureSchema()`，用
`CREATE TABLE IF NOT EXISTS` + `ALTER ... ADD COLUMN IF NOT EXISTS`；无版本化迁移目录。

**决策**：新模块照抄该范式（`backend/internal/learning/store.go`），
`ADD COLUMN IF NOT EXISTS` 在 PG 11+ 只改元数据，不会长时间锁表。

**后果**：✅ 与仓库一致、零学习成本；
⚠️ **已知债**（`02-现状盘点.md` §1.1）：历史库状态不可复现、无回滚脚本。
本方案不修，但把它列入 Phase 4 的"契约变更登记"里显式记录。

---

## ADR-006 · 协作复用既有 workspace，不新建团队体系

**背景**：`identity` 已有 `workspaces` / `workspace_members`（角色 `owner|invitee`，邀请上限 3）
与 `tasks.workspace_id` 行级隔离。

**决策**：协作 = workspace 成员 + `work_item_participants`（owner/assignee/watcher）+ `visibility`。
不引入组织/团队/部门层级。

**后果**：✅ 范围可控、与既有租户隔离一致；
⚠️ 邀请上限 3 对多人协作场景偏紧 → 列为 Phase 3 的待评估项（`identity/store.go:38`）。

---

## ADR-007 · 陈旧提醒静默退役，不发补偿通知

**状态**：定稿（**本轮明确不做**，理由如下，不是待办）

**背景**：P4 给工作项提醒加了 24h 陈旧上限（`DefaultStaleAfter`），
超龄的 `remind_at` 静默归零、只计入 `stale` 计数
（`executors/workitem_reminder.go:150-160`）。这留下一个悬而未决的问题：
**用户设的提醒被丢掉了，要不要告诉ta一声？**

这不是 bug——现有设计已经回答了「提醒退役后去哪看」：
> The work item itself still shows up as overdue in the task list,
> which is where a stale reminder belongs.

即：工作项仍以逾期形态留在任务列表里。**可见性不缺，缺的只是「戳一下」。**

**决策**：**保持静默，不加补偿通知。**

**理由**：
1. **补偿通知会摧毁陈旧上限本身的目的。** 上限存在的意义是把通知量
   绑定在陈旧窗口上，而不是绑定在「服务停了多久」上。若逐条补偿，
   停机一周就会回来 7N 条通知——正是这个上限要防的。
2. **若要做摘要，又需要一个新的幂等锚点。** 现成的幂等靠
   `event_id = reminder:<remind_at>` + `work_item_events` 主键，
   而那张表是 **task 作用域**的；工作区级摘要没有可复用的锚点，
   要么新增 DDL，要么引入限流状态。**在所有新 DDL 都还没在真实库上验过的当下，
   为一个「锦上添花」扩大未验证面是错误的取舍。**
3. **补偿内容会是噪声。** 「你 3 天前设的提醒已过期」——用户此刻要的是
   任务列表里那条逾期项本身，告诉他「提醒被丢了」反而制造了一个
   需要额外处理的新对象。

**后果**：
- ✅ 停机恢复不会产生通知风暴；陈旧窗口是通知量的唯一上界；
- ✅ 不新增任何未验证的 DDL；
- ⚠️ **已知且接受的代价**：若用户设完提醒就忘了这件事，ta 不会收到
  「提醒已过期」的通知。缓解手段是任务列表的逾期视图——这是产品选择的
  结果，不是遗漏。若日后要改，更合理的形态是**在任务列表里显示
  「提醒已过期」徽标**（读时派生、零 DDL），而不是推送。

**替代方案**：
- 逐条补偿通知 → 放弃（理由 1）；
- 工作区级摘要 + 新的幂等锚点 → 暂缓（理由 2），待真实库验证条件具备后重议；
- 读时派生的逾期徽标 → **推荐的后续形态**，零 DDL、可后加。
