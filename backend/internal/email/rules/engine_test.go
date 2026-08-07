package rules

import (
	"encoding/json"
	"strings"
	"testing"
)

func mkInput() EmailInput {
	return EmailInput{From: "alice@example.com", Subject: "Quarterly Report", Importance: "normal"}
}

func TestParseRules_Empty(t *testing.T) {
	rs, err := ParseRules("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rs) != 0 {
		t.Fatalf("expected 0 rules, got %d", len(rs))
	}
}

func TestParseRules_LegacyShape(t *testing.T) {
	raw := `{"whitelist":["boss@x.com"],"blacklist":["spam@x.com"],"keywords":["invoice"]}`
	rs, err := ParseRules(raw)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(rs) != 3 {
		t.Fatalf("expected 3 legacy rules, got %d", len(rs))
	}
	if rs[0].Type != "sender-whitelist" || rs[0].Actions[0].Name != "mark-important" {
		t.Fatalf("first rule mismatch: %+v", rs[0])
	}
	if rs[1].Type != "sender-blacklist" || rs[1].Actions[0].Name != "unsupported" {
		t.Fatalf("blacklist should map to unsupported, got %+v", rs[1])
	}
	if rs[2].Type != "subject-keyword" || rs[2].Actions[0].Name != "label-category" {
		t.Fatalf("keywords should map to label-category, got %+v", rs[2])
	}
}

func TestParseRules_NewShape(t *testing.T) {
	raw := `{"rules":[{"type":"sender-whitelist","pattern":"ceo@x.com","actions":["mark-important"]}]}`
	rs, err := ParseRules(raw)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(rs) != 1 || rs[0].Pattern != "ceo@x.com" {
		t.Fatalf("unexpected parsed rules: %+v", rs)
	}
}

func TestParseRules_InvalidJSON(t *testing.T) {
	if _, err := ParseRules("not json"); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestParseRules_NewShapeInvalid(t *testing.T) {
	if _, err := ParseRules(`{"rules": "oops"}`); err == nil {
		t.Fatal("expected error when rules is not an array")
	}
}

func TestEvaluate_WhitelistTriggersMarkImportant(t *testing.T) {
	rules, _ := ParseRules(`{"rules":[{"type":"sender-whitelist","pattern":"alice@example.com","actions":["mark-important"]}]}`)
	res := Evaluate(rules, mkInput())
	if len(res) != 1 || res[0].Action != ActionMarkImportant {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestEvaluate_BlacklistMapsToUnsupported(t *testing.T) {
	rules, _ := ParseRules(`{"rules":[{"type":"sender-blacklist","pattern":"alice@example.com","actions":["archive"]}]}`)
	res := Evaluate(rules, mkInput())
	if len(res) != 1 || res[0].Action != ActionUnsupported {
		t.Fatalf("blacklist archive should be unsupported, got %+v", res)
	}
}

func TestEvaluate_Keyword(t *testing.T) {
	rules, _ := ParseRules(`{"rules":[{"type":"subject-keyword","pattern":"report","actions":["label-category"]}]}`)
	res := Evaluate(rules, mkInput())
	if len(res) != 1 || res[0].Action != ActionLabelCategory {
		t.Fatalf("expected label-category, got %+v", res)
	}
}

func TestEvaluate_DomainMatch(t *testing.T) {
	rules, _ := ParseRules(`{"rules":[{"type":"domain-match","pattern":"example.com","actions":["label-category"]}]}`)
	res := Evaluate(rules, mkInput())
	if len(res) != 1 {
		t.Fatalf("expected 1 result, got %+v", res)
	}
}

func TestEvaluate_DomainMiss(t *testing.T) {
	rules, _ := ParseRules(`{"rules":[{"type":"domain-match","pattern":"other.com","actions":["label-category"]}]}`)
	if res := Evaluate(rules, mkInput()); len(res) != 0 {
		t.Fatalf("expected 0 results, got %+v", res)
	}
}

func TestEvaluate_ImportanceMin(t *testing.T) {
	rules, _ := ParseRules(`{"rules":[{"type":"importance-min","pattern":"high","actions":["mark-important"]}]}`)
	if res := Evaluate(rules, EmailInput{From: "x@x", Importance: "low"}); len(res) != 0 {
		t.Fatalf("low should not match high threshold, got %+v", res)
	}
	if res := Evaluate(rules, EmailInput{From: "x@x", Importance: "high"}); len(res) != 1 {
		t.Fatalf("high should match, got %+v", res)
	}
}

func TestEvaluate_DedupesAndSorts(t *testing.T) {
	rules, _ := ParseRules(`{"rules":[
		{"type":"sender-whitelist","pattern":"alice@example.com","actions":["label-category"]},
		{"type":"subject-keyword","pattern":"quarterly","actions":["label-category","mark-important"]}
	]}`)
	res := Evaluate(rules, mkInput())
	actions := make([]string, 0, len(res))
	for _, r := range res {
		actions = append(actions, string(r.Action))
	}
	got := strings.Join(actions, ",")
	want := "label-category,mark-important"
	if got != want {
		t.Fatalf("dedup+sort mismatch: got %q want %q", got, want)
	}
}

func TestEvaluate_UnknownTypeSkipped(t *testing.T) {
	rules, _ := ParseRules(`{"rules":[{"type":"future-feature","pattern":"x","actions":["mark-important"]}]}`)
	if res := Evaluate(rules, mkInput()); len(res) != 0 {
		t.Fatalf("unknown type should not match, got %+v", res)
	}
}

func TestEvaluate_UnknownActionSkipped(t *testing.T) {
	rules, _ := ParseRules(`{"rules":[{"type":"sender-whitelist","pattern":"alice@example.com","actions":["nope"]}]}`)
	if res := Evaluate(rules, mkInput()); len(res) != 0 {
		t.Fatalf("unknown action should be skipped, got %+v", res)
	}
}

func TestMatchEmail(t *testing.T) {
	cases := map[string]struct{ pattern, email string; want bool }{
		"exact":      {"alice@x.com", "alice@x.com", true},
		"wildcard":   {"*@x.com", "alice@x.com", true},
		"wildmiss":   {"*@y.com", "alice@x.com", false},
		"regex":      {`^[a-z]+@x\.com$`, "alice@x.com", true},
		"empty":      {"", "alice@x.com", false},
		"emptyaddr":  {"x@y", "", false},
	}
	for name, tc := range cases {
		if got := matchEmail(tc.pattern, tc.email); got != tc.want {
			t.Fatalf("%s: got %v want %v", name, got, tc.want)
		}
	}
}

func TestSupportedActions_Stable(t *testing.T) {
	got := SupportedActions()
	if len(got) != 2 {
		t.Fatalf("expected 2 supported actions, got %v", got)
	}
}

func TestImportanceRank(t *testing.T) {
	if ImportanceRank("high") != 3 || ImportanceRank("normal") != 2 || ImportanceRank("low") != 1 {
		t.Fatalf("ranking broken: %d/%d/%d", ImportanceRank("high"), ImportanceRank("normal"), ImportanceRank("low"))
	}
}

// 确保 ParseRules 在传入包含 "rules" 但值为非数组时返回错误，而非静默吞掉。
func TestParseRules_RejectsNonArrayRules(t *testing.T) {
	if _, err := ParseRules(`{"rules":{}}`); err == nil {
		t.Fatal("expected error when rules is not an array")
	}
}

// 规则 JSON 支持 [{"name":"label-category","category":"work"}] 形式，
// Evaluate 应把 category 透传到 ActionResult，方便调用方落库到
// emails.category 而不是被静默丢弃。
func TestEvaluate_LabelCategoryPropagatesCategory(t *testing.T) {
	raw := `{"rules":[{"type":"subject-keyword","pattern":"report","actions":[{"name":"label-category","category":"work"}]}]}`
	rs, err := ParseRules(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	res := Evaluate(rs, mkInput())
	if len(res) != 1 || res[0].Action != ActionLabelCategory || res[0].Category != "work" {
		t.Fatalf("category not propagated: %+v", res)
	}
}

// 留作占位以避免遗留未用 import。
var _ = json.Marshal
