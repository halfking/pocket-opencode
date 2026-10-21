package server

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"

	"github.com/halfking/pocket-opencode/backend/internal/mcp"
)

// sessionBundleRow is one session on the task-detail page.
type sessionBundleRow struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Lane         string `json:"lane"` // current | historical | local
	AgentKind    string `json:"agentKind,omitempty"`
	AgentSession string `json:"agentSessionId,omitempty"`
	GwSessionID  string `json:"gwSessionId,omitempty"`
	InstanceID   string `json:"instanceId,omitempty"`
	Role         string `json:"role,omitempty"`
	StartedAt    string `json:"startedAt,omitempty"`
	EndedAt      string `json:"endedAt,omitempty"`
	TokensIn     int    `json:"tokensIn"`
	TokensOut    int    `json:"tokensOut"`
	TokensCache  *int   `json:"tokensCache"`
}

type sessionBundle struct {
	Current     []sessionBundleRow `json:"current"`
	Historical  []sessionBundleRow `json:"historical"`
	LocalOnly   []sessionBundleRow `json:"localOnly"`
	UsageTotals struct {
		Input  int  `json:"input"`
		Output int  `json:"output"`
		Cache  *int `json:"cache"`
	} `json:"usageTotals"`
}

type accMeta struct {
	AgentKind      string `json:"agent_kind"`
	AgentSessionID string `json:"agent_session_id"`
	GwSessionID    string `json:"gw_session_id"`
	SessionPath    string `json:"session_path"`
	DispatchID     string `json:"dispatch_id"`
}

func (s *Server) handleTaskSessionBundle(w http.ResponseWriter, r *http.Request, taskID string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	out := sessionBundle{
		Current:    []sessionBundleRow{},
		Historical: []sessionBundleRow{},
		LocalOnly:  []sessionBundleRow{},
	}

	seen := map[string]struct{}{}
	if s.mcpClient != nil {
		rows, err := s.mcpClient.ListSessions(r.Context(), taskID, 100)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		for _, row := range rows {
			item := accSessionToRow(row)
			if item.AgentSession != "" {
				seen[item.AgentSession] = struct{}{}
			}
			seen[item.ID] = struct{}{}
			if item.Lane == "current" {
				out.Current = append(out.Current, item)
			} else {
				out.Historical = append(out.Historical, item)
			}
			out.UsageTotals.Input += item.TokensIn
			out.UsageTotals.Output += item.TokensOut
		}
	}

	if s.taskStore != nil {
		links, err := s.taskStore.ListSessionsForTaskScoped(r.Context(), taskID, s.workspaceIDFromRequest(r))
		if err == nil {
			for _, link := range links {
				if _, ok := seen[link.SessionID]; ok {
					continue
				}
				out.LocalOnly = append(out.LocalOnly, sessionBundleRow{
					ID:           link.SessionID,
					Title:        link.SessionID,
					Lane:         "local",
					AgentSession: link.SessionID,
					InstanceID:   link.InstanceID,
					Role:         link.Role,
				})
			}
		}
	}

	if len(out.Current) == 0 && len(out.Historical) == 0 && len(out.LocalOnly) == 0 {
		if row := s.diskSessionBundleRow(r.Context(), s.workspaceIDFromRequest(r), taskID); row != nil {
			out.Current = append(out.Current, *row)
		}
	}

	writeJSON(w, http.StatusOK, out)
}

// accSessionToRow 把 ACC 上报的一条会话映射成任务详情页的一行。
//
// 取值顺序：**ACC 顶层字段优先，metadata 兜底**。
// 2026-10-22 实测 ACC 的 acc_list_sessions 把 agent_session_id /
// gw_session_id / dispatch_id 放在**顶层**（sessionmanager.SessionRecord），
// 从来不返回 "metadata" 这个嵌套对象；旧实现只读 metadata，于是
// agent_session_id 取不到、退到 row.SessionID（ACC 也不返回 "session_id"，
// 是空串）⇒ session-bundle 里的会话 id 是空的（实测 `{"id":""}`），
// 会话点不开。metadata 分支保留给更老的上报格式。
func accSessionToRow(row mcp.AccSession) sessionBundleRow {
	var meta accMeta
	if len(row.Metadata) > 0 {
		_ = json.Unmarshal(row.Metadata, &meta)
	}
	pick := func(top string, fallback string) string {
		if v := strings.TrimSpace(top); v != "" {
			return v
		}
		return strings.TrimSpace(fallback)
	}
	agentSess := pick(row.AgentSessionID, meta.AgentSessionID)
	if agentSess == "" {
		agentSess = pick(row.SessionID, "")
	}
	gwSession := pick(row.GwSessionID, meta.GwSessionID)
	agentKind := pick(row.AgentKind, meta.AgentKind)
	sessionPath := pick(row.SessionPath, meta.SessionPath)
	// ACC 现在给 created_at；老格式给 started_at。
	startedAt := pick(row.StartedAt, row.CreatedAt)
	title := strings.TrimSpace(row.Input)
	if title == "" {
		title = agentSess
	}
	return sessionBundleRow{
		ID:           pick(row.ID, row.SessionID),
		Title:        title,
		Lane:         classifyLane(sessionPath, gwSession),
		AgentKind:    firstNonEmpty(agentKind, row.AgentID),
		AgentSession: agentSess,
		GwSessionID:  gwSession,
		StartedAt:    startedAt,
		EndedAt:      row.EndedAt,
		TokensIn:     row.InputTokens,
		TokensOut:    row.OutputTokens,
	}
}

func classifyLane(sessionPath, gwSessionID string) string {
	path := strings.TrimSpace(sessionPath)
	if path != "" && fileExistsRemapped(path) {
		return "current"
	}
	if strings.TrimSpace(gwSessionID) != "" {
		return "historical"
	}
	if path != "" {
		return "historical"
	}
	return "current"
}

func fileExistsRemapped(path string) bool {
	for _, p := range []string{path, remapDiskHome(path)} {
		if p == "" {
			continue
		}
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return true
		}
	}
	return false
}

func remapDiskHome(p string) string {
	diskHome := strings.TrimSpace(os.Getenv("POCKET_DISK_HOME"))
	if diskHome == "" || !strings.HasPrefix(p, "/Users/") {
		return ""
	}
	rest := strings.TrimPrefix(p, "/Users/")
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return diskHome + rest[i:]
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
