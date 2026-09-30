package flashcards

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// BUG-O 回归锁（2026-09-30 真机验收）—— **静态锁**，不是连库集成测试。
//
// 现象：真机建卡片 -> POST 201 -> PG 里有 note 和 card -> 宿主侧
// GET /api/flashcards?since=0 能拿到那张卡，但卡组详情页「开始复习」恒 disabled，
// 卡片永远不出现。
//
// 根因分两半。客户端那半在 frontend/src/stores/flashcards.ts：它把 lastSyncedAt
// 设成 floor(serverTimeMs/1000)（服务器此刻时间），而不是本批数据实际收到的最大
// updated_at；这半有独立测试 frontend/src/stores/flashcards-sync-watermark.test.ts。
//
// 服务端这半就是本文件：增量过滤必须是 **>=** 而不是 >。
// 为什么客户端改对了服务端还得改：水位线取「本批最大 updated_at」后，严格大于
// 依然会在**同一秒内的多条变更**上丢数据 —— 客户端先收到 A（updated_at=T，
// 水位线=T），服务端随后在同一秒写入 B（updated_at=T），下一轮 since=T，
// `T > T` 不成立，B 永久拉不回来。
//
// 改成 >= 会重复返回水位线那一秒的行；客户端 merge-by-id 幂等，重复没有副作用，
// 而漏数据不可逆。这个不对称是刻意的。
//
// 判据用源码正则而非连库：要防的是**语义被改回去**，不是查库的可用性。
// 端到端证据在真机用例 scripts/redmi-write-ops-modules.mjs 与 scripts/verify-buglnm.mjs。
func TestIncrementalFiltersAreInclusive(t *testing.T) {
	src, err := os.ReadFile("store.go") // 测试 cwd 即包目录
	if err != nil {
		t.Fatalf("read store.go: %v", err)
	}
	text := string(src)

	strict := regexp.MustCompile(`(?:updated_at|deleted_at)\s*>\s*\$2`)
	if loc := strict.FindStringIndex(text); loc != nil {
		t.Errorf("store.go 仍有严格大于过滤 %q：增量过滤必须是 >=（BUG-O）",
			text[loc[0]:loc[1]])
	}

	// 反向判据：防止有人把比较整个删掉来"让测试变绿"。
	for _, want := range []string{"updated_at >= $2", "deleted_at >= $2"} {
		if !strings.Contains(text, want) {
			t.Errorf("store.go 中找不到 %q：增量过滤被误删", want)
		}
	}

	// 数量核对：List*Since / ListDeleted*Since 一共 6 处 SQL 过滤，
	// 少改一处就说明还有入口会漏数据。
	if got := strings.Count(text, ">= $2"); got < 6 {
		t.Errorf("只找到 %d 处 `>= $2`，期望至少 6 处（notes/cards/decks 各 2）", got)
	}
}
