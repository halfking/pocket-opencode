package server

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestOIDCResponseBodyBudget(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		fail bool
	}{
		{"valid", `{"sub":"fixture-user"}`, false},
		{"malformed", `{"sub":`, true},
		{"oversized valid prefix", `{"sub":"fixture-user"}` + strings.Repeat(" ", maxOIDCResponseBytes), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := &http.Response{Body: io.NopCloser(strings.NewReader(tc.body))}
			var result oidcUserInfo
			err := decodeOIDCResponse(resp, &result)
			if (err != nil) != tc.fail {
				t.Fatalf("decodeOIDCResponse error = %v, want failure %v", err, tc.fail)
			}
			if !tc.fail && result.Sub != "fixture-user" {
				t.Fatalf("decoded sub = %q", result.Sub)
			}
		})
	}
}
