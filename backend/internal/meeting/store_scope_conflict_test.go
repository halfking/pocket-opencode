package meeting

// §943 判据：「作用域冲突」这条口径**两个实现必须逐字一致**。
//
// ⚠⚠ 为什么要有这么一条判据（本节最贵的发现）：
//
//	修复前 PG 版的 `ON CONFLICT (id) DO UPDATE` **不带任何谓词**
//	⇒ 撞上一个**别的作用域**的 id，就把那**一行整个改掉**
//	（title / transcript / summary / status / location / participants /
//	note_id 全换成提交者的内容，而 owner_id / workspace_id 不在 SET 列表里
//	⇒ 那行仍然属于原主人，**正文却已经是别人的了**）
//
//	⇒ ⇒ ⇒ 而**内存版早就挡住了**（它会换号）
//	⇒ ⇒ ⇒ **两个实现分叉**
//	⇒ ⇒ ⇒ 而当时唯一钉这条语义的承重门
//	    （server 包的 TestClientIDCollisionAcrossWorkspacesNeverOverwrites）
//	    **只跑在内存版上** ⇒ ⇒ ⇒⇒ **门禁是绿的，生产是破的**
//
//	⇒ ⇒⇒ 修法不是「给 PG 补一道门」，而是「**一条判据同时钉两个实现**」
//	⇒ ⇒⇒ 于是「两个实现分叉」这件事本身变成**可测的**。
//
// ⚠ 判据量的是**行为**（落库之后的行），不是源码文本。
//
// ⚠ 需要真 PG：没有 PG 时本用例会 skip（退出码仍是 0）⇒ ⇒ 变异批次用
//   forbid_skip=True 把「被 skip」变成失败（见 scripts/mutations/s943.py）。
//
// ⚠ 三条腿缺一不可（每条各钉一种碰撞形态）：
//
//	① 跨 **owner** 撞 id（= 越权那一头）：受害者的行**一字未改**；
//	② 跨 **workspace**（同 owner）撞 id：同样换号、同样不覆盖
//	   —— 这正是既有承重门 3 钉的那一头，此处补上「PG 那一半」；
//	③ 同 owner 同 workspace 撞 id：**负控**（幂等更新，不换号、只有一行）
//	   —— 它证明「守卫没有把正常路径也挡掉」。
import (
	"reflect"
	"testing"
)

const (
	s943ID      = "meeting-s943-collision"
	s943OwnerA  = "user-s943-a"
	s943OwnerB  = "user-s943-b"
	s943WsA     = "ws-s943-a"
	s943WsB     = "ws-s943-b"
	s943TitleA  = "A 的会议"
	s943BodyA   = "A 的正文：预算八十万。"
	s943TitleB  = "B 的会议"
	s943BodyB   = "B 的正文：这是别人写进来的。"
	s943StatusA = "recording"
)

// s943Observation 是「一次碰撞之后能观测到的东西」。
//
// ⚠ 为什么要做成**结构体**再逐字段比，而不是逐条断言：
//
//	判据的核心不是「PG 版对不对」，而是「**两个实现的行为逐字一致**」
//	⇒ ⇒ ⇒ 两个实现跑**同一组**用例 ⇒ 产出两份 observation ⇒ 逐字段比
//	⇒ ⇒⇒ ⇒ 于是「分叉」本身就是被测对象。
//
// ⚠ 为什么不直接比 id 字符串：两个实现换号时**各自**发自己的号
//
//	（内存版与 PG 版都叫 mtg_<纳秒>_<自增>，但纳秒不同）⇒ ⇒⇒ 只比
//	「换没换」（SameID）⇒ ⇒⇒ 这样比较才与实现无关。
type s943Observation struct {
	// Failed 表示这次写入**报了错**。
	Failed bool
	// SameID 表示返回的那一行的 id 与请求的 id **相同**（没换号）。
	SameID bool
	// VictimTitle / VictimBody / VictimStatus 是**受害者**那一行现在的内容。
	VictimTitle  string
	VictimBody   string
	VictimStatus string
	// VictimRows 是受害者作用域里的行数。
	VictimRows int
	// ActorRows 是**提交者**作用域里的行数。
	ActorRows int
	// ActorOwner 是提交者拿到的那一行的 owner（换号后必须是提交者自己）。
	ActorOwner string
	// ActorTitle 是提交者拿到的那一行的 title。
	ActorTitle string
}

// s943Stores 造出**两个实现**：内存版与 PG 版。
//
// ⚠ 两者必须来自**同一份数据语义**（同一组请求）⇒ ⇒⇒ 这样「逐字一致」才有意义。
func s943Stores(t *testing.T) (mem MeetingStore, pg MeetingStore) {
	t.Helper()
	pgStore, cleanup := newPGTestEnv(t)
	t.Cleanup(cleanup)
	return NewStore(), pgStore
}

// s943Observe 跑一次「受害者先占 id ⇒ 别人拿同一个 id 来写」的碰撞，返回观测。
//
// ⚠ 两个实现走**完全相同**的请求序列 ⇒ ⇒⇒ 观测之差只可能来自实现本身。
func s943Observe(t *testing.T, store MeetingStore, actorOwner, actorWS string) s943Observation {
	t.Helper()
	// ① 受害者先占住这个 id。
	if _, err := store.CreateScoped(CreateMeetingRequest{
		ID:                s943ID,
		Title:             s943TitleA,
		RefinedTranscript: s943BodyA,
		Summary:           "A 的摘要",
		Location:          "A 的地点",
	}, s943OwnerA, s943WsA); err != nil {
		t.Fatalf("受害者建会失败: %v", err)
	}
	// ② 别人拿**同一个 id** 来写（越权那一头就是这个动作）。
	m, err := store.CreateScoped(CreateMeetingRequest{
		ID:                s943ID,
		Title:             s943TitleB,
		RefinedTranscript: s943BodyB,
		Summary:           "B 的摘要",
		Location:          "B 的地点",
	}, actorOwner, actorWS)

	obs := s943Observation{Failed: err != nil}
	if m != nil {
		obs.SameID = m.ID == s943ID
		obs.ActorOwner = m.OwnerID
		obs.ActorTitle = m.Title
	}
	// ③ 受害者那一行现在是什么样。
	if v, err := store.GetScoped(s943ID, s943OwnerA, s943WsA); err == nil {
		obs.VictimTitle = v.Title
		obs.VictimBody = v.Transcript
		obs.VictimStatus = v.Status
	} else {
		obs.VictimTitle = "<读不到>"
	}
	_, obs.VictimRows, _ = store.ListScoped(s943OwnerA, s943WsA, ListOptions{})
	_, obs.ActorRows, _ = store.ListScoped(actorOwner, actorWS, ListOptions{})
	return obs
}

// s943Compare 两个实现跑同一个用例，断言**逐字一致**并返回观测。
func s943Compare(t *testing.T, actorOwner, actorWS string) s943Observation {
	t.Helper()
	mem, pg := s943Stores(t)
	obsMem := s943Observe(t, mem, actorOwner, actorWS)
	obsPG := s943Observe(t, pg, actorOwner, actorWS)
	if !reflect.DeepEqual(obsMem, obsPG) {
		t.Fatalf("两个实现行为**不一致** ⇒ 口径已分叉：\n 内存版 %+v\n PG 版  %+v",
			obsMem, obsPG)
	}
	return obsMem
}

// TestS943CrossOwnerCollisionNeverOverwrites 跨 **owner** 撞 id ⇒ 绝不覆盖。
//
// ⚠ 这是**越权**那一头：撞的是**另一个用户**的 id。
// ⇒ 受害者的行必须**一字未改**，提交者必须被**换号**（拿到自己的一行）。
func TestS943CrossOwnerCollisionNeverOverwrites(t *testing.T) {
	obs := s943Compare(t, s943OwnerB, s943WsA)

	if obs.Failed {
		t.Fatalf("撞上别人的 id 反而报错了：%+v（口径应是「换号」，不是报错）", obs)
	}
	if obs.SameID {
		t.Fatalf("撞上别人的 id 却还用了同一个 id ⇒ 那就是**覆盖**别人的行（越权写入）")
	}
	if obs.VictimTitle != s943TitleA {
		t.Errorf("受害者的标题变成了 %q（want %q）⇒ 别人的行被覆盖", obs.VictimTitle, s943TitleA)
	}
	if obs.VictimBody != s943BodyA {
		t.Errorf("受害者的正文变成了 %q（want %q）⇒ 别人的行被覆盖", obs.VictimBody, s943BodyA)
	}
	if obs.VictimStatus != s943StatusA {
		t.Errorf("受害者的 status 变成了 %q（want %q）", obs.VictimStatus, s943StatusA)
	}
	if obs.VictimRows != 1 {
		t.Errorf("受害者作用域里有 %d 行（want 1）⇒ 多出来的那行是谁的？", obs.VictimRows)
	}
	if obs.ActorRows != 1 {
		t.Errorf("提交者作用域里有 %d 行（want 1）", obs.ActorRows)
	}
	if obs.ActorOwner != s943OwnerB {
		t.Errorf("提交者拿到的行 owner=%q（want %q）⇒ 它没有落在自己名下", obs.ActorOwner, s943OwnerB)
	}
	if obs.ActorTitle != s943TitleB {
		t.Errorf("提交者拿到的行 title=%q（want %q）", obs.ActorTitle, s943TitleB)
	}
}

// TestS943CrossWorkspaceCollisionNeverOverwrites 跨 **workspace**（同 owner）撞 id。
//
// ⚠ 这是**既有承重门 3**（server 包的
//
//	TestClientIDCollisionAcrossWorkspacesNeverOverwrites）钉的那一头，
//	但那条门**只跑在内存版上** ⇒ ⇒⇒ 此处补上 PG 那一半。
//
// ⚠ 语义与既有门**一字未改**：换号 + 201 + 别人的行不动。
func TestS943CrossWorkspaceCollisionNeverOverwrites(t *testing.T) {
	obs := s943Compare(t, s943OwnerA, s943WsB)

	if obs.Failed {
		t.Fatalf("跨 workspace 撞号报错：%+v（既有承重门要求「换号 + 201」）", obs)
	}
	if obs.SameID {
		t.Fatalf("跨 workspace 撞号却还用了同一个 id ⇒ workspace 隔离失效")
	}
	if obs.VictimTitle != s943TitleA || obs.VictimBody != s943BodyA {
		t.Errorf("受害者的行被改了：title=%q body=%q（want %q / %q）",
			obs.VictimTitle, obs.VictimBody, s943TitleA, s943BodyA)
	}
	if obs.VictimRows != 1 {
		t.Errorf("受害者作用域里有 %d 行（want 1）", obs.VictimRows)
	}
	if obs.ActorRows != 1 {
		t.Errorf("提交者作用域里有 %d 行（want 1）", obs.ActorRows)
	}
}

// TestS943SameScopeCollisionStaysIdempotent **负控**（正常路径不许被守卫挡掉）。
//
// ⚠ 同 owner 同 workspace 用同一个 id 再写一次 ⇒ 必须是**幂等更新**：
// 不换号、仍然只有一行、内容被更新。
// ⇒ ⇒ 这条证明「归属守卫没有把正常路径也挡掉」（守卫若写错成「一律不更新」，
// 这条会红）。
func TestS943SameScopeCollisionStaysIdempotent(t *testing.T) {
	mem, pg := s943Stores(t)

	for name, store := range map[string]MeetingStore{"内存版": mem, "PG版": pg} {
		t.Run(name, func(t *testing.T) {
			first, err := store.CreateScoped(CreateMeetingRequest{
				ID: s943ID, Title: s943TitleA, RefinedTranscript: s943BodyA,
			}, s943OwnerA, s943WsA)
			if err != nil {
				t.Fatalf("建会: %v", err)
			}
			second, err := store.CreateScoped(CreateMeetingRequest{
				ID: s943ID, Title: s943TitleB, RefinedTranscript: s943BodyB,
			}, s943OwnerA, s943WsA)
			if err != nil {
				t.Fatalf("同作用域重发报错（应是幂等更新）: %v", err)
			}
			if second.ID != first.ID || second.ID != s943ID {
				t.Fatalf("同作用域重发换了号：%q → %q（应恒为 %q）", first.ID, second.ID, s943ID)
			}
			_, total, err := store.ListScoped(s943OwnerA, s943WsA, ListOptions{})
			if err != nil {
				t.Fatalf("ListScoped: %v", err)
			}
			if total != 1 {
				t.Fatalf("同作用域重发后有 %d 行（want 1）⇒ 幂等被打坏", total)
			}
			got, err := store.GetScoped(s943ID, s943OwnerA, s943WsA)
			if err != nil {
				t.Fatalf("GetScoped: %v", err)
			}
			if got.Title != s943TitleB || got.Transcript != s943BodyB {
				t.Fatalf("同作用域重发没有更新内容：title=%q transcript=%q（want %q / %q）",
					got.Title, got.Transcript, s943TitleB, s943BodyB)
			}
		})
	}
}
