// Package disk 提供「磁盘会话聚合」适配器：直接读取本机各编程 agent 落在磁盘
// 上的会话转录（Claude Code 的 JSONL、Codex 的 rollout JSONL + state SQLite），
// 归一化后以 internal/adapter.OpenCodeAdapter 接口暴露，让没有 HTTP 控制面的
// agent 也能被 pocketd 的会话视图/移动端复用。
//
// 解析逻辑移植自 Wake（Rust，/Users/xutaohuang/workspace/ai/Wake）的
// crates/wake-core/src/adapters/{claude,codex,parse_utils,sqlite_ro}.rs，
// 保留其归一化模型（SessionMeta / TranscriptMessage / ToolCallView）与文件
// 格式知识，不引入 Wake 运行时。
//
// 三条硬约束：
//  1. **只读**：绝不写 agent 的 jsonl / sqlite；SQLite 一律 mode=ro 打开，
//     直开失败才拷贝 db + -wal + -shm 到临时目录再只读打开（见 sqlite_ro.go）。
//  2. **不信任客户端 URL**：instanceBaseURL 只接受本包内置的 disk:// locator
//     常量（由 registry 按 workspace 作用域解析 instance_id 得到），任何其它
//     取值直接报错，杜绝把客户端字符串当路径/URL 使用。
//  3. **写路径显式不支持**：SendPrompt / CreateSession / Interrupt / 审批回复 /
//     事件订阅一律返回 ErrNotSupported，磁盘 agent 没有控制面可写。
package disk

// 单条消息正文 / 工具输入输出的上限（与 Wake models.rs 的 MAX_* 对齐）。
const (
	maxMsgText = 32 * 1024
	maxToolIO  = 16 * 1024
	maxTitle   = 80
	// untitled 是无标题会话的占位标题（与 Wake UNTITLED 一致）。
	untitled = "Untitled"
)

// Role 是归一化后的消息角色。
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
)

// MessageKind 区分正文消息与被折叠的元信息（注入内容、压缩边界）。
type MessageKind string

const (
	// KindText 是真实对话正文，参与消息计数与标题推导。
	KindText MessageKind = "text"
	// KindMeta 是 IDE/CLI 注入的伪用户消息或系统元信息，折叠展示。
	KindMeta MessageKind = "meta"
	// KindCompactSummary 是上下文压缩边界。
	KindCompactSummary MessageKind = "compact-summary"
)

// ToolCallView 是归一化的工具调用视图。
type ToolCallView struct {
	ID           string
	Name         string
	InputPreview string
	Input        string
	Output       string
	IsError      bool
}

// TranscriptMessage 是归一化的单条会话消息。
type TranscriptMessage struct {
	// Seq 是会话内稳定序号，由 assignSeq 在消息序列定型后统一回填。
	Seq       int
	Role      Role
	Kind      MessageKind
	Text      string
	Truncated bool
	ToolCalls []ToolCallView
	Thinking  string
	// Timestamp 是 epoch 毫秒；0 表示上游未提供。
	Timestamp int64
	Model     string
}

// SessionMeta 是归一化的会话元数据（列表视图用）。
type SessionMeta struct {
	// Key 是全局唯一键 `{agent}:{id}`。
	Key   string
	ID    string
	Agent string
	Title string
	// ProjectPath 是会话的 cwd；ProjectName 是其末段目录名。
	ProjectPath string
	ProjectName string
	// FilePath 是转录文件路径；SQLite 型数据源用 `<db>#<id>` 虚拟路径。
	FilePath     string
	CreatedAt    int64 // epoch ms
	UpdatedAt    int64 // epoch ms
	MessageCount int64
	SizeBytes    int64
	GitBranch    string
	Model        string
	TokensUsed   int64
	Archived     bool
	// Source 是会话来源（Codex 的 originator：CLI / IDE extension 等）。
	Source string
}

// sessionFileRef 指向一个待解析的会话文件（Wake SessionFileRef 的移植）。
type sessionFileRef struct {
	agent    string
	nativeID string
	filePath string
	mtimeMS  int64
	size     int64
}
