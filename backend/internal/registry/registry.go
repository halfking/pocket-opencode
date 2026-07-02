package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/model"
)

// InstanceConfig 实例配置（从环境变量或配置文件加载）
type InstanceConfig struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	NPSClientID int    `json:"npsClientId"`
	NPSHost     string `json:"npsHost"`
	APIBaseURL  string `json:"apiBaseURL"`
	Environment string `json:"environment"`
}

// Registry 实例注册表（增强版）
type Registry struct {
	mu        sync.RWMutex
	instances map[string]*model.PocketInstance
	apiURLMap map[string]string // instanceID -> apiBaseURL
	
	// 新增：自动发现和心跳
	discoveryEnabled bool
	heartbeatInterval time.Duration
	discoveryFunc    DiscoveryFunc
}

// DiscoveryFunc 实例发现函数类型
type DiscoveryFunc func(ctx context.Context) ([]InstanceConfig, error)

func NewRegistry() *Registry {
	return &Registry{
		instances:         make(map[string]*model.PocketInstance),
		apiURLMap:         make(map[string]string),
		discoveryEnabled:  false,
		heartbeatInterval: 30 * time.Second,
	}
}

// EnableAutoDiscovery 启用自动发现
func (r *Registry) EnableAutoDiscovery(discoveryFunc DiscoveryFunc, interval time.Duration) {
	r.mu.Lock()
	r.discoveryEnabled = true
	r.discoveryFunc = discoveryFunc
	r.heartbeatInterval = interval
	r.mu.Unlock()
}

// StartAutoDiscovery 启动自动发现和健康检查
func (r *Registry) StartAutoDiscovery(ctx context.Context) {
	if !r.discoveryEnabled || r.discoveryFunc == nil {
		log.Println("⚠️ 自动发现未启用")
		return
	}

	log.Printf("✅ 启动 OpenCode 实例自动发现（间隔: %v）", r.heartbeatInterval)

	// 立即执行一次发现
	r.discoverAndUpdate(ctx)

	// 定时发现和健康检查
	ticker := time.NewTicker(r.heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("🛑 停止实例自动发现")
			return
		case <-ticker.C:
			r.discoverAndUpdate(ctx)
			r.healthCheck(ctx)
		}
	}
}

// discoverAndUpdate 发现并更新实例
func (r *Registry) discoverAndUpdate(ctx context.Context) {
	configs, err := r.discoveryFunc(ctx)
	if err != nil {
		log.Printf("⚠️ 实例发现失败: %v", err)
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// 标记所有现有实例为待验证
	discovered := make(map[string]bool)

	// 更新或添加发现的实例
	for _, cfg := range configs {
		discovered[cfg.ID] = true

		if existing, ok := r.instances[cfg.ID]; ok {
			// 更新现有实例
			existing.DisplayName = cfg.DisplayName
			existing.Environment = cfg.Environment
			existing.NPSClientID = cfg.NPSClientID
		} else {
			// 添加新实例
			instance := &model.PocketInstance{
				ID:              cfg.ID,
				DisplayName:     cfg.DisplayName,
				Environment:     cfg.Environment,
				NPSClientID:     cfg.NPSClientID,
				Capabilities:    []string{"session", "summary", "pty"},
				Health:          "unknown",
				LastHeartbeatAt: time.Now().UTC().Format(time.RFC3339),
			}
			r.instances[cfg.ID] = instance
			r.apiURLMap[cfg.ID] = cfg.APIBaseURL
			log.Printf("✅ 发现新实例: %s (%s)", cfg.DisplayName, cfg.ID)
		}
	}

	// 标记未发现的实例为离线
	for id, instance := range r.instances {
		if !discovered[id] {
			instance.Health = "offline"
			log.Printf("⚠️ 实例离线: %s (%s)", instance.DisplayName, id)
		}
	}
}

// healthCheck 健康检查所有实例
func (r *Registry) healthCheck(ctx context.Context) {
	r.mu.RLock()
	instances := make([]string, 0, len(r.instances))
	urls := make(map[string]string)
	
	for id, apiURL := range r.apiURLMap {
		instances = append(instances, id)
		urls[id] = apiURL
	}
	r.mu.RUnlock()

	// 并发检查所有实例
	var wg sync.WaitGroup
	for _, instanceID := range instances {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			
			apiURL := urls[id]
			health := r.checkInstanceHealth(ctx, apiURL)
			
			r.mu.Lock()
			if instance, ok := r.instances[id]; ok {
				instance.Health = health
				instance.LastHeartbeatAt = time.Now().UTC().Format(time.RFC3339)
			}
			r.mu.Unlock()
		}(instanceID)
	}
	
	wg.Wait()
}

// checkInstanceHealth 检查单个实例健康状态
func (r *Registry) checkInstanceHealth(ctx context.Context, apiBaseURL string) string {
	// 修正：使用实际的 OpenCode API 端点 /api/health
	endpoint := apiBaseURL + "/api/health"

	client := &http.Client{Timeout: 3 * time.Second}
	
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "unhealthy"
	}

	resp, err := client.Do(req)
	if err != nil {
		return "unhealthy"
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "unhealthy"
	}

	// 验证响应格式：{ "healthy": true }
	var result struct {
		Healthy bool `json:"healthy"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "unhealthy"
	}

	if result.Healthy {
		return "healthy"
	}

	return "unhealthy"
}

// RegisterInstance 手动注册实例（支持动态注册）
func (r *Registry) RegisterInstance(instance *model.PocketInstance) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if instance.ID == "" {
		return fmt.Errorf("instance ID is required")
	}

	r.instances[instance.ID] = instance
	log.Printf("✅ 手动注册实例: %s (%s)", instance.DisplayName, instance.ID)
	
	return nil
}

// SetInstanceAPIBase 设置实例的 API 地址
func (r *Registry) SetInstanceAPIBase(instanceID, apiBaseURL string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.apiURLMap[instanceID] = apiBaseURL
}

// LoadFromConfig 从配置加载实例（手动配置模式）
func (r *Registry) LoadFromConfig(configs []InstanceConfig) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, cfg := range configs {
		instance := &model.PocketInstance{
			ID:              cfg.ID,
			DisplayName:     cfg.DisplayName,
			Environment:     cfg.Environment,
			NPSClientID:     cfg.NPSClientID,
			Capabilities:    []string{"session", "summary", "pty"},
			Health:          "unknown",
			LastHeartbeatAt: time.Now().UTC().Format(time.RFC3339),
		}
		r.instances[cfg.ID] = instance
		r.apiURLMap[cfg.ID] = cfg.APIBaseURL
		log.Printf("✅ 加载配置实例: %s (%s)", cfg.DisplayName, cfg.ID)
	}

	return nil
}

// GetInstanceAPIBase 根据实例 ID 获取 API base URL
func (r *Registry) GetInstanceAPIBase(instanceID string) (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	apiURL, ok := r.apiURLMap[instanceID]
	if !ok {
		return "", fmt.Errorf("instance not found: %s", instanceID)
	}
	return apiURL, nil
}

// GetInstance 获取实例信息
func (r *Registry) GetInstance(instanceID string) (*model.PocketInstance, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	instance, ok := r.instances[instanceID]
	if !ok {
		return nil, fmt.Errorf("instance not found: %s", instanceID)
	}
	return instance, nil
}

// ListInstances 列出所有实例
func (r *Registry) ListInstances() []model.PocketInstance {
	r.mu.RLock()
	defer r.mu.RUnlock()

	instances := make([]model.PocketInstance, 0, len(r.instances))
	for _, instance := range r.instances {
		instances = append(instances, *instance)
	}
	return instances
}

// ListHealthyInstances 列出所有健康的实例
func (r *Registry) ListHealthyInstances() []model.PocketInstance {
	r.mu.RLock()
	defer r.mu.RUnlock()

	instances := make([]model.PocketInstance, 0)
	for _, instance := range r.instances {
		if instance.Health == "healthy" {
			instances = append(instances, *instance)
		}
	}
	return instances
}

// UpdateInstanceHealth 更新实例健康状态
func (r *Registry) UpdateInstanceHealth(instanceID string, health string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if instance, ok := r.instances[instanceID]; ok {
		instance.Health = health
		instance.LastHeartbeatAt = time.Now().UTC().Format(time.RFC3339)
	}
}

// UnregisterInstance 注销实例
func (r *Registry) UnregisterInstance(instanceID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.instances, instanceID)
	delete(r.apiURLMap, instanceID)
	log.Printf("✅ 注销实例: %s", instanceID)
}

// ParseConfigJSON 从 JSON 字符串解析实例配置
func ParseConfigJSON(jsonStr string) ([]InstanceConfig, error) {
	var configs []InstanceConfig
	if err := json.Unmarshal([]byte(jsonStr), &configs); err != nil {
		return nil, fmt.Errorf("failed to parse instance config: %w", err)
	}
	return configs, nil
}
