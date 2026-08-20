package disk

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// 本文件是 Wake crates/wake-core/src/adapters/parse_utils.rs 的 Go 移植：
// 七家 adapter 共用的截断/时间/标题/预览工具。行为对齐 Rust 版，便于两边
// 对同一份转录得到一致的归一化结果。

// clip 把 s 截断到 max 字节（在 UTF-8 边界上），返回截断后的文本与是否被截断。
func clip(s string, max int) (string, bool) {
	if len(s) <= max {
		return s, false
	}
	end := max
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end] + "\n… (truncated)", true
}

// assignSeq 回填会话内稳定序号。必须在消息序列定型后、返回前调用。
func assignSeq(messages []TranscriptMessage) {
	for i := range messages {
		messages[i].Seq = i
	}
}

// mtimeMS 把文件 mtime 转成 epoch 毫秒。
func mtimeMS(info fs.FileInfo) int64 {
	if info == nil {
		return 0
	}
	return info.ModTime().UnixMilli()
}

// projectNameOf 从 cwd 推导项目显示名（路径末段）。
func projectNameOf(cwd string) string {
	if cwd == "" {
		return "Unknown project"
	}
	name := filepath.Base(cwd)
	if name == "" || name == "." || name == string(filepath.Separator) {
		return "Unknown project"
	}
	return name
}

// isoMS 解析 RFC3339 时间串为 epoch 毫秒；失败返回 0。
func isoMS(s string) int64 {
	if s == "" {
		return 0
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UnixMilli()
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UnixMilli()
	}
	return 0
}

// toEpochMS 把 JSON 值（ISO8601 字符串 / unix 秒 / unix 毫秒）转成 epoch 毫秒。
func toEpochMS(v any) int64 {
	switch n := v.(type) {
	case float64:
		if n > 1e12 {
			return int64(n)
		}
		if n > 0 {
			return int64(n * 1000)
		}
		return 0
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			return 0
		}
		return toEpochMS(f)
	case string:
		return isoMS(n)
	default:
		return 0
	}
}

// userKind 判定 user 消息的 kind：注入内容归 Meta 折叠。
func userKind(text string) MessageKind {
	if isInjectedUserContent(text) {
		return KindMeta
	}
	return KindText
}

// mkMsg 构造一条纯文本消息（含 clip）。
func mkMsg(role Role, kind MessageKind, text string, ts int64) TranscriptMessage {
	clipped, truncated := clip(text, maxMsgText)
	return TranscriptMessage{
		Role:      role,
		Kind:      kind,
		Text:      clipped,
		Truncated: truncated,
		Timestamp: ts,
	}
}

// titleFromMessages 用首条真实用户消息推导标题（七家共用的回退链）。
func titleFromMessages(messages []TranscriptMessage) string {
	for _, m := range messages {
		if m.Role == RoleUser && m.Kind == KindText {
			if t := cleanTitleCandidate(m.Text); t != "" {
				return t
			}
		}
	}
	return ""
}

// listJSONLRefs 递归枚举 dir 下的非空 .jsonl 为 sessionFileRef。
// nativeID 从文件 stem 提取会话 id（多数 agent 恒等，Codex 需剥 rollout 前缀）。
func listJSONLRefs(dir, agent string, nativeID func(string) string) []sessionFileRef {
	refs := make([]sessionFileRef, 0)
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return refs
	}
	// WalkDir 忽略单个条目的错误（权限/竞态删除），保证聚合尽力而为。
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() {
			return nil //nolint:nilerr // 单个条目失败不应中断整体扫描
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".jsonl") {
			return nil
		}
		fi, ferr := d.Info()
		if ferr != nil || fi.Size() == 0 {
			return nil
		}
		stem := strings.TrimSuffix(name, ".jsonl")
		refs = append(refs, sessionFileRef{
			agent:    agent,
			nativeID: nativeID(stem),
			filePath: path,
			mtimeMS:  mtimeMS(fi),
			size:     fi.Size(),
		})
		return nil
	})
	return refs
}

// toolCallView 把 tool_use 块归一化为 ToolCallView（preview + pretty input）。
func toolCallView(id, name string, input any, output string, isError bool) ToolCallView {
	inputJSON := ""
	if input != nil {
		if b, err := json.MarshalIndent(input, "", "  "); err == nil {
			inputJSON = string(b)
		}
	}
	if name == "" {
		name = "tool"
	}
	view := ToolCallView{
		ID:           id,
		Name:         name,
		InputPreview: makePreview(input),
		IsError:      isError,
	}
	if inputJSON != "" {
		view.Input, _ = clip(inputJSON, maxToolIO)
	}
	if output != "" {
		view.Output, _ = clip(output, maxToolIO)
	}
	return view
}

// makePreview 生成工具输入的单行摘要。
func makePreview(input any) string {
	const maxPreview = 200
	cand := ""
	switch v := input.(type) {
	case nil:
		return ""
	case string:
		cand = v
	case map[string]any:
		for _, key := range []string{"command", "file_path", "path", "pattern", "query", "url", "description"} {
			if s, ok := v[key].(string); ok && strings.TrimSpace(s) != "" {
				cand = s
				break
			}
		}
		if cand == "" {
			if b, err := json.Marshal(v); err == nil {
				cand = string(b)
			}
		}
	default:
		if b, err := json.Marshal(v); err == nil {
			cand = string(b)
		}
	}
	one := strings.Join(strings.Fields(cand), " ")
	return truncateRunes(one, maxPreview)
}

// truncateRunes 按 rune 截断并追加省略号。
func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}

// cleanTitleCandidate 清洗标题候选：剥 slash-command 壳与 system-reminder 等
// 标签，压成单行并截断。返回空串表示该候选不可用。
func cleanTitleCandidate(raw string) string {
	s := stripTagBlock(raw, "system-reminder")
	s = stripTagBlock(s, "local-command-caveat")
	s = stripTagBlock(s, "local-command-stdout")

	args, hasArgs := extractTag(s, "command-args")
	name, hasName := extractTag(s, "command-name")
	if hasArgs || hasName {
		switch {
		case hasArgs && strings.TrimSpace(args) != "":
			s = args
		case hasName:
			s = name
		default:
			s = ""
		}
	}

	// 去掉残余短标签（<foo> → 空格；未闭合的 '<' 原样保留）
	var out strings.Builder
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		if runes[i] != '<' {
			out.WriteRune(runes[i])
			continue
		}
		var tag []rune
		closed := false
		j := i + 1
		for ; j < len(runes); j++ {
			if runes[j] == '>' {
				closed = true
				break
			}
			if runes[j] == '\n' || len(tag) > 60 {
				break
			}
			tag = append(tag, runes[j])
		}
		if closed {
			out.WriteRune(' ')
			i = j
			continue
		}
		out.WriteRune('<')
		out.WriteString(string(tag))
		i = j - 1
	}

	compact := strings.Join(strings.Fields(out.String()), " ")
	if compact == "" {
		return ""
	}
	return truncateRunes(compact, maxTitle)
}

// stripTagBlock 删除 <tag>...</tag> 区块（未闭合时丢弃其后全部内容）。
func stripTagBlock(s, tag string) string {
	openTag, closeTag := "<"+tag+">", "</"+tag+">"
	var out strings.Builder
	rest := s
	for {
		i := strings.Index(rest, openTag)
		if i < 0 {
			out.WriteString(rest)
			return out.String()
		}
		out.WriteString(rest[:i])
		out.WriteString(" ")
		j := strings.Index(rest[i:], closeTag)
		if j < 0 {
			return out.String()
		}
		rest = rest[i+j+len(closeTag):]
	}
}

// extractTag 取出 <tag>...</tag> 的内容。
func extractTag(s, tag string) (string, bool) {
	openTag, closeTag := "<"+tag+">", "</"+tag+">"
	i := strings.Index(s, openTag)
	if i < 0 {
		return "", false
	}
	rest := s[i+len(openTag):]
	j := strings.Index(rest, closeTag)
	if j < 0 {
		return "", false
	}
	return rest[:j], true
}

// injectedPrefixes 是 IDE/CLI 注入型「用户」消息的前缀特征。
var injectedPrefixes = []string{
	"<recommended_plugins",
	"<environment_context",
	"<user_instructions",
	"<permissions",
	"<workspace",
	"<system-",
	"<context ",
	"<session_context",
	"IMPORTANT: Do NOT read",
	"Caveat: The messages below",
	"# Files pasted by the user",
}

// isInjectedUserContent 判断是否为注入型「用户」消息：非用户手打，应折叠为
// Meta 且不参与标题推导。
func isInjectedUserContent(text string) bool {
	t := strings.TrimLeft(text, " \t\r\n")
	for _, p := range injectedPrefixes {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	// Codex 的 skill 展开体：skill 目录 + SKILL.md 路径
	if strings.Contains(t, "/.codex/plugins/") {
		return true
	}
	return strings.Contains(t, "/plugins/cache/") && strings.Contains(t, "SKILL.md")
}
