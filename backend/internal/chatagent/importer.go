package chatagent

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// FrontMatter 是 agency-agents-zh/*.md 文件的 YAML 头部结构。
type FrontMatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Emoji       string `yaml:"emoji"`
	Color       string `yaml:"color"`
}

// ParseAgentFile 从一个 .md 文件解析出 Agent。
//
// 文件格式：
//   ---
//   name: AI 工程师
//   description: 精通机器学习...
//   emoji: 🤖
//   color: purple
//   ---
//
//   # AI 工程师
//   你是**AI 工程师**...
//
// 返回的 Agent.ID 从文件名提取（去掉 .md 后缀），Department 从目录名提取。
func ParseAgentFile(path string) (*Agent, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	content := string(raw)
	if !strings.HasPrefix(content, "---\n") {
		return nil, fmt.Errorf("missing YAML frontmatter")
	}

	// 提取 YAML frontmatter（第一个 --- 到第二个 ---）
	rest := content[4:]
	idx := strings.Index(rest, "\n---\n")
	if idx < 0 {
		return nil, fmt.Errorf("invalid frontmatter format (missing closing ---)")
	}

	yamlPart := rest[:idx]
	markdownPart := strings.TrimSpace(rest[idx+5:])

	var fm FrontMatter
	if err := yaml.Unmarshal([]byte(yamlPart), &fm); err != nil {
		return nil, fmt.Errorf("parse YAML: %w", err)
	}

	// 从路径提取 id 与 department
	base := filepath.Base(path)
	id := strings.TrimSuffix(base, ".md")
	dept := filepath.Base(filepath.Dir(path))

	return &Agent{
		ID:           id,
		WorkspaceID:  "", // 内置角色 workspace_id 为空（全局共享）
		Name:         fm.Name,
		Description:  fm.Description,
		Department:   dept,
		Emoji:        fm.Emoji,
		Color:        fm.Color,
		SystemPrompt: markdownPart,
		IsBuiltin:    true,
		CreatedAt:    time.Now().Unix(),
		UpdatedAt:    time.Now().Unix(),
	}, nil
}

// importBuiltin 是 ImportBuiltinAgents 的共享实现，对 StoreIface 都适用。
// 它只调用接口的 List + Create 方法（不直接接触底层 DB），所以 PG Store
// 和 SQLiteStore 都能复用同一份遍历 / 解析逻辑。
func importBuiltin(ctx context.Context, store StoreIface, repoPath string) error {
	all, err := store.List(ctx, "", "")
	if err != nil {
		return fmt.Errorf("list agents: %w", err)
	}
	builtinCount := 0
	for _, a := range all {
		if a.IsBuiltin {
			builtinCount++
		}
	}
	if builtinCount > 0 {
		log.Printf("[chatagent] %d builtin agents already imported, skipping", builtinCount)
		return nil
	}

	log.Printf("[chatagent] importing builtin agents from %s", repoPath)

	var agents []*Agent
	err = filepath.Walk(repoPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}

		// 跳过非角色文件（文件名全大写 = 文档/清单类文件）
		base := filepath.Base(path)
		if strings.ToUpper(base) == base || base == "UPSTREAM.md" {
			return nil
		}

		agent, err := ParseAgentFile(path)
		if err != nil {
			log.Printf("[chatagent] skip %s: %v", path, err)
			return nil
		}
		agents = append(agents, agent)
		return nil
	})
	if err != nil {
		return err
	}

	if len(agents) == 0 {
		log.Printf("[chatagent] no valid agent files found in %s", repoPath)
		return nil
	}

	for _, a := range agents {
		if err := store.Create(ctx, a); err != nil {
			return fmt.Errorf("insert %s: %w", a.ID, err)
		}
	}

	log.Printf("[chatagent] imported %d builtin agents", len(agents))
	return nil
}

// ImportBuiltinAgents 在 PG Store 上调用：委托给通用 importBuiltin。
func (s *Store) ImportBuiltinAgents(ctx context.Context, repoPath string) error {
	if s.pool == nil {
		return fmt.Errorf("store not configured")
	}
	return importBuiltin(ctx, s, repoPath)
}

// SQLiteStore 上的 ImportBuiltinAgents 留给后续 sprint 合入 SQLiteStore
// 实现后再启用（依赖 SQLiteStore 自身的 List/Create 接口实现）。
