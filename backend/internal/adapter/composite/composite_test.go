package composite

import (
	"context"
	"sync"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/adapter"
	"github.com/halfking/pocket-opencode/backend/internal/adapter/disk"
	"github.com/halfking/pocket-opencode/backend/internal/opencode"
)

// recordingAdapter 是 adapter.OpenCodeAdapter 的最小实现，把每次调用记录成
// "tag:method:locator"，用于验证复合适配器确实按 locator 把调用路由到了
// 正确的底层适配器。它也实现了审批/问答方法，使复合适配器的类型断言成立。
type recordingAdapter struct {
	tag   string
	mu    sync.Mutex
	calls []string
}

func newRecordingAdapter(tag string) *recordingAdapter {
	return &recordingAdapter{tag: tag}
}

func (r *recordingAdapter) log(method, locator string) {
	r.mu.Lock()
	r.calls = append(r.calls, r.tag+":"+method+":"+locator)
	r.mu.Unlock()
}

func (r *recordingAdapter) callsFor(tag string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, c := range r.calls {
		if len(c) >= len(tag)+1 && c[:len(tag)] == tag {
			out = append(out, c)
		}
	}
	return out
}

// ---- adapter.OpenCodeAdapter 基础方法 ----

func (r *recordingAdapter) ListSessions(ctx context.Context, u string) ([]adapter.OpenCodeSession, error) {
	r.log("ListSessions", u)
	return nil, nil
}
func (r *recordingAdapter) GetSessionSummary(ctx context.Context, u, id string) (string, error) {
	r.log("GetSessionSummary", u)
	return "", nil
}
func (r *recordingAdapter) ListRemoteTasks(ctx context.Context, u, status string, limit int) ([]adapter.RemoteTask, error) {
	r.log("ListRemoteTasks", u)
	return nil, nil
}
func (r *recordingAdapter) CreateSession(ctx context.Context, u string, p *adapter.CreateSessionRequest) (*adapter.OpenCodeSessionInfo, error) {
	r.log("CreateSession", u)
	return nil, nil
}
func (r *recordingAdapter) GetMessages(ctx context.Context, u, id string, limit int, order string) ([]adapter.OpenCodeMessage, error) {
	r.log("GetMessages", u)
	return nil, nil
}
func (r *recordingAdapter) SendPrompt(ctx context.Context, u, id string, p *adapter.SendPromptRequest) (*adapter.SendPromptResponse, error) {
	r.log("SendPrompt", u)
	return nil, nil
}
func (r *recordingAdapter) InterruptSession(ctx context.Context, u, id string) error {
	r.log("InterruptSession", u)
	return nil
}
func (r *recordingAdapter) DeleteSession(ctx context.Context, u, id string) error {
	r.log("DeleteSession", u)
	return nil
}
func (r *recordingAdapter) SubscribeEvents(ctx context.Context, u, dir, ws string) (<-chan adapter.OpenCodeEvent, func(), error) {
	r.log("SubscribeEvents", u)
	return nil, func() {}, nil
}
func (r *recordingAdapter) HealthCheck(ctx context.Context, u string) error {
	r.log("HealthCheck", u)
	return nil
}

// ---- 可选能力：审批 / 问答 ----

func (r *recordingAdapter) GetPermissionRequests(ctx context.Context, u, id string) ([]adapter.PermissionRequest, error) {
	r.log("GetPermissionRequests", u)
	return nil, nil
}
func (r *recordingAdapter) GetAllPendingPermissionRequests(ctx context.Context, u, dir, ws string) ([]adapter.PermissionRequest, error) {
	r.log("GetAllPendingPermissionRequests", u)
	return nil, nil
}
func (r *recordingAdapter) ReplyPermission(ctx context.Context, u, id, rid string, reply adapter.PermissionReply, msg string) error {
	r.log("ReplyPermission", u)
	return nil
}
func (r *recordingAdapter) GetQuestionRequests(ctx context.Context, u, id string) ([]adapter.QuestionRequest, error) {
	r.log("GetQuestionRequests", u)
	return nil, nil
}
func (r *recordingAdapter) GetAllPendingQuestionRequests(ctx context.Context, u, dir, ws string) ([]adapter.QuestionRequest, error) {
	r.log("GetAllPendingQuestionRequests", u)
	return nil, nil
}
func (r *recordingAdapter) ReplyQuestion(ctx context.Context, u, id, rid string, answers []adapter.QuestionAnswer) error {
	r.log("ReplyQuestion", u)
	return nil
}
func (r *recordingAdapter) RejectQuestion(ctx context.Context, u, id, rid string) error {
	r.log("RejectQuestion", u)
	return nil
}

func TestComposite_ImplementsInterfaces(t *testing.T) {
	c := New(newRecordingAdapter("http"), newRecordingAdapter("disk"))
	var _ adapter.OpenCodeAdapter = c
	// 审批/问答管理器依赖的运行时类型断言必须成立。
	if _, ok := interface{}(c).(opencode.PermissionCaller); !ok {
		t.Fatal("composite must satisfy opencode.PermissionCaller")
	}
	if _, ok := interface{}(c).(opencode.QuestionCaller); !ok {
		t.Fatal("composite must satisfy opencode.QuestionCaller")
	}
}

func TestComposite_RoutesByLocator(t *testing.T) {
	httpA := newRecordingAdapter("http")
	diskA := newRecordingAdapter("disk")
	c := New(httpA, diskA)
	ctx := context.Background()

	// disk locator → disk 适配器
	if _, err := c.ListSessions(ctx, disk.LocatorClaude); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListRemoteTasks(ctx, disk.LocatorCodex, "", 10); err != nil {
		t.Fatal(err)
	}
	if err := c.HealthCheck(ctx, disk.LocatorClaude); err != nil {
		t.Fatal(err)
	}
	// 审批/问答也按 locator 路由到 disk
	if _, err := c.GetPermissionRequests(ctx, disk.LocatorClaude, "s1"); err != nil {
		t.Fatal(err)
	}
	if err := c.ReplyPermission(ctx, disk.LocatorCodex, "s1", "r1", adapter.PermissionReplyAlways, "ok"); err != nil {
		t.Fatal(err)
	}
	if err := c.ReplyQuestion(ctx, disk.LocatorClaude, "s1", "r1", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetAllPendingQuestionRequests(ctx, disk.LocatorCodex, "", ""); err != nil {
		t.Fatal(err)
	}

	if got := diskA.callsFor("disk"); len(got) == 0 {
		t.Fatalf("expected disk adapter to receive calls, got %v", httpA.callsFor("http"))
	}
	if got := httpA.callsFor("http"); len(got) != 0 {
		t.Fatalf("http adapter must NOT be called for disk locators, got %v", got)
	}

	// 非 disk locator（OpenCode 实例 URL）→ HTTP 适配器
	if _, err := c.ListSessions(ctx, "http://host:14096/opencode"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetQuestionRequests(ctx, "http://host:14096/opencode", "s9"); err != nil {
		t.Fatal(err)
	}
	if got := httpA.callsFor("http"); len(got) == 0 {
		t.Fatal("expected http adapter to receive calls for non-disk locator")
	}
	if got := diskA.callsFor("disk"); len(got) != 7 { // 前面 7 个 disk 调用
		t.Fatalf("disk adapter call count changed unexpectedly: %v", got)
	}
}
