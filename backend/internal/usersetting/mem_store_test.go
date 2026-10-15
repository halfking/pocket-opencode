package usersetting

import (
	"encoding/json"
	"testing"
)

func TestMemStorePutLastWriteWins(t *testing.T) {
	store := NewMemStore()
	first, err := store.Put(Record{
		UserID: "user-admin", WorkspaceID: "default",
		Namespace: "llm_gateway", ID: "default",
		Payload:   json.RawMessage(`{"baseURL":"https://llm.kxpms.cn/v1"}`),
		UpdatedAt: 10,
	})
	if err != nil || !first.Applied {
		t.Fatalf("first put: applied=%v err=%v", first, err)
	}

	stale, err := store.Put(Record{
		UserID: "user-admin", WorkspaceID: "default",
		Namespace: "llm_gateway", ID: "default",
		Payload:   json.RawMessage(`{"baseURL":"https://old.example"}`),
		UpdatedAt: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	if stale.Applied || !stale.Conflict {
		t.Fatalf("stale put should conflict: %+v", stale)
	}

	newer, err := store.Put(Record{
		UserID: "user-admin", WorkspaceID: "default",
		Namespace: "llm_gateway", ID: "default",
		Payload:   json.RawMessage(`{"baseURL":"https://llm.kxpms.cn/v1"}`),
		UpdatedAt: 12,
	})
	if err != nil || !newer.Applied {
		t.Fatalf("newer put: %+v err=%v", newer, err)
	}

	got, err := store.Get("user-admin", "default", "llm_gateway", "default")
	if err != nil || got == nil || got.UpdatedAt != 12 {
		t.Fatalf("get after newer: %+v err=%v", got, err)
	}
}

// TestPutClearSecretSameSecondStillClears 钉住「同秒内清空不被 LWW 吃掉」。
//
// 背景（2026-10-08 真机验证）：STT 设置的连续保存实测会「点了没反应」。
// 根因是 DecidePut 用**秒级**时间戳、同值即 DecisionKeep ⇒
// 同一个用户在同一秒内连发两次保存时，第二次被整条丢弃 ——
// 包括「清空凭据」那次。
//
// 为什么这是缺陷而不只是测试不便：`updated_at` 的精度决定了「两次真实操作」
// 是否会被合并。在设置页里，用户「改设置 → 立刻清 key」是**正常操作序列**，
// 它不该因为发生在同一秒就被静默丢弃。
//
// ⇒ 本判据钉住「ClearSecret 的意图优先于时间戳比较」：显式清空是一个
// **安全相关**的动作（用户要撤销一把凭据），不能因为时钟分辨率而被吞掉。
func TestPutClearSecretSameSecondStillClears(t *testing.T) {
	m := NewMemStore()
	const u, w, ns = "u", "w", "stt"
	sec := int64(1700000000)

	if _, err := m.Put(Record{
		UserID: u, WorkspaceID: w, Namespace: ns, ID: "minimax",
		Payload: []byte(`{}`), Secret: "sk-real", UpdatedAt: sec,
	}); err != nil {
		t.Fatal(err)
	}
	rec, _ := m.Get(u, w, ns, "minimax")
	if rec == nil || rec.Secret != "sk-real" {
		t.Fatalf("前置：key 应已存入，实际 %+v", rec)
	}

	// **同一个秒**内清空。
	res, err := m.Put(Record{
		UserID: u, WorkspaceID: w, Namespace: ns, ID: "minimax",
		Payload: []byte(`{}`), Secret: "", ClearSecret: true, UpdatedAt: sec,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Applied {
		t.Fatalf("显式清空被 LWW 当成「服务端更新」丢弃了（Applied=false）—— " +
			"用户在设置页连点两次就清不掉凭据")
	}
	after, _ := m.Get(u, w, ns, "minimax")
	if after == nil || after.Secret != "" {
		t.Errorf("清空后 Secret 应为空，实际 %q", func() string {
			if after == nil {
				return "<nil>"
			}
			return after.Secret
		}())
	}
}
