package agent

// transport_ws.go — ACP over WebSocket transport
//
// ACP WS draft (v2 RFD)：
//   - GET /acp with Upgrade: websocket → JSON-RPC over text frames
//   - Binary frames ignored
//
// 实现：用 gorilla/websocket 客户端；每个 Call 发送一个 text frame，server
// 返回 response frame（同步）；Notify 单 text frame 不等响应；Recv 读
// next frame。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// WSTransport 是 ACP over WebSocket transport。
type WSTransport struct {
	cfg  TransportConfig
	conn *websocket.Conn

	mu      sync.Mutex
	closed  bool
	idAlloc *IDAllocator
	pending *PendingCalls
	writeMu sync.Mutex // WebSocket 写是单 conn，需串行

	recvCh   chan []byte
	cancelFn context.CancelFunc
	wg       sync.WaitGroup
}

// NewWSTransport 构造（不连接）。
func NewWSTransport(cfg TransportConfig) *WSTransport {
	return &WSTransport{
		cfg:     cfg,
		idAlloc: NewIDAllocator(),
		pending: NewPendingCalls(),
		recvCh:  make(chan []byte, 32),
	}
}

// Start 建立 WebSocket 连接。
func (t *WSTransport) Start(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.conn != nil {
		return errors.New("transport already started")
	}

	dialer := &websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}
	if t.cfg.InsecureSkipVerify {
		dialer.TLSClientConfig = insecureSkipVerifyTLSConfig()
	}

	headers := http.Header{}
	if t.cfg.AuthToken != "" {
		headers.Set("Authorization", "Bearer "+t.cfg.AuthToken)
	}
	for k, v := range t.cfg.Headers {
		headers.Set(k, v)
	}

	conn, resp, err := dialer.DialContext(ctx, t.cfg.BaseURL+"/acp", headers)
	if err != nil {
		// 拨号错误可能是连接被拒 / 协议不对；尝试分类
		if resp != nil {
			body := readBody(resp.Body, 200)
			resp.Body.Close()
			return NewUpstreamError(resp.StatusCode, body, err)
		}
		return ClassifyNetworkError(err)
	}
	t.conn = conn

	recvCtx, cancel := context.WithCancel(context.Background())
	t.cancelFn = cancel
	t.wg.Add(1)
	go t.readLoop(recvCtx)
	return nil
}

// Close 关闭。
func (t *WSTransport) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	pending := t.pending
	conn := t.conn
	t.mu.Unlock()

	pending.Cancel()
	if t.cancelFn != nil {
		t.cancelFn()
	}
	if conn != nil {
		// 发 close frame
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			time.Now().Add(time.Second))
		conn.Close()
	}
	t.wg.Wait()
	return nil
}

// Call 发送 WS text frame 并等响应。
func (t *WSTransport) Call(ctx context.Context, method string, params any, out any) error {
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

	t.writeMu.Lock()
	if err := t.conn.WriteMessage(websocket.TextMessage, reqBytes); err != nil {
		t.writeMu.Unlock()
		return ClassifyNetworkError(err)
	}
	t.writeMu.Unlock()

	// 简化实现：等下一帧（Call 串行语义下假设 1:1）
	ctx2, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	select {
	case frame := <-t.recvCh:
		_, _, response, errResp, _ := ParseFrame(frame)
		switch {
		case response != nil:
			if out != nil && len(response.Result) > 0 {
				if err := json.Unmarshal(response.Result, out); err != nil {
					return NewProtocolError(err)
				}
			}
			return nil
		case errResp != nil:
			return NewUpstreamError(errResp.Error.Code, errResp.Error.Message, nil)
		default:
			return NewProtocolError(errors.New("non-response frame on Call"))
		}
	case <-ctx2.Done():
		return NewTimeoutError(ctx2.Err())
	}
}

// Notify 发送 WS notification。
func (t *WSTransport) Notify(ctx context.Context, method string, params any) error {
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

	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	if err := t.conn.WriteMessage(websocket.TextMessage, notifBytes); err != nil {
		return ClassifyNetworkError(err)
	}
	return nil
}

// Recv 接收下一帧。
func (t *WSTransport) Recv(ctx context.Context) ([]byte, error) {
	select {
	case frame := <-t.recvCh:
		return frame, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// readLoop 是 WS reader goroutine。
func (t *WSTransport) readLoop(ctx context.Context) {
	defer t.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		_ = t.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		_, data, err := t.conn.ReadMessage()
		if err != nil {
			t.recvCh <- []byte(`{"error":"read closed"}`)
			return
		}
		select {
		case <-ctx.Done():
			return
		case t.recvCh <- data:
		}
	}
}
