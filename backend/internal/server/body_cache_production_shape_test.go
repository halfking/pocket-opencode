package server

// body_cache_production_shape_test.go —— 加密正文缓存在**生产调用形态**下的命中。
//
// ## 缺陷背景（2026-10-03 实测）
//
// 写端 writeCachedEmailBody 故意把头部 UID 写成 0 作「未记录」哨兵，注释写明
// 「让 readCachedEmailBody 跳过 UID 校验」。而读端原本判断的是
// `expectedUID > 0 && prefixUID != expectedUID` —— 跳过与否看的是 **expectedUID**
// 而不是 prefixUID，于是**写 0 必然判失配**。
//
// 后果不是个别文件失效，是**整份缓存 100% 失效**：实测 data/email-bodies/
// 54 个文件的头部 UID 全为 0，而三个生产调用点全部传真实 UID：
//
//	handleEmailBody（邮件详情页）    server_assistant.go
//	发票正文增强                     server_email_invoice.go
//	summarizeBody（AI 摘要取正文）   server_email_summary.go
//
// 三处因此每次都退回 IMAP 重拉整封原文；POP3 来源的邮件取不到（详情页 502）。
//
// ## 为什么既有单测没抓到
//
// email_body_cache_test.go 里每一条读缓存的用例都传 `expectedUID = 0`，
// 而 0 恰好是让旧判断通过的那个值 —— **测的是生产从不走的路径**。
// 本文件所有判据一律用 expectedUID != 0 的生产形态，并在
// TestBodyCacheAssertionsDoNotAllUseZeroUID 里把这一点钉住。
//
// ## 负控（实测可转红）
//
//  - 把读端判断改回 `expectedUID > 0 && prefixUID != expectedUID`
//    → TestBodyCache_UnrecordedUIDHeaderStillHitsWithRealUID 转红。
//  - 把「已记录且不一致才算旧缓存」写成「一律跳过」
//    → TestBodyCache_RecordedMismatchedUIDIsStillMiss 转红。

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeCacheWithHeaderUID 造一份头部带指定 UID 的缓存（绕开写端固定写 0）。
func writeCacheWithHeaderUID(t *testing.T, srv *Server, dir, emailID string, headerUID int64, plaintext string) {
	t.Helper()
	enc, err := srv.emailCrypto.EncryptString(plaintext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	hdr := make([]byte, 9)
	binary.BigEndian.PutUint64(hdr[:8], uint64(headerUID))
	hdr[8] = bodyCacheFormatMIME
	if err := os.WriteFile(filepath.Join(dir, bodyCacheDirName, emailID+".bin"),
		append(hdr, []byte(enc)...), 0o600); err != nil {
		t.Fatalf("write cache: %v", err)
	}
}

// 生产形态：写端写的 0 哨兵 + 调用方持有的真实 UID。修好之前必然未命中。
func TestBodyCache_UnrecordedUIDHeaderStillHitsWithRealUID(t *testing.T) {
	srv, dir := newBodyCacheTestServer(t)
	ctx := context.Background()

	// 走真正的写端，确保复现的是「写端写 0」这个真实形态，而不是手工拼的。
	const emailID = "em-prod-shape"
	if err := srv.writeCachedEmailBody(ctx, emailID, []byte("真实正文"), bodyCacheFormatMIME); err != nil {
		t.Fatalf("write cache: %v", err)
	}

	// 自检：头部 UID 确实是 0（写端的哨兵），否则本用例证明不了任何事。
	raw, err := os.ReadFile(filepath.Join(dir, bodyCacheDirName, emailID+".bin"))
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	if got := int64(binary.BigEndian.Uint64(raw[:8])); got != 0 {
		t.Fatalf("写端写入的头部 UID = %d, want 0（未记录哨兵）—— 写端形态变了，本用例失去前提", got)
	}

	const realUID = int64(1298896144)
	got, err := srv.readCachedEmailBody(ctx, emailID, realUID)
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	if got == nil {
		t.Fatalf("带真实 UID（%d）读取未命中 —— 这就是生产三个调用点每天都在经历的事："+
			"每次都退回 IMAP 重拉整封原文，POP3 来源的邮件还会直接取不到", realUID)
	}
	if string(got) != "真实正文" {
		t.Errorf("hit content = %q, want %q", got, "真实正文")
	}
}

// 反向：头部**记了** UID 且与调用方不一致时，仍必须判旧缓存（陈旧性保护不能被拆掉）。
func TestBodyCache_RecordedMismatchedUIDIsStillMiss(t *testing.T) {
	srv, dir := newBodyCacheTestServer(t)
	ctx := context.Background()

	writeCacheWithHeaderUID(t, srv, dir, "em-stale", 1000, "旧正文")

	if got, err := srv.readCachedEmailBody(ctx, "em-stale", 2000); err != nil {
		t.Fatalf("read: %v", err)
	} else if got != nil {
		t.Errorf("头部 UID=1000 而调用方持有 2000 时仍命中了 %d 字节 —— "+
			"陈旧性保护被拆掉了：sync 换 UID 后会拿旧内容当新邮件的正文", len(got))
	}

	// 一致时必须命中，否则这个判据就是恒暗的。
	if got, err := srv.readCachedEmailBody(ctx, "em-stale", 1000); err != nil {
		t.Fatalf("read matching uid: %v", err)
	} else if got == nil || string(got) != "旧正文" {
		t.Errorf("UID 一致时未命中（got=%d 字节）—— 判据对「一致」这一侧失明了", len(got))
	}
}

// 生产上真实存在的形态：0 哨兵 + 任意真实 UID 一律命中。
func TestBodyCache_SentinelHeaderIgnoresAnyCallerUID(t *testing.T) {
	srv, _ := newBodyCacheTestServer(t)
	ctx := context.Background()

	const emailID = "em-sentinel"
	if err := srv.writeCachedEmailBody(ctx, emailID, []byte("哨兵正文"), bodyCacheFormatText); err != nil {
		t.Fatalf("write: %v", err)
	}
	for _, uid := range []int64{1, 42, 1298896144, 1 << 40} {
		got, err := srv.readCachedEmailBody(ctx, emailID, uid)
		if err != nil {
			t.Fatalf("uid=%d: %v", uid, err)
		}
		if got == nil || string(got) != "哨兵正文" {
			t.Fatalf("uid=%d 时未命中（got=%d 字节）—— 0 哨兵本该与调用方 UID 无关", uid, len(got))
		}
	}
}

// 判据自检：读缓存的用例**不许**全用 expectedUID=0。
//
// 这条是本文件存在的理由：旧缺陷能活这么久，正是因为既有断言全走 0。
// 若有人把新判据也写成 expectedUID=0，本条转红。
func TestBodyCacheAssertionsDoNotAllUseZeroUID(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(".", "*.go"))
	if err != nil || len(files) == 0 {
		t.Skipf("no go files here: %v", err)
	}
	var zeroLiteral, uidBearing, total int
	for _, f := range files {
		b, rerr := os.ReadFile(f)
		if rerr != nil {
			continue
		}
		src := string(b)
		for i := 0; ; {
			j := strings.Index(src[i:], "readCachedEmailBody(")
			if j < 0 {
				break
			}
			// 调用参数里可能嵌 `newBodyCacheTestServer(t)` 这类带括号的表达式，
			// 所以不能按第一个 ')' 截断 —— 那会只看到半个调用（第一版正则就栽在
			// 这里：它报「字面量 0 = 0」，而 email_body_cache_test.go 明明有一处
			// 传 0，漏匹配让这条判据恒暗）。改为取到**行尾**为止。
			start := i + j + len("readCachedEmailBody(")
			end := strings.IndexByte(src[start:], '\n')
			if end < 0 {
				break
			}
			call := src[start : start+end]
			total++
			switch {
			case strings.HasSuffix(strings.TrimSpace(call), ", 0)") || strings.HasSuffix(strings.TrimSpace(call), ",0)"):
				zeroLiteral++
			case strings.Contains(call, "UID") || strings.Contains(call, "uid"):
				uidBearing++
			}
			i = start
		}
	}
	t.Logf("readCachedEmailBody 调用点：共 %d，字面量 0 = %d，实参带 UID = %d",
		total, zeroLiteral, uidBearing)
	if total == 0 {
		t.Fatal("一个 readCachedEmailBody 调用点都没扫到 —— 匹配逻辑坏了，" +
			"这条判据正在恒暗（它本该在「全用 0」时转红）")
	}
	if zeroLiteral == 0 {
		t.Errorf("扫到 %d 处调用、却一处字面量 0 都没有 —— 匹配逻辑多半坏了："+
			"email_body_cache_test.go 里的 legacy 用例明明传的是 0", total)
	}
	if uidBearing == 0 {
		t.Errorf("全部 %d 处调用都传字面量 0 —— 这正是上一版缺陷能长期存活的原因"+
			"（测的是生产从不走的路径）。生产调用点传的是 em.UID / row.UID。", total)
	}
}
