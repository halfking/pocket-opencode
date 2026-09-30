package task

// Work-item classification tests. The enum is the user-visible contract for
// "work and tasks are one thing, only the type differs", so the validation has
// to be a closed set: an unchecked free-text field is what made the previous
// `category` a no-op between the frontend and the backend.

import "testing"

func TestValidTypeAcceptsEveryDeclaredType(t *testing.T) {
	for _, typ := range AllTypes() {
		if !ValidType(typ) {
			t.Errorf("ValidType(%q) = false, want true", typ)
		}
		if TypeGroup(typ) == "" {
			t.Errorf("TypeGroup(%q) = \"\", want a group", typ)
		}
	}
}

func TestValidTypeRejectsUnknownValues(t *testing.T) {
	for _, typ := range []string{"", " ", "unknown", "DEV", "work", "学习", "dev ", "dev2"} {
		if ValidType(typ) {
			t.Errorf("ValidType(%q) = true, want false", typ)
		}
	}
}

func TestTypeGroupMapping(t *testing.T) {
	cases := map[string]string{
		TypeDev: GroupWork, TypeOps: GroupWork, TypeProject: GroupWork,
		TypeMeeting: GroupWork, TypeDoc: GroupWork, TypeComms: GroupWork,
		TypeAdmin:  GroupWork,
		TypeErrand: GroupLife, TypeFamily: GroupLife, TypeFinance: GroupLife, TypeHealth: GroupLife,
		TypeStudy: GroupLearning, TypeResearch: GroupLearning, TypeReview: GroupLearning,
		TypeOther: GroupOther,
	}
	for typ, want := range cases {
		if got := TypeGroup(typ); got != want {
			t.Errorf("TypeGroup(%q) = %q, want %q", typ, got, want)
		}
	}
	if got := TypeGroup("nope"); got != "" {
		t.Errorf("TypeGroup(nope) = %q, want \"\"", got)
	}
}

// AllTypes drives the UI picker, so it must be deterministic across calls —
// otherwise the order of the type chips changes on every render.
func TestAllTypesIsStableAndGrouped(t *testing.T) {
	first := AllTypes()
	second := AllTypes()
	if len(first) != len(second) {
		t.Fatalf("AllTypes() length changed between calls: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("AllTypes() is not stable at %d: %q vs %q", i, first[i], second[i])
		}
	}
	if first[0] != TypeAdmin {
		t.Errorf("AllTypes()[0] = %q, want %q (work group first, sorted)", first[0], TypeAdmin)
	}
	if first[len(first)-1] != TypeOther {
		t.Errorf("AllTypes() last = %q, want %q", first[len(first)-1], TypeOther)
	}
	// Every group must appear before the next one starts.
	lastRank := -1
	for _, typ := range first {
		rank := groupRank(TypeGroup(typ))
		if rank < lastRank {
			t.Fatalf("group order regressed at %q", typ)
		}
		lastRank = rank
	}
	// No duplicates: the map and the list must not disagree.
	seen := map[string]bool{}
	for _, typ := range first {
		if seen[typ] {
			t.Errorf("AllTypes() contains %q twice", typ)
		}
		seen[typ] = true
	}
	if len(seen) != len(typeGroups) {
		t.Errorf("AllTypes() has %d entries, the map has %d", len(seen), len(typeGroups))
	}
}

func TestValidVisibility(t *testing.T) {
	for _, v := range []string{VisibilityPrivate, VisibilityShared, VisibilityWorkspace} {
		if !ValidVisibility(v) {
			t.Errorf("ValidVisibility(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"", "public", "Private", "team"} {
		if ValidVisibility(v) {
			t.Errorf("ValidVisibility(%q) = true, want false", v)
		}
	}
}

func TestValidOriginKind(t *testing.T) {
	for _, k := range []string{"", OriginNote, OriginEmail, OriginRSS, OriginMeeting, OriginAgent, OriginManual, OriginImport} {
		if !ValidOriginKind(k) {
			t.Errorf("ValidOriginKind(%q) = false, want true", k)
		}
	}
	for _, k := range []string{"sms", "Note", "task"} {
		if ValidOriginKind(k) {
			t.Errorf("ValidOriginKind(%q) = true, want false", k)
		}
	}
}

func TestValidParticipantRole(t *testing.T) {
	for _, r := range []string{RoleOwner, RoleAssignee, RoleWatcher} {
		if !ValidParticipantRole(r) {
			t.Errorf("ValidParticipantRole(%q) = false, want true", r)
		}
	}
	for _, r := range []string{"", "admin", "Owner"} {
		if ValidParticipantRole(r) {
			t.Errorf("ValidParticipantRole(%q) = true, want false", r)
		}
	}
}

func TestValidEventType(t *testing.T) {
	for _, e := range []string{EventCreated, EventAssigned, EventStatusChanged, EventDueChanged, EventComment, EventCompleted, EventReminded} {
		if !ValidEventType(e) {
			t.Errorf("ValidEventType(%q) = false, want true", e)
		}
	}
	for _, e := range []string{"", "deleted", "Created"} {
		if ValidEventType(e) {
			t.Errorf("ValidEventType(%q) = true, want false", e)
		}
	}
}

// normalizeWorkItem is the single write-path defaulting rule: an old client that
// sends no classification must still produce a renderable row, not a zero-value
// type that the UI would show as a blank chip.
func TestNormalizeWorkItemFillsDefaults(t *testing.T) {
	tk := &Task{}
	normalizeWorkItem(tk)
	if tk.Type != TypeOther {
		t.Errorf("Type = %q, want %q", tk.Type, TypeOther)
	}
	if tk.TypeGroup != GroupOther {
		t.Errorf("TypeGroup = %q, want %q", tk.TypeGroup, GroupOther)
	}
	if tk.Visibility != VisibilityPrivate {
		t.Errorf("Visibility = %q, want %q", tk.Visibility, VisibilityPrivate)
	}
	if tk.Assignees == nil || len(tk.Assignees) != 0 {
		t.Errorf("Assignees = %v, want an empty non-nil slice", tk.Assignees)
	}
	if tk.Tags == nil || len(tk.Tags) != 0 {
		t.Errorf("Tags = %v, want an empty non-nil slice", tk.Tags)
	}
}

func TestNormalizeWorkItemKeepsExplicitValues(t *testing.T) {
	tk := &Task{Type: TypeStudy, Visibility: VisibilityShared, Tags: []string{"p0"}}
	normalizeWorkItem(tk)
	if tk.Type != TypeStudy || tk.TypeGroup != GroupLearning || tk.Visibility != VisibilityShared {
		t.Errorf("normalizeWorkItem overwrote explicit values: %+v", tk)
	}
	if len(tk.Tags) != 1 || tk.Tags[0] != "p0" {
		t.Errorf("Tags = %v, want [p0]", tk.Tags)
	}
}

func TestEncodeDecodeStringListRoundTrip(t *testing.T) {
	if got := encodeStringList(nil); string(got) != "[]" {
		t.Errorf("encodeStringList(nil) = %s, want []", got)
	}
	raw := encodeStringList([]string{"u1", "u2"})
	if got := decodeStringList(raw); len(got) != 2 || got[0] != "u1" || got[1] != "u2" {
		t.Errorf("round trip = %v, want [u1 u2]", got)
	}
	// A corrupt or NULL column must not break a whole list page.
	if got := decodeStringList(nil); got == nil || len(got) != 0 {
		t.Errorf("decodeStringList(nil) = %v, want an empty non-nil slice", got)
	}
	if got := decodeStringList([]byte("{not json")); got == nil || len(got) != 0 {
		t.Errorf("decodeStringList(garbage) = %v, want an empty non-nil slice", got)
	}
}
