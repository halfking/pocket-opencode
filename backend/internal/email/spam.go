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

// spamWeakWords 弱信号词，两条以上或配合营销发件人特征才判垃圾。
var spamWeakWords = []string{
	"促销", "优惠", "折扣", "特价", "新品上架", "会员日", "活动邀请",
	"推广", "营销", "订阅更新", "本周精选", "专属福利", "扫码", "海报",
	"promo", "sale", "discount", "deal", "newsletter", "weekly digest",
	"exclusive offer", "limited time",
}

// spamSenderHints 发件人 local-part / 域名特征。
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
func LooksLikeSpam(from, subject, snippet string, invoiceCandidate, important bool) SpamVerdict {
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
	if weakHits >= 2 {
		add(40, "营销词×"+strconv.Itoa(weakHits)+":"+weakHit)
	}
	for _, h := range spamSenderHints {
		if strings.Contains(fromLower, h) {
			add(30, "营销发件人特征:"+h)
			break
		}
	}
	for _, p := range spamSubjectPatterns {
		if strings.Contains(subject, p) {
			add(45, "退订特征:"+p)
			break
		}
	}
	// 纯图片/纯 HTML 单元格堆叠类广告常见特征：摘要几乎无有效文本
	if snippet != "" && !utf8.ValidString(snippet) {
		add(20, "摘要编码异常")
	}
	if score >= 100 {
		return SpamVerdict{Spam: true, Score: score, Why: strings.Join(whys, "; ")}
	}
	return SpamVerdict{}
}
