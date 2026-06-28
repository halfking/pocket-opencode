package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// ModelConfig 模型配置
type ModelConfig struct {
	Providers       []Provider `json:"providers"`
	DefaultProvider string     `json:"defaultProvider,omitempty"`
	Timeout         int        `json:"timeout,omitempty"`
}

// Provider 模型提供商
type Provider struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Enabled  bool              `json:"enabled"`
	APIKey   string            `json:"apiKey,omitempty"`
	BaseURL  string            `json:"baseURL,omitempty"`
	Models   []ModelDefinition `json:"models"`
	Priority int               `json:"priority,omitempty"`
}

// ModelDefinition 模型定义
type ModelDefinition struct {
	ID            string        `json:"id"`
	DisplayName   string        `json:"displayName"`
	Enabled       bool          `json:"enabled"`
	MaxTokens     int           `json:"maxTokens,omitempty"`
	Temperature   float64       `json:"temperature,omitempty"`
	ContextWindow int           `json:"contextWindow,omitempty"`
	Pricing       *ModelPricing `json:"pricing,omitempty"`
}

// ModelPricing 模型价格
type ModelPricing struct {
	Input  float64 `json:"input"`
	Output float64 `json:"output"`
}

// OpenCodeConfigAdapter OpenCode 配置适配器接口
type OpenCodeConfigAdapter interface {
	GetModelConfig(ctx context.Context, instanceBaseURL string) (*ModelConfig, error)
	UpdateModelConfig(ctx context.Context, instanceBaseURL string, config *ModelConfig) error
	ReloadConfig(ctx context.Context, instanceBaseURL string) error
	TestModel(ctx context.Context, instanceBaseURL, providerID, modelID string) error
}

// OpenCodeConfigHTTPAdapter HTTP 配置适配器
type OpenCodeConfigHTTPAdapter struct {
	client  *http.Client
	timeout time.Duration
}

// NewOpenCodeConfigHTTPAdapter 创建配置适配器
func NewOpenCodeConfigHTTPAdapter(timeoutMS int) *OpenCodeConfigHTTPAdapter {
	return &OpenCodeConfigHTTPAdapter{
		client:  &http.Client{},
		timeout: time.Duration(timeoutMS) * time.Millisecond,
	}
}

// GetModelConfig 获取模型配置
func (a *OpenCodeConfigHTTPAdapter) GetModelConfig(ctx context.Context, instanceBaseURL string) (*ModelConfig, error) {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	url := fmt.Sprintf("%s/api/config/models", instanceBaseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get model config failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get model config returned %d", resp.StatusCode)
	}

	var result struct {
		Config ModelConfig `json:"config"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode config failed: %w", err)
	}

	return &result.Config, nil
}

// UpdateModelConfig 更新模型配置
func (a *OpenCodeConfigHTTPAdapter) UpdateModelConfig(ctx context.Context, instanceBaseURL string, config *ModelConfig) error {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	body, err := json.Marshal(map[string]interface{}{"config": config})
	if err != nil {
		return fmt.Errorf("marshal config failed: %w", err)
	}

	url := fmt.Sprintf("%s/api/config/models", instanceBaseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("update model config failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("update model config returned %d", resp.StatusCode)
	}

	return nil
}

// ReloadConfig 热加载配置
func (a *OpenCodeConfigHTTPAdapter) ReloadConfig(ctx context.Context, instanceBaseURL string) error {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	url := fmt.Sprintf("%s/api/config/reload", instanceBaseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return fmt.Errorf("create request failed: %w", err)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("reload config failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("reload config returned %d", resp.StatusCode)
	}

	return nil
}

// TestModel 测试模型连接
func (a *OpenCodeConfigHTTPAdapter) TestModel(ctx context.Context, instanceBaseURL, providerID, modelID string) error {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	body, _ := json.Marshal(map[string]string{
		"providerId": providerID,
		"modelId":    modelID,
	})

	url := fmt.Sprintf("%s/api/config/models/test", instanceBaseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("test model failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("test model returned %d", resp.StatusCode)
	}

	return nil
}
