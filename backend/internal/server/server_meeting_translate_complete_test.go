package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/aigate"
)

// MockLLMGateway 模拟 LLM 网关
type MockLLMGateway struct {
	response string
	err      error
}

func (m *MockLLMGateway) Chat(ctx context.Context, model string, messages []aigate.ChatMessage) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.response, nil
}

func TestHandleMeetingTranslate(t *testing.T) {
	tests := []struct {
		name           string
		requestBody    map[string]interface{}
		mockResponse   string
		mockError      error
		expectedStatus int
		checkResponse  func(*testing.T, map[string]interface{})
	}{
		{
			name: "成功翻译中文到英文",
			requestBody: map[string]interface{}{
				"text":       "你好，世界",
				"sourceLang": "zh",
				"targetLang": "en",
			},
			mockResponse:   "Hello, world",
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, resp map[string]interface{}) {
				if resp["translation"] != "Hello, world" {
					t.Errorf("expected translation 'Hello, world', got %v", resp["translation"])
				}
				if resp["detectedLang"] != "zh" {
					t.Errorf("expected detectedLang 'zh', got %v", resp["detectedLang"])
				}
				if resp["model"] != "gpt-4o-mini" {
					t.Errorf("expected model 'gpt-4o-mini', got %v", resp["model"])
				}
			},
		},
		{
			name: "自动检测语言",
			requestBody: map[string]interface{}{
				"text":       "Hello",
				"sourceLang": "auto",
				"targetLang": "zh",
			},
			mockResponse:   "你好",
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, resp map[string]interface{}) {
				if resp["translation"] != "你好" {
					t.Errorf("expected translation '你好', got %v", resp["translation"])
				}
				if resp["detectedLang"] != "en" {
					t.Errorf("expected detectedLang 'en', got %v", resp["detectedLang"])
				}
			},
		},
		{
			name: "日文检测",
			requestBody: map[string]interface{}{
				"text":       "こんにちは",
				"sourceLang": "auto",
				"targetLang": "zh",
			},
			mockResponse:   "你好",
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, resp map[string]interface{}) {
				if resp["detectedLang"] != "ja" {
					t.Errorf("expected detectedLang 'ja', got %v", resp["detectedLang"])
				}
			},
		},
		{
			name: "韩文检测",
			requestBody: map[string]interface{}{
				"text":       "안녕하세요",
				"sourceLang": "auto",
				"targetLang": "zh",
			},
			mockResponse:   "你好",
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, resp map[string]interface{}) {
				if resp["detectedLang"] != "ko" {
					t.Errorf("expected detectedLang 'ko', got %v", resp["detectedLang"])
				}
			},
		},
		{
			name: "缺少必需字段",
			requestBody: map[string]interface{}{
				"text": "",
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "文本过长",
			requestBody: map[string]interface{}{
				"text":       string(make([]byte, 6000)),
				"targetLang": "en",
			},
			expectedStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 创建模拟 server
			s := &Server{
				llm: &MockLLMGateway{
					response: tt.mockResponse,
					err:      tt.mockError,
				},
			}

			// 创建请求
			body, _ := json.Marshal(tt.requestBody)
			req := httptest.NewRequest(http.MethodPost, "/api/meetings/translate", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			// 执行请求
			s.handleMeetingTranslate(w, req)

			// 检查状态码
			if w.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, w.Code)
			}

			// 检查响应内容
			if tt.checkResponse != nil && w.Code == http.StatusOK {
				var resp map[string]interface{}
				if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
					t.Fatalf("failed to parse response: %v", err)
				}
				tt.checkResponse(t, resp)
			}
		})
	}
}

func TestHandleMeetingTranslateBatch(t *testing.T) {
	tests := []struct {
		name           string
		requestBody    map[string]interface{}
		mockResponse   string
		expectedStatus int
		checkResponse  func(*testing.T, map[string]interface{})
	}{
		{
			name: "批量翻译成功",
			requestBody: map[string]interface{}{
				"texts":      []string{"Hello", "World", "Goodbye"},
				"targetLang": "zh",
			},
			mockResponse: `[
				{"index": 0, "translation": "你好"},
				{"index": 1, "translation": "世界"},
				{"index": 2, "translation": "再见"}
			]`,
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, resp map[string]interface{}) {
				translations := resp["translations"].([]interface{})
				if len(translations) != 3 {
					t.Errorf("expected 3 translations, got %d", len(translations))
				}
				if translations[0] != "你好" {
					t.Errorf("expected first translation '你好', got %v", translations[0])
				}
			},
		},
		{
			name: "空数组",
			requestBody: map[string]interface{}{
				"texts":      []string{},
				"targetLang": "zh",
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "超过50条",
			requestBody: map[string]interface{}{
				"texts":      make([]string, 51),
				"targetLang": "zh",
			},
			expectedStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{
				llm: &MockLLMGateway{
					response: tt.mockResponse,
				},
			}

			body, _ := json.Marshal(tt.requestBody)
			req := httptest.NewRequest(http.MethodPost, "/api/meetings/translate-batch", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			s.handleMeetingTranslateBatch(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, w.Code)
			}

			if tt.checkResponse != nil && w.Code == http.StatusOK {
				var resp map[string]interface{}
				if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
					t.Fatalf("failed to parse response: %v", err)
				}
				tt.checkResponse(t, resp)
			}
		})
	}
}

func TestDetectLanguage(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		expected string
	}{
		{"中文检测", "你好世界", "zh"},
		{"英文检测", "Hello world", "en"},
		{"日文平假名", "こんにちは", "ja"},
		{"日文片假名", "カタカナ", "ja"},
		{"韩文检测", "안녕하세요", "ko"},
		{"混合文本（中文为主）", "你好Hello世界", "zh"},
		{"空文本", "", "auto"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detectLanguage(tt.text)
			if result != tt.expected {
				t.Errorf("detectLanguage(%q) = %q, want %q", tt.text, result, tt.expected)
			}
		})
	}
}

func TestBuildTranslatePrompt(t *testing.T) {
	tests := []struct {
		name       string
		text       string
		sourceLang string
		targetLang string
		contains   []string
	}{
		{
			name:       "指定源语言",
			text:       "Hello",
			sourceLang: "en",
			targetLang: "zh",
			contains:   []string{"English", "Chinese", "Hello"},
		},
		{
			name:       "自动检测",
			text:       "Hello",
			sourceLang: "auto",
			targetLang: "zh",
			contains:   []string{"Chinese", "Hello"},
		},
		{
			name:       "空源语言",
			text:       "Hello",
			sourceLang: "",
			targetLang: "zh",
			contains:   []string{"Chinese", "Hello"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prompt := buildTranslatePrompt(tt.text, tt.sourceLang, tt.targetLang)
			for _, want := range tt.contains {
				if !strings.Contains(prompt, want) {
					t.Errorf("prompt should contain %q, got: %q", want, prompt)
				}
			}
		})
	}
}
