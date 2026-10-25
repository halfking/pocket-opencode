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
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// ErrNoDispatchableAgent 表示「ACC 上现在没有可派单的智能体」——
// 与「读 ACC 失败」是**两件不同的事**：后者是通道问题（网关 502、
// 超时、401），前者是库存问题。调用方必须能把它们分开报给用户，
// 否则一次网关抖动会被说成「没有智能体可用」。
var ErrNoDispatchableAgent = errors.New("accruntime: no dispatchable agent")

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
	AgentID           string `json:"agent_id,omitempty"`
	Kind              string `json:"kind,omitempty"`
	CommunicationMode string `json:"communication_mode,omitempty"`
	PID               int    `json:"pid,omitempty"`
	Command           string `json:"command,omitempty"`
	LastActiveAt      string `json:"last_active_at,omitempty"`

	// State 是 **inventory 形状**里的状态字段（2026-10-25 实测）。
	//
	// ACC 的 runtime 清单里，每个 runtime 自带一份 agent_inventory
	//（心跳推上来的）：{"agent_id","kind","state","communication_mode","skills"}。
	// 这里的字段名是 **state**，不是 status —— 而回连读面那份是 status。
	// 两种形状都在 ACC 契约里活着，折进同一个 Status 字段。
	State string `json:"state,omitempty"`
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
	if a.Status == "" {
		// inventory 形状（心跳推上来的那份）用 state，不叫 status。
		a.Status = a.State
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

// ListRuntimeAgents 读 **运行时清单**（不是注册表）。
//
// 这一面有**两条路**，两条都要走：
//
//	① 推送面：GET /api/v2/runtime/runtimes 里，每个 runtime 自带一份
//	   agent_inventory（心跳推上来的）。
//	② 拉取面：GET /api/v2/runtime/runtimes/{runtime_id}/agents —— ACC **回连**
//	   daemon 的读面（GET {api_endpoint}/api/v1/runtime/agents，api_endpoint 由
//	   daemon 注册时广告），拿到的是更全的那份（带 pid/command/last_active_at）。
//
// ★ 为什么两条都要读（2026-10-25 实测）：
//
//	· ACC 的**注册表**（GET /api/v2/agents）在 acc-go 容器重启后是**空的**，
//	  而 agent-companion 明明在线（daemon 侧 /api/v1/runtime/agents 有 31 个）；
//	· 拉取面要求 ACC **能回连到那台机器**：ACC 主机不在本机时，daemon 广告的
//	  地址在 ACC 那边可能指向**另一台机器**，回连拿到 401/502（实测错误正文：
//	  `runtime_read_face_unreachable: read-face /api/v1/runtime/agents: status
//	  401: {"message":"missing bearer token"}`）⇒ 只读拉取面时，整张表会空掉；
//	· 推送面（agent_inventory）**不依赖回连** —— daemon 心跳里就带着活的清单
//	  （实测 31 条：zcode-cli 20 / mmcode-helper 8 / mmcode 1 / zcode 1 /
//	  opencode 1，state = running/idle/denied_by_policy）。
//
// ★ 两面**故意**不一致，所以合并规则是写死的，不是"谁后到谁赢"：
//
//	实测同一台主机上 31 条里有 **14 条矛盾**，而且**两个方向都有**：
//
//		zcode-cli-34660：推送面 running / 拉取面 offline（进程已经退出了）
//		mmcode-81930：  推送面 denied_by_policy / 拉取面 running
//
//	因此：状态以**拉取面**为准（它是请求时刻现读的存活信号，也是唯一带
//	pid/command/last_active_at 的那份）；但推送面多带一样拉取面不知道的事实
//	——**本机 allowlist 的裁决**（denied_by_policy），策略拒绝**不能**被拉取面
//	里一句 running 盖掉：把被拒的智能体说成可派单，是危险的那一侧。
//
// 回连不通**不是故障**（那台机器没广告读面、或不可达）—— 推送面拿到多少就返回多少。
func (c *Client) ListRuntimeAgents(ctx context.Context) ([]Agent, error) {
	if err := c.ready(); err != nil {
		return nil, err
	}
	runtimes, err := c.listRuntimes(ctx)
	if err != nil {
		return nil, err
	}
	byID := map[string]*agentFaces{}
	var order []string
	slot := func(key string) *agentFaces {
		f, ok := byID[key]
		if !ok {
			f = &agentFaces{}
			byID[key] = f
			order = append(order, key)
		}
		return f
	}
	for _, rt := range runtimes {
		// ① 推送面：runtime 清单里自带的 inventory
		for _, a := range rt.AgentInventory {
			a.normalize()
			key := agentKey(a)
			if key == "" {
				continue
			}
			cp := a
			slot(key).push = &cp
		}
		// ② 拉取面：ACC 回连 runtime 的读面
		agents, err := c.listRuntimeAgentsOf(ctx, rt.RuntimeID)
		if err != nil {
			// 单个 runtime 读不到（没广告读面 / 机器不可达 / 读面要鉴权）
			// 不影响其它 runtime，也不影响推送面已经拿到的清单 ——
			// 跳过即可，不能让一个连不上的机器把整张表清空。
			continue
		}
		for _, a := range agents {
			a.normalize()
			key := agentKey(a)
			if key == "" {
				continue
			}
			cp := a
			slot(key).read = &cp
		}
	}
	out := make([]Agent, 0, len(order))
	for _, k := range order {
		out = append(out, resolveAgent(*byID[k]))
	}
	return out, nil
}

// agentStateDeniedByPolicy 是清单里的"在，但不许跑"。
//
// 这是一个**独立取值**而不是复用 "offline" 或干脆不报（见 agent-companion
// 的 InventoryStateDeniedByPolicy）："offline" 会让人以为重启一下就好，
// 而省略这一条会让这台机器看起来像从没装过这个智能体。
const agentStateDeniedByPolicy = "denied_by_policy"

// agentFaces 是同一个智能体在 ACC 里的两面。
type agentFaces struct {
	// push 是心跳推上来的那一份（运行时清单里的 agent_inventory）。
	push *Agent
	// read 是 ACC 回连 daemon 读面拿到的那一份。
	read *Agent
}

// agentKey 取一份记录的身份键。两面用的是同一个 id 空间
// （agent_id / id），所以同一个智能体在两面能对上。
func agentKey(a Agent) string {
	if a.AgentID != "" {
		return a.AgentID
	}
	return a.ID
}

// resolveAgent 把同一智能体的两面合成一条记录。
//
// 规则见 ListRuntimeAgents 的注释：状态以拉取面为准（更新、且更全），
// 但推送面的策略裁决优先于任何运行状态；其余字段两面互补，谁有谁赢。
func resolveAgent(f agentFaces) Agent {
	out := Agent{}
	if f.push != nil {
		out = *f.push
	}
	if f.read != nil {
		read := *f.read
		// 拉取面独有的字段（pid/command/last_active_at/capabilities）补上。
		fillEmpty(&out, read)
		// 存活信号以拉取面为准 —— 它是请求时刻现读的。
		if read.Status != "" {
			out.Status = read.Status
		}
	}
	// 策略裁决优先于任何运行状态：被拒的智能体不能被说成可派单。
	if f.push != nil && f.push.State == agentStateDeniedByPolicy {
		out.Status = agentStateDeniedByPolicy
	}
	return out
}

// fillEmpty 把 src 的非空字段填进 dst 尚未有值的字段（互补，不是覆盖）。
func fillEmpty(dst *Agent, src Agent) {
	if dst.ID == "" {
		dst.ID = src.ID
	}
	if dst.Name == "" {
		dst.Name = src.Name
	}
	if dst.Type == "" {
		dst.Type = src.Type
	}
	if dst.Status == "" {
		dst.Status = src.Status
	}
	if dst.LastSeenAt == "" {
		dst.LastSeenAt = src.LastSeenAt
	}
	if len(dst.Capabilities) == 0 {
		dst.Capabilities = src.Capabilities
	}
	if dst.AgentID == "" {
		dst.AgentID = src.AgentID
	}
	if dst.Kind == "" {
		dst.Kind = src.Kind
	}
	if dst.CommunicationMode == "" {
		dst.CommunicationMode = src.CommunicationMode
	}
	if dst.PID == 0 {
		dst.PID = src.PID
	}
	if dst.Command == "" {
		dst.Command = src.Command
	}
	if dst.LastActiveAt == "" {
		dst.LastActiveAt = src.LastActiveAt
	}
	if dst.State == "" {
		dst.State = src.State
	}
}

// runtimeRecord 是一条已注册 runtime 的记录。
type runtimeRecord struct {
	RuntimeID string `json:"runtime_id"`
	// AgentInventory 是 runtime **自己推上来**的智能体清单（心跳里带的）。
	// 它不依赖 ACC 回连 ⇒ ACC 连不上这台机器的读面时，活的清单仍然在这里。
	AgentInventory []Agent `json:"agent_inventory"`
}

// listRuntimes 读已注册 runtime 的记录（含各自推上来的 agent_inventory）。
func (c *Client) listRuntimes(ctx context.Context) ([]runtimeRecord, error) {
	resp, err := c.http.Get(ctx, "/api/v2/runtime/runtimes", nil)
	if err != nil {
		return nil, fmt.Errorf("accruntime: list runtimes: %w", err)
	}
	raw, err := readAll(resp)
	if err != nil {
		return nil, fmt.Errorf("accruntime: list runtimes: %w", err)
	}
	var doc struct {
		Data  []runtimeRecord `json:"data"`
		Items []runtimeRecord `json:"items"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("accruntime: decode runtimes: %w", err)
	}
	var out []runtimeRecord
	for _, r := range doc.Data {
		if r.RuntimeID != "" {
			out = append(out, r)
		}
	}
	for _, r := range doc.Items {
		if r.RuntimeID != "" {
			out = append(out, r)
		}
	}
	return out, nil
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

// ResolveDispatchTarget picks the (runtime_id, agent_id) pair a dispatch
// should be sent to.
//
// The runtime is the agent-companion instance registered with ACC; the agent
// is one slot inside it. ACC's dispatch endpoint needs BOTH, and the pair has
// to exist — a dispatch to an unknown runtime or agent is accepted by ACC and
// then never picked up (the command sits at status=created forever).
//
// Rules:
//   - agentID given → the runtime that pushed that agent up is used. The
//     **push face** (agent_inventory in the runtime list) is used on purpose:
//     it does not depend on ACC being able to call back to that host.
//   - neither given → the first runtime with a dispatchable agent.
//   - an agent whose state is denied_by_policy is never picked: ACC's own
//     policy refused it, and dispatching to it would produce a task that can
//     only fail.
func (c *Client) ResolveDispatchTarget(ctx context.Context, agentID, runtimeID string) (string, string, error) {
	if err := c.ready(); err != nil {
		return "", "", err
	}
	runtimes, err := c.listRuntimes(ctx)
	if err != nil {
		return "", "", err
	}
	wantAgent := strings.TrimSpace(agentID)
	wantRuntime := strings.TrimSpace(runtimeID)
	for _, rt := range runtimes {
		if wantRuntime != "" && rt.RuntimeID != wantRuntime {
			continue
		}
		for _, a := range rt.AgentInventory {
			a.normalize()
			id := strings.TrimSpace(a.AgentID)
			if id == "" {
				id = strings.TrimSpace(a.ID)
			}
			if id == "" {
				continue
			}
			if wantAgent != "" && id != wantAgent {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(a.State), "denied_by_policy") {
				continue
			}
			return rt.RuntimeID, id, nil
		}
	}
	if wantAgent != "" {
		return "", "", fmt.Errorf("%w: runtime 没有可派单的 %q", ErrNoDispatchableAgent, wantAgent)
	}
	return "", "", ErrNoDispatchableAgent
}

// EscapePathSegment 把 id 里的路径分隔符转义掉，防止 task_id 形如
// "acs-macbook-30b:dispatch_xxx" 时把 URL 结构带偏。
func EscapePathSegment(s string) string {
	return url.PathEscape(strings.TrimSpace(s))
}
