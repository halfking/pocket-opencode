// Package accruntime is a typed facade over acchttp.Client for the ACC
// Runtime Control API (/api/v2/runtime/*) and the human approval inbox
// (/api/v2/approvals*). It exists so pocketd can answer ACC permission
// gates, cancel dispatched commands and mirror the human approval inbox
// without re-implementing transport concerns.
//
// Fail-closed contract (mirrors agent-companion internal/acc
// EmitPermissionRequest): ACC 404/501 on the permission endpoint means the
// gate is not wired in the deployed ACC version. Callers MUST treat
// ErrGateUnavailable as a deny — never as "skip the gate and allow".
//
// All responses are read through an 8 MiB limit and error strings never
// carry the API key (acchttp redacts URLs; keys only live in the
// Authorization header).
package accruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/acchttp"
)

// maxResponseBytes bounds every response body read (2^23 = 8 MiB).
const maxResponseBytes = 8 << 20

// ErrGateUnavailable indicates ACC's permission endpoint is not wired
// (HTTP 404/501). Callers must default to deny (fail-closed) exactly like
// agent-companion's ErrPermissionGateUnavailable contract.
var ErrGateUnavailable = errors.New("accruntime: permission gate unavailable")

// Client is a typed ACC Runtime Control surface on top of acchttp.Client.
// It is thread-safe; share one instance per deployment.
type Client struct {
	http *acchttp.Client
}

// New builds a Client from an acchttp.Config (BaseURL required).
func New(cfg acchttp.Config) (*Client, error) {
	h, err := acchttp.New(cfg)
	if err != nil {
		return nil, err
	}
	return &Client{http: h}, nil
}

// PermissionDecision is pocketd's answer to a pending ACC permission gate.
//
// DispatchID identifies the ACC command (dispatch) the tool call belongs
// to; ToolCallID/SourceRef/CorrelationID form the mandatory data-loop
// triple ACC joins into the dispatch timeline (same triple the companion
// sends when raising the request).
type PermissionDecision struct {
	DispatchID    string // path parameter: ACC command id
	ToolCallID    string // body: tool_call_id
	SourceRef     string // body: source_ref
	CorrelationID string // body: correlation_id
	OptionID      string // body: decision.option_id (passthrough, e.g. "once"/"always"/"reject")
	Allow         bool   // body: decision.allow
	Reason        string // body: decision.reason
}

// validate enforces the mandatory triple plus the dispatch id. Missing
// values are a caller bug: ACC cannot stitch the decision into the
// timeline, so we refuse before hitting the wire.
func (d PermissionDecision) validate() error {
	for field, v := range map[string]string{
		"dispatch_id":    d.DispatchID,
		"tool_call_id":   d.ToolCallID,
		"source_ref":     d.SourceRef,
		"correlation_id": d.CorrelationID,
	} {
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("accruntime: permission decision: %s is required", field)
		}
	}
	return nil
}

// AnswerPermission posts a permission decision to
// POST /api/v2/runtime/commands/{dispatch}/permission.
//
// Error classification:
//   - missing dispatch/triple → validation error, no HTTP call
//   - ACC 404 / 501 → ErrGateUnavailable (caller must deny fail-closed)
//   - other non-2xx → wrapped transport/HTTP error
//   - network failure → wrapped error
func (c *Client) AnswerPermission(ctx context.Context, d PermissionDecision) error {
	if err := c.ready(); err != nil {
		return err
	}
	if err := d.validate(); err != nil {
		return err
	}
	body := map[string]any{
		"tool_call_id":   d.ToolCallID,
		"source_ref":     d.SourceRef,
		"correlation_id": d.CorrelationID,
		"decision": map[string]any{
			"option_id": d.OptionID,
			"allow":     d.Allow,
			"reason":    d.Reason,
		},
	}
	resp, err := c.http.Post(ctx,
		"/api/v2/runtime/commands/"+url.PathEscape(d.DispatchID)+"/permission", nil, body)
	if err != nil {
		if code, ok := httpStatusFromError(err); ok &&
			(code == http.StatusNotFound || code == http.StatusNotImplemented) {
			return fmt.Errorf("%w (ACC HTTP %d)", ErrGateUnavailable, code)
		}
		return fmt.Errorf("accruntime: answer permission: %w", err)
	}
	return discard(resp)
}

// Command is the ACC runtime-control view of one dispatched command.
//
// FencingToken is the token the CURRENT lease holder must present when it
// acts on the command (cancel/start/heartbeat/complete). It changes on every
// lease acquisition, so it can only be read — never cached — right before the
// action that needs it.
type Command struct {
	DispatchID   string `json:"dispatch_id"`
	RunID        string `json:"run_id"`
	TaskID       string `json:"task_id"`
	AgentID      string `json:"agent_id"`
	Status       string `json:"status"`
	FencingToken uint64 `json:"fencing_token"`
	CancelState  string `json:"cancel_state"`
	ErrorCode    string `json:"error_code"`
}

// commandEnvelope is ACC's {"ok":true,"data":{...}} wrapper.
type commandEnvelope struct {
	OK   bool            `json:"ok"`
	Data json.RawMessage `json:"data"`
}

// GetCommand reads GET /api/v2/runtime/commands/{command_id}.
//
// Why it exists: ACC's holder-fenced endpoints (cancel among them) reject a
// request whose fencing_token is not the CURRENT one. Pocketd holds the
// holder id (it is the lease holder that bound the task) but not the token,
// and the token is not part of the task row — it moves with every lease
// acquisition. So the only correct way to act on a command is to read the
// command first and act with the token it reports right now.
//
// Measured (2026-10-25, live ACC + live daemon): cancelling WITHOUT the
// token answers HTTP 409 {"code":"lease_fenced","message":"orchestration:
// stale fencing token"} and the dispatched run then dies of `agent_busy`
// after its slot wait times out — i.e. the operator asked to stop and the
// system answered "busy, retry later". With the token read from this
// endpoint the same cancel answers 200 and the run lands `cancelled`.
func (c *Client) GetCommand(ctx context.Context, commandID string) (Command, error) {
	if err := c.ready(); err != nil {
		return Command{}, err
	}
	if strings.TrimSpace(commandID) == "" {
		return Command{}, errors.New("accruntime: get command: command_id is required")
	}
	resp, err := c.http.Get(ctx, "/api/v2/runtime/commands/"+url.PathEscape(commandID), nil)
	if err != nil {
		return Command{}, fmt.Errorf("accruntime: get command %s: %w", commandID, err)
	}
	raw, err := readAll(resp)
	if err != nil {
		return Command{}, fmt.Errorf("accruntime: get command %s: %w", commandID, err)
	}
	var env commandEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return Command{}, fmt.Errorf("accruntime: get command %s: decode: %w", commandID, err)
	}
	body := env.Data
	if len(body) == 0 {
		// Some deployments answer with the command at the top level.
		body = raw
	}
	var cmd Command
	if err := json.Unmarshal(body, &cmd); err != nil {
		return Command{}, fmt.Errorf("accruntime: get command %s: decode data: %w", commandID, err)
	}
	return cmd, nil
}

// CancelCommand posts POST /api/v2/runtime/commands/{command_id}/cancel
// with a {"holder_id","fencing_token","reason"} body. ACC's cancel endpoint
// is holder-fenced: an empty holderID fails client-side (fail-closed, no wire
// call), and a fencing token that is not the current one is rejected by ACC
// with HTTP 409 lease_fenced. The Idempotency-Key header is
// "pocket-cancel-"+commandID so ACC's orchestration plane dedupes retries of
// the same cancel intent.
//
// fencingToken == 0 means "no token presented" — the wire call still happens
// and ACC decides. Callers that can read the command should pass the token
// GetCommand reported (see its doc for the measured failure without it).
func (c *Client) CancelCommand(ctx context.Context, commandID, holderID string, fencingToken uint64, reason string) error {
	if err := c.ready(); err != nil {
		return err
	}
	if strings.TrimSpace(commandID) == "" {
		return errors.New("accruntime: cancel command: command_id is required")
	}
	if strings.TrimSpace(holderID) == "" {
		return errors.New("accruntime: cancel command: holder_id is required (holder-fenced endpoint)")
	}
	headers := map[string]string{"Idempotency-Key": "pocket-cancel-" + commandID}
	body := map[string]any{"holder_id": holderID, "reason": reason}
	if fencingToken > 0 {
		body["fencing_token"] = fencingToken
	}
	resp, err := c.http.Post(ctx,
		"/api/v2/runtime/commands/"+url.PathEscape(commandID)+"/cancel", headers, body)
	if err != nil {
		return fmt.Errorf("accruntime: cancel command %s: %w", commandID, err)
	}
	return discard(resp)
}

// DispatchRequest is the body of POST /api/v2/runtime/dispatch.
//
// SourceRef and CorrelationID are NOT optional decoration: ACC copies them
// into the command's input_snapshot, the agent-companion daemon reads them
// back as the durable event's Source/CorrelationID, and its ACP permission
// gate refuses (fail-closed) to run when either is empty.
//
// Measured 2026-10-25 against the live ACC + the live daemon:
//
//	dispatch without source_ref/correlation_id → the agent starts, asks for a
//	tool permission, and the daemon answers
//	  "facade acp permission denied (fail-closed) ... reason=missing correlation triple"
//	⇒ the run dies with error_code=permission_denied.
//	the same dispatch WITH both fields → no "missing correlation triple"; the
//	run proceeds and the command reaches status=running.
type DispatchRequest struct {
	// IdempotencyKey dedupes retries of the same dispatch intent.
	IdempotencyKey string `json:"idempotency_key"`
	// RuntimeID is the agent-companion runtime that should execute the task
	// (e.g. "acs-ac30-01").
	RuntimeID string `json:"runtime_id"`
	// AgentID is the agent slot inside that runtime (e.g. "opencode").
	AgentID string `json:"agent_id"`
	// TaskID is the ACC task to execute.
	TaskID string `json:"task_id"`
	// Operation is the command verb ("agent.task").
	Operation string `json:"operation"`
	// SourceRef names the initiator ("pocketd").
	SourceRef string `json:"source_ref"`
	// CorrelationID ties the dispatch to the Pocket task that caused it.
	CorrelationID string `json:"correlation_id"`
	// Payload is the operation payload (e.g. {"prompt": "..."}).
	Payload map[string]any `json:"payload,omitempty"`
}

// DispatchResult is ACC's answer to a dispatch.
//
// The wire shape is {"command_id": "...", "data": {dispatch_id, run_id,
// task_id, status, ...}}. The data envelope is where the ids live, so the
// decoder reads it from there and falls back to the top level for
// deployments that answer differently.
type DispatchResult struct {
	CommandID  string `json:"command_id"`
	DispatchID string `json:"dispatch_id"`
	RunID      string `json:"run_id"`
	TaskID     string `json:"task_id"`
	Status     string `json:"status"`
	AgentID    string `json:"agent_id"`
}

// Dispatch posts POST /api/v2/runtime/dispatch: "run this ACC task on that
// agent-companion runtime".
func (c *Client) Dispatch(ctx context.Context, req DispatchRequest) (DispatchResult, error) {
	if err := c.ready(); err != nil {
		return DispatchResult{}, err
	}
	if strings.TrimSpace(req.RuntimeID) == "" {
		return DispatchResult{}, errors.New("accruntime: dispatch: runtime_id is required")
	}
	if strings.TrimSpace(req.AgentID) == "" {
		return DispatchResult{}, errors.New("accruntime: dispatch: agent_id is required")
	}
	if strings.TrimSpace(req.TaskID) == "" {
		return DispatchResult{}, errors.New("accruntime: dispatch: task_id is required")
	}
	if strings.TrimSpace(req.SourceRef) == "" {
		// Fail closed here rather than let the run die later with
		// "missing correlation triple": the symptom would be an opaque
		// permission_denied minutes after the dispatch was accepted.
		return DispatchResult{}, errors.New("accruntime: dispatch: source_ref is required (the agent's permission gate refuses a dispatch without it)")
	}
	if strings.TrimSpace(req.CorrelationID) == "" {
		return DispatchResult{}, errors.New("accruntime: dispatch: correlation_id is required (the agent's permission gate refuses a dispatch without it)")
	}
	body := map[string]any{
		"runtime_id":     req.RuntimeID,
		"agent_id":       req.AgentID,
		"task_id":        req.TaskID,
		"operation":      req.Operation,
		"source_ref":     req.SourceRef,
		"correlation_id": req.CorrelationID,
	}
	if req.IdempotencyKey != "" {
		body["idempotency_key"] = req.IdempotencyKey
	}
	if req.Payload != nil {
		body["payload"] = req.Payload
	}
	resp, err := c.http.Post(ctx, "/api/v2/runtime/dispatch", nil, body)
	if err != nil {
		return DispatchResult{}, fmt.Errorf("accruntime: dispatch: %w", err)
	}
	raw, err := readAll(resp)
	if err != nil {
		return DispatchResult{}, fmt.Errorf("accruntime: dispatch: %w", err)
	}
	var env struct {
		CommandID string          `json:"command_id"`
		Data      json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return DispatchResult{}, fmt.Errorf("accruntime: dispatch: decode: %w", err)
	}
	payload := env.Data
	if len(payload) == 0 {
		payload = raw
	}
	var out DispatchResult
	if err := json.Unmarshal(payload, &out); err != nil {
		return DispatchResult{}, fmt.Errorf("accruntime: dispatch: decode data: %w", err)
	}
	if out.CommandID == "" {
		out.CommandID = env.CommandID
	}
	if out.DispatchID == "" {
		return DispatchResult{}, fmt.Errorf("accruntime: dispatch: ACC accepted the dispatch but returned no dispatch_id")
	}
	return out, nil
}

// Approval is one entry of ACC's human approval inbox. Fields mirror
// acc-go handlers.ApprovalRequest; decoding is lenient (see
// ListApprovals) so unknown envelope shapes do not break the read.
type Approval struct {
	TaskID          string         `json:"task_id"`
	Title           string         `json:"title"`
	Kind            string         `json:"kind"`
	Phase           string         `json:"phase"`
	AgentID         string         `json:"agent_id,omitempty"`
	RequestedBy     string         `json:"requested_by"`
	Question        string         `json:"question"`
	RequestedAt     time.Time      `json:"requested_at,omitempty"`
	PendingRequests int            `json:"pending_requests"`
	Payload         map[string]any `json:"payload,omitempty"`
}

// UnmarshalJSON decodes an Approval leniently: requested_at may be an
// RFC3339 string, unix seconds or unix milliseconds.
func (a *Approval) UnmarshalJSON(data []byte) error {
	type alias Approval
	aux := struct {
		RequestedAt json.RawMessage `json:"requested_at"`
		*alias
	}{alias: (*alias)(a)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	a.RequestedAt = lenientTime(aux.RequestedAt)
	return nil
}

// ListApprovals reads GET /api/v2/approvals. The envelope is parsed
// leniently: {"approvals":[...]}, {"data":[...]}, {"items":[...]} or a
// bare JSON array all decode; an object without a recognizable list
// decodes as an empty inbox rather than an error.
func (c *Client) ListApprovals(ctx context.Context) ([]Approval, error) {
	if err := c.ready(); err != nil {
		return nil, err
	}
	resp, err := c.http.Get(ctx, "/api/v2/approvals", nil)
	if err != nil {
		return nil, fmt.Errorf("accruntime: list approvals: %w", err)
	}
	raw, err := readAll(resp)
	if err != nil {
		return nil, fmt.Errorf("accruntime: list approvals: %w", err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err == nil {
		for _, key := range []string{"approvals", "data", "items"} {
			v, ok := doc[key]
			if !ok {
				continue
			}
			var out []Approval
			if err := json.Unmarshal(v, &out); err == nil {
				return out, nil
			}
		}
		// Envelope object without a usable list: an empty, non-error inbox.
		return []Approval{}, nil
	}
	var out []Approval
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("accruntime: decode approvals: %w", err)
	}
	return out, nil
}

// Approve posts POST /api/v2/approvals/{task_id}/approve. reason is the
// optional human comment ACC folds into the audit note.
func (c *Client) Approve(ctx context.Context, taskID, reason string) error {
	return c.approvalAction(ctx, "approve", taskID, reason)
}

// Reject posts POST /api/v2/approvals/{task_id}/reject. ACC mandates a
// reason on reject, so an empty reason fails before the HTTP call.
func (c *Client) Reject(ctx context.Context, taskID, reason string) error {
	if strings.TrimSpace(reason) == "" {
		return errors.New("accruntime: reject approval: reason is required")
	}
	return c.approvalAction(ctx, "reject", taskID, reason)
}

func (c *Client) approvalAction(ctx context.Context, action, taskID, reason string) error {
	if err := c.ready(); err != nil {
		return err
	}
	if strings.TrimSpace(taskID) == "" {
		return fmt.Errorf("accruntime: %s approval: task_id is required", action)
	}
	// ACC's ApprovalActionInput carries comment (approve, optional) and
	// reason (reject, mandatory) — send exactly the field each action uses.
	body := map[string]string{}
	if action == "approve" {
		body["comment"] = reason
	} else {
		body["reason"] = reason
	}
	resp, err := c.http.Post(ctx,
		"/api/v2/approvals/"+url.PathEscape(taskID)+"/"+action, nil, body)
	if err != nil {
		return fmt.Errorf("accruntime: %s approval %s: %w", action, taskID, err)
	}
	return discard(resp)
}

func (c *Client) ready() error {
	if c == nil || c.http == nil {
		return errors.New("accruntime: client is nil")
	}
	return nil
}

// readAll drains a response body under the 8 MiB cap and closes it.
func readAll(resp *acchttp.Response) ([]byte, error) {
	if resp == nil || resp.Body == nil {
		return nil, errors.New("acchttp: response body is nil")
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
}

// discard drains-and-closes a success response body so the underlying
// connection is reusable, still under the 8 MiB read cap.
func discard(resp *acchttp.Response) error {
	_, err := readAll(resp)
	return err
}

// httpStatusRe extracts the HTTP status acchttp embeds in non-2xx errors
// ("acchttp: HTTP <code>: <body>"; retries wrap the last attempt, hence
// the last-match semantics).
var httpStatusRe = regexp.MustCompile(`acchttp: HTTP (\d{3}):`)

func httpStatusFromError(err error) (int, bool) {
	if err == nil {
		return 0, false
	}
	matches := httpStatusRe.FindAllStringSubmatch(err.Error(), -1)
	if len(matches) == 0 {
		return 0, false
	}
	code, convErr := strconv.Atoi(matches[len(matches)-1][1])
	if convErr != nil {
		return 0, false
	}
	return code, true
}

// lenientTime decodes requested_at that may arrive as RFC3339 text, unix
// seconds or unix milliseconds. Anything unparsable becomes the zero time
// instead of failing the whole inbox decode.
func lenientTime(raw json.RawMessage) time.Time {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return time.Time{}
	}
	if n, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
		if n > 1e12 { // milliseconds
			return time.UnixMilli(n)
		}
		return time.Unix(n, 0)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t
		}
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			if n > 1e12 {
				return time.UnixMilli(n)
			}
			return time.Unix(n, 0)
		}
	}
	return time.Time{}
}
