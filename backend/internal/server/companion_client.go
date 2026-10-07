package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// CompanionClient is a client for the agent-companion read face.
//
// Scope note: companion is the AUTHORITY for which agents exist on this
// host, what sessions they hold, and what state each run is in. openpocket
// consumes that; it does not re-derive any of it locally. Every method
// here is read-only on purpose — task mutation (operate / cancel / prompt
// edit) belongs to ACC's control plane, and the mobile approval client in
// internal/accruntime is what drives it. Adding a write here would create
// a second, unaudited path to the same actions.

// CompanionClient is a read-only client for agent-companion native APIs.
type CompanionClient struct {
	baseURL string
	secret  string
	http    *http.Client
}

func NewCompanionClient(baseURL, secret string) *CompanionClient {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil
	}
	return &CompanionClient{
		baseURL: baseURL,
		secret:  secret,
		http:    &http.Client{Timeout: 12 * time.Second},
	}
}

// SetCompanionClient attaches the companion read client.
//
// A nil argument is IGNORED rather than assigned. NewCompanionClient
// returns nil for a blank URL, so an assignment-on-nil would look like a
// successful injection while leaving the server without a client — and a
// later config reload that briefly resolves no URL would silently disable
// an integration that was previously working. The two states ("never
// configured" and "temporarily unconfigured") must not be conflated.
func (s *Server) SetCompanionClient(c *CompanionClient) {
	if s != nil && c != nil {
		s.companion = c
	}
}

type companionSession struct {
	ID         string   `json:"id"`
	Kind       string   `json:"kind"`
	Agent      string   `json:"agent"`
	DriverKind string   `json:"driverKind"`
	Resumable  bool     `json:"resumable"`
	Title      string   `json:"title"`
	Path       string   `json:"path"`
	Project    string   `json:"project"`
	Tags       []string `json:"tags"`
	UpdatedAt  int64    `json:"updatedAt"`
	SizeBytes  int64    `json:"sizeBytes"`
	Source     string   `json:"source"`
}

type companionMessage struct {
	Seq  int    `json:"seq"`
	ID   string `json:"id"`
	TS   int64  `json:"ts"`
	Type string `json:"type"`
	Role string `json:"role"`
	Name string `json:"name,omitempty"`
	Text string `json:"text"`
}

// companionScanMeta mirrors companion's `meta` object on list responses.
// It is what tells a caller whether the list it just read is complete or
// a snapshot of a still-refreshing store — an empty `sessions` array means
// "nothing indexed" and "scanned:false" means "not looked yet", and those
// two must never be rendered the same way.
type companionScanMeta struct {
	Scanned       bool   `json:"scanned"`
	ScannedAt     string `json:"scannedAt"`
	Sessions      int    `json:"sessions"`
	LastRefreshMs int64  `json:"lastRefreshMs"`
}

// companionSessionList is the paged list envelope returned by
// GET /api/mobile/native/sessions.
type companionSessionList struct {
	Sessions []companionSession `json:"sessions"`
	Total    int                `json:"total"`
	Limit    int                `json:"limit"`
	Offset   int                `json:"offset"`
	Meta     companionScanMeta  `json:"meta"`
}

// companionRun mirrors companion's run snapshot: the execution-level
// state of a dispatched task.
//
// Field names come from the live daemon, not from the operate response.
// The two differ and mixing them up yields a silently zero-valued struct:
//   - POST .../operate answers {runId, agentKind, nativeSessionId, ...}
//     (camelCase — that is the request-side receipt)
//   - GET /runs/:id and GET /runs answer {run_id, kind, session_id, ...}
//     (snake_case — agentfacade.RunSnapshot)
//
// They are kept as two types on purpose: a client that reuses one struct
// for both ends up with an empty RunID and cannot correlate a dispatch
// with its history.
type companionRun struct {
	RunID     string `json:"run_id"`
	Dispatch  string `json:"dispatch_id"`
	TaskID    string `json:"task_id"`
	AgentID   string `json:"agent_id"`
	Kind      string `json:"kind"`
	State     string `json:"state"`
	SessionID string `json:"session_id"`
	Updates   int    `json:"updates"`
	LastEvent string `json:"last_event"`
	// Retained marks a terminal run served from companion's short-lived
	// in-memory history rather than the live run table.
	Retained bool `json:"retained"`
}

// companionRunReceipt is the POST /native/sessions/:id/operate response.
type companionRunReceipt struct {
	RunID     string `json:"runId"`
	State     string `json:"state"`
	AgentKind string `json:"agentKind"`
	SessionID string `json:"nativeSessionId"`
	Provider  string `json:"providerSessionId"`
}

// companionSessionLink is one row of GET /api/mobile/session-links — the
// binding between a native session and the ACC canonical task that owns
// it. This is the join that makes "which task is this session doing?"
// answerable from the mobile side.
type companionSessionLink struct {
	Kind      string `json:"kind"`
	SessionID string `json:"sessionId"`
	TaskID    string `json:"taskId"`
	ProjectID string `json:"projectId"`
	UpdatedAt int64  `json:"updatedAt"`
}

func (c *CompanionClient) get(path string, q url.Values, dest any) error {
	if c == nil {
		return fmt.Errorf("companion not configured")
	}
	u := c.baseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	if c.secret != "" {
		req.Header.Set("Authorization", "Bearer "+c.secret)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("companion %s: http %d: %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if dest == nil {
		return nil
	}
	return json.Unmarshal(body, dest)
}

// post issues a JSON POST and returns the raw body. Callers unmarshal it
// themselves so the wire shape is visible at the call site — the operate
// receipt and the run reads use different field naming, and hiding that
// behind one shared decode is how the mismatch slips through.
func (c *CompanionClient) post(path string, payload any) ([]byte, error) {
	if c == nil {
		return nil, fmt.Errorf("companion not configured")
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, c.baseURL+path, bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	if c.secret != "" {
		req.Header.Set("Authorization", "Bearer "+c.secret)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("companion %s: http %d: %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

// maxResponseBytes caps a single companion read. A transcript page on a
// long session can be large; this keeps a runaway response from pinning
// the mobile request goroutine.
const maxResponseBytes = 8 << 20

// ListNativeSessions reads the host's session inventory.
//
// kind is the native-store kind ("mmcode", "claude", "opencode", ...);
// empty means every kind. limit/offset are passed through so a caller
// can page without the server inventing a default the client cannot
// reason about.
func (c *CompanionClient) ListNativeSessions(kind string, limit, offset int) (companionSessionList, error) {
	var out companionSessionList
	q := url.Values{}
	if k := nativeAgentKind(kind); k != "" {
		q.Set("kind", k)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if offset > 0 {
		q.Set("offset", strconv.Itoa(offset))
	}
	err := c.get("/api/mobile/native/sessions", q, &out)
	return out, err
}

// ListSessionLinks reads the session↔ACC-task bindings companion holds.
func (c *CompanionClient) ListSessionLinks() ([]companionSessionLink, error) {
	var out struct {
		Links []companionSessionLink `json:"links"`
		Total int                    `json:"total"`
	}
	err := c.get("/api/mobile/session-links", nil, &out)
	return out.Links, err
}

// ListRuns reads the task history: live runs plus the terminal runs
// companion retains (GET /api/v1/runs).
//
// This is the "sync my task history" surface, so it deliberately reads
// the LIST route rather than polling one id at a time — a client that
// only ever calls GetRun cannot discover runs it did not start itself.
func (c *CompanionClient) ListRuns(limit int) ([]companionRun, error) {
	var out struct {
		Runs  []companionRun `json:"runs"`
		Total int            `json:"total"`
	}
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	err := c.get("/api/v1/runs", q, &out)
	return out.Runs, err
}

// GetRun reads one run. Companion answers this route with a FLAT snapshot
// (not wrapped in {"run": …}); the test pins that shape, because an
// envelope assumption here decodes into a zero-valued struct with no
// error at all.
func (c *CompanionClient) GetRun(runID string) (companionRun, error) {
	var out companionRun
	err := c.get("/api/v1/runs/"+url.PathEscape(runID), nil, &out)
	return out, err
}

// OperateSession dispatches a prompt into a native session and returns the
// run receipt. The response is camelCase (runId/nativeSessionId), unlike
// the run reads above — see companionRunReceipt.
func (c *CompanionClient) OperateSession(kind, sessionID, prompt, operationID string) (companionRunReceipt, error) {
	body := map[string]any{"prompt": prompt}
	if operationID != "" {
		body["operation_id"] = operationID
	}
	if kind != "" {
		body["kind"] = nativeAgentKind(kind)
	}
	raw, err := c.post("/api/v1/native/sessions/"+url.PathEscape(sessionID)+"/operate", body)
	if err != nil {
		return companionRunReceipt{}, err
	}
	var out companionRunReceipt
	err = json.Unmarshal(raw, &out)
	return out, err
}

func (c *CompanionClient) GetTranscript(kind, id, types string) (companionSession, []companionMessage, error) {
	return c.GetTranscriptPage(kind, id, types, 0, 0)
}

// GetTranscriptPage 增量/分页读取会话正文：afterSeq 为 keyset 游标
// （只返回 Seq > afterSeq 的行，0 = 从头），limit<=0 时由 companion 决定。
// 客户端以返回行的最大 seq 作为下一次的 afterSeq，实现断点续传增量同步
// （docs/2026-09-09-list-sync-rules.md §4.1 会话正文按需/增量加载）。
func (c *CompanionClient) GetTranscriptPage(kind, id, types string, afterSeq, limit int) (companionSession, []companionMessage, error) {
	q := url.Values{}
	if k := nativeAgentKind(kind); k != "" {
		q.Set("kind", k)
	}
	if types != "" {
		q.Set("types", types)
	}
	if afterSeq > 0 {
		q.Set("after_seq", strconv.Itoa(afterSeq))
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var out struct {
		Session  companionSession   `json:"session"`
		Messages []companionMessage `json:"messages"`
	}
	err := c.get("/api/v1/native/sessions/"+url.PathEscape(id)+"/transcript", q, &out)
	return out.Session, out.Messages, err
}

func (c *CompanionClient) GetSession(kind, id string) (companionSession, error) {
	q := url.Values{}
	if k := nativeAgentKind(kind); k != "" {
		q.Set("kind", k)
	}
	var out companionSession
	err := c.get("/api/v1/native/sessions/"+url.PathEscape(id), q, &out)
	return out, err
}

func nativeAgentKind(kind string) string {
	kind = strings.TrimSpace(kind)
	if strings.HasPrefix(kind, "disk-") {
		return strings.TrimPrefix(kind, "disk-")
	}
	return kind
}
