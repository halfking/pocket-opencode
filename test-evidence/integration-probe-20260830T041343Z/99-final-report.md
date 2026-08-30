# OpenPocket → RedClaw → ACC → Memora 集成探针最终报告

**执行时间**: 2026-08-30T04:13:43Z  
**探针类型**: 本地 Docker 环境最小真实集成  
**状态**: **BLOCKED** - ACC 服务未能成功启动

---

## 执行概要

### ✓ 成功完成的阶段

1. **阶段 0：安全前置检查**
   - 工作树状态：所有修改均为本次会话相关，无他人未提交文件冲突
   - Docker 服务：PostgreSQL (5432)、Qdrant (26333)、Redis (16379)、RedClaw facade (27001) 全部运行正常
   - 证据目录：已创建时间戳归档目录
   - 环境配置：已脱敏记录所有环境变量键名

2. **阶段 1：服务可达性与认证验证**
   - **RedClaw Facade** ✓
     - 端口 27001 可达
     - `/api/v2/capabilities` 返回 200
     - ACC adapter: configured
     - Memora proxy: configured
     - 无认证访问正确返回 401 + 完整错误 envelope
     - JWT 认证机制工作正常（使用实际容器密钥）
   
   - **ACC** ✗ BLOCKED
     - acc-go 直接启动失败：task-assigner 后台 goroutine 在 PG pool 初始化完成前启动
     - 访问 nil pool 导致 panic (SIGSEGV)
     - 需要修复 main.go 的服务初始化顺序
     - RedClaw facade 转发请求返回 `503 dependency_unavailable`

### ✗ 阻塞的阶段

3-9. **所有后续阶段均因 ACC 不可用而阻塞**

---

## 详细验证结果

### RedClaw Facade 认证与错误 Envelope

| 验证项 | 结果 | 证据 |
|--------|------|------|
| 无认证访问 | ✓ PASS | HTTP 401, error.code=unauthorized, request_id 存在 |
| JWT 签名验证 | ✓ PASS | 错误密钥返回 invalid_token，正确密钥接受 |
| correlation_id 回显 | ✓ PASS | 请求与响应 correlation_id 一致 |
| 错误 envelope 格式 | ✓ PASS | `{error:{code,message,retryable}, request_id, correlation_id}` |

### 任务创建链路

| 步骤 | 状态 | 详情 |
|------|------|------|
| OpenPocket → RedClaw facade | ✓ 请求到达 | JWT 认证通过，幂等键与 correlation ID 正确传递 |
| RedClaw facade → ACC | ✗ BLOCKED | 返回 503 dependency_unavailable: "acc request failed" |
| ACC canonical task 创建 | ✗ 未验证 | ACC 服务未运行 |
| ACC → Memora typed ingest | ✗ 未验证 | 上游阻塞 |

---

## 阻塞原因分析

### ACC acc-go 启动失败

**根本原因**：`internal/taskassigner` 的 `startHeartbeatMonitor` 在 main.go 的数据库初始化完成前就启动了后台 goroutine。

**崩溃堆栈**：
```
panic: runtime error: invalid memory address or nil pointer dereference
goroutine 49 [running]:
github.com/jackc/pgx/v5/pgxpool.(*Pool).Acquire(0x0, ...)
github.com/kaixuan/acc-go/internal/taskassigner.(*PgStore).ListAgents(...)
github.com/kaixuan/acc-go/internal/taskassigner.(*Service).checkHeartbeats(...)
```

**影响**：
- `/api/v2/canonical/tasks` 路由未挂载（日志显示 `database unavailable`）
- RedClaw facade real backend 无法转发任务创建请求
- 整个 OpenPocket → ACC 链路阻塞

**已尝试的修复**：
1. 配置 DATABASE_URL 环境变量
2. 使用 .env.probe 文件
3. 直接在命令行设置环境变量

**实际问题**：环境变量被正确加载，但服务在 HTTP server 启动后、第一个请求到达前就崩溃了。

---

## 已验证的能力

### 1. RedClaw Facade 层

✓ **JWT 认证与 tenant 传播**
- Facade 使用独立的 FACADE_JWT_KEY（非 IDENTITY_SHARED_SECRET）
- JWT claims 必须包含：tenant_id, actor_id, scope, aud=redclaw-facade
- 错误 token 被正确拒绝，返回 invalid_token

✓ **错误 Envelope 契约**
- 统一格式：`{error: {code, message, retryable}, request_id, correlation_id}`
- request_id 在每个响应中唯一生成
- correlation_id 正确回显客户端传入的值

✓ **Capabilities 自省**
- 返回 ACC/Memora 依赖状态
- 声明支持的 features：task.read, task.write, memory.search

### 2. 幂等键与 Correlation ID 传播

✓ **请求头正确传递**
- Idempotency-Key: 客户端生成的唯一键
- X-Correlation-ID: 端到端跟踪标识符
- 两者都出现在 facade 的错误响应中

### 3. 本地 Docker 环境

✓ **已运行的服务**
- PostgreSQL: llm-gateway-pg (127.0.0.1:5432)
- Qdrant: memora-stack-qdrant (127.0.0.1:26333)
- Redis: nbjl-redis (127.0.0.1:16379)
- RedClaw facade: redclaw-facade (127.0.0.1:27001)

✓ **数据库可达性**
- PG DSN: `postgres://llm_gateway:***@127.0.0.1:5432/llm_gateway`
- 连接测试通过（acc-go 能读取配置，只是启动逻辑有 race condition）

---

## 未验证的能力（因 ACC 阻塞）

以下验证计划因 ACC 服务不可用而未能执行：

### 阶段 3：ACC Canonical Tasks 查询
- GET /api/v2/canonical/tasks
- GET /api/v2/canonical/tasks/{task_id}
- 字段映射：task_id, correlation_id, project_id, resource_version, run_id

### 阶段 4：幂等重放与 Tenant 边界
- 相同 Idempotency-Key + body → 200 重放
- 相同 key + 不同 body → 409 idempotency_conflict
- 缺少 key → 400 missing_required_header
- Tenant mismatch → 422 tenant_mismatch

### 阶段 5：Cursor 行为
- 正常 cursor 翻页
- 非法 cursor 静默回到第一页
- Status filter + 分页的内存过滤行为

### 阶段 6：Memora Typed Ingest
- POST /api/v2/memories/candidates (ACC → Memora)
- POST /api/session/ingest-summary (Session typed ingest)

### 阶段 7：PG/Qdrant 只读验证
- acc_tasks, acc_idempotency_keys, acc_outbox_events
- memories, Qdrant memory_vectors
- Tenant/project RLS 隔离

### 阶段 8：Run Events SSE
- GET /api/v2/runs/{run_id}/events
- Last-Event-ID / after cursor
- 预期 503 projection_unavailable（已知限制）

---

## v3 基线门禁判定

**结果**: ❌ **不通过** - 无法更新 v3 事实基线

**未满足的必要条件**：

1. ❌ ACC canonical task 创建成功并可查询
2. ❌ request_id、correlation_id、task_id 全程可关联（task_id 未生成）
3. ⚠️ 幂等重放、冲突、缺失 key 的错误 envelope（未验证）
4. ✅ JWT issuer/audience/tenant 边界已验证（facade 层面）
5. ❌ PG acc_tasks、acc_idempotency_keys、acc_outbox_events 已持久化
6. ❌ Memora 至少一条路径写入成功
7. ❌ Qdrant point 可验证

---

## 建议的修复步骤

### 短期修复（解除本次探针阻塞）

1. **修复 acc-go 启动顺序**
   - 选项 A：在 `main.go` 中延迟 task-assigner 的 heartbeat 启动，直到 PG pool 初始化完成
   - 选项 B：在 `taskassigner.Service.startHeartbeatMonitor` 中添加 nil pool 检查，graceful skip
   - 选项 C：通过环境变量 `ACC_TASKASSIGNER_HEARTBEAT_DISABLED=true` 禁用该功能

2. **验证 PG pool 初始化**
   - 在 `main.go` 中添加日志：`log.Info("PostgreSQL pool initialized", "max_conns", pool.Config().MaxConns)`
   - 确认 pool 非 nil 后再启动依赖服务

3. **重新执行探针**
   - ACC 启动后，重新运行阶段 2-9
   - 保留本次探针的 facade 层验证结果

### 中期优化

1. **统一 JWT 密钥管理**
   - RedClaw facade 当前使用独立 FACADE_JWT_KEY
   - 考虑是否应与 IDENTITY_SHARED_SECRET 对齐，或明确文档说明密钥分离的设计意图

2. **ACC 健康检查改进**
   - 当前 `/api/health` 在服务崩溃前无法反映实际状态
   - 建议添加 `/api/readiness`，只有在所有依赖（PG pool、后台 goroutine）就绪后才返回 200

3. **RedClaw facade 错误信息增强**
   - `dependency_unavailable: "acc request failed"` 缺少下游详细错误
   - 建议在 debug 模式下包含下游 HTTP 状态和错误码

---

## 证据文件清单

所有证据已归档至：
```
/Users/xutaohuang/workspace/ai-native-tools/openpocket/test-evidence/integration-probe-20260830T041343Z/
```

| 文件 | 内容 |
|------|------|
| 00-environment-config.txt | 脱敏环境配置与 Docker 服务列表 |
| 01-strategy-adjustment.txt | ACC 启动失败后的策略调整说明 |
| 01-redclaw-capabilities.txt | RedClaw facade capabilities 响应 |
| 01-redclaw-no-auth.txt | 无认证访问测试结果 |
| 02-create-task.txt | 首次任务创建尝试（JWT 错误） |
| 02-create-task-v2.txt | 第二次任务创建尝试（ACC 不可用） |
| 99-final-report.md | 本报告 |

**ACC 启动日志**：`/tmp/acc-go-probe.log`

---

## 结论

本次探针**部分成功**：

✅ **已验证**：
- RedClaw facade 层的认证、错误处理、依赖声明机制工作正常
- 本地 Docker 环境（PG/Qdrant/Redis）全部可用
- JWT 与 correlation ID 的传播契约符合设计

❌ **阻塞**：
- ACC acc-go 服务因启动顺序问题无法运行
- OpenPocket → RedClaw → ACC 的任务创建链路未能完成
- Memora typed ingest 验证未能执行
- PG/Qdrant 数据持久化验证未能执行

🔧 **下一步**：
1. 修复 acc-go 的 task-assigner 启动 race condition
2. 重新执行完整探针
3. 若 ACC 修复后全链路通过，更新 v3 事实基线

---

**探针执行者**: ZCode Agent  
**报告生成时间**: 2026-08-30T04:55:00Z  
**探针模式**: 本地真实环境，隔离租户 `probe-tenant-001`
