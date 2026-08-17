package quota

import "github.com/halfking/pocket-opencode/backend/internal/llmbff"

// ModelPrice is USD per 1K tokens (input / output separately). Numbers reflect
// 2026-08 snapshot; production deployments may override via PG / config.
//
// "default" sentinel matches any unrecognized model name and is the safest
// fallback (we never want to charge $0 for a model we don't recognize — that's
// how S3 cost dashboards drift away from reality).
type ModelPrice struct {
	Model         string  `json:"model"`
	InputPer1K    float64 `json:"input_per_1k"`
	OutputPer1K   float64 `json:"output_per_1k"`
}

// defaultPriceTable is the static pricing table. Future iterations should
// source this from PG (per-deployment) so we can update without a redeploy.
var defaultPriceTable = []ModelPrice{
	// OpenAI
	{Model: "gpt-4o", InputPer1K: 0.005, OutputPer1K: 0.015},
	{Model: "gpt-4o-mini", InputPer1K: 0.00015, OutputPer1K: 0.0006},
	{Model: "gpt-4.1", InputPer1K: 0.002, OutputPer1K: 0.008},
	{Model: "gpt-4.1-mini", InputPer1K: 0.0004, OutputPer1K: 0.0016},
	{Model: "gpt-4.1-nano", InputPer1K: 0.0001, OutputPer1K: 0.0004},
	{Model: "o1", InputPer1K: 0.015, OutputPer1K: 0.06},
	{Model: "o3", InputPer1K: 0.01, OutputPer1K: 0.04},
	// Anthropic
	{Model: "claude-3-5-sonnet", InputPer1K: 0.003, OutputPer1K: 0.015},
	{Model: "claude-3-5-haiku", InputPer1K: 0.0008, OutputPer1K: 0.004},
	{Model: "claude-3-opus", InputPer1K: 0.015, OutputPer1K: 0.075},
	// Google
	{Model: "gemini-1.5-pro", InputPer1K: 0.00125, OutputPer1K: 0.005},
	{Model: "gemini-1.5-flash", InputPer1K: 0.000075, OutputPer1K: 0.0003},
	// Groq (whisper is STT, not in this table)
	{Model: "llama-3.1-70b-versatile", InputPer1K: 0.00059, OutputPer1K: 0.00079},
	{Model: "llama-3.1-8b-instant", InputPer1K: 0.00005, OutputPer1K: 0.00008},
	// Embeddings
	{Model: "text-embedding-3-small", InputPer1K: 0.00002, OutputPer1K: 0},
	{Model: "text-embedding-3-large", InputPer1K: 0.00013, OutputPer1K: 0},
}

// DefaultPrice returns the price for the given model. Unknown models fall back
// to a conservative estimate so usage dashboards don't drift to zero.
func DefaultPrice(model string) ModelPrice {
	for _, p := range defaultPriceTable {
		if p.Model == model {
			return p
		}
	}
	// 兜底：取表中位价；显式标注这是估算，避免误把未识别模型当 0。
	return ModelPrice{
		Model:       model,
		InputPer1K:  0.001,
		OutputPer1K: 0.003,
	}
}

// CostFromUsage 把 llmbff.Usage 转为美元成本。Token 数 ≤ 0 直接返 0。
func CostFromUsage(model string, u llmbff.Usage) float64 {
	if u.PromptTokens <= 0 && u.CompletionTokens <= 0 {
		return 0
	}
	p := DefaultPrice(model)
	cost := float64(u.PromptTokens)/1000.0*p.InputPer1K +
		float64(u.CompletionTokens)/1000.0*p.OutputPer1K
	// 四舍五入到 1e-6 美元，避免浮点尾巴污染数据库。
	return float64(int64(cost*1e6+0.5)) / 1e6
}

// ApplyCost 计算并写回 Usage.CostUSD，返回新值（不修改原值）。
func ApplyCost(model string, u llmbff.Usage) llmbff.Usage {
	u.CostUSD = CostFromUsage(model, u)
	return u
}
