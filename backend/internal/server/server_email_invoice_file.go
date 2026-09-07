package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/halfking/pocket-opencode/backend/internal/email"
)

func (s *Server) invoiceFileAbs(inv *email.Invoice) (string, error) {
	if inv == nil || inv.FilePath == "" {
		return "", os.ErrNotExist
	}
	abs := filepath.Join(s.dataDir, inv.FilePath)
	root := filepath.Clean(s.dataDir) + string(filepath.Separator)
	if !strings.HasPrefix(filepath.Clean(abs), root) {
		return "", os.ErrInvalid
	}
	return abs, nil
}

func (s *Server) loadScopedInvoiceFile(w http.ResponseWriter, r *http.Request, id string) (*email.Invoice, []byte, bool) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return nil, nil, false
	}
	inv, err := s.emailStore.GetInvoiceByIDScoped(r.Context(), id, s.userIDFromRequest(r), s.workspaceIDFromRequest(r))
	if err != nil {
		if err == email.ErrNotFound {
			writeError(w, http.StatusNotFound, "invoice not found")
			return nil, nil, false
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return nil, nil, false
	}
	abs, err := s.invoiceFileAbs(inv)
	if err != nil {
		if err == os.ErrInvalid {
			writeError(w, http.StatusBadRequest, "invalid file path")
			return nil, nil, false
		}
		writeError(w, http.StatusNotFound, "invoice file not harvested yet")
		return nil, nil, false
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		writeError(w, http.StatusNotFound, "invoice file missing")
		return nil, nil, false
	}
	return inv, data, true
}

// handleEmailInvoiceFile — GET /api/emails/invoices/{id}/file
func (s *Server) handleEmailInvoiceFile(w http.ResponseWriter, r *http.Request, id string) {
	inv, data, ok := s.loadScopedInvoiceFile(w, r, id)
	if !ok {
		return
	}
	kind, _ := email.DetectInvoiceMedia(data)
	name := inv.FileName
	if name == "" {
		name = "invoice"
	}
	inline := r.URL.Query().Get("inline") == "1"
	disp := "attachment"
	if inline {
		disp = "inline"
	}
	w.Header().Set("Content-Type", invoiceContentType(kind))
	w.Header().Set("Content-Disposition", fmt.Sprintf("%s; filename=%q", disp, name))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// handleEmailInvoiceThumb — GET /api/emails/invoices/{id}/thumb
func (s *Server) handleEmailInvoiceThumb(w http.ResponseWriter, r *http.Request, id string) {
	_, data, ok := s.loadScopedInvoiceFile(w, r, id)
	if !ok {
		return
	}
	thumb, ct, extracted := email.ExtractInvoiceThumb(data)
	if !extracted {
		writeError(w, http.StatusNotFound, "thumbnail unavailable")
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(thumb)
}

func invoiceContentType(kind string) string {
	switch kind {
	case "pdf":
		return "application/pdf"
	case "jpeg":
		return "image/jpeg"
	case "png":
		return "image/png"
	case "webp":
		return "image/webp"
	default:
		return "application/octet-stream"
	}
}
