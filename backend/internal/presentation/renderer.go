package presentation

import (
	"fmt"
	"html"
	"strings"
)

// Renderer PPT 渲染器
type Renderer struct{}

// RenderHTML 将 Presentation 渲染为 HTML 幻灯片
func (r *Renderer) RenderHTML(p *Presentation) (string, error) {
	if len(p.Slides) == 0 {
		return "", fmt.Errorf("no slides to render")
	}

	var b strings.Builder

	b.WriteString("<!DOCTYPE html>\n<html>\n<head>\n")
	b.WriteString("<meta charset=\"utf-8\">\n")
	b.WriteString(fmt.Sprintf("<title>%s</title>\n", html.EscapeString(p.Title)))
	b.WriteString("<style>\n")
	b.WriteString("body { font-family: sans-serif; margin: 0; padding: 0; background: #f5f5f5; }\n")
	b.WriteString(".slide { width: 900px; min-height: 500px; margin: 20px auto; padding: 40px; ")
	b.WriteString("background: white; box-shadow: 0 2px 8px rgba(0,0,0,0.1); border-radius: 8px; }\n")
	b.WriteString(".slide h1 { font-size: 32px; color: #333; }\n")
	b.WriteString(".slide p { font-size: 16px; line-height: 1.6; color: #666; }\n")
	b.WriteString("</style>\n</head>\n<body>\n")

	for _, slide := range p.Slides {
		b.WriteString("<div class=\"slide\">\n")
		b.WriteString(fmt.Sprintf("<h1>%s</h1>\n", html.EscapeString(slide.Title)))
		b.WriteString(fmt.Sprintf("<p>%s</p>\n", html.EscapeString(slide.Content)))
		b.WriteString("</div>\n")
	}

	b.WriteString("</body>\n</html>")
	return b.String(), nil
}

// RenderToMarkdown 将 Presentation 渲染为 Markdown
func (r *Renderer) RenderToMarkdown(p *Presentation) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("# %s\n\n", p.Title))

	for i, slide := range p.Slides {
		b.WriteString(fmt.Sprintf("## Slide %d: %s\n\n", i+1, slide.Title))
		b.WriteString(slide.Content + "\n\n")
		if slide.Note != "" {
			b.WriteString(fmt.Sprintf("> 备注: %s\n\n", slide.Note))
		}
		b.WriteString("---\n\n")
	}

	return b.String()
}