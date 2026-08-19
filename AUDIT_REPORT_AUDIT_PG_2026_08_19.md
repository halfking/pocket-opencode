# 代码审计报告 — Audit PG 持久化（cursor 精度 + Record 并发唯一性）

**审计日期**: 2026-08-19
**审计范围**: audit PG 持久化两处生产代码变更（commit `59191b5` + `6aaacd5`）
**模块**: `backend/internal/redclaw`（audit.go, audit_pg.go）
**审计标准**: comprehensive-code-audit（数据溯源 / 业务流程闭环 / 状态机 / 并发安全 / 数据兼容）
**当前 HEAD**: `6892ba5`（main，已与 origin/main 同步；本任务两个 commit 已并入）

---

## 0. 变更摘要（被审计的代码）

| 文件 | 变更 | 目的 |
|------|------|------|
| `internal/redclaw/audit.go` | `encodeAuditCursor`/`decodeAuditCursor`：`UnixMilli()` → `UnixNano()` | 修复游标跨页**重复计数** |
| `internal/redclaw/audit_pg.go` | `Record` ID 生成：加 `sync/atomic` 计数器，ID = `aud_<nano>_<seq>` | 修复并发写入**丢数据** |
| `internal/redclaw/audit_cursor_precision_test.go`（新增） | 内存态亚毫秒 cursor 回归 | 覆盖修复 |
| `internal/redclaw/audit_pg_cursor_test.go`（新增） | PG 态亚毫秒 cursor / same-ts 消歧 | 覆盖修复 |
| `internal/redclaw/audit_pg_concurrent_test.go`（新增） | 8×50 并发 Record+QueryRange，断言 400/400 耐久 | 覆盖修复 + 暴露并发 bug |

---

## 1. 数据溯源（Data Flow Traceability）

**写入链路（Source → Destination）**
- **Source**: `server.Write` / `server.WriteWithClaims`（HTTP handler、scheduler、worker）构造 `AuditEntry`；字段来源：
  - `Action` / `Resource`：调用方按业务命名（`vault.sync.upload` 等）。
  - `UserID` / `TenantID`：从 `auth.Claims` 派生（system tenant 走 `auditSystemTenant` 常量）。
  - `Detail`：`audit_writer` 在写入前做脱敏 + 长度截断（独立关注点，本次未改）。
  - `Timestamp` / `IP`：helper 自动填充（`time.Now()` / `X-Forwarded-For`）。
- **Destination**:
  - PG 路径：`PGAuditStore.Record` → `INSERT INTO audit_entries (...) ON CONFLICT(id) DO NOTHING`。
  - 内存路径：`AuditStore.Record` → 追加到加锁 slice。
- **读出链路**: `QueryRange` → 游标分页 → `/api/audit/export`（jsonl/csv）与 `/api/audit/logs`。

**每个字段均有明确用途与落库/回读路径**；无孤儿字段（详见 server 层 `audit_writer.go` 的脱敏规约）。✅ 通过。

---

## 2. 业务流程闭环（Business Process Closure）

**审计导出分页循环**（P1 核心）
```
QueryRange(StartTime, Limit) → 收集 → if NextCursor=="" break; else 带 AfterCursor 续页
```
- **Initiation**: `StartTime` / `AfterCursor` 起点明确。
- **Termination**: `NextCursor` 为空即终止；游标 `(timestamp,id)` 严格单调递增，必然推进。
- **Recovery / 超时**: 单次 `QueryRange` 受 `context.Background()`（无显式超时，但 PG 侧有连接级超时兜底）。
- **幂等**: `Record` 的 `ON CONFLICT(id) DO NOTHING` 保证同 ID 重试安全。

分页测试（`TestPGAuditStore_QueryRangeCursorPagination` 等）已验证：分页终止、无空洞、无重复 ID、跨页时间有序。✅ 闭环完整。

---

## 3. 状态机（State Machine）

审计存储是**只追加日志（append-only）**，无多状态转换，无状态机。相关不变量：
- **幂等性**: `id` 为主键；修复后每次写入 ID 唯一 → `ON CONFLICT` 仅在「调用方显式传入相同 ID 的重试」时触发，属正确语义（不再误吞不同写入）。✅

---

## 4. 并发安全（Concurrency Safety）— 本次关键发现

### 4.1 发现并修复的 bug：Record ID 碰撞导致静默丢数据（P0→已修复）
**根因**: 原 ID 生成 `aud_%d_%d` = `UnixNano()` + `UnixNano()%1e6`，两部分同源。macOS `time.Now()` 时钟分辨率较粗，并发（或同纳秒）`Record` 会得到相同 ID → `ON CONFLICT(id) DO NOTHING` 把**不同的审计条目静默丢弃**。

**复现证据**: 新增并发测试 `TestPGAuditStore_ConcurrentRecordQueryRange`（8 goroutine × 50）：
- 旧方案：400 次写入仅持久化 **397** 条（丢 3 条）。
- 修复后（`atomic` 计数器保证 ID 唯一）：**400/400** 耐久。

**修复**: `pgAuditIDSeq uint64` + `atomic.AddUint64`，ID = `aud_<UnixNano>_<seq>`，进程内严格递增，主键值唯一。

### 4.2 其他并发路径
- **内存 `AuditStore`**: `Record` 用 `mu.Lock()`，`QueryRange` 用 `mu.RLock()` → 读写安全（`TestAuditLog_ConcurrentAccess` + 新增亚毫秒测试在 `-race` 下通过）。✅
- **PG `PGAuditStore`**: 无共享可变 Go 状态（仅 `pgAuditIDSeq` 为 atomic）→ 无数据竞争；行级并发由 PG 保证。✅
- **数据竞争检测**: `go test -race ./internal/redclaw/... ./internal/server/...`（带 DSN）全绿。✅

---

## 5. 数据兼容性（Data Compatibility）

| 变更 | 兼容性判断 |
|------|-----------|
| 游标格式 `UnixMilli` → `UnixNano` | 游标是**不透明字符串**，仅 `QueryRange` 内部往返使用，无外部解析器。旧 ms 精度数据仍可解码（nanos = ms×1e6，精确）。✅ |
| 条目 ID 格式 `aud_<nano>_<nano%1e6>` → `aud_<nano>_<seq>` | `id` 仅作主键，无外部依赖；新格式更唯一。历史行不受影响（主键不变更）。✅ |
| `POCKET_TEST_POSTGRES_DSN` 指向 `kaixuan` 而非 `llm_gateway` | `llm_gateway` 库有 `citus_columnar` 的 `enforce_columnar_trigger` 事件触发器，在 `search_path` 被钉到隔离 schema 时建表失败；`kaixuan`（生产 pocketd 实际使用）无此触发器。✅ 属环境选择，非代码不兼容。 |

---

## 6. 发现项（Findings）

| 级别 | 项 | 状态 |
|------|----|------|
| P0 | Record 并发 ID 碰撞静默丢数据 | ✅ 已修复（atomic 计数器） |
| P0 | 游标 ms 精度丢失导致跨页重复计数 | ✅ 已修复（UnixNano） |
| P2 | `PGAuditStoreWithPool` 生产侧未被使用（仅被自身测试引用） | ✅ 已决策 A：删除 wrapper 及其专属测试；生产路径直接使用 `NewPGAuditStore`，nil pool 由调用方处理 |
| P2 | 16 个 store 各自内联 `CREATE TABLE`，无统一 schema 引导 | ✅ 已决策：保持 store-local 内联 DDL；不移入会话迁移包，也暂不引入统一 bootstrap，维持独立初始化与隔离 schema 测试范式 |

**正面亮点**: 测试沿用 lobster/identity 的「隔离 schema + 无 DSN 则 skip」模式，可在 CI 与本地无缝切换；E2E 已用真实 `pocketd` 启动验证 HTTP→PG 全链路持久化与回读。

---

## 7. 验证证据（Verification）

```
# 带真实 PG（llm-gateway-pg / kaixuan），-race
go test ./internal/redclaw/... ./internal/server/... -race -count=1   → ok / ok

# 无 DSN（CI 路径，PG 测试 skip）
go test ./internal/redclaw/... ./internal/server/... -count=1          → ok / ok

# E2E（真实启动 pocketd，连 kaixuan，schema opencode_pocket_e2e）
POST /api/auth/login(admin/admin) → token
GET  /api/audit/export?format=jsonl (Authorization: Bearer $TOKEN) → 200
  → 在 kaixuan.opencode_pocket_e2e.audit_entries 写入 audit.export 行
GET  /api/audit/export 二次 → 从 PG 回读出该行（写+读全链路验证）
```

**总体评级**: A（两处 P0 并发/精度缺陷已修复并有回归测试覆盖；E2E 全链路验证通过）。
