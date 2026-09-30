package task

// Work-item classification. Work and tasks are one entity (docs/学习muse/
// 03-架构方案.md §1): the only difference is Type. The list is a closed set on
// purpose — an unchecked free-text category is what made the previous
// `category` field a no-op between the frontend and the backend.
//
// Group is derived, never stored: it exists so the UI can fold the list into
// four buckets without persisting a redundant column.
const (
	// work group
	TypeDev     = "dev"
	TypeOps     = "ops"
	TypeProject = "project"
	TypeMeeting = "meeting"
	TypeDoc     = "doc"
	TypeComms   = "comms"
	TypeAdmin   = "admin"
	// life group
	TypeErrand  = "errand"
	TypeFamily  = "family"
	TypeFinance = "finance"
	TypeHealth  = "health"
	// learning group
	TypeStudy    = "study"
	TypeResearch = "research"
	TypeReview   = "review"
	// fallback
	TypeOther = "other"
)

// Group names returned by TypeGroup.
const (
	GroupWork     = "work"
	GroupLife     = "life"
	GroupLearning = "learning"
	GroupOther    = "other"
)

// typeGroups is the single source of truth for type -> group. Anything missing
// from this map is rejected by ValidType, so the map and the type list can
// never drift apart.
var typeGroups = map[string]string{
	TypeDev: GroupWork, TypeOps: GroupWork, TypeProject: GroupWork,
	TypeMeeting: GroupWork, TypeDoc: GroupWork, TypeComms: GroupWork,
	TypeAdmin: GroupWork,

	TypeErrand: GroupLife, TypeFamily: GroupLife,
	TypeFinance: GroupLife, TypeHealth: GroupLife,

	TypeStudy: GroupLearning, TypeResearch: GroupLearning, TypeReview: GroupLearning,

	TypeOther: GroupOther,
}

// TypeGroup returns the fold group for a type, or "" when the type is unknown.
// Callers that accept untrusted input should validate with ValidType first.
func TypeGroup(t string) string { return typeGroups[t] }

// ValidType reports whether t is an accepted classification. The empty string
// is invalid on purpose: callers normalise it to TypeOther before validation.
func ValidType(t string) bool {
	_, ok := typeGroups[t]
	return ok
}

// AllTypes returns every accepted type, sorted by group then name. The UI uses
// it to build the type picker; the server uses it for validation messages.
func AllTypes() []string {
	groups := []string{GroupWork, GroupLife, GroupLearning, GroupOther}
	out := make([]string, 0, len(typeGroups))
	for _, g := range groups {
		for t, tg := range typeGroups {
			if tg == g {
				out = append(out, t)
			}
		}
	}
	// Map iteration is random; sort inside each group for a stable picker order.
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			gi, gj := groupRank(typeGroups[out[i]]), groupRank(typeGroups[out[j]])
			if gj < gi || (gj == gi && out[j] < out[i]) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func groupRank(g string) int {
	switch g {
	case GroupWork:
		return 0
	case GroupLife:
		return 1
	case GroupLearning:
		return 2
	default:
		return 3
	}
}

// Task visibility, i.e. who may read a work item beyond its author.
const (
	VisibilityPrivate   = "private"   // participants only
	VisibilityShared    = "shared"    // participants + workspace members may read
	VisibilityWorkspace = "workspace" // all workspace members
)

// ValidVisibility reports whether v is an accepted visibility level.
func ValidVisibility(v string) bool {
	switch v {
	case VisibilityPrivate, VisibilityShared, VisibilityWorkspace:
		return true
	default:
		return false
	}
}

// Origin kinds recorded on a work item so the UI can link back to the source
// entity (note / email / rss / meeting) it was captured from.
const (
	OriginNote    = "note"
	OriginEmail   = "email"
	OriginRSS     = "rss"
	OriginMeeting = "meeting"
	OriginAgent   = "agent"
	OriginManual  = "manual"
	OriginImport  = "import"
)

// ValidOriginKind reports whether k is an accepted origin kind. The empty
// string is valid and means "created directly in the work list".
func ValidOriginKind(k string) bool {
	switch k {
	case "", OriginNote, OriginEmail, OriginRSS, OriginMeeting, OriginAgent, OriginManual, OriginImport:
		return true
	default:
		return false
	}
}
