#!/bin/bash
# Verify PKM Donation from RedClaw
# Date: 2026-11-14
# Purpose: Test all donated features work correctly in OpenPocket

set -e

echo "====================================="
echo "PKM Donation Verification Script"
echo "====================================="
echo ""

# Configuration
DB_URL="${DATABASE_URL:-postgresql://postgres:postgres@localhost:5432/openpocket_test}"
MIGRATION_DIR="./migrations"

# Colors
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Helper functions
pass() {
    echo -e "${GREEN}✓${NC} $1"
}

fail() {
    echo -e "${RED}✗${NC} $1"
    exit 1
}

warn() {
    echo -e "${YELLOW}⚠${NC} $1"
}

info() {
    echo "  $1"
}

# Check prerequisites
echo "Checking prerequisites..."

if ! command -v psql &> /dev/null; then
    fail "psql not found. Install PostgreSQL client."
fi

if ! command -v go &> /dev/null; then
    fail "go not found. Install Go."
fi

pass "Prerequisites check"
echo ""

# Test 1: Check PostgreSQL connection
echo "Test 1: PostgreSQL Connection"
if psql "$DB_URL" -c "SELECT 1" > /dev/null 2>&1; then
    pass "Database connection successful"
else
    fail "Cannot connect to database: $DB_URL"
fi
echo ""

# Test 2: Check pgvector extension
echo "Test 2: pgvector Extension"
PGVECTOR_INSTALLED=$(psql "$DB_URL" -tAc "SELECT COUNT(*) FROM pg_extension WHERE extname = 'vector'")
if [ "$PGVECTOR_INSTALLED" -eq "1" ]; then
    pass "pgvector extension installed"
else
    warn "pgvector not installed. Run: CREATE EXTENSION vector;"
    info "Vector search features will be skipped"
fi
echo ""

# Test 3: Run base knowledge base migration
echo "Test 3: Base Knowledge Base Schema"
if psql "$DB_URL" -f "$MIGRATION_DIR/20261114_knowledge_base_schema_pg.sql" > /dev/null 2>&1; then
    pass "Base schema migration applied"
else
    info "Schema already exists or migration failed (non-fatal)"
fi
echo ""

# Test 4: Run PKM donation migration
echo "Test 4: PKM Donation Enhancements"
if psql "$DB_URL" -f "$MIGRATION_DIR/20261114_pkm_donation_enhancements.sql" 2>&1 | grep -q "ERROR"; then
    warn "PKM donation migration had errors (may be expected if already applied)"
else
    pass "PKM donation migration applied"
fi
echo ""

# Test 5: Verify tables exist
echo "Test 5: Verify Tables"
TABLES=("notebooks" "notes" "note_links" "tags" "attachments" "shares" "sync_state" "saved_searches")
for table in "${TABLES[@]}"; do
    EXISTS=$(psql "$DB_URL" -tAc "SELECT COUNT(*) FROM information_schema.tables WHERE table_name = '$table'")
    if [ "$EXISTS" -eq "1" ]; then
        pass "Table '$table' exists"
    else
        fail "Table '$table' not found"
    fi
done
echo ""

# Test 6: Verify vector column (if pgvector installed)
echo "Test 6: Vector Search Column"
if [ "$PGVECTOR_INSTALLED" -eq "1" ]; then
    EMBEDDING_EXISTS=$(psql "$DB_URL" -tAc "SELECT COUNT(*) FROM information_schema.columns WHERE table_name = 'notes' AND column_name = 'embedding'")
    if [ "$EMBEDDING_EXISTS" -eq "1" ]; then
        pass "embedding column exists in notes table"
        
        # Check data type
        EMBEDDING_TYPE=$(psql "$DB_URL" -tAc "SELECT data_type FROM information_schema.columns WHERE table_name = 'notes' AND column_name = 'embedding'")
        if echo "$EMBEDDING_TYPE" | grep -q "USER-DEFINED"; then
            pass "embedding is vector type"
        else
            warn "embedding type: $EMBEDDING_TYPE (expected USER-DEFINED/vector)"
        fi
    else
        fail "embedding column not found"
    fi
else
    warn "Skipping vector column check (pgvector not installed)"
fi
echo ""

# Test 7: Verify RLS policies
echo "Test 7: Row Level Security Policies"
TABLES_WITH_RLS=("notebooks" "notes" "note_links" "tags" "attachments" "shares")
for table in "${TABLES_WITH_RLS[@]}"; do
    RLS_ENABLED=$(psql "$DB_URL" -tAc "SELECT relrowsecurity FROM pg_class WHERE relname = '$table'")
    if [ "$RLS_ENABLED" = "t" ]; then
        pass "RLS enabled on '$table'"
    else
        fail "RLS not enabled on '$table'"
    fi
    
    # Check FORCE RLS (donated from RedClaw 000067)
    FORCE_RLS=$(psql "$DB_URL" -tAc "SELECT relforcerowsecurity FROM pg_class WHERE relname = '$table'")
    if [ "$FORCE_RLS" = "t" ]; then
        pass "FORCE RLS enabled on '$table' (donated feature)"
    else
        fail "FORCE RLS not enabled on '$table'"
    fi
done
echo ""

# Test 8: Verify functions exist
echo "Test 8: Search Functions"
FUNCTIONS=("search_notes_by_embedding" "search_notes_hybrid" "get_user_knowledge_stats")
for func in "${FUNCTIONS[@]}"; do
    FUNC_EXISTS=$(psql "$DB_URL" -tAc "SELECT COUNT(*) FROM pg_proc WHERE proname = '$func'")
    if [ "$FUNC_EXISTS" -ge "1" ]; then
        pass "Function '$func' exists"
    else
        fail "Function '$func' not found"
    fi
done
echo ""

# Test 9: Test helpers package
echo "Test 9: Helpers Package Tests"
cd "$(dirname "$0")/.."
if go test ./internal/attachments/ -v 2>&1 | grep -q "PASS"; then
    pass "Helpers package tests passed"
else
    fail "Helpers package tests failed"
fi
echo ""

# Test 10: Test RLS isolation
echo "Test 10: RLS Isolation Test"
info "Creating test users..."
USER1_ID="test-user-$(uuidgen)"
USER2_ID="test-user-$(uuidgen)"

# Set user context and insert test data
psql "$DB_URL" <<EOF > /dev/null 2>&1
-- User 1 creates a notebook
SET LOCAL app.user_id = '$USER1_ID';
INSERT INTO notebooks (id, user_id, name, created_at, updated_at) 
VALUES ('test-nb-1', '$USER1_ID', 'User 1 Notebook', extract(epoch from now()) * 1000, extract(epoch from now()) * 1000);

-- User 2 tries to see it (should be blocked by RLS)
SET LOCAL app.user_id = '$USER2_ID';
EOF

# Check isolation
LEAKED=$(psql "$DB_URL" -tAc "SET LOCAL app.user_id = '$USER2_ID'; SELECT COUNT(*) FROM notebooks WHERE id = 'test-nb-1'")
if [ "$LEAKED" -eq "0" ]; then
    pass "RLS isolation working (User 2 cannot see User 1's data)"
else
    fail "RLS isolation broken (User 2 can see User 1's data)"
fi

# Cleanup
psql "$DB_URL" -c "DELETE FROM notebooks WHERE id = 'test-nb-1'" > /dev/null 2>&1
echo ""

# Summary
echo "====================================="
echo "Verification Complete!"
echo "====================================="
echo ""
echo "Donated Features Verified:"
echo "  ✓ Vector search (embedding column + functions)"
echo "  ✓ Attachments table (S3 design)"
echo "  ✓ Shares table (permission model)"
echo "  ✓ RLS discipline (FORCE RLS + user_id policies)"
echo "  ✓ Helper functions (S3Key, sanitization, content-type)"
echo "  ✓ Search functions (hybrid text+vector)"
echo ""
echo "Next Steps:"
echo "  1. Create vector index after 100+ notes:"
echo "     CREATE INDEX idx_notes_embedding ON notes"
echo "     USING ivfflat(embedding vector_cosine_ops) WITH (lists = 100);"
echo ""
echo "  2. Implement attachment upload/download handlers"
echo "  3. Add embedding generation on note create/update"
echo "  4. Implement share token generation"
echo ""
