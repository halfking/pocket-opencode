package email

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/kxmemory"
)

type RawClassifyResult struct {
	EmailID    string
	Category   string
	Importance string
	Summary    string
	Action     string
	// Reason 是 AI 判定该分类/重要度的依据。
	//
	// 2026-10-02 补。这个字段原先**根本不存在**：kxmemory 的分类响应契约里
	// 带 action_reason（docs/2026-07-02-kxmemory-api-contract.md），
	// client.Result.ActionReason 也早就解出来了，但 classifyRun 在构造
	// RawClassifyResult 时只搬了 Category/Importance/Summary/SuggestedAction，
	// action_reason 在**这一跳**被丢弃 —— 于是后面的
	// SetClassificationScoped（不写这一列）拿不到它，分类器给出的判定依据
	// 永远进不了库。
	//
	// 实测 blast radius：真库 122 封邮件 action_reason **全为空**，而同一响应里
	// 的 ai_summary **122 封全有值**。两者来自同一个分类结果，这排除了
	// 「分类器没返回」的解释——它返回了，只是被这里扔掉。
	// 后果：提醒卡片无法回答「为什么这封被判为重要」，用户无从判断该不该点开。
	Reason string
}

func BuildClassifyWrites(in []RawClassifyResult) []RawClassifyResult {
	out := make([]RawClassifyResult, 0, len(in))
	for _, r := range in {
		r.Category = NormalizeCategory(r.Category)
		// Importance 也要归一化。修复前这里只处理了 Category：kxmemory
		// 返回什么就原样落库，而下游 splitReminderCandidates 用
		// `case "high"` 精确匹配（pipeline.go:727）——上游返回 "High" 时
		// 重要邮件会**静默漏提醒**，报告里 remindersSent=0 也不报错。
		// DB 层没有 CHECK 约束兜底（实测 pg_constraint 对 emails 返回 0 行），
		// 脏值会一直留着。
		r.Importance = NormalizeImportance(r.Importance)
		if r.EmailID == "" || r.Category == "" {
			continue
		}
		out = append(out, r)
	}
	return out
}

// NormalizeImportance 把上游（kxmemory / 规则）返回的重要度归一成
// high / medium / low 三档之一；无法识别时返回空串。
//
// 空串的语义是「未分类」——splitReminderCandidates 会把它计入
// unclassified，让报告里的 0 变得可解释；这比落一个匹配不上的脏值好得多
// （脏值既不提醒也不计数，是最坏情况）。
//
// 覆盖上游常见偏差：大小写（"High"）、中文（"高"）、数字档位（"1"）。
func NormalizeImportance(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	switch s {
	case "":
		return ""
	case "high", "h", "1", "高", "重要", "紧急", "urgent", "critical":
		return "high"
	case "medium", "med", "m", "2", "中", "普通", "normal":
		return "medium"
	case "low", "l", "3", "低", "次要", "minor":
		return "low"
	}
	return ""
}

func ShouldProcessAfterFetch(syncedAccounts, newEmails int) bool {
	_ = newEmails
	return syncedAccounts > 0
}

// NeverSyncedAccounts 挑出「enabled 但**从未成功同步过一次**」的账户地址。
//
// 2026-10-02 补。这是个真实的静默失效：调度器每分钟对每个到期账户跑一次
// Sync，失败的分支是
//
//	log.Printf("[email/scheduler] sync %s failed: %v", accountID, err)
//	return
//
// 也就是说一个**从上线起就一次都没连上过**的邮箱，只会在日志里每分钟刷一行，
// 然后被下一行刷走。没有任何计数器、没有任何报告字段、诊断页也不看这个。
// 结果就是需求 1（每天定时或手工收信）对它等于完全没实现，而界面上
// 「5 个邮箱已配置、收信正常」与「其中 1 个从来没通过认证」长得一模一样。
//
// 真实库 2026-10-02 读到的实例：5 个 enabled 账户中
// feikemanager1@163.com 的 last_synced_uid=0、last_synced_at=0、
// 邮件数 0，而同库另外 4 个都在正常推进 UID。
//
// 单独抽成纯函数是为了能脱离数据库验证——这个判据本身极易写反
// （把「同步过但很旧」和「从没同步过」混为一谈，而前者其实是正常的）。
func NeverSyncedAccounts(accounts []Account) []string {
	var out []string
	for _, a := range accounts {
		if !a.Enabled {
			continue
		}
		if a.LastSyncedAt > 0 {
			continue
		}
		out = append(out, a.EmailAddress)
	}
	return out
}

// neverSyncedWarning 组装「开了却从没同步成功过」的告警文案。
//
// 抽成纯函数是为了能断言文案本身，而不只断言「有没有打日志」——
// 这类告警的价值几乎全在措辞上：只说「有 N 个邮箱没同步」而不说去哪儿看，
// 排查入口是缺失的，而真正的原因（认证拒绝 / 地址错 / 端口不通）只出现在
// 调度器日志里。
func neverSyncedWarning(dead, total int, addrs string) string {
	return fmt.Sprintf("%d/%d 个已启用邮箱**从未成功同步过**（last_synced_at=0）：%s。"+
		"它们的凭据/服务器配置很可能不可用，需求 1 的收信对这几个账户完全没生效；"+
		"同期其它账户正常。排查顺序：先看 %s 日志里的「sync <account> failed」那一行。",
		dead, total, addrs, logPrefix)
}

// ClassifySkipReason 说明「同步成功了但分类根本没跑」的原因；返回空串表示可以继续。
//
// 2026-10-02 补。原先 scheduler 的写法是
//
//	if s.kxmem == nil || userID == "" || !ShouldProcessAfterFetch(1, n) {
//	    return
//	}
//
// 三个条件里任意一个成立就直接 return，**不记日志、不报错、不计数**。
// 而 `POCKET_KXMEMORY_BASE_URL` 未配置时 s.kxmem 恒为 nil，也就是
// **每次同步之后自动分类都被静默跳过**——邮件进来了、importance 永远空、
// 需求 4 永远不提醒，报告上却只有 RemindersUnclassified 一个数字。
//
// 特别值得警惕的是它与手动路径的**不对称**：HTTP 端点
// （/api/emails/classify）有一条 LLM 网关兜底（server 包的
// classifyViaGateway），kxmemory 没配也能分类；而 Scheduler 在
// internal/email 包里，拿不到 Server 的网关，只能在 kxmem==nil 时放弃。
// 于是同一次部署里「手动触发能分类、每天自动跑不分类」。
//
// 诊断页也帮不上忙：integration_status.go 会分别报
// kxmemory disabled 与 llm-gateway enabled，两条**单看都准确**，
// 合起来却让人以为自动分类有兜底。
//
// 这里只把原因**说出来**，不替产品决定要不要把网关兜底接进 Scheduler
// （那是跨包的设计改动，需要单独拍板）。但至少它不再是无声的。
func ClassifySkipReason(kxConfigured bool, userID string) string {
	switch {
	case !kxConfigured:
		return "kxmemory 未配置（POCKET_KXMEMORY_BASE_URL 为空），" +
			"自动分类被跳过；手动 /api/emails/classify 有 LLM 网关兜底，定时路径没有"
	case userID == "":
		return "账户没有 user 归属，自动分类被跳过"
	}
	return ""
}

// ClassifyUnclassified 委托 kxmemory 逐封处理未归类邮件。IMAP 在 Fetcher /
// Scheduler 里跑，WebView 不碰邮箱协议。
//
// 第二个返回值是**逐条失败**的汇总，不是「整个函数失败」。
//
// 2026-10-02 补。原来三处失败分支（kx 调用出错/返回空、写库出错、
// BuildClassifyWrites 产出空）全是裸 `continue`：函数照常返回 (n, nil)，
// 而 n 只是**成功数**。于是
//
//	「这批没有待分类邮件」      → (0, nil)
//	「20 封全部分类失败」       → (0, nil)   ← 与上一行完全相同
//
// 三个调用点又都写成 `if _, err := ...`，把成功数也丢了。两个调用点
// （scheduler.go 的定时分类、server_assistant.go 的收信后分类）都只在
// `err != nil` 时打日志，于是**分类整体失效在日志和报告里都不留任何痕迹**。
//
// 后果不是理论上的：importance 写不进去 → splitReminderCandidates 把它们
// 计入 unclassified → 需求 4 永远不提醒，而报告上只有
// RemindersUnclassified 一个数字，读起来和「还没轮到分类」一模一样。
// 真实库 2026-10-02 观测到的就是当天 7 封里 5 封未分类、其中 3 封已取回
// 但从未被分类过。排查时最自然的错误结论是「分类器还没跑到」——
// 真去检查 kxmemory 配置，而真实原因可能在上游返回或写库。
//
// 改成：任何一条失败都进日志，并汇总进返回的 error。调用点无需改动签名，
// 它们现有的 `err != nil` 分支会把这件事打进日志。
func ClassifyUnclassified(ctx context.Context, store *Store, kx kxmemory.Client, userID, workspaceID string, limit int) (int, error) {
	if store == nil || kx == nil || userID == "" {
		return 0, nil
	}
	items, err := store.ListUnclassifiedScoped(ctx, userID, workspaceID, limit)
	if err != nil {
		return 0, err
	}
	n := 0
	var failed int
	var firstErr error
	noteFailure := func(id string, cause error) {
		failed++
		if firstErr == nil {
			firstErr = cause
		}
		log.Printf("[email/classify] email=%s 分类失败: %v", id, cause)
	}
	for _, it := range items {
		callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		resp, cerr := kx.ClassifyEmails(callCtx, kxmemory.ClassifyEmailsRequest{
			Emails: []kxmemory.EmailForClassification{{
				EmailID: it.ID, Subject: it.Subject, Snippet: it.Snippet,
				FromAddress: it.FromAddress, FromName: it.FromName,
			}},
		})
		if cerr != nil || resp == nil || len(resp.Results) == 0 {
			cause := cerr
			if cause == nil {
				cause = fmt.Errorf("kxmemory 返回空结果（resp=%v results=%d）", resp, resultCount(resp))
			}
			cancel()
			noteFailure(it.ID, cause)
			continue
		}
		row := resp.Results[0]
		writes := BuildClassifyWrites([]RawClassifyResult{{
			EmailID: it.ID, Category: row.Category, Importance: row.Importance,
			Summary: row.Summary, Action: row.SuggestedAction,
			// 必须一起搬：漏了这一项 action_reason 就止步于此，
			// 后面无论调哪个写库方法都补不回来（真库 122/122 为空的成因）。
			Reason: row.ActionReason,
		}})
		if len(writes) == 0 {
			cancel()
			noteFailure(it.ID, fmt.Errorf(
				"分类结果不可用：EmailID=%q category=%q importance=%q（归一化后 category 为空）",
				row.EmailID, row.Category, row.Importance))
			continue
		}
		w := writes[0]
		// 走带 reason 的写库方法。用 SetClassificationScoped 会静默丢列——
		// 那个方法的签名里就没有它，编译通过、运行不报错、库里永远是空。
		err := store.SetClassificationWithReasonScoped(callCtx, w.EmailID, userID, workspaceID,
			w.Category, w.Importance, w.Summary, w.Action, w.Reason)
		cancel()
		if err != nil {
			noteFailure(it.ID, fmt.Errorf("写库失败: %w", err))
			continue
		}
		n++
	}
	if failed > 0 {
		return n, fmt.Errorf("%d/%d 封分类失败（成功 %d）；首条错误: %w",
			failed, len(items), n, firstErr)
	}
	return n, nil
}

// resultCount 只为让「kxmemory 返回空结果」这条日志能区分「resp 为 nil」
// 与「resp 非 nil 但 Results 为空」——两者是不同的上游故障。
func resultCount(resp *kxmemory.ClassifyEmailsResponse) int {
	if resp == nil {
		return 0
	}
	return len(resp.Results)
}
