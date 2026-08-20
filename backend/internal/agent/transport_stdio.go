package agent

// transport_stdio.go — ACP over stdio JSON-RPC 2.0 transport
//
// 启动 agent 作为子进程，agent 从 stdin 读 JSON-RPC 请求，写响应到 stdout。
// 日志走 stderr（转发到 pocketd logger）。
//
// 帧格式（per ACP spec）：
//   - 每条 JSON-RPC 帧必须是单行 UTF-8 JSON + `\n`
//   - 子进程 stdout 只能输出 JSON-RPC 帧（不能有其他内容）
//   - 子进程 stderr 输出自由文本日志
//
// 并发模型：
//   - 一个 transport 实例 = 一个子进程
//   - 一个 goroutine 调 Recv 读 stdout（line scanner）
//   - 多个 goroutine 并发 Call（id 由 PendingCalls 匹配）
//   - Notify 直接写 stdin
//
// 生命周期：
//   - Start → 启动子进程 + 启动 Recv goroutine
//   - Close → 关闭 stdin + 关闭 stdout reader + 关闭子进程
//   - 子进程崩溃 → Recv 返回 error，Call/Notify 也失败；caller 应 Close+Restart

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

// StdioTransport 是 stdio JSON-RPC transport。
type StdioTransport struct {
	cfg    TransportConfig
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser

	mu      sync.Mutex
	closed  bool
	idAlloc *IDAllocator
	pending *PendingCalls

	// Recv goroutine 通过这个 channel 把帧推给调用方。
	recvCh    chan []byte
	recvErrCh chan error

	// cancelFn 用于关闭所有 background goroutine
	cancelFn context.CancelFunc
	wg       sync.WaitGroup
}

// NewStdioTransport 构造（不启动子进程）。
func NewStdioTransport(cfg TransportConfig) *StdioTransport {
	return &StdioTransport{
		cfg:       cfg,
		idAlloc:   NewIDAllocator(),
		pending:   NewPendingCalls(),
		recvCh:    make(chan []byte, 32),
		recvErrCh: make(chan error, 1),
	}
}

// Start 启动子进程并启动 Recv goroutine。
func (t *StdioTransport) Start(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cmd != nil {
		return errors.New("transport already started")
	}

	cmd := exec.CommandContext(ctx, t.cfg.AgentPath, t.cfg.AgentArgs...)
	cmd.Env = append(os.Environ(), t.cfg.AgentEnv...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return NewUnreachableError(err)
	}

	t.cmd = cmd
	t.stdin = stdin
	t.stdout = stdout
	t.stderr = stderr

	recvCtx, cancel := context.WithCancel(context.Background())
	t.cancelFn = cancel

	// 启动 stdout reader goroutine
	t.wg.Add(1)
	go t.readStdout(recvCtx, stdout)

	// 启动 stderr forwarder goroutine（仅在 cfg.Logger 非 nil 时）
	if t.cfg.Logger != nil {
		t.wg.Add(1)
		go t.forwardStderr(recvCtx, stderr)
	}

	return nil
}

// Close 关闭 transport + 杀子进程。
func (t *StdioTransport) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	pending := t.pending
	t.mu.Unlock()

	// 1. 取消所有 in-flight calls
	pending.Cancel()

	// 2. 关闭 reader goroutines
	if t.cancelFn != nil {
		t.cancelFn()
	}

	// 3. 关闭 stdin（让 agent 优雅退出）
	if t.stdin != nil {
		_ = t.stdin.Close()
	}

	// 4. 等待子进程退出（5s 超时；强杀兜底）
	if t.cmd != nil && t.cmd.Process != nil {
		done := make(chan error, 1)
		go func() { done <- t.cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = t.cmd.Process.Kill()
		}
	}

	// 5. 等待所有 goroutine 退出
	t.wg.Wait()
	return nil
}

// Call 发送 JSON-RPC request 并等待 response。
func (t *StdioTransport) Call(ctx context.Context, method string, params any, out any) error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return NewProtocolError(errors.New("transport closed"))
	}
	id := t.idAlloc.Next()
	ch := t.pending.Register(id)
	stdin := t.stdin
	t.mu.Unlock()

	paramsJSON, err := encodeJSON(params)
	if err != nil {
		return fmt.Errorf("encode params: %w", err)
	}
	reqBytes, err := MarshalRequest(id, method, paramsJSON)
	if err != nil {
		return NewProtocolError(err)
	}

	// 写 stdin（线程安全因为 stdio pipe 支持并发写）
	if _, err := stdin.Write(reqBytes); err != nil {
		return ClassifyNetworkError(err)
	}

	// 等响应（同时监听 ctx cancel 和子进程崩溃）
	select {
	case resp, ok := <-ch:
		if !ok {
			return NewProtocolError(errors.New("call cancelled"))
		}
		if out == nil {
			return nil
		}
		if len(resp.Result) == 0 {
			return NewProtocolError(errors.New("empty result"))
		}
		if err := json.Unmarshal(resp.Result, out); err != nil {
			return NewProtocolError(fmt.Errorf("decode result: %w", err))
		}
		return nil
	case <-ctx.Done():
		return NewTimeoutError(ctx.Err())
	}
}

// Notify 发送 JSON-RPC notification（无 id 不等响应）。
func (t *StdioTransport) Notify(ctx context.Context, method string, params any) error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return NewProtocolError(errors.New("transport closed"))
	}
	stdin := t.stdin
	t.mu.Unlock()

	paramsJSON, err := encodeJSON(params)
	if err != nil {
		return fmt.Errorf("encode params: %w", err)
	}
	notifBytes, err := MarshalNotification(method, paramsJSON)
	if err != nil {
		return NewProtocolError(err)
	}
	if _, err := stdin.Write(notifBytes); err != nil {
		return ClassifyNetworkError(err)
	}
	return nil
}

// Recv 接收下一帧。
func (t *StdioTransport) Recv(ctx context.Context) ([]byte, error) {
	select {
	case frame := <-t.recvCh:
		return frame, nil
	case err := <-t.recvErrCh:
		return nil, err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// readStdout 是 Recv 的 producer goroutine — 从 stdout 读行并分发。
func (t *StdioTransport) readStdout(ctx context.Context, r io.Reader) {
	defer t.wg.Done()
	scanner := bufio.NewScanner(r)
	// ACP frame 单行 JSON；扩到 4 MB 容纳大 prompt 响应
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		// 分类分发：response / error / notification / request
		// Recv() 路径只关心 notification（消费者自己处理）。
		// response / error 自动投递给 PendingCalls。
		frameType, _, resp, errResp, _ := ParseFrame(line)
		switch frameType {
		case "response":
			if resp != nil {
				t.pending.Deliver(resp)
			}
			// 同时也推给 Recv() 调用方（兼容性）
			select {
			case <-ctx.Done():
				return
			case t.recvCh <- append([]byte{}, line...):
			default:
				// channel 满，丢弃（响应已经被 PendingCalls 处理）
			}
		case "error":
			if errResp != nil {
				// error response 也作为 response 投递（带空 result）
				t.pending.Deliver(&Response{
					JSONRPC: "2.0",
					ID:      errResp.ID,
				})
			}
			select {
			case <-ctx.Done():
				return
			case t.recvErrCh <- NewUpstreamError(errResp.Error.Code, errResp.Error.Message, nil):
			default:
			}
		default:
			// notification / request / unknown → 推给 Recv 调用方
			select {
			case <-ctx.Done():
				return
			case t.recvCh <- append([]byte{}, line...):
			}
		}
	}
	// scanner 退出 = EOF（agent 关闭 stdout）或错误
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		select {
		case t.recvErrCh <- NewProtocolError(err):
		default:
		}
	} else {
		select {
		case t.recvErrCh <- NewUnreachableError(io.EOF):
		default:
		}
	}
}

// forwardStderr 把子进程 stderr 日志转发到 cfg.Logger。
func (t *StdioTransport) forwardStderr(ctx context.Context, r io.Reader) {
	defer t.wg.Done()
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		t.cfg.Logger(scanner.Text())
	}
}
