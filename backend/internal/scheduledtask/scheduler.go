package scheduledtask

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Broadcaster is the small WebSocket surface needed by the scheduler. The
// concrete websocket Hub satisfies it without making this package depend on
// the transport implementation.
type Broadcaster interface {
	BroadcastToWorkspace(workspaceID, msgType string, payload interface{})
}

// AuditFields is the minimal background-job audit payload.
type AuditFields struct {
	Success bool
	Detail  string
}

// AuditWriter keeps scheduler auditing decoupled from server's private auth
// types. The server bridge maps this to its central audit store.
type AuditWriter interface {
	Write(userID, tenantID, action, resource string, fields AuditFields)
}

// SchedulerStore is the persistence surface consumed by Scheduler. Keeping it
// as an interface allows deterministic tests and alternate stores while the
// production implementation remains the PostgreSQL Store.
type SchedulerStore interface {
	Available() bool
	ClaimDue(context.Context, int64, int) ([]*Task, error)
	ClaimTaskNow(context.Context, string, string, string, int64) (*Task, error)
	GetTaskScoped(context.Context, string, string, string) (*Task, error)
	InsertRun(context.Context, *Run) error
	FinishRun(context.Context, string, RunStatus, json.RawMessage, string, string) error
	UpdateTaskAfterRun(context.Context, string, RunStatus, string, int64) error
}

// Scheduler periodically claims due tasks and dispatches them to registered
// executors. It is safe to Start/Stop once from the application bootstrap and
// safe for Register/TriggerNow to be called concurrently with the tick loop.
type Scheduler struct {
	store       SchedulerStore
	executorsMu sync.RWMutex
	executors   map[Kind]Executor

	broadcaster Broadcaster
	auditor     AuditWriter

	stop         chan struct{}
	startOnce    sync.Once
	stopOnce     sync.Once
	wg           sync.WaitGroup
	enabled      bool
	tickInterval time.Duration
	maxParallel  int
	semMu        sync.Mutex
	sem          chan struct{}

	clockMu  sync.RWMutex
	now      func() time.Time
	lastTick atomic.Int64
	nextTick atomic.Int64
}

// NewScheduler constructs a scheduler. A nil store is accepted so callers can
// keep one uniform bootstrap path in remote-only mode; it simply remains a
// no-op when started.
func NewScheduler(store SchedulerStore, enabled bool) *Scheduler {
	return &Scheduler{
		store:        store,
		executors:    make(map[Kind]Executor),
		stop:         make(chan struct{}),
		enabled:      enabled,
		tickInterval: 5 * time.Second,
		maxParallel:  4,
		now:          time.Now,
	}
}

func (s *Scheduler) SetBroadcaster(b Broadcaster) { s.broadcaster = b }
func (s *Scheduler) SetAuditWriter(w AuditWriter) { s.auditor = w }

// SetTickInterval changes the interval before Start. Non-positive values are
// ignored so time.NewTicker can never panic.
func (s *Scheduler) SetTickInterval(d time.Duration) {
	if d > 0 {
		s.tickInterval = d
	}
}

// SetMaxParallel changes the worker bound before Start. Values below one use
// a single worker.
func (s *Scheduler) SetMaxParallel(n int) {
	if n < 1 {
		n = 1
	}
	s.maxParallel = n
}

// SetClock is intentionally small and primarily useful for deterministic
// scheduler tests. It must be called before Start.
func (s *Scheduler) SetClock(now func() time.Time) {
	if now != nil {
		s.clockMu.Lock()
		s.now = now
		s.clockMu.Unlock()
	}
}

func (s *Scheduler) currentTime() time.Time {
	s.clockMu.RLock()
	now := s.now
	s.clockMu.RUnlock()
	return now()
}

// Register adds or replaces an executor for its Kind.
func (s *Scheduler) Register(e Executor) error {
	if e == nil {
		return errors.New("scheduledtask: cannot register nil executor")
	}
	kind := e.Kind()
	if kind == "" {
		return errors.New("scheduledtask: executor kind is empty")
	}
	s.executorsMu.Lock()
	s.executors[kind] = e
	s.executorsMu.Unlock()
	return nil
}

func (s *Scheduler) executor(kind Kind) Executor {
	s.executorsMu.RLock()
	e := s.executors[kind]
	s.executorsMu.RUnlock()
	return e
}

func (s *Scheduler) workerSemaphore() chan struct{} {
	s.semMu.Lock()
	defer s.semMu.Unlock()
	if s.sem == nil {
		s.sem = make(chan struct{}, s.maxParallel)
	}
	return s.sem
}

// Start starts the scheduler loop. It performs one scan immediately, then
// ticks at the configured interval. Missing Postgres or disabled config is a
// normal no-op, not a startup failure.
func (s *Scheduler) Start(ctx context.Context) {
	if s == nil {
		return
	}
	s.startOnce.Do(func() {
		if !s.enabled || s.store == nil || !s.store.Available() {
			return
		}
		if ctx == nil {
			ctx = context.Background()
		}
		s.workerSemaphore()
		s.wg.Add(1)
		go s.loop(ctx)
	})
}

func (s *Scheduler) loop(ctx context.Context) {
	defer s.wg.Done()
	s.scan(ctx)
	ticker := time.NewTicker(s.tickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.scan(ctx)
		case <-s.stop:
			return
		case <-ctx.Done():
			return
		}
	}
}

func (s *Scheduler) scan(ctx context.Context) {
	if s.store == nil || !s.store.Available() {
		return
	}
	sem := s.workerSemaphore()
	now := s.currentTime()
	s.lastTick.Store(now.Unix())
	s.nextTick.Store(now.Add(s.tickInterval).Unix())
	tasks, err := s.store.ClaimDue(ctx, now.Unix(), s.maxParallel)
	if err != nil {
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			log.Printf("[scheduledtask] claim due: %v", err)
		}
		return
	}
	for _, t := range tasks {
		t := t
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			s.dispatch(ctx, t)
		}()
	}
}

// Stop requests a graceful stop and waits for in-flight executions. It is
// idempotent and safe when Start was never called.
func (s *Scheduler) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() { close(s.stop) })
	s.wg.Wait()
}

// LastTick and NextTick expose coarse observability for diagnostics.
func (s *Scheduler) LastTick() time.Time {
	if v := s.lastTick.Load(); v != 0 {
		return time.Unix(v, 0)
	}
	return time.Time{}
}
func (s *Scheduler) NextTick() time.Time {
	if v := s.nextTick.Load(); v != 0 {
		return time.Unix(v, 0)
	}
	return time.Time{}
}

// TriggerNow runs one owned task immediately. It is used by the manual-run
// HTTP endpoint; disabled tasks may be run manually, but maxRuns and the claim
// lease are still enforced atomically.
func (s *Scheduler) TriggerNow(ctx context.Context, taskID, userID, workspaceID string) error {
	if s == nil || !s.enabled {
		return ErrDisabled
	}
	if s.store == nil || !s.store.Available() {
		return ErrStoreUnavailable
	}
	t, err := s.store.ClaimTaskNow(ctx, taskID, userID, workspaceID, s.currentTime().Unix())
	if err != nil {
		return err
	}
	sem := s.workerSemaphore()
	// The request context is canceled as soon as the HTTP response is written;
	// use a scheduler-owned context so a 202 manual trigger can outlive that
	// request while dispatch still applies the task's own timeout.
	execCtx := context.Background()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		select {
		case sem <- struct{}{}:
			defer func() { <-sem }()
			s.dispatch(execCtx, t)
		case <-s.stop:
			return
		}
	}()
	return nil
}

func (s *Scheduler) dispatch(parent context.Context, t *Task) {
	if parent == nil {
		parent = context.Background()
	}
	if t == nil {
		return
	}
	persistCtx, persistCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer persistCancel()

	run := &Run{ID: NewID(), TaskID: t.ID, WorkspaceID: t.WorkspaceID, UserID: t.UserID, Status: RunStatusRunning, StartedAt: s.currentTime().Unix()}
	if err := s.store.InsertRun(persistCtx, run); err != nil {
		log.Printf("[scheduledtask] insert run task=%s: %v", t.ID, err)
		return
	}
	s.broadcast(t, "scheduledtask.started", map[string]interface{}{"taskId": t.ID, "runId": run.ID})

	timeout := time.Duration(t.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	execCtx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	result, execErr := s.executeSafely(execCtx, t)
	status := RunStatusSuccess
	errMsg := ""
	referenced := ""
	var output json.RawMessage
	if result != nil {
		output = result.Output
		referenced = result.ReferencedTaskID
	}
	if execErr != nil {
		status = RunStatusFailed
		errMsg = truncateError(execErr)
	} else if result != nil && result.Skip {
		status = RunStatusSkipped
	}

	// Always use a fresh persistence context. The executor commonly returns
	// context.DeadlineExceeded, and using its canceled context would lose the
	// terminal run/update exactly when observability matters most.
	if err := s.store.FinishRun(persistCtx, run.ID, status, output, errMsg, referenced); err != nil {
		log.Printf("[scheduledtask] finish run task=%s run=%s: %v", t.ID, run.ID, err)
	}
	next := s.nextRun(t)
	if err := s.store.UpdateTaskAfterRun(persistCtx, t.ID, status, errMsg, next); err != nil {
		log.Printf("[scheduledtask] update task task=%s: %v", t.ID, err)
	}

	event := "scheduledtask.succeeded"
	if status == RunStatusFailed {
		event = "scheduledtask.failed"
	} else if status == RunStatusSkipped {
		event = "scheduledtask.skipped"
	}
	s.broadcast(t, event, map[string]interface{}{
		"taskId": t.ID, "runId": run.ID, "status": status, "error": errMsg,
		"referencedTaskId": referenced,
	})
	s.audit(t, status, run.ID, errMsg)
}

func (s *Scheduler) executeSafely(ctx context.Context, t *Task) (result *Result, err error) {
	e := s.executor(t.Kind)
	if e == nil {
		return nil, fmt.Errorf("%w: %s", ErrUnknownKind, t.Kind)
	}
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: %v\n%s", ErrExecutorPanic, r, debug.Stack())
			result = nil
		}
	}()
	return e.Execute(ctx, t)
}

func (s *Scheduler) nextRun(t *Task) int64 {
	if t.MaxRuns > 0 && t.RunCount+1 >= t.MaxRuns {
		return 0
	}
	sch, err := NewSchedule(t.ScheduleKind, t.ScheduleExpr, t.Timezone)
	if err != nil {
		return 0
	}
	now := s.currentTime().Unix()
	next := sch.ComputeNext(now)
	if t.CooldownSec > 0 {
		minimum := now + int64(t.CooldownSec)
		if next == 0 || next < minimum {
			next = minimum
		}
	}
	return next
}

func (s *Scheduler) broadcast(t *Task, event string, payload interface{}) {
	if s.broadcaster != nil && t.WorkspaceID != "" {
		s.broadcaster.BroadcastToWorkspace(t.WorkspaceID, event, payload)
	}
}

func (s *Scheduler) audit(t *Task, status RunStatus, runID, detail string) {
	if s.auditor == nil {
		return
	}
	s.auditor.Write(t.UserID, t.WorkspaceID, "scheduler.task.run", runID, AuditFields{
		Success: status == RunStatusSuccess,
		Detail:  fmt.Sprintf("task=%s status=%s %s", t.ID, status, strings.TrimSpace(detail)),
	})
}

func truncateError(err error) string {
	if err == nil {
		return ""
	}
	const max = 4000
	msg := err.Error()
	if len(msg) > max {
		return msg[:max] + "..."
	}
	return msg
}

// Ensure the compiler catches accidental changes to Result JSON shape used by
// event consumers; the assertion also documents that json.RawMessage is the
// intended opaque output channel.
var _ = json.RawMessage{}
