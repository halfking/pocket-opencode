// internal/presentation/generator_test.go
package presentation

import (
	"strings"
	"testing"
)

func TestGenerator_GeneratePRD(t *testing.T) {
	g := &Generator{}
	req := GenerateRequest{
		Type:    TypePRD,
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
	if resp.Presentation.Type != TypePRD {
		t.Errorf("expected type %s, got %s", TypePRD, resp.Presentation.Type)
	}
	if resp.Presentation.Title == "" {
		t.Error("expected non-empty title")
	}
	if resp.RawContent == "" {
		t.Error("expected non-empty raw content")
	}
	if resp.Presentation.Status != StatusDraft {
		t.Errorf("expected status %s, got %s", StatusDraft, resp.Presentation.Status)
	}
}

func TestGenerator_GenerateWithSlides(t *testing.T) {
	g := &Generator{}
	req := GenerateRequest{
		Type:    TypeWeekly,
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
	if !strings.Contains(err.Error(), "unsupported type") {
		t.Errorf("expected 'unsupported type' in error, got: %v", err)
	}
}

func TestGenerator_EmptyTopic(t *testing.T) {
	g := &Generator{}
	_, err := g.Generate(GenerateRequest{Type: TypePRD, Topic: ""})
	if err == nil {
		t.Error("expected error for empty topic")
	}
	if !strings.Contains(err.Error(), "topic is required") {
		t.Errorf("expected 'topic is required' in error, got: %v", err)
	}
}

func TestGenerator_TopicTooLong(t *testing.T) {
	g := &Generator{}
	longTopic := strings.Repeat("a", MaxTopicLength+1)
	_, err := g.Generate(GenerateRequest{Type: TypePRD, Topic: longTopic})
	if err == nil {
		t.Error("expected error for topic exceeding max length")
	}
	if !strings.Contains(err.Error(), "exceeds maximum length") {
		t.Errorf("expected 'exceeds maximum length' in error, got: %v", err)
	}
}

func TestGenerator_ContextTooLong(t *testing.T) {
	g := &Generator{}
	longContext := strings.Repeat("a", MaxContextLength+1)
	_, err := g.Generate(GenerateRequest{
		Type:    TypePRD,
		Topic:   "Test",
		Context: longContext,
	})
	if err == nil {
		t.Error("expected error for context exceeding max length")
	}
}

func TestGenerator_AllFieldsPopulated(t *testing.T) {
	g := &Generator{}
	req := GenerateRequest{
		Type:      TypeTechSpec,
		Topic:     "API Design",
		Context:   "Need RESTful API",
		Audience:  "Backend developers",
		KeyPoints: "Security, Performance, Scalability",
	}

	resp, err := g.Generate(req)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	content := resp.RawContent
	if !strings.Contains(content, req.Topic) {
		t.Error("expected topic in content")
	}
	if !strings.Contains(content, req.Context) {
		t.Error("expected context in content")
	}
	if !strings.Contains(content, req.Audience) {
		t.Error("expected audience in content")
	}
	if !strings.Contains(content, req.KeyPoints) {
		t.Error("expected key points in content")
	}
}

func TestGenerator_MinimalRequest(t *testing.T) {
	g := &Generator{}
	req := GenerateRequest{
		Type:  TypeWeekly,
		Topic: "Status Update",
	}

	resp, err := g.Generate(req)
	if err != nil {
		t.Fatalf("Generate failed with minimal request: %v", err)
	}
	if resp.Presentation == nil {
		t.Fatal("expected non-nil presentation")
	}
	if len(resp.Presentation.Slides) == 0 {
		t.Error("expected at least one slide even with minimal request")
	}
}