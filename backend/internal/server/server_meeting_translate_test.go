package server

import (
	"strings"
	"testing"
)

// TestDetectLanguage 测试语言检测
func TestDetectLanguage(t *testing.T) {
	tests := []struct {
		text     string
		expected string
	}{
		{"你好世界", "zh"},
		{"Hello world", "en"},
		{"こんにちは", "ja"},
		{"안녕하세요", "ko"},
		{"", "auto"},
	}

	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			result := detectLanguage(tt.text)
			if result != tt.expected {
				t.Errorf("detectLanguage(%q) = %q, 期望 %q", tt.text, result, tt.expected)
			}
		})
	}
}

// TestBuildTranslatePrompt 测试翻译 prompt 构建
func TestBuildTranslatePrompt(t *testing.T) {
	tests := []struct {
		name       string
		text       string
		sourceLang string
		targetLang string
		shouldContain []string
	}{
		{
			name:       "指定源语言",
			text:       "Hello",
			sourceLang: "en",
			targetLang: "zh",
			shouldContain: []string{"English", "Chinese", "Hello"},
		},
		{
			name:       "自动检测",
			text:       "Hello",
			sourceLang: "auto",
			targetLang: "zh",
			shouldContain: []string{"Chinese", "Hello"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prompt := buildTranslatePrompt(tt.text, tt.sourceLang, tt.targetLang)
			for _, s := range tt.shouldContain {
				if !strings.Contains(prompt, s) {
					t.Errorf("prompt 应包含 %q, 实际: %s", s, prompt)
				}
			}
		})
	}
}
