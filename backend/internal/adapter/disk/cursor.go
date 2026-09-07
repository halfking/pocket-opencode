package disk

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Cursor 把对话写成 ~/.cursor/projects/<slug>/agent-transcripts/<uuid>/<uuid>.jsonl
// 一行一个 {role,message.content[]}。列表只 peek 前几 KB 抽 user_query 当标题。

const cursorAgent = "cursor"

type cursorReader struct {
	root string
}

func newCursorReader(home string) *cursorReader {
	if home == "" {
		return &cursorReader{}
	}
	return &cursorReader{root: filepath.Join(home, ".cursor", "projects")}
}

func (r *cursorReader) agent() string       { return cursorAgent }
func (r *cursorReader) displayName() string { return "Cursor (disk)" }
func (r *cursorReader) dataPath() string    { return r.root }

func (r *cursorReader) detect() bool {
	if r.root == "" {
		return false
	}
	info, err := os.Stat(r.root)
	return err == nil && info.IsDir()
}

func (r *cursorReader) listRefs() []sessionFileRef {
	if !r.detect() {
		return nil
	}
	all := listJSONLRefs(r.root, cursorAgent, func(stem string) string { return stem })
	filtered := all[:0]
	for _, ref := range all {
		if strings.Contains(ref.filePath, string(filepath.Separator)+"agent-transcripts"+string(filepath.Separator)) {
			filtered = append(filtered, ref)
		}
	}
	return capSessionRefs(filtered, maxDiskList)
}

func (r *cursorReader) listSessions() ([]SessionMeta, error) {
	if !r.detect() {
		return nil, fmt.Errorf("cursor data directory not found: %s", r.root)
	}
	refs := r.listRefs()
	out := make([]SessionMeta, 0, len(refs))
	for _, ref := range refs {
		title := cursorPeekTitle(ref.filePath)
		if title == "" {
			title = untitled
		}
		out = append(out, SessionMeta{
			Key:       cursorAgent + ":" + ref.nativeID,
			ID:        ref.nativeID,
			Agent:     cursorAgent,
			Title:     title,
			FilePath:  ref.filePath,
			UpdatedAt: ref.mtimeMS,
			CreatedAt: ref.mtimeMS,
			SizeBytes: ref.size,
		})
	}
	return out, nil
}

func (r *cursorReader) transcript(sessionID string) (SessionMeta, []TranscriptMessage, error) {
	path, err := r.findFile(sessionID)
	if err != nil {
		return SessionMeta{}, nil, err
	}
	msgs, model, title := parseCursorJSONL(path)
	fi, _ := os.Stat(path)
	meta := SessionMeta{
		Key:          cursorAgent + ":" + sessionID,
		ID:           sessionID,
		Agent:        cursorAgent,
		Title:        title,
		FilePath:     path,
		Model:        model,
		MessageCount: int64(len(msgs)),
	}
	if fi != nil {
		meta.UpdatedAt = mtimeMS(fi)
		meta.CreatedAt = mtimeMS(fi)
		meta.SizeBytes = fi.Size()
	}
	if meta.Title == "" {
		meta.Title = untitled
	}
	return meta, msgs, nil
}

func (r *cursorReader) findFile(sessionID string) (string, error) {
	for _, ref := range r.listRefs() {
		if ref.nativeID == sessionID {
			return ref.filePath, nil
		}
	}
	// 列表被 cap 截断时回退全盘匹配 id 文件名。
	var found string
	_ = filepath.WalkDir(r.root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() {
			return nil
		}
		if d.Name() == sessionID+".jsonl" {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	if found == "" {
		return "", fmt.Errorf("cursor session not found: %s", sessionID)
	}
	return found, nil
}

func cursorPeekTitle(path string) string {
	for _, obj := range peekJSONLMaps(path, 8<<10, 6) {
		role, _ := obj["role"].(string)
		if role == "" {
			role, _ = obj["type"].(string)
		}
		if role != "user" {
			continue
		}
		msg, _ := obj["message"].(map[string]any)
		if msg == nil {
			continue
		}
		text := contentText(msg["content"])
		if q, ok := extractTag(text, "user_query"); ok && strings.TrimSpace(q) != "" {
			text = q
		} else {
			text = stripTagBlock(text, "timestamp")
		}
		if t := cleanTitleCandidate(text); t != "" {
			return t
		}
	}
	return ""
}

func parseCursorJSONL(path string) ([]TranscriptMessage, string, string) {
	f, err := os.Open(path)
	if err != nil {
		return nil, "", ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	var msgs []TranscriptMessage
	var model, title string
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var obj map[string]any
		if json.Unmarshal([]byte(line), &obj) != nil {
			continue
		}
		role, _ := obj["role"].(string)
		if role == "" {
			role, _ = obj["type"].(string)
		}
		msg, _ := obj["message"].(map[string]any)
		text := ""
		if msg != nil {
			text = contentText(msg["content"])
			if model == "" {
				if m, _ := msg["model"].(string); m != "" && m != "<synthetic>" {
					model = m
				}
			}
		}
		if text == "" {
			continue
		}
		switch role {
		case "user":
			kind := userKind(text)
			m := mkMsg(RoleUser, kind, text, toEpochMS(obj["timestamp"]))
			if kind == KindText && title == "" {
				if q, ok := extractTag(text, "user_query"); ok && strings.TrimSpace(q) != "" {
					title = cleanTitleCandidate(q)
				} else {
					title = cleanTitleCandidate(stripTagBlock(text, "timestamp"))
				}
			}
			msgs = append(msgs, m)
		case "assistant":
			m := mkMsg(RoleAssistant, KindText, text, toEpochMS(obj["timestamp"]))
			m.Model = model
			msgs = append(msgs, m)
		}
	}
	assignSeq(msgs)
	if title == "" {
		title = titleFromMessages(msgs)
	}
	return msgs, model, title
}
