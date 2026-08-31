package chatagent

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// MarketplaceMigration 为 chat_agents 表添加市场化支持字段。
//
// 新增字段：
//   - marketplace_id TEXT  — 关联的市场包 ID（从 marketplace 安装时填充）
//   - skill_refs     JSONB — 绑定的技能列表 ["skill-id-1", "skill-id-2"]
//   - publisher      TEXT  — 发布者（用户 ID 或组织名）
//   - version        TEXT  — 版本号（semver）
//   - tags           JSONB — 标签数组 ["productivity", "code"]
//
// 这些字段对内置角色和自定义角色均可用：
//   - 内置角色：由 importer 从 agency-agents-zh 派生时可预填充
//   - 自定义角色：用户创建时可选填
//   - 市场角色：从 marketplace 安装后自动设置 marketplace_id + skill_refs
const marketplaceMigration = `
DO $$
BEGIN
	IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='chat_agents' AND column_name='marketplace_id') THEN
		ALTER TABLE chat_agents ADD COLUMN marketplace_id TEXT;
	END IF;
	IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='chat_agents' AND column_name='skill_refs') THEN
		ALTER TABLE chat_agents ADD COLUMN skill_refs JSONB DEFAULT '[]';
	END IF;
	IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='chat_agents' AND column_name='publisher') THEN
		ALTER TABLE chat_agents ADD COLUMN publisher TEXT;
	END IF;
	IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='chat_agents' AND column_name='version') THEN
		ALTER TABLE chat_agents ADD COLUMN version TEXT;
	END IF;
	IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='chat_agents' AND column_name='tags') THEN
		ALTER TABLE chat_agents ADD COLUMN tags JSONB DEFAULT '[]';
	END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_chat_agents_marketplace ON chat_agents(marketplace_id) WHERE marketplace_id IS NOT NULL;
`

// RunMarketplaceMigration 执行市场化迁移（幂等，可重复调用）。
func RunMarketplaceMigration(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return fmt.Errorf("chatagent: pool not configured")
	}
	_, err := pool.Exec(ctx, marketplaceMigration)
	return err
}
