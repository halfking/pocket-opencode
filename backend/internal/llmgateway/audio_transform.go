// Package llmgateway — audio_transform.go
//
// 网关转写后处理二件套（/v1/audio/refine + /v1/audio/analyze）的 Go 客户端。
// 2026-10-06 ASR 多供应商轮新增：会议流的最终形态是「转写 → 精修 → 周期
// 分析出提示」，这里先给服务端一条直连网关的干净调用路径（不依赖前端的
// stt-cloud.ts）；server_meeting 的接入由后续轮完成（该文件当前有并行
// 工作在改，避免在同文件上撞车）。
//
// 两端点的契约（网关 domains/streaming/audio_transform.go，2026-10-06 起）：
//
//	POST /v1/audio/refine   {model,text,language,hotwords,context,ops,
//	                         include_corrections}
//	                        → {refined,corrections[],ignored_hotwords[],llm_model}
//	POST /v1/audio/analyze  {model,transcript,prior_summary,style,language,
//	                         max_points}
//	                        → {summary,key_points[],decisions[],action_items[],
//	                           open_questions[],hints[],topics[],llm_model}
//
// 鉴权与 /v1/* 数据面同款（Bearer sk-*）；4xx 会带上游原文（如智谱余额
// 1113），调用方按 status 分流即可，无需解析错误体。
package llmgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// RefineRequest 是精细化转写的入参。Model 必填（执行精修的 chat 模型，
// 成本归属本 Client 的 APIKey）；其余可空。
type RefineRequest struct {
	Model    string   `json:"model"`
	Text     string   `json:"text"`
	Language string   `json:"language,omitempty"`
	Hotwords []string `json:"hotwords,omitempty"`
	Context  string   `json:"context,omitempty"`
	// IncludeCorrections 为 true 时响应带逐条修改清单（from/to/reason）。
	IncludeCorrections bool `json:"include_corrections,omitempty"`
}

// RefineCorrection 是一条真实发生的实质修改。
type RefineCorrection struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Reason string `json:"reason,omitempty"`
}

// RefineResult 是精细化转写的出参。IgnoredWords 是精修结果里没出现的热词
// ——可能是同音字形没纠过来，也可能确实没说；为空时才代表热词全部生效。
type RefineResult struct {
	Refined      string             `json:"refined"`
	Corrections  []RefineCorrection `json:"corrections,omitempty"`
	IgnoredWords []string           `json:"ignored_hotwords,omitempty"`
	LLMModel     string             `json:"llm_model,omitempty"`
}

// AnalyzeRequest 是实时总结分析的入参。PriorSummary 非空即为增量滚动模式
// ——响应的 Summary 是合并新增内容后的**最新全文摘要**，调用方持有滚动
// 状态，周期性调用即可（网关无状态）。
type AnalyzeRequest struct {
	Model        string `json:"model"`
	Transcript   string `json:"transcript"`
	PriorSummary string `json:"prior_summary,omitempty"`
	// Style: auto / meeting / interview / customer_service / lecture。
	Style    string `json:"style,omitempty"`
	Language string `json:"language,omitempty"`
	// MaxPoints 是 key_points 上限（1-30，网关默认 8）。
	MaxPoints int `json:"max_points,omitempty"`
}

// AnalyzeActionItem 是一条待办；Owner 未在音频里出现时为空。
type AnalyzeActionItem struct {
	Text  string `json:"text"`
	Owner string `json:"owner,omitempty"`
}

// AnalyzeResult 是总结分析的出参。Hints 是给主持人的实时提示（值得追问
// 的点/风险/待确认数字），是本端点区别于纯摘要的价值所在；文本太短时
// Summary 会如实说明且各列表为空——调用方不要把空列表当错误。
type AnalyzeResult struct {
	Summary       string              `json:"summary"`
	KeyPoints     []string            `json:"key_points"`
	Decisions     []string            `json:"decisions"`
	ActionItems   []AnalyzeActionItem `json:"action_items"`
	OpenQuestions []string            `json:"open_questions"`
	Hints         []string            `json:"hints"`
	Topics        []string            `json:"topics"`
	LLMModel      string              `json:"llm_model,omitempty"`
}

// 调用超时：LLM 长文输出在慢上游上会到十几秒（2026-10-06 实测
// minimax-text-01 refine 10.6s / analyze 14.3s），不要按普通 REST 的
// 5s 级超时配。
const audioTransformTimeout = 90 * time.Second

// RefineTranscription 调网关 /v1/audio/refine。
func (c *Client) RefineTranscription(ctx context.Context, req RefineRequest) (*RefineResult, error) {
	if strings.TrimSpace(req.Model) == "" || strings.TrimSpace(req.Text) == "" {
		return nil, fmt.Errorf("model and text are required")
	}
	var out RefineResult
	if err := c.postAudioTransform(ctx, "/v1/audio/refine", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AnalyzeTranscription 调网关 /v1/audio/analyze。
func (c *Client) AnalyzeTranscription(ctx context.Context, req AnalyzeRequest) (*AnalyzeResult, error) {
	if strings.TrimSpace(req.Model) == "" || strings.TrimSpace(req.Transcript) == "" {
		return nil, fmt.Errorf("model and transcript are required")
	}
	var out AnalyzeResult
	if err := c.postAudioTransform(ctx, "/v1/audio/analyze", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) postAudioTransform(ctx context.Context, path string, req any, out any) error {
	ctx, cancel := context.WithTimeout(ctx, audioTransformTimeout)
	defer cancel()
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var eb struct {
			Error struct {
				Message string `json:"message"`
				Code    string `json:"code"`
			} `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&eb)
		if eb.Error.Code != "" {
			return fmt.Errorf("gateway %s: %s", eb.Error.Code, eb.Error.Message)
		}
		return fmt.Errorf("gateway http %d on %s", resp.StatusCode, path)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
