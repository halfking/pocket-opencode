package email

// raw_body_dead.go — 「这封邮件的原文在服务端已经不存在」的淘汰记账。
//
// ## 要解决的实测问题
//
// 2026-10-03 生产实测：em-10443 / em-10444（AWS 账户告警，2026-05-31）两封邮件
// 的原文已被**单独从服务器删除**（同一账户 uid 10432 / 10424 / 11 取回正常，
// 10443 / 10444 取回失败——排除了 UIDVALIDITY 变更这个解释）。于是：
//
//   - 它们每轮都进第 1.5 步的取原文预算（maxInvoiceBodyFetches=24）；
//   - 每轮都必然失败，每轮都留两条失败记录；
//   - 失败还把整步的报错文案带成「IMAP 侧问题」（pipeline.go 的
//     `invoice raw body fetch failed …（IMAP 侧问题，本轮未建档）`），
//     归因是错的：不是 IMAP 坏了，是这两封信没了。
//
// 当天 6 次取原文预算里就有 2 次花在这两封永远不会成功的邮件上。
//
// ## 为什么是「连续 N 轮」而不是「一次就淘汰」
//
// 「服务端取回 0 条消息」这个信号有两个成因，**只有第二个是永久的**：
//
//  1. 邮件被 expunge / 移出该文件夹 —— 永久；
//  2. UIDVALIDITY 变了（服务器重建邮箱后常见）—— 库里的 uid 整体失效，
//     旧 uid 取不到任何东西，但邮件本身还在，只是要用新 uid 取。
//
// 一次 0 条就永久淘汰，会在场景 2 下把**整批**邮件永久判死。所以：
//
//   - 连续 rawBodyGoneStreakThreshold 轮都观察到该信号才置 dead 标记；
//   - 标记满 rawBodyDeadRetryAfter 之后自动复检（清标记、重置 streak）。
//
// 这样即使判错了，代价上界也就是「14 天内少试两次」，而不是永久损失。
//
// ## 为什么单独查一次而不是给 Email 加字段
//
// 本仓库踩过一个很贵的坑：给 emails 加的列被某些 SELECT 漏查，于是下游拿到
// 恒零值、守卫分支在生产里从不执行、且**不产生任何错误信号**（见 store.go
// GetEmailByID 里 message_id / body_purged 那段说明）。这里的取用点只有
// 第 1.5 步一个，与其把新列塞进七条各不相同的扫描语句（每条都要记得加），
// 不如在这里单独发一条窄查询——漏查的可能是整段代码，而这里只有一段。

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// rawBodyFailureKind 是「取原文失败」的成因分类。
//
// 为什么要分类：第 1.5 步原来只有一句「（IMAP 侧问题，本轮未建档）」，
// 而实测里这些失败至少有三类，「IMAP 侧问题」对其中两类是错的。把「邮件
// 已被从服务器删除」和「POP3 来源没有原文缓存」也算成 IMAP 故障，运维就会
// 去查一个没坏的东西。
type rawBodyFailureKind int

const (
	// rawBodyFailureNone：没有失败（没有 job，或本轮被预算顺延）。
	rawBodyFailureNone rawBodyFailureKind = iota
	// rawBodyFailureGone：服务端已无此消息（两条通道都确认）。
	rawBodyFailureGone
	// rawBodyFailurePOP3：POP3 来源无原文缓存且自愈失败——与 IMAP 无关。
	rawBodyFailurePOP3
	// rawBodyFailureOther：网络/协议/凭据等。只有这一类谈得上「IMAP 侧」。
	rawBodyFailureOther
)

func (k rawBodyFailureKind) String() string {
	switch k {
	case rawBodyFailureNone:
		return "none"
	case rawBodyFailureGone:
		return "gone"
	case rawBodyFailurePOP3:
		return "pop3"
	case rawBodyFailureOther:
		return "other"
	default:
		return fmt.Sprintf("unknown(%d)", int(k))
	}
}

// classifyRawBodyFetchFailure 按**错误的身份**判定成因，不看错误文案。
//
// 刻意不用 strings.Contains：文案一改归因就静默漂移，而漂移方向恰恰是
// 「说成 IMAP 故障」——也就是回到缺陷现场。
func classifyRawBodyFetchFailure(err error) rawBodyFailureKind {
	if err == nil {
		return rawBodyFailureNone
	}
	if errors.Is(err, ErrRawBodyGone) {
		return rawBodyFailureGone
	}
	var pop3Err *pop3RawBodyError
	if errors.As(err, &pop3Err) {
		return rawBodyFailurePOP3
	}
	return rawBodyFailureOther
}

const (
	// rawBodyGoneStreakThreshold 是判定「这封邮件的原文已永久取不到」所需的
	// 连续观察轮数。见文件头「为什么是连续 N 轮而不是一次就淘汰」。
	rawBodyGoneStreakThreshold = 3

	// rawBodyDeadRetryAfter 是死信标记的保留时长，到期自动复检。
	//
	// 它是 UIDVALIDITY 误判的安全阀：真被误判的邮件最多损失两个复检轮次
	// 的取原文预算，而不是被永久排除在发票建档之外。
	rawBodyDeadRetryAfter = 14 * 24 * time.Hour
)

// migrateRawBodyDead 幂等地给 emails 加上死信记账的两列。
//
// 不能挪用既有列：processed_at 参与同步水位线
// （GREATEST(date, processed_at, created_at)），body_purged 触发「禁止回源」
// 与摘要守卫。挪用任何一个都会改变与本问题无关的行为。
func (s *Store) migrateRawBodyDead(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
		ALTER TABLE emails ADD COLUMN IF NOT EXISTS raw_body_gone_streak INTEGER NOT NULL DEFAULT 0;
		ALTER TABLE emails ADD COLUMN IF NOT EXISTS raw_body_dead_at TIMESTAMPTZ;
	`)
	return err
}

// ListRawBodyDeadEmailIDs 返回回看窗口内仍带死信标记的邮件 ID 集合。
//
// since 是 Unix 秒，与 ListEmailsSince 同口径，便于调用方只查自己关心的窗口。
func (s *Store) ListRawBodyDeadEmailIDs(ctx context.Context, since int64) (map[string]bool, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id FROM emails
		WHERE raw_body_dead_at IS NOT NULL AND date >= $1
	`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// MarkRawBodyGone 记一次「取原文没取到」的观察，返回该邮件当前的
// streak 与是否已被判定为死信。
//
// gone=false（这次失败不是「服务端没有这条消息」）会把 streak 清零：
// 网络抖动、账号临时不可用这类原因不该累积成死信。
//
// 刻意**不改 updated_at**：它是对客户端的增量同步水位线，把这类内部记账
// 写进去会让客户端反复收到它根本不关心的同步事件。
func (s *Store) MarkRawBodyGone(ctx context.Context, emailID string, gone bool) (streak int, dead bool, err error) {
	err = s.pool.QueryRow(ctx, `
		UPDATE emails SET
			raw_body_gone_streak = CASE WHEN $2 THEN COALESCE(raw_body_gone_streak, 0) + 1 ELSE 0 END,
			raw_body_dead_at = CASE
				WHEN $2 AND COALESCE(raw_body_gone_streak, 0) + 1 >= $3
					THEN COALESCE(raw_body_dead_at, now())
				WHEN $2 THEN raw_body_dead_at
				ELSE NULL
			END
		WHERE id = $1
		RETURNING COALESCE(raw_body_gone_streak, 0), raw_body_dead_at IS NOT NULL
	`, emailID, gone, rawBodyGoneStreakThreshold).Scan(&streak, &dead)
	if err != nil {
		return 0, false, fmt.Errorf("email: mark raw body gone (email=%s): %w", emailID, err)
	}
	return streak, dead, nil
}

// ReArmStaleRawBodyDead 清掉超过保留期的死信标记，让它们重新参与取原文。
//
// 返回被复检的邮件数。调用方应在本轮取原文**之前**调用。
func (s *Store) ReArmStaleRawBodyDead(ctx context.Context, olderThan time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE emails SET raw_body_dead_at = NULL, raw_body_gone_streak = 0
		WHERE raw_body_dead_at IS NOT NULL AND raw_body_dead_at < $1
	`, olderThan.UTC())
	if err != nil {
		return 0, fmt.Errorf("email: re-arm stale raw-body dead letters: %w", err)
	}
	return tag.RowsAffected(), nil
}
