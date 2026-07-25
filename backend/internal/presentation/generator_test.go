// internal/presentation/generator_test.go
package presentation

import (
	"testing"
)

func TestGenerator_GeneratePRD(t *testing.T) {
	g := &Generator{}
	req := GenerateRequest{
		Type:    "prd",
		Topic:   "移动端AI编程助手",
		Context: "需要一款面向程序员的产品",
	}

	resp, err := g.Generate(req)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	if resp.Presentation == nil {
		t.Fatal("expected non-nil presentation")
	}
	if resp.Presentation.Type != "prd" {
		t.Errorf("expected type prd, got %s", resp.Presentation.Type)
	}
	if resp.Presentation.Title == "" {
		t.Error("expected non-empty title")
	}
	if resp.RawContent == "" {
		t.Error("expected non-empty raw content")
	}
}

func TestGenerator_GenerateWithSlides(t *testing.T) {
	g := &Generator{}
	req := GenerateRequest{
		Type:    "weekly",
		Topic:   "2026年7月第4周",
		Context: "项目进展顺利",
	}

	resp, err := g.Generate(req)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	if len(resp.Presentation.Slides) == 0 {
		t.Error("expected at least one slide")
	}
}

func TestGenerator_InvalidType(t *testing.T) {
	g := &Generator{}
	_, err := g.Generate(GenerateRequest{Type: "invalid", Topic: "test"})
	if err == nil {
		t.Error("expected error for invalid type")
	}
}

func TestGenerator_EmptyTopic(t *testing.T) {
	g := &Generator{}
	_, err := g.Generate(GenerateRequest{Type: "prd", Topic: ""})
	if err == nil {
		t.Error("expected error for empty topic")
	}
}