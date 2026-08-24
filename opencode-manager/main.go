package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

const Version = "0.1.0"

type Config struct {
	BackendURL   string            `json:"backendURL"`
	InstanceID   string            `json:"instanceID"`
	OpenCodePath string            `json:"opencodePath"`
	ConfigPath   string            `json:"configPath,omitempty"`
	AutoStart    bool              `json:"autoStart"`
	Port         int               `json:"port"`
	AuthToken    string            `json:"authToken"`
	HealthCheck  HealthCheckConfig `json:"healthCheck"`
}

type HealthCheckConfig struct {
	Interval int `json:"interval"` // seconds
	Timeout  int `json:"timeout"`  // seconds
}

type InstanceManager struct {
	config      Config
	ws          *websocket.Conn
	opencode    *OpenCodeProcess
	healthCheck *HealthChecker
	stopChan    chan struct{}
}

type OpenCodeProcess struct {
	Cmd       *exec.Cmd
	PID       int
	StartTime time.Time
	Status    string
}

type HealthChecker struct {
	manager  *InstanceManager
	interval time.Duration
	timeout  time.Duration
}

type WebSocketMessage struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

func main() {
	log.Printf("OpenCode Instance Manager v%s", Version)

	// Load configuration
	config, err := loadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Create manager
	manager := NewInstanceManager(config)

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start manager
	if err := manager.Start(); err != nil {
		log.Fatalf("Failed to start manager: %v", err)
	}

	// Wait for shutdown signal
	<-sigChan
	log.Println("Shutting down...")

	// Stop manager
	if err := manager.Stop(); err != nil {
		log.Printf("Error during shutdown: %v", err)
	}

	log.Println("Shutdown complete")
}

func NewInstanceManager(config Config) *InstanceManager {
	return &InstanceManager{
		config:   config,
		stopChan: make(chan struct{}),
	}
}

func (m *InstanceManager) Start() error {
	log.Println("Starting Instance Manager...")

	// 1. Connect to Backend
	if err := m.connectToBackend(); err != nil {
		return fmt.Errorf("failed to connect to backend: %w", err)
	}

	// 2. Register with Backend
	m.registerWithBackend()

	// 3. Auto-start OpenCode if configured
	if m.config.AutoStart {
		if err := m.startOpenCode(); err != nil {
			log.Printf("Warning: Failed to auto-start OpenCode: %v", err)
		}
	}

	// 4. Start health checker
	m.healthCheck = NewHealthChecker(m)
	go m.healthCheck.Start()

	// 5. Start message handler
	go m.handleMessages()

	log.Println("Instance Manager started successfully")
	return nil
}

func (m *InstanceManager) Stop() error {
	log.Println("Stopping Instance Manager...")

	// Signal stop
	close(m.stopChan)

	// Stop OpenCode
	if err := m.stopOpenCode(); err != nil {
		log.Printf("Error stopping OpenCode: %v", err)
	}

	// Unregister from Backend
	m.unregisterFromBackend()

	// Close WebSocket
	if m.ws != nil {
		m.ws.Close()
	}

	return nil
}

func (m *InstanceManager) connectToBackend() error {
	wsURL := fmt.Sprintf("%s/plugin/ws?type=manager&id=%s", m.config.BackendURL, m.config.InstanceID)

	log.Printf("Connecting to Backend: %s", wsURL)

	header := http.Header{}
	header.Add("Authorization", fmt.Sprintf("Bearer %s", m.config.AuthToken))

	ws, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		return fmt.Errorf("dial failed: %w", err)
	}

	m.ws = ws
	log.Println("Connected to Backend")

	return nil
}

func (m *InstanceManager) registerWithBackend() {
	hostname, _ := os.Hostname()

	payload := map[string]interface{}{
		"instanceID":   m.config.InstanceID,
		"hostname":     hostname,
		"version":      Version,
		"opencodePath": m.config.OpenCodePath,
		"configPath":   m.config.ConfigPath,
		"timestamp":    time.Now().Format(time.RFC3339),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		log.Printf("build manager.register payload: %v", err)
		return
	}

	msg := WebSocketMessage{
		Type: "manager.register",
		Data: data,
	}

	m.sendMessage(msg)
	log.Println("Registered with Backend")
}

func (m *InstanceManager) unregisterFromBackend() {
	data, err := json.Marshal(map[string]interface{}{
		"instanceID": m.config.InstanceID,
	})
	if err != nil {
		log.Printf("build manager.unregister payload: %v", err)
		return
	}

	msg := WebSocketMessage{
		Type: "manager.unregister",
		Data: data,
	}

	m.sendMessage(msg)
}

func (m *InstanceManager) startOpenCode() error {
	if m.opencode != nil && m.opencode.Status == "running" {
		return fmt.Errorf("OpenCode is already running")
	}

	log.Println("Starting OpenCode...")

	cmd := exec.Command("bun", "run", "dev")
	cmd.Dir = m.config.OpenCodePath
	if m.config.ConfigPath != "" {
		content, err := os.ReadFile(m.config.ConfigPath)
		if err != nil {
			return fmt.Errorf("read configured OpenCode config: %w", err)
		}
		cmd.Env = append(os.Environ(), "OPENCODE_CONFIG_PATH="+m.config.ConfigPath, "OPENCODE_CONFIG_CONTENT="+string(content))
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start OpenCode: %w", err)
	}

	m.opencode = &OpenCodeProcess{
		Cmd:       cmd,
		PID:       cmd.Process.Pid,
		StartTime: time.Now(),
		Status:    "starting",
	}

	// Wait for OpenCode to be ready
	if err := m.waitForOpenCode(); err != nil {
		m.opencode.Status = "error"
		return err
	}

	m.opencode.Status = "running"
	m.reportStatus("running")

	log.Printf("OpenCode started (PID: %d)", m.opencode.PID)
	return nil
}

func (m *InstanceManager) stopOpenCode() error {
	if m.opencode == nil {
		return nil
	}

	log.Println("Stopping OpenCode...")

	// Try graceful shutdown first
	if err := m.opencode.Cmd.Process.Signal(syscall.SIGTERM); err != nil {
		// Force kill if graceful fails
		if err := m.opencode.Cmd.Process.Kill(); err != nil {
			return fmt.Errorf("failed to kill OpenCode: %w", err)
		}
	}

	// Wait for process to exit
	m.opencode.Cmd.Wait()

	m.opencode.Status = "stopped"
	m.reportStatus("stopped")

	log.Println("OpenCode stopped")
	return nil
}

func (m *InstanceManager) restartOpenCode() error {
	log.Println("Restarting OpenCode...")

	if err := m.stopOpenCode(); err != nil {
		log.Printf("Warning: Error during stop: %v", err)
	}

	time.Sleep(2 * time.Second)

	return m.startOpenCode()
}

func (m *InstanceManager) waitForOpenCode() error {
	timeout := time.After(30 * time.Second)
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	// Pinned contract (docs/opencode-contract.md §3.1): health is served at
	// GET /global/health, not /api/health. See
	// packages/opencode/src/server/routes/instance/httpapi/groups/global.ts:68.
	apiURL := fmt.Sprintf("http://localhost:%d/global/health", m.config.Port)

	for {
		select {
		case <-timeout:
			return fmt.Errorf("timeout waiting for OpenCode to start")
		case <-ticker.C:
			resp, err := http.Get(apiURL)
			if err == nil && resp.StatusCode == 200 {
				return nil
			}
		}
	}
}

func (m *InstanceManager) handleMessages() {
	for {
		select {
		case <-m.stopChan:
			return
		default:
			var msg WebSocketMessage
			if err := m.ws.ReadJSON(&msg); err != nil {
				log.Printf("WebSocket read error: %v", err)
				time.Sleep(5 * time.Second)
				continue
			}

			m.handleCommand(msg)
		}
	}
}

func (m *InstanceManager) handleCommand(msg WebSocketMessage) {
	log.Printf("Received command: %s", msg.Type)

	var err error

	switch msg.Type {
	case "command.start":
		err = m.startOpenCode()
	case "command.stop":
		err = m.stopOpenCode()
	case "command.restart":
		err = m.restartOpenCode()
	case "command.status":
		m.sendStatus()
	case "command.migrate_to":
		// 会话跨主机迁移执行端：拉迁移包 → 创建新会话 → 发送续接 prompt → 回报新 sessionID
		err = m.handleMigrateTo(msg.Data)
	default:
		log.Printf("Unknown command: %s", msg.Type)
		return
	}

	if err != nil {
		log.Printf("Command failed: %v", err)
		m.sendCommandResult(msg.Type, false, err.Error())
	} else {
		m.sendCommandResult(msg.Type, true, "")
	}
}

// migrateToInput 是 command.migrate_to 命令的入参。
type migrateToInput struct {
	PackURL        string   `json:"packURL"`
	PackToken      string   `json:"packToken,omitempty"`
	PromptText     string   `json:"promptText,omitempty"`      // Pocket 端预拼接好的提示词（优先用）
	PromptTemplate []string `json:"promptTemplates,omitempty"` // 否则按模板名在 manager 端拼（简化版）
	WorkingDir     string   `json:"workingDirectory,omitempty"`
	Agent          string   `json:"agent,omitempty"`
	Model          string   `json:"model,omitempty"`
}

// handleMigrateTo 在本机 OpenCode 执行会话迁移：
//  1. 从 PackURL 拉迁移包
//  2. 取 promptText（Pocket 预拼）或 fallback 简化拼接
//  3. POST /session 创建新会话
//  4. POST /session/{id}/prompt 发送续接 prompt
//  5. 回报新 sessionID（Pocket 据此建立 task_session_links 映射）
//
// 注：完整的 4 类提示词模板在 Pocket internal/migration/prompts 与 opencode-plugin/src/prompts.ts，
// manager 端只做简化 fallback（仅拼 summary），生产路径应由 Pocket 预拼 promptText 下发。
func (m *InstanceManager) handleMigrateTo(raw json.RawMessage) error {
	var in migrateToInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return fmt.Errorf("parse migrate_to input: %w", err)
	}
	if in.PackURL == "" {
		return fmt.Errorf("packURL is required")
	}

	baseURL := fmt.Sprintf("http://localhost:%d", m.config.Port)

	// 1. 拉迁移包
	pack, err := fetchMigrationPack(in.PackURL, in.PackToken)
	if err != nil {
		return fmt.Errorf("fetch pack: %w", err)
	}

	// 2. 取提示词
	prompt := in.PromptText
	if prompt == "" {
		prompt = buildFallbackPrompt(pack)
	}

	// 3. 创建新会话
	newSessionID, err := createOpenCodeSession(baseURL, in.Agent, in.Model, in.WorkingDir, m.config.AuthToken)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}

	// 4. 发送续接 prompt
	if err := sendOpenCodePrompt(baseURL, newSessionID, prompt, m.config.AuthToken); err != nil {
		return fmt.Errorf("send prompt: %w", err)
	}

	log.Printf("✅ migrate_to done: new session %s (from %s)", newSessionID, pack.sessionMetaID())

	// 5. 回报新 sessionID（在 command.result 里带上）
	m.sendCommandResultWithData("command.migrate_to", true, "", map[string]interface{}{
		"newSessionID":  newSessionID,
		"fromSessionID": pack.sessionMetaID(),
	})
	return nil
}

func (m *InstanceManager) sendCommandResult(commandType string, success bool, errorMsg string) {
	result := map[string]interface{}{
		"command": commandType,
		"success": success,
	}
	if errorMsg != "" {
		result["error"] = errorMsg
	}

	data, _ := json.Marshal(result)
	msg := WebSocketMessage{
		Type: "command.result",
		Data: data,
	}

	m.sendMessage(msg)
}

func (m *InstanceManager) reportStatus(status string) {
	data, _ := json.Marshal(map[string]interface{}{
		"instanceID": m.config.InstanceID,
		"status":     status,
		"version":    Version,
		"machine":    collectMachineInfo(),
		"timestamp":  time.Now().Format(time.RFC3339),
	})

	msg := WebSocketMessage{
		Type: "instance.status",
		Data: data,
	}

	m.sendMessage(msg)
}

// collectMachineInfo 收集本机机器信息，上报给 Pocket 用于实例画像与跨主机迁移的目标选择。
func collectMachineInfo() map[string]interface{} {
	hostname, _ := os.Hostname()
	return map[string]interface{}{
		"hostname": hostname,
		"platform": runtime.GOOS,
		"arch":     runtime.GOARCH,
		"cpus":     runtime.NumCPU(),
	}
}

func (m *InstanceManager) sendStatus() {
	status := "stopped"
	var pid int
	var uptime time.Duration

	if m.opencode != nil {
		status = m.opencode.Status
		pid = m.opencode.PID
		uptime = time.Since(m.opencode.StartTime)
	}

	data, _ := json.Marshal(map[string]interface{}{
		"instanceID": m.config.InstanceID,
		"status":     status,
		"pid":        pid,
		"uptime":     uptime.Seconds(),
		"timestamp":  time.Now().Format(time.RFC3339),
	})

	msg := WebSocketMessage{
		Type: "instance.status",
		Data: data,
	}

	m.sendMessage(msg)
}

func (m *InstanceManager) sendMessage(msg WebSocketMessage) {
	if m.ws == nil {
		log.Println("WebSocket not connected")
		return
	}

	if err := m.ws.WriteJSON(msg); err != nil {
		log.Printf("Failed to send message: %v", err)
	}
}

func NewHealthChecker(manager *InstanceManager) *HealthChecker {
	return &HealthChecker{
		manager:  manager,
		interval: time.Duration(manager.config.HealthCheck.Interval) * time.Second,
		timeout:  time.Duration(manager.config.HealthCheck.Timeout) * time.Second,
	}
}

func (h *HealthChecker) Start() {
	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()

	for {
		select {
		case <-h.manager.stopChan:
			return
		case <-ticker.C:
			h.Check()
		}
	}
}

func (h *HealthChecker) Check() {
	if h.manager.opencode == nil {
		return
	}

	// Pinned contract (docs/opencode-contract.md §3.1): health is served at
	// GET /global/health, not /api/health. See
	// packages/opencode/src/server/routes/instance/httpapi/groups/global.ts:68.
	apiURL := fmt.Sprintf("http://localhost:%d/global/health", h.manager.config.Port)

	client := &http.Client{Timeout: h.timeout}
	resp, err := client.Get(apiURL)

	healthy := (err == nil && resp.StatusCode == 200)

	if !healthy && h.manager.opencode.Status == "running" {
		log.Println("Health check failed, marking as unhealthy")
		h.manager.opencode.Status = "unhealthy"
		h.manager.reportStatus("unhealthy")
	} else if healthy && h.manager.opencode.Status != "running" {
		log.Println("Health check passed, marking as running")
		h.manager.opencode.Status = "running"
		h.manager.reportStatus("running")
	}
}

func loadConfig() (Config, error) {
	configPath := os.Getenv("OPENCODE_MANAGER_CONFIG")
	if configPath == "" {
		configPath = "/etc/opencode-instance-manager/config.json"
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return Config{}, fmt.Errorf("failed to read config file: %w", err)
	}

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return Config{}, fmt.Errorf("failed to parse config: %w", err)
	}

	// Set defaults
	if config.Port == 0 {
		// 与 Pocket NetworkDiscovery 扫描端口对齐（discovery.go DefaultPorts 首位）。
		// 历史值 4096 会导致 Pocket 扫不到，必须用 14096。
		config.Port = 14096
	}
	if config.HealthCheck.Interval == 0 {
		config.HealthCheck.Interval = 30
	}
	if config.HealthCheck.Timeout == 0 {
		config.HealthCheck.Timeout = 5
	}

	return config, nil
}

// =============================================================================
// 会话跨主机迁移辅助（command.migrate_to 执行端）
// =============================================================================

// migrationPack 是迁移包的最小解析结构（只取迁移执行所需的字段）。
type migrationPack struct {
	SessionMeta struct {
		ID        string `json:"id"`
		Title     string `json:"title"`
		Directory string `json:"directory"`
		Instance  string `json:"instance"`
	} `json:"session_meta"`
	ResumeBrief struct {
		NextAction string `json:"next_action"`
	} `json:"resume_brief"`
	Summary string `json:"summary"`
}

// sessionMetaID 暴露给日志用。
func (p *migrationPack) sessionMetaID() string { return p.SessionMeta.ID }

// fetchMigrationPack 从 PackURL（llm-gateway /v1/sessions/{id}/pack 或 Pocket 中转）拉迁移包。
func fetchMigrationPack(packURL, token string) (*migrationPack, error) {
	req, err := http.NewRequest(http.MethodGet, packURL, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var pack migrationPack
	if err := json.NewDecoder(resp.Body).Decode(&pack); err != nil {
		return nil, fmt.Errorf("decode pack: %w", err)
	}
	return &pack, nil
}

// buildFallbackPrompt 在 Pocket 未预拼 promptText 时的简化兜底。
// 生产路径应优先用 Pocket internal/migration/prompts 的完整 4 模板拼接后下发。
func buildFallbackPrompt(p *migrationPack) string {
	title := p.SessionMeta.Title
	if title == "" {
		title = p.SessionMeta.ID
	}
	prompt := fmt.Sprintf("# 任务迁移续接\n来源会话：%s\n来源实例：%s\n\n", title, p.SessionMeta.Instance)
	if p.Summary != "" {
		prompt += "## 上次摘要\n" + p.Summary + "\n\n"
	}
	if p.ResumeBrief.NextAction != "" {
		prompt += "## 下一步\n" + p.ResumeBrief.NextAction + "\n\n"
	}
	prompt += "请先检查当前工作目录与 git 状态，确认环境一致后从上一步接续，不要重头开始。"
	return prompt
}

// createOpenCodeSession 调本机 OpenCode POST /session 创建新会话，返回新 sessionID。
//
// Pinned contract (docs/opencode-contract.md §3.3, upstream
// packages/opencode/src/server/routes/instance/httpapi/groups/session.ts:87
// and packages/opencode/src/session/session.ts:249-259):
// the body must be Session.CreateInput
//   { parentID?, title?, agent?, model?, metadata?, permission?, workspaceID? }.
// The legacy { location: { directory } } shape is NOT accepted by pinned upstream —
// location.directory is now a top-level `directory` field on Session.Info (read
// only) and is no longer in the create payload.
func createOpenCodeSession(baseURL, agent, model, workDir, authToken string) (string, error) {
	_ = workDir // workspace/directory is set via CWD on the OpenCode process; not part of the wire payload.
	payload := map[string]interface{}{
		"agent": agent,
		"model": model,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest(http.MethodPost, baseURL+"/session", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if authToken != "" {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode session response: %w", err)
	}
	if result.ID == "" {
		return "", fmt.Errorf("session response missing id")
	}
	return result.ID, nil
}

// sendOpenCodePrompt 调本机 OpenCode POST /session/{id}/message 发送续接 prompt。
//
// Pinned contract (docs/opencode-contract.md §3.3, upstream
// packages/opencode/src/server/routes/instance/httpapi/groups/session.ts:95
// and packages/opencode/src/session/prompt.ts:1579-1601):
// the body must be PromptPayload and `parts` is REQUIRED. Legacy
// { id, prompt:{text}, delivery } is NOT accepted.
func sendOpenCodePrompt(baseURL, sessionID, prompt, authToken string) error {
	payload := map[string]interface{}{
		"parts": []map[string]interface{}{{"type": "text", "text": prompt}},
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest(http.MethodPost, baseURL+"/session/"+sessionID+"/message", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if authToken != "" {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// sendCommandResultWithData 发送带附加数据的 command.result（用于 migrate_to 回报新 sessionID）。
func (m *InstanceManager) sendCommandResultWithData(commandType string, success bool, errorMsg string, extra map[string]interface{}) {
	result := map[string]interface{}{
		"command": commandType,
		"success": success,
	}
	if errorMsg != "" {
		result["error"] = errorMsg
	}
	for k, v := range extra {
		result[k] = v
	}

	data, _ := json.Marshal(result)
	msg := WebSocketMessage{
		Type: "command.result",
		Data: data,
	}
	m.sendMessage(msg)
}
