package server

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestParseNoteSummaryPayload 锁住「笔记总结能抽出行动项」这条新能力。
//
// 背景（2026-10-06）：需求「录音时即时总结，并把一些时间点自动加入计划日程」
// 在随手记侧此前完全没有落点 —— 服务端只回一个 summary 字符串，前端拿不到
// 任何期限，「明天下午三点」这类时间点在语音笔记里直接消失。
func TestParseNoteSummaryPayload_ExtractsActionItems(t *testing.T) {
	raw := `{"summary":"讨论了结算上线风险。","action_items":[
		{"text":"补回滚脚本","assignee":"张三","due":"明天下午三点"},
		{"text":"通知客服","due":"周五上午10点"}
	]}`
	summary, items := parseNoteSummaryPayload(raw)

	if summary != "讨论了结算上线风险。" {
		t.Fatalf("summary=%q", summary)
	}
	if len(items) != 2 {
		t.Fatalf("期望 2 条行动项，实际 %d：%+v", len(items), items)
	}
	if items[0].Text != "补回滚脚本" || items[0].Assignee != "张三" || items[0].Due != "明天下午三点" {
		t.Fatalf("第 1 条解析错：%+v", items[0])
	}
	// due 必须保留**用户原话**：后端与用户设备可能不在同一时区，
	// 在这里换算成时间戳会把「明天下午三点」算错时区。
	if items[1].Due != "周五上午10点" {
		t.Fatalf("due 必须保留中文原话，实际 %q", items[1].Due)
	}
	if items[1].Assignee != "" {
		t.Fatalf("未给负责人应为空，实际 %q", items[1].Assignee)
	}
}

// 解析失败必须把模型原文当 summary 返回。
//
// ★ 这条是本函数里**最要紧**的容错：改动前提示词就要纯文本。若这里返回
// 空 summary，一次格式抖动就会让用户已经写好的语音笔记「总结消失」——
// 一次字段格式的失败，代价远大于少几个行动项。
func TestParseNoteSummaryPayload_FallsBackToRawText(t *testing.T) {
	for _, raw := range []string{
		"这是一段普通的中文总结。",
		"",
		"```json\n{\"summary\":",  // 截断的 JSON
	} {
		summary, items := parseNoteSummaryPayload(raw)
		if summary != raw {
			t.Fatalf("解析失败时 summary 必须回落到原文：得到 %q，期望 %q", summary, raw)
		}
		if len(items) != 0 {
			t.Fatalf("解析失败时不应编造行动项：%+v", items)
		}
	}
}

// markdown 代码围栏：模型很爱加，extractJSON 负责剥掉。
func TestParseNoteSummaryPayload_StripsCodeFence(t *testing.T) {
	raw := "```json\n{\"summary\":\"结论：先做灰度。\",\"action_items\":[{\"text\":\"排灰度计划\",\"due\":\"明天\"}]}\n```"
	summary, items := parseNoteSummaryPayload(raw)
	if summary != "结论：先做灰度。" {
		t.Fatalf("summary=%q", summary)
	}
	if len(items) != 1 || items[0].Text != "排灰度计划" {
		t.Fatalf("items=%+v", items)
	}
}

// 空 text 的条目必须丢弃：一条没有内容的待办在列表里是纯噪声。
func TestParseNoteSummaryPayload_DropsBlankText(t *testing.T) {
	raw := `{"summary":"s","action_items":[{"text":"   "},{"text":"真的一条"},{"text":""}]}`
	_, items := parseNoteSummaryPayload(raw)
	if len(items) != 1 || items[0].Text != "真的一条" {
		t.Fatalf("items=%+v", items)
	}
}

// 无 action_items 字段时必须是空数组而不是 nil：
// JSON 序列化成 null 会让前端的 `action_items` 拿到 null。
func TestParseNoteSummaryPayload_MissingFieldMarshalsAsArray(t *testing.T) {
	summary, items := parseNoteSummaryPayload(`{"summary":"只有摘要"}`)
	if summary != "只有摘要" {
		t.Fatalf("summary=%q", summary)
	}
	b, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "[]" {
		t.Fatalf("序列化成 %s，期望 [] （null 会让前端拿到 null）", b)
	}
}

// 负控：确认解析结果真的能被前端 normalizeActionItems 消费。
// 这里只验字段名与会议侧一致（前端那份 normalize 不认 snake_case 之外的形式）。
func TestParseNoteSummaryPayload_FieldsMatchMeetingContract(t *testing.T) {
	_, items := parseNoteSummaryPayload(`{"summary":"s","action_items":[{"text":"t","assignee":"a","due":"d"}]}`)
	if len(items) != 1 {
		t.Fatalf("items=%+v", items)
	}
	b, err := json.Marshal(items[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"text"`, `"assignee"`, `"due"`} {
		if !strings.Contains(string(b), key) {
			t.Fatalf("序列化结果缺 %s：%s", key, b)
		}
	}
}
