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

// InstanceConfig 实例配置（从环境变量或配置文件加载，或由发现/注册产生）
type InstanceConfig struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	NPSClientID int    `json:"npsClientId"`
	NPSHost     string `json:"npsHost"`
	APIBaseURL  string `json:"apiBaseURL"`
	Environment string `json:"environment"`

	// —— 迁移方案扩展（可选，发现/注册时填充）——
	Hostname string          `json:"hostname,omitempty"` // 主机名
	IP       string          `json:"ip,omitempty"`       // 主 IP
	Port     int             `json:"port,omitempty"`     // 端口
	Version  string          `json:"version,omitempty"`  // 版本
	Machine  model.MachineInfo `json:"machine,omitempty"` // 机器信息
	Origin   string          `json:"origin,omitempty"`   // discovered/registered/static/acc
	// Capabilities 留空时由 Registry 用默认值兜底；探测成功后由 capabilities 探测覆盖
	Capabilities []string `json:"capabilities,omitempty"`
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
			// 更新现有实例（保留 Origin/Health，更新展示与机器信息）
			existing.DisplayName = cfg.DisplayName
			existing.Environment = cfg.Environment
			existing.NPSClientID = cfg.NPSClientID
			if cfg.APIBaseURL != "" {
				existing.APIBaseURL = cfg.APIBaseURL
				r.apiURLMap[cfg.ID] = cfg.APIBaseURL
			}
			applyConfigFields(existing, cfg)
		} else {
			// 添加新实例
			instance := &model.PocketInstance{
				ID:              cfg.ID,
				DisplayName:     cfg.DisplayName,
				Environment:     cfg.Environment,
				NPSClientID:     cfg.NPSClientID,
				Capabilities:    defaultCapabilities(cfg.Capabilities),
				Health:          "unknown",
				LastHeartbeatAt: time.Now().UTC().Format(time.RFC3339),
				APIBaseURL:      cfg.APIBaseURL,
				MigrationStatus: "idle",
			}
			applyConfigFields(instance, cfg)
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

// healthCheck 健康检查所有实例，并在响应包含自描述字段时同步更新
// capabilities/version/machine（Phase 迁移方案：能力真实探测）。
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
			probe := r.checkInstanceHealth(ctx, apiURL)

			r.mu.Lock()
			if instance, ok := r.instances[id]; ok {
				instance.Health = probe.Health
				instance.LastHeartbeatAt = time.Now().UTC().Format(time.RFC3339)
				// 同步自描述字段（仅在实例端提供时覆盖）
				if probe.Version != "" {
					instance.Version = probe.Version
				}
				if len(probe.Capabilities) > 0 {
					instance.Capabilities = probe.Capabilities
				}
				if probe.Machine != (model.MachineInfo{}) {
					instance.Machine = probe.Machine
					if probe.Machine.Hostname != "" {
						instance.Hostname = probe.Machine.Hostname
					}
				}
			}
			r.mu.Unlock()
		}(instanceID)
	}

	wg.Wait()
}

// healthProbe 是 checkInstanceHealth 的结构化返回值。
type healthProbe struct {
	Health       string
	Version      string
	Capabilities []string
	Machine      model.MachineInfo
}

// checkInstanceHealth 检查单个实例健康状态，并尝试从 health 响应中
// 提取 version/capabilities/machine 自描述字段（边端 manager 可挂同一端口提供）。
// OpenCode 真实端点是 GET /global/health（无 /api 前缀）。
func (r *Registry) checkInstanceHealth(ctx context.Context, apiBaseURL string) healthProbe {
	endpoint := apiBaseURL + "/global/health"

	client := &http.Client{Timeout: 3 * time.Second}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return healthProbe{Health: "unhealthy"}
	}

	resp, err := client.Do(req)
	if err != nil {
		return healthProbe{Health: "unhealthy"}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return healthProbe{Health: "unhealthy"}
	}

	var result struct {
		Healthy      bool              `json:"healthy"`
		Status       string            `json:"status"`
		Version      string            `json:"version"`
		Capabilities []string          `json:"capabilities"`
		Machine      model.MachineInfo `json:"machine"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return healthProbe{Health: "unhealthy"}
	}

	health := "unhealthy"
	if result.Healthy || result.Status == "ok" {
		health = "healthy"
	}

	return healthProbe{
		Health:       health,
		Version:      result.Version,
		Capabilities: result.Capabilities,
		Machine:      result.Machine,
	}
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
			Capabilities:    defaultCapabilities(cfg.Capabilities),
			Health:          "unknown",
			LastHeartbeatAt: time.Now().UTC().Format(time.RFC3339),
			APIBaseURL:      cfg.APIBaseURL,
			MigrationStatus: "idle",
		}
		applyConfigFields(instance, cfg)
		if instance.Origin == "" {
			instance.Origin = "static"
		}
		r.instances[cfg.ID] = instance
		r.apiURLMap[cfg.ID] = cfg.APIBaseURL
		log.Printf("✅ 加载配置实例: %s (%s)", cfg.DisplayName, cfg.ID)
	}

	return nil
}

// applyConfigFields 把 InstanceConfig 的新增可选字段（hostname/ip/port/version/machine/origin/capabilities）
// 叠加到 PocketInstance。空值不覆盖已有值（origin 例外：仅在为空时填充）。
func applyConfigFields(inst *model.PocketInstance, cfg InstanceConfig) {
	if cfg.Hostname != "" {
		inst.Hostname = cfg.Hostname
		if inst.Machine.Hostname == "" {
			inst.Machine.Hostname = cfg.Hostname
		}
	}
	if cfg.IP != "" {
		inst.IP = cfg.IP
	}
	if cfg.Port != 0 {
		inst.Port = cfg.Port
	}
	if cfg.Version != "" {
		inst.Version = cfg.Version
	}
	if cfg.Machine != (model.MachineInfo{}) {
		inst.Machine = cfg.Machine
	}
	if cfg.Origin != "" && inst.Origin == "" {
		inst.Origin = cfg.Origin
	}
	if len(cfg.Capabilities) > 0 {
		inst.Capabilities = cfg.Capabilities
	}
}

// defaultCapabilities 在未提供能力列表时返回兜底值。
// 真实能力应由 capabilities 探测（checkInstanceHealth 时附带）覆盖。
func defaultCapabilities(provided []string) []string {
	if len(provided) > 0 {
		return provided
	}
	return []string{"session", "summary", "pty"}
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

// RegisterRegisteredInstance 实现 model.InstanceRegistrar 接口。
// 由 PluginHub 在收到边端 instance.register 时调用，把插件上报的实例写入 Registry。
// origin 标记为 "registered"（区别于 discovered/static/acc）。
// 已存在的实例只更新展示与机器字段（保留 Health），并刷新 apiURLMap（plugin 可更新 API 地址）。
func (r *Registry) RegisterRegisteredInstance(info model.RegisteredInstanceInfo) error {
	if info.ID == "" {
		return fmt.Errorf("instance ID is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	machine := model.MachineInfo{
		Hostname: info.Hostname,
		Platform: info.Platform,
		Arch:     info.Arch,
		CPUs:     info.CPUs,
		MemoryMB: info.MemoryMB,
	}

	if existing, ok := r.instances[info.ID]; ok {
		existing.DisplayName = orDefault(info.DisplayName, existing.DisplayName)
		existing.Environment = orDefault(info.Environment, existing.Environment)
		existing.Version = orDefault(info.Version, existing.Version)
		if info.Hostname != "" {
			existing.Hostname = info.Hostname
		}
		if machine != (model.MachineInfo{}) {
			existing.Machine = machine
		}
		if len(info.Capabilities) > 0 {
			existing.Capabilities = info.Capabilities
		}
		if info.APIBaseURL != "" {
			existing.APIBaseURL = info.APIBaseURL
			r.apiURLMap[info.ID] = info.APIBaseURL
		}
		// 注册即在线
		existing.Health = "healthy"
		existing.LastHeartbeatAt = time.Now().UTC().Format(time.RFC3339)
		return nil
	}

	caps := info.Capabilities
	if len(caps) == 0 {
		caps = []string{"session", "summary", "pty"}
	}
	instance := &model.PocketInstance{
		ID:              info.ID,
		DisplayName:     info.DisplayName,
		Environment:     info.Environment,
		Capabilities:    caps,
		Health:          "healthy",
		LastHeartbeatAt: time.Now().UTC().Format(time.RFC3339),
		APIBaseURL:      info.APIBaseURL,
		Hostname:        info.Hostname,
		Version:         info.Version,
		Machine:         machine,
		Origin:          "registered",
		MigrationStatus: "idle",
	}
	r.instances[info.ID] = instance
	if info.APIBaseURL != "" {
		r.apiURLMap[info.ID] = info.APIBaseURL
	}
	log.Printf("✅ 边端注册实例: %s (%s) origin=registered", info.DisplayName, info.ID)
	return nil
}

// TouchInstance 实现 websocket.InstanceRegistrar 接口，心跳时刷新实例在线时间与状态。
func (r *Registry) TouchInstance(instanceID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if inst, ok := r.instances[instanceID]; ok {
		inst.LastHeartbeatAt = time.Now().UTC().Format(time.RFC3339)
		// 心跳即在线（不覆盖 offline 之外的状态时，至少不把 healthy 降级）
		if inst.Health == "offline" || inst.Health == "unhealthy" || inst.Health == "unknown" {
			inst.Health = "healthy"
		}
	}
}

// RegisteredInstanceInfo 已移至 model 包，供 websocket 与 registry 共享。

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// ParseConfigJSON 从 JSON 字符串解析实例配置
func ParseConfigJSON(jsonStr string) ([]InstanceConfig, error) {
	var configs []InstanceConfig
	if err := json.Unmarshal([]byte(jsonStr), &configs); err != nil {
		return nil, fmt.Errorf("failed to parse instance config: %w", err)
	}
	return configs, nil
}
