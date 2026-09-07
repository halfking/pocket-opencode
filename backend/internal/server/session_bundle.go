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

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func accSessionToRow(row mcp.AccSession) sessionBundleRow {
	var meta accMeta
	if len(row.Metadata) > 0 {
		_ = json.Unmarshal(row.Metadata, &meta)
	}
	agentSess := meta.AgentSessionID
	if agentSess == "" {
		agentSess = row.SessionID
	}
	title := strings.TrimSpace(row.Input)
	if title == "" {
		title = agentSess
	}
	return sessionBundleRow{
		ID:           row.SessionID,
		Title:        title,
		Lane:         classifyLane(meta.SessionPath, meta.GwSessionID),
		AgentKind:    firstNonEmpty(meta.AgentKind, row.AgentID),
		AgentSession: agentSess,
		GwSessionID:  meta.GwSessionID,
		StartedAt:    row.StartedAt,
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
