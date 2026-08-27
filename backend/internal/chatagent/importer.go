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

// ImportBuiltinAgents 从 agency-agents-zh/ 目录批量导入内置角色。
//
// 如果 chat_agents 表已有内置角色（is_builtin=1），跳过导入（幂等）。
// 遍历 repoPath 下的所有 .md 文件，跳过：
//   - 非角色文件（README/CATALOG/AGENT-LIST/UPSTREAM 等大写文件名）
//   - 解析失败的文件（记录 warn，继续处理其他文件）
func (s *Store) ImportBuiltinAgents(ctx context.Context, repoPath string) error {
	if s.pool == nil {
		return fmt.Errorf("store not configured")
	}

	// 检查是否已导入过（表中有内置角色）
	var count int
	if err := s.pool.QueryRow(ctx, "SELECT COUNT(*) FROM chat_agents WHERE is_builtin = 1").Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		log.Printf("[chatagent] %d builtin agents already imported, skipping", count)
		return nil
	}

	log.Printf("[chatagent] importing builtin agents from %s", repoPath)

	var agents []*Agent
	err := filepath.Walk(repoPath, func(path string, info os.FileInfo, err error) error {
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

	// 批量插入（事务）
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	for _, a := range agents {
		_, err := tx.Exec(ctx, `
			INSERT INTO chat_agents (id, workspace_id, name, description, department, emoji, color, system_prompt, is_builtin, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		`, a.ID, a.WorkspaceID, a.Name, a.Description, a.Department, a.Emoji, a.Color, a.SystemPrompt, a.IsBuiltin, a.CreatedAt, a.UpdatedAt)
		if err != nil {
			return fmt.Errorf("insert %s: %w", a.ID, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}

	log.Printf("[chatagent] imported %d builtin agents", len(agents))
	return nil
}
