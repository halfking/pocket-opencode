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

// TestResolveMaxConns 钉住连接数上限的判定（2026-10-28 §976）。
//
// ⚠ 这条判据对着**旧实现**（`if cfg.MaxConns == 0 { cfg.MaxConns = 10 }`）必红：
// 旧实现里 4 仍然是 4（因为 ParseConfig 之后它永远不是 0）⇒ 「2 核机器上只有
// 4 条连接」这个隐患原样留着。
func TestResolveMaxConns(t *testing.T) {
	cases := []struct {
		name    string
		parsed  int32
		dsn     string
		want    int32
		comment string
	}{
		{
			name:   "2 核机器的默认(4)被抬到 10",
			parsed: 4,
			dsn:    "postgres://u:p@127.0.0.1:5432/db?sslmode=disable",
			want:   10,
		},
		{
			name:   "多核机器的 16 原样保留(只升不降)",
			parsed: 16,
			dsn:    "postgres://u:p@127.0.0.1:5432/db?sslmode=disable",
			want:   16,
		},
		{
			name:   "DSN 显式点名 pool_max_conns ⇒ 以 DSN 为准",
			parsed: 4,
			dsn:    "postgres://u:p@127.0.0.1:5432/db?pool_max_conns=4",
			want:   4,
		},
		{
			name:   "DSN 显式点名 2 ⇒ 也不被默认值抬上去",
			parsed: 2,
			dsn:    "postgres://u:p@127.0.0.1:5432/db?pool_max_conns=2",
			want:   2,
		},
		{
			name:   "DSN 显式点名 32 ⇒ 原样",
			parsed: 32,
			dsn:    "postgres://u:p@127.0.0.1:5432/db?pool_max_conns=32",
			want:   32,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveMaxConns(tc.parsed, tc.dsn); got != tc.want {
				t.Fatalf("resolveMaxConns(%d, %q) = %d, want %d", tc.parsed, tc.dsn, got, tc.want)
			}
		})
	}
}

// TestDsnPinsMaxConns 单独钉住「用户是不是点名了 pool_max_conns」。
func TestDsnPinsMaxConns(t *testing.T) {
	cases := []struct {
		name string
		dsn  string
		want bool
	}{
		{name: "没写", dsn: "postgres://u:p@127.0.0.1:5432/db", want: false},
		{name: "只有 sslmode", dsn: "postgres://u:p@127.0.0.1:5432/db?sslmode=disable", want: false},
		{name: "写了 pool_max_conns", dsn: "postgres://u:p@127.0.0.1:5432/db?pool_max_conns=4", want: true},
		{name: "写在别的参数后面", dsn: "postgres://u:p@127.0.0.1:5432/db?sslmode=disable&pool_max_conns=8", want: true},
		{name: "DSN 根本解析不了 ⇒ 不算点名", dsn: "://not a dsn", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := dsnPinsMaxConns(tc.dsn); got != tc.want {
				t.Fatalf("dsnPinsMaxConns(%q) = %v, want %v", tc.dsn, got, tc.want)
			}
		})
	}
}
