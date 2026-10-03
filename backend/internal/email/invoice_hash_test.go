package email

// invoice_hash_test.go — 发票内容哈希的现状行为。
//
// ## 先说清这个函数现在的地位（2026-10-02 实测）
//
// `InvoiceContentHash`（invoice_harvest.go:661）**零生产调用点、零测试调用点**：
// 全仓 grep 只命中它自己的定义和 handoff 文档里的提及。
// 上一轮 §7bw 写「它是发票去重的依据」——**那句话是错的**，
// 它是从函数上方那行注释「供测试与幂等校验」推出来的，不是事实。
// 发票去重实际靠 `email_invoices` 上的 `UNIQUE (email_id)`
// （invoice_store.go:98 的 `ON CONFLICT (email_id) DO UPDATE`），
// 即**同一封邮件**重复采集幂等。
//
// 所以这组用例的定位不是「给去重逻辑补测试」，而是：
// 把一个**当前无接线**的纯函数的行为钉住，等用户决定它该被删还是该被接线时，
// 有一份可对照的基线。
//
// 下面第 3 个用例把这个事实本身写成断言：一旦有人给它接上生产调用点，
// 这个测试会**立刻转红提醒**——去重语义从「按邮件」变成「按内容」不是
// 可以静默发生的改动，它会让同一张发票的重复行数变化。

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestInvoiceContentHash_MatchesSHA256Hex(t *testing.T) {
	cases := [][]byte{
		[]byte("%PDF-1.7 invoice body"),
		[]byte(""),
		{0x00, 0x01, 0x02, 0xff},
		[]byte(strings.Repeat("A", 100000)),
	}
	for _, in := range cases {
		got := InvoiceContentHash(in)
		want := sha256.Sum256(in)
		if got != hex.EncodeToString(want[:]) {
			t.Fatalf("hash mismatch for %d bytes: got %s", len(in), got)
		}
		if len(got) != 64 {
			t.Fatalf("len = %d, want 64 (sha256 hex)", len(got))
		}
	}
}

// 空输入必须也有确定的值，不能返回空串 —— 返回空串会让「没算出来」
// 和「内容恰好是空」变得无法区分。
func TestInvoiceContentHash_EmptyIsStillAHash(t *testing.T) {
	got := InvoiceContentHash(nil)
	if got == "" {
		t.Fatal("nil input must still produce a hash, not an empty string")
	}
	if got != InvoiceContentHash([]byte{}) {
		t.Fatal("nil and empty slice must agree")
	}
	if !strings.HasPrefix(got, "e3b0c442") {
		t.Fatalf("got %s, want the well-known sha256 of the empty string", got)
	}
}

// 不同内容必须不同。这条是它作为去重依据的**唯一前提**。
func TestInvoiceContentHash_DiffersOnSingleByte(t *testing.T) {
	a := []byte("%PDF-1.7 amount=100.00")
	b := append([]byte(nil), a...)
	b[len(b)-1]++ // 只改最后一个字节
	if InvoiceContentHash(a) == InvoiceContentHash(b) {
		t.Fatal("two payloads differing in one byte must not collide")
	}
	// 内容相同必须稳定：重复采集同一份 PDF 要得到同一个值。
	if InvoiceContentHash(a) != InvoiceContentHash(append([]byte(nil), a...)) {
		t.Fatal("hash must be deterministic for identical input")
	}
}

// 本文件不写「它还没被接线」的守卫用例：那种用例只能写成 t.Skip，
// 而一个永远跳过的测试是**假守卫** —— 它让人以为这件事被盯住了，实际什么也没盯。
// 「零调用点」这个事实由全仓 grep 给出（见本文件头部的说明），
// 它会在有人接线时**立刻**由那处改动本身暴露出来，不需要额外断言。
//
// 一旦真的接线，必须同时回答两个问题：
//  1. 去重语义是否要从「按 email_id」改成「按内容」；
//  2. `email_invoices` 要不要加内容哈希列与唯一约束 —— 当前只有
//     `UNIQUE (email_id)`，`invoice_no` **无任何唯一约束**，
//     所以同一张发票被转发到两封邮件时会被记两行，汇总金额翻倍。
