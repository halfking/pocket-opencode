package accruntime

// 生态读取面：ACC 的 agent 注册表与 canonical 任务面。
//
// 为什么单独一个文件：这两个面与 Runtime Control（审批/取消）职责不同 ——
// 它们是**只读**的生态视图，不参与 permission gate，也不该被 fail-closed
// 语义牵连。放在一起会让调用方误以为"读 agent 失败"等于"门禁不可用"。
//
// 鉴权形态与 approvals 面完全一致（HS256 服务 JWT），实测自 2026-10-22
// 的本机 ACC：
//
//	GET /api/v2/agents             → 200 {"agents":[{"id":"agent-companion-host",…}]}
//	GET /api/v2/canonical/tasks    → 200 {"tasks":[],"data":[],"page":{…}}
//
// 无 Authorization → 401 missing bearer token；静态 Bearer → 401 invalid
// bearer token。静态 token 恒 401，且与"ACC 挂了"不可区分，所以这一面必须
// 走 accjwt 的签名路径。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Agent 是 ACC agent 注册表里的一条记录。agent-companion 的**每个实例**
// （本机 daemon、CI 里的 runtime-dev、各机器上的 acs-*）都是独立一条，
// status 是判断"这批智能体现在活着没有"的第一手依据。
//
// ★ 2026-10-25 实测：本机 ACC 的**注册表**（GET /api/v2/agents）在容器重启后
// 是空的，而**运行时清单**（GET /api/v2/runtime/runtimes/{id}/agents，由 ACC
// 回连 daemon 的读面）里是满的 —— 两面都是「ACC 里的 agent 清单」，但只有
// 后者带活的状态。于是这里认**两种形状**：
//
//	注册表形状：{"id","name","type","status","last_seen_at","capabilities"}
//	运行时形状：{"agent_id","kind","status","communication_mode","pid",
//	             "command","last_active_at","capabilities"}
//	             （契约见 agent-companion docs/ACC_BRIDGE_SPEC.md §AgentState）
//
// normalize() 把运行时形状折进注册表形状，下游（UI/调用方）只看一套字段。
type Agent struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Type         string   `json:"type,omitempty"`
	Status       string   `json:"status,omitempty"`
	LastSeenAt   string   `json:"last_seen_at,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`

	// 运行时清单的契约字段（注册表形状不会出现）。
	AgentID           string   `json:"agent_id,omitempty"`
	Kind              string   `json:"kind,omitempty"`
	CommunicationMode string   `json:"communication_mode,omitempty"`
	PID               int      `json:"pid,omitempty"`
	Command           string   `json:"command,omitempty"`
	LastActiveAt      string   `json:"last_active_at,omitempty"`
}

// normalize 把运行时清单的契约字段折进注册表形状的字段里。
// 没有这一步，UI 拿到的 type/status 会整片为空 —— 看上去就是「智能体不认识」。
func (a *Agent) normalize() {
	if a.ID == "" {
		a.ID = a.AgentID
	}
	if a.Name == "" {
		a.Name = a.AgentID
	}
	if a.Type == "" {
		a.Type = a.Kind
	}
	if a.LastSeenAt == "" {
		a.LastSeenAt = a.LastActiveAt
	}
}

// ListAgents 读 GET /api/v2/agents。
//
// 响应外层是 {"agents":[…]}。ACC 也可能在别处用 {"data":[…]}，两种都认；
// 外层是对象但认不出列表时返回空切片而不是错误 —— "注册表里现在没有
// agent" 是合法状态，不是故障。
func (c *Client) ListAgents(ctx context.Context) ([]Agent, error) {
	if err := c.ready(); err != nil {
		return nil, err
	}
	resp, err := c.http.Get(ctx, "/api/v2/agents", nil)
	if err != nil {
		return nil, fmt.Errorf("accruntime: list agents: %w", err)
	}
	raw, err := readAll(resp)
	if err != nil {
		return nil, fmt.Errorf("accruntime: list agents: %w", err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err == nil {
		for _, key := range []string{"agents", "data", "items"} {
			v, ok := doc[key]
			if !ok {
				continue
			}
			var out []Agent
			if err := json.Unmarshal(v, &out); err == nil {
				return out, nil
			}
		}
		return []Agent{}, nil
	}
	var out []Agent
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("accruntime: decode agents: %w", err)
	}
	return out, nil
}

// ListRuntimeAgents 读 **运行时清单**（不是注册表）：
//
//	GET /api/v2/runtime/runtimes                      → 已注册的 runtime 列表
//	GET /api/v2/runtime/runtimes/{runtime_id}/agents  → 该 runtime 的智能体清单
//
// ★ 为什么必须读这一面（2026-10-25 实测）：
//
//	· ACC 的**注册表**（GET /api/v2/agents）在 acc-go 容器重启后是**空的**，
//	  而 agent-companion 明明在线（daemon 侧 /api/v1/agents 有 43 个）；
//	· 运行时清单这一面是 ACC **回连 daemon 的读面**（GET {api_endpoint}
//	  /api/v1/runtime/agents，api_endpoint 由 daemon 注册时广告），拿到的是
//	  **活的**清单，带 status/communication_mode/pid；
//	· 契约见 agent-companion docs/ACC_BRIDGE_SPEC.md。
//
// 回连不通时（runtime 没广告 api_endpoint、或 daemon 不可达）ACC 会退回到
// drivers 兜底面（fallback_used=true）—— 那时 agents 为空，**不是故障**，
// 故这里返回空切片而不是错误，交给调用方决定怎么呈现。
func (c *Client) ListRuntimeAgents(ctx context.Context) ([]Agent, error) {
	if err := c.ready(); err != nil {
		return nil, err
	}
	runtimes, err := c.listRuntimeIDs(ctx)
	if err != nil {
		return nil, err
	}
	var out []Agent
	for _, rid := range runtimes {
		agents, err := c.listRuntimeAgentsOf(ctx, rid)
		if err != nil {
			// 单个 runtime 读不到（没广告读面 / 机器已下线）不影响其它
			// runtime 的清单 —— 跳过即可，不能让一个下线的机器把整张表清空。
			continue
		}
		out = append(out, agents...)
	}
	for i := range out {
		out[i].normalize()
	}
	return out, nil
}

// listRuntimeIDs 读已注册 runtime 的 id 列表。
func (c *Client) listRuntimeIDs(ctx context.Context) ([]string, error) {
	resp, err := c.http.Get(ctx, "/api/v2/runtime/runtimes", nil)
	if err != nil {
		return nil, fmt.Errorf("accruntime: list runtimes: %w", err)
	}
	raw, err := readAll(resp)
	if err != nil {
		return nil, fmt.Errorf("accruntime: list runtimes: %w", err)
	}
	var doc struct {
		Data []struct {
			RuntimeID string `json:"runtime_id"`
		} `json:"data"`
		Items []struct {
			RuntimeID string `json:"runtime_id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("accruntime: decode runtimes: %w", err)
	}
	var ids []string
	for _, r := range doc.Data {
		if r.RuntimeID != "" {
			ids = append(ids, r.RuntimeID)
		}
	}
	for _, r := range doc.Items {
		if r.RuntimeID != "" {
			ids = append(ids, r.RuntimeID)
		}
	}
	return ids, nil
}

// listRuntimeAgentsOf 读单个 runtime 的智能体清单。
func (c *Client) listRuntimeAgentsOf(ctx context.Context, runtimeID string) ([]Agent, error) {
	resp, err := c.http.Get(ctx, "/api/v2/runtime/runtimes/"+url.PathEscape(runtimeID)+"/agents", nil)
	if err != nil {
		return nil, fmt.Errorf("accruntime: list runtime agents: %w", err)
	}
	raw, err := readAll(resp)
	if err != nil {
		return nil, fmt.Errorf("accruntime: list runtime agents: %w", err)
	}
	var doc struct {
		Agents []Agent `json:"agents"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("accruntime: decode runtime agents: %w", err)
	}
	return doc.Agents, nil
}

// ListCanonicalTasks 读 GET /api/v2/canonical/tasks —— ACC canonical
// contract 的任务面。
//
// 条目按**原始 JSON 透传**（json.RawMessage），不在这里钉一份字段表：
// ACC 的 task 结构（acc.Task）随 canonical contract 演进，pocketd 抄一份
// 就会变成"字段永远缺一个"的影子契约，而透传既不丢字段、ACC 加字段时
// 也自动跟随。调用方要字段自己解。
//
// limit<=0 时用 ACC 的默认值（50）。响应外层同时有 "tasks" 与 "data"
// 两个同源列表，认哪个都行。
func (c *Client) ListCanonicalTasks(ctx context.Context, limit int) ([]json.RawMessage, error) {
	if err := c.ready(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	resp, err := c.http.Get(ctx,
		"/api/v2/canonical/tasks?limit="+strconv.Itoa(limit), nil)
	if err != nil {
		return nil, fmt.Errorf("accruntime: list canonical tasks: %w", err)
	}
	raw, err := readAll(resp)
	if err != nil {
		return nil, fmt.Errorf("accruntime: list canonical tasks: %w", err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err == nil {
		for _, key := range []string{"tasks", "data", "items"} {
			v, ok := doc[key]
			if !ok {
				continue
			}
			var out []json.RawMessage
			if err := json.Unmarshal(v, &out); err == nil {
				return out, nil
			}
		}
		return []json.RawMessage{}, nil
	}
	var out []json.RawMessage
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("accruntime: decode canonical tasks: %w", err)
	}
	return out, nil
}

// ListProjects 读 GET /api/v2/projects —— canonical contract 的项目面。
// 项目是 agent-companion 实例在 ACC 里的归属单元（companion 的 dispatch
// 会写进项目），列出来才知道某批会话属于谁。
//
// 条目同样按原始 JSON 透传，理由同 ListCanonicalTasks。
func (c *Client) ListProjects(ctx context.Context) ([]json.RawMessage, error) {
	if err := c.ready(); err != nil {
		return nil, err
	}
	resp, err := c.http.Get(ctx, "/api/v2/projects", nil)
	if err != nil {
		return nil, fmt.Errorf("accruntime: list projects: %w", err)
	}
	raw, err := readAll(resp)
	if err != nil {
		return nil, fmt.Errorf("accruntime: list projects: %w", err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err == nil {
		for _, key := range []string{"projects", "data", "items"} {
			v, ok := doc[key]
			if !ok {
				continue
			}
			var out []json.RawMessage
			if err := json.Unmarshal(v, &out); err == nil {
				return out, nil
			}
		}
		return []json.RawMessage{}, nil
	}
	var out []json.RawMessage
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("accruntime: decode projects: %w", err)
	}
	return out, nil
}

// EscapePathSegment 把 id 里的路径分隔符转义掉，防止 task_id 形如
// "acs-macbook-30b:dispatch_xxx" 时把 URL 结构带偏。
func EscapePathSegment(s string) string {
	return url.PathEscape(strings.TrimSpace(s))
}
