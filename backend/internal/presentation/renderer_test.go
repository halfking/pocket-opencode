package presentation

import (
	"strings"
	"testing"
)

func TestRenderer_RenderHTML(t *testing.T) {
	r := &Renderer{}
	p := &Presentation{
		Title: "Test",
		Slides: []Slide{
			{Title: "Page 1", Content: "Hello"},
			{Title: "Page 2", Content: "World"},
		},
	}

	html, err := r.RenderHTML(p)
	if err != nil {
		t.Fatalf("RenderHTML failed: %v", err)
	}
	if !strings.Contains(html, "Page 1") {
		t.Error("expected Page 1 in HTML")
	}
	if !strings.Contains(html, "Page 2") {
		t.Error("expected Page 2 in HTML")
	}
	if !strings.Contains(html, "<html") {
		t.Error("expected HTML tag")
	}
}

func TestRenderer_RenderHTML_EmptySlides(t *testing.T) {
	r := &Renderer{}
	_, err := r.RenderHTML(&Presentation{Title: "Empty", Slides: nil})
	if err == nil {
		t.Error("expected error for empty slides")
	}
}

func TestRenderer_RenderToMarkdown(t *testing.T) {
	r := &Renderer{}
	p := &Presentation{
		Title: "Test",
		Slides: []Slide{
			{Title: "Page 1", Content: "Content 1"},
		},
	}

	md := r.RenderToMarkdown(p)
	if !strings.Contains(md, "# Test") {
		t.Error("expected title in markdown")
	}
	if !strings.Contains(md, "## Slide 1") {
		t.Error("expected slide title in markdown")
	}
}