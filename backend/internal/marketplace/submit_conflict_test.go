package marketplace

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestSubmitDuplicateVersionIsConflict 锁定 BUG-Z：
// 同一 workspace + 同名包 + **同一版本号**重复提交，撞
// marketplace_versions_pkey 唯一约束。
//
// 修之前原始 pgx 错误一路冒泡到 server 层 writeMarketplaceError 的
// default 分支，被写成 **500 Internal Server Error**。但这是客户端重复提交
// 造成的（换个版本号就能继续），不该被当成服务端故障 —— 而且前端会把 5xx
// 当作可重试反复重试。与已修的 BUG-M 同一类。
//
// 修法：Submit 用 wrapUniqueViolation 把 SQLSTATE 23505 包成
// ErrMarketplaceConflict，server 层据此映射到 409。
//
// 判据必须能区分通/不通：
//   - `errors.Is(err, ErrMarketplaceConflict)` 为 false  → 没修好
//   - 错误里仍含 "23505"/"duplicate key" 原始串        → 没被翻译
//
// 另外单独验证 wrapUniqueViolation(nil) 与非 23505 错误**原样透传**，
// 避免这个助手变成「把所有错误都吞成 409」。
func TestSubmitDuplicateVersionIsConflict(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	req := SubmitRequest{
		WorkspaceID: "ws-bugz",
		Name:        "报告助手",
		Kind:        "skill",
		Version:     "1.0.0",
		Digest:      "sha256:bugz",
	}

	first, err := store.Submit(ctx, req)
	if err != nil {
		t.Fatalf("首次提交应成功: %v", err)
	}
	if first.VersionID == "" {
		t.Fatal("首次提交返回了空 version_id")
	}

	// 第二次：同名 + 同版本 → 必然撞 pkey
	_, err = store.Submit(ctx, req)
	if err == nil {
		t.Fatal("重复提交同名同版本应当失败，却成功了 —— 唯一约束没生效？")
	}

	if !errors.Is(err, ErrMarketplaceConflict) {
		t.Fatalf("重复提交应返回 ErrMarketplaceConflict（server 据此映射 409），实际 = %v", err)
	}
	if strings.Contains(err.Error(), "23505") || strings.Contains(err.Error(), "duplicate key") {
		t.Errorf("原始 pgx 错误串泄漏到对外文案里，应翻译成面向用户/客户端的冲突说明: %v", err)
	}
	t.Logf("重复提交返回: %v", err)
}

// TestSubmitDifferentVersionSucceeds 对照组：
// **只把版本号改掉**就该成功（409 只针对真正的重复，不是「这个包存在过」）。
// 没有这条对照，一个「凡是有包就返回 409」的错误实现也能通过上面的测试。
func TestSubmitDifferentVersionSucceeds(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	base := SubmitRequest{
		WorkspaceID: "ws-bugz2",
		Name:        "报告助手",
		Kind:        "skill",
		Version:     "1.0.0",
		Digest:      "sha256:a",
	}
	if _, err := store.Submit(ctx, base); err != nil {
		t.Fatalf("首次提交应成功: %v", err)
	}

	next := base
	next.Version = "1.0.1"
	next.Digest = "sha256:b"
	if _, err := store.Submit(ctx, next); err != nil {
		t.Fatalf("换版本号后应成功，却失败: %v", err)
	}
}

// TestWrapUniqueViolationPassthrough 证明这个助手**不是**「把所有错误都变成 409」：
// 非 23505 的错误必须原样返回，nil 必须返回 nil。
func TestWrapUniqueViolationPassthrough(t *testing.T) {
	if got := wrapUniqueViolation(nil, "x"); got != nil {
		t.Errorf("wrapUniqueViolation(nil) 应返回 nil，实际 %v", got)
	}
	plain := errors.New("connection refused")
	if got := wrapUniqueViolation(plain, "x"); !errors.Is(got, plain) || got != plain {
		t.Errorf("非唯一约束错误应原样透传，实际 %v", got)
	}
	if errors.Is(wrapUniqueViolation(plain, "x"), ErrMarketplaceConflict) {
		t.Error("非唯一约束错误不应被包成 ErrMarketplaceConflict")
	}
}
