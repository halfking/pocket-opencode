package server

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/multilang"
)

// handleMultilingualTranscribe 处理多语言转写请求
//
// POST /api/meetings/:id/transcribe/multilingual
// 请求体: multipart/form-data
//   - audio: 音频文件
//   - languages: 逗号分隔的语言列表（可选，空=自动检测）
//   - enable_translation: 是否翻译（可选，默认false）
//   - target_language: 目标语言（可选，默认zh）
func (s *Server) handleMultilingualTranscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}

	// 解析表单
	if err := r.ParseMultipartForm(100 << 20); err != nil { // 100MB
		writeError(w, http.StatusBadRequest, "parse form: "+err.Error())
		return
	}

	// 读取音频文件
	file, _, err := r.FormFile("audio")
	if err != nil {
		writeError(w, http.StatusBadRequest, "audio file required")
		return
	}
	defer file.Close()

	audioData, err := io.ReadAll(io.LimitReader(file, 100<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read audio: "+err.Error())
		return
	}

	// 解析参数
	var sourceLangs []string
	if langStr := r.FormValue("languages"); langStr != "" {
		sourceLangs = strings.Split(langStr, ",")
	}

	enableTrans := r.FormValue("enable_translation") == "true"
	targetLang := r.FormValue("target_language")
	if targetLang == "" {
		targetLang = "zh"
	}

	// 执行多语言转写
	transcriber := multilang.NewMultilingualTranscriber(multilang.Config{
		SupportedLanguages: []string{"zh", "en", "ja", "ko", "es", "fr", "de"},
		EnableAutoDetect:   len(sourceLangs) == 0,
		EnableTranslation:  enableTrans,
		TargetLanguage:     targetLang,
	})

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	result, err := transcriber.Transcribe(ctx, multilang.TranscribeRequest{
		AudioData:     audioData,
		SourceLangs:   sourceLangs,
		EnableTrans:   enableTrans,
		TargetLang:    targetLang,
		EnableSpeaker: true,
	})

	if err != nil {
		writeError(w, http.StatusInternalServerError, "transcription failed: "+err.Error())
		return
	}

	// 返回结果
	writeJSON(w, http.StatusOK, result)
}
