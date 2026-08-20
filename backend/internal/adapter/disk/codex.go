package disk

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// 本文件是 Wake crates/wake-core/src/adapters/codex.rs 的 Go 移植。
//
// Codex 的会话有两份数据：
//   - rollout JSONL：~/.codex/sessions/**/rollout-<ts>-<uuid>.jsonl（归档在
//     ~/.codex/archived_sessions），是转录的事实来源。
//   - state_5.sqlite 的 threads 表：用户手动命名的标题、cwd、model、token 数、
//     archived 标记等「快元数据」，只读打开用于增强列表视图（read_threads）。
//
// rollout 行是 {timestamp, type, payload}：
//   session_meta / turn_context / response_item / event_msg / compacted /
//   world_state。response_item 是主线（message / reasoning / function_call /
//   function_call_output）；完全没有 response_item 的会话退回 event_msg 流。

const codexAgent = "codex"

// codexReader 读取 ~/.codex 下的 rollout 与 state DB。
type codexReader struct {
	sessionsDir string
	archivedDir string
	stateDB     string
}

func newCodexReader(home string) *codexReader {
	if home == "" {
		return &codexReader{}
	}
	root := filepath.Join(home, ".codex")
	return &codexReader{
		sessionsDir: filepath.Join(root, "sessions"),
		archivedDir: filepath.Join(root, "archived_sessions"),
		stateDB:     filepath.Join(root, "state_5.sqlite"),
	}
}

func (r *codexReader) agent() string       { return codexAgent }
func (r *codexReader) displayName() string { return "Codex (disk)" }
func (r *codexReader) dataPath() string    { return r.sessionsDir }

// detect 判断本机是否装了 Codex（会话目录存在）。
func (r *codexReader) detect() bool {
	for _, dir := range []string{r.sessionsDir, r.archivedDir} {
		if dir == "" {
			continue
		}
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return true
		}
	}
	return false
}

// listRefs 枚举 sessions + archived_sessions 下的 rollout jsonl。
func (r *codexReader) listRefs() []sessionFileRef {
	refs := listJSONLRefs(r.sessionsDir, codexAgent, rolloutNativeID)
	return append(refs, listJSONLRefs(r.archivedDir, codexAgent, rolloutNativeID)...)
}

// listSessions 解析全部 rollout，并用 state DB 的快元数据增强标题等字段。
func (r *codexReader) listSessions() ([]SessionMeta, error) {
	if !r.detect() {
		return nil, fmt.Errorf("codex data directory not found: %s", r.sessionsDir)
	}
	refs := r.listRefs()
	// state DB 不可用（未装/被锁/表结构变化）时降级为纯 rollout 解析。
	quick := r.quickMeta(context.Background(), refs)
	out := make([]SessionMeta, 0, len(refs))
	for _, ref := range refs {
		parsed, err := parseRollout(ref.filePath)
		if err != nil {
			continue
		}
		meta := codexMeta(ref, parsed, r.archivedDir)
		if q, ok := quick[ref.filePath]; ok {
			meta = mergeQuickMeta(meta, q)
		}
		out = append(out, meta)
	}
	return out, nil
}

// transcript 解析指定会话（按 rollout 的 native id 或 state DB 的线程 id 匹配）。
func (r *codexReader) transcript(sessionID string) (SessionMeta, []TranscriptMessage, error) {
	refs := r.listRefs()
	quick := r.quickMeta(context.Background(), refs)
	for _, ref := range refs {
		q, hasQuick := quick[ref.filePath]
		if ref.nativeID != sessionID && !(hasQuick && q.ID == sessionID) {
			continue
		}
		parsed, err := parseRollout(ref.filePath)
		if err != nil {
			return SessionMeta{}, nil, err
		}
		meta := codexMeta(ref, parsed, r.archivedDir)
		if hasQuick {
			meta = mergeQuickMeta(meta, q)
		}
		return meta, parsed.messages, nil
	}
	return SessionMeta{}, nil, fmt.Errorf("session not found: %s", sessionID)
}

// quickMeta 只读读取 state DB 的 threads 表，按 rollout_path 建索引。
// 任何失败都返回空 map（列表仍可由 rollout 解析得出）。
func (r *codexReader) quickMeta(ctx context.Context, refs []sessionFileRef) map[string]SessionMeta {
	out := map[string]SessionMeta{}
	if r.stateDB == "" || len(refs) == 0 {
		return out
	}
	rows, err := readThreads(ctx, r.stateDB)
	if err != nil || len(rows) == 0 {
		return out
	}
	byPath := make(map[string]threadRow, len(rows))
	for _, row := range rows {
		byPath[row.rolloutPath] = row
	}
	for _, ref := range refs {
		row, ok := byPath[ref.filePath]
		if !ok {
			continue
		}
		created, updated := row.createdAtMS, row.updatedAtMS
		if created == 0 {
			created = ref.mtimeMS
		}
		if updated == 0 {
			updated = ref.mtimeMS
		}
		out[ref.filePath] = SessionMeta{
			Key:         codexAgent + ":" + row.id,
			ID:          row.id,
			Agent:       codexAgent,
			Title:       codexQuickTitle(row),
			ProjectPath: row.cwd,
			ProjectName: projectNameOf(row.cwd),
			FilePath:    ref.filePath,
			CreatedAt:   created,
			UpdatedAt:   updated,
			SizeBytes:   ref.size,
			GitBranch:   row.gitBranch,
			Model:       row.model,
			TokensUsed:  row.tokensUsed,
			Archived:    row.archived,
			Source:      row.source,
		}
	}
	return out
}

// codexQuickTitle 优先用用户手动命名的 name，其次清洗过的 title。
func codexQuickTitle(row threadRow) string {
	if strings.TrimSpace(row.name) != "" {
		return row.name
	}
	// Codex 自家 state DB 会把注入文本存进 title，同样要过滤。
	if !isInjectedUserContent(row.title) {
		if t := cleanTitleCandidate(row.title); t != "" {
			return t
		}
	}
	return untitled
}

// mergeQuickMeta 把 state DB 的快元数据叠加到 rollout 解析结果上。
// 语义与 Wake merge_quick_meta 一致：title/key/id 以 state 为准（用户命名优先），
// source/model/tokens 以 rollout 为准、state 只兜底。
func mergeQuickMeta(parsed, quick SessionMeta) SessionMeta {
	if quick.Title != "" && quick.Title != untitled {
		parsed.Title = quick.Title
	}
	if quick.Key != "" {
		parsed.Key = quick.Key
	}
	if quick.ID != "" {
		parsed.ID = quick.ID
	}
	if parsed.Source == "" {
		parsed.Source = quick.Source
	}
	if parsed.Model == "" {
		parsed.Model = quick.Model
	}
	if parsed.TokensUsed == 0 {
		parsed.TokensUsed = quick.TokensUsed
	}
	if quick.Archived {
		parsed.Archived = true
	}
	if parsed.ProjectPath == "" && quick.ProjectPath != "" {
		parsed.ProjectPath = quick.ProjectPath
		parsed.ProjectName = quick.ProjectName
	}
	return parsed
}

// threadRow 是 state_5.sqlite threads 表的一行。
type threadRow struct {
	id          string
	rolloutPath string
	cwd         string
	title       string
	name        string
	tokensUsed  int64
	archived    bool
	gitBranch   string
	model       string
	source      string
	createdAtMS int64
	updatedAtMS int64
}

// readThreads 只读读取 threads 表（绝不写；连接由 sqliteRO 负责关闭与清理）。
func readThreads(ctx context.Context, stateDB string) ([]threadRow, error) {
	ro, err := openSQLiteRO(ctx, stateDB, "codex")
	if err != nil {
		return nil, err
	}
	defer ro.close()

	rows, err := ro.db.QueryContext(ctx, `SELECT id, rollout_path, cwd, title, name, tokens_used,
		archived, git_branch, model, source, created_at_ms, updated_at_ms FROM threads`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]threadRow, 0, 64)
	for rows.Next() {
		var (
			row                                          threadRow
			cwd, title, name, gitBranch, model, source   sql.NullString
			tokensUsed, archived, createdAtMS, updatedAt sql.NullInt64
		)
		if err := rows.Scan(&row.id, &row.rolloutPath, &cwd, &title, &name, &tokensUsed,
			&archived, &gitBranch, &model, &source, &createdAtMS, &updatedAt); err != nil {
			return nil, err
		}
		row.cwd = cwd.String
		row.title = title.String
		row.name = name.String
		row.tokensUsed = tokensUsed.Int64
		row.archived = archived.Int64 == 1
		row.gitBranch = gitBranch.String
		row.model = model.String
		row.source = source.String
		row.createdAtMS = createdAtMS.Int64
		row.updatedAtMS = updatedAt.Int64
		out = append(out, row)
	}
	return out, rows.Err()
}

// codexParse 是单个 rollout 文件的解析产物。
type codexParse struct {
	messages     []TranscriptMessage
	cwd          string
	gitBranch    string
	model        string
	source       string
	tokensUsed   int64
	createdAt    int64
	updatedAt    int64
	unknownLines int
}

// friendlySource 把 rollout 首行的 originator 映射为友好名。
// state DB 的 source 列会把 Codex Desktop 与 IDE 扩展都归为 "vscode"，
// originator 才分得开。
func friendlySource(originator string) string {
	switch originator {
	case "":
		return ""
	case "codex_cli_rs", "codex-tui":
		return "CLI"
	case "codex_exec":
		return "exec"
	case "codex_vscode":
		return "IDE extension"
	case "codex_work_desktop":
		return "Codex Desktop"
	default:
		return originator
	}
}

// parseRollout 解析一个 Codex rollout 文件。
func parseRollout(path string) (*codexParse, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	out := &codexParse{messages: make([]TranscriptMessage, 0, 64)}
	// eventFallback 收集 event_msg 流，仅当 response_item 完全缺席时启用。
	eventFallback := make([]TranscriptMessage, 0)
	toolIndex := map[string][2]int{}
	sawSessionMeta := false

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
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
		ts := toEpochMS(row["timestamp"])
		if ts > 0 {
			if out.createdAt == 0 {
				out.createdAt = ts
			}
			if ts > out.updatedAt {
				out.updatedAt = ts
			}
		}
		typ, _ := row["type"].(string)
		payload, hasPayload := row["payload"].(map[string]any)
		if !hasPayload {
			if typ != "compacted" && typ != "world_state" {
				out.unknownLines++
			}
			continue
		}

		switch typ {
		case "session_meta":
			if sawSessionMeta {
				continue
			}
			sawSessionMeta = true
			if c, ok := payload["cwd"].(string); ok {
				out.cwd = c
			}
			if o, ok := payload["originator"].(string); ok {
				out.source = friendlySource(o)
			}
			if git, ok := payload["git"].(map[string]any); ok {
				if b, ok := git["branch"].(string); ok {
					out.gitBranch = b
				}
			}
		case "turn_context":
			if out.cwd == "" {
				if c, ok := payload["cwd"].(string); ok {
					out.cwd = c
				}
			}
			if m, ok := payload["model"].(string); ok {
				out.model = m
			}
		case "response_item":
			out.messages = appendResponseItem(out.messages, toolIndex, payload, ts)
		case "event_msg":
			switch pt, _ := payload["type"].(string); pt {
			case "token_count":
				if info, ok := payload["info"].(map[string]any); ok {
					if usage, ok := info["total_token_usage"].(map[string]any); ok {
						if total, ok := usage["total_tokens"].(float64); ok {
							out.tokensUsed = int64(total)
						}
					}
				}
			case "user_message":
				if m, ok := payload["message"].(string); ok && strings.TrimSpace(m) != "" {
					m = strings.TrimSpace(m)
					eventFallback = append(eventFallback, mkMsg(RoleUser, userKind(m), m, ts))
				}
			case "agent_message":
				if m, ok := payload["message"].(string); ok && strings.TrimSpace(m) != "" {
					eventFallback = append(eventFallback, mkMsg(RoleAssistant, KindText, strings.TrimSpace(m), ts))
				}
			}
		case "compacted":
			out.messages = append(out.messages, mkMsg(RoleSystem, KindCompactSummary, "── Context compacted ──", ts))
		case "world_state":
			// 无展示价值，静默跳过。
		default:
			out.unknownLines++
		}
	}
	if err := scanner.Err(); err != nil {
		out.unknownLines++
	}

	// response_item 完全缺席的会话退回 event_msg 流。
	hasReal := false
	for _, m := range out.messages {
		if m.Kind == KindText && m.Text != "" {
			hasReal = true
			break
		}
	}
	if !hasReal && len(eventFallback) > 0 {
		out.messages = eventFallback
	}
	assignSeq(out.messages)
	return out, nil
}

// appendResponseItem 处理一条 response_item payload。
func appendResponseItem(messages []TranscriptMessage, toolIndex map[string][2]int, payload map[string]any, ts int64) []TranscriptMessage {
	switch pt, _ := payload["type"].(string); pt {
	case "message":
		role, _ := payload["role"].(string)
		parts := make([]string, 0, 4)
		switch content := payload["content"].(type) {
		case []any:
			for _, raw := range content {
				block, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				switch blockType, _ := block["type"].(string); blockType {
				case "input_text", "output_text", "text":
					if t, ok := block["text"].(string); ok {
						parts = append(parts, t)
					}
				}
			}
		case string:
			parts = append(parts, content)
		}
		text := strings.TrimSpace(strings.Join(parts, "\n\n"))
		if text == "" {
			return messages
		}
		switch role {
		case "user":
			return append(messages, mkMsg(RoleUser, userKind(text), text, ts))
		case "assistant":
			return append(messages, mkMsg(RoleAssistant, KindText, text, ts))
		default:
			return append(messages, mkMsg(RoleSystem, KindMeta, text, ts))
		}

	case "reasoning":
		// encrypted_content 丢弃，只取明文 summary。
		parts := make([]string, 0, 2)
		if summary, ok := payload["summary"].([]any); ok {
			for _, raw := range summary {
				if block, ok := raw.(map[string]any); ok {
					if t, ok := block["text"].(string); ok {
						parts = append(parts, t)
					}
				}
			}
		}
		if len(parts) == 0 {
			return messages
		}
		thinking, _ := clip(strings.Join(parts, "\n\n"), maxToolIO)
		if n := len(messages); n > 0 {
			last := &messages[n-1]
			if last.Role == RoleAssistant && last.Text == "" && last.Thinking == "" {
				last.Thinking = thinking
				return messages
			}
		}
		m := mkMsg(RoleAssistant, KindText, "", ts)
		m.Thinking = thinking
		return append(messages, m)

	case "function_call", "custom_tool_call", "local_shell_call":
		callID := firstString(payload["call_id"], payload["id"])
		name := firstString(payload["name"])
		if name == "" {
			name = "exec"
		}
		rawInput := firstString(payload["arguments"], payload["input"])
		if rawInput == "" {
			if action, ok := payload["action"]; ok && action != nil {
				if b, err := json.Marshal(action); err == nil {
					rawInput = string(b)
				}
			}
		}
		// arguments 通常是 JSON 字符串；能解开就用结构化值做 preview。
		var previewSource any = rawInput
		if rawInput != "" {
			var decoded any
			if err := json.Unmarshal([]byte(rawInput), &decoded); err == nil {
				previewSource = decoded
			}
		}
		call := ToolCallView{
			ID:           callID,
			Name:         name,
			InputPreview: makePreview(previewSource),
		}
		if rawInput != "" {
			call.Input, _ = clip(rawInput, maxToolIO)
		}
		// 工具调用挂在最近的 assistant 文本消息上；没有就造一个空宿主。
		needHost := true
		if n := len(messages); n > 0 {
			last := messages[n-1]
			needHost = !(last.Role == RoleAssistant && last.Kind == KindText)
		}
		if needHost {
			messages = append(messages, mkMsg(RoleAssistant, KindText, "", ts))
		}
		mi := len(messages) - 1
		messages[mi].ToolCalls = append(messages[mi].ToolCalls, call)
		if callID != "" {
			toolIndex[callID] = [2]int{mi, len(messages[mi].ToolCalls) - 1}
		}
		return messages

	case "function_call_output", "custom_tool_call_output":
		callID, _ := payload["call_id"].(string)
		pos, ok := toolIndex[callID]
		if !ok {
			return messages
		}
		outText := ""
		switch o := payload["output"].(type) {
		case string:
			outText = o
		case map[string]any:
			if c, ok := o["content"].(string); ok {
				outText = c
			} else if b, err := json.Marshal(o); err == nil {
				outText = string(b)
			}
		}
		messages[pos[0]].ToolCalls[pos[1]].Output, _ = clip(outText, maxToolIO)
		return messages
	}
	return messages
}

// firstString 返回第一个非空字符串值。
func firstString(values ...any) string {
	for _, v := range values {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return ""
}

// rolloutNativeID 从 rollout 文件名 stem 提取会话 uuid：
// rollout-2026-08-14T11-47-18-<uuid> → <uuid>。
func rolloutNativeID(stem string) string {
	rest, ok := strings.CutPrefix(stem, "rollout-")
	if !ok {
		return stem
	}
	// 跳过 "YYYY-MM-DDTHH-MM-SS-" 前缀（19 字符 + 尾随 '-'）
	if len(rest) > 20 && rest[10] == 'T' {
		return rest[20:]
	}
	return stem
}

// codexMeta 由 rollout 解析结果与文件信息组装会话元数据。
func codexMeta(ref sessionFileRef, p *codexParse, archivedDir string) SessionMeta {
	title := titleFromMessages(p.messages)
	if title == "" {
		title = untitled
	}
	created, updated := p.createdAt, p.updatedAt
	if created == 0 {
		created = ref.mtimeMS
	}
	if updated == 0 {
		updated = ref.mtimeMS
	}
	return SessionMeta{
		Key:          codexAgent + ":" + ref.nativeID,
		ID:           ref.nativeID,
		Agent:        codexAgent,
		Title:        title,
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
		Archived:     archivedDir != "" && strings.HasPrefix(ref.filePath, archivedDir),
		Source:       p.source,
	}
}
