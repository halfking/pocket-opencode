package meeting

import (
	"fmt"
	"testing"
	"time"
)

// TestCreateScopedIDCollision 是诊断用测试，用来确认 store.go 里
// `fmt.Sprintf("mtg_%d", time.Now().UnixNano())` 是否会在同一时钟刻度内
// 生成重复 ID。
//
// 背景：TestMeetingWorkspaceIsolation/list_A 在全包跑时失败，报
//
//	list total/items=0/0, want 1
//	cross-workspace meeting GET status=200 body={... workspace_id:"ws-b" ...}
//
// 第二条尤其说明问题：请求 meetingA.ID 却返回了 ws-b 的那条。两个症状
// 用同一个原因就能解释 —— 两次 CreateScoped 拿到了**相同的 ID**，
// s.meetings[m.ID] 让后者覆盖前者，前者被静默丢弃。
//
// 这个测试的价值在于它**不依赖测试执行顺序**，也不依赖整体负载：
// 直接在同一个 store 上高频创建，看 ID 是否重复。
func TestCreateScopedIDCollision(t *testing.T) {
	const rounds = 200
	s := NewStore()
	seen := make(map[string]int, rounds)
	dupes := 0
	var firstDup string

	for i := 0; i < rounds; i++ {
		m, err := s.CreateScoped(CreateMeetingRequest{Title: fmt.Sprintf("m-%d", i)}, "shared-user", "ws-a")
		if err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		if prev, ok := seen[m.ID]; ok {
			dupes++
			if firstDup == "" {
				firstDup = fmt.Sprintf("id=%s 由第 %d 次和第 %d 次创建同时产生", m.ID, prev, i)
			}
			continue
		}
		seen[m.ID] = i
	}

	t.Logf("连续创建 %d 条，唯一 ID %d 个，重复 %d 次", rounds, len(seen), dupes)
	if firstDup != "" {
		t.Logf("首个碰撞：%s", firstDup)
	}
	// 同时确认：即使 ID 重复，被覆盖的那条会议确实从 store 里消失了。
	all, err := s.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	t.Logf("store 里实际存了 %d 条（期望 %d）", len(all), rounds)

	if dupes > 0 {
		t.Errorf("确认缺陷：%d 次 ID 碰撞，store 丢失 %d 条会议", dupes, rounds-len(all))
	} else {
		t.Logf("本次运行未复现碰撞（时钟精度问题，不稳定）—— 需提高 rounds 或换机器复跑")
	}
}

// TestUnixNanoResolution 量出本机 time.Now().UnixNano() 的最小刻度。
//
// 它测的是**平台特性，不是产品行为**，所以只记录不判定失败：
// 粗粒度时钟本身不是缺陷，缺陷是拿它当唯一 ID 来源。
// 它的作用是把「会不会撞」从概率问题变成可计算的问题 —— 本机实测
// 1000 次调用只产生 1 个不同值，也就是说纯纳秒 ID 在负载下几乎必然重复。
func TestUnixNanoResolution(t *testing.T) {
	const n = 1000
	vals := make([]int64, 0, n)
	for i := 0; i < n; i++ {
		vals = append(vals, time.Now().UnixNano())
	}
	minDelta := int64(0)
	haveMin := false
	distinct := make(map[int64]struct{}, n)
	backwards := 0
	worstBack := int64(0)
	for i := 1; i < len(vals); i++ {
		d := vals[i] - vals[i-1]
		if d < 0 {
			// ★ 2026-10-25（审计轮）：这里原来是一句 t.Fatalf —— 但这个
			// 测试**测的是平台特性（宿主墙钟），不是产品行为**，它自己的
			// 注释就写着「只记录不判定失败」。墙钟被 NTP 校时或宿主机调整
			// 一下就会回拨 ⇒ 与产品无关的事件让整个 internal/meeting 包变红
			//（实测：vals[235] 比 vals[234] 小 0.5ms，go test 直接红）。
			//
			// 而且它当初守的那个**真缺陷**（会议 ID 撞车导致静默丢数据）
			// 已经在同一个提交里修掉了：store.go 现在用
			// `mtg_<nano>_<seq>`，seq 是进程内单调递增序号 ⇒ 同一进程内
			// 必然唯一，与墙钟刻度无关。
			//
			// 所以回拨不但不该判红，它**加强**了本测试的结论：纳秒时间戳
			// 连单调性都不保证，更不能当唯一 ID 来源。记下来即可。
			backwards++
			if d < worstBack {
				worstBack = d
			}
		}
		if !haveMin || d < minDelta {
			minDelta = d
			haveMin = true
		}
	}
	for _, v := range vals {
		distinct[v] = struct{}{}
	}
	t.Logf("%d 次 time.Now() 产生 %d 个不同值，最小间隔 %dns", n, len(distinct), minDelta)
	if minDelta == 0 {
		t.Logf("本机时钟刻度为 0ns —— 这正是会议 ID 必须带单调序号、不能只用纳秒时间戳的原因")
	}
	if backwards > 0 {
		t.Logf("⚠ 观察到 %d 次时钟回拨（最大回退 %dns）：宿主墙钟被调整过 —— "+
			"这是「不能拿 time.Now().UnixNano() 当唯一 ID」的又一条证据，不判红",
			backwards, -worstBack)
	}
}
