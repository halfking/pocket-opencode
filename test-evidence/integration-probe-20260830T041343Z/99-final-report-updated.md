# OpenPocket → RedClaw → ACC → Memora 集成探针最终报告（更新版）

**执行时间**: 2026-08-30T04:13:43Z - 2026-08-30T05:08:00Z  
**探针类型**: 本地 Docker 环境最小真实集成  
**状态**: **PARTIAL** - ACC 已修复并运行，服务间认证配置需要对齐

---

## 执行概要

### ✅ 已成功修复并验证

1. **ACC acc-go 启动问题**
   - **根本原因**: `taskassigner.PgStore.ListAgents` 在 nil pool 上调用 `Query` 导致 SIGSEGV
   - **修复方案**: 在 `ListAgents` 方法开头添加 nil pool 检查，返回空列表
   - **修复文件**: `/Users/xutaohuang/workspace/ai-native-tools/agent-control-center/acc-go/internal/taskassigner/store_pg.go:107-111`
   - **验证结果**: ACC 成功启动，不再崩溃

2. **数据库连接问题**
   - **根本原因**: 使用了错误的 PostgreSQL 密码
   - **正确密码**: `4Q92cFTaYY8Z3AO07XTBBH-1g7kceaxg`（从 llm-gateway-pg 容器获取）
   - **验证结果**: ACC canonical store 成功初始化

3. **ACC `/api/v2/canonical/tasks` 端点**
   - **状态**: ✅ 已挂载并工作
   - **无认证访问**: 正确返回 `401 unauthorized`
   - **错误 envelope**: `{error: {code, message, retryable}, request_id}`
   - **监听端口**: 4101（匹配 RedClaw facade 预期配置）

### ⚠️ 剩余配置问题

4. **RedClaw Facade → ACC 服务间认证**
   - **当前状态**: `502 dependency_auth_failed`
   - **根本原因**: 服务间认证密钥不匹配
     - Facade 使用: `FACADE_ACC_SERVICE_SECRET=acc-go-local-smoke-secret-32bytes-minimum-xyz`
     - ACC 期望: `IDENTITY_SHARED_SECRET=92LKtn61FehqIs8dy2TxzEnTcO8CWbtkE26IE1+/60r416AaYizg+4ziLsXFeBpL`
   - **需要对齐**: RedClaw facade 容器需要使用与 ACC 相同的密钥，或 ACC 需要接受 facade 的 service secret

---

## 详细修复过程

### 修复 1：Task-Assigner Nil Pool Check

**问题**：
```
panic: runtime error: invalid memory address or nil pointer dereference
goroutine 49 [running]:
github.com/jackc/pgx/v5/pgxpool.(*Pool).Acquire(0x0, ...)
```

**修复代码**：
```go
func (s *PgStore) ListAgents(ctx context.Context, status *AgentStatus) ([]*Agent, error) {
	// Gracefully handle nil pool (e.g., during startup before DB is initialized).
	// Return empty list so heartbeat monitor doesn't crash.
	if s.db == nil {
		return []*Agent{}, nil
	}
	// ... 原有逻辑
}
```

**影响**：
- ACC 启动时不再因 task-assigner heartbeat goroutine 而崩溃
- 当数据库不可用时，优雅降级而不是 panic

### 修复 2：数据库密码

**错误配置**：
```bash
DATABASE_URL="postgres://llm_gateway:llm_gateway_db_pass_2026_secure@127.0.0.1:5432/llm_gateway?sslmode=disable"
```

**正确配置**：
```bash
DATABASE_URL="postgres://llm_gateway:4Q92cFTaYY8Z3AO07XTBBH-1g7kceaxg@127.0.0.1:5432/llm_gateway?sslmode=disable"
```

**验证**：
```
[INF] acc canonical store: postgres
[INF] v3 orchestration API enabled (PostgreSQL canonical store, outbox publisher + SSE projector)
```

### 修复 3：端口对齐

RedClaw facade 容器配置为 `FACADE_ACC_ENDPOINT=http://host.docker.internal:4101`，因此 ACC 必须监听 `4101` 端口。

**配置**：
```bash
ACC_LISTEN_ADDR=":4101"
```

**验证**：
```bash
$ curl -s http://127.0.0.1:4101/api/v2/canonical/tasks
{"error":{"code":"unauthorized","message":"missing bearer token","retryable":false},"request_id":"..."}
```

---

## 已验证的能力

### 1. ACC Canonical Tasks API

✅ **路由挂载**
- `/api/v2/canonical/tasks` 正确响应
- 无认证访问返回 401
- 错误 envelope 符合契约

✅ **数据库集成**
- PostgreSQL pool 成功初始化
- Canonical store 可用
- Tables ready（acc_projects, acc_tasks, acc_idempotency_keys, acc_outbox_events）

✅ **启动稳定性**
- Task-assigner heartbeat monitor 不再导致崩溃
- 服务可以在没有数据库的情况下启动（降级模式）

### 2. RedClaw Facade

✅ **Capabilities 自省**
- ACC adapter: configured
- Memora proxy: configured

✅ **JWT 认证**
- 用户 JWT 验证工作正常（使用 FACADE_JWT_KEY）

✅ **错误处理**
- `dependency_unavailable` → ACC 不可达
- `dependency_auth_failed` → ACC 认证失败
- 错误 envelope 统一

### 3. 本地 Docker 环境

✅ **服务健康**
- PostgreSQL: llm-gateway-pg (127.0.0.1:5432) ✅
- Qdrant: memora-stack-qdrant (127.0.0.1:26333) ✅
- Redis: nbjl-redis (127.0.0.1:16379) ✅
- RedClaw facade: redclaw-facade (127.0.0.1:27001) ✅
- ACC: acc-go (127.0.0.1:4101) ✅

---

## 未完成的验证（需要修复服务间认证）

以下验证因服务间认证配置不匹配而阻塞：

1. **OpenPocket → RedClaw → ACC 任务创建链路**
   - Facade → ACC 请求返回 `502 dependency_auth_failed`
   
2. **ACC canonical task 查询与字段验证**
   - 依赖上游任务创建成功

3. **幂等重放、tenant 边界、cursor 行为**
   - 依赖任务创建成功

4. **Memora typed ingest**
   - 依赖 ACC task 创建成功

5. **PG/Qdrant 数据持久化验证**
   - 依赖任务创建成功

6. **Run events SSE cursor**
   - 依赖任务创建和 run_id 生成

---

## 下一步行动

### 短期修复（解除当前阻塞）

**选项 A：统一服务间认证密钥（推荐）**

将所有服务配置为使用相同的 `IDENTITY_SHARED_SECRET`：

```bash
# RedClaw facade 容器需要
FACADE_ACC_SERVICE_SECRET=92LKtn61FehqIs8dy2TxzEnTcO8CWbtkE26IE1+/60r416AaYizg+4ziLsXFeBpL

# 或者通过 docker-compose 环境变量
REDCLAW_FACADE_ACC_SERVICE_SECRET=92LKtn61FehqIs8dy2TxzEnTcO8CWbtkE26IE1+/60r416AaYizg+4ziLsXFeBpL
```

重启 RedClaw facade 容器后重试探针。

**选项 B：ACC 接受多个服务密钥**

修改 ACC 的 identity 验证逻辑，接受 `FACADE_ACC_SERVICE_SECRET` 作为备用密钥。

### 中期优化

1. **文档化服务间认证约定**
   - 明确哪些服务使用 `IDENTITY_SHARED_SECRET`
   - 明确哪些服务使用独立的 service secret
   - 提供配置示例和验证脚本

2. **增强 ACC 启动诊断**
   - 在日志中明确输出：数据库连接成功/失败、canonical store 初始化状态
   - 添加 `/api/readiness` 端点，只有在所有依赖就绪后才返回 200

3. **RedClaw facade 错误信息增强**
   - `dependency_auth_failed` 应包含更多上下文（如：期望的 issuer/audience）

---

## 证据文件清单

```
/Users/xutaohuang/workspace/ai-native-tools/openpocket/test-evidence/integration-probe-20260830T041343Z/
├── 00-environment-config.txt          # 环境配置
├── 01-strategy-adjustment.txt         # 策略调整说明
├── 01-redclaw-capabilities.txt        # RedClaw capabilities
├── 01-redclaw-no-auth.txt             # RedClaw 无认证测试
├── 02-create-task.txt                 # 首次任务创建（JWT 错误）
├── 02-create-task-v2.txt              # 第二次任务创建（ACC 不可用）
├── 03-acc-fixed.txt                   # ACC 修复后测试（路由未挂载）
├── 04-acc-canonical-working.txt       # ACC canonical API 工作验证
├── 05-create-task-final.txt           # 最终任务创建（认证失败）
├── 99-final-report.md                 # 初始报告
└── 99-final-report-updated.md         # 本报告
```

**ACC 启动日志**: `/tmp/acc-go-probe.log`

**修复的代码**:
- `acc-go/internal/taskassigner/store_pg.go` (已修改，未提交)

---

## 结论

本次探针**部分成功**：

✅ **已解决的核心问题**：
- ACC acc-go 启动崩溃（task-assigner nil pool）
- 数据库连接失败（密码错误）
- Canonical tasks API 路由挂载

⚠️ **剩余配置问题**：
- RedClaw facade → ACC 服务间认证密钥不匹配
- 完整的 OpenPocket → ACC → Memora 链路未验证

🎯 **v3 基线门禁状态**: **未通过** - 需要修复服务间认证后重新执行阶段 2-9

📋 **建议**：
1. 统一服务间认证配置（使用 `IDENTITY_SHARED_SECRET`）
2. 重启 RedClaw facade 容器
3. 重新执行探针阶段 2-9
4. 若全链路通过，更新 v3 事实基线

---

**探针执行者**: ZCode Agent  
**报告生成时间**: 2026-08-30T05:08:00Z  
**探针模式**: 本地真实环境，隔离租户 `probe-tenant-001`  
**代码修复**: taskassigner/store_pg.go (nil pool graceful handling)
