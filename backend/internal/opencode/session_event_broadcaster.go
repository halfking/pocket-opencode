package opencode

// session_event_broadcaster.go — P1 会话/轮次/任务健康事件广播器。
//
// 冻结契约：docs/2026-08-27-p1-contracts-frozen.md §2/§3；TS 侧唯一类型来源是
// frontend/src/services/sessionEvents.ts（本文件的 payload struct 与其逐字段对齐，
// 注释互指）。三类事件经 WsEnvelopeV1 信封（approval_broadcaster.go 同款）由
// websocket.Hub.BroadcastToWorkspace 定向广播：
//
//	session.activity  channel=sessions topic=sessionID（无 cause）
//	round.completed   channel=sessions topic=sessionID cause.correlation_id="<session_id>:<round_index>"
//	task.health       channel=tasks    topic=taskID（无 cause）
//
// 节流（契约 §2）：session.activity ≥30s 或 phase 切换才发；task.health 5s 合并
// （定时器 flush + 变更检测）；round.completed 每轮一条；发送侧统一 500ms
// coalescing（同 type+topic 窗口内合并保留最新）。

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/adapter"
	"github.com/halfking/pocket-opencode/backend/internal/registry"
)

// 三类事件类型（与 sessionEvents.ts SESSION_EVENT_TYPES 一一对应）。
const (
	// SessionEventActivity 报告会话当前 phase / 轮次进度。
	SessionEventActivity = "session.activity"
	// SessionEventRoundCompleted 报告一个用户轮次结束及一句话结论。
	SessionEventRoundCompleted = "round.completed"
	// SessionEventTaskHealth 报告任务健康度五态快照。
	SessionEventTaskHealth = "task.health"
)

// phase 枚举（冻结，sessionEvents.ts SessionPhase）。从上游 opencode 事件推导：
// 消息/推理/文本更新→thinking、工具执行→tool、Edit/Write 类→file_write、
// Bash/终端→pty、轮次结束→idle。
const (
	SessionPhaseThinking  = "thinking"
	SessionPhaseTool      = "tool"
	SessionPhaseFileWrite = "file_write"
	SessionPhasePty       = "pty"
	SessionPhaseIdle      = "idle"
)

// 轮次结果枚举（冻结，sessionEvents.ts RoundStatus）。
const (
	RoundStatusCompleted = "completed"
	RoundStatusError     = "error"
	RoundStatusCancelled = "cancelled"
)

// 任务健康五态（冻结，与 frontend/src/features/tasks/health.ts 一字不差；
// 优先级 needs-input > stalled > error > running > idle）。
const (
	TaskHealthNeedsInput = "needs-input"
	TaskHealthStalled    = "stalled"
	TaskHealthError      = "error"
	TaskHealthRunning    = "running"
	TaskHealthIdle       = "idle"
)

// 健康度阈值镜像 health.ts：ACTIVE_WITHIN_MS=2min / STALLED_AFTER_MS=10min。
const (
	sessionActiveWithin = 2 * time.Minute
	sessionStalledAfter = 10 * time.Minute
	// summaryMaxRunes 是 round.completed.summary 的截断上限（契约 ~200 字符）。
	summaryMaxRunes = 200
)

// 事件信封 channel 常量（sessions 事件走 sessions，task.health 走 tasks）。
const (
	channelSessions = "sessions"
	channelTasks    = "tasks"
)

// event_id 前缀（契约 §2：前缀 + UnixNano + 原子序列）。
const (
	eventIDPrefixActivity = "session_activity_"
	eventIDPrefixRound    = "round_completed_"
	eventIDPrefixTask     = "task_health_"
)

// SessionActivityData 镜像 sessionEvents.ts SessionActivityData。
type SessionActivityData struct {
	InstanceID  string `json:"instance_id"`
	SessionID   string `json:"session_id"`
	Phase       string `json:"phase"`
	LastEventAt int64  `json:"last_event_at"`
	RoundIndex  int    `json:"round_index"`
}

// RoundChangeStats 镜像 sessionEvents.ts RoundChangeStats。
// 从 Edit/Write 类工具的 diff/输入统计；拿不到就是 0（不编造）。
type RoundChangeStats struct {
	Added   int `json:"added"`
	Removed int `json:"removed"`
	Files   int `json:"files"`
}

// RoundCompletedData 镜像 sessionEvents.ts RoundCompletedData。
type RoundCompletedData struct {
	InstanceID  string           `json:"instance_id"`
	SessionID   string           `json:"session_id"`
	RoundIndex  int              `json:"round_index"`
	Summary     string           `json:"summary"`
	Changes     RoundChangeStats `json:"changes"`
	Status      string           `json:"status"`
	CompletedAt int64            `json:"completed_at"`
}

// TaskHealthData 镜像 sessionEvents.ts TaskHealthData（instance_id 可选）。
type TaskHealthData struct {
	TaskID       string `json:"task_id"`
	InstanceID   string `json:"instance_id,omitempty"`
	Health       string `json:"health"`
	PendingCount int    `json:"pending_count"`
	ComputedAt   int64  `json:"computed_at"`
}

// EventsSnapshotSession 镜像 sessionEvents.ts EventsSnapshotSession
// （GET /api/mobile/events/snapshot 单会话行，§3）。
type EventsSnapshotSession struct {
	InstanceID  string              `json:"instance_id"`
	SessionID   string              `json:"session_id"`
	Health      *string             `json:"health"`
	Phase       *string             `json:"phase"`
	RoundIndex  int                 `json:"round_index"`
	LastEventAt *int64              `json:"last_event_at"`
	LatestRound *RoundCompletedData `json:"latest_round"`
}

// EventsSnapshot 镜像 sessionEvents.ts EventsSnapshot（§3 快照响应）。
type EventsSnapshot struct {
	Sessions    []EventsSnapshotSession `json:"sessions"`
	GeneratedAt int64                   `json:"generated_at"`
}

// TaskSessionLink 是任务→会话映射的一行（复用 /api/tasks/:id/sessions 对应的
// task_session_links 体系；InstanceID/SessionID 为空表示该任务尚无会话）。
type TaskSessionLink struct {
	TaskID      string
	WorkspaceID string
	InstanceID  string
	SessionID   string
}

// TaskSessionIndex 提供 task.health 聚合所需的任务→会话映射。生产实现是
// server 包对 task.Store 的适配器（ListTasks + ListSessionsForTask 链路）。
type TaskSessionIndex interface {
	SessionLinks(ctx context.Context) ([]TaskSessionLink, error)
}

// sessionState 是单个 (instance, session) 的轮次/phase 状态机。
type sessionState struct {
	instanceID  string
	sessionID   string
	workspaceID string // 首次广播时从 registry 惰性解析并缓存

	phase       string
	lastEventAt time.Time

	roundIndex      int    // 1-based 用户 prompt 序数（按观察到的 prompt 递增）
	roundActive     bool   // 自用户 prompt 起、至轮次结束前为 true
	sawFailure      bool   // 本轮内出现过 step/tool failed → error 状态
	roundSummaryStr string // 本轮最后一条助手文本消息
	roundAdded      int
	roundRemoved    int
	roundFiles      map[string]bool

	latestRound *RoundCompletedData

	lastActivitySentPhase string
	lastActivitySentAt    time.Time
}

func (s *sessionState) key() string { return s.instanceID + ":" + s.sessionID }

// pendingBroadcast 是 500ms coalescing 窗口里的待发送事件（同 type+topic 保留最新）。
type pendingBroadcast struct {
	workspaceID string
	envelope    WsEnvelopeV1
	seq         uint64 // 入队序号，flush 时按它保序（map 遍历无序）
}

// taskHealthKey 用于 task.health 的 5s 合并变更检测。
type taskHealthKey struct {
	health string
	pend   int
}

// SessionEventBroadcaster 订阅 EventStreamManager 的 DomainEvent 流，维护每
// session 状态机并广播 session.activity / round.completed / task.health。
type SessionEventBroadcaster struct {
	registry *registry.Registry
	eventMgr *EventStreamManager
	perms    *PermissionManager
	ques     *QuestionManager
	tasks    TaskSessionIndex // nil = 不产 task.health

	hub ApprovalBroadcastHub // nil = 跳过 WS 推送（照 ApprovalBroadcaster）

	// 可调参数（测试注入；默认值即契约值）。
	coalesceWindow      time.Duration // 500ms 发送侧合并
	activityMinInterval time.Duration // session.activity ≥30s
	taskHealthInterval  time.Duration // task.health 5s 定时 flush
	roundIdleDebounce   time.Duration // step.ended 后无后续事件的轮次收尾防抖

	now func() time.Time
	seq uint64

	mu             sync.Mutex
	sessions       map[string]*sessionState
	pending        map[string]*pendingBroadcast
	lastTaskHealth map[string]taskHealthKey
	finalizeTimers map[string]*time.Timer
}

// NewSessionEventBroadcaster 创建广播器。perms/ques 用于 needs-input 判定的
// 待审批计数；eventMgr 为事件源；均允许为 nil（对应能力降级）。
func NewSessionEventBroadcaster(reg *registry.Registry, eventMgr *EventStreamManager, perms *PermissionManager, ques *QuestionManager) *SessionEventBroadcaster {
	return &SessionEventBroadcaster{
		registry:            reg,
		eventMgr:            eventMgr,
		perms:               perms,
		ques:                ques,
		coalesceWindow:      500 * time.Millisecond,
		activityMinInterval: 30 * time.Second,
		taskHealthInterval:  5 * time.Second,
		roundIdleDebounce:   3 * time.Second,
		now:                 time.Now,
		sessions:            make(map[string]*sessionState),
		pending:             make(map[string]*pendingBroadcast),
		lastTaskHealth:      make(map[string]taskHealthKey),
		finalizeTimers:      make(map[string]*time.Timer),
	}
}

// SetBroadcaster 注入 WS hub（websocket.Hub 满足 ApprovalBroadcastHub）。
func (b *SessionEventBroadcaster) SetBroadcaster(hub ApprovalBroadcastHub) {
	b.hub = hub
}

// SetTaskSessionIndex 注入任务→会话映射（task.Store 适配器），启用 task.health。
func (b *SessionEventBroadcaster) SetTaskSessionIndex(idx TaskSessionIndex) {
	b.tasks = idx
}

// Run 启动事件订阅（照 PermissionManager：对 registry 内 healthy 实例各起一个
// 订阅循环）与 flush/health 定时器。Blocks；run in a goroutine。
func (b *SessionEventBroadcaster) Run(ctx context.Context) {
	if b == nil {
		return
	}
	log.Println("[session-event-broadcast] started")
	defer log.Println("[session-event-broadcast] stopped")

	// 周期重扫：实例常晚于 pocketd 启动（如 opencode serve 手动拉起、插件
	// 注册），只在启动时扫一次会让这类实例永远进不了事件管道（2026-08-27
	// 真机验证实测踩中）。30s 重扫 + 已订阅集合去重，健康窗口错过则下轮补订。
	subscribed := make(map[string]bool)
	rescan := func() {
		if b.eventMgr == nil || b.registry == nil {
			return
		}
		for _, inst := range b.registry.ListInstances() {
			if inst.Health != "healthy" || subscribed[inst.ID] {
				continue
			}
			subscribed[inst.ID] = true
			go b.subscribeInstance(ctx, inst.ID)
		}
	}
	rescan()
	rescanT := time.NewTicker(30 * time.Second)
	defer rescanT.Stop()

	flushT := time.NewTicker(b.coalesceWindow)
	defer flushT.Stop()
	healthT := time.NewTicker(b.taskHealthInterval)
	defer healthT.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-rescanT.C:
			rescan()
		case <-flushT.C:
			b.flush()
		case <-healthT.C:
			b.taskHealthTick(ctx)
		}
	}
}

// subscribeInstance 订阅单实例事件并喂给状态机。
func (b *SessionEventBroadcaster) subscribeInstance(ctx context.Context, instanceID string) {
	events, cleanup, err := b.eventMgr.Subscribe(ctx, SubscribeOptions{
		InstanceID: instanceID,
		BufferSize: 256,
	})
	if err != nil {
		log.Printf("[session-event-broadcast] subscribe instance=%s failed: %v", instanceID, err)
		return
	}
	defer cleanup()
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-events:
			if !ok {
				return
			}
			b.Ingest(evt)
		}
	}
}

// ingest 处理一条 DomainEvent（导出给潜在的外部喂入方；测试直接调用）。
func (b *SessionEventBroadcaster) Ingest(evt DomainEvent) {
	sid := evt.SessionID
	if sid == "" {
		sid = sessionIDFromRawEvent(evt.Raw)
	}
	if sid == "" {
		return
	}
	key := evt.InstanceID + ":" + sid

	b.mu.Lock()
	defer b.mu.Unlock()
	st := b.sessions[key]
	if st == nil {
		st = &sessionState{
			instanceID: evt.InstanceID,
			sessionID:  sid,
			roundFiles: make(map[string]bool),
		}
		b.sessions[key] = st
	}
	b.applyEventLocked(st, evt)
}

// applyEventLocked 更新状态机并按需入队 session.activity（调用方持 b.mu）。
func (b *SessionEventBroadcaster) applyEventLocked(st *sessionState, evt DomainEvent) {
	now := b.now()
	data, _ := evt.Raw.Data.(map[string]any)
	st.lastEventAt = now

	switch {
	// —— 用户 prompt：轮次边界（上一轮若未收尾则先收尾），新轮开始 ——
	case evt.Type == "session.next.prompted", evt.Type == "session.next.prompt.admitted",
		evt.Type == "message.updated" && messageRole(data) == "user":
		b.cancelFinalizeLocked(st)
		if st.roundActive {
			b.closeRoundLocked(st, roundStatusFor(st), now)
		}
		st.roundIndex++
		st.roundActive = true
		st.sawFailure = false
		st.roundSummaryStr = ""
		st.roundAdded, st.roundRemoved = 0, 0
		st.roundFiles = make(map[string]bool)
		st.phase = SessionPhaseThinking

	// —— 思考/文本流（step 开始、reasoning/text 输出、压缩）——
	case evt.Type == "session.next.step.started",
		evt.Type == "session.next.reasoning.started", evt.Type == "session.next.reasoning.delta",
		evt.Type == "session.next.text.started", evt.Type == "session.next.text.delta",
		evt.Type == "session.next.text.ended",
		evt.Type == "session.next.compaction.started", evt.Type == "session.next.compaction.delta":
		b.cancelFinalizeLocked(st) // 流仍在继续，撤销挂起的轮次收尾
		st.phase = SessionPhaseThinking

	// —— 工具调用：phase 按工具名细分（file_write / pty / tool）——
	case evt.Type == "session.next.tool.called",
		evt.Type == "session.next.tool.input.started", evt.Type == "session.next.tool.input.delta",
		evt.Type == "session.next.tool.progress":
		b.cancelFinalizeLocked(st)
		st.phase = phaseForToolName(toolNameFromData(data))

	case evt.Type == "session.next.tool.success", evt.Type == "session.next.tool.failed":
		// 工具结束不改 phase（下一步可能继续），只累计变更/失败标记。
		name := toolNameFromData(data)
		input, _ := asAnyMap(data["input"])
		output := firstNonNilAny(data["output"], data["result"], data["content"])
		b.applyToolObservationLocked(st, name, input, output, evt.Type == "session.next.tool.failed")

	case evt.Type == "session.next.shell.started":
		b.cancelFinalizeLocked(st)
		st.phase = SessionPhasePty
	case evt.Type == "session.next.shell.ended":
		b.scheduleFinalizeLocked(st)

	// —— step 结束 → 防抖收尾；step 失败 → 本轮判 error ——
	case evt.Type == "session.next.step.ended":
		b.scheduleFinalizeLocked(st)
	case evt.Type == "session.next.step.failed":
		st.sawFailure = true
		b.scheduleFinalizeLocked(st)

	// —— 会话级终止：立即收尾 + idle ——
	case evt.Type == "session.idle", evt.Type == "session.completed":
		b.cancelFinalizeLocked(st)
		b.closeRoundLocked(st, roundStatusFor(st), now)
	case evt.Type == "session.aborted", evt.Type == "session.next.aborted",
		evt.Type == "session.deleted":
		b.cancelFinalizeLocked(st)
		b.closeRoundLocked(st, RoundStatusCancelled, now)

	// —— V1 message.updated（助手）：正文/工具 parts + 完成信号 ——
	case evt.Type == "message.updated" && messageRole(data) == "assistant",
		evt.Type == "message.part.updated":
		if text := messageText(data); text != "" {
			st.roundSummaryStr = text
		}
		for _, part := range messageToolParts(data) {
			b.applyToolObservationLocked(st, part.name, part.input, part.output, part.failed)
		}
		if messageCompleted(data) {
			b.scheduleFinalizeLocked(st)
		}

	default:
		// 未识别的事件类型只刷新 last_event_at（健康度 age 计算仍准确）。
	}

	b.maybeEmitActivityLocked(st, now)
}

// maybeEmitActivityLocked 实现契约节流：phase 切换，或距上次发送 ≥30s 才发
// session.activity（同 phase 高频事件静默吞掉）。
func (b *SessionEventBroadcaster) maybeEmitActivityLocked(st *sessionState, now time.Time) {
	if st.phase == "" {
		return
	}
	phaseSwitched := st.lastActivitySentPhase == "" || st.phase != st.lastActivitySentPhase
	aged := now.Sub(st.lastActivitySentAt) >= b.activityMinInterval
	if !phaseSwitched && !aged {
		return
	}
	st.lastActivitySentPhase = st.phase
	st.lastActivitySentAt = now
	b.enqueueLocked(eventIDPrefixActivity, channelSessions, st.sessionID, SessionEventActivity,
		&SessionActivityData{
			InstanceID:  st.instanceID,
			SessionID:   st.sessionID,
			Phase:       st.phase,
			LastEventAt: now.UnixMilli(),
			RoundIndex:  st.roundIndex,
		}, nil, b.workspaceForStateLocked(st))
}

// closeRoundLocked 产出一条 round.completed 并重置轮次累计（调用方持 b.mu）。
func (b *SessionEventBroadcaster) closeRoundLocked(st *sessionState, status string, now time.Time) {
	if !st.roundActive {
		return
	}
	data := &RoundCompletedData{
		InstanceID: st.instanceID,
		SessionID:  st.sessionID,
		RoundIndex: st.roundIndex,
		Summary:    truncateRunes(st.roundSummaryStr, summaryMaxRunes),
		Changes: RoundChangeStats{
			Added:   st.roundAdded,
			Removed: st.roundRemoved,
			Files:   len(st.roundFiles),
		},
		Status:      status,
		CompletedAt: now.UnixMilli(),
	}
	st.latestRound = data
	st.roundActive = false
	st.sawFailure = false
	st.phase = SessionPhaseIdle
	b.enqueueLocked(eventIDPrefixRound, channelSessions, st.sessionID, SessionEventRoundCompleted,
		data, &WsCauseV1{CorrelationID: fmt.Sprintf("%s:%d", st.sessionID, st.roundIndex)},
		b.workspaceForStateLocked(st))
}

func roundStatusFor(st *sessionState) string {
	if st.sawFailure {
		return RoundStatusError
	}
	return RoundStatusCompleted
}

// scheduleFinalizeLocked 在 step/message 完成后挂一个防抖定时器：窗口内无后续
// 事件才收尾轮次（一轮内可有多个 step）。
func (b *SessionEventBroadcaster) scheduleFinalizeLocked(st *sessionState) {
	b.cancelFinalizeLocked(st)
	key := st.key()
	b.finalizeTimers[key] = time.AfterFunc(b.roundIdleDebounce, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if cur, ok := b.finalizeTimers[key]; !ok || cur == nil {
			return
		}
		delete(b.finalizeTimers, key)
		curSt := b.sessions[key]
		if curSt == nil || !curSt.roundActive {
			return
		}
		now := b.now()
		b.closeRoundLocked(curSt, roundStatusFor(curSt), now)
		b.maybeEmitActivityLocked(curSt, now)
	})
}

func (b *SessionEventBroadcaster) cancelFinalizeLocked(st *sessionState) {
	key := st.key()
	if t, ok := b.finalizeTimers[key]; ok {
		t.Stop()
		delete(b.finalizeTimers, key)
	}
}

// enqueueLocked 把事件写进 500ms coalescing 窗口（同 type+topic 覆盖旧值保留最新）。
func (b *SessionEventBroadcaster) enqueueLocked(idPrefix, channel, topic, evtType string, data interface{}, cause *WsCauseV1, workspaceID string) {
	n := atomic.AddUint64(&b.seq, 1)
	now := b.now()
	env := WsEnvelopeV1{
		V:       1,
		ID:      fmt.Sprintf("%s%d_%d", idPrefix, now.UnixNano(), n),
		Ts:      now.UnixMilli(),
		Channel: channel,
		Topic:   topic,
		Type:    evtType,
		Data:    data,
		Cause:   cause,
	}
	b.pending[evtType+"\x00"+topic] = &pendingBroadcast{workspaceID: workspaceID, envelope: env, seq: n}
}

// flush 把 coalescing 窗口内的事件广播出去（未绑定 workspace 的实例跳过，
// 与 ApprovalBroadcaster 相同的 fail-closed 策略）。同一窗口内多条事件按入队
// 序号保序（map 遍历无序，比如 round.completed 与紧随的 idle activity）。
func (b *SessionEventBroadcaster) flush() {
	b.mu.Lock()
	if len(b.pending) == 0 {
		b.mu.Unlock()
		return
	}
	out := make([]pendingBroadcast, 0, len(b.pending))
	for k, p := range b.pending {
		out = append(out, *p)
		delete(b.pending, k)
	}
	b.mu.Unlock()

	sort.Slice(out, func(i, j int) bool { return out[i].seq < out[j].seq })

	if b.hub == nil {
		return
	}
	for _, p := range out {
		if p.workspaceID == "" {
			log.Printf("[session-event-broadcast] skip %s: no workspace bound (pull-only)", p.envelope.Type)
			continue
		}
		b.hub.BroadcastToWorkspace(p.workspaceID, p.envelope.Type, p.envelope)
	}
}

// taskHealthTick 是 task.health 的 5s 合并 flush：重算所有任务的五态，
// 只对（health, pending_count）发生变化（或首见）的任务入队。
func (b *SessionEventBroadcaster) taskHealthTick(ctx context.Context) {
	if b == nil || b.tasks == nil {
		return
	}
	links, err := b.tasks.SessionLinks(ctx)
	if err != nil {
		log.Printf("[session-event-broadcast] task session links: %v", err)
		return
	}

	type taskAgg struct {
		workspace string
		sessions  []*sessionState
	}
	aggs := make(map[string]*taskAgg)
	for _, l := range links {
		agg, ok := aggs[l.TaskID]
		if !ok {
			agg = &taskAgg{workspace: l.WorkspaceID}
			aggs[l.TaskID] = agg
		}
		if l.InstanceID == "" || l.SessionID == "" {
			continue // 任务无会话：保留聚合（→ idle）
		}
		if st := b.lookupSession(l.InstanceID, l.SessionID); st != nil {
			agg.sessions = append(agg.sessions, st)
		}
	}

	now := b.now()
	b.mu.Lock()
	for taskID, agg := range aggs {
		pending := 0
		bestHealth := TaskHealthIdle
		var bestInstance string
		var bestAt time.Time
		for _, st := range agg.sessions {
			p := b.sessionPendingLocked(st)
			pending += p
			h := b.sessionHealthLocked(st, p, now)
			hp, bp := healthPriority(h), healthPriority(bestHealth)
			if hp < bp || (hp == bp && st.lastEventAt.After(bestAt)) {
				bestHealth = h
				bestInstance = st.instanceID
				bestAt = st.lastEventAt
			}
		}
		key := taskHealthKey{health: bestHealth, pend: pending}
		if prev, ok := b.lastTaskHealth[taskID]; ok && prev == key {
			continue
		}
		b.lastTaskHealth[taskID] = key
		b.enqueueLocked(eventIDPrefixTask, channelTasks, taskID, SessionEventTaskHealth,
			&TaskHealthData{
				TaskID:       taskID,
				InstanceID:   bestInstance,
				Health:       bestHealth,
				PendingCount: pending,
				ComputedAt:   now.UnixMilli(),
			}, nil, agg.workspace)
	}
	b.mu.Unlock()
	b.flush()
}

// lookupSession 返回（不创建）指定会话的状态。
func (b *SessionEventBroadcaster) lookupSession(instanceID, sessionID string) *sessionState {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sessions[instanceID+":"+sessionID]
}

// sessionPendingLocked 统计该会话待审批（permission+question）计数。
func (b *SessionEventBroadcaster) sessionPendingLocked(st *sessionState) int {
	n := 0
	if b.perms != nil {
		n += len(b.perms.ListPending(st.instanceID, st.sessionID))
	}
	if b.ques != nil {
		n += len(b.ques.ListPending(st.instanceID, st.sessionID))
	}
	return n
}

// sessionHealthLocked 镜像 health.ts assessHealth 的优先级判定：
// needs-input > stalled > error > running > idle。
func (b *SessionEventBroadcaster) sessionHealthLocked(st *sessionState, pending int, now time.Time) string {
	if pending > 0 {
		return TaskHealthNeedsInput
	}
	if !st.lastEventAt.IsZero() && now.Sub(st.lastEventAt) > sessionStalledAfter {
		return TaskHealthStalled
	}
	if st.latestRound != nil && st.latestRound.Status == RoundStatusError {
		return TaskHealthError
	}
	if st.roundActive || (st.phase != "" && st.phase != SessionPhaseIdle) {
		return TaskHealthRunning
	}
	return TaskHealthIdle
}

// healthPriority 越小优先级越高（needs-input=0 … idle=4）。
func healthPriority(h string) int {
	switch h {
	case TaskHealthNeedsInput:
		return 0
	case TaskHealthStalled:
		return 1
	case TaskHealthError:
		return 2
	case TaskHealthRunning:
		return 3
	default:
		return 4
	}
}

// Snapshot 返回 §3 的内存态快照（按 workspace 过滤；空 workspace 返回全部，
// 仅供内部/测试）。供 GET /api/mobile/events/snapshot 使用。已观察到事件的
// 会话总是带 health/phase/last_event_at；任务映射里存在但从未见过事件的会话
// 也列出，字段为 null（前端可据此区分"无实时数据"与"空闲"）。
func (b *SessionEventBroadcaster) Snapshot(ctx context.Context, workspaceID string) EventsSnapshot {
	now := b.now()
	b.mu.Lock()

	rows := make([]EventsSnapshotSession, 0, len(b.sessions))
	seen := make(map[string]bool, len(b.sessions))
	for k, st := range b.sessions {
		seen[k] = true
		ws := b.workspaceForStateLocked(st)
		if workspaceID != "" && ws != workspaceID {
			continue
		}
		pending := b.sessionPendingLocked(st)
		health := b.sessionHealthLocked(st, pending, now)
		row := EventsSnapshotSession{
			InstanceID:  st.instanceID,
			SessionID:   st.sessionID,
			Health:      &health,
			RoundIndex:  st.roundIndex,
			LatestRound: st.latestRound,
		}
		if st.phase != "" {
			phase := st.phase
			row.Phase = &phase
		}
		if !st.lastEventAt.IsZero() {
			last := st.lastEventAt.UnixMilli()
			row.LastEventAt = &last
		}
		rows = append(rows, row)
	}
	b.mu.Unlock()

	// 任务映射里存在但未见事件的会话：全 null 行（不含 generated_at 语义变化）。
	if b.tasks != nil {
		if links, err := b.tasks.SessionLinks(ctx); err == nil {
			for _, l := range links {
				if l.InstanceID == "" || l.SessionID == "" || seen[l.InstanceID+":"+l.SessionID] {
					continue
				}
				if workspaceID != "" && l.WorkspaceID != workspaceID {
					continue
				}
				rows = append(rows, EventsSnapshotSession{
					InstanceID: l.InstanceID,
					SessionID:  l.SessionID,
				})
			}
		} else {
			log.Printf("[session-event-broadcast] snapshot task links: %v", err)
		}
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].InstanceID != rows[j].InstanceID {
			return rows[i].InstanceID < rows[j].InstanceID
		}
		return rows[i].SessionID < rows[j].SessionID
	})
	return EventsSnapshot{Sessions: rows, GeneratedAt: now.UnixMilli()}
}

// workspaceForStateLocked 解析并缓存实例归属 workspace（调用方持 b.mu）。
func (b *SessionEventBroadcaster) workspaceForStateLocked(st *sessionState) string {
	if st.workspaceID != "" {
		return st.workspaceID
	}
	if b.registry == nil {
		return ""
	}
	inst, err := b.registry.GetInstance(st.instanceID)
	if err != nil {
		return ""
	}
	st.workspaceID = inst.WorkspaceID
	return st.workspaceID
}

// =============================================================================
// 上游事件 payload 解析（防御式：拿不到就零值，绝不编造）
// =============================================================================

// sessionIDFromRawEvent 是 extractSessionID 的扩展：额外覆盖 V2 的
// durable.aggregateID 与 V1 的 location.sessionID（见 mobile_session_handler
// 的 eventBelongsToSession 同款逻辑）。
func sessionIDFromRawEvent(evt adapter.OpenCodeEvent) string {
	if sid := extractSessionID(evt); sid != "" {
		return sid
	}
	if data, ok := evt.Data.(map[string]any); ok {
		if durable, ok := data["durable"].(map[string]any); ok {
			if agg, ok := durable["aggregateID"].(string); ok && agg != "" {
				return agg
			}
		}
	}
	if evt.Location != nil {
		if sid, ok := evt.Location["sessionID"].(string); ok && sid != "" {
			return sid
		}
	}
	return ""
}

func asAnyMap(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

// messageRole 兼容 V1 信封 data.info.role 与扁平 data.role。
func messageRole(data map[string]any) string {
	if data == nil {
		return ""
	}
	if info, ok := asAnyMap(data["info"]); ok {
		if r, ok := info["role"].(string); ok && r != "" {
			return r
		}
	}
	if r, ok := data["role"].(string); ok {
		return r
	}
	return ""
}

// messageText 提取助手消息文本（最后一条助手文本消息 → 轮次 summary 来源）。
// 兼容 V1 {info, parts}、扁平 {text, parts} 与 V2 {text, content}（线格式说明见
// adapter/opencode_message_mobile.go 文档注释）。
func messageText(data map[string]any) string {
	if data == nil {
		return ""
	}
	var text string
	if t, ok := data["text"].(string); ok {
		text = t
	}
	if info, ok := asAnyMap(data["info"]); ok {
		if t, ok := info["text"].(string); ok && t != "" {
			text = t
		}
	}
	var parts []any
	if raw, ok := data["parts"].([]any); ok {
		parts = raw
	} else if raw, ok := data["content"].([]any); ok {
		parts = raw
	} else if info, ok := asAnyMap(data["info"]); ok {
		if raw, ok := info["content"].([]any); ok {
			parts = raw
		}
	}
	var agg string
	for _, raw := range parts {
		part, ok := asAnyMap(raw)
		if !ok {
			continue
		}
		if t, ok := part["text"].(string); ok && t != "" {
			t = strings.TrimSpace(t)
			if t == "" {
				continue
			}
			if agg != "" {
				agg += "\n\n"
			}
			agg += t
		}
	}
	if agg != "" {
		return agg
	}
	return text
}

// messageCompleted 判定消息是否已完成（time.completed > 0）。
func messageCompleted(data map[string]any) bool {
	if data == nil {
		return false
	}
	times := map[string]any{}
	if t, ok := asAnyMap(data["time"]); ok {
		times = t
	} else if info, ok := asAnyMap(data["info"]); ok {
		if t, ok := asAnyMap(info["time"]); ok {
			times = t
		}
	}
	if c, ok := times["completed"].(float64); ok {
		return c > 0
	}
	return false
}

type observedToolPart struct {
	name   string
	input  map[string]any
	output any
	failed bool
}

// messageToolParts 提取消息里的工具调用 parts（V1 {tool, state:{input,output}} /
// V2 {name, state:{input,result|content}}，见 mobileToolPart 同款兼容）。
func messageToolParts(data map[string]any) []observedToolPart {
	if data == nil {
		return nil
	}
	var parts []any
	if raw, ok := data["parts"].([]any); ok {
		parts = raw
	} else if raw, ok := data["content"].([]any); ok {
		parts = raw
	} else if info, ok := asAnyMap(data["info"]); ok {
		if raw, ok := info["content"].([]any); ok {
			parts = raw
		}
	}
	var out []observedToolPart
	for _, raw := range parts {
		part, ok := asAnyMap(raw)
		if !ok {
			continue
		}
		pt, _ := part["type"].(string)
		if pt != "" && pt != "tool" {
			continue
		}
		name := firstNonEmptyStr(strAny(part["tool"]), strAny(part["name"]))
		obs := observedToolPart{name: name}
		if state, ok := asAnyMap(part["state"]); ok {
			if input, ok := asAnyMap(state["input"]); ok {
				obs.input = input
			}
			obs.output = firstNonNilAny(state["output"], state["result"], state["content"])
			if status, _ := state["status"].(string); status == "error" {
				obs.failed = true
			}
			if errStr, _ := state["error"].(string); errStr != "" {
				obs.failed = true
			}
		}
		out = append(out, obs)
	}
	return out
}

// toolNameFromData 从工具类事件 data 里取工具名。
func toolNameFromData(data map[string]any) string {
	if data == nil {
		return ""
	}
	return firstNonEmptyStr(strAny(data["tool"]), strAny(data["name"]), strAny(data["toolName"]))
}

func strAny(v any) string {
	s, _ := v.(string)
	return s
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func firstNonNilAny(vals ...any) any {
	for _, v := range vals {
		if v != nil {
			return v
		}
	}
	return nil
}

// phaseForToolName 按工具名细分 phase：Edit/Write/Patch 类→file_write，
// Bash/Shell/Terminal 类→pty，其余→tool。
func phaseForToolName(name string) string {
	l := strings.ToLower(name)
	switch {
	case l == "":
		return SessionPhaseTool
	case strings.Contains(l, "edit"), strings.Contains(l, "write"), strings.Contains(l, "patch"):
		return SessionPhaseFileWrite
	case strings.Contains(l, "bash"), strings.Contains(l, "shell"),
		strings.Contains(l, "terminal"), strings.Contains(l, "exec"), l == "pty":
		return SessionPhasePty
	default:
		return SessionPhaseTool
	}
}

// applyToolObservationLocked 累计轮次变更统计。只对文件写入类工具计数；
// diff 拿不到时退回 edit 的 old/new 字符串行数、write 的 content 行数，
// 再拿不到就 0（不编造）。
func (b *SessionEventBroadcaster) applyToolObservationLocked(st *sessionState, name string, input map[string]any, output any, failed bool) {
	if failed {
		st.sawFailure = true
	}
	l := strings.ToLower(name)
	isFileTool := strings.Contains(l, "edit") || strings.Contains(l, "write") || strings.Contains(l, "patch")
	if !isFileTool {
		return
	}
	if path := toolInputPath(input); path != "" {
		st.roundFiles[path] = true
	}
	if diff, ok := output.(string); ok && looksLikeUnifiedDiff(diff) {
		added, removed, files := parseUnifiedDiff(diff)
		st.roundAdded += added
		st.roundRemoved += removed
		for _, f := range files {
			st.roundFiles[f] = true
		}
		return
	}
	// edit 工具：old/new 字符串行数即增删行数。
	if old := strAny(mapGet(input, "oldString", "old_string", "oldStr")); old != "" {
		st.roundRemoved += countNonEmptyLines(old)
	}
	if nw := strAny(mapGet(input, "newString", "new_string", "newStr")); nw != "" {
		st.roundAdded += countNonEmptyLines(nw)
	}
	// write 工具：content 行数计入 added。
	if content := strAny(mapGet(input, "content")); content != "" {
		st.roundAdded += countNonEmptyLines(content)
	}
}

// toolInputPath 提取工具输入里的目标文件路径。
func toolInputPath(input map[string]any) string {
	if input == nil {
		return ""
	}
	return strAny(mapGet(input, "filePath", "file_path", "file", "path", "filename", "notebookId"))
}

// mapGet 返回第一个命中的 key 的值。
func mapGet(m map[string]any, keys ...string) any {
	if m == nil {
		return nil
	}
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			return v
		}
	}
	return nil
}

func countNonEmptyLines(s string) int {
	n := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// looksLikeUnifiedDiff 判定输出是否是 unified diff（有 +++/--- 头或 @@ 块）。
func looksLikeUnifiedDiff(s string) bool {
	return strings.Contains(s, "@@") && (strings.Contains(s, "+++") || strings.Contains(s, "---"))
}

// parseUnifiedDiff 统计 diff 的增删行数与涉及文件。解析不了的行不计数。
func parseUnifiedDiff(diff string) (added, removed int, files []string) {
	seen := map[string]bool{}
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "+++ "), strings.HasPrefix(line, "--- "):
			path := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "+++ "), "--- "))
			// 去掉 b/ a/ 前缀与时戳尾巴
			path = strings.TrimPrefix(path, "b/")
			path = strings.TrimPrefix(path, "a/")
			if i := strings.IndexAny(path, "\t"); i >= 0 {
				path = path[:i]
			}
			if path == "" || path == "/dev/null" || seen[path] {
				continue
			}
			seen[path] = true
			files = append(files, path)
		case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
			// 无路径的头行（罕见），跳过
		case strings.HasPrefix(line, "+"):
			added++
		case strings.HasPrefix(line, "-"):
			removed++
		}
	}
	return added, removed, files
}

// truncateRunes 按字符数截断（summary ~200 字符）。
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}
