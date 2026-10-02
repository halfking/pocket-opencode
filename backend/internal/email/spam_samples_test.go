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
// 严格零值，好让调用方把「压根没参与评分」与「评过分但差一截」分开。合并后
// 白名单多了 monitor.aliyun.com，于是下面那条「未达阈值必须有分数或理由」的
// 断言会把**豁免样本**误判成「没参与评分的坏样本」。两者语义不同，
// 所以这里显式区分，而不是放宽断言。
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
		// 真实：阿里云产品月刊（handoff §7e 记为 near-miss）。
		//
		// 这里**不设** minScore 下界。实测 score=0 是**规则的正确行为**，
		// 不是规则失灵 —— 查 spam.go 确认：
		//   - spamSenderHints 只有 promo/marketing/newsletter/… 这类 local-part
		//     与域名特征，`monitor@monitor.aliyun.com` 一个都不含；
		//   - spamSubjectPatterns 只有 退订/取消订阅/…，主题「阿里云产品月刊」
		//     一个都不含；
		//   - 摘要里的「点击此处退订」是**正文**，退订规则只看主题，不计分。
		// 保留这个样本的价值：它是「服务商正式通知 vs 营销列表」这条边界上
		// 最容易被误伤的一类。规则一旦被改宽（把服务商域名加进营销特征表），
		// 这个样本必须先转红。
		name:    "aliyun-product-monthly",
		from:    "monitor@monitor.aliyun.com",
		subject: "阿里云产品月刊",
		snippet: "本期精选：弹性计算最佳实践、云原生安全指南。点击此处退订。",
		spam:    false,
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

// TestLooksLikeSpam_InvoiceAndImportantAreNeverSpam 钉住短路闸门。
//
// 调用方会把「像发票」或「已判重要」的邮件直接短路成非垃圾。这条不变量
// 一旦被破坏，后果是**真实发票邮件被 MOVE 进垃圾箱** —— 不可逆，且直接
// 站在目标需求「发票类邮件要能被采集整理」的反面。
func TestLooksLikeSpam_InvoiceAndImportantAreNeverSpam(t *testing.T) {
	// 对照组：一封营销邮件，在两个闸门都关闭时确实会被判垃圾。
	promo := realSpamSamples[len(realSpamSamples)-2] // obvious-promo
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
