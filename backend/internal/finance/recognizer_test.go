// internal/finance/recognizer_test.go
package finance

import (
	"testing"
)

func TestRecognizer_Parse_Expense(t *testing.T) {
	r := NewRecognizer()
	result := r.Parse("中午吃饭花了38块")
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Type != "expense" {
		t.Errorf("expected expense, got %s", result.Type)
	}
	if result.Amount != 38 {
		t.Errorf("expected 38, got %f", result.Amount)
	}
	if result.Category != "餐饮" {
		t.Errorf("expected 餐饮, got %s", result.Category)
	}
}

func TestRecognizer_Parse_Income(t *testing.T) {
	r := NewRecognizer()
	result := r.Parse("收到项目尾款5000块")
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Type != "income" {
		t.Errorf("expected income, got %s", result.Type)
	}
	if result.Amount != 5000 {
		t.Errorf("expected 5000, got %f", result.Amount)
	}
}

func TestRecognizer_Parse_Taxi(t *testing.T) {
	r := NewRecognizer()
	result := r.Parse("打车去客户那里花了45块钱")
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Type != "expense" {
		t.Errorf("expected expense, got %s", result.Type)
	}
	if result.Category != "交通" {
		t.Errorf("expected 交通, got %s", result.Category)
	}
}

func TestRecognizer_Parse_Salary(t *testing.T) {
	r := NewRecognizer()
	result := r.Parse("发了工资15000块")
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Type != "income" {
		t.Errorf("expected income, got %s", result.Type)
	}
	if result.Category != "工资" {
		t.Errorf("expected 工资, got %s", result.Category)
	}
}

func TestRecognizer_Parse_Unrecognized(t *testing.T) {
	r := NewRecognizer()
	result := r.Parse("今天天气真好")
	if result != nil {
		t.Errorf("expected nil for unrecognized, got %+v", result)
	}
}

func TestRecognizer_Parse_Empty(t *testing.T) {
	r := NewRecognizer()
	result := r.Parse("")
	if result != nil {
		t.Error("expected nil for empty input")
	}
}