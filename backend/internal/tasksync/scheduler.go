// Package tasksync periodically pulls tasks from ACC (via MCP client) and
// caches them in the local PG task store. This gives the UI fast local
// reads while keeping the source of truth at the ACC system.
package tasksync

import (
	"context"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/background"
	"github.com/halfking/pocket-opencode/backend/internal/mcp"
	"github.com/halfking/pocket-opencode/backend/internal/task"
)

// Scheduler runs a background ticker that fetches ACC tasks at a fixed
// interval and upserts them into the local task store.
type Scheduler struct {
	mcpClient  *mcp.Client
	taskStore  *task.Store
	interval   time.Duration
	stop       chan struct{}
	lastErrLog time.Time
	// commands reads ACC's runtime-control face. Optional (nil ⇒ the
	// ACC-bound status backfill is skipped): it is what lets a delegated
	// task's card leave 「进行中」 once the dispatch is really over.
	commands CommandStatusReader
	// statusInterval is the cadence of the ACC-bound status backfill.
	//
	// It is deliberately **shorter** than the task-list sync interval:
	// the list sync replays ACC's kanban (5min is fine for a list), while
	// the backfill answers "did the run I delegated just finish?" — and
	// the App's card is watched live. 15s keeps the card honest without
	// hammering ACC (only rows that are still active are read).
	statusInterval time.Duration
}

// New creates a scheduler. Pass nil mcpClient to disable (will be a no-op).
func New(client *mcp.Client, store *task.Store, interval time.Duration) *Scheduler {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	return &Scheduler{
		mcpClient: client,
		taskStore: store,
		interval:  interval,
		stop:      make(chan struct{}),
	}
}

// WithCommandReader wires the ACC runtime-control reader used to backfill
// the status of delegated tasks. Returns the receiver so it can be chained
// onto New(...).
func (s *Scheduler) WithCommandReader(r CommandStatusReader) *Scheduler {
	if s != nil && r != nil {
		s.commands = r
	}
	return s
}

// WithStatusInterval sets the cadence of the ACC-bound status backfill.
// 0 ⇒ the 15s default.
func (s *Scheduler) WithStatusInterval(d time.Duration) *Scheduler {
	if s != nil && d > 0 {
		s.statusInterval = d
	}
	return s
}

// Start launches the background goroutine. Call once from main.
func (s *Scheduler) Start(ctx context.Context) {
	if s.mcpClient == nil || s.taskStore == nil {
		log.Println("[tasksync] disabled (mcpClient or taskStore not configured)")
		return
	}
	background.Go1("tasksync/loop", s.loop, ctx)
	log.Printf("[tasksync] started, interval=%s", s.interval)
}

// Stop signals the loop to exit.
func (s *Scheduler) Stop() {
	close(s.stop)
}

func (s *Scheduler) loop(ctx context.Context) {
	// 启动后立即同步一次
	s.runOnce(ctx)

	t := time.NewTicker(s.interval)
	defer t.Stop()
	statusEvery := s.statusInterval
	if statusEvery <= 0 {
		statusEvery = 15 * time.Second
	}
	st := time.NewTicker(statusEvery)
	defer st.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ctx.Done():
			return
		case <-t.C:
			s.runOnce(ctx)
		case <-st.C:
			// 只回填「还在活动」的 ACC 绑定行：一次很小的查询，
			// 读不到就跳过（读不到 ≠ 失败）。
			if n := s.refreshACCDispatchStatuses(ctx); n > 0 {
				log.Printf("[tasksync] acc-bound task status backfilled: %d row(s)", n)
			}
		}
	}
}

// runOnce fetches ACC tasks once and upserts to local store.
func (s *Scheduler) runOnce(ctx context.Context) {
	fetchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	parsed, err := s.mcpClient.GetRemoteTasks(fetchCtx, "", 500)
	if err != nil {
		// 节流日志，避免 ACC 故障时刷屏
		if time.Since(s.lastErrLog) > time.Minute {
			log.Printf("[tasksync] fetch ACC tasks failed: %v", err)
			s.lastErrLog = time.Now()
		}
		recordAudit("", systemTenantID(), "tasksync.sync.error", "acc_task:",
			AuditFields{Success: false, Detail: "fetch_failed"})
		return
	}

	now := time.Now()
	saved := 0
	for _, p := range parsed {
		t := task.Task{
			ID:        p.ID,
			Title:     p.Title,
			Status:    p.Status,
			Priority:  "normal",
			Source:    "acc",
			CreatedAt: now,
			UpdatedAt: now,
		}
		// Upsert 而非纯 INSERT：ACC 任务每个周期都会重放，纯 INSERT 从第二个
		// 周期起持续触发 PG duplicate key（tasks_pkey）——调用方虽按 23505
		// 静默跳过，但 PG 服务端日志每次都记录（2026-09-08~09 累计 508 条）。
		// Upsert 同时让本地缓存跟随 ACC 的 title/status 变化。
		if err := s.taskStore.UpsertTask(ctx, &t); err != nil {
			// PostgreSQL UNIQUE 冲突错误码 23505 = 已存在，静默跳过
			// 其他错误（连接断开、schema 问题等）记录日志，避免静默吞掉真实故障
			errStr := err.Error()
			if !strings.Contains(errStr, "23505") && !strings.Contains(errStr, "duplicate key") {
				log.Printf("[tasksync] upsert ACC task %s failed: %v", p.ID, err)
				recordAudit("", systemTenantID(), "tasksync.sync.error", "acc_task:"+p.ID,
					AuditFields{Success: false, Detail: "create_failed"})
			}
			continue
		}
		saved++
	}
	log.Printf("[tasksync] synced %d ACC tasks", len(parsed))
	recordAudit("", systemTenantID(), "tasksync.sync", "acc_task:batch",
		AuditFields{Success: true, Detail: countDetail(len(parsed), saved)})

	// ACC 派单的状态回填：把本地「委托给 ACC」那一行的状态跟着命令面走。
	// 这一步独立于上面的清单同步 —— 委托出来的任务**不在** ACC 的看板清单里
	// （实测），它的终态只存在于命令面。
	//
	// 计数不是裸丢（§947 卡口）：真的改了行才留一条日志，
	// 否则每 5 分钟一条「改了 0 行」纯属噪音。
	if n := s.refreshACCDispatchStatuses(ctx); n > 0 {
		log.Printf("[tasksync] acc-bound task status backfilled: %d row(s)", n)
	}
}

// systemTenantID 与 server.AuditSystemTenant 同语义；tasksync 不依赖
// server 包，因此在这里复制常量字符串。两处必须在 CI 期间保持一致
// （通过 grep 检查两边字符串）。
func systemTenantID() string { return "system:acc" }

func countDetail(parsed, saved int) string {
	return "parsed=" + strconv.Itoa(parsed) + " saved=" + strconv.Itoa(saved)
}
