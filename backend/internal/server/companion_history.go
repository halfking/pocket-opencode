package server

// companion_history.go —— **任务历史**与**生成的文件**的读面。
//
// ★ 这一层补的是什么（2026-10-27 实测，不是推测）：
//
//	1. 任务历史：agent-companion 的 GET /api/v1/runs 此前对 **ACC 派单**
//	   恒返回 {"runs":null,"total":0} —— 根因是 ACC 派单走
//	   accbridge.FacadeExecutor，不经过 API 层的 operate，所以终态 run
//	   从来没被留痕（agent-companion 本轮已修，见 accbridge.RunRecorder）。
//	   留痕修好之后，daemon 侧有历史了，但 **pocketd 侧没有出口**：
//	   CompanionClient.ListRuns 存在，却没有任何路由用它 ⇒ 手机上依然
//	   「看不到任务历史」。
//
//	2. 生成的文件：daemon 侧本来就有产出目录
//	   (<StateDir>/artifacts/<dispatch_id>/output.txt) 和字节路由
//	   /api/v1/artifacts/:dispatch_id/:name，但 pocketd 既没有客户端方法、
//	   也没有路由 ⇒ 手机上看不到任何产出。
//
// 所以这里补的是**最后一跳的出口**：读面在 daemon 侧（accbridge RunRecorder
// + artifacts 清单路由），出口在 pocketd 侧（本文件）。
//
// 数据来源是 agent-companion（不是 ACC）：ACC 的 canonical task 列表在本机
// 实测是空的，按 task_id 查会话永远查不到。沿用 /api/acc/approvals 的
// 先例——同一个前缀下，数据源是 companion。
//
// 鉴权沿用 pocketd 惯例：requireAuth（用户态）；daemon 侧的鉴权由
// CompanionClient 用 AC_API_SECRET 完成，pocketd 不代做。

import (
	"net/http"
	"strconv"
	"strings"
)

// accRunItem is one run in the task history, as the App sees it.
type accRunItem struct {
	RunID      string `json:"run_id"`
	DispatchID string `json:"dispatch_id"`
	TaskID     string `json:"task_id"`
	AgentID    string `json:"agent_id"`
	Kind       string `json:"kind"`
	State      string `json:"state"`
	SessionID  string `json:"session_id"`
	Updates    int    `json:"updates"`
	LastEvent  string `json:"last_event"`
	Retained   bool   `json:"retained"`
	StartedAt  string `json:"started_at"`
	EndedAt    string `json:"ended_at"`
	// Answer is the agent's final reply. Empty means the run produced
	// none — which is a different fact from "not read yet".
	Answer string `json:"answer"`
}

// accArtifactFile is one produced file.
//
// SHA256 is not decoration: the completion declares a content_hash for
// the product, and this is the number a consumer compares against.
type accArtifactFile struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
	ReadPath string `json:"read_path"`
}

// handleACCRuns lists the task history: the runs the host executed.
//
// GET /api/acc/runs[?limit=N]
//
// ★ 503 when companion is not configured: an empty list and "the history
//
//	surface is not wired" are different facts, and rendering them the
//	same way is how a broken integration looks healthy.
func (s *Server) handleACCRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeStructuredJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	if s.companion == nil {
		writeStructuredJSONError(w, http.StatusServiceUnavailable, "companion_not_configured",
			"agent-companion client not configured; set POCKET_COMPANION_URL")
		return
	}
	limit := 50
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 200 {
		limit = 200
	}
	runs, err := s.companion.ListRuns(limit)
	if err != nil {
		writeStructuredJSONError(w, http.StatusBadGateway, "companion_unavailable",
			"read agent-companion run history failed: "+err.Error())
		return
	}
	out := make([]accRunItem, 0, len(runs))
	for _, r := range runs {
		out = append(out, accRunItem{
			RunID:      r.RunID,
			DispatchID: r.Dispatch,
			TaskID:     r.TaskID,
			AgentID:    r.AgentID,
			Kind:       r.Kind,
			State:      r.State,
			SessionID:  r.SessionID,
			Updates:    r.Updates,
			LastEvent:  r.LastEvent,
			Retained:   r.Retained,
			StartedAt:  r.StartedAt,
			EndedAt:    r.EndedAt,
			Answer:     r.Answer,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"source": "companion",
		"count":  len(out),
		"runs":   out,
	})
}

// handleACCRunOps serves the per-run read faces.
//
// GET /api/acc/runs/{run_id}/artifacts
//
// One request answers everything about what a run produced:
//
//   - artifacts: the tools it invoked and what they returned;
//   - files: the files it left on disk (the "生成的文件");
//   - truncated/dropped: whether the tool list was capped — a capped
//     list must never be rendered as "that was everything".
//
// The two halves come from two daemon routes and are joined here,
// because a user asking "what did this run produce?" wants one answer,
// not two.
func (s *Server) handleACCRunOps(w http.ResponseWriter, r *http.Request) {
	if s.companion == nil {
		writeStructuredJSONError(w, http.StatusServiceUnavailable, "companion_not_configured",
			"agent-companion client not configured; set POCKET_COMPANION_URL")
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/acc/runs/")
	rest = strings.Trim(rest, "/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[1] != "artifacts" || parts[0] == "" {
		writeStructuredJSONError(w, http.StatusNotFound, "not_found", "unknown run route")
		return
	}
	if r.Method != http.MethodGet {
		writeStructuredJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	runID := parts[0]
	arts, dropped, truncated, err := s.companion.ListRunArtifacts(runID)
	if err != nil {
		writeStructuredJSONError(w, http.StatusBadGateway, "companion_unavailable",
			"read agent-companion run artifacts failed: "+err.Error())
		return
	}
	// The dispatch's product area is addressed by the dispatch id. For
	// an ACC-dispatched run the daemon's run id IS the dispatch id (the
	// facade is started with req.RunID = event.DispatchID), so the run id
	// is the right key. A run with no dispatch id produced no product
	// area — that is reported as an empty list, not as an error.
	files := []accArtifactFile{}
	if runID != "" {
		fs, _, ferr := s.companion.ListArtifactFiles(runID)
		if ferr == nil && fs != nil {
			for _, f := range fs {
				files = append(files, accArtifactFile{
					Name:     f.Name,
					Size:     f.Size,
					SHA256:   f.SHA256,
					ReadPath: f.ReadPath,
				})
			}
		}
	}
	out := make([]map[string]any, 0, len(arts))
	for _, a := range arts {
		out = append(out, map[string]any{
			"tool_call_id": a.ToolCallID,
			"tool_title":   a.ToolTitle,
			"tool_kind":    a.ToolKind,
			"content":      a.Content,
			"at":           a.At,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"source":     "companion",
		"run_id":     runID,
		"artifacts":  out,
		"count":      len(out),
		"dropped":    dropped,
		"truncated":  truncated,
		"files":      files,
		"file_count": len(files),
	})
}

// handleACCArtifactOps serves the bytes of one produced file.
//
// GET /api/acc/artifacts/{dispatch_id}/{name}
//
// The bytes are proxied verbatim and the daemon's digest rides along in
// X-Artifact-Sha256, so the client can check the bytes it got against
// the content_hash the completion declared.
func (s *Server) handleACCArtifactOps(w http.ResponseWriter, r *http.Request) {
	if s.companion == nil {
		writeStructuredJSONError(w, http.StatusServiceUnavailable, "companion_not_configured",
			"agent-companion client not configured; set POCKET_COMPANION_URL")
		return
	}
	if r.Method != http.MethodGet {
		writeStructuredJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/acc/artifacts/")
	rest = strings.Trim(rest, "/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		writeStructuredJSONError(w, http.StatusNotFound, "not_found", "unknown artifact route")
		return
	}
	payload, digest, err := s.companion.GetArtifactBytes(r.Context(), parts[0], parts[1])
	if err != nil {
		writeStructuredJSONError(w, http.StatusBadGateway, "companion_unavailable",
			"read agent-companion artifact failed: "+err.Error())
		return
	}
	// ★ JSON，不是裸字节。
	//
	// daemon 的字节路由给的是 text/plain（ACC 是按文件契约消费它的），
	// 但移动端的 http 客户端只解 JSON（httpOnce 里是 res.json()，遇到
	// 非 JSON 会直接报错）⇒ 这里包一层，让 App 走同一条 JSON 通道。
	//
	// 摘要一起带回来：完成包声明的是 content_hash，App 拿到字节后能当场
	// 对账，而不是"看起来像那个文件"。
	writeJSON(w, http.StatusOK, map[string]any{
		"dispatch_id": parts[0],
		"name":        parts[1],
		"content":     string(payload),
		"sha256":      digest,
		"size":        len(payload),
	})
}
