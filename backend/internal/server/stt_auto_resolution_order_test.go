// stt_auto_resolution_order_test.go —— §468
//
// 钉住 **auto 通道的回落顺序** 与**各通道的默认模型**。
//
// ── 为什么这道门存在 ────────────────────────────────────────────────
//
// `resolveSTTTarget` 的 auto 分支有一段**很长的理由注释**，论证
// 「MiniMax 必须排在外部之前」（会议要长录音 + 说话人标签 + 即时出字，
// 三件套只有 MiniMax 那一档同时满足；且只有用户**已申请了 MiniMax key**
// 才进这条路）。
//
// ⚠ 而这条**直接影响花钱**的决策顺序：
//
//	网关 → MiniMax（asr-1.0，$0.38/h）→ 外部（gpt-4o-mini-transcribe，$0.18/h）
//
//	⇒ **未配 MiniMax key 时的实际默认是 $0.18/h，比清单里最便宜的贵 15 倍。**
//
// 既有门覆盖的是**错误诚实性**（`TestResolveTargetWithoutMiniMaxKeyIsHonest`
// 钉住「没配 key 不许静默回退到别的通道」）、**auto 会落到外部**
// （`TestResolveTargetAutoFallsBackToExternal`）、以及「手工指定模型不可用不许静默换」。
// ⛔ **没有任何一条断言「auto 里 MiniMax 先于外部」，也没有任何一条断言两个默认模型名。**
//
// ⇒ 缺口很具体：**把 external 挪到 minimax 前面**（这正是一个「优化默认成本」的
// 后人会做的改动）会让门**全绿**，而它把默认成本翻了倍。
//
// 本门是**行为级**的：真的构造 server、真的调 `resolveSTTTarget`，
// 不是源码文本断言 —— 同 [[报缺陷前先读那一行 helper]]：
// 只断言「代码里写着 minimaxTarget() 在前」证明不了它真的会先被试。

package server

import (
	"context"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/stt"
	"github.com/halfking/pocket-opencode/backend/internal/usersetting"
)

// seedMiniMaxKey 单独播种 MiniMax 凭据（stt/minimax 记录的 Secret）。
//
// ⚠ 为什么要单独一条记录：`usersetting.Record.Secret` 是**单值**，
// 外部 key 与 MiniMax key 共用 `stt/default` 载荷但 Secret 分开存
// （见 server_stt_settings.go 的 sttSettingsMiniMaxID）。
func seedMiniMaxKey(userID, wsID, secret string) usersetting.Record {
	return usersetting.Record{
		UserID: userID, WorkspaceID: wsID,
		Namespace: "stt", ID: sttSettingsMiniMaxID,
		Secret: secret, UpdatedAt: 1,
	}
}

// TestAutoPrefersMiniMaxOverExternal —— 三条通道里只有 MiniMax 与外部可用时，
// 必须选 **MiniMax**（成本更高、能力更全的那一档）。
//
// 这条断言的是**顺序**，不是「能不能拿到目标」：
// 既有门只证明了「auto 会落到外部」，没证明「外部不是第一顺位」。
func TestAutoPrefersMiniMaxOverExternal(t *testing.T) {
	srv, store := sttTestServer(t, "") // 无网关 key ⇒ 网关通道不通

	// 只给外部 key，不给模型名 ⇒ 走外部默认档。
	store.Put(seedSTTSetting("ord-user", "ord-ws",
		`{"channel":"auto","externalBaseURL":"https://api.openai.com/v1"}`,
		"sk-external-only"))
	store.Put(seedMiniMaxKey("ord-user", "ord-ws", "sk-minimax"))

	target, err := srv.resolveSTTTarget(context.Background(),
		stt.Scope{UserID: "ord-user", WorkspaceID: "ord-ws"})
	if err != nil {
		t.Fatalf("auto 通道应能解析出目标：%v", err)
	}
	if target.Channel != stt.ChannelMiniMax {
		t.Fatalf("auto 里 MiniMax 必须排在外部之前，实测拿到 %q（模型 %q）。\n"+
			"  ⚠ 若是有意改成「优先选便宜的」：\n"+
			"    · 这会让默认成本从 $0.18/h 变成 $0.38/h（×2.1）；\n"+
			"    · 理由注释里说的「会议要说话人标签 + 即时出字」会落空；\n"+
			"    · 那属于产品决定 ⇒ 先更新简报 #14，再回来改这里。",
			target.Channel, target.Model)
	}
	if target.Model != "asr-1.0" {
		t.Errorf("MiniMax 通道的默认模型应是 asr-1.0，实测 %q", target.Model)
	}
}

// TestAutoFallsBackToExternalDefaultModel —— 没有网关、没有 MiniMax key 时，
// 实际默认是外部的 **gpt-4o-mini-transcribe**（$0.18/h）。
//
// 这条把「默认落在哪一档」钉成事实：它比清单里最便宜的档贵 15 倍，
// 而「要不要为了便宜牺牲中文精度」是简报 #14 待拍的产品问题
// —— 但**在拍板之前，默认档是什么必须写死**，否则没人知道自己在付什么。
func TestAutoFallsBackToExternalDefaultModel(t *testing.T) {
	srv, store := sttTestServer(t, "")

	store.Put(seedSTTSetting("ord-user2", "ord-ws2",
		`{"channel":"auto","externalBaseURL":"https://api.openai.com/v1"}`,
		"sk-external-only"))
	// ⚠ 刻意**不**播种 MiniMax key ⇒ 只剩外部一条路。

	target, err := srv.resolveSTTTarget(context.Background(),
		stt.Scope{UserID: "ord-user2", WorkspaceID: "ord-ws2"})
	if err != nil {
		t.Fatalf("auto 通道应回退到外部：%v", err)
	}
	if target.Channel != stt.ChannelExternal {
		t.Fatalf("无 MiniMax key 时应落到外部，实测 %q", target.Channel)
	}
	if target.Model != "gpt-4o-mini-transcribe" {
		t.Errorf("外部通道的默认模型应是 gpt-4o-mini-transcribe，实测 %q\n"+
			"  ⚠ 模型名变了要连带更新简报 #14 的默认档结论", target.Model)
	}
	// 成本字段必须**有值**而不是 0/未知：用户要看的是「这一档多少钱」。
	if target.CostUSDPerHour <= 0 {
		t.Errorf("默认档的 CostUSDPerHour 应来自 KnownUSDPerHour，实测 %v（0 = 界面显示「未知」）",
			target.CostUSDPerHour)
	}
}
