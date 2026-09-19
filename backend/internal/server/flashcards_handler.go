package server

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/flashcards"
)

// ---------------------------------------------------------------------------
// /api/flashcards — collection endpoint
// ---------------------------------------------------------------------------

// handleFlashcardsCollection routes GET/POST on /api/flashcards.
//
// GET returns the incremental pull envelope per the contract §2:
//
//	{cards, decks, serverTimeMs, deletedIds?}
//
// POST creates a new note (with one initial card in state=0, due=now).
//
// Both paths derive user_id strictly from the JWT via
// Server.userIDFromRequest; any userId/userID field in the body is stripped
// with a warning (see requireOwnerAuthority).
func (s *Server) handleFlashcardsCollection(w http.ResponseWriter, r *http.Request) {
	if s.flashcardStore == nil {
		writeError(w, http.StatusServiceUnavailable, "flashcards store not configured")
		return
	}
	userID := s.userIDFromRequest(r)

	switch r.Method {
	case http.MethodGet:
		s.flashcardsGetCollection(w, r, userID)
	case http.MethodPost:
		s.flashcardsCreateNote(w, r, userID)
	default:
		writeError(w, http.StatusMethodNotAllowed, "GET or POST only")
	}
}

func (s *Server) flashcardsGetCollection(w http.ResponseWriter, r *http.Request, userID string) {
	ctx := r.Context()
	since, limit := parseFlashcardsSinceLimit(r)
	cards, err := s.flashcardStore.ListCardsSince(ctx, userID, since, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	decks, err := s.flashcardStore.ListDeckConfigsSince(ctx, userID, since, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	deletedIDs, err := s.flashcardStore.ListDeletedCardsSince(ctx, userID, since, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp := map[string]interface{}{
		"cards":        cards,
		"decks":        decks,
		"serverTimeMs": time.Now().UnixMilli(),
	}
	if since > 0 {
		resp["deletedIds"] = deletedIDs
	}
	writeJSON(w, http.StatusOK, resp)
}

// flashcardsCreateNote decodes a JSON body containing deckId/front/back/
// tags? and creates a note + one initial card. The userId/userID field is
// always stripped — JWT is authoritative.
func (s *Server) flashcardsCreateNote(w http.ResponseWriter, r *http.Request, userID string) {
	body, err := decodeFlashcardsBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var input struct {
		DeckID string   `json:"deckId"`
		Front  string   `json:"front"`
		Back   string   `json:"back"`
		Tags   []string `json:"tags"`
	}
	if err := json.Unmarshal(body, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if input.DeckID == "" {
		writeError(w, http.StatusBadRequest, "deckId is required")
		return
	}
	if strings.TrimSpace(input.Front) == "" || strings.TrimSpace(input.Back) == "" {
		writeError(w, http.StatusBadRequest, "front and back are required")
		return
	}

	note := &flashcards.Note{
		ID:     newFlashcardID("note"),
		UserID: userID,
		DeckID: input.DeckID,
		Front:  input.Front,
		Back:   input.Back,
		Tags:   flashcards.TagsToJSON(input.Tags),
	}
	if err := s.flashcardStore.CreateNote(r.Context(), note); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// One initial card in state=0 (new) with due = now (sec). The frontend
	// will re-compute due/state through ts-fsrs if it wants to schedule
	// the card for a specific deck-config graduation day.
	now := time.Now().Unix()
	card := &flashcards.Card{
		ID:        newFlashcardID("card"),
		NoteID:    note.ID,
		UserID:    userID,
		DeckID:    note.DeckID,
		State:     0,
		Due:       now,
		Usn:       0,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.flashcardStore.CreateCard(r.Context(), card); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"note": note,
		"card": card,
	})
}

// ---------------------------------------------------------------------------
// /api/flashcards/ — item dispatcher
// ---------------------------------------------------------------------------

// handleFlashcardsItem dispatches the path-based sub-resources:
//
//	GET    /api/flashcards/notes?since=<sec>            → notes list
//	POST   /api/flashcards/cards                        → create extra card
//	PATCH  /api/flashcards/cards/:id                    → patch due/state
//	POST   /api/flashcards/cards/:id/review             → record review
//	GET    /api/flashcards/decks/:id/due?now=<sec>      → due cards for deck
//	PATCH  /api/flashcards/notes/:id                    → edit note
//	DELETE /api/flashcards/notes/:id                    → soft-delete + cascade
func (s *Server) handleFlashcardsItem(w http.ResponseWriter, r *http.Request) {
	if s.flashcardStore == nil {
		writeError(w, http.StatusServiceUnavailable, "flashcards store not configured")
		return
	}
	userID := s.userIDFromRequest(r)

	path := strings.TrimPrefix(r.URL.Path, "/api/flashcards/")
	parts := strings.Split(strings.Trim(path, "/"), "/")

	if len(parts) == 1 && parts[0] == "notes" {
		s.flashcardsNotesCollection(w, r, userID)
		return
	}
	if len(parts) == 1 && parts[0] == "cards" && r.Method == http.MethodPost {
		s.flashcardsCreateCard(w, r, userID)
		return
	}
	if len(parts) >= 2 && parts[0] == "cards" {
		switch {
		case len(parts) == 2 && r.Method == http.MethodPatch:
			s.flashcardsPatchCard(w, r, userID, parts[1])
			return
		case len(parts) == 3 && parts[2] == "review" && r.Method == http.MethodPost:
			s.flashcardsReviewCard(w, r, userID, parts[1])
			return
		}
	}
	if len(parts) >= 3 && parts[0] == "decks" && parts[2] == "due" && r.Method == http.MethodGet {
		s.flashcardsDeckDue(w, r, userID, parts[1])
		return
	}
	if len(parts) >= 2 && parts[0] == "notes" {
		switch r.Method {
		case http.MethodPatch:
			s.flashcardsPatchNote(w, r, userID, parts[1])
			return
		case http.MethodDelete:
			s.flashcardsDeleteNote(w, r, userID, parts[1])
			return
		case http.MethodGet:
			s.flashcardsGetNote(w, r, userID, parts[1])
			return
		}
	}
	writeError(w, http.StatusNotFound, "not found")
}

func (s *Server) flashcardsNotesCollection(w http.ResponseWriter, r *http.Request, userID string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	since, limit := parseFlashcardsSinceLimit(r)
	notes, err := s.flashcardStore.ListNotesSince(r.Context(), userID, since, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	deletedIDs, err := s.flashcardStore.ListDeletedNotesSince(r.Context(), userID, since, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp := map[string]interface{}{
		"notes":        notes,
		"serverTimeMs": time.Now().UnixMilli(),
	}
	if since > 0 {
		resp["deletedIds"] = deletedIDs
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) flashcardsGetNote(w http.ResponseWriter, r *http.Request, userID, id string) {
	n, err := s.flashcardStore.GetNote(r.Context(), userID, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if n == nil {
		writeError(w, http.StatusNotFound, "note not found")
		return
	}
	writeJSON(w, http.StatusOK, n)
}

func (s *Server) flashcardsPatchNote(w http.ResponseWriter, r *http.Request, userID, id string) {
	body, err := decodeFlashcardsBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var input struct {
		Front *string   `json:"front"`
		Back  *string   `json:"back"`
		Tags  *[]string `json:"tags"`
	}
	if err := json.Unmarshal(body, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	var tagsJSON *string
	if input.Tags != nil {
		s := flashcards.TagsToJSON(*input.Tags)
		tagsJSON = &s
	}
	updated, err := s.flashcardStore.UpdateNote(r.Context(), userID, id, input.Front, input.Back, tagsJSON)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if updated == nil {
		writeError(w, http.StatusNotFound, "note not found")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) flashcardsDeleteNote(w http.ResponseWriter, r *http.Request, userID, id string) {
	if err := s.flashcardStore.SoftDeleteNote(r.Context(), userID, id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

// flashcardsCreateCard handles POST /api/flashcards/cards (extra card for
// an existing note).
func (s *Server) flashcardsCreateCard(w http.ResponseWriter, r *http.Request, userID string) {
	body, err := decodeFlashcardsBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var input struct {
		NoteID string `json:"noteId"`
		Due    *int64 `json:"due"`
	}
	if err := json.Unmarshal(body, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if input.NoteID == "" {
		writeError(w, http.StatusBadRequest, "noteId is required")
		return
	}
	note, err := s.flashcardStore.GetNote(r.Context(), userID, input.NoteID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if note == nil {
		writeError(w, http.StatusNotFound, "note not found")
		return
	}
	now := time.Now().Unix()
	due := now
	if input.Due != nil {
		due = *input.Due
	}
	card := &flashcards.Card{
		ID:        newFlashcardID("card"),
		NoteID:    note.ID,
		UserID:    userID,
		DeckID:    note.DeckID,
		State:     0,
		Due:       due,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.flashcardStore.CreateCard(r.Context(), card); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, card)
}

func (s *Server) flashcardsPatchCard(w http.ResponseWriter, r *http.Request, userID, id string) {
	body, err := decodeFlashcardsBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var input struct {
		Due   *int64 `json:"due"`
		State *int   `json:"state"`
	}
	if err := json.Unmarshal(body, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if input.State != nil && (*input.State < 0 || *input.State > 3) {
		writeError(w, http.StatusBadRequest, "state must be 0..3")
		return
	}
	updated, err := s.flashcardStore.UpdateCard(r.Context(), userID, id, input.Due, input.State)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if updated == nil {
		writeError(w, http.StatusNotFound, "card not found")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// flashcardsReviewCard handles POST /api/flashcards/cards/:id/review. v1
// persistence only — FSRS math stays client-side (see cards.go).
func (s *Server) flashcardsReviewCard(w http.ResponseWriter, r *http.Request, userID, id string) {
	body, err := decodeFlashcardsBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var input struct {
		Rating     int   `json:"rating"`
		ReviewedAt int64 `json:"reviewedAt"`
	}
	if err := json.Unmarshal(body, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	card, log, err := s.flashcardStore.RecordReview(r.Context(), userID, id, input.Rating, input.ReviewedAt)
	if err != nil {
		if errors.Is(err, flashcards.ErrCardNotFound) {
			writeError(w, http.StatusNotFound, "card not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"card": card,
		"log":  log,
	})
}

// flashcardsDeckDue handles GET /api/flashcards/decks/:id/due?now=<sec>.
func (s *Server) flashcardsDeckDue(w http.ResponseWriter, r *http.Request, userID, deckID string) {
	now := time.Now().Unix()
	if raw := r.URL.Query().Get("now"); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v > 0 {
			now = v
		}
	}
	cards, err := s.flashcardStore.ListDueCardsInDeck(r.Context(), userID, deckID, now, 500)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"cards":    cards,
		"totalDue": len(cards),
	})
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// decodeFlashcardsBody reads the request body (capped) and rejects any
// userId/userID fields with a log warning. The authority user_id is always
// the JWT claim from Server.userIDFromRequest.
func decodeFlashcardsBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, errors.New("request body is required")
	}
	const maxBody = 1 << 20
	limited := io.LimitReader(r.Body, maxBody+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, errors.New("read request body: " + err.Error())
	}
	if len(data) > maxBody {
		return nil, errors.New("request body too large")
	}
	if hasAuthorityField(data) {
		log.Printf("[flashcards] WARN: ignoring userId/userID in request body; JWT is authoritative")
	}
	return data, nil
}

// hasAuthorityField does a fast case-insensitive search for "userId" or
// "userID" anywhere in the JSON. Good enough to catch obvious mistakes
// without paying the full Unmarshal cost twice.
func hasAuthorityField(data []byte) bool {
	low := strings.ToLower(string(data))
	return strings.Contains(low, `"userid"`)
}

// parseFlashcardsSinceLimit extracts since/limit query params with sane
// defaults. since <= 0 means "no filter"; limit clamped to (0, 1000].
func parseFlashcardsSinceLimit(r *http.Request) (int64, int) {
	var since int64
	if raw := r.URL.Query().Get("since"); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil {
			since = v
		}
	}
	limit := 500
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			limit = v
			if limit > 1000 {
				limit = 1000
			}
		}
	}
	return since, limit
}

// newFlashcardID returns a 32-char hex id with a short prefix so the DB
// inspector can tell note ids from card ids at a glance.
func newFlashcardID(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return prefix + "_" + time.Now().Format("20060102150405.000000000")
	}
	const hex = "0123456789abcdef"
	out := make([]byte, 0, len(prefix)+1+32)
	out = append(out, prefix...)
	out = append(out, '_')
	for _, by := range b {
		out = append(out, hex[by>>4], hex[by&0x0f])
	}
	return string(out)
}
