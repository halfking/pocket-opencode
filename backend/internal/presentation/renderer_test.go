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
	if !strings.Contains(err.Error(), "no slides") {
		t.Errorf("expected 'no slides' in error, got: %v", err)
	}
}

func TestRenderer_RenderHTML_NilPresentation(t *testing.T) {
	r := &Renderer{}
	_, err := r.RenderHTML(nil)
	if err == nil {
		t.Error("expected error for nil presentation")
	}
	if !strings.Contains(err.Error(), "cannot be nil") {
		t.Errorf("expected 'cannot be nil' in error, got: %v", err)
	}
}

func TestRenderer_RenderHTML_XSSProtection(t *testing.T) {
	r := &Renderer{}
	p := &Presentation{
		Title: "<script>alert('xss')</script>",
		Slides: []Slide{
			{
				Title:   "<img src=x onerror=alert(1)>",
				Content: "<script>alert('content')</script>\n<a href=\"javascript:alert(1)\">click</a>",
			},
		},
	}

	html, err := r.RenderHTML(p)
	if err != nil {
		t.Fatalf("RenderHTML failed: %v", err)
	}

	// Verify that dangerous HTML tags are escaped (checking for unescaped opening tags)
	// We check that raw script/img tags don't appear in executable form
	if strings.Contains(html, "<script>") || strings.Contains(html, "</script>") {
		t.Error("XSS vulnerability: unescaped script tag in output")
	}
	if strings.Contains(html, "<img src=") && !strings.Contains(html, "&lt;img src=") {
		t.Error("XSS vulnerability: unescaped img tag with src attribute")
	}
	
	// More importantly: verify that the dangerous content appears ONLY in escaped form
	// The content should be inside our template's <h1> or <p> tags as text, not as executable HTML
	if strings.Contains(html, "<h1><script>") || strings.Contains(html, "<p><script>") {
		t.Error("XSS vulnerability: script tag executable within content")
	}
	if strings.Contains(html, "src=x onerror=") && !strings.Contains(html, "src=x onerror=alert") {
		t.Error("XSS vulnerability: onerror handler could be executable")
	}

	// Verify that escaped versions are present
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Error("expected escaped script tag")
	}
	if !strings.Contains(html, "&lt;img") {
		t.Error("expected escaped img tag")
	}
	
	// Most important: verify structure is safe - user content is inside text nodes, not attributes/tags
	// The title should be: <title>ESCAPED_CONTENT</title>
	// The slide content should be: <h1>ESCAPED_CONTENT</h1> and <p>ESCAPED_CONTENT</p>
	if !strings.Contains(html, "<h1>&lt;img") {
		t.Error("expected img tag to be escaped inside h1")
	}
	if !strings.Contains(html, "<p>&lt;script&gt;") {
		t.Error("expected script tag to be escaped inside p")
	}
}

func TestRenderer_RenderHTML_NewlineHandling(t *testing.T) {
	r := &Renderer{}
	p := &Presentation{
		Title: "Test",
		Slides: []Slide{
			{Title: "Multi-line", Content: "Line 1\nLine 2\nLine 3"},
		},
	}

	html, err := r.RenderHTML(p)
	if err != nil {
		t.Fatalf("RenderHTML failed: %v", err)
	}

	// Verify newlines are converted to <br>
	if !strings.Contains(html, "<br>") {
		t.Error("expected newlines to be converted to <br> tags")
	}
	if strings.Count(html, "<br>") < 2 {
		t.Error("expected at least 2 <br> tags for 3 lines")
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

	md, err := r.RenderToMarkdown(p)
	if err != nil {
		t.Fatalf("RenderToMarkdown failed: %v", err)
	}
	if !strings.Contains(md, "# Test") {
		t.Error("expected title in markdown")
	}
	if !strings.Contains(md, "## Slide 1") {
		t.Error("expected slide title in markdown")
	}
}

func TestRenderer_RenderToMarkdown_NilPresentation(t *testing.T) {
	r := &Renderer{}
	_, err := r.RenderToMarkdown(nil)
	if err == nil {
		t.Error("expected error for nil presentation")
	}
	if !strings.Contains(err.Error(), "cannot be nil") {
		t.Errorf("expected 'cannot be nil' in error, got: %v", err)
	}
}

func TestRenderer_RenderToMarkdown_EmptySlides(t *testing.T) {
	r := &Renderer{}
	_, err := r.RenderToMarkdown(&Presentation{Title: "Empty", Slides: nil})
	if err == nil {
		t.Error("expected error for empty slides")
	}
}

func TestRenderer_RenderToMarkdown_WithNotes(t *testing.T) {
	r := &Renderer{}
	p := &Presentation{
		Title: "Test with Notes",
		Slides: []Slide{
			{
				Title:   "Slide 1",
				Content: "Main content",
				Note:    "Speaker notes here",
			},
			{
				Title:   "Slide 2",
				Content: "More content",
				Note:    "",
			},
		},
	}

	md, err := r.RenderToMarkdown(p)
	if err != nil {
		t.Fatalf("RenderToMarkdown failed: %v", err)
	}

	// Verify notes are included when present
	if !strings.Contains(md, "Speaker notes here") {
		t.Error("expected speaker notes in markdown")
	}
	if !strings.Contains(md, "> Note:") {
		t.Error("expected note marker in markdown")
	}

	// Verify separator between slides
	if !strings.Contains(md, "---") {
		t.Error("expected slide separator in markdown")
	}
}

func TestRenderer_RenderToMarkdown_MultipleSlides(t *testing.T) {
	r := &Renderer{}
	p := &Presentation{
		Title: "Multi Slide",
		Slides: []Slide{
			{Title: "Slide 1", Content: "Content 1"},
			{Title: "Slide 2", Content: "Content 2"},
			{Title: "Slide 3", Content: "Content 3"},
		},
	}

	md, err := r.RenderToMarkdown(p)
	if err != nil {
		t.Fatalf("RenderToMarkdown failed: %v", err)
	}

	// Verify all slides are present
	for i := 1; i <= 3; i++ {
		expected := "## Slide " + string(rune('0'+i))
		if !strings.Contains(md, expected) {
			t.Errorf("expected %q in markdown", expected)
		}
	}
}