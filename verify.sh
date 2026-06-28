#!/bin/bash
# OpenCode Pocket quick verification script

set -e

echo "OpenCode Pocket verification"
echo "================================"
echo ""

GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m'

check_pass() {
    echo -e "${GREEN}[PASS] $1${NC}"
}

check_fail() {
    echo -e "${RED}[FAIL] $1${NC}"
}

check_warn() {
    echo -e "${YELLOW}[WARN] $1${NC}"
}

echo "1. Check repository structure"
echo "----------------------------"

if [ -d "backend" ] && [ -f "backend/cmd/pocketd/main.go" ]; then
    check_pass "Backend structure present"
else
    check_fail "Backend structure missing"
    exit 1
fi

if [ -d "frontend" ] && [ -f "frontend/package.json" ]; then
    check_pass "Frontend structure present"
else
    check_fail "Frontend structure missing"
    exit 1
fi

DOC_COUNT=$(find docs -name "*.md" 2>/dev/null | wc -l | tr -d ' ')
if [ "$DOC_COUNT" -ge 5 ]; then
    check_pass "Documentation set present ($DOC_COUNT files)"
else
    check_warn "Documentation set looks small ($DOC_COUNT files)"
fi

echo ""
echo "2. Check backend build"
echo "----------------------------"

cd backend
if go build -o /tmp/pocketd cmd/pocketd/main.go >/tmp/pocketd-build.log 2>&1; then
    check_pass "Backend build succeeded"
    rm -f /tmp/pocketd /tmp/pocketd-build.log
else
    check_fail "Backend build failed"
    cat /tmp/pocketd-build.log
    exit 1
fi

echo ""
echo "3. Run backend tests"
echo "----------------------------"

if go test ./... >/tmp/pocketd-test.log 2>&1; then
    check_pass "Backend tests passed"
    rm -f /tmp/pocketd-test.log
else
    check_fail "Backend tests failed"
    cat /tmp/pocketd-test.log
    exit 1
fi

cd ..

echo ""
echo "4. Check frontend build"
echo "----------------------------"

cd frontend
if npm run build >/tmp/frontend-build.log 2>&1; then
    check_pass "Frontend build succeeded"
    rm -f /tmp/frontend-build.log
else
    check_fail "Frontend build failed"
    cat /tmp/frontend-build.log
    exit 1
fi

cd ..

echo ""
echo "5. Check Android shell scaffold"
echo "----------------------------"

if [ -f "android/capacitor.config.ts" ]; then
    check_pass "Android Capacitor config present"
else
    check_warn "Android Capacitor config missing"
fi

echo ""
echo "6. Validate key documents"
echo "----------------------------"

DOCS=(
    "README.md"
    "LICENSE"
    "DESIGN.md"
    "IMPLEMENTATION_PLAN.md"
    "docs/QUICK_INTEGRATION.md"
    "docs/INTEGRATION.md"
    "docs/PRODUCTION_DEPLOYMENT.md"
    "docs/DEPLOYMENT_ENV_VARS.md"
    "docs/DEPLOYMENT_CHECKLIST.md"
)

for doc in "${DOCS[@]}"; do
    if [ -f "$doc" ]; then
        check_pass "$(basename "$doc")"
    else
        check_warn "$(basename "$doc") missing"
    fi
done

echo ""
echo "7. Check example configuration files"
echo "----------------------------"

if [ -f ".env.example" ]; then
    check_pass ".env.example present"
else
    check_warn ".env.example missing"
fi

if [ -f ".env.integration.example" ]; then
    check_pass ".env.integration.example present"
else
    check_warn ".env.integration.example missing"
fi

echo ""
echo "8. Summary"
echo "----------------------------"
check_pass "Verification complete"
echo "Backend, frontend, docs, and example config were checked."
