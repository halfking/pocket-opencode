package accruntime

// ecosystem_runtime_faces_test.go — ACC 运行时清单的**两面**读取与合并。
//
// 2026-10-25 实测（真实 ACC + 真实 daemon）：
//
//	· 推送面（运行时清单里的 agent_inventory）：31 条活智能体，不依赖 ACC 回连。
//	· 拉取面（ACC 回连 daemon 的 /api/v1/runtime/agents）：502 —— ACC 回连不带
//	  bearer，daemon 的读面在 authMiddleware 之后。
//	· 两面在 31 条里有 14 条互相矛盾，且方向相反：zcode-cli 推送面 running /
//	  拉取面 offline；mmcode 推送面 denied_by_policy / 拉取面 running。
//
// 所以这组测试钉的是三件事：推送面能不能独自供数、state 会不会折成 status、
// 两面合并时谁说了算（存活信号看拉取面，策略裁决看推送面）。

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// findAgent 按 id 找一条记录。
func findAgent(t *testing.T, agents []Agent, id string) Agent {
	t.Helper()
	for _, a := range agents {
		if a.ID == id {
			return a
		}
	}
	t.Fatalf("清单里没有 %s（现有 %d 条）", id, len(agents))
	return Agent{}
}

// TestListRuntimeAgents_PushFaceSuppliesAgentsWhenReadFaceUnreachable 是这一组
// 的主回归。
//
// 只读拉取面时整张表会空掉 —— 因为 ACC 回连那台机器的读面失败（401/502），
// 而"看得到智能体"恰恰是这一面存在的理由。推送面不依赖回连，所以它必须能
// 独自供数。
//
// 修复前（只读拉取面）：这张表是空的 ⇒ 这条测试必红。
func TestListRuntimeAgents_PushFaceSuppliesAgentsWhenReadFaceUnreachable(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v2/runtime/runtimes":
			_, _ = w.Write([]byte(`{"data":[{"runtime_id":"acs-ac30-01","agent_inventory":[
				{"agent_id":"zcode-cli-34660","kind":"zcode-cli","state":"running",
				 "communication_mode":"runtime_observe_only"},
				{"agent_id":"mmcode-81930","kind":"mmcode","state":"denied_by_policy",
				 "communication_mode":"runtime_observe_only"}
			]}]}`))
		case "/api/v2/runtime/runtimes/acs-ac30-01/agents":
			// ACC 回连读面失败（实测就是这个正文）。
			http.Error(w, "runtime_read_face_unreachable: status 401", http.StatusBadGateway)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	agents, err := newEcoClient(t, ts).ListRuntimeAgents(context.Background())
	if err != nil {
		t.Fatalf("ListRuntimeAgents: %v", err)
	}
	if len(agents) != 2 {
		t.Fatalf("len(agents) = %d, want 2（读面不通不该让推送面也变空）：%+v", len(agents), agents)
	}

	// 清单形状用的是 state，不是 status —— 必须折进 status，否则下游看到一片空。
	zcode := findAgent(t, agents, "zcode-cli-34660")
	if zcode.Type != "zcode-cli" {
		t.Errorf("Type = %q, want zcode-cli", zcode.Type)
	}
	if zcode.Status != "running" {
		t.Errorf("Status = %q, want running（state 必须折成 status）", zcode.Status)
	}

	mmcode := findAgent(t, agents, "mmcode-81930")
	if mmcode.Status != "denied_by_policy" {
		t.Errorf("Status = %q, want denied_by_policy", mmcode.Status)
	}
}

// TestListRuntimeAgents_MergeRulesBetweenTheTwoFaces 钉住合并规则本身。
//
// 两面矛盾时谁说了算不是"谁后到谁赢"，而是分字段判定：
//   - 存活信号（status）以**拉取面**为准 —— 它是请求时刻现读的，比心跳那份
//     更贴近现场（心跳那份在修复前是注册时刻的冻结快照）；
//   - 策略裁决（denied_by_policy）以**推送面**为准 —— 拉取面根本不知道
//     allowlist，被拒的智能体不能被说成 running；
//   - 拉取面独有的字段（pid/command/last_active_at）补进结果。
func TestListRuntimeAgents_MergeRulesBetweenTheTwoFaces(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v2/runtime/runtimes":
			_, _ = w.Write([]byte(`{"data":[{"runtime_id":"acs-ac30-01","agent_inventory":[
				{"agent_id":"zcode-cli-34660","kind":"zcode-cli","state":"running"},
				{"agent_id":"mmcode-81930","kind":"mmcode","state":"denied_by_policy"}
			]}]}`))
		case "/api/v2/runtime/runtimes/acs-ac30-01/agents":
			_, _ = w.Write([]byte(`{"agents":[
				{"agent_id":"zcode-cli-34660","kind":"zcode-cli","status":"offline",
				 "pid":34660,"command":"/usr/local/bin/zcode",
				 "last_active_at":"2026-10-25T17:06:32+08:00",
				 "capabilities":["cli"]},
				{"agent_id":"mmcode-81930","kind":"mmcode","status":"running",
				 "pid":81930,"command":"/Applications/MiniMax Code.app/Contents/MacOS/MiniMax Code",
				 "last_active_at":"2026-10-25T17:06:32+08:00",
				 "capabilities":["editor","ide","ai_inline"]}
			]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	agents, err := newEcoClient(t, ts).ListRuntimeAgents(context.Background())
	if err != nil {
		t.Fatalf("ListRuntimeAgents: %v", err)
	}
	if len(agents) != 2 {
		t.Fatalf("len(agents) = %d, want 2（两面按 agent_id 合并，不该翻倍）：%+v", len(agents), agents)
	}

	// 存活信号：拉取面说 offline（进程已退出）⇒ 以它为准。
	zcode := findAgent(t, agents, "zcode-cli-34660")
	if zcode.Status != "offline" {
		t.Errorf("Status = %q, want offline（存活信号以拉取面为准）", zcode.Status)
	}
	// 拉取面独有的字段要补进来。
	if zcode.PID != 34660 {
		t.Errorf("PID = %d, want 34660（拉取面独有的字段必须补上）", zcode.PID)
	}
	if zcode.Command == "" {
		t.Error("Command 为空 —— 拉取面独有的字段丢了")
	}
	if zcode.LastSeenAt != "2026-10-25T17:06:32+08:00" {
		t.Errorf("LastSeenAt = %q", zcode.LastSeenAt)
	}
	if len(zcode.Capabilities) != 1 || zcode.Capabilities[0] != "cli" {
		t.Errorf("Capabilities = %v", zcode.Capabilities)
	}

	// 策略裁决：拉取面说 running，但推送面说被 allowlist 拒了 ⇒ 以推送面为准。
	mmcode := findAgent(t, agents, "mmcode-81930")
	if mmcode.Status != "denied_by_policy" {
		t.Errorf("Status = %q, want denied_by_policy（策略拒绝不能被 running 盖掉）", mmcode.Status)
	}
	if mmcode.PID != 81930 {
		t.Errorf("PID = %d, want 81930", mmcode.PID)
	}
}

// TestListRuntimeAgents_PushFaceOnlyAgentStillAppears 钉住"只在推送面出现"的
// 智能体不会因为拉取面没有它而消失。
//
// 实测 31 条里有 1 条只出现在推送面（拉取面没有对应行）。丢掉它 = 少报一个
// 还活着的智能体。
func TestListRuntimeAgents_PushFaceOnlyAgentStillAppears(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v2/runtime/runtimes":
			_, _ = w.Write([]byte(`{"data":[{"runtime_id":"acs-ac30-01","agent_inventory":[
				{"agent_id":"only-push","kind":"zcode-cli","state":"idle"}
			]}]}`))
		case "/api/v2/runtime/runtimes/acs-ac30-01/agents":
			_, _ = w.Write([]byte(`{"agents":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	agents, err := newEcoClient(t, ts).ListRuntimeAgents(context.Background())
	if err != nil {
		t.Fatalf("ListRuntimeAgents: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("len(agents) = %d, want 1：%+v", len(agents), agents)
	}
	a := findAgent(t, agents, "only-push")
	if a.Status != "idle" || a.Type != "zcode-cli" {
		t.Errorf("agent = %+v", a)
	}
}
