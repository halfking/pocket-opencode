// internal/finance/recognizer.go
package finance

import (
	"regexp"
	"strconv"
	"strings"
)

// ParseResult 语音解析结果
type ParseResult struct {
	Type     string  `json:"type"`     // income / expense
	Amount   float64 `json:"amount"`
	Category string  `json:"category"`
	Note     string  `json:"note"`
}

// Recognizer 语音记账识别引擎
type Recognizer struct {
	amountRegex *regexp.Regexp
}

func NewRecognizer() *Recognizer {
	return &Recognizer{
		amountRegex: regexp.MustCompile(`(\d+\.?\d*)\s*块`),
	}
}

// Parse 解析语音输入，返回记账结果
func (r *Recognizer) Parse(input string) *ParseResult {
	if strings.TrimSpace(input) == "" {
		return nil
	}

	lower := strings.ToLower(input)

	// 提取金额
	matches := r.amountRegex.FindStringSubmatch(input)
	if len(matches) < 2 {
		return nil
	}

	amount, err := strconv.ParseFloat(matches[1], 64)
	if err != nil || amount <= 0 {
		return nil
	}

	// 判断类型：收入还是支出
	isIncome := hasAny(lower, []string{"收到", "收入", "入账", "进账", "收款", "回款", "工资"})

	// 分类
	var category string
	if isIncome {
		if hasAny(lower, []string{"工资", "薪水", "薪资"}) {
			category = "工资"
		} else if hasAny(lower, []string{"项目", "尾款", "款"}) {
			category = "项目收入"
		} else {
			category = "其他收入"
		}
	} else {
		if hasAny(lower, []string{"吃饭", "午餐", "晚餐", "早餐", "外卖", "餐饮", "吃喝"}) {
			category = "餐饮"
		} else if hasAny(lower, []string{"打车", "出租", "滴滴", "地铁", "公交", "交通", "加油", "停车"}) {
			category = "交通"
		} else if hasAny(lower, []string{"购物", "买", "超市", "网购"}) {
			category = "购物"
		} else {
			category = "其他"
		}
	}

	txType := "expense"
	if isIncome {
		txType = "income"
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