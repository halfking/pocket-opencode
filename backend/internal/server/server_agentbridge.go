package server

// server_agentbridge.go — S0-D Agent Bridge HTTP handlers + adapter wiring.
//
// Routes:
//   GET    /api/agents                  列出当前 workspace 的 agents
//   POST   /api/agents                  注册一个新 agent（绑定到 instance_id）
//   GET    /api/agents/{id}             agent 详情
//   POST   /api/agents/{id}/send        给 agent 发 prompt（创建 session + 自动 attach task）
//   DELETE /api/agents/{id}             注销 agent
//
// 底层依赖：
//   - agentbridge.Store（PG）
//   - agentbridge.Bridge（编排：resolve instance → create session → send → attach）
//   - Bridge 通过 SessionCreator/InstanceResolver/TaskAttacher 接口注入具体实现：
//       SessionCreator   ← opencodeSessionCreator（包装 adapter.OpenCodeHTTPAdapter）
//       InstanceResolver ← registryResolver（包装 registry.Registry）
//       TaskAttacher     ← taskAttacher（包装 task.Store）

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/halfking/pocket-opencode/backend/internal/adapter"
	"github.com/halfking/pocket-opencode/backend/internal/agentbridge"
	"github.com/halfking/pocket-opencode/backend/internal/task"
)

// ---- 适配器：把现有 adapter / registry / task.Store 适配到 Bridge 接口 ----
// 这些类型在 server 包内，main.go 通过下面的导出构造函数获取。

// opencodeSessionCreator 适配 adapter.OpenCodeHTTPAdapter → agentbridge.SessionCreator。
type opencodeSessionCreator struct {
	oc adapter.OpenCodeAdapter
}

func (c *opencodeSessionCreator) CreateSessionOnInstance(ctx context.Context, apiBase string, in *agentbridge.CreateSessionInput) (*agentbridge.SessionInfo, error) {
	req := &adapter.CreateSessionRequest{}
	if in.Agent != "" {
		req.Agent = &in.Agent
	}
	if in.ModelID != "" {
		provider := in.ProviderID
		if provider == "" {
			provider = "kaixuan"
		}
		req.Model = &adapter.ModelRefHTTP{ID: in.ModelID, ProviderID: provider}
	}
	if in.Directory != "" {
		req.Location = &adapter.LocationRefRef{Directory: in.Directory}
	}
	info, err := c.oc.CreateSession(ctx, apiBase, req)
	if err != nil {
		return nil, err
	}
	if info == nil {
		return nil, errors.New("nil session info")
	}
	return &agentbridge.SessionInfo{ID: info.ID}, nil
}

func (c *opencodeSessionCreator) SendPromptToSession(ctx context.Context, apiBase, sessionID string, in *agentbridge.SendPromptInput) error {
	payload := &adapter.SendPromptRequest{
		Parts: []adapter.PromptPart{{Type: "text", Text: in.Text}},
	}
	if in.Agent != "" {
		payload.Agent = &in.Agent
	}
	_, err := c.oc.SendPrompt(ctx, apiBase, sessionID, payload)
	return err
}

// instanceAPIBaseResolver 是 registry 暴露的极简接口（避免 server 包反向依赖 registry）。
type instanceAPIBaseResolver interface {
	GetInstanceAPIBase(id string) (string, error)
}

// registryResolver 适配 registry → agentbridge.InstanceResolver。
type registryResolver struct{ r instanceAPIBaseResolver }

func (rr *registryResolver) ResolveAPIBase(id string) (string, error) {
	if rr.r == nil {
		return "", errors.New("registry not configured")
	}
	return rr.r.GetInstanceAPIBase(id)
}

// taskAttacher 适配 task.Store → agentbridge.TaskAttacher。
type taskAttacher struct{ store *task.Store }

func (t *taskAttacher) AttachSession(ctx context.Context, taskID, instanceID, sessionID, role string) error {
	if t == nil || t.store == nil {
		return errors.New("task store not configured")
	}
	return t.store.AttachSession(ctx, task.SessionLink{
		TaskID: taskID, InstanceID: instanceID, SessionID: sessionID, Role: role,
	})
}

// NewAgentBridgeAdapters 是 main.go 用的导出构造函数，把现有的
// opencodeAdapter / registry / task.Store 包成 Bridge 依赖。
func NewAgentBridgeAdapters(oc adapter.OpenCodeAdapter, reg instanceAPIBaseResolver, ts *task.Store) (
	agentbridge.SessionCreator, agentbridge.InstanceResolver, agentbridge.TaskAttacher) {
	var creator agentbridge.SessionCreator
	if oc != nil {
		creator = &opencodeSessionCreator{oc: oc}
	}
	var resolver agentbridge.InstanceResolver
	if reg != nil {
		resolver = &registryResolver{r: reg}
	}
	var attacher agentbridge.TaskAttacher
	if ts != nil {
		attacher = &taskAttacher{store: ts}
	}
	return creator, resolver, attacher
}

// ---- HTTP handlers ----

// handleAgents 处理 GET/POST /api/agents
func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	if s.agentStore == nil {
		writeError(w, http.StatusServiceUnavailable, "agent bridge not configured")
		return
	}
	switch r.Method {
	case http.MethodGet:
		wsID := s.workspaceIDFromRequest(r)
		agents, err := s.agentStore.ListByWorkspace(r.Context(), wsID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list agents: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"agents": agents})
	case http.MethodPost:
		s.createAgent(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "GET or POST only")
	}
}

func (s *Server) createAgent(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID           string   `json:"id"`
		InstanceID   string   `json:"instance_id"`
		Name         string   `json:"name"`
		Role         string   `json:"role"`
		Capabilities []string `json:"capabilities"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if body.InstanceID == "" || body.Name == "" {
		writeError(w, http.StatusBadRequest, "instance_id and name required")
		return
	}
	if body.ID == "" {
		body.ID = "agent_" + body.InstanceID + "_" + strings.ToLower(strings.ReplaceAll(body.Name, " ", "_"))
	}
	a := &agentbridge.Agent{
		ID:           body.ID,
		WorkspaceID:  s.workspaceIDFromRequest(r),
		InstanceID:   body.InstanceID,
		Name:         body.Name,
		Role:         agentbridge.Role(body.Role),
		Capabilities: body.Capabilities,
	}
	if err := s.agentStore.Create(r.Context(), a); err != nil {
		if errors.Is(err, agentbridge.ErrLimitReached) {
			writeError(w, http.StatusConflict, "agent limit reached")
			return
		}
		writeError(w, http.StatusInternalServerError, "create agent: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

// handleAgentOps 处理 /api/agents/{id}[/send]
func (s *Server) handleAgentOps(w http.ResponseWriter, r *http.Request) {
	if s.agentStore == nil {
		writeError(w, http.StatusServiceUnavailable, "agent bridge not configured")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/agents/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusBadRequest, "agent id required")
		return
	}
	agentID := parts[0]

	if len(parts) == 1 {
		// GET /api/agents/{id}  /  DELETE /api/agents/{id}
		switch r.Method {
		case http.MethodGet:
			a, err := s.agentStore.Get(r.Context(), agentID)
			if err != nil {
				writeError(w, http.StatusNotFound, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, a)
		case http.MethodDelete:
			if err := s.agentStore.Delete(r.Context(), agentID); err != nil {
				writeError(w, http.StatusNotFound, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"deleted": agentID})
		default:
			writeError(w, http.StatusMethodNotAllowed, "GET or DELETE only")
		}
		return
	}

	if parts[1] == "send" && r.Method == http.MethodPost {
		s.sendToAgent(w, r, agentID)
		return
	}
	writeError(w, http.StatusNotFound, "unknown subpath: "+parts[1])
}

func (s *Server) sendToAgent(w http.ResponseWriter, r *http.Request, agentID string) {
	if s.agentBridge == nil {
		writeError(w, http.StatusServiceUnavailable, "agent bridge not configured")
		return
	}
	var body struct {
		Prompt     string `json:"prompt"`
		TaskID     string `json:"task_id"`
		Role       string `json:"role"`
		AgentName  string `json:"agent"`
		ModelID    string `json:"model_id"`
		ProviderID string `json:"provider_id"`
		Directory  string `json:"directory"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if body.Prompt == "" {
		writeError(w, http.StatusBadRequest, "prompt required")
		return
	}
	res, err := s.agentBridge.Send(r.Context(), agentID, body.Prompt, agentbridge.SendOptions{
		TaskID: body.TaskID, Role: body.Role, AgentName: body.AgentName,
		ModelID: body.ModelID, ProviderID: body.ProviderID, Directory: body.Directory,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "agent send: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}
