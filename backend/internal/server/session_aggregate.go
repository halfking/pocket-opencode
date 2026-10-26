package server

import (
	"context"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/adapter"
	"github.com/halfking/pocket-opencode/backend/internal/background"
	"github.com/halfking/pocket-opencode/backend/internal/model"
)

// shouldAggregateSessions 决定「所有实例」聚合时要不要打这个实例。
// disk 本地源始终扫；discovered/static HTTP 死节点（unknown/offline）会把
// 整页拖到客户端超时，所以只保留 health=healthy 的网络实例。
func shouldAggregateSessions(inst model.PocketInstance) bool {
	if inst.Origin == "disk" || strings.HasPrefix(inst.APIBaseURL, "disk://") {
		return true
	}
	return inst.Health == "healthy"
}

func listSessionsTimeout(apiBase string) time.Duration {
	if strings.HasPrefix(apiBase, "disk://") {
		return 15 * time.Second
	}
	return 3 * time.Second
}

func sortSessionsByUpdated(sessions []adapter.OpenCodeSession) {
	sort.SliceStable(sessions, func(i, j int) bool {
		return sessions[i].TimeUpdated > sessions[j].TimeUpdated
	})
}

func pageSessions(all []adapter.OpenCodeSession, offset, limit int) []adapter.OpenCodeSession {
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		return []adapter.OpenCodeSession{}
	}
	if offset >= len(all) {
		return []adapter.OpenCodeSession{}
	}
	end := offset + limit
	if end > len(all) {
		end = len(all)
	}
	return all[offset:end]
}

func tagSessions(sessions []adapter.OpenCodeSession, inst model.PocketInstance) {
	name := inst.DisplayName
	if name == "" {
		name = inst.ID
	}
	for i := range sessions {
		sessions[i].InstanceID = inst.ID
		sessions[i].InstanceName = name
	}
}

func (s *Server) listSessionsAcrossInstances(ctx context.Context, workspaceID string, instances []model.PocketInstance) []adapter.OpenCodeSession {
	if s.registry == nil || s.opencode == nil {
		return nil
	}

	type batch struct {
		sessions []adapter.OpenCodeSession
	}
	ch := make(chan batch, len(instances))
	var wg sync.WaitGroup

	for _, inst := range instances {
		if !shouldAggregateSessions(inst) {
			continue
		}
		inst := inst
		wg.Add(1)
		background.Go("session/list-instances", func() {
			defer wg.Done()
			apiBase, err := s.registry.GetInstanceAPIBaseForWorkspace(workspaceID, inst.ID)
			if err != nil {
				log.Printf("Failed to get API base for instance %s: %v", inst.ID, err)
				return
			}
			instCtx, cancel := context.WithTimeout(ctx, listSessionsTimeout(apiBase))
			defer cancel()
			sessions, err := s.opencode.ListSessions(instCtx, apiBase)
			if err != nil {
				log.Printf("Failed to list sessions for instance %s: %v", inst.ID, err)
				return
			}
			tagSessions(sessions, inst)
			ch <- batch{sessions: sessions}
		})
	}

	background.Go("session/list-collector", func() {
		wg.Wait()
		close(ch)
	})

	var all []adapter.OpenCodeSession
	for b := range ch {
		all = append(all, b.sessions...)
	}
	sortSessionsByUpdated(all)
	return all
}

// accTasksListTimeout 是「列 ACC 任务」这次远端调用的**预算**。
//
// ★ 2026-10-27 新增：ACC 的 MCP 面会抖（实测 EOF / 超时 / 400 都出现过），
//   而 GET /api/tasks 是 App 的主屏入口 —— 实测事故：ACC 超时让这个接口
//   耗时 41 秒、响应体写不出去 ⇒ 客户端 "other side closed" ⇒ 任务页全废。
//   ⇒ 远端源一律**有预算**：超了就跳过该源，本地源照常返回，
//     ACC 的数据由后台 tasksync（15s 一跳）补齐。
//
// 为什么是 5s：比 OpenCode 源的 3s 宽一点（ACC 那边一次往返经常 1.3~4.2s，
// 实测 200 的耗时在 1.2~4.3s 之间），但远小于会让请求写不出去��量级。
const accTasksListTimeout = 5 * time.Second
