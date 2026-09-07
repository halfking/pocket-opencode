package disk

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ZCode CLI 会话落在 ~/.zcode/cli/agents/sess_<id>/agent_*/transcript.jsonl。
// 列表按 sess_* 目录聚合（一个父会话一条），归档目录 session-archive 一并扫。

const zcodeAgent = "zcode"

type zcodeReader struct {
	agentsDir  string
	archiveDir string
}

func newZcodeReader(home string) *zcodeReader {
	if home == "" {
		return &zcodeReader{}
	}
	root := filepath.Join(home, ".zcode")
	return &zcodeReader{
		agentsDir:  filepath.Join(root, "cli", "agents"),
		archiveDir: filepath.Join(root, "session-archive"),
	}
}

func (r *zcodeReader) agent() string       { return zcodeAgent }
func (r *zcodeReader) displayName() string { return "ZCode (disk)" }
func (r *zcodeReader) dataPath() string    { return r.agentsDir }

func (r *zcodeReader) detect() bool {
	for _, dir := range []string{r.agentsDir, r.archiveDir} {
		if dir == "" {
			continue
		}
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return true
		}
	}
	return false
}

type zcodeSessDir struct {
	id       string
	dir      string
	file     string
	mtimeMS  int64
	size     int64
	archived bool
}

func (r *zcodeReader) listSessDirs() []zcodeSessDir {
	out := make([]zcodeSessDir, 0)
	out = append(out, walkZcodeAgents(r.agentsDir, false)...)
	out = append(out, walkZcodeAgents(r.archiveDir, true)...)
	if len(out) > maxDiskList {
		sort.Slice(out, func(i, j int) bool { return out[i].mtimeMS > out[j].mtimeMS })
		out = out[:maxDiskList]
	}
	return out
}

func walkZcodeAgents(root string, archived bool) []zcodeSessDir {
	if root == "" {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	out := make([]zcodeSessDir, 0)
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "sess_") {
			continue
		}
		dir := filepath.Join(root, e.Name())
		file, size, mtime := firstZcodeTranscript(dir)
		if file == "" {
			continue
		}
		fi, _ := os.Stat(dir)
		if fi != nil && mtimeMS(fi) > mtime {
			mtime = mtimeMS(fi)
		}
		out = append(out, zcodeSessDir{
			id:       e.Name(),
			dir:      dir,
			file:     file,
			mtimeMS:  mtime,
			size:     size,
			archived: archived,
		})
	}
	return out
}

func firstZcodeTranscript(sessDir string) (string, int64, int64) {
	agents, err := os.ReadDir(sessDir)
	if err != nil {
		return "", 0, 0
	}
	var best string
	var bestSize, bestMtime int64
	for _, a := range agents {
		if !a.IsDir() {
			continue
		}
		p := filepath.Join(sessDir, a.Name(), "transcript.jsonl")
		fi, err := os.Stat(p)
		if err != nil || fi.IsDir() || fi.Size() == 0 {
			continue
		}
		mt := mtimeMS(fi)
		if mt >= bestMtime {
			best, bestSize, bestMtime = p, fi.Size(), mt
		}
	}
	return best, bestSize, bestMtime
}

func (r *zcodeReader) listSessions() ([]SessionMeta, error) {
	if !r.detect() {
		return nil, fmt.Errorf("zcode data directory not found: %s", r.agentsDir)
	}
	sess := r.listSessDirs()
	out := make([]SessionMeta, 0, len(sess))
	for _, s := range sess {
		title := zcodePeekTitle(s.file)
		if title == "" {
			title = untitled
		}
		out = append(out, SessionMeta{
			Key:       zcodeAgent + ":" + s.id,
			ID:        s.id,
			Agent:     zcodeAgent,
			Title:     title,
			FilePath:  s.file,
			UpdatedAt: s.mtimeMS,
			CreatedAt: s.mtimeMS,
			SizeBytes: s.size,
			Archived:  s.archived,
		})
	}
	return out, nil
}

func (r *zcodeReader) transcript(sessionID string) (SessionMeta, []TranscriptMessage, error) {
	var hit *zcodeSessDir
	for _, s := range r.listSessDirs() {
		if s.id == sessionID {
			cp := s
			hit = &cp
			break
		}
	}
	if hit == nil {
		return SessionMeta{}, nil, fmt.Errorf("zcode session not found: %s", sessionID)
	}
	msgs, model, title := parseZcodeJSONL(hit.file)
	if title == "" {
		title = untitled
	}
	meta := SessionMeta{
		Key:          zcodeAgent + ":" + sessionID,
		ID:           sessionID,
		Agent:        zcodeAgent,
		Title:        title,
		FilePath:     hit.file,
		UpdatedAt:    hit.mtimeMS,
		CreatedAt:    hit.mtimeMS,
		SizeBytes:    hit.size,
		Archived:     hit.archived,
		Model:        model,
		MessageCount: int64(len(msgs)),
	}
	return meta, msgs, nil
}

func zcodePeekTitle(path string) string {
	for _, obj := range peekJSONLMaps(path, 8<<10, 8) {
		typ, _ := obj["type"].(string)
		if typ != "turn_started" {
			continue
		}
		if t := cleanTitleCandidate(jsonString(obj, "payload", "input")); t != "" {
			return t
		}
	}
	return ""
}

func parseZcodeJSONL(path string) ([]TranscriptMessage, string, string) {
	f, err := os.Open(path)
	if err != nil {
		return nil, "", ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	var msgs []TranscriptMessage
	var model, title string
	var assistant strings.Builder
	flushAssistant := func(ts int64) {
		if assistant.Len() == 0 {
			return
		}
		m := mkMsg(RoleAssistant, KindText, assistant.String(), ts)
		m.Model = model
		msgs = append(msgs, m)
		assistant.Reset()
	}
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var obj map[string]any
		if json.Unmarshal([]byte(line), &obj) != nil {
			continue
		}
		typ, _ := obj["type"].(string)
		ts := isoMS(jsonString(obj, "timestamp"))
		switch typ {
		case "turn_started":
			flushAssistant(ts)
			input := jsonString(obj, "payload", "input")
			if input == "" {
				continue
			}
			kind := userKind(input)
			msgs = append(msgs, mkMsg(RoleUser, kind, input, ts))
			if kind == KindText && title == "" {
				title = cleanTitleCandidate(input)
			}
		case "model_network_status":
			if model == "" {
				if m := jsonString(obj, "payload", "model", "id"); m != "" {
					model = m
				} else if m := jsonString(obj, "payload", "model", "modelId"); m != "" {
					model = m
				}
			}
		case "model_streaming":
			if d := jsonString(obj, "payload", "delta"); d != "" {
				assistant.WriteString(d)
			}
		}
	}
	flushAssistant(0)
	assignSeq(msgs)
	if title == "" {
		title = titleFromMessages(msgs)
	}
	return msgs, model, title
}
