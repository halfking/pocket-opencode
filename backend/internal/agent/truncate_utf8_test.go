package agent

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// truncateStr 必须**永远不会**切出非法 UTF-8。
//
// 为什么这道断言值得存在：被截断的三条产物里，有两条会入库、一条会 JSON
// 编码后送到前端。2026-10-02 实测 PostgreSQL 对非法 UTF-8 的反应是
// 直接拒绝写入（SQLSTATE 22021），而 Go 的 json.Marshal 会把非法字节
// 替换成 U+FFFD（界面上的「�」）。两者都不是"显示难看"，是功能坏了。
//
// 旧实现是 s[:maxLen-3] + "..."，中文一个字 3 字节，落在字中间就切坏了。
func TestTruncateStr_NeverSplitsRune(t *testing.T) {
	// 覆盖各种长度：1~4 字节的字符（ASCII / 拉丁扩展 / 常用汉字 / 生僻字）
	corpus := []string{
		strings.Repeat("a", 500),
		strings.Repeat("é", 500),
		strings.Repeat("事", 500),
		strings.Repeat("𠀀", 500), // 4 字节，生僻字区
		strings.Repeat("a事𠀀é", 200),
		"这是一封来自客户的邮件，关于上季度对账单与发票开具事宜，请查收附件。",
	}
	for _, maxLen := range []int{4, 5, 6, 7, 8, 9, 10, 17, 33, 100, 200, 500} {
		for ci, s := range corpus {
			got := truncateStr(s, maxLen)
			if !utf8.ValidString(got) {
				t.Errorf("maxLen=%d corpus[%d]: 截断结果不是合法 UTF-8，末 6 字节 = % x",
					maxLen, ci, tailBytes(got, 6))
			}
			if len(got) > maxLen && maxLen >= 4 {
				t.Errorf("maxLen=%d corpus[%d]: 结果 %d 字节，超出上限",
					maxLen, ci, len(got))
			}
		}
	}
}

// TestTruncateStr_ActuallyTruncates 是**对照组**。
//
// 没有它，上面那条可能在 truncateStr 变成"直接返回原串"时也照样绿
// ——那就等于把截断功能悄悄废掉了。
func TestTruncateStr_ActuallyTruncates(t *testing.T) {
	s := strings.Repeat("事", 500)
	got := truncateStr(s, 50)
	if len(got) >= len(s) {
		t.Fatalf("没有截断：输入 %d 字节，输出 %d 字节", len(s), len(got))
	}
	if len(got) > 50 {
		t.Errorf("输出 %d 字节，超过上限 50", len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("截断后应保留省略号，实际 = %q", got)
	}
}

// TestTruncateStr_ShortInputUnchanged 确认没超长时原样返回。
func TestTruncateStr_ShortInputUnchanged(t *testing.T) {
	for _, s := range []string{"", "短", "abc", strings.Repeat("事", 10)} {
		if got := truncateStr(s, 200); got != s {
			t.Errorf("未超长却被改动：%q -> %q", s, got)
		}
	}
}

func tailBytes(s string, n int) []byte {
	b := []byte(s)
	if len(b) <= n {
		return b
	}
	return b[len(b)-n:]
}
