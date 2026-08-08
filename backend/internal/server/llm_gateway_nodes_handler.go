package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/halfking/pocket-opencode/backend/internal/redclaw"
)

// ─────────────────────────────────────────────────────────────────────────────
// 白名单代理表
//
// 这里刻意不做通用 pass-through（比如 /api/llm-gateway/nodes/{id}/raw?path=…）。
// 那种设计等于把 pocket 变成一个"带凭据的任意 HTTP 代理"：任何拿到 pocket
// 普通用户 token 的人都能借网关的 super_admin 身份打网关的任意端点，包括
// 删除租户、导出凭据密文。所以每条上游路径都要在下面显式声明。
// ─────────────────────────────────────────────────────────────────────────────

// gatewayProxyRoute 描述一条允许代理的上游端点。
type gatewayProxyRoute struct {
	// upstreamMethod 是发给网关的方法，与移动端用的方法解耦
	// （移动端有些"动作"用 POST，但上游其实是 PUT）。
	upstreamMethod string
	// upstreamPath 是上游路径。含 {cid} / {pid} 占位符时由 pathParams 填充。
	upstreamPath string
	// write 为 true 时要求 pocket 侧 admin 角色，并记审计。
	write bool
	// allowedQuery 白名单化 query 参数：只有列出的参数会被透传，
	// 其余一律丢弃，避免调用方注入 tenant_id 之类的越权参数。
	allowedQuery []string
	// forcedQuery 强制附加的 query（例如 monitor-summary 的 detail 模式）。
	forcedQuery map[string]string
}

// gatewayProxyRoutes 的 key 是移动端可见的动作名，形如 "GET providers"。
// 路径参数用 {cid}/{pid} 表示，由 URL 里的 segment 提供。
var gatewayProxyRoutes = map[string]gatewayProxyRoute{
	// ── 读：供应商 ──
	"GET providers": {
		upstreamMethod: http.MethodGet,
		upstreamPath:   "/api/providers",
		allowedQuery:   []string{"search"},
	},

	// ── 读：凭据 ──
	// 列表模式不带 credential_id，上游返回聚合计数；不含 models[]。
	"GET credentials": {
		upstreamMethod: http.MethodGet,
		upstreamPath:   "/api/credentials/monitor-summary",
		allowedQuery:   []string{"provider_id"},
	},
	// 详情模式带 credential_id，上游返回 models[]（per-model probe/可用性/P95）。
	"GET credentials/{cid}": {
		upstreamMethod: http.MethodGet,
		upstreamPath:   "/api/credentials/monitor-summary",
		allowedQuery:   []string{},
		forcedQuery:    map[string]string{"credential_id": "{cid}"},
	},
	"GET credentials/{cid}/history": {
		upstreamMethod: http.MethodGet,
		upstreamPath:   "/api/credentials/model-history",
		allowedQuery:   []string{"raw_model", "limit"},
		forcedQuery:    map[string]string{"credential_id": "{cid}"},
	},
	"GET credentials/{cid}/window": {
		upstreamMethod: http.MethodGet,
		upstreamPath:   "/api/credentials/sliding-window",
		allowedQuery:   []string{"model", "minutes", "limit"},
		forcedQuery:    map[string]string{"credential_id": "{cid}"},
	},
	"GET credentials/{cid}/decisions": {
		upstreamMethod: http.MethodGet,
		upstreamPath:   "/api/credentials/decisions",
		allowedQuery:   []string{"limit"},
		forcedQuery:    map[string]string{"credential_id": "{cid}"},
	},

	// ── 读：模型与路由 ──
	"GET models": {
		upstreamMethod: http.MethodGet,
		upstreamPath:   "/api/routing/model-tree",
		allowedQuery:   []string{"featured_only"},
	},
	"GET routing/overview": {
		upstreamMethod: http.MethodGet,
		upstreamPath:   "/api/routing/overview",
		allowedQuery:   []string{"featured_only"},
	},
	"GET routing/health": {
		upstreamMethod: http.MethodGet,
		upstreamPath:   "/api/routing/health",
		allowedQuery:   []string{},
	},
	"GET routing/resolve": {
		upstreamMethod: http.MethodGet,
		upstreamPath:   "/api/routing/resolve",
		allowedQuery:   []string{"model", "client_profile"},
	},

	// ── 读：汇总 ──
	"GET board": {
		upstreamMethod: http.MethodGet,
		upstreamPath:   "/api/admin/dashboard/board",
		allowedQuery:   []string{"days", "provider_id", "include_operational"},
	},
	"GET usage": {
		upstreamMethod: http.MethodGet,
		upstreamPath:   "/api/usage",
		allowedQuery:   []string{"days", "group_by"},
	},

	// ── 写：凭据状态 ──
	"POST credentials/promote": {
		upstreamMethod: http.MethodPost,
		upstreamPath:   "/api/credentials/promote",
		write:          true,
	},
	"POST credentials/demote": {
		upstreamMethod: http.MethodPost,
		upstreamPath:   "/api/credentials/demote",
		write:          true,
	},
	"POST credentials/model-toggle": {
		upstreamMethod: http.MethodPost,
		upstreamPath:   "/api/credentials/model-toggle",
		write:          true,
	},
	"POST credentials/set-manual-disabled": {
		upstreamMethod: http.MethodPost,
		upstreamPath:   "/api/credentials/set-manual-disabled",
		write:          true,
	},
	"POST credentials/clear-manual-disabled": {
		upstreamMethod: http.MethodPost,
		upstreamPath:   "/api/credentials/clear-manual-disabled",
		write:          true,
	},

	// ── 写：供应商启用/禁用 ──
	// 上游是 PATCH 且语义为"翻转"（UPDATE providers SET enabled = NOT enabled），
	// 不是"设为指定值"。移动端用 POST 表达动作，这里映射到上游 PATCH ——
	// upstreamMethod 与移动端方法解耦正是为了这种情况。
	//
	// 影响面最大的一条：关掉一个 provider 等于让其下所有凭据一起下线。
	"POST providers/{pid}/toggle": {
		upstreamMethod: http.MethodPatch,
		upstreamPath:   "/api/providers/{pid}/toggle",
		write:          true,
	},

	// ── 写：探测 ──
	// 会产生真实 upstream 调用与费用，结果进泳道。
	"POST routing/probe": {
		upstreamMethod: http.MethodPost,
		upstreamPath:   "/api/routing/probe",
		write:          true,
	},
}

// maxGatewayRequestBytes 限制移动端能转发给上游的请求体大小。
// 白名单里的写端点参数都很小（几个 id + reason），1MB 足够宽松。
const maxGatewayRequestBytes = 1 << 20

// ─────────────────────────────────────────────────────────────────────────────
// 路由入口
// ─────────────────────────────────────────────────────────────────────────────

// handleLLMGatewayNodes 处理 /api/llm-gateway/nodes 与 /api/llm-gateway/nodes/…
//
// 形态：
//
//	GET    /api/llm-gateway/nodes                    列出节点
//	POST   /api/llm-gateway/nodes                    新增节点
//	GET    /api/llm-gateway/nodes/{id}               节点详情
//	PUT    /api/llm-gateway/nodes/{id}               更新节点
//	DELETE /api/llm-gateway/nodes/{id}               删除节点
//	POST   /api/llm-gateway/nodes/{id}/probe         验证凭据（登录 + /api/auth/me）
//	GET    /api/llm-gateway/nodes/{id}/overview      汇总（board + routing/health 并发聚合）
//	GET    /api/llm-gateway/nodes/{id}/live/event    实时请求流（SSE 代理）
//	*      /api/llm-gateway/nodes/{id}/{action…}     白名单代理
func (s *Server) handleLLMGatewayNodes(w http.ResponseWriter, r *http.Request) {
	if s.gatewayNodes == nil {
		writeError(w, http.StatusServiceUnavailable, "gateway node registry not configured (requires PostgreSQL + POCKET_EMAIL_MASTER_KEY)")
		return
	}

	workspaceID := s.workspaceIDFromRequest(r)
	rest := strings.TrimPrefix(r.URL.Path, "/api/llm-gateway/nodes")
	rest = strings.Trim(rest, "/")

	// 集合级：/api/llm-gateway/nodes
	if rest == "" {
		switch r.Method {
		case http.MethodGet:
			s.listGatewayNodes(w, r, workspaceID)
		case http.MethodPost:
			s.createGatewayNode(w, r, workspaceID)
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}

	segments := strings.Split(rest, "/")
	nodeID, err := strconv.ParseInt(segments[0], 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid node id")
		return
	}

	// 单节点级：/api/llm-gateway/nodes/{id}
	if len(segments) == 1 {
		switch r.Method {
		case http.MethodGet:
			s.getGatewayNode(w, r, workspaceID, nodeID)
		case http.MethodPut:
			s.updateGatewayNode(w, r, workspaceID, nodeID)
		case http.MethodDelete:
			s.deleteGatewayNode(w, r, workspaceID, nodeID)
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}

	action := strings.Join(segments[1:], "/")

	// 特殊动作：探测节点自身连通性（不是白名单代理，因为它要写回 health 列）。
	if action == "probe" && r.Method == http.MethodPost {
		s.probeGatewayNode(w, r, workspaceID, nodeID)
		return
	}
	// 聚合视图：一次请求拿到移动端首屏需要的全部数据。
	if action == "overview" && r.Method == http.MethodGet {
		s.gatewayNodeOverview(w, r, workspaceID, nodeID)
		return
	}
	// SSE 实时流。路径含 /event 是刻意的：auth_helper.go 只对含 /event 的路径
	// 接受 ?token=，这样浏览器 EventSource 无需自定义 header 即可鉴权。
	if action == "live/event" && r.Method == http.MethodGet {
		s.gatewayNodeLiveStream(w, r, workspaceID, nodeID)
		return
	}

	s.proxyGatewayNode(w, r, workspaceID, nodeID, action)
}

// ─────────────────────────────────────────────────────────────────────────────
// 节点 CRUD
// ─────────────────────────────────────────────────────────────────────────────

func (s *Server) listGatewayNodes(w http.ResponseWriter, r *http.Request, workspaceID string) {
	nodes, err := s.gatewayNodes.List(r.Context(), workspaceID)
	if err != nil {
		log.Printf("[gateway-nodes] list failed: %v", err)
		writeError(w, http.StatusInternalServerError, "list nodes failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"nodes": nodes,
		"total": len(nodes),
		// allowPrivate 让前端能解释"为什么内网地址保存后探测失败"。
		"allowPrivateHosts": gatewayAllowPrivate(),
	})
}

func (s *Server) getGatewayNode(w http.ResponseWriter, r *http.Request, workspaceID string, nodeID int64) {
	node, err := s.gatewayNodes.Get(r.Context(), workspaceID, nodeID)
	if err != nil {
		s.writeGatewayStoreError(w, err, "get node failed")
		return
	}
	writeJSON(w, http.StatusOK, node)
}

// gatewayNodeRequest 是创建/更新的请求体。指针字段区分"未提供"与"置空"。
type gatewayNodeRequest struct {
	Name          *string `json:"name"`
	BaseURL       *string `json:"baseURL"`
	AdminUsername *string `json:"adminUsername"`
	AdminPassword *string `json:"adminPassword"`
	DataAPIKey    *string `json:"dataApiKey"`
	Enabled       *bool   `json:"enabled"`
}

func (req gatewayNodeRequest) toInput() GatewayNodeInput {
	return GatewayNodeInput{
		Name:          req.Name,
		BaseURL:       req.BaseURL,
		AdminUsername: req.AdminUsername,
		AdminPassword: req.AdminPassword,
		DataAPIKey:    req.DataAPIKey,
		Enabled:       req.Enabled,
	}
}

func (s *Server) createGatewayNode(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if !s.requireGatewayAdmin(w, r) {
		return
	}
	var req gatewayNodeRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// 落库前先校验 URL：不然一个内网/元数据地址会被存进表里，之后每次
	// 代理调用都要在出站阶段才失败，错误信息也没有这里清楚。
	if req.BaseURL != nil {
		if err := validateGatewayURL(strings.TrimSuffix(strings.TrimRight(strings.TrimSpace(*req.BaseURL), "/"), "/v1")); err != nil {
			writeError(w, http.StatusBadRequest, "invalid baseURL: "+err.Error())
			return
		}
	}

	node, err := s.gatewayNodes.Create(r.Context(), workspaceID, req.toInput())
	if err != nil {
		s.writeGatewayStoreError(w, err, "create node failed")
		return
	}
	s.auditGateway(r, "llm_gateway.node.create", node.Name, fmt.Sprintf("node_id=%d base_url=%s", node.ID, node.BaseURL), true)
	writeJSON(w, http.StatusCreated, node)
}

func (s *Server) updateGatewayNode(w http.ResponseWriter, r *http.Request, workspaceID string, nodeID int64) {
	if !s.requireGatewayAdmin(w, r) {
		return
	}
	var req gatewayNodeRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.BaseURL != nil {
		if err := validateGatewayURL(strings.TrimSuffix(strings.TrimRight(strings.TrimSpace(*req.BaseURL), "/"), "/v1")); err != nil {
			writeError(w, http.StatusBadRequest, "invalid baseURL: "+err.Error())
			return
		}
	}

	node, err := s.gatewayNodes.Update(r.Context(), workspaceID, nodeID, req.toInput())
	if err != nil {
		s.writeGatewayStoreError(w, err, "update node failed")
		return
	}
	// 凭据可能变了，丢弃缓存 token。
	if s.gatewayClient != nil {
		s.gatewayClient.InvalidateNode(workspaceID, nodeID)
	}
	s.auditGateway(r, "llm_gateway.node.update", node.Name, fmt.Sprintf("node_id=%d", node.ID), true)
	writeJSON(w, http.StatusOK, node)
}

func (s *Server) deleteGatewayNode(w http.ResponseWriter, r *http.Request, workspaceID string, nodeID int64) {
	if !s.requireGatewayAdmin(w, r) {
		return
	}
	if err := s.gatewayNodes.Delete(r.Context(), workspaceID, nodeID); err != nil {
		s.writeGatewayStoreError(w, err, "delete node failed")
		return
	}
	if s.gatewayClient != nil {
		s.gatewayClient.InvalidateNode(workspaceID, nodeID)
	}
	s.auditGateway(r, "llm_gateway.node.delete", strconv.FormatInt(nodeID, 10), fmt.Sprintf("node_id=%d", nodeID), true)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// probeGatewayNode 验证节点凭据并把结果写回 health 列。
func (s *Server) probeGatewayNode(w http.ResponseWriter, r *http.Request, workspaceID string, nodeID int64) {
	secret, err := s.gatewayNodes.LoadWithSecret(r.Context(), workspaceID, nodeID)
	if err != nil {
		s.writeGatewayStoreError(w, err, "load node failed")
		return
	}

	role, probeErr := s.gatewayClient.probe(r.Context(), secret)
	status := gatewayHealthOK
	errMsg := ""
	if probeErr != nil {
		status = gatewayHealthError
		errMsg = probeErr.Error()
	}
	if err := s.gatewayNodes.RecordHealth(r.Context(), workspaceID, nodeID, status, role, errMsg); err != nil {
		log.Printf("[gateway-nodes] record health failed: %v", err)
	}

	resp := map[string]any{
		"ok":     probeErr == nil,
		"status": status,
		"role":   role,
	}
	if probeErr != nil {
		resp["error"] = errMsg
	}
	// super_admin 之外的角色访问 /api/providers 与 /api/routing/probe 会被上游
	// 403。这里提前告诉前端，省一次让人困惑的失败。
	if probeErr == nil && role != "" && role != "super_admin" {
		resp["warning"] = fmt.Sprintf("account role is %q; provider list and model probe require super_admin on the gateway", role)
	}
	writeJSON(w, http.StatusOK, resp)
}

// ─────────────────────────────────────────────────────────────────────────────
// 辅助
// ─────────────────────────────────────────────────────────────────────────────

// requireGatewayAdmin 对写操作要求 pocket 侧 admin 角色。
// 与 server_audit.go 的判定保持一致（claims.Role != "admin" → 403）。
func (s *Server) requireGatewayAdmin(w http.ResponseWriter, r *http.Request) bool {
	claims := s.claimsFromContext(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	if claims.Role != "admin" {
		writeError(w, http.StatusForbidden, "admin role required for gateway write operations")
		return false
	}
	return true
}

func (s *Server) writeGatewayStoreError(w http.ResponseWriter, err error, fallback string) {
	if errors.Is(err, ErrGatewayNodeNotFound) {
		writeError(w, http.StatusNotFound, "gateway node not found")
		return
	}
	// 校验类错误（name required / 唯一键冲突）应回 400/409 而不是 500。
	msg := err.Error()
	switch {
	case strings.Contains(msg, "is required"), strings.Contains(msg, "must not be empty"):
		writeError(w, http.StatusBadRequest, msg)
	case strings.Contains(msg, "duplicate key"), strings.Contains(msg, "idx_llm_gw_nodes_ws_name"):
		writeError(w, http.StatusConflict, "a node with this name already exists in the workspace")
	default:
		log.Printf("[gateway-nodes] %s: %v", fallback, err)
		writeError(w, http.StatusInternalServerError, fallback)
	}
}

// auditGateway 记一条 pocket 侧审计。所有写操作都会调用。
func (s *Server) auditGateway(r *http.Request, action, resource, detail string, success bool) {
	if s.auditStore == nil {
		return
	}
	claims := s.claimsFromContext(r)
	userID, tenantID := "", ""
	if claims != nil {
		userID, tenantID = claims.UserID, claims.WorkspaceID
	}
	if err := s.auditStore.Record(&redclaw.AuditEntry{
		Action:   action,
		UserID:   userID,
		TenantID: tenantID,
		Resource: resource,
		Detail:   detail,
		Success:  success,
		IP:       clientIPFromRequest(r),
	}); err != nil {
		log.Printf("[gateway-nodes] audit record failed: %v", err)
	}
}

// clientIPFromRequest 取客户端 IP，优先反代头。
func clientIPFromRequest(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if idx := strings.IndexByte(xff, ','); idx > 0 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	if rip := r.Header.Get("X-Real-IP"); rip != "" {
		return strings.TrimSpace(rip)
	}
	host := r.RemoteAddr
	if idx := strings.LastIndexByte(host, ':'); idx > 0 {
		return host[:idx]
	}
	return host
}

func decodeJSONBody(r *http.Request, dst any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxGatewayRequestBytes+1))
	if err != nil {
		return fmt.Errorf("read request body failed")
	}
	if len(body) > maxGatewayRequestBytes {
		return fmt.Errorf("request body too large")
	}
	if len(body) == 0 {
		return fmt.Errorf("request body is required")
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return fmt.Errorf("invalid json: %v", err)
	}
	return nil
}

// writeGatewayUpstreamError 把上游错误翻译成移动端响应。
// 上游的 403/404 语义比一律 502 有用得多，所以原样透传这些状态码。
func writeGatewayUpstreamError(w http.ResponseWriter, err error) {
	var apiErr *gatewayAPIError
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusUnauthorized:
			// 上游 401 说明节点凭据失效，对移动端来说不是"你未登录"，
			// 而是"网关拒绝了我们保存的账号"，用 502 + 明确消息表达。
			writeJSON(w, http.StatusBadGateway, map[string]any{
				"error":          "gateway rejected the stored admin credentials",
				"upstreamStatus": apiErr.StatusCode,
				"detail":         apiErr.Message,
			})
		case http.StatusForbidden, http.StatusNotFound, http.StatusConflict,
			http.StatusBadRequest, http.StatusServiceUnavailable, http.StatusTooManyRequests:
			writeJSON(w, apiErr.StatusCode, map[string]any{
				"error":          apiErr.Message,
				"upstreamStatus": apiErr.StatusCode,
			})
		default:
			writeJSON(w, http.StatusBadGateway, map[string]any{
				"error":          apiErr.Message,
				"upstreamStatus": apiErr.StatusCode,
			})
		}
		return
	}
	writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
}
