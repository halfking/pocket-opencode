package tasksync

// ACC 派单状态的回填：让「委托给 ACC」出来的本地任务行跟着 ACC 的派单走。
//
// ★ 为什么必须单独做这一步（2026-10-26 实测，不是推测）：
//
//	· 「委托给 ACC」在本地建的是**一行**任务（id=task-xxx，
//	  workspace=调用者的 workspace，source='acc'，acc_dispatch_id=派单 id）。
//	  App 里显示的就是这一行。
//	· ACC 的任务清单面（MCP acc_list_tasks）**不包含**这一行：委托建的是
//	  kind='agent_task/opencode' 的任务，不在看板清单里；而且清单里的行
//	  连 status 字段都没有（实测 status=None）。
//	· 于是这一行的状态**没有任何来源** ⇒ App 里的卡片永远停在「进行中」，
//	  哪怕智能体早已跑完、ACC 的命令早已 succeeded。
//
// 所以权威的终态只能从**命令面**读：GET /api/v2/runtime/commands/{dispatch_id}
// 的 status 就是那次派单的真实结局（succeeded / failed / cancelled / expired）。
//
// 这一步只改**状态**，不碰别的列：标题、归属、验收证据都是本地拥有的。

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/accruntime"
	"github.com/halfking/pocket-opencode/backend/internal/task"
)

// AccCommandStatusReader adapts accruntime.Client to CommandStatusReader.
//
// It lives here (not in the server package) so the wiring in main.go is a
// one-liner and so this package keeps exactly one dependency on the ACC
// transport.
type AccCommandStatusReader struct {
	Client *accruntime.Client
}

// CommandStatus reads one dispatch's status from ACC's runtime-control
// face. An error means "could not read" — it never means "failed".
func (r AccCommandStatusReader) CommandStatus(ctx context.Context, dispatchID string) (string, error) {
	if r.Client == nil {
		return "", errors.New("accruntime: client not configured")
	}
	cmd, err := r.Client.GetCommand(ctx, dispatchID)
	if err != nil {
		return "", err
	}
	return cmd.Status, nil
}

// CommandStatusReader reads the terminal status of an ACC dispatch.
//
// It is declared here (consumer side) and satisfied by a thin adapter over
// accruntime.Client, so this package stays free of the ACC transport.
type CommandStatusReader interface {
	// CommandStatus returns the dispatch's current status. An error means
	// "could not read" — never "failed".
	CommandStatus(ctx context.Context, dispatchID string) (string, error)
}

// accDispatchStatusLocal maps ACC's dispatch status onto the local task
// status vocabulary.
//
// The mapping is deliberately conservative: an unknown ACC status maps to
// "" (no change), because inventing a terminal state from a status we do
// not understand would mark a running task as finished.
func accDispatchStatusLocal(accStatus string) string {
	switch strings.ToLower(strings.TrimSpace(accStatus)) {
	case "succeeded", "success", "completed", "done":
		return "completed"
	case "failed", "error":
		return "failed"
	case "cancelled", "canceled":
		return "cancelled"
	case "expired", "superseded":
		return "cancelled"
	default:
		return ""
	}
}

// refreshACCDispatchStatuses reads the ACC command face for every local
// row that is bound to a dispatch and still active, and writes back the
// terminal status when ACC reports one.
//
// Fail-closed on the read side: an unreadable command leaves the row
// untouched (it may still be running). A row is only moved to a terminal
// state when ACC itself said so.
func (s *Scheduler) refreshACCDispatchStatuses(ctx context.Context) int {
	if s.commands == nil || s.taskStore == nil {
		return 0
	}
	rows, err := s.taskStore.ListACCBoundActiveTasks(ctx)
	if err != nil {
		if time.Since(s.lastErrLog) > time.Minute {
			log.Printf("[tasksync] list acc-bound tasks failed: %v", err)
			s.lastErrLog = time.Now()
		}
		return 0
	}
	updated := 0
	for _, t := range rows {
		dispatchID := strings.TrimSpace(t.ACCDispatchID)
		if dispatchID == "" {
			continue
		}
		readCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		accStatus, err := s.commands.CommandStatus(readCtx, dispatchID)
		cancel()
		if err != nil {
			if time.Since(s.lastErrLog) > time.Minute {
				log.Printf("[tasksync] read acc command %s failed: %v", dispatchID, err)
				s.lastErrLog = time.Now()
			}
			continue
		}
		local := accDispatchStatusLocal(accStatus)
		if local == "" {
			continue
		}
		if _, err := s.taskStore.UpdateTaskScoped(ctx, t.ID, t.WorkspaceID, task.TaskUpdate{
			Status: &local,
		}); err != nil {
			log.Printf("[tasksync] update acc-bound task %s status failed: %v", t.ID, err)
			continue
		}
		updated++
		log.Printf("[tasksync] acc-bound task %s -> %s (dispatch=%s acc_status=%s)",
			t.ID, local, dispatchID, accStatus)
	}
	return updated
}
