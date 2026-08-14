# OpenPocket Mock Contract Test

> 版本：v1-draft  
> 目的：在 RedClaw façade 真实实现前，用 mock server 验证 Pocket client 契约遵从性。

## 1. 测试策略

- **Consumer-driven**：Pocket 定义期望的 façade 行为。
- **Mock-first**：先用 mock 跑通所有场景，再对接真实 provider。
- **工具**：Go `httptest` + `testify`，或 Python `responses`/`pytest`。

## 2. Test Case 结构

```yaml
contract_test:
  name: string
  provider: redclaw-facade
  consumer: openpocket
  scenario: string
  mock_request:
    method: GET|POST|DELETE
    path: string
    headers: map
    body: object optional
  mock_response:
    status: integer
    headers: map
    body: object
  assertions:
    - type: status|body|header
      field: optional
      expected: value
  side_effects:
    - 本地数据写入或队列更新
```

## 3. 核心测试场景

### 3.1 Task Create

```yaml
name: task_create_success
provider: redclaw-facade
consumer: openpocket
scenario: 用户在 Pocket 创建平台任务

mock_request:
  method: POST
  path: /api/v2/tasks
  headers:
    Authorization: Bearer mock-service-jwt
    Idempotency-Key: pocket-task-123
    X-Correlation-ID: corr-123
  body:
    project_id: project-123
    title: Test task
    task_contract:
      type: agent_task
      risk_level: low

mock_response:
  status: 202
  body:
    data:
      task_id: acc-task-456
      status: accepted
      status_url: /api/v2/tasks/acc-task-456
      operation_id: op-789
    request_id: req-123
    correlation_id: corr-123

assertions:
  - type: status
    expected: 202
  - type: body
    field: data.task_id
    expected: acc-task-456

side_effects:
  - Pocket 本地写入 mapping: pocket_task_id=pocket-task-123, acc_task_id=acc-task-456
  - correlation_id 记录到本地
```

### 3.2 Task List

```yaml
name: task_list_success
scenario: 用户在 Pocket 查看任务列表

mock_request:
  method: GET
  path: /api/v2/tasks?project_id=project-123&limit=10

mock_response:
  status: 200
  body:
    data:
      - task_id: acc-task-456
        project_id: project-123
        title: Test task
        status: running
        resource_version: 3
        updated_at: "2026-08-14T00:00:00Z"
    page:
      limit: 10
      next_cursor: null
    request_id: req-124

assertions:
  - type: status
    expected: 200
  - type: body
    field: data[0].task_id
    expected: acc-task-456

side_effects:
  - Pocket 本地缓存更新 status=running, resource_version=3
```

### 3.3 Approval Decision

```yaml
name: approval_decision_approve
scenario: 用户在 Pocket 批准审批

mock_request:
  method: POST
  path: /api/v2/approvals/gate-123/decision
  headers:
    Idempotency-Key: pocket-approval-123
    X-Correlation-ID: corr-124
  body:
    decision: approve
    reason: LGTM
    expected_gate_version: 1
    candidate_decisions:
      - candidate_id: cand-123
        decision: promote

mock_response:
  status: 202
  body:
    data:
      approval_id: approval-456
      gate_id: gate-123
      status: accepted
      status_url: /api/v2/gates/gate-123
    request_id: req-125
    correlation_id: corr-124

assertions:
  - type: status
    expected: 202
  - type: body
    field: data.gate_id
    expected: gate-123

side_effects:
  - Pocket 本地记录 approval 状态 pending
```

### 3.4 Memory Search

```yaml
name: memory_search_success
scenario: Pocket 检索记忆

mock_request:
  method: POST
  path: /api/v2/memory/search
  headers:
    X-Correlation-ID: corr-125
  body:
    query: project summary
    scope_chain:
      tenant_id: tenant-123
      project_id: project-123
    top_k: 5
    token_budget: 2000
    policy:
      on_degraded: degraded_with_warning

mock_response:
  status: 200
  body:
    data:
      items:
        - source: memory
          memory_id: memory-123
          score: 0.92
          token_count: 128
          policy_decision: allow
          snippet: summary text
      degraded: false
    request_id: req-126
    correlation_id: corr-125

assertions:
  - type: status
    expected: 200
  - type: body
    field: data.items[0].memory_id
    expected: memory-123

side_effects:
  - Pocket 展示检索结果
```

### 3.5 Notification Ack

```yaml
name: notification_ack_success
scenario: Pocket 确认通知

mock_request:
  method: POST
  path: /api/v2/notifications/noti-123/ack
  headers:
    Idempotency-Key: pocket-ack-123

mock_response:
  status: 200
  body:
    data:
      notification_id: noti-123
      ack_at: "2026-08-14T00:00:00Z"
    request_id: req-127

assertions:
  - type: status
    expected: 200

side_effects:
  - Pocket 本地标记通知已确认
```

### 3.6 Error: Tenant Mismatch

```yaml
name: task_create_tenant_mismatch
scenario: Pocket JWT tenant 与 body project tenant 不匹配

mock_request:
  method: POST
  path: /api/v2/tasks
  body:
    project_id: other-tenant-project
    title: Test

mock_response:
  status: 422
  body:
    error:
      code: tenant_mismatch
      message: project does not belong to JWT tenant
      retryable: false
    request_id: req-128

assertions:
  - type: status
    expected: 422
  - type: body
    field: error.code
    expected: tenant_mismatch

side_effects:
  - Pocket 展示错误，不写本地映射
```

### 3.7 Error: Idempotency Replay

```yaml
name: task_create_idempotency_replay
scenario: Pocket 重试同一 Idempotency-Key

mock_request:
  method: POST
  path: /api/v2/tasks
  headers:
    Idempotency-Key: pocket-task-123

mock_response:
  status: 200
  body:
    data:
      task_id: acc-task-456
      status: accepted
    request_id: req-129

assertions:
  - type: status
    expected: 200
  - type: body
    field: data.task_id
    expected: acc-task-456

side_effects:
  - Pocket 不重复写 mapping
```

### 3.8 SSE Reconnect

```yaml
name: sse_reconnect_with_cursor
scenario: Pocket SSE 断线后用 Last-Event-ID 续传

mock_request:
  method: GET
  path: /api/v2/runs/run-123/events?after=evt-100
  headers:
    Last-Event-ID: evt-100

mock_response:
  status: 200
  content_type: text/event-stream
  body: |
    id: evt-101
    event: task.state.changed.v1
    data: {"task_id":"task-123","status":"completed"}

assertions:
  - type: status
    expected: 200
  - type: header
    field: Content-Type
    expected: text/event-stream

side_effects:
  - Pocket 更新本地 cursor=evt-101
  - 更新任务状态=completed
```

## 4. Mock 实现示例（Go）

```go
package pocketclient_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/halfking/pocket-opencode/backend/internal/redclaw"
)

func TestTaskCreate_Success(t *testing.T) {
	// Mock RedClaw façade
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/api/v2/tasks", r.URL.Path)
		assert.NotEmpty(t, r.Header.Get("Idempotency-Key"))
		
		w.WriteHeader(202)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"task_id":    "acc-task-456",
				"status":     "accepted",
				"status_url": "/api/v2/tasks/acc-task-456",
			},
			"request_id":     "req-123",
			"correlation_id": "corr-123",
		})
	}))
	defer mockServer.Close()

	// 使用 mock server URL 初始化 Pocket RedClaw client
	client := redclaw.NewClient(mockServer.URL, "mock-token")
	
	resp, err := client.CreateTask(redclaw.CreateTaskRequest{
		ProjectID: "project-123",
		Title:     "Test task",
		TaskContract: redclaw.TaskContract{
			Type:      "agent_task",
			RiskLevel: "low",
		},
	})

	assert.NoError(t, err)
	assert.Equal(t, "acc-task-456", resp.Data.TaskID)
	assert.Equal(t, "accepted", resp.Data.Status)
}
```

## 5. 运行与 CI 集成

```bash
# 本地运行
go test ./backend/internal/redclaw/... -v -tags=contract

# CI pipeline
- name: Contract Tests
  run: |
    go test -v -tags=contract ./backend/internal/redclaw/...
    # 可选：生成契约报告并上传
```

## 6. 契约冻结与版本化

- 契约测试通过后，RedClaw 实现方必须兼容 mock 行为。
- 新增字段只能可选，不能改变已有必填字段语义。
- breaking change 需升级 API 版本。
