package server

import (
	"errors"
	"fmt"
	"testing"
)

func TestIsNoCandidateError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain error without json", errors.New("connection refused"), false},
		{"empty 503 with no body", errors.New("llm-gateway stream 503: "), false},
		{"503 no_candidate body", errors.New(
			`llm-gateway stream 503: {"error":{"code":"no_candidate","kind":"no_candidate","message":"No available provider for model 'claude-sonnet-4.5'","request_id":"abc","type":"server_error"}}`,
		), true},
		{"503 different error code", errors.New(
			`llm-gateway stream 503: {"error":{"code":"rate_limit","kind":"rate_limit","message":"slow down"}}`,
		), false},
		{"401 unauthorized body", errors.New(
			`llm-gateway stream 401: {"error":{"code":"unauthorized","message":"bad key"}}`,
		), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isNoCandidateError(tc.err); got != tc.want {
				t.Errorf("isNoCandidateError = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPickFallbackModel(t *testing.T) {
	cases := []struct {
		name      string
		original  string
		preferred []string
		catalog   []string
		want      string
	}{
		{"empty preferred", "auto", nil, []string{"a", "b"}, ""},
		{"first matches original, second is fallback", "auto",
			[]string{"auto-skip", "claude-sonnet-5"},
			[]string{"claude-sonnet-5", "glm-5.2"}, "claude-sonnet-5"},
		{"original matches preferred[0], skip it", "minimax-m3",
			[]string{"minimax-m3", "claude-sonnet-5", "glm-5.2"},
			[]string{"claude-sonnet-5", "glm-5.2"}, "claude-sonnet-5"},
		{"preferred not in catalog filtered out", "auto",
			[]string{"not-in-catalog-1", "claude-sonnet-5"},
			[]string{"claude-sonnet-5"}, "claude-sonnet-5"},
		{"all preferred not in catalog", "auto",
			[]string{"x", "y"},
			[]string{"a", "b"}, ""},
		{"empty catalog allows all preferred", "auto",
			[]string{"claude-sonnet-5", "glm-5.2"}, nil, "claude-sonnet-5"},
		{"skip empty entries", "auto",
			[]string{"", "claude-sonnet-5"},
			[]string{"claude-sonnet-5"}, "claude-sonnet-5"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := pickFallbackModel(tc.original, tc.preferred, tc.catalog)
			if got != tc.want {
				t.Errorf("pickFallbackModel(%q, %v, %v) = %q, want %q",
					tc.original, tc.preferred, tc.catalog, got, tc.want)
			}
		})
	}
}

// fakeNoCandidateErr 用来在其它需要 error 的场景里复用现成的 error 字符串。
func fakeNoCandidateErr() error {
	return fmt.Errorf("llm-gateway stream 503: {\"error\":{\"code\":\"no_candidate\",\"kind\":\"no_candidate\",\"message\":\"x\"}}")
}
