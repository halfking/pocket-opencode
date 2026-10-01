package email

// selectUIDWindow 的正确性判据。
//
// 这道测试守护的是「邮件永远收不到」这个级别的缺陷：2026-10-02 之前的
// 实现在超过 50 封时取**最新**的 50 封，而 last_synced_uid 被写成这 50 封
// 里最大的 UID —— 被丢掉���更旧的那些 UID 落在 last_synced_uid 之下，
// 正常同步再也不会搜到它们，且没有任何报错。

import (
	"testing"

	"github.com/emersion/go-imap/v2"
)

func uidsFrom(start, n int) []imap.UID {
	out := make([]imap.UID, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, imap.UID(start+i))
	}
	return out
}

// simulateRound 复刻 fetcher 的一轮：选窗口 → 入库 → 把 last_synced_uid
// 推进到本轮入库的最大 UID。
func simulateRound(pending []imap.UID, lastSyncedUID imap.UID, limit int) (collected []imap.UID, newLastSynced imap.UID) {
	// UID SEARCH: uid > last_synced_uid
	var search []imap.UID
	for _, u := range pending {
		if u > lastSyncedUID {
			search = append(search, u)
		}
	}
	window, _ := selectUIDWindow(search, limit)
	for _, u := range window {
		collected = append(collected, u)
		if u > newLastSynced {
			newLastSynced = u
		}
	}
	return collected, newLastSynced
}

// TestSelectUIDWindow_TakesOldestNotNewest 是本测试的核心断言。
//
// 如果 selectUIDWindow 取回最新 50 封，模拟两轮之后 UID 1..70 永远收不到。
func TestSelectUIDWindow_TakesOldestNotNewest(t *testing.T) {
	pending := uidsFrom(1, 120)
	last := imap.UID(0)

	var got []imap.UID
	after1, last1 := simulateRound(pending, last, syncUIDWindow)
	got = append(got, after1...)
	after2, last2 := simulateRound(pending, last1, syncUIDWindow)
	got = append(got, after2...)
	after3, last3 := simulateRound(pending, last2, syncUIDWindow)
	got = append(got, after3...)

	if len(after1) != 50 || len(after2) != 50 || len(after3) != 20 {
		t.Fatalf("每轮数量 = %d/%d/%d，期望 50/50/20",
			len(after1), len(after2), len(after3))
	}
	if first := got[0]; first != 1 {
		t.Errorf("第一轮第一封 = %d，期望 1（应从最旧的开始，而不是跳过）", first)
	}
	if after1[len(after1)-1] != 50 {
		t.Errorf("第一轮最后一封 = %d，期望 50", after1[len(after1)-1])
	}
	if after2[0] != 51 {
		t.Errorf("第二轮第一封 = %d，期望 51", after2[0])
	}
	if last3 != 120 {
		t.Errorf("三轮后 last_synced_uid = %d，期望 120", last3)
	}

	// 一封都不能少、不能重
	seen := map[imap.UID]int{}
	for _, u := range got {
		seen[u]++
	}
	for i := 1; i <= 120; i++ {
		switch seen[imap.UID(i)] {
		case 1:
		case 0:
			t.Fatalf("UID %d 从未被任何一轮拉到 —— 这正是用户说的「邮件缺内容」", i)
		default:
			t.Fatalf("UID %d 被拉了 %d 次", i, seen[imap.UID(i)])
		}
	}
}

// TestSelectUIDWindow_NoLossUnderContinuousArrival 模拟「新邮件持续到达」时
// 仍然收敛：每轮来 30 封新的，同时处理 50 封旧的。
func TestSelectUIDWindow_NoLossUnderContinuousArrival(t *testing.T) {
	pending := uidsFrom(1, 30)
	last := imap.UID(0)
	collected := map[imap.UID]bool{}
	nextUID := imap.UID(31)

	for round := 1; round <= 8; round++ {
		// 每轮新到 30 封
		for i := 0; i < 30; i++ {
			pending = append(pending, nextUID)
			nextUID++
		}
		got, newLast := simulateRound(pending, last, syncUIDWindow)
		for _, u := range got {
			if collected[u] {
				t.Fatalf("第 %d 轮重复拉到 UID %d", round, u)
			}
			collected[u] = true
		}
		if newLast <= last {
			t.Fatalf("第 %d 轮 last_synced_uid 没有前进：%d -> %d", round, last, newLast)
		}
		last = newLast
	}

	// 8 轮 × 30 封新 = 240 封，加上最初的 30 封。处理能力 50/轮 > 到达 30/轮，
	// 必须把积压清空：除了最后不足一轮的尾部，全部都该被拉到。
	missing := 0
	for i := 1; int(i) < int(nextUID); i++ {
		if !collected[imap.UID(i)] {
			missing++
			if missing <= 5 {
				t.Logf("未拉到 UID %d", i)
			}
		}
	}
	if missing > syncUIDWindow {
		t.Errorf("积压 %d 封未处理，超过一轮容量 %d —— 同步追不上到达速度", missing, syncUIDWindow)
	}
}

// TestSelectUIDWindow_SortsInput 验证乱序输入也能正确取最旧的。
func TestSelectUIDWindow_SortsInput(t *testing.T) {
	in := []imap.UID{90, 10, 70, 30, 50}
	w, dropped := selectUIDWindow(in, 3)
	if dropped != 2 {
		t.Errorf("dropped = %d，期望 2", dropped)
	}
	want := []imap.UID{10, 30, 50}
	for i := range want {
		if w[i] != want[i] {
			t.Fatalf("窗口 = %v，期望 %v", w, want)
		}
	}
	// 不能改动调用方的切片
	if in[0] != 90 {
		t.Errorf("selectUIDWindow 改动了入参：%v", in)
	}
}

// TestSelectUIDWindow_UnderLimit 是**对照组**：没超限时必须原样全取、
// dropped 为 0。否则上面那些用例可能在"什么都不取"的情况下也通过。
func TestSelectUIDWindow_UnderLimit(t *testing.T) {
	in := uidsFrom(1, 50)
	w, dropped := selectUIDWindow(in, syncUIDWindow)
	if dropped != 0 {
		t.Errorf("恰好 50 封时 dropped = %d，期望 0", dropped)
	}
	if len(w) != 50 {
		t.Errorf("恰好 50 封时窗口长度 = %d，期望 50", len(w))
	}
	if w[0] != 1 || w[49] != 50 {
		t.Errorf("窗口 = [%d..%d]，期望 [1..50]", w[0], w[49])
	}
}
