package agent

// registry.go — Agent Registry
//
// 按 AgentRef 分发到正确的 AgentAdapter 实例。
//
// 用法：
//   reg := agent.NewRegistry()
//   reg.Register(AgentRef{Type:"opencode", Target:"http://localhost:4096"}, opencodeAdapter)
//   reg.Register(AgentRef{Type:"acp-stdio", Target:"/usr/local/bin/codex"}, stdioAdapter)
//
//   adapter, ok := reg.Get(AgentRef{...})   // lookup
//   adapter, ok, agentRef := reg.GetByInstanceID("inst-1")  // legacy: map instance_id → agent

import (
	"context"
	"fmt"
	"sync"
)

// Registry 管理多个 AgentAdapter 实例。
//
// 线程安全：所有方法都可并发调用。
type Registry struct {
	mu       sync.RWMutex
	adapters map[AgentRef]AgentAdapter

	// InstanceID → AgentRef 映射（兼容旧 `instance_id` query 参数）。
	// 当 OpenCode HTTP path 还没迁移时，handler 可以用 GetByInstanceID 解析。
	instanceMap map[string]AgentRef
}

// NewRegistry 构造。
func NewRegistry() *Registry {
	return &Registry{
		adapters:    make(map[AgentRef]AgentAdapter),
		instanceMap: make(map[string]AgentRef),
	}
}

// Register 注册 adapter + 可选 instance_id 别名。
func (r *Registry) Register(ref AgentRef, adapter AgentAdapter, instanceID ...string) error {
	if !ref.IsValid() {
		return fmt.Errorf("invalid agent ref: %s", ref)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.adapters[ref] = adapter
	for _, id := range instanceID {
		r.instanceMap[id] = ref
	}
	return nil
}

// Unregister 移除 adapter。
func (r *Registry) Unregister(ref AgentRef) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.adapters, ref)
	// 同时清理 instanceMap
	for id, mapped := range r.instanceMap {
		if mapped == ref {
			delete(r.instanceMap, id)
		}
	}
}

// Get 按 AgentRef 查找 adapter。
func (r *Registry) Get(ref AgentRef) (AgentAdapter, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.adapters[ref]
	return a, ok
}

// GetByInstanceID 按 instance_id 查找 adapter（兼容旧 OpenCode 路径）。
//
// 如果 instanceID 不在 map 里，返回 zero ref + false。
func (r *Registry) GetByInstanceID(instanceID string) (AgentAdapter, AgentRef, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ref, ok := r.instanceMap[instanceID]
	if !ok {
		return nil, AgentRef{}, false
	}
	a, ok := r.adapters[ref]
	if !ok {
		return nil, AgentRef{}, false
	}
	return a, ref, true
}

// All 列出所有注册的 adapter（用于 diagnostics / admin endpoint）。
func (r *Registry) All() []RegisteredAdapter {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]RegisteredAdapter, 0, len(r.adapters))
	for ref, a := range r.adapters {
		out = append(out, RegisteredAdapter{
			Ref:     ref,
			Adapter: a,
		})
	}
	return out
}

// RegisteredAdapter 是 registry 条目（含 ref + adapter）。
type RegisteredAdapter struct {
	Ref     AgentRef
	Adapter AgentAdapter
}

// HealthCheckAll 检查所有注册的 adapter（并行）。
//
// 返回每个 ref 的状态（up/down）+ 错误。用于 /api/diagnostics/agents 端点。
func (r *Registry) HealthCheckAll(ctx context.Context) map[AgentRef]HealthStatus {
	r.mu.RLock()
	refs := make([]AgentRef, 0, len(r.adapters))
	adapters := make(map[AgentRef]AgentAdapter, len(r.adapters))
	for ref, a := range r.adapters {
		refs = append(refs, ref)
		adapters[ref] = a
	}
	r.mu.RUnlock()

	out := make(map[AgentRef]HealthStatus, len(refs))
	var (
		wg sync.WaitGroup
		mu sync.Mutex
	)
	for _, ref := range refs {
		wg.Add(1)
		go func(ref AgentRef) {
			defer wg.Done()
			status := HealthStatus{Ref: ref}
			if err := adapters[ref].HealthCheck(ctx, ref); err != nil {
				status.Error = err.Error()
				status.Up = false
			} else {
				status.Up = true
			}
			mu.Lock()
			out[ref] = status
			mu.Unlock()
		}(ref)
	}
	wg.Wait()
	return out
}

// HealthStatus 是单个 adapter 的健康状态。
type HealthStatus struct {
	Ref   AgentRef
	Up    bool
	Error string
}
