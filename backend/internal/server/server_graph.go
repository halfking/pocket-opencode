package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/background"
	"github.com/halfking/pocket-opencode/backend/internal/notes"
)

// =====================================================================
// Knowledge Graph API (docs/2026-11-14 §3.4: 关系图谱可视化)
// =====================================================================

// GraphNode represents a node in the knowledge graph (note, notebook, or tag).
type GraphNode struct {
	ID    string    `json:"id"`
	Label string    `json:"label"`
	Type  string    `json:"type"` // "note" | "notebook" | "tag"
	Size  int       `json:"size"` // Link count (degree)
	Data  *NodeData `json:"data,omitempty"`
}

// NodeData contains additional metadata for graph nodes.
type NodeData struct {
	Domain    string   `json:"domain,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	CreatedAt string   `json:"createdAt,omitempty"`
	UpdatedAt string   `json:"updatedAt,omitempty"`
	Snippet   string   `json:"snippet,omitempty"`
}

// GraphEdge represents an edge in the knowledge graph.
type GraphEdge struct {
	Source string  `json:"source"`
	Target string  `json:"target"`
	Type   string  `json:"type"`   // "link" | "reference" | "tag" | "notebook"
	Weight float64 `json:"weight"` // Strength of relationship
}

// GraphData is the complete graph structure (nodes + edges).
type GraphData struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

// handleKnowledgeGraph — GET /api/knowledge/graph
//
// Returns the complete knowledge graph for the authenticated user, including:
//   - All notes as nodes
//   - All notebooks as nodes (if they contain notes)
//   - All tags as nodes
//   - Links between notes (from note_links table)
//   - Note-to-notebook relationships
//   - Note-to-tag relationships
//
// Query parameters:
//   - notebook: Filter by notebook ID (optional)
//   - domain: Filter by domain (work/study/life/idea)
//   - limit: Max number of notes to include (default 200)
func (s *Server) handleKnowledgeGraph(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	uid := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)

	// Parse query parameters
	notebookFilter := r.URL.Query().Get("notebook")
	domainFilter := r.URL.Query().Get("domain")
	limit := 200
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if l, err := parseIntSafe(limitStr); err == nil && l > 0 && l <= 500 {
			limit = l
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	graph, err := s.buildKnowledgeGraph(ctx, uid, wsID, notebookFilter, domainFilter, limit)
	if err != nil {
		log.Printf("[knowledge-graph] build failed: %v", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, graph)
}

// handleKnowledgeGraphForNote — GET /api/knowledge/graph/note/:id
//
// Returns a localized graph centered around a specific note, including:
//   - The central note
//   - Directly linked notes (1-hop neighbors)
//   - Shared tags
//   - Parent notebook
//   - Backlinks
//
// This provides a focused view for exploring relationships around a single note.
func (s *Server) handleKnowledgeGraphForNote(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract note ID from path: /api/knowledge/graph/note/{id}
	path := strings.TrimPrefix(r.URL.Path, "/api/knowledge/graph/note/")
	noteID := strings.TrimSuffix(path, "/")
	if noteID == "" {
		writeError(w, http.StatusBadRequest, "missing note id")
		return
	}

	uid := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	// Verify note exists and belongs to user
	if s.notesStore == nil {
		writeError(w, http.StatusServiceUnavailable, "notes store not configured")
		return
	}

	note, err := s.notesStore.GetByIDScoped(ctx, noteID, uid, wsID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if note == nil {
		writeError(w, http.StatusNotFound, "note not found")
		return
	}

	graph, err := s.buildLocalGraph(ctx, uid, wsID, noteID)
	if err != nil {
		log.Printf("[knowledge-graph] local build failed: %v", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, graph)
}

// buildKnowledgeGraph constructs the complete knowledge graph.
func (s *Server) buildKnowledgeGraph(ctx context.Context, userID, workspaceID, notebookFilter, domainFilter string, limit int) (*GraphData, error) {
	if s.notesStore == nil {
		return &GraphData{Nodes: []GraphNode{}, Edges: []GraphEdge{}}, nil
	}

	// Fetch notes
	var notesList []struct {
		ID         string
		Title      string
		Snippet    string
		Tags       string
		Domain     string
		NotebookID string
		CreatedAt  int64
		UpdatedAt  int64
	}

	// Use appropriate query based on filters
	var allNotes []notes.Note
	var err error

	if notebookFilter != "" {
		allNotes, err = s.notesStore.ListByNotebook(ctx, userID, workspaceID, notebookFilter)
	} else if domainFilter != "" {
		allNotes, err = s.notesStore.ListScoped(ctx, userID, workspaceID, domainFilter)
	} else {
		allNotes, err = s.notesStore.ListScoped(ctx, userID, workspaceID, "")
	}

	if err != nil {
		return nil, err
	}

	// Convert to our structure (simplified for graph)
	for i, note := range allNotes {
		if i >= limit {
			break
		}
		notesList = append(notesList, struct {
			ID         string
			Title      string
			Snippet    string
			Tags       string
			Domain     string
			NotebookID string
			CreatedAt  int64
			UpdatedAt  int64
		}{
			ID:         note.ID,
			Title:      note.Title,
			Snippet:    note.Snippet,
			Tags:       note.Tags,
			Domain:     note.Domain,
			NotebookID: note.NotebookID,
			CreatedAt:  note.CreatedAt,
			UpdatedAt:  note.UpdatedAt,
		})
	}

	// Build graph concurrently
	var wg sync.WaitGroup
	var mu sync.Mutex

	nodes := make(map[string]GraphNode)
	edges := []GraphEdge{}
	notebookMap := make(map[string]bool)
	tagMap := make(map[string]int) // tag -> count

	// Add note nodes
	for _, note := range notesList {
		var tags []string
		if note.Tags != "" {
			_ = json.Unmarshal([]byte(note.Tags), &tags)
		}

		nodes[note.ID] = GraphNode{
			ID:    note.ID,
			Label: truncateLabel(note.Title, 50),
			Type:  "note",
			Size:  0, // Will be calculated from edges
			Data: &NodeData{
				Domain:    note.Domain,
				Tags:      tags,
				CreatedAt: formatTimestamp(note.CreatedAt),
				UpdatedAt: formatTimestamp(note.UpdatedAt),
				Snippet:   truncateLabel(note.Snippet, 100),
			},
		}

		// Track notebooks
		if note.NotebookID != "" {
			notebookMap[note.NotebookID] = true
		}

		// Track tags
		for _, tag := range tags {
			tagMap[tag]++
		}
	}

	// Fetch note links
	if s.noteLinksStore != nil {
		wg.Add(1)
		background.Go("knowledge-graph-fetch-links", func() {
			defer wg.Done()
			noteIDs := make([]string, 0, len(notesList))
			for _, n := range notesList {
				noteIDs = append(noteIDs, n.ID)
			}

			for _, noteID := range noteIDs {
				outgoing, incoming, err := s.noteLinksStore.GetRelatedNotes(noteID)
				if err != nil {
					continue
				}

				mu.Lock()
				// Add outgoing links
				for _, link := range outgoing {
					if _, exists := nodes[link.TargetNoteID]; exists {
						edges = append(edges, GraphEdge{
							Source: link.SourceNoteID,
							Target: link.TargetNoteID,
							Type:   "link",
							Weight: link.Confidence,
						})
					}
				}

				// Add incoming links (backlinks)
				for _, link := range incoming {
					if _, exists := nodes[link.SourceNoteID]; exists {
						edges = append(edges, GraphEdge{
							Source: link.SourceNoteID,
							Target: noteID,
							Type:   "reference",
							Weight: 1.0,
						})
					}
				}
				mu.Unlock()
			}
		})
	}

	wg.Wait()

	// Add notebook nodes
	if s.notebookStore != nil {
		for notebookID := range notebookMap {
			nb, err := s.notebookStore.GetByID(notebookID)
			if err == nil && nb != nil {
				nodes[notebookID] = GraphNode{
					ID:    notebookID,
					Label: nb.Name,
					Type:  "notebook",
					Size:  0,
				}

				// Add notebook edges
				for _, note := range notesList {
					if note.NotebookID == notebookID {
						edges = append(edges, GraphEdge{
							Source: note.ID,
							Target: notebookID,
							Type:   "notebook",
							Weight: 1.0,
						})
					}
				}
			}
		}
	}

	// Add tag nodes (only tags with > 1 note)
	for tag, count := range tagMap {
		if count > 1 {
			tagID := "tag:" + tag
			nodes[tagID] = GraphNode{
				ID:    tagID,
				Label: tag,
				Type:  "tag",
				Size:  count,
			}

			// Add tag edges
			for _, note := range notesList {
				var tags []string
				if note.Tags != "" {
					_ = json.Unmarshal([]byte(note.Tags), &tags)
				}
				for _, t := range tags {
					if t == tag {
						edges = append(edges, GraphEdge{
							Source: note.ID,
							Target: tagID,
							Type:   "tag",
							Weight: 0.5,
						})
					}
				}
			}
		}
	}

	// Calculate node sizes (degree)
	nodeSizes := make(map[string]int)
	for _, edge := range edges {
		nodeSizes[edge.Source]++
		nodeSizes[edge.Target]++
	}

	// Update node sizes
	nodesSlice := make([]GraphNode, 0, len(nodes))
	for id, node := range nodes {
		node.Size = nodeSizes[id]
		if node.Size == 0 {
			node.Size = 1 // Minimum size for isolated nodes
		}
		nodesSlice = append(nodesSlice, node)
	}

	return &GraphData{
		Nodes: nodesSlice,
		Edges: edges,
	}, nil
}

// buildLocalGraph constructs a localized graph around a specific note.
func (s *Server) buildLocalGraph(ctx context.Context, userID, workspaceID, noteID string) (*GraphData, error) {
	if s.notesStore == nil || s.noteLinksStore == nil {
		return &GraphData{Nodes: []GraphNode{}, Edges: []GraphEdge{}}, nil
	}

	nodes := make(map[string]GraphNode)
	edges := []GraphEdge{}

	// Get the central note
	centralNote, err := s.notesStore.GetByIDScoped(ctx, noteID, userID, workspaceID)
	if err != nil || centralNote == nil {
		return nil, err
	}

	var centralTags []string
	if centralNote.Tags != "" {
		_ = json.Unmarshal([]byte(centralNote.Tags), &centralTags)
	}

	// Add central note
	nodes[noteID] = GraphNode{
		ID:    noteID,
		Label: truncateLabel(centralNote.Title, 50),
		Type:  "note",
		Size:  5, // Central node is larger
		Data: &NodeData{
			Domain:    centralNote.Domain,
			Tags:      centralTags,
			CreatedAt: formatTimestamp(centralNote.CreatedAt),
			Snippet:   truncateLabel(centralNote.Snippet, 100),
		},
	}

	// Get linked notes
	outgoing, incoming, err := s.noteLinksStore.GetRelatedNotes(noteID)
	if err == nil {
		// Add outgoing links
		for _, link := range outgoing {
			targetNote, err := s.notesStore.GetByIDScoped(ctx, link.TargetNoteID, userID, workspaceID)
			if err == nil && targetNote != nil {
				var tags []string
				if targetNote.Tags != "" {
					_ = json.Unmarshal([]byte(targetNote.Tags), &tags)
				}

				nodes[link.TargetNoteID] = GraphNode{
					ID:    link.TargetNoteID,
					Label: truncateLabel(targetNote.Title, 50),
					Type:  "note",
					Size:  3,
					Data: &NodeData{
						Tags:    tags,
						Snippet: truncateLabel(targetNote.Snippet, 100),
					},
				}

				edges = append(edges, GraphEdge{
					Source: noteID,
					Target: link.TargetNoteID,
					Type:   "link",
					Weight: link.Confidence,
				})
			}
		}

		// Add backlinks
		for _, link := range incoming {
			sourceNote, err := s.notesStore.GetByIDScoped(ctx, link.SourceNoteID, userID, workspaceID)
			if err == nil && sourceNote != nil {
				var tags []string
				if sourceNote.Tags != "" {
					_ = json.Unmarshal([]byte(sourceNote.Tags), &tags)
				}

				nodes[link.SourceNoteID] = GraphNode{
					ID:    link.SourceNoteID,
					Label: truncateLabel(sourceNote.Title, 50),
					Type:  "note",
					Size:  3,
					Data: &NodeData{
						Tags:    tags,
						Snippet: truncateLabel(sourceNote.Snippet, 100),
					},
				}

				edges = append(edges, GraphEdge{
					Source: link.SourceNoteID,
					Target: noteID,
					Type:   "reference",
					Weight: 1.0,
				})
			}
		}
	}

	// Add notebook node if present
	if centralNote.NotebookID != "" && s.notebookStore != nil {
		nb, err := s.notebookStore.GetByID(centralNote.NotebookID)
		if err == nil && nb != nil {
			nodes[nb.ID] = GraphNode{
				ID:    nb.ID,
				Label: nb.Name,
				Type:  "notebook",
				Size:  4,
			}

			edges = append(edges, GraphEdge{
				Source: noteID,
				Target: nb.ID,
				Type:   "notebook",
				Weight: 1.0,
			})
		}
	}

	// Add tag nodes
	for _, tag := range centralTags {
		tagID := "tag:" + tag
		nodes[tagID] = GraphNode{
			ID:    tagID,
			Label: tag,
			Type:  "tag",
			Size:  2,
		}

		edges = append(edges, GraphEdge{
			Source: noteID,
			Target: tagID,
			Type:   "tag",
			Weight: 0.5,
		})
	}

	// Convert nodes map to slice
	nodesSlice := make([]GraphNode, 0, len(nodes))
	for _, node := range nodes {
		nodesSlice = append(nodesSlice, node)
	}

	return &GraphData{
		Nodes: nodesSlice,
		Edges: edges,
	}, nil
}

// Helper functions

func truncateLabel(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func formatTimestamp(ts int64) string {
	if ts == 0 {
		return ""
	}
	return time.Unix(ts, 0).Format(time.RFC3339)
}

func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func parseIntSafe(s string) (int, error) {
	return strconv.Atoi(s)
}
