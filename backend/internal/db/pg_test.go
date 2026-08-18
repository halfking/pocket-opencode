package db

import "testing"

func TestIsValidIdent(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "simple", value: "opencode_pocket", want: true},
		{name: "leading underscore", value: "_private", want: true},
		{name: "digits after first", value: "schema2", want: true},
		{name: "empty", value: "", want: false},
		{name: "leading digit", value: "2schema", want: false},
		{name: "space", value: "open code", want: false},
		{name: "quote", value: `safe";DROP SCHEMA public;--`, want: false},
		{name: "hyphen", value: "opencode-pocket", want: false},
		{name: "public schema", value: "public", want: false},
		{name: "catalog schema", value: "pg_catalog", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isValidIdent(tc.value); got != tc.want {
				t.Fatalf("isValidIdent(%q) = %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}

func TestIsValidIdentRejectsOverlongNames(t *testing.T) {
	name := make([]byte, 64)
	for i := range name {
		name[i] = 'a'
	}
	if isValidIdent(string(name)) {
		t.Fatal("schema names longer than PostgreSQL's identifier limit must be rejected")
	}
}

func TestQuoteIdent(t *testing.T) {
	if got := quoteIdent("opencode_pocket"); got != `"opencode_pocket"` {
		t.Fatalf("quoteIdent(simple) = %q", got)
	}
	if got := quoteIdent(`tenant"schema`); got != `"tenant""schema"` {
		t.Fatalf("quoteIdent(embedded quote) = %q", got)
	}
}
