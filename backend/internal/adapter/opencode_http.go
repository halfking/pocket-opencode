package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

	// 修正：实际 API 路径是 /api/session
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, instanceBaseURL+"/api/session", nil)
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

	// 修正：实际 API 没有 /summarize 端点，改用 /api/session/:sessionID 获取 title
	url := fmt.Sprintf("%s/api/session/%s", instanceBaseURL, sessionID)
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

// opencodeSessionInfo 映射 OpenCode /api/session 响应中的 SessionInfo。
// 基于实际源码：~/workspace/ai/opencode/packages/core/src/session/schema.ts
type opencodeSessionInfo struct {
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

// parseSessionList 解析 OpenCode /api/session 响应。
// 实际响应格式：{ "data": [SessionInfo], "cursor": {...} }
func parseSessionList(body io.Reader) ([]OpenCodeSession, error) {
	var response struct {
		Data []opencodeSessionInfo `json:"data"`
	}
	if err := json.NewDecoder(body).Decode(&response); err != nil {
		return nil, fmt.Errorf("decode sessions failed: %w", err)
	}
	sessions := make([]OpenCodeSession, 0, len(response.Data))
	for _, s := range response.Data {
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

// ListRemoteTasks 从指定 OpenCode 实例的 /api/session API 获取开发会话列表，
// 将每个 Session 映射为 RemoteTask。一个 OpenCode Session 即一个"开发任务"。
func (a *OpenCodeHTTPAdapter) ListRemoteTasks(ctx context.Context, instanceBaseURL, status string, limit int) ([]RemoteTask, error) {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	if instanceBaseURL == "" {
		return nil, fmt.Errorf("ListRemoteTasks requires a non-empty instanceBaseURL")
	}

	// 修正：使用正确的 API 路径和查询参数
	url := instanceBaseURL + "/api/session"
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
		Data []opencodeSessionInfo `json:"data"`
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
func (a *OpenCodeHTTPAdapter) GetSessionDetail(ctx context.Context, instanceBaseURL, sessionID string) (*opencodeSessionInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	url := fmt.Sprintf("%s/api/session/%s", instanceBaseURL, sessionID)
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
		Data opencodeSessionInfo `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("decode session failed: %w", err)
	}

	return &response.Data, nil
}

// opencodeMessage 映射 OpenCode Message 结构
type opencodeMessage struct {
	ID   string                 `json:"id"`
	Type string                 `json:"type"`
	Data map[string]interface{} `json:"data,omitempty"`
}

// GetSessionMessages 获取会话消息
func (a *OpenCodeHTTPAdapter) GetSessionMessages(ctx context.Context, instanceBaseURL, sessionID string, limit int) ([]opencodeMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	url := fmt.Sprintf("%s/api/session/%s/message", instanceBaseURL, sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}

	if limit > 0 {
		q := req.URL.Query()
		q.Add("limit", fmt.Sprintf("%d", limit))
		req.URL.RawQuery = q.Encode()
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("opencode get messages request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("opencode get messages returned %d", resp.StatusCode)
	}

	var response struct {
		Data []opencodeMessage `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("decode messages failed: %w", err)
	}

	return response.Data, nil
}

// HealthCheck 检查 OpenCode 实例健康状态
func (a *OpenCodeHTTPAdapter) HealthCheck(ctx context.Context, instanceBaseURL string) error {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, instanceBaseURL+"/api/health", nil)
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
		Healthy bool `json:"healthy"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode health check response failed: %w", err)
	}

	if !result.Healthy {
		return fmt.Errorf("instance reports unhealthy")
	}

	return nil
}
