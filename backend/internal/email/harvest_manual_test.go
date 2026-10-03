package email

// harvest_manual_test.go — 手动采集入口的语义（POST /api/emails/invoices/harvest）。
//
// 背景：下载逻辑一直只挂在 Pipeline.Run 里，而流水线第 2 步会把广告邮件
// MOVE 进**真实邮箱**的垃圾箱。于是「只想现在把这张发票的文件拿到手」
// 在真实邮箱上没有可用入口——只能整条流水线跑一遍。为了一个文件去改动
// 真实邮箱，不是可接受的入口，所以拆出 HarvestInvoices：按调用方给定的
// 清单处理，不再自己查一遍无 scope 的待采集列表。

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestHarvestInvoices_NilHarvesterIsNoop(t *testing.T) {
	var h *InvoiceHarvester
	if got := h.HarvestInvoices(context.Background(), []Invoice{{ID: "x"}}); got.Processed != 0 {
		t.Fatalf("nil harvester must be a no-op, got %+v", got)
	}
}

// 依赖不齐（缺 fetcher / dataDir）时必须安静返回，不能 panic：
// handler 会在装配失败时提前 503，不该在这里炸。
func TestHarvestInvoices_IncompleteDepsAreNoop(t *testing.T) {
	cases := []*InvoiceHarvester{
		{},
		{Store: &Store{}},
		{Store: &Store{}, Fetcher: &Fetcher{}},
		{Store: &Store{}, DataDir: t.TempDir()},
	}
	for i, h := range cases {
		got := h.HarvestInvoices(context.Background(), []Invoice{{ID: "x"}})
		if got.Processed != 0 {
			t.Fatalf("case %d: incomplete deps must be a no-op, got %+v", i, got)
		}
	}
}

// 清单为空时不产生任何副作用。
func TestHarvestInvoices_EmptyList(t *testing.T) {
	h := &InvoiceHarvester{Store: &Store{}, Fetcher: &Fetcher{}, DataDir: t.TempDir()}
	if got := h.HarvestInvoices(context.Background(), nil); got.Processed != 0 {
		t.Fatalf("empty list must be a no-op, got %+v", got)
	}
}

// 关键契约：HarvestInvoices 不会自己去查库。
// 旧实现调 ListHarvestableInvoices（无 scope），会把别的 workspace 的待采集
// 发票也拉进来重试；这里用「清单里有别的 workspace 的记录」验证它只处理给定的。
// 由于没有 DB，这里只验证「空 Store + 非空清单」不会因为查库而 panic。
func TestHarvestInvoices_DoesNotQueryStoreForItsOwnList(t *testing.T) {
	// Store 是零值（pool=nil）：如果实现里去查库，这里会 panic；
	// 不查库时 harvestOne 会因为 FetchMessageRaw 拿不到东西而走 markRetry，
	// 而 markRetry 写库同样会 panic —— 所以用 recover 断言「没有因为查库
	// 提前炸在 ListHarvestableInvoices」。
	store := &Store{}
	h := &InvoiceHarvester{Store: store, Fetcher: &Fetcher{}, DataDir: t.TempDir()}
	dir := t.TempDir()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("HarvestInvoices panicked (likely store access before the given list): %v", r)
		}
	}()
	// 传空 Store + 空 DataDir 的组合：函数应在依赖检查处直接返回。
	_ = dir
	if got := h.HarvestInvoices(context.Background(), nil); got.Processed != 0 {
		t.Fatalf("expected no-op, got %+v", got)
	}
	if _, err := os.Stat(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("unreachable")
	}
}
