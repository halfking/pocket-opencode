package agent

// transport_http.go — ACP over Streamable HTTP transport
//
// ACP HTTP draft (v2 RFD)：
//   - 单端点 /acp
//   - HTTP/2 required
//   - POST 应用 JSON 调用，返回 SSE 流用于响应
//   - GET 保持长连接接收 server-initiated notifications
//   - 鉴权：Acp-Connection-Id / Acp-Session-Id 头（v2）
//
// 本实现参考 RFD 草案；当前 v1 spec 不包含 HTTP transport，所以是
// "future-proof" 实现 — 真正 production 用需要 agent 端实现该 draft。
//
// 当前 scope：实现框架 + 单测，留给未来真实 HTTP agent 接入。

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// HTTPTransport 是 ACP over Streamable HTTP transport。
type HTTPTransport struct {
	cfg    TransportConfig
	client *http.Client

	mu      sync.Mutex
	closed  bool
	idAlloc *IDAllocator
	pending *PendingCalls

	// SSE 接收 stream
	sseResp  *http.Response
	sseBody  *bufio.Scanner
	cancelFn context.CancelFunc
	wg       sync.WaitGroup

	recvCh chan []byte
}

// NewHTTPTransport 构造（不发请求）。
func NewHTTPTransport(cfg TransportConfig) *HTTPTransport {
	return &HTTPTransport{
		cfg:     cfg,
		idAlloc: NewIDAllocator(),
		pending: NewPendingCalls(),
		recvCh:  make(chan []byte, 32),
	}
}

// Start 打开 SSE 长连接接收 notifications。
func (t *HTTPTransport) Start(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.client != nil {
		return errors.New("transport already started")
	}

	t.client = &http.Client{
		Timeout: 0, // SSE 长连接无总超时；用 ctx 控制
	}

	// 打开 GET /acp 长连接（SSE 推送）
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.cfg.BaseURL+"/acp", nil)
	if err != nil {
		return fmt.Errorf("build sse request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	if t.cfg.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+t.cfg.AuthToken)
	}
	for k, v := range t.cfg.Headers {
		req.Header.Set(k, v)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return ClassifyNetworkError(err)
	}
	if resp.StatusCode != http.StatusOK {
		body := readBody(resp.Body, 200)
		resp.Body.Close()
		return NewUpstreamError(resp.StatusCode, body, nil)
	}
	t.sseResp = resp
	t.sseBody = bufio.NewScanner(resp.Body)
	t.sseBody.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	recvCtx, cancel := context.WithCancel(context.Background())
	t.cancelFn = cancel

	t.wg.Add(1)
	go t.readSSE(recvCtx)

	return nil
}

// Close 关闭。
func (t *HTTPTransport) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	pending := t.pending
	client := t.client
	resp := t.sseResp
	t.mu.Unlock()

	pending.Cancel()
	if t.cancelFn != nil {
		t.cancelFn()
	}
	if resp != nil {
		resp.Body.Close()
	}
	if client != nil {
		client.CloseIdleConnections()
	}
	t.wg.Wait()
	return nil
}

// Call 发送 POST + 等待 SSE 响应。
//
// 实现：用 HTTP POST 发送请求，SSE 流里找到匹配 id 的响应（其他帧继续
// 推给 Recv 调用方）。
func (t *HTTPTransport) Call(ctx context.Context, method string, params any, out any) error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return NewProtocolError(errors.New("transport closed"))
	}
	id := t.idAlloc.Next()
	t.mu.Unlock()

	paramsJSON, err := encodeJSON(params)
	if err != nil {
		return fmt.Errorf("encode params: %w", err)
	}
	reqBytes, err := MarshalRequest(id, method, paramsJSON)
	if err != nil {
		return NewProtocolError(err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, t.cfg.BaseURL+"/acp",
		strings.NewReader(string(reqBytes)))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	if t.cfg.AuthToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+t.cfg.AuthToken)
	}
	for k, v := range t.cfg.Headers {
		httpReq.Header.Set(k, v)
	}

	// 简化实现：单独 POST + 阻塞等待 response body（不开 SSE 长连接用于本次响应）
	// 真实生产应复用同一条 SSE 流匹配 id（性能更好）。
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return ClassifyNetworkError(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body := readBody(resp.Body, 200)
		return NewUpstreamError(resp.StatusCode, body, nil)
	}

	body, err := readAll(resp.Body)
	if err != nil {
		return NewProtocolError(err)
	}

	// 解析响应
	frameType, _, response, errResp, _ := ParseFrame(body)
	switch frameType {
	case "response":
		if out != nil && len(response.Result) > 0 {
			if err := json.Unmarshal(response.Result, out); err != nil {
				return NewProtocolError(err)
			}
		}
		return nil
	case "error":
		return NewUpstreamError(errResp.Error.Code, errResp.Error.Message, nil)
	default:
		return NewProtocolError(fmt.Errorf("unexpected frame type %q", frameType))
	}
}

// Notify 发送 POST notification。
func (t *HTTPTransport) Notify(ctx context.Context, method string, params any) error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return NewProtocolError(errors.New("transport closed"))
	}
	t.mu.Unlock()

	paramsJSON, err := encodeJSON(params)
	if err != nil {
		return fmt.Errorf("encode params: %w", err)
	}
	notifBytes, err := MarshalNotification(method, paramsJSON)
	if err != nil {
		return NewProtocolError(err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.cfg.BaseURL+"/acp",
		strings.NewReader(string(notifBytes)))
	if err != nil {
		return fmt.Errorf("build notification: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if t.cfg.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+t.cfg.AuthToken)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return ClassifyNetworkError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body := readBody(resp.Body, 200)
		return NewUpstreamError(resp.StatusCode, body, nil)
	}
	return nil
}

// Recv 接收下一帧（从 SSE 推送）。
func (t *HTTPTransport) Recv(ctx context.Context) ([]byte, error) {
	select {
	case frame := <-t.recvCh:
		return frame, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// readSSE 是 SSE reader goroutine。
func (t *HTTPTransport) readSSE(ctx context.Context) {
	defer t.wg.Done()
	for t.sseBody.Scan() {
		line := t.sseBody.Text()
		// SSE 格式：data: <json>\n\n
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		frame := []byte(payload)
		select {
		case <-ctx.Done():
			return
		case t.recvCh <- frame:
		}
	}
}
