package disk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/adapter"
	"github.com/halfking/pocket-opencode/backend/internal/model"
)

// 本机 disk agent 的 instance_id 与 locator。
//
// locator 不是网络地址：它只是 registry 里 instance_id → 本地数据源的解析结果，
// 由 registry 按 workspace 作用域返回。Adapter 只接受这两个常量值，客户端传来的
// 任何字符串都无法变成路径或 URL（沿用 pocketd「绝不信任客户端 URL」原则）。
const (
	InstanceClaude = "disk-claude"
	InstanceCodex  = "disk-codex"

	LocatorClaude = "disk://claude"
	LocatorCodex  = "disk://codex"
)

// ErrNotSupported 表示该操作对磁盘会话聚合无意义。磁盘 agent 没有控制面：
// 不能发 prompt、不能建/删会话、不能中断、不能回复审批、没有事件流。
// 磁盘数据严格只读，DeleteSession 尤其不能实现——那会删掉 agent 的真实转录。
var ErrNotSupported = errors.New("not supported for disk adapter: read-only disk session aggregation")

// reader 是单个 agent 的磁盘读取器（Wake AgentAdapter 的最小 Go 对应物）。
type reader interface {
	agent() string
	displayName() string
	dataPath() string
	detect() bool
	listSessions() ([]SessionMeta, error)
	transcript(sessionID string) (SessionMeta, []TranscriptMessage, error)
}

// Adapter 实现 adapter.OpenCodeAdapter 的只读子集：把本机 Claude Code / Codex
// 的磁盘会话当成「实例」暴露给既有会话视图与移动端路由。
type Adapter struct {
	readers map[string]reader // locator → reader
}

// 编译期确认 disk.Adapter 满足既有适配器接口，能被路由/事件层复用。
var _ adapter.OpenCodeAdapter = (*Adapter)(nil)

// New 用当前用户的 home 目录构造 Adapter。
func New() *Adapter {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	return NewWithHome(home)
}

// NewWithHome 允许注入 home 目录（测试用）。
func NewWithHome(home string) *Adapter {
	return &Adapter{readers: map[string]reader{
		LocatorClaude: newClaudeReader(home),
		LocatorCodex:  newCodexReader(home),
	}}
}

// InstanceDescriptor 描述一个待注册的 disk 实例。
type InstanceDescriptor struct {
	InstanceID  string
	Locator     string
	DisplayName string
	Agent       string
	DataPath    string
}

// instanceLocators 固定 instance_id → locator 映射（唯一可信来源）。
var instanceLocators = []struct {
	instanceID string
	locator    string
}{
	{InstanceClaude, LocatorClaude},
	{InstanceCodex, LocatorCodex},
}

// DetectedInstances 返回本机真实存在数据目录的 disk 实例（未安装的 agent 不注册）。
func (a *Adapter) DetectedInstances() []InstanceDescriptor {
	out := make([]InstanceDescriptor, 0, len(instanceLocators))
	for _, item := range instanceLocators {
		r, ok := a.readers[item.locator]
		if !ok || !r.detect() {
			continue
		}
		out = append(out, InstanceDescriptor{
			InstanceID:  item.instanceID,
			Locator:     item.locator,
			DisplayName: r.displayName(),
			Agent:       r.agent(),
			DataPath:    r.dataPath(),
		})
	}
	return out
}

// InstanceRegistrar 是 disk 实例注册所需的 registry 能力子集
// （*registry.Registry 已满足），避免本包反向依赖整个 registry 包。
type InstanceRegistrar interface {
	RegisterInstance(instance *model.PocketInstance) error
	SetInstanceAPIBase(instanceID, apiBaseURL string)
}

// Register 把检测到的 disk 实例写入 registry。
//
// workspaceID 为空时注册为「运维共享只读资源」：所有 workspace 都能读
// （ListInstancesForWorkspace / GetInstanceAPIBaseForWorkspace 放行），但
// GetWritableInstanceAPIBaseForWorkspace 会拒绝——正好匹配 disk 适配器的
// 只读语义。传入具体 workspaceID 则把实例限定给该租户。
//
// 返回成功注册的 instance_id 列表。
func (a *Adapter) Register(reg InstanceRegistrar, workspaceID string) ([]string, error) {
	if reg == nil {
		return nil, errors.New("disk: instance registrar is required")
	}
	registered := make([]string, 0, 2)
	var firstErr error
	now := time.Now().UTC().Format(time.RFC3339)
	for _, desc := range a.DetectedInstances() {
		instance := &model.PocketInstance{
			ID:          desc.InstanceID,
			DisplayName: desc.DisplayName,
			Environment: "local",
			// 只读磁盘聚合：声明能力时不含 prompt/pty，前端据此隐藏写操作。
			Capabilities:    []string{"session", "transcript", "read-only"},
			Health:          "healthy", // 本机文件，只要检测到目录就可用
			LastHeartbeatAt: now,
			APIBaseURL:      desc.Locator,
			Origin:          "disk",
			WorkspaceID:     workspaceID,
			MigrationStatus: "idle",
		}
		if err := reg.RegisterInstance(instance); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("register %s: %w", desc.InstanceID, err)
			}
			continue
		}
		reg.SetInstanceAPIBase(desc.InstanceID, desc.Locator)
		registered = append(registered, desc.InstanceID)
	}
	return registered, firstErr
}

// resolve 把 instanceBaseURL 解析为 reader。只认内置 locator 常量：
// 未知取值一律报错，绝不当成路径/URL 使用。
func (a *Adapter) resolve(instanceBaseURL string) (reader, error) {
	r, ok := a.readers[strings.TrimSpace(instanceBaseURL)]
	if !ok {
		return nil, fmt.Errorf("disk adapter: unknown locator %q (expected %s or %s)",
			instanceBaseURL, LocatorClaude, LocatorCodex)
	}
	if !r.detect() {
		return nil, fmt.Errorf("disk adapter: %s data directory not found: %s", r.agent(), r.dataPath())
	}
	return r, nil
}

// IsLocator 报告 instanceBaseURL 是否为本包管理的 disk locator。
// 调用方（路由层）可用它决定把请求交给 disk 适配器还是 HTTP 适配器。
func IsLocator(instanceBaseURL string) bool {
	switch strings.TrimSpace(instanceBaseURL) {
	case LocatorClaude, LocatorCodex:
		return true
	default:
		return false
	}
}

// ---- 读路径 ----

// ListSessions 列出该 disk 实例的全部会话，按最近更新降序。
func (a *Adapter) ListSessions(ctx context.Context, instanceBaseURL string) ([]adapter.OpenCodeSession, error) {
	metas, err := a.listSessionMetas(ctx, instanceBaseURL)
	if err != nil {
		return nil, err
	}
	out := make([]adapter.OpenCodeSession, 0, len(metas))
	for _, meta := range metas {
		out = append(out, adapter.OpenCodeSession{
			ID:          meta.ID,
			Title:       meta.Title,
			Status:      sessionStatus(meta.UpdatedAt),
			TimeUpdated: meta.UpdatedAt,
		})
	}
	return out, nil
}

// ListSessionMetas 暴露归一化元数据（供 acc_report_session 上报等内部调用方使用）。
func (a *Adapter) ListSessionMetas(ctx context.Context, instanceBaseURL string) ([]SessionMeta, error) {
	return a.listSessionMetas(ctx, instanceBaseURL)
}

func (a *Adapter) listSessionMetas(ctx context.Context, instanceBaseURL string) ([]SessionMeta, error) {
	r, err := a.resolve(instanceBaseURL)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	metas, err := r.listSessions()
	if err != nil {
		return nil, err
	}
	sort.SliceStable(metas, func(i, j int) bool { return metas[i].UpdatedAt > metas[j].UpdatedAt })
	return metas, nil
}

// GetSessionSummary 返回会话标题（磁盘转录没有独立摘要）。
func (a *Adapter) GetSessionSummary(ctx context.Context, instanceBaseURL, sessionID string) (string, error) {
	r, err := a.resolve(instanceBaseURL)
	if err != nil {
		return "", err
	}
	meta, _, err := r.transcript(sessionID)
	if err != nil {
		return "", err
	}
	return meta.Title, nil
}

// ListRemoteTasks 把每个磁盘会话映射为一个只读「开发任务」，与 OpenCode HTTP
// 适配器的 session→task 语义保持一致。
func (a *Adapter) ListRemoteTasks(ctx context.Context, instanceBaseURL, status string, limit int) ([]adapter.RemoteTask, error) {
	metas, err := a.listSessionMetas(ctx, instanceBaseURL)
	if err != nil {
		return nil, err
	}
	out := make([]adapter.RemoteTask, 0, len(metas))
	for _, meta := range metas {
		st := sessionStatus(meta.UpdatedAt)
		if status != "" && status != st {
			continue
		}
		out = append(out, adapter.RemoteTask{
			ID:     meta.ID,
			Title:  meta.Title,
			Status: st,
			Owner:  meta.Agent,
		})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

// GetSessionMessages 返回归一化后的会话消息。
// cursor 不支持（磁盘转录一次解析完整文件），传入时忽略并返回空 cursor。
func (a *Adapter) GetSessionMessages(ctx context.Context, instanceBaseURL, sessionID string, limit int, order string, cursor string) (*adapter.SessionMessagesResponse, error) {
	r, err := a.resolve(instanceBaseURL)
	if err != nil {
		return nil, err
	}
	_, messages, err := r.transcript(sessionID)
	if err != nil {
		return nil, err
	}
	return &adapter.SessionMessagesResponse{
		Data: toOpenCodeMessages(sessionID, applyWindow(messages, limit, order)),
	}, nil
}

// GetMessages 是 OpenCodeAdapter 接口要求的简化版消息拉取。
func (a *Adapter) GetMessages(ctx context.Context, instanceBaseURL, sessionID string, limit int, order string) ([]adapter.OpenCodeMessage, error) {
	resp, err := a.GetSessionMessages(ctx, instanceBaseURL, sessionID, limit, order, "")
	if err != nil {
		return nil, err
	}
	return resp.Data, nil
}

// GetSessionContext 返回最后一个压缩边界之后的消息（等价于 OpenCode 的
// /session/:id/context 语义）。
func (a *Adapter) GetSessionContext(ctx context.Context, instanceBaseURL, sessionID string) ([]adapter.OpenCodeMessage, error) {
	r, err := a.resolve(instanceBaseURL)
	if err != nil {
		return nil, err
	}
	_, messages, err := r.transcript(sessionID)
	if err != nil {
		return nil, err
	}
	start := 0
	for i, m := range messages {
		if m.Kind == KindCompactSummary {
			start = i + 1
		}
	}
	return toOpenCodeMessages(sessionID, messages[start:]), nil
}

// HealthCheck 对磁盘适配器等价于「数据目录是否可读」。
func (a *Adapter) HealthCheck(ctx context.Context, instanceBaseURL string) error {
	_, err := a.resolve(instanceBaseURL)
	return err
}

// ---- 写路径：磁盘 agent 无控制面，一律显式不支持 ----

// CreateSession 不支持：磁盘 agent 的会话由 agent 自己创建。
func (a *Adapter) CreateSession(ctx context.Context, instanceBaseURL string, payload *adapter.CreateSessionRequest) (*adapter.OpenCodeSessionInfo, error) {
	return nil, fmt.Errorf("CreateSession: %w", ErrNotSupported)
}

// SendPrompt 不支持：需要 agent 的控制面（HTTP / ACP stdio），磁盘只有历史转录。
func (a *Adapter) SendPrompt(ctx context.Context, instanceBaseURL, sessionID string, payload *adapter.SendPromptRequest) (*adapter.SendPromptResponse, error) {
	return nil, fmt.Errorf("SendPrompt: %w", ErrNotSupported)
}

// InterruptSession 不支持：没有正在运行的 agent 循环可中断。
func (a *Adapter) InterruptSession(ctx context.Context, instanceBaseURL, sessionID string) error {
	return fmt.Errorf("InterruptSession: %w", ErrNotSupported)
}

// DeleteSession 永不支持：删除会等于抹掉 agent 的真实转录文件（只读铁律）。
func (a *Adapter) DeleteSession(ctx context.Context, instanceBaseURL, sessionID string) error {
	return fmt.Errorf("DeleteSession: %w", ErrNotSupported)
}

// SubscribeEvents 不支持：本阶段不做文件 watch 增量事件（后续可加 fsnotify）。
func (a *Adapter) SubscribeEvents(ctx context.Context, instanceBaseURL, directory, workspaceID string) (<-chan adapter.OpenCodeEvent, func(), error) {
	return nil, nil, fmt.Errorf("SubscribeEvents: %w", ErrNotSupported)
}

// ReplyPermission 不支持：磁盘转录里的权限请求已是历史，无处回复。
func (a *Adapter) ReplyPermission(ctx context.Context, instanceBaseURL, sessionID, requestID string, reply adapter.PermissionReply, message string) error {
	return fmt.Errorf("ReplyPermission: %w", ErrNotSupported)
}

// ReplyQuestion 不支持：同上。
func (a *Adapter) ReplyQuestion(ctx context.Context, instanceBaseURL, sessionID, requestID string, answers []adapter.QuestionAnswer) error {
	return fmt.Errorf("ReplyQuestion: %w", ErrNotSupported)
}

// RejectQuestion 不支持：同上。
func (a *Adapter) RejectQuestion(ctx context.Context, instanceBaseURL, sessionID, requestID string) error {
	return fmt.Errorf("RejectQuestion: %w", ErrNotSupported)
}

// ---- 审批「待处理列表」：历史转录永远没有待处理项 ----
//
// 这四个方法让 disk.Adapter 满足 opencode.PermissionCaller / QuestionCaller
// 的形状，从而能被 PermissionManager / QuestionManager 直接接纳而不丢能力。
// 它们返回空列表（而非错误）——「没有待审批项」才是只读历史数据的正确答案，
// 也避免管理器每轮轮询都刷错误日志。

// GetPermissionRequests 恒为空：磁盘会话没有活的权限请求。
func (a *Adapter) GetPermissionRequests(ctx context.Context, instanceBaseURL, sessionID string) ([]adapter.PermissionRequest, error) {
	return nil, nil
}

// GetAllPendingPermissionRequests 恒为空，同上。
func (a *Adapter) GetAllPendingPermissionRequests(ctx context.Context, instanceBaseURL, directory, workspaceID string) ([]adapter.PermissionRequest, error) {
	return nil, nil
}

// GetQuestionRequests 恒为空，同上。
func (a *Adapter) GetQuestionRequests(ctx context.Context, instanceBaseURL, sessionID string) ([]adapter.QuestionRequest, error) {
	return nil, nil
}

// GetAllPendingQuestionRequests 恒为空，同上。
func (a *Adapter) GetAllPendingQuestionRequests(ctx context.Context, instanceBaseURL, directory, workspaceID string) ([]adapter.QuestionRequest, error) {
	return nil, nil
}

// ---- 归一化输出 ----

// sessionStatus 按最近更新时间推断 active/idle（与 parseSessionList 同规则）。
func sessionStatus(updatedAtMS int64) string {
	if updatedAtMS > 0 && time.Since(time.UnixMilli(updatedAtMS)) < 5*time.Minute {
		return "active"
	}
	return "idle"
}

// applyWindow 按 order/limit 截取消息窗口。order="desc" 时最新在前。
func applyWindow(messages []TranscriptMessage, limit int, order string) []TranscriptMessage {
	out := make([]TranscriptMessage, len(messages))
	copy(out, messages)
	if strings.EqualFold(order, "desc") {
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// toOpenCodeMessages 把归一化消息转成 OpenCode V1 信封形状
// （{info:{id,sessionID,role,time},parts:[...]}），从而复用既有的
// opencodeMessage.ToMobile() 映射——移动端无需为 disk 会话做任何改动。
func toOpenCodeMessages(sessionID string, messages []TranscriptMessage) []adapter.OpenCodeMessage {
	out := make([]adapter.OpenCodeMessage, 0, len(messages))
	for _, m := range messages {
		id := fmt.Sprintf("msg_%s_%d", sessionID, m.Seq)
		info := map[string]any{
			"id":        id,
			"sessionID": sessionID,
			"role":      string(m.Role),
			"time":      map[string]any{"created": m.Timestamp},
			// kind 让上层能识别被折叠的注入内容/压缩边界（V1 信封的额外字段被忽略）。
			"kind": string(m.Kind),
		}
		if m.Model != "" {
			info["model"] = m.Model
		}
		parts := make([]any, 0, 2+len(m.ToolCalls))
		if m.Thinking != "" {
			parts = append(parts, map[string]any{"type": "reasoning", "text": m.Thinking})
		}
		if m.Text != "" {
			parts = append(parts, map[string]any{"type": "text", "text": m.Text})
		}
		for _, tc := range m.ToolCalls {
			parts = append(parts, toolPart(tc))
		}
		out = append(out, adapter.OpenCodeMessage{
			ID:   id,
			Type: string(m.Role),
			Data: map[string]any{"info": info, "parts": parts},
		})
	}
	return out
}

// toolPart 把 ToolCallView 转成 V1 tool part（tool/callID/state 三件套）。
func toolPart(tc ToolCallView) map[string]any {
	status := "pending" // 没有输出说明这次调用没有回来（会话被中断）
	switch {
	case tc.IsError:
		status = "error"
	case tc.Output != "":
		status = "completed"
	}
	state := map[string]any{"status": status}
	if input := toolInput(tc); len(input) > 0 {
		state["input"] = input
	}
	if tc.IsError {
		state["error"] = tc.Output
	} else if tc.Output != "" {
		state["output"] = tc.Output
	}
	return map[string]any{
		"type":   "tool",
		"tool":   tc.Name,
		"callID": tc.ID,
		"state":  state,
	}
}

// toolInput 尽量把工具输入还原成结构化对象（移动端期望 input 是对象）；
// 不是 JSON 对象时退回单行预览。
func toolInput(tc ToolCallView) map[string]any {
	if tc.Input != "" {
		var decoded map[string]any
		if err := json.Unmarshal([]byte(tc.Input), &decoded); err == nil && len(decoded) > 0 {
			return decoded
		}
	}
	if tc.InputPreview != "" {
		return map[string]any{"preview": tc.InputPreview}
	}
	return nil
}
