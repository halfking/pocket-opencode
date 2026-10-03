package server

import "testing"

// 网关兜底分类器的输出解析回归。
//
// 背景：邮件「自动归纳整理」原本只依赖仓外的 kxmemory 服务，没配
// POCKET_KXMEMORY_BASE_URL 时 /api/emails/classify 一律 503。现在补了
// LLM 网关兜底，而这条链路最脆的一环就是「模型输出能不能被解析出来」——
// 模型多一句寒暄、多一层代码块围栏、漏一个 importance 字段，整条自动归纳
// 就会静默失效。所以这里逐个形态锁死。
func TestParseGatewayClassification_PlainJSON(t *testing.T) {
	c, imp, sum, act, ok := parseGatewayClassification(
		`{"category":"bill","importance":"high","summary":"9 月对账单已出","suggested_action":"核对金额"}`)
	if !ok {
		t.Fatal("应当解析成功")
	}
	if c != "bill" {
		t.Errorf("category = %q，期望 bill", c)
	}
	if imp != "high" {
		t.Errorf("importance = %q，期望 high", imp)
	}
	if sum != "9 月对账单已出" {
		t.Errorf("summary = %q", sum)
	}
	if act != "核对金额" {
		t.Errorf("suggested_action = %q", act)
	}
}

func TestParseGatewayClassification_ToleratesCodeFence(t *testing.T) {
	in := "```json\n{\"category\":\"work\",\"importance\":\"medium\",\"summary\":\"变更通知\"}\n```"
	c, _, _, _, ok := parseGatewayClassification(in)
	if !ok || c != "work" {
		t.Fatalf("带 ```json 围栏应能解析，实际 ok=%v category=%q", ok, c)
	}
	// 无语言标注的裸围栏也要认
	in2 := "```\n{\"category\":\"spam\",\"importance\":\"low\",\"summary\":\"广告\"}\n```"
	c2, _, _, _, ok2 := parseGatewayClassification(in2)
	if !ok2 || c2 != "spam" {
		t.Fatalf("裸 ``` 围栏应能解析，实际 ok=%v category=%q", ok2, c2)
	}
}

func TestParseGatewayClassification_ToleratesSurroundingProse(t *testing.T) {
	in := "好的，分类结果如下：\n{\"category\":\"notification\",\"importance\":\"low\",\"summary\":\"系统提醒\"}\n希望有帮助！"
	c, imp, _, _, ok := parseGatewayClassification(in)
	if !ok || c != "notification" || imp != "low" {
		t.Fatalf("前后带寒暄应仍能解析，实际 ok=%v category=%q importance=%q", ok, c, imp)
	}
}

func TestParseGatewayClassification_AliasesAndWhitelist(t *testing.T) {
	// 广告别名：categoryAliases 里 ad/ads/promo → marketing
	c, _, _, _, ok := parseGatewayClassification(`{"category":"ads","importance":"low","summary":"x"}`)
	if !ok || c != "marketing" {
		t.Fatalf("别名应归一到 marketing，实际 %q ok=%v", c, ok)
	}
	// 未知值：NormalizeCategory 一律归 personal，保留而不是整封失败
	c2, _, _, _, ok2 := parseGatewayClassification(`{"category":"weird-thing","importance":"low","summary":"x"}`)
	if !ok2 || c2 != "personal" {
		t.Fatalf("未知 category 应兜底 personal，实际 %q ok=%v", c2, ok2)
	}
}

func TestParseGatewayClassification_MissingImportanceDefaultsMedium(t *testing.T) {
	// 只缺次要字段不应让整封分类失败
	c, imp, _, _, ok := parseGatewayClassification(`{"category":"work","summary":"x"}`)
	if !ok || c != "work" {
		t.Fatalf("缺 importance 仍应解析成功，实际 ok=%v category=%q", ok, c)
	}
	if imp != "medium" {
		t.Errorf("importance = %q，期望兜底 medium", imp)
	}
	// 返回顺序是 (category, importance, summary, action, ok)
	_, imp2, _, _, _ := parseGatewayClassification(`{"category":"work","importance":"URGENT!!"}`)
	if imp2 != "medium" {
		t.Errorf("非法 importance = %q，期望兜底 medium", imp2)
	}
}

func TestParseGatewayClassification_RejectsUnusableOutput(t *testing.T) {
	cases := map[string]string{
		"空串":          "",
		"纯文本":         "我觉得这封邮件挺重要的",
		"截断的 JSON":    `{"category":"bil`,
		"没有 category": `{"importance":"high","summary":"x"}`,
		"category 为空": `{"category":"","importance":"high"}`,
	}
	for name, in := range cases {
		if _, _, _, _, ok := parseGatewayClassification(in); ok {
			t.Errorf("%s：不应解析成功，实际 ok=true", name)
		}
	}
}

func TestParseGatewayClassification_TruncatesLongSummary(t *testing.T) {
	long := ""
	for i := 0; i < 200; i++ {
		long += "字"
	}
	_, _, sum, _, ok := parseGatewayClassification(
		`{"category":"work","importance":"low","summary":"` + long + `"}`)
	if !ok {
		t.Fatal("应当解析成功")
	}
	if n := len([]rune(sum)); n > 60 {
		t.Errorf("摘要长度 %d 超过 60 字上限", n)
	}
}

// firstNonEmptyStr：发件人显示名缺失时要退回地址。
func TestFirstNonEmptyStr(t *testing.T) {
	if got := firstNonEmptyStr("", "  ", "a@b.c"); got != "a@b.c" {
		t.Errorf("got %q", got)
	}
	if got := firstNonEmptyStr("名字", "a@b.c"); got != "名字" {
		t.Errorf("got %q", got)
	}
	if got := firstNonEmptyStr("", ""); got != "" {
		t.Errorf("got %q", got)
	}
}
