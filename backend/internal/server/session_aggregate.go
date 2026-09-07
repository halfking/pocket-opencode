package server

import (
	"context"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/adapter"
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
		go func() {
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
		}()
	}

	go func() {
		wg.Wait()
		close(ch)
	}()

	var all []adapter.OpenCodeSession
	for b := range ch {
		all = append(all, b.sessions...)
	}
	sortSessionsByUpdated(all)
	return all
}
