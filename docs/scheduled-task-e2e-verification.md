# Scheduled Task End-to-End Verification Guide

## 概述

本文档指导如何在真实环境中验证定时自动化系统的完整闭环：

```
create → claim → execute → audit → WebSocket → history
```

## 验证环境要求

### 必需组件

1. **PostgreSQL** - 持久化任务定义和运行记录
2. **Running pocketd** - 后端服务和调度器
3. **JWT Token** - 有效的认证凭据

### 可选组件（用于执行器测试）

4. **RedClaw** - 测试 `redclaw_chat` 和 `redclaw_knowledge` 执行器
5. **ACC MCP** - 测试 `acc_mcp` 执行器
6. **Agent Bridge** - 测试 `agent_bridge` 执行器

## 验证方法

### 方法 1: 自动化脚本（推荐）

使用提供的验证脚本进行完整的端到端测试：

```bash
# 1. 设置环境变量
export POCKET_API_BASE="http://localhost:8080"
export POCKET_JWT_TOKEN="your-jwt-token-here"
export POCKET_POSTGRES_DSN="postgres://user:pass@host:5432/dbname?sslmode=disable"

# 2. 运行验证脚本
./scripts/verify-scheduled-tasks.sh
```

脚本会自动执行以下步骤：
- ✓ 创建测试任务
- ✓ 验证持久化（列表查询）
- ✓ 触发手动执行
- ✓ 检查运行历史
- ✓ 更新任务状态
- ✓ 删除任务
- ✓ 运行 Go 集成测试（如果 DSN 可用）

### 方法 2: Go 集成测试

运行集成测试套件：

```bash
cd backend

# 运行所有 scheduled task 集成测试
export POCKET_POSTGRES_DSN="postgres://..."
go test -v -count=1 -timeout=60s ./internal/server -run TestScheduledTask

# 仅运行端到端测试
go test -v -count=1 -timeout=60s ./internal/server -run TestScheduledTaskEndToEnd

# 仅运行租户隔离测试
go test -v -count=1 -timeout=60s ./internal/server -run TestScheduledTaskTenantIsolation
```

### 方法 3: 手动 API 测试

使用 `curl` 或 Postman 手动测试每个端点。

#### 3.1 创建任务

```bash
curl -X POST http://localhost:8080/api/scheduled-tasks \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Test Webhook Task",
    "description": "Manual E2E test",
    "kind": "webhook",
    "schedule_kind": "interval",
    "schedule_expr": "10m",
    "timezone": "Asia/Shanghai",
    "enabled": true,
    "timeout_sec": 30,
    "payload": {
      "url": "https://httpbin.org/post",
      "method": "POST",
      "body": {"test": true}
    }
  }'
```

预期响应：`201 Created`，包含完整的任务对象和生成的 `id`。

#### 3.2 列出任务

```bash
curl -X GET http://localhost:8080/api/scheduled-tasks \
  -H "Authorization: Bearer $TOKEN"
```

预期响应：`200 OK`，包含 `tasks` 数组。

#### 3.3 手动触发执行

```bash
curl -X POST http://localhost:8080/api/scheduled-tasks/{task_id}/run \
  -H "Authorization: Bearer $TOKEN"
```

预期响应：`202 Accepted`，表示任务已加入执行队列。

#### 3.4 查看运行历史

```bash
curl -X GET http://localhost:8080/api/scheduled-tasks/{task_id}/runs \
  -H "Authorization: Bearer $TOKEN"
```

预期响应：`200 OK`，包含 `runs` 数组，每个 run 有 `status`、`output`、`error` 等字段。

#### 3.5 更新任务

```bash
curl -X PATCH http://localhost:8080/api/scheduled-tasks/{task_id} \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"enabled": false}'
```

预期响应：`200 OK`，包含更新后的任务对象。

#### 3.6 删除任务

```bash
curl -X DELETE http://localhost:8080/api/scheduled-tasks/{task_id} \
  -H "Authorization: Bearer $TOKEN"
```

预期响应：`204 No Content`。

## 验证检查清单

### 基础功能验证

- [ ] **任务 CRUD**
  - [ ] 创建任务成功，返回有效 ID
  - [ ] 列表查询包含创建的任务
  - [ ] 单个任务查询返回完整信息
  - [ ] 更新任务成功
  - [ ] 删除任务成功

- [ ] **执行流程**
  - [ ] 手动触发返回 202 Accepted
  - [ ] Run 记录被创建（状态为 `running`）
  - [ ] 执行完成后状态变为 `success` 或 `failed`
  - [ ] Run 历史可查询
  - [ ] Output 字段包含执行结果

- [ ] **调度器**
  - [ ] Scheduler 在配置的 tick_interval 后开始扫描
  - [ ] 到期任务被自动执行
  - [ ] `next_run_at` 在执行后正确更新
  - [ ] `max_runs` 限制被遵守
  - [ ] `cooldown_sec` 生效

### 可靠性验证

- [ ] **并发安全**
  - [ ] 同一任务不会被重复执行（lease 保护）
  - [ ] 多个任务可并发执行（worker pool）
  - [ ] 手动触发与调度触发不冲突

- [ ] **错误处理**
  - [ ] 执行超时后任务被标记为 failed
  - [ ] Executor panic 被捕获，记录错误信息
  - [ ] 网络错误被正确处理和记录
  - [ ] 无效 payload 被拒绝（创建时）

- [ ] **崩溃恢复**
  - [ ] 进程重启后，lease 过期的任务可被重新声明
  - [ ] 旧的 `running` run 不阻塞后续执行
  - [ ] 任务定义和历史在数据库中完整保留

### 安全性验证

- [ ] **认证授权**
  - [ ] 无 token 请求返回 401
  - [ ] 用户只能访问自己 workspace 的任务
  - [ ] 跨 workspace 访问返回 404
  - [ ] Payload 中的 `user_id`/`workspace_id` 被忽略（使用 JWT claims）

- [ ] **Webhook 安全**
  - [ ] 私网地址默认被阻止（除非显式放行）
  - [ ] Metadata 端点（169.254.169.254）始终被阻止
  - [ ] Timeout 限制生效
  - [ ] 响应大小有合理限制

### 集成验证

- [ ] **RedClaw 执行器**
  - [ ] `redclaw_chat` 任务成功调用 LLM
  - [ ] `redclaw_knowledge` 任务成功查询知识库
  - [ ] Workspace 与 RedClaw tenant 不匹配时拒绝执行

- [ ] **ACC 执行器**
  - [ ] `acc_mcp` 任务成功调用 ACC tool
  - [ ] Workspace 与 ACC tenant 不匹配时拒绝执行
  - [ ] Tool 参数验证正确

- [ ] **审计与通知**
  - [ ] 每次创建/更新/删除产生审计事件
  - [ ] 每次运行产生 `scheduler.task.run` 审计事件
  - [ ] WebSocket 广播 `scheduledtask.started` 事件
  - [ ] WebSocket 广播 `scheduledtask.succeeded`/`failed` 事件

## PostgreSQL 数据验证

连接到数据库直接检查数据完整性：

```sql
-- 查看所有任务
SELECT id, name, kind, schedule_kind, enabled, next_run_at, run_count 
FROM scheduled_tasks 
ORDER BY created_at DESC 
LIMIT 10;

-- 查看最近的运行记录
SELECT id, task_id, status, started_at, finished_at, 
       EXTRACT(EPOCH FROM (finished_at - started_at)) as duration_sec
FROM scheduled_task_runs 
ORDER BY started_at DESC 
LIMIT 20;

-- 检查是否有泄漏的 running 状态（lease 过期但未恢复）
SELECT r.id, r.task_id, r.started_at, t.lease_until,
       NOW() - r.started_at as elapsed,
       NOW() > t.lease_until as lease_expired
FROM scheduled_task_runs r
JOIN scheduled_tasks t ON r.task_id = t.id
WHERE r.status = 'running'
  AND r.finished_at IS NULL;

-- 统计各状态的运行数量
SELECT status, COUNT(*) as count
FROM scheduled_task_runs
GROUP BY status;
```

## 日志监控

启动 pocketd 时，关注以下日志：

```bash
# 启动日志
grep "scheduler" pocketd.log | head -20

# 任务声明
grep "claimed task" pocketd.log

# 执行开始
grep "executing task" pocketd.log

# 执行完成
grep "task completed" pocketd.log

# 错误日志
grep -i "error\|panic\|failed" pocketd.log | grep -i "schedul"
```

## 常见问题排查

### 问题 1: 创建任务返回 503

**原因**: `scheduledTaskStore` 未初始化，通常是因为 `POCKET_POSTGRES_DSN` 未配置。

**解决**:
```bash
export POCKET_POSTGRES_DSN="postgres://user:pass@host:5432/dbname?sslmode=disable"
# 重启 pocketd
```

### 问题 2: 手动触发后没有运行记录

**原因**: 
1. Scheduler 未启动（`POCKET_SCHEDULER_ENABLED=false`）
2. 执行器未注册
3. Worker pool 已满

**排查**:
```bash
# 检查 scheduler 配置
grep POCKET_SCHEDULER_ .env

# 检查日志
tail -f pocketd.log | grep -i schedul
```

### 问题 3: 任务一直处于 running 状态

**原因**: 
1. 执行器超时但未正确标记失败
2. 进程崩溃，lease 尚未过期

**解决**:
- 等待 lease 过期（默认 `timeout_sec + 60s`）
- 或手动更新数据库：
  ```sql
  UPDATE scheduled_task_runs 
  SET status = 'failed', 
      error = 'timeout or crash recovery',
      finished_at = NOW()
  WHERE id = 'run-xxx' AND status = 'running';
  ```

### 问题 4: RedClaw/ACC 执行失败

**原因**: Workspace 与配置的 tenant 不匹配。

**验证**:
```bash
# 检查任务的 workspace_id
curl -X GET http://localhost:8080/api/scheduled-tasks/{task_id} \
  -H "Authorization: Bearer $TOKEN" | jq .workspace_id

# 检查 RedClaw 配置
echo $REDCLAW_TENANT_ID

# 检查 ACC 配置
echo $ACC_MCP_TENANT
```

当前实现的 RedClaw 和 ACC 客户端是静态 tenant 配置。如果任务的 workspace 与客户端 tenant 不匹配，执行器会 fail-closed 拒绝执行。

## 待实现的增强

根据审计报告，以下功能待后续实现：

1. **Stale run 清理**: 为过期的 `running` run 添加维护任务，自动标记为 `abandoned`
2. **多租户 client factory**: 支持动态创建 workspace-scoped RedClaw/ACC 客户端
3. **Intent forward executor**: 实现 `intent_forward` 执行器（当前 UI 已隐藏该选项）
4. **更丰富的调度选项**: 支持 cron 表达式、特定日期/时间
5. **执行器扩展机制**: 允许插件注册自定义执行器

## 参考资料

- [Scheduled Task System Documentation](./scheduled-task-system.md)
- [Audit Report](./audit-scheduled-task-20260830.md)
- 后端实现: `backend/internal/scheduledtask/`
- API 实现: `backend/internal/server/scheduled_task_handler.go`
- 前端 UI: `frontend/src/features/scheduled-tasks/`

## 总结

完成以上验证后，你应该能够确认：

1. ✅ 任务定义和运行记录正确持久化到 PostgreSQL
2. ✅ 调度器按配置间隔扫描并执行到期任务
3. ✅ 手动触发和自动调度都能正常工作
4. ✅ 租户隔离得到正确执行
5. ✅ 审计事件和 WebSocket 通知正常发送
6. ✅ 错误处理和超时保护正常工作
7. ✅ 各类执行器（Webhook/RedClaw/ACC）按预期执行

如有任何问题，请参考日志和 PostgreSQL 数据进行排查，或联系开发团队。
