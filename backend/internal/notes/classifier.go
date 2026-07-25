// internal/notes/classifier.go
package notes

import (
	"strings"
)

// ClassificationResult 分类结果
type ClassificationResult struct {
	Type string   `json:"type"` // tech / meeting / todo / idea / product / learning / general
	Tags []string `json:"tags"`
}

// Classifier 笔记 AI 分类引擎（规则引擎，后续可替换为 LLM）
type Classifier struct{}

// Classify 对笔记内容进行分类
func (c *Classifier) Classify(content string) *ClassificationResult {
	lower := strings.ToLower(content)

	result := &ClassificationResult{
		Type: "general",
		Tags: c.ExtractTags(content),
	}

	// 会议记录
	if hasAny(lower, []string{"会议", "周会", "讨论", "sprint", "agenda", "会议纪要",
		"参会", "议程", "决策"}) {
		result.Type = "meeting"
		return result
	}

	// 灵感想法
	if hasAny(lower, []string{"想法", "主意", "灵感", "突发奇想", "想到", "建议"}) {
		result.Type = "idea"
		return result
	}

	// 学习笔记（优先于技术，因为学习内容可能包含技术关键词如 Kubernetes）
	if hasAny(lower, []string{"学习", "教程", "笔记", "知识点", "总结",
		"理解", "概念", "原理"}) {
		result.Type = "learning"
		return result
	}

	// 产品需求（优先于待办，因为产品反馈可能包含"需要"关键词）
	if hasAny(lower, []string{"用户", "需求", "功能", "产品", "优化", "反馈",
		"体验", "界面"}) {
		result.Type = "product"
		return result
	}

	// 待办事项（优先于技术，因为待办可能包含技术关键词如 API）
	if hasAny(lower, []string{"记得", "需要", "别忘了", "todo", "待办", "完成",
		"处理", "跟进", "提醒"}) {
		result.Type = "todo"
		return result
	}

	// 技术笔记
	if hasAny(lower, []string{"代码", "函数", "api", "go语言", "python", "javascript", "实现",
		"架构", "数据库", "算法", "docker", "kubernetes", "k8s", "git", "编译"}) {
		result.Type = "tech"
		return result
	}

	return result
}

// ExtractTags 从内容中提取标签
func (c *Classifier) ExtractTags(content string) []string {
	var tags []string
	seen := make(map[string]bool)

	techKeywords := map[string]string{
		"go": "Go", "golang": "Go",
		"python": "Python", "javascript": "JavaScript",
		"typescript": "TypeScript", "vue": "Vue",
		"react": "React", "docker": "Docker",
		"kubernetes": "Kubernetes", "k8s": "Kubernetes",
		"postgresql": "PostgreSQL", "postgres": "PostgreSQL",
		"redis": "Redis", "mysql": "MySQL",
		"aws": "AWS", "api": "API",
		"ai": "AI", "llm": "LLM",
		"git": "Git", "linux": "Linux",
	}

	lower := strings.ToLower(content)
	for keyword, tag := range techKeywords {
		if strings.Contains(lower, keyword) && !seen[tag] {
			tags = append(tags, tag)
			seen[tag] = true
		}
	}

	return tags
}

func hasAny(s string, keywords []string) bool {
	for _, kw := range keywords {
		if strings.Contains(s, kw) {
			return true
		}
	}
	return false
}