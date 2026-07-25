// internal/presentation/generator.go
package presentation

import (
	"fmt"
	"strings"
	"time"
)

// Generator 产品方案生成引擎
type Generator struct{}

// Generate 根据请求生成方案
func (g *Generator) Generate(req GenerateRequest) (*GenerateResponse, error) {
	switch req.Type {
	case "prd", "tech-spec", "weekly":
		// valid
	default:
		return nil, fmt.Errorf("unsupported type: %s", req.Type)
	}

	if req.Topic == "" {
		return nil, fmt.Errorf("topic is required")
	}

	// 生成内容
	content := g.generateContent(req)

	// 生成幻灯片
	slides := g.generateSlides(req, content)

	p := &Presentation{
		ID:        fmt.Sprintf("pres_%d", time.Now().UnixNano()),
		Title:     req.Topic,
		Type:      req.Type,
		Content:   content,
		Slides:    slides,
		Status:    "draft",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	return &GenerateResponse{
		Presentation: p,
		RawContent:   content,
	}, nil
}

func (g *Generator) generateContent(req GenerateRequest) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("# %s\n\n", req.Topic))
	b.WriteString(fmt.Sprintf("> 类型: %s\n\n", req.Type))

	if req.Context != "" {
		b.WriteString("## 背景\n\n")
		b.WriteString(req.Context + "\n\n")
	}

	if req.Audience != "" {
		b.WriteString("## 目标受众\n\n")
		b.WriteString(req.Audience + "\n\n")
	}

	if req.KeyPoints != "" {
		b.WriteString("## 关键要点\n\n")
		b.WriteString(req.KeyPoints + "\n\n")
	}

	b.WriteString("## 下一步行动\n\n")
	b.WriteString("- 完善方案细节\n")
	b.WriteString("- 团队评审\n")
	b.WriteString("- 确定排期\n")

	return b.String()
}

func (g *Generator) generateSlides(req GenerateRequest, content string) []Slide {
	lines := strings.Split(content, "\n")

	var slides []Slide
	var currentSlide Slide

	for _, line := range lines {
		if strings.HasPrefix(line, "# ") {
			if currentSlide.Title != "" {
				slides = append(slides, currentSlide)
			}
			currentSlide = Slide{
				Title:   strings.TrimPrefix(line, "# "),
				Content: "",
			}
		} else if strings.HasPrefix(line, "## ") {
			if currentSlide.Title != "" {
				slides = append(slides, currentSlide)
			}
			currentSlide = Slide{
				Title:   strings.TrimPrefix(line, "## "),
				Content: "",
			}
		} else {
			currentSlide.Content += line + "\n"
		}
	}

	if currentSlide.Title != "" {
		slides = append(slides, currentSlide)
	}

	// 确保至少有一页
	if len(slides) == 0 {
		slides = []Slide{
			{Title: req.Topic, Content: req.Context},
		}
	}

	return slides
}