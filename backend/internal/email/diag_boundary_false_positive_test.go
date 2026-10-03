package email

// diag_boundary_false_positive_test.go — 纯函数诊断：MIME 守卫会不会误伤正常邮件。
//
// ## 为什么查这个
//
// 2026-10-03 只读实测（logs/zz-boundary-fp.txt）：用 SQL 在**真实库的摘要**上
// 跑 containsMIMESource 的三条判据，发现
//
//	reBoundaryToken = `--(?:[=_-]|[Pp]art[_-])`   (snippet.go:467)
//
// 命中了 13 条摘要，其中绝大多数**不含任何 MIME 头 token**，纯误伤。两个
// 无可争议的样本（都是**真实业务正文**）：
//
//	uid 1298896144（工商银行信用卡对账单）  命中子串：---人民币(本位币)---
//	uid 1298896146（产品更新 newsletter）    命中子串：---------------
//
// 前者是对账单里「合计人民币(本位币)」两侧的分隔符，后者是邮件里的分割线。
// 它们与 MIME 毫无关系。
//
// ## 误伤的后果不是「多挡一点」，而是**摘要被整条丢掉**
//
// mime.go:645-648：
//
//	if t := strings.TrimSpace(msg.TextBody); t != "" && !containsMIMESource(msg.TextBody) { return t }
//	if h := strings.TrimSpace(msg.HTMLBody); h != "" && !containsMIMESource(msg.HTMLBody) { return h }
//
// 命中 ⇒ 返回空 ⇒ 回到 fetcher.go:958 的回落 DeriveSnippet(parsed.HTMLBody)，
// 而 DeriveSnippet 内部同样调 containsMIMESource，大概率也返回空
// ⇒ **邮件列表里那一格是空白**。需求 7「在邮件窗口查看各类邮件」直接受影响。
//
// 本文件不连数据库、不改任何生产正则：把真实库里的原样文本拿来问那个函数。

import "testing"

// 门控已移除（2026-10-03）：reBoundaryToken 的裸 `-` 已从字符类去掉，
// 本文件断言的缺陷**已修复**，它从「已知缺陷的证据」转为**常驻护栏**。
//
// 此前加门控是为了不把「缺陷」伪装成「我这轮改坏了」；现在缺陷没了，
// 继续门控只会让它失去牙齿（没人会记得去设那个环境变量）。
//
// 负控：把 snippet.go:467 改回 `--(?:[=_-]|[Pp]art[_-])` → 本文件立刻转红。
func TestDiagBoundaryFalsePositiveOnRealSnippets(t *testing.T) {
	// 全部取自真实库 summaries 的**原样片段**（logs/zz-boundary-fp.txt M5）。
	cases := []struct {
		name    string
		snippet string
		why     string
		// legitimate=true 表示这是**正常业务文本**，containsMIMESource 不该拦它。
		// 用显式字段而不是靠名字前缀比较——`name[:6]` 撞上中文冒号会静默失配，
		// 而那种失配只会让断言列表少一条，不会让测试变红。
		legitimate bool
	}{
		{
			name:       "工商银行对账单：合计行两侧的分隔符",
			snippet:    "信 用 卡 对 账 单尊敬的黄旭涛先生,您好! 本期余额 ---人民币(本位币)--- 9097",
			why:        "「---人民币(本位币)---」里的 --- 命中 `--[=_-]`，但这是对账单正文",
			legitimate: true,
		},
		{
			name:       "产品更新 newsletter：正文分割线",
			snippet:    "Claude Opus 5.5, GPT-6, the Requesty CLI and more. product updates ---------------",
			why:        "一串连字符的分割线命中 `--[=_-]`，但这是排版元素",
			legitimate: true,
		},
		{
			name:       "消费明细：转发行里的真 boundary",
			snippet:    "消费 支付宝-支付宝支付科技有限公司 ------=_Part_8505717_",
			why:        "确实含真 boundary 尾巴，属于**应当**拦下的那一类",
			legitimate: false,
		},
	}

	t.Logf("reBoundaryToken = %s", reBoundaryToken.String())
	t.Logf("")

	var falsePositives []string
	missedGenuine := 0
	for _, c := range cases {
		hitBoundary := reBoundaryToken.MatchString(c.snippet)
		hitHeader := reMIMEHeaderToken.MatchString(c.snippet)
		total := containsMIMESource(c.snippet)
		verdict := "OK(拦住)"
		if total && c.legitimate {
			verdict = "*** 误伤 ***"
			falsePositives = append(falsePositives, c.name)
		}
		if !total && !c.legitimate {
			verdict = "*** 漏放（真 MIME 没拦住）***"
			missedGenuine++
		}
		t.Logf("%-52s boundary=%-5v headerToken=%-5v containsMIMESource=%-5v  %s",
			c.name, hitBoundary, hitHeader, total, verdict)
		t.Logf("    %s", c.why)
	}

	if len(falsePositives) > 0 {
		t.Errorf("containsMIMESource 对 %d 条**正常业务文本**返回 true："+
			"按 mime.go:645-648 会被整条丢弃，邮件列表显示空白；"+
			"命中它们的都是正文里的 --- 分隔符/连字符分割线，与 MIME 无关。%v",
			len(falsePositives), falsePositives)
	}
	if missedGenuine > 0 {
		t.Errorf("反向漏放 %d 条：真 boundary 没被拦下，守卫有缺口。", missedGenuine)
	}
}
