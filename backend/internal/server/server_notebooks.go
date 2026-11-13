package server

// Notebooks HTTP handlers (docs/2026-11-14-knowledge-base-integration-plan.md
// §5.1.2 Backend API 扩展, §7.2 Notebooks API).
//
// Routes (registered in server.go, both behind requireAuth):
//
//	GET    /api/notebooks            handleNotebooks          — 列出（树形）
//	POST   /api/notebooks            handleNotebooks          — 创建
//	GET    /api/notebooks/:id        handleNotebookOperations — 详情
//	PUT    /api/notebooks/:id        handleNotebookOperations — 更新
//	DELETE /api/notebooks/:id        handleNotebookOperations — 删除
//	GET    /api/notebooks/:id/notes  handleNotebookOperations — 笔记本内的笔记
//
// Identity always comes from the JWT (userIDFromRequest /
// workspaceIDFromRequest); a client-supplied userId is ignored. The store is
// nil-safe: when SetNotebookStore was never called the routes answer 503,
// matching the flashcards/learning convention.

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/halfking/pocket-opencode/backend/internal/notebooks"
)

// handleNotebooks serves the collection routes: GET (tree) and POST (create).
func (s *Server) handleNotebooks(w http.ResponseWriter, r *http.Request) {
	if s.notebookStore == nil {
		writeError(w, http.StatusServiceUnavailable, "notebooks store not configured")
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.handleNotebooksGet(w, r)
	case http.MethodPost:
		s.handleNotebooksCreate(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "GET/POST only")
	}
}

// handleNotebookOperations serves the item routes under /api/notebooks/:id,
// including the /notes sub-resource.
func (s *Server) handleNotebookOperations(w http.ResponseWriter, r *http.Request) {
	if s.notebookStore == nil {
		writeError(w, http.StatusServiceUnavailable, "notebooks store not configured")
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/notebooks/")
	rest = strings.TrimSuffix(rest, "/")
	if rest == "" {
		writeError(w, http.StatusBadRequest, "missing notebook id")
		return
	}
	if strings.HasSuffix(rest, "/notes") {
		s.handleNotebookNotes(w, r, strings.TrimSuffix(rest, "/notes"))
		return
	}
	if strings.Contains(rest, "/") {
		writeError(w, http.StatusNotFound, "unknown notebooks sub-resource")
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.handleNotebookGet(w, r, rest)
	case http.MethodPut, http.MethodPatch:
		s.handleNotebookUpdate(w, r, rest)
	case http.MethodDelete:
		s.handleNotebookDelete(w, r, rest)
	default:
		writeError(w, http.StatusMethodNotAllowed, "GET/PUT/DELETE only")
	}
}

// handleNotebooksGet — GET /api/notebooks. Returns the caller's notebooks as a
// tree (roots carry their children recursively).
func (s *Server) handleNotebooksGet(w http.ResponseWriter, r *http.Request) {
	tree, err := s.notebookStore.ListByUser(s.userIDFromRequest(r), s.workspaceIDFromRequest(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tree == nil {
		tree = []notebooks.Notebook{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"notebooks": tree})
}

// handleNotebooksCreate — POST /api/notebooks. Body shape per §7.2:
// {name, parentId, icon, color, sortOrder}.
func (s *Server) handleNotebooksCreate(w http.ResponseWriter, r *http.Request) {
	var input notebooks.NotebookInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if err := input.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	uid := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)
	if err := s.ensureNotebookOwned(input.ParentID, uid, wsID); err != nil {
		s.writeNotebookLookupError(w, err)
		return
	}
	nb := &notebooks.Notebook{
		ID:          randomID("nb"),
		UserID:      uid,
		WorkspaceID: wsID,
		Name:        input.Name,
		ParentID:    input.ParentID,
		Icon:        input.Icon,
		Color:       input.Color,
		SortOrder:   input.SortOrder,
	}
	if err := s.notebookStore.Create(nb); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, nb)
}

// handleNotebookGet — GET /api/notebooks/:id.
func (s *Server) handleNotebookGet(w http.ResponseWriter, r *http.Request, id string) {
	nb, err := s.notebookStore.GetByID(id)
	if err != nil {
		s.writeNotebookLookupError(w, err)
		return
	}
	if !s.notebookVisible(nb, r) {
		writeError(w, http.StatusNotFound, "notebook not found")
		return
	}
	writeJSON(w, http.StatusOK, nb)
}

// handleNotebookUpdate — PUT /api/notebooks/:id. Ownership fields (userId,
// workspaceId) are immutable; only the NotebookInput fields are writable.
func (s *Server) handleNotebookUpdate(w http.ResponseWriter, r *http.Request, id string) {
	existing, err := s.notebookStore.GetByID(id)
	if err != nil {
		s.writeNotebookLookupError(w, err)
		return
	}
	if !s.notebookVisible(existing, r) {
		writeError(w, http.StatusNotFound, "notebook not found")
		return
	}
	var input notebooks.NotebookInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if err := input.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if input.ParentID == id {
		writeError(w, http.StatusBadRequest, "notebook cannot be its own parent")
		return
	}
	if err := s.ensureNotebookOwned(input.ParentID, existing.UserID, existing.WorkspaceID); err != nil {
		s.writeNotebookLookupError(w, err)
		return
	}
	existing.Name = input.Name
	existing.ParentID = input.ParentID
	existing.Icon = input.Icon
	existing.Color = input.Color
	existing.SortOrder = input.SortOrder
	if err := s.notebookStore.Update(existing); err != nil {
		s.writeNotebookLookupError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, existing)
}

// handleNotebookDelete — DELETE /api/notebooks/:id. Child notebooks cascade
// and notes move to the parent via the DB trigger (see notebooks.Store.Delete).
func (s *Server) handleNotebookDelete(w http.ResponseWriter, r *http.Request, id string) {
	existing, err := s.notebookStore.GetByID(id)
	if err != nil {
		s.writeNotebookLookupError(w, err)
		return
	}
	if !s.notebookVisible(existing, r) {
		writeError(w, http.StatusNotFound, "notebook not found")
		return
	}
	if err := s.notebookStore.Delete(id); err != nil {
		s.writeNotebookLookupError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

// handleNotebookNotes — GET /api/notebooks/:id/notes. Notes come from the
// notes store (Postgres), scoped to the caller; the notebook itself must be
// owned by the caller.
func (s *Server) handleNotebookNotes(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	nb, err := s.notebookStore.GetByID(id)
	if err != nil {
		s.writeNotebookLookupError(w, err)
		return
	}
	if !s.notebookVisible(nb, r) {
		writeError(w, http.StatusNotFound, "notebook not found")
		return
	}
	if s.notesStore == nil {
		writeError(w, http.StatusServiceUnavailable, "notes store not configured")
		return
	}
	list, err := s.notesStore.ListByNotebook(r.Context(), s.userIDFromRequest(r), s.workspaceIDFromRequest(r), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"notes": list})
}

// notebookVisible reports whether nb belongs to the authenticated caller.
// A foreign notebook is reported as 404, never 403, so ids are not enumerable.
func (s *Server) notebookVisible(nb *notebooks.Notebook, r *http.Request) bool {
	if nb.UserID != s.userIDFromRequest(r) {
		return false
	}
	wsID := s.workspaceIDFromRequest(r)
	return nb.WorkspaceID == wsID || (nb.WorkspaceID == "" && wsID == "default")
}

// ensureNotebookOwned checks that a parent notebook (when set) exists and
// belongs to the caller, so a notebook cannot be filed under someone else's.
func (s *Server) ensureNotebookOwned(parentID, userID, workspaceID string) error {
	if parentID == "" {
		return nil
	}
	parent, err := s.notebookStore.GetByID(parentID)
	if err != nil {
		return err
	}
	wsID := workspaceID
	if wsID == "" {
		wsID = "default"
	}
	owned := parent.UserID == userID &&
		(parent.WorkspaceID == wsID || (parent.WorkspaceID == "" && wsID == "default"))
	if !owned {
		return errors.New("notebook not found: " + parentID)
	}
	return nil
}

// writeNotebookLookupError maps the store's "notebook not found: …" errors to
// 404; everything else is a server-side failure.
func (s *Server) writeNotebookLookupError(w http.ResponseWriter, err error) {
	if err != nil && strings.HasPrefix(err.Error(), "notebook not found") {
		writeError(w, http.StatusNotFound, "notebook not found")
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}
