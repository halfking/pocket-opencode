package model

// MachineInfo 描述实例所在主机的机器信息（由边端插件/manager 上报或探测得到）。
// 用于跨主机迁移时识别目标环境、工作目录重映射等。
type MachineInfo struct {
	Hostname string `json:"hostname,omitempty"`
	Platform string `json:"platform,omitempty"` // darwin/linux/windows
	Arch     string `json:"arch,omitempty"`     // arm64/amd64
	CPUs     int    `json:"cpus,omitempty"`
	MemoryMB int64  `json:"memoryMb,omitempty"` // 总内存（MB）
}

// PocketInstance 描述一个可被 Pocket 管理/指挥的 OpenCode/ZCode 实例。
//
// Origin 取值：
//   - "discovered"  : 通过网络扫描发现
//   - "registered"  : 边端插件/manager 主动注册
//   - "static"      : 静态配置（POCKET_OPENCODE_INSTANCES）
//   - "acc"         : 来自 ACC 统一注册表
//
// MigrationStatus 取值：idle / incoming / outgoing（跨主机迁移时由迁移服务设置）。
type PocketInstance struct {
	ID              string      `json:"id"`
	DisplayName     string      `json:"displayName"`
	Environment     string      `json:"environment"`
	NPSClientID     int         `json:"npsClientId"`
	Capabilities    []string    `json:"capabilities"`
	Health          string      `json:"health"`
	LastHeartbeatAt string      `json:"lastHeartbeatAt"`

	// —— Phase 迁移方案新增字段 ——
	APIBaseURL      string      `json:"apiBaseURL,omitempty"`      // 实例 HTTP API 根地址（从 registry.apiURLMap 提升为字段，便于序列化与跨主机传递）
	Hostname        string      `json:"hostname,omitempty"`        // 主机名（machine.hostname 的快捷副本）
	IP              string      `json:"ip,omitempty"`              // 主 IP（用于展示与去重）
	Port            int         `json:"port,omitempty"`            // API 端口
	Version         string      `json:"version,omitempty"`         // OpenCode/ZCode 版本
	Machine         MachineInfo `json:"machine,omitempty"`         // 主机机器信息
	Origin          string      `json:"origin,omitempty"`          // 来源：discovered/registered/static/acc
	MigrationStatus string      `json:"migrationStatus,omitempty"` // 迁移状态：idle/incoming/outgoing
	ActiveSessions  int         `json:"activeSessions,omitempty"`  // 活跃会话数（展示用，由调用方填充）
	CPUPercent      float64     `json:"cpuPercent,omitempty"`      // CPU 占用百分比（展示用，可选）
}

type TaskSummary struct {
	ID                string `json:"id"`
	Title             string `json:"title"`
	Status            string `json:"status"`
	Priority          string `json:"priority"`
	WorkstreamID      string `json:"workstreamId"`
	SessionCount      int    `json:"sessionCount"`
	PendingApprovals  int    `json:"pendingApprovals"`
}

// SessionResumeBrief 是会话迁移包的语义层（迁移时由导出端填充，导入端用作续接提示词输入）。
// 该结构在 Phase 0 已预留但零使用；会话跨主机迁移方案将其作为迁移包的核心语义字段。
type SessionResumeBrief struct {
	InstanceID    string   `json:"instanceId"`
	SessionID     string   `json:"sessionId"`
	TaskID        string   `json:"taskId"`
	Title         string   `json:"title"`
	CurrentState  string   `json:"currentState"`
	LastObjective string   `json:"lastObjective"`
	Decisions     []string `json:"decisions"`
	ChangedFiles  []string `json:"changedFiles"`
	Blockers      []string `json:"blockers"`
	NextAction    string   `json:"nextAction"`

	// —— 迁移方案扩展 ——
	Attachments []AttachmentRef `json:"attachments,omitempty"` // 产物文件引用（CloudReve URL）
	TurnCount   int             `json:"turnCount,omitempty"`   // 已执行轮次数
}

// AttachmentRef 描述一个产物文件在云端（files.itestu.cn/CloudReve）的引用。
// 迁移时由导出端把本地文件上传到 CloudReve 后填入 URL，导入端按 URL 拉取。
type AttachmentRef struct {
	Type         string `json:"type"`          // file/diff/report/log
	Name         string `json:"name"`          // 原始文件名
	CloudReveURL string `json:"cloudreveUrl"`  // https://files.itestu.cn/api/v3/file/get/{id}/{name}?sign=
	Size         int64  `json:"size,omitempty"`  // 字节数
	Hash         string `json:"hash,omitempty"` // 内容校验（sha256）
}

// =============================================================================
// 边端实例注册（plugin/manager → PluginHub → Registry）
// =============================================================================

// RegisteredInstanceInfo 是边端注册上报的实例信息（由 plugin 的 InstanceInfo 映射而来）。
// 放在 model 包，供 websocket 和 registry 共享，避免两包互相依赖。
type RegisteredInstanceInfo struct {
	ID           string
	DisplayName  string
	APIBaseURL   string // 可选：plugin/manager 在注册时提供本机 OpenCode 的 API 地址
	Environment  string
	Version      string
	Capabilities []string
	Hostname     string
	Platform     string
	Arch         string
	CPUs         int
	MemoryMB     int64
}

// InstanceRegistrar 由 Registry 实现，PluginHub 通过它把边端注册/心跳写入 Registry。
// 定义在 model 包，websocket 包依赖 model 即可，不反向依赖 registry。
type InstanceRegistrar interface {
	RegisterRegisteredInstance(info RegisteredInstanceInfo) error
	TouchInstance(instanceID string)
}
