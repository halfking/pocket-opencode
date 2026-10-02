package notifycenter

// limit_clamp_test.go — 需求 4 的**数据前提**：客户端把首次加载的 limit
// 抬到 200，只有后端真的接受 200 才有效。
//
// 为什么这条不能省（这是本轮实际踩到的）：真库当时只有 24 条通知，于是
// 真机上 limit=50 / 200 / 201 / 500 返回的条数**全都是 24** —
// 「被接受」与「被静默压回 50」在数据上完全无法区分。任何只断言
// "count > 0" 或 "count == 24" 的判据在这种数据下都是恒真的。
//
// 所以这里造 260 条，造到**跨过 200 与 50 两条线**，让 200 与 50 的
// 返回条数必然不同；否则判据自己就是恒真的。
//
// 上限在哪：service.go 的 `if limit <= 0 || limit > 200 { limit = 50 }` ——
// 它是**静默**的：客户端调 500 不会得到错误，只会拿到 50 条。
// 这正是前端那条「前后端上限常量必须相等」断言要防的东西；
// 这条 Go 侧测试则证明那个 200 此刻确实还是 200。

import (
	"context"
	"fmt"
	"testing"
)

func TestListNotifications_LimitClampBoundary(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const total = 260
	for i := 0; i < total; i++ {
		if err := s.InsertNotification(ctx, &Notification{
			ID:          fmt.Sprintf("probe-%03d", i),
			WorkspaceID: "ws_limit_probe",
			Source:      "email",
			Kind:        "important",
			Title:       fmt.Sprintf("probe %d", i),
			Body:        "x",
		}); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	got := func(limit int) int {
		rows, err := s.ListNotifications(ctx, "ws_limit_probe", limit, 0)
		if err != nil {
			t.Fatalf("list limit=%d: %v", limit, err)
		}
		return len(rows)
	}

	got50, got200, got201, got500 := got(50), got(200), got(201), got(500)
	t.Logf("inserted=%d  limit50=%d limit200=%d limit201=%d limit500=%d", total, got50, got200, got201, got500)

	// 前提自检：数据必须跨过两条线，否则下面所有断言都恒真。
	if got50 == got200 {
		t.Fatalf("前提不成立：limit=50 与 limit=200 返回同样条数（%d），"+
			"说明造的条数没跨过上限，判据在这种数据下测不出任何东西", got50)
	}

	if got200 != 200 {
		t.Errorf("limit=200 应返回 200 条，实际 %d —— 客户端首次加载会永久丢历史", got200)
	}
	if got201 != 50 {
		t.Errorf("limit=201 应被静默压回 50，实际 %d —— 上限已变，前端那条"+
			"「前后端上限常量必须相等」的断言前提失效", got201)
	}
	if got500 != 50 {
		t.Errorf("limit=500 应被静默压回 50，实际 %d", got500)
	}
	if got50 != 50 {
		t.Errorf("limit=50 应返回 50 条，实际 %d", got50)
	}
}
