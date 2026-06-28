package adapter

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type NPSWebAPIAdapter struct {
	baseURL string
	authKey string
	client  *http.Client
}

func NewNPSWebAPIAdapter(baseURL, authKey string) *NPSWebAPIAdapter {
	return &NPSWebAPIAdapter{
		baseURL: baseURL,
		authKey: authKey,
		client:  &http.Client{Timeout: 10 * time.Second},
	}
}

func (a *NPSWebAPIAdapter) ListClients(ctx context.Context) ([]NPSClient, error) {
	timestamp := time.Now().Unix()
	authKeyHash := a.signRequest(timestamp)

	data := url.Values{}
	data.Set("auth_key", authKeyHash)
	data.Set("timestamp", strconv.FormatInt(timestamp, 10))
	data.Set("offset", "0")
	data.Set("limit", "100")

	resp, err := a.client.PostForm(a.baseURL+"/client/list", data)
	if err != nil {
		return nil, fmt.Errorf("nps client list request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("nps client list returned %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Status int `json:"status"`
		Rows   []struct {
			ID     int    `json:"id"`
			Remark string `json:"remark"`
			Vkey   string `json:"vkey"`
		} `json:"rows"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("nps client list decode failed: %w", err)
	}

	if result.Status != 1 {
		return nil, fmt.Errorf("nps client list status=%d", result.Status)
	}

	clients := make([]NPSClient, 0, len(result.Rows))
	for _, row := range result.Rows {
		name := row.Remark
		if name == "" {
			name = row.Vkey
		}
		clients = append(clients, NPSClient{
			ID:   row.ID,
			Name: name,
		})
	}

	return clients, nil
}

func (a *NPSWebAPIAdapter) ListTunnels(ctx context.Context) ([]NPSTunnel, error) {
	// TODO: implement if needed
	return []NPSTunnel{}, nil
}

func (a *NPSWebAPIAdapter) signRequest(timestamp int64) string {
	raw := a.authKey + strconv.FormatInt(timestamp, 10)
	hash := md5.Sum([]byte(raw))
	return fmt.Sprintf("%x", hash)
}
