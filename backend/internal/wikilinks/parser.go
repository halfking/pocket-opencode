// Package wikilinks provides wiki-style bidirectional link parsing for notes.
//
// Supports the following syntaxes:
//   - [[title]] - links to a note by title
//   - [[title|display text]] - links with custom display text
//
// Reference: docs/2026-11-14-knowledge-base-integration-plan.md Phase 3.2
package wikilinks

import (
	"regexp"
	"strings"
)

// WikiLink represents a parsed wiki-style link from note content.
type WikiLink struct {
	// Raw is the complete [[...]] text including brackets
	Raw string
	// Target is the note title to link to
	Target string
	// DisplayText is the optional display text (if using [[target|display]] syntax)
	DisplayText string
	// Start is the byte offset where the link starts in the content
	Start int
	// End is the byte offset where the link ends in the content
	End int
}

// wikiLinkPattern matches [[keyword]] or [[keyword|display text]]
// Captures:
//   Group 1: full match including brackets
//   Group 2: target (before | or entire content)
//   Group 3: optional display text (after |)
var wikiLinkPattern = regexp.MustCompile(`\[\[([^\]|]+)(?:\|([^\]]+))?\]\]`)

// ParseWikiLinks extracts all wiki links from the given content.
// Returns a slice of WikiLink structs containing parsed link information.
//
// Example:
//   content := "See [[Q3 Budget]] and [[Process|approval process]]"
//   links := ParseWikiLinks(content)
//   // Returns: [{Target: "Q3 Budget"}, {Target: "Process", DisplayText: "approval process"}]
func ParseWikiLinks(content string) []WikiLink {
	matches := wikiLinkPattern.FindAllStringSubmatchIndex(content, -1)
	if len(matches) == 0 {
		return nil
	}

	links := make([]WikiLink, 0, len(matches))
	for _, match := range matches {
		// match[0] = start of full match, match[1] = end of full match
		// match[2] = start of target, match[3] = end of target
		// match[4] = start of display text (if exists), match[5] = end of display text
		
		raw := content[match[0]:match[1]]
		target := strings.TrimSpace(content[match[2]:match[3]])
		
		var displayText string
		if match[4] != -1 && match[5] != -1 {
			displayText = strings.TrimSpace(content[match[4]:match[5]])
		}

		links = append(links, WikiLink{
			Raw:         raw,
			Target:      target,
			DisplayText: displayText,
			Start:       match[0],
			End:         match[1],
		})
	}

	return links
}

// ExtractTargets returns a deduplicated list of all target note titles
// mentioned in wiki links within the content.
//
// Example:
//   content := "See [[Budget]] and [[Budget]] again"
//   targets := ExtractTargets(content)
//   // Returns: ["Budget"]
func ExtractTargets(content string) []string {
	links := ParseWikiLinks(content)
	if len(links) == 0 {
		return nil
	}

	seen := make(map[string]bool)
	targets := make([]string, 0, len(links))
	
	for _, link := range links {
		if !seen[link.Target] {
			seen[link.Target] = true
			targets = append(targets, link.Target)
		}
	}

	return targets
}

// ReplaceWikiLinks replaces wiki links in content with actual links using the noteMap.
//
// noteMap maps note titles (case-insensitive) to note IDs.
// Links to existing notes are replaced with markdown links: [display](/#/notes/{id})
// Links to non-existent notes are marked with a special class for UI rendering.
//
// Example:
//   noteMap := map[string]string{"budget": "note-123"}
//   content := "See [[Budget]] for details"
//   result := ReplaceWikiLinks(content, noteMap)
//   // Returns: "See [Budget](/#/notes/note-123) for details"
func ReplaceWikiLinks(content string, noteMap map[string]string) string {
	links := ParseWikiLinks(content)
	if len(links) == 0 {
		return content
	}

	// Process links in reverse order to maintain correct offsets
	result := content
	for i := len(links) - 1; i >= 0; i-- {
		link := links[i]
		
		// Look up note ID (case-insensitive)
		targetLower := strings.ToLower(link.Target)
		noteID, exists := noteMap[targetLower]
		
		// Determine display text
		display := link.DisplayText
		if display == "" {
			display = link.Target
		}
		
		var replacement string
		if exists {
			// Replace with clickable markdown link
			replacement = "[" + display + "](/#/notes/" + noteID + ")"
		} else {
			// Mark as unresolved for UI rendering (e.g., grayed out)
			replacement = "[[" + display + "]]" // Keep as-is, let UI handle styling
		}
		
		result = result[:link.Start] + replacement + result[link.End:]
	}

	return result
}

// NormalizeTitle normalizes a note title for matching.
// Converts to lowercase and trims whitespace for case-insensitive lookups.
func NormalizeTitle(title string) string {
	return strings.ToLower(strings.TrimSpace(title))
}

// BuildNoteMap creates a map from normalized titles to note IDs.
// This is used by ReplaceWikiLinks for efficient lookups.
//
// notes should be a slice of objects with Title and ID fields.
// The map keys are normalized (lowercase, trimmed) titles.
func BuildNoteMap(notes []struct{ Title, ID string }) map[string]string {
	noteMap := make(map[string]string, len(notes))
	for _, note := range notes {
		if note.Title != "" {
			noteMap[NormalizeTitle(note.Title)] = note.ID
		}
	}
	return noteMap
}
