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
	// Ledger 发布飞书共享台账（电子表格）。为 nil 或不可用时只生成本地 CSV/MD。
	Ledger  LedgerPublisher
	DataDir string
	// SpamLookbackDays 垃圾清理扫描窗口（默认 7 天）。
	SpamLookbackDays int
	// SpamDryRun=true 时第 2 步只判定不 MOVE（真实邮箱首次运行的安全阀）。
	SpamDryRun bool
	// AccountSyncTimeout 单账户同步墙钟上限；<=0 时用 DefaultAccountSyncTimeout。
	AccountSyncTimeout time.Duration
}

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
	// SpamDryRun>0 表示本轮是预演：这 SpamDryRun 封「本可以移走但没移」，
	// 逐账户列在 SpamDryRunSamples 里。真实邮箱上先看这个再决定是否真移。
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
	// （notifyImportant 里硬编码的 2 天）更老」的邮件数。
	//
	// 为什么必须有它：那些邮件**永远不会被提醒**——不是「这轮没轮到」，是
	// 「不在扫描范围里」。没有这个计数时，报告上的 0 分不清
	// 「这批确实没有重要邮件」和「有 20 封重要的但它们太老了」，
	// 需求 4 看起来就像没实现。和 RemindersUnclassified 是同一类问题的
	// 时间维度版本（见 reminder_diag_test.go 记录的另一次）。
	//
	// 注意这**不是**「发了多少条提醒」，两者不可互相替代。
	RemindersOutOfWindow int `json:"remindersOutOfWindow,omitempty"`
	// 发票候选（步骤 1.5）的三个计数。同样的理由：只报 invoices.Processed
	// 时，「扫了 0 封候选」和「这批里没有发票」长得一模一样，
	// 于是 0 到底是链路没跑、还是跑了但没命中，看报告分不出来。
	// 2026-10-02 就是靠 scanned 这个计数才定位到 24h 窗口把历史邮件全挡在
	// 外面——在那之前 invoices 全是 0，看上去像「邮箱里没有发票」。
	InvoiceCandidatesScanned int `json:"invoiceCandidatesScanned,omitempty"`
	InvoiceCandidatesCreated int `json:"invoiceCandidatesCreated,omitempty"`
	// InvoiceBodyFetchDeferred 是超出单轮 IMAP 预算、顺延到下一轮的候选数。
	InvoiceBodyFetchDeferred int           `json:"invoiceBodyFetchDeferred,omitempty"`
	Invoices                 HarvestResult `json:"invoices"`
	FeishuPushed             int           `json:"feishuPushed"`
	FeishuFailed             int           `json:"feishuFailed"`
	ShareDocCSV              string        `json:"shareDocCsv,omitempty"`
	ShareDocMD               string        `json:"shareDocMd,omitempty"`
	// ShareDocURL 是飞书共享台账链接（未配置飞书时为空，本地 CSV/MD 仍会生成）。
	ShareDocURL string   `json:"shareDocUrl,omitempty"`
	Errors      []string `json:"errors,omitempty"`
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
	scopes := map[[2]string]struct{}{}
	for _, acc := range accounts {
		if acc.UserID != "" {
			scopes[[2]string{acc.UserID, defaultWorkspace(acc.WorkspaceID)}] = struct{}{}
		}
	}
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
	}
	return rep
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
	emails, _, err := p.Store.ListEmailsSince(ctx,
		rep.StartedAt-int64(invoiceCandidateLookbackDays)*86400, invoiceCandidateScanLimit)
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
	// jobs 的下标 → keptIdx 结果的下标，便于回填
	posInKept := make([]int, len(jobs))
	for k, idx := range keptIdx {
		posInKept[idx] = k
	}

	// 并发拉原文：与第 1 步同理，单封信不应拖住整步。
	bodies := p.fetchBodies(ctx, keptIdx, jobs)

	created = 0
	fetchFailed := 0
	for i := range cands {
		c := &cands[i]
		if c.jobAt >= 0 {
			k := posInKept[c.jobAt]
			var b bodyResult
			if k < len(bodies) {
				b = bodies[k]
			}
			if b.err != nil || b.parsed == nil {
				fetchFailed++
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
		rep.AddError("invoice raw body fetch failed for %d message(s) (IMAP 侧问题，本轮未建档)", fetchFailed)
	}
	deferred := len(jobs) - len(keptIdx)
	rep.InvoiceCandidatesScanned = len(emails)
	rep.InvoiceCandidatesCreated = created
	rep.InvoiceBodyFetchDeferred = deferred
	log.Printf("[email/pipeline] step1.5 window=%dd scanned=%d rawBodyFetches=%d deferred=%d fetchFailed=%d autoCreated=%d",
		invoiceCandidateLookbackDays, len(emails), len(keptIdx), deferred, fetchFailed, created)
	if created > 0 {
		log.Printf("[email/pipeline] auto-created %d invoice candidates", created)
	}
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
			raw, err := p.Fetcher.FetchMessageRaw(ctx, e.AccountID, e.UID)
			if err != nil {
				results[k] = bodyResult{err: err}
				log.Printf("[email/pipeline] step1.5 body fetch acct=%s uid=%d FAILED after %s: %v",
					e.AccountID, e.UID, time.Since(t0).Round(time.Millisecond), err)
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
			rep.SpamDryRun++
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
		log.Printf("[email/pipeline] spam dry-run: %d mail(s) would be moved, %d near-miss (未判垃圾但有分，开真实 MOVE 前值得人看一眼)",
			rep.SpamDryRun, countNearMiss(rep.SpamNearMiss))
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

// notifyImportant 对未提醒过的重要邮件派发通知并记录时间。
func (p *Pipeline) notifyImportant(ctx context.Context, rep *PipelineReport) {
	if p.Notifier == nil {
		return
	}
	since := time.Now().AddDate(0, 0, -2).Unix()
	// 窗口之外的高重要度邮件：它们**永远不会被提醒**，但报告上原本看不出来。
	// 先数出来，再决定要不要改窗口——改窗口是产品取舍，可见性不是。
	if n, err := p.Store.CountHighImportanceOutside(ctx, since, 2000); err != nil {
		rep.AddError("reminder out-of-window count: %v", err)
	} else if n > 0 {
		rep.RemindersOutOfWindow = n
		log.Printf("[email/pipeline] %d 封 importance=high 的邮件早于 %d 秒（2 天窗口），"+
			"**永远不会进入重要提醒** —— 报告里 RemindersSent=0 有一部分是这个原因",
			n, time.Now().Unix()-since)
	}
	emails, notified, err := p.Store.ListEmailsSince(ctx, since, 500)
	if err != nil {
		rep.AddError("reminder scan list: %v", err)
		return
	}
	rep.RemindersScanned = len(emails)
	candidates, unclassified := splitReminderCandidates(emails, notified)
	rep.RemindersUnclassified = unclassified
	if unclassified > 0 {
		log.Printf("[email/pipeline] %d/%d 封邮件 importance 为空 —— 未被 AI 分类过，"+
			"不会进入重要提醒（检查 POCKET_KXMEMORY_BASE_URL）", unclassified, len(emails))
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
	if p.Pusher == nil || !p.Pusher.Available() {
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
			inv.InvoiceNo, inv.InvoiceDate, inv.Status, inv.FileName, inv.Subject,
		})
		// 合计口径与 LedgerRows 保持一致：**只统计已下载的**。
		// 2026-10-01 修正（见 ledger.go 的详细说明）：原来无条件累加全部记录，
		// failed 发票若带着错误抽取出的非零金额，会静默把对账总额算高，
		// 而且没有任何地方会提示。两处口径必须一致，否则 CSV 与飞书表格
		// 的「合计」会给出两个不同的数。
		if !((inv.Status == "downloaded" || inv.Status == "filed") && inv.FilePath != "") {
			continue
		}
		if _, seen := centsByCur[cur]; !seen {
			curOrder = append(curOrder, cur)
		}
		centsByCur[cur] += int64(math.Round(amount * 100))
		countByCur[cur]++
	}
	// 每个币种一行合计：币种列（索引 3）带上币种，金额仍在索引 7，
	// 与单币种旧形状的列位保持一致，下游解析不用分两套规则。
	totalRows := make([][]string, 0, len(curOrder)+1)
	for _, cur := range curOrder {
		cells := []string{"合计", "", "", "", "", "", "", "", ""}
		if len(curOrder) > 1 {
			cells[3] = cur
		}
		cells[7] = fmt.Sprintf("%.2f", float64(centsByCur[cur])/100)
		totalRows = append(totalRows, cells)
	}
	// 空清单也必须有合计行（需求：「整理一个列表…并汇总金额」）：
	// 只有表头 + 一行 0 合计，下游按行数算范围时才不用特判。与 LedgerRows 同理。
	if len(curOrder) == 0 {
		totalRows = append(totalRows, []string{"合计", "", "", "", "", "", "", "0.00", ""})
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
	csv.WriteString("费用类型,对方单位,金额,币种,发票号,日期,状态,文件名,来源邮件\n")
	for _, r := range append(append([][]string{}, rows...), totalRows...) {
		cells := make([]string, len(r))
		for i, c := range r {
			cells[i] = csvSafeCell(c)
		}
		csv.WriteString(strings.Join(cells, ",") + "\n")
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
	if err := os.WriteFile(csvPath, append([]byte(utf8BOM), []byte(csv.String())...), 0o600); err != nil {
		return "", "", err
	}

	// 抬头必须说清是哪种货币：多币种时逐币种列出，绝不给一个无币种的裸数字。
	amountSummary := fmt.Sprintf("%.2f", total)
	if len(sumByCur) > 0 {
		amountSummary = strings.Join(sumByCur, " + ")
	}
	md := &strings.Builder{}
	md.WriteString("# 发票汇总\n\n")
	md.WriteString(fmt.Sprintf("生成时间：%s · 共 %d 张 · 合计金额 **%s**\n\n",
		time.Now().Format("2006-01-02 15:04"), len(invoices), amountSummary))
	md.WriteString("| 费用类型 | 对方单位 | 金额 | 发票号 | 日期 | 状态 | 文件 |\n")
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
