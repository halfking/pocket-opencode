package email

// spam_samples_test.go — 清垃圾规则在**真实信箱语料**上的离线回归。
//
// 与 spam_realdata_test.go 的分工（两者都要，别只留一个）：
//
//   - spam_realdata_test.go：连真库（需 POCKET_REAL_MAIL_DSN），把**全部**
//     真实邮件灌进 LooksLikeSpam 报告命中率。适合「现在真实信箱里到底
//     有没有广告」这种一次性诊断，**不能进 CI**（依赖生产 schema）。
//   - 本文件：把 7 封真实样本**固化**成期望值，零外部依赖，**每次提交都跑**。
//     规则一旦被改宽（加了服务商域名进营销特征表）或改窄（阈值提高、
//     弱词表缩水），这里立刻转红并指出是哪一封。
//
// 为什么必须有这层（2026-10-01 真机数据）：
//
//	pipeline 预演连续多轮 spamMoved=0、near-miss=0。单看这个 0 分不出三种
//	完全不同的情况：
//	  (a) 真实信箱确实没有广告；
//	  (b) 规则在真实数据上形同虚设；
//	  (c) near-miss 可观测性坏了，差一点的邮件根本没被算出来。
//	夹具邮件是按规则设计的，天然会命中，证明不了规则在真实语料上的行为。
//
// 样本来自 5 个真实账户（huangxutao@kxpms.cn / 56551681@qq.com /
// feikemanager@163.com / feikemanager1@163.com / kimmy.huang@163.com）
// 共 155 封的抽样，**已合成化**：不含真实发票号、真实金额、真实内部地址。
import (
	"strings"
	"testing"
)

// senderIsExempt 复刻 spam.go 里的域名白名单判定：命中就是**不评分**的零值路径。
//
// 合并说明：TestLooksLikeSpam_ExposesScoreBelowThreshold 明确要求豁免/短路返回
// 严格零值，好让调用方把「压根没参与评分」与「评过分但差一截」分开。下面那条
// 「未达阈值必须有分数或理由」的断言会把**豁免样本**误判成「没参与评分的坏样本」。
// 两者语义不同，所以这里显式区分，而不是放宽断言。
//
// （此说明最初写成「合并后白名单多了 monitor.aliyun.com」——那是 2026-10-02
// 一次被推翻的中间口径。白名单现已移除该条目，见下方
// TestLooksLikeSpam_AliyunHasNoDomainBackdoor。别把这句话当现状读。）
func senderIsExempt(from string) bool {
	fromLower := strings.ToLower(strings.TrimSpace(from))
	i := strings.LastIndex(fromLower, "@")
	if i < 0 {
		return false
	}
	domain := fromLower[i+1:]
	for _, w := range spamDomainWhitelist {
		if strings.Contains(domain, w) {
			return true
		}
	}
	return false
}

type realSpamSample struct {
	name    string
	from    string
	subject string
	snippet string
	// spam 期望该样本被判为垃圾。
	spam bool
	// minScore 是分数下界：规则往正确方向走时分数不该更低。
	// 用来抓「规则被削弱」这种静默回归。
	minScore int
}

var realSpamSamples = []realSpamSample{
	{
		// 真实高频：GitHub 通知（库内 74 封）。工作通知，绝不能当垃圾。
		name:    "github-action-notification",
		from:    "notifications@github.com",
		subject: "[halfking/ai-native-gateway-core] Run failed: Security Scan - main (12db02c)",
		snippet: "Secret Scanning failed (3 annotations). Workflow run finished. View results: https://github.com/.../actions/runs/36758295610",
		spam:    false,
	},
	{
		// 真实高频：API 充值通知（库内 69 封）。个人交易通知，不能当垃圾。
		name:    "api-topup-receipt",
		from:    "69551681@qq.com",
		subject: "[API VibeCoding] 余额充值成功",
		snippet: "您的余额已充值成功，当前余额 39,061.11，账户状态正常。",
		spam:    false,
	},
	{
		// 真实：额度告急 + 充值链接（handoff §7 记为「字面 HTML 上屏」那类）。
		// 带营销味但也是真实业务通知，判成垃圾会丢掉用户关心的额度信息。
		name:    "quota-low-alert",
		from:    "u1@syapi.cn",
		subject: "您的额度即将用尽",
		snippet: "当前剩余额度为 ¥0.002116，为不影响您的使用，请及时充值。充值链接：https://u.syapi.cn/console/topup",
		spam:    false,
	},
	{
		// 真实：银行信用卡日汇总。工作邮件，绝不能当垃圾。
		name:    "bank-card-daily-summary",
		from:    "ccsvc@message.cmbchina.com",
		subject: "每日信用卡使用汇总",
		snippet: "您尾号1234的信用卡昨日使用情况如下，可用额度 39,061.11，积分余额 46,472。",
		spam:    false,
	},
	{
		// 真实：阿里云产品月刊（handoff §7e 曾记为 near-miss）。
		//
		// 2026-10-02 **人工拍板：判为垃圾**。取「退订特征命中即 100 分」那条
		// 规则，而不是给这个域开后门。这条决策当天被推翻过两次——
		// 中间一版以「保留域名豁免」落地过（7a77ae49），最终定稿为移除豁免。
		//
		// 因果订正过两次，都值得留着：
		//  1. 更早的注释写「退订规则只看主题，摘要里的『点击此处退订』不计分」
		//     ——**那是错的**。spam.go 的退订段「命中即 100」明确
		//     `strings.Contains(subject, p) || strings.Contains(snippet, p)`，
		//     主题**和**摘要都查，这封信本该拿满 100 分。
		//  2. 改口成「score=0 是白名单豁免的**正确结果**」——那也不对。白名单判定
		//     在评分**之前**就 `return SpamVerdict{}`，所以 score=0 的真正含义是
		//     **压根没参与评分**，不是「评了分判定为非垃圾」。判据（spam:false）
		//     是绿的，实现却是空转——用豁免掩盖规则冲突，会把冲突变成沉默。
		//
		// **代价（明确接受，非实现缺陷）**：阿里云服务月刊会被判垃圾，用户
		// 收不到。真实服务通知 + 正文带退订链接这一类兜不住；误伤由
		// invoiceCandidate / important 短路和出票/账单类域名白名单兜住。
		// 若将来要收回这条规则，判据就是本样本转红。
		name:     "aliyun-product-monthly",
		from:     "monitor@monitor.aliyun.com",
		subject:  "阿里云产品月刊",
		snippet:  "本期精选：弹性计算最佳实践、云原生安全指南。点击此处退订。",
		spam:     true,
		minScore: 100,
	},
	{
		// 强营销：促销 + 优惠 + 限时 + 退订，四类弱信号密集。
		// 这一封必须被判为垃圾 —— 它存在的意义是钉住「弱词按数量分级」
		// 这条规则真的在起作用（1 个弱词不给分、按数量升档）。
		name:    "obvious-promo",
		from:    "noreply@promo.example.com",
		subject: "限时优惠！全场促销，折扣好价，点击立减",
		snippet: "今日精选好文，限时优惠，折扣好价，点击立减。回复 TD 退订。",
		spam:    true,
	},
	{
		// 强营销词直击（spamStrongWords 路径）。
		name:    "strong-spam-word",
		from:    "sales@spam.example.com",
		subject: "中奖通知：恭喜您获得现金大奖",
		snippet: "点击链接立即领取大奖，名额有限。",
		spam:    true,
	},
}

func TestLooksLikeSpam_OnRealMailboxSamples(t *testing.T) {
	for _, s := range realSpamSamples {
		t.Run(s.name, func(t *testing.T) {
			// 发票候选 / 已判重要 的邮件由调用方短路传入，永不判垃圾。
			// 真实样本里都不是这两类，所以传 false。
			got := LooksLikeSpam(s.from, s.subject, s.snippet, false, false, 0) /* senderVolume=0「未统计」*/
			if got.Spam != s.spam {
				t.Fatalf("Spam=%v, want %v (score=%d why=%q)", got.Spam, s.spam, got.Score, got.Why)
			}
			// 未达阈值的样本也必须带分数或理由，否则调用方分不出
			// 「压根没参与评分（豁免）」和「评过分但差一截」——预演报告就
			// 退化成命中/未命中两态，阈值没法用真实数据校准。
			//
			// 但**白名单豁免的样本是合法的全零**：它按设计就不参与评分
			// （见 senderIsExempt 与 TestLooksLikeSpam_ExposesScoreBelowThreshold）。
			// 只有「既非豁免、又交出全零」才是坏样本。
			if !got.Spam && got.Score == 0 && got.Why == "" && !senderIsExempt(s.from) {
				t.Fatalf("unclassified sample returned a zero verdict: nothing to calibrate against")
			}
			if got.Score < s.minScore {
				t.Fatalf("score=%d, want >= %d (rule lost signal; why=%q)", got.Score, s.minScore, got.Why)
			}
		})
	}
}

// TestLooksLikeSpam_AliyunHasNoDomainBackdoor 钉住 2026-10-02 的最终拍板口径：
// **monitor.aliyun.com 没有域名后门**，它必须走和其他地址完全一样的评分路径。
//
// 为什么需要这条独立断言，而不只靠 realSpamSamples 里那条样本：
// 样本表只钉住「这封信判垃圾」。但它曾经以 spam:false 通过过一次——
// 靠的是「把 monitor.aliyun.com 加进 spamDomainWhitelist」。而白名单判定在
// LooksLikeSpam 里位于评分**之前**（直接 `return SpamVerdict{}`），于是
// **判据是绿的，实现却是空转**：退订规则一次都没执行过。
// 「判成非垃圾」和「压根没判」在测试里长得一模一样，这条断言就是用来
// 区分它们的。
//
// 钉住的不变式：对同一个 (subject, snippet)，**任何域名**都必须交出
// 逐字相同的 verdict。若将来有人再把这个域加回白名单来「消除误伤」，
// 本测试会先红，并在错误信息里指出「仍在域名白名单里被提前 return 短路了」。
//
// 负控实测（2026-10-02）：把 "monitor.aliyun.com" 加回 spamDomainWhitelist，
// 本测试如期转红，错误信息正是上面那句；移除后转绿。判据有承重能力。
//
// 若将来要改口径（重新豁免该域），这正是需要重新拍板的时刻，不该被悄悄改掉。
func TestLooksLikeSpam_AliyunHasNoDomainBackdoor(t *testing.T) {
	// 与 realSpamSamples 里的 aliyun-product-monthly 逐字相同的邮件。
	const (
		subject = "阿里云产品月刊"
		snippet = "本期精选：弹性计算最佳实践、云原生安全指南。点击此处退订。"
	)

	// 至少两个对照域名，覆盖两种情形：
	//   - noreply@aliyun.com：与 monitor.aliyun.com 同家，但**不在**白名单
	//     （判据是 strings.Contains(domain, 白名单条目)）；
	//   - 完全无关的域名：排除「阿里云整域被放行」这种更粗的后门。
	controls := []string{"noreply@aliyun.com", "newsletter@example.com"}
	for _, from := range controls {
		got := LooksLikeSpam(from, subject, snippet, false, false, 0)
		if !got.Spam || got.Score != 100 || got.Why == "" {
			t.Fatalf("对照组 %s 应判垃圾 100 分且带 Why（退订规则确实查摘要且命中即 100），"+
				"实际 Spam=%v score=%d why=%q", from, got.Spam, got.Score, got.Why)
		}
	}

	// 实验组：monitor.aliyun.com。必须与对照组**逐字相同**。
	// 逐字比较三个字段而不是只比 Spam，是关键：白名单短路返回的是
	// score=0/why="" 的零值，只比 Spam 会漏掉「压根没评分」这一路。
	got := LooksLikeSpam("monitor@monitor.aliyun.com", subject, snippet, false, false, 0)
	for _, c := range controls {
		want := LooksLikeSpam(c, subject, snippet, false, false, 0)
		if got.Spam != want.Spam || got.Score != want.Score || got.Why != want.Why {
			t.Fatalf("monitor.aliyun.com 必须与普通地址 %s 判定逐字相同"+
				"（2026-10-02 拍板：不给该域开后门）；"+
				"实际 spam=%v score=%d why=%q，对照 spam=%v score=%d why=%q。"+
				"若 got 是 score=0/why=\"\"，说明它仍在域名白名单里被提前 return 短路了，"+
				"退订规则从未执行——判据绿而实现空转，正是本测试要拦的东西。",
				c, got.Spam, got.Score, got.Why, want.Spam, want.Score, want.Why)
		}
	}
}

// TestLooksLikeSpam_InvoiceAndImportantAreNeverSpam 钉住短路闸门。
//
// 调用方会把「像发票」或「已判重要」的邮件直接短路成非垃圾。这条不变量
// 一旦被破坏，后果是**真实发票邮件被 MOVE 进垃圾箱** —— 不可逆，且直接
// 站在目标需求「发票类邮件要能被采集整理」的反面。
func TestLooksLikeSpam_InvoiceAndImportantAreNeverSpam(t *testing.T) {
	// 对照组：一封营销邮件，在两个闸门都关闭时确实会被判垃圾。
	promo := realSpamSamples[len(realSpamSamples)-2]                                 // obvious-promo
	base := LooksLikeSpam(promo.from, promo.subject, promo.snippet, false, false, 0) /* senderVolume=0「未统计」*/
	if !base.Spam {
		t.Fatalf("control failed: promo sample should be spam with both gates open, got score=%d why=%q", base.Score, base.Why)
	}
	for _, tc := range []struct {
		name         string
		invoice, imp bool
	}{
		{"invoice candidate", true, false},
		{"important", false, true},
		{"both", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := LooksLikeSpam(promo.from, promo.subject, promo.snippet, tc.invoice, tc.imp, 0) /* senderVolume=0「未统计」*/
			if got.Spam {
				t.Fatalf("short-circuit failed: %s mail was classified as spam (score=%d)", tc.name, got.Score)
			}
		})
	}
}

// TestRealSamples_NeverFlagWorkMail 单独强调最要紧的不变量：
// **工作邮件永远不能被判为垃圾**。误判的代价（丢掉真实业务邮件）远高于
// 漏判，而且 MOVE 是对真实邮箱的写操作，误判不可逆。
func TestRealSamples_NeverFlagWorkMail(t *testing.T) {
	protected := []string{
		"notifications@github.com",
		"69551681@qq.com",
		"ccsvc@message.cmbchina.com",
		"noreply@tm.openai.com",
		"no-reply@amazonaws.com",
		"westlakebusiness@apple.com",
	}
	for _, s := range realSpamSamples {
		v := LooksLikeSpam(s.from, s.subject, s.snippet, false, false, 0) /* senderVolume=0「未统计」*/
		for _, p := range protected {
			if s.from == p && v.Spam {
				t.Fatalf("work mail from %s classified as spam (score=%d why=%q) - MOVE is irreversible on a real mailbox", p, v.Score, v.Why)
			}
		}
	}
}
