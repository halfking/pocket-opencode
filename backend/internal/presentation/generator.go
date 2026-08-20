// internal/presentation/generator.go
package presentation

import (
	"fmt"
	"strings"
	"time"
)

// Generator generates business proposals and presentations
type Generator struct{}

// Generate creates a presentation based on the provided request
func (g *Generator) Generate(req GenerateRequest) (*GenerateResponse, error) {
	// Validate type
	switch req.Type {
	case TypePRD, TypeTechSpec, TypeWeekly:
		// valid
	default:
		return nil, fmt.Errorf("generate presentation: unsupported type %q (must be prd, tech-spec, or weekly)", req.Type)
	}

	// Validate required fields
	if req.Topic == "" {
		return nil, fmt.Errorf("generate presentation: topic is required")
	}

	// Validate input lengths to prevent DoS
	if len(req.Topic) > MaxTopicLength {
		return nil, fmt.Errorf("generate presentation: topic exceeds maximum length of %d characters", MaxTopicLength)
	}
	if len(req.Context) > MaxContextLength {
		return nil, fmt.Errorf("generate presentation: context exceeds maximum length of %d characters", MaxContextLength)
	}
	if len(req.Audience) > MaxAudienceLength {
		return nil, fmt.Errorf("generate presentation: audience exceeds maximum length of %d characters", MaxAudienceLength)
	}
	if len(req.KeyPoints) > MaxKeyPointsLength {
		return nil, fmt.Errorf("generate presentation: key_points exceeds maximum length of %d characters", MaxKeyPointsLength)
	}

	// Generate content
	content := g.generateContent(req)

	// Generate slides
	slides := g.generateSlides(req, content)

	p := &Presentation{
		ID:        fmt.Sprintf("pres_%d", time.Now().UnixNano()),
		Title:     req.Topic,
		Type:      req.Type,
		Content:   content,
		Slides:    slides,
		Status:    StatusDraft,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	return &GenerateResponse{
		Presentation: p,
		RawContent:   content,
	}, nil
}

// generateContent creates markdown content from the request
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

// generateSlides converts markdown content into slides
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