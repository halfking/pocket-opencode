# P1 契约冻结（2026-08-27，主代理签发）

> 本文档是 P1 多子代理并行的**唯一契约依据**。schema 由主代理先于一切实现冻结
> （设计文档 §5.1/§5.2 授权），子代理 B 负责在 Go 侧镜像实现 + 契约测试锁定，
> **不得单方面改字段名/语义**；确有 Go 侧不可满足之处，在报告中上报，由主代理
> 统一修订本文件与 TS 类型后再同步。

## 1. 范围与命名空间（子代理只改自己命名空间，禁改他人文件）

| 子代理 | 可改动范围 | 禁止 |
|---|---|---|
| B 后端事件 | `backend/internal/opencode/session_event_broadcaster.go`(+`_test.go`) 新建；`backend/internal/server/` 新建 mobile_events_handler + `server.go` **仅追加路由行**；`cmd/pocketd/main.go` **仅追加装配**；`frontend/src/services/__tests__/sessionEvents.test.mjs` 新建 | 改 `frontend/src/services/sessionEvents.ts`（已冻结）；改 `backend/internal/agent/**`（E 的领地） |
| A 会话工作台 | `frontend/src/features/sessions/SessionConversationView.vue` + 新建 `features/sessions/{RoundTimeline.vue,SessionStatusBar.vue,SessionDetailDrawer.vue,useSessionEvents.ts}`(+测试) + `frontend/src/app/router-mobile.ts`（301 收敛） | 改 `features/opencode/SessionDetailView.vue`（被收敛对象，只加路由 301 不改文件）；改 Composer（C 的领地） |
| C 输入系统 | 新建 `frontend/src/features/sessions/{SessionComposer.vue,useSessionDrafts.ts}`(+测试) + `frontend/src/native/{draftStore.ts}` + `frontend/src/native/schema.ts`（**仅追加** draft 表迁移） | 改 `SessionConversationView.vue`（A 的领地，接线由 A/主代理完成） |
| D 真机验证 | 只写 `test-evidence/P1-mobile-ux/device-verification-2026-08-27.md`；adb 只读操作 + 装包 | 改任何 src 代码 |
| E 存量红 | worktree `/tmp/opencode-pocket-e`（分支 `fix/p1-backend-stdio-tests`）内 `backend/internal/agent/**` | 在主工作树改动 |

## 2. 事件 wire schema（冻结）

三层信封完全复用 approval 链路现状（`approval_broadcaster.go:37-53` ↔ `idempotentWsBus.ts:31-44`）：

```
WS 线上 JSON: {type, payload}                  ← hub.go Message 外层
payload: WsEnvelopeV1 {v:1, id, ts, channel, topic, type, data, cause?}
data:    业务 payload（下表）
```

| 事件 | channel | topic | cause | 业务 payload 字段 |
|---|---|---|---|---|
| `session.activity` | `sessions` | sessionID | 无 | `instance_id, session_id, phase, last_event_at(epoch ms), round_index` |
| `round.completed` | `sessions` | sessionID | `correlation_id: "<session_id>:<round_index>"` | `instance_id, session_id, round_index, summary, changes{added,removed,files}, status, completed_at(epoch ms)` |
| `task.health` | `tasks` | taskID | 无 | `task_id, instance_id?, health, pending_count, computed_at(epoch ms)` |

- **枚举（冻结）**：`phase ∈ thinking|tool|file_write|pty|idle`；`status ∈ completed|error|cancelled`；`health ∈ needs-input|stalled|error|running|idle`（与 `features/tasks/health.ts` 五态一字不差）。
- **round_index**：1-based，按会话内用户 prompt 序数递增；前端历史轮次按"用户消息边界"客户端分组使用同一编号规则。
- **event_id**：`session_activity_` / `round_completed_` / `task_health_` + UnixNano + 原子序列（照抄 `approval_broadcaster.go:276-279` 模式）。
- **节流**：`session.activity` ≥30s 或 phase 切换才发；`task.health` 5s 合并；`round.completed` 每轮一条。发送侧 500ms coalescing。
- TS 侧类型唯一来源：`frontend/src/services/sessionEvents.ts`（已冻结落盘）；Go 侧镜像 struct 以注释互指。解析采用双层解包（`env.data` = WsEnvelopeV1，`env.data.data` = 业务 payload），见同文件 parse 函数。

## 3. 快照追赶端点（§5.2.2 P1 落地形态）

`GET /api/mobile/events/snapshot`（requireAuth，与 approvals 同鉴权）→ 200 JSON：

```json
{ "sessions": [ { "instance_id": "...", "session_id": "...", "health": "running|null",
  "phase": "tool|null", "round_index": 3, "last_event_at": 1234567890123|null,
  "latest_round": <RoundCompletedData|null> } ], "generated_at": 1234567890123 }
```

内存态即可（EventStream 活跃会话的最新快照 + 最近一轮缓存）；不落库、不做事件回放。重连后前端拉一次快照对齐，幂等由业务层"最新轮次/最新 phase 覆盖"保证。

## 4. SessionComposer 组件契约（C 实现 / A 挂载）

```ts
// frontend/src/features/sessions/SessionComposer.vue
interface SessionComposerTarget { id: string; label: string }
Props: {
  sessionId: string          // 固定目标模式：当前会话（草稿 key）
  sessionLabel?: string      // chip 文案，默认 sessionId 截断
  targets?: SessionComposerTarget[]  // 可切换目标模式（P1 仅契约就绪，工作台用固定模式）
  modelTarget?: string       // targets 模式下 v-model:target
  disabled?: boolean         // 外部 sending 等禁用
  initialText?: string       // ?prompt= 一次性预填
}
Emits: {
  (e: 'send', text: string): void        // 已确认发送；组件内已清草稿
  (e: 'update:target', id: string): void // 仅 targets 模式
}
```

行为冻结：指令模板 chips `继续 / 停下 / 总结当前进展 / 跑测试 / 忽略错误继续`，一点即发（emit send），仅 `停下` 先二次确认；voice-bar/STT 转写入草稿（承接 P0 纪律，不直发）；草稿按 sessionId 存 SQLite（`drafts(session_id TEXT PRIMARY KEY, text TEXT, updated_at INTEGER)`），输入 500ms 防抖落盘，send 时清除；发送按钮 ≥44px 热区贴右下拇指区 + `env(safe-area-inset-bottom)`；颜色用既有 CSS 变量（本工程无 Vant，遵循工程内既有组件风格 + mobile-ui-generator 的热区/safe-area 纪律）。

## 5. 各代理验证要求（报告必须附命令与输出摘要）

- B：`go test ./internal/opencode/ ./internal/server/ -count=1` + `go vet ./...` 绿；Go fixture 测试 + TS fixture 测试（node --test）双向锁定信封形状。
- A：`npm run typecheck` + 相关 vitest/node --test 绿；旧详情路由 301 生效的证据（路由表 diff）。
- C：`npm run typecheck` + draftStore/composer 单测绿（SQLite 用现有 sqlDb 测试基建）。
- D：adb 装包 + P0 链路逐项验证结果（含失败项原样记录）。
- E：`go test ./internal/agent/ -race -count=1` 绿 + 全包回归结论。

## 6. 编排事实

- A/C 不再等待 B 完成：主代理已冻结 §2/§3 契约并落盘 TS 类型，A/C 直接 import `sessionEvents.ts` 开工（B 只镜像不改 TS）。
- 集成顺序：B/D/E/A/C 全部并行 → 主代理接线 Composer → 全量门禁 → 治理三件套 → 单 commit。

## 7. 偏差备案（集成阶段主代理裁决，2026-08-27）

| 偏差 | 提出方 | 裁决 |
|---|---|---|
| 草稿表名 `local_drafts` 而非 §4 字面 `drafts` | C | **接受**——遵守 schema.ts 既有"所有表带 `local_` 前缀"设计原则；列名/类型/约束与契约一字不差（`draftStore.test.mjs` 锁定） |
| Composer prop 名 `modelTarget` 导致 `v-model:target` 简写不可用（Vue 要求 prop 名为 `target`） | C | **接受并登记**——P1 工作台只用固定模式不受影响；P2 启用可切换模式时统一修订（改名 `target` + `update:target`） |
| 快照 `health`/`last_event_at` 的 null 语义收窄为"任务映射中存在但从未见过事件的会话"（全 null 行） | B | **接受**——§3 null 用法的子集，TS 类型不变 |
| 任务→会话映射复用 DB（`task_session_links`，Postgres）而非内存态 | B | **接受**——比契约预估的内存降级更强；无 PG 部署下 task.health 不产事件（快照端点仍可用），已在 EVIDENCE-LEDGER 风险项登记 |
| 旧 opencode 详情路由在 router-mobile.ts 中从未注册（`opencodeRoutes` 死链） | A | **登记**——301 以新增 redirect 落地兜住旧入口链与深链，`features/opencode/routes.ts` 与 `SessionDetailView.vue` 未动 |
