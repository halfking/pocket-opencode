package server

import (
	"net/http"
	"strconv"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/identity"
	"github.com/halfking/pocket-opencode/backend/internal/meeting"
)

type meetingMinutesSnapshot struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	Minutes      string    `json:"minutes"`
	Participants []string  `json:"participants"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (s *Server) handleMeetingMinutesHistory(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	claims := s.claimsFromContext(r)
	if claims == nil || claims.UserID == "" || claims.WorkspaceID == "" {
		writeError(w, http.StatusUnauthorized, "verified workspace claims required")
		return
	}
	if s.meetingStore == nil || s.identityStore == nil {
		writeError(w, http.StatusServiceUnavailable, "meeting history unavailable")
		return
	}
	if claims.ResourceScope == "" {
		status, _, err := s.identityStore.CheckMembership(r.Context(), claims.WorkspaceID, claims.UserID)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "workspace membership verification unavailable")
			return
		}
		if status != identity.MembershipActive {
			writeError(w, http.StatusForbidden, "current workspace membership required")
			return
		}
		if s.loadSyncPolicyState(claims.UserID, claims.WorkspaceID).Modules["meetings"] != "cloud" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "module_policy_local", "module": "meetings"})
			return
		}
	}
	offset := 0
	if raw := r.URL.Query().Get("offset"); raw != "" {
		var err error
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 || offset > meeting.ListMaxOffset {
			writeError(w, http.StatusBadRequest, "invalid history offset")
			return
		}
	}
	rows, total, err := s.meetingStore.ListScoped(claims.UserID, claims.WorkspaceID, meeting.ListOptions{Limit: 20, Offset: offset})
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "meeting history lookup unavailable")
		return
	}
	snapshots := make([]meetingMinutesSnapshot, 0, len(rows))
	for _, row := range rows {
		snapshots = append(snapshots, meetingMinutesSnapshot{
			ID: row.ID, Title: row.Title, Minutes: row.Summary, Participants: append([]string{}, row.Participants...),
			CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC(),
		})
	}
	var next *int
	if n := offset + len(rows); n > offset && n < total && n <= meeting.ListMaxOffset {
		next = &n
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user_id": claims.UserID, "workspace_id": claims.WorkspaceID, "meetings": snapshots, "next_offset": next,
	})
}
