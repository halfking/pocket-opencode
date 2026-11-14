package wikilinks

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

// NoteStore defines the interface for note storage operations needed by the resolver.
type NoteStore interface {
	// GetByTitle finds a note by its title (case-insensitive)
	GetByTitle(ctx context.Context, title, userID, workspaceID string) (*Note, error)
	// GetAllByUser retrieves all notes for a user (for building note maps)
	GetAllByUser(ctx context.Context, userID, workspaceID string) ([]Note, error)
}

// LinkStore defines the interface for note link storage operations.
type LinkStore interface {
	// CreateBatch creates multiple links in a transaction
	CreateBatch(links []NoteLink) error
	// GetOutgoingLinks retrieves all links from a source note
	GetOutgoingLinks(sourceNoteID string) ([]NoteLink, error)
}

// Note represents a minimal note structure needed for resolution.
type Note struct {
	ID    string
	Title string
}

// NoteLink represents a link between notes.
type NoteLink struct {
	ID           string
	SourceNoteID string
	TargetNoteID string
	LinkType     string
	Confidence   float64
	CreatedBy    string
	Reason       string
	CreatedAt    int64
}

// Resolver resolves wiki links in note content and creates bidirectional links.
type Resolver struct {
	noteStore NoteStore
	linkStore LinkStore
}

// NewResolver creates a new wiki link resolver.
func NewResolver(noteStore NoteStore, linkStore LinkStore) *Resolver {
	return &Resolver{
		noteStore: noteStore,
		linkStore: linkStore,
	}
}

// ResolveLinks parses wiki links in the note content and creates corresponding
// note links in the database. Returns the list of created links or an error.
//
// This function:
//  1. Parses all [[...]] wiki links from the content
//  2. Looks up target notes by title (case-insensitive)
//  3. Creates bidirectional links for found notes
//  4. Skips links to non-existent notes (user can see them as unresolved in UI)
//
// Example:
//   content := "See [[Q3 Budget]] and [[Process]]"
//   links, err := resolver.ResolveLinks(ctx, "note-123", content, "user-1", "ws-1")
func (r *Resolver) ResolveLinks(ctx context.Context, noteID, content, userID, workspaceID string) ([]NoteLink, error) {
	// Extract wiki link targets from content
	targets := ExtractTargets(content)
	if len(targets) == 0 {
		return nil, nil
	}

	// Get existing outgoing links to avoid duplicates
	existingLinks, err := r.linkStore.GetOutgoingLinks(noteID)
	if err != nil {
		return nil, fmt.Errorf("get existing links: %w", err)
	}

	existingTargets := make(map[string]bool)
	for _, link := range existingLinks {
		existingTargets[link.TargetNoteID] = true
	}

	// Resolve each target to a note ID
	var newLinks []NoteLink
	for _, target := range targets {
		// Look up target note by title
		targetNote, err := r.noteStore.GetByTitle(ctx, target, userID, workspaceID)
		if err != nil {
			// Log but don't fail - note might not exist yet
			continue
		}
		if targetNote == nil {
			// Target note doesn't exist - skip for now
			continue
		}

		// Prevent self-linking
		if targetNote.ID == noteID {
			continue
		}

		// Skip if link already exists
		if existingTargets[targetNote.ID] {
			continue
		}

		// Create bidirectional link
		link := NoteLink{
			ID:           randomLinkID(),
			SourceNoteID: noteID,
			TargetNoteID: targetNote.ID,
			LinkType:     "bidirectional",
			Confidence:   1.0,
			CreatedBy:    "wikilink",
			Reason:       fmt.Sprintf("Wiki link: [[%s]]", target),
		}

		newLinks = append(newLinks, link)
	}

	// Create all new links in a batch
	if len(newLinks) > 0 {
		if err := r.linkStore.CreateBatch(newLinks); err != nil {
			return nil, fmt.Errorf("create links: %w", err)
		}
	}

	return newLinks, nil
}

// BuildNoteTitleMap creates a map from normalized titles to note IDs for all user notes.
// This is useful for rendering wiki links in the UI.
func (r *Resolver) BuildNoteTitleMap(ctx context.Context, userID, workspaceID string) (map[string]string, error) {
	notes, err := r.noteStore.GetAllByUser(ctx, userID, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("get all notes: %w", err)
	}

	noteMap := make(map[string]string, len(notes))
	for _, note := range notes {
		if note.Title != "" {
			noteMap[NormalizeTitle(note.Title)] = note.ID
		}
	}

	return noteMap, nil
}

// randomLinkID generates a random link ID.
func randomLinkID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return "link-" + hex.EncodeToString(b)
}

// FindUnresolvedLinks returns wiki link targets that don't have corresponding notes.
// Useful for showing "create note" suggestions in the UI.
func (r *Resolver) FindUnresolvedLinks(ctx context.Context, content, userID, workspaceID string) ([]string, error) {
	targets := ExtractTargets(content)
	if len(targets) == 0 {
		return nil, nil
	}

	var unresolved []string
	for _, target := range targets {
		note, err := r.noteStore.GetByTitle(ctx, target, userID, workspaceID)
		if err != nil {
			return nil, fmt.Errorf("check note existence: %w", err)
		}
		if note == nil {
			unresolved = append(unresolved, target)
		}
	}

	return unresolved, nil
}

// RenderContent replaces wiki links in content with markdown links for display.
// Uses the note map to resolve titles to note IDs.
func (r *Resolver) RenderContent(ctx context.Context, content, userID, workspaceID string) (string, error) {
	noteMap, err := r.BuildNoteTitleMap(ctx, userID, workspaceID)
	if err != nil {
		return content, err
	}

	return ReplaceWikiLinks(content, noteMap), nil
}

// SuggestLinks analyzes note content and suggests potential wiki links based on
// existing note titles. Returns a list of note titles that appear in the content
// but are not yet wiki-linked.
func (r *Resolver) SuggestLinks(ctx context.Context, content, userID, workspaceID string) ([]string, error) {
	// Get all user notes
	notes, err := r.noteStore.GetAllByUser(ctx, userID, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("get user notes: %w", err)
	}

	// Get already linked titles
	existingTargets := ExtractTargets(content)
	existing := make(map[string]bool)
	for _, target := range existingTargets {
		existing[NormalizeTitle(target)] = true
	}

	// Find note titles that appear in content but aren't wiki-linked
	contentLower := strings.ToLower(content)
	var suggestions []string
	for _, note := range notes {
		if note.Title == "" {
			continue
		}

		normalizedTitle := NormalizeTitle(note.Title)
		if existing[normalizedTitle] {
			continue // Already linked
		}

		// Check if title appears in content
		if strings.Contains(contentLower, strings.ToLower(note.Title)) {
			suggestions = append(suggestions, note.Title)
		}
	}

	return suggestions, nil
}
