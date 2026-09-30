package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/aigate"
	"github.com/halfking/pocket-opencode/backend/internal/email"
	"github.com/halfking/pocket-opencode/backend/internal/llmbff"
)

// 邮件自动归纳整理的「网关兜底分类器」。
//
// 背景：邮件分类原本只走 kxmemory（仓外独立 FastAPI 服务，由
// POCKET_KXMEMORY_BASE_URL 装配）。没配这个环境变量时 s.kxmemory == nil，
// /api/emails/classify 一律 503 —— 也就是用户报的「邮件管理没有自动归纳
// 整理的能力」。而应用本身已经配好了 LLM 网关（同一套配置驱动 AI 对话），
// 分类却完全用不上它。
//
// 这里补一条兜底路径：kxmemory 不可用或调用失败时，改用已配置的 LLM 网关
// 做同样的分类，让「自动归纳整理」在只有网关的部署里也能工作。

// emailClassifySystemPrompt 要求模型只回一个 JSON 对象。
//
// 分类取值必须与 email.categoryWhitelist 一致（work / bill / notification /
// personal / marketing / spam），否则 NormalizeCategory 会把未知值一律
// 归到 personal，账单类邮件会被错分。
const emailClassifySystemPrompt = `你是邮件归类助手。只输出一个 JSON 对象，不要任何解释文字或 Markdown 代码块。
字段：
- category: 只能是 work / bill / notification / personal / marketing / spam 之一
- importance: 只能是 high / medium / low 之一
- summary: 不超过 40 字的中文摘要
- suggested_action: 一句话建议动作，可为空字符串

判据：
- bill: 账单、发票、对账单、付款、订阅扣费、税务
- work: 工作协作、项目、审批、招聘、运维通知
- notification: 系统告警、状态提醒、纯通知类
- personal: 私人往来、家庭、朋友
- marketing: 促销、广告、推荐
- spam: 垃圾邮件、欺诈`

// jsonFenceRE 匹配模型偶尔会加上的 ```json ... ``` 包裹。
var jsonFenceRE = regexp.MustCompile("(?s)```(?:json)?\\s*(.*?)```")

// firstJSONObjectRE 在自由文本里抓第一个 {...} 块（模型有时会在 JSON 前后
// 加一句"好的，分类结果如下："）。
var firstJSONObjectRE = regexp.MustCompile(`(?s)\{.*\}`)

type gatewayClassification struct {
	Category        string `json:"category"`
	Importance      string `json:"importance"`
	Summary         string `json:"summary"`
	SuggestedAction string `json:"suggested_action"`
}

// parseGatewayClassification 从模型输出里解析分类结果。
//
// 保持为纯函数以便单测：模型输出的形态是这个链路最脆的一环——多一句
// 寒暄、多一层代码块围栏，就会让整条自动归纳静默失效。
func parseGatewayClassification(content string) (category, importance, summary, action string, ok bool) {
	s := strings.TrimSpace(content)
	if s == "" {
		return "", "", "", "", false
	}
	if m := jsonFenceRE.FindStringSubmatch(s); len(m) == 2 {
		s = strings.TrimSpace(m[1])
	}
	if !strings.HasPrefix(s, "{") {
		m := firstJSONObjectRE.FindString(s)
		if m == "" {
			return "", "", "", "", false
		}
		s = m
	}
	var g gatewayClassification
	if err := json.Unmarshal([]byte(s), &g); err != nil {
		return "", "", "", "", false
	}
	category = email.NormalizeCategory(g.Category)
	if category == "" {
		return "", "", "", "", false
	}
	importance = strings.ToLower(strings.TrimSpace(g.Importance))
	switch importance {
	case "high", "medium", "low":
	default:
		// 模型漏给或给错 importance 时按 medium 兜底：归类已经成功，
		// 不该因为一个次要字段整封判失败。
		importance = "medium"
	}
	summary = strings.TrimSpace(g.Summary)
	if len([]rune(summary)) > 60 {
		summary = string([]rune(summary)[:60])
	}
	action = strings.TrimSpace(g.SuggestedAction)
	return category, importance, summary, action, true
}

// emailClassifyModel 解析分类要用的模型：优先 workspace 网关的 preferred
// 列表首项，其次 cfg.LLMModel。全空返回 ""，由调用方判为不可用。
func (s *Server) emailClassifyModel(userID, workspaceID string) string {
	gw := s.ResolveGatewayForUser(userID, workspaceID)
	for _, m := range gw.PreferredModels {
		if m = strings.TrimSpace(m); m != "" {
			return m
		}
	}
	return strings.TrimSpace(s.cfg.LLMModel)
}

// classifyViaGateway 用已配置的 LLM 网关给单封邮件分类。
func (s *Server) classifyViaGateway(ctx context.Context, it email.ClassifyItem, userID, workspaceID string) (classifyResultJSON, error) {
	out := classifyResultJSON{EmailID: it.ID}

	if s.llmBFF == nil && s.llm == nil {
		out.Error = "no classifier available (kxmemory and llm gateway both unconfigured)"
		return out, fmt.Errorf("%s", out.Error)
	}
	model := s.emailClassifyModel(userID, workspaceID)
	if model == "" {
		out.Error = "no model available for email classification"
		return out, fmt.Errorf("%s", out.Error)
	}

	// 只喂 snippet，不喂整封正文：分类不需要全文，而正文可能很大
	// （带附件的邮件 BODY[] 可达数 MB）。
	user := strings.TrimSpace(strings.Join([]string{
		"发件人: " + strings.TrimSpace(firstNonEmptyStr(it.FromName, it.FromAddress)),
		"主题: " + it.Subject,
		"摘要: " + truncateStr(it.Snippet, 600),
	}, "\n"))

	messages := []aigate.ChatMessage{
		{Role: "system", Content: emailClassifySystemPrompt},
		{Role: "user", Content: user},
	}

	callCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
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
			Kind:        "email-classify",
		}, "email-classify")
		if err != nil {
			out.Error = err.Error()
			return out, err
		}
		content = resp.Content
	} else {
		var err error
		content, err = s.llm.Chat(callCtx, model, messages)
		if err != nil {
			out.Error = err.Error()
			return out, err
		}
	}

	category, importance, summary, action, ok := parseGatewayClassification(content)
	if !ok {
		out.Error = "unparseable classifier output"
		log.Printf("[email/classify] %s: unparseable gateway output: %q", it.ID, truncateStr(content, 200))
		return out, fmt.Errorf("%s", out.Error)
	}

	out.Category = category
	out.Importance = importance
	out.Summary = summary
	if err := s.emailStore.SetClassificationScoped(callCtx, it.ID, userID, workspaceID,
		category, importance, summary, action); err != nil {
		out.Error = err.Error()
		return out, err
	}
	return out, nil
}

// firstNonEmptyStr 取第一个非空字符串（发件人显示名缺失时退回地址）。
// session_bundle.go 里已有一个同名的 firstNonEmpty，这里避免重名。
func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
