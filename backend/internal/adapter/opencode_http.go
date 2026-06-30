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

	url := fmt.Sprintf("%s/session/%s/summarize", instanceBaseURL, sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("create request failed: %w", err)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("opencode get summary request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("opencode get summary returned %d", resp.StatusCode)
	}

	var result struct {
		Summary string `json:"summary"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode summary failed: %w", err)
	}

	return result.Summary, nil
}

// opencodeSessionInfo 映射 OpenCode /session 响应中我们关心的字段。
// 字段名与 sst/opencode 的 Session.Info schema 对齐（见 ~/workspace/ai/opencode 源码）。
type opencodeSessionInfo struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Time  struct {
		Created int64 `json:"created"` // Unix ms
		Updated int64 `json:"updated"` // Unix ms
	} `json:"time"`
	Summary *struct {
		Additions int `json:"additions"`
		Deletions int `json:"deletions"`
		Files     int `json:"files"`
	} `json:"summary"`
	Agent string `json:"agent"`
}

// parseSessionList 解析 OpenCode /session 响应。
func parseSessionList(body io.Reader) ([]OpenCodeSession, error) {
	var infos []opencodeSessionInfo
	if err := json.NewDecoder(body).Decode(&infos); err != nil {
		return nil, fmt.Errorf("decode sessions failed: %w", err)
	}
	sessions := make([]OpenCodeSession, 0, len(infos))
	for _, s := range infos {
		sessions = append(sessions, OpenCodeSession{
			ID:     s.ID,
			Title:  s.Title,
			Status: "idle", // OpenCode 默认空闲；实时状态需调 /session/status
		})
	}
	return sessions, nil
}

// ListRemoteTasks 从指定 OpenCode 实例的 /session API 获取开发会话列表，
// 将每个 Session 映射为 RemoteTask。一个 OpenCode Session 即一个"开发任务"。
func (a *OpenCodeHTTPAdapter) ListRemoteTasks(ctx context.Context, instanceBaseURL, status string, limit int) ([]RemoteTask, error) {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	if instanceBaseURL == "" {
		return nil, fmt.Errorf("ListRemoteTasks requires a non-empty instanceBaseURL")
	}

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

	var infos []opencodeSessionInfo
	if err := json.NewDecoder(resp.Body).Decode(&infos); err != nil {
		return nil, fmt.Errorf("decode sessions failed: %w", err)
	}

	// 尝试获取实时会话状态（idle/busy/retry）。失败则退化为 idle/active。
	statusMap := make(map[string]string, len(infos))
	statusReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, instanceBaseURL+"/session/status", nil)
	if statusReq != nil {
		if statusResp, err := a.client.Do(statusReq); err == nil {
			if statusResp.StatusCode == http.StatusOK {
				_ = json.NewDecoder(statusResp.Body).Decode(&statusMap)
			}
			statusResp.Body.Close()
		}
	}

	tasks := make([]RemoteTask, 0, len(infos))
	for _, s := range infos {
		rt := RemoteTask{
			ID:     s.ID,
			Title:  s.Title,
			Owner:  s.Agent,
			Status: mapSessionStatus(statusMap[s.ID]),
		}
		tasks = append(tasks, rt)
		if limit > 0 && len(tasks) >= limit {
			break
		}
	}
	return tasks, nil
}

// mapSessionStatus 将 OpenCode 的 idle/busy/retry 状态映射为 Pocket 前端状态。
func mapSessionStatus(sessionStatus string) string {
	switch sessionStatus {
	case "busy", "retry":
		return "in_progress"
	default: // idle 或未知
		return "active"
	}
}
