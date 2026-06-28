package adapter

import (
	"context"
	"encoding/json"
	"fmt"
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

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, instanceBaseURL+"/api/sessions", nil)
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

	var result []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Info   struct {
			Title string `json:"title"`
		} `json:"info"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode sessions failed: %w", err)
	}

	sessions := make([]OpenCodeSession, 0, len(result))
	for _, s := range result {
		sessions = append(sessions, OpenCodeSession{
			ID:     s.ID,
			Title:  s.Info.Title,
			Status: s.Status,
		})
	}

	return sessions, nil
}

func (a *OpenCodeHTTPAdapter) GetSessionSummary(ctx context.Context, instanceBaseURL, sessionID string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	url := fmt.Sprintf("%s/api/sessions/%s/summarize", instanceBaseURL, sessionID)
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
