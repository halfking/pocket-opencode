package finance

import "testing"

// BUG-H (2026-09-30) 回归：原 amountRegex 除数字外全部可选，且取第一个匹配，
// 导致标识符里的数字抢占金额。"E2E 50 元" 被解析成 amount=2，
// 且 /api/finance/parse 返回 200，错误金额静默入账。
func TestRecognizer_AmountNotHijackedByLeadingIdentifier(t *testing.T) {
	r := NewRecognizer()
	cases := []struct {
		in   string
		want float64
	}{
		{"E2E 50 元", 50},
		{"E2E 50", 50},
		{"test2 打车 30 元", 30},
		{"v1 打车 30 块", 30},
		// 回归护栏：既有行为不能被改坏
		{"打车 32 元", 32},
		{"买菜 66 元", 66},
		{"50 元", 50},
		{"¥32", 32},
		{"$18.5", 18.5},
		// 裸数字（无符号无单位）仍要支持，但不得粘在 ASCII 字母后
		{"吃饭花了38", 38},
		{"入账1000", 1000},
		{"收款1000", 1000},
		{"项目尾款3000到账", 3000},
		// 取第一个金额的既有语义
		{"买了100块的东西，又花了50块打车", 100},
		{"花了-100块", 100},
	}
	for _, c := range cases {
		got := r.Parse(c.in)
		if got == nil {
			t.Errorf("Parse(%q) = nil, want amount %v", c.in, c.want)
			continue
		}
		if got.Amount != c.want {
			t.Errorf("Parse(%q).Amount = %v, want %v", c.in, got.Amount, c.want)
		}
	}
}

// 无法识别金额时必须返回 nil（前端据此提示"未能识别金额或收支类型"），
// 绝不能静默套用默认值把错误数据写进账本。
func TestRecognizer_NoAmountReturnsNil(t *testing.T) {
	r := NewRecognizer()
	for _, in := range []string{
		"今天天气真好",
		"今天去吃饭了",
		"乱七八糟没有数字",
		"E2E",       // 只有标识符里的数字，不构成金额
		"",          // 空输入
		"   \t\n  ", // 纯空白
		"花了0块",      // 金额为 0 视为未识别
	} {
		if got := r.Parse(in); got != nil {
			t.Errorf("Parse(%q) = %+v, want nil", in, got)
		}
	}
}
