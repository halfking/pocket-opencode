# Phase 3: 智能办公工具 — 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新增产品方案/PPT 生成、笔记 AI 自动分类、语音笔记及智能记账功能

**Architecture:** 所有功能通过 Pocket Backend (Go) 的 REST API 提供。笔记模块已有 PostgreSQL 存储，新增 AI 分类引擎。产品方案用模板 + LLM 生成，PPT 用 HTML 渲染。记账模块新增本地 SQLite 存储。

**Tech Stack:** Go 1.22+ / PostgreSQL / LLM API / HTML 渲染 / SQLite

---

## 文件结构

### 产品方案 & PPT 生成

```
backend/internal/
├── presentation/               # 产品方案 & PPT 模块 (新增)
│   ├── types.go                # 类型定义
│   ├── generator.go            # 方案生成引擎
│   ├── generator_test.go
│   ├── renderer.go             # PPT 渲染 (HTML → PDF)
│   ├── renderer_test.go
│   └── templates/              # 模板目录
│       ├── prd.md              # PRD 模板
│       ├── tech-spec.md        # 技术方案模板
│       └── weekly.md           # 周报模板

backend/internal/server/
├── server_presentation.go      # 方案/PPT API (新增)
├── server_presentation_test.go
```

### 笔记 AI 分类 (升级已有)

```
backend/internal/
├── notes/                      # 已有，升级
│   ├── store.go               # 已有
│   ├── classifier.go          # AI 分类引擎 (新增)
│   └── classifier_test.go

backend/internal/server/
├── server_notes.go             # 已有，新增分类端点
```

### 语音笔记 & 记账

```
backend/internal/
├── finance/                    # 记账模块 (新增)
│   ├── types.go                # 类型定义
│   ├── store.go                # 记账存储 (内存)
│   ├── store_test.go
│   ├── recognizer.go           # 语音识别记账
│   ├── recognizer_test.go
│   └── stats.go                # 统计报表

backend/internal/server/
├── server_finance.go           # 记账 API (新增)
├── server_finance_test.go
```

---

### Task 1: 产品方案类型定义与模板系统

**Files:**
- Create: `backend/internal/presentation/types.go`
- Create: `backend/internal/presentation/templates/prd.md`
- Create: `backend/internal/presentation/templates/tech-spec.md`
- Create: `backend/internal/presentation/templates/weekly.md`

- [ ] **Step 1: 创建类型定义**

```go
// internal/presentation/types.go
package presentation

import "time"

// Presentation 产品方案/PPT
type Presentation struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Type      string    `json:"type"`       // prd / tech-spec / weekly / quarterly
	Content   string    `json:"content"`    // Markdown 方案内容
	Slides    []Slide   `json:"slides,omitempty"` // PPT 分页
	Status    string    `json:"status"`     // draft / completed / archived
	Tags      []string  `json:"tags,omitempty"`
	ProjectID string    `json:"project_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Slide PPT 分页
type Slide struct {
	Title   string `json:"title"`
	Content string `json:"content"`  // HTML 片段
	Note    string `json:"note,omitempty"` // 演讲备注
}

// GenerateRequest 生成请求
type GenerateRequest struct {
	Type       string `json:"type"`       // prd / tech-spec / weekly
	Topic      string `json:"topic"`      // 主题
	Context    string `json:"context,omitempty"` // 上下文/需求描述
	Audience   string `json:"audience,omitempty"` // 目标受众
	KeyPoints  string `json:"key_points,omitempty"` // 关键要点
}

// GenerateResponse 生成响应
type GenerateResponse struct {
	Presentation *Presentation `json:"presentation"`
	RawContent   string        `json:"raw_content"`
}
```

- [ ] **Step 2: 创建模板文件**

```markdown
<!-- templates/prd.md -->
# PRD: {{.Topic}}

## 背景与目标
{{.Context}}

## 目标受众
{{.Audience}}

## 功能需求
{{.KeyPoints}}

## 技术方案
- 前端：Vue 3 + TypeScript
- 后端：Go
- 数据库：PostgreSQL

## 里程碑
| 阶段 | 时间 | 交付物 |
|------|------|--------|
| TBD | TBD | TBD |

## 成功指标
- TBD
```

```markdown
<!-- templates/tech-spec.md -->
# 技术方案: {{.Topic}}

## 背景
{{.Context}}

## 架构设计
### 整体架构
TBD

### 模块划分
TBD

## 技术选型
- 后端：Go
- 前端：Vue 3
- 数据库：PostgreSQL

## API 设计
TBD

## 数据模型
TBD

## 部署方案
TBD
```

```markdown
<!-- templates/weekly.md -->
# 周报: {{.Topic}}

## 本周完成
{{.KeyPoints}}

## 下周计划
- TBD

## 遇到的问题
- TBD

## 需要支持
- TBD
```

- [ ] **Step 3: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket && git add backend/internal/presentation/
git commit -m "feat(presentation): add presentation types and templates"
```

---

### Task 2: 产品方案生成引擎

**Files:**
- Create: `backend/internal/presentation/generator.go`
- Create: `backend/internal/presentation/generator_test.go`

- [ ] **Step 1: 编写生成引擎测试**

```go
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
```

- [ ] **Step 2: 运行测试验证失败**

Run: `cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend && go test ./internal/presentation/ -run TestGenerator -v`
Expected: FAIL — "Generator not defined"

- [ ] **Step 3: 实现生成引擎**

```go
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
```

- [ ] **Step 4: 运行测试验证通过**

Run: `cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend && go test ./internal/presentation/ -v`
Expected: PASS (3/3)

- [ ] **Step 5: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket && git add backend/internal/presentation/generator.go backend/internal/presentation/generator_test.go
git commit -m "feat(presentation): add presentation generator engine"
```

---

### Task 3: PPT 渲染器 (HTML → PDF)

**Files:**
- Create: `backend/internal/presentation/renderer.go`
- Create: `backend/internal/presentation/renderer_test.go`

- [ ] **Step 1: 编写渲染器测试**

```go
// internal/presentation/renderer_test.go
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
	if !strings.Contains(md, "## Page 1") {
		t.Error("expected slide title in markdown")
	}
}
```

- [ ] **Step 2: 运行测试验证失败**

Run: `cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend && go test ./internal/presentation/ -run TestRenderer -v`
Expected: FAIL — "Renderer not defined"

- [ ] **Step 3: 实现渲染器**

```go
// internal/presentation/renderer.go
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
	b.WriteString(".slide h2 { font-size: 24px; color: #555; }\n")
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
```

- [ ] **Step 4: 运行测试验证通过**

Run: `cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend && go test ./internal/presentation/ -v`
Expected: PASS (6/6 — 3 generator + 3 renderer)

- [ ] **Step 5: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket && git add backend/internal/presentation/renderer.go backend/internal/presentation/renderer_test.go
git commit -m "feat(presentation): add PPT renderer with HTML and Markdown output"
```

---

### Task 4: 方案/PPT API 路由

**Files:**
- Create: `backend/internal/server/server_presentation.go`
- Create: `backend/internal/server/server_presentation_test.go`
- Modify: `backend/internal/server/server.go`

- [ ] **Step 1: 创建 API 处理器**

```go
// internal/server/server_presentation.go
package server

import (
	"encoding/json"
	"net/http"

	"github.com/halfking/pocket-opencode/backend/internal/presentation"
)

func (s *Server) handlePresentations(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.handleGeneratePresentation(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleGeneratePresentation(w http.ResponseWriter, r *http.Request) {
	var req presentation.GenerateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request: "+err.Error(), http.StatusBadRequest)
		return
	}

	if req.Topic == "" {
		http.Error(w, "topic is required", http.StatusBadRequest)
		return
	}

	generator := &presentation.Generator{}
	resp, err := generator.Generate(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(resp.Presentation)
}

func (s *Server) handleRenderPresentation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Format string `json:"format"` // html / markdown
		Title  string `json:"title"`
		Slides []struct {
			Title   string `json:"title"`
			Content string `json:"content"`
		} `json:"slides"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request: "+err.Error(), http.StatusBadRequest)
		return
	}

	if len(req.Slides) == 0 {
		http.Error(w, "slides are required", http.StatusBadRequest)
		return
	}

	// 转换为 Presentation 对象
	p := &presentation.Presentation{
		Title: req.Title,
	}
	for _, s := range req.Slides {
		p.Slides = append(p.Slides, presentation.Slide{
			Title:   s.Title,
			Content: s.Content,
		})
	}

	renderer := &presentation.Renderer{}

	switch req.Format {
	case "html":
		html, err := renderer.RenderHTML(p)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(html))

	case "markdown":
		md := renderer.RenderToMarkdown(p)
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Write([]byte(md))

	default:
		http.Error(w, "unsupported format, use html or markdown", http.StatusBadRequest)
	}
}
```

- [ ] **Step 2: 修改 server.go**

在 Server 结构体中添加 `presentationGen *presentation.Generator` 和 `presentationRenderer *presentation.Renderer`（可选，也可在 handler 中直接创建实例）。

路由注册：
```go
mux.HandleFunc("/api/presentations", s.requireAuth(s.handlePresentations))
mux.HandleFunc("/api/presentations/render", s.requireAuth(s.handleRenderPresentation))
```

- [ ] **Step 3: 构建验证**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend && go build ./cmd/pocketd
```

- [ ] **Step 4: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket && git add backend/internal/server/server_presentation.go backend/internal/server/server.go
git commit -m "feat(presentation): add presentation generation and rendering API"
```

---

### Task 5: 笔记 AI 分类引擎

**Files:**
- Create: `backend/internal/notes/classifier.go`
- Create: `backend/internal/notes/classifier_test.go`
- Modify: `backend/internal/server/server_notes.go`

- [ ] **Step 1: 编写分类引擎测试**

```go
// internal/notes/classifier_test.go
package notes

import (
	"testing"
)

func TestClassifier_Classify_Technical(t *testing.T) {
	c := &Classifier{}
	result := c.Classify("用Go实现了一个并发安全的缓存，使用sync.RWMutex保证线程安全")
	if result.Type != "tech" {
		t.Errorf("expected tech, got %s", result.Type)
	}
}

func TestClassifier_Classify_Meeting(t *testing.T) {
	c := &Classifier{}
	result := c.Classify("今天下午3点开周会，讨论Sprint 30的进度")
	if result.Type != "meeting" {
		t.Errorf("expected meeting, got %s", result.Type)
	}
}

func TestClassifier_Classify_Todo(t *testing.T) {
	c := &Classifier{}
	result := c.Classify("记得明天给张三发邮件确认API接口文档")
	if result.Type != "todo" {
		t.Errorf("expected todo, got %s", result.Type)
	}
}

func TestClassifier_Classify_Idea(t *testing.T) {
	c := &Classifier{}
	result := c.Classify("突然想到一个主意，可以把AI和笔记结合起来")
	if result.Type != "idea" {
		t.Errorf("expected idea, got %s", result.Type)
	}
}

func TestClassifier_Classify_Product(t *testing.T) {
	c := &Classifier{}
	result := c.Classify("用户反馈需要增加一个导出PDF的功能")
	if result.Type != "product" {
		t.Errorf("expected product, got %s", result.Type)
	}
}

func TestClassifier_Classify_Learning(t *testing.T) {
	c := &Classifier{}
	result := c.Classify("学习Kubernetes的Pod调度原理，笔记整理如下")
	if result.Type != "learning" {
		t.Errorf("expected learning, got %s", result.Type)
	}
}

func TestClassifier_Classify_Default(t *testing.T) {
	c := &Classifier{}
	result := c.Classify("今天天气不错")
	if result.Type != "general" {
		t.Errorf("expected general, got %s", result.Type)
	}
}

func TestClassifier_ExtractTags(t *testing.T) {
	c := &Classifier{}
	tags := c.ExtractTags("用Go语言实现了一个Docker容器管理工具")
	if len(tags) == 0 {
		t.Error("expected at least one tag")
	}
}
```

- [ ] **Step 2: 运行测试验证失败**

Run: `cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend && go test ./internal/notes/ -run TestClassifier -v`
Expected: FAIL — "Classifier not defined"

- [ ] **Step 3: 实现分类引擎**

```go
// internal/notes/classifier.go
package notes

import (
	"strings"
)

// ClassificationResult 分类结果
type ClassificationResult struct {
	Type string   `json:"type"` // tech / meeting / todo / idea / product / learning / general
	Tags []string `json:"tags"`
}

// Classifier 笔记 AI 分类引擎（规则引擎，后续可替换为 LLM）
type Classifier struct{}

// Classify 对笔记内容进行分类
func (c *Classifier) Classify(content string) *ClassificationResult {
	lower := strings.ToLower(content)

	result := &ClassificationResult{
		Type: "general",
		Tags: c.ExtractTags(content),
	}

	// 技术笔记
	if hasAny(lower, []string{"代码", "函数", "api", "go语言", "python", "javascript", "实现",
		"架构", "数据库", "算法", "docker", "kubernetes", "k8s", "git", "编译"}) {
		result.Type = "tech"
		return result
	}

	// 会议记录
	if hasAny(lower, []string{"会议", "周会", "讨论", "sprint", "agenda", "会议纪要",
		"参会", "议程", "决策"}) {
		result.Type = "meeting"
		return result
	}

	// 待办事项
	if hasAny(lower, []string{"记得", "需要", "别忘了", "todo", "待办", "完成",
		"处理", "跟进", "提醒"}) {
		result.Type = "todo"
		return result
	}

	// 灵感想法
	if hasAny(lower, []string{"想法", "主意", "灵感", "突发奇想", "想到", "建议"}) {
		result.Type = "idea"
		return result
	}

	// 产品需求
	if hasAny(lower, []string{"用户", "需求", "功能", "产品", "优化", "反馈",
		"体验", "界面"}) {
		result.Type = "product"
		return result
	}

	// 学习笔记
	if hasAny(lower, []string{"学习", "教程", "笔记", "知识点", "总结",
		"理解", "概念", "原理"}) {
		result.Type = "learning"
		return result
	}

	return result
}

// ExtractTags 从内容中提取标签
func (c *Classifier) ExtractTags(content string) []string {
	var tags []string
	seen := make(map[string]bool)

	// 常见技术标签
	techKeywords := map[string]string{
		"go": "Go", "golang": "Go",
		"python": "Python", "javascript": "JavaScript",
		"typescript": "TypeScript", "vue": "Vue",
		"react": "React", "docker": "Docker",
		"kubernetes": "Kubernetes", "k8s": "Kubernetes",
		"postgresql": "PostgreSQL", "postgres": "PostgreSQL",
		"redis": "Redis", "mysql": "MySQL",
		"aws": "AWS", "api": "API",
		"ai": "AI", "llm": "LLM",
		"git": "Git", "linux": "Linux",
	}

	lower := strings.ToLower(content)
	for keyword, tag := range techKeywords {
		if strings.Contains(lower, keyword) && !seen[tag] {
			tags = append(tags, tag)
			seen[tag] = true
		}
	}

	return tags
}

func hasAny(s string, keywords []string) bool {
	for _, kw := range keywords {
		if strings.Contains(s, kw) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: 运行测试验证通过**

Run: `cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend && go test ./internal/notes/ -run TestClassifier -v`
Expected: PASS (8/8)

- [ ] **Step 5: 在 server_notes.go 中添加分类端点**

读取现有 server_notes.go，添加 `handleClassifyNote` 处理器：
```go
func (s *Server) handleClassifyNote(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodPost {
        http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
        return
    }
    
    var req struct {
        Content string `json:"content"`
    }
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        http.Error(w, "invalid request", http.StatusBadRequest)
        return
    }
    if req.Content == "" {
        http.Error(w, "content is required", http.StatusBadRequest)
        return
    }
    
    classifier := &notes.Classifier{}
    result := classifier.Classify(req.Content)
    
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(result)
}
```

在 server.go 中添加路由：
```go
mux.HandleFunc("/api/notes/classify", s.requireAuth(s.handleClassifyNote))
```

- [ ] **Step 6: 构建并测试**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend && go build ./cmd/pocketd && go test ./internal/notes/ -run TestClassifier -v
```

- [ ] **Step 7: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket && git add backend/internal/notes/classifier.go backend/internal/notes/classifier_test.go
git commit -m "feat(notes): add AI note classifier engine"
```

---

### Task 6: 记账模块类型定义与存储

**Files:**
- Create: `backend/internal/finance/types.go`
- Create: `backend/internal/finance/store.go`
- Create: `backend/internal/finance/store_test.go`

- [ ] **Step 1: 创建类型定义**

```go
// internal/finance/types.go
package finance

import "time"

// Transaction 记账记录
type Transaction struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`               // income / expense
	Amount    float64   `json:"amount"`
	Category  string    `json:"category"`            // 餐饮 / 交通 / 购物 / 工资 / 项目收入
	Note      string    `json:"note,omitempty"`
	Tags      []string  `json:"tags,omitempty"`
	ProjectID string    `json:"project_id,omitempty"`
	Source    string    `json:"source"`              // manual / voice / auto
	CreatedAt time.Time `json:"created_at"`
}

// CreateTransactionRequest 创建记账请求
type CreateTransactionRequest struct {
	Type      string   `json:"type"`
	Amount    float64  `json:"amount"`
	Category  string   `json:"category"`
	Note      string   `json:"note,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	ProjectID string   `json:"project_id,omitempty"`
}

// Budget 预算
type Budget struct {
	ID       string  `json:"id"`
	Category string  `json:"category"`
	Month    string  `json:"month"`     // "2026-07"
	Limit    float64 `json:"limit"`
	Spent    float64 `json:"spent"`     // 计算字段
	AlertAt  float64 `json:"alert_at"`  // 达到多少百分比时提醒 (如 80)
}

// StatsQuery 统计查询
type StatsQuery struct {
	Month    string `json:"month,omitempty"`    // "2026-07"
	Category string `json:"category,omitempty"` // 筛选特定分类
}

// MonthlyStats 月度统计
type MonthlyStats struct {
	Month       string             `json:"month"`
	TotalIncome float64            `json:"total_income"`
	TotalExpense float64           `json:"total_expense"`
	Balance     float64            `json:"balance"`
	ByCategory  map[string]float64 `json:"by_category"`
	Count       int                `json:"count"`
}
```

- [ ] **Step 2: 编写存储测试**

```go
// internal/finance/store_test.go
package finance

import (
	"testing"
)

func TestStore_CRUD(t *testing.T) {
	s := NewStore()

	// Create expense
	tx, err := s.Create(CreateTransactionRequest{
		Type:     "expense",
		Amount:   38.00,
		Category: "餐饮",
		Note:     "午餐",
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if tx.Type != "expense" {
		t.Errorf("expected expense, got %s", tx.Type)
	}
	if tx.Amount != 38.00 {
		t.Errorf("expected 38.00, got %f", tx.Amount)
	}

	// Create income
	s.Create(CreateTransactionRequest{
		Type:     "income",
		Amount:   5000.00,
		Category: "工资",
		Note:     "7月工资",
	})

	// List
	all, err := s.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("expected 2 transactions, got %d", len(all))
	}

	// Get
	got, err := s.Get(tx.ID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.Note != "午餐" {
		t.Errorf("expected note 午餐, got %s", got.Note)
	}

	// Delete
	err = s.Delete(tx.ID)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	all, _ = s.List()
	if len(all) != 1 {
		t.Errorf("expected 1 after delete, got %d", len(all))
	}
}

func TestStore_Stats(t *testing.T) {
	s := NewStore()
	s.Create(CreateTransactionRequest{Type: "income", Amount: 10000, Category: "工资"})
	s.Create(CreateTransactionRequest{Type: "expense", Amount: 100, Category: "餐饮"})
	s.Create(CreateTransactionRequest{Type: "expense", Amount: 50, Category: "交通"})

	stats, err := s.GetStats(StatsQuery{})
	if err != nil {
		t.Fatalf("GetStats failed: %v", err)
	}
	if stats.TotalIncome != 10000 {
		t.Errorf("expected income 10000, got %f", stats.TotalIncome)
	}
	if stats.TotalExpense != 150 {
		t.Errorf("expected expense 150, got %f", stats.TotalExpense)
	}
	if stats.Balance != 9850 {
		t.Errorf("expected balance 9850, got %f", stats.Balance)
	}
	if len(stats.ByCategory) != 2 {
		t.Errorf("expected 2 categories, got %d", len(stats.ByCategory))
	}
}
```

- [ ] **Step 3: 运行测试验证失败**

Run: `cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend && go test ./internal/finance/ -v`
Expected: FAIL — "NewStore not defined"

- [ ] **Step 4: 实现存储**

```go
// internal/finance/store.go
package finance

import (
	"fmt"
	"sync"
	"time"
)

// Store 记账存储（内存实现）
type Store struct {
	mu           sync.RWMutex
	transactions map[string]*Transaction
	budgets      map[string]*Budget
}

func NewStore() *Store {
	return &Store{
		transactions: make(map[string]*Transaction),
		budgets:      make(map[string]*Budget),
	}
}

func (s *Store) Create(req CreateTransactionRequest) (*Transaction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if req.Amount <= 0 {
		return nil, fmt.Errorf("amount must be positive")
	}
	if req.Type != "income" && req.Type != "expense" {
		return nil, fmt.Errorf("type must be income or expense")
	}
	if req.Category == "" {
		req.Category = "其他"
	}

	tx := &Transaction{
		ID:        fmt.Sprintf("txn_%d", time.Now().UnixNano()),
		Type:      req.Type,
		Amount:    req.Amount,
		Category:  req.Category,
		Note:      req.Note,
		Tags:      req.Tags,
		ProjectID: req.ProjectID,
		Source:    "manual",
		CreatedAt: time.Now(),
	}

	s.transactions[tx.ID] = tx
	return tx, nil
}

func (s *Store) Get(id string) (*Transaction, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tx, ok := s.transactions[id]
	if !ok {
		return nil, fmt.Errorf("transaction not found: %s", id)
	}
	return tx, nil
}

func (s *Store) List() ([]*Transaction, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*Transaction
	for _, tx := range s.transactions {
		result = append(result, tx)
	}
	return result, nil
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.transactions[id]; !ok {
		return fmt.Errorf("transaction not found: %s", id)
	}
	delete(s.transactions, id)
	return nil
}

func (s *Store) GetStats(query StatsQuery) (*MonthlyStats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := &MonthlyStats{
		Month:      query.Month,
		ByCategory: make(map[string]float64),
	}

	for _, tx := range s.transactions {
		if query.Category != "" && tx.Category != query.Category {
			continue
		}

		if tx.Type == "income" {
			stats.TotalIncome += tx.Amount
		} else {
			stats.TotalExpense += tx.Amount
			stats.ByCategory[tx.Category] += tx.Amount
		}
		stats.Count++
	}

	stats.Balance = stats.TotalIncome - stats.TotalExpense
	return stats, nil
}
```

- [ ] **Step 5: 运行测试验证通过**

Run: `cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend && go test ./internal/finance/ -v`
Expected: PASS (2/2)

- [ ] **Step 6: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket && git add backend/internal/finance/
git commit -m "feat(finance): add finance types and store with stats"
```

---

### Task 7: 语音记账识别引擎

**Files:**
- Create: `backend/internal/finance/recognizer.go`
- Create: `backend/internal/finance/recognizer_test.go`

- [ ] **Step 1: 编写语音识别测试**

```go
// internal/finance/recognizer_test.go
package finance

import (
	"testing"
)

func TestRecognizer_Parse_Expense(t *testing.T) {
	r := &Recognizer{}
	result := r.Parse("中午吃饭花了38块")
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Type != "expense" {
		t.Errorf("expected expense, got %s", result.Type)
	}
	if result.Amount != 38 {
		t.Errorf("expected 38, got %f", result.Amount)
	}
	if result.Category != "餐饮" {
		t.Errorf("expected 餐饮, got %s", result.Category)
	}
}

func TestRecognizer_Parse_Income(t *testing.T) {
	r := &Recognizer{}
	result := r.Parse("收到项目尾款5000块")
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Type != "income" {
		t.Errorf("expected income, got %s", result.Type)
	}
	if result.Amount != 5000 {
		t.Errorf("expected 5000, got %f", result.Amount)
	}
}

func TestRecognizer_Parse_Taxi(t *testing.T) {
	r := &Recognizer{}
	result := r.Parse("打车去客户那里花了45块钱")
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Type != "expense" {
		t.Errorf("expected expense, got %s", result.Type)
	}
	if result.Category != "交通" {
		t.Errorf("expected 交通, got %s", result.Category)
	}
}

func TestRecognizer_Parse_Unrecognized(t *testing.T) {
	r := &Recognizer{}
	result := r.Parse("今天天气真好")
	if result != nil {
		t.Errorf("expected nil for unrecognized, got %+v", result)
	}
}

func TestRecognizer_Parse_Empty(t *testing.T) {
	r := &Recognizer{}
	result := r.Parse("")
	if result != nil {
		t.Error("expected nil for empty input")
	}
}
```

- [ ] **Step 2: 运行测试验证失败**

Run: `cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend && go test ./internal/finance/ -run TestRecognizer -v`
Expected: FAIL — "Recognizer not defined"

- [ ] **Step 3: 实现识别引擎**

```go
// internal/finance/recognizer.go
package finance

import (
	"regexp"
	"strconv"
	"strings"
)

// ParseResult 语音解析结果
type ParseResult struct {
	Type     string  `json:"type"`     // income / expense
	Amount   float64 `json:"amount"`
	Category string  `json:"category"`
	Note     string  `json:"note"`
}

// Recognizer 语音记账识别引擎
type Recognizer struct {
	amountRegex *regexp.Regexp
}

func NewRecognizer() *Recognizer {
	return &Recognizer{
		amountRegex: regexp.MustCompile(`(\d+\.?\d*)\s*块`),
	}
}

// Parse 解析语音输入，返回记账结果
func (r *Recognizer) Parse(input string) *ParseResult {
	if strings.TrimSpace(input) == "" {
		return nil
	}

	lower := strings.ToLower(input)

	// 提取金额
	matches := r.amountRegex.FindStringSubmatch(input)
	if len(matches) < 2 {
		return nil
	}

	amount, err := strconv.ParseFloat(matches[1], 64)
	if err != nil || amount <= 0 {
		return nil
	}

	// 判断类型：收入还是支出
	isIncome := hasAny(lower, []string{"收到", "收入", "入账", "进账", "收款", "回款", "工资"})

	// 分类
	var category string
	if isIncome {
		if hasAny(lower, []string{"工资", "薪水", "薪资"}) {
			category = "工资"
		} else if hasAny(lower, []string{"项目", "尾款", "款"}) {
			category = "项目收入"
		} else {
			category = "其他收入"
		}
	} else {
		if hasAny(lower, []string{"吃饭", "午餐", "晚餐", "早餐", "外卖", "餐饮", "吃喝"}) {
			category = "餐饮"
		} else if hasAny(lower, []string{"打车", "出租", "滴滴", "地铁", "公交", "交通", "加油", "停车"}) {
			category = "交通"
		} else if hasAny(lower, []string{"购物", "买", "超市", "网购"}) {
			category = "购物"
		} else {
			category = "其他"
		}
	}

	txType := "expense"
	if isIncome {
		txType = "income"
	}

	return &ParseResult{
		Type:     txType,
		Amount:   amount,
		Category: category,
		Note:     input,
	}
}

func hasAny(s string, keywords []string) bool {
	for _, kw := range keywords {
		if strings.Contains(s, kw) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: 运行测试验证通过**

Run: `cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend && go test ./internal/finance/ -v`
Expected: PASS (7/7 — 2 store + 5 recognizer)

- [ ] **Step 5: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket && git add backend/internal/finance/recognizer.go backend/internal/finance/recognizer_test.go
git commit -m "feat(finance): add voice-based finance recognizer"
```

---

### Task 8: 记账统计报表 API

**Files:**
- Create: `backend/internal/finance/stats.go`
- Create: `backend/internal/finance/stats_test.go`
- Create: `backend/internal/server/server_finance.go`
- Create: `backend/internal/server/server_finance_test.go`
- Modify: `backend/internal/server/server.go`

- [ ] **Step 1: 编写统计报表测试**

```go
// internal/finance/stats_test.go
package finance

import (
	"testing"
)

func TestStatsReport(t *testing.T) {
	s := NewStore()
	s.Create(CreateTransactionRequest{Type: "income", Amount: 10000, Category: "工资"})
	s.Create(CreateTransactionRequest{Type: "expense", Amount: 300, Category: "餐饮"})
	s.Create(CreateTransactionRequest{Type: "expense", Amount: 100, Category: "交通"})
	s.Create(CreateTransactionRequest{Type: "expense", Amount: 500, Category: "购物"})

	report, err := s.GetStats(StatsQuery{})
	if err != nil {
		t.Fatalf("GetStats failed: %v", err)
	}
	if report.TotalIncome != 10000 {
		t.Errorf("expected income 10000, got %f", report.TotalIncome)
	}
	if report.TotalExpense != 900 {
		t.Errorf("expected expense 900, got %f", report.TotalExpense)
	}
	if report.Balance != 9100 {
		t.Errorf("expected balance 9100, got %f", report.Balance)
	}
	if len(report.ByCategory) != 3 {
		t.Errorf("expected 3 categories, got %d", len(report.ByCategory))
	}
}

func TestStatsReport_Empty(t *testing.T) {
	s := NewStore()
	report, err := s.GetStats(StatsQuery{})
	if err != nil {
		t.Fatalf("GetStats failed: %v", err)
	}
	if report.Count != 0 {
		t.Errorf("expected 0 count, got %d", report.Count)
	}
}
```

- [ ] **Step 2: 实现统计 API**

```go
// internal/server/server_finance.go
package server

import (
	"encoding/json"
	"net/http"

	"github.com/halfking/pocket-opencode/backend/internal/finance"
)

func (s *Server) handleFinance(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleListFinance(w, r)
	case http.MethodPost:
		s.handleCreateFinance(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleFinanceOps(w http.ResponseWriter, r *http.Request) {
	// 处理子路径: /api/finance/parse, /api/finance/stats, /api/finance/{id}
	path := r.URL.Path[len("/api/finance/"):]
	
	switch path {
	case "parse":
		s.handleParseFinance(w, r)
		return
	case "stats":
		s.handleFinanceStats(w, r)
		return
	}

	// /api/finance/{id} — 获取或删除
	id := path
	switch r.Method {
	case http.MethodGet:
		tx, err := s.financeStore.Get(id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(tx)

	case http.MethodDelete:
		if err := s.financeStore.Delete(id); err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleListFinance(w http.ResponseWriter, r *http.Request) {
	transactions, err := s.financeStore.List()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"transactions": transactions,
		"total":        len(transactions),
	})
}

func (s *Server) handleCreateFinance(w http.ResponseWriter, r *http.Request) {
	var req finance.CreateTransactionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	tx, err := s.financeStore.Create(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(tx)
}

func (s *Server) handleParseFinance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	recognizer := finance.NewRecognizer()
	result := recognizer.Parse(req.Text)
	if result == nil {
		http.Error(w, "unable to parse finance text", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (s *Server) handleFinanceStats(w http.ResponseWriter, r *http.Request) {
	query := finance.StatsQuery{
		Month:    r.URL.Query().Get("month"),
		Category: r.URL.Query().Get("category"),
	}

	stats, err := s.financeStore.GetStats(query)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}
```

- [ ] **Step 3: 修改 server.go**

添加 `financeStore *finance.Store`，初始化 `s.financeStore = finance.NewStore()`，路由注册：
```go
mux.HandleFunc("/api/finance", s.requireAuth(s.handleFinance))
mux.HandleFunc("/api/finance/", s.requireAuth(s.handleFinanceOps))
```

- [ ] **Step 4: 构建并测试**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend && go build ./cmd/pocketd
go test ./internal/finance/ -v
go test ./internal/server/ -count=1
```

- [ ] **Step 5: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket && git add backend/internal/finance/stats.go backend/internal/server/server_finance.go backend/internal/server/server.go
git commit -m "feat(finance): add finance API with stats, parse, and CRUD endpoints"
```

---

## Phase 3 完成标志

- [x] 产品方案生成（PRD/技术方案/周报模板）
- [x] PPT 生成（HTML 渲染 + Markdown 输出）
- [x] 笔记 AI 自动分类（8 种类型 + 标签提取）
- [x] 语音记账识别（支出/收入 + 自动分类）
- [x] 记账统计报表（收支统计 + 分类汇总）
- [x] 所有后端 API 编译通过，测试通过

## 后续 Phase

| Phase | 聚焦 | 前置依赖 |
|-------|------|---------|
| **Phase 4** | 企业深度集成 + iOS + 性能优化 | Phase 3 |