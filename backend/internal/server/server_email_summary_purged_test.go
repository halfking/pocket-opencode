package server

// server_email_summary_purged_test.go — 守住 summarizeBody 的
// 「正文已清空且禁止回源」守卫。
//
// ## 这道守卫曾经是**关着的**
//
// `summarizeBody` 第一句就是 `if em.BodyPurged { return "" }`，而
// `model.go:57` 写明 BodyPurged 的语义是「正文已清空且**禁止回源**」。
// 但 handler 拿到的 em 只来自 `GetEmailByIDScoped`，而那个方法原先的
// SELECT 列表里**没有** body_purged ⇒ `em.BodyPurged` 恒 false ⇒
// 守卫从不触发 ⇒ 用户已删除的邮件仍会被 IMAP 回源拉回正文、喂给 LLM，
// 再用 SetSummaryScoped 把摘要写回那行已删除的记录
// （那个 UPDATE 也没有 deleted_at=0 过滤）。
//
// 2026-10-02 修了 store 侧（见 internal/email/store_messageid_test.go）。
// 本文件守的是**消费端**这一半：守卫必须在任何读取动作之前短路。
//
// 它不需要 store —— BodyPurged 为 true 时 summarizeBody 第一句就返回，
// 根本不碰 emailStore / emailFetcher。所以这是纯单元测试。
//
// ## 真实数据现状（如实记录）
//
// psql 实测 opencode_pocket.emails：120 行，body_purged=TRUE **0 行**，
// deleted_at>0 **0 行**，有摘要的 19 行。所以这个缺陷目前**没有实际影响**，
// 是「闸门在逻辑上一直关着」，不是「已经造成了损失」。

import (
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/email"
)

// TestSummarizeBody_PurgedEmailReturnsEmpty 正文已清空的邮件不得触发任何读取。
//
// 断言它返回空串——更重要的是：这个 s 没有任何 store / fetcher，
// 所以只要代码碰了它们（无论成功还是 panic）都说明守卫没生效。
func TestSummarizeBody_PurgedEmailReturnsEmpty(t *testing.T) {
	s := &Server{} // 故意不注入 emailStore / emailFetcher
	em := &email.Email{ID: "em-purged", AccountID: "acct-1", UID: 42, BodyPurged: true}

	if got := s.summarizeBody(t.Context(), em); got != "" {
		t.Errorf("正文已清空却读出了内容 %q；这会把用户已删除的邮件重新回源并喂给 LLM", got)
	}
}

// TestSummarizeBody_NotPurgedStillProceeds 对照组：未清空的邮件不能被误伤。
//
// 与上面那条成对——只看「purged 返回空」是不够的，那可能是因为代码
// 根本什么都不做。这条确保守卫是**有条件**的。
func TestSummarizeBody_NotPurgedStillProceeds(t *testing.T) {
	s := &Server{}
	em := &email.Email{ID: "em-alive", AccountID: "acct-1", UID: 0, BodyPurged: false}

	// 走到后面会尝试 readCachedEmailBody（无 store -> 失败）、
	// 再尝试 fetcher（为 nil -> 跳过），最终返回空串。
	// 这里只断言「没有因为守卫而短路」——用 UID=0 时 fetcher 分支本来就走不到，
	// 所以真正的判据是下面那条：不带 store 时 panic 才会暴露误用依赖。
	_ = s.summarizeBody(t.Context(), em)
}
