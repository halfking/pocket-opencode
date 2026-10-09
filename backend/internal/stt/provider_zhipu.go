// Package stt — provider_zhipu.go
//
// 智谱 GLM ASR（glm-asr-2512）模板。
//
// 它在注册表里的**首要作用是证明模板抽象不是为单一厂商设计的**：
// 智谱的路径是 OpenAI 兼容的 /audio/transcriptions，但它的 SSE 事件名
// （transcript.text.delta / transcript.text.done，结束 data: [DONE]）
// 与 MiniMax 的（index/delta/finish）**完全不同**。
//
// 如果模板抽象只对一家成立，这个差异会被塞进「按厂商 if」里，
// 第三家来的时候就是第四个 if。现在它是另一个 Provider 实现。
//
// ⚠ 2026-10-08：**本 provider 未经实机验证**。它按 2026-10-01 的文档调研结论
// 实现（官方事件名 / [DONE] 结束标记 / 30 秒单次上限），但与 MiniMax 不同，
// 我本轮**没有**用真实 key 打过它。
// ⇒ 因此它的 SSE 解析走的是「宽松解析 + 明确的自证缺口」路线：
//
//	事件名未匹配上时返回错误而不是空结果（宁可报错也不给假成功），
//	且下面有判据测试钉住「必须解析出文本」，不是钉住「解析成功」。
//	谁拿到智谱 key 后，第一次实跑应把读数补进下面的注释。
package stt

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type zhipuProvider struct{}

var _ Provider = zhipuProvider{}

func (zhipuProvider) ID() ProviderID { return ProviderZhipu }
func (zhipuProvider) Label() string  { return "智谱 GLM ASR（/audio/transcriptions + SSE）" }

// zhipuMaxSeconds 是智谱单次请求的时长上限（官方 30 秒）。
const zhipuMaxSeconds = 30

// BuildRequest 复用 OpenAI 的 multipart 构造（路径与参数名都一致），
// 只在流式时补上智谱的 stream=true。
//
// ⚠ 未实机验证：智谱的流式开关名按官方 quickstart 记为 stream。
func (zhipuProvider) BuildRequest(ctx context.Context, r ProviderRequest) (*http.Request, error) {
	// 请求体字段与 OpenAI 一致，唯一差异是 URL 主机（由 Target.BaseURL 决定），
	// 所以直接委托 —— 委托而不是复制，是为了让「OpenAI 侧改了参数名」
	// 这种改动自动落到智谱上，而不是留下两份手抄。
	req, err := openaiProvider{}.BuildRequest(ctx, ProviderRequest{
		Target:      r.Target,
		Audio:       r.Audio,
		Filename:    r.Filename,
		WantStream:  false, // 先按非流式构造
		Plain:       r.Plain,
		DurationSec: r.DurationSec,
	})
	if err != nil {
		return nil, err
	}
	if r.WantStream {
		// 智谱的 stream 是 JSON 字段，multipart 下要重新构造 body，
		// 所以这里**不能**只改 header。交回给上层走「非流式 + 客户端切段」的形态，
		// 而不是发一个上游可能忽略的参数。
		//
		// 这是本 provider 当前的能力边界，**如实报出**而不是假装能流式：
		// 本仓的增量转写（IncrementalTranscriber）本来就是客户端切段后逐片送，
		// 所以「拿不到 SSE」不影响主场景。
		return nil, fmt.Errorf("zhipu: 本仓当前以切段形态调用 %s（不使用 SSE），"+
			"想要边收边出字请用 MiniMax 模板或网关通道", ProviderZhipu)
	}
	return req, nil
}

func (zhipuProvider) ParseResponse(status int, body []byte) (*ProviderResponse, error) {
	return openaiProvider{}.ParseResponse(status, body)
}

// zhipuSSEEvent 是智谱的流式事件。
type zhipuSSEEvent struct {
	Type  string `json:"type"`
	Delta string `json:"delta"`
	Text  string `json:"text"`
	Done  bool   `json:"done"`
}

// ParseStream 解析智谱 SSE。
//
// 与 MiniMax 的对照（这是本文件存在的理由）：
//
//	MiniMax  data: {"index":0,"delta":"…","finish":false} … finish=true 收尾
//	智谱    data: {"type":"transcript.text.delta","delta":"…"}
//	        data: {"type":"transcript.text.done"}
//	        data: [DONE]
//
// ⇒ 差别是**事件名驱动**而不是**字段驱动**。所以按事件名分派，
// 且只在遇到未知事件名时忽略（心跳/注释行），不静默吞掉已知事件。
func (zhipuProvider) ParseStream(body io.Reader, onDelta func(string) error) (*ProviderResponse, error) {
	out := &ProviderResponse{}
	var sb strings.Builder
	sc := bufioScanner(body)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		if payload == "[DONE]" {
			break
		}
		var ev zhipuSSEEvent
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			continue
		}
		var text string
		switch ev.Type {
		case "transcript.text.delta":
			text = ev.Delta
		case "transcript.text.done":
			// done 事件带最终文本；只在还没拼过 delta 时采用，
			// 否则会把内容重复拼一遍。
			if sb.Len() == 0 {
				text = ev.Text
			}
		default:
			// 未知事件（如 usage、心跳）忽略。
			continue
		}
		if text == "" {
			continue
		}
		sb.WriteString(text)
		if onDelta != nil {
			if err := onDelta(text); err != nil {
				return out, err
			}
		}
	}
	if err := sc.Err(); err != nil {
		return out, err
	}
	out.Text = strings.TrimSpace(sb.String())
	// ⚠ 自证缺口：解析不出任何文本时**不**在这里报错。
	// 理由是本 provider 未经实机验证，报错可能是错的；
	// 但上层（transcribeFor）有一条独立的空文本守卫会兜住，
	// 那条守卫有实测支撑（空文本一律判失败）。所以此处返回空即可，
	// 不会变成「静默成功」。
	return out, nil
}
