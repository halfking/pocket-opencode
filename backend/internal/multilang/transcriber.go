// Package multilang 提供多语言并行转写功能
package multilang

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Config 多语言转写配置
type Config struct {
	// SupportedLanguages 支持的语言列表
	SupportedLanguages []string
	// EnableAutoDetect 是否启用自动语言检测
	EnableAutoDetect bool
	// EnableTranslation 是否启用翻译
	EnableTranslation bool
	// TargetLanguage 翻译目标语言
	TargetLanguage string
	// STTProvider STT服务提供者配置
	STTProvider map[string]string // language -> provider
}

// TranscriptSegment 转写片段（带语言标识）
type TranscriptSegment struct {
	Language    string  `json:"language"`     // 检测到的语言
	Text        string  `json:"text"`         // 原文
	Translation string  `json:"translation"`  // 译文（如果启用）
	Start       float64 `json:"start"`        // 开始时间（秒）
	End         float64 `json:"end"`          // 结束时间（秒）
	Confidence  float64 `json:"confidence"`   // 置信度
	Speaker     string  `json:"speaker"`      // 说话人（如果有）
}

// MultilingualTranscriber 多语言转写器
type MultilingualTranscriber struct {
	config Config
	mu     sync.RWMutex
}

// NewMultilingualTranscriber 创建多语言转写器
func NewMultilingualTranscriber(cfg Config) *MultilingualTranscriber {
	if len(cfg.SupportedLanguages) == 0 {
		cfg.SupportedLanguages = []string{"zh", "en", "ja", "ko", "es", "fr", "de"}
	}
	if cfg.TargetLanguage == "" {
		cfg.TargetLanguage = "zh" // 默认译为中文
	}
	return &MultilingualTranscriber{
		config: cfg,
	}
}

// TranscribeRequest 转写请求
type TranscribeRequest struct {
	AudioData     []byte   // 音频数据
	SourceLangs   []string // 指定的源语言（空=自动检测）
	EnableTrans   bool     // 是否翻译
	TargetLang    string   // 目标语言
	EnableSpeaker bool     // 是否包含说话人信息
}

// TranscribeResult 转写结果
type TranscribeResult struct {
	Segments       []TranscriptSegment `json:"segments"`
	DetectedLangs  []string            `json:"detected_langs"`  // 检测到的所有语言
	ProcessTimeMS  int64               `json:"process_time_ms"` // 处理时间
	TranslationMap map[string]string   `json:"translation_map"` // 语言对照表
}

// Transcribe 执行多语言并行转写
//
// 流程：
// 1. 语言检测（如果启用）
// 2. 针对每种语言并行调用对应的STT服务
// 3. 合并结果并排序
// 4. 如果启用翻译，并行翻译所有片段
func (m *MultilingualTranscriber) Transcribe(ctx context.Context, req TranscribeRequest) (*TranscribeResult, error) {
	t0 := time.Now()
	
	// 1. 确定要转写的语言
	targetLangs := req.SourceLangs
	if len(targetLangs) == 0 && m.config.EnableAutoDetect {
		// 自动检测语言
		detected, err := m.detectLanguages(ctx, req.AudioData)
		if err != nil {
			return nil, fmt.Errorf("language detection failed: %w", err)
		}
		targetLangs = detected
	}
	
	if len(targetLangs) == 0 {
		targetLangs = []string{"zh"} // 默认中文
	}
	
	// 2. 并行转写每种语言
	var wg sync.WaitGroup
	resultChan := make(chan []TranscriptSegment, len(targetLangs))
	errChan := make(chan error, len(targetLangs))
	
	for _, lang := range targetLangs {
		wg.Add(1)
		go func(language string) {
			defer wg.Done()
			segments, err := m.transcribeLanguage(ctx, req.AudioData, language)
			if err != nil {
				errChan <- fmt.Errorf("%s: %w", language, err)
				return
			}
			resultChan <- segments
		}(lang)
	}
	
	// 等待所有转写完成
	go func() {
		wg.Wait()
		close(resultChan)
		close(errChan)
	}()
	
	// 收集结果
	var allSegments []TranscriptSegment
	for segments := range resultChan {
		allSegments = append(allSegments, segments...)
	}
	
	// 检查错误
	for err := range errChan {
		// 记录错误但不中断（部分语言失败不影响其他）
		fmt.Printf("transcription error: %v\n", err)
	}
	
	// 3. 按时间排序片段
	sortSegmentsByTime(allSegments)
	
	// 4. 如果启用翻译，并行翻译
	if req.EnableTrans || m.config.EnableTranslation {
		targetLang := req.TargetLang
		if targetLang == "" {
			targetLang = m.config.TargetLanguage
		}
		if err := m.translateSegments(ctx, allSegments, targetLang); err != nil {
			// 翻译失败不影响转写结果
			fmt.Printf("translation error: %v\n", err)
		}
	}
	
	// 5. 构建结果
	result := &TranscribeResult{
		Segments:       allSegments,
		DetectedLangs:  targetLangs,
		ProcessTimeMS:  time.Since(t0).Milliseconds(),
		TranslationMap: make(map[string]string),
	}
	
	// 构建语言对照表
	for _, lang := range targetLangs {
		result.TranslationMap[lang] = getLanguageName(lang)
	}
	
	return result, nil
}

// detectLanguages 检测音频中的语言
func (m *MultilingualTranscriber) detectLanguages(ctx context.Context, audio []byte) ([]string, error) {
	// TODO: 实际实现需要调用语言检测服务
	// 这里返回配置的所有支持语言作为占位
	return m.config.SupportedLanguages[:2], nil // 简化：只返回前两种
}

// transcribeLanguage 转写特定语言
func (m *MultilingualTranscriber) transcribeLanguage(ctx context.Context, audio []byte, lang string) ([]TranscriptSegment, error) {
	// TODO: 实际实现需要调用对应语言的STT服务
	// 这里返回模拟数据
	return []TranscriptSegment{
		{
			Language:   lang,
			Text:       fmt.Sprintf("[%s] Sample transcription text", lang),
			Start:      0.0,
			End:        5.0,
			Confidence: 0.95,
		},
	}, nil
}

// translateSegments 并行翻译所有片段
func (m *MultilingualTranscriber) translateSegments(ctx context.Context, segments []TranscriptSegment, targetLang string) error {
	var wg sync.WaitGroup
	
	for i := range segments {
		// 跳过已经是目标语言的片段
		if segments[i].Language == targetLang {
			continue
		}
		
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			// TODO: 实际实现需要调用翻译服务
			segments[idx].Translation = fmt.Sprintf("[Translated to %s] %s", targetLang, segments[idx].Text)
		}(i)
	}
	
	wg.Wait()
	return nil
}

// sortSegmentsByTime 按时间排序片段
func sortSegmentsByTime(segments []TranscriptSegment) {
	// 简单的冒泡排序（生产环境应使用sort.Slice）
	for i := 0; i < len(segments); i++ {
		for j := i + 1; j < len(segments); j++ {
			if segments[i].Start > segments[j].Start {
				segments[i], segments[j] = segments[j], segments[i]
			}
		}
	}
}

// getLanguageName 获取语言名称
func getLanguageName(code string) string {
	names := map[string]string{
		"zh": "中文",
		"en": "English",
		"ja": "日本語",
		"ko": "한국어",
		"es": "Español",
		"fr": "Français",
		"de": "Deutsch",
	}
	if name, ok := names[code]; ok {
		return name
	}
	return code
}
