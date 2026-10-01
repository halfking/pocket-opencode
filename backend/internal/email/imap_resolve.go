package email

// imap_resolve.go — POP3 来源邮件的 IMAP 真实 UID 反查。
//
// 背景（真实死结，2026-10-01 实测）：
// POP3 降级路径把邮件落库时，`uid` 写的是**位置序号**（第几封），不是 IMAP
// UID。harvestOne 的安全守卫拒绝拿这个合成 UID 去 `UID FETCH`（会取到完全
// 不相干的另一封，把别人的邮件存成这封发票的 PDF）。而 POP3 原文缓存
// （data/email-bodies-raw/）在 QQ 上从未落盘——POP3 只在 IMAP 失败时才跑，
// IMAP 修好后就不再跑。三者叠加的结果是：QQ Wallet 的两张真实发票邮件
// （uid=134/135，`[QQ Wallet] Electronic Invoice Issuance Notice`）永远停在
// failed，采集器明确报错却拿不到原文。
//
// 本文件提供第三条安全路径：**不用合成 UID，先用 IMAP SEARCH
// （HEADER From + HEADER Subject + 日期窗口）反查真实 UID，只有唯一命中
// 才返回**。这与「拿位置序号盲 FETCH」有本质区别：
//   - 盲 FETCH：位置序号恰好是别的邮件的 UID → 取到**错误的**邮件；
//   - SEARCH 反查：按发件人+主题在服务端匹配，命中唯一才用它的真实 UID。
//
// 安全前提是「唯一命中」。同发件人同主题可能有多封（QQ Wallet 连着发两封
// 就真实发生过），此时必须拒绝而不是猜——宁可继续 failed，也不把另一封
// 的附件当成这封的发票。

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/emersion/go-imap/v2"
)

// ErrUIDNotResolved 表示 IMAP 侧没能唯一定位到真实 UID。
//
// 调用方必须把它当成「这次不采、下一轮再说」，而不是「换条路硬取」——
// 定位不到通常意味着邮件已被移出 INBOX、或同主题有多封无法区分。
var ErrUIDNotResolved = fmt.Errorf("imap: could not uniquely resolve real UID")

// resolveRealUIDWindow 是 Subject 反查时允许的日期回溯窗口。
//
// 发票邮件从收到到被采集通常在数天到数周内（跨过一轮每日调度）。
// 窗口开太大 → 同主题的历史邮件混进来，破坏「唯一命中」的判定价值；
// 开太小 → 月初的发票跨月就再也定位不到。60 天是个折中：覆盖跨月调度，
// 又不至于把半年前的同主题邮件拉进来。
const resolveRealUIDWindow = 60 * 24 * time.Hour

// ResolveRealUIDByHeader 用 IMAP SEARCH 按「发件人 + 主题 + 日期窗口」反查
// 一封邮件的**真实 IMAP UID**。仅当唯一命中时返回该 UID。
//
// 专治 POP3 落库邮件的合成 UID：这类邮件不能直接 FETCH，但可以从 IMAP
// 侧用头部特征把真正的自己找回来。返回值语义严格：
//   - (uid>0, nil)：唯一命中，uid 是可安全 FETCH 的真实 UID；
//   - (0, ErrUIDNotResolved)：0 命中或 >1 命中，调用方**不得**猜；
//   - (0, err)：连接/协议层错误，可重试。
func (f *Fetcher) ResolveRealUIDByHeader(
	ctx context.Context, accountID, from, subject string, emailDateUnix int64,
) (int64, error) {
	if f == nil || f.store == nil || f.crypto == nil {
		return 0, fmt.Errorf("email: fetcher not configured")
	}
	if subject == "" {
		return 0, fmt.Errorf("resolve uid: empty subject")
	}
	acc, encryptedCred, err := f.store.GetAccountByID(ctx, accountID)
	if err != nil {
		return 0, fmt.Errorf("load account: %w", err)
	}
	if !acc.Enabled {
		return 0, fmt.Errorf("account disabled")
	}
	cred, err := f.crypto.DecryptString(encryptedCred)
	if err != nil {
		return 0, fmt.Errorf("decrypt credential: %w", err)
	}
	if cred == "" || cred == "oauth-pending-no-credential" {
		return 0, fmt.Errorf("account has no usable credential")
	}

	addr := fmt.Sprintf("%s:%d", acc.IMAPHost, acc.IMAPPort)
	client, err := f.dial(addr)
	if err != nil {
		return 0, fmt.Errorf("dial %s: %w", addr, err)
	}
	defer client.Close()
	if err := f.login(client, *acc, cred); err != nil {
		return 0, fmt.Errorf("login %s: %w", acc.EmailAddress, err)
	}
	// 与 FetchMessageRaw 一样，SELECT 前必须发 ID，否则 163 会 Unsafe Login。
	sendClientID(client, acc.EmailAddress)
	if _, err := client.Select("INBOX", nil).Wait(); err != nil {
		return 0, fmt.Errorf("select INBOX: %w", err)
	}

	// 搜索条件：主题必选（发票邮件之间主题稳定可区分）；发件人可选
	// （有 from 才加，减少误匹配）；日期用 SINCE/BEFORE 把范围收在
	// emailDate 前后 resolveRealUIDWindow 内，天然排除同主题的远期旧邮件。
	criteria := &imap.SearchCriteria{}
	criteria.Header = append(criteria.Header, imap.SearchCriteriaHeaderField{Key: "Subject", Value: subject})
	if from != "" {
		criteria.Header = append(criteria.Header, imap.SearchCriteriaHeaderField{Key: "From", Value: from})
	}
	if emailDateUnix > 0 {
		center := time.Unix(emailDateUnix, 0)
		criteria.Since = center.Add(-resolveRealUIDWindow)
		criteria.Before = center.Add(resolveRealUIDWindow)
	}

	searchData, err := client.UIDSearch(criteria, nil).Wait()
	if err != nil {
		return 0, fmt.Errorf("search by header: %w", err)
	}
	return pickUniqueUID(searchData.AllUIDs(), acc.EmailAddress, subject, from)
}

// pickUniqueUID 从 SEARCH 命中集合里挑出**唯一**的 UID。
//
// 抽成纯函数是为了能脱离 IMAP 服务器直接测「唯一命中才返回」这条底线——
// 它正是当初拒绝合成 UID 时要守的东西，不该只能靠集成测试覆盖。
// email/subject 仅用于日志，命中 0 或 >1 一律返回 ErrUIDNotResolved。
func pickUniqueUID(uids []imap.UID, emailAddr, subject, from string) (int64, error) {
	switch len(uids) {
	case 0:
		log.Printf("[email/resolve-uid] %s: 0 hit subject=%q from=%q — not in INBOX (moved or deleted)",
			emailAddr, subject, from)
		return 0, ErrUIDNotResolved
	case 1:
		uid := int64(uids[0])
		log.Printf("[email/resolve-uid] %s: unique hit uid=%d subject=%q (POP3 synthetic uid replaced by real IMAP uid)",
			emailAddr, uid, subject)
		return uid, nil
	default:
		// 多于一条：无法区分是哪一封。绝不取最新/最旧来猜——那正是当初
		// 要防的「拿到不相干的邮件」。如实报未解析，交给下一轮或人工。
		log.Printf("[email/resolve-uid] %s: %d hits for subject=%q from=%q — ambiguous, refusing to guess",
			emailAddr, len(uids), subject, from)
		return 0, ErrUIDNotResolved
	}
}
