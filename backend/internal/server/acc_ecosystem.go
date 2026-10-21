package server

// ACC 生态读取面：让 pocketd 把「经 ACC 看到的 agent-companion 生态」原样
// 呈现出来 —— 注册的 agent（哪些 agent-companion 实例活着）、canonical
// 任务、以及各 agent 上报回来的会话。
//
// 为什么要有这层（2026-10-22 实测）：
//
//   · pocketd 此前**只能**直连 agent-companion（v1 面，AC_API_SECRET）读
//     transcript，**没有**一条经 ACC 的读取路径；而 ACC 才是多机器 agent
//     汇聚的地方 —— 本机 ACC 上挂着十几个 agent-companion 实例
//     （acs-macbook-30 / acs-local / runtime-dev-01 …）。
//   · pocketd 已有 ACC MCP 客户端，但**只**在两处被用到：按 task_id 查会话
//     （session-bundle）与同步 ACC 任务。而 ACC 的 canonical task 列表在
//     本机实测是**空的**（/api/v2/canonical/tasks → tasks:[]），于是
//     「按 task 查会话」永远查不到 —— ACC 里明明有 agent-companion 上报的
//     会话，pocketd 却一个也列不出来。
//
// 所以这层补的是**读取入口**，不改任何既有语义：
//   · agents  / projects / tasks 走 ACC v2 REST（accruntime，HS256 服务 JWT）
//   · sessions 走 ACC MCP 的 acc_list_sessions（mcp 客户端，租户内全量）
//
// 鉴权沿用 pocketd 惯例：requireAuth（用户态）。ACC 那一侧的租户/权限
// 校验照旧由 ACC 用服务 JWT 的 claims 完成，pocketd 不代做。

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/halfking/pocket-opencode/backend/internal/accruntime"
)

// accAgentRow 是返回给调用方的 agent 记录（ACC 侧原样字段）。
type accAgentRow struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Type         string   `json:"type,omitempty"`
	Status       string   `json:"status,omitempty"`
	LastSeenAt   string   `json:"last_seen_at,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
}

// handleACCAgents 返回 ACC agent 注册表里的全部 agent。
//
// agent-companion 的每个实例都是独立一条（名字都叫 agent-companion，id 不同），
// status 字段是判断"这批智能体现在活着没有"的第一手依据。
func (s *Server) handleACCAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.accRuntime == nil {
		http.Error(w, "ACC runtime client not configured", http.StatusServiceUnavailable)
		return
	}
	agents, err := s.accRuntime.ListAgents(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	rows := make([]accAgentRow, 0, len(agents))
	for _, a := range agents {
		rows = append(rows, accAgentRow{
			ID:           a.ID,
			Name:         a.Name,
			Type:         a.Type,
			Status:       a.Status,
			LastSeenAt:   a.LastSeenAt,
			Capabilities: a.Capabilities,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"source": "acc",
		"count":  len(rows),
		"agents": rows,
	})
}

// handleACCTasks 返回 ACC canonical contract 的任务面。
//
// 条目按**原始 JSON** 透传（json.RawMessage）：ACC 的 task 结构随 canonical
// contract 演进，pocketd 抄一份字段表就会变成"字段永远缺一个"的影子契约，
// 透传既不丢字段、ACC 加字段时也自动跟随。
func (s *Server) handleACCTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.accRuntime == nil {
		http.Error(w, "ACC runtime client not configured", http.StatusServiceUnavailable)
		return
	}
	limit := 50
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 200 {
		limit = 200
	}
	tasks, err := s.accRuntime.ListCanonicalTasks(r.Context(), limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"source": "acc",
		"count":  len(tasks),
		"tasks":  rawJSONList(tasks),
	})
}

// handleACCProjects 返回 ACC canonical contract 的项目面（agent-companion
// 实例在 ACC 里的归属单元）。同样按原始 JSON 透传。
func (s *Server) handleACCProjects(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.accRuntime == nil {
		http.Error(w, "ACC runtime client not configured", http.StatusServiceUnavailable)
		return
	}
	projects, err := s.accRuntime.ListProjects(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"source":   "acc",
		"count":    len(projects),
		"projects": rawJSONList(projects),
	})
}

// handleACCSessions 返回 ACC 里**全部**已上报会话（当前租户内）。
//
// 走 MCP 的 acc_list_sessions（不带 task_id）。与 /api/tasks/{id}/session-bundle
// 的区别：后者按已知 task_id 查单条任务的会话，而本机 ACC 的 canonical task
// 列表是空的 ⇒ 按 task 查不到任何东西，会话"存在于 ACC 却列不出来"。这里
// 不带过滤，把 agent-companion 上报回来的会话整批列出。
//
// 字段映射见 mcp.AccSession：ACC 把 id / agent_session_id / gw_session_id /
// dispatch_id 放在**顶层**，老代码只读一个 ACC 从不返回的 "metadata"，导致
// 会话 id 解析成空串（session-bundle 实测返回过 "id":""）。
func (s *Server) handleACCSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.mcpClient == nil {
		http.Error(w, "ACC MCP client not configured", http.StatusServiceUnavailable)
		return
	}
	limit := 50
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 200 {
		limit = 200
	}
	sessions, err := s.mcpClient.ListAllSessions(r.Context(), limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	rows := make([]map[string]any, 0, len(sessions))
	for _, sess := range sessions {
		row := map[string]any{
			"id":             sess.ID,
			"taskId":         sess.TaskID,
			"gatewayType":    sess.GatewayType,
			"agentId":        sess.AgentID,
			"agentKind":      sess.AgentKind,
			"agentSessionId": sess.AgentSessionID,
			"gwSessionId":    sess.GwSessionID,
			"dispatchId":     sess.DispatchID,
			"input":          sess.Input,
			"output":         sess.Output,
			"inputTokens":    sess.InputTokens,
			"outputTokens":   sess.OutputTokens,
			"createdAt":      sess.CreatedAt,
			"deviceName":     sess.DeviceName,
			"modelId":        sess.ModelID,
			"sessionPath":    sess.SessionPath,
			"tenantId":       sess.TenantID,
		}
		rows = append(rows, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"source":   "acc",
		"count":    len(rows),
		"sessions": rows,
	})
}

// rawJSONList 把 []json.RawMessage 转成能直接写进响应体的列表。
// 空的 RawMessage 会被跳过（避免写出 null 这种坏 JSON）。
func rawJSONList(items []json.RawMessage) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(items))
	for _, it := range items {
		if len(strings.TrimSpace(string(it))) == 0 {
			continue
		}
		out = append(out, it)
	}
	return out
}

// accEcosystemReader 是本文件依赖的 ACC 读取面。断言放在这里而不是
// 在 handler 里逐个 nil-check：少一个方法要在**编译期**炸，而不是等线上
// 才发现某个路由静默 503。
type accEcosystemReader interface {
	ListAgents(ctx context.Context) ([]accruntime.Agent, error)
	ListCanonicalTasks(ctx context.Context, limit int) ([]json.RawMessage, error)
	ListProjects(ctx context.Context) ([]json.RawMessage, error)
}

var _ accEcosystemReader = (*accruntime.Client)(nil)
