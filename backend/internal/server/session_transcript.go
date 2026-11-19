package server

import (
	"net/http"
	"strconv"
	"strings"
)

func (s *Server) handleTaskSessionDetail(w http.ResponseWriter, r *http.Request, taskID, sessionID string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	title := sessionID
	for _, c := range s.companionCandidates() {
		if sess, err := c.GetSession("", sessionID); err == nil && sess.Title != "" {
			title = sess.Title
			break
		}
	}
	writeSessionJSON(w, map[string]any{
		"id": sessionID, "taskId": taskID, "title": title,
		"tokens": map[string]any{"input": nil, "cacheRead": nil, "output": nil},
	})
}

func (s *Server) handleTaskSessionTranscript(w http.ResponseWriter, r *http.Request, taskID, sessionID string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if len(s.companionCandidates()) == 0 {
		http.Error(w, "companion not configured", http.StatusServiceUnavailable)
		return
	}
	kind := r.URL.Query().Get("kind")
	types := r.URL.Query().Get("types")
	// 增量续传（docs/2026-09-09-list-sync-rules.md §4.1）：after_seq 为客户端
	// 已持有的最大消息 seq（keyset 游标），limit 限制单批行数。两者缺省时
	// 行为与旧契约完全一致（全量、companion 默认 limit）。
	afterSeq, _ := strconv.Atoi(r.URL.Query().Get("after_seq"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	// 会话 id 只在持有它的 companion 上命中（dispatch 会话在执行 runtime 的
	// companion，本机 companion 只有 acc_link 自建档）—— 按序试错。
	var sess any
	var msgs any
	var lastErr error
	for _, c := range s.companionCandidates() {
		gotSess, gotMsgs, err := c.GetTranscriptPage(kind, sessionID, types, afterSeq, limit)
		if err == nil {
			sess, msgs = gotSess, gotMsgs
			break
		}
		lastErr = err
	}
	if lastErr != nil && sess == nil {
		http.Error(w, lastErr.Error(), http.StatusBadGateway)
		return
	}
	_ = taskID
	writeSessionJSON(w, map[string]any{"session": sess, "messages": msgs})
}

func (s *Server) handleExtractTitle(w http.ResponseWriter, r *http.Request, taskID, sessionID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	title := sessionID
	for _, c := range s.companionCandidates() {
		_, msgs, err := c.GetTranscript("", sessionID, "user")
		if err != nil {
			continue
		}
		for _, m := range msgs {
			if t := strings.TrimSpace(m.Text); t != "" {
				runes := []rune(t)
				if len(runes) > 80 {
					t = string(runes[:80])
				}
				title = t
				break
			}
		}
		if title != sessionID {
			break
		}
	}
	_ = taskID
	writeSessionJSON(w, map[string]any{"title": title})
}

func (s *Server) handleSessionSummarize(w http.ResponseWriter, r *http.Request, taskID, sessionID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	summary := ""
	for _, c := range s.companionCandidates() {
		_, msgs, err := c.GetTranscript("", sessionID, "user,assistant")
		if err != nil {
			continue
		}
		var b strings.Builder
		for _, m := range msgs {
			if m.Text == "" {
				continue
			}
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(m.Type)
			b.WriteString(": ")
			b.WriteString(m.Text)
			if b.Len() > 1500 {
				break
			}
		}
		summary = b.String()
		if len([]rune(summary)) > 400 {
			summary = string([]rune(summary)[:400]) + "…"
		}
		break
	}
	if summary == "" {
		summary = "暂无摘要"
	}
	_ = taskID
	writeSessionJSON(w, map[string]any{"summary": summary})
}

// writeSessionJSON 写会话转写相关的 JSON 响应。
//
// ⚠ §946：走 writeJSON ⇒ 编码错误不再被吞（原来这里是 `_ =`）。
// 　 字节与旧实现逐字相同（§946 实测：Marshal+"\n" == Encoder.Encode）。
func writeSessionJSON(w http.ResponseWriter, v any) {
	writeJSON(w, http.StatusOK, v)
}
