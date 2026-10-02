package email

// raw_body_resolve.go — 「取一封邮件的完整原文」的**唯一**实现，POP3 感知。
//
// ## 为什么要有这个文件
//
// 取原文这件事在本包里散落过两套实现，而它们的能力不一样：
//
//	pipeline.go 的第 2 趟（fetchInvoiceBodies）
//	    → 直接 p.Fetcher.FetchMessageRaw(ctx, e.AccountID, e.UID)
//
//	harvestOne（采集器）
//	    → POP3 来源先读 BodyCache，未命中走 POP3 位置序号自愈，再走 IMAP 反查
//
// 采集器那套是 2026-10-02 为 BUG-AV 加的，pipeline 那套**从没跟上**。后果
// （2026-10-03 04:55 实测，handoff §7.4.4）：
//
// 两封真实「通行费电子发票」（em-pop3-…-ZL0014_…，合计 24.61 元）在库里
// 一直没有台账行。流水线第 2 趟取原文必然失败（FetchMessageRaw 在 mime.go:98
// 无条件 dial IMAPHost:IMAPPort，是 IMAP 专用），于是永远建不了档。
//
// 而它们的原文**就在磁盘上**（data/email-bodies-raw/<id>.bin，POP3 同步时落盘），
// 采集器读的就是那份缓存。
//
// 两套实现并存还会**漂移**：改一边忘了另一边，下一次缺陷以同样的形态复发，
// 而这次已经复发过了。所以合并成一份。
//
// ## POP3 位置序号陷阱（这段是本文件存在的理由，不要绕过）
//
// POP3 降级路径落库时给的 `UID` 是**位置序号**（第几封），不是 IMAP UID。
// 拿它去 `UID FETCH` 会取到**毫不相干的另一封邮件**——把它当这封发票的原文
// 解析，就会把别人的邮件存成这封的发票 PDF。宁可失败，绝不返回「疑似」的原文。
//
// 每一条自愈路径拿到原文后都要过 sameEmailMessage 校验；校验不过就丢弃并
// 继续下一条，绝不放行。

import (
	"context"
	"fmt"
	"log"
)

// rawBodySource 说明原文是从哪儿取到的。只用于日志与诊断。
type rawBodySource string

const (
	// rawBodyIMAPUID：IMAP 来源邮件，正常走 UID FETCH。
	rawBodyIMAPUID rawBodySource = "imap-uid-fetch"
	// rawBodyCache：POP3 来源，命中同步时落盘的原文缓存。
	rawBodyCache rawBodySource = "body-cache"
	// rawBodyPOP3Index：POP3 来源，缓存未命中，用位置序号回 POP3 RETR 补取。
	rawBodyPOP3Index rawBodySource = "pop3-index-selfheal"
	// rawBodyIMAPRealUID：POP3 来源，POP3 补取失败，改用 IMAP SEARCH 反查真实 UID。
	rawBodyIMAPRealUID rawBodySource = "imap-real-uid-selfheal"
	// rawBodyFailed：一条都没成功。
	rawBodyFailed rawBodySource = "failed"
)

// pop3RawBodyError 是 POP3 来源取原文失败的错误。
// 分开两个字段是为了**逐字保留**原实现写进 invoice_harvest.go 的错误文案：
// 有既有测试与人工排障记录依赖那两句措辞，改写会让它们对不上。
type pop3RawBodyError struct {
	NoCache     bool
	CacheErr    error
	SelfHealErr error
}

func (e *pop3RawBodyError) Error() string {
	if e.NoCache {
		return "POP3-sourced email and no raw body cache configured; " +
			"refusing to IMAP-FETCH a positional index (would fetch the wrong message)"
	}
	return fmt.Sprintf("POP3-sourced email raw body cache miss (err=%v) and self-heal failed: %v; "+
		"refusing to IMAP-FETCH a positional index (would fetch the wrong message)",
		e.CacheErr, e.SelfHealErr)
}

// resolveRawBody 取一封邮件的完整原文（RFC 5322 字节）。
//
// em 为 POP3 来源（isPOP3SourcedEmail）时**绝不**把 em.UID 当 IMAP UID 用，
// 顺序固定为：BodyCache → POP3 位置序号 RETR → IMAP SEARCH 反查真实 UID。
// 每一步取回的原文都过 sameEmailMessage 校验。
//
// 非 POP3 来源只走 IMAP UID FETCH，行为与原实现一致。
//
// label 只进日志，用来区分调用方（原来采集器的日志里带 invoice=%s）。
func resolveRawBody(
	ctx context.Context,
	fetcher *Fetcher,
	cache BodyCache,
	em *Email,
	label string,
) ([]byte, rawBodySource, error) {
	if em == nil {
		return nil, rawBodyFailed, fmt.Errorf("resolveRawBody: nil email")
	}
	if !isPOP3SourcedEmail(*em) {
		if fetcher == nil {
			return nil, rawBodyFailed, fmt.Errorf("resolveRawBody: fetcher not configured")
		}
		raw, err := fetcher.FetchMessageRaw(ctx, em.AccountID, em.UID)
		if err != nil {
			return nil, rawBodyIMAPUID, err
		}
		return raw, rawBodyIMAPUID, nil
	}

	// ── POP3 来源 ──────────────────────────────────────────────────────
	// 见文件头「POP3 位置序号陷阱」：以下每一步都不使用 IMAP UID FETCH。
	//
	// 判据的**顺序**是有讲究的，不能调换：先判 BodyCache，再判 fetcher。
	// 运维看到的失败原因必须指向真正缺的那一样——POP3 邮件连原文缓存都没有时，
	// 报「fetcher 没配」会让人去查一个其实没坏的东西（pop3_uid_test.go
	// 的 TestHarvestOne_RefusesPOP3PositionalUID 就是钉这条的：Fetcher 与
	// BodyCache 同时为 nil 时，LastError 必须含 POP3）。
	if cache == nil {
		return nil, rawBodyFailed, &pop3RawBodyError{NoCache: true}
	}
	cached, cerr := cache.Get(em.ID, em.UID)
	if cerr == nil && len(cached) > 0 {
		return cached, rawBodyCache, nil
	}
	// 缓存未命中，下面两条自愈腿都需要 fetcher。
	if fetcher == nil {
		return nil, rawBodyFailed, &pop3RawBodyError{
			CacheErr:    cerr,
			SelfHealErr: fmt.Errorf("no fetcher configured for self-heal"),
		}
	}

	var selfHealErr error
	// 路径 1：POP3 位置序号补取（首选）。位置序号在 POP3 侧有效。
	if em.UID > 0 {
		raw, err := fetcher.RefetchPOP3RawByIndex(ctx, em.AccountID, int(em.UID))
		switch {
		case err != nil:
			log.Printf("[email/raw-body]%s self-heal POP3 index=%d failed: %v", label, em.UID, err)
			selfHealErr = err
		case len(raw) == 0:
			selfHealErr = fmt.Errorf("POP3 index=%d returned empty", em.UID)
			log.Printf("[email/raw-body]%s self-heal POP3 index=%d returned empty", label, em.UID)
		case !sameEmailMessage(em, raw):
			selfHealErr = fmt.Errorf("POP3 index=%d returned a DIFFERENT message", em.UID)
			log.Printf("[email/raw-body]%s self-heal POP3 index=%d returned a DIFFERENT message — discarded", label, em.UID)
		default:
			log.Printf("[email/raw-body]%s self-heal via POP3 index=%d (same message confirmed)", label, em.UID)
			return raw, rawBodyPOP3Index, nil
		}
	}

	// 路径 2：IMAP SEARCH 反查真实 UID。邮件已同步到 IMAP 时才可能命中。
	realUID, rerr := fetcher.ResolveRealUIDByHeader(ctx, em.AccountID, em.FromAddress, em.Subject, em.Date)
	if rerr == nil && realUID > 0 {
		raw, ferr := fetcher.FetchMessageRaw(ctx, em.AccountID, realUID)
		switch {
		case ferr != nil:
			selfHealErr = fmt.Errorf("IMAP fetch by resolved uid=%d: %w", realUID, ferr)
			log.Printf("[email/raw-body]%s self-heal IMAP real uid=%d failed: %v", label, realUID, ferr)
		case !sameEmailMessage(em, raw):
			selfHealErr = fmt.Errorf("IMAP resolved uid=%d returned a DIFFERENT message", realUID)
			log.Printf("[email/raw-body]%s self-heal IMAP resolved uid=%d returned a DIFFERENT message — discarded", label, realUID)
		default:
			log.Printf("[email/raw-body]%s self-heal via IMAP real uid=%d (same message confirmed)", label, realUID)
			return raw, rawBodyIMAPRealUID, nil
		}
	} else {
		if selfHealErr == nil {
			selfHealErr = rerr
		}
		log.Printf("[email/raw-body]%s IMAP real-UID resolve failed: %v", label, rerr)
	}

	return nil, rawBodyFailed, &pop3RawBodyError{CacheErr: cerr, SelfHealErr: selfHealErr}
}
