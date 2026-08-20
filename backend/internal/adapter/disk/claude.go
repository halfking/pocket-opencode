package disk

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// 本文件是 Wake crates/wake-core/src/adapters/claude.rs 的 Go 移植：
// Claude Code 把每个会话写成 ~/.claude/projects/<slug>/<session-id>.jsonl，
// 一行一个事件（user / assistant / system / custom-title / 若干非消息类型）。
//
// 与 Wake 一致的取舍：
//   - assistant 的多个 content block（text/thinking/tool_use）按 message.id
//     聚合成一条消息；tool_result 出现在后续 user 行，按 tool_use_id 回填。
//   - 侧车目录 `<session-id>/subagents/*.jsonl` 与 `memory/` 不算主会话
//     （memory 显式排除：那是记忆而非会话）。
//   - isSidechain=true 的行在主线解析时跳过。

// claudeAgent 是归一化后的 agent 标识（与 Wake AgentId::ClaudeCode 一致）。
const claudeAgent = "claude-code"

// claudeSkipTypes 是已知的非消息行类型，静默跳过（不计入 unknown）。
var claudeSkipTypes = map[string]bool{
	"queue-operation":       true,
	"mode":                  true,
	"last-prompt":           true,
	"permission-mode":       true,
	"file-history-snapshot": true,
	"file-history-delta":    true,
	"pr-link":               true,
	"frame-link":            true,
	"attachment":            true,
	"summary":               true,
}

// claudeReader 读取 ~/.claude/projects 下的会话转录。
type claudeReader struct {
	root string
}

// newClaudeReader 用 home 目录推导数据根目录；home 为空时退化为不可用。
func newClaudeReader(home string) *claudeReader {
	if home == "" {
		return &claudeReader{}
	}
	return &claudeReader{root: filepath.Join(home, ".claude", "projects")}
}

func (r *claudeReader) agent() string       { return claudeAgent }
func (r *claudeReader) displayName() string { return "Claude Code (disk)" }
func (r *claudeReader) dataPath() string    { return r.root }

// detect 判断本机是否装了 Claude Code（数据目录存在）。
func (r *claudeReader) detect() bool {
	if r.root == "" {
		return false
	}
	info, err := os.Stat(r.root)
	return err == nil && info.IsDir()
}

// listRefs 枚举 <projects>/<slug>/*.jsonl（只取一层，排除 subagents/memory 侧车）。
func (r *claudeReader) listRefs() []sessionFileRef {
	refs := make([]sessionFileRef, 0)
	if r.root == "" {
		return refs
	}
	projects, err := os.ReadDir(r.root)
	if err != nil {
		return refs
	}
	for _, project := range projects {
		if !project.IsDir() {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(r.root, project.Name()))
		if err != nil {
			continue
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".jsonl") {
				continue
			}
			info, err := entry.Info()
			if err != nil || info.Size() == 0 {
				continue
			}
			refs = append(refs, sessionFileRef{
				agent:    claudeAgent,
				nativeID: strings.TrimSuffix(name, ".jsonl"),
				filePath: filepath.Join(r.root, project.Name(), name),
				mtimeMS:  mtimeMS(info),
				size:     info.Size(),
			})
		}
	}
	return refs
}

// listSessions 返回全部会话元数据（按 UpdatedAt 由调用方排序）。
func (r *claudeReader) listSessions() ([]SessionMeta, error) {
	if !r.detect() {
		return nil, fmt.Errorf("claude data directory not found: %s", r.root)
	}
	refs := r.listRefs()
	out := make([]SessionMeta, 0, len(refs))
	for _, ref := range refs {
		parsed, err := parseClaudeJSONL(ref.filePath, false)
		if err != nil {
			// 单个坏文件不应让整个列表失败（与 Wake 的尽力而为一致）。
			continue
		}
		out = append(out, claudeMeta(ref, parsed))
	}
	return out, nil
}

// transcript 解析指定会话的完整转录。
func (r *claudeReader) transcript(sessionID string) (SessionMeta, []TranscriptMessage, error) {
	for _, ref := range r.listRefs() {
		if ref.nativeID != sessionID {
			continue
		}
		parsed, err := parseClaudeJSONL(ref.filePath, false)
		if err != nil {
			return SessionMeta{}, nil, err
		}
		return claudeMeta(ref, parsed), parsed.messages, nil
	}
	return SessionMeta{}, nil, fmt.Errorf("session not found: %s", sessionID)
}

// claudeParse 是单个 jsonl 文件的解析产物。
type claudeParse struct {
	messages     []TranscriptMessage
	title        string
	cwd          string
	gitBranch    string
	model        string
	tokensUsed   int64
	createdAt    int64
	updatedAt    int64
	unknownLines int
}

// pendingAssistant 聚合同一个 message.id 下的多个 content block。
type pendingAssistant struct {
	msgID     string
	hasMsgID  bool
	text      []string
	thinking  []string
	toolCalls []ToolCallView
	timestamp int64
	model     string
}

// parseClaudeJSONL 解析一个 Claude Code 会话文件。
// includeSidechain=true 时保留 isSidechain 行（用于单独加载 subagent 转录）。
func parseClaudeJSONL(path string, includeSidechain bool) (*claudeParse, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	out := &claudeParse{messages: make([]TranscriptMessage, 0, 64)}
	// toolIndex: tool_use id → (消息下标, 该消息内 tool_calls 下标)
	toolIndex := map[string][2]int{}
	var pending *pendingAssistant
	customTitle := ""
	fallbackTitle := ""

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024) // 单行可能很长（大 tool 输出）
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			out.unknownLines++
			continue
		}
		typ, _ := row["type"].(string)

		if typ == "custom-title" {
			if t, ok := row["customTitle"].(string); ok && strings.TrimSpace(t) != "" {
				customTitle = strings.TrimSpace(t)
			}
			continue
		}
		if typ != "user" && typ != "assistant" && typ != "system" {
			if typ == "" || !claudeSkipTypes[typ] {
				out.unknownLines++
			}
			continue
		}

		// ---- 消息行公共信封 ----
		if out.cwd == "" {
			if c, ok := row["cwd"].(string); ok {
				out.cwd = c
			}
		}
		if b, ok := row["gitBranch"].(string); ok && b != "" {
			out.gitBranch = b
		}
		ts := toEpochMS(row["timestamp"])
		if ts > 0 {
			if out.createdAt == 0 {
				out.createdAt = ts
			}
			if ts > out.updatedAt {
				out.updatedAt = ts
			}
		}
		if side, ok := row["isSidechain"].(bool); ok && side && !includeSidechain {
			continue
		}

		message, _ := row["message"].(map[string]any)

		if typ == "system" {
			out.messages = flushAssistant(pending, out.messages, toolIndex)
			pending = nil
			if subtype, _ := row["subtype"].(string); subtype == "compact_boundary" {
				out.messages = append(out.messages, mkMsg(RoleSystem, KindCompactSummary, "── Context compacted ──", ts))
			} else if content, ok := row["content"].(string); ok && content != "" {
				text, truncated := clip(content, maxToolIO)
				out.messages = append(out.messages, TranscriptMessage{
					Role:      RoleSystem,
					Kind:      KindMeta,
					Text:      text,
					Truncated: truncated,
					Timestamp: ts,
				})
			}
			continue
		}

		if typ == "user" {
			out.messages = flushAssistant(pending, out.messages, toolIndex)
			pending = nil
			if message == nil {
				continue
			}
			parts := make([]string, 0, 4)
			switch content := message["content"].(type) {
			case string:
				parts = append(parts, content)
			case []any:
				for _, raw := range content {
					block, ok := raw.(map[string]any)
					if !ok {
						continue
					}
					switch blockType, _ := block["type"].(string); blockType {
					case "text":
						if t, ok := block["text"].(string); ok {
							parts = append(parts, t)
						}
					case "tool_result":
						id, _ := block["tool_use_id"].(string)
						pos, ok := toolIndex[id]
						if !ok {
							continue
						}
						target := &out.messages[pos[0]].ToolCalls[pos[1]]
						target.Output, _ = clip(stringifyToolResult(block["content"]), maxToolIO)
						if isErr, ok := block["is_error"].(bool); ok && isErr {
							target.IsError = true
						}
					case "image":
						parts = append(parts, "[image]")
					}
				}
			}

			text := strings.TrimSpace(strings.Join(parts, "\n\n"))
			if text == "" {
				continue
			}
			isMeta, _ := row["isMeta"].(bool)
			isCompact, _ := row["isCompactSummary"].(bool)
			kind := KindText
			switch {
			case isCompact:
				kind = KindCompactSummary
			case isMeta || isInjectedUserContent(text):
				kind = KindMeta
			}
			if kind == KindText && fallbackTitle == "" {
				fallbackTitle = cleanTitleCandidate(text)
			}
			clipped, truncated := clip(text, maxMsgText)
			out.messages = append(out.messages, TranscriptMessage{
				Role:      RoleUser,
				Kind:      kind,
				Text:      clipped,
				Truncated: truncated,
				Timestamp: ts,
			})
			continue
		}

		// ---- assistant ----
		if message == nil {
			continue
		}
		msgID, hasMsgID := message["id"].(string)
		needNew := pending == nil || (hasMsgID && pending.hasMsgID && pending.msgID != msgID)
		if needNew {
			out.messages = flushAssistant(pending, out.messages, toolIndex)
			pending = &pendingAssistant{msgID: msgID, hasMsgID: hasMsgID, timestamp: ts}
		}
		if !pending.hasMsgID && hasMsgID {
			pending.msgID, pending.hasMsgID = msgID, true
		}
		// "<synthetic>" 是系统合成消息（中断提示等）的占位 model，不是真实模型。
		if m, ok := message["model"].(string); ok && m != "" && m != "<synthetic>" {
			pending.model = m
			out.model = m
		}
		if usage, ok := message["usage"].(map[string]any); ok {
			out.tokensUsed += numField(usage, "input_tokens") +
				numField(usage, "output_tokens") +
				numField(usage, "cache_creation_input_tokens")
		}
		switch content := message["content"].(type) {
		case []any:
			for _, raw := range content {
				block, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				switch blockType, _ := block["type"].(string); blockType {
				case "text":
					if t, ok := block["text"].(string); ok && strings.TrimSpace(t) != "" {
						pending.text = append(pending.text, t)
					}
				case "thinking":
					if t, ok := block["thinking"].(string); ok && strings.TrimSpace(t) != "" {
						pending.thinking = append(pending.thinking, t)
					}
				case "tool_use":
					id, _ := block["id"].(string)
					name, _ := block["name"].(string)
					pending.toolCalls = append(pending.toolCalls, toolCallView(id, name, block["input"], "", false))
				}
			}
		case string:
			if strings.TrimSpace(content) != "" {
				pending.text = append(pending.text, content)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		// 截断/超长行：保留已解析部分并记一次 unknown（与 Wake 的容错一致）。
		out.unknownLines++
	}
	out.messages = flushAssistant(pending, out.messages, toolIndex)
	assignSeq(out.messages)

	switch {
	case customTitle != "":
		out.title = customTitle
	case fallbackTitle != "":
		out.title = fallbackTitle
	default:
		out.title = untitled
	}
	return out, nil
}

// flushAssistant 把 pending 聚合体写成一条 assistant 消息，并登记其 tool_use
// id → 位置，供后续 user 行的 tool_result 回填。
func flushAssistant(p *pendingAssistant, messages []TranscriptMessage, toolIndex map[string][2]int) []TranscriptMessage {
	if p == nil {
		return messages
	}
	text := strings.Join(p.text, "\n\n")
	if text == "" && len(p.thinking) == 0 && len(p.toolCalls) == 0 {
		return messages
	}
	clipped, truncated := clip(text, maxMsgText)
	thinking := ""
	if len(p.thinking) > 0 {
		thinking, _ = clip(strings.Join(p.thinking, "\n\n"), maxToolIO)
	}
	msgIdx := len(messages)
	for i, tc := range p.toolCalls {
		if tc.ID != "" {
			toolIndex[tc.ID] = [2]int{msgIdx, i}
		}
	}
	return append(messages, TranscriptMessage{
		Role:      RoleAssistant,
		Kind:      KindText,
		Text:      clipped,
		Truncated: truncated,
		ToolCalls: p.toolCalls,
		Thinking:  thinking,
		Timestamp: p.timestamp,
		Model:     p.model,
	})
}

// stringifyToolResult 把 tool_result 的 content（字符串/块数组/对象）拍平为文本。
func stringifyToolResult(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		parts := make([]string, 0, len(v))
		for _, raw := range v {
			block, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			switch blockType, _ := block["type"].(string); blockType {
			case "text":
				if t, ok := block["text"].(string); ok {
					parts = append(parts, t)
				}
			case "image":
				parts = append(parts, "[image]")
			}
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		if b, err := json.Marshal(v); err == nil {
			return string(b)
		}
		return ""
	default:
		return ""
	}
}

// numField 取 JSON 对象里的数值字段（缺失/非数值记 0）。
func numField(obj map[string]any, key string) int64 {
	if f, ok := obj[key].(float64); ok {
		return int64(f)
	}
	return 0
}

// claudeMeta 由解析结果与文件信息组装会话元数据。
func claudeMeta(ref sessionFileRef, p *claudeParse) SessionMeta {
	created, updated := p.createdAt, p.updatedAt
	if created == 0 {
		created = ref.mtimeMS
	}
	if updated == 0 {
		updated = ref.mtimeMS
	}
	return SessionMeta{
		Key:          claudeAgent + ":" + ref.nativeID,
		ID:           ref.nativeID,
		Agent:        claudeAgent,
		Title:        p.title,
		ProjectPath:  p.cwd,
		ProjectName:  projectNameOf(p.cwd),
		FilePath:     ref.filePath,
		CreatedAt:    created,
		UpdatedAt:    updated,
		MessageCount: countTextMessages(p.messages),
		SizeBytes:    ref.size,
		GitBranch:    p.gitBranch,
		Model:        p.model,
		TokensUsed:   p.tokensUsed,
	}
}

// countTextMessages 只数真实正文消息（Meta/压缩边界不算）。
func countTextMessages(messages []TranscriptMessage) int64 {
	var n int64
	for _, m := range messages {
		if m.Kind == KindText {
			n++
		}
	}
	return n
}
