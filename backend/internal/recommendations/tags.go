package recommendations

import (
	"encoding/json"
	"sort"
	"strings"
)

// TagSuggester provides intelligent tag suggestions based on note content.
type TagSuggester struct{}

// NewTagSuggester creates a new tag suggester.
func NewTagSuggester() *TagSuggester {
	return &TagSuggester{}
}

// termScore tracks a term and its frequency score for ranking.
type termScore struct {
	term  string
	score float64
}

// SuggestTags analyzes note content and suggests relevant tags.
// Returns up to 5 suggested tags, prioritizing:
//  1. Terms that appear frequently in the content
//  2. Terms from the title (weighted higher)
//  3. Terms not already in existingTags
//
// The algorithm uses simple keyword extraction (term frequency) without
// external dependencies.
func (ts *TagSuggester) SuggestTags(content string, existingTags []string) []string {
	// Parse existing tags to avoid duplicates
	existingSet := make(map[string]bool)
	for _, tag := range existingTags {
		existingSet[strings.ToLower(strings.TrimSpace(tag))] = true
	}

	// Extract terms and count frequencies
	terms := extractTerms(content)
	termFreq := make(map[string]int)
	for _, term := range terms {
		termFreq[term]++
	}

	// Score terms (frequency-based, with bonus for longer terms)
	scored := make([]termScore, 0, len(termFreq))
	for term, freq := range termFreq {
		// Skip if already tagged
		if existingSet[term] {
			continue
		}
		// Score: frequency * length bonus (longer terms are often more specific)
		lengthBonus := 1.0
		if len(term) > 4 {
			lengthBonus = 1.2
		}
		if len(term) > 8 {
			lengthBonus = 1.5
		}
		score := float64(freq) * lengthBonus
		scored = append(scored, termScore{term: term, score: score})
	}

	// Sort by descending score
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	// Return top 5 suggestions
	suggestions := make([]string, 0, 5)
	for i := 0; i < len(scored) && i < 5; i++ {
		suggestions = append(suggestions, scored[i].term)
	}

	return suggestions
}

// SuggestTagsFromNote is a convenience method that extracts content from
// a note's title, content, and domain, then suggests tags.
func (ts *TagSuggester) SuggestTagsFromNote(title, content, domain string, existingTagsJSON string) []string {
	// Parse existing tags from JSON array
	var existingTags []string
	if existingTagsJSON != "" {
		_ = json.Unmarshal([]byte(existingTagsJSON), &existingTags)
	}

	// Combine title (weighted 3x), content, and domain into analysis text
	analysisText := strings.Repeat(title+" ", 3) + " " + content
	if domain != "" {
		analysisText += " " + domain
	}

	return ts.SuggestTags(analysisText, existingTags)
}

// ExtractKeywords extracts the most significant keywords from text.
// Returns up to `limit` keywords, sorted by relevance.
// This is similar to SuggestTags but doesn't filter by existing tags.
func (ts *TagSuggester) ExtractKeywords(text string, limit int) []string {
	if limit <= 0 {
		limit = 10
	}

	// Extract and score terms
	terms := extractTerms(text)
	termFreq := make(map[string]int)
	for _, term := range terms {
		termFreq[term]++
	}

	// Score and sort
	scored := make([]termScore, 0, len(termFreq))
	for term, freq := range termFreq {
		// Longer terms get bonus (they're usually more specific/meaningful)
		lengthBonus := 1.0 + (float64(len(term)) / 20.0)
		score := float64(freq) * lengthBonus
		scored = append(scored, termScore{term: term, score: score})
	}

	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	// Return top N
	keywords := make([]string, 0, limit)
	for i := 0; i < len(scored) && i < limit; i++ {
		keywords = append(keywords, scored[i].term)
	}

	return keywords
}

// MergeTagSuggestions combines multiple tag suggestion sources and deduplicates.
// Useful when suggesting tags from both content analysis and similar notes.
func (ts *TagSuggester) MergeTagSuggestions(suggestions ...[]string) []string {
	seen := make(map[string]bool)
	merged := make([]string, 0)

	for _, suggestionList := range suggestions {
		for _, tag := range suggestionList {
			normalized := strings.ToLower(strings.TrimSpace(tag))
			if normalized != "" && !seen[normalized] {
				seen[normalized] = true
				merged = append(merged, tag)
			}
		}
	}

	return merged
}
