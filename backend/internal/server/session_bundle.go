package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/halfking/pocket-opencode/backend/internal/mcp"
	"github.com/halfking/pocket-opencode/backend/internal/task"
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
		rows := s.accSessionsForTask(r.Context(), s.accTaskCandidates(r, taskID))
		probes := 0
		for _, row := range rows {
			item := accSessionToRow(row)
			if item.AgentSession != "" {
				seen[item.AgentSession] = struct{}{}
			}
			seen[item.ID] = struct{}{}
			// PLAN.md §1 的分层语义：ACC 行能被 companion 打开（native 文件
			// 或内存 SessionState）→ **当前**；否则 gw_session_id → 网关。
			// fileExistsRemapped 只覆盖 pocketd 本机文件这一种形态；派单会话
			// 的正文在执行 runtime 的 companion 里（2026-11-20 实测），要问
			// companion 而不是 stat 本机路径。探测有上限（会话多的大任务不
			// 因此变慢），且只对已判成 historical 的行补探。
			if item.Lane != "current" && item.AgentSession != "" && probes < accSessionProbeCap {
				if c := s.companionForSessionRow(row); c != nil {
					probes++
					if _, err := c.GetSession(item.AgentKind, item.AgentSession); err == nil {
						item.Lane = "current"
					}
				}
			}
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

// accSessionProbeCap 是一次 bundle 请求里 companion 打开性探测的上限。
// 探测是每行一个 GET；大任务（会话几十条）不能被它拖慢 —— 超出上限的行
// 维持 fileExists/gwSession 的判定（PLAN.md 语义的近似），首屏不受影响。
const accSessionProbeCap = 20

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// accTaskCandidates 计算「这个本地任务在 ACC 侧可能叫什么」：
//  1. 本地 id 本身 —— companion 自建档的任务（acc_link）本地 id 就是
//     ACC canonical id（实测 0ced2eda… 任务）；
//  2. 绑定里的 acc_task_id —— pocketd 委派链路建的任务本地 id 是
//     task-<hex>（2026-11-20 实测 task-ed77f89b → ACC 43cb68b8），不解析
//     绑定的话 ACC 会话永远查不到（bundle 三段全空的另一半根因）。
func (s *Server) accTaskCandidates(r *http.Request, taskID string) []string {
	candidates := []string{taskID}
	if s.taskStore == nil {
		return candidates
	}
	t, err := s.taskStore.GetTaskScoped(r.Context(), taskID, s.workspaceIDFromRequest(r))
	if err != nil || t == nil {
		return candidates
	}
	if b := task.ACCBinding(t); b.TaskID != "" && b.TaskID != taskID {
		candidates = append(candidates, b.TaskID)
	}
	return candidates
}

// accSessionsForTask 取「挂在任一候选 ACC 任务 id 上的全部会话行」。
//
// ★ 2026-11-20 实测（211 生产，PhaseB-dispatch-probe 派单）：
// ACC 的 dispatch frame 里 task_id 本身带 **runtime 前缀**
// （companion 收到的就是 `runtime-211-docker:43cb68b8…`），companion 照实
// 上报 ⇒ ACC ledger 里派单会话的 task_id 是「runtime_id:canonical_id」；
// 而本地 companion 自建任务的会话（acc_link 链路）用**裸** canonical id
// 上报。只查裸 id 会漏掉全部派单会话。
//
// 三级取数（每个候选 id）：
//  1. 裸 id —— acc_link 链路（companion 自建档）的形态；
//  2. `<配置的派单 runtime>:id` —— dispatch 链路（ACC 派发时加前缀）；
//  3. 全部为空时，拉全量（≤200）按 `":" + id` 后缀匹配兜底 —— 覆盖
//     handoff 到其它 runtime 的会话（那些 runtime 不在配置里）。
//
// 合并按行 id 去重，保持 ACC 返回顺序。
func (s *Server) accSessionsForTask(ctx context.Context, candidates []string) []mcp.AccSession {
	if s.mcpClient == nil || len(candidates) == 0 {
		return nil
	}
	fetch := func(id string) []mcp.AccSession {
		rows, err := s.mcpClient.ListSessions(ctx, id, 100)
		if err != nil {
			log.Printf("[session-bundle] acc_list_sessions(%q) failed: %v", id, err)
			return nil
		}
		return rows
	}
	byID := map[string]struct{}{}
	var out []mcp.AccSession
	merge := func(rows []mcp.AccSession) {
		for _, row := range rows {
			if row.ID == "" {
				continue
			}
			if _, ok := byID[row.ID]; ok {
				continue
			}
			byID[row.ID] = struct{}{}
			out = append(out, row)
		}
	}
	suffixes := make([]string, 0, len(candidates))
	for _, id := range candidates {
		if strings.TrimSpace(id) == "" {
			continue
		}
		merge(fetch(id))
		if rt := strings.TrimSpace(s.cfg.ACCDispatchRuntimeID); rt != "" {
			merge(fetch(rt + ":" + id))
		}
		suffixes = append(suffixes, ":"+id)
	}
	if len(out) == 0 {
		all, err := s.mcpClient.ListAllSessions(ctx, 200)
		if err != nil {
			log.Printf("[session-bundle] acc_list_sessions(all) failed: %v", err)
			return out
		}
		for _, row := range all {
			if row.ID == "" {
				continue
			}
			matched := false
			for _, suf := range suffixes {
				if strings.HasSuffix(row.TaskID, suf) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
			if _, ok := byID[row.ID]; ok {
				continue
			}
			byID[row.ID] = struct{}{}
			out = append(out, row)
		}
	}
	return out
}
