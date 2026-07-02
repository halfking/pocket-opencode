// Package tasksync periodically pulls tasks from ACC (via MCP client) and
// caches them in the local PG task store. This gives the UI fast local
// reads while keeping the source of truth at the ACC system.
package tasksync

import (
	"context"
	"log"
	"strings"
	"time"

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

// Start launches the background goroutine. Call once from main.
func (s *Scheduler) Start(ctx context.Context) {
	if s.mcpClient == nil || s.taskStore == nil {
		log.Println("[tasksync] disabled (mcpClient or taskStore not configured)")
		return
	}
	go s.loop(ctx)
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
	for {
		select {
		case <-s.stop:
			return
		case <-ctx.Done():
			return
		case <-t.C:
			s.runOnce(ctx)
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
		return
	}

	now := time.Now()
	for _, p := range parsed {
		t := task.Task{
			ID:               p.ID,
			Title:            p.Title,
			Status:           p.Status,
			Priority:         "normal",
			Source:           "acc",
			CreatedAt:        now,
			UpdatedAt:        now,
		}
		if err := s.taskStore.CreateTask(ctx, &t); err != nil {
			// PostgreSQL UNIQUE 冲突错误码 23505 = 已存在，静默跳过
			// 其他错误（连接断开、schema 问题等）记录日志，避免静默吞掉真实故障
			errStr := err.Error()
			if !strings.Contains(errStr, "23505") && !strings.Contains(errStr, "duplicate key") {
				log.Printf("[tasksync] create ACC task %s failed: %v", p.ID, err)
			}
			continue
		}
	}
	log.Printf("[tasksync] synced %d ACC tasks", len(parsed))
}