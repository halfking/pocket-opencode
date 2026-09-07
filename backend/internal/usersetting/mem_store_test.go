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
