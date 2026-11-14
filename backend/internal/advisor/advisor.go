// Package advisor 实现动态参谋功能
// 在录音转写过程中实时检索相关知识并推送建议
package advisor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Config 动态参谋配置
type Config struct {
	// KxmemoryBaseURL kxmemory服务地址
	KxmemoryBaseURL string
	// KxmemoryAPIKey API密钥
	KxmemoryAPIKey string
	// QueryInterval 查询间隔（避免频繁查询）
	QueryInterval time.Duration
	// MinKeywordLength 最小关键词长度
	MinKeywordLength int
	// MaxSuggestions 每次最多返回建议数
	MaxSuggestions int
}

// Advisor 动态参谋
type Advisor struct {
	config Config
	// lastQueryTime 上次查询时间（防抖）
	lastQueryTime time.Time
	// keywordBuffer 关键词缓冲区
	keywordBuffer []string
}

// NewAdvisor 创建动态参谋
func NewAdvisor(cfg Config) *Advisor {
	if cfg.QueryInterval == 0 {
		cfg.QueryInterval = 5 * time.Second
	}
	if cfg.MinKeywordLength == 0 {
		cfg.MinKeywordLength = 2
	}
	if cfg.MaxSuggestions == 0 {
		cfg.MaxSuggestions = 5
	}
	return &Advisor{
		config:        cfg,
		keywordBuffer: make([]string, 0, 20),
	}
}

// Suggestion 建议项
type Suggestion struct {
	Type    string  `json:"type"`    // note, memora, web
	Title   string  `json:"title"`   // 标题
	Content string  `json:"content"` // 内容摘要
	URL     string  `json:"url"`     // 链接（如果有）
	Score   float64 `json:"score"`   // 相关度分数
}

// OnTranscriptUpdate 处理转写更新
//
// 提取关键词，定期查询知识库，返回相关建议。
// 使用防抖策略避免频繁查询。
func (a *Advisor) OnTranscriptUpdate(ctx context.Context, newText string) ([]Suggestion, error) {
	if newText == "" {
		return nil, nil
	}

	// 提取关键词
	keywords := a.extractKeywords(newText)
	a.keywordBuffer = append(a.keywordBuffer, keywords...)

	// 防抖：距上次查询不足间隔时跳过
	now := time.Now()
	if now.Sub(a.lastQueryTime) < a.config.QueryInterval {
		return nil, nil
	}

	// 没有关键词时跳过
	if len(a.keywordBuffer) == 0 {
		return nil, nil
	}

	a.lastQueryTime = now

	// 构造查询词
	query := strings.Join(a.keywordBuffer, " ")
	a.keywordBuffer = a.keywordBuffer[:0] // 清空缓冲区

	// 查询知识库
	suggestions, err := a.queryKnowledge(ctx, query)
	if err != nil {
		// 查询失败不阻塞转写流程
		return nil, fmt.Errorf("query knowledge: %w", err)
	}

	return suggestions, nil
}

// extractKeywords 提取关键词（简化实现）
//
// TODO: 可以集成更复杂的NLP分词和关键词提取算法
func (a *Advisor) extractKeywords(text string) []string {
	// 简单实现：按空格分词，过滤短词和停用词
	words := strings.Fields(text)
	keywords := make([]string, 0, len(words))

	stopWords := map[string]bool{
		"的": true, "了": true, "在": true, "是": true, "我": true,
		"有": true, "和": true, "就": true, "不": true, "人": true,
		"都": true, "一": true, "个": true, "上": true, "也": true,
		"这": true, "那": true, "要": true, "会": true, "可": true,
		"the": true, "is": true, "a": true, "an": true, "and": true,
		"or": true, "but": true, "in": true, "on": true, "at": true,
	}

	for _, word := range words {
		word = strings.TrimSpace(word)
		// 过滤短词和停用词
		if len(word) >= a.config.MinKeywordLength && !stopWords[strings.ToLower(word)] {
			keywords = append(keywords, word)
		}
	}

	return keywords
}

// queryKnowledge 查询知识库
//
// 并行查询多个源：笔记、memora、网络搜索
func (a *Advisor) queryKnowledge(ctx context.Context, query string) ([]Suggestion, error) {
	suggestions := make([]Suggestion, 0, a.config.MaxSuggestions)
	
	// 1. 查询kxmemory（如果配置了）
	if a.config.KxmemoryBaseURL != "" {
		kxSuggestions, err := a.queryKxmemory(ctx, query)
		if err == nil {
			suggestions = append(suggestions, kxSuggestions...)
		}
	}
	
	// 2. 本地笔记查询（TODO: 集成notes store）
	// 3. 网络搜索（TODO: 可选集成）

	// 限制返回数量
	if len(suggestions) > a.config.MaxSuggestions {
		suggestions = suggestions[:a.config.MaxSuggestions]
	}
	
	// 如果没有结果，返回提示
	if len(suggestions) == 0 {
		suggestions = []Suggestion{
			{
				Type:    "system",
				Title:   "未找到相关信息",
				Content: fmt.Sprintf("暂无与「%s」相关的知识", query),
				Score:   0.0,
			},
		}
	}

	return suggestions, nil
}

// queryKxmemory 查询kxmemory服务
func (a *Advisor) queryKxmemory(ctx context.Context, query string) ([]Suggestion, error) {
	// 构造kxmemory检索请求
	reqBody := map[string]interface{}{
		"query": query,
		"top_k": a.config.MaxSuggestions,
	}
	
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	
	// 发送请求（简化实现，生产环境需要更完善的HTTP客户端）
	url := a.config.KxmemoryBaseURL + "/v1/search"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	
	req.Header.Set("Content-Type", "application/json")
	if a.config.KxmemoryAPIKey != "" {
		req.Header.Set("Authorization", "Bearer "+a.config.KxmemoryAPIKey)
	}
	
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("kxmemory returned %d", resp.StatusCode)
	}
	
	// 解析响应
	var result struct {
		Results []struct {
			Title   string  `json:"title"`
			Content string  `json:"content"`
			Score   float64 `json:"score"`
			URL     string  `json:"url"`
		} `json:"results"`
	}
	
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	
	// 转换为Suggestion格式
	suggestions := make([]Suggestion, 0, len(result.Results))
	for _, r := range result.Results {
		suggestions = append(suggestions, Suggestion{
			Type:    "memora",
			Title:   r.Title,
			Content: r.Content,
			URL:     r.URL,
			Score:   r.Score,
		})
	}
	
	return suggestions, nil
}

// Reset 重置状态（新会议开始时调用）
func (a *Advisor) Reset() {
	a.lastQueryTime = time.Time{}
	a.keywordBuffer = a.keywordBuffer[:0]
}
