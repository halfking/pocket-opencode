// internal/finance/types.go
package finance

import "time"

const (
	// TransactionTypeIncome represents income transactions
	TransactionTypeIncome = "income"
	// TransactionTypeExpense represents expense transactions
	TransactionTypeExpense = "expense"
)

// Transaction 记账记录
type Transaction struct {
	ID          string    `json:"id"`
	OwnerID     string    `json:"owner_id,omitempty"`
	WorkspaceID string    `json:"workspace_id,omitempty"`
	Type        string    `json:"type"` // income / expense
	Amount      float64   `json:"amount"`
	Category    string    `json:"category"` // 餐饮 / 交通 / 购物 / 工资 / 项目收入
	Note        string    `json:"note,omitempty"`
	Tags        []string  `json:"tags,omitempty"`
	ProjectID   string    `json:"project_id,omitempty"`
	Source      string    `json:"source"`             // manual / voice / auto / invoice
	NoteRef     string    `json:"note_ref,omitempty"` // 幂等键（如 "note:<id>"），同键去重
	CreatedAt   time.Time `json:"created_at"`
}

// CreateTransactionRequest 创建记账请求
// Type must be either "income" or "expense"
// Amount must be positive
type CreateTransactionRequest struct {
	Type      string   `json:"type"`
	Amount    float64  `json:"amount"`
	Category  string   `json:"category"`
	Note      string   `json:"note,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	ProjectID string   `json:"project_id,omitempty"`
	// Source 入账来源：manual（默认）| voice（语音解析）| auto（笔记自动记账）| invoice（发票入账）。
	Source string `json:"source,omitempty"`
	// NoteRef 幂等键：同 owner+workspace 下非空且已存在时返回既有记录而不重复入账。
	NoteRef string `json:"note_ref,omitempty"`
}

// Budget 预算配置
type Budget struct {
	ID       string  `json:"id"`
	Category string  `json:"category"`
	Month    string  `json:"month"` // "2026-07"
	Limit    float64 `json:"limit"`
	Spent    float64 `json:"spent"`    // 计算字段
	AlertAt  float64 `json:"alert_at"` // 达到多少百分比时提醒 (如 80)
}

// StatsQuery 统计查询
type StatsQuery struct {
	Month    string `json:"month,omitempty"`    // "2026-07"
	Category string `json:"category,omitempty"` // 筛选特定分类
	// TZOffsetMinutes 客户端所在时区相对 UTC 的分钟偏移（东八区=480，即
	// -getTimezoneOffset()）。nil 时保持旧行为：PG 按数据库会话时区分桶。
	TZOffsetMinutes *int `json:"tz_offset_minutes,omitempty"`
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
