package rss

import (
	"strings"
	"time"
)

type FilterResult struct {
	Matched      bool
	Relevance    float64
	MatchReasons []string
}

// Evaluate applies enabled rules as an OR set. Exclusions always win. An empty
// rule set accepts every item. Matching is case-insensitive and Unicode-aware.
func Evaluate(item Item, rules []FilterRule) FilterResult {
	if len(rules) == 0 {
		return FilterResult{Matched: true}
	}
	accepted := false
	best := FilterResult{}
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		r := evaluateRule(item, rule)
		if r.Matched && (!accepted || r.Relevance > best.Relevance) {
			best = r
			accepted = true
		}
	}
	best.Matched = accepted
	return best
}
func evaluateRule(item Item, rule FilterRule) FilterResult {
	text := strings.ToLower(strings.Join([]string{item.Title, item.Summary, item.Content, item.Author, strings.Join(item.Categories, " ")}, " "))
	reasons := []string{}
	score := 0.0
	for _, k := range rule.ExcludeKeywords {
		k = strings.ToLower(strings.TrimSpace(k))
		if k != "" && strings.Contains(text, k) {
			return FilterResult{Matched: false, MatchReasons: []string{"excluded:" + k}}
		}
	}
	if len(rule.Languages) > 0 {
		ok := false
		lang := strings.ToLower(strings.TrimSpace(item.Language))
		for _, want := range rule.Languages {
			want = strings.ToLower(strings.TrimSpace(want))
			if want != "" && (lang == want || strings.HasPrefix(lang, want+"-") || strings.HasPrefix(want, lang+"-")) {
				ok = true
				break
			}
		}
		if !ok {
			return FilterResult{Matched: false, MatchReasons: []string{"language"}}
		}
		reasons = append(reasons, "language")
		score += 0.2
	}
	when := item.PublishedAt
	if when == nil {
		when = item.UpdatedAt
	}
	if rule.Since != nil && (when == nil || when.Before(*rule.Since)) {
		return FilterResult{Matched: false, MatchReasons: []string{"before-window"}}
	}
	if rule.Until != nil && (when == nil || when.After(*rule.Until)) {
		return FilterResult{Matched: false, MatchReasons: []string{"after-window"}}
	}
	if rule.Since != nil || rule.Until != nil {
		reasons = append(reasons, "time-window")
		score += 0.2
	}
	if len(rule.IncludeKeywords) > 0 {
		found := 0
		for _, k := range rule.IncludeKeywords {
			k = strings.ToLower(strings.TrimSpace(k))
			if k != "" && strings.Contains(text, k) {
				found++
				reasons = append(reasons, "included:"+k)
			}
		}
		if found == 0 {
			return FilterResult{Matched: false, MatchReasons: []string{"missing-include"}}
		}
		score += float64(found) / float64(len(rule.IncludeKeywords))
	} else {
		score += 0.1
	}
	if rule.MinRelevance > 0 && score < rule.MinRelevance {
		return FilterResult{Matched: false, Relevance: score, MatchReasons: []string{"below-relevance"}}
	}
	return FilterResult{Matched: true, Relevance: score, MatchReasons: reasons}
}

func ApplyFilter(item Item, rules []FilterRule) (Item, bool) {
	r := Evaluate(item, rules)
	item.Relevance = r.Relevance
	item.MatchReasons = r.MatchReasons
	if !r.Matched {
		return item, false
	}
	return item, true
}
func ApplyFilters(items []Item, rules []FilterRule) []Item {
	out := make([]Item, 0, len(items))
	for _, it := range items {
		if x, ok := ApplyFilter(it, rules); ok {
			out = append(out, x)
		}
	}
	return out
}

// FilterRuleRequest is convenient for JSON/API adapters while keeping the
// domain independent of Echo or net/http.
type FilterRuleRequest struct {
	Name            string     `json:"name"`
	Enabled         *bool      `json:"enabled"`
	IncludeKeywords []string   `json:"includeKeywords"`
	ExcludeKeywords []string   `json:"excludeKeywords"`
	Languages       []string   `json:"languages"`
	Since          *time.Time `json:"since,omitempty"`
	Until          *time.Time `json:"until,omitempty"`
	MinRelevance    float64    `json:"minRelevance"`
}
