package server

// §938 判据：服务端「精校 → 待办落库」这条链。
//
// ⚠ 这条链修复前同时踩了六个坑（简报第 162 条）：
//
//	D1 不带 workspace ⇒ 落进遗留单租户桶 "default" ⇒ 用户看不见；
//	D2 Status="pending" 不在任务状态契约里 ⇒ 前端三个分桶都不收 ⇒ 列表不显示；
//	D3 不带 owner、不种参与者 ⇒ CanReadWorkItem 对创建者本人为 false ⇒ 点不开；
//	D4 批内不去重 ⇒ 同一件事建两条；
//	D5 没有跨次幂等 ⇒ 同一场会议再精校一次就再插一遍；
//	D6 不写溯源列 ⇒ from-source 链的去重看不见它 ⇒ 手动转一次再多一整套。
//
// ⚠ 判据量的是**落库之后的行**与**查重之后的结果**，不是源码文本 ——
//   「源码里出现了 workspace_id」这种形状守卫证明不了「真的落进了那个租户」。
//
// ⚠ 需要真 PG：task.Store 只有 PostgreSQL 一个实现 ⇒ 没有内存版可测
//   ⇒ ⇒ 租户落点 / 溯源列 / 查重这三件事**没有别的实现能测**。
//   没有 PG 时这些用例会 skip（退出码仍是 0）⇒ ⇒ 变异批次用
//   forbid_skip=True 把「被 skip」变成失败（见 scripts/mutations/s938.py）。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/learning/sources"
	"github.com/halfking/pocket-opencode/backend/internal/meeting"
	"github.com/halfking/pocket-opencode/backend/internal/task"
)

const (
	s938Workspace = "ws-s938"
	s938Owner     = "user-s938"
)

// newS938Server 造一个带真 PG 任务库的 Server。
func newS938Server(t *testing.T) (*Server, *task.Store) {
	t.Helper()
	pool := mustTestPool(t)
	store, err := task.NewStore(pool)
	if err != nil {
		t.Fatalf("task.NewStore: %v", err)
	}
	srv, _ := newTestServerWithAuth(t)
	srv.taskStore = store
	return srv, store
}

// ── 纯函数层（不需要 PG）────────────────────────────────────────────

// TestS938MeetingTodoDedupeKeyTrims 判定键要忽略首尾空白。
//
// ⚠ 与前端 dedupeTodos 同形（`i.text.trim()`）⇒ 两侧判据要一起改。
func TestS938MeetingTodoDedupeKeyTrims(t *testing.T) {
	if got := meetingTodoDedupeKey("  准备对比表  "); got != "准备对比表" {
		t.Errorf("meetingTodoDedupeKey=%q, want %q", got, "准备对比表")
	}
	if got := meetingTodoDedupeKey("   "); got != "" {
		t.Errorf("空白键=%q, want 空串", got)
	}
}

// TestS938DedupeTodoItemsKeepsFirstOccurrence 批内去重**保留第一条**。
//
// ⚠ 为什么是第一条：提取顺序是 todos → action_items → structured_minutes，
//
//	而前端只读 refine.todos ⇒ 第一条才是与前端同源、due 最完整的那份
//	⇒ ⇒ 保留后面那条会丢掉 due。
func TestS938DedupeTodoItemsKeepsFirstOccurrence(t *testing.T) {
	in := []todoItem{
		{Text: "准备对比表", Due: "下周三下午三点"},
		{Text: "准备对比表", Due: ""},
		{Text: "  准备对比表  "},
		{Text: "确认预算"},
	}
	out := dedupeTodoItems(in)
	if len(out) != 2 {
		t.Fatalf("dedupeTodoItems 长度=%d, want 2（批内去重没修掉）: %#v", len(out), out)
	}
	if out[0].Text != "准备对比表" || out[0].Due != "下周三下午三点" {
		t.Errorf("第一条=%#v, want 保留 todos 那条（含 due）", out[0])
	}
	if out[1].Text != "确认预算" {
		t.Errorf("第二条=%#v, want 确认预算", out[1])
	}
}

// TestS938DedupeTodoItemsDropsBlankText 空白待办不进库。
func TestS938DedupeTodoItemsDropsBlankText(t *testing.T) {
	out := dedupeTodoItems([]todoItem{{Text: ""}, {Text: "   "}, {Text: "确认预算"}})
	if len(out) != 1 || out[0].Text != "确认预算" {
		t.Errorf("dedupeTodoItems=%#v, want 只剩「确认预算」", out)
	}
}

// TestS938ExtractTodosCollectsAllThreeSources 提取阶段仍从**三处**收。
//
// ⚠ 这条是**口径守卫**：收集阶段**不**去重（去重发生在建之前，与前端口径同形）
//
//	⇒ ⇒ 要是有人把去重挪进 extractTodos，这条会红（那是口径漂移，不是修复）。
func TestS938ExtractTodosCollectsAllThreeSources(t *testing.T) {
	result := map[string]any{
		"todos": []any{
			map[string]any{"text": "准备对比表", "due": "下周三"},
		},
		"action_items": []any{
			map[string]any{"text": "确认预算"},
		},
		"structured_minutes": map[string]any{
			"action_items": []any{
				map[string]any{"text": "准备对比表"},
			},
		},
	}
	got := extractTodos(result)
	if len(got) != 3 {
		t.Fatalf("extractTodos 长度=%d, want 3（提取阶段不该去重）: %#v", len(got), got)
	}
	if got[0].Text != "准备对比表" || got[0].Due != "下周三" {
		t.Errorf("第一条=%#v, want todos 那条（含 due）", got[0])
	}
}

// ── 落库层（需要真 PG）──────────────────────────────────────────────

// TestS938IngestMeetingTasksWritesTenantOwnerAndOrigin 落点四件套。
//
// ⚠ D1：workspace 必须是**调用方的真实租户**，不是遗留单租户桶 "default"。
// ⚠ D2：status 必须是 "active"（前端三个分桶只认 active/blocked/completed）。
// ⚠ D3：owner 必须是创建者（CanReadWorkItem 依赖它）。
// ⚠ D6：溯源列必须写（跨次去重与两条路径互见都靠它）。
func TestS938IngestMeetingTasksWritesTenantOwnerAndOrigin(t *testing.T) {
	srv, store := newS938Server(t)
	ctx := context.Background()
	target := meetingTodoTarget{
		WorkspaceID: s938Workspace,
		OwnerID:     s938Owner,
		MeetingID:   "mtg-s938-tenant",
	}
	if created := srv.ingestMeetingTasks(ctx, target, []todoItem{{Text: "准备对比表"}}); created != 1 {
		t.Fatalf("created=%d, want 1", created)
	}

	rows, err := store.ListTasksScoped(ctx, s938Workspace)
	if err != nil {
		t.Fatalf("ListTasksScoped: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("真实租户 %q 下的待办=%d 条, want 1（D1：落错桶 ⇒ 用户看不见）: %#v",
			s938Workspace, len(rows), rows)
	}
	got := rows[0]
	if got.WorkspaceID != s938Workspace {
		t.Errorf("workspace_id=%q, want %q", got.WorkspaceID, s938Workspace)
	}
	if got.OwnerID != s938Owner {
		t.Errorf("owner_id=%q, want %q（D3：没有归属 ⇒ 点不开）", got.OwnerID, s938Owner)
	}
	if got.OriginKind != task.OriginMeeting {
		t.Errorf("origin_kind=%q, want %q（D6：没有溯源 ⇒ 两条路径互相看不见）",
			got.OriginKind, task.OriginMeeting)
	}
	if got.OriginRef != target.MeetingID {
		t.Errorf("origin_ref=%q, want %q", got.OriginRef, target.MeetingID)
	}
	if got.Status != "active" {
		t.Errorf("status=%q, want \"active\"（D2：'pending' 不在任何分桶里 ⇒ 列表不显示）", got.Status)
	}

	// D1 的另一半：不能有东西漏进遗留单租户桶（那是跨租户可见）。
	leaked, err := store.ListTasksScoped(ctx, task.DefaultWorkspaceID)
	if err != nil {
		t.Fatalf("ListTasksScoped(default): %v", err)
	}
	if len(leaked) != 0 {
		t.Errorf("default 桶里落了 %d 条会议待办（D1：租户落点错了 ⇒ 跨租户可见）: %#v", len(leaked), leaked)
	}
}

// TestS938IngestMeetingTasksSeedsOwnerParticipant 创建者要能读自己的待办。
//
// ⚠ D3 的**行为**判据：owner_id 落库还不够 —— visibility 默认 private
//
//	且没有参与者时，CanReadWorkItem 对创建者本人返回 false
//	⇒ ⇒ 详情 / 协作 / 层级这些入口点不开。
func TestS938IngestMeetingTasksSeedsOwnerParticipant(t *testing.T) {
	srv, store := newS938Server(t)
	ctx := context.Background()
	target := meetingTodoTarget{
		WorkspaceID: s938Workspace,
		OwnerID:     s938Owner,
		MeetingID:   "mtg-s938-part",
	}
	if created := srv.ingestMeetingTasks(ctx, target, []todoItem{{Text: "确认预算"}}); created != 1 {
		t.Fatalf("created=%d, want 1", created)
	}
	rows, err := store.ListTasksScoped(ctx, s938Workspace)
	if err != nil || len(rows) != 1 {
		t.Fatalf("ListTasksScoped: err=%v rows=%d", err, len(rows))
	}
	got := rows[0]
	parts, err := store.ListParticipants(ctx, got.ID, s938Workspace)
	if err != nil {
		t.Fatalf("ListParticipants: %v", err)
	}
	found := false
	for _, p := range parts {
		if p.UserID == s938Owner {
			found = true
		}
	}
	if !found {
		t.Errorf("参与者=%#v, want 含创建者 %q（D3：没人能读这条待办）", parts, s938Owner)
	}
	if !task.CanReadWorkItem(&got, parts, s938Owner) {
		t.Errorf("CanReadWorkItem(创建者)=false（D3：创建者读不到自己建的待办）")
	}
}

// TestS938IngestMeetingTasksIsIdempotentAcrossCalls 跨次幂等。
//
// ⚠ 「录一段 → 停 → 再录一段」是正常用法 ⇒ 同一场会议会精校多次
//
//	⇒ 第二次必须**一条都不建**。
func TestS938IngestMeetingTasksIsIdempotentAcrossCalls(t *testing.T) {
	srv, store := newS938Server(t)
	ctx := context.Background()
	target := meetingTodoTarget{
		WorkspaceID: s938Workspace,
		OwnerID:     s938Owner,
		MeetingID:   "mtg-s938-idem",
	}
	first := srv.ingestMeetingTasks(ctx, target, []todoItem{{Text: "确认预算"}})
	second := srv.ingestMeetingTasks(ctx, target, []todoItem{{Text: "确认预算"}})
	if first != 1 {
		t.Fatalf("第一次 created=%d, want 1", first)
	}
	if second != 0 {
		t.Errorf("第二次 created=%d, want 0（跨次幂等没修掉 ⇒ 同一件事插两遍）", second)
	}
	rows, err := store.ListTasksScoped(ctx, s938Workspace)
	if err != nil {
		t.Fatalf("ListTasksScoped: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("库里 %d 条, want 1（跨次重复没挡）", len(rows))
	}
}

// TestS938IngestMeetingTasksSkipsDuplicateWithinBatch 批内去重。
//
// ⚠ D4 的行为判据：精校提示词**明确要求**模型同时填 todos 与
//
//	structured_minutes.action_items ⇒ 同一件事一定会被收两遍
//	⇒ ⇒ 建库时必须只建一条。
func TestS938IngestMeetingTasksSkipsDuplicateWithinBatch(t *testing.T) {
	srv, store := newS938Server(t)
	ctx := context.Background()
	target := meetingTodoTarget{
		WorkspaceID: s938Workspace,
		OwnerID:     s938Owner,
		MeetingID:   "mtg-s938-batch",
	}
	created := srv.ingestMeetingTasks(ctx, target, []todoItem{
		{Text: "准备对比表", Due: "下周三"},
		{Text: "准备对比表"},
		{Text: "  准备对比表  "},
		{Text: "确认预算"},
	})
	if created != 2 {
		t.Errorf("created=%d, want 2（批内重复没挡 ⇒ 同一件事建两条）", created)
	}
	rows, err := store.ListTasksScoped(ctx, s938Workspace)
	if err != nil {
		t.Fatalf("ListTasksScoped: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("库里 %d 条, want 2: %#v", len(rows), rows)
	}
}

// TestS938IngestMeetingTasksKeepsSameTitleAcrossMeetings 跨会议**不得**去重。
//
// ⚠ 这是**负控**：去重的键是「(会议, 标题)」⇒ 两场不同的会议里
//
//	同名的一条待办是**两件事** ⇒ 误去重会把用户的待办吃掉。
func TestS938IngestMeetingTasksKeepsSameTitleAcrossMeetings(t *testing.T) {
	srv, store := newS938Server(t)
	ctx := context.Background()
	a := meetingTodoTarget{WorkspaceID: s938Workspace, OwnerID: s938Owner, MeetingID: "mtg-s938-a"}
	b := meetingTodoTarget{WorkspaceID: s938Workspace, OwnerID: s938Owner, MeetingID: "mtg-s938-b"}
	if created := srv.ingestMeetingTasks(ctx, a, []todoItem{{Text: "准备对比表"}}); created != 1 {
		t.Fatalf("会议 A created=%d, want 1", created)
	}
	if created := srv.ingestMeetingTasks(ctx, b, []todoItem{{Text: "准备对比表"}}); created != 1 {
		t.Errorf("会议 B created=%d, want 1（跨会议被误去重 ⇒ 用户的待办被吃掉）", created)
	}
	rows, err := store.ListTasksScoped(ctx, s938Workspace)
	if err != nil {
		t.Fatalf("ListTasksScoped: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("库里 %d 条, want 2（两场会议各一条）", len(rows))
	}
}

// TestS938FromSourceSkipsTasksCreatedByRefine 两条落库路径**必须互见**。
//
// ⚠ D6 的端到端判据：from-source 链（POST /api/tasks/from-source）本来就有
//
//	按 (origin_kind, origin_ref, title) 的幂等，但它键上的溯源列
//	**精校链一条都不写** ⇒ ⇒ 它看不见精校链建的 task
//	⇒ ⇒ 用户手动转一次「待办」，就再得一整套重复的。
func TestS938FromSourceSkipsTasksCreatedByRefine(t *testing.T) {
	srv, store := newS938Server(t)
	srv.SetLearningSources(sources.New(nil, nil, nil, srv.meetingStore))
	ctx := context.Background()
	const meetingID = "mtg-s938-fs"

	m, err := srv.meetingStore.CreateScoped(meeting.CreateMeetingRequest{
		ID:    meetingID,
		Title: "周会",
	}, s938Owner, s938Workspace)
	if err != nil {
		t.Fatalf("CreateScoped: %v", err)
	}
	m.ActionItems = []meeting.ActionItem{{Task: "准备对比表"}}
	if err := srv.meetingStore.UpdateScoped(m, s938Owner, s938Workspace); err != nil {
		t.Fatalf("UpdateScoped: %v", err)
	}

	created := srv.ingestMeetingTasks(ctx, meetingTodoTarget{
		WorkspaceID: s938Workspace,
		OwnerID:     s938Owner,
		MeetingID:   meetingID,
	}, []todoItem{{Text: "准备对比表"}})
	if created != 1 {
		t.Fatalf("精校链 created=%d, want 1", created)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/tasks/from-source", strings.NewReader("{}"))
	rr := httptest.NewRecorder()
	srv.createTasksFromMeeting(rr, req, s938Workspace, s938Owner, meetingID, task.TypeOther)

	var resp struct {
		Tasks   []task.Task `json:"tasks"`
		Skipped int         `json:"skipped"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析 from-source 响应失败: %v (body=%s)", err, rr.Body.String())
	}
	if len(resp.Tasks) != 0 {
		t.Errorf("from-source 又建了 %d 条（D6：它看不见精校链建的 ⇒ 手动转一次多一整套）: %#v",
			len(resp.Tasks), resp.Tasks)
	}
	if resp.Skipped != 1 {
		t.Errorf("skipped=%d, want 1", resp.Skipped)
	}
	rows, err := store.ListTasksScoped(ctx, s938Workspace)
	if err != nil {
		t.Fatalf("ListTasksScoped: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("库里 %d 条, want 1（两条路径建出了两套）", len(rows))
	}
}

// TestS938ListTasksByOriginIsWorkspaceScoped 查重入口必须按租户隔离。
//
// ⚠ 查重键若漏了 workspace ⇒ 一场会议的待办会把**别的租户**的同名待办挡掉
//
//	⇒ ⇒ 用户的待办被别人的会议吃掉。
func TestS938ListTasksByOriginIsWorkspaceScoped(t *testing.T) {
	srv, store := newS938Server(t)
	ctx := context.Background()
	target := meetingTodoTarget{
		WorkspaceID: s938Workspace,
		OwnerID:     s938Owner,
		MeetingID:   "mtg-s938-scope",
	}
	if created := srv.ingestMeetingTasks(ctx, target, []todoItem{{Text: "确认预算"}}); created != 1 {
		t.Fatalf("created=%d, want 1", created)
	}
	rows, err := store.ListTasksByOrigin(ctx, s938Workspace, task.OriginMeeting, target.MeetingID)
	if err != nil {
		t.Fatalf("ListTasksByOrigin: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("本租户查到 %d 条, want 1", len(rows))
	}
	other, err := store.ListTasksByOrigin(ctx, "ws-s938-other", task.OriginMeeting, target.MeetingID)
	if err != nil {
		t.Fatalf("ListTasksByOrigin(other): %v", err)
	}
	if len(other) != 0 {
		t.Errorf("别的租户查到 %d 条, want 0（查重键漏了 workspace ⇒ 跨租户互相挡）", len(other))
	}
	// 溯源不匹配 ⇒ 查不到（别把它退化成全表扫）。
	none, err := store.ListTasksByOrigin(ctx, s938Workspace, task.OriginMeeting, "mtg-s938-other")
	if err != nil {
		t.Fatalf("ListTasksByOrigin(other meeting): %v", err)
	}
	if len(none) != 0 {
		t.Errorf("别的会议查到 %d 条, want 0", len(none))
	}
}
