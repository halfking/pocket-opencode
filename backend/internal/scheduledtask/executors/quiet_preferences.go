package executors

// Adapter that resolves a user's do-not-disturb preferences from the generic
// per-user settings store.
//
// The executor runs from the scheduler, not from a request, so it cannot ask
// the client what time it is where the user is. Storing the timezone with the
// user is what makes the window correct for a tick nobody is watching: the
// document is `notifications` / `quiet-hours` and looks like
//
//	{"timezone":"Asia/Shanghai","startMin":1350,"endMin":450}
//
// A missing document, a malformed one, or a store that is not configured all
// mean the same thing: fall back to the server default. The one thing this
// adapter must never do is guess UTC, because that is the bug it replaces.

import (
	"context"

	"github.com/halfking/pocket-opencode/backend/internal/task"
	"github.com/halfking/pocket-opencode/backend/internal/usersetting"
)

// The user-setting document that holds these preferences.
const (
	QuietSettingsNamespace = "notifications"
	QuietSettingsID        = "quiet-hours"
)

// SettingsReader is the slice of usersetting.Repository this adapter needs.
type SettingsReader interface {
	Get(userID, workspaceID, namespace, id string) (*usersetting.Record, error)
}

type settingsQuietPreferences struct{ reader SettingsReader }

// NewSettingsQuietPreferences returns a lookup backed by the per-user settings
// store. A nil reader yields a lookup that reports "no preferences", which the
// executor treats as "use the server default".
func NewSettingsQuietPreferences(reader SettingsReader) QuietPreferencesLookup {
	return &settingsQuietPreferences{reader: reader}
}

func (s *settingsQuietPreferences) QuietPreferences(_ context.Context, wsID, userID string) (task.QuietPreferences, bool) {
	if s == nil || s.reader == nil || userID == "" {
		return task.QuietPreferences{}, false
	}
	rec, err := s.reader.Get(userID, wsID, QuietSettingsNamespace, QuietSettingsID)
	if err != nil || rec == nil {
		// Not found is the normal case for a user who never touched the
		// setting; a store error is handled the same way, because failing to
		// read a preference must not stop reminders from firing.
		return task.QuietPreferences{}, false
	}
	return task.QuietPreferencesFromPayload(rec.Payload)
}
