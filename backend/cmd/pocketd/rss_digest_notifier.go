// rss_digest_notifier.go — 把每日摘要接到通知中心。
//
// 用户的原话是"每天收到一份全部信息的摘要"。只有日报页而没有推送，
// 就等于要求用户自己记得每天打开 —— 那不叫"收到"。所以日报生成后
// 立刻走一次 notifycenter：前台 WS 立刻到，后台进入 inbox。
package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/halfking/pocket-opencode/backend/internal/notifycenter"
	"github.com/halfking/pocket-opencode/backend/internal/rss"
)

// rssDigestNotifier 把 rss.DigestService 的投递出口接到 notifycenter。
type rssDigestNotifier struct{ svc *notifycenter.Service }

// NotifyDigest 生成"今日摘要已就绪"的通知。
//
// 通知体只放概览（N 条 / 分类分布 / 前几条标题），不放全文：通知是唤醒
// 手段，全文留给日报页与分享，避免把推送做成一个读不完的长文本。
func (n *rssDigestNotifier) NotifyDigest(ctx context.Context, sc rss.Scope, d *rss.Digest) error {
	if n == nil || n.svc == nil || d == nil {
		return nil
	}
	payload, err := json.Marshal(map[string]any{
		"date":        d.Date,
		"itemCount":   d.ItemCount,
		"sourceCount": d.SourceCount,
		"route":       "rss-digest",
	})
	if err != nil {
		return fmt.Errorf("rss digest payload: %w", err)
	}
	title := "今日信息摘要"
	if d.ItemCount == 0 {
		title = "今日信息摘要（暂无新内容）"
	}
	_, err = n.svc.Dispatch(ctx, notifycenter.Event{
		WorkspaceID: sc.WorkspaceID,
		UserID:      sc.UserID,
		Source:      "rss",
		Kind:        "rss.digest.ready",
		Title:       title,
		Body:        d.Headline,
		Payload:     payload,
		Priority:    "normal",
	})
	return err
}
