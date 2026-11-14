package wikilinks

import (
	"reflect"
	"testing"
)

func TestParseWikiLinks(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []WikiLink
	}{
		{
			name:    "no wiki links",
			content: "Just plain text without any links",
			want:    nil,
		},
		{
			name:    "simple wiki link",
			content: "See [[Q3 Budget]] for details",
			want: []WikiLink{
				{
					Raw:         "[[Q3 Budget]]",
					Target:      "Q3 Budget",
					DisplayText: "",
					Start:       4,
					End:         18,
				},
			},
		},
		{
			name:    "wiki link with display text",
			content: "Read the [[Budget Process|approval process]] documentation",
			want: []WikiLink{
				{
					Raw:         "[[Budget Process|approval process]]",
					Target:      "Budget Process",
					DisplayText: "approval process",
					Start:       9,
					End:         44,
				},
			},
		},
		{
			name:    "multiple wiki links",
			content: "See [[Q3 Budget]] and [[Q2 Report]] for comparison",
			want: []WikiLink{
				{
					Raw:         "[[Q3 Budget]]",
					Target:      "Q3 Budget",
					DisplayText: "",
					Start:       4,
					End:         18,
				},
				{
					Raw:         "[[Q2 Report]]",
					Target:      "Q2 Report",
					DisplayText: "",
					Start:       22,
					End:         36,
				},
			},
		},
		{
			name:    "mixed wiki links with and without display text",
			content: "Check [[Budget]] and [[Process|approval flow]]",
			want: []WikiLink{
				{
					Raw:         "[[Budget]]",
					Target:      "Budget",
					DisplayText: "",
					Start:       6,
					End:         16,
				},
				{
					Raw:         "[[Process|approval flow]]",
					Target:      "Process",
					DisplayText: "approval flow",
					Start:       21,
					End:         46,
				},
			},
		},
		{
			name:    "wiki links with Chinese characters",
			content: "参考[[预算审批流程]]和[[技术方案|方案文档]]",
			want: []WikiLink{
				{
					Raw:         "[[预算审批流程]]",
					Target:      "预算审批流程",
					DisplayText: "",
					Start:       6,
					End:         21,
				},
				{
					Raw:         "[[技术方案|方案文档]]",
					Target:      "技术方案",
					DisplayText: "方案文档",
					Start:       24,
					End:         41,
				},
			},
		},
		{
			name:    "wiki link with extra spaces",
			content: "See [[ Budget Process | approval ]] here",
			want: []WikiLink{
				{
					Raw:         "[[ Budget Process | approval ]]",
					Target:      "Budget Process",
					DisplayText: "approval",
					Start:       4,
					End:         35,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseWikiLinks(tt.content)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseWikiLinks() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestExtractTargets(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string
	}{
		{
			name:    "no links",
			content: "Plain text",
			want:    nil,
		},
		{
			name:    "single link",
			content: "See [[Budget]]",
			want:    []string{"Budget"},
		},
		{
			name:    "duplicate links",
			content: "See [[Budget]] and [[Budget]] again",
			want:    []string{"Budget"},
		},
		{
			name:    "multiple unique links",
			content: "[[Budget]], [[Process]], and [[Report]]",
			want:    []string{"Budget", "Process", "Report"},
		},
		{
			name:    "links with display text",
			content: "[[Budget|Q3]] and [[Process|flow]]",
			want:    []string{"Budget", "Process"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractTargets(tt.content)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ExtractTargets() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestReplaceWikiLinks(t *testing.T) {
	tests := []struct {
		name    string
		content string
		noteMap map[string]string
		want    string
	}{
		{
			name:    "no links",
			content: "Plain text",
			noteMap: map[string]string{},
			want:    "Plain text",
		},
		{
			name:    "link to existing note",
			content: "See [[Budget]] for details",
			noteMap: map[string]string{"budget": "note-123"},
			want:    "See [Budget](/#/notes/note-123) for details",
		},
		{
			name:    "link to non-existent note",
			content: "See [[NonExistent]] for details",
			noteMap: map[string]string{},
			want:    "See [[NonExistent]] for details",
		},
		{
			name:    "link with display text to existing note",
			content: "Read [[Budget Process|the process]]",
			noteMap: map[string]string{"budget process": "note-456"},
			want:    "Read [the process](/#/notes/note-456)",
		},
		{
			name:    "multiple links mixed existing and non-existing",
			content: "See [[Budget]] and [[Unknown]]",
			noteMap: map[string]string{"budget": "note-123"},
			want:    "See [Budget](/#/notes/note-123) and [[Unknown]]",
		},
		{
			name:    "case insensitive matching",
			content: "[[BUDGET]] and [[BuDgEt]]",
			noteMap: map[string]string{"budget": "note-123"},
			want:    "[BUDGET](/#/notes/note-123) and [BuDgEt](/#/notes/note-123)",
		},
		{
			name:    "Chinese links",
			content: "参考[[预算流程]]",
			noteMap: map[string]string{"预算流程": "note-789"},
			want:    "参考[预算流程](/#/notes/note-789)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ReplaceWikiLinks(tt.content, tt.noteMap)
			if got != tt.want {
				t.Errorf("ReplaceWikiLinks() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNormalizeTitle(t *testing.T) {
	tests := []struct {
		name  string
		title string
		want  string
	}{
		{
			name:  "lowercase",
			title: "budget",
			want:  "budget",
		},
		{
			name:  "uppercase",
			title: "BUDGET",
			want:  "budget",
		},
		{
			name:  "mixed case",
			title: "BuDgEt",
			want:  "budget",
		},
		{
			name:  "with spaces",
			title: "  Budget Process  ",
			want:  "budget process",
		},
		{
			name:  "Chinese characters",
			title: "预算流程",
			want:  "预算流程",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeTitle(tt.title)
			if got != tt.want {
				t.Errorf("NormalizeTitle() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildNoteMap(t *testing.T) {
	notes := []struct{ Title, ID string }{
		{Title: "Q3 Budget", ID: "note-123"},
		{Title: "Budget Process", ID: "note-456"},
		{Title: "REPORT", ID: "note-789"},
		{Title: "", ID: "note-empty"}, // Should be ignored
	}

	got := BuildNoteMap(notes)
	want := map[string]string{
		"q3 budget":       "note-123",
		"budget process":  "note-456",
		"report":          "note-789",
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("BuildNoteMap() = %v, want %v", got, want)
	}
}
