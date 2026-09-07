package disk

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"sort"
	"strings"
)

// maxDiskList 限制列表扫描量：Cursor/ZCode 本机可能有上千份转录，
// 列表只取最近 N 条，打开会话时再按 id 精确定位文件。
const maxDiskList = 400

// peekJSONLMaps 只读打开 jsonl 的前 maxBytes，解析最多 maxLines 条对象。
func peekJSONLMaps(path string, maxBytes, maxLines int) []map[string]any {
	if maxBytes <= 0 {
		maxBytes = 8 << 10
	}
	if maxLines <= 0 {
		maxLines = 8
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	limited := &ioLimited{r: f, remain: maxBytes}
	sc := bufio.NewScanner(limited)
	sc.Buffer(make([]byte, 0, 64*1024), 256*1024)
	out := make([]map[string]any, 0, maxLines)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var obj map[string]any
		if json.Unmarshal([]byte(line), &obj) != nil {
			continue
		}
		out = append(out, obj)
		if len(out) >= maxLines {
			break
		}
	}
	return out
}

// ioLimited 在读满 remain 字节后返回 EOF，避免列表扫描吞掉 10MB+ 转录。
type ioLimited struct {
	r      *os.File
	remain int
}

func (l *ioLimited) Read(p []byte) (int, error) {
	if l.remain <= 0 {
		return 0, io.EOF
	}
	if len(p) > l.remain {
		p = p[:l.remain]
	}
	n, err := l.r.Read(p)
	l.remain -= n
	return n, err
}

func capSessionRefs(refs []sessionFileRef, n int) []sessionFileRef {
	if n <= 0 || len(refs) <= n {
		return refs
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].mtimeMS > refs[j].mtimeMS })
	return refs[:n]
}

func jsonString(m map[string]any, keys ...string) string {
	cur := any(m)
	for _, k := range keys {
		obj, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = obj[k]
	}
	s, _ := cur.(string)
	return s
}

func contentText(raw any) string {
	switch v := raw.(type) {
	case string:
		return v
	case []any:
		var b strings.Builder
		for _, item := range v {
			obj, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if t, _ := obj["text"].(string); t != "" {
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
				b.WriteString(t)
			}
		}
		return b.String()
	default:
		return ""
	}
}
