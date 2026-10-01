package kxmemory

// classify_action_reason_test.go — 分类响应里的 action_reason 必须被解析出来。
//
// 缺陷（2026-10-01 全字段对账时查出）：响应契约
// docs/2026-07-02-kxmemory-api-contract.md 的示例明写返回 action_reason，
// 但 EmailClassificationResult 结构体**没有这个字段**。Go 的 encoding/json
// 对未知字段静默丢弃、不报错，于是服务端一切正常、没有任何日志，
// 而 emails.action_reason 在真库里 162 封已分类邮件上**全为空**。
//
// 这类缺陷单靠「接口能跑通」永远发现不了——必须断言**具体字段**被解出来。
// 所以本文件按契约文档的原文 JSON 逐字段钉死。
//
// 负控：删掉 DTO 的 ActionReason 字段 -> 本文件转红。

import (
	"encoding/json"
	"testing"
)

// 与 docs/2026-07-02-kxmemory-api-contract.md 响应示例同形。
const contractClassifyResponse = `{
  "results": [
    {
      "email_id": "em-xxx",
      "category": "work",
      "importance": "high",
      "summary": "张经理要求周五前确认 Q3 预算",
      "suggested_action": "reply",
      "action_reason": "包含截止日期且需回复确认"
    }
  ]
}`

// 契约里的每个字段都必须真的解出来，尤其是 action_reason。
func TestClassifyEmailsResponse_ParsesAllContractFields(t *testing.T) {
	var resp ClassifyEmailsResponse
	if err := json.Unmarshal([]byte(contractClassifyResponse), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("results = %d, want 1", len(resp.Results))
	}
	r := resp.Results[0]
	if r.EmailID != "em-xxx" {
		t.Errorf("email_id = %q", r.EmailID)
	}
	if r.Category != "work" {
		t.Errorf("category = %q", r.Category)
	}
	if r.Importance != "high" {
		t.Errorf("importance = %q", r.Importance)
	}
	if r.Summary != "张经理要求周五前确认 Q3 预算" {
		t.Errorf("summary = %q", r.Summary)
	}
	if r.SuggestedAction != "reply" {
		t.Errorf("suggested_action = %q", r.SuggestedAction)
	}
	if r.ActionReason != "包含截止日期且需回复确认" {
		t.Fatalf("action_reason = %q —— 契约里有这个字段；DTO 漏掉时 encoding/json "+
			"静默丢弃，服务端不报错但依据永远存不进库", r.ActionReason)
	}
}

// 契约未给 action_reason 时应为空串（而不是残留上一次的值或报错）。
func TestClassifyEmailsResponse_MissingActionReasonIsEmpty(t *testing.T) {
	var resp ClassifyEmailsResponse
	raw := `{"results":[{"email_id":"em-1","category":"work","importance":"low","summary":"s"}]}`
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("results = %d, want 1", len(resp.Results))
	}
	if resp.Results[0].ActionReason != "" {
		t.Errorf("缺失字段应为空串，实际 %q", resp.Results[0].ActionReason)
	}
}
