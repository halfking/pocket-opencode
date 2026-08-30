#!/usr/bin/env bash
#
# verify-scheduled-tasks.sh
# 
# 端到端验证脚本，用于在开发/预发环境验证 scheduled task 系统的完整闭环：
# create → claim → execute → audit → WebSocket → history
#
# 使用方法：
#   export POCKET_POSTGRES_DSN="postgres://user:pass@host:5432/dbname?sslmode=disable"
#   export POCKET_API_BASE="http://localhost:8080"  # 或预发环境 URL
#   export POCKET_JWT_TOKEN="your-jwt-token"
#   ./scripts/verify-scheduled-tasks.sh
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

log_info() {
    echo -e "${BLUE}[INFO]${NC} $*"
}

log_success() {
    echo -e "${GREEN}[✓]${NC} $*"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $*"
}

log_error() {
    echo -e "${RED}[✗]${NC} $*"
}

# 检查必需的环境变量
check_env() {
    local missing=0
    
    if [[ -z "${POCKET_API_BASE:-}" ]]; then
        log_error "POCKET_API_BASE not set"
        missing=1
    fi
    
    if [[ -z "${POCKET_JWT_TOKEN:-}" ]]; then
        log_error "POCKET_JWT_TOKEN not set"
        missing=1
    fi
    
    if [[ $missing -eq 1 ]]; then
        echo ""
        echo "Required environment variables:"
        echo "  POCKET_API_BASE   - API endpoint (e.g., http://localhost:8080)"
        echo "  POCKET_JWT_TOKEN  - Valid JWT token for authentication"
        echo ""
        echo "Optional:"
        echo "  POCKET_POSTGRES_DSN - For running integration tests"
        exit 1
    fi
}

# API 调用辅助函数
api_call() {
    local method="$1"
    local path="$2"
    local data="${3:-}"
    
    local url="${POCKET_API_BASE}${path}"
    local args=(
        -X "$method"
        -H "Authorization: Bearer ${POCKET_JWT_TOKEN}"
        -H "Content-Type: application/json"
        -s -w "\n%{http_code}"
    )
    
    if [[ -n "$data" ]]; then
        args+=(-d "$data")
    fi
    
    curl "${args[@]}" "$url"
}

# 解析 curl 响应（最后一行是状态码）
parse_response() {
    local response="$1"
    local body
    local status
    
    body=$(echo "$response" | head -n -1)
    status=$(echo "$response" | tail -n 1)
    
    echo "$body"
    return "$status"
}

# 步骤 1: 创建定时任务
create_task() {
    log_info "Step 1: Creating scheduled task..."
    
    local payload
    payload=$(cat <<EOF
{
  "name": "E2E Verification Task $(date +%s)",
  "description": "Automated verification test",
  "kind": "webhook",
  "schedule_kind": "interval",
  "schedule_expr": "5m",
  "timezone": "UTC",
  "enabled": true,
  "timeout_sec": 30,
  "payload": {
    "url": "https://httpbin.org/post",
    "method": "POST",
    "body": {
      "test": "e2e",
      "timestamp": "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    }
  }
}
EOF
)
    
    local response
    response=$(api_call POST "/api/scheduled-tasks" "$payload")
    local body status
    body=$(echo "$response" | head -n -1)
    status=$(echo "$response" | tail -n 1)
    
    if [[ "$status" != "201" ]]; then
        log_error "Failed to create task (HTTP $status)"
        echo "$body" | jq . 2>/dev/null || echo "$body"
        return 1
    fi
    
    TASK_ID=$(echo "$body" | jq -r '.id')
    if [[ -z "$TASK_ID" || "$TASK_ID" == "null" ]]; then
        log_error "Failed to extract task ID from response"
        return 1
    fi
    
    log_success "Created task: $TASK_ID"
    echo "$body" | jq .
    return 0
}

# 步骤 2: 列出任务验证持久化
list_tasks() {
    log_info "Step 2: Listing tasks to verify persistence..."
    
    local response
    response=$(api_call GET "/api/scheduled-tasks")
    local body status
    body=$(echo "$response" | head -n -1)
    status=$(echo "$response" | tail -n 1)
    
    if [[ "$status" != "200" ]]; then
        log_error "Failed to list tasks (HTTP $status)"
        return 1
    fi
    
    local found
    found=$(echo "$body" | jq -r ".tasks[] | select(.id == \"$TASK_ID\") | .id")
    
    if [[ "$found" == "$TASK_ID" ]]; then
        log_success "Task found in list"
        return 0
    else
        log_error "Created task not found in list"
        return 1
    fi
}

# 步骤 3: 手动触发执行
trigger_task() {
    log_info "Step 3: Triggering manual execution..."
    
    local response
    response=$(api_call POST "/api/scheduled-tasks/$TASK_ID/run")
    local status
    status=$(echo "$response" | tail -n 1)
    
    if [[ "$status" != "202" ]]; then
        log_error "Failed to trigger task (HTTP $status)"
        return 1
    fi
    
    log_success "Task triggered (202 Accepted)"
    return 0
}

# 步骤 4: 等待执行并检查运行历史
check_run_history() {
    log_info "Step 4: Waiting for execution (10 seconds)..."
    sleep 10
    
    log_info "Checking run history..."
    local response
    response=$(api_call GET "/api/scheduled-tasks/$TASK_ID/runs")
    local body status
    body=$(echo "$response" | head -n -1)
    status=$(echo "$response" | tail -n 1)
    
    if [[ "$status" != "200" ]]; then
        log_error "Failed to get run history (HTTP $status)"
        return 1
    fi
    
    local run_count
    run_count=$(echo "$body" | jq -r '.runs | length')
    
    if [[ "$run_count" -eq 0 ]]; then
        log_error "No runs found after manual trigger"
        return 1
    fi
    
    local last_status
    last_status=$(echo "$body" | jq -r '.runs[0].status')
    
    log_success "Found $run_count run(s), last status: $last_status"
    echo "$body" | jq '.runs[0]'
    
    if [[ "$last_status" == "running" ]]; then
        log_warn "Run still in progress, may need more time"
    fi
    
    return 0
}

# 步骤 5: 更新任务（禁用）
update_task() {
    log_info "Step 5: Disabling task..."
    
    local payload='{"enabled": false}'
    local response
    response=$(api_call PATCH "/api/scheduled-tasks/$TASK_ID" "$payload")
    local status
    status=$(echo "$response" | tail -n 1)
    
    if [[ "$status" != "200" ]]; then
        log_error "Failed to update task (HTTP $status)"
        return 1
    fi
    
    log_success "Task disabled"
    return 0
}

# 步骤 6: 删除任务
delete_task() {
    log_info "Step 6: Deleting task..."
    
    local response
    response=$(api_call DELETE "/api/scheduled-tasks/$TASK_ID")
    local status
    status=$(echo "$response" | tail -n 1)
    
    if [[ "$status" != "204" ]]; then
        log_error "Failed to delete task (HTTP $status)"
        return 1
    fi
    
    log_success "Task deleted"
    return 0
}

# 运行 Go 集成测试（可选）
run_go_tests() {
    if [[ -z "${POCKET_POSTGRES_DSN:-}" ]]; then
        log_warn "POCKET_POSTGRES_DSN not set, skipping Go integration tests"
        return 0
    fi
    
    log_info "Running Go integration tests..."
    
    cd "$PROJECT_ROOT/backend"
    
    if go test -v -count=1 -timeout=60s \
        ./internal/server -run "TestScheduledTask" \
        2>&1 | tee /tmp/scheduled-task-test.log; then
        log_success "Go integration tests passed"
        return 0
    else
        log_error "Go integration tests failed"
        log_info "Check /tmp/scheduled-task-test.log for details"
        return 1
    fi
}

# 主流程
main() {
    echo ""
    echo "═══════════════════════════════════════════════════════"
    echo "  Scheduled Task End-to-End Verification"
    echo "═══════════════════════════════════════════════════════"
    echo ""
    
    check_env
    
    log_info "API Base: $POCKET_API_BASE"
    echo ""
    
    local failed=0
    
    # API 端到端测试
    if ! create_task; then failed=1; fi
    echo ""
    
    if [[ $failed -eq 0 ]] && ! list_tasks; then failed=1; fi
    echo ""
    
    if [[ $failed -eq 0 ]] && ! trigger_task; then failed=1; fi
    echo ""
    
    if [[ $failed -eq 0 ]] && ! check_run_history; then failed=1; fi
    echo ""
    
    if [[ $failed -eq 0 ]] && ! update_task; then failed=1; fi
    echo ""
    
    # 总是尝试清理
    if [[ -n "${TASK_ID:-}" ]]; then
        delete_task || true
        echo ""
    fi
    
    # Go 集成测试
    if [[ $failed -eq 0 ]]; then
        run_go_tests || failed=1
        echo ""
    fi
    
    # 总结
    echo "═══════════════════════════════════════════════════════"
    if [[ $failed -eq 0 ]]; then
        log_success "All verification steps passed!"
        echo ""
        echo "Next steps:"
        echo "  1. Check audit logs for scheduler.task.* events"
        echo "  2. Monitor WebSocket events in browser DevTools"
        echo "  3. Verify PostgreSQL scheduled_tasks and scheduled_task_runs tables"
        echo "  4. Test RedClaw/ACC executors with real tenant configuration"
        exit 0
    else
        log_error "Verification failed"
        echo ""
        echo "Troubleshooting:"
        echo "  - Check server logs for errors"
        echo "  - Verify POCKET_POSTGRES_DSN is correct"
        echo "  - Ensure scheduler is enabled (POCKET_SCHEDULER_ENABLED=true)"
        echo "  - Check JWT token has valid workspace_id"
        exit 1
    fi
}

main "$@"
