package server

// integration_status.go — P3 §5 「企业集成只读状态」端点。
//
// GET /api/integration/status 返回每个外部集成的「已配置 / 已连接 +
// 当前 capabilities」快照。该端点供 admin / ops 视图使用，未来前端
// 「集成状态」页面直接消费；当前未集成前端，admin 可直接 curl 验证。
//
// 行为约束：
//   - 仅 requireAuth；不强制 admin（admin 校验在后续 Phase D 引入；
//     本轮不阻塞 admin 视图外的常规用户了解集成是否启用）。
//   - 每个 connector 单独列 capabilities.connector、read/write 与 tools，
//     与 mcp.Client.Capabilities() 保持一致。
//   - 当 cfg 未设置对应 base URL / api key 时返回 disabled；**绝不**直接
//     暴露 baseURL / apiKey 字符串本身（避免内部地址泄露）。

import (
	"net/http"
	"os"
)

// integrationStatusResponse 是 /api/integration/status 的响应结构。
type integrationStatusResponse struct {
	Integrations map[string]integrationEntry `json:"integrations"`
}

// integrationEntry 是单个集成的状态摘要。
type integrationEntry struct {
	Enabled      bool     `json:"enabled"`
	Configured   bool     `json:"configured"` // 凭据是否齐全
	Read         bool     `json:"read"`
	Write        bool     `json:"write"`
	Tools        []string `json:"tools,omitempty"`
	Capabilities string   `json:"capabilities,omitempty"` // 人类可读描述
}

// handleIntegrationStatus 暴露企业集成状态快照。
func (s *Server) handleIntegrationStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	resp := integrationStatusResponse{
		Integrations: map[string]integrationEntry{},
	}

	// ACC：通过 mcpClient 是否被注入 + Capabilities() 派生。
	// T1.2 起 MCP 客户端具备写能力（acc_create_task / acc_task_claim /
	// acc_task_complete / acc_report_session），描述串随 caps.Write 变化，
	// 不再硬编码 read-only。注意：这只表示「pocketd 能调 ACC 的写 tool」，
	// /api/tasks POST source=acc 仍然 fail-closed 拒绝（见 server.go 的 acc 守卫）。
	if s.mcpClient != nil {
		caps := s.mcpClient.Capabilities()
		mode := "read-only: "
		if caps.Write {
			mode = "read-write: "
		}
		resp.Integrations["acc"] = integrationEntry{
			Enabled:      caps.Read,
			Configured:   true,
			Read:         caps.Read,
			Write:        caps.Write,
			Tools:        caps.Tools,
			Capabilities: mode + caps.Connector,
		}
	} else {
		resp.Integrations["acc"] = integrationEntry{
			Enabled:      false,
			Configured:   false,
			Capabilities: "disabled: POCKET_MCP_BASE_URL not set",
		}
	}

	// kxmemory：通过注入的 client 是否非 nil 判定；写能力永远为 false
	// （kxmemory.Client 当前无任何写语义，只有 classify / daily_summary /
	// stats 三个只读 / 写入笔记分类的远端调用，本端只做「可消费性」判定）。
	if s.kxmemory != nil {
		resp.Integrations["kxmemory"] = integrationEntry{
			Enabled:      true,
			Configured:   true,
			Read:         true,
			Write:        false,
			Capabilities: "classify / daily_summary / stats",
		}
	} else {
		resp.Integrations["kxmemory"] = integrationEntry{
			Enabled:      false,
			Configured:   false,
			Capabilities: "disabled: POCKET_KXMEMORY_BASE_URL not set",
		}
	}

	// llm-gateway：不能用 llmGWCache 非空（newServer 无条件创建）也不
	// 能用 snapshot.BaseURL（default 态带硬编码回退 URL）判定。配置信
	// 号 = env 显式设置 或 cache 里有该 workspace 的持久化条目
	// （POST /api/llm-gateway/config 保存并从 PG 加载）。
	gwConfigured := os.Getenv("POCKET_LLM_GATEWAY_URL") != "" ||
		s.llmGWCache.has(s.workspaceIDFromRequest(r))
	if gwConfigured {
		resp.Integrations["llm_gateway"] = integrationEntry{
			Enabled:      true,
			Configured:   true,
			Read:         true,
			Write:        false, // 当前仅提供模型列表 / 探测 / 配置；写视为「启用配置」语义，不在只读 scope
			Capabilities: "models / nodes / probe / config",
		}
	} else {
		resp.Integrations["llm_gateway"] = integrationEntry{
			Enabled:      false,
			Configured:   false,
			Capabilities: "disabled: gateway not explicitly configured",
		}
	}

	writeJSON(w, http.StatusOK, resp)
}
