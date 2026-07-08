// Package migration 编排会话跨主机迁移。
//
// 迁移流程（重建式）：
//  1. 从源实例拉取会话消息 + 元数据（OpenCode HTTP API 或 llm-gateway export）
//  2. 组装迁移包（SessionResumeBrief + 消息流 + 附件引用）
//  3. 用 4 类提示词模板（env_sync/task_resume/result_verify/acc_report）拼接注入 prompt
//  4. 选择目标实例（健康 + 负载评分）
//  5. 经 PluginHub 向目标实例的 opencode-manager/plugin 下发 session.migrate_to 命令
//  6. 目标端拉迁移包 → 创建新会话 → 发送续接 prompt → 回报新 sessionID
//  7. 用 task_session_links (Role=migrated_from/migrated_to) 建立逻辑会话映射
//
// 设计要点：
//   - 迁移服务是无状态编排器，依赖 registry（实例选择）、opencode adapter（拉消息）、
//     pluginHub（下发命令）、taskStore（逻辑映射）。任一为 nil 时降级。
//   - 迁移包的语义层复用 model.SessionResumeBrief（已扩 Attachments/TurnCount）。
//   - 4 类提示词模板与 opencode-plugin/src/prompts.ts 对齐，保证两端拼接一致。
package migration

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/adapter"
	"github.com/halfking/pocket-opencode/backend/internal/model"
	"github.com/halfking/pocket-opencode/backend/internal/registry"
	"github.com/halfking/pocket-opencode/backend/internal/task"
	"github.com/halfking/pocket-opencode/backend/internal/websocket"
)

// Service 编排会话跨主机迁移。所有依赖均可为 nil（降级）。
type Service struct {
	registry *registry.Registry    // 实例选择（必需）
	opencode adapter.OpenCodeAdapter // 从源实例拉消息（必需）
	pluginHub *websocket.PluginHub  // 下发 migrate_to 命令到目标实例（必需）
	taskStore *task.Store           // 建立逻辑会话映射（可选）
}

// New 构造迁移服务。
func New(reg *registry.Registry, oc adapter.OpenCodeAdapter, hub *websocket.PluginHub, ts *task.Store) *Service {
	return &Service{
		registry:  reg,
		opencode:  oc,
		pluginHub: hub,
		taskStore: ts,
	}
}

// MigrationRequest 是发起一次迁移的入参。
type MigrationRequest struct {
	FromInstanceID string   `json:"fromInstanceId"` // 源实例
	SessionID      string   `json:"sessionId"`      // 源会话
	ToInstanceID   string   `json:"toInstanceId,omitempty"`   // 目标实例（空=自动选）
	TaskID         string   `json:"taskId,omitempty"`         // 关联任务（建逻辑映射用）
	PromptTemplates []string `json:"promptTemplates,omitempty"` // 启用的提示词模板
	WorkingDir     string   `json:"workingDirectory,omitempty"` // 目标工作目录覆盖
}

// MigrationResult 是迁移结果。
type MigrationResult struct {
	Success       bool   `json:"success"`
	FromInstance  string `json:"fromInstance"`
	FromSession   string `json:"fromSession"`
	ToInstance    string `json:"toInstance"`
	NewSessionID  string `json:"newSessionId,omitempty"` // 目标端创建的新会话
	PackID        string `json:"packId,omitempty"`       // 迁移包 ID（若有 staging）
	TurnsMigrated int    `json:"turnsMigrated"`
	Error         string `json:"error,omitempty"`
	StartedAt     string `json:"startedAt"`
	CompletedAt   string `json:"completedAt,omitempty"`
}

// Migrate 执行一次迁移。主要步骤见包注释。
func (s *Service) Migrate(ctx context.Context, req MigrationRequest) (*MigrationResult, error) {
	started := time.Now().UTC().Format(time.RFC3339)
	result := &MigrationResult{
		FromInstance: req.FromInstanceID,
		FromSession:  req.SessionID,
		StartedAt:    started,
	}

	if s.registry == nil || s.opencode == nil || s.pluginHub == nil {
		result.Error = "migration service not fully configured (registry/opencode/pluginHub required)"
		return result, fmt.Errorf("%s", result.Error)
	}

	// 1. 解析源实例 API 地址
	fromBase, err := s.registry.GetInstanceAPIBase(req.FromInstanceID)
	if err != nil {
		result.Error = fmt.Sprintf("source instance not found: %v", err)
		return result, fmt.Errorf("%s", result.Error)
	}

	// 2. 从源实例拉会话消息 + 元数据，组装迁移包
	pack, err := s.buildPackFromOpenCode(ctx, fromBase, req.SessionID, req.FromInstanceID)
	if err != nil {
		result.Error = fmt.Sprintf("build pack: %v", err)
		return result, fmt.Errorf("%s", result.Error)
	}
	result.TurnsMigrated = pack.TurnCount

	// 3. 选择目标实例
	toInstance := req.ToInstanceID
	if toInstance == "" {
		toInstance, err = s.selectTarget(req.FromInstanceID)
		if err != nil {
			result.Error = fmt.Sprintf("select target: %v", err)
			return result, fmt.Errorf("%s", result.Error)
		}
	}
	result.ToInstance = toInstance

	// 4. 拼接注入提示词（默认 3 类：env_sync + task_resume + result_verify）
	templates := req.PromptTemplates
	if len(templates) == 0 {
		templates = []string{"env_sync", "task_resume", "result_verify"}
	}
	promptText := BuildPrompts(pack, templates)

	// 5. 序列化迁移包，经 PluginHub 下发 session.migrate_to 命令
	// 命令是异步的：目标端收到后在本地创建新会话，再通过 command.result 回报 newSessionID。
	// 迁移服务此处只负责"成功下发"，newSessionID 由后续 command.result 事件回填（或留空待补）。
	packBytes, _ := json.Marshal(pack)
	cmdPayload := map[string]interface{}{
		"packURL":          "", // 内联传递：pack 直接放 payload，目标端无需二次拉取
		"promptText":       promptText,
		"workingDirectory": req.WorkingDir,
		"packInline":       packBytes, // 目标端 manager/plugin 优先用内联包（json.RawMessage 兼容）
		// 携带来源信息，供目标端建立反向映射
		"fromInstance":     req.FromInstanceID,
		"fromSession":      req.SessionID,
		"taskId":           req.TaskID,
	}

	err = s.pluginHub.SendCommandToInstance(toInstance, websocket.Message{
		Type:    "session.migrate_to",
		Payload: cmdPayload,
	})
	if err != nil {
		result.Error = fmt.Sprintf("dispatch migrate_to to %s: %v", toInstance, err)
		return result, fmt.Errorf("%s", result.Error)
	}

	// 6. 命令已下发。newSessionID 异步回填（目标端 command.result 到达后由 webhook/事件处理器更新）。
	// 此处记录一条 pending 迁移映射（Role=migrated_to，新 sessionID 暂空，待回填）。
	if s.taskStore != nil && req.TaskID != "" {
		s.recordMigrationLink(req.TaskID, req.FromInstanceID, req.SessionID, toInstance, result.NewSessionID)
	}

	result.Success = true
	result.CompletedAt = time.Now().UTC().Format(time.RFC3339)
	log.Printf("✅ 迁移命令已下发: %s/%s → %s (轮次:%d, 提示词:%d字符)",
		req.FromInstanceID, req.SessionID, toInstance, result.TurnsMigrated, len(promptText))
	return result, nil
}

// Preview 预览迁移：拉取源会话组装迁移包 + 拼接提示词，但不实际下发命令。
// 用于迁移向导第一步"选择内容"展示。返回 (pack, promptText, error)。
func (s *Service) Preview(ctx context.Context, fromInstanceID, sessionID string, templates []string) (*model.SessionResumeBrief, string, error) {
	if s.registry == nil || s.opencode == nil {
		return nil, "", fmt.Errorf("registry/opencode required for preview")
	}
	fromBase, err := s.registry.GetInstanceAPIBase(fromInstanceID)
	if err != nil {
		return nil, "", fmt.Errorf("source instance not found: %w", err)
	}
	pack, err := s.buildPackFromOpenCode(ctx, fromBase, sessionID, fromInstanceID)
	if err != nil {
		return nil, "", fmt.Errorf("build pack: %w", err)
	}
	if len(templates) == 0 {
		templates = []string{"env_sync", "task_resume", "result_verify"}
	}
	prompt := BuildPrompts(pack, templates)
	return pack, prompt, nil
}
// 由 server 层的 command.result 处理器在识别到 migrate_to 结果时调用。
func (s *Service) CompleteMigration(ctx context.Context, taskID, toInstance, newSessionID, fromInstance, fromSession string) error {
	if s.taskStore == nil {
		return nil
	}
	// 更新 migrated_to 链接的 sessionID（之前可能是空/占位）
	return s.taskStore.AttachSession(ctx, task.SessionLink{
		TaskID:     taskID,
		InstanceID: toInstance,
		SessionID:  newSessionID,
		Role:       "migrated_to",
	})
}

// buildPackFromOpenCode 从源 OpenCode 实例拉取会话消息，组装迁移包。
// 复用 adapter.GetSessionDetail + adapter.GetMessages（已修正为双格式解析，支持裸数组响应）。
func (s *Service) buildPackFromOpenCode(ctx context.Context, instanceBaseURL, sessionID, instanceID string) (*model.SessionResumeBrief, error) {
	httpAdapter, ok := s.opencode.(*adapter.OpenCodeHTTPAdapter)
	if !ok {
		return nil, fmt.Errorf("opencode adapter is not HTTP adapter")
	}

	// 会话元数据（OpenCodeSessionInfo 含 Title/Location.Directory）
	detail, err := httpAdapter.GetSessionDetail(ctx, instanceBaseURL, sessionID)
	if err != nil {
		return nil, fmt.Errorf("get session detail: %w", err)
	}

	// 消息流：复用 adapter.GetMessages（已修正支持裸数组响应，不再需要 fetchMessagesRaw）
	msgs, err := httpAdapter.GetMessages(ctx, instanceBaseURL, sessionID, 50, "asc")
	if err != nil {
		return nil, fmt.Errorf("get messages: %w", err)
	}

	pack := &model.SessionResumeBrief{
		InstanceID:    instanceID,
		SessionID:     sessionID,
		Title:         detail.Title,
		LastObjective: detail.Title,
		TurnCount:     len(msgs),
	}

	// 从消息里提取最后一条 assistant 回复作为 currentState/nextAction 线索。
	// OpenCodeMessage.Data 是 V1 结构 {info:{role}, parts:[{type,text}]}。
	pack.CurrentState = summarizeLastTurnFromAdapter(msgs)
	pack.NextAction = inferNextActionFromAdapter(msgs)

	return pack, nil
}

// selectTarget 选择目标实例：健康 + 活跃会话最少 + 非源实例。
func (s *Service) selectTarget(fromInstanceID string) (string, error) {
	all := s.registry.ListInstances()
	var candidates []model.PocketInstance
	for _, inst := range all {
		if inst.ID == fromInstanceID {
			continue
		}
		if inst.Health == "healthy" && inst.MigrationStatus != "incoming" && inst.MigrationStatus != "outgoing" {
			candidates = append(candidates, inst)
		}
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("no healthy target instance available (excluding source %s)", fromInstanceID)
	}
	// 按 ActiveSessions 升序（负载最低优先），并列时按 Origin 优先级（registered > discovered > static）
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].ActiveSessions != candidates[j].ActiveSessions {
			return candidates[i].ActiveSessions < candidates[j].ActiveSessions
		}
		return originPriority(candidates[i].Origin) > originPriority(candidates[j].Origin)
	})
	return candidates[0].ID, nil
}

func originPriority(o string) int {
	switch o {
	case "registered":
		return 3
	case "discovered":
		return 2
	case "static":
		return 1
	}
	return 0
}

// recordMigrationLink 在 task_session_links 记录迁移关系（逻辑会话链）。
// Role 用新取值 migrated_from（旧会话）/ migrated_to（新会话）。
func (s *Service) recordMigrationLink(taskID, fromInst, fromSession, toInst, newSession string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	now := time.Now().Unix()
	// 旧会话标记为 migrated_from
	_ = s.execLink(ctx, taskID, fromInst, fromSession, "migrated_from", now)
	// 新会话标记为 migrated_to
	_ = s.execLink(ctx, taskID, toInst, newSession, "migrated_to", now)
}

var linkMu sync.Mutex

func (s *Service) execLink(ctx context.Context, taskID, instanceID, sessionID, role string, ts int64) error {
	linkMu.Lock()
	defer linkMu.Unlock()
	// 复用 task.Store 的 AttachSession（它 INSERT OR 跳过）
	if s.taskStore == nil {
		return nil
	}
	return s.taskStore.AttachSession(ctx, task.SessionLink{
		TaskID:     taskID,
		InstanceID: instanceID,
		SessionID:  sessionID,
		Role:       role,
	})
}

// summarizeLastTurnFromAdapter 从 adapter 消息流里取最后一条 assistant 文本作为当前状态摘要。
// OpenCodeMessage.Data 是 V1 结构 {info:{role}, parts:[{type,text}]}。
func summarizeLastTurnFromAdapter(msgs []adapter.OpenCodeMessage) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if roleFromData(msgs[i].Data) != "assistant" {
			continue
		}
		text := extractTextFromData(msgs[i].Data)
		if text == "" {
			continue
		}
		if len(text) > 500 {
			return text[:500] + "..."
		}
		return text
	}
	return ""
}

// inferNextActionFromAdapter 从最后一条 assistant 消息推断下一步（取末尾 200 字符）。
func inferNextActionFromAdapter(msgs []adapter.OpenCodeMessage) string {
	last := summarizeLastTurnFromAdapter(msgs)
	if last == "" {
		return ""
	}
	if len(last) > 200 {
		return "..." + last[len(last)-200:]
	}
	return last
}

// roleFromData 从 V1 message Data 里取 info.role。
func roleFromData(data map[string]interface{}) string {
	if info, ok := data["info"].(map[string]interface{}); ok {
		if r, ok := info["role"].(string); ok {
			return r
		}
	}
	return ""
}

// extractTextFromData 从 V1 message Data 里拼接 parts[].text（type==text）。
func extractTextFromData(data map[string]interface{}) string {
	parts, ok := data["parts"].([]interface{})
	if !ok {
		return ""
	}
	var sb strings.Builder
	for _, p := range parts {
		pm, ok := p.(map[string]interface{})
		if !ok {
			continue
		}
		if pm["type"] != "text" {
			continue
		}
		if t, ok := pm["text"].(string); ok {
			sb.WriteString(t)
		}
	}
	return sb.String()
}
