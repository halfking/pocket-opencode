package server

// server_llm_models.go — S0-B unified LLM BFF: model catalog endpoint.
//
//   GET /api/llm/models   返回当前 workspace 网关下可用模型列表（OpenAI 兼容
//                          GET /v1/models），供前端模型选择器动态填充。
//
// 与 /api/llm/stream 一样，网关凭据（apiKey）只存在于后端；这里用请求时解析出的
// GatewayConfig 现拉客户端，绝不把 key 下发到前端。

import (
	"net/http"

	"github.com/halfking/pocket-opencode/backend/internal/llmgateway"
)

// handleLLMBFFModels 返回当前 workspace 网关下可用的模型列表。
//
// 优先使用网关的实时 /v1/models；当网关未配置（baseURL/apiKey 缺失）时返回 503。
// 响应体：{ models: string[], source: "gateway", base_url: string }
func (s *Server) handleLLMBFFModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	st := s.ResolveGateway(s.workspaceIDFromRequest(r))
	if st.BaseURL == "" || st.APIKey == "" {
		writeError(w, http.StatusServiceUnavailable, "gateway not configured")
		return
	}
	client := llmgateway.NewClient(st.BaseURL, st.APIKey)
	models, err := client.ListModels(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "list models: "+err.Error())
		return
	}
	ids := make([]string, 0, len(models))
	for _, m := range models {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"models":    ids,
		"source":    "gateway",
		"base_url":  st.BaseURL,
		"preferred": st.PreferredModels,
	})
}
