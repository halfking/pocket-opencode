package server

import (
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/finance"
)

// 笔记重新总结时，本次解析结果与已入账记录的一致性判定（幂等提示素材）。
func TestNoteBookkeepingMismatch(t *testing.T) {
	base := &finance.Transaction{Type: "expense", Amount: 12.5}

	cases := []struct {
		name         string
		parsedType   string
		parsedAmount float64
		tx           *finance.Transaction
		want         bool
	}{
		{"一致", "expense", 12.5, base, false},
		{"浮点噪声视为一致", "expense", 12.5000001, base, false},
		{"金额不同", "expense", 20, base, true},
		{"方向不同", "income", 12.5, base, true},
		{"方向金额均不同", "income", 30, base, true},
		{"无既有记录不算不一致", "expense", 99, nil, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := noteBookkeepingMismatch(tc.parsedType, tc.parsedAmount, tc.tx); got != tc.want {
				t.Fatalf("noteBookkeepingMismatch(%s, %v, %+v) = %v, want %v",
					tc.parsedType, tc.parsedAmount, tc.tx, got, tc.want)
			}
		})
	}
}
