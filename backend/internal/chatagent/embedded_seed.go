package chatagent

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
)

// 内置专家库种子（由 backend/cmd/gen-agent-seed 从 agency-agents-zh 生成）：
//
//	go run ./cmd/gen-agent-seed -repo /path/to/agency-agents-zh \
//	     -o internal/chatagent/seed/agents.json -format json
//
// 内嵌后 pocketd 在任意环境（PG / SQLite、无外网、无克隆仓库）首次启动都能
// 自动初始化完整专家库，不再依赖 POCKET_AGENTS_REPO_PATH 或手工导入 SQL。
//
//go:embed seed/agents.json
var embeddedSeedJSON []byte

func parseEmbeddedSeed() ([]*Agent, error) {
	var agents []*Agent
	if err := json.Unmarshal(embeddedSeedJSON, &agents); err != nil {
		return nil, fmt.Errorf("parse embedded seed: %w", err)
	}
	if len(agents) == 0 {
		return nil, fmt.Errorf("embedded seed is empty")
	}
	return agents, nil
}

// ensureFromSlice 是嵌入式 / 仓库导入共用的落库循环。
func ensureFromSlice(ctx context.Context, store StoreIface, agents []*Agent, source string) error {
	for _, a := range agents {
		a.IsBuiltin = true
		a.WorkspaceID = ""
		if err := store.Create(ctx, a); err != nil {
			return fmt.Errorf("insert %s: %w", a.ID, err)
		}
	}
	log.Printf("[chatagent] seeded %d builtin agents from %s", len(agents), source)
	return nil
}

// hasBuiltinAgents 判断库里是否已有任意内置角色（导入幂等条件）。
func hasBuiltinAgents(ctx context.Context, store StoreIface) (bool, error) {
	all, err := store.List(ctx, "", "")
	if err != nil {
		return false, fmt.Errorf("list agents: %w", err)
	}
	for _, a := range all {
		if a.IsBuiltin {
			return true, nil
		}
	}
	return false, nil
}

// EnsureBuiltinAgents 启动时保证专家库已初始化（幂等：已有任意内置角色即跳过）。
//
// 来源优先级：
//  1. repoPath 非空 → 从 agency-agents-zh 仓库目录解析（支持未发版的最新角色）；
//     仓库导入失败（路径失效等）自动回落到内嵌种子并告警。
//  2. 否则 → 使用编译期内嵌种子 agents.json。
//
// 注意：管理员通过 API 删除全部内置角色后，重启会重新灌入种子——这是"恢复
// 出厂专家库"的预期行为；只要还剩任意内置/自定义角色，导入即静默跳过。
func EnsureBuiltinAgents(ctx context.Context, store StoreIface, repoPath string) error {
	has, err := hasBuiltinAgents(ctx, store)
	if err != nil {
		return err
	}
	if has {
		log.Printf("[chatagent] builtin agents already present, seed skipped")
		return nil
	}

	if repoPath != "" {
		agents, err := CollectBuiltinAgents(repoPath)
		if err == nil && len(agents) > 0 {
			return ensureFromSlice(ctx, store, agents, "repo "+repoPath)
		}
		if err != nil {
			log.Printf("[chatagent] WARN: collect from repo %s failed: %v, falling back to embedded seed", repoPath, err)
		} else {
			log.Printf("[chatagent] WARN: no agents in repo %s, falling back to embedded seed", repoPath)
		}
	}

	agents, err := parseEmbeddedSeed()
	if err != nil {
		return err
	}
	return ensureFromSlice(ctx, store, agents, "embedded seed")
}
