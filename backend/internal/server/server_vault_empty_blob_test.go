// internal/server/server_vault_empty_blob_test.go
package server

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/adapter"
	"github.com/halfking/pocket-opencode/backend/internal/auth"
	"github.com/halfking/pocket-opencode/backend/internal/config"
)

// BUG-AG 回归：POST /api/vault/sync/ 收到**空 blob** 时直接 PutLatest 覆盖，
// 回 200 {"ok":true}。空 blob 的语义不是「清空密码箱」，而是「客户端这次没拿到数据」——
// 原生 Keystore 插件缺失时正好会走到这条路，于是用户已存的密文被静默清空，
// 而响应是「成功」、前端还会广播 vault.synced。
//
// 判据能在有缺陷一侧失败：撤掉空 blob 拦截后，
// 「空 blob 被拒」和「哨兵仍在」两条立刻红（400 → 200、blob 变空）。
func TestVaultSync_RejectsEmptyBlob(t *testing.T) {
	// 复用本包已有的 fake store（与 audit_writer_vault_test.go 同一个），
	// 避免再造一个实现会漂移的内存 store。
	store := newFakeVaultStore()

	signer, err := auth.NewSigner("test-secret-012345678901234567890123", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	token, err := signer.SignWithWorkspace("u1", "member", "ws-a")
	if err != nil {
		t.Fatal(err)
	}

	srv := newServer(
		config.Config{OpenCodeTimeoutMS: "5000"},
		adapter.NewStaticNPSAdapter(),
		adapter.NewOpenCodeHTTPAdapter(5000),
		nil, nil, nil, nil, nil, store, nil, nil, nil, nil,
		nil, nil, nil, signer, nil, nil, nil, nil, "", false, nil, nil,
	)
	h := srv.Handler()

	const wsID = "ws-a"
	const uid = "u1"
	const sentinel = "SENTINEL-BLOB-CONTENT"

	// 先放一段哨兵密文
	if err := store.PutLatest(context.Background(), wsID, uid, sentinel, 1); err != nil {
		t.Fatalf("seed PutLatest: %v", err)
	}
	blob0, ver0, err := store.GetLatest(context.Background(), wsID, uid)
	if err != nil || blob0 != sentinel {
		t.Fatalf("seed readback blob=%q ver=%d err=%v", blob0, ver0, err)
	}

	// ---- 上传空 blob：应当被拒 ----
	rr := serveWorkspaceJSON(t, h, http.MethodPost, "/api/vault/sync/", token, `{"blob":"","version":2}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("空 blob 上传 status=%d（期望 400）, body=%s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), `"ok":true`) {
		t.Fatalf("空 blob 被拒时不应回 ok:true, body=%s", rr.Body.String())
	}

	// 关键判据：哨兵必须还在，且版本号没有被推进
	blob1, ver1, err := store.GetLatest(context.Background(), wsID, uid)
	if err != nil {
		t.Fatalf("after reject readback: %v", err)
	}
	if blob1 != sentinel {
		t.Fatalf("空 blob 被拒后，哨兵密文被覆盖了：blob=%q", blob1)
	}
	if ver1 != ver0 {
		t.Fatalf("空 blob 被拒后版本号仍被推进：%d -> %d", ver0, ver1)
	}

	// ---- 纯空白也必须拒（客户端可能传 "   "） ----
	rr2 := serveWorkspaceJSON(t, h, http.MethodPost, "/api/vault/sync/", token, `{"blob":"   ","version":3}`)
	if rr2.Code != http.StatusBadRequest {
		t.Fatalf("纯空白 blob status=%d（期望 400）, body=%s", rr2.Code, rr2.Body.String())
	}
	blob2, _, _ := store.GetLatest(context.Background(), wsID, uid)
	if blob2 != sentinel {
		t.Fatalf("纯空白 blob 覆盖了哨兵：blob=%q", blob2)
	}

	// ---- 阳性对照：非空 blob 仍必须能正常上传（否则这个修复把功能堵死了） ----
	rr3 := serveWorkspaceJSON(t, h, http.MethodPost, "/api/vault/sync/", token, `{"blob":"NEW-BLOB","version":4}`)
	if rr3.Code != http.StatusOK {
		t.Fatalf("正常上传 status=%d（期望 200）, body=%s", rr3.Code, rr3.Body.String())
	}
	blob3, ver3, _ := store.GetLatest(context.Background(), wsID, uid)
	if blob3 != "NEW-BLOB" || ver3 != 4 {
		t.Fatalf("正常上传未生效：blob=%q ver=%d", blob3, ver3)
	}
}
