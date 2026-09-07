package disk

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// OpenCode 桌面/CLI 把会话存在 ~/.local/share/opencode/opencode.db（session 表）。
// 只读直开，禁止拷贝数 GB 的库。子会话（parent_id 非空）不进列表。

const opencodeAgent = "opencode"

type opencodeReader struct {
	dbPath string
}

func newOpencodeReader(home string) *opencodeReader {
	if home == "" {
		return &opencodeReader{}
	}
	return &opencodeReader{dbPath: filepath.Join(home, ".local", "share", "opencode", "opencode.db")}
}

func (r *opencodeReader) agent() string       { return opencodeAgent }
func (r *opencodeReader) displayName() string { return "OpenCode (disk)" }
func (r *opencodeReader) dataPath() string    { return r.dbPath }

func (r *opencodeReader) detect() bool {
	if r.dbPath == "" {
		return false
	}
	info, err := os.Stat(r.dbPath)
	return err == nil && !info.IsDir()
}

func (r *opencodeReader) listSessions() ([]SessionMeta, error) {
	if !r.detect() {
		return nil, fmt.Errorf("opencode db not found: %s", r.dbPath)
	}
	h, err := openSQLiteRO(context.Background(), r.dbPath, "opencode")
	if err != nil {
		return nil, err
	}
	defer h.close()

	rows, err := h.db.QueryContext(context.Background(), `
SELECT id, title, directory, time_created, time_updated,
       COALESCE(time_archived, 0), COALESCE(model, ''),
       tokens_input + tokens_output + tokens_reasoning
FROM session
WHERE parent_id IS NULL OR parent_id = ''
ORDER BY time_updated DESC
LIMIT ?`, maxDiskList)
	if err != nil {
		return nil, fmt.Errorf("opencode list sessions: %w", err)
	}
	defer rows.Close()

	out := make([]SessionMeta, 0)
	for rows.Next() {
		var id, title, dir, model string
		var created, updated, archived, tokens int64
		if err := rows.Scan(&id, &title, &dir, &created, &updated, &archived, &model, &tokens); err != nil {
			return nil, err
		}
		if strings.TrimSpace(title) == "" {
			title = untitled
		}
		out = append(out, SessionMeta{
			Key:          opencodeAgent + ":" + id,
			ID:           id,
			Agent:        opencodeAgent,
			Title:        title,
			ProjectPath:  dir,
			ProjectName:  projectNameOf(dir),
			FilePath:     virtualPath(r.dbPath, id),
			CreatedAt:    created,
			UpdatedAt:    updated,
			Model:        model,
			TokensUsed:   tokens,
			Archived:     archived > 0,
			MessageCount: 0,
		})
	}
	return out, rows.Err()
}

func (r *opencodeReader) transcript(sessionID string) (SessionMeta, []TranscriptMessage, error) {
	if !r.detect() {
		return SessionMeta{}, nil, fmt.Errorf("opencode db not found: %s", r.dbPath)
	}
	h, err := openSQLiteRO(context.Background(), r.dbPath, "opencode")
	if err != nil {
		return SessionMeta{}, nil, err
	}
	defer h.close()

	var id, title, dir, model string
	var created, updated, archived, tokens int64
	err = h.db.QueryRowContext(context.Background(), `
SELECT id, title, directory, time_created, time_updated,
       COALESCE(time_archived, 0), COALESCE(model, ''),
       tokens_input + tokens_output + tokens_reasoning
FROM session WHERE id = ?`, sessionID).Scan(&id, &title, &dir, &created, &updated, &archived, &model, &tokens)
	if err != nil {
		return SessionMeta{}, nil, fmt.Errorf("opencode session not found: %s", sessionID)
	}
	if strings.TrimSpace(title) == "" {
		title = untitled
	}
	meta := SessionMeta{
		Key:         opencodeAgent + ":" + id,
		ID:          id,
		Agent:       opencodeAgent,
		Title:       title,
		ProjectPath: dir,
		ProjectName: projectNameOf(dir),
		FilePath:    virtualPath(r.dbPath, id),
		CreatedAt:   created,
		UpdatedAt:   updated,
		Model:       model,
		TokensUsed:  tokens,
		Archived:    archived > 0,
	}
	msgs, err := r.loadMessages(sessionID)
	if err != nil {
		return meta, nil, err
	}
	meta.MessageCount = int64(len(msgs))
	return meta, msgs, nil
}

func (r *opencodeReader) loadMessages(sessionID string) ([]TranscriptMessage, error) {
	h, err := openSQLiteRO(context.Background(), r.dbPath, "opencode")
	if err != nil {
		return nil, err
	}
	defer h.close()

	rows, err := h.db.QueryContext(context.Background(), `
SELECT data FROM message
WHERE session_id = ?
ORDER BY time_created ASC
LIMIT 200`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("opencode messages: %w", err)
	}
	defer rows.Close()

	var msgs []TranscriptMessage
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if m, ok := parseOpencodeMessageJSON(raw); ok {
			msgs = append(msgs, m)
		}
	}
	assignSeq(msgs)
	return msgs, rows.Err()
}

func parseOpencodeMessageJSON(raw string) (TranscriptMessage, bool) {
	var env struct {
		Role string `json:"role"`
		Time struct {
			Created int64 `json:"created"`
		} `json:"time"`
		Model struct {
			ModelID string `json:"modelID"`
		} `json:"model"`
	}
	if json.Unmarshal([]byte(raw), &env) != nil || env.Role == "" {
		return TranscriptMessage{}, false
	}
	role := Role(env.Role)
	if role != RoleUser && role != RoleAssistant && role != RoleSystem {
		return TranscriptMessage{}, false
	}
	text := opencodeMessageText(raw)
	if text == "" {
		return TranscriptMessage{}, false
	}
	m := mkMsg(role, userKind(text), text, env.Time.Created)
	m.Model = env.Model.ModelID
	return m, true
}

func opencodeMessageText(raw string) string {
	var obj map[string]any
	if json.Unmarshal([]byte(raw), &obj) != nil {
		return ""
	}
	if t := contentText(obj["content"]); t != "" {
		return t
	}
	if parts, ok := obj["parts"].([]any); ok {
		return contentText(parts)
	}
	if t, _ := obj["text"].(string); t != "" {
		return t
	}
	return ""
}
