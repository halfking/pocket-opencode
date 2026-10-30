package server

import (
	"bytes"
	"context"
	"errors"
	"github.com/halfking/pocket-opencode/backend/internal/auth"
	"github.com/halfking/pocket-opencode/backend/internal/background"
	"net/http"
	"time"
)

func (s *Server) tryResourceAuth(w http.ResponseWriter, r *http.Request, raw string, next http.HandlerFunc) bool {
	claims, err := s.jwtSigner.ParseResource(raw, time.Now())
	if err != nil {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	scope := auth.ResourceScope(r.Method, r.URL.Path, r.URL.RawPath)
	allowed := false
	for _, v := range claims.Scopes {
		if v == scope {
			allowed = true
		}
	}
	if scope == "" || !allowed {
		writeError(w, 403, "resource scope does not allow this route")
		return true
	}
	code, role := s.resourceCurrent(r.Context(), claims, scope)
	if code != 200 {
		writeError(w, code, role)
		return true
	}
	ctx, cancel := context.WithDeadline(r.Context(), claims.ExpiresAt.Time)
	defer cancel()
	// Long-running nonstreaming calls are cancelled on expiry/revocation. No
	// streaming route belongs to the allowlist, and response writes are fenced.
	// §949：走 background.Go —— 裸 go 语句里的 panic 会终止整个进程。
	background.Go("resource-auth/revalidate", func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if code, _ := s.resourceCurrent(ctx, claims, scope); code != 200 {
					cancel()
					return
				}
			}
		}
	})
	ctx = context.WithValue(ctx, authClaimsContextKey{}, &authClaims{UserID: claims.Subject, TenantID: claims.TenantID, WorkspaceID: claims.WorkspaceID, Role: role})
	r = r.Clone(ctx)
	r.Header.Set("X-User-ID", claims.Subject)
	r.Header.Set("X-Tenant-ID", claims.TenantID)
	r.Header.Set("X-Workspace-ID", claims.WorkspaceID)
	r.Header.Set("X-User-Role", role)
	fenced := &resourceResponseWriter{ResponseWriter: w, ctx: ctx, validate: func() bool {
		code, _ := s.resourceCurrent(ctx, claims, scope)
		if code != 200 {
			cancel()
			return false
		}
		return true
	}}
	if scope == "email-metadata" && r.URL.Path == "/api/email/accounts" {
		s.handleResourceEmailAccounts(fenced, r)
	} else {
		next(fenced, r)
	}
	fenced.publish()
	return true
}

type resourceResponseWriter struct {
	http.ResponseWriter
	ctx      context.Context
	validate func() bool
	header   http.Header
	status   int
	body     bytes.Buffer
	failed   bool
}

func (w *resourceResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}
func (w *resourceResponseWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		w.failed = true
		return 0, err
	}
	if w.body.Len()+len(p) > 8<<20 {
		w.failed = true
		return 0, errors.New("resource response exceeds safe buffer")
	}
	if w.status == 0 {
		w.status = 200
	}
	return w.body.Write(p)
}
func (w *resourceResponseWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
}

// No resource scope permits streaming. Buffering prevents an early success
// header from escaping before final authorization and bounds memory use.
func (w *resourceResponseWriter) publish() {
	w.ResponseWriter.Header().Set("Cache-Control", "no-store")
	if w.failed || w.ctx.Err() != nil || (w.validate != nil && !w.validate()) {
		w.ResponseWriter.Header().Set("Content-Type", "application/json")
		// Admission already happened: callers must not automatically replay an
		// ambiguous write after expiry or revocation while work was in flight.
		w.ResponseWriter.WriteHeader(http.StatusConflict)
		_, _ = w.ResponseWriter.Write([]byte(`{"error":"resource_authorization_changed","message":"resource authorization changed while the request was in flight"}`))
		return
	}
	for key, values := range w.Header() {
		for _, value := range values {
			w.ResponseWriter.Header().Add(key, value)
		}
	}
	w.ResponseWriter.Header().Set("Cache-Control", "no-store")
	status := w.status
	if status == 0 {
		status = 200
	}
	w.ResponseWriter.WriteHeader(status)
	_, _ = w.ResponseWriter.Write(w.body.Bytes())
}

func (s *Server) handleResourceEmailAccounts(w http.ResponseWriter, r *http.Request) {
	if s.emailStore == nil {
		writeError(w, 503, "email store not configured")
		return
	}
	accounts, err := s.emailStore.ListAccountsScoped(r.Context(), s.userIDFromRequest(r), s.workspaceIDFromRequest(r))
	if err != nil {
		writeError(w, 503, "email metadata lookup unavailable")
		return
	}
	accounts = filterEmailAccountsSince(accounts, parseSinceQuery(r.URL.Query().Get("since")))
	rows := make([]map[string]any, 0, len(accounts))
	for _, a := range accounts {
		rows = append(rows, map[string]any{"id": a.ID, "userId": a.UserID, "workspaceId": a.WorkspaceID, "displayName": a.DisplayName, "emailAddress": a.EmailAddress, "lastSyncedAt": a.LastSyncedAt, "createdAt": a.CreatedAt, "updatedAt": a.UpdatedAt})
	}
	writeJSON(w, 200, map[string]any{"accounts": rows})
}
