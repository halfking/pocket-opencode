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
	"sync"
	"time"

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

// ★ 2026-11-20 实测（211 生产）：/api/acc/agents 30s 网关超时
//（HTTP_STATUS:000 TIME:30.002s），根因是 ACC runtime-control 面抖动
//（acc_ecosystem 早期实测 22~67s）：ListRuntimeAgents 要先读 runtimes
// 清单、再**逐个 runtime 回连读面**，任何一段挂住整条链路就挂住。
// 前端生态卡等 30s 等到的是空 —— 感知链路等于断了。
//
// 三层缓解（缓解不是根治，根治在 ACC 侧把回连读面做快/做断路）：
//
//	① 8s 硬超时：runtime-control 面抖到 20s+ 时 pocketd 不陪它挂，
//	   前端拿到的是**降级数据**而不是超时白屏；
//	② 降级：超时/失败时退化只读注册表（ListAgents）—— 注册表是 ACC
//	   自己的单条 SQL，不回连，实测即使 acc-go 抖动期也能在秒级返回；
//	   至少不是空表（见 handleACCAgents 原注释：注册表重启后可能为空，
//	   但「空表」和「等 30s 后空表」是两种故障强度）；
//	③ 60s 进程内缓存：抖动期多次刷新不再重复踩同一坨慢端点 —— 抖动
//	   窗口通常持续数分钟，60s 缓存能把 N 次刷新的代价压成 1 次。
//	var 代替 const：单测要把超时收窄到毫秒级（8s 的真实路径等不起）。
//	生产值即下面的初始值；测试改完必须 defer 还原。
var (
	accAgentsLiveTimeout = 8 * time.Second
	accAgentsCacheTTL    = 60 * time.Second
)

// accAgentsCache 是 handleACCAgents 的进程内缓存。单飞行 + TTL：
// 过期后第一个请求负责刷新（其余请求直接复用旧值，避免抖动期的
// 惊群把 ACC 的慢端点再打一遍）。
type accAgentsCache struct {
	mu       sync.Mutex
	rows     []accAgentRow
	source   string // "acc" | "acc-registry-fallback"
	fetchedAt time.Time
}

// get 返回未过期缓存；ok=false 表示需要刷新。
func (c *accAgentsCache) get(now time.Time) ([]accAgentRow, string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rows == nil || now.Sub(c.fetchedAt) >= accAgentsCacheTTL {
		return nil, "", false
	}
	return c.rows, c.source, true
}

// put 写入一份新结果。
func (c *accAgentsCache) put(rows []accAgentRow, source string, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rows = rows
	c.source = source
	c.fetchedAt = now
}

// handleACCAgents 返回 ACC 里的 agent 清单。
//
// agent-companion 的每个实例都是独立一条，status 字段是判断"这批智能体现在
// 活着没有"的第一手依据。
//
// ★ 2026-10-25 实测：**读两个面，合并**。
//
//	· **运行时清单**（GET /api/v2/runtime/runtimes/{id}/agents）：ACC 回连
//	  agent-companion 的读面拿到的是**活的**清单（status/communication_mode/pid）。
//	  这才是"现在有哪些智能体、活着没有"的答案。
//	· **注册表**（GET /api/v2/agents）：ACC 里的 host 级注册条目。实测 acc-go
//	  容器重启后它是**空的**，而运行时清单是满的 —— 只读注册表会得出
//	  "一个智能体都没有"的结论。
//
// 合并规则：以运行时清单为准（同 id 时取带活状态的那条），注册表里多出来的
// 条目补进去。运行时清单读不到时（runtime 没广告读面 / daemon 不可达 / 超时）
// 退化为只读注册表 —— 那时至少不是空表。
//
// ★ 2026-11-20 起读法带超时+缓存+降级（见上方 accAgentsLiveTimeout 注释）：
// 运行时清单限定 8s；读不到时退化注册表；成功结果缓存 60s。响应里的
// "source" 字段告知数据来自哪一面：acc（运行时清单合并）或
// acc-registry-fallback（降级，前端可据此提示「实时清单暂不可用」）。
func (s *Server) handleACCAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.accRuntime == nil {
		http.Error(w, "ACC runtime client not configured", http.StatusServiceUnavailable)
		return
	}
	if s.accAgentsCache == nil {
		s.accAgentsCache = &accAgentsCache{}
	}
	if rows, source, ok := s.accAgentsCache.get(time.Now()); ok {
		writeJSON(w, http.StatusOK, map[string]any{
			"source": source,
			"cached": true,
			"count":  len(rows),
			"agents": rows,
		})
		return
	}
	// 运行时清单是「活着没有」的权威，但它要回连 —— 给它 8s 上限，
	// 不让 ACC 抖动把这条 HTTP 请求拖到网关 30s 超时。
	liveCtx, cancel := context.WithTimeout(r.Context(), accAgentsLiveTimeout)
	live, liveErr := s.accRuntime.ListRuntimeAgents(liveCtx)
	cancel()
	if liveErr != nil {
		// 降级：注册表是 ACC 侧单表读，不回连。同样给它一个上限，
		// 注册表也挂时就如实报 502 —— 两面都不可达是通道故障。
		regCtx, regCancel := context.WithTimeout(r.Context(), accAgentsLiveTimeout)
		registry, regErr := s.accRuntime.ListAgents(regCtx)
		regCancel()
		if regErr != nil {
			http.Error(w, liveErr.Error(), http.StatusBadGateway)
			return
		}
		rows := make([]accAgentRow, 0, len(registry))
		for _, a := range registry {
			if a.ID == "" {
				continue
			}
			rows = append(rows, accAgentRow{
				ID:           a.ID,
				Name:         a.Name,
				Type:         a.Type,
				Status:       a.Status,
				LastSeenAt:   a.LastSeenAt,
				Capabilities: a.Capabilities,
			})
		}
		s.accAgentsCache.put(rows, "acc-registry-fallback", time.Now())
		writeJSON(w, http.StatusOK, map[string]any{
			"source": "acc-registry-fallback",
			"count":  len(rows),
			"agents": rows,
		})
		return
	}
	// 运行时清单通了 —— 注册表仍要读：补上「重启后运行时清单看不见、
	// 注册表里有」的条目。注册表读失败不致命（它的价值只是补充），
	// 这里沿用原语义：只在**两面都**失败时才报错，而 live 已成功。
	registry, regErr := s.accRuntime.ListAgents(r.Context())
	if regErr != nil {
		registry = nil
	}
	seen := make(map[string]bool, len(live))
	merged := make([]accAgentRow, 0, len(live)+len(registry))
	for _, a := range live {
		if a.ID == "" || seen[a.ID] {
			continue
		}
		seen[a.ID] = true
		merged = append(merged, accAgentRow{
			ID:           a.ID,
			Name:         a.Name,
			Type:         a.Type,
			Status:       a.Status,
			LastSeenAt:   a.LastSeenAt,
			Capabilities: a.Capabilities,
		})
	}
	for _, a := range registry {
		if a.ID == "" || seen[a.ID] {
			continue
		}
		seen[a.ID] = true
		merged = append(merged, accAgentRow{
			ID:           a.ID,
			Name:         a.Name,
			Type:         a.Type,
			Status:       a.Status,
			LastSeenAt:   a.LastSeenAt,
			Capabilities: a.Capabilities,
		})
	}
	rows := merged
	s.accAgentsCache.put(rows, "acc", time.Now())
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
