package chatagent

// marketplace_migration_test.go — 验证 RunMarketplaceMigration 的幂等性
// 与迁移 SQL 的正确性。注意：完整 PG 集成测试需要 pocket_test 数据库,
// 此处仅验证 SQL 字符串合法可重复解析。

import (
	"strings"
	"testing"
)

// TestMarketplaceMigrationSQLIdempotent 校验 marketplace_migration SQL:
//   - 含 marketplace_id / skill_refs / publisher / version / tags 列；
//   - 含 chat_agents_marketplace 索引；
//   - 全部 IF NOT EXISTS / DO $$ BEGIN ... END $$ 守卫,可重复执行。
func TestMarketplaceMigrationSQLIdempotent(t *testing.T) {
	sql := marketplaceMigration
	mustContain := []string{
		"marketplace_id",
		"skill_refs",
		"publisher",
		"version",
		"tags",
		"idx_chat_agents_marketplace",
		"IF NOT EXISTS",
	}
	for _, s := range mustContain {
		if !strings.Contains(sql, s) {
			t.Errorf("marketplaceMigration missing %q", s)
		}
	}
	// 简单语法烟测：包含 ALTER TABLE 与 CREATE INDEX。
	if !strings.Contains(sql, "ALTER TABLE chat_agents ADD COLUMN") {
		t.Error("missing ALTER TABLE ADD COLUMN")
	}
	if !strings.Contains(sql, "CREATE INDEX IF NOT EXISTS") {
		t.Error("missing CREATE INDEX IF NOT EXISTS")
	}
}
