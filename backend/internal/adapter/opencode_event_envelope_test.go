package adapter

import (
	"encoding/json"
	"testing"
)

// TestOpenCodeEventUnmarshalPropertiesEnvelope 锁定 2026-08-27 真机验证发现的
// opencode v1.14.33 事件信封漂移兼容：新格式 {type, properties:{...}} 必须归一化
// 进 Data，旧格式 {type, data:{...}} 原样保留。所有 Data 读取方（extractSessionID、
// eventBelongsToSession、session_event_broadcaster）依赖这一归一化。
func TestOpenCodeEventUnmarshalPropertiesEnvelope(t *testing.T) {
	t.Run("v1.14.33 properties 信封归一化进 Data", func(t *testing.T) {
		raw := `{"type":"message.updated","properties":{"sessionID":"ses_abc","info":{"role":"user"}}}`
		var evt OpenCodeEvent
		if err := json.Unmarshal([]byte(raw), &evt); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if evt.Type != "message.updated" {
			t.Fatalf("Type = %q, want message.updated", evt.Type)
		}
		data, ok := evt.Data.(map[string]any)
		if !ok {
			t.Fatalf("Data 类型 = %T, want map[string]any", evt.Data)
		}
		if got := data["sessionID"]; got != "ses_abc" {
			t.Fatalf("Data.sessionID = %v, want ses_abc", got)
		}
	})

	t.Run("旧格式 data 原样保留", func(t *testing.T) {
		raw := `{"id":"e1","type":"session.updated","data":{"sessionID":"ses_old"},"location":{"sessionID":"ses_old"}}`
		var evt OpenCodeEvent
		if err := json.Unmarshal([]byte(raw), &evt); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		data, ok := evt.Data.(map[string]any)
		if !ok {
			t.Fatalf("Data 类型 = %T, want map[string]any", evt.Data)
		}
		if got := data["sessionID"]; got != "ses_old" {
			t.Fatalf("Data.sessionID = %v, want ses_old", got)
		}
		if evt.Location["sessionID"] != "ses_old" {
			t.Fatalf("Location 被意外改动")
		}
	})

	t.Run("归一化后重序列化仍是 {type,data} 形状（前端 SSE 契约）", func(t *testing.T) {
		raw := `{"type":"message.part.updated","properties":{"sessionID":"ses_x","part":{"text":"hi"}}}`
		var evt OpenCodeEvent
		if err := json.Unmarshal([]byte(raw), &evt); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		out, err := json.Marshal(evt)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var round map[string]any
		if err := json.Unmarshal(out, &round); err != nil {
			t.Fatalf("re-unmarshal: %v", err)
		}
		if _, ok := round["data"].(map[string]any); !ok {
			t.Fatalf("重序列化缺少 data 字段: %s", out)
		}
		if _, ok := round["properties"]; ok {
			t.Fatalf("重序列化不应出现 properties 字段: %s", out)
		}
	})
}
