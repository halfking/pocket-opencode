// internal/finance/types.go
package finance

import "time"

// Transaction 记账记录
type Transaction struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`               // income / expense
	Amount    float64   `json:"amount"`
	Category  string    `json:"category"`            // 餐饮 / 交通 / 购物 / 工资 / 项目收入
	Note      string    `json:"note,omitempty"`
	Tags      []string  `json:"tags,omitempty"`
	ProjectID string    `json:"project_id,omitempty"`
	Source    string    `json:"source"`              // manual / voice / auto
	CreatedAt time.Time `json:"created_at"`
}

// CreateTransactionRequest 创建记账请求
type CreateTransactionRequest struct {
	Type      string   `json:"type"`
	Amount    float64  `json:"amount"`
	Category  string   `json:"category"`
	Note      string   `json:"note,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	ProjectID string   `json:"project_id,omitempty"`
}

// Budget 预算
type Budget struct {
	ID       string  `json:"id"`
	Category string  `json:"category"`
	Month    string  `json:"month"`     // "2026-07"
	Limit    float64 `json:"limit"`
	Spent    float64 `json:"spent"`     // 计算字段
	AlertAt  float64 `json:"alert_at"`  // 达到多少百分比时提醒 (如 80)
}

// StatsQuery 统计查询
type StatsQuery struct {
	Month    string `json:"month,omitempty"`    // "2026-07"
	Category string `json:"category,omitempty"` // 筛选特定分类
}

// MonthlyStats 月度统计
type MonthlyStats struct {
	Month        string             `json:"month"`
	TotalIncome  float64            `json:"total_income"`
	TotalExpense float64            `json:"total_expense"`
	Balance      float64            `json:"balance"`
	ByCategory   map[string]float64 `json:"by_category"`
	Count        int                `json:"count"`
}