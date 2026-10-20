package server

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
)

// handleTaskRunEvents projects canonical ACC history through Pocket. The
// binding and workspace are resolved server-side; clients cannot supply a run.
func (s *Server) handleTaskRunEvents(w http.ResponseWriter, r *http.Request, taskID string) {
	if s.taskStore == nil || s.mcpClient == nil {
		s.writeStructuredError(w, r, http.StatusServiceUnavailable, CodeUpstreamUnavailable, "ACC task/run projection unavailable")
		return
	}
	ws := s.workspaceIDFromRequest(r)
	binding, err := s.taskStore.GetTaskRunBinding(r.Context(), ws, taskID)
	if err != nil {
		s.writeStructuredError(w, r, http.StatusNotFound, CodeNotFound, "task/run binding not found")
		return
	}
	after := uint64(0)
	raw := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	if raw == "" {
		raw = strings.TrimSpace(r.URL.Query().Get("after"))
	}
	if raw != "" {
		n, e := strconv.ParseUint(raw, 10, 64)
		if e != nil {
			s.writeStructuredError(w, r, http.StatusBadRequest, CodeInvalidRequest, "after must be an unsigned sequence")
			return
		}
		after = n
	}
	events, err := s.mcpClient.ListRunEvents(r.Context(), binding.RunID, after)
	if err != nil {
		s.writeStructuredError(w, r, http.StatusBadGateway, CodeUpstreamUnavailable, "ACC run events unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	for i, ev := range events {
		// ⚠ §946：事件编码失败原来被 `_` 吞掉 ⇒ 客户端收到一条**空 data**的事件
		//　⇒ ⇒⇒ 停下并留痕（状态行早已提交 ⇒ 改不了状态码）。
		payload, mErr := json.Marshal(ev)
		if mErr != nil {
			log.Printf("[ERROR] 任务运行事件编码失败（已写出 %d/%d 条，中断该响应）：%v", i, len(events), mErr)
			break
		}
		// ⚠ §946：SSE 是**流式**响应 ⇒ 写失败（客户端断开）意味着**这条流已经死了**
		//　⇒ ⇒⇒ 继续写只会一次次失败 ⇒ ⇒⇒ 停下并留痕。
		if _, err := w.Write([]byte("id: " + strconv.FormatUint(ev.Sequence, 10) + "\nevent: " + ev.EventType + "\ndata: " + string(payload) + "\n\n")); err != nil {
			log.Printf("[WARN] 任务运行事件流写出失败（已写出 %d/%d 条，中断该响应）：%v", i, len(events), err)
			break
		}
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}
