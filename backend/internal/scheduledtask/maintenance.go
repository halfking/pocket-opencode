package scheduledtask

import (
	"context"
	"fmt"
	"log"
	"time"
)

// MaintenanceConfig 配置维护任务的行为
type MaintenanceConfig struct {
	// StaleRunThreshold 定义多久后将 running 状态的 run 视为 stale
	// 默认为 1 小时
	StaleRunThreshold time.Duration
	
	// DryRun 如果为 true，只报告 stale runs 但不修改数据库
	DryRun bool
	
	// Logger 用于记录维护操作
	Logger *log.Logger
}

// MaintenanceResult 包含维护操作的统计信息
type MaintenanceResult struct {
	StaleRunsFound    int
	StaleRunsMarked   int
	StaleRunsSkipped  int
	TasksRecovered    int
	Errors            []error
}

// RunMaintenance 执行定时任务系统的维护操作：
// 1. 识别过期的 running runs（started_at 超过阈值且 lease 已过期）
// 2. 将它们标记为 abandoned
// 3. 如果关联的 task lease 也已过期，清除 lease 使任务可被重新调度
func (s *Store) RunMaintenance(ctx context.Context, cfg MaintenanceConfig) (*MaintenanceResult, error) {
	if !s.Available() {
		return nil, ErrStoreUnavailable
	}
	
	if cfg.StaleRunThreshold == 0 {
		cfg.StaleRunThreshold = 1 * time.Hour
	}
	
	result := &MaintenanceResult{}
	
	// 开始事务
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	
	// 查找 stale runs
	staleThreshold := time.Now().Add(-cfg.StaleRunThreshold).Unix()
	query := `
		SELECT r.id, r.task_id, r.started_at, t.lease_until, t.id as task_exists
		FROM scheduled_task_runs r
		LEFT JOIN scheduled_tasks t ON r.task_id = t.id
		WHERE r.status = 'running'
		  AND r.finished_at = 0
		  AND r.started_at < $1
	`
	
	rows, err := tx.Query(ctx, query, staleThreshold)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	
	type staleRun struct {
		runID      string
		taskID     string
		startedAt  int64
		leaseUntil int64
		taskExists string
	}
	
	var staleRuns []staleRun
	for rows.Next() {
		var sr staleRun
		var leaseUntil *int64
		var taskExists *string
		
		if err := rows.Scan(&sr.runID, &sr.taskID, &sr.startedAt, &leaseUntil, &taskExists); err != nil {
			result.Errors = append(result.Errors, err)
			continue
		}
		
		if leaseUntil != nil {
			sr.leaseUntil = *leaseUntil
		}
		if taskExists != nil {
			sr.taskExists = *taskExists
		}
		
		staleRuns = append(staleRuns, sr)
	}
	
	result.StaleRunsFound = len(staleRuns)
	
	if cfg.Logger != nil {
		cfg.Logger.Printf("Found %d stale running runs (threshold: %v)", 
			result.StaleRunsFound, cfg.StaleRunThreshold)
	}
	
	if cfg.DryRun {
		if cfg.Logger != nil {
			for _, sr := range staleRuns {
				elapsed := time.Since(time.Unix(sr.startedAt, 0))
				cfg.Logger.Printf("  [DRY RUN] Would mark run %s (task %s) as abandoned (running for %v)", 
					sr.runID, sr.taskID, elapsed.Round(time.Second))
			}
		}
		result.StaleRunsSkipped = len(staleRuns)
		return result, nil
	}
	
	// 标记 stale runs 为 abandoned
	now := time.Now().Unix()
	for _, sr := range staleRuns {
		updateRunQuery := `
			UPDATE scheduled_task_runs
			SET status = 'failed',
			    error = 'abandoned: exceeded stale threshold',
			    finished_at = $1
			WHERE id = $2 AND status = 'running'
		`
		
		tag, err := tx.Exec(ctx, updateRunQuery, now, sr.runID)
		if err != nil {
			result.Errors = append(result.Errors, err)
			continue
		}
		
		if tag.RowsAffected() > 0 {
			result.StaleRunsMarked++
			
			if cfg.Logger != nil {
				elapsed := time.Since(time.Unix(sr.startedAt, 0))
				cfg.Logger.Printf("  Marked run %s as abandoned (was running for %v)", 
					sr.runID, elapsed.Round(time.Second))
			}
			
				// 如果任务仍然存在且 lease 已过期，清除 lease
				if sr.taskExists != "" && sr.leaseUntil > 0 {
					if sr.leaseUntil < now {
						clearLeaseQuery := `
							UPDATE scheduled_tasks
							SET lease_until = 0
							WHERE id = $1 AND lease_until = $2
						`
						
						tag, err := tx.Exec(ctx, clearLeaseQuery, sr.taskID, sr.leaseUntil)
						if err != nil {
							result.Errors = append(result.Errors, err)
						} else if tag.RowsAffected() > 0 {
							result.TasksRecovered++
							if cfg.Logger != nil {
								cfg.Logger.Printf("    Cleared expired lease for task %s", sr.taskID)
							}
						}
					}
				}
		}
	}
	
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	
	if cfg.Logger != nil {
		cfg.Logger.Printf("Maintenance complete: marked %d runs as abandoned, recovered %d tasks",
			result.StaleRunsMarked, result.TasksRecovered)
	}
	
	return result, nil
}

// MaintenanceTask 可以被调度器周期性调用以执行维护
func (s *Scheduler) RunMaintenanceCycle(ctx context.Context) error {
	store, ok := s.store.(*Store)
	if !ok {
		return nil // 不是 PostgreSQL store，跳过维护
	}
	
	cfg := MaintenanceConfig{
		StaleRunThreshold: 1 * time.Hour,
		DryRun:           false,
		Logger:           log.Default(),
	}
	
	result, err := store.RunMaintenance(ctx, cfg)
	if err != nil {
		return err
	}
	
	// 如果发现并处理了 stale runs，通过 auditor 记录
	if result.StaleRunsMarked > 0 && s.auditor != nil {
		s.auditor.Write(
			"system",
			"system",
			"scheduler.maintenance",
			"scheduled_tasks",
			AuditFields{
				Success: true,
				Detail:  fmt.Sprintf("marked %d stale runs as abandoned, recovered %d tasks", 
					result.StaleRunsMarked, result.TasksRecovered),
			},
		)
	}
	
	return nil
}
