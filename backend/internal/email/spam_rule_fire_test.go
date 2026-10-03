package email

// spam_rule_fire_test.go — 钉死「清垃圾规则在真实形态的垃圾邮件上确实会触发」。
//
// 背景：2026-10-01 凌晨在 6 个真实账户（443 封邮件）上跑预演，报告
// 「0 mail(s) would be moved」。单看这个数字无法区分「信箱里确实没广告」和
// 「规则形同虚设」。本测试用真实世界里最典型的几类垃圾邮件做判据：规则必须
// 判它们为垃圾，且必须给出**可读的 Why**（预演报告要给人看理由）。
//
// 另外固定一个已知的可诊断性事实：LooksLikeSpam 在未达阈值时返回**零值**
// SpamVerdict，Score/Why 被丢弃。所以调用方拿不到「差一点就判垃圾」的信息，
// 阈值也就无法基于真实数据调优——这是已知的可诊断性缺口，不是判定 bug。

import "testing"

func TestLooksLikeSpam_FiresOnRealisticSpam(t *testing.T) {
	cases := []struct {
		name, from, subject, snippet string
	}{
		{"中奖", "noreply@some-promo.cn", "恭喜您获得大奖！点击领取", "恭喜您获得大奖！点击领取"},
		{"限时抢购", "promo@shop.example.com", "限时抢购｜全场 1 折起", "限时抢购"},
		{"英文中奖", "winner@lottery.example.com", "Congratulations! You have won $1000", "claim your prize now"},
		{"退订", "news@adhouse.cn", "今日精选好文（回复 退订 退订本邮件）", "退订"},
		{"促销弱词×2", "vip@brand.cn", "会员日 专属福利 限时特惠", "促销 折扣 优惠"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := LooksLikeSpam(c.from, c.subject, c.snippet, false, false, 1)
			if !v.Spam {
				t.Fatalf("未判为垃圾: from=%q subject=%q snippet=%q（score=%d）",
					c.from, c.subject, c.snippet, v.Score)
			}
			if v.Why == "" {
				t.Fatalf("判为垃圾但没有理由，预演报告无法给人看：from=%q", c.from)
			}
			if v.Score < 100 {
				t.Fatalf("Spam=true 但 score=%d < 100，判定与阈值不自洽：%s", v.Score, v.Why)
			}
		})
	}
}

func TestLooksLikeSpam_DoesNotFireOnWorkMail(t *testing.T) {
	cases := []struct{ name, from, subject, snippet string }{
		{"发票", "noreply@service.dzfp.com", "杭州创客家投资管理有限公司开具的发票", "发票号码 26332000008261110741"},
		{"生产变更", "ops@kxpms.cn", "生产环境变更通知（请确认）", "请确认变更窗口"},
		{"CI", "noreply@github.com", "[ai-native-gateway-core] Run failed: Security Scan - main", "Run failed"},
		{"账单", "billing@alipay.com", "支付宝账单", "本期应还金额"},
		{"白名单域", "admin@exmail.qq.com", "促销 折扣 优惠 秒杀", "限时抢购 免费领取"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if v := LooksLikeSpam(c.from, c.subject, c.snippet, false, false, 1); v.Spam {
				t.Fatalf("误判为垃圾: from=%q subject=%q why=%s", c.from, c.subject, v.Why)
			}
		})
	}
}

// TestLooksLikeSpam_ExposesScoreBelowThreshold 固定「未达阈值时分数仍然可见」。
//
// 2026-10-01 之前这里断言的是相反的事（Score 被清零），那是记录一个**可诊断性
// 缺口**：预演报告只有命中/未命中两态，无法用真实数据校准阈值，也无法判断规则
// 是否在真实数据上失灵。缺口已修，所以这里改成钉死新行为。
//
// 同时把两种「非垃圾」区分开：
//   - 豁免（invoiceCandidate / 白名单域）：压根没参与评分 → Score=0, Why=""
//   - 评过分但不够线：Score>0, Why 有内容
// 调用方要靠这个区别决定「是否值得人工看一眼」。
func TestLooksLikeSpam_ExposesScoreBelowThreshold(t *testing.T) {
	// 只有弱信号（发件人 30 分），达不到 100 阈值。
	v := LooksLikeSpam("newsletter@newsletter.aliyun.com", "本周精选", "", false, false, 1)
	if v.Spam {
		t.Fatalf("单条弱信号不该判垃圾: %+v", v)
	}
	if v.Score <= 0 {
		t.Fatalf("Score=%d，未达阈值时也必须暴露分数，否则预演报告无法供人调阈值", v.Score)
	}
	if v.Why == "" {
		t.Fatal("未达阈值时也必须给出理由（预演报告要给人看差在哪）")
	}

	// 豁免路径：邮件根本没参与评分。
	ex := LooksLikeSpam("billing@alipay.com", "促销 折扣 优惠 秒杀", "限时抢购", false, false, 1)
	if ex.Score != 0 || ex.Why != "" {
		t.Fatalf("白名单域应走豁免（不评分），却拿到 score=%d why=%q", ex.Score, ex.Why)
	}
	// invoiceCandidate 短路同理。
	ic := LooksLikeSpam("promo@shop.cn", "促销 折扣 优惠 秒杀", "限时抢购", true, false, 1)
	if ic.Score != 0 || ic.Why != "" {
		t.Fatalf("发票候选应短路不评分，却拿到 score=%d why=%q", ic.Score, ic.Why)
	}
}
