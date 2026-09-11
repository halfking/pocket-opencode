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

// CancelCommand posts POST /api/v2/runtime/commands/{command_id}/cancel
// with a {"reason"} body. The Idempotency-Key header is
// "pocket-cancel-"+commandID so ACC's orchestration plane dedupes
// retries of the same cancel intent.
func (c *Client) CancelCommand(ctx context.Context, commandID, reason string) error {
	if err := c.ready(); err != nil {
		return err
	}
	if strings.TrimSpace(commandID) == "" {
		return errors.New("accruntime: cancel command: command_id is required")
	}
	headers := map[string]string{"Idempotency-Key": "pocket-cancel-" + commandID}
	resp, err := c.http.Post(ctx,
		"/api/v2/runtime/commands/"+url.PathEscape(commandID)+"/cancel", headers,
		map[string]string{"reason": reason})
	if err != nil {
		return fmt.Errorf("accruntime: cancel command %s: %w", commandID, err)
	}
	return discard(resp)
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
