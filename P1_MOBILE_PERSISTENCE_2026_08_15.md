# P1 移动持久化与审计 — 交付报告

**日期:** 2026-08-15
**状态:** ✅ 全部完成，测试通过
**上游:** 接续 SESSION_HANDOFF_2026_08_14.md（P0 完成于 cc5ab5d）

---

## 1. 移动离线持久化（最高优先级）✅

### SQLite Schema（`frontend/src/native/schema.ts`）
新增四组表，复用 `local_assets` 的 client_rev/server_rev/dirty LWW 模式：

- `local_mobile_sessions` — 客户端 id（`loc_` 前缀）+ server_id 回填 + 墓碑删除
- `local_mobile_messages` — pending/sent/remote 状态 + prompt 幂等键
- `local_mobile_approvals` — 审批快照 + 本地决定 + 过期标记
- `local_outbox` — PR13 `OutboxStorage` 的 SQLite 落地（幂等键唯一索引）

### 同步协议（conflict-free LWW）
- **版本号**: 服务端行 = 上游 `time.updated`（Unix ms）；本地行 = `updated_at` + dirty
- **pull**: 仅本地非 dirty 且远端版本更新时覆盖；dirty 行跳过（由 push 收敛）
- **push**: dirty 创建走幂等键重放 → 绑定 serverId；墓碑走上游 DELETE
- **游标**: 从数据取高水位（不用服务端时钟），避免时钟偏移漏拉

### 后端支持（Go）
- `GET /api/mobile/sessions?since=N` — 增量过滤（`OpenCodeSession.TimeUpdated` 新字段）
- `POST /api/mobile/sessions` + `Idempotency-Key` 头 — 重放缓存（workspace+instance+key 范围，24h TTL），防离线重放产生重复上游会话

### 集成测试
`frontend/src/native/__tests__/mobileSync.integration.test.mjs`：
断网建 session + prompt → 全落 SQLite → 恢复 → drain + sync 自动收敛 → 断言上游恰一个会话、prompt 送达、本地绑定 serverId、重复入队不产生重复实体。

## 2. 移动离线队列 ✅

- `SqliteOutboxStore`（`outboxStore.ts`）— 实现 PR13 `OutboxStorage`，put 幂等合并
- `drainOutbox`（`outboxDrain.ts`）— claim → 发送 → 成功删除 / 指数退避（full jitter, 60s cap）/ 超限死信；409（审批过期）终态；workspace 不匹配直接丢弃（SEC-06）
- `createMobileOutboxSenders` — `session.create` / `session.prompt` / `approval.reply` 生产发送器，成功后回写本地（绑定 serverId / 标记 sent）
- `mobileOffline.ts` — UI 离线写入口（建会话/发 prompt/审批回复 → 本地落库 + outbox 入队）

测试覆盖: 断网退避 → 恢复自动重放 → 死信 → 终态 → workspace 隔离。

## 3. 审计导出 ✅

- `redclaw.AuditStore.QueryRange` — 时间范围 + 不透明游标（编码 timestamp:id，同毫秒精确续传），**二分定位 O(log n)**，不全量扫描
- `Record` 保留调用方时间戳（零值才取当前时间）
- `GET /api/audit/export?format=jsonl|csv&start=&end=&cursor=&limit=` — admin 专用、租户范围强制取自 JWT；JSON Lines / CSV（引号转义）；`X-Audit-Next-Cursor` 分页续传

## 测试基建改进

新测试直接 **import 真实 TS 模块**（node 22 `--experimental-strip-types` + 显式 `.ts` 扩展名），替代 PR13 的镜像副本方式；SQL 跑真实 SQLite（`node:sqlite`，与生产同 schema）。

## 验证

```bash
# 后端（全量通过）
cd backend && go test ./... -count=1

# 前端
cd frontend
node --experimental-strip-types --test src/native/__tests__/*.test.mjs   # 21/21
node --test src/utils/__tests__/outbox.test.mjs                           # 13/13
npm run typecheck   # clean
npm run build:fast  # clean
```

## 文件清单

**前端新增:** `native/sqlDb.ts` `native/mobileSync.ts` `native/outboxStore.ts` `native/outboxDrain.ts` `native/mobileOffline.ts` + 3 个测试文件
**前端修改:** `native/schema.ts`（4 组表）`utils/outbox.ts`（.ts 导入）`tsconfig.json`（allowImportingTsExtensions）
**后端新增:** `server/mobile_idempotency.go` `server/mobile_session_sync_test.go` `server/server_audit_export.go` `server/server_audit_export_test.go` `redclaw/audit_range_test.go`
**后端修改:** `adapter/adapter.go` + `adapter/opencode_http.go`（TimeUpdated）`server/mobile_session_handler.go`（since + 幂等）`server/server.go`（路由 + 缓存注入）`redclaw/audit.go`（QueryRange）

## 遗留（下次会话）

- `local_mobile_approvals` 的 pull 侧回填（当前只有写路径；审批列表仍在线拉取）
- drain 循环的宿主接线（App 在 network-change/resume 事件触发 `drainOutbox` + `syncSessions`）
- 审计导出对接外部 SIEM / 落盘轮转策略
