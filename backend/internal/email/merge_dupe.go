package email

// merge_dupe.go — 合并同一封邮件的 IMAP/POP3 重复副本（受门禁的一次性数据修复）。
//
// 背景（2026-10-01 三层预演实证）：
//   - §7v  43 组同 (account,from,subject,date) 的候选 -> 40 组是 IMAP1+POP31
//   - §7w  加真实 Message-ID 比对 -> 29 真副本 / 2 NOT-DUP / 9 取不回来
//   - §7x  加状态可迁移性     -> 17 组，且**零字段需迁移**（IMAP 侧已完整）
//
// 合并策略：保留 IMAP 侧（真实 IMAP UID、可再 FETCH、message_id 已真实），
// 对 POP3 侧只打**墓碑**（deleted_at），**不清数据**——
// 刻意**不复用** SoftDeleteEmailsScoped：那个是「用户删除」语义，会顺手
// body_purged=TRUE / snippet='' / body_path=NULL，把刚回填的原文缓存抹掉。
//
// **三重安全闸**（任一不满足就跳过该组，绝不猜测）：
//   1. POP3 侧必须是 em-pop3- 前缀（IMAP 侧反之）；
//   2. 两侧**真实 Message-ID 必须相等**（由调用方回填比对后传入，本函数不联网）；
//   3. 保留侧必须持有 IMAP UID > 0（位置序号当 UID 的那侧不能当保留侧）。
//
// 本文件只提供**纯逻辑**（判定 + 计划），DB 写操作在 merge_dupe_store.go，
// 网络回填在 diag_backfill_align_test.go。分层是为了让判定可脱离 DB 单测。

import (
	"fmt"
	"strings"
)

// MergeSide 描述一组重复副本的两侧。
type MergeSide struct {
	EmailID string
	IsPOP3  bool
	UID     int64
	// RealMessageID 是**回填后**的真实 Message-ID（POP3 侧由 UIDL RETR 解析得到）。
	RealMessageID string
	// HasInvoice 表示该侧挂着 email_invoices 记录。
	HasInvoice bool
}

// MergePlan 是一组重复副本的合并计划。
type MergePlan struct {
	KeepEmailID string // 保留（IMAP 侧）
	TombstoneID string // 打墓碑（POP3 侧）
	Subject     string
	Reason      string
}

// ErrSkipMerge 表示这一组不满足合并条件，应原样跳过。
var ErrSkipMerge = fmt.Errorf("merge: group does not qualify")

// planMergeDupes 对一组候选给出合并计划。**纯函数**。
//
// 候选由调用方按 (account, from, subject, date) 聚类得到，但**同主题同时刻
// 可能是两封不同的邮件**（§7w 实测 `【network-switch】邮件通道测试` 就是
// 两封），所以必须靠真实 Message-ID 相等来确认。
func planMergeDupes(a, b MergeSide, subject string) (MergePlan, error) {
	// 闸 1：一侧 POP3、一侧 IMAP。两侧同源不算重复副本。
	if a.IsPOP3 == b.IsPOP3 {
		return MergePlan{}, fmt.Errorf("%w: both sides same source (imap=%v)", ErrSkipMerge, a.IsPOP3)
	}
	keep, drop := a, b
	if a.IsPOP3 {
		keep, drop = b, a
	}
	// 闸 2：真实 Message-ID 相等才算同一封。任一缺失就不动。
	if keep.RealMessageID == "" || drop.RealMessageID == "" {
		return MergePlan{}, fmt.Errorf("%w: missing real message-id (keep=%q drop=%q)",
			ErrSkipMerge, keep.RealMessageID, drop.RealMessageID)
	}
	if !strings.EqualFold(keep.RealMessageID, drop.RealMessageID) {
		return MergePlan{}, fmt.Errorf("%w: message-id differs (keep=%q drop=%q) — two different emails",
			ErrSkipMerge, keep.RealMessageID, drop.RealMessageID)
	}
	// 闸 3：保留侧必须持有真实 IMAP UID。位置序号（POP3 侧 uid）不能当 IMAP UID。
	if keep.UID <= 0 {
		return MergePlan{}, fmt.Errorf("%w: keep side has no IMAP uid (%s)", ErrSkipMerge, keep.EmailID)
	}
	reason := "same real message-id; keeping IMAP side (real UID + fetchable)"
	if keep.HasInvoice || drop.HasInvoice {
		reason += "; invoice present"
	}
	return MergePlan{
		KeepEmailID: keep.EmailID,
		TombstoneID: drop.EmailID,
		Subject:     subject,
		Reason:      reason,
	}, nil
}
