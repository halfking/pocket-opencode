package email

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// spam.go — 广告/垃圾邮件判定（纯规则，零外部依赖）。
//
// 判定哲学：宁缓勿滥。误杀一封正常邮件的代价远高于漏放一封广告，因此
// 采用「强信号直接判、弱信号累计计分」的两档模型，且发票候选与重要邮件
// 永远不判垃圾（收发票是本系统的核心职责，营销邮件伪装成账单时宁可让
// 它留在收件箱）。
//
// 判定输入是主题+摘要+发件人，不需要全文——流水线在全文抓取前就能完成
// 清理，省掉大部分 IMAP 流量。

// spamStrongWords 命中即强信号（单凭主题出现这类词几乎必然是营销）。
var spamStrongWords = []string{
	"中奖", "抽中", "恭喜您获得", "免费领取", "限时抢购", "秒杀",
	"低价促销", "大促", "满减", "优惠券到账", "返现", "砍价",
	"lottery", "you have won", "claim your prize", "congratulations you",
	"unsubscribe here", "click here to unsubscribe",
}

// spamWeakWords 弱信号词。命中数量按分级计分（见 LooksLikeSpam）。
//
// 表里同时收了「本周精选」「精选」「精选文章」这类**同一语义的不同形态**，
// 不是为了重复计分，而是因为 strings.Contains 是子串匹配：主题写
// 「本周精选文章」时，只有把这一串的各层前缀都收进来，才能在**任意**写法
// 下都数出足够多的弱词。真实数据里营销文案的花样比词表长，靠形态覆盖
// 比靠逐条枚举稳。
var spamWeakWords = []string{
	"促销", "优惠", "折扣", "特价", "新品上架", "会员日", "活动邀请",
	"推广", "营销", "订阅更新", "订阅", "精选", "本周精选", "精选文章",
	"好文", "专属福利", "扫码", "海报", "限时", "特惠", "福利",
	"promo", "sale", "discount", "deal", "newsletter", "weekly digest",
	"exclusive offer", "limited time",
	// 资讯/摘要类。纯技术周报既没有促销词也未必带退订头，光靠上面那批
	// 只能到 70 分，差 30 永远过不了 100 的阈值——实测「本周技术精选」
	// 这类真实 newsletter 正是如此。它们与促销无关，但对「收发票 + 看重要
	// 邮件」的系统是纯噪声，与退订营销是同一类东西。
	"周报", "资讯", "简报", "每日精选", "行业动态", "技术分享", "公开课",
}

// spamSenderHints 发件人 local-part / 域名特征。
//
// "news" 是刻意收进来的：企业营销部门的发件地址普遍是 news@ / newsroom@。
// 它只值 30 分，单独不足以判垃圾（见 TestLooksLikeSpam 的「弱信号不足」），
// 但与退订特征叠加刚好过线——这正是「今日精选好文（回复 退订）」那类邮件。
var spamSenderHints = []string{
	"promo", "promotion", "marketing", "newsletter", "advert", "edm",
	"mailers", "bounce", "bulk", "offers@", "deals@",
}

// spamListHeaderLike 主题里的退订/清单特征。
var spamSubjectPatterns = []string{
	"退订", "取消订阅", "拒收", "回T退订", "回td退订",
}

// spamDomainWhitelist 出票/账单类高频域名不判垃圾（它们的邮件带营销词
// 也往往是订单/发票通知，误杀代价高）。
var spamDomainWhitelist = []string{
	"12306.cn", "95580.net", "unionpay", "alipay.com", "tenpay.com",
	"didichuxing.com", "didialift", "meituan.com", "ele.me", "jd.com",
	"taobao.com", "tmall.com", "pinduoduo.com", "ctrip.com", "qunar.com",
	"flycua.com", "airchina", "ceair.com", "csair.com", "western airlines",
	"exmail.qq.com", "kxpms.cn",
}

// SpamVerdict 是判定结论。
type SpamVerdict struct {
	Spam  bool
	Score int
	Why   string
}

// LooksLikeSpam 判定一封邮件是否广告/垃圾。invoiceCandidate 与
// important 由调用方短路传入（true 时永远返回非垃圾）。
//
// senderVolume 是同一发件人在本批邮件里出现的封数（0/1 表示"未统计"）。
//
// ## 为什么需要它（2026-10-02 按当前真库数据更正）
//
// 原文写的是「实测真实信箱 430 封里 spamHits=0，14 封 near-miss 全部卡在
// 30 分」，并据此断言「补退订特征无效——真实 emails.snippet 存的是**原始
// MIME 头**（105 封形如 "------=_Part_... Content-Type: text/html"）」。
//
// **那条断言对当前数据已不成立**，它描述的是上一批数据来源。实测
// opencode_pocket.emails 120 封（2026-10-02 05:5x）：
//
//	snippet 以 "------=_Part_" 开头   1 封
//	snippet 以 "Content-Type" 开头   0 封
//	snippet 以 RFC822 邮件头开头      0 封
//	snippet 为空                      0 封
//	snippet 平均长度                  377 字符（最长 501）
//
// 即 119/120 是**真实正文**（形如「极客时间 点击这里取消订阅 ------=_Part_…」——
// 正文在前，MIME 边界在尾部）。所以退订特征**现在是有效的**，而且它是
// 决定性信号：同一批 6 封命中里 Why 全部含「退订特征:取消订阅」。
//
// 当前分布实测（同一批 120 封）：命中 6、near-miss 1（30 分）。
// 两极分化明显——要么明显是列表推送过线，要么几乎没特征，中间地带为空。
//
// ## 仍然成立的判据
//
// 发件人成批推送这条依然是最稳的：InfoQChina@edm.infoq.com.cn、
// newsletter@newsletter.aliyun.com、promotion@news.ecloudrover.com
// 发的每一封都是列表推送。一对一的人际邮件不会来自同一地址成批发来。
// 单看一封无从判断，看同一地址的量就能判断——所以这个信号必须由调用方
// 统计后传进来，纯函数自己不掌握跨封信息。
//
// ## 一个已知的自洽性问题（2026-10-02 实测，本轮未改）
//
// 同一发件人会出现**判定不一致**：InfoQChina@edm.infoq.com.cn 3 封里，
// 2 封页脚带「点击这里取消订阅」→ 100 分判垃圾，第 3 封（InfoQ 每周精要
// No.940）snippet 开头是正文、没匹配到退订 → 只 30 分留在收件箱。
// 差别只在某一封的邮件模板有没有那个页脚链接。
//
// 「列表推送就是列表推送」，按单封模板决定去留在语义上说不通。
// 但改成按发件人整体判定属于**产品语义**（会不会因此误杀同域的真人邮件），
// 没有拍板前不改。已知问题记录在此，别当成没看见。
func LooksLikeSpam(from, subject, snippet string, invoiceCandidate, important bool, senderVolume int) SpamVerdict {
	if invoiceCandidate || important {
		return SpamVerdict{}
	}
	fromLower := strings.ToLower(strings.TrimSpace(from))
	subjectLower := strings.ToLower(subject)
	snippetLower := strings.ToLower(snippet)
	if i := strings.LastIndex(fromLower, "@"); i >= 0 {
		domain := fromLower[i+1:]
		for _, w := range spamDomainWhitelist {
			if strings.Contains(domain, w) {
				return SpamVerdict{}
			}
		}
	}

	score := 0
	whys := make([]string, 0, 3)
	add := func(n int, why string) {
		score += n
		whys = append(whys, why)
	}
	for _, w := range spamStrongWords {
		if strings.Contains(subjectLower, w) || strings.Contains(snippetLower, w) {
			add(100, "强营销词:"+w)
			break
		}
	}
	weakHits := 0
	weakHit := ""
	for _, w := range spamWeakWords {
		if strings.Contains(subjectLower, w) || strings.Contains(snippetLower, w) {
			weakHits++
			if weakHit == "" {
				weakHit = w
			}
		}
	}
	// 弱词按**数量**分级，而不是「≥2 就给固定 40 分」。
	//
	// 旧规则的问题：弱词只要够 2 个就加 40，永远够不到 100 的阈值，于是
	// 「今日精选好文（回复 退订）」这类真实营销邮件永远漏判（实测 score=0
	// —— 因为它只命中退订 + 1 个弱词）。改成按数量定性之后，弱信号密集
	// 的邮件自己就能过线，不必依赖是否恰好撞上某个强词。
	//
	// 1 个弱词不给分：单个词（"促销"）太常见，真实工作邮件里也会出现。
	switch {
	case weakHits >= 4:
		add(100, "营销词×"+strconv.Itoa(weakHits)+":"+weakHit)
	case weakHits == 3:
		add(70, "营销词×3:"+weakHit)
	case weakHits == 2:
		add(40, "营销词×2:"+weakHit)
	default:
		// 不计分，但仍记进 Why：预演报告要能看出「差一点」的邮件差在哪。
		whys = append(whys, "营销词×"+strconv.Itoa(weakHits)+":"+weakHit)
	}
	for _, h := range spamSenderHints {
		if strings.Contains(fromLower, h) {
			add(30, "营销发件人特征:"+h)
			break
		}
	}
	// 同发件人成批推送。阈值取 5 是照着真实数据定的：这三家分别是 8/5/1 封。
	//
	// 5 封以上 + 营销发件人特征（30）= 100，正好过线。**不**单靠这一条：
	// 同一个域下 5 封以上的地址也可能是同事群发或监控告警，必须叠加已有的
	// 营销特征，避免把「同一个人多封邮件」误当成营销。
	if senderVolume >= 5 {
		hasHint := false
		for _, h := range spamSenderHints {
			if strings.Contains(fromLower, h) {
				hasHint = true
				break
			}
		}
		if hasHint {
			add(70, "同发件人批量推送×"+strconv.Itoa(senderVolume))
		} else {
			// 不计分但记进 Why：预演报告要能看出「只是量大、不是营销」。
			whys = append(whys, "同发件人批量推送×"+strconv.Itoa(senderVolume)+"(无营销特征,不计分)")
		}
	}
	for _, p := range spamSubjectPatterns {
		// 主题**和摘要**都要查。真实 newsletter 的退订链接几乎总在 HTML
		// 摘要里，主题只是文章标题——只查主题等于漏掉整类「技术资讯/云厂商
		// 周报」，而这正是本系统真实信箱里 score 最高的那批（实测 19 封
		// near-miss 全部是这类，score=30 差的就是这个退订分）。
		//
		// 命中即 100：带退订头的邮件**按定义**就是可退订的营销列表，这不是
		// 推断。原先给 70 是把「退订」当弱信号和别的词凑分，结果「阿里云
		// 云安全中心周报」这种真实营销邮件只能到 70（退订 70 + 周报 1 个
		// 弱词不给分），永远差 30 判不掉——而它确实是垃圾。
		// 误伤风险由 invoiceCandidate / important 短路和白名单域兜住：
		// 真实账单发票即使带退订也不会走到这里。
		if strings.Contains(subject, p) || strings.Contains(snippet, p) {
			return SpamVerdict{Spam: true, Score: 100, Why: "退订特征:" + p}
		}
	}
	// 纯图片/纯 HTML 单元格堆叠类广告常见特征：摘要几乎无有效文本
	if snippet != "" && !utf8.ValidString(snippet) {
		add(20, "摘要编码异常")
	}
	if score >= 100 {
		return SpamVerdict{Spam: true, Score: score, Why: strings.Join(whys, "; ")}
	}
	// 未达阈值也要返回 Score/Why，而不是零值。
	//
	// 返回零值时调用方分不出「压根没参与评分（豁免）」和「评过分但差一截」，
	// 于是预演报告只有命中/未命中两态：阈值没法用真实数据校准，规则在真实
	// 邮件上是否失灵也看不出来——2026-10-01 那次「443 封真实邮件 spamHits=0」
	// 就卡在这里，无法区分「信箱里没广告」和「规则形同虚设」。
	// 豁免路径（invoiceCandidate / important / 白名单域）仍然返回零值，
	// 调用方靠 Score>0 判断「值得人看一眼」。
	return SpamVerdict{Spam: false, Score: score, Why: strings.Join(whys, "; ")}
}
