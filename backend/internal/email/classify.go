package email

import "strings"

var categoryWhitelist = map[string]struct{}{
	"work": {}, "bill": {}, "notification": {}, "personal": {}, "marketing": {}, "spam": {},
}

var categoryAliases = map[string]string{
	"ad": "marketing", "ads": "marketing", "advertisement": "marketing", "promo": "marketing",
}

func NormalizeCategory(raw string) string {
	key := strings.ToLower(strings.TrimSpace(raw))
	if key == "" {
		return ""
	}
	if mapped, ok := categoryAliases[key]; ok {
		key = mapped
	}
	if _, ok := categoryWhitelist[key]; ok {
		return key
	}
	return "personal"
}

func NeedsClassification(category string) bool {
	return strings.TrimSpace(category) == ""
}

func CapClassifyIDs(ids []string, limit int) []string {
	if limit <= 0 {
		limit = 20
	}
	if limit > 20 {
		limit = 20
	}
	out := make([]string, 0, limit)
	for _, id := range ids {
		if id == "" {
			continue
		}
		out = append(out, id)
		if len(out) >= limit {
			break
		}
	}
	return out
}
