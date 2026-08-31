// gen-agent-seed — 从 agency-agents-zh 角色仓库生成 chat_agents 种子 SQL。
//
// 用途：把内置专家角色（提示词）以 SQL 种子文件形式内置到数据库，使重建后
// 的库无需运行时克隆角色仓库即开箱可用（运行时 POCKET_AGENTS_REPO_PATH 导入
// 因幂等条件会自动跳过，两者不冲突）。
//
// 用法：
//
//	go run ./cmd/gen-agent-seed -repo /path/to/agency-agents-zh -o deploy/sql/chat_agents_seed.sql
//
// 产物自包含：CREATE TABLE IF NOT EXISTS（与 chatagent.Store 的 DDL 一致）+
// 索引 + INSERT ... ON CONFLICT (id) DO NOTHING（可重复执行）。
// created_at/updated_at 固定为 SEED_TS，保证产物确定性（可 diff/可审计）。
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/halfking/pocket-opencode/backend/internal/chatagent"
)

// SEED_TS 固定时间戳（Unix 秒）。更新种子时递增，保证 ON CONFLICT 之外
// 的时间字段也可预测。
const SEED_TS int64 = 1756569600 // 2026-08-31 00:00:00 UTC

func main() {
	repoPath := flag.String("repo", "", "agency-agents-zh 仓库路径（必填）")
	outPath := flag.String("o", "", "输出 SQL 文件路径（缺省打印到 stdout）")
	flag.Parse()

	if *repoPath == "" {
		fmt.Fprintln(os.Stderr, "用法: gen-agent-seed -repo /path/to/agency-agents-zh [-o out.sql]")
		os.Exit(1)
	}
	abs, err := filepath.Abs(*repoPath)
	if err != nil {
		fatal(err)
	}

	agents, err := chatagent.CollectBuiltinAgents(abs)
	if err != nil {
		fatal(err)
	}
	if len(agents) == 0 {
		fatal(fmt.Errorf("no valid agent files found in %s", abs))
	}
	// 确定性排序：department → id，产物可 diff。
	sort.Slice(agents, func(i, j int) bool {
		if agents[i].Department != agents[j].Department {
			return agents[i].Department < agents[j].Department
		}
		return agents[i].ID < agents[j].ID
	})

	var b strings.Builder
	b.WriteString("-- ============================================================\n")
	b.WriteString("-- chat_agents 种子数据（内置专家角色 / 提示词）\n")
	b.WriteString("-- 由 backend/cmd/gen-agent-seed 从 agency-agents-zh 生成，请勿手改。\n")
	b.WriteString("-- 幂等：可重复执行（ON CONFLICT DO NOTHING）。\n")
	fmt.Fprintf(&b, "-- 角色 %d 个 · 生成基准时间戳 %d\n", len(agents), SEED_TS)
	b.WriteString("-- ============================================================\n\n")
	b.WriteString("BEGIN;\n\n")
	b.WriteString(`CREATE TABLE IF NOT EXISTS chat_agents (
	id            TEXT PRIMARY KEY,
	workspace_id  TEXT NOT NULL,
	name          TEXT NOT NULL,
	description   TEXT NOT NULL DEFAULT '',
	department    TEXT NOT NULL,
	emoji         TEXT,
	color         TEXT,
	system_prompt TEXT NOT NULL,
	is_builtin    INTEGER NOT NULL DEFAULT 0,
	created_at    INTEGER NOT NULL,
	updated_at    INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_chat_agents_ws ON chat_agents(workspace_id);
CREATE INDEX IF NOT EXISTS idx_chat_agents_dept ON chat_agents(department);
CREATE INDEX IF NOT EXISTS idx_chat_agents_builtin ON chat_agents(is_builtin);

`)

	for _, a := range agents {
		if strings.TrimSpace(a.ID) == "" || strings.ContainsAny(a.ID, "'\"\n") {
			fatal(fmt.Errorf("agent id invalid: %q", a.ID))
		}
		fmt.Fprintf(&b,
			"INSERT INTO chat_agents (id, workspace_id, name, description, department, emoji, color, system_prompt, is_builtin, created_at, updated_at) VALUES ('%s', '', '%s', '%s', '%s', %s, %s, '%s', 1, %d, %d) ON CONFLICT (id) DO NOTHING;\n",
			sqlEscape(a.ID),
			sqlEscape(a.Name),
			sqlEscape(a.Description),
			sqlEscape(a.Department),
			sqlNullable(a.Emoji),
			sqlNullable(a.Color),
			sqlEscape(a.SystemPrompt),
			SEED_TS, SEED_TS,
		)
	}

	b.WriteString("\nCOMMIT;\n")

	if *outPath == "" {
		os.Stdout.WriteString(b.String())
		return
	}
	if err := os.MkdirAll(filepath.Dir(*outPath), 0o755); err != nil {
		fatal(err)
	}
	if err := os.WriteFile(*outPath, []byte(b.String()), 0o644); err != nil {
		fatal(err)
	}
	fmt.Fprintf(os.Stderr, "[gen-agent-seed] %d agents -> %s\n", len(agents), *outPath)
}

// sqlEscape 单引号转义（PostgreSQL 字符串字面量）。
func sqlEscape(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

// sqlNullable 空值输出 NULL，非空输出转义字面量。
func sqlNullable(s string) string {
	if s == "" {
		return "NULL"
	}
	return "'" + sqlEscape(s) + "'"
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "[gen-agent-seed]", err)
	os.Exit(1)
}
