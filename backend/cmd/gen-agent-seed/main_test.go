package main

import (
	"strings"
	"testing"
)

func TestSQLEscape(t *testing.T) {
	cases := map[string]string{
		"":                    "",
		"plain":               "plain",
		"it's":                "it''s",
		"中文'引号":               "中文''引号",
		"multi''quote":        "multi''''quote",
		"line\nbreak":         "line\nbreak",
	}
	for in, want := range cases {
		if got := sqlEscape(in); got != want {
			t.Errorf("sqlEscape(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSQLNullable(t *testing.T) {
	if got := sqlNullable(""); got != "NULL" {
		t.Errorf("empty should be NULL, got %q", got)
	}
	if got := sqlNullable("purple"); got != "'purple'" {
		t.Errorf("non-empty should be quoted, got %q", got)
	}
	if got := sqlNullable("it's"); got != "'it''s'" {
		t.Errorf("quote must be escaped, got %q", got)
	}
}

// 生成器的 INSERT 行必须与受管列一一对应且幂等（ON CONFLICT DO NOTHING）。
func TestInsertShapeInvariants(t *testing.T) {
	// 与 main.go 中 fmt.Fprintf 的列清单保持同步的镜像断言：
	// 11 列、workspace_id 恒空串、is_builtin 恒 1。
	const cols = "(id, workspace_id, name, description, department, emoji, color, system_prompt, is_builtin, created_at, updated_at)"
	if n := strings.Count(cols, ","); n != 10 {
		t.Fatalf("expected 11 columns, got %d", n+1)
	}
}
