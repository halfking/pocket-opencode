// internal/finance/recognizer.go
package finance

import (
	"regexp"
	"strconv"
	"strings"
)

// ParseResult 语音解析结果，包含识别出的交易信息
type ParseResult struct {
	Type     string  `json:"type"`     // income / expense
	Amount   float64 `json:"amount"`   // 金额（必须为正数）
	Category string  `json:"category"` // 分类（餐饮、交通、工资等）
	Note     string  `json:"note"`     // 原始输入文本
}

// Recognizer 语音记账识别引擎，用于解析自然语言输入并提取交易信息
type Recognizer struct {
	// amountRegex 优先匹配「货币符号+数字」或「数字+货币单位」——金额最可靠。
	amountRegex *regexp.Regexp
	// looseAmountRegex 兜底：无货币符号也无单位时，接受任意数字，
	// 但数字前不得紧跟 ASCII 字母/数字。
	looseAmountRegex *regexp.Regexp
}

// NewRecognizer 创建新的语音识别器实例
func NewRecognizer() *Recognizer {
	return &Recognizer{
		// BUG-H (2026-09-30)：原正则是 `[¥$]?\s*(\d+...)\s*(?:块钱?|元|钱)?`，
		// 除数字外全部可选，且 FindStringSubmatch 取【第一个】匹配。
		// 于是 "E2E 50 元" 里 "E2E" 的 2 被当成金额（实测 amount=2，50 丢失），
		// /api/finance/parse 还返回 200，错误金额静默入账。
		//
		// 改为两级策略，既修掉误取又保住既有契约：
		//   1) 优先匹配带货币符号或货币单位的金额（"打车 32 元" -> 32）；
		//   2) 都没有时回退到裸数字，但要求数字前不是 ASCII 字母/数字
		//      （"吃饭花了38" -> 38 保留；"E2E 50" 里 E2E 的 2 被排除 -> 50）。
		amountRegex:      regexp.MustCompile(`[¥$]\s*(\d+(?:\.\d{1,2})?)|(\d+(?:\.\d{1,2})?)\s*(?:块钱?|元|钱)`),
		looseAmountRegex: regexp.MustCompile(`(?:^|[^A-Za-z0-9])\s*(\d+(?:\.\d{1,2})?)`),
	}
}

// extractAmount 提取金额。优先带货币符号/单位的写法，其次是不粘在 ASCII
// 字母后的裸数字。返回 0 表示未识别到。
func (r *Recognizer) extractAmount(input string) float64 {
	if m := r.amountRegex.FindStringSubmatch(input); len(m) >= 3 {
		s := m[1]
		if s == "" {
			s = m[2]
		}
		if s != "" {
			if v, err := strconv.ParseFloat(s, 64); err == nil {
				return v
			}
		}
	}
	if m := r.looseAmountRegex.FindStringSubmatch(input); len(m) >= 2 && m[1] != "" {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			return v
		}
	}
	return 0
}

// Parse 解析语音输入，返回记账结果
// 输入为空或无法识别时返回 nil
// 成功识别时返回包含交易类型、金额、分类的结果
func (r *Recognizer) Parse(input string) *ParseResult {
	if strings.TrimSpace(input) == "" {
		return nil
	}

	lower := strings.ToLower(input)

	// 提取金额
	amount := r.extractAmount(input)
	if amount <= 0 {
		return nil
	}

	// 判断类型：收入还是支出
	isIncome := hasAny(lower, []string{"收到", "收入", "入账", "进账", "收款", "回款", "工资", "发了", "到账"})

	// 分类
	var category string
	if isIncome {
		if hasAny(lower, []string{"工资", "薪水", "薪资"}) {
			category = "工资"
		} else if hasAny(lower, []string{"项目", "尾款", "款项", "回款"}) {
			category = "项目收入"
		} else {
			category = "其他收入"
		}
	} else {
		// 交通类别检查优先级高于餐饮，避免"花了"误判
		if hasAny(lower, []string{"打车", "出租", "滴滴", "地铁", "公交", "交通", "加油", "停车"}) {
			category = "交通"
		} else if hasAny(lower, []string{"吃饭", "午餐", "晚餐", "早餐", "外卖", "餐饮", "吃喝", "花了"}) {
			category = "餐饮"
		} else if hasAny(lower, []string{"购物", "买", "超市", "网购"}) {
			category = "购物"
		} else {
			category = "其他"
		}
	}

	txType := TransactionTypeExpense
	if isIncome {
		txType = TransactionTypeIncome
	}

	return &ParseResult{
		Type:     txType,
		Amount:   amount,
		Category: category,
		Note:     input,
	}
}

func hasAny(s string, keywords []string) bool {
	for _, kw := range keywords {
		if strings.Contains(s, kw) {
			return true
		}
	}
	return false
}
