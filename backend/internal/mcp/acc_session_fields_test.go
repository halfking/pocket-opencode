package mcp

// acc_session_fields_test.go — 回归：ACC 的 acc_list_sessions 把
// id / agent_session_id / gw_session_id / dispatch_id 放在**顶层**
// （acc-go internal/sessionmanager.SessionRecord），而旧的 AccSession 结构体
// 读的是 "session_id" / "metadata" —— 其中 "metadata" ACC 从不返回。
//
// 后果不是"少几个字段"，而是会话标识全丢：session-bundle 实测返回过
// `{"current":[{"id":"", ...}]}`，会话点不开。
//
// 下面用**真实抓到的 ACC 响应**（2026-10-22 实测本机 ACC :4101）锁死映射。

import (
	"encoding/json"
	"testing"
)

// accSessionSample 是 ACC 真实返回的一条 acc_list_sessions 记录。
const accSessionSample = `{
  "id": "156b2830-98f8-4f04-9678-1ee373dea05e",
  "gateway_type": "custom-agent",
  "task_id": "acs-macbook-30b:dispatch_b4e8f48b-3ba1-4373-aec3-0b0161c4f33f",
  "input": "ReClaw code-review/start: wf-ec",
  "output": "run failed: prompt_failed",
  "input_tokens": 0,
  "output_tokens": 0,
  "agent_session_id": "ses_ece2e86ffffeY1EcUq2uB2OtVP",
  "gw_session_id": "gw_dispatch_65dcd3c3-efd0-4c06-9952-9c5f0a0416c0",
  "dispatch_id": "dispatch_65dcd3c3-efd0-4c06-9952-9c5f0a0416c0",
  "tenant_id": "tenant-a",
  "created_at": "2026-10-12T20:32:55.469521Z"
}`

func TestAccSession_ParsesTopLevelACCFields(t *testing.T) {
	var sess AccSession
	if err := json.Unmarshal([]byte(accSessionSample), &sess); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// 这几个是"会话能不能被点开/续跑"的关键字段。
	if sess.ID != "156b2830-98f8-4f04-9678-1ee373dea05e" {
		t.Errorf("ID = %q, want 156b2830-...", sess.ID)
	}
	if sess.AgentSessionID != "ses_ece2e86ffffeY1EcUq2uB2OtVP" {
		t.Errorf("AgentSessionID = %q, want ses_ece2e86...", sess.AgentSessionID)
	}
	if sess.GwSessionID != "gw_dispatch_65dcd3c3-efd0-4c06-9952-9c5f0a0416c0" {
		t.Errorf("GwSessionID = %q", sess.GwSessionID)
	}
	if sess.DispatchID != "dispatch_65dcd3c3-efd0-4c06-9952-9c5f0a0416c0" {
		t.Errorf("DispatchID = %q", sess.DispatchID)
	}
	if sess.TaskID != "acs-macbook-30b:dispatch_b4e8f48b-3ba1-4373-aec3-0b0161c4f33f" {
		t.Errorf("TaskID = %q", sess.TaskID)
	}
	if sess.CreatedAt != "2026-10-12T20:32:55.469521Z" {
		t.Errorf("CreatedAt = %q", sess.CreatedAt)
	}
	if sess.Output != "run failed: prompt_failed" {
		t.Errorf("Output = %q", sess.Output)
	}
	if sess.TenantID != "tenant-a" {
		t.Errorf("TenantID = %q", sess.TenantID)
	}
	if sess.GatewayType != "custom-agent" {
		t.Errorf("GatewayType = %q", sess.GatewayType)
	}
	if sess.Input != "ReClaw code-review/start: wf-ec" {
		t.Errorf("Input = %q", sess.Input)
	}
}

// 老上报格式：字段裹在 metadata 里。ACC 现版本不再返回它，但历史数据里
// 可能还有 —— 解析不能因此报错，且兜底字段仍应可读。
func TestAccSession_LegacyMetadataFormatStillDecodes(t *testing.T) {
	const legacy = `{
	  "id": "legacy-1",
	  "task_id": "t-9",
	  "input": "hi",
	  "metadata": {"agent_session_id":"ses_old","gw_session_id":"gw_old","agent_kind":"pi"}
	}`
	var sess AccSession
	if err := json.Unmarshal([]byte(legacy), &sess); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if sess.ID != "legacy-1" {
		t.Errorf("ID = %q, want legacy-1", sess.ID)
	}
	if len(sess.Metadata) == 0 {
		t.Fatal("Metadata should still be captured for legacy payloads")
	}
	var meta struct {
		AgentSessionID string `json:"agent_session_id"`
	}
	if err := json.Unmarshal(sess.Metadata, &meta); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if meta.AgentSessionID != "ses_old" {
		t.Errorf("metadata.agent_session_id = %q, want ses_old", meta.AgentSessionID)
	}
}
