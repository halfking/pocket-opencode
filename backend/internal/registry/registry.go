package registry

import (
	"encoding/json"
	"fmt"
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

// Registry 实例注册表
type Registry struct {
	mu        sync.RWMutex
	instances map[string]*model.PocketInstance
	apiURLMap map[string]string // instanceID -> apiBaseURL
}

func NewRegistry() *Registry {
	return &Registry{
		instances: make(map[string]*model.PocketInstance),
		apiURLMap: make(map[string]string),
	}
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

// UpdateInstanceHealth 更新实例健康状态
func (r *Registry) UpdateInstanceHealth(instanceID string, health string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if instance, ok := r.instances[instanceID]; ok {
		instance.Health = health
		instance.LastHeartbeatAt = time.Now().UTC().Format(time.RFC3339)
	}
}

// ParseConfigJSON 从 JSON 字符串解析实例配置
func ParseConfigJSON(jsonStr string) ([]InstanceConfig, error) {
	var configs []InstanceConfig
	if err := json.Unmarshal([]byte(jsonStr), &configs); err != nil {
		return nil, fmt.Errorf("failed to parse instance config: %w", err)
	}
	return configs, nil
}
