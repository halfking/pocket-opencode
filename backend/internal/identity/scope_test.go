package identity

// scope_test.go — §944 D3 判据：devices 的**归属守卫**。
//
// ⚠⚠ 本节查出来的缺陷（**修复前实测**）：
//
//	`Store.UpsertDevice` 的 `ON CONFLICT (id) DO UPDATE` **不带任何谓词**
//	⇒ ⇒ ⇒ 撞上一个**别的用户**的 device id 时，它会把那一行
//	**连 workspace_id 一起搬进提交者的租户**（SET 里**有** workspace_id）
//	⇒ ⇒ ⇒ 受害者的设备行**从自己的租户消失**（他在自己的设备列表里再也
//	看不到、也撤不掉它）、出现在攻击者租户里，且 push_token 被换成攻击者的
//	⇒ ⇒⇒⇒ 推送通道被劫持 + 设备审计丢失。
//
// ⚠⚠ **为什么守卫断言的是 user_id 而不是 workspace_id**：
//
//	★ §944.2 的通用口径：**只断言「不可搬走」的归属列**。
//	· user_id **不在** SET 列表里 ⇒ 它**永远不会被搬走** ⇒ 它就是
//	  「这一行属于谁」的唯一稳定锚点 ⇒ ⇒ 撞号时它对不上 ⇒ 必须挡。
//	· workspace_id **在** SET 列表里 ⇒ 「设备跟随主人换租户」是这条路径
//	  的**既有语义** ⇒ ⇒⇒ 若把 workspace_id 也断言住，就会把这条正常路径
//	  **误伤**。
//	⇒ ⇒⇒ 所以负控里**必须**有「同一个用户换 workspace 重注册仍然成功」
//	  这一腿——它把「守卫没有误伤既有语义」钉住。
//
// ⚠ 判据量的是**行为**（撞号后：报错 + 受害者的行一字未改），不是源码文本。
import (
	"context"
	"errors"
	"testing"
)

const (
	s944DeviceID = "device-s944-victim"
	s944DevUser  = "user-s944-victim"
	s944DevOther = "user-s944-attacker"
	s944DevWS    = "ws-s944-victim"
	s944DevWS2   = "ws-s944-other"
)

// s944Device 造一条设备行。
func s944Device(id, user, ws, pushToken string) *Device {
	return &Device{
		ID:          id,
		UserID:      user,
		WorkspaceID: ws,
		Fingerprint: "fingerprint-" + id,
		PushToken:   pushToken,
		OS:          "iOS",
	}
}

// TestS944UpsertDeviceOfAnotherUserIsRejected 撞号 ⇒ 报错 + 受害者的设备行一字未改。
func TestS944UpsertDeviceOfAnotherUserIsRejected(t *testing.T) {
	ctx := context.Background()
	store, cleanup := newTestStore(t)
	defer cleanup()

	if err := store.UpsertDevice(ctx, s944Device(s944DeviceID, s944DevUser, s944DevWS, "victim-push-token")); err != nil {
		t.Fatalf("受害者注册设备应成功: %v", err)
	}

	err := store.UpsertDevice(ctx, s944Device(s944DeviceID, s944DevOther, s944DevWS2, "attacker-push-token"))
	// ⚠ 用 Errorf 而不是 Fatalf ⇒ 守卫被去掉时，**行被改没改**那条检查
	//   仍然会跑 ⇒ ⇒⇒ 「越权覆盖」这个症状**一定**出现在输出里。
	if err == nil {
		t.Errorf("攻击者撞 id 竟然成功了 ⇒ 归属守卫没生效（设备行被搬走 + push_token 被换）")
	} else if !errors.Is(err, ErrDeviceScopeConflict) {
		t.Errorf("撞 id 应返回 ErrDeviceScopeConflict，实际 %v", err)
	}

	devs, err := store.ListDevices(ctx, s944DevWS)
	if err != nil {
		t.Fatalf("列设备失败: %v", err)
	}
	if len(devs) != 1 {
		t.Fatalf("受害者的租户里应有 1 台设备，实际 %d 台 ⇒ 那一行被搬走了", len(devs))
	}
	if devs[0].PushToken != "victim-push-token" {
		t.Errorf("受害者的 push_token 变成了 %q ⇒ **越权覆盖**", devs[0].PushToken)
	}
	if devs[0].WorkspaceID != s944DevWS {
		t.Errorf("设备行的 workspace 变成了 %q（want %q）⇒ 归属被搬走了", devs[0].WorkspaceID, s944DevWS)
	}
	if devs[0].UserID != s944DevUser {
		t.Errorf("设备行的 user_id 变成了 %q（want %q）", devs[0].UserID, s944DevUser)
	}
}

// TestS944UpsertDeviceSameUserAcrossWorkspacesStillWorks 负控①：同一用户刷新设备 ⇒ 照常更新。
func TestS944UpsertDeviceSameUserAcrossWorkspacesStillWorks(t *testing.T) {
	ctx := context.Background()
	store, cleanup := newTestStore(t)
	defer cleanup()

	if err := store.UpsertDevice(ctx, s944Device(s944DeviceID, s944DevUser, s944DevWS, "token-v1")); err != nil {
		t.Fatalf("首次注册应成功: %v", err)
	}
	if err := store.UpsertDevice(ctx, s944Device(s944DeviceID, s944DevUser, s944DevWS, "token-v2")); err != nil {
		t.Fatalf("同一用户刷新设备应成功，实际: %v", err)
	}
	devs, err := store.ListDevices(ctx, s944DevWS)
	if err != nil {
		t.Fatalf("列设备失败: %v", err)
	}
	if len(devs) != 1 {
		t.Fatalf("应有 1 台设备，实际 %d", len(devs))
	}
	if devs[0].PushToken != "token-v2" {
		t.Errorf("刷新后 push_token 是 %q（want token-v2）", devs[0].PushToken)
	}
}

// TestS944UpsertDeviceSameUserInAnotherWorkspaceStillFollows 负控②：同一用户换 workspace ⇒ 设备跟着走。
//
// ⚠ 这一腿把「设备跟随主人跨租户」这条**既有语义**钉住
//
//	（SET 里**有** workspace_id 就是为了这个）⇒ ⇒⇒ 如果守卫误把它挡了，
//	这一腿会红 ⇒ ⇒⇒⇒ 于是「守卫该断言哪一列」这件事就有了判据。
func TestS944UpsertDeviceSameUserInAnotherWorkspaceStillFollows(t *testing.T) {
	ctx := context.Background()
	store, cleanup := newTestStore(t)
	defer cleanup()

	if err := store.UpsertDevice(ctx, s944Device(s944DeviceID, s944DevUser, s944DevWS, "token-v1")); err != nil {
		t.Fatalf("首次注册应成功: %v", err)
	}
	if err := store.UpsertDevice(ctx, s944Device(s944DeviceID, s944DevUser, s944DevWS2, "token-v2")); err != nil {
		t.Fatalf("同一用户换 workspace 重注册应成功（设备跟随主人），实际: %v", err)
	}
	devs, err := store.ListDevices(ctx, s944DevWS2)
	if err != nil {
		t.Fatalf("列设备失败: %v", err)
	}
	if len(devs) != 1 {
		t.Fatalf("新 workspace 里应有 1 台设备，实际 %d ⇒ 守卫误伤了「设备跟随主人」", len(devs))
	}
	if devs[0].WorkspaceID != s944DevWS2 {
		t.Errorf("设备行的 workspace 是 %q（want %q）", devs[0].WorkspaceID, s944DevWS2)
	}
}
