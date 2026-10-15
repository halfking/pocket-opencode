package usersetting

import "encoding/json"

// Record is one user-scoped configuration document.
type Record struct {
	UserID      string          `json:"userId"`
	WorkspaceID string          `json:"workspaceId"`
	Namespace   string          `json:"namespace"`
	ID          string          `json:"id"`
	Payload     json.RawMessage `json:"payload"`
	UpdatedAt   int64           `json:"updatedAt"`
	HasSecret   bool            `json:"hasSecret,omitempty"`
	Secret      string          `json:"-"`

	// ClearSecret 显式要求**清空**已存的密文。
	//
	// ★ 为什么需要它，而不是用空 Secret 表示（2026-10-08 真机验证踩到）：
	//   `Secret == ""` 在实现里表示「**本次不改动密文**」——
	//   MemStore.Put 有 `if ok && rec.Secret == "" { rec.Secret = existing.Secret }`
	//   （见 mem_store.go），PG 层同义。这么设计本身是对的：设置保存要能
	//   「只改 payload、不碰凭据」，而多数调用方手上没有密文（不回显）。
	//
	//   但它让「清空」**无法表达**：传空串会被当成「不改动」，
	//   于是上层那个 `__clear__` 哨兵从上线起就没生效过 ——
	//   实测现象是用户点「清除 API Key」，界面上 hasApiKey 一直为 true，
	//   转写继续拿旧凭据去跑，恒 401，而用户没有任何办法解决。
	//
	//   两种语义必须由**两个不同字段**承载，不能共用一个空串。
	//   本字段为 true 时，实现层必须把已存密文清掉并让 HasSecret 变 false。
	ClearSecret bool `json:"clearSecret,omitempty"`
}

// PutResult is the LWW outcome of a PUT.
type PutResult struct {
	Applied  bool   `json:"applied"`
	Conflict bool   `json:"conflict"`
	Record   Record `json:"record"`
}

// Repository is the persistence seam used by HTTP handlers and tests.
type Repository interface {
	List(userID, workspaceID string) ([]Record, error)
	Get(userID, workspaceID, namespace, id string) (*Record, error)
	Put(rec Record) (*PutResult, error)
}
