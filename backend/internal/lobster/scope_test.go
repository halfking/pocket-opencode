package lobster

// scope_test.go — §944 D2 判据：asset_mirrors 的**归属守卫**。
//
// ⚠⚠ 本节查出来的缺陷（**修复前实测**）：
//
//	`SyncStore.Push` 的 `ON CONFLICT (id) DO UPDATE` **不带任何谓词**
//	⇒ ⇒ ⇒ 撞上一个**别的 workspace** 的 asset_id 时，它会把那一行
//	**连 cipher_blob 一起搬进提交者的租户**
//	（SET 里**有** workspace_id 与 cipher_blob）
//	⇒ ⇒ ⇒ 受害者的加密资产**从自己的租户消失**（他的设备再也拉不到它）、
//	出现在攻击者租户里，且内容被攻击者的密文**覆盖**
//	⇒ ⇒⇒⇒ 而 handler 那一行注释自己写着「强制绑定 caller workspace，
//	**防越权**」⇒ ⇒⇒⇒ **SQL 并没有兑现 handler 承诺的那句「防越权」**。
//
// ⚠ 判据量的是**行为**（撞号后：报错 + 受害者的行一字未改），不是源码文本。
//
// ⚠ 负控（同一文件内自带）：**同一个 workspace** 用同一个 id 重推 ⇒ 仍然
//   更新那一行 ⇒ ⇒⇒ 这一腿证明「守卫没有把正常路径也挡掉」。
import (
	"context"
	"errors"
	"testing"
)

const (
	s944AssetID  = "asset-s944-victim"
	s944WSVictim = "ws-s944-victim"
	s944WSAttack = "ws-s944-attacker"
)

// s944Mirror 造一条资产镜像。
func s944Mirror(id, ws, blob string) *AssetMirror {
	return &AssetMirror{
		ID:          id,
		WorkspaceID: ws,
		Kind:        "note",
		ClientRev:   1,
		CipherBlob:  blob,
	}
}

// TestS944PushAssetFromAnotherWorkspaceIsRejected 撞号 ⇒ 报错 + 受害者的密文没被动。
func TestS944PushAssetFromAnotherWorkspaceIsRejected(t *testing.T) {
	ctx := context.Background()
	store, cleanup := newTestStore(t)
	defer cleanup()

	if _, err := store.Push(ctx, s944Mirror(s944AssetID, s944WSVictim, "victim-cipher")); err != nil {
		t.Fatalf("受害者推自己的资产应成功: %v", err)
	}

	// 攻击者拿**同一个 asset_id** 来推（handler 会把 workspace 强制成调用者的）。
	_, err := store.Push(ctx, s944Mirror(s944AssetID, s944WSAttack, "attacker-cipher"))
	// ⚠ 用 Errorf 而不是 Fatalf ⇒ 守卫被去掉时，**行被改没改**那条检查
	//   仍然会跑 ⇒ ⇒⇒ 于是「越权覆盖」这个症状**一定**出现在输出里
	//   ⇒ ⇒⇒⇒ （★ 否则 Fatalf 会在前面就把测试掐断 ⇒ 症状就看不见了
	//   ⇒ ⇒⇒⇒ ⇒ 变异「转红了」却没人知道它红在哪个症状上。）
	if err == nil {
		t.Errorf("攻击者撞 id 竟然成功了 ⇒ 归属守卫没生效（密文被搬走并覆盖）")
	} else if !errors.Is(err, ErrAssetScopeConflict) {
		t.Errorf("撞 id 应返回 ErrAssetScopeConflict，实际 %v", err)
	}

	// 受害者的行必须**一字未改**。
	got, err := store.GetMirror(ctx, s944AssetID)
	if err != nil {
		t.Fatalf("读受害者的资产失败: %v", err)
	}
	if got.CipherBlob != "victim-cipher" {
		t.Errorf("受害者的密文变成了 %q（want victim-cipher）⇒ **越权覆盖**", got.CipherBlob)
	}
	if got.WorkspaceID != s944WSVictim {
		t.Errorf("那一行被搬到了 workspace %q（want %q）⇒ 归属被搬走了", got.WorkspaceID, s944WSVictim)
	}
}

// TestS944PushSameWorkspaceStillUpdates 负控：同一个 workspace 重推 ⇒ 仍然更新。
func TestS944PushSameWorkspaceStillUpdates(t *testing.T) {
	ctx := context.Background()
	store, cleanup := newTestStore(t)
	defer cleanup()

	if _, err := store.Push(ctx, s944Mirror(s944AssetID, s944WSVictim, "cipher-v1")); err != nil {
		t.Fatalf("首次推送应成功: %v", err)
	}
	m := s944Mirror(s944AssetID, s944WSVictim, "cipher-v2")
	m.ClientRev = 2
	if _, err := store.Push(ctx, m); err != nil {
		t.Fatalf("同 workspace 重推应成功，实际: %v", err)
	}
	got, err := store.GetMirror(ctx, s944AssetID)
	if err != nil {
		t.Fatalf("读资产失败: %v", err)
	}
	if got.CipherBlob != "cipher-v2" {
		t.Errorf("重推后密文是 %q（want cipher-v2）⇒ 守卫把正常路径也挡掉了", got.CipherBlob)
	}
}
