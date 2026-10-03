package server

// POP3 原文缓存的**真实数据**离线验证（2026-10-03）。
//
// 护栏 TestHandleEmailBody_POP3RawCacheAndIMAPGuard 是源码级的，它只能证明
// 「接线在那里」，证明不了「接对了」—— 两边的缓存格式并不相同：
//
//	email-bodies/     （server 层）8B UID + 1B format + base64
//	email-bodies-raw/ （POP3 层）8B UID +            base64
//
// 读错目录、或把 raw 当成 server 那份去解析，都会得到「读不出来」或
// 「解出垃圾」，而源码级护栏两者都判绿。所以这里拿**磁盘上真实的**
// email-bodies-raw 跑一遍 FileBodyCache.Get，断言拿回来的就是原始字节。
//
// 门控（语料不进仓库，且需要 data/email_master.key）：
//
//	POCKET_DIAG_POP3_RAW=<含 email_master.key 的数据目录> go test ./internal/server/ -run TestPOP3RawCache -v
//
// 目录为空 / 没解密出任何文件 ⇒ t.Fatal，不许静默 skip：一个永远 skip 的
// 真实数据测试会被下一轮读成「跑过了没问题」，而它其实一次都没跑。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/email"
)

func TestPOP3RawCache_RealCorpusRoundTrip(t *testing.T) {
	dataDir := os.Getenv("POCKET_DIAG_POP3_RAW")
	if dataDir == "" {
		t.Skip("设 POCKET_DIAG_POP3_RAW=<含 email_master.key 的数据目录> 才跑（真实语料不进仓库）")
	}
	key, err := email.EnsureMasterKey("", dataDir)
	if err != nil {
		t.Fatalf("EnsureMasterKey(%s): %v", dataDir, err)
	}
	cr, err := email.NewCrypto(key)
	if err != nil {
		t.Fatalf("NewCrypto: %v", err)
	}
	dir := filepath.Join(dataDir, "email-bodies-raw")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读不到 %s：%v", dir, err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".bin" {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		t.Fatalf("%s 下没有 .bin —— 解密步骤没产出，判据从未工作", dir)
	}

	cache := email.NewFileBodyCache(dataDir, cr)
	ok, mismatched, empty := 0, 0, 0
	for _, n := range names {
		id := n[:len(n)-len(filepath.Ext(n))]
		// uid=0 ⇒ 跳过 UID 校验，只验「能解出原文」。UID 语义由
		// body_cache_test.go 的往返用例覆盖，这里不重复。
		raw, gerr := cache.Get(id, 0)
		if gerr != nil || len(raw) == 0 {
			empty++
			t.Logf("EMPTY  %-58s err=%v", n, gerr)
			continue
		}
		// 原文必须是 RFC 5322 报文：以头字段行开头。POP3 抓下来的整封原文
		// 必然带 Received:/From:/Subject: 之类；解出乱码或空串说明格式错位。
		if !looksLikeRFC5322Raw(raw) {
			mismatched++
			t.Logf("GARBLE %-58s first=%q", n, headBytes(raw, 60))
			continue
		}
		ok++
	}
	t.Logf("真实 POP3 原文 %d 封：解出原文 %d，解不出 %d，解出但不像报文 %d", len(names), ok, empty, mismatched)
	if ok == 0 {
		t.Fatal("零个样本解出原文 —— 判据从未在工作，结论不成立")
	}
	if empty > 0 {
		t.Errorf("%d 封真实原文解不出来：读到的目录/格式与写入端不一致", empty)
	}
	if mismatched > 0 {
		t.Errorf("%d 封解出来不像 RFC 5322 报文：多半是按错格式解析（raw 层没有 format 字节）", mismatched)
	}
}

// looksLikeRFC5322Raw 判断解出来的字节是不是一封 RFC 5322 报文的头。
//
// 踩过的两个坑都在判据这一侧，不是被测代码：
//
//  1. 漏了**折叠续行**（RFC 5322 §2.2.3，以空白开头的续行），于是
//     `Authentication-Results: …` 下一行的 "\t qchina@…; dkim=pass(…)" 首字符
//     是 tab、字段名解析直接失败。49 封真报文全被报成 GARBLE。
//  2. headBytes 把控制字节转成 `.`，于是**空行变成了 "."**，`line == ""` 永远
//     不成立 → 就算头早就结束了也返回 false。修法是别在判据里改字节，打日志
//     用 %q 让 Go 自己转义。
//  3. 要求「必须遇到头结束的空行」——真邮件的头可以很长（QQ 的
//     X-QQ-XMAILINFO 追踪头几百行 base64、DKIM 签名几百字节），8KB/16KB
//     窗口里**根本没有空行**，「走完没见到空行 ⇒ false」把真报文判成乱码，
//     49 封里一度全红。
//
// 现在的判据是量化正向形状：**前 16KB 内至少 3 行合法头字段**。
// 它对真报文恒真（Received/From/Date/Subject/Message-ID/MIME-Version…），
// 对「按错格式解出来的垃圾」几乎不可能成立（行首不会出现 `字段名:` 结构）。
// 折叠续行（以空白开头）不算字段行。
func looksLikeRFC5322Raw(b []byte) bool {
	head := headBytes(b, 16*1024)
	fields := 0
	for _, line := range splitLines(head) {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			break // 头结束，字段数已定
		}
		if line[0] == ' ' || line[0] == '\t' {
			continue // 折叠续行，不是新字段
		}
		i := 0
		for i < len(line) && isAtext(line[i]) {
			i++
		}
		if i == 0 || i >= len(line) || line[i] != ':' {
			return false // 非续行、又不是字段行 ⇒ 不是报文头
		}
		fields++
	}
	return fields >= 3
}

// isAtext 按 RFC 5322 §3.2.3 的 atext 定义判断：ALPHA / DIGIT /
// "!#$%&'*+-/=?^_`{|}~"。
//
// 为什么必须是**协议定义的完整集合**而不是我随手挑的几个：第三版写的是
// `[A-Za-z-]`，结果 QQ 追踪头里的 `sesame_open: 3de0be30…` 被判成「不是头」——
// `_` 是合法 atext。于是这一行触发 return false，**整封真报文被判成乱码**。
// 字段名判据一旦比协议窄，报出来的就不是「格式不对」，而是「这不是邮件」，
// 指向完全错误的方向。真实邮件的字段名什么形状都有，宁可按协议全放。
func isAtext(c byte) bool {
	if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
		return true
	}
	return strings.IndexByte("!#$%&'*+-/=?^_`{|}~", c) >= 0
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// headBytes 取前 n 字节。**原样返回，不改任何字节** —— 判据要看到真实的 \r\n，
// 把它换成可见字符会让「空行」永远匹配不上（见 looksLikeRFC5322Raw 的坑 2）。
func headBytes(b []byte, n int) string {
	if len(b) < n {
		n = len(b)
	}
	return string(b[:n])
}

func isUpper(c byte) bool { return c >= 'A' && c <= 'Z' }
