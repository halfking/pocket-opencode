package email

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// pipeline.go — 每日邮件处理流水线（可定时、可手动触发）。
//
// 对应需求：
//   - 每天定时/手动收信（步骤 1）
//   - 清理广告与垃圾邮件到垃圾箱（步骤 2，判定 spam.go + MOVE junk.go）
//   - 其它重要邮件提醒（步骤 3，notifycenter）
//   - 发票下载整理 {费用类型}-{对方单位}-{金额}-{日期}.pdf（步骤 4，
//     invoice_harvest.go，多次重试）
//   - 推送飞书；无法发送时生成共享汇总文档 + 列表 + 合计金额（步骤 5/6）
//
// 执行位置（需求「可以设备本地进行，也可以委托服务端进行，默认本地」）：
// 本地部署形态下 pocketd 就运行在设备本地，Pipeline 即"本地执行"路径；
// server 模式由上层（server 包）把同样的 Run 动作转发给远端编排 URL，
// Pipeline 自身不感知。

// InvoicePusher 把下载好的发票文件推到飞书。实现由 server 包装 feishu.Client。
type InvoicePusher interface {
	PushInvoice(ctx context.Context, inv Invoice, absPath string) error
	Available() bool
}

// ImportantNotifier 派发重要邮件提醒。实现由 server 包装 notifycenter.Service。
type ImportantNotifier interface {
	NotifyImportantEmail(ctx context.Context, e Email) error
}

// Pipeline 流水线。
type Pipeline struct {
	Store    *Store
	Fetcher  *Fetcher
	Harvest  *InvoiceHarvester
	Pusher   InvoicePusher     // 可为 nil：跳过飞书，直接走共享文档
	Notifier ImportantNotifier // 可为 nil：跳过提醒
	// BodyCache 是 POP3 降级路径落盘的邮件原文缓存（见 body_cache.go）。
	// 第 2 趟的取原文要靠它：POP3 来源的 em.UID 是位置序号，
	// FetchMessageRaw（IMAP 专用）永远取不到原文，于是 POP3 发票建不了档。
	// nil 时 POP3 来源取原文会明确失败（不退化成拿位置序号去 IMAP FETCH）。
	BodyCache BodyCache
	// Ledger 发布飞书共享台账（电子表格）。为 nil 或不可用时只生成本地 CSV/MD。
	Ledger LedgerPublisher
	// Classifier 对未归类邮件跑一次分类（需求 4 的定时路径）。可为 nil：
	// 为 nil 时第 1.6 步整步跳过，报告里 ClassifySkip 记下原因。
	//
	// 为什么需要它：importance 是**提醒的唯一入口**（splitReminderCandidates
	// 只看 importance='high'），而它此前只有两条写入路径——账户规则与
	// kxmemory 分类。kxmemory 未配时定时路径整步放弃，于是新邮件的
	// importance 永远是空，需求 4 对新邮件恒 0 条提醒。手动端点
	// /api/emails/classify 有 LLM 网关兜底，Scheduler 拿不到那个兜底
	// （跨包）。这里开一个注入口，让上层把网关兜底接进来。
	//
	// 顺序是硬要求：必须排在第 3 步 notifyImportant **之前**。排在后面的话
	// 本轮新邮件仍然不会被提醒，看起来「分类已经跑过」而需求 4 依旧不响。
	Classifier EmailClassifier
	DataDir    string
	// SpamLookbackDays 垃圾清理扫描窗口（默认 7 天）。
	SpamLookbackDays int
	// SpamDryRun=true 时第 2 步只判定不 MOVE（真实邮箱首次运行的安全阀）。
	SpamDryRun bool
	// AccountSyncTimeout 单账户同步墙钟上限；<=0 时用 DefaultAccountSyncTimeout。
	AccountSyncTimeout time.Duration
	// A4Grid 是每日 A4 网格导出阶段的格数（2=2x2，3=3x3）。其它值（默认 0）
	// 关闭该阶段，报告里 A4ExportSkip 会写明关闭原因。见 pipeline_a4.go。
	A4Grid int
}

// EmailClassifier 对一个 (user, workspace) 下的未归类邮件跑一次分类，
// 返回真正写库成功的条数。
//
// 签名与错误语义与 ClassifyUnclassified 保持一致：第二个返回值是**逐条失败**
// 的汇总，不是「整个函数失败」——(0, nil) 与 (0, err) 必须在报告上可区分。
type EmailClassifier func(ctx context.Context, userID, workspaceID string, limit int) (int, error)

// classifyBatchLimit 单轮分类的封数上限。
//
// 上限存在的原因是分类是**逐条串行**的（每封一个 20s 超时），不设上限的话
// 积压上千封时第 1.6 步会吃掉整轮时间预算，挤掉后面的提醒与发票采集。
// 剩下的留给下一轮——分类本来就是可增量重入的。
//
// 取 20 还有个机械原因：Store.ListUnclassifiedScoped 自己把 >20 的值重置为
// 20。写在这里是为了让这个上限**在本文件里可见**，而不是散落在 store 的
// 静默钳制里——但要知道，真正生效的那道是 store 那道。
const classifyBatchLimit = 20

// spamPreviewCap 每个账户在预演报告里最多列多少个主题样本。
const spamPreviewCap = 10

// syncConcurrency 第 1 步同时同步几个账户。
//
// 为什么不能串行（BUG-AU 实测）：6 个真实账户里 5 个是 0.4~1.3 秒，
// 只有 imap.qq.com 这一个会「TCP 已建立但服务端不回命令」——实测连挂 4 分
// 11 秒仍在等，客户端 5 分钟 headers 超时先走，整轮 15 分钟 ctx 也切不断它
// （go-imap 不响应 ctx 取消）。串行下总耗时 = 各账户之和，于是健康账户全被
// 一个坏账户拖住。改成有界并发后，总耗时 ≈ 最慢的那个账户。
//
// 上限取 3 而不是账户数：一来避免同时对同一服务商开太多连接触发更严的限流
// （本轮 QQ 已经很可能是被反复全量同步限流才不回命令），二来给 PG 留余量。
const syncConcurrency = 3

// DefaultAccountSyncTimeout 是单个账户同步的墙钟上限默认值。
//
// 依据：健康账户实测 0.3~1.5 秒（§7d.2）。而「连接建好但服务端不回命令」的
// 账户实测能挂 7 分钟以上。90 秒对健康账户是三个数量级的余量，对卡死的
// 账户则是明确的止损点。Pipeline.AccountSyncTimeout 可覆盖。
const DefaultAccountSyncTimeout = 90 * time.Second

// syncAccounts 有界并发地同步所有账户，逐账户记耗时。
//
// 并发安全性：Fetcher 的字段（dialTLS / insecureSkipVerify / useStartTLS /
// store / crypto）建好后只读；Crypto 只持有不可变的 cipher.AEAD 且
// DecryptString 每次现生成随机 nonce；Store 走 PG 连接池。因此 Sync 可以
// 并发调用。
func (p *Pipeline) syncAccounts(ctx context.Context, accounts []Account, rep *PipelineReport) {
	if len(accounts) == 0 {
		return
	}
	workers := syncConcurrency
	if workers > len(accounts) {
		workers = len(accounts)
	}
	type result struct {
		new    int
		err    error
		elapse time.Duration
		email  string
	}
	results := make([]result, len(accounts))

	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i := range accounts {
		// 调度前检查取消：HTTP 客户端断开或 15 分钟 ctx 到点后不再派新活，
		// 否则排队的账户还会挨个去连一遍被限流的服务商。
		if ctx.Err() != nil {
			rep.AddError("run cancelled at step1 before %s: %v", accounts[i].EmailAddress, ctx.Err())
			log.Printf("[email/pipeline] step1 cancelled, %d account(s) skipped: %v",
				len(accounts)-i, ctx.Err())
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, acc Account) {
			defer wg.Done()
			defer func() { <-sem }()
			t0 := time.Now()
			// 墙钟上限：go-imap 不响应 ctx 取消（imapclient.Options 里没有
			// ReadTimeout 字段），所以外层 ctx 的 15 分钟**切不断在途的那次读**。
			// 实测 exmail.qq.com / imap.qq.com 接受连接后不回命令，单个账户
			// 能把整轮拖过 7 分钟。
			//
			// 双保险：
			//   - 连接侧：fetcher 给每条 IMAP 连接挂了 60s 空闲 deadline
			//     （见 fetcher.go 的 deadlineConn），卡死的那次读会自己变成
			//     一条超时错误，Sync 能返回、`defer client.Close()` 能执行、
			//     连接不会泄漏。
			//   - 调度侧：这里到期就登记 TIMED OUT 并继续下一账户。Sync 若
			//     真的超过上限仍在后台跑完（落库是幂等的：按 message_id
			//     upsert、LastSyncedUID 单调），所以「放着它跑完」不污染数据。
			//
			// 两者都要有：只有调度侧没有连接侧，卡死的账户每轮泄漏一条连接，
			// 实测攒到 7 条后新连接本身就开始变慢。
			ch := make(chan result, 1) // 缓冲 1：超时后没人收也不会永久阻塞 goroutine
			go func() {
				n, err := p.Fetcher.Sync(ctx, acc.ID)
				ch <- result{new: n, err: err, elapse: time.Since(t0).Round(time.Millisecond), email: acc.EmailAddress}
			}()
			limit := p.AccountSyncTimeout
			if limit <= 0 {
				limit = DefaultAccountSyncTimeout
			}
			timer := time.NewTimer(limit)
			defer timer.Stop()
			select {
			case r := <-ch:
				results[idx] = r
				if r.err != nil {
					// 该账户已有一轮同步在跑（scheduler 或另一条流水线）。
					// 这是正常现象不是失败：记日志即可，计进 errors 会让
					// 每轮报告都挂一条红，淹没真正需要人看的故障。
					if errors.Is(r.err, ErrSyncInFlight) {
						log.Printf("[email/pipeline] step1 sync %s skipped after %s: %v", acc.EmailAddress, r.elapse, r.err)
						results[idx] = result{elapse: r.elapse, email: acc.EmailAddress}
						return
					}
					log.Printf("[email/pipeline] step1 sync %s FAILED after %s: %v", acc.EmailAddress, r.elapse, r.err)
					return
				}
				log.Printf("[email/pipeline] step1 sync %s new=%d in %s", acc.EmailAddress, r.new, r.elapse)
			case <-timer.C:
				results[idx] = result{
					elapse: limit.Round(time.Millisecond),
					email:  acc.EmailAddress,
					err:    fmt.Errorf("account sync exceeded %s (IMAP 未响应；后台仍在收尾，本轮不等)", limit),
				}
				log.Printf("[email/pipeline] step1 sync %s TIMED OUT after %s — 不再等待，继续后续步骤",
					acc.EmailAddress, limit)
			}
		}(i, accounts[i])
	}
	wg.Wait()

	for _, r := range results {
		if r.email == "" {
			continue // 取消前没派到活
		}
		if r.err != nil {
			rep.AddError("sync %s: %v", r.email, r.err)
			continue
		}
		rep.AccountsSynced++
		rep.NewEmails += r.new
	}
}

// SpamPreviewItem 是预演报告里一个账户的判定结果。
type SpamPreviewItem struct {
	AccountID string   `json:"accountId"`
	Count     int      `json:"count"`
	Why       string   `json:"why"`
	Subjects  []string `json:"subjects,omitempty"`
	// Near 是未判垃圾但拿到分数的邮件（参与评分、未过 100 阈值）。
	Near []SpamNearMiss `json:"near,omitempty"`
}

// SpamNearMiss 是一封「参与了评分但没到垃圾阈值」的邮件。
//
// 存在意义：预演报告是用户决定开不开真实 MOVE 的唯一依据。只给
// 命中/未命中两态时，规则离真实数据有多远是看不见的——2026-10-01 实测
// 444 封真实邮件命中 0 封，却有 17 封拿到 30 分（newsletter/EDM 发件人特征），
// 它们就卡在门槛外侧。阈值该不该调、这些订阅该不该留，得由人看着具体主题决定。
type SpamNearMiss struct {
	From    string `json:"from"`
	Subject string `json:"subject"`
	Score   int    `json:"score"`
	Why     string `json:"why"`
}

func countNearMiss(items []SpamPreviewItem) int {
	n := 0
	for _, it := range items {
		n += len(it.Near)
	}
	return n
}

// PipelineReport 一轮执行的结果汇总。
type PipelineReport struct {
	StartedAt      int64 `json:"startedAt"`
	FinishedAt     int64 `json:"finishedAt"`
	DurationMs     int64 `json:"durationMs"`
	AccountsSynced int   `json:"accountsSynced"`
	NewEmails      int   `json:"newEmails"`
	SpamMoved      int   `json:"spamMoved"`
	SpamLocalOnly  int   `json:"spamLocalOnly"`
	// SpamDryRun>0 表示本轮是预演：这 SpamDryRun **封**邮件「本可以移走但没移」，
	// 逐账户列在 SpamDryRunSamples 里。真实邮箱上先看这个再决定是否真移。
	//
	// 单位是**邮件**不是账户，2026-10-02 修：它原先在账户循环里 `++`，数的是
	// 账户数，而日志把它写成「%d mail(s) would be moved」、注释写成「这
	// SpamDryRun 封」。两个后果：
	//   1. 预演报告的条数**少于**真实 MOVE 会移的条数（真实分支的
	//      `SpamMoved += moved` 数的是邮件）——预演恰恰是「开 MOVE 前看的
	//      那个数」，它报少报就等于让人在错误的量级上做决定；
	//   2. SpamDryRun 与 SpamMoved 单位不同却长得一样，报告上无法互相印证。
	// 真实库当场抓到：1 个账户 2 封「【阿里云】云安全中心周报」被判垃圾，
	// 旧实现报 1 封，真跑会移 2 封。
	SpamDryRun        int               `json:"spamDryRun,omitempty"`
	SpamDryRunSamples []SpamPreviewItem `json:"spamDryRunSamples,omitempty"`
	// SpamNearMiss 是「未判垃圾但有分」的邮件，按账户分组。
	// 开真实 MOVE 之前这是必看项：命中数低不代表规则贴近真实数据。
	SpamNearMiss  []SpamPreviewItem `json:"spamNearMiss,omitempty"`
	RemindersSent int               `json:"remindersSent"`
	// RemindersScanned 是本轮进入提醒判定的邮件数；RemindersUnclassified 是
	// 其中 **importance 为空** 的数量。
	//
	// 为什么必须有它：提醒只对 `importance='high'` 触发，而 importance 是
	// AI 分类（kxmemory）写进去的。kxmemory 没配时（POCKET_KXMEMORY_BASE_URL
	// 未设置，启动日志明写 `AI classification/SSOT disabled`）新邮件的
	// importance 永远是空，于是 RemindersSent 恒为 0 —— 但这个 0 **分不清**
	// 「这批邮件里确实没有重要的」和「邮件根本没被分类过」。两者在报告里长得
	// 一模一样，于是需求 4 看起来像没实现，其实只是缺一个依赖配置。
	//
	// 和 §spam 那次 near-miss 是同一类问题：可观测性缺口让「功能是否失灵」
	// 没法判断。有了这两个计数，看报告就知道该去配 AI 还是该去调规则。
	RemindersScanned      int `json:"remindersScanned,omitempty"`
	RemindersUnclassified int `json:"remindersUnclassified,omitempty"`
	// RemindersOutOfWindow 是「importance=high、从未提醒、但 date 比扫描窗口
	// （notifyImportant 里的 importantReminderLookbackDays，原为硬编码 2 天）
	// 更老」的邮件数。
	//
	// 为什么必须有它：那些邮件**永远不会被提醒**——不是「这轮没轮到」，是
	// 「不在扫描范围里」。没有这个计数时，报告上的 0 分不清
	// 「这批确实没有重要邮件」和「有 20 封重要的但它们太老了」，
	// 需求 4 看起来就像没实现。和 RemindersUnclassified 是同一类问题的
	// 时间维度版本（见 reminder_diag_test.go 记录的另一次）。
	//
	// 注意这**不是**「发了多少条提醒」，两者不可互相替代。
	RemindersOutOfWindow int `json:"remindersOutOfWindow,omitempty"`
	// FeishuSkip 是「飞书推送这一步**根本没跑**」的原因，非空即表示本轮
	// 飞书出站一步都没执行。
	//
	// 为什么必须有它（2026-10-02 真实库发现）：FeishuPushed 与 FeishuFailed
	// 都是 0 时，下面两种情况在报告上**完全一样**：
	//
	//	「飞书没配，整步被跳过」
	//	「飞书配了，但这轮没有需要推送的发票」
	//
	// 真实库里第二列的事实是前者——两张发票行 feishu_sent_at 都是 0，
	// POCKET_FEISHU_APP_ID / APP_SECRET / INVOICE_CHAT_ID / INVOICE_FOLDER_TOKEN
	// 四项在本机任何地方都没配，而 pushInvoiceSet 当时是**静默 return**，
	// 报告上不留下任何痕迹。需求 3 的主交付物「发送到飞书上」于是看起来
	// 像是「跑过了、0 条」，实际上是「一次都没跑过」。
	//
	// 和 RemindersUnclassified / RemindersOutOfWindow 是同一类问题的又一次：
	// 缺的不是功能，是「没发生」与「发生了但结果是 0」之间的可区分性。
	// 共享台账（PublishLedgerScoped）走同一个 client、同一个缺口，这里不重复
	// 记一遍，避免两个字段说同一件事。
	FeishuSkip string `json:"feishuSkip,omitempty"`
	// RemindersPending 是「这轮**将要**推送的条数」——判定完候选、在真正
	// 逐条 Notify 之前就记下来。
	//
	// 为什么必须有它：RemindersSent 只有推完才知道，而 notifyImportant
	// **没有限流**，候选有多少就推多少。2026-10-02 窗口从 2 天放宽到 90 天
	// （37c53e6d）之后，跑着的二进制第一次要跑这个窗口时，真实库里积压了
	// **32 封**未提醒的 high（用与生产等价的判据数出来的）——但在那之前，
	// 报告上没有任何一个数字能提前说出「这轮会推 32 条」。
	//
	// RemindersOutOfWindow 救不了这个场景：它数的是窗口**外**的，而积压
	// 全在窗口**内**。两个计数缺一不可，合起来才画得出全貌：
	// 「窗外 N 封永远轮不到」+「窗内这轮要推 M 条」。
	//
	// 注意它与 RemindersSent 不可互相替代：推送失败时 Pending > Sent，
	// 这个差值就是「本该提醒却没提醒出去」的条数。
	RemindersPending int `json:"remindersPending,omitempty"`
	// Classified 是第 1.6 步本轮**写库成功**的分类条数。
	// ClassifySkip 是「分类这一步**根本没跑**」的原因，非空即表示本轮
	// 一封都没分类。
	//
	// 为什么需要它（与 FeishuSkip 同一类问题）：importance 是提醒的唯一入口，
	// 而它只在分类里被写。kxmemory 未配时定时路径整步放弃，于是新邮件
	// importance 恒空、需求 4 恒 0 条提醒——但报告上「没配分类器」与
	// 「分类跑了但没有待分类的邮件」都表现为「0 条提醒」，长得一模一样。
	// ClassifySkip 把第一种情况显式说出来，排查时不用再去翻启动日志。
	Classified   int    `json:"classified,omitempty"`
	ClassifySkip string `json:"classifySkip,omitempty"`
	// 发票候选（步骤 1.5）的三个计数。同样的理由：只报 invoices.Processed
	// 时，「扫了 0 封候选」和「这批里没有发票」长得一模一样，
	// 于是 0 到底是链路没跑、还是跑了但没命中，看报告分不出来。
	// 2026-10-02 就是靠 scanned 这个计数才定位到 24h 窗口把历史邮件全挡在
	// 外面——在那之前 invoices 全是 0，看上去像「邮箱里没有发票」。
	InvoiceCandidatesScanned int `json:"invoiceCandidatesScanned,omitempty"`
	InvoiceCandidatesCreated int `json:"invoiceCandidatesCreated,omitempty"`
	// InvoiceBodyFetchDeferred 是超出单轮 IMAP 预算、顺延到下一轮的候选数。
	InvoiceBodyFetchDeferred int `json:"invoiceBodyFetchDeferred,omitempty"`
	// InvoiceBodyDeadLettered 是本轮新判定为「原文在服务端已不存在」的邮件数。
	// 单独一个字段而不是混进 fetchFailed：前者在修复后应当稳定在一个很小的
	// 数字上并最终归零（它们不再占用预算），后者会一直有网络噪声。
	InvoiceBodyDeadLettered int           `json:"invoiceBodyDeadLettered,omitempty"`
	Invoices                HarvestResult `json:"invoices"`
	FeishuPushed            int           `json:"feishuPushed"`
	FeishuFailed            int           `json:"feishuFailed"`
	ShareDocCSV             string        `json:"shareDocCsv,omitempty"`
	ShareDocMD              string        `json:"shareDocMd,omitempty"`
	// ShareDocURL 是飞书共享台账链接（未配置飞书时为空，本地 CSV/MD 仍会生成）。
	ShareDocURL string `json:"shareDocUrl,omitempty"`
	// A4* 是每日 A4 网格导出阶段的结果（见 pipeline_a4.go）。该阶段默认关闭
	// （POCKET_EMAIL_A4_GRID 未设为 2/3）。
	//
	// A4ExportSkip 存在的意义是让「没导出」与「压根没有票」可区分：把阶段打开
	// 之后，字段为空会长成「导出成功了但没东西可导」的样子。
	A4ExportPath    string   `json:"a4ExportPath,omitempty"`
	A4ExportCount   int      `json:"a4ExportCount,omitempty"`
	A4ExportSkipped []string `json:"a4ExportSkipped,omitempty"`
	A4ExportMarked  int      `json:"a4ExportMarked,omitempty"`
	A4ExportSkip    string   `json:"a4ExportSkip,omitempty"`
	Errors          []string `json:"errors,omitempty"`
}

// AddError 记录非致命错误（流水线继续跑完）。
func (r *PipelineReport) AddError(format string, args ...any) {
	r.Errors = append(r.Errors, fmt.Sprintf(format, args...))
}

// stepStart 打一条分步进度日志：步骤名 + 距本轮开始的耗时。
//
// 背景：真实邮箱上一轮 Run 跑了 9 分钟仍未结束，而日志里只有开头两行和最后
// 一行汇总，中途完全没有可观测信息，无法判断卡在哪个账户、哪一步。收 5 个
// 真实账户（最大 62 封）本该是秒级，这里每步都留下耗时与计数后才谈得上定位
// 与优化。
func stepStart(rep *PipelineReport, start time.Time, format string, args ...any) {
	log.Printf("[email/pipeline] step %s (t+%s)",
		fmt.Sprintf(format, args...),
		time.Since(start).Round(time.Millisecond))
}

// Run 执行一轮完整流水线。
func (p *Pipeline) Run(ctx context.Context) *PipelineReport {
	rep := &PipelineReport{StartedAt: time.Now().Unix()}
	start := time.Now()
	defer func() {
		rep.FinishedAt = time.Now().Unix()
		rep.DurationMs = time.Since(start).Milliseconds()
		log.Printf("[email/pipeline] done synced=%d new=%d spam=%d(+%d local) reminders=%d inv=%+v feishu=%d/%d errors=%d",
			rep.AccountsSynced, rep.NewEmails, rep.SpamMoved, rep.SpamLocalOnly,
			rep.RemindersSent, rep.Invoices, rep.FeishuPushed, rep.FeishuFailed, len(rep.Errors))
	}()

	// 1) 全账户收信
	accounts, err := p.Store.ListEnabledAccountsWithWorkspace(ctx)
	if err != nil {
		rep.AddError("list accounts: %v", err)
		return rep
	}
	stepStart(rep, start, "1/5 sync %d account(s)", len(accounts))
	p.syncAccounts(ctx, accounts, rep)

	// 1.5) 发票候选自动建档（供第 4 步采集下载；背景见函数注释）
	stepStart(rep, start, "1.5/5 invoice candidates")
	p.extractInvoiceCandidates(ctx, accounts, rep)

	// 1.6) 分类。**必须排在第 3 步之前**：importance 是提醒的唯一入口，
	// 分类排在提醒之后的话本轮新邮件仍然不会被提醒，而报告上会显示
	// 「分类已跑过 N 封」——看起来修好了，需求 4 其实还是不响。
	stepStart(rep, start, "1.6/5 classify")
	p.classifyPending(ctx, pipelineScopes(accounts), rep)

	// 2) 垃圾清理
	stepStart(rep, start, "2/5 spam clean (dryRun=%v)", p.SpamDryRun)
	p.cleanSpam(ctx, rep)

	// 3) 重要邮件提醒
	stepStart(rep, start, "3/5 important reminders")
	p.notifyImportant(ctx, rep)

	// 4) 发票采集（下载/渲染/命名落盘）
	if p.Harvest != nil {
		stepStart(rep, start, "4/5 invoice harvest")
		rep.Invoices = p.Harvest.HarvestAll(ctx)
	}

	// 5) 飞书推送 + 6) 共享汇总文档（总是生成，作为可核查的清单）。
	// 发票行带 (user, workspace) 隔离：复用 intentLoop 的 scope 去重方式，
	// 对每个 scope 独立推送与汇总，避免跨工作区串数据。
	scopes := pipelineScopes(accounts)
	stepStart(rep, start, "5/5 push+ledger over %d scope(s)", len(scopes))
	for sc := range scopes {
		invoices, err := p.Store.ListInvoicesScoped(ctx, sc[0], sc[1], "downloaded", 500)
		if err != nil {
			rep.AddError("list downloaded invoices scope=%v: %v", sc, err)
			continue
		}
		p.pushInvoiceSet(ctx, invoices, sc[0], sc[1], rep)
		// 共享台账：飞书可用时在飞书上建一张真表格（可分享），
		// 本地 CSV/MD 始终生成（离线兜底 + 对账留存）。
		if url, lerr := p.PublishLedgerScoped(ctx, sc[0], sc[1]); lerr != nil {
			rep.AddError("publish ledger scope=%v: %v", sc, lerr)
		} else if url != "" {
			rep.ShareDocURL = url
		}
		// 曾经写成 `if _, _, err := p.BuildInvoiceSummaryDocs(...)`：文件生成了，
		// 路径却被丢进 `_` —— PipelineReport.ShareDocCSV / ShareDocMD 于是**恒为空**。
		//
		// 为什么一直没人发现：手动触发走的是另一条路
		// （server_email_pipeline.go:497 单独调一次并回填到 HTTP 响应），
		// 只有**定时**这一条路径受影响。于是「定时跑完的日报里看不到汇总文档
		// 在哪」——需求 3 明确要的「共享文档 + 列表 + 金额汇总」在无人值守场景
		// 下等于没有交付。
		csvPath, mdPath, err := p.BuildInvoiceSummaryDocs(ctx, sc[0], sc[1])
		if err != nil {
			rep.AddError("summary docs scope=%v: %v", sc, err)
		} else {
			// 多 scope 时逐个覆盖：这两个字段是**单值**，报告只能指一个 scope。
			// 最后一轮赢，与 ShareDocURL 的既有行为一致；每个 scope 的文件都已落盘。
			rep.ShareDocCSV = csvPath
			rep.ShareDocMD = mdPath
		}
		// A4 网格导出（见 pipeline_a4.go）。必须放在 scope 循环**内**：
		// 一个发票文件只属于一个 workspace，放到循环外就只会导出最后一个
		// scope 的票，其它工作区的票永远拿不到可打印凭证。
		p.exportPendingA4(ctx, rep, sc[0], sc[1], invoices)
	}
	return rep
}

// pipelineScopes 从账户列表推导需要按 (user, workspace) 隔离处理的 scope 集合。
//
// 抽成函数是因为它现在有**两个**调用点（第 1.6 步分类、第 5 步推送+台账）。
// 留在两处各写一遍的话，任何一处漏掉「UserID 为空要跳过」或漏掉
// defaultWorkspace 归一，两个步骤的 scope 口径就会分叉——而这种分叉
// 表现为「有的发票被推了、有的没有」，极难从报告上看出是 scope 算错了。
func pipelineScopes(accounts []Account) map[[2]string]struct{} {
	scopes := make(map[[2]string]struct{}, len(accounts))
	for _, acc := range accounts {
		if acc.UserID == "" {
			continue
		}
		scopes[[2]string{acc.UserID, defaultWorkspace(acc.WorkspaceID)}] = struct{}{}
	}
	return scopes
}

// classifyPending 是第 1.6 步：对每个 scope 分类尚未归类的邮件。
//
// 没有分类器时整步跳过，但**必须留下 ClassifySkip**：这一整步缺失与
// 「跑了但没有待分类的邮件」在报告上都是「0 条提醒」，只有把这个原因
// 显式记下来，需求 4 不响的时候才知道该去配分类器而不是去查邮件。
func (p *Pipeline) classifyPending(ctx context.Context, scopes map[[2]string]struct{}, rep *PipelineReport) {
	if p.Classifier == nil {
		rep.ClassifySkip = "未注入分类器（POCKET_EMAIL_CLASSIFY_VIA_GATEWAY 未开启，" +
			"或该部署没有 LLM 网关）——本轮新邮件 importance 不会被写入，需求 4 对新邮件不会有提醒"
		log.Printf("[email/pipeline] %s", rep.ClassifySkip)
		return
	}
	total := 0
	for sc := range scopes {
		n, err := p.Classifier(ctx, sc[0], sc[1], classifyBatchLimit)
		if err != nil {
			// 逐条失败汇总，不是整步失败：成功的那些已经写库了。
			rep.AddError("classify scope=%v: %v", sc, err)
		}
		total += n
	}
	rep.Classified = total
	log.Printf("[email/pipeline] classified %d email(s) across %d scope(s)", total, len(scopes))
}

// extractInvoiceCandidates 把近期入库、命中发票关键词但尚未建档的邮件自动
// 建发票记录，供第 4 步 HarvestAll 下载附件。
//
// 背景缺陷：发票自动提取原本只接在客户端推送路径（handleEmailSync 模式 B 的
// classifyEmailsAsync）；服务端 IMAP 抓取（模式 A）与定时 pipeline 都不触发，
// 导致「收取发票类邮件并解析整理、下载发票文件」对纯服务端部署永远不发生。
// IMAP 路径只落 envelope（snippet 为空、金额/发票号在正文里），因此候选命中
// 后需 FetchMessageRaw 拉原文做二次提取（与手动提取端点同路径）。
func (p *Pipeline) extractInvoiceCandidates(ctx context.Context, accounts []Account, rep *PipelineReport) {
	lookbackSince := rep.StartedAt - int64(invoiceCandidateLookbackDays)*86400

	// 死信淘汰（raw_body_dead.go）：先把过了保留期的标记复检掉，再读出当前
	// 仍被标记的邮件。顺序不能反——先读后复检会让刚复检的邮件本轮仍被跳过。
	if n, err := p.Store.ReArmStaleRawBodyDead(ctx, time.Now().Add(-rawBodyDeadRetryAfter)); err != nil {
		rep.AddError("raw-body dead letter re-arm: %v", err)
	} else if n > 0 {
		log.Printf("[email/pipeline] step1.5 re-armed %d stale raw-body dead letter(s) for re-check", n)
	}
	deadLetters, err := p.Store.ListRawBodyDeadEmailIDs(ctx, lookbackSince)
	if err != nil {
		// 读不到标记集就不能安全地跳过任何邮件：宁可这一轮多花预算，
		// 也不能因为查询失败而把「已淘汰」当成「已确认可取」。
		rep.AddError("raw-body dead letter list: %v", err)
		deadLetters = nil
	}

	emails, _, err := p.Store.ListEmailsSince(ctx,
		lookbackSince, invoiceCandidateScanLimit)
	if err != nil {
		rep.AddError("invoice candidates list: %v", err)
		return
	}
	scope := make(map[string][2]string, len(accounts))
	for _, a := range accounts {
		if a.UserID != "" {
			scope[a.ID] = [2]string{a.UserID, defaultWorkspace(a.WorkspaceID)}
		}
	}
	created := 0
	deadSkipped := 0

	// 两类「需要拉原文」的邮件合并成一批并发处理：
	//   - date：envelope 已命中发票但缺开票日期（IMAP 只落 envelope，日期在正文里）；
	//   - candidate：主题/摘要命中关键词但 envelope 没命中，需要正文二次提取。
	// 原实现是一个 for 循环逐封开一次完整 IMAP 会话，串行且无上限。实测
	// 24h 窗口内 151 封未建档邮件时这一步跑了 6 分钟仍未完（客户端 5 分钟
	// headers 超时先走），而 go-imap 不响应 ctx 取消，单封卡住就整步停摆。
	type invoiceCandidate struct {
		email  Email
		scope  [2]string
		inv    *Invoice // ExtractInvoice/ExtractInvoiceLoose 返回指针，hit=false 时可能为 nil
		hit    bool
		reason string // 对应 bodyJob.reason；无 job 时为空
		jobAt  int    // jobs 下标，-1 表示本轮不拉原文
	}
	var cands []invoiceCandidate
	var jobs []bodyJob

	for i := range emails {
		e := emails[i]
		sc, ok := scope[e.AccountID]
		if !ok {
			continue
		}
		// 已判定死信：原文在服务端已不存在，再拉一次必然失败，只会白占
		// 取原文预算（2026-10-03 实测两封被删的 AWS 告警占了 6 次里的 2 次）。
		// 跳过发生在**建 job 之前**，这才是真正省下预算的位置。
		if deadLetters[e.ID] {
			deadSkipped++
			continue
		}
		if _, err := p.Store.GetInvoiceByEmailID(ctx, e.ID); err == nil {
			continue // 已建档，幂等跳过
		}
		inv, hit := ExtractInvoice(e, "")
		c := invoiceCandidate{email: e, scope: sc, inv: inv, hit: hit, jobAt: -1}
		if p.Fetcher != nil && e.UID > 0 {
			c.reason = invoiceBodyReason(hit, inv, e)
			if c.reason != "" {
				c.jobAt = len(jobs)
				jobs = append(jobs, bodyJob{email: e, reason: c.reason})
			}
		}
		cands = append(cands, c)
	}

	// 限量：date 类（已经确定是发票，只差日期）优先，candidate 类（推测性
	// 扫描）按顺序补足。超出的不静默丢弃——记进日志，下一轮还会再来。
	keptIdx := limitInvoiceBodyJobs(jobs)
	if skipped := len(jobs) - len(keptIdx); skipped > 0 {
		log.Printf("[email/pipeline] step1.5 raw-body budget %d exceeded, %d candidate(s) deferred to next run",
			maxInvoiceBodyFetches, skipped)
	}
	// jobs 的下标 → keptIdx 结果的下标，便于回填。
	posInKept := mapJobsToKeptPositions(len(jobs), keptIdx)

	// 并发拉原文：与第 1 步同理，单封信不应拖住整步。
	bodies := p.fetchBodies(ctx, keptIdx, jobs)

	created = 0
	fetchFailed := 0
	// 报错归因按成因分桶。原实现只有一句「（IMAP 侧问题，本轮未建档）」，
	// 而实测里这些失败的成因至少有三类，且「IMAP 侧问题」对其中两类是错的：
	//   - gone：服务端已无此消息（邮件被单独删除）——不是 IMAP 坏了；
	//   - pop3：POP3 来源无原文缓存且自愈失败——与 IMAP 无关；
	//   - other：网络/协议/凭据等——只有这一类谈得上「IMAP 侧」。
	// 一律写成「IMAP 侧问题」会把运维引向查一个没坏的东西（fetchInvoiceBodies
	// 的 POP3 判定链注释里记的就是同一类误导）。
	var failGone, failPOP3, failOther, failDeferred int
	newlyDead := 0
	for i := range cands {
		c := &cands[i]
		if c.jobAt >= 0 {
			k := posInKept[c.jobAt]
			var b bodyResult
			// k == -1 是「本轮被预算顺延」，没有对应的 fetch 结果。
			// 旧代码只判 `k < len(bodies)`，而顺延的 job 在 posInKept 里是零值 0，
			// 于是它会读到 bodies[0]——**另一封邮件的正文**。
			if k >= 0 && k < len(bodies) {
				b = bodies[k]
			}
			if b.err != nil || b.parsed == nil {
				fetchFailed++
				if k >= 0 {
					// 只对真正试过的这一轮记账；顺延的 job 没试过，不该累加 streak。
					kind := classifyRawBodyFetchFailure(b.err)
					switch kind {
					case rawBodyFailureGone:
						failGone++
					case rawBodyFailurePOP3:
						failPOP3++
					case rawBodyFailureOther:
						failOther++
					}
					if _, dead, merr := p.Store.MarkRawBodyGone(ctx, c.email.ID,
						kind == rawBodyFailureGone); merr != nil {
						rep.AddError("raw-body dead letter mark email=%s: %v", c.email.ID, merr)
					} else if dead && !deadLetters[c.email.ID] {
						// 本轮才跨过阈值（此前不在标记集里）。
						newlyDead++
					}
				} else {
					// 被预算顺延：它没有「拉取失败」，只是没轮到。归到
					// failOther 会把预算说成故障，所以单列。
					failDeferred++
				}
				if c.reason == "candidate" {
					// 与原实现一致：正文拉不到就没法二次提取，本轮不建档。
					// 下一轮会重来（幂等，不会重复建档）。
					c.hit = false
				}
				// date 类拉不到就保持无日期建档，行为同 fetchInvoiceDateFromBody。
			} else {
				text := b.parsed.TextBody
				if text == "" {
					text = b.parsed.HTMLBody
				}
				if c.reason == "date" {
					applyParsedBodyDate(c.inv, text)
				} else {
					// 金额常常只印在附件里（主题写「对账单」、正文写「见附件」），
					// 所以把「有没有发票类附件」一起告诉规则层，否则这封邮件会在
					// 采集器看到附件之前就被丢掉。
					inv, hit := ExtractInvoiceLoose(c.email,
						b.parsed.TextBody+"\n"+b.parsed.HTMLBody,
						HasInvoiceAttachment(b.parsed.Attachments))
					if !hit {
						c.hit = false
					} else {
						c.inv = inv
						c.hit = true
						// 复用同一份正文补日期：原来要再开一次 IMAP 会话，
						// 纯属浪费（单轮 151 封未建档时这一步是主要耗时来源）。
						if c.inv.InvoiceDate == "" {
							if d := ParseInvoiceDate(text); d != "" {
								c.inv.InvoiceDate = d
							}
						}
					}
				}
			}
		}
		if !c.hit {
			continue
		}
		if _, err := p.Store.UpsertInvoice(ctx, c.inv, c.scope[0], c.scope[1]); err != nil {
			rep.AddError("invoice upsert email=%s: %v", c.email.ID, err)
			continue
		}
		created++
	}
	if fetchFailed > 0 {
		// 归因必须能区分成因：原来一句「（IMAP 侧问题）」把「邮件已被从服务器
		// 删除」和「POP3 来源没有原文缓存」都算成 IMAP 故障，两种都会把排查
		// 引向一个没坏的东西（2026-10-03 实测的线上表现就是照这句去查 IMAP）。
		rep.AddError("invoice raw body fetch: %d 未建档（服务端已无此消息 %d、POP3 无原文且自愈失败 %d、"+
			"网络/协议/凭据等其他失败 %d、本轮被预算顺延 %d）；连续 %d 轮「服务端已无此消息」的邮件"+
			"会被淘汰，本轮新淘汰 %d 封、累计跳过 %d 封",
			fetchFailed, failGone, failPOP3, failOther, failDeferred,
			rawBodyGoneStreakThreshold, newlyDead, deadSkipped)
	}
	deferred := len(jobs) - len(keptIdx)
	rep.InvoiceCandidatesScanned = len(emails)
	rep.InvoiceCandidatesCreated = created
	rep.InvoiceBodyFetchDeferred = deferred
	rep.InvoiceBodyDeadLettered = newlyDead
	log.Printf("[email/pipeline] step1.5 window=%dd scanned=%d rawBodyFetches=%d deferred=%d fetchFailed=%d"+
		" failGone=%d failPOP3=%d failOther=%d newlyDead=%d deadSkipped=%d autoCreated=%d",
		invoiceCandidateLookbackDays, len(emails), len(keptIdx), deferred, fetchFailed,
		failGone, failPOP3, failOther, newlyDead, deadSkipped, created)
	if newlyDead > 0 {
		log.Printf("[email/pipeline] step1.5 retired %d message(s) whose raw body no longer exists server-side", newlyDead)
	}
	if created > 0 {
		log.Printf("[email/pipeline] auto-created %d invoice candidates", created)
	}
}

// mapJobsToKeptPositions 把 jobs 的下标映射到「在 keptIdx 结果里的位置」。
//
// **-1 是「本轮被预算顺延、没拉」的哨兵，绝不能用零值。**
//
// 零值会让顺延的 job 读到 bodies[0]——那是**另一封邮件**的解析结果，于是把
// 别人的正文拿来补这封的开票日期、把别人的附件当成这封的发票落进台账。
// 2026-10-03 复核时发现的既有缺陷：原实现 `make([]int, len(jobs))` 全零，
// 只给保留下来的 job 回填，而调用方的门控是 `c.jobAt >= 0`（顺延的 jobAt
// 同样 >= 0），于是顺延分支畅通无阻地读到了 bodies[0]。
//
// 抽成纯函数是因为这个映射原先内联在一个 120 行的大循环里，从外部完全
// 测不到——而它错的形态是「静默串档」，测试不写就没有牙齿。
func mapJobsToKeptPositions(numJobs int, keptIdx []int) []int {
	pos := make([]int, numJobs)
	for i := range pos {
		pos[i] = -1
	}
	for k, idx := range keptIdx {
		if idx >= 0 && idx < numJobs {
			pos[idx] = k
		}
	}
	return pos
}

// invoiceCandidateLookbackDays 是第 1.5 步扫描「尚未建档的发票候选」的回看天数。
//
// 原实现硬编码 24 小时（rep.StartedAt-86400）。实测真实库：120 封邮件里只有
// 2 封落在 24h 窗口内，唯一一张 envelope 就能识别的真实发票
// （「…的发票，发票号码：2633…，金额：3500.00元…」）在窗口之外，
// 于是 email_invoices 一直是 0 行——功能看起来实现了，实际对历史邮件、
// 上次同步失败期间积压的邮件、延迟入库的邮件**从不触发**。定时任务每天
// 跑一次，24h 窗口意味着任何一次漏掉的邮件就永久丢失。
//
// 放宽窗口不会让代价失控：envelope 判定（主题+摘要正则）不碰 IMAP，
// 只是多扫一些行；真正贵的「拉原文」仍受 maxInvoiceBodyFetches 预算限制，
// 超出的顺延到下一轮。
const invoiceCandidateLookbackDays = 90

// invoiceCandidateScanLimit 是第 1.5 步 envelope 扫描的行数上限。
//
// 必须与回看窗口配套调大：原来窗口 24h + LIMIT 500 时，500 行几乎必然被
// 最近的邮件占满，回看窗口再宽也够不到旧邮件（ORDER BY date DESC 从最新
// 开始取）。Store.ListEmailsSince 自身把 >2000 的值重置为 500，故此处取其上限。
const invoiceCandidateScanLimit = 2000

// maxInvoiceBodyFetches 是第 1.5 步单轮最多拉多少封原文。
//
// 每封原文 = 一次完整 IMAP 会话（dial/login/select/fetch）。实测 24h 窗口内
// 151 封未建档邮件时，串行无上限的版本跑了 6 分钟仍未完。限量让单轮耗时
// 有上界；超出的 candidate（推测性扫描）顺延到下一轮，date（已确定是发票
// 只差日期）永远优先，不会被挤掉。
const maxInvoiceBodyFetches = 24

// bodyJob 是一封「需要拉原文」的邮件及拉它的目的。
type bodyJob struct {
	email  Email
	reason string // "date" | "candidate"
}

// bodyResult 是拉原文的结果。
type bodyResult struct {
	parsed *ParsedMessage
	err    error
}

// limitInvoiceBodyJobs 在 maxInvoiceBodyFetches 预算内挑选要拉原文的 job，
// 返回被保留的 job 在原切片中的下标。date 类优先，candidate 类按原顺序补足。
func limitInvoiceBodyJobs(jobs []bodyJob) []int {
	if len(jobs) <= maxInvoiceBodyFetches {
		kept := make([]int, len(jobs))
		for i := range jobs {
			kept[i] = i
		}
		return kept
	}
	kept := make([]int, 0, maxInvoiceBodyFetches)
	for i := range jobs {
		if jobs[i].reason == "date" && len(kept) < maxInvoiceBodyFetches {
			kept = append(kept, i)
		}
	}
	for i := range jobs {
		if jobs[i].reason == "candidate" && len(kept) < maxInvoiceBodyFetches {
			kept = append(kept, i)
		}
	}
	return kept
}

// invoiceBodyReason 判定这封邮件本轮值不值得拉原文，拉了为什么。
//
// 抽成纯函数是因为这个判定原先埋在 step1.5 的巨型循环里（invoiceCandidate
// 是函数内局部类型），从外部**完全无法测试**——而 "date" 这条分支正是为
// 真实缺陷加的：IMAP 路径只落 envelope，开票日期在正文里，导致规范文件名
// 退化成采集当天。修了却没有任何测试保护，等于没修。
//
// 返回 "date" | "candidate" | ""。
func invoiceBodyReason(hit bool, inv *Invoice, e Email) string {
	switch {
	case !hit && InvoiceCandidate(e):
		// 关键词命中才值得读正文：24h 窗口内 miss 邮件可能几十封。
		// 与 server 侧 extractInvoicesAsync 的门槛一致。
		return "candidate"
	case hit && inv != nil && inv.InvoiceDate == "":
		// 命中了但**没有开票日期**：正文里的「开票日期」看不到，
		// 规范文件名会退化成采集当天（真库实测：
		// 「其他-杭州创客家…-3500.00-2026-10-01.pdf」里的 2026-10-01
		// 是采集当天，不是票面开票日期——该行的 invoice_date 至今为空）。
		return "date"
	}
	return ""
}

// applyParsedBodyDate 用拉回来的正文补开票日期。已有日期时**不覆盖**——
// 正文里的散落日期可能不是票面日期，envelope/XML 解析出的值更可信。
// 返回最终生效的日期（空串表示仍无日期）。
func applyParsedBodyDate(inv *Invoice, text string) string {
	if inv == nil || inv.InvoiceDate != "" || text == "" {
		if inv == nil {
			return ""
		}
		return inv.InvoiceDate
	}
	if d := ParseInvoiceDate(text); d != "" {
		inv.InvoiceDate = d
	}
	return inv.InvoiceDate
}

// fetchBodies 有界并发地拉取这些 job 的原文并解析。
func (p *Pipeline) fetchBodies(ctx context.Context, keptIdx []int, jobs []bodyJob) []bodyResult {
	results := make([]bodyResult, len(keptIdx))
	if len(keptIdx) == 0 {
		return results
	}
	workers := syncConcurrency
	if workers > len(keptIdx) {
		workers = len(keptIdx)
	}
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for k, idx := range keptIdx {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(k, idx int) {
			defer wg.Done()
			defer func() { <-sem }()
			e := jobs[idx].email
			t0 := time.Now()
			// 取原文必须**POP3 感知**：FetchMessageRaw 是 IMAP 专用的
			// （mime.go:98 无条件 dial IMAPHost:IMAPPort），而 POP3 降级路径
			// 落库的邮件 em.UID 是位置序号，对 IMAP UID FETCH 毫无意义。
			// 此前这一趟直接调 FetchMessageRaw，于是 POP3 来源的发票候选
			// 取原文必然失败 → 永远建不了档（2026-10-03 实测：两封通行费
			// 电子发票 24.61 元一直没有台账行，而它们的原文就在
			// data/email-bodies-raw/ 里躺着）。与采集器共用 resolveRawBody，
			// 免得两处实现再次漂移——上一次漂移就是这么发生的。
			raw, src, err := resolveRawBody(ctx, p.Fetcher, p.BodyCache, &e,
				fmt.Sprintf(" step1.5 email=%s", e.ID))
			if err != nil {
				results[k] = bodyResult{err: err}
				log.Printf("[email/pipeline] step1.5 body fetch email=%s acct=%s uid=%d via=%s FAILED after %s: %v",
					e.ID, e.AccountID, e.UID, src, time.Since(t0).Round(time.Millisecond), err)
				return
			}
			parsed, perr := ParseMIMEMessage(raw)
			results[k] = bodyResult{parsed: parsed, err: perr}
			log.Printf("[email/pipeline] step1.5 body fetch acct=%s uid=%d in %s",
				e.AccountID, e.UID, time.Since(t0).Round(time.Millisecond))
		}(k, idx)
	}
	wg.Wait()
	return results
}

// fetchInvoiceDateFromBody 拉原文正文找开票日期。失败返回空串（不阻断流水线）。
func (p *Pipeline) fetchInvoiceDateFromBody(ctx context.Context, e Email) string {
	raw, err := p.Fetcher.FetchMessageRaw(ctx, e.AccountID, e.UID)
	if err != nil {
		return ""
	}
	parsed, perr := ParseMIMEMessage(raw)
	if perr != nil {
		return ""
	}
	text := parsed.TextBody
	if text == "" {
		text = parsed.HTMLBody
	}
	return ParseInvoiceDate(text)
}

// cleanSpam 扫描近期邮件，把广告/垃圾移进 IMAP 垃圾箱并落本地标记。
//
// SpamDryRun=true 时**只判定不移动**：把「会移哪些、为什么」写进报告，
// 不发任何 IMAP MOVE、也不改本地分类。这是给真实邮箱上的第一次运行留的
// 安全阀——垃圾判定规则没在真实邮箱上验证过，让它无人值守地搬真实邮件
// 风险太大；先看判定结果，确认无误再关掉 dryRun。
func (p *Pipeline) cleanSpam(ctx context.Context, rep *PipelineReport) {
	lookback := p.SpamLookbackDays
	if lookback <= 0 {
		lookback = 7
	}
	since := time.Now().AddDate(0, 0, -lookback).Unix()
	emails, _, err := p.Store.ListEmailsSince(ctx, since, 1000)
	if err != nil {
		rep.AddError("spam scan list: %v", err)
		return
	}
	byAccount := map[string][]int64{}
	whyByAccount := map[string]string{}
	samplesByAccount := map[string][]string{}
	// near-miss：参与了评分但没过 100 阈值的邮件。
	//
	// 为什么必须单独列出来：预演报告是你决定「要不要开真实 MOVE」的唯一依据，
	// 而只看命中/未命中两态时，规则离真实数据有多远是看不见的。实测 444 封
	// 真实邮件里命中 0 封，但有 17 封拿到 30 分（全是 newsletter/EDM 发件人
	// 特征）——它们就卡在门槛外侧。阈值该不该调、这些订阅该不该留，只能由你
	// 看着具体主题决定，规则自己不该替你决定。
	nearByAccount := map[string][]SpamNearMiss{}
	// 同发件人在本批里的封数：判断「列表推送 vs 人际邮件」需要跨封信息，
	// 纯函数 LooksLikeSpam 自己拿不到，由这里统计后传进去。
	senderVolume := map[string]int{}
	for i := range emails {
		senderVolume[strings.ToLower(strings.TrimSpace(emails[i].FromAddress))]++
	}
	for i := range emails {
		e := emails[i]
		if e.Category == "spam" || e.Category == "archived" {
			continue
		}
		inv := InvoiceCandidate(e)
		v := LooksLikeSpam(e.FromAddress, e.Subject, e.Snippet, inv, e.Importance == "high",
			senderVolume[strings.ToLower(strings.TrimSpace(e.FromAddress))])
		if v.Spam {
			byAccount[e.AccountID] = append(byAccount[e.AccountID], e.UID)
			if whyByAccount[e.AccountID] == "" {
				whyByAccount[e.AccountID] = v.Why
			}
			if len(samplesByAccount[e.AccountID]) < spamPreviewCap {
				samplesByAccount[e.AccountID] = append(samplesByAccount[e.AccountID], e.Subject)
			}
			continue
		}
		if v.Score > 0 && len(nearByAccount[e.AccountID]) < spamPreviewCap {
			nearByAccount[e.AccountID] = append(nearByAccount[e.AccountID], SpamNearMiss{
				From:    e.FromAddress,
				Subject: e.Subject,
				Score:   v.Score,
				Why:     v.Why,
			})
		}
	}
	// 按分数降序：最接近阈值的排最前。
	for _, list := range nearByAccount {
		sort.Slice(list, func(i, j int) bool { return list[i].Score > list[j].Score })
	}
	if p.SpamDryRun {
		for accountID, uids := range byAccount {
			// += len(uids) 而不是 ++：单位必须是邮件，见字段注释。
			rep.SpamDryRun += len(uids)
			rep.SpamDryRunSamples = append(rep.SpamDryRunSamples, SpamPreviewItem{
				AccountID: accountID,
				Count:     len(uids),
				Why:       whyByAccount[accountID],
				Subjects:  samplesByAccount[accountID],
			})
		}
		// near-miss 区块：即便一封都没命中也要输出，否则「0」和「规则失灵」
		// 在报告里长得一模一样（这正是 2026-10-01 排查时踩的坑）。
		accountIDs := make([]string, 0, len(nearByAccount))
		for id := range nearByAccount {
			accountIDs = append(accountIDs, id)
		}
		sort.Strings(accountIDs)
		for _, id := range accountIDs {
			rep.SpamNearMiss = append(rep.SpamNearMiss, SpamPreviewItem{
				AccountID: id,
				Count:     len(nearByAccount[id]),
				Near:      nearByAccount[id],
			})
		}
		log.Printf("[email/pipeline] spam dry-run: %d mail(s) across %d account(s) would be moved, %d near-miss (未判垃圾但有分，开真实 MOVE 前值得人看一眼)",
			rep.SpamDryRun, len(rep.SpamDryRunSamples), countNearMiss(rep.SpamNearMiss))
		return
	}
	for accountID, uids := range byAccount {
		if p.Fetcher != nil {
			moved, err := p.Fetcher.MoveEmailsToJunk(ctx, accountID, uids)
			rep.SpamMoved += moved
			if err != nil {
				// MOVE 失败（无垃圾箱/服务器拒绝）：仍落本地标记，保持收件箱视图干净
				rep.SpamLocalOnly += len(uids) - moved
				rep.AddError("junk move account=%s: %v", accountID, err)
			}
		}
		if err := p.Store.MarkEmailsSpamByUID(ctx, accountID, uids); err != nil {
			rep.AddError("spam mark account=%s: %v", accountID, err)
		}
		if len(uids) > 0 {
			log.Printf("[email/pipeline] spam clean account=%s uids=%d why=%q", accountID, len(uids), whyByAccount[accountID])
		}
	}
}

// splitReminderCandidates 把扫描到的邮件分成「该提醒」与「还没被分类过」两组。
//
// 抽成纯函数是为了能脱离数据库验证这段判定 —— 它决定需求 4 到底是
// 「链路正常、只是这批邮件不重要」还是「邮件根本没进过 AI 分类」，
// 而这两种情况在旧的 `remindersSent=0` 里长得一模一样。
func splitReminderCandidates(emails []Email, notified []int64) (toNotify []Email, unclassified int) {
	for i := range emails {
		if i >= len(notified) {
			break
		}
		e := emails[i]
		if notified[i] > 0 || e.Category == "spam" {
			continue
		}
		switch e.Importance {
		case "high":
			toNotify = append(toNotify, e)
		case "":
			// 还没被 AI 分类过：既不是「已提醒」，也不是「不重要」，
			// 它只是**不知道**。单独计数，否则报告里的 0 无法解释。
			unclassified++
		}
	}
	return toNotify, unclassified
}

// importantReminderLookbackDays 是重要邮件提醒的回看天数。
//
// 原实现硬编码 2 天（time.Now().AddDate(0, 0, -2)）。缺陷与
// invoiceCandidateLookbackDays 是同一个：定时任务每天跑一次，2 天窗口意味着
// 任何一次漏掉的邮件——服务端重启、通知中心当时不可用、AI 分类器连续失败、
// 机器关机——都会**永久**失去被提醒的机会。
//
// 2026-10-02 对真实库只读统计（55 封 importance=high，未删）：
//
//	落在 2 天窗口内   =  1 封
//	落在 2 天窗口外   = 54 封
//	  其中从未提醒过  = 30 封  ← 这 30 封在 2 天窗口下**永远不会被提醒**
//	最老的一封 high   = 23 天前
//
// 也就是说 2 天窗口下，当前每 55 封重要邮件只有 1 封真正进得了扫描。
// 90 天窗口能完整覆盖现有数据（最老的 high 才 23 天），还留有余量应对积压。
//
// 放宽到 90 天的代价：扫描行数变多（不碰 IMAP，只是多读几行 emails），
// 且首次上线时会把积压的老 high 一次性全部推送出来（按当前数据约 30 封）。
// 后者是产品取舍（要不要限流、要不要只推最近 N 条），已单列待拍板，
// **不在本轮擅自决定**。
const importantReminderLookbackDays = 90

// importantReminderScanLimit 是重要提醒扫描的行数上限。
//
// 必须与回看窗口配套调大，理由与 invoiceCandidateScanLimit 完全一致：
// Store.ListEmailsSince 是 `ORDER BY date DESC LIMIT n`，500 行几乎必然被
// 最近的邮件占满，**宽窗口形同虚设**——被挤掉的恰好是窗口末端那些
// 「重要但很老」的邮件，也就是这个窗口本来要救的那批。Store.ListEmailsSince
// 自身把 >2000 的值重置为 500，故此处取其上限。
const importantReminderScanLimit = 2000

// notifyImportant 对未提醒过的重要邮件派发通知并记录时间。
func (p *Pipeline) notifyImportant(ctx context.Context, rep *PipelineReport) {
	if p.Notifier == nil {
		return
	}
	since := time.Now().AddDate(0, 0, -importantReminderLookbackDays).Unix()
	// 窗口之外的高重要度邮件：它们**永远不会被提醒**，但报告上原本看不出来。
	// 先数出来，再决定要不要改窗口——改窗口是产品取舍，可见性不是。
	if n, err := p.Store.CountHighImportanceOutside(ctx, since, 2000); err != nil {
		rep.AddError("reminder out-of-window count: %v", err)
	} else if n > 0 {
		rep.RemindersOutOfWindow = n
		log.Printf("[email/pipeline] %d 封 importance=high 的邮件早于 %d 秒（%d 天窗口），"+
			"**永远不会进入重要提醒** —— 报告里 RemindersSent=0 有一部分是这个原因",
			n, time.Now().Unix()-since, importantReminderLookbackDays)
	}
	emails, notified, err := p.Store.ListEmailsSince(ctx, since, importantReminderScanLimit)
	if err != nil {
		rep.AddError("reminder scan list: %v", err)
		return
	}
	rep.RemindersScanned = len(emails)
	candidates, unclassified := splitReminderCandidates(emails, notified)
	rep.RemindersUnclassified = unclassified
	// 在推之前就把条数记下来。推送失败时 Pending > Sent，那个差值就是
	// 「本该提醒却没提醒出去」的数量——只有 Sent 时这个信息就丢了。
	rep.RemindersPending = len(candidates)
	if rep.RemindersPending > 0 {
		log.Printf("[email/pipeline] 本轮将推送 %d 条重要邮件提醒（%d 天窗口内、importance=high、"+
			"从未提醒、非 spam）。notifyImportant 没有限流，条数就是候选数——"+
			"积压会在首次覆盖到它们的这一轮一次性推出。",
			rep.RemindersPending, importantReminderLookbackDays)
	}
	if unclassified > 0 {
		log.Printf("[email/pipeline] %s", reminderUnclassifiedHint(unclassified, len(emails)))
	}
	var ids []string
	var sent []Email
	for _, e := range candidates {
		if err := p.Notifier.NotifyImportantEmail(ctx, e); err != nil {
			rep.AddError("notify email=%s: %v", e.ID, err)
			continue
		}
		sent = append(sent, e)
		ids = append(ids, e.ID)
	}
	if len(ids) > 0 {
		if err := p.Store.MarkEmailsNotified(ctx, ids, time.Now().Unix()); err != nil {
			rep.AddError("mark notified: %v", err)
		}
		rep.RemindersSent = len(ids)
		log.Printf("[email/pipeline] reminders sent: %v", emailSubjects(sent))
	}
}

// reminderUnclassifiedHint 组装「importance 为空」的诊断提示。
//
// 单独抽成纯函数，是因为这段提示本身是被当排查入口用的，写错比不写更糟。
// 它原来只说「未被 AI 分类过（检查 POCKET_KXMEMORY_BASE_URL）」，但 importance
// 在生产里其实有**两条**写入路径，只提一条会把排查方向带偏：
//
//  1. 账户规则：fetcher.go 里 `rules.Evaluate` 命中 mark-important 时，
//     入库即写 importance=high，与 AI 完全无关。前提是该账户配了
//     email_accounts.rules（为空时 ParseRules 返回 nil，整条路径不执行）。
//  2. AI 分类：classify_run.go → SetClassificationScoped，需要
//     POCKET_KXMEMORY_BASE_URL 或已接线的 LLM provider。
//
// 真实库实测（**2026-10-02 复核，此前版本记的是「importance 恒为空」，已被证伪**）：
//
//	importance 分布（127 封未删）：high 55 / medium 44 / low 23 / 空 5
//	high 且 notified_at>0：24 封   ← 提醒链路历史上确实触发过
//	email_accounts.rules：5 个账户**全为 NULL**（规则路径确实没配）
//
// 也就是说「两条路都不通」是**错的**：只有规则路径不通，AI 路径曾经通过。
// 保持旧结论会让人把排查方向带到「去配 kxmemory」上，而真正该看的是
// 定时路径为什么不再分类（见下）。
//
// **2026-10-02 另一个坑：kxmemory 未配时定时路径根本不分类。**
// scheduler 同步成功后的判定里 `s.kxmem == nil` 会直接 return，而 LLM 网关
// 兜底（server 包的 classifyViaGateway）只接在 HTTP 端点
// /api/emails/classify 上，Scheduler 在本包内拿不到它。也就是
// **手动触发能分类、每天自动跑不分类**，而这个差异此前无任何日志。
// 现已由 ClassifySkipReason + sync.Once 日志暴露（见 classify_run.go）。
//
// 教训：这段注释自己写着「真实库实测」，但**记录状态的注释不会自己声明
// 过期**。我本人 2026-10-02 就因为直接引用了上一版结论，误判成
// 「q4 的 90 天窗口一封也不会提醒」，被真库数据当场打脸。
func reminderUnclassifiedHint(unclassified, scanned int) string {
	return fmt.Sprintf("%d/%d 封邮件 importance 为空 —— 不会进入重要提醒。"+
		"importance 只有两条写入路径：① 账户规则（email_accounts.rules 里的 "+
		"mark-important，入库即生效，与 AI 无关）② AI 分类"+
		"（POCKET_KXMEMORY_BASE_URL 或已接线的 LLM provider）。"+
		"两条都不通时就是这个结果，先确认账户有没有配 rules。",
		unclassified, scanned)
}

// pushInvoices 已由 Run 内的 scope 循环实现（见上）。

func emailSubjects(emails []Email) []string {
	out := make([]string, 0, len(emails))
	for _, e := range emails {
		out = append(out, e.Subject)
	}
	return out
}

// pushInvoiceSet 把给定发票推飞书（下载文件读盘），成功标记 feishu_sent_at。
// 推送失败保留 feishu_sent_at=0，由共享汇总文档兜底（需求允许两条路径）。
func (p *Pipeline) pushInvoiceSet(ctx context.Context, invoices []Invoice, userID, workspaceID string, rep *PipelineReport) {
	if p.Pusher == nil {
		rep.FeishuSkip = "feishu pusher not configured"
		return
	}
	if !p.Pusher.Available() {
		// 这个函数每个 scope 调一次，所以只在第一次记时打日志，否则一轮
		// 5 个账户就刷 5 行同样的告警，真正出事时反而看不见（与
		// ClassifySkipOnce 同一个理由）。
		if rep.FeishuSkip == "" {
			log.Printf("[email/pipeline] 飞书推送被跳过：%s —— 本轮不会推任何发票到飞书，"+
				"需求 3 的「发送到飞书」这一步没有执行。报告里 feishuSkip 非空即为此故。",
				"feishu credentials missing (POCKET_FEISHU_APP_ID / APP_SECRET / INVOICE_CHAT_ID)")
		}
		rep.FeishuSkip = "feishu credentials missing (POCKET_FEISHU_APP_ID / APP_SECRET / INVOICE_CHAT_ID)"
		return
	}
	var pushed []string
	for _, inv := range invoices {
		if inv.Status != "downloaded" || inv.FeishuSentAt > 0 || inv.FilePath == "" {
			continue
		}
		abs := filepath.Join(p.DataDir, inv.FilePath)
		if err := p.Pusher.PushInvoice(ctx, inv, abs); err != nil {
			rep.FeishuFailed++
			rep.AddError("push %s: %v", inv.FileName, err)
			continue
		}
		pushed = append(pushed, inv.ID)
	}
	if len(pushed) > 0 {
		if err := p.Store.MarkInvoicesFeishuSent(ctx, pushed, userID, workspaceID, time.Now().Unix()); err != nil {
			rep.AddError("mark feishu sent: %v", err)
		}
		rep.FeishuPushed += len(pushed)
	}
}

// PushInvoicesScoped 供 server 层手动触发：推送指定（或全部 downloaded）发票。
func (p *Pipeline) PushInvoicesScoped(ctx context.Context, ids []string, userID, workspaceID string) *PipelineReport {
	rep := &PipelineReport{StartedAt: time.Now().Unix()}
	invoices, err := p.Store.ListInvoicesByIDScoped(ctx, ids, userID, workspaceID)
	if err != nil {
		rep.AddError("list invoices: %v", err)
		return rep
	}
	p.pushInvoiceSet(ctx, invoices, userID, workspaceID, rep)
	rep.FinishedAt = time.Now().Unix()
	return rep
}

// PublishLedgerScoped 把该 scope 的全部发票清单发布成共享台账（飞书电子表格）。
//
// 与 BuildInvoiceSummaryDocs 的分工：后者写设备本地文件（离线兜底/留存），
// 这里产出**别人能打开的**共享文档。发布器为空或不可用时返回空 URL + nil error，
// 调用方据此不报错——本地汇总照常生成。
//
// 复用：同一 (workspace, user) 在进程内已经建过台账时直接返回上一次的链接。
// 没有这一步，GET /api/emails/invoices/summary 每刷新一次就新建一张飞书表格，
// 用户的云盘会被同一个清单刷屏——读接口产生这种副作用本身就是错的。
// 飞书目前没有「按标题查表」的接口可用，所以复用只能做到进程级；
// 重启后第一次读仍会新建一张，之后复用。
func (p *Pipeline) PublishLedgerScoped(ctx context.Context, userID, workspaceID string) (string, error) {
	if p.Ledger == nil || !p.Ledger.Available() {
		return "", nil
	}
	if url := p.Ledger.PublishedURL(workspaceID, userID); url != "" {
		return url, nil
	}
	invoices, err := p.Store.ListInvoicesScoped(ctx, userID, workspaceID, "", 500)
	if err != nil {
		return "", fmt.Errorf("list invoices: %w", err)
	}
	if len(invoices) == 0 {
		return "", nil
	}
	title := LedgerTitle(workspaceID, time.Now())
	url, err := p.Ledger.PublishLedger(ctx, title, invoices)
	if err != nil {
		return "", err
	}
	p.Ledger.RememberPublished(workspaceID, userID, url)
	return url, nil
}

// utf8BOM 是 UTF-8 字节序标记（EF BB BF）。
//
// 只用在会被 Excel 打开的 CSV 上——见 BuildInvoiceSummaryDocs 里的说明。
const utf8BOM = "\xEF\xBB\xBF"

// BuildInvoiceSummaryDocs 生成共享汇总文档（CSV 清单 + Markdown 报表，
// 含合计金额）。返回两个文件的绝对路径。文件总在每轮流水线末尾重建，
// 作为「无法发送飞书时的共享文档」兜底与对账清单。
func (p *Pipeline) BuildInvoiceSummaryDocs(ctx context.Context, userID, workspaceID string) (csvPath, mdPath string, err error) {
	invoices, err := p.Store.ListInvoicesScoped(ctx, userID, workspaceID, "", 500)
	if err != nil {
		return "", "", err
	}
	return WriteInvoiceSummaryDocs(p.DataDir, workspaceID, invoices)
}

// invoiceSummaryHeader 是汇总 CSV 的列定义。合计行按**这张表**定位「金额」列，
// 不再靠手数字符串里的逗号个数。
//
// 2026-10-02 加「核验」列：与 ledger.go 的飞书表头保持同一组列。两侧列数/
// 列序必须一致，否则用户在飞书表格和本地 CSV 之间对照时会错位。
var invoiceSummaryHeader = []string{
	"费用类型", "对方单位", "金额", "币种", "发票号", "日期", "状态", "核验", "文件名", "来源邮件",
}

// invoiceSummaryTotalRow 生成合计行，长度与表头一致，金额落在「金额」列。
//
// 2026-10-02 修正：原来写死 `"合计,,,,,,,%.2f,\n"`，7 个逗号把 3500.00 放到了
// **第 8 列「文件名」**——用 CSV 解析器实测确认（金额列空着、文件名列写着合计）。
// 需求原文要的是「汇总金额」，落在文件名列里，人在 Excel 里根本对不上账。
// 改成按表头定位，以后调整列顺序也不会再错位。
//
// 2026-10-02 合并修订：参数从 float64 改成 string。调用方现在按币种分组，
// 每个币种的金额是**已经按整数分算好的字符串**（centsByCur[cur]/100），
// 若这里再收 float64 走一遍 fmt("%.2f") 就等于允许调用方传一个没对齐
// round2 的值——那正是本函数当初要消灭的那类错位。传字符串让「已格式化」
// 这件事在类型上可见。
func invoiceSummaryTotalRow(amount string) []string {
	row := make([]string, len(invoiceSummaryHeader))
	row[0] = "合计"
	for i, col := range invoiceSummaryHeader {
		if col == "金额" {
			row[i] = amount
		}
	}
	return row
}

// WriteInvoiceSummaryDocs 把发票清单写为 CSV + Markdown 汇总文档。
func WriteInvoiceSummaryDocs(dataDir, workspaceID string, invoices []Invoice) (string, string, error) {
	dir := filepath.Join(dataDir, "email-invoices", "exports", defaultWorkspace(workspaceID))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", err
	}
	stamp := time.Now().Format("20060102-150405")
	csvPath := filepath.Join(dir, "invoices-summary-"+stamp+".csv")
	mdPath := filepath.Join(dir, "invoices-summary-"+stamp+".md")

	// 合计与明细行必须用**同一个** round2 口径，否则用户拿计算器逐行相加
	// 会对不上账。实测（2026-10-01）：明细 1.005 / 2.675 / 8.615 时，
	// 逐行 %.2f 相加 = 12.30，而裸 float64 累加再 %.2f = 12.29，差 1 分。
	// 整数分累加保证 total 与 sum(round2(每行)) 恒等。
	//
	// 合计**按币种分组**（2026-10-01 补）。本函数是 WriteInvoiceSummaryDocs，
	// 与飞书表格那条路径（ledger.go:LedgerRows）是两段独立代码：LedgerRows
	// 早已按币种分组出多行合计，而这里仍把所有币种直接相加，于是
	// 100.00 USD + 50.00 CNY 会在本地 CSV 里写成「合计 150.00」——币种
	// 列是空的、Markdown 标题里也没有币种，读者无从判断这个数字是什么。
	// 跨币种相加不是金额。同一条需求（「汇总金额」）的两条路径必须同口径。
	//
	// 单一币种（当前真实数据 7 张全是 CNY）时保持旧输出形状不变：仍是一行
	// 不带币种标签的合计，避免让已有的对账习惯（按下标取第 8 列）失效。
	//
	// 冲突记录：main 侧（243cda44）在这里只有一个裸 `var total float64` +
	// `counted`，且合计行由 invoiceSummaryTotalRow(total) 单独追加在 CSV
	// 末尾。取本分支侧的按币种分组形态，但把 main 的 `counted` 叠加进来
	// （见下面 Markdown 抬头）——两者不是同一件事：counted 回答「几张被计入」，
	// centsByCur 回答「各币种各多少」。单取任一侧都会丢掉另一半语义。
	centsByCur := map[string]int64{}
	countByCur := map[string]int{}
	var curOrder []string
	rows := make([][]string, 0, len(invoices))
	for _, inv := range invoices {
		amount := round2(inv.Amount)
		cur := currencyOrDefault(inv.Currency)
		// 明细行：所有发票都列出来（不计入合计 ≠ 从列表消失）。
		rows = append(rows, []string{
			inv.Category, inv.Seller, fmt.Sprintf("%.2f", amount), inv.Currency,
			inv.InvoiceNo, inv.InvoiceDate, inv.Status, InvoiceVerifiedLabel(inv), inv.FileName, inv.Subject,
		})
		// 计入合计的门槛用**唯一**判据 InvoiceCountsTowardTotal，与 LedgerRows
		// 逐字共用同一个函数。此前这里和 ledger.go 各写了一份逐字符相同的
		// 内联表达式——两份相同的代码就是两份可以各自漂移的代码。
		//
		// 注意 counted 与 centsByCur 在同一处递增：把「计入了几张」和
		// 「各币种各多少」绑在一起，才能在 Markdown 头部如实说明覆盖范围。
		if !InvoiceCountsTowardTotal(inv) {
			continue
		}
		if _, seen := centsByCur[cur]; !seen {
			curOrder = append(curOrder, cur)
		}
		centsByCur[cur] += int64(math.Round(amount * 100))
		countByCur[cur]++
	}
	// counted 是**全部币种**的计入张数（不分币种），供 Markdown 抬头说清
	// 覆盖范围；它与 countByCur 的关系是 sum(countByCur[cur]) == counted。
	counted := 0
	for _, cur := range curOrder {
		counted += countByCur[cur]
	}

	// 每个币种一行合计：币种列带上币种，金额按**表头定位**而不是硬编码下标。
	//
	// 冲突记录：main 侧（243cda44）为此专门抽了 invoiceSummaryTotalRow，
	// 因为原来的 `"合计,,,,,,,%.2f,\n"` 用 7 个逗号把金额放到了**第 8 列
	// 「文件名」**——用 CSV 解析器实测确认过（金额列空着、文件名列写着合计）。
	// 需求原文要的是「汇总金额」，落在文件名列里人在 Excel 里根本对不上账。
	// 本分支的 totalRows 同样是硬编码下标，且第 7 位（索引 7）正好是「文件名」，
	// 于是**继承了同一个缺陷**。这里取 main 的修法：按 invoiceSummaryHeader
	// 里的列名定位，以后调整列顺序也不会再错位。
	totalRows := make([][]string, 0, len(curOrder)+1)
	for _, cur := range curOrder {
		cells := invoiceSummaryTotalRow(fmt.Sprintf("%.2f", float64(centsByCur[cur])/100))
		// 多币种时才在币种列标注；单币种保持旧形状（不带币种标签），
		// 避免让已有的对账习惯失效。
		if len(curOrder) > 1 {
			for i, col := range invoiceSummaryHeader {
				if col == "币种" {
					cells[i] = cur
				}
			}
		}
		totalRows = append(totalRows, cells)
	}
	// 空清单也必须有合计行（需求：「整理一个列表…并汇总金额」）：
	// 只有表头 + 一行 0 合计，下游按行数算范围时才不用特判。与 LedgerRows 同理。
	// 同样走 invoiceSummaryTotalRow —— 这里也**不能**硬编码下标（见上）。
	if len(curOrder) == 0 {
		totalRows = append(totalRows, invoiceSummaryTotalRow("0.00"))
	}

	// 供 Markdown 抬头用：单币种给一个数，多币种给逐币种的描述。
	sumByCur := make([]string, 0, len(curOrder))
	var total float64
	for _, cur := range curOrder {
		sum := float64(centsByCur[cur]) / 100
		total += sum
		if len(curOrder) > 1 {
			sumByCur = append(sumByCur, fmt.Sprintf("%s %.2f", cur, sum))
		}
	}

	csv := &strings.Builder{}
	csv.WriteString(strings.Join(invoiceSummaryHeader, ",") + "\n")
	// 合计行只进 CSV。它不能混进 rows —— Markdown 表格按 7 列渲染每一行，
	// 塞进去会多出一张空壳的「合计 | | | 3500.00 | ...」行（金额会落在状态列）。
	for _, r := range append(append([][]string{}, rows...), totalRows...) {
		cells := make([]string, len(r))
		for i, c := range r {
			cells[i] = csvSafeCell(c)
		}
		csv.WriteString(strings.Join(cells, ",") + "\n")
	}

	// 抬头必须说清是哪种货币：多币种时逐币种列出，绝不给一个无币种的裸数字。
	amountSummary := fmt.Sprintf("%.2f", total)
	if len(sumByCur) > 0 {
		amountSummary = strings.Join(sumByCur, " + ")
	}
	md := &strings.Builder{}
	md.WriteString("# 发票汇总\n\n")
	// 抬头必须说清三件事：共几张、其中几张计入合计、合计金额是多少。
	//
	// 「共 N 张」和「计入合计 M 张」必须分开说。main 侧（243cda44）发现：
	// 原来头部写的是 len(invoices)（**全部**发票），而金额只累加
	// status ∈ {downloaded, filed} 且 FilePath 非空的。于是只要清单里混进
	// pending/failed 发票，头部就是「共 3 张 · 合计金额 100.00」——读者必然
	// 以为这 3 张都算进了 100，实际只有 1 张。和 2026-10-01 修过的
	// LedgerTotal 是同一类问题：同一个数字在两处用不同口径，且没有任何提示。
	//
	// 而金额本身用 amountSummary（多币种时逐币种列出），那是本分支的形态：
	// 绝不给一个无币种的裸数字。两者说的是不同的事，必须都在。
	md.WriteString(fmt.Sprintf("生成时间：%s · 共 %d 张（计入合计 %d 张）· 合计金额 **%s**\n\n",
		time.Now().Format("2006-01-02 15:04"), len(invoices), counted, amountSummary))
	md.WriteString("| 费用类型 | 对方单位 | 金额 | 发票号 | 日期 | 状态 | 核验 |\n")
	// 末列是**核验状态**（r[7]=InvoiceVerifiedLabel），不是文件名。
	// 原先这里写的是「| 文件 |」——表头说文件、内容是「已核验/未核验」，
	// 而文件名在 r[8]，从头到尾没进过 Markdown。CSV 侧是「核验」与
	// 「文件名」两列分开的，没有这个问题。
	// 要在 MD 里也带文件名就**加一列**，不要把这一列改名了事——
	// 下方合计行的列数假设依赖这个 7 列形状。
	md.WriteString("|---|---|---:|---|---|---|---|\n")
	for _, r := range rows {
		md.WriteString(fmt.Sprintf("| %s | %s | %s %s | %s | %s | %s | %s |\n",
			r[0], r[1], r[2], r[3], r[4], r[5], r[6], r[7]))
	}
	// 多币种时在明细表后附逐币种合计，单币种不加（与 CSV 一行合计对应）。
	if len(sumByCur) > 0 {
		md.WriteString("\n## 合计（按币种）\n\n")
		for _, cur := range curOrder {
			md.WriteString(fmt.Sprintf("- %s：%.2f（共 %d 张）\n",
				cur, float64(centsByCur[cur])/100, countByCur[cur]))
		}
	}
	// CSV 必须带 UTF-8 BOM（2026-10-02 修，实测证据见 §7ct）。
	//
	// 为什么：中文 Windows 的 Excel 打开**无 BOM** 的 UTF-8 CSV 时，会按系统
	// ANSI 代码页（GBK）解码，于是「费用类型/对方单位/金额」全变乱码。
	// 真实文件实测首 3 字节 = E8 B4 B9（"费" 的 UTF-8 前三字节），确实没有 BOM。
	// 这份 CSV 是需求 3 明确要交付的「整理一个列表」——用户拿到打不开就等于没做。
	//
	// 只给 CSV 加，不给 Markdown 加：MD 不由 Excel 打开，BOM 反而会在第一行
	// 前面多出三个不可见字符。
	//
	// 无兼容风险：全树没有任何代码解析这些 CSV——它们只以**文件名**的形式
	// 过 API（shareDocCsv），测试里也只有 os.Stat，不读内容。
	//
	// 合并事故记录：解 243cda44 的冲突时，这一整段（含 os.WriteFile）曾被
	// 一起吞掉，函数只写 MD 就返回。症状很隐蔽——编译通过、vet 通过、
	// 多数用例照常绿，只有真正去 stat CSV 的那几个转红
	// （TestWriteInvoiceSummaryDocs_*、TestBuildInvoiceSummaryDocs_*），
	// 报的却是「文件不存在」而不是「少了 WriteFile」。
	if err := os.WriteFile(csvPath, append([]byte(utf8BOM), []byte(csv.String())...), 0o600); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(mdPath, []byte(md.String()), 0o600); err != nil {
		return csvPath, "", err
	}
	return csvPath, mdPath, nil
}

// csvSafeCell 防 CSV 公式注入（=/-/+/@ 开头的单元格前置单引号）并转义引号。
func csvSafeCell(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if s[0] == '=' || s[0] == '-' || s[0] == '+' || s[0] == '@' {
		s = "'" + s
	}
	if strings.ContainsAny(s, ",\"\n") {
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	return s
}
