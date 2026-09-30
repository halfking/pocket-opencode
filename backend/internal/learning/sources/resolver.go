// Package sources adapts the four content domains (notes / email / RSS /
// meeting) to learning.SourceResolver, so a "one-click add to learning" action
// only has to send a source id.
//
// It lives in its own package on purpose: internal/learning stays free of
// knowledge about notes/email/rss/meeting (that would couple the learning loop
// to four unrelated schemas and make it untestable without all four stores).
// cmd/pocketd wires the pieces together.
//
// Every lookup is tenant-scoped. An unscoped read here would let any user file
// another user's email as their own study material — that is an exfiltration
// path, not a missing-validation nit.
package sources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/halfking/pocket-opencode/backend/internal/email"
	"github.com/halfking/pocket-opencode/backend/internal/learning"
	"github.com/halfking/pocket-opencode/backend/internal/meeting"
	"github.com/halfking/pocket-opencode/backend/internal/notes"
	"github.com/halfking/pocket-opencode/backend/internal/rss"
	"github.com/jackc/pgx/v5"
)

// emailScopedReader is the slice of the email store the resolver needs.
//
// It is an interface rather than *email.Store so the tenancy rule below can be
// tested without a database: the interesting case is a store that *answers*
// with somebody else's email, and that cannot be staged without real rows.
type emailScopedReader interface {
	// GetEmailByIDScoped must return (nil, nil) for an email that is not
	// owned by (userID, workspaceID). An implementation that ignores the
	// scope arguments must never be wired here.
	GetEmailByIDScoped(ctx context.Context, id, userID, workspaceID string) (*email.Email, error)
}

// Resolver implements learning.SourceResolver over the real stores. Any store
// may be nil (remote-only mode); the corresponding kind then answers
// "not found" instead of panicking.
type Resolver struct {
	notes   *notes.Store
	emails  emailScopedReader
	rss     *rss.Store
	meeting *meeting.Store
}

// New builds a resolver. All arguments are optional.
func New(notesStore *notes.Store, emailStore *email.Store, rssStore *rss.Store, meetingStore *meeting.Store) *Resolver {
	return &Resolver{notes: notesStore, emails: emailStore, rss: rssStore, meeting: meetingStore}
}

var _ learning.SourceResolver = (*Resolver)(nil)

// Resolve implements learning.SourceResolver.
func (r *Resolver) Resolve(ctx context.Context, kind, sourceID, userID, workspaceID string) (*learning.ResolvedSource, error) {
	if strings.TrimSpace(sourceID) == "" {
		return nil, learning.ErrSourceNotFound
	}
	wsID := workspaceID
	if wsID == "" {
		wsID = learning.DefaultWorkspaceID
	}

	switch learning.SourceKind(kind) {
	case learning.SourceNote:
		if r.notes == nil {
			return nil, learning.ErrSourceNotFound
		}
		note, err := r.notes.GetByIDScoped(ctx, sourceID, userID, wsID)
		if err != nil {
			return nil, wrapNotFound("note", err)
		}
		title := strings.TrimSpace(note.Title)
		if title == "" {
			// Voice notes often have no title; fall back to the snippet so the
			// inbox row is never blank.
			title = truncate(strings.TrimSpace(note.Snippet), 40)
		}
		if title == "" {
			title = fmt.Sprintf("note %s", sourceID)
		}
		return &learning.ResolvedSource{
			Title:   title,
			Summary: note.Snippet,
			Tags:    decodeJSONTags(note.Tags),
		}, nil

	case learning.SourceEmail:
		if r.emails == nil {
			return nil, learning.ErrSourceNotFound
		}
		// The lookup itself is tenant-scoped (it joins email_accounts), so a
		// foreign email is indistinguishable from a missing one. Do NOT
		// re-check msg.WorkspaceID here: the detail projection never selects
		// that column, so it is always "" and such a check passes for every
		// caller — which is exactly how this path became an exfiltration hole.
		msg, err := r.emails.GetEmailByIDScoped(ctx, sourceID, userID, wsID)
		if err != nil {
			return nil, wrapNotFound("email", err)
		}
		if msg == nil {
			return nil, fmt.Errorf("%w: email", learning.ErrSourceNotFound)
		}
		title := strings.TrimSpace(msg.Subject)
		if title == "" {
			title = truncate(strings.TrimSpace(msg.Snippet), 40)
		}
		if title == "" {
			title = fmt.Sprintf("email %s", sourceID)
		}
		summary := msg.AISummary
		if summary == "" {
			summary = msg.Snippet
		}
		return &learning.ResolvedSource{
			Title:   title,
			Summary: summary,
			Tags:    emailTags(msg),
		}, nil

	case learning.SourceRSS:
		if r.rss == nil {
			return nil, learning.ErrSourceNotFound
		}
		item, err := r.rss.GetItem(ctx, sourceID, rss.Scope{UserID: userID, WorkspaceID: wsID})
		if err != nil {
			return nil, wrapNotFound("rss item", err)
		}
		title := strings.TrimSpace(item.Title)
		if title == "" {
			title = truncate(strings.TrimSpace(item.Summary), 40)
		}
		if title == "" {
			title = fmt.Sprintf("feed item %s", sourceID)
		}
		return &learning.ResolvedSource{
			Title:   title,
			Summary: firstNonEmpty(item.Summary, item.Content),
			Tags:    item.Categories,
		}, nil

	case learning.SourceMeeting:
		if r.meeting == nil {
			return nil, learning.ErrSourceNotFound
		}
		m, err := r.meeting.GetScoped(sourceID, userID, wsID)
		if err != nil {
			return nil, wrapNotFound("meeting", err)
		}
		title := strings.TrimSpace(m.Title)
		if title == "" {
			title = fmt.Sprintf("meeting %s", sourceID)
		}
		return &learning.ResolvedSource{
			Title:   title,
			Summary: m.Summary,
			Tags:    m.Tags,
		}, nil

	default:
		// chat / manual have no server-side source row: the client supplies the
		// title. Reporting "not found" keeps the one-click path honest instead
		// of inventing a title from an id.
		return nil, learning.ErrSourceNotFound
	}
}

// ActionItemsForMeeting returns the pending action items of a meeting, used by
// the meeting -> tasks conversion (P2). Empty is a valid answer: a meeting
// without action items produces zero tasks, not an error.
func (r *Resolver) ActionItemsForMeeting(meetingID, userID, workspaceID string) (*meeting.Meeting, error) {
	if r.meeting == nil {
		return nil, learning.ErrSourceNotFound
	}
	wsID := workspaceID
	if wsID == "" {
		wsID = learning.DefaultWorkspaceID
	}
	m, err := r.meeting.GetScoped(meetingID, userID, wsID)
	if err != nil {
		return nil, wrapNotFound("meeting", err)
	}
	return m, nil
}

// wrapNotFound maps the per-store "no rows" errors onto the shared sentinel so
// callers (and the HTTP layer) do not have to know each store's error type.
//
// The match is on the message because the four stores do not share an error
// type (pgx.ErrNoRows, their own ErrNotFound…). Anything unrecognised is
// returned as-is: a PG outage must surface as a failure, because reporting it
// as "not found" makes the client silently drop the user's action.
func wrapNotFound(kind string, err error) error {
	if err == nil {
		return learning.ErrSourceNotFound
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: %s", learning.ErrSourceNotFound, kind)
	}
	msg := strings.ToLower(err.Error())
	for _, marker := range []string{"no rows", "not found", "no such"} {
		if strings.Contains(msg, marker) {
			return fmt.Errorf("%w: %s", learning.ErrSourceNotFound, kind)
		}
	}
	return fmt.Errorf("resolve %s source: %w", kind, err)
}

func truncate(s string, limit int) string {
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "…"
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func decodeJSONTags(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

// emailTags lifts the classifier's labels so a learning item inherits the same
// vocabulary the inbox already shows.
func emailTags(m *email.Email) []string {
	var out []string
	if c := strings.TrimSpace(m.Category); c != "" {
		out = append(out, c)
	}
	if m.Importance != "" {
		out = append(out, "importance:"+m.Importance)
	}
	return out
}
