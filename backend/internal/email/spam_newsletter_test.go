package email

// spam_newsletter_test.go — 技术资讯/云厂商 newsletter 必须判为垃圾。
//
// 背景（2026-10-01）：真实信箱里 19 封 near-miss（score>0 但没过 100 阈值）
// **全部**是这一类——InfoQ 13 封、阿里云 newsletter 5 封、ecloudrover 1 封，
// 当时全部 score=30（只命中发件人特征），差 70 分。它们不是"促销广告"，
// 而是技术资讯推送：对本系统（收发票 + 看重要邮件）是纯噪声，但对用户是
// 可读的资讯——所以也不该用促销词去套。
//
// 两个真实缺陷：
//
//  1. 退订特征只查**主题**。真实 newsletter 的退订链接几乎总在 HTML 摘要里，
//     主题只是文章标题。后果：「阿里云云安全中心周报」score=0，
//     「本周技术精选」score=0——整类漏判，且 Why 里看不出差在哪。
//
//  2. 退订命中只给 70 分，要和别的词凑够 100。后果：即使摘要里有退订，
//     「阿里云云安全中心周报」也只到 70，永远差 30。而带退订头的邮件
//     **按定义**就是可退订的营销列表，命中即应判定。
//
// 负控对照：
//   - 负控A（退订只查主题）  -> TestLooksLikeSpam_Newsletter_* 的退订类用例转红
//   - 负控B（退订只给 70 分）-> 「阿里云云安全中心周报」退回 70 分转红
//
// 防误伤由既有的 TestLooksLikeSpam_DoesNotFireOnWorkMail 覆盖（发票/账单/
// CI/生产变更/白名单域），本文件不重复，但会跑一遍确认新词没抬高基线。

import "testing"

// 真实信箱里出现过的形态。from/subject 仿真实值，snippet 取 newsletter
// HTML 摘要的典型开头（含退订尾巴）。
func TestLooksLikeSpam_NewsletterWithUnsubscribeInSnippet(t *testing.T) {
	cases := []struct{ name, from, subject, snippet string }{
		{
			"阿里云周报",
			"no_reply@aliyun.com",
			"【阿里云】云安全中心周报",
			"本周安全事件汇总。如不想再接收，请点击此处退订",
		},
		{
			"InfoQ 每日精选",
			"noreply@infoq.cn",
			"InfoQ 每日精选 | 架构与云原生实践",
			"今日精选文章，点击退订",
		},
		{
			"退订链接只在摘要里",
			"news@tech-media.example.com",
			"本周技术精选",
			"如果不想再收到本邮件，请点击这里退订",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := LooksLikeSpam(c.from, c.subject, c.snippet, false, false)
			if !v.Spam {
				t.Fatalf("技术资讯推送未判为垃圾: from=%q subject=%q score=%d why=%q",
					c.from, c.subject, v.Score, v.Why)
			}
			if v.Why == "" {
				t.Fatalf("判为垃圾但 Why 为空，预演报告无法给人看理由: %q", c.from)
			}
		})
	}
}

// 纯资讯类：既没有促销词，也未必带退订头（「周报」「精选」等）。
// 这类靠弱信号密度过线，不依赖退订。
func TestLooksLikeSpam_PureDigestWithoutUnsubscribe(t *testing.T) {
	v := LooksLikeSpam("digest@tech-media.example.com",
		"技术周报：本周精选文章", "本周精选文章与行业动态汇总", false, false)
	if !v.Spam {
		t.Fatalf("纯资讯周报未判为垃圾: score=%d why=%q", v.Score, v.Why)
	}
}

// 防误伤：同样含「周报」「精选」措辞的**工作邮件**不能被判掉。
// 新增资讯类弱词抬高了命中数，这一组是它的直接代价，必须钉住。
func TestLooksLikeSpam_DigestWordingOnWorkMail(t *testing.T) {
	cases := []struct{ name, from, subject, snippet string }{
		{"项目周报", "pm@kxpms.cn", "支付网关项目周报（本周进展）", "本周进展：联调完成，下周提测"},
		{"技术分享会", "tech@kxpms.cn", "内部技术分享会：本周精选议题", "议题含支付网关与风控设计"},
		{"重要周报需处理", "ops@kxpms.cn", "生产环境周报：本周精选待办", "请确认本周变更窗口"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if v := LooksLikeSpam(c.from, c.subject, c.snippet, false, false); v.Spam {
				t.Fatalf("工作邮件被误判为垃圾: from=%q subject=%q score=%d why=%s",
					c.from, c.subject, v.Score, v.Why)
			}
		})
	}
}
