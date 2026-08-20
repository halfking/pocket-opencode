package tasksync

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/adapter/disk"
	"github.com/halfking/pocket-opencode/backend/internal/mcp"
)

// DiskSessionScheduler 周期性地把本机 disk 适配器检测到的会话上报给 ACC
// （经 mcp.Client.ReportSession），让 ACC 侧归集本地聚合会话。
//
// 与任务拉取调度器（Scheduler）同形：5 分钟一次、启动即跑一次、best-effort。
// 上报是幂等的（按 session 的 UpdatedAt 去重：未变化则跳过），避免每次
// 轮询都把整盘会话重报一遍。磁盘读取严格只读，本调度器不写任何 agent 数据。
type DiskSessionScheduler struct {
	diskAdapter *disk.Adapter
	mcpClient   *mcp.Client
	interval    time.Duration
	stop        chan struct{}

	mu       sync.Mutex
	reported map[string]int64 // "<instanceID>:<sessionID>" → 上次上报的 UpdatedAt(ms)
	lastErr  time.Time
}

// NewDiskSessionScheduler 构造调度器。diskAdapter 或 mcpClient 为 nil 时
// Start 自动降级为 no-op。
func NewDiskSessionScheduler(d *disk.Adapter, c *mcp.Client, interval time.Duration) *DiskSessionScheduler {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	return &DiskSessionScheduler{
		diskAdapter: d,
		mcpClient:   c,
		interval:    interval,
		stop:        make(chan struct{}),
		reported:    make(map[string]int64),
	}
}

// Start 启动后台循环。diskAdapter / mcpClient 缺失时直接 no-op。
func (s *DiskSessionScheduler) Start(ctx context.Context) {
	if s.diskAdapter == nil || s.mcpClient == nil {
		log.Println("[disksession-sync] disabled (disk adapter or mcp client not configured)")
		return
	}
	go s.loop(ctx)
	log.Printf("[disksession-sync] started, interval=%s", s.interval)
}

// Stop 通知循环退出。
func (s *DiskSessionScheduler) Stop() {
	close(s.stop)
}

func (s *DiskSessionScheduler) loop(ctx context.Context) {
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

// runOnce 上报当前检测到的所有 disk 实例会话（幂等去重）。
func (s *DiskSessionScheduler) runOnce(ctx context.Context) {
	for _, inst := range s.diskAdapter.DetectedInstances() {
		metas, err := s.diskAdapter.ListSessionMetas(ctx, inst.Locator)
		if err != nil {
			if time.Since(s.lastErr) > time.Minute {
				log.Printf("[disksession-sync] list %s failed: %v", inst.InstanceID, err)
				s.lastErr = time.Now()
			}
			continue
		}
		for _, m := range metas {
			key := inst.InstanceID + ":" + m.ID
			s.mu.Lock()
			seen := s.reported[key]
			if m.UpdatedAt != 0 && m.UpdatedAt == seen {
				s.mu.Unlock()
				continue // 未变化，跳过
			}
			s.reported[key] = m.UpdatedAt
			s.mu.Unlock()

			args := map[string]interface{}{
				"gateway_type": inst.Agent,
				"device_name":  inst.DisplayName,
				"agent_id":     inst.Agent,
				"session_id":   m.ID,
				"task_id":      "",
				"input":        m.Title,
				"output":       "",
				"model_id":     m.Model,
			}
			if _, err := s.mcpClient.ReportSession(ctx, args); err != nil {
				if time.Since(s.lastErr) > time.Minute {
					log.Printf("[disksession-sync] report %s/%s failed: %v", inst.InstanceID, m.ID, err)
					s.lastErr = time.Now()
				}
			}
		}
	}
}
