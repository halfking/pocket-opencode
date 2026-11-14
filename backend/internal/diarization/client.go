// Package diarization 提供说话人分离服务的客户端封装
//
// 集成方案：
//  1. 本地部署：pyannote-audio + WhisperX（推荐，隐私优先）
//  2. 云端API：第三方说话人分离服务（备选）
//
// 数据流：
//  音频 -> VAD分段 -> ASR转写 -> 说话人标签对齐 -> 带speaker的segments
package diarization

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"
)

// Config 说话人分离服务配置
type Config struct {
	// BaseURL 服务地址，如 http://192.168.31.30:8002
	BaseURL string
	// APIKey 鉴权密钥（如果服务需要）
	APIKey string
	// Timeout 请求超时
	Timeout time.Duration
}

// Client 说话人分离客户端
type Client struct {
	config Config
	http   *http.Client
}

// NewClient 创建说话人分离客户端
func NewClient(cfg Config) *Client {
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Minute // 说话人分离较慢
	}
	return &Client{
		config: cfg,
		http:   &http.Client{Timeout: cfg.Timeout},
	}
}

// Segment 带说话人标签的片段
type Segment struct {
	Speaker string  `json:"speaker"` // SPEAKER_00, SPEAKER_01, ...
	Text    string  `json:"text"`
	Start   float64 `json:"start"` // 秒
	End     float64 `json:"end"`   // 秒
}

// DiarizeRequest 说话人分离请求
type DiarizeRequest struct {
	// Audio 音频字节（WAV/MP3等）
	Audio []byte
	// Filename 文件名（用于Content-Type推断）
	Filename string
	// NumSpeakers 预期说话人数量（可选，-1表示自动检测）
	NumSpeakers int
	// MinSpeakers 最少说话人数（可选）
	MinSpeakers int
	// MaxSpeakers 最多说话人数（可选）
	MaxSpeakers int
}

// DiarizeResult 说话人分离结果
type DiarizeResult struct {
	Segments      []Segment `json:"segments"`
	SpeakerCount  int       `json:"speaker_count"`
	DurationMS    int64     `json:"duration_ms"`
	ProcessTimeMS int64     `json:"process_time_ms"`
}

// Diarize 执行说话人分离
//
// 返回带说话人标签的时间段列表。如果服务不可用，返回错误。
func (c *Client) Diarize(ctx context.Context, req DiarizeRequest) (*DiarizeResult, error) {
	if c.config.BaseURL == "" {
		return nil, fmt.Errorf("diarization service not configured")
	}

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	// 音频文件
	fw, err := w.CreateFormFile("audio", req.Filename)
	if err != nil {
		return nil, fmt.Errorf("create form file: %w", err)
	}
	if _, err := fw.Write(req.Audio); err != nil {
		return nil, fmt.Errorf("write audio: %w", err)
	}

	// 参数
	if req.NumSpeakers > 0 {
		_ = w.WriteField("num_speakers", fmt.Sprintf("%d", req.NumSpeakers))
	}
	if req.MinSpeakers > 0 {
		_ = w.WriteField("min_speakers", fmt.Sprintf("%d", req.MinSpeakers))
	}
	if req.MaxSpeakers > 0 {
		_ = w.WriteField("max_speakers", fmt.Sprintf("%d", req.MaxSpeakers))
	}

	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("close multipart: %w", err)
	}

	url := c.config.BaseURL + "/diarize"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", w.FormDataContentType())
	if c.config.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.config.APIKey)
	}

	t0 := time.Now()
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 10<<20)) // 10MB limit

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("diarization failed (HTTP %d): %s",
			resp.StatusCode, string(body))
	}

	var result DiarizeResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	result.ProcessTimeMS = time.Since(t0).Milliseconds()
	return &result, nil
}

// Health 检查服务健康状态
func (c *Client) Health(ctx context.Context) error {
	if c.config.BaseURL == "" {
		return fmt.Errorf("diarization service not configured")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.config.BaseURL+"/health", nil)
	if err != nil {
		return err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("service unhealthy: HTTP %d", resp.StatusCode)
	}
	return nil
}
