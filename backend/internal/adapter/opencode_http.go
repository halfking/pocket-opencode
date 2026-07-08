package adapter

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type OpenCodeHTTPAdapter struct {
	client  *http.Client
	timeout time.Duration
}

func NewOpenCodeHTTPAdapter(timeoutMS int) *OpenCodeHTTPAdapter {
	return &OpenCodeHTTPAdapter{
		client:  &http.Client{},
		timeout: time.Duration(timeoutMS) * time.Millisecond,
	}
}

func (a *OpenCodeHTTPAdapter) ListSessions(ctx context.Context, instanceBaseURL string) ([]OpenCodeSession, error) {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	// 修正：实际 API 路径是 /session
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, instanceBaseURL+"/session", nil)
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("opencode list sessions request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("opencode list sessions returned %d", resp.StatusCode)
	}

	sessions, err := parseSessionList(resp.Body)
	if err != nil {
		return nil, err
	}
	return sessions, nil
}

func (a *OpenCodeHTTPAdapter) GetSessionSummary(ctx context.Context, instanceBaseURL, sessionID string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	// 修正：实际 API 没有 /summarize 端点，改用 /session/:sessionID 获取 title
	url := fmt.Sprintf("%s/session/%s", instanceBaseURL, sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("create request failed: %w", err)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("opencode get session request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("opencode get session returned %d", resp.StatusCode)
	}

	// 修正：响应格式是 { "data": SessionInfo }
	var result struct {
		Data struct {
			Title string `json:"title"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode session failed: %w", err)
	}

	return result.Data.Title, nil
}

// OpenCodeSessionInfo 映射 OpenCode /session 响应中的 SessionInfo。
// 基于实际源码：~/workspace/ai/opencode/packages/core/src/session/schema.ts
// 别名 OpenCodeSessionInfo 提供给外部 package 使用。
type OpenCodeSessionInfo struct {
	ID        string  `json:"id"`
	ParentID  *string `json:"parentID,omitempty"`
	ProjectID string  `json:"projectID"`
	Agent     *string `json:"agent,omitempty"`
	Model     *struct {
		ID         string  `json:"id"`
		ProviderID string  `json:"providerID"`
		Variant    *string `json:"variant,omitempty"`
	} `json:"model,omitempty"`
	Cost   float64 `json:"cost"`
	Tokens struct {
		Input     float64 `json:"input"`
		Output    float64 `json:"output"`
		Reasoning float64 `json:"reasoning"`
		Cache     struct {
			Read  float64 `json:"read"`
			Write float64 `json:"write"`
		} `json:"cache"`
	} `json:"tokens"`
	Time struct {
		Created  int64  `json:"created"`  // Unix ms
		Updated  int64  `json:"updated"`  // Unix ms
		Archived *int64 `json:"archived,omitempty"`
	} `json:"time"`
	Title    string `json:"title"`
	Location struct {
		Directory   string  `json:"directory"`
		WorkspaceID *string `json:"workspaceID,omitempty"`
	} `json:"location"`
	Subpath *string `json:"subpath,omitempty"`
}

// parseSessionList 解析 OpenCode /session 响应。
// 实际响应格式：直接是数组 [SessionInfo, ...]
func parseSessionList(body io.Reader) ([]OpenCodeSession, error) {
	// 先尝试读取响应体
	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("read response body failed: %w", err)
	}

	// 尝试解析为数组（OpenCode 实际格式）
	var sessionsInfo []OpenCodeSessionInfo
	if err := json.Unmarshal(bodyBytes, &sessionsInfo); err != nil {
		// 如果失败，尝试解析为对象格式 {"data": [...]}
		var response struct {
			Data []OpenCodeSessionInfo `json:"data"`
		}
		if err2 := json.Unmarshal(bodyBytes, &response); err2 != nil {
			return nil, fmt.Errorf("decode sessions failed: %w (raw: %s)", err, string(bodyBytes[:min(200, len(bodyBytes))]))
		}
		sessionsInfo = response.Data
	}

	sessions := make([]OpenCodeSession, 0, len(sessionsInfo))
	for _, s := range sessionsInfo {
		// 判断状态：根据最后更新时间
		status := "idle"
		if time.Since(time.UnixMilli(s.Time.Updated)) < 5*time.Minute {
			status = "active"
		}

		sessions = append(sessions, OpenCodeSession{
			ID:     s.ID,
			Title:  s.Title,
			Status: status,
		})
	}
	return sessions, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ListRemoteTasks 从指定 OpenCode 实例的 /session API 获取开发会话列表，
// 将每个 Session 映射为 RemoteTask。一个 OpenCode Session 即一个"开发任务"。
func (a *OpenCodeHTTPAdapter) ListRemoteTasks(ctx context.Context, instanceBaseURL, status string, limit int) ([]RemoteTask, error) {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	if instanceBaseURL == "" {
		return nil, fmt.Errorf("ListRemoteTasks requires a non-empty instanceBaseURL")
	}

	// 修正：使用正确的 API 路径和查询参数
	url := instanceBaseURL + "/session"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}

	// 添加查询参数
	q := req.URL.Query()
	if limit > 0 {
		q.Add("limit", fmt.Sprintf("%d", limit))
	}
	q.Add("order", "desc") // 最新的优先
	req.URL.RawQuery = q.Encode()

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("opencode list sessions request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("opencode list sessions returned %d", resp.StatusCode)
	}

	// 修正：响应格式是 { "data": [SessionInfo], "cursor": {...} }
	var response struct {
		Data []OpenCodeSessionInfo `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("decode sessions failed: %w", err)
	}

	tasks := make([]RemoteTask, 0, len(response.Data))
	for _, s := range response.Data {
		// 判断会话状态：根据最后更新时间
		sessionStatus := "active"
		if time.Since(time.UnixMilli(s.Time.Updated)) > 10*time.Minute {
			sessionStatus = "idle"
		}

		owner := "system"
		if s.Agent != nil {
			owner = *s.Agent
		}

		rt := RemoteTask{
			ID:     s.ID,
			Title:  s.Title,
			Owner:  owner,
			Status: sessionStatus,
		}
		tasks = append(tasks, rt)
	}
	return tasks, nil
}

// GetSessionDetail 获取会话详细信息
func (a *OpenCodeHTTPAdapter) GetSessionDetail(ctx context.Context, instanceBaseURL, sessionID string) (*OpenCodeSessionInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	url := fmt.Sprintf("%s/session/%s", instanceBaseURL, sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("opencode get session request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("opencode get session returned %d", resp.StatusCode)
	}

	var response struct {
		Data OpenCodeSessionInfo `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("decode session failed: %w", err)
	}

	return &response.Data, nil
}

// CreateSession 创建新会话
// API: POST /session
// Payload: { id?, agent?, model?, location? }
func (a *OpenCodeHTTPAdapter) CreateSession(ctx context.Context, instanceBaseURL string, payload *CreateSessionRequest) (*OpenCodeSessionInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	url := instanceBaseURL + "/session"
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload failed: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("opencode create session request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("opencode create session returned %d", resp.StatusCode)
	}

	var response struct {
		Data OpenCodeSessionInfo `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("decode session failed: %w", err)
	}

	return &response.Data, nil
}

// SendPrompt 发送 Prompt 到指定会话
// OpenCode 真实 API: POST /session/:sessionID/message（无 /prompt 后缀）
// Payload: { id?, prompt, delivery?, resume? }
func (a *OpenCodeHTTPAdapter) SendPrompt(ctx context.Context, instanceBaseURL, sessionID string, payload *SendPromptRequest) (*SendPromptResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	url := fmt.Sprintf("%s/session/%s/message", instanceBaseURL, sessionID)
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload failed: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}
	// OpenCode 会按 Content-Type 解析 JSON body
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("opencode send prompt request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		r, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("opencode send prompt returned %d: %s", resp.StatusCode, string(r))
	}

	// 兼容两种响应：
	// 1) { data: { messageID, enqueued, position } }
	// 2) 直接返回 SessionInput/Message 结构（未来版本）
	var wrapper struct {
		Data SendPromptResponse `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&wrapper); err == nil {
		return &wrapper.Data, nil
	}

	// 回退：某些版本可能只返回 200 + 空体 / SSE 起始，不影响"已发送"语义
	return &SendPromptResponse{Enqueued: true}, nil
}

// opencodeMessage 映射 OpenCode Message 结构
type opencodeMessage struct {
	ID   string                 `json:"id"`
	Type string                 `json:"type"`
	Data map[string]interface{} `json:"data,omitempty"`
}

// OpenCodeMessage is the exported version of opencodeMessage for external use
type OpenCodeMessage = opencodeMessage

// SessionMessagesResponse 消息列表响应
type SessionMessagesResponse struct {
	Data   []OpenCodeMessage `json:"data"`
	Cursor *MessageCursor    `json:"cursor,omitempty"`
}

// MessageCursor 消息分页游标
type MessageCursor struct {
	Previous *string `json:"previous,omitempty"`
	Next     *string `json:"next,omitempty"`
}

// GetSessionMessages 获取会话消息（完整版，支持游标分页）
// API: GET /session/:sessionID/message?limit=&order=&cursor=
func (a *OpenCodeHTTPAdapter) GetSessionMessages(ctx context.Context, instanceBaseURL, sessionID string, limit int, order string, cursor string) (*SessionMessagesResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	url := fmt.Sprintf("%s/session/%s/message", instanceBaseURL, sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}

	q := req.URL.Query()
	if limit > 0 {
		q.Add("limit", fmt.Sprintf("%d", limit))
	}
	if order != "" {
		q.Add("order", order)
	}
	if cursor != "" {
		q.Add("cursor", cursor)
	}
	req.URL.RawQuery = q.Encode()

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("opencode get messages request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("opencode get messages returned %d", resp.StatusCode)
	}

	// OpenCode 真实响应是裸数组 [...]，但旧版/某些端点可能返回 {data:[...]} 包装。
	// 双格式兼容：先试裸数组，再试包装对象（与 parseSessionList 策略一致）。
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read messages body failed: %w", err)
	}

	response := &SessionMessagesResponse{}
	trimmed := strings.TrimSpace(string(body))
	if strings.HasPrefix(trimmed, "[") {
		// 裸数组格式
		if err := json.Unmarshal(body, &response.Data); err != nil {
			return nil, fmt.Errorf("decode messages (array) failed: %w", err)
		}
	} else {
		// 包装对象格式 {data:[...], cursor:{...}}
		var wrapper SessionMessagesResponse
		if err := json.Unmarshal(body, &wrapper); err != nil {
			return nil, fmt.Errorf("decode messages (wrapped) failed: %w", err)
		}
		response.Data = wrapper.Data
		response.Cursor = wrapper.Cursor
	}

	return response, nil
}

// GetSessionContext 获取会话上下文（最后压缩点之后的所有消息）
// API: GET /session/:sessionID/context
func (a *OpenCodeHTTPAdapter) GetSessionContext(ctx context.Context, instanceBaseURL, sessionID string) ([]OpenCodeMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	url := fmt.Sprintf("%s/session/%s/context", instanceBaseURL, sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("opencode get context request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("opencode get context returned %d", resp.StatusCode)
	}

	var response struct {
		Data []OpenCodeMessage `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("decode context failed: %w", err)
	}

	return response.Data, nil
}

// InterruptSession 中断 session 当前的 agent 循环
// API: POST /session/:sessionID/interrupt (V2)
func (a *OpenCodeHTTPAdapter) InterruptSession(ctx context.Context, instanceBaseURL, sessionID string) error {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	url := fmt.Sprintf("%s/session/%s/interrupt", instanceBaseURL, sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return fmt.Errorf("create interrupt request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("opencode interrupt request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("opencode interrupt returned %d", resp.StatusCode)
	}
	return nil
}

// DeleteSession 删除指定的 session
// API: DELETE /session/:sessionID (Phase 2.1 新增)
func (a *OpenCodeHTTPAdapter) DeleteSession(ctx context.Context, instanceBaseURL, sessionID string) error {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	url := fmt.Sprintf("%s/session/%s", instanceBaseURL, sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("create delete request failed: %w", err)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("opencode delete session request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("opencode delete session returned %d", resp.StatusCode)
	}
	return nil
}

// GetMessages 拉取 session 历史消息（用于 SSE 断线后回填）
func (a *OpenCodeHTTPAdapter) GetMessages(ctx context.Context, instanceBaseURL, sessionID string, limit int, order string) ([]OpenCodeMessage, error) {
	resp, err := a.GetSessionMessages(ctx, instanceBaseURL, sessionID, limit, order, "")
	if err != nil {
		return nil, err
	}
	return resp.Data, nil
}

// CompactSession 压缩会话
// API: POST /session/:sessionID/compact
func (a *OpenCodeHTTPAdapter) CompactSession(ctx context.Context, instanceBaseURL, sessionID string) error {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	url := fmt.Sprintf("%s/session/%s/compact", instanceBaseURL, sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return fmt.Errorf("create request failed: %w", err)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("opencode compact session request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("opencode compact session returned %d", resp.StatusCode)
	}

	return nil
}

// WaitForSessionIdle 等待会话代理循环变为空闲
// API: POST /session/:sessionID/wait
func (a *OpenCodeHTTPAdapter) WaitForSessionIdle(ctx context.Context, instanceBaseURL, sessionID string) error {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	url := fmt.Sprintf("%s/session/%s/wait", instanceBaseURL, sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return fmt.Errorf("create request failed: %w", err)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("opencode wait session request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("opencode wait session returned %d", resp.StatusCode)
	}

	return nil
}

// HealthCheck 检查 OpenCode 实例健康状态。
// OpenCode 真实端点是 GET /global/health（无 /api 前缀），返回 {"healthy":true,"version":"..."}。
// 历史代码误用 /api/health，导致扫描找不到真实实例——此处修正。
func (a *OpenCodeHTTPAdapter) HealthCheck(ctx context.Context, instanceBaseURL string) error {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, instanceBaseURL+"/global/health", nil)
	if err != nil {
		return fmt.Errorf("create health check request failed: %w", err)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("health check request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check returned %d", resp.StatusCode)
	}

	var result struct {
		Healthy bool   `json:"healthy"`
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode health check response failed: %w", err)
	}

	if !result.Healthy {
		return fmt.Errorf("instance reports unhealthy")
	}

	return nil
}

// =============================================================================
// 权限审批 API（Permission V2）
// =============================================================================

// PermissionRequest 权限请求对象
// 对应 OpenCode: ~/workspace/ai/opencode/packages/core/src/permission.ts
//   export const Request = Schema.Struct({ id, sessionID, action, resources, save?, metadata?, source? })
type PermissionRequest struct {
	ID        string            `json:"id"`        // per_xxx
	SessionID string            `json:"sessionID"` // ses_xxx
	Action    string            `json:"action"`    // "bash" | "edit" | "webfetch" | ...
	Resources []string          `json:"resources"` // ["rm -rf /tmp/x", "/etc/passwd"]
	Save      []string          `json:"save,omitempty"`
	Metadata  map[string]any    `json:"metadata,omitempty"`
	Source    *PermissionSource `json:"source,omitempty"`
}

// PermissionSource 权限来源
type PermissionSource struct {
	Type      string `json:"type"` // "tool"
	MessageID string `json:"messageID"`
	CallID    string `json:"callID"`
}

// PermissionReply 权限回复值
//   export const Reply = Schema.Literals(["once", "always", "reject"])
type PermissionReply string

const (
	PermissionReplyOnce   PermissionReply = "once"
	PermissionReplyAlways PermissionReply = "always"
	PermissionReplyReject PermissionReply = "reject"
)

// PermissionReplyRequest 权限回复请求体
type PermissionReplyRequest struct {
	Reply   PermissionReply `json:"reply"`
	Message string          `json:"message,omitempty"`
}

// GetPermissionRequests 获取会话的待审批权限请求列表
// API: GET /session/:sessionID/permission
// 响应: { data: PermissionRequest[] }
func (a *OpenCodeHTTPAdapter) GetPermissionRequests(ctx context.Context, instanceBaseURL, sessionID string) ([]PermissionRequest, error) {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	url := fmt.Sprintf("%s/session/%s/permission", instanceBaseURL, sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create permission list request failed: %w", err)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("opencode get permission requests failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("opencode get permission requests returned %d: %s", resp.StatusCode, string(body))
	}

	var response struct {
		Data []PermissionRequest `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("decode permission requests failed: %w", err)
	}

	return response.Data, nil
}

// GetAllPendingPermissionRequests 获取所有位置下的待审批权限请求
// API: GET /permission/request?directory=&workspaceID=（OpenCode 路径无 /api 前缀）
// 响应: Location.response(PermissionRequest[])
func (a *OpenCodeHTTPAdapter) GetAllPendingPermissionRequests(ctx context.Context, instanceBaseURL, directory, workspaceID string) ([]PermissionRequest, error) {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	url := instanceBaseURL + "/permission/request"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create permission list request failed: %w", err)
	}

	q := req.URL.Query()
	if directory != "" {
		q.Add("directory", directory)
	}
	if workspaceID != "" {
		q.Add("workspaceID", workspaceID)
	}
	req.URL.RawQuery = q.Encode()

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("opencode get all pending permission requests failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("opencode get all pending permission requests returned %d: %s", resp.StatusCode, string(body))
	}

	var response struct {
		Data []PermissionRequest `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("decode permission requests failed: %w", err)
	}

	return response.Data, nil
}

// ReplyPermission 回复权限请求
// API: POST /session/:sessionID/permission/:requestID/reply
// Payload: { reply: "once"|"always"|"reject", message?: string }
// 响应: 204 No Content
func (a *OpenCodeHTTPAdapter) ReplyPermission(ctx context.Context, instanceBaseURL, sessionID, requestID string, reply PermissionReply, message string) error {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	url := fmt.Sprintf("%s/session/%s/permission/%s/reply", instanceBaseURL, sessionID, requestID)
	payload := PermissionReplyRequest{Reply: reply, Message: message}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal reply payload failed: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create reply request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("opencode reply permission request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("opencode reply permission returned %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// =============================================================================
// 问答交互 API（Question V2）
// =============================================================================

// QuestionOption 问题选项
type QuestionOption struct {
	Label       string `json:"label"`       // 显示文本（1-5个词）
	Description string `json:"description"` // 选项说明
}

// QuestionInfo 单个问题
type QuestionInfo struct {
	Question  string          `json:"question"`  // 完整问题
	Header    string          `json:"header"`    // 短标签（≤30 字符）
	Options   []QuestionOption `json:"options"`  // 可选项
	Multiple  *bool           `json:"multiple,omitempty"`  // 是否允许多选
	Custom    *bool           `json:"custom,omitempty"`    // 是否允许自定义回答
}

// QuestionRequest 问题请求对象
// 对应 OpenCode: ~/workspace/ai/opencode/packages/core/src/question.ts
//   export const Request = Schema.Struct({ id, sessionID, questions, tool? })
type QuestionRequest struct {
	ID        string         `json:"id"`        // que_xxx
	SessionID string         `json:"sessionID"` // ses_xxx
	Questions []QuestionInfo `json:"questions"`
	Tool      *QuestionTool  `json:"tool,omitempty"`
}

// QuestionTool 工具上下文
type QuestionTool struct {
	MessageID string `json:"messageID"`
	CallID    string `json:"callID"`
}

// QuestionAnswer 单个问题的回答（选项标签数组）
type QuestionAnswer []string

// QuestionReplyRequest 问答回复请求体
// 对应: Reply = { answers: Answer[] }，每个 answer 是该问题所选标签数组
type QuestionReplyRequest struct {
	Answers []QuestionAnswer `json:"answers"`
}

// GetQuestionRequests 获取会话的待回答问题列表
// API: GET /session/:sessionID/question
// 响应: { data: QuestionRequest[] }
func (a *OpenCodeHTTPAdapter) GetQuestionRequests(ctx context.Context, instanceBaseURL, sessionID string) ([]QuestionRequest, error) {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	url := fmt.Sprintf("%s/session/%s/question", instanceBaseURL, sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create question list request failed: %w", err)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("opencode get question requests failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("opencode get question requests returned %d: %s", resp.StatusCode, string(body))
	}

	var response struct {
		Data []QuestionRequest `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("decode question requests failed: %w", err)
	}

	return response.Data, nil
}

// GetAllPendingQuestionRequests 获取所有位置下的待回答问题
// API: GET /api/question/request?directory=&workspaceID=
func (a *OpenCodeHTTPAdapter) GetAllPendingQuestionRequests(ctx context.Context, instanceBaseURL, directory, workspaceID string) ([]QuestionRequest, error) {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	url := instanceBaseURL + "/question/request"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create question list request failed: %w", err)
	}

	q := req.URL.Query()
	if directory != "" {
		q.Add("directory", directory)
	}
	if workspaceID != "" {
		q.Add("workspaceID", workspaceID)
	}
	req.URL.RawQuery = q.Encode()

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("opencode get all pending question requests failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("opencode get all pending question requests returned %d: %s", resp.StatusCode, string(body))
	}

	var response struct {
		Data []QuestionRequest `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("decode question requests failed: %w", err)
	}

	return response.Data, nil
}

// ReplyQuestion 回答问题
// API: POST /session/:sessionID/question/:requestID/reply
// Payload: { answers: [[label1, label2], [label3], ...] } —— 每个问题对应一个答案数组
// 响应: 204 No Content
func (a *OpenCodeHTTPAdapter) ReplyQuestion(ctx context.Context, instanceBaseURL, sessionID, requestID string, answers []QuestionAnswer) error {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	url := fmt.Sprintf("%s/session/%s/question/%s/reply", instanceBaseURL, sessionID, requestID)
	payload := QuestionReplyRequest{Answers: answers}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal reply payload failed: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create reply request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("opencode reply question request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("opencode reply question returned %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// RejectQuestion 拒绝回答问题
// API: POST /session/:sessionID/question/:requestID/reject
// 响应: 204 No Content
func (a *OpenCodeHTTPAdapter) RejectQuestion(ctx context.Context, instanceBaseURL, sessionID, requestID string) error {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	url := fmt.Sprintf("%s/session/%s/question/%s/reject", instanceBaseURL, sessionID, requestID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return fmt.Errorf("create reject request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("opencode reject question request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("opencode reject question returned %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// =============================================================================
// 事件流 API（SSE）
// =============================================================================

// OpenCodeEvent OpenCode 事件结构
// 对应 ~/workspace/ai/opencode/packages/server/src/groups/event.ts
type OpenCodeEvent struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Location map[string]any `json:"location,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
	Version  *int           `json:"version,omitempty"`
	Data     any            `json:"data"`
}

// SubscribeEvents 订阅 OpenCode 的事件流（SSE），返回只读 channel。
// API: GET /api/event
// Content-Type: text/event-stream
// 协议：每个事件以 "data: <json>\n\n" 分隔的 SSE 格式传输
//
// 调用方负责在不再需要时调用返回的 cancel 函数来关闭连接。
func (a *OpenCodeHTTPAdapter) SubscribeEvents(ctx context.Context, instanceBaseURL, directory, workspaceID string) (<-chan OpenCodeEvent, func(), error) {
	// SSE 连接需要长超时，使用独立的 client
	sseClient := &http.Client{
		Timeout: 0, // 无限超时（由 ctx 控制）
	}

	url := instanceBaseURL + "/event"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("create event subscribe request failed: %w", err)
	}

	q := req.URL.Query()
	if directory != "" {
		q.Add("directory", directory)
	}
	if workspaceID != "" {
		q.Add("workspaceID", workspaceID)
	}
	req.URL.RawQuery = q.Encode()

	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")

	resp, err := sseClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("opencode subscribe events request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, nil, fmt.Errorf("opencode subscribe events returned %d", resp.StatusCode)
	}

	// 验证 Content-Type
	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(contentType, "text/event-stream") {
		resp.Body.Close()
		return nil, nil, fmt.Errorf("expected text/event-stream, got %s", contentType)
	}

	events := make(chan OpenCodeEvent, 64)
	cancel := func() {
		resp.Body.Close()
		// 关闭 channel 在 consumer 端完成（通过 ctx 取消触发 reader 退出）
	}

	// 启动后台 goroutine 读取 SSE 流
	go func() {
		defer close(events)
		reader := bufio.NewReader(resp.Body)
		var dataBuffer strings.Builder

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			line, err := reader.ReadString('\n')
			if err != nil {
				if err != io.EOF {
					// 静默退出（连接断开通常是 ctx 取消导致）
					return
				}
				return
			}

			line = strings.TrimRight(line, "\r\n")

			// 空行表示一个事件的结束
			if line == "" {
				if dataBuffer.Len() == 0 {
					continue
				}
				// 解析事件数据
				rawData := dataBuffer.String()
				dataBuffer.Reset()

				var event OpenCodeEvent
				if err := json.Unmarshal([]byte(rawData), &event); err != nil {
					// 跳过无法解析的事件
					continue
				}

				select {
				case events <- event:
				case <-ctx.Done():
					return
				}
				continue
			}

			// 处理 SSE 前缀
			if strings.HasPrefix(line, "data:") {
				payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				if dataBuffer.Len() > 0 {
					dataBuffer.WriteString("\n")
				}
				dataBuffer.WriteString(payload)
			}
			// 忽略其他 SSE 字段（event:, id:, retry:）
		}
	}()

	return events, cancel, nil
}