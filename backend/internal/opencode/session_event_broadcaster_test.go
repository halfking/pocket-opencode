package opencode

// session_event_broadcaster_test.go — P1 契约测试（Go 侧锁定）。
//
// 冻结契约：docs/2026-08-27-p1-contracts-frozen.md §2/§3。三类事件广播出的
// 完整 wire JSON（外层 {type,payload}，payload=WsEnvelopeV1 全字段，data=业务
// payload 全字段）必须与 TS 侧 fixture（frontend/src/services/__tests__/
// sessionEvents.test.mjs）中的样本逐字段一致 —— 两侧样本共用同一组字面量，
// 任何一侧改字段名都会被另一方打红。固定时钟 1750000000000ms 保证 event_id /
// ts / *_at 全部确定可比。

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/adapter"
	"github.com/halfking/pocket-opencode/backend/internal/model"
	"github.com/halfking/pocket-opencode/backend/internal/registry"
)

// fixture 基准时钟：1750000000000ms（UnixNano=1750000000000000000）。
const fixtureBaseMs = int64(1750000000000)

// ---- 双向锁定样本（与 TS fixture sessionEvents.test.mjs 逐字节同义）----

const fixtureActivityWire = `{"type":"session.activity","payload":{"v":1,"id":"session_activity_1750000000000000000_2","ts":1750000000000,"channel":"sessions","topic":"ses_fx_1","type":"session.activity","data":{"instance_id":"inst_fx","session_id":"ses_fx_1","phase":"file_write","last_event_at":1750000000000,"round_index":1}}}`

const fixtureTaskHealthWire = `{"type":"task.health","payload":{"v":1,"id":"task_health_1750000000000000000_3","ts":1750000000000,"channel":"tasks","topic":"task_fx_1","type":"task.health","data":{"task_id":"task_fx_1","instance_id":"inst_fx","health":"running","pending_count":0,"computed_at":1750000000000}}}`

const fixtureRoundCompletedWire = `{"type":"round.completed","payload":{"v":1,"id":"round_completed_1750000000000000000_4","ts":1750000000000,"channel":"sessions","topic":"ses_fx_1","type":"round.completed","data":{"instance_id":"inst_fx","session_id":"ses_fx_1","round_index":1,"summary":"已完成登录页修复并补齐回归测试","changes":{"added":12,"removed":3,"files":2},"status":"completed","completed_at":1750000000000},"cause":{"correlation_id":"ses_fx_1:1"}}}`

// 同一管道的首尾两条 session.activity（thinking 起步 / idle 收尾）。
const fixtureActivityThinkingWire = `{"type":"session.activity","payload":{"v":1,"id":"session_activity_1750000000000000000_1","ts":1750000000000,"channel":"sessions","topic":"ses_fx_1","type":"session.activity","data":{"instance_id":"inst_fx","session_id":"ses_fx_1","phase":"thinking","last_event_at":1750000000000,"round_index":1}}}`

const fixtureActivityIdleWire = `{"type":"session.activity","payload":{"v":1,"id":"session_activity_1750000000000000000_5","ts":1750000000000,"channel":"sessions","topic":"ses_fx_1","type":"session.activity","data":{"instance_id":"inst_fx","session_id":"ses_fx_1","phase":"idle","last_event_at":1750000000000,"round_index":1}}}`

// staticTaskIndex 是测试用任务→会话映射。
type staticTaskIndex struct{ links []TaskSessionLink }

func (s staticTaskIndex) SessionLinks(context.Context) ([]TaskSessionLink, error) {
	return s.links, nil
}

// newSessionEventBroadcasterFixture 装配 registry + 管理器 + recording hub。
// 时钟固定在 fixtureBaseMs；roundIdleDebounce 拉长到 1h（本组测试走
// session.idle 的确定性收尾路径，防抖路径单独测）。
func newSessionEventBroadcasterFixture(t *testing.T, instances ...*model.PocketInstance) (*SessionEventBroadcaster, *recordingApprovalHub, *PermissionManager, *QuestionManager, func()) {
	t.Helper()

	reg := registry.NewRegistry()
	for _, inst := range instances {
		if err := reg.RegisterInstance(inst); err != nil {
			t.Fatalf("register instance %s: %v", inst.ID, err)
		}
		reg.SetInstanceAPIBase(inst.ID, "http://"+inst.ID+".fake")
	}

	ad := newFakePermissionAdapter()
	permMgr := NewPermissionManager(reg, ad, PermissionManagerOptions{PollInterval: time.Hour}, nil)
	quesMgr := NewQuestionManager(reg, ad, QuestionManagerOptions{PollInterval: time.Hour}, nil)

	hub := &recordingApprovalHub{}
	b := NewSessionEventBroadcaster(reg, nil, permMgr, quesMgr)
	b.SetBroadcaster(hub)
	fixed := time.UnixMilli(fixtureBaseMs)
	b.now = func() time.Time { return fixed }
	b.roundIdleDebounce = time.Hour

	cleanup := func() {
		permMgr.Close()
		quesMgr.Close()
	}
	return b, hub, permMgr, quesMgr, cleanup
}

func domainEvt(instanceID, sessionID, typ string, data map[string]any) DomainEvent {
	return DomainEvent{
		InstanceID: instanceID,
		SessionID:  sessionID,
		Type:       typ,
		Raw:        adapter.OpenCodeEvent{ID: "evt-" + typ, Type: typ, Data: data},
		ReceivedAt: time.Now(),
	}
}

// assertWireJSONEqual 把一条广播重编为 {type,payload} wire JSON 并与样本深比较。
func assertWireJSONEqual(t *testing.T, idx int, wantJSON, gotType string, gotPayload WsEnvelopeV1) {
	t.Helper()
	var want map[string]any
	if err := json.Unmarshal([]byte(wantJSON), &want); err != nil {
		t.Fatalf("sample JSON invalid: %v", err)
	}
	raw, err := json.Marshal(map[string]any{"type": gotType, "payload": gotPayload})
	if err != nil {
		t.Fatalf("marshal broadcast: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("re-parse broadcast: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("broadcast #%d wire JSON mismatch:\n got: %s\nwant: %s", idx+1, raw, wantJSON)
	}
}

// TestSessionEventBroadcaster_WireContractFixtures 是 §2 的双向锁定主测试：
// 一条完整管道（prompt → edit 工具 → 助手消息（正文 + 2 个 edit diff）→
// task.health tick → session.idle）产出 5 条广播，其中三条（file_write
// activity / task.health / round.completed）与 TS fixture 的三个样本逐字段一致。
func TestSessionEventBroadcaster_WireContractFixtures(t *testing.T) {
	b, hub, _, _, cleanup := newSessionEventBroadcasterFixture(t, workspaceInstance("inst_fx", "ws_fx"))
	defer cleanup()
	b.SetTaskSessionIndex(staticTaskIndex{links: []TaskSessionLink{
		{TaskID: "task_fx_1", WorkspaceID: "ws_fx", InstanceID: "inst_fx", SessionID: "ses_fx_1"},
	}})

	// 1) 用户 prompt → round 1 开始，phase=thinking → activity#1。
	b.Ingest(domainEvt("inst_fx", "ses_fx_1", "session.next.prompted", map[string]any{}))
	b.flush()

	// 2) edit 工具调用 → phase=file_write（phase 切换）→ activity#2（样本一）。
	b.Ingest(domainEvt("inst_fx", "ses_fx_1", "session.next.tool.called", map[string]any{"tool": "edit"}))
	b.flush()

	// 3) 助手消息完成：正文 + 两个 edit 工具 parts（diff 统计 12+/3-/2 files）。
	diff1 := "--- a/src/login.vue\n+++ b/src/login.vue\n@@ -10,7 +10,15 @@\n ctx\n+l1\n+l2\n+l3\n+l4\n+l5\n+l6\n+l7\n+l8\n-o1\n-o2\n-o3\n"
	diff2 := "--- a/src/login.test.ts\n+++ b/src/login.test.ts\n@@ -1,3 +1,7 @@\n ctx\n+t1\n+t2\n+t3\n+t4\n"
	msgData := map[string]any{
		"info": map[string]any{
			"id": "msg_a1", "role": "assistant", "sessionID": "ses_fx_1",
			"time": map[string]any{"created": float64(fixtureBaseMs), "completed": float64(fixtureBaseMs)},
		},
		"parts": []any{
			map[string]any{"type": "text", "text": "已完成登录页修复并补齐回归测试"},
			map[string]any{"type": "tool", "tool": "edit", "state": map[string]any{
				"status": "completed",
				"input":  map[string]any{"filePath": "src/login.vue"},
				"output": diff1,
			}},
			map[string]any{"type": "tool", "tool": "edit", "state": map[string]any{
				"status": "completed",
				"input":  map[string]any{"filePath": "src/login.test.ts"},
				"output": diff2,
			}},
		},
	}
	b.Ingest(domainEvt("inst_fx", "ses_fx_1", "message.updated", msgData))
	b.flush()

	// 4) task.health tick：round 进行中 → running → task.health#3（样本二）。
	b.taskHealthTick(context.Background())

	// 5) session.idle → round.completed#4（样本三）+ activity idle#5。
	b.Ingest(domainEvt("inst_fx", "ses_fx_1", "session.idle", map[string]any{}))
	b.flush()

	events := hub.events()
	if len(events) != 5 {
		t.Fatalf("expected 5 broadcasts, got %d: %+v", len(events), events)
	}
	assertWireJSONEqual(t, 0, fixtureActivityThinkingWire, events[0].msgType, events[0].envelope)
	assertWireJSONEqual(t, 1, fixtureActivityWire, events[1].msgType, events[1].envelope)
	assertWireJSONEqual(t, 2, fixtureTaskHealthWire, events[2].msgType, events[2].envelope)
	assertWireJSONEqual(t, 3, fixtureRoundCompletedWire, events[3].msgType, events[3].envelope)
	assertWireJSONEqual(t, 4, fixtureActivityIdleWire, events[4].msgType, events[4].envelope)

	// workspace 定向：所有广播都进 ws_fx。
	for i, evt := range events {
		if evt.workspaceID != "ws_fx" {
			t.Errorf("broadcast #%d workspace = %q, want ws_fx", i, evt.workspaceID)
		}
	}

	// 变更检测：session.idle 已把健康度从 running 变为 idle，第二次 tick 恰好
	// 补发一条 task.health(idle)；再 tick（状态未变）则静默。
	n := len(hub.events())
	b.taskHealthTick(context.Background())
	events = hub.events()
	if len(events) != n+1 || events[len(events)-1].msgType != SessionEventTaskHealth {
		t.Fatalf("changed task health: %d -> %d, want exactly one more task.health", n, len(events))
	}
	hdata, ok := events[len(events)-1].envelope.Data.(*TaskHealthData)
	if !ok || hdata.Health != TaskHealthIdle {
		t.Errorf("post-idle task health = %+v, want idle", events[len(events)-1].envelope.Data)
	}
	n = len(hub.events())
	b.taskHealthTick(context.Background())
	if got := len(hub.events()); got != n {
		t.Errorf("unchanged task health re-broadcast: %d -> %d", n, got)
	}
}

// TestSessionEventBroadcaster_ActivityThrottle 验证 ≥30s 或 phase 切换才发。
func TestSessionEventBroadcaster_ActivityThrottle(t *testing.T) {
	b, hub, _, _, cleanup := newSessionEventBroadcasterFixture(t, workspaceInstance("inst-a", "ws-a"))
	defer cleanup()

	now := time.UnixMilli(fixtureBaseMs)
	b.now = func() time.Time { return now }

	step := func(typ string, data map[string]any) { b.Ingest(domainEvt("inst-a", "ses-1", typ, data)) }

	// prompt → thinking（首发）。
	step("session.next.prompted", nil)
	b.flush()
	if n := len(hub.events()); n != 1 {
		t.Fatalf("after prompt: %d broadcasts, want 1", n)
	}
	// 同 phase 的事件（reasoning.delta）在 30s 内 → 静默。
	step("session.next.reasoning.delta", nil)
	b.flush()
	if n := len(hub.events()); n != 1 {
		t.Fatalf("same-phase within 30s: %d broadcasts, want 1", n)
	}
	// phase 切换（bash 工具）→ 立即发。
	step("session.next.tool.called", map[string]any{"tool": "bash"})
	b.flush()
	if n := len(hub.events()); n != 2 {
		t.Fatalf("phase switch: %d broadcasts, want 2", n)
	}
	// 同 phase（pty）30s 内 → 静默。
	step("session.next.shell.started", nil)
	b.flush()
	if n := len(hub.events()); n != 2 {
		t.Fatalf("pty same-phase within 30s: %d broadcasts, want 2", n)
	}
	// 推进 31s 后同 phase → 补发。
	now = now.Add(31 * time.Second)
	step("session.next.shell.started", nil)
	b.flush()
	events := hub.events()
	if len(events) != 3 {
		t.Fatalf("after 31s: %d broadcasts, want 3", len(events))
	}
	last := events[len(events)-1]
	data, ok := last.envelope.Data.(*SessionActivityData)
	if !ok {
		t.Fatalf("data type = %T", last.envelope.Data)
	}
	if data.Phase != SessionPhasePty || data.LastEventAt != now.UnixMilli() {
		t.Errorf("aged activity = %+v, want phase=pty last_event_at=%d", data, now.UnixMilli())
	}
}

// TestSessionEventBroadcaster_Coalescing 验证 500ms 发送侧合并：同 type+topic
// 窗口内保留最新，不同 type 不互相吞。
func TestSessionEventBroadcaster_Coalescing(t *testing.T) {
	b, hub, _, _, cleanup := newSessionEventBroadcasterFixture(t, workspaceInstance("inst-a", "ws-a"))
	defer cleanup()

	now := time.UnixMilli(fixtureBaseMs)
	b.now = func() time.Time { return now }

	// 两条同 phase 的 activity 都入队（第二条距首条 >30s），一次 flush 只剩最新。
	b.Ingest(domainEvt("inst-a", "ses-1", "session.next.prompted", nil)) // thinking 入队
	now = now.Add(31 * time.Second)
	b.Ingest(domainEvt("inst-a", "ses-1", "session.next.reasoning.delta", nil)) // thinking 再入队
	// 再加一条 round.completed（不同 type，不得被合并吞掉）。
	b.mu.Lock()
	st := b.sessions["inst-a:ses-1"]
	st.roundActive = true
	b.closeRoundLocked(st, RoundStatusCompleted, now)
	b.mu.Unlock()
	b.flush()

	events := hub.events()
	if len(events) != 2 {
		t.Fatalf("coalesced flush: %d broadcasts, want 2 (latest activity + round.completed), got %+v", len(events), events)
	}
	byType := map[string]WsEnvelopeV1{}
	for _, evt := range events {
		byType[evt.msgType] = evt.envelope
	}
	activity, ok := byType[SessionEventActivity].Data.(*SessionActivityData)
	if !ok {
		t.Fatalf("activity data type = %T", byType[SessionEventActivity].Data)
	}
	if activity.LastEventAt != now.UnixMilli() {
		t.Errorf("coalesced activity last_event_at = %d, want %d (latest)", activity.LastEventAt, now.UnixMilli())
	}
	if _, ok := byType[SessionEventRoundCompleted]; !ok {
		t.Error("round.completed was swallowed by activity coalescing")
	}
}

// TestSessionEventBroadcaster_RoundDebounceAndStatuses 覆盖轮次收尾的防抖路径
// 与三种 status（completed / error / cancelled）。
func TestSessionEventBroadcaster_RoundDebounceAndStatuses(t *testing.T) {
	b, hub, _, _, cleanup := newSessionEventBroadcasterFixture(t, workspaceInstance("inst-a", "ws-a"))
	defer cleanup()
	b.roundIdleDebounce = 20 * time.Millisecond

	waitForRounds := func(n int) []WsEnvelopeV1 {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			b.flush() // 定时器回调只入队；由轮询侧触发 coalescing flush
			var rounds []WsEnvelopeV1
			for _, evt := range hub.events() {
				if evt.msgType == SessionEventRoundCompleted {
					rounds = append(rounds, evt.envelope)
				}
			}
			if len(rounds) >= n {
				return rounds
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("round.completed count never reached %d", n)
		return nil
	}

	// 防抖收尾：message.completed + step.ended 后 20ms 无后续 → completed。
	b.Ingest(domainEvt("inst-a", "ses-1", "session.next.prompted", nil))
	b.Ingest(domainEvt("inst-a", "ses-1", "message.updated", map[string]any{
		"info":  map[string]any{"role": "assistant", "time": map[string]any{"completed": float64(fixtureBaseMs)}},
		"parts": []any{map[string]any{"type": "text", "text": "结论一"}},
	}))
	b.Ingest(domainEvt("inst-a", "ses-1", "session.next.step.ended", nil))
	rounds := waitForRounds(1)
	if data, ok := rounds[0].Data.(*RoundCompletedData); !ok || data.Status != RoundStatusCompleted || data.Summary != "结论一" {
		t.Errorf("debounced round = %+v, want status=completed summary=结论一", rounds[0].Data)
	}

	// 第二轮：tool.failed → error。
	b.Ingest(domainEvt("inst-a", "ses-1", "session.next.prompted", nil))
	b.Ingest(domainEvt("inst-a", "ses-1", "session.next.tool.failed", map[string]any{"tool": "bash"}))
	b.Ingest(domainEvt("inst-a", "ses-1", "session.idle", nil))
	rounds = waitForRounds(2)
	if data, ok := rounds[1].Data.(*RoundCompletedData); !ok || data.Status != RoundStatusError || data.RoundIndex != 2 {
		t.Errorf("error round = %+v, want status=error round_index=2", rounds[1].Data)
	}

	// 第三轮：aborted → cancelled。
	b.Ingest(domainEvt("inst-a", "ses-1", "session.next.prompted", nil))
	b.Ingest(domainEvt("inst-a", "ses-1", "session.aborted", nil))
	rounds = waitForRounds(3)
	if data, ok := rounds[2].Data.(*RoundCompletedData); !ok || data.Status != RoundStatusCancelled {
		t.Errorf("cancelled round = %+v, want status=cancelled", rounds[2].Data)
	}

	// 防抖窗口内的续轮事件会撤销挂起收尾：step.ended → 立刻 text.delta → 不收尾。
	before := len(hub.events())
	b.Ingest(domainEvt("inst-a", "ses-1", "session.next.prompted", nil))
	b.Ingest(domainEvt("inst-a", "ses-1", "session.next.step.ended", nil))
	b.Ingest(domainEvt("inst-a", "ses-1", "session.next.text.delta", map[string]any{"delta": "x"}))
	time.Sleep(80 * time.Millisecond)
	for _, evt := range hub.events()[before:] {
		if evt.msgType == SessionEventRoundCompleted {
			t.Error("round finalized despite continuing stream within debounce window")
		}
	}
}

// TestSessionEventBroadcaster_TaskHealthStates 覆盖五态优先级
// （needs-input > stalled > error > running > idle）与无会话任务 idle。
func TestSessionEventBroadcaster_TaskHealthStates(t *testing.T) {
	b, hub, permMgr, _, cleanup := newSessionEventBroadcasterFixture(t, workspaceInstance("inst-a", "ws-a"))
	defer cleanup()

	now := time.UnixMilli(fixtureBaseMs)
	b.now = func() time.Time { return now }

	b.SetTaskSessionIndex(staticTaskIndex{links: []TaskSessionLink{
		{TaskID: "task_pend", WorkspaceID: "ws-a", InstanceID: "inst-a", SessionID: "ses_p"},
		{TaskID: "task_stall", WorkspaceID: "ws-a", InstanceID: "inst-a", SessionID: "ses_s"},
		{TaskID: "task_err", WorkspaceID: "ws-a", InstanceID: "inst-a", SessionID: "ses_e"},
		{TaskID: "task_bare", WorkspaceID: "ws-a"}, // 无会话任务
	}})

	// ses_p：round 进行中 + 挂起权限 → needs-input。
	b.Ingest(domainEvt("inst-a", "ses_p", "session.next.prompted", nil))
	permMgr.handleNewPermissionFromEvent("inst-a", "ses_p", map[string]any{
		"id": "per-fx", "action": "bash", "resources": []interface{}{"ls"},
	})

	// ses_s：round 结束 + 11 分钟无事件 → stalled。
	b.Ingest(domainEvt("inst-a", "ses_s", "session.next.prompted", nil))
	b.Ingest(domainEvt("inst-a", "ses_s", "session.idle", nil))
	now = now.Add(11 * time.Minute)
	// ses_e：round 以失败告终 → error（last_event_at 刚刷新，不满足 stalled）。
	b.Ingest(domainEvt("inst-a", "ses_e", "session.next.prompted", nil))
	b.Ingest(domainEvt("inst-a", "ses_e", "session.next.tool.failed", map[string]any{"tool": "edit"}))
	b.Ingest(domainEvt("inst-a", "ses_e", "session.idle", nil))

	b.taskHealthTick(context.Background())

	healthByTask := map[string]*TaskHealthData{}
	for _, evt := range hub.events() {
		if evt.msgType != SessionEventTaskHealth {
			continue
		}
		data, ok := evt.envelope.Data.(*TaskHealthData)
		if !ok {
			t.Fatalf("task.health data type = %T", evt.envelope.Data)
		}
		healthByTask[data.TaskID] = data
	}
	want := map[string]struct {
		health  string
		pending int
	}{
		"task_pend":  {TaskHealthNeedsInput, 1},
		"task_stall": {TaskHealthStalled, 0},
		"task_err":   {TaskHealthError, 0},
		"task_bare":  {TaskHealthIdle, 0},
	}
	if len(healthByTask) != len(want) {
		t.Fatalf("task.health broadcasts = %v, want tasks %v", healthByTask, want)
	}
	for taskID, w := range want {
		data, ok := healthByTask[taskID]
		if !ok {
			t.Errorf("missing task.health for %s", taskID)
			continue
		}
		if data.Health != w.health || data.PendingCount != w.pending {
			t.Errorf("task %s = %+v, want health=%s pending=%d", taskID, data, w.health, w.pending)
		}
		if data.ComputedAt == 0 {
			t.Errorf("task %s computed_at must be epoch ms", taskID)
		}
	}
	// 无会话任务 instance_id 省略（TS 侧 instance_id?）。
	if bare := healthByTask["task_bare"]; bare != nil {
		raw, _ := json.Marshal(bare)
		if strings.Contains(string(raw), "instance_id") {
			t.Errorf("task_bare must omit instance_id, got %s", raw)
		}
	}
}

// TestSessionEventBroadcaster_Snapshot 验证 §3 快照形状与 workspace 过滤。
func TestSessionEventBroadcaster_Snapshot(t *testing.T) {
	b, _, _, _, cleanup := newSessionEventBroadcasterFixture(t,
		workspaceInstance("inst_fx", "ws_fx"),
		workspaceInstance("inst_other", "ws_other"),
	)
	defer cleanup()

	b.Ingest(domainEvt("inst_fx", "ses_fx_1", "session.next.prompted", nil))
	b.Ingest(domainEvt("inst_fx", "ses_fx_1", "session.next.tool.called", map[string]any{"tool": "grep"}))
	b.Ingest(domainEvt("inst_fx", "ses_fx_1", "session.idle", nil))
	b.Ingest(domainEvt("inst_other", "ses_o_1", "session.next.prompted", nil))

	snap := b.Snapshot(context.Background(), "ws_fx")
	if len(snap.Sessions) != 1 {
		t.Fatalf("snapshot sessions = %+v, want 1 (ws_fx only)", snap.Sessions)
	}
	row := snap.Sessions[0]
	if row.InstanceID != "inst_fx" || row.SessionID != "ses_fx_1" {
		t.Errorf("row routing = %s/%s", row.InstanceID, row.SessionID)
	}
	if row.Health == nil || *row.Health != TaskHealthIdle {
		t.Errorf("row health = %v, want idle (round finished)", row.Health)
	}
	if row.Phase == nil || *row.Phase != SessionPhaseIdle {
		t.Errorf("row phase = %v, want idle", row.Phase)
	}
	if row.RoundIndex != 1 {
		t.Errorf("row round_index = %d, want 1", row.RoundIndex)
	}
	if row.LastEventAt == nil || *row.LastEventAt != fixtureBaseMs {
		t.Errorf("row last_event_at = %v, want %d", row.LastEventAt, fixtureBaseMs)
	}
	if row.LatestRound == nil || row.LatestRound.Status != RoundStatusCompleted {
		t.Errorf("row latest_round = %+v, want completed round", row.LatestRound)
	}
	if snap.GeneratedAt != fixtureBaseMs {
		t.Errorf("generated_at = %d, want %d", snap.GeneratedAt, fixtureBaseMs)
	}

	// JSON 形状锁定：任务映射里存在但从未见过事件的会话 → 全 null 行。
	b.SetTaskSessionIndex(staticTaskIndex{links: []TaskSessionLink{
		{TaskID: "task_o", WorkspaceID: "ws_other", InstanceID: "inst_other", SessionID: "ses_o_2"},
	}})
	otherSnap := b.Snapshot(context.Background(), "ws_other")
	if len(otherSnap.Sessions) != 2 {
		t.Fatalf("ws_other sessions = %d, want 2 (observed ses_o_1 + linked-only ses_o_2)", len(otherSnap.Sessions))
	}
	raw, err := json.Marshal(otherSnap)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	var generic struct {
		Sessions []map[string]any `json:"sessions"`
	}
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("re-parse snapshot: %v", err)
	}
	var nullRow map[string]any
	for _, r := range generic.Sessions {
		if r["session_id"] == "ses_o_2" {
			nullRow = r
		}
	}
	if nullRow == nil {
		t.Fatalf("ses_o_2 missing from snapshot: %s", raw)
	}
	for _, key := range []string{"health", "phase", "last_event_at", "latest_round"} {
		if v, ok := nullRow[key]; !ok || v != nil {
			t.Errorf("ses_o_2.%s = %v (%v), want explicit null", key, v, ok)
		}
	}
	if v, ok := nullRow["round_index"]; !ok || v != float64(0) {
		t.Errorf("ses_o_2.round_index = %v (%v), want 0", v, ok)
	}
}

// TestSessionEventBroadcaster_WorkspaceIsolation：未绑定 workspace 的实例不广播。
func TestSessionEventBroadcaster_WorkspaceIsolation(t *testing.T) {
	b, hub, _, _, cleanup := newSessionEventBroadcasterFixture(t, workspaceInstance("inst-shared", ""))
	defer cleanup()

	b.Ingest(domainEvt("inst-shared", "ses-s", "session.next.prompted", nil))
	b.Ingest(domainEvt("inst-shared", "ses-s", "session.idle", nil))
	b.flush()
	if events := hub.events(); len(events) != 0 {
		t.Fatalf("shared instance must not broadcast, got %+v", events)
	}
}

// TestSessionEventBroadcaster_NoHubIsSafe：nil hub 不 panic。
func TestSessionEventBroadcaster_NoHubIsSafe(t *testing.T) {
	reg := registry.NewRegistry()
	inst := workspaceInstance("inst-a", "ws-a")
	if err := reg.RegisterInstance(inst); err != nil {
		t.Fatalf("register instance: %v", err)
	}
	b := NewSessionEventBroadcaster(reg, nil, nil, nil)
	b.Ingest(domainEvt("inst-a", "ses-1", "session.next.prompted", nil))
	b.flush()
	b.taskHealthTick(context.Background())
}
