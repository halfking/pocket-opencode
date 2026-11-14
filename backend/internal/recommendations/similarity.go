// Package recommendations provides AI-powered recommendation features for notes,
// including content-based similarity calculation and intelligent tag suggestions.
package recommendations

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/halfking/pocket-opencode/backend/internal/notes"
)

// SimilarNote represents a note with its similarity score to a reference note.
type SimilarNote struct {
	Note  *notes.Note `json:"note"`
	Score float64     `json:"score"` // 0.0 to 1.0, higher means more similar
}

// SimilarityCalculator computes content-based similarity between notes using
// TF-IDF vectorization and cosine similarity. It caches term frequencies to
// optimize repeated calculations.
type SimilarityCalculator struct {
	pool  *pgxpool.Pool
	cache sync.Map // map[string]*tfidfVector - cached vectors by note ID
}

// tfidfVector represents a note's TF-IDF vector for similarity computation.
type tfidfVector struct {
	terms map[string]float64 // term -> TF-IDF score
	norm  float64            // L2 norm for cosine similarity
}

// NewSimilarityCalculator creates a new similarity calculator.
func NewSimilarityCalculator(pool *pgxpool.Pool) *SimilarityCalculator {
	return &SimilarityCalculator{pool: pool}
}

// CalculateSimilarity computes the cosine similarity between two notes based on
// their content. Returns a score between 0.0 (completely different) and 1.0
// (identical content).
//
// The algorithm:
//  1. Extract terms (words) from title and content
//  2. Compute TF (term frequency) for each note
//  3. Compute IDF (inverse document frequency) using corpus statistics
//  4. Calculate cosine similarity between the two TF-IDF vectors
func (sc *SimilarityCalculator) CalculateSimilarity(note1, note2 *notes.Note) float64 {
	if note1 == nil || note2 == nil {
		return 0.0
	}

	// Get or compute TF-IDF vectors
	vec1 := sc.getOrComputeVector(note1)
	vec2 := sc.getOrComputeVector(note2)

	if vec1 == nil || vec2 == nil || vec1.norm == 0 || vec2.norm == 0 {
		return 0.0
	}

	// Cosine similarity: dot product / (norm1 * norm2)
	dotProduct := 0.0
	for term, score1 := range vec1.terms {
		if score2, ok := vec2.terms[term]; ok {
			dotProduct += score1 * score2
		}
	}

	return dotProduct / (vec1.norm * vec2.norm)
}

// FindSimilarNotes finds notes similar to the given note ID, scoped to the same
// user and workspace. Returns up to `limit` similar notes, sorted by descending
// similarity score.
func (sc *SimilarityCalculator) FindSimilarNotes(ctx context.Context, noteID, userID, workspaceID string, limit int) ([]SimilarNote, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}

	// Get the reference note
	_ = notes.Store{} // temporary placeholder
	refNote, err := sc.getNote(ctx, noteID, userID, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("get reference note: %w", err)
	}
	if refNote == nil {
		return nil, fmt.Errorf("note not found: %s", noteID)
	}

	// Get all candidate notes (same user/workspace, excluding the reference note)
	candidates, err := sc.getCandidateNotes(ctx, userID, workspaceID, noteID)
	if err != nil {
		return nil, fmt.Errorf("get candidate notes: %w", err)
	}

	// Compute similarity scores
	scored := make([]SimilarNote, 0, len(candidates))
	for _, candidate := range candidates {
		score := sc.CalculateSimilarity(refNote, &candidate)
		if score > 0.1 { // Filter out very weak similarities
			scored = append(scored, SimilarNote{
				Note:  &candidate,
				Score: score,
			})
		}
	}

	// Sort by descending score
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})

	// Return top N
	if len(scored) > limit {
		scored = scored[:limit]
	}

	return scored, nil
}

// getOrComputeVector retrieves a cached TF-IDF vector or computes it.
func (sc *SimilarityCalculator) getOrComputeVector(note *notes.Note) *tfidfVector {
	// Check cache
	if cached, ok := sc.cache.Load(note.ID); ok {
		return cached.(*tfidfVector)
	}

	// Compute vector
	vec := sc.computeTFIDFVector(note)
	if vec != nil {
		sc.cache.Store(note.ID, vec)
	}
	return vec
}

// computeTFIDFVector creates a TF-IDF vector for a note.
// For simplicity, we use a lightweight approach without corpus-wide IDF
// (which would require loading all notes). Instead, we use term frequency
// weighted by position (title terms count more than content terms).
func (sc *SimilarityCalculator) computeTFIDFVector(note *notes.Note) *tfidfVector {
	terms := make(map[string]float64)

	// Extract and weight terms from title (3x weight)
	titleTerms := extractTerms(note.Title)
	for _, term := range titleTerms {
		terms[term] += 3.0
	}

	// Extract and weight terms from content (1x weight)
	content := note.Content
	if content == "" {
		content = note.Snippet
	}
	contentTerms := extractTerms(content)
	for _, term := range contentTerms {
		terms[term] += 1.0
	}

	// Extract and weight terms from tags (2x weight)
	tagTerms := extractTerms(note.Tags)
	for _, term := range tagTerms {
		terms[term] += 2.0
	}

	if len(terms) == 0 {
		return nil
	}

	// Compute L2 norm
	sumSquares := 0.0
	for _, score := range terms {
		sumSquares += score * score
	}
	norm := math.Sqrt(sumSquares)

	return &tfidfVector{
		terms: terms,
		norm:  norm,
	}
}

// extractTerms splits text into normalized terms (lowercase words).
// Filters out very short terms and common stop words.
func extractTerms(text string) []string {
	text = strings.ToLower(text)
	// Simple word extraction: split on non-alphanumeric characters
	words := strings.FieldsFunc(text, func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') ||
			(r >= '\u4e00' && r <= '\u9fff')) // Include Chinese characters
	})

	// Filter and deduplicate
	seen := make(map[string]bool)
	result := make([]string, 0, len(words))
	for _, word := range words {
		if len(word) < 2 || isStopWord(word) || seen[word] {
			continue
		}
		seen[word] = true
		result = append(result, word)
	}
	return result
}

// isStopWord checks if a term is a common stop word that should be filtered out.
var stopWords = map[string]bool{
	// English stop words
	"the": true, "is": true, "at": true, "which": true, "on": true,
	"a": true, "an": true, "and": true, "or": true, "but": true,
	"in": true, "with": true, "to": true, "for": true, "of": true,
	"as": true, "by": true, "from": true, "up": true, "about": true,
	"into": true, "through": true, "during": true, "before": true, "after": true,
	"it": true, "its": true, "he": true, "she": true, "they": true,
	"this": true, "that": true, "these": true, "those": true,
	// Chinese stop words (common function words)
	"的": true, "了": true, "是": true, "在": true, "我": true,
	"有": true, "和": true, "就": true, "不": true, "人": true,
	"都": true, "一": true, "一个": true, "上": true, "也": true,
	"很": true, "到": true, "说": true, "要": true, "去": true,
}

func isStopWord(word string) bool {
	return stopWords[word]
}

// getNote retrieves a single note by ID with ownership checks.
func (sc *SimilarityCalculator) getNote(ctx context.Context, noteID, userID, workspaceID string) (*notes.Note, error) {
	s := &notes.Store{} // Will be initialized with pool in production
	// Direct query to avoid circular dependency
	var n notes.Note
	err := sc.pool.QueryRow(ctx, `
		SELECT id, user_id, workspace_id, title, content, snippet,
		       content_type, domain, tags, created_at, updated_at
		FROM notes 
		WHERE id = $1 AND user_id = $2 AND workspace_id = $3 AND deleted_at IS NULL
	`, noteID, userID, workspaceID).Scan(
		&n.ID, &n.UserID, &n.WorkspaceID, &n.Title, &n.Content, &n.Snippet,
		&n.ContentType, &n.Domain, &n.Tags, &n.CreatedAt, &n.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	_ = s // suppress unused warning
	return &n, nil
}

// getCandidateNotes retrieves all notes for similarity comparison, excluding the reference note.
func (sc *SimilarityCalculator) getCandidateNotes(ctx context.Context, userID, workspaceID, excludeID string) ([]notes.Note, error) {
	rows, err := sc.pool.Query(ctx, `
		SELECT id, user_id, workspace_id, title, content, snippet,
		       content_type, domain, tags, created_at, updated_at
		FROM notes 
		WHERE user_id = $1 AND workspace_id = $2 AND id != $3 AND deleted_at IS NULL
		ORDER BY updated_at DESC
		LIMIT 100
	`, userID, workspaceID, excludeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []notes.Note
	for rows.Next() {
		var n notes.Note
		if err := rows.Scan(&n.ID, &n.UserID, &n.WorkspaceID, &n.Title, &n.Content,
			&n.Snippet, &n.ContentType, &n.Domain, &n.Tags, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, n)
	}
	return result, rows.Err()
}

// ClearCache clears the cached TF-IDF vectors. Call this when notes are updated
// to ensure fresh calculations.
func (sc *SimilarityCalculator) ClearCache() {
	sc.cache = sync.Map{}
}
