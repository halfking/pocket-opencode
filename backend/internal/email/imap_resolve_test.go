package email

// imap_resolve_test.go — POP3 合成 UID 反查真实 IMAP UID 的判定逻辑。
//
// 这条路径替代的是「拿位置序号盲 IMAP FETCH」，而盲 FETCH 会取到不相干的
// 邮件（把别人的附件存成这封发票的 PDF）。所以被测的底线只有一条：
// **只有 SEARCH 唯一命中才返回 UID；0 命中和多命中都拒绝，绝不猜。**
//
// 每个用例都配负控对照：把 pickUniqueUID 换成「总是取第一个」的旧式猜测
// 实现，多命中用例必须转红——否则这些用例根本没在测「拒绝猜测」。

import (
	"errors"
	"testing"

	"github.com/emersion/go-imap/v2"
)

func TestPickUniqueUID_UniqueHitReturnsUID(t *testing.T) {
	// 唯一命中：真实场景里 IMAP 侧只有一封同发件人同主题的邮件。
	got, err := pickUniqueUID([]imap.UID{4821}, "a@qq.com", "[QQ Wallet] Electronic Invoice Issuance Notice", "noreply@qq.com")
	if err != nil {
		t.Fatalf("unique hit must not error: %v", err)
	}
	if got != 4821 {
		t.Fatalf("got uid=%d, want 4821 (the only hit)", got)
	}
}

func TestPickUniqueUID_ZeroHitsIsNotResolved(t *testing.T) {
	// 0 命中：邮件已被移出 INBOX / 删除 / 主题被服务商改写。
	got, err := pickUniqueUID(nil, "a@qq.com", "subj", "from@x.com")
	if !errors.Is(err, ErrUIDNotResolved) {
		t.Fatalf("0 hit must be ErrUIDNotResolved, got err=%v", err)
	}
	if got != 0 {
		t.Fatalf("0 hit must return uid=0, got %d", got)
	}
}

func TestPickUniqueUID_AmbiguousRefusesToGuess(t *testing.T) {
	// 多命中：真实踩过——QQ Wallet 连着发两封同主题发票。
	// 这里绝不能返回 134 或 135 里的任何一个：猜错=把另一封的发票
	// 存成这封的 PDF，正是当初拒绝合成 UID 要防的事故。
	got, err := pickUniqueUID([]imap.UID{134, 135}, "a@qq.com", "subj", "from@x.com")
	if !errors.Is(err, ErrUIDNotResolved) {
		t.Fatalf("ambiguous must be ErrUIDNotResolved, got err=%v", err)
	}
	if got != 0 {
		t.Fatalf("ambiguous must return uid=0 (refuse to guess), got %d", got)
	}
}
