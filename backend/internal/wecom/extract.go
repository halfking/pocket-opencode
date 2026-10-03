package wecom

import (
	"regexp"
	"sort"
	"strings"
)

// encryptRe 是 extractEncrypt 的兜底解析。
//
// 刻意写得宽松：<Encrypt> 与 </Encrypt> 之间可能是 CDATA、可能带换行与缩进。
// 捕获组只取标签内的原始内容（含可能的 CDATA 包裹由调用方处理）。
var encryptRe = regexp.MustCompile(`(?is)<Encrypt>\s*(?:<!\[CDATA\[)?(.*?)(?:\]\]>)?\s*</Encrypt>`)

func regexExtractEncrypt(s string) string {
	m := encryptRe.FindStringSubmatch(s)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}

// assertSortedStringsIsDeterministic 不是导出 API，是给测试用的自检：
// sort.Strings 对相同输入必须给出相同顺序，否则 Sign 的实现不可复现。
//
// 抽成函数而不是让测试直接 import sort，是为了在实现与断言之间只留一个改动点。
func assertSortedStringsIsDeterministic(parts []string) []string {
	out := append([]string(nil), parts...)
	sort.Strings(out)
	return out
}
