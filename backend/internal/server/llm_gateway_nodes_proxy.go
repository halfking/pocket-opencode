package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// resolveGatewayRoute 把移动端的 action 解析成白名单表里的一条上游路由。
//
// action 形如 "credentials/12/history"，需要先把数字 segment 归一化成
// 占位符（"credentials/{cid}/history"）才能查表。这样查表 key 是有限集合，
// 不存在把任意路径拼进上游 URL 的可能。
func resolveGatewayRoute(method, action string) (gatewayProxyRoute, map[string]string, bool) {
	segments := strings.Split(strings.Trim(action, "/"), "/")
	params := map[string]string{}

	for i, seg := range segments {
		if seg == "" {
			return gatewayProxyRoute{}, nil, false
		}
		// 只有纯数字 segment 会被当成资源 id。
		if _, err := strconv.ParseInt(seg, 10, 64); err != nil {
			continue
		}
		// 依据前一个 segment 决定占位符名字，避免 credentials/12 与
		// providers/12 撞同一个 key。
		switch {
		case i > 0 && segments[i-1] == "credentials":
			params["cid"] = seg
			segments[i] = "{cid}"
		case i > 0 && segments[i-1] == "providers":
			params["pid"] = seg
			segments[i] = "{pid}"
		default:
			return gatewayProxyRoute{}, nil, false
		}
	}

	key := method + " " + strings.Join(segments, "/")
	route, ok := gatewayProxyRoutes[key]
	if !ok {
		return gatewayProxyRoute{}, nil, false
	}
	return route, params, true
}

// substituteParams 把 {cid}/{pid} 填进模板。
func substituteParams(template string, params map[string]string) string {
	out := template
	for k, v := range params {
		out = strings.ReplaceAll(out, "{"+k+"}", v)
	}
	return out
}

// buildUpstreamQuery 按白名单构造转发给上游的 query。
//
// 只有 allowedQuery 里列出的 key 会被透传，且每个 key 只取第一个值。
// forcedQuery 最后覆盖 —— 例如 credentials/{cid} 强制 credential_id，
// 调用方无法用 query 覆盖成别的 id。
func buildUpstreamQuery(route gatewayProxyRoute, incoming url.Values, params map[string]string) url.Values {
	out := url.Values{}
	for _, key := range route.allowedQuery {
		if v := incoming.Get(key); v != "" {
			out.Set(key, v)
		}
	}
	for key, tmpl := range route.forcedQuery {
		out.Set(key, substituteParams(tmpl, params))
	}
	return out
}

// proxyGatewayNode 执行一次白名单代理调用。
func (s *Server) proxyGatewayNode(w http.ResponseWriter, r *http.Request, workspaceID string, nodeID int64, action string) {
	route, params, ok := resolveGatewayRoute(r.Method, action)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("unsupported gateway action %q", r.Method+" "+action))
		return
	}

	// 写操作要 pocket admin 角色。读操作任何已认证用户都可以。
	if route.write && !s.requireGatewayAdmin(w, r) {
		return
	}

	var body []byte
	if route.upstreamMethod == http.MethodPost || route.upstreamMethod == http.MethodPut {
		raw, err := io.ReadAll(io.LimitReader(r.Body, maxGatewayRequestBytes+1))
		if err != nil {
			writeError(w, http.StatusBadRequest, "read request body failed")
			return
		}
		if len(raw) > maxGatewayRequestBytes {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		// 校验是合法 JSON 再转发，避免把畸形 body 打到上游。
		if len(raw) > 0 {
			var probe any
			if err := json.Unmarshal(raw, &probe); err != nil {
				writeError(w, http.StatusBadRequest, "invalid json body")
				return
			}
		}
		body = raw
	}

	secret, err := s.gatewayNodes.LoadWithSecret(r.Context(), workspaceID, nodeID)
	if err != nil {
		s.writeGatewayStoreError(w, err, "load node failed")
		return
	}
	if !secret.Node.Enabled {
		writeError(w, http.StatusConflict, "gateway node is disabled")
		return
	}

	upstreamPath := substituteParams(route.upstreamPath, params)
	query := buildUpstreamQuery(route, r.URL.Query(), params)

	payload, err := s.gatewayClient.do(r.Context(), secret, route.upstreamMethod, upstreamPath, query, body)
	if route.write {
		detail := fmt.Sprintf("node_id=%d upstream=%s %s", nodeID, route.upstreamMethod, upstreamPath)
		if len(body) > 0 && len(body) <= 512 {
			detail += " body=" + string(body)
		}
		s.auditGateway(r, "llm_gateway."+strings.ReplaceAll(action, "/", "."), secret.Node.Name, detail, err == nil)
	}
	if err != nil {
		writeGatewayUpstreamError(w, err)
		return
	}

	// 上游已经是 JSON，直接透传，不重新编解码（省一次 marshal，也不改变
	// 上游的字段顺序/精度）。
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(payload); err != nil {
		log.Printf("[gateway-nodes] write response failed: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 聚合视图
// ─────────────────────────────────────────────────────────────────────────────

// gatewayNodeOverview 并发拉取移动端首屏需要的几块数据，合成一个响应。
//
// 移动端网络往返成本高，逐个调用会让首屏等上好几秒。这里并发发起，
// 任何一块失败都不影响其余部分 —— 失败的块以 errors[key] 形式返回，
// 前端可以只把那一张卡片标成不可用。
func (s *Server) gatewayNodeOverview(w http.ResponseWriter, r *http.Request, workspaceID string, nodeID int64) {
	secret, err := s.gatewayNodes.LoadWithSecret(r.Context(), workspaceID, nodeID)
	if err != nil {
		s.writeGatewayStoreError(w, err, "load node failed")
		return
	}
	if !secret.Node.Enabled {
		writeError(w, http.StatusConflict, "gateway node is disabled")
		return
	}

	days := r.URL.Query().Get("days")
	if days == "" {
		days = "1"
	}

	type block struct {
		key   string
		path  string
		query url.Values
	}
	blocks := []block{
		{key: "board", path: "/api/admin/dashboard/board", query: url.Values{"days": {days}}},
		{key: "routingHealth", path: "/api/routing/health"},
		{key: "credentials", path: "/api/credentials/monitor-summary"},
	}

	// 整个聚合给一个总预算，避免单块慢拖垮首屏。
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()

	var (
		mu      sync.Mutex
		results = map[string]json.RawMessage{}
		errs    = map[string]string{}
		wg      sync.WaitGroup
	)
	for _, b := range blocks {
		wg.Add(1)
		go func(b block) {
			defer wg.Done()
			payload, err := s.gatewayClient.do(ctx, secret, http.MethodGet, b.path, b.query, nil)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs[b.key] = err.Error()
				return
			}
			results[b.key] = json.RawMessage(payload)
		}(b)
	}
	wg.Wait()

	resp := map[string]any{
		"node":        secret.Node,
		"generatedAt": time.Now().UTC().Format(time.RFC3339),
	}
	for k, v := range results {
		resp[k] = v
	}
	if len(errs) > 0 {
		resp["errors"] = errs
	}
	writeJSON(w, http.StatusOK, resp)
}

// ─────────────────────────────────────────────────────────────────────────────
// SSE 实时请求流
// ─────────────────────────────────────────────────────────────────────────────

// gatewayNodeLiveStream 把上游 /api/admin/live-stream 转发给移动端。
//
// 上游发的是标准 SSE（data: {...}\n\n + : ping 注释行）。这里做字节级
// 透传而不解析信封：泳道快照结构复杂且在演进，解析只会引入不必要的耦合，
// 让前端直接消费上游契约更稳。
func (s *Server) gatewayNodeLiveStream(w http.ResponseWriter, r *http.Request, workspaceID string, nodeID int64) {
	secret, err := s.gatewayNodes.LoadWithSecret(r.Context(), workspaceID, nodeID)
	if err != nil {
		s.writeGatewayStoreError(w, err, "load node failed")
		return
	}
	if !secret.Node.Enabled {
		writeError(w, http.StatusConflict, "gateway node is disabled")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported by this server")
		return
	}

	// 长连接的写 deadline 已由 longLivedPathMiddleware 清掉（参见 server.go）。
	// 那里是单一来源，避免每个 handler 散落同样的代码 —— 很容易漏一个端点
	// 然后用户在 30s 处看到莫名其妙的断流。

	// 客户端断开时 cancel，让上游连接跟着收敛，不留空转的 SSE。
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	resp, err := s.gatewayClient.stream(ctx, secret, "/api/admin/live-stream", nil)
	if err != nil {
		writeGatewayUpstreamError(w, err)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// 关掉 nginx 的响应缓冲，否则 SSE 会被攒到 buffer 满才吐出来。
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// 逐行 pump。SSE 事件以空行分隔，按行转发即可保持事件边界。
	// 单行上限调高（默认 64KB 不够）：initial_data 携带完整泳道快照，
	// 凭据/模型多的环境下单条 data: 行可以到几百 KB。
	reader := bufio.NewReaderSize(resp.Body, 64*1024)
	const maxSSELineBytes = 2 << 20

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			if len(line) > maxSSELineBytes {
				log.Printf("[gateway-nodes] dropping oversized SSE line (%d bytes) from node %d", len(line), nodeID)
			} else if _, werr := w.Write(line); werr != nil {
				// 移动端断开，正常收敛。
				return
			} else {
				// 空行代表一个事件结束，此时才 flush，避免把半个事件推给客户端。
				if len(strings.TrimSpace(string(line))) == 0 {
					flusher.Flush()
				}
			}
		}
		if err != nil {
			if err != io.EOF {
				log.Printf("[gateway-nodes] live-stream read error (node %d): %v", nodeID, err)
			}
			return
		}
	}
}
