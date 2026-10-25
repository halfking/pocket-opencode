package meeting

// pg_precision_test.go —— §966 的判据：「**写进 PG 的时刻**」这条纪律
// 在会议族（PG 版 + 内存版）上的落地。
//
// ⚠⚠ 为什么这些判据**必须注入时钟**（而不是直接用 `time.Now()`）：
//
//	macOS 的 `time.Now()` 是**微秒对齐**的（§964 实测：纳秒尾 2000/2000 全为 0）
//	⇒ ⇒ 若用真实时钟，那条「写进去的值 == 返回给调用方的值」在 macOS 上
//	**恒真**（两个值逐字相同 ⇒ 判据没有牙）
//	⇒ ⇒ ⇒ 所以这里注入一个**确定带 123ns 尾**的时刻
//	⇒ ⇒ ⇒ ⇒ 于是在**任何平台**上，判据都在量「store 有没有截断」。
//
// ⚠ 三条判据各管一段：
//
//	① TestS966MemstoreTimestampsShareOneScale（**不需 PG**）
//	   内存版：建会 / 更新 / 改元 / 删（墓碑）四处时刻**全部**被截断。
//	② TestS966PGWriteEqualsReturnedValue（**需真 PG**）
//	   PG 版：「写进去的值」==「返回给调用方的值」==「库里读回来的值」（三方严格等值）。
//	③ TestS966TwoImplementationsShareOneScale（**需真 PG**）
//	   同一个注入时钟下，**两个实现**产出的时刻**逐字相同**
//	   （§943「两个实现分叉时门禁是绿的」那条纪律，在时间刻度上的落法）。
//
// ⚠ 没有 PG 时 ②③ skip（退出码仍是 0）⇒ ⇒ 变异批次用 forbid_skip=True
//	把「被 skip」变成失败（见 scripts/mutations/s966.py）。

import (
	"testing"
	"time"
)

// s966PlantedAt 构造一个**确定带 123ns 尾**的时刻。
//
// ⚠ 为什么是 123ns（照抄 s943PlantedAt 的形态）：
//
//	① 必须**有**亚微秒尾 ⇒ 否则判据在微秒对齐的平台上恒真；
//	② 必须是**确定的**尾数 ⇒ 否则「差了多少」这件事没有确定答案；
//	③ 123ns 落在 .5µs 之下 ⇒ **任何**取整方向都会舍掉它
//	⇒ ⇒ ⇒ 于是「截断后的值」在两种取整规则下**相同**
//	⇒ ⇒ ⇒ 这条判据量的是「有没有截断」，而不是「取整方向对不对」
//	（取整方向由 pgtime 包那条判据单独管）。
func s966PlantedAt() time.Time {
	return time.Now().Truncate(time.Microsecond).Add(123 * time.Nanosecond)
}

// s966Floor 是「种植时刻被截断后应有的值」。
func s966Floor(t time.Time) time.Time { return t.Truncate(time.Microsecond) }

// TestS966MemstoreTimestampsShareOneScale 钉住内存版的四处时刻。
func TestS966MemstoreTimestampsShareOneScale(t *testing.T) {
	planted := s966PlantedAt()
	// 负控：种植时刻必须真的带 ns 尾 ⇒ 否则下面所有断言都是恒真的。
	if got := planted.Sub(s966Floor(planted)); got != 123*time.Nanosecond {
		t.Fatalf("负控：种植时刻必须带 123ns 尾，实际 %v", got)
	}

	store := NewStore()
	store.SetNowFunc(func() time.Time { return planted })

	m, err := store.CreateScoped(CreateMeetingRequest{
		ID:    "meeting-s966-mem",
		Title: "S966 内存版",
	}, "owner-s966", "ws-s966")
	if err != nil {
		t.Fatalf("CreateScoped: %v", err)
	}
	if !m.CreatedAt.Equal(s966Floor(planted)) {
		t.Errorf("内存版 created_at 必须被截断到微秒：got %v want %v", m.CreatedAt, s966Floor(planted))
	}
	if !m.UpdatedAt.Equal(s966Floor(planted)) {
		t.Errorf("内存版 updated_at 必须被截断到微秒：got %v want %v", m.UpdatedAt, s966Floor(planted))
	}

	// 改元那条链返回的时刻（UpdateMetaScoped 的返回值）。
	updatedAt, err := store.UpdateMetaScoped(m.ID, "owner-s966", "ws-s966", nil, nil)
	if err != nil {
		t.Fatalf("UpdateMetaScoped: %v", err)
	}
	if !updatedAt.Equal(s966Floor(planted)) {
		t.Errorf("内存版 UpdateMetaScoped 返回的时刻必须被截断：got %v want %v", updatedAt, s966Floor(planted))
	}

	// 墓碑的 at：它与 PG 版的 deleted_at 是**同一个概念** ⇒ 必须同刻度。
	if err := store.DeleteScoped(m.ID, "owner-s966", "ws-s966"); err != nil {
		t.Fatalf("DeleteScoped: %v", err)
	}
	// 用 DeletedIDsSince 在**微秒**粒度上量墓碑的时刻：
	// since = 截断值 − 1µs ⇒ 必须找得到；since = 截断值 + 1µs ⇒ 必须找不到。
	if got := store.DeletedIDsSince("owner-s966", "ws-s966", s966Floor(planted).Add(-time.Microsecond)); len(got) != 1 {
		t.Errorf("负控失败：墓碑时刻应落在截断值上，since=截断值−1µs 必须找得到，实际 %d 条", len(got))
	}
	if got := store.DeletedIDsSince("owner-s966", "ws-s966", s966Floor(planted).Add(time.Microsecond)); len(got) != 0 {
		t.Errorf("墓碑时刻必须**小于**截断值+1µs，实际找到 %d 条（墓碑时刻被写成了纳秒精度？）", len(got))
	}
}

// TestS966PGWriteEqualsReturnedValue 钉住 PG 版的「三方严格等值」。
//
// ⚠ 为什么这条是**真 PG**：§964 那条判据（`TestS964TombstoneFixture…`）
//
//	只做了**算术自证**（`stored := base`），**没有连过 PG**
//	⇒ ⇒ ⇒ 「PG 往返差确实是 123ns」这件事**当时没有任何判据守着**
//	⇒ ⇒ ⇒ 本条补上这个洞：真的写进去、真的读回来。
func TestS966PGWriteEqualsReturnedValue(t *testing.T) {
	store, cleanup := newPGTestEnv(t)
	defer cleanup()

	planted := s966PlantedAt()
	if got := planted.Sub(s966Floor(planted)); got != 123*time.Nanosecond {
		t.Fatalf("负控：种植时刻必须带 123ns 尾，实际 %v", got)
	}
	store.SetNowFunc(func() time.Time { return planted })

	created, err := store.CreateScoped(CreateMeetingRequest{
		ID:    "meeting-s966-pg",
		Title: "S966 PG 版",
	}, "owner-s966", "ws-s966")
	if err != nil {
		t.Fatalf("CreateScoped: %v", err)
	}

	// ① 返回给调用方的时刻必须已经被截断。
	if !created.CreatedAt.Equal(s966Floor(planted)) {
		t.Errorf("PG 版返回的 created_at 必须被截断：got %v want %v", created.CreatedAt, s966Floor(planted))
	}
	// ② 库里读回来的值必须与返回给调用方的值**逐字相同**。
	got, err := store.GetScoped(created.ID, "owner-s966", "ws-s966")
	if err != nil {
		t.Fatalf("GetScoped: %v", err)
	}
	if !got.CreatedAt.Equal(created.CreatedAt) {
		t.Errorf("库里读回来的 created_at 必须与返回给调用方的值**逐字相同**：db=%v returned=%v",
			got.CreatedAt, created.CreatedAt)
	}
	if !got.UpdatedAt.Equal(created.UpdatedAt) {
		t.Errorf("库里读回来的 updated_at 必须与返回给调用方的值**逐字相同**：db=%v returned=%v",
			got.UpdatedAt, created.UpdatedAt)
	}
	// ③ 负控：这两个值必须**不逐字相同**于种植时刻（否则上面的断言是恒真的）。
	if got.CreatedAt.Equal(planted) {
		t.Error("负控失败：读回来的时刻与种植时刻逐字相同 ⇒ 上面两条断言没有区分度")
	}

	// ④ ★★ 「**返回值不是从库里读回来的**」的那条路径 —— 本 store 里只有一处：
	//
	//	UpdateMetaScoped 返回的是**内存里的 `now`**（pg_store.go:636/645），
	//	而它写进库的是**同一份值** ⇒ ⇒ ⇒ 若 store 没有截断，
	//	⇒ ⇒ ⇒ **返回值带 ns 尾、库里那一行是微秒** ⇒ ⇒ ⇒ 两者不等
	//	⇒ ⇒ ⇒ 而这个不等**会一路走到 API 响应里**（updated_at）。
	//
	// ⚠⚠ 这条是**变异批次 M3 打出来的**：M3 把 PG 版的截断去掉后，
	//	判据**没有红** ⇒ ⇒ ⇒ 查下来是**判据有洞**（①② 被 RETURNING 兜住了）
	//	⇒ ⇒ ⇒ 洞在这一条上 ⇒ 现在补上。
	newTitle := "S966 PG 版（改标题）"
	returnedAt, err := store.UpdateMetaScoped(created.ID, "owner-s966", "ws-s966", &newTitle, nil)
	if err != nil {
		t.Fatalf("UpdateMetaScoped: %v", err)
	}
	after, err := store.GetScoped(created.ID, "owner-s966", "ws-s966")
	if err != nil {
		t.Fatalf("GetScoped after UpdateMetaScoped: %v", err)
	}
	if !after.UpdatedAt.Equal(returnedAt) {
		t.Errorf("UpdateMetaScoped 返回的时刻必须与库里那一行**逐字相同**：returned=%v db=%v",
			returnedAt, after.UpdatedAt)
	}
	if !returnedAt.Equal(s966Floor(planted)) {
		t.Errorf("UpdateMetaScoped 返回的时刻必须已被截断：got %v want %v", returnedAt, s966Floor(planted))
	}
}

// TestS966TwoImplementationsShareOneScale 钉住「两个实现同刻度」。
//
// ⚠ 这条判据的由来（§943 那条纪律的延伸）：
//
//	§943 的发现是「**两个实现分叉时门禁是绿的**」
//	（当时那条承重门只跑在内存版上）⇒ 修法是「**一条判据同时钉两个实现**」。
//	⇒ ⇒ ⇒ §966 把这条纪律用到**时间刻度**上：
//	内存版与 PG 版是同一个接口的两个实现 ⇒ 它们产出的时刻必须**逐字相同**。
//	⇒ ⇒ ⇒ 而 §964 修完 PG 版之后，**内存版反而成了那个分叉的一边**
//	⇒ ⇒ ⇒ ⇒ 这就是「修了一边 ⇒ 两边分叉」的同族。
func TestS966TwoImplementationsShareOneScale(t *testing.T) {
	pgStore, cleanup := newPGTestEnv(t)
	defer cleanup()

	planted := s966PlantedAt()
	memStore := NewStore()
	memStore.SetNowFunc(func() time.Time { return planted })
	pgStore.SetNowFunc(func() time.Time { return planted })

	memM, err := memStore.CreateScoped(CreateMeetingRequest{
		ID:    "meeting-s966-mem-2",
		Title: "S966 内存版 2",
	}, "owner-s966", "ws-s966")
	if err != nil {
		t.Fatalf("内存版 CreateScoped: %v", err)
	}
	pgM, err := pgStore.CreateScoped(CreateMeetingRequest{
		ID:    "meeting-s966-pg-2",
		Title: "S966 PG 版 2",
	}, "owner-s966", "ws-s966")
	if err != nil {
		t.Fatalf("PG 版 CreateScoped: %v", err)
	}

	if !memM.CreatedAt.Equal(pgM.CreatedAt) {
		t.Errorf("两个实现产出的 created_at 必须逐字相同：mem=%v pg=%v", memM.CreatedAt, pgM.CreatedAt)
	}
	if !memM.UpdatedAt.Equal(pgM.UpdatedAt) {
		t.Errorf("两个实现产出的 updated_at 必须逐字相同：mem=%v pg=%v", memM.UpdatedAt, pgM.UpdatedAt)
	}
	if !memM.CreatedAt.Equal(s966Floor(planted)) {
		t.Errorf("两个实现都必须落在截断后的刻度上：mem=%v want %v", memM.CreatedAt, s966Floor(planted))
	}
}
