package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/halfking/pocket-opencode/backend/internal/aigate"
)

// handleMeetingTranslate handles POST /api/meetings/{id}/translate
// Cicada Phase A: 实时字幕单次翻译
func (s *Server) handleMeetingTranslate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text       string `json:"text"`
		SourceLang string `json:"sourceLang"`
		TargetLang string `json:"targetLang"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Text == "" || req.TargetLang == "" {
		writeError(w, http.StatusBadRequest, "text and targetLang required")
		return
	}

	if len(req.Text) > 5000 {
		writeError(w, http.StatusBadRequest, "text too long (max 5000 chars)")
		return
	}

	// 构造翻译 prompt
	prompt := buildTranslatePrompt(req.Text, req.SourceLang, req.TargetLang)

	// 调用 LLM 网关
	ctx := r.Context()
	messages := []aigate.ChatMessage{
		{Role: "system", Content: "You are a professional translator. Translate accurately and naturally, preserving tone and context."},
		{Role: "user", Content: prompt},
	}
	
	translation, err := s.llm.Chat(ctx, "gpt-4o-mini", messages)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("translation failed: %v", err))
		return
	}

	translation = strings.TrimSpace(translation)
	if translation == "" {
		writeError(w, http.StatusBadGateway, "empty translation result")
		return
	}

	// 检测源语言
	detectedLang := req.SourceLang
	if detectedLang == "" || detectedLang == "auto" {
		detectedLang = detectLanguage(req.Text)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"translation":  translation,
		"detectedLang": detectedLang,
		"model":        "gpt-4o-mini",
	})
}

// handleMeetingTranslateBatch handles POST /api/meetings/{id}/translate-batch
// Cicada Phase A: 批量翻译（开启翻译时用）
func (s *Server) handleMeetingTranslateBatch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Texts      []string `json:"texts"`
		SourceLang string   `json:"sourceLang"`
		TargetLang string   `json:"targetLang"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if len(req.Texts) == 0 || len(req.Texts) > 50 {
		writeError(w, http.StatusBadRequest, "texts must have 1-50 items")
		return
	}

	if req.TargetLang == "" {
		writeError(w, http.StatusBadRequest, "targetLang required")
		return
	}

	// 检查总长度
	totalLen := 0
	for _, t := range req.Texts {
		totalLen += len(t)
	}
	if totalLen > 20000 {
		writeError(w, http.StatusBadRequest, "total text too long (max 20000 chars)")
		return
	}

	// 构造批量翻译 prompt (JSON 格式)
	type InputItem struct {
		Index int    `json:"index"`
		Text  string `json:"text"`
	}
	inputs := make([]InputItem, len(req.Texts))
	for i, t := range req.Texts {
		inputs[i] = InputItem{Index: i, Text: t}
	}
	inputJSON, _ := json.Marshal(inputs)

	langNames := map[string]string{
		"zh": "Chinese", "en": "English", "ja": "Japanese",
		"ko": "Korean", "es": "Spanish", "fr": "French", "de": "German",
	}
	targetName := langNames[req.TargetLang]
	if targetName == "" {
		targetName = req.TargetLang
	}

	prompt := fmt.Sprintf(`Translate the following texts to %s. Return a JSON array in the same order.

Input:
%s

Output format: [{"index": 0, "translation": "..."}, ...]`, targetName, string(inputJSON))

	// 调用 LLM
	ctx := r.Context()
	messages := []aigate.ChatMessage{
		{Role: "system", Content: "You are a professional translator. Always respond with valid JSON."},
		{Role: "user", Content: prompt},
	}
	
	result, err := s.llm.Chat(ctx, "gpt-4o-mini", messages)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("translation failed: %v", err))
		return
	}

	// 解析响应
	type OutputItem struct {
		Index       int    `json:"index"`
		Translation string `json:"translation"`
	}
	var outputs []OutputItem
	content := strings.TrimSpace(result)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	if err := json.Unmarshal([]byte(content), &outputs); err != nil {
		writeError(w, http.StatusBadGateway, "failed to parse translation result")
		return
	}

	// 按 index 排序并提取
	translations := make([]string, len(req.Texts))
	for _, out := range outputs {
		if out.Index >= 0 && out.Index < len(translations) {
			translations[out.Index] = out.Translation
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"translations": translations,
		"model":        "gpt-4o-mini",
	})
}

// buildTranslatePrompt 构造翻译 prompt
func buildTranslatePrompt(text, sourceLang, targetLang string) string {
	langNames := map[string]string{
		"zh": "Chinese", "en": "English", "ja": "Japanese",
		"ko": "Korean", "es": "Spanish", "fr": "French", "de": "German",
	}

	targetName := langNames[targetLang]
	if targetName == "" {
		targetName = targetLang
	}

	if sourceLang != "" && sourceLang != "auto" {
		sourceName := langNames[sourceLang]
		if sourceName == "" {
			sourceName = sourceLang
		}
		return fmt.Sprintf("Translate the following %s text to %s:\n\n%s", sourceName, targetName, text)
	}

	return fmt.Sprintf("Translate the following text to %s:\n\n%s", targetName, text)
}

// detectLanguage 简单的语言检测（基于字符集）
func detectLanguage(text string) string {
	cjkCount := 0
	latinCount := 0
	for _, r := range text {
		if r >= 0x4E00 && r <= 0x9FFF { // CJK 统一汉字
			cjkCount++
		} else if (r >= 0x3040 && r <= 0x309F) || (r >= 0x30A0 && r <= 0x30FF) { // 日文平假名/片假名
			return "ja"
		} else if r >= 0xAC00 && r <= 0xD7AF { // 韩文
			return "ko"
		} else if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			latinCount++
		}
	}

	if cjkCount > latinCount {
		return "zh"
	}
	if latinCount > 0 {
		return "en"
	}
	return "auto"
}
