// Package taskgen 提供任务生成与智能体关联功能
package taskgen

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Config 任务生成配置
type Config struct {
	// LLMBaseURL LLM服务地址
	LLMBaseURL string
	// LLMAPIKey LLM API密钥
	LLMAPIKey string
	// LLMModel 使用的模型
	LLMModel string
	// ACCRuntimeURL ACC Runtime地址（用于提交任务）
	ACCRuntimeURL string
	// ACCSecret ACC控制密钥
	ACCSecret string
}

// Task 生成的任务
type Task struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`       // 任务标题
	Description string    `json:"description"` // 任务描述
	Prompt      string    `json:"prompt"`      // 可用的提示词
	Priority    string    `json:"priority"`    // 优先级：high/medium/low
	Tags        []string  `json:"tags"`        // 标签
	CreatedAt   time.Time `json:"created_at"`
	Status      string    `json:"status"`      // pending/submitted/running/completed/failed
	AgentTaskID string    `json:"agent_task_id"` // 智能体任务ID（提交后）
}

// TaskGenerator 任务生成器
type TaskGenerator struct {
	config Config
}

// NewTaskGenerator 创建任务生成器
func NewTaskGenerator(cfg Config) *TaskGenerator {
	return &TaskGenerator{
		config: cfg,
	}
}

// AnalyzeRequest 分析请求
type AnalyzeRequest struct {
	Content     string   // 会议内容/转写文本
	Summary     string   // 会议总结
	Participants []string // 参与人员
	Keywords    []string // 关键词
}

// AnalyzeResult 分析结果
type AnalyzeResult struct {
	Tasks          []Task   `json:"tasks"`           // 生成的任务列表
	ActionItems    []string `json:"action_items"`    // 行动项
	Decisions      []string `json:"decisions"`       // 决策记录
	FollowUps      []string `json:"follow_ups"`      // 后续跟进
	ProcessTimeMS  int64    `json:"process_time_ms"` // 处理时间
}

// AnalyzeAndGenerateTasks 分析内容并生成任务
//
// 流程：
// 1. 提取行动项和决策
// 2. 识别任务模式（待办、跟进、研究等）
// 3. 为每个任务生成可用的提示词
// 4. 分配优先级
func (g *TaskGenerator) AnalyzeAndGenerateTasks(ctx context.Context, req AnalyzeRequest) (*AnalyzeResult, error) {
	t0 := time.Now()
	
	result := &AnalyzeResult{
		Tasks:         make([]Task, 0),
		ActionItems:   make([]string, 0),
		Decisions:     make([]string, 0),
		FollowUps:     make([]string, 0),
	}
	
	// 1. 提取行动项
	actionItems := g.extractActionItems(req.Content)
	result.ActionItems = actionItems
	
	// 2. 提取决策
	decisions := g.extractDecisions(req.Content)
	result.Decisions = decisions
	
	// 3. 提取后续跟进
	followUps := g.extractFollowUps(req.Content)
	result.FollowUps = followUps
	
	// 4. 为每个行动项生成任务
	for i, item := range actionItems {
		task := g.generateTask(item, i+1, req)
		result.Tasks = append(result.Tasks, task)
	}
	
	// 5. 为决策生成确认任务
	for i, decision := range decisions {
		task := Task{
			ID:          fmt.Sprintf("decision-%d-%d", time.Now().Unix(), i),
			Title:       "确认决策：" + truncateString(decision, 50),
			Description: decision,
			Prompt:      g.generateDecisionPrompt(decision, req),
			Priority:    "medium",
			Tags:        []string{"decision", "confirmation"},
			CreatedAt:   time.Now(),
			Status:      "pending",
		}
		result.Tasks = append(result.Tasks, task)
	}
	
	result.ProcessTimeMS = time.Since(t0).Milliseconds()
	return result, nil
}

// extractActionItems 提取行动项
func (g *TaskGenerator) extractActionItems(content string) []string {
	items := make([]string, 0)
	
	// 匹配常见的行动项模式
	patterns := []string{
		`需要\s*(\S+)\s*(.+)`,
		`要求\s*(\S+)\s*(.+)`,
		`请\s*(\S+)\s*(.+)`,
		`TODO:\s*(.+)`,
		`行动项[：:]\s*(.+)`,
		`(\S+)\s*负责\s*(.+)`,
	}
	
	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindAllStringSubmatch(content, -1)
		for _, match := range matches {
			if len(match) > 1 {
				item := strings.TrimSpace(match[len(match)-1])
				if len(item) > 5 { // 过滤过短的匹配
					items = append(items, item)
				}
			}
		}
	}
	
	return items
}

// extractDecisions 提取决策
func (g *TaskGenerator) extractDecisions(content string) []string {
	decisions := make([]string, 0)
	
	// 匹配决策模式
	patterns := []string{
		`决定\s*(.+)`,
		`确定\s*(.+)`,
		`最终\s*(.+)`,
		`达成共识[：:]\s*(.+)`,
	}
	
	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindAllStringSubmatch(content, -1)
		for _, match := range matches {
			if len(match) > 1 {
				decision := strings.TrimSpace(match[1])
				if len(decision) > 5 {
					decisions = append(decisions, decision)
				}
			}
		}
	}
	
	return decisions
}

// extractFollowUps 提取后续跟进
func (g *TaskGenerator) extractFollowUps(content string) []string {
	followUps := make([]string, 0)
	
	// 匹配跟进模式
	patterns := []string{
		`下一步\s*(.+)`,
		`后续\s*(.+)`,
		`跟进[：:]\s*(.+)`,
		`待确认[：:]\s*(.+)`,
	}
	
	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindAllStringSubmatch(content, -1)
		for _, match := range matches {
			if len(match) > 1 {
				followUp := strings.TrimSpace(match[1])
				if len(followUp) > 5 {
					followUps = append(followUps, followUp)
				}
			}
		}
	}
	
	return followUps
}

// generateTask 生成任务
func (g *TaskGenerator) generateTask(actionItem string, index int, req AnalyzeRequest) Task {
	task := Task{
		ID:          fmt.Sprintf("task-%d-%d", time.Now().Unix(), index),
		Title:       truncateString(actionItem, 80),
		Description: actionItem,
		Prompt:      g.generatePrompt(actionItem, req),
		Priority:    g.determinePriority(actionItem),
		Tags:        g.extractTags(actionItem),
		CreatedAt:   time.Now(),
		Status:      "pending",
	}
	return task
}

// generatePrompt 生成可用的提示词
func (g *TaskGenerator) generatePrompt(actionItem string, req AnalyzeRequest) string {
	// 构建上下文丰富的提示词
	var prompt strings.Builder
	
	prompt.WriteString("## 任务背景\n\n")
	if req.Summary != "" {
		prompt.WriteString("会议总结：" + req.Summary + "\n\n")
	}
	
	prompt.WriteString("## 具体任务\n\n")
	prompt.WriteString(actionItem + "\n\n")
	
	prompt.WriteString("## 执行要求\n\n")
	prompt.WriteString("1. 理解任务目标和预期成果\n")
	prompt.WriteString("2. 制定详细的执行步骤\n")
	prompt.WriteString("3. 识别可能的风险和依赖\n")
	prompt.WriteString("4. 提供可交付的成果\n\n")
	
	if len(req.Keywords) > 0 {
		prompt.WriteString("## 相关关键词\n\n")
		prompt.WriteString(strings.Join(req.Keywords, ", ") + "\n\n")
	}
	
	prompt.WriteString("## 输出格式\n\n")
	prompt.WriteString("请提供：\n")
	prompt.WriteString("- 执行计划\n")
	prompt.WriteString("- 预计工作量\n")
	prompt.WriteString("- 交付物清单\n")
	prompt.WriteString("- 潜在风险\n")
	
	return prompt.String()
}

// generateDecisionPrompt 生成决策确认提示词
func (g *TaskGenerator) generateDecisionPrompt(decision string, req AnalyzeRequest) string {
	var prompt strings.Builder
	
	prompt.WriteString("## 决策记录\n\n")
	prompt.WriteString(decision + "\n\n")
	
	prompt.WriteString("## 确认事项\n\n")
	prompt.WriteString("请确认以下内容：\n")
	prompt.WriteString("1. 决策的背景和原因\n")
	prompt.WriteString("2. 决策的影响范围\n")
	prompt.WriteString("3. 实施步骤和时间表\n")
	prompt.WriteString("4. 责任人和相关方\n")
	prompt.WriteString("5. 跟踪和评估方法\n")
	
	return prompt.String()
}

// determinePriority 确定优先级
func (g *TaskGenerator) determinePriority(actionItem string) string {
	item := strings.ToLower(actionItem)
	
	// 高优先级关键词
	highPriorityKeywords := []string{"紧急", "立即", "马上", "尽快", "urgent", "asap", "critical"}
	for _, keyword := range highPriorityKeywords {
		if strings.Contains(item, keyword) {
			return "high"
		}
	}
	
	// 低优先级关键词
	lowPriorityKeywords := []string{"考虑", "建议", "可以", "可选", "optional", "consider"}
	for _, keyword := range lowPriorityKeywords {
		if strings.Contains(item, keyword) {
			return "low"
		}
	}
	
	return "medium"
}

// extractTags 提取标签
func (g *TaskGenerator) extractTags(actionItem string) []string {
	tags := make([]string, 0)
	item := strings.ToLower(actionItem)
	
	// 预定义的标签映射
	tagKeywords := map[string][]string{
		"research":    {"研究", "调研", "分析", "research", "investigate"},
		"development": {"开发", "实现", "编码", "develop", "implement", "code"},
		"review":      {"审核", "评审", "检查", "review", "check"},
		"document":    {"文档", "记录", "整理", "document", "write"},
		"meeting":     {"会议", "讨论", "沟通", "meeting", "discuss"},
		"testing":     {"测试", "验证", "test", "verify"},
	}
	
	for tag, keywords := range tagKeywords {
		for _, keyword := range keywords {
			if strings.Contains(item, keyword) {
				tags = append(tags, tag)
				break
			}
		}
	}
	
	if len(tags) == 0 {
		tags = append(tags, "general")
	}
	
	return tags
}

// SubmitToAgent 提交任务给智能体
func (g *TaskGenerator) SubmitToAgent(ctx context.Context, task *Task) error {
	if g.config.ACCRuntimeURL == "" {
		return fmt.Errorf("ACC Runtime not configured")
	}
	
	// TODO: 实际实现需要调用ACC Runtime API
	// 这里返回模拟结果
	task.AgentTaskID = fmt.Sprintf("acc-task-%d", time.Now().Unix())
	task.Status = "submitted"
	
	return nil
}

// GetTaskStatus 获取任务执行状态
func (g *TaskGenerator) GetTaskStatus(ctx context.Context, agentTaskID string) (string, error) {
	if g.config.ACCRuntimeURL == "" {
		return "", fmt.Errorf("ACC Runtime not configured")
	}
	
	// TODO: 实际实现需要调用ACC Runtime API查询状态
	// 这里返回模拟状态
	return "running", nil
}

// truncateString 截断字符串
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
