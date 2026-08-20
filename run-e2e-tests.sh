#!/bin/bash
# Complete end-to-end test script

set -e

echo "=========================================="
echo "OpenCode Pocket - 端到端测试"
echo "=========================================="
echo ""

# Colors
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m'

TEST_DIR="test-evidence/e2e-$(date +%Y%m%d-%H%M%S)"
mkdir -p "$TEST_DIR"

PASSED=0
FAILED=0

# Test function
test_step() {
    local name=$1
    echo -n "Testing: $name ... "
}

test_pass() {
    echo -e "${GREEN}✓ PASS${NC}"
    PASSED=$((PASSED + 1))
}

test_fail() {
    local msg=$1
    echo -e "${RED}✗ FAIL${NC}"
    echo "  Error: $msg"
    FAILED=$((FAILED + 1))
}

# 1. Build Plugin
echo "=========================================="
echo "1. Building Plugin"
echo "=========================================="

test_step "Plugin build"
cd opencode-plugin
if npm install && npm run build; then
    test_pass
    echo "  Output: dist/index.js, dist/index.d.ts"
else
    test_fail "Plugin build failed"
fi
cd ..

echo ""

# 2. Build Manager
echo "=========================================="
echo "2. Building Manager"
echo "=========================================="

test_step "Manager build"
cd opencode-manager
if go build -o opencode-manager main.go; then
    test_pass
    ls -lh opencode-manager
else
    test_fail "Manager build failed"
fi
cd ..

echo ""

# 3. Check Backend
echo "=========================================="
echo "3. Checking Backend"
echo "=========================================="

test_step "Backend health check"
if curl -sf http://localhost:8088/healthz > /dev/null; then
    test_pass
else
    test_fail "Backend not running"
    echo "  Start with: cd backend && ./start-dev.sh"
fi

test_step "Backend login"
TOKEN=$(curl -s -X POST http://localhost:8088/api/auth/login \
    -H "Content-Type: application/json" \
    -d "{\"username\":\"${POCKET_AUTH_USER:-admin}\",\"password\":\"${POCKET_AUTH_PASS}\"}" | jq -r '.token // empty')

if [ -n "$TOKEN" ]; then
    test_pass
    echo "  Token: ${TOKEN:0:40}..."
else
    test_fail "Login failed"
fi

echo ""

# 4. Test WebSocket endpoint
echo "=========================================="
echo "4. Testing WebSocket Endpoint"
echo "=========================================="

test_step "WebSocket availability"
# Check if WebSocket endpoint exists
if curl -sf http://localhost:8088/api/plugin/status > /dev/null 2>&1; then
    test_pass
else
    echo -e "${YELLOW}⚠ SKIP${NC} (endpoint may not be registered yet)"
fi

echo ""

# 5. Check OpenCode API
echo "=========================================="
echo "5. Checking OpenCode API"
echo "=========================================="

test_step "OpenCode running"
if lsof -i :4096 > /dev/null 2>&1; then
    test_pass
else
    echo -e "${YELLOW}⚠ SKIP${NC} (OpenCode not running)"
    echo "  Start with: cd ~/workspace/ai/opencode && bun run dev"
fi

echo ""

# 6. Integration test (if Backend and OpenCode are both running)
echo "=========================================="
echo "6. Integration Test"
echo "=========================================="

if curl -sf http://localhost:8088/healthz > /dev/null && \
   curl -sf http://localhost:4096/api/health > /dev/null 2>&1; then
    
    test_step "Instance discovery"
    INSTANCES=$(curl -s http://localhost:8088/api/instances \
        -H "Authorization: Bearer $TOKEN")
    
    INSTANCE_COUNT=$(echo "$INSTANCES" | jq '.instances | length')
    if [ "$INSTANCE_COUNT" -gt 0 ]; then
        test_pass
        echo "  Instances: $INSTANCE_COUNT"
    else
        test_fail "No instances found"
    fi
else
    echo -e "${YELLOW}⚠ SKIP${NC} (Backend or OpenCode not running)"
fi

echo ""

# 7. File checks
echo "=========================================="
echo "7. File Structure Check"
echo "=========================================="

test_step "Plugin files"
if [ -f "opencode-plugin/dist/index.js" ] && \
   [ -f "opencode-plugin/dist/index.d.ts" ]; then
    test_pass
else
    test_fail "Plugin dist files missing"
fi

test_step "Manager binary"
if [ -f "opencode-manager/opencode-manager" ]; then
    test_pass
else
    test_fail "Manager binary missing"
fi

test_step "Backend WebSocket hub"
if [ -f "backend/internal/websocket/plugin_hub.go" ]; then
    test_pass
else
    test_fail "WebSocket hub file missing"
fi

echo ""

# Summary
echo "=========================================="
echo "Test Summary"
echo "=========================================="
echo -e "Passed: ${GREEN}$PASSED${NC}"
echo -e "Failed: ${RED}$FAILED${NC}"
echo "Total:  $((PASSED + FAILED))"
echo ""

if [ $FAILED -eq 0 ]; then
    echo -e "${GREEN}✓ All tests passed!${NC}"
    exit 0
else
    echo -e "${RED}✗ Some tests failed${NC}"
    exit 1
fi
