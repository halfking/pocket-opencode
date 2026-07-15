// Package kxmemory provides a Go client for the kxmemory FastAPI service.
//
// kxmemory 负责 AI 编排（笔记分类/SSOT/邮件总结），pocketd 在龙虾架构
// Phase C 后定位为无状态网关，本客户端只做 HTTP 转发，不持久化用户数据。
//
// API 契约见 docs/2026-07-02-kxmemory-api-contract.md
package kxmemory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client 是 kxmemory HTTP 客户端，所有调用无状态。
type Client struct {
	BaseURL string
	APIKey  string // 可选 Bearer token（如果 kxmemory 开启认证）
	Client  *http.Client
}

// NewClient 构造 kxmemory 客户端。baseURL 如 http://localhost:8000。
func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Client:  &http.Client{Timeout: 30 * time.Second},
	}
}

// ---- 笔记相关 ----

// ClassifyNoteRequest 对应 POST /v1/notes/classify
type ClassifyNoteRequest struct {
	Content     string   `json:"content"`
	Title       string   `json:"title,omitempty"`
	ContentType string   `json:"content_type,omitempty"` // voice / text
	Domain      string   `json:"domain,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

type ClassifyNoteResponse struct {
	Status         string          `json:"status"` // success / conflict_detected
	Classification Classification  `json:"classification"`
	SmartLinks     []SmartLink     `json:"smart_links,omitempty"`
	Todos          []ExtractedTodo `json:"todos,omitempty"`
	SSOTConflicts  []SSOTConflict  `json:"ssot_conflicts,omitempty"`
}

type Classification struct {
	Domain         string   `json:"domain"`          // work / study / life / idea
	Category       string   `json:"category"`        // meeting / plan / idea / log / ...
	Tags           []string `json:"tags"`
	SuggestedTitle string   `json:"suggested_title"`
	Confidence     float64  `json:"confidence"`
}

type SmartLink struct {
	TargetID   string  `json:"target_id"`
	LinkType   string  `json:"link_type"` // references / updates / contradicts / complements / related_to
	Confidence float64 `json:"confidence"`
}

type ExtractedTodo struct {
	Text     string `json:"text"`
	DueDate  string `json:"due_date,omitempty"`
	Priority string `json:"priority,omitempty"` // low / medium / high / urgent
}

type SSOTConflict struct {
	ExistingNoteID string `json:"existing_note_id"`
	ConflictType   string `json:"conflict_type"` // contradiction / update / duplicate
	Snippet        string `json:"snippet"`
	Confidence     float64 `json:"confidence"`
}

// ClassifyNote 调用 kxmemory 分类接口（笔记创建/更新后调用）。
func (c *Client) ClassifyNote(ctx context.Context, req ClassifyNoteRequest) (*ClassifyNoteResponse, error) {
	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/v1/notes/classify", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.Client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("kxmemory classify: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		r, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("kxmemory classify %d: %s", resp.StatusCode, string(r))
	}

	var out ClassifyNoteResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode classify response: %w", err)
	}
	return &out, nil
}

// ---- 邮件相关 ----

// ClassifyEmailsRequest 对应 POST /v1/emails/classify
type ClassifyEmailsRequest struct {
	Emails []EmailForClassification `json:"emails"`
}

type EmailForClassification struct {
	EmailID     string `json:"email_id"`
	Subject     string `json:"subject"`
	Snippet     string `json:"snippet"` // 正文前 ~500 字
	FromAddress string `json:"from_address"`
	FromName    string `json:"from_name,omitempty"`
}

type ClassifyEmailsResponse struct {
	Results []EmailClassificationResult `json:"results"`
}

type EmailClassificationResult struct {
	EmailID         string `json:"email_id"`
	Category        string `json:"category"`    // work / bill / notification / personal / marketing / spam
	Importance      string `json:"importance"`  // high / medium / low
	Summary         string `json:"summary"`
	SuggestedAction string `json:"suggested_action,omitempty"`
}

// ClassifyEmails 批量分类邮件（IMAP 抓取后调用）。
func (c *Client) ClassifyEmails(ctx context.Context, req ClassifyEmailsRequest) (*ClassifyEmailsResponse, error) {
	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/v1/emails/classify", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.Client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("kxmemory classify emails: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		r, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("kxmemory classify emails %d: %s", resp.StatusCode, string(r))
	}

	var out ClassifyEmailsResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode classify emails response: %w", err)
	}
	return &out, nil
}

// DailySummaryRequest 对应 POST /v1/emails/daily-summary
type DailySummaryRequest struct {
	Date     string                     `json:"date"` // YYYY-MM-DD
	Emails   []EmailForClassification   `json:"emails"`
}

type DailySummaryResponse struct {
	Date      string              `json:"date"`
	Summary   string              `json:"summary"`
	Breakdown []CategoryBreakdown `json:"breakdown"`
	Todos     []ExtractedTodo     `json:"todos"`
}

type CategoryBreakdown struct {
	Category string `json:"category"`
	Count    int    `json:"count"`
	Snippet  string `json:"snippet"`
}

// DailySummary 生成每日邮件总结（定时任务/手动触发）。
func (c *Client) DailySummary(ctx context.Context, req DailySummaryRequest) (*DailySummaryResponse, error) {
	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/v1/emails/daily-summary", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.Client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("kxmemory daily summary: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		r, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("kxmemory daily summary %d: %s", resp.StatusCode, string(r))
	}

	var out DailySummaryResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode daily summary response: %w", err)
	}
	return &out, nil
}

// ---- 会议相关 ----

type MeetingSegment struct {
	Speaker string `json:"speaker"`
	Text    string `json:"text"`
	Lang    string `json:"lang"`
	StartMs int    `json:"start_ms"`
	EndMs   int    `json:"end_ms"`
}

type MeetingMeta struct {
	Title        string   `json:"title"`
	Participants []string `json:"participants"`
	Location     string   `json:"location,omitempty"`
}

type MeetingSummaryRequest struct {
	MeetingID   string           `json:"meeting_id"`
	Segments    []MeetingSegment `json:"segments"`
	PrevSummary string           `json:"prev_summary,omitempty"`
	Meta        MeetingMeta      `json:"meta,omitempty"`
}

type MeetingActionItem struct {
	Text     string `json:"text"`
	Assignee string `json:"assignee,omitempty"`
	Due      string `json:"due,omitempty"`
}

type MeetingSummaryResponse struct {
	Summary       string              `json:"summary"`
	KeyPoints     []string            `json:"key_points"`
	ActionItems   []MeetingActionItem `json:"action_items"`
	Decisions     []string            `json:"decisions"`
	OpenQuestions []string            `json:"open_questions"`
}

type MeetingRecommendRequest struct {
	MeetingID string           `json:"meeting_id"`
	Segments  []MeetingSegment `json:"segments"`
	Summary   string           `json:"summary,omitempty"`
}

type RecommendItem struct {
	Type    string  `json:"type"`
	ID      string  `json:"id"`
	Title   string  `json:"title"`
	Snippet string  `json:"snippet"`
	Score   float64 `json:"score"`
}

type MeetingRecommendResponse struct {
	Items []RecommendItem `json:"items"`
}

type MeetingRefineRequest struct {
	MeetingID   string           `json:"meeting_id"`
	Segments    []MeetingSegment `json:"segments"`
	TargetLangs []string         `json:"target_langs,omitempty"`
}

type MeetingRefineResponse struct {
	RefinedTranscript string              `json:"refined_transcript"`
	Translations      map[string]string   `json:"translations"`
	StructuredMinutes map[string]any      `json:"structured_minutes"`
	Todos             []MeetingActionItem `json:"todos"`
	NoteID            string              `json:"note_id,omitempty"`
}

func (c *Client) postJSON(ctx context.Context, path string, reqBody, out any) error {
	body, _ := json.Marshal(reqBody)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	resp, err := c.Client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		r, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("kxmemory %s %d: %s", path, resp.StatusCode, string(r))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) MeetingSummary(ctx context.Context, req MeetingSummaryRequest) (*MeetingSummaryResponse, error) {
	var out MeetingSummaryResponse
	if err := c.postJSON(ctx, "/v1/meetings/summary", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) MeetingRecommend(ctx context.Context, req MeetingRecommendRequest) (*MeetingRecommendResponse, error) {
	var out MeetingRecommendResponse
	if err := c.postJSON(ctx, "/v1/meetings/recommend", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) MeetingRefine(ctx context.Context, req MeetingRefineRequest) (*MeetingRefineResponse, error) {
	var out MeetingRefineResponse
	if err := c.postJSON(ctx, "/v1/meetings/refine", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
