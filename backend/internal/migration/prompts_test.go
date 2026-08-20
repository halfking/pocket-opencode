package migration

import (
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/adapter"
	"github.com/halfking/pocket-opencode/backend/internal/model"
)

func TestBuildPrompts_AllTemplates(t *testing.T) {
	brief := &model.SessionResumeBrief{
		SessionID:    "ses_test123",
		InstanceID:   "inst_test",
		Title:        "重构认证模块",
		CurrentState: "已完成JWT迁移，测试通过",
		NextAction:   "更新文档并提交PR",
		Decisions:    []string{"使用HS256", "密钥存env"},
		ChangedFiles: []string{"auth/jwt.go", "auth/middleware.go"},
		Blockers:     []string{"测试环境PG未配"},
		Summary:      "将JWT从RS256迁移到HS256",
	}

	prompt := BuildPrompts(brief, []string{"env_sync", "task_resume", "result_verify", "acc_report"})

	// 验证头部
	if prompt == "" {
		t.Fatal("BuildPrompts returned empty string")
	}

	// 验证所有模板都包含
	checks := []string{
		"任务迁移续接",
		"ses_test123",
		"环境同步",
		"任务续接",
		"已完成JWT迁移",
		"更新文档并提交PR",
		"成果验证",
		"auth/jwt.go",
		"ACC 汇报",
	}
	for _, check := range checks {
		if !contains(prompt, check) {
			t.Errorf("prompt missing expected content: %q", check)
		}
	}
}

func TestBuildPrompts_PartialTemplates(t *testing.T) {
	brief := &model.SessionResumeBrief{
		SessionID:  "ses_test",
		Title:      "测试",
		NextAction: "下一步",
	}

	// 只选 env_sync
	prompt := BuildPrompts(brief, []string{"env_sync"})
	if !contains(prompt, "环境同步") {
		t.Error("env_sync template missing")
	}
	if contains(prompt, "任务续接") {
		t.Error("task_resume should not be included")
	}
}

func TestBuildPrompts_NilBrief(t *testing.T) {
	// 不应 panic
	prompt := BuildPrompts(nil, []string{"env_sync"})
	if prompt == "" {
		t.Error("BuildPrompts with nil brief should still return non-empty")
	}
}

func TestBuildPrompts_SummaryFallback(t *testing.T) {
	// NextAction 为空时，应回退到 Summary（与 TS 端对齐）
	brief := &model.SessionResumeBrief{
		SessionID: "ses_test",
		Summary:   "这是摘要内容",
	}

	prompt := BuildPrompts(brief, []string{"task_resume"})
	if !contains(prompt, "这是摘要内容") {
		t.Error("task_resume should fallback to Summary when NextAction is empty")
	}
	if !contains(prompt, "请根据摘要判断下一步") {
		t.Error("task_resume should include summary guidance text")
	}
}

func TestRoleFromData(t *testing.T) {
	tests := []struct {
		name string
		data map[string]interface{}
		want string
	}{
		{"assistant", map[string]interface{}{"info": map[string]interface{}{"role": "assistant"}}, "assistant"},
		{"user", map[string]interface{}{"info": map[string]interface{}{"role": "user"}}, "user"},
		{"missing info", map[string]interface{}{"parts": []interface{}{}}, ""},
		{"nil data", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := roleFromData(tt.data)
			if got != tt.want {
				t.Errorf("roleFromData() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractTextFromData(t *testing.T) {
	data := map[string]interface{}{
		"parts": []interface{}{
			map[string]interface{}{"type": "text", "text": "hello "},
			map[string]interface{}{"type": "tool", "text": "ignored"},
			map[string]interface{}{"type": "text", "text": "world"},
		},
	}
	got := extractTextFromData(data)
	if got != "hello world" {
		t.Errorf("extractTextFromData() = %q, want %q", got, "hello world")
	}
}

func TestSummarizeLastTurnFromAdapter(t *testing.T) {
	msgs := []adapter.OpenCodeMessage{
		{Data: map[string]interface{}{"info": map[string]interface{}{"role": "user"}, "parts": []interface{}{map[string]interface{}{"type": "text", "text": "question"}}}},
		{Data: map[string]interface{}{"info": map[string]interface{}{"role": "assistant"}, "parts": []interface{}{map[string]interface{}{"type": "text", "text": "answer"}}}},
	}
	got := summarizeLastTurnFromAdapter(msgs)
	if got != "answer" {
		t.Errorf("summarizeLastTurnFromAdapter() = %q, want %q", got, "answer")
	}
}

func TestSummarizeLastTurnFromAdapter_Truncation(t *testing.T) {
	longText := ""
	for i := 0; i < 600; i++ {
		longText += "x"
	}
	msgs := []adapter.OpenCodeMessage{
		{Data: map[string]interface{}{"info": map[string]interface{}{"role": "assistant"}, "parts": []interface{}{map[string]interface{}{"type": "text", "text": longText}}}},
	}
	got := summarizeLastTurnFromAdapter(msgs)
	if len(got) > 503 { // 500 + "..."
		t.Errorf("summarizeLastTurnFromAdapter() not truncated, len=%d", len(got))
	}
	if !contains(got, "...") {
		t.Error("truncated text should end with ...")
	}
}

func TestOriginPriority(t *testing.T) {
	tests := []struct {
		origin string
		want   int
	}{
		{"registered", 3},
		{"discovered", 2},
		{"static", 1},
		{"unknown", 0},
		{"", 0},
	}
	for _, tt := range tests {
		got := originPriority(tt.origin)
		if got != tt.want {
			t.Errorf("originPriority(%q) = %d, want %d", tt.origin, got, tt.want)
		}
	}
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}
