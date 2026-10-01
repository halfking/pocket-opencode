package email

// 历史邮件回补（2026-10-01：「一天前的邮件看不到」的服务端侧修复）。
//
// ## 为什么需要
//
// Sync 每轮只取 `LastSyncedUID+1 .. UIDNext` 里**最近的 50 封**
// （fetcher.go 里 `if len(uids) > 50 { uids = uids[len(uids)-50:] }`）。
// 于是只要用户一天没打开 App、服务端那段时间没跑同步，那一天的邮件就
// **从未进过服务端库** —— 客户端再怎么自愈也拉不回来，因为它们根本不在服务端。
//
// ## 做法
//
// 与 Sync 的关键差异：
//   - Sync 按 UID 区间搜「新的」，只能往前追；
//   - Backfill 按**日期**（IMAP SINCE）搜「一段时间内的」，能一次性把窗口
//     内的历史拉回来，且不依赖 LastSyncedUID 是否正确。
//
// 落库走同一个 InsertEmail（含 message_id/uid 唯一键与 snippet 派生），
// 因此重跑幂等：ON CONFLICT DO UPDATE 覆盖同一条，不会产生重复行。
//
// ## 绝不推进 LastSyncedUID
//
// 历史邮件的 UID 必然小于当前游标。推进游标会让后续增量同步**跳过**
// 尚未拉取的新邮件 —— 那是灾难性的数据丢失。这点必须与 Sync 严格区分。

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/halfking/pocket-opencode/backend/internal/email/rules"
)

// BackfillOptions 控制一次历史回补的规模。
type BackfillOptions struct {
	// Days 回看天数。<=0 时取 DefaultBackfillDays。
	Days int
	// MaxMessages 单账户单次最多入库封数。<=0 时取 DefaultBackfillMax。
	MaxMessages int
	// BatchSize 单次 FETCH 的封数。太大会让服务端一次性吐巨量数据、触发风控，
	// 太小则往返次数多。200 是实测较稳的量级。
	BatchSize int
	// Timeout 整轮墙钟预算。
	Timeout time.Duration
}

const (
	// DefaultBackfillDays 是无参调用时的默认回看天数。
	//
	// 30 天是用户明确选定的深度：足以覆盖「一天前看不到」这类症状，
	// 又不至于让大邮箱一次涌入数万封把库和 UI 压垮。
	DefaultBackfillDays = 30
	// DefaultBackfillMax 单账户单次入库上限，防止一次把库写爆。
	DefaultBackfillMax  = 2000
	backfillBatchSize   = 200
	backfillTimeout     = 5 * time.Minute
)

// BackfillReport 是一次历史回补的结果。
type BackfillReport struct {
	AccountID string `json:"accountId"`
	// Fetched 从 IMAP 搜到的封数（窗口内总数，含未处理的）。
	Fetched int `json:"fetched"`
	// Saved 实际写入库的封数。
	Saved int `json:"saved"`
	// Skipped 因达到 MaxMessages 上限或出错而未处理的封数。
	Skipped int `json:"skipped"`
	// Days 实际回看的天数。
	Days int `json:"days"`
	// Error 非空表示该账户失败（不影响其他账户）。
	Error string `json:"error,omitempty"`
}

// BackfillHistory 按日期窗口把 IMAP 历史邮件拉回服务端库。
func (f *Fetcher) BackfillHistory(ctx context.Context, accountID string, opts BackfillOptions) BackfillReport {
	rep := BackfillReport{AccountID: accountID}
	if opts.Days > 0 {
		rep.Days = opts.Days
	} else {
		rep.Days = DefaultBackfillDays
	}
	maxMsgs := opts.MaxMessages
	if maxMsgs <= 0 {
		maxMsgs = DefaultBackfillMax
	}
	batch := opts.BatchSize
	if batch <= 0 {
		batch = backfillBatchSize
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = backfillTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if f.store == nil {
		rep.Error = "email: store not configured"
		return rep
	}
	acc, encryptedCred, err := f.store.GetAccountByID(ctx, accountID)
	if err != nil {
		rep.Error = fmt.Sprintf("load account: %v", err)
		return rep
	}
	if !acc.Enabled {
		rep.Error = "account disabled"
		return rep
	}
	cred, err := f.crypto.DecryptString(encryptedCred)
	if err != nil {
		rep.Error = fmt.Sprintf("decrypt credential: %v", err)
		return rep
	}
	if cred == "" || cred == "oauth-pending-no-credential" {
		rep.Error = "account has no usable credential"
		return rep
	}

	addr := fmt.Sprintf("%s:%d", acc.IMAPHost, acc.IMAPPort)
	client, err := f.dial(addr)
	if err != nil {
		rep.Error = fmt.Sprintf("dial: %v", err)
		return rep
	}
	defer client.Close()
	if err := f.login(client, *acc, cred); err != nil {
		rep.Error = fmt.Sprintf("login: %v", err)
		return rep
	}
	sendClientID(client, acc.EmailAddress)
	if _, err := client.Select("INBOX", nil).Wait(); err != nil {
		rep.Error = fmt.Sprintf("select INBOX: %v", err)
		return rep
	}

	// IMAP SINCE 只精确到天，窗口起点取「今天 - (days-1)」，
	// 正好覆盖 days 个自然日（含今天），不多拉。
	cutoff := time.Now().AddDate(0, 0, -(rep.Days - 1))
	searchData, err := client.Search(&imap.SearchCriteria{
		Since: cutoff,
	}, nil).Wait()
	if err != nil {
		rep.Error = fmt.Sprintf("search: %v", err)
		return rep
	}
	seqs := searchData.AllSeqNums()
	rep.Fetched = len(seqs)
	if len(seqs) == 0 {
		return rep
	}
	if len(seqs) > maxMsgs {
		// 超上限时保留**较新**的：用户关心的是最近丢失的那批，
		// 更旧的留到下一轮（本轮已写入的会因 ON CONFLICT 幂等跳过）。
		rep.Skipped = len(seqs) - maxMsgs
		seqs = seqs[len(seqs)-maxMsgs:]
	}

	parsedRules, ruleErr := rules.ParseRules(acc.Rules)
	if ruleErr != nil {
		log.Printf("[email/backfill] parse rules for %s failed: %v (skipping rules)", acc.EmailAddress, ruleErr)
		parsedRules = nil
	}

	for start := 0; start < len(seqs); start += batch {
		if err := ctx.Err(); err != nil {
			rep.Error = "timeout"
			rep.Skipped += len(seqs) - start
			return rep
		}
		end := start + batch
		if end > len(seqs) {
			end = len(seqs)
		}
		var set imap.SeqSet
		for _, s := range seqs[start:end] {
			set.AddNum(s)
		}
		// 与 Sync 的批量取件**完全一致**：只取 envelope + UID + INTERNALDATE。
		//
		// 不在批量里加 BODY[TEXT]<0.1024>：Sync 明确不这么做，理由是部分 IMAP
		// server（Greenmail 等）对部分取回的响应缺 SP 分隔符，会让
		// imapwire 解析失败，进而让整批 FETCH 报错——历史回补一次要处理
		// 几十上百封，踩中就是整批拿不到。snippet 走同连接的按需单封补取。
		messages, ferr := client.Fetch(set, &imap.FetchOptions{
			Envelope:     true,
			UID:          true,
			InternalDate: true,
		}).Collect()
		if ferr != nil {
			log.Printf("[email/backfill] %s fetch seq %d-%d: %v", acc.EmailAddress, start, end, ferr)
			rep.Skipped += end - start
			continue
		}
		for _, m := range messages {
			em, ok, pending := f.emailFromMessage(m, *acc, client, parsedRules, nil)
			if !ok {
				continue
			}
			if err := f.store.InsertEmail(ctx, em); err != nil {
				log.Printf("[email/backfill] insert %s: %v", em.ID, err)
				continue
			}
			// 副作用型规则动作与 Sync 同语义：落 intent 表由 scheduler 消费。
			// 历史回补同样要落，否则这批邮件的自动回复/归档永远不执行。
			for _, p := range pending {
				if err := f.recordActionIntent(ctx, em, *acc, p.action); err != nil {
					log.Printf("[email/backfill] record action intent %s email=%s: %v", p.action.Action, em.ID, err)
				}
			}
			rep.Saved++
		}
	}
	return rep
}

// emailFromMessage 把一条 IMAP message 转成可入库的 Email。
//
// 与 Sync 走**完全相同**的映射：两处各写一份的话，任何一处改了
// message_id 兜底或 snippet 派生，另一处就会产出不兼容的行，
// 重跑时互相覆盖。这是抽取它的唯一理由。
func (f *Fetcher) emailFromMessage(
	m *imapclient.FetchMessageBuffer,
	acc Account,
	client *imapclient.Client,
	parsedRules []rules.Rule,
	// onStep 是可选打点钩子（Sync 传 tr.step 用于慢步骤告警，Backfill 传 nil）。
	onStep func(string),
) (Email, bool, []pendingIntent) {
	if m.Envelope == nil {
		return Email{}, false, nil
	}
	fromAddr, fromName := "", ""
	if len(m.Envelope.From) > 0 {
		fromAddr = m.Envelope.From[0].Addr()
		fromName = m.Envelope.From[0].Name
	}
	// IMAP ENVELOPE 的 Subject/个人名是 RFC 2047 编码字，不解码的话
	// 列表里全是 `=?GBK?B?...?=`，且发票关键词匹配全部落空。
	fromName = decodeMIMEWord(fromName)
	subject := decodeMIMEWord(m.Envelope.Subject)
	uid := m.UID

	// 缺 Date 头的邮件 envelope Date 是 Go 零值，.Unix() 会落成
	// -62135596800 这类负值，从此进不了任何 date 窗口扫描。
	date := m.Envelope.Date.Unix()
	if m.Envelope.Date.IsZero() {
		if !m.InternalDate.IsZero() {
			date = m.InternalDate.Unix()
		} else {
			date = time.Now().Unix()
		}
	}

	var snippet string
	for _, bs := range m.BodySection {
		snippet = DeriveSnippet(bs.Bytes, 500)
		break
	}
	if snippet == "" && client != nil {
		if onStep != nil {
			onStep(fmt.Sprintf("snippet uid=%d", uid))
		}
		snippet = f.fetchSnippetOnConnected(client, uid)
	}

	messageID := ""
	if m.Envelope.MessageID != "" {
		messageID = strings.TrimPrefix(strings.TrimSuffix(m.Envelope.MessageID, ">"), "<")
	}
	if messageID == "" {
		// 部分服务端不返回 Message-ID，会撞 UNIQUE(account_id, message_id)
		// 被 ON CONFLICT DO NOTHING 静默跳过；用 uid 维度合成键兜底。
		messageID = fmt.Sprintf("uid-%d", uid)
	}

	em := Email{
		ID:          fmt.Sprintf("em-%d-%s", uid, acc.ID),
		AccountID:   acc.ID,
		WorkspaceID: acc.WorkspaceID,
		MessageID:   messageID,
		UID:         int64(uid),
		FromAddress: fromAddr,
		FromName:    fromName,
		Subject:     subject,
		Snippet:     snippet,
		Date:        date,
	}
	em, pending := applyInlineRules(em, parsedRules, m.Envelope.Date)
	return em, true, pending
}

// pendingIntent 是规则评估出的、需要延后落库（副作用型）的动作。
type pendingIntent struct {
	action rules.ActionResult
	email  Email
}

// applyInlineRules 只落地**内联型**规则效果（重要/分类/归档 + ActionReason），
// 把副作用型动作（route-folder / trigger-autoreply）作为 pendingIntent 返回，
// 由调用方在有 ctx/acc 的位置写 email_action_intents。
//
// 抽出它是为了让 Sync 与 Backfill 的规则行为**逐字节一致**：
// 内联型效果直接写在 Email 上，两边都调用本函数；副作用型由各自落库，
// 因为它们需要 Sync 里的 recordActionIntent(ctx, acc, act) 上下文。
func applyInlineRules(em Email, parsed []rules.Rule, receivedAt time.Time) (Email, []pendingIntent) {
	if len(parsed) == 0 {
		return em, nil
	}
	apply := rules.Evaluate(parsed, rules.EmailInput{
		From:       em.FromAddress,
		Subject:    em.Subject,
		Body:       em.Snippet,
		Importance: em.Importance,
		Category:   em.Category,
		ReceivedAt: receivedAt,
	})
	if len(apply) == 0 {
		return em, nil
	}
	var pending []pendingIntent
	reasons := make([]string, 0, len(apply))
	for _, act := range apply {
		switch act.Action {
		case rules.ActionMarkImportant:
			em.Importance = "high"
		case rules.ActionLabelCategory:
			if cat := strings.TrimSpace(act.Category); cat != "" {
				em.Category = cat
			}
		case rules.ActionArchive:
			// 归档直接入库生效：category=archived + 已读。
			// 不引入 IMAP MOVE 副作用，重跑 sync 不会重复移动。
			em.Category = "archived"
			em.IsRead = true
		case rules.ActionRouteFolder, rules.ActionTriggerAutoReply:
			pending = append(pending, pendingIntent{action: act, email: em})
		}
		if act.Action != rules.ActionUnsupported {
			reasons = append(reasons, string(act.Action)+": "+act.Reason)
		}
	}
	if len(reasons) > 0 {
		em.ActionReason = strings.Join(reasons, "; ")
	}
	return em, pending
}
