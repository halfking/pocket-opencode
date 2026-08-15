package facade

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SchemaVersionV1 is the only supported event envelope schema version.
const SchemaVersionV1 = "1.0"

// Event is one parsed SSE frame from the façade event stream.
type Event struct {
	// ID is the SSE `id:` line — the resumable cursor (event_id).
	ID string
	// Type is the SSE `event:` line, e.g. `task.state.changed.v1`.
	Type string
	// Data is the raw SSE `data:` payload (multi-line data joined by \n).
	Data []byte
}

// EventEnvelope is the logical envelope of a façade event. schema_version is
// normalized to "1.0"; event_id defaults to the SSE id line and type defaults
// to the SSE event line when the payload does not carry them explicitly.
type EventEnvelope struct {
	EventID       string          `json:"event_id"`
	CorrelationID string          `json:"correlation_id"`
	SchemaVersion string          `json:"schema_version"`
	Type          string          `json:"type"`
	Payload       json.RawMessage `json:"payload"`
}

// Envelope projects the event into the logical envelope. It accepts both the
// explicit envelope shape (event_id/correlation_id/schema_version/payload in
// the data JSON) and the minimal shape (fields only in the SSE frame).
func (e Event) Envelope() EventEnvelope {
	env := EventEnvelope{
		EventID:       e.ID,
		Type:          e.Type,
		SchemaVersion: SchemaVersionV1,
		Payload:       json.RawMessage(e.Data),
	}
	var explicit struct {
		EventID       string          `json:"event_id"`
		CorrelationID string          `json:"correlation_id"`
		SchemaVersion string          `json:"schema_version"`
		Type          string          `json:"type"`
		Payload       json.RawMessage `json:"payload"`
	}
	if len(e.Data) > 0 && json.Unmarshal(e.Data, &explicit) == nil {
		if explicit.EventID != "" {
			env.EventID = explicit.EventID
		}
		env.CorrelationID = explicit.CorrelationID
		if explicit.SchemaVersion != "" {
			env.SchemaVersion = explicit.SchemaVersion
		}
		if explicit.Type != "" {
			env.Type = explicit.Type
		}
		if len(explicit.Payload) > 0 {
			env.Payload = explicit.Payload
		}
	}
	return env
}

// EventStreamConfig tunes SSE subscription behaviour.
type EventStreamConfig struct {
	// RetryMax is the maximum number of automatic reconnects after a dropped
	// connection. 0 means the default (3); negative disables reconnect.
	RetryMax int
	// RetryBackoff is the wait between reconnects (default 500ms).
	RetryBackoff time.Duration
}

// defaultStreamRetries / defaultStreamBackoff are used when EventStreamConfig
// leaves the zero value.
const (
	defaultStreamRetries = 3
	defaultStreamBackoff = 500 * time.Millisecond
)

// StreamRunEvents subscribes to GET /api/v2/runs/{run_id}/events (SSE).
//
// Resume semantics: the current cursor is sent both as the `after` query
// parameter and as the standard `Last-Event-ID` header; after every received
// event the cursor advances to that event's id, so a reconnect resumes
// exactly after the last delivered event. Handler errors abort the stream and
// are returned to the caller.
func (c *Client) StreamRunEvents(ctx context.Context, runID string, after string, cfg EventStreamConfig, handler func(Event) error) error {
	if runID == "" {
		return fmt.Errorf("facade: run_id is required")
	}
	if handler == nil {
		return fmt.Errorf("facade: handler is required")
	}
	retryMax := cfg.RetryMax
	if retryMax == 0 {
		retryMax = defaultStreamRetries
	}
	backoff := cfg.RetryBackoff
	if backoff <= 0 {
		backoff = defaultStreamBackoff
	}

	cursor := after
	path := "/api/v2/runs/" + url.PathEscape(runID) + "/events"
	retries := 0

	for {
		next, dropped, err := c.consumeEventStream(ctx, path, cursor, handler)
		if err != nil {
			return err
		}
		if next != "" {
			cursor = next
		}
		if !dropped {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if retries >= retryMax {
			return fmt.Errorf("facade: event stream dropped after %d reconnects (cursor=%s)", retries, cursor)
		}
		retries++
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
	}
}

// consumeEventStream reads one SSE connection until it drops or the context
// is cancelled. It returns the latest event cursor, whether the connection
// dropped without the context being cancelled (i.e. reconnect is allowed),
// and a fatal error (non-2xx response or handler error).
func (c *Client) consumeEventStream(ctx context.Context, path, cursor string, handler func(Event) error) (latest string, dropped bool, err error) {
	query := url.Values{}
	if cursor != "" {
		query.Set("after", cursor)
	}
	u := *c.baseURL
	u.Path = strings.TrimRight(u.Path, "/") + path
	u.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", false, fmt.Errorf("facade: build event request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	req.Header.Set("Accept", "text/event-stream")
	if cursor != "" {
		req.Header.Set("Last-Event-ID", cursor)
	}

	// SSE streams must not inherit the shared client's request Timeout, which
	// would kill long-lived streams; use a client without a deadline and rely
	// on the request context instead.
	httpClient := &http.Client{}
	if tr := c.httpDo.Transport; tr != nil {
		httpClient.Transport = tr
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return latest, false, ctx.Err()
		}
		return latest, true, nil // connect error: reconnectable
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return latest, false, parseAPIError(resp)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var id, eventType string
	var dataLines []string

	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			ev, ok := buildEvent(&id, &eventType, &dataLines)
			if !ok {
				continue
			}
			if ev.ID != "" {
				latest = ev.ID
			}
			if err := handler(ev); err != nil {
				return latest, false, fmt.Errorf("facade: event handler: %w", err)
			}
		case strings.HasPrefix(line, ":"):
			// SSE comment / keepalive, ignore.
		case strings.HasPrefix(line, "id:"):
			id = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
		case strings.HasPrefix(line, "event:"):
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if ev, ok := buildEvent(&id, &eventType, &dataLines); ok {
		if ev.ID != "" {
			latest = ev.ID
		}
		if err := handler(ev); err != nil {
			return latest, false, fmt.Errorf("facade: event handler: %w", err)
		}
	}

	if scanErr := scanner.Err(); scanErr != nil {
		if ctx.Err() != nil {
			return latest, false, ctx.Err()
		}
		return latest, true, nil // network drop: reconnect with cursor
	}
	if ctx.Err() != nil {
		return latest, false, ctx.Err()
	}
	// Server closed the stream without error: reconnect to resume.
	return latest, true, nil
}

// buildEvent materializes the pending SSE frame from the accumulator fields
// and resets them. ok=false when nothing was accumulated.
func buildEvent(id *string, eventType *string, dataLines *[]string) (Event, bool) {
	if *id == "" && *eventType == "" && len(*dataLines) == 0 {
		return Event{}, false
	}
	ev := Event{
		ID:   *id,
		Type: *eventType,
		Data: []byte(strings.Join(*dataLines, "\n")),
	}
	*id, *eventType, *dataLines = "", "", nil
	return ev, true
}
