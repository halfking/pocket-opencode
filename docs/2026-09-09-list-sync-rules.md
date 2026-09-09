# 列表数据拉取与更新同步规则（规范 v1）

- 日期：2026-09-09
- 状态：现行（as-built + 本次增量落地）
- 适用范围：opencode-pocket 全部前端列表/详情页与 pocketd 后端数据服务
- 关联文档：`docs/DATA_ARCHITECTURE.md`、`docs/2026-09-08-task-session-detail/PLAN.md`、`docs/2026-09-06-email-local-invoice.md`

---

## 1. 目标与原则

在数据传输量、及时性、用户体验三者间取最优：

1. **本地优先（Local-First）**：本地加密 SQLite（SQLCipher，`lobster`）是列表的第一数据源。列表页打开先读本地、即刻渲染，网络只用来"补差"。
2. **增量同步（Delta Sync）**：客户端用本地最大更新时间（或游标）向服务端只拉"有变化的行"，不重复拉全量。
3. **服务端是 SSOT（Single Source of Truth）**：本地是缓存与离线工作区；冲突以服务端为准做 LWW（Last-Write-Win），本地用户操作状态（如已读/星标）按领域约定豁免回写覆盖。
4. **上行异步、本地先行**：新增/修改先写本地 SQLite（立即生效），再经 outbox / dirty 队列异步同步到服务端，幂等重试。
5. **无刷新更新 + 可感知动画**：增量落库后差异合并进列表，不整页刷新、不丢滚动；新增/更新/移除必须有动画让人感知。
6. **列表与内容分离**：列表只拉瘦行（元数据），正文/大字段进入详情时按需加载（懒加载），后台可预取一屏。
7. **现场保持**：列表→详情→返回，筛选条件、分类、分区、滚动位置保持原样；仅当详情侧真的修改了影响列表的数据时才刷新列表，且刷新不改变筛选与状态。

---

## 2. 分层架构

```
┌─ 前端列表页（Vue3 + KeepAlive）
│    ① 读本地 SQLite/快照 → 立即渲染
│    ② 后台增量拉取（since/游标）→ 写入本地
│    ③ 差异合并进响应式列表 + TransitionGroup 动画
│
├─ 本地存储层
│    - SQLCipher SQLite（src/native/local-db.ts + schema.ts）：邮件/笔记/会议/
│      发票/待办/会话镜像/资产/草稿 等结构化表，记录 created_at/updated_at/来源
│    - list-sync 快照（src/native/list-sync/snapshot-store.ts）：远活列表
│      （如 OpenCode 实例会话）的 stale-while-revalidate 快照
│    - outbox（src/native/outboxStore.ts / outboxDrain.ts）：上行队列
│
├─ pocketd 后端（PostgreSQL/内存 store）
│    - 列表 API 支持 since / limit / cursor 增量 + deletedIds 墓碑 + serverTimeMs
│    - 正文独立端点按需返回（邮件 body、会话 transcript）
│
└─ agent-companion（智能体本地 → 服务端 → 客户端三方链）
     - companion 只读扫描智能体本机会话（5s 增量）
     - pocketd 经 CompanionClient 按需取元数据/正文（不落地正文）
     - 客户端只打 pocketd BFF，不直连 companion
```

---

## 3. 增量同步协议（后端 API 约定）

### 3.1 请求参数

| 参数 | 含义 | 约定 |
|---|---|---|
| `since` | 增量水位 | Unix 秒或毫秒均可（服务端归一；>1e12 视为毫秒）。只回传"更新时间戳 > since"的行 |
| `limit` | 单批上限 | 必须有默认值与上限（如默认 200 上限 500），防止全量放大 |
| `cursor` / `offset` | 分页 | keyset cursor 优先（`{id, created_at}` base64，见 `internal/server/cursor.go`），offset 兼容存量 |
| `include_content` 类 | 内容裁剪 | 正文一律不出现在列表端点，独立端点按需取 |

### 3.2 响应信封

```jsonc
{
  "<items>": [ /* 变更行（瘦行，无正文大字段） */ ],
  "total": 123,            // 可选：当前过滤条件下的总数
  "serverTimeMs": 1757400000000,   // 服务器当前时间（毫秒）——客户端校正时钟漂移
  "deletedIds": ["id1", "id2"]     // 软删除墓碑：deleted_at > since 的行 id
}
```

- **墓碑机制**：软删除（deleted_at）的行不再出现在列表里，客户端无从感知"别端删了什么"；带 `since` 请求时必须附 `deletedIds`，客户端据此移除/标记本地缓存行。
- **serverTimeMs**：客户端用它校正 `since` 基线，避免端侧时钟偏移导致漏单。
- **时间戳归一**：服务端各 filter 允许秒/毫秒混存（`stampAfterSince` 模式），对无时间戳的行 **fail-open 保留**，不允许因时间缺失清空数据。

### 3.3 已达标的端点（as-built）

| 领域 | 增量能力 | 位置 |
|---|---|---|
| 邮件列表 `GET /api/emails` | `since`（GREATEST(date, processed_at, created_at)）+ `deletedIds` + `serverTimeMs` | `server_assistant.go handleEmails`、`email/store.go ListEmailsScoped/ListDeletedEmailIDsScoped` |
| 邮件账户/日摘要/休假 | `since` 内存过滤 | `server_assistant.go` |
| 会议 `GET /api/meetings` | `since`（UpdatedAt）+ `deletedIds`（内存墓碑）+ `serverTimeMs` | `server_meeting_ingest.go`、`meeting/store.go` |
| 实例/会话/任务 | `since` 内存过滤（`server_since.go`，重建——该文件曾在并发会话事故中丢失导致 main 编译失败） | `server.go` + `server_since.go` |
| 移动会话 `GET /api/mobile/sessions` | `since`(ms) + `serverTimeMs`，游标存 SQLite | `mobile_session_handler.go`、前端 `mobileSync.ts` |
| 任务 local 源 / 审计 | keyset cursor（`PaginatedResponse`） | `cursor.go` |
| 笔记 `GET /api/notes` | `since`（updated_at）+ `deletedIds`（PG 软删墓碑）+ `serverTimeMs` | `server_assistant.go`、`notes/store.go ListDeletedIDsScoped` |
| 定时任务 `GET /api/scheduled-tasks` | `since`（updated_at）+ `deletedIds`（墓碑侧表 `scheduled_task_tombstones`，硬删除也可追溯）+ `serverTimeMs` | `scheduled_task_handler.go`、`scheduledtask/store.go` |
| 通知中心 `GET /api/notifications` | `since`（created_at，行不可变）+ `serverTimeMs`；无删除语义故无墓碑 | `server_notifycenter.go` |
| 聊天摘要 `GET /api/chat-summaries` | `since`（created_at）+ `serverTimeMs` | `server_chat_summary.go` |
| OpenCode 缓存会话 `GET /api/opencode/sessions` | `since`（CachedSession.UpdatedAt）+ `serverTimeMs` | `server_opencode.go` |
| 聊天角色 `GET /api/chat-agents` | `since`（UpdatedAt 秒，内存过滤）+ `serverTimeMs`；删除传播走 chatagent 云同步协议（版本比对），列表不带 deletedIds | `server_chatagent.go`、`server_since.go` |
| 任务会话正文 `GET /api/tasks/{id}/sessions/{sid}/transcript` | `after_seq`（keyset 游标，Seq > after_seq）+ `limit`，companion 透传；路由已于本次接线（原分发器缺失导致 404） | `server.go handleTaskOperations`、`companion_client.go GetTranscriptPage` |

**待改造**：无——全部列表端点已达 §3.2 信封。会议为内存 store，墓碑重启即失（客户端首次全量对账兜底）。

### 3.4 agent-companion 三方同步：回写链路边界评估（2026-09-09）

目标中的「companion 从智能体本地拉数据 → 与服务端对比 → 有变化写回服务端 → 同步更新客户端」，经评估**本仓库内不可完整实施**，边界如下：

| 环节 | 归属 | 现状 |
|---|---|---|
| 智能体本机文件扫描（nativestore，只读） | agent-companion 仓库 | 已实现（5s 增量、指纹去重）；但**容器化部署时 scanner 被禁用**（日志：`runtime scanner disabled: unsupported platform`），`/api/v1/native/*` 恒 400 |
| companion → pocketd 写回（对比后上行） | 需两侧同时改 | **不可单仓实施**：pocketd 有意不落地会话正文（上游 opencode sqlite 是 SSOT），没有可写的正文端点；companion 侧也没有 pocketd 写客户端 |
| pocketd → 客户端 | 本仓库 | ✅ 本次已打通增量：`GET /api/tasks/{id}/sessions/{sid}/transcript` 支持 `after_seq`（keyset）+ `limit` 透传（`CompanionClient.GetTranscriptPage`），消息行带 `seq` 供客户端续传 |

**替代方案（本仓库内可落地，推荐）**：不做服务端写回，改为**客户端驱动增量**——客户端经 pocketd 代理用 `after_seq` 增量拉正文 → 写入本地 SQLite 镜像（`local_mobile_messages` 已有 LWW 合并器 `mobileSync.ts`）→ UI 无刷新更新。数据流：`智能体本地 → companion（只读）→ pocketd（透传）→ 客户端本地库`，服务端保持无状态代理，避免双写一致性问题。端到端验证需 companion 以宿主机进程部署（`deploy-local.sh` 宿主模式，nativestore 可扫描到真实会话文件）；当前容器部署形态下仅能验证 pocketd 侧路由与降级（已验证：transcript 路由 404→503 接线确认）。

---

## 4. 客户端列表加载流程（每列表页的标准五步）

```
① 本地第一轮：读 SQLite 分页 / 快照 → 立即渲染（loading 仅在本地为空时出现）
② 后台增量：since = 本地 max(updated_at)（或持久化游标）→ GET 列表 API
③ 落库：响应行 upsert 进本地（尊重软删/豁免字段）；deletedIds 标记本地行
④ 差异合并：重读本地（或 LWW planner 合并）→ 替换响应式数组，不整页刷新
⑤ 动画：TransitionGroup 类动画呈现新增/更新/移除（§7）
```

参考实现（本次核对/补齐）：

- 邮件：`EmailInboxView.vue load()`（先 `showLocal()`，后台 `pullInboxFromServer()`：`syncAccountsFromServer` → `since=max(updated_at)` 增量拉 → 墓碑清理 → 重读本地）；失败不回滚本地列表。
- 发票：`use-invoice-list.ts`（本地页 + `pullServerPage` + `pushDirtyInvoices` 回推）。
- 会话列表（OpenCode 实例）：`sessions/SessionListView.vue`——远活数据，本地第一轮用 **list-sync 快照**（stale-while-revalidate），网络失败快照兜底（本次新增）。
- 笔记/会议/联系人/密码箱：纯本地 SQLite（数据在本机生产），服务端列表用于 ingest/汇总。

### 4.1 内容懒加载

- 列表瘦行不含正文：邮件列表只有 snippet/摘要，正文走 `GET /api/emails/{id}/body`（缓存优先，IMAP 兜底，落加密文件不入库）；会话列表只有元数据，正文走 `/transcript`、`/history` 按需端点。
- 后台可异步预取一屏正文（如 `email-body-cache`），进入详情时先查本地缓存、未命中再拉。

---

## 5. 上行同步（本地 → 服务端）

1. **本地先行**：新增/修改先写本地 SQLite，UI 立即反映；写路径打 `dirty`/outbox 标记。
2. **异步回推**：outbox drain / store 内 `pushDirty*` 按队列重放；未同步创建（serverId 空）→ create，已同步 → patch。
3. **幂等**：upsert 按 id 冲突覆盖；服务端 LWW 按 updatedAt（`usersetting/lww.go DecidePut` 模式）；409 冲突回传 `server_version`（chatagent sync 模式）。
4. **豁免字段**：用户操作状态不同步覆盖回本地（如邮件 `is_read`/`is_starred` 服务端不做权威回传，见 `emails-store.ts upsertEmail` ON CONFLICT 子句）。
5. **软删除一致性**：本端 purge → 本地 `deleted_at/body_purged` 阻止同步回填（`shouldSkipSyncWrite`）→ 服务端 `SoftDeleteEmailsScoped`（正文置空、保留标题/摘要）→ 其他端经墓碑 `deletedIds` 感知。

---

## 6. 现场保持（列表 ⇄ 详情）

机制（commit 1d61e54 起，全部列表页必须遵守）：

1. **KeepAlive 白名单**：列表页 `defineOptions({ name })` 并登记进 `use-list-scene.ts LIST_CACHE_NAMES`；详情/编辑页不缓存，每次进入重挂载拉最新。
2. **useListScene(scope, refresh)**：失活存 `#main` 滚动；激活时 `consumeListDirty(scope)` 为真才调 refresh，否则零请求；随后恢复滚动。
3. **详情页变更登记**：详情/编辑页真正改了影响列表的数据才 `markListDirty(scope)`（email/note/meeting/scheduled-task/pkm/vault 详情均已接线）。
4. **筛选/分类/分区状态**：存组件实例（KeepAlive 天然保留）或 store，禁止在 onActivated 重置。
5. 自管滚动容器（scrollMode:'self'）的列表 DOM 在 KeepAlive 内整棵保留，滚动天然不丢。

## 6.1 交互动线检查要求

- 详情页弹出→操作完成→关闭，必须回到前一页面的前一状态（筛选、分类、滚动、分区）。
- 仅当修正的数据对前一页面有影响时刷新前一页面，且刷新不得改变条件/分类/状态（dirty 按需刷新即为该规则的实现）。
- 新增列表页接线清单：见 `use-list-scene.ts`；邮件分类 chip、搜索状态、选中态均在实例上保留。

---

## 7. 列表动画规范

1. 列表容器使用 Vue `<TransitionGroup>`，命名 `*-list`（邮件 `elist`、会话 `slist`）。
2. 时长与缓动基准：进入 300–350ms ease（自上滑入 + 淡入）；移出 250ms ease（向右滑出 + 淡出，`leave-active` 置 `position:absolute` 让其余项直接补位）；移动 300ms ease（`move` 类 transition: transform）。
3. 容器需 `position:relative`（leave absolute 的定位上下文）；列表页容器本身参与滚动布局，动画不得引起页面级跳动。
4. 不带 key 的哨兵节点（如"加载更多"）放在 TransitionGroup **外**。
5. 禁止破坏滚动的"自动插入到视口顶部"：增量新行经动画进入，但列表不自动滚顶；实时批量新条目可沿用 `NewItemsBanner` 提示 + 显式刷新。

---

## 8. 本地数据库缓存同步规则（SQLite）

1. 每张镜像表带 `created_at`、`updated_at`（毫秒）、必要时的 `deleted_at`、`body_purged`、来源字段；`updated_at` 取**服务端时间戳**（有则用之），作为下次增量 `since` 基线。
2. 列表查询统一过滤 `IFNULL(deleted_at,0)=0`；软删行保留标题/摘要、正文置空。
3. upsert 一律 `ON CONFLICT(id) DO UPDATE`，豁免字段用 `local_emails.xxx` 保留本地值。
4. 迁移走 `local-db.ts` 增量迁移（幂等 ALTER/CREATE），与 `schema.ts` 同步演进。
5. 快照（snapshot-store）仅存列表瘦行 JSON，按 namespace+scope key 隔离；属尽力而为缓存，不可用时静默降级直连网络。

---

## 9. 邮件功能（本期需求核对）

| 需求 | 状态 | 实现 |
|---|---|---|
| 导航栏「归类」按钮，AI 逐封给未归类邮件打标 | ✅ 已有（本次复核） | `EmailInboxView` 归类按钮 → `runClassify`（`classifyInbox(20)` 循环、进度/可取消）；服务端后台批处理走 kxmemory（`classifyEmailsAsync`），WS `email.classified` 推送补齐 |
| 分类体系含 重要/垃圾/广告/通知 等，可批量处理 | ✅ | `INBOX_CATEGORY_CHIPS` + `normalizeEmailCategory`；分类 chip 即批量过滤入口 |
| 导航栏「删除」→ 列表前多选框 → 批量删除 | ✅ | select 模式 + checkbox（`email-inbox-select.ts`）；确认文案"正文将清空，仅保留标题和摘要" |
| 伪删除：内容置空、保留标题/摘要 | ✅ | 前端 `buildPurgePatch/purgeEmailsLocal`；后端 `SoftDeleteEmailsScoped`（snippet 置空、ai_summary 保留/折叠、body_path 清除、删磁盘正文） |
| 搜索：发件人/标题/时间范围/关键字，作用于当前分类列表 | ✅ | `email-inbox-search.ts` 本地过滤（与分类 chip 叠加） |

本次补强：其他端删除的邮件经 **`deletedIds` 墓碑**在增量拉取时同步软删本地（`syncEmailsFromServer` → `purgeEmailsLocal`），列表以动画移除——多端删除一致性此前缺失。

---

## 10. 本次变更清单（2026-09-09）

**后端**

1. `internal/email/store.go`：新增 `ListDeletedEmailIDsScoped`（墓碑查询，秒/毫秒归一）。
2. `internal/server/server_assistant.go`：`GET /api/emails` 响应增加 `deletedIds`（带 since 时）与 `serverTimeMs`。
3. `internal/meeting/store.go`：内存墓碑表 `tombstones` + `DeletedIDsSince`。
4. `internal/server/server_meeting_ingest.go`：`GET /api/meetings` 支持 `since`（秒/毫秒）、返回 `deletedIds`、`serverTimeMs`。
5. **修复 main 编译**：重建并发会话事故中丢失的 `internal/server/server_since.go`（`parseSinceQuery`/`filter{Instances,Sessions,Tasks,Vacations,EmailAccounts,EmailSummaries}Since`/`remoteTaskUpdatedAt`/`enrichSessionsFromCompanion`）；`adapter.RemoteTask` 补 `UpdatedAt`（毫秒，`ListRemoteTasks` 从会话 time.updated 填充）。

**前端**

6. `api/email.ts`：`listEmails` 返回类型加 `deletedIds?/serverTimeMs?`。
7. `emails-store.ts syncEmailsFromServer`：消费墓碑 → `purgeEmailsLocal`（软删 + 清正文缓存 + 防回填）。
8. `EmailInboxView.vue`：列表改 `<TransitionGroup name="elist">` + 动画 CSS（§7 规范的参考实现）。
9. `sessions/SessionListView.vue`：本地快照 stale-while-revalidate（`list-sync/snapshot-store`，scope=workspace+instance）+ 网络失败快照兜底 + `<TransitionGroup name="slist">` 动画 + `useListScene('sessions')` 现场保持（返回不重拉，滚动/筛选保留）。
10. `scheduled-tasks`：`api.list(enabledOnly, since)` 增量化 + 信封解析；store 增量合并（本地最大 updated_at 作基线、墓碑经 `deleteLocalSetting` 移除镜像行防僵尸复活）；`ScheduledTaskListView` 加 `<TransitionGroup name="tlist">` 动画。
11. `notifications`：`notificationsApi.list` 支持 `since`；`notification.ts loadInbox` 增量拉取并按时间归并（避免全量替换导致列表闪烁）。

**后端（第二批）**

12. `notes`：`GET /api/notes` 支持 `since` + `deletedIds`（`ListDeletedIDsScoped`，PG 软删墓碑）+ `serverTimeMs`。
13. `scheduledtask`：新增墓碑侧表（`scheduled_task_tombstones`，幂等迁移），`DeleteTaskScoped` 记录墓碑，`GET` 支持 `since` + `deletedIds` + `serverTimeMs`。
14. `notifycenter`：`GET /api/notifications` 支持 `since` + `serverTimeMs`。
15. `chat_summary`：`GET /api/chat-summaries` 支持 `since` + `serverTimeMs`。
16. `server_opencode.go`：`GET /api/opencode/sessions` 支持 `since` + `serverTimeMs`。
17. `server_since.go`：新增 `filter{Notes,ScheduledTasks,Notifications,ChatSummaries,CachedSessions}Since` 五个过滤器。

**后端（第三批）**

18. `chatagent`：`GET /api/chat-agents` 支持 `since`（UpdatedAt 秒内存过滤）+ `serverTimeMs`（`filterChatAgentsSince`）；删除传播走云同步协议版本比对。
19. `companion_client.go`：`companionMessage` 补 `seq`；新增 `GetTranscriptPage`（`after_seq` keyset + `limit`），`GetTranscript` 保持旧签名委托之。
20. `server.go handleTaskOperations`：补接丢失的路由注册——`GET .../session-bundle`、`GET .../sessions/{sid}/transcript`、`POST .../extract-title`、`POST .../summarize`（transcript 支持 `after_seq`/`limit` 透传；这些 handler 此前在并发事故中成为死代码 404）。
21. 回写链路边界评估结论写入 §3.4：服务端写回不可单仓实施，采用客户端驱动增量替代方案。

**验证**：后端 `go build ./...`、`go vet ./...`、`go test ./internal/{email,meeting,adapter,server,notes,scheduledtask,notifycenter,chatagent}/` 全绿；前端 `npm run typecheck` 通过；运行时信封探针（含 chat-agents 创建→增量可见→删除闭环、transcript 路由接线确认）与墓碑闭环、模拟器 UI 验证见 §11。

---

## 11. 真机/模拟器运行验证（2026-09-09，Android 模拟器）

环境：pocket_clone AVD（emulator-5554），pocketd 本地构建（含本次全部改动，PG 模式）于 `:8088`，APK `VITE_API_BASE=http://10.0.2.2:8088`。证据目录 `test-evidence/2026-09-09-list-sync-verify/`。

| 验证项 | 结果 | 证据 |
|---|---|---|
| 后端信封运行时探针（7 端点均返回 since 信封） | ✅ | curl 探针输出（deletedIds/serverTimeMs 全部在场） |
| 定时任务墓碑闭环：创建→since 可见→删除→deletedIds 下发→全量消失 | ✅ | id `9dd6f068…` 全流程 |
| 邮件页本地第一轮加载（248 封即时渲染）+ 导航栏四件套 | ✅ | `state-1-bill-category.png` |
| 列表→详情→返回保现场：选「账单」分类→进详情→返回，分类保持「账单」不跳回「全部」，滚动位置保持；详情自动已读后返回按 dirty 刷新且筛选不变 | ✅ | `state-1~4` 四连截图 |
| 多选软删除：复选框出现、确认文案「正文将清空，仅保留标题和摘要」、删除后无整页刷新就地移除 | ✅ | `select-mode.png`、`confirm-dialog2.png`、`after-delete.png` |
| 列表项动画（leave 滑出 + move 补位） | ✅ | `anim_delete.mp4`（过渡帧可见卡片重影/位移） |

## 12. 遗留缺口（后续按本规范改造）

1. 会议为内存 store：需持久化 + 持久墓碑，重启后增量不丢。
2. snapshot-store 落 SQLite（`local_list_snapshots` 表已建，当前实现为 localStorage），原生壳统一走加密库。
3. companion 增量正文链的端到端验证：需 agent-companion 以宿主机进程部署（nativestore 可扫描真实会话）；容器部署 scanner 禁用（见 §3.4）。前端 `after_seq` 续传消费（拉增量正文写 `local_mobile_messages` 镜像）按 §3.4 替代方案接线。
4. 移动会话（`mobileSync`）与实例会话（`/api/sessions`）两域并存：实例会话远活属性强，保持快照策略；`since` 游标化待 upstream 稳定后接入。
5. 邮件正文懒加载在本地 dev 无 IMAP fetcher 时优雅降级（真机已验证提示路径），正文链路需在配齐 fetcher 的环境补测。
