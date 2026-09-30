package server

// 单封邮件的「按需总结」（2026-10-01 需求：详情页给一个总结按钮）。
//
// 为什么不能直接用 /api/emails/classify：那个接口的语义是「批量补齐未分类邮件」，
// 数据源是 ListUnclassifiedScoped —— 已经分类过的邮件根本不会出现在结果里。
// 而用户要的恰恰是「对**当前这封**邮件点一下总结」，这封邮件大概率早已被
// 同步流程分类过。所以这里单开一条按 id 的路径。
//
// 两条硬约束：
//  1. **幂等**：已有摘要就直接返回，不再调 LLM。需求明确「总结后不需要再总结」，
//     同时也避免重复点击反复烧 token。
//  2. **按 (user, workspace) 取数**：不接受 client 传 accountId/workspaceId，
//     一律从请求上下文推导，与邮件详情/正文接口保持一致。

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/aigate"
	"github.com/halfking/pocket-opencode/backend/internal/email"
	"github.com/halfking/pocket-opencode/backend/internal/llmbff"
)

// emailSummarySystemPrompt 约束模型只回 JSON，避免把「好的，摘要如下」也存进去。
const emailSummarySystemPrompt = `你是邮件摘要助手。只输出一个 JSON 对象，不要任何解释文字或 Markdown 代码块。
字段：
- summary: 不超过 60 字的中文摘要，说明这封邮件讲了什么、是否需要用户行动

要求：
- 只依据邮件内容，不要臆造未出现的信息
- 保留关键数字（金额、日期、订单号、单号）
- 若正文为空，则根据主题与发件人概括`

// emailSummaryResponse 是 /api/emails/{id}/summarize 的响应。
type emailSummaryResponse struct {
	EmailID string `json:"emailId"`
	Summary string `json:"summary"`
	// Cached=true 表示这次没有调用 LLM，直接复用了已有摘要。
	Cached bool `json:"cached"`
}

func (s *Server) handleEmailSummarize(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	if s.emailStore == nil {
		writeError(w, http.StatusServiceUnavailable, "email store not configured")
		return
	}
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing email id")
		return
	}
	// 总结需要 LLM；两个网关都没配就明确报错，而不是静默返回空摘要。
	if s.kxmemory == nil && s.llmBFF == nil && s.llm == nil {
		writeError(w, http.StatusServiceUnavailable, "email summarizer not configured")
		return
	}
	userID := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)

	em, err := s.emailStore.GetEmailByIDScoped(r.Context(), id, userID, wsID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if em == nil {
		writeError(w, http.StatusNotFound, "email not found")
		return
	}

	// 幂等：已有摘要直接返回，不调 LLM。
	if existing, ok := reusableSummary(em.AISummary); ok {
		writeJSON(w, http.StatusOK, emailSummaryResponse{
			EmailID: em.ID, Summary: existing, Cached: true,
		})
		return
	}

	summary, err := s.summarizeWithLLM(r.Context(), em, userID, wsID, s.summarizeBody(r.Context(), em))
	if err != nil {
		log.Printf("[email/summarize] %s: %v", em.ID, err)
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, emailSummaryResponse{
		EmailID: em.ID, Summary: summary, Cached: false,
	})
}

// reusableSummary 判定是否可以复用已有摘要（即本次无需再总结）。
//
// 需求明确「总结后不需要再总结」，所以这是硬约束而不是优化：重复调 LLM 既烧
// token，又可能让用户看到每次措辞不同的「总结」。纯函数是为了能脱离数据库单测。
func reusableSummary(aiSummary string) (string, bool) {
	s := strings.TrimSpace(aiSummary)
	if s == "" {
		return "", false
	}
	return s, true
}

// summarizeWithLLM 组装提示词 → 调 LLM → 解析 → 落库。
// bodyText 允许为空（正文已清理时按主题与发件人概括）。
func (s *Server) summarizeWithLLM(
	ctx context.Context, em *email.Email, userID, workspaceID, bodyText string,
) (string, error) {
	model := s.emailClassifyModel(userID, workspaceID)
	if model == "" {
		return "", fmt.Errorf("no model available for email summary")
	}

	parts := []string{
		"发件人: " + firstNonEmptyStr(em.FromName, em.FromAddress),
		"主题: " + firstNonEmptyStr(em.Subject),
	}
	if d := strings.TrimSpace(bodyText); d != "" {
		parts = append(parts, "正文: "+truncateStr(d, 3000))
	} else {
		parts = append(parts, "正文: （无正文或正文已清理）")
	}

	messages := []aigate.ChatMessage{
		{Role: "system", Content: emailSummarySystemPrompt},
		{Role: "user", Content: strings.Join(parts, "\n")},
	}

	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var content string
	if s.llmBFF != nil {
		llmMsgs := make([]llmbff.Message, 0, len(messages))
		for _, m := range messages {
			llmMsgs = append(llmMsgs, llmbff.Message{Role: llmbff.Role(m.Role), Content: m.Content})
		}
		resp, err := s.llmBFF.Chat(callCtx, llmbff.ChatRequest{
			WorkspaceID: workspaceID,
			Model:       model,
			Messages:    llmMsgs,
			Temperature: 0,
			Kind:        "email-summary",
		}, "email-summary")
		if err != nil {
			return "", err
		}
		content = resp.Content
	} else {
		var err error
		content, err = s.llm.Chat(callCtx, model, messages)
		if err != nil {
			return "", err
		}
	}

	summary := parseGatewaySummary(content)
	if summary == "" {
		log.Printf("[email/summarize] %s: unparseable output: %q", em.ID, truncateStr(content, 200))
		return "", fmt.Errorf("unparseable summarizer output")
	}
	if err := s.emailStore.SetSummaryScoped(callCtx, em.ID, userID, workspaceID, summary); err != nil {
		return "", err
	}
	return summary, nil
}

// summarizeBody 读取正文：加密缓存优先，其次 IMAP 回源。
//
// 摘要比分类更需要正文——只有 snippet 常常概括不出「用户要不要做什么」。
func (s *Server) summarizeBody(ctx context.Context, em *email.Email) string {
	if em.BodyPurged {
		return ""
	}
	if b, err := s.readCachedEmailBody(ctx, em.ID, em.UID); err == nil {
		if t := email.ExtractDisplayBody(b); strings.TrimSpace(t) != "" {
			return t
		}
	}
	if s.emailFetcher != nil && em.UID > 0 {
		if b, err := s.emailFetcher.FetchMessageRaw(ctx, em.AccountID, em.UID); err == nil {
			return email.ExtractDisplayBody(b)
		}
	}
	return ""
}


// parseGatewaySummary 从模型输出里取出 summary 字段。
//
// 与分类的 JSON 解析分开：总结只需要一个字段，独立解析可以避免为了一个
// summary 去跑整条分类校验（category 缺失就直接判失败，对总结毫无意义）。
func parseGatewaySummary(raw string) string {
	text := strings.TrimSpace(raw)
	if text == "" {
		return ""
	}
	// 去掉 ```json ... ``` 围栏
	if strings.HasPrefix(text, "```") {
		if i := strings.Index(text, "\n"); i >= 0 {
			text = text[i+1:]
		}
		if j := strings.LastIndex(text, "```"); j >= 0 {
			text = text[:j]
		}
		text = strings.TrimSpace(text)
	}
	// 找到第一个 { 与最后一个 } 之间的 JSON
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return ""
	}
	var obj struct {
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal([]byte(text[start:end+1]), &obj); err != nil {
		return ""
	}
	return truncateStr(strings.TrimSpace(obj.Summary), 200)
}
