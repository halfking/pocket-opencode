package server

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/halfking/pocket-opencode/backend/internal/adapter"
	"github.com/halfking/pocket-opencode/backend/internal/opencode"
	mobilews "github.com/halfking/pocket-opencode/backend/internal/websocket"
)

// MobileAPI provides optimized endpoints for mobile clients.
type MobileAPI struct {
	httpAdapter *adapter.OpenCodeHTTPAdapter
	eventMgr    *opencode.EventStreamManager
	permMgr     *opencode.PermissionManager
	questionMgr *opencode.QuestionManager
	wsHub       *mobilews.MobileWSHub
}

// MobileSessionListItem is a lightweight session representation for mobile.
type MobileSessionListItem struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	ModelName   string    `json:"modelName,omitempty"`
	Status      string    `json:"status"` // "idle", "busy", "retry"
	UpdatedAt   time.Time `json:"updatedAt"`
	Preview     string    `json:"preview"`     // Last message preview
	HasPending  bool      `json:"hasPending"`  // Has pending approvals
	PendingType string    `json:"pendingType,omitempty"` // "permission" | "question"
}

// MobileMessage is a compressed message structure for mobile display.
type MobileMessage struct {
	ID        string                 `json:"id"`
	Type      string                 `json:"type"` // "user", "assistant", "system"
	Text      string                 `json:"text,omitempty"`
	Tools     []MobileToolExecution  `json:"tools,omitempty"`
	Reasoning *MobileReasoning       `json:"reasoning,omitempty"`
	Timestamp int64                  `json:"timestamp"`
}

// MobileToolExecution represents a tool call in mobile format.
type MobileToolExecution struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Status  string `json:"status"`  // "running", "completed", "error"
	Summary string `json:"summary"` // Compressed output summary
}

// MobileReasoning represents reasoning content in mobile format.
type MobileReasoning struct {
	ID       string `json:"id"`
	Summary  string `json:"summary"`  // Collapsed reasoning content
	Expanded bool   `json:"expanded"`
}

// NewMobileAPI creates a new mobile API handler.
func NewMobileAPI(
	httpAdapter *adapter.OpenCodeHTTPAdapter,
	eventMgr *opencode.EventStreamManager,
	permMgr *opencode.PermissionManager,
	questionMgr *opencode.QuestionManager,
	wsHub *mobilews.MobileWSHub,
) *MobileAPI {
	return &MobileAPI{
		httpAdapter: httpAdapter,
		eventMgr:    eventMgr,
		permMgr:     permMgr,
		questionMgr: questionMgr,
		wsHub:       wsHub,
	}
}

// RegisterRoutes registers mobile API routes.
func (api *MobileAPI) RegisterRoutes(e *echo.Group) {
	mobile := e.Group("/mobile")

	// Session endpoints
	mobile.GET("/sessions", api.ListSessions)
	mobile.GET("/sessions/search", api.SearchSessions) // Phase 2.2: 搜索会话
	mobile.GET("/sessions/:id", api.GetSession)
	mobile.GET("/sessions/:id/summary", api.GetSessionSummary) // Phase 2.3: 会话摘要
	mobile.GET("/sessions/:id/messages", api.GetMessages)
	mobile.POST("/sessions/:id/prompt", api.SendPrompt)

	// Approval endpoints
	mobile.GET("/approvals", api.ListApprovals)
	mobile.POST("/approvals/permission/:id/reply", api.ReplyPermission)
	mobile.POST("/approvals/question/:id/reply", api.ReplyQuestion)
	mobile.POST("/approvals/question/:id/reject", api.RejectQuestion)

	// Voice input endpoint
	mobile.POST("/voice/input", api.ProcessVoiceInput)

	// WebSocket endpoint
	mobile.GET("/ws", api.HandleWebSocket)
}

// =============================================================================
// Session Endpoints
// =============================================================================

// ListSessions returns a lightweight list of sessions.
// Query params:
//   - limit: number of sessions to return (default 30)
//   - cursor: pagination cursor
//   - order: "asc" or "desc" (default "desc")
func (api *MobileAPI) ListSessions(c echo.Context) error {
	limit, _ := strconv.Atoi(c.QueryParam("limit"))
	if limit <= 0 || limit > 100 {
		limit = 30
	}

	cursor := c.QueryParam("cursor")
	order := c.QueryParam("order")
	if order != "asc" && order != "desc" {
		order = "desc"
	}

	// Get sessions from OpenCode
	// TODO: Implement pagination with cursor
	sessions, err := api.httpAdapter.ListSessions(c.Request().Context(), "")
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	// Get pending approvals count per session
	permissions := api.permMgr.ListPending("", "")
	questions := api.questionMgr.ListPending("", "")

	pendingBySession := make(map[string]string)
	for _, p := range permissions {
		if _, exists := pendingBySession[p.SessionID]; !exists {
			pendingBySession[p.SessionID] = "permission"
		}
	}
	for _, q := range questions {
		if _, exists := pendingBySession[q.SessionID]; !exists {
			pendingBySession[q.SessionID] = "question"
		}
	}

	// Convert to mobile format
	items := make([]MobileSessionListItem, 0, len(sessions))
	for _, s := range sessions {
		item := MobileSessionListItem{
			ID:        s.ID,
			Title:     s.Title,
			ModelName: "",           // OpenCodeSession doesn't have Model field
			Status:    determineStatus(s),
			UpdatedAt: time.Now(),   // OpenCodeSession doesn't have UpdatedAt field
			Preview:   "",           // OpenCodeSession doesn't have LastMessage field
		}

		if pendingType, hasPending := pendingBySession[s.ID]; hasPending {
			item.HasPending = true
			item.PendingType = pendingType
		}

		items = append(items, item)
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"data":   items,
		"cursor": cursor, // TODO: Return next cursor
	})
}

// SearchSessions searches sessions by keyword.
// Query params:
//   - q: search keyword (required)
//   - limit: number of results (default 20)
// Phase 2.2: 搜索会话功能
func (api *MobileAPI) SearchSessions(c echo.Context) error {
	query := c.QueryParam("q")
	if query == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "missing search query 'q'")
	}

	limit, _ := strconv.Atoi(c.QueryParam("limit"))
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	// 获取所有会话
	sessions, err := api.httpAdapter.ListSessions(c.Request().Context(), "")
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	// 搜索匹配的会话
	results := make([]MobileSessionListItem, 0)
	queryLower := toLowerCase(query)

	for _, s := range sessions {
		if len(results) >= limit {
			break
		}

		// 匹配标题或ID
		if containsIgnoreCase(s.Title, queryLower) || containsIgnoreCase(s.ID, queryLower) {
			item := MobileSessionListItem{
				ID:        s.ID,
				Title:     s.Title,
				Status:    determineStatus(s),
				UpdatedAt: time.Now(),
			}
			results = append(results, item)
		}
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"data":  results,
		"query": query,
		"total": len(results),
	})
}

// GetSession returns session details.
func (api *MobileAPI) GetSession(c echo.Context) error {
	sessionID := c.Param("id")

	summary, err := api.httpAdapter.GetSessionSummary(c.Request().Context(), "", sessionID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"data": summary,
	})
}

// GetSessionSummary returns a summary of the session.
// Phase 2.3: 会话摘要生成功能
func (api *MobileAPI) GetSessionSummary(c echo.Context) error {
	sessionID := c.Param("id")

	// 获取会话标题作为基础摘要
	title, err := api.httpAdapter.GetSessionSummary(c.Request().Context(), "", sessionID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	// 获取消息历史用于生成详细摘要
	messages, err := api.httpAdapter.GetMessages(c.Request().Context(), "", sessionID, 20, "desc")
	if err != nil {
		// 如果获取消息失败，返回标题作为摘要
		return c.JSON(http.StatusOK, map[string]interface{}{
			"data": map[string]interface{}{
				"sessionID": sessionID,
				"title":     title,
				"summary":   title,
				"messageCount": 0,
			},
		})
	}

	// 生成摘要：统计消息类型和提取关键信息
	summary := generateSummary(title, messages)

	return c.JSON(http.StatusOK, map[string]interface{}{
		"data": map[string]interface{}{
			"sessionID":    sessionID,
			"title":        title,
			"summary":      summary,
			"messageCount": len(messages),
		},
	})
}

// generateSummary 从消息历史生成会话摘要
func generateSummary(title string, messages []adapter.OpenCodeMessage) string {
	if len(messages) == 0 {
		return title
	}

	// 统计消息类型
	userCount := 0
	assistantCount := 0
	toolCount := 0

	for _, msg := range messages {
		switch msg.Type {
		case "user":
			userCount++
		case "assistant":
			assistantCount++
		case "tool":
			toolCount++
		}
	}

	// 构建摘要
	summary := title
	if userCount > 0 || assistantCount > 0 {
		summary += fmt.Sprintf(" (用户消息: %d, AI回复: %d", userCount, assistantCount)
		if toolCount > 0 {
			summary += fmt.Sprintf(", 工具调用: %d", toolCount)
		}
		summary += ")"
	}

	return summary
}

// GetMessages returns messages for a session.
// Query params:
//   - limit: number of messages (default 50, max 100)
//   - after: sequence number to start from (for incremental sync)
//   - order: "asc" or "desc" (default "desc")
func (api *MobileAPI) GetMessages(c echo.Context) error {
	sessionID := c.Param("id")

	limit, _ := strconv.Atoi(c.QueryParam("limit"))
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	order := c.QueryParam("order")
	if order != "asc" && order != "desc" {
		order = "desc"
	}

	after := c.QueryParam("after")

	resp, err := api.httpAdapter.GetSessionMessages(c.Request().Context(), "", sessionID, limit, order, after)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	// Convert to mobile format
	messages := make([]MobileMessage, 0, len(resp.Data))
	for _, msg := range resp.Data {
		// Extract fields from opencodeMessage struct
		mobileMsg := MobileMessage{
			ID:        msg.ID,
			Type:      msg.Type,
			Text:      "",
			Timestamp: time.Now().Unix(),
		}
		// Try to extract text from data map if available
		if msg.Data != nil {
			if text, ok := msg.Data["text"].(string); ok {
				mobileMsg.Text = text
			}
		}
		messages = append(messages, mobileMsg)
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"data":   messages,
		"cursor": resp.Cursor,
	})
}

// SendPrompt sends a prompt to a session.
func (api *MobileAPI) SendPrompt(c echo.Context) error {
	sessionID := c.Param("id")

	var req struct {
		Text string `json:"text"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	if req.Text == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "text is required")
	}

	// Create SendPromptRequest with text prompt
	promptReq := &adapter.SendPromptRequest{
		Prompt: adapter.PromptPayload{
			Text: req.Text,
		},
	}

	_, err := api.httpAdapter.SendPrompt(c.Request().Context(), "", sessionID, promptReq)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	// Broadcast event
	api.wsHub.BroadcastEvent(mobilews.MobileEvent{
		Type:      mobilews.EventSessionUpdated,
		SessionID: sessionID,
		Data:      map[string]string{"action": "prompt_sent"},
	})

	return c.NoContent(http.StatusAccepted)
}

// =============================================================================
// Approval Endpoints
// =============================================================================

// ListApprovals returns all pending approvals (permissions + questions).
func (api *MobileAPI) ListApprovals(c echo.Context) error {
	instanceID := c.QueryParam("instanceId")
	sessionID := c.QueryParam("sessionId")

	permissions := api.permMgr.ListPending(instanceID, sessionID)
	questions := api.questionMgr.ListPending(instanceID, sessionID)

	return c.JSON(http.StatusOK, map[string]interface{}{
		"permissions": permissions,
		"questions":   questions,
	})
}

// ReplyPermission replies to a permission request.
func (api *MobileAPI) ReplyPermission(c echo.Context) error {
	requestID := c.Param("id")

	var req struct {
		InstanceID string `json:"instanceId"`
		SessionID  string `json:"sessionId"`
		Reply      string `json:"reply"`  // "once" | "always" | "reject"
		Message    string `json:"message"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	var reply adapter.PermissionReply
	switch req.Reply {
	case "once":
		reply = adapter.PermissionReplyOnce
	case "always":
		reply = adapter.PermissionReplyAlways
	case "reject":
		reply = adapter.PermissionReplyReject
	default:
		return echo.NewHTTPError(http.StatusBadRequest, "invalid reply")
	}

	err := api.permMgr.Reply(c.Request().Context(), req.InstanceID, req.SessionID, requestID, reply, req.Message)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	return c.NoContent(http.StatusNoContent)
}

// ReplyQuestion replies to a question request.
func (api *MobileAPI) ReplyQuestion(c echo.Context) error {
	requestID := c.Param("id")

	var req struct {
		InstanceID string                    `json:"instanceId"`
		SessionID  string                    `json:"sessionId"`
		Answers    []adapter.QuestionAnswer  `json:"answers"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	err := api.questionMgr.Reply(c.Request().Context(), req.InstanceID, req.SessionID, requestID, req.Answers)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	return c.NoContent(http.StatusNoContent)
}

// RejectQuestion rejects a question request.
func (api *MobileAPI) RejectQuestion(c echo.Context) error {
	requestID := c.Param("id")

	var req struct {
		InstanceID string `json:"instanceId"`
		SessionID  string `json:"sessionId"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	err := api.questionMgr.Reject(c.Request().Context(), req.InstanceID, req.SessionID, requestID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	return c.NoContent(http.StatusNoContent)
}

// =============================================================================
// Voice Input Endpoint
// =============================================================================

// ProcessVoiceInput processes audio input from mobile client.
func (api *MobileAPI) ProcessVoiceInput(c echo.Context) error {
	// TODO: Implement voice recognition integration
	// For now, return a placeholder response
	return c.JSON(http.StatusOK, map[string]interface{}{
		"text":      "Voice recognition not yet implemented",
		"isCommand": false,
	})
}

// =============================================================================
// WebSocket Endpoint
// =============================================================================

// HandleWebSocket upgrades the connection to WebSocket.
func (api *MobileAPI) HandleWebSocket(c echo.Context) error {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			// TODO: Implement proper origin checking
			return true
		},
	}

	ws, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
	if err != nil {
		return err
	}

	// Extract user/device info from context or headers
	userID := c.Request().Header.Get("X-User-ID")
	deviceID := c.Request().Header.Get("X-Device-ID")
	if deviceID == "" {
		deviceID = c.RealIP()
	}

	api.wsHub.ServeClient(ws, userID, deviceID)
	return nil
}

// =============================================================================
// Helper functions
// =============================================================================

func determineStatus(s adapter.OpenCodeSession) string {
	// Simple status determination based on session status
	// OpenCodeSession already has a Status field
	if s.Status == "active" {
		return "busy"
	}
	return "idle"
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func convertToMobileMessage(msg interface{}) MobileMessage {
	// TODO: Implement full message conversion based on actual message structure
	// This is a placeholder implementation
	// The message type from GetSessionMessages is opencodeMessage which is not exported
	// For now, we'll use a generic interface and extract fields dynamically
	msgMap, ok := msg.(map[string]interface{})
	if !ok {
		return MobileMessage{}
	}

	id, _ := msgMap["id"].(string)
	msgType, _ := msgMap["type"].(string)

	return MobileMessage{
		ID:        id,
		Type:      msgType,
		Text:      "",
		Timestamp: time.Now().Unix(),
	}
}

// Phase 2.2: 搜索辅助函数

func toLowerCase(s string) string {
	result := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c = c + 32
		}
		result[i] = c
	}
	return string(result)
}

func containsIgnoreCase(s, substr string) bool {
	if substr == "" {
		return true
	}
	sLower := toLowerCase(s)
	substrLower := toLowerCase(substr)
	return contains(sLower, substrLower)
}

func contains(s, substr string) bool {
	return len(substr) <= len(s) && (substr == "" || searchString(s, substr))
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
