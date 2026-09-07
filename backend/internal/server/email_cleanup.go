package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/halfking/pocket-opencode/backend/internal/email"
)

// handleEmailCleanup — POST /api/emails/cleanup
//
// dryRun=true：只按主题/来源/日期预览。
// dryRun=false：先 IMAP MOVE 到 Junk，再删 MOVE 成功的 PG 行。
func (s *Server) handleEmailCleanup(w http.ResponseWriter, r *http.Request) {
	if s.emailStore == nil {
		writeError(w, http.StatusServiceUnavailable, "email store not configured")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
		return
	}
	var body struct {
		AccountID string `json:"accountId"`
		Subject   string `json:"subject"`
		From      string `json:"from"`
		Since     int64  `json:"since"`
		Until     int64  `json:"until"`
		DryRun    bool   `json:"dryRun"`
	}
	if len(strings.TrimSpace(string(raw))) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid body")
			return
		}
	}
	f := email.CleanupFilter{
		AccountID: strings.TrimSpace(body.AccountID),
		Subject:   strings.TrimSpace(body.Subject),
		From:      strings.TrimSpace(body.From),
		Since:     body.Since,
		Until:     body.Until,
	}
	if err := f.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var mover email.JunkUIDMover
	if s.emailFetcher != nil {
		mover = s.emailFetcher
	}
	rep, err := email.RunCleanup(
		r.Context(),
		s.emailStore,
		mover,
		f,
		s.userIDFromRequest(r),
		s.workspaceIDFromRequest(r),
		body.DryRun,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rep)
}
