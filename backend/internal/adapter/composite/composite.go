// Package composite 提供一个「按 locator 路由」的 adapter.OpenCodeAdapter，把
// 既有的 HTTP OpenCode 适配器（控制面，支持审批/问答回复）与本机 disk 会话
// 适配器（只读聚合，见 internal/adapter/disk）合并成单一适配器，使移动端流量
// 可以按 instance 的 locator 透明地打到任一侧：
//
//   - disk.IsLocator(url)（disk://claude | disk://codex）→ disk 适配器
//   - 其它（http://... 等 OpenCode 实例）→ HTTP 适配器
//
// 这样 /api/sessions?instance_id=disk-claude 这类请求就能被 disk 适配器处理，
// 而既有 OpenCode HTTP 实例行为完全不变。
//
// 它同时实现 opencode.PermissionCaller / opencode.QuestionCaller 的形状（方法
// 签名仅用 adapter 包类型，故无需在运行时强依赖 opencode 包，可在编译期断言），
// 使审批/问答管理器的运行时类型断言继续成立——disk 路径对这些写操作返回
// ErrNotSupported / 空列表，与「磁盘 agent 无控制面」语义一致，不丢能力。
package composite

import (
	"context"

	"github.com/halfking/pocket-opencode/backend/internal/adapter"
	"github.com/halfking/pocket-opencode/backend/internal/adapter/disk"
	"github.com/halfking/pocket-opencode/backend/internal/opencode"
)

// CompositeAdapter 合并 HTTP 与 disk 两个底层适配器，按 locator 路由。
type CompositeAdapter struct {
	http adapter.OpenCodeAdapter // OpenCode HTTP 控制面（支持审批/问答）
	disk adapter.OpenCodeAdapter // 只读磁盘会话聚合
}

// New 构造复合适配器。两个入参都必须实现 adapter.OpenCodeAdapter。
func New(httpAdapter, diskAdapter adapter.OpenCodeAdapter) *CompositeAdapter {
	return &CompositeAdapter{http: httpAdapter, disk: diskAdapter}
}

// sub 按 locator 选择底层适配器：
// disk locator → disk；其它 → HTTP。
func (c *CompositeAdapter) sub(locator string) adapter.OpenCodeAdapter {
	if disk.IsLocator(locator) {
		return c.disk
	}
	return c.http
}

// 编译期断言：CompositeAdapter 满足既有适配器接口 + 审批/问答能力。
var (
	_ adapter.OpenCodeAdapter   = (*CompositeAdapter)(nil)
	_ opencode.PermissionCaller = (*CompositeAdapter)(nil)
	_ opencode.QuestionCaller   = (*CompositeAdapter)(nil)
)

// ---- 基础 OpenCodeAdapter 方法：全部按 locator 路由给底层适配器 ----

func (c *CompositeAdapter) ListSessions(ctx context.Context, instanceBaseURL string) ([]adapter.OpenCodeSession, error) {
	return c.sub(instanceBaseURL).ListSessions(ctx, instanceBaseURL)
}

func (c *CompositeAdapter) GetSessionSummary(ctx context.Context, instanceBaseURL, sessionID string) (string, error) {
	return c.sub(instanceBaseURL).GetSessionSummary(ctx, instanceBaseURL, sessionID)
}

func (c *CompositeAdapter) ListRemoteTasks(ctx context.Context, instanceBaseURL, status string, limit int) ([]adapter.RemoteTask, error) {
	return c.sub(instanceBaseURL).ListRemoteTasks(ctx, instanceBaseURL, status, limit)
}

func (c *CompositeAdapter) CreateSession(ctx context.Context, instanceBaseURL string, payload *adapter.CreateSessionRequest) (*adapter.OpenCodeSessionInfo, error) {
	return c.sub(instanceBaseURL).CreateSession(ctx, instanceBaseURL, payload)
}

func (c *CompositeAdapter) GetMessages(ctx context.Context, instanceBaseURL, sessionID string, limit int, order string) ([]adapter.OpenCodeMessage, error) {
	return c.sub(instanceBaseURL).GetMessages(ctx, instanceBaseURL, sessionID, limit, order)
}

func (c *CompositeAdapter) SendPrompt(ctx context.Context, instanceBaseURL, sessionID string, payload *adapter.SendPromptRequest) (*adapter.SendPromptResponse, error) {
	return c.sub(instanceBaseURL).SendPrompt(ctx, instanceBaseURL, sessionID, payload)
}

func (c *CompositeAdapter) InterruptSession(ctx context.Context, instanceBaseURL, sessionID string) error {
	return c.sub(instanceBaseURL).InterruptSession(ctx, instanceBaseURL, sessionID)
}

func (c *CompositeAdapter) DeleteSession(ctx context.Context, instanceBaseURL, sessionID string) error {
	return c.sub(instanceBaseURL).DeleteSession(ctx, instanceBaseURL, sessionID)
}

func (c *CompositeAdapter) SubscribeEvents(ctx context.Context, instanceBaseURL, directory, workspaceID string) (<-chan adapter.OpenCodeEvent, func(), error) {
	return c.sub(instanceBaseURL).SubscribeEvents(ctx, instanceBaseURL, directory, workspaceID)
}

func (c *CompositeAdapter) HealthCheck(ctx context.Context, instanceBaseURL string) error {
	return c.sub(instanceBaseURL).HealthCheck(ctx, instanceBaseURL)
}

// ---- 可选能力：审批 / 问答（opencode.PermissionCaller / QuestionCaller）----
//
// 这些方法不在 adapter.OpenCodeAdapter 接口里，但 permission_manager /
// question_manager 会做运行时类型断言。两个底层适配器都实现了它们
// （disk 侧返回 ErrNotSupported / 空列表），故这里用本地接口断言后委托，
// 路由同样由 locator 决定。

type permissionCaller interface {
	GetPermissionRequests(ctx context.Context, baseURL, sessionID string) ([]adapter.PermissionRequest, error)
	ReplyPermission(ctx context.Context, baseURL, sessionID, requestID string, reply adapter.PermissionReply, message string) error
}

type allPermissionCaller interface {
	permissionCaller
	GetAllPendingPermissionRequests(ctx context.Context, baseURL, directory, workspaceID string) ([]adapter.PermissionRequest, error)
}

type questionCaller interface {
	GetQuestionRequests(ctx context.Context, baseURL, sessionID string) ([]adapter.QuestionRequest, error)
	ReplyQuestion(ctx context.Context, baseURL, sessionID, requestID string, answers []adapter.QuestionAnswer) error
	RejectQuestion(ctx context.Context, baseURL, sessionID, requestID string) error
}

type allQuestionCaller interface {
	questionCaller
	GetAllPendingQuestionRequests(ctx context.Context, baseURL, directory, workspaceID string) ([]adapter.QuestionRequest, error)
}

// GetPermissionRequests 按 locator 委托给底层适配器。
func (c *CompositeAdapter) GetPermissionRequests(ctx context.Context, baseURL, sessionID string) ([]adapter.PermissionRequest, error) {
	if pc, ok := c.sub(baseURL).(permissionCaller); ok {
		return pc.GetPermissionRequests(ctx, baseURL, sessionID)
	}
	return nil, nil
}

// GetAllPendingPermissionRequests 按 locator 委托给底层适配器。
func (c *CompositeAdapter) GetAllPendingPermissionRequests(ctx context.Context, baseURL, directory, workspaceID string) ([]adapter.PermissionRequest, error) {
	if pc, ok := c.sub(baseURL).(allPermissionCaller); ok {
		return pc.GetAllPendingPermissionRequests(ctx, baseURL, directory, workspaceID)
	}
	return nil, nil
}

// ReplyPermission 按 locator 委托给底层适配器。
func (c *CompositeAdapter) ReplyPermission(ctx context.Context, baseURL, sessionID, requestID string, reply adapter.PermissionReply, message string) error {
	if pc, ok := c.sub(baseURL).(permissionCaller); ok {
		return pc.ReplyPermission(ctx, baseURL, sessionID, requestID, reply, message)
	}
	return disk.ErrNotSupported
}

// GetQuestionRequests 按 locator 委托给底层适配器。
func (c *CompositeAdapter) GetQuestionRequests(ctx context.Context, baseURL, sessionID string) ([]adapter.QuestionRequest, error) {
	if qc, ok := c.sub(baseURL).(questionCaller); ok {
		return qc.GetQuestionRequests(ctx, baseURL, sessionID)
	}
	return nil, nil
}

// GetAllPendingQuestionRequests 按 locator 委托给底层适配器。
func (c *CompositeAdapter) GetAllPendingQuestionRequests(ctx context.Context, baseURL, directory, workspaceID string) ([]adapter.QuestionRequest, error) {
	if qc, ok := c.sub(baseURL).(allQuestionCaller); ok {
		return qc.GetAllPendingQuestionRequests(ctx, baseURL, directory, workspaceID)
	}
	return nil, nil
}

// ReplyQuestion 按 locator 委托给底层适配器。
func (c *CompositeAdapter) ReplyQuestion(ctx context.Context, baseURL, sessionID, requestID string, answers []adapter.QuestionAnswer) error {
	if qc, ok := c.sub(baseURL).(questionCaller); ok {
		return qc.ReplyQuestion(ctx, baseURL, sessionID, requestID, answers)
	}
	return disk.ErrNotSupported
}

// RejectQuestion 按 locator 委托给底层适配器。
func (c *CompositeAdapter) RejectQuestion(ctx context.Context, baseURL, sessionID, requestID string) error {
	if qc, ok := c.sub(baseURL).(questionCaller); ok {
		return qc.RejectQuestion(ctx, baseURL, sessionID, requestID)
	}
	return disk.ErrNotSupported
}
