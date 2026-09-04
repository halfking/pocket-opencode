package agent

// adapter_pi.go — pi coding agent adapter（headless JSONL 驱动，非 ACP）
//
// pi（@earendil-works/pi-coding-agent）没有 ACP/JSON-RPC 控制面，方法名也无法
// 复用 ACPStdioAdapter。它的可编程入口是 headless 单次运行：
//
//	pi --mode json "<prompt>"            # 新会话
//	pi --mode json --session <id> "..."  # resume（必须同 CWD）
//
// stdout 逐行输出 JSONL 事件流（实测 0.85.0，2026-09）：
//
//	{"type":"session","id":"<uuid>","cwd":"..."}
//	{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"..."}}
//	{"type":"tool_execution_start","toolCallId":"...","toolName":"bash","args":{...}}
//	{"type":"tool_execution_end","toolCallId":"...","isError":false,...}
//	{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"..."}],
//	  "usage":{"input":..,"output":..,"totalTokens":..,"cost":{"total":..}},"stopReason":"stop"}}
//	{"type":"agent_end",...}
//
// 关键陷阱：provider 错误（认证失败等）时 pi 仍以 exit 0 退出，失败只能从
// 事件流判断（assistant message_end 的 stopReason=="error" + errorMessage，
// 或顶层 error 事件）。此时 SendPrompt 返回 *Error（AGENT_UPSTREAM），同时向
// 订阅者投递 type="error" 的 AgentEvent，双通道保证业务面看到失败。
//
// 生命周期模型：与 ACPStdioAdapter 的长驻进程 + 双向 RPC 不同，这里每次
// SendPrompt 拉起一个一次性子进程，SendPrompt 同步阻塞到该轮结束（与 ACP
// session/prompt 的 stopReason 语义对齐）；事件经 SubscribeEvents 的通道扇出。

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// PiAdapter 实现 AgentAdapter，通过驱动 pi CLI 的 headless JSON 模式。
type PiAdapter struct {
	binPath string // pi 可执行文件路径
	workDir string // 默认工作目录（空 = 继承 pocketd 进程的 CWD）

	mu       sync.Mutex
	nextSub  int64
	subs     map[int64]piSubscriber
	sessions map[string]*piSessionState // sessionKey → 状态（含 pending 别名）
	runs     map[string]*piRun          // sessionKey → 活跃 run（InterruptSession 用）
}

type piSubscriber struct {
	ch  chan AgentEvent
	ctx context.Context
}

// piSessionState 记录一个会话的 resume 所需信息。
// CreateSession 生成 pending 占位 ID（pi 的真实 UUID 要等首轮 session 头才有），
// SendPrompt 结束后把 pending ID 别名到真实 ID。
type piSessionState struct {
	ID        string // pi 真实 session UUID（pending 阶段为空）
	PendingID string // CreateSession 占位 ID（非 pending 会话为空）
	CWD       string
	Title     string
}

// piRun 是一次进行中的 headless 运行。
type piRun struct {
	cancel context.CancelFunc
}

// piPrefix 是 CreateSession 生成的占位会话 ID 前缀。
const piPendingPrefix = "pi-pending-"

// NewPiAdapter 构造。binPath 是 pi 可执行文件路径（如 ~/.npm-global/bin/pi）。
// workDir 为空时子进程继承当前进程 CWD。
func NewPiAdapter(binPath string, workDir string) *PiAdapter {
	return &PiAdapter{
		binPath:  binPath,
		workDir:  workDir,
		subs:     make(map[int64]piSubscriber),
		sessions: make(map[string]*piSessionState),
		runs:     make(map[string]*piRun),
	}
}

// AdapterType 实现 AgentAdapter。
func (a *PiAdapter) AdapterType() string { return "pi" }

// Capabilities 实现 AgentAdapter。
//
// pi 的 headless 单次运行模型没有会话列表/删除/消息回放等控制面（数据只落在
// 本地 ~/.pi/agent/sessions，另有 disk 只读聚合路径消费），所以会话类能力位
// 全部 false，前端按能力协商自动降级。Streaming = true：SendPrompt 期间的
// JSONL 事件经 SubscribeEvents 通道实时扇出。
//
// 注意：没有实现 PermissionCapable / QuestionCapable —— handler 用类型断言
// 探测，缺省自动走降级路径。
func (a *PiAdapter) Capabilities(ctx context.Context, ref AgentRef) (*AgentCapabilities, error) {
	return &AgentCapabilities{
		LoadSession:     false,
		ListSessions:    false,
		DeleteSession:   false,
		SetMode:         false,
		SetConfigOption: false,
		PromptImage:     false,
		PromptAudio:     false,
		PromptEmbedCtx:  false,
		MCPHTTP:         false,
		MCPSSE:          false,
		Permission:      false,
		Question:        false,
		Streaming:       true,
	}, nil
}

// HealthCheck 实现 AgentAdapter：验证 pi 二进制可执行（--version 快速返回）。
func (a *PiAdapter) HealthCheck(ctx context.Context, ref AgentRef) error {
	if a.binPath == "" {
		return NewUnreachableError(errors.New("pi binary path is empty"))
	}
	if info, err := os.Stat(a.binPath); err != nil || info.IsDir() {
		return NewUnreachableError(fmt.Errorf("pi binary not found: %s", a.binPath))
	}
	vctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(vctx, a.binPath, "--version").Output()
	if err != nil {
		return NewUnreachableError(fmt.Errorf("pi --version failed: %w", err))
	}
	_ = out // 版本号仅用于验证可执行，不做进一步解析
	return nil
}

// ---- 会话生命周期 ----

// ListSessions 不支持（headless 模型没有会话列表控制面）。
func (a *PiAdapter) ListSessions(ctx context.Context, ref AgentRef, opts ListOptions) ([]AgentSession, error) {
	return nil, NewCapabilityError("listSessions")
}

// CreateSession 生成一个本地占位会话（pending）。
//
// pi 只在首轮 prompt 后才产生真实 session UUID（JSONL 的 session 头），所以
// 这里只登记 cwd/title 并返回占位 ID；SendPrompt 运行后占位 ID 与真实 UUID
// 双向别名，后续用任意一个 resume 都能找到 cwd。
func (a *PiAdapter) CreateSession(ctx context.Context, ref AgentRef, req *CreateSessionRequest) (*AgentSession, error) {
	if req == nil {
		req = &CreateSessionRequest{}
	}
	pendingID := piPendingPrefix + strconv.FormatInt(time.Now().UnixNano(), 36)
	st := &piSessionState{
		PendingID: pendingID,
		CWD:       req.WorkingDir,
		Title:     req.Title,
	}
	a.mu.Lock()
	a.sessions[pendingID] = st
	a.mu.Unlock()
	return &AgentSession{
		ID:         pendingID,
		Title:      req.Title,
		Status:     "idle",
		Agent:      "pi",
		WorkingDir: req.WorkingDir,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}, nil
}

// LoadSession 不支持。
func (a *PiAdapter) LoadSession(ctx context.Context, ref AgentRef, sessionID string) (*AgentSession, error) {
	return nil, NewCapabilityError("loadSession")
}

// DeleteSession 不支持（也绝不应删除 agent 的本地转录）。
func (a *PiAdapter) DeleteSession(ctx context.Context, ref AgentRef, sessionID string) error {
	return NewCapabilityError("deleteSession")
}

// GetMessages 不支持（历史转录走 disk 只读聚合，不经 adapter 控制面）。
func (a *PiAdapter) GetMessages(ctx context.Context, ref AgentRef, sessionID string, opts ListOptions) ([]AgentMessage, error) {
	return nil, NewCapabilityError("getMessages")
}

// SetSessionMode 不支持。
func (a *PiAdapter) SetSessionMode(ctx context.Context, ref AgentRef, sessionID, modeID string) error {
	return NewCapabilityError("setMode")
}

// ---- 对话 ----

// SendPrompt 拉起一次 `pi --mode json` 子进程并同步等待该轮结束。
//
//   - sessionID 为空或 pending 占位 ID → 新会话（真实 UUID 从 session 头提取）
//   - sessionID 为真实 pi UUID → `--session <id>` resume
//   - 工作目录优先级：会话登记的 CWD > req.Metadata["cwd"] > adapter workDir > 继承
//
// 返回的 SendPromptResult.StopReason 映射自最后一条 assistant message_end。
// provider 错误（exit 0 但流中 stopReason=="error"）返回 *Error（AGENT_UPSTREAM）。
func (a *PiAdapter) SendPrompt(ctx context.Context, ref AgentRef, sessionID string, req *SendPromptRequest) (*SendPromptResult, error) {
	if req == nil {
		req = &SendPromptRequest{}
	}
	prompt := piPromptText(req)
	if strings.TrimSpace(prompt) == "" {
		return nil, NewBadRequestError(400, "empty prompt", nil)
	}

	// 解析会话状态与工作目录。
	a.mu.Lock()
	st := a.sessions[sessionID]
	a.mu.Unlock()
	cwd := a.workDir
	if st != nil && st.CWD != "" {
		cwd = st.CWD
	}
	if v, ok := req.Metadata["cwd"].(string); ok && v != "" {
		cwd = v
	}
	isPending := st != nil && st.PendingID == sessionID

	// 组装命令行：--mode json [--session <id>] -- "<prompt>"
	args := make([]string, 0, 8)
	args = append(args, "--mode", "json")
	if sessionID != "" && !isPending {
		args = append(args, "--session", sessionID)
	}
	args = append(args, "--", prompt)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(runCtx, a.binPath, args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, NewUnreachableError(err)
	}
	if err := cmd.Start(); err != nil {
		return nil, NewUnreachableError(err)
	}

	// 登记活跃 run（InterruptSession 按 sessionKey 取消）。
	runKey := sessionID
	a.mu.Lock()
	a.runs[runKey] = &piRun{cancel: cancel}
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.runs, runKey)
		a.mu.Unlock()
	}()

	// 逐行解析 JSONL → AgentEvent 扇出；同时累积最终结果。
	final := &piFinal{}
	sessID := sessionID
	if isPending {
		sessID = "" // 等真实 UUID
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		evt, info := piParseLine(scanner.Text())
		if info != nil {
			if info.SessionID != "" {
				sessID = info.SessionID
			}
			info.applyTo(final)
		}
		if evt == nil {
			continue
		}
		if evt.SessionID == "" {
			evt.SessionID = sessID
		}
		evt.Timestamp = time.Now()
		a.emit(runCtx, *evt)
	}

	waitErr := cmd.Wait()

	// 运行结束后：真实 session UUID 回填会话状态（cwd 是 resume 的必要条件）。
	if sessID != "" {
		a.mu.Lock()
		if st == nil {
			st = &piSessionState{}
			a.sessions[sessID] = st
			if sessionID != "" {
				a.sessions[sessionID] = st // resume 传入的 key 也指向同一状态
			}
		}
		if st.ID == "" {
			st.ID = sessID
		}
		if st.CWD == "" && cwd != "" {
			st.CWD = cwd
		}
		if sessionID != "" && sessID != sessionID {
			a.sessions[sessID] = st // pending/别名 → 真实 UUID
		}
		a.mu.Unlock()
	}

	// ---- 错误分类（顺序敏感）----
	if ctxErr := ctx.Err(); ctxErr != nil {
		// 外部 ctx 取消（含超时）。
		if errors.Is(ctxErr, context.Canceled) {
			return nil, NewCancelledError()
		}
		return nil, NewTimeoutError(ctxErr)
	}
	if waitErr != nil {
		if runCtx.Err() != nil {
			// 内部 cancel（InterruptSession）杀掉进程，Wait 报 signal: killed。
			return nil, NewCancelledError()
		}
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			return nil, &Error{
				Code: "AGENT_UPSTREAM",
				Message: fmt.Sprintf("pi exited with code %d: %s",
					exitErr.ExitCode(), truncateStr(stderr.String(), 200)),
				CanRetry: true,
			}
		}
		return nil, NewUnreachableError(waitErr)
	}
	if final.errMsg != "" {
		// 陷阱：provider 失败时 pi 仍 exit 0，只能从事件流判断。
		// error 事件已扇出给订阅者；这里让 SendPrompt 也返回失败。
		return nil, &Error{
			Code:     "AGENT_UPSTREAM",
			Message:  "pi provider error: " + final.errMsg,
			CanRetry: true,
		}
	}
	if sessID == "" {
		return nil, NewProtocolError(errors.New("pi produced no session header"))
	}

	return &SendPromptResult{
		StopReason: piMapStopReason(final.stopReason),
	}, nil
}

// InterruptSession 取消该会话进行中的 headless 运行（kill 子进程）。
// 没有活跃 run 时是幂等 no-op。
func (a *PiAdapter) InterruptSession(ctx context.Context, ref AgentRef, sessionID string) error {
	a.mu.Lock()
	run := a.runs[sessionID]
	a.mu.Unlock()
	if run != nil {
		run.cancel()
	}
	return nil
}

// ---- 流式事件 ----

// SubscribeEvents 实现 AgentAdapter。
//
// 返回的通道接收所有进行中 SendPrompt 运行的事件（多订阅者各得一份完整拷贝）。
// cleanup 或 ctx cancel 后通道关闭、停止投递。没有订阅者时事件被丢弃
// （SendPrompt 的最终结果不依赖订阅）。
func (a *PiAdapter) SubscribeEvents(ctx context.Context, ref AgentRef) (<-chan AgentEvent, func(), error) {
	ch := make(chan AgentEvent, 64)
	a.mu.Lock()
	a.nextSub++
	id := a.nextSub
	a.subs[id] = piSubscriber{ch: ch, ctx: ctx}
	a.mu.Unlock()

	cleanup := func() {
		a.mu.Lock()
		if _, ok := a.subs[id]; ok {
			delete(a.subs, id)
			close(ch)
		}
		a.mu.Unlock()
	}
	// ctx cancel 时自动清理（cleanup 幂等，双路径安全）。
	go func() {
		<-ctx.Done()
		cleanup()
	}()
	return ch, cleanup, nil
}

// emit 把事件扇出给所有订阅者。单个慢订阅者最多阻塞到其 ctx 或 runCtx 结束，
// 不拖垮其他订阅者之外的逻辑。
func (a *PiAdapter) emit(ctx context.Context, evt AgentEvent) {
	a.mu.Lock()
	subs := make([]piSubscriber, 0, len(a.subs))
	for _, s := range a.subs {
		subs = append(subs, s)
	}
	a.mu.Unlock()
	for _, s := range subs {
		select {
		case s.ch <- evt:
		case <-s.ctx.Done():
		case <-ctx.Done():
			return
		}
	}
}

// Close 停止所有活跃 run 并清理状态（AgentAdapter 接口之外的生命周期辅助）。
func (a *PiAdapter) Close() error {
	a.mu.Lock()
	runs := a.runs
	a.runs = make(map[string]*piRun)
	a.subs = make(map[int64]piSubscriber)
	a.mu.Unlock()
	for _, r := range runs {
		r.cancel()
	}
	return nil
}

// 编译期断言。
var _ AgentAdapter = (*PiAdapter)(nil)

// ---- JSONL 解析 ----

// piUsage 是 assistant message_end 的 usage 摘要。
type piUsage struct {
	Input       int64
	Output      int64
	TotalTokens int64
	CostUSD     float64
}

// piFinal 累积一轮运行的最终结果（SendPrompt 返回值与错误判定用）。
type piFinal struct {
	text       string
	stopReason string
	errMsg     string
	usage      *piUsage
}

// applyTo 把单行信息累积进最终结果（后到的 assistant message_end 完全覆盖
// 前者——pi 自动重试成功时会用好的 message_end 覆盖之前的错误）。
func (i *piLineInfo) applyTo(f *piFinal) {
	if i == nil || f == nil {
		return
	}
	if i.hasFinal {
		f.text = i.text
		f.stopReason = i.stopReason
		f.usage = i.usage
		f.errMsg = i.errMsg
		return
	}
	if i.errMsg != "" {
		// 顶层 error 事件（无 message_end 的失败路径）。
		f.errMsg = i.errMsg
	}
}

// piLineInfo 是单行 JSONL 解析出的辅助信息（evt 为对外的 AgentEvent，可为 nil）。
type piLineInfo struct {
	SessionID  string
	CWD        string
	hasFinal   bool
	text       string
	stopReason string
	errMsg     string
	usage      *piUsage
}

// piParseLine 解析一行 pi JSONL 输出。
//
// 返回 (事件, 行信息)。事件为 nil 表示该行不产生对外事件；行信息用于
// SendPrompt 累积最终结果（session UUID、最终文本、usage、错误）。
//
// 事件映射表（pi → AgentEvent.Type）：
//
//	session                                        → session
//	message_update (text_delta)                    → message_chunk
//	message_update (thinking_delta)                → thought_chunk
//	tool_execution_start                           → tool_call
//	tool_execution_end                             → tool_call_update
//	message_end (assistant, stopReason=="error")   → error
//	message_end (assistant, 其他)                  → message_end
//	agent_end                                      → done
//	error（防御性：顶层错误事件）                  → error
func piParseLine(line string) (*AgentEvent, *piLineInfo) {
	line = strings.TrimSpace(line)
	if line == "" || line[0] != '{' {
		return nil, nil
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(line), &obj); err != nil {
		return nil, nil
	}
	typ, _ := obj["type"].(string)
	info := &piLineInfo{}

	switch typ {
	case "session":
		info.SessionID, _ = obj["id"].(string)
		info.CWD, _ = obj["cwd"].(string)
		return &AgentEvent{
			Type:      "session",
			SessionID: info.SessionID,
			Data:      map[string]any{"raw": obj},
		}, info

	case "message_update":
		am, _ := obj["assistantMessageEvent"].(map[string]any)
		at, _ := am["type"].(string)
		switch at {
		case "text_delta":
			delta, _ := am["delta"].(string)
			if delta == "" {
				return nil, nil
			}
			return &AgentEvent{
				Type: "message_chunk",
				Data: map[string]any{"text": delta, "raw": obj},
			}, nil
		case "thinking_delta":
			delta, _ := am["delta"].(string)
			if delta == "" {
				return nil, nil
			}
			return &AgentEvent{
				Type: "thought_chunk",
				Data: map[string]any{"text": delta, "raw": obj},
			}, nil
		}
		return nil, nil

	case "tool_execution_start":
		id, _ := obj["toolCallId"].(string)
		name, _ := obj["toolName"].(string)
		args, _ := obj["args"].(map[string]any)
		return &AgentEvent{
			Type: "tool_call",
			Data: map[string]any{
				"toolCallId": id,
				"toolName":   name,
				"args":       args,
			},
		}, nil

	case "tool_execution_end":
		id, _ := obj["toolCallId"].(string)
		isError, _ := obj["isError"].(bool)
		return &AgentEvent{
			Type: "tool_call_update",
			Data: map[string]any{
				"toolCallId": id,
				"isError":    isError,
				"raw":        obj,
			},
		}, nil

	case "message_end":
		msg, _ := obj["message"].(map[string]any)
		if role, _ := msg["role"].(string); role != "assistant" {
			return nil, nil
		}
		info.hasFinal = true
		info.text = piTextOf(msg)
		info.stopReason, _ = msg["stopReason"].(string)
		info.usage = piUsageOf(msg)
		if info.stopReason == "error" {
			info.errMsg, _ = msg["errorMessage"].(string)
			if info.errMsg == "" {
				info.errMsg = "pi assistant message failed (stopReason=error)"
			}
			return &AgentEvent{
				Type: "error",
				Data: map[string]any{
					"message":    info.errMsg,
					"stopReason": info.stopReason,
					"raw":        obj,
				},
			}, info
		}
		data := map[string]any{
			"text":       info.text,
			"stopReason": info.stopReason,
			"raw":        obj,
		}
		if info.usage != nil {
			data["usage"] = map[string]any{
				"input":       info.usage.Input,
				"output":      info.usage.Output,
				"totalTokens": info.usage.TotalTokens,
				"cost":        info.usage.CostUSD,
			}
		}
		return &AgentEvent{Type: "message_end", Data: data}, info

	case "agent_end":
		return &AgentEvent{
			Type: "done",
			Data: map[string]any{"raw": obj},
		}, nil

	case "error":
		// 防御性：顶层错误事件（pi 版本演进可能引入）。
		info.errMsg = piErrorMessage(obj)
		return &AgentEvent{
			Type: "error",
			Data: map[string]any{"message": info.errMsg, "raw": obj},
		}, info
	}
	return nil, nil
}

// piTextOf 拼接 message content 里的全部 text 块。
func piTextOf(msg map[string]any) string {
	blocks, ok := msg["content"].([]any)
	if !ok {
		return ""
	}
	var sb strings.Builder
	for _, b := range blocks {
		m, ok := b.(map[string]any)
		if !ok || m["type"] != "text" {
			continue
		}
		if t, ok := m["text"].(string); ok {
			sb.WriteString(t)
		}
	}
	return sb.String()
}

// piUsageOf 提取 message.usage（pi 字段：input / output / totalTokens / cost.total）。
func piUsageOf(msg map[string]any) *piUsage {
	mu, ok := msg["usage"].(map[string]any)
	if !ok {
		return nil
	}
	u := &piUsage{
		Input:       piInt(mu["input"]),
		Output:      piInt(mu["output"]),
		TotalTokens: piInt(mu["totalTokens"]),
	}
	if c, ok := mu["cost"].(map[string]any); ok {
		u.CostUSD = piFloat(c["total"])
	}
	return u
}

// piErrorMessage 从顶层错误对象提取消息文本（兼容多种字段形状）。
func piErrorMessage(obj map[string]any) string {
	if s, ok := obj["errorMessage"].(string); ok && s != "" {
		return s
	}
	if s, ok := obj["message"].(string); ok && s != "" {
		return s
	}
	if m, ok := obj["error"].(map[string]any); ok {
		if s, ok := m["message"].(string); ok && s != "" {
			return s
		}
	}
	return "unknown pi error"
}

// piMapStopReason 把 pi stopReason 映射到 ACP 风格（unknown 透传）。
func piMapStopReason(s string) string {
	switch s {
	case "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "aborted":
		return "cancelled"
	case "":
		return ""
	default:
		return s
	}
}

// piPromptText 拼装 prompt：Text 优先；Parts 时串联其中的 text 块
// （pi headless 只接受文本 prompt）。
func piPromptText(req *SendPromptRequest) string {
	if req.Text != "" {
		return req.Text
	}
	var sb strings.Builder
	for _, p := range req.Parts {
		if p.Text != "" {
			if sb.Len() > 0 {
				sb.WriteString("\n\n")
			}
			sb.WriteString(p.Text)
		}
	}
	return sb.String()
}

// piInt / piFloat 安全取数值字段（JSON unmarshal 数字为 float64）。
func piInt(v any) int64 {
	if f, ok := v.(float64); ok {
		return int64(f)
	}
	return 0
}

func piFloat(v any) float64 {
	if f, ok := v.(float64); ok {
		return f
	}
	return 0
}
