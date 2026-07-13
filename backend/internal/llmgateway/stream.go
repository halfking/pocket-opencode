package llmgateway

// stream.go — SSE parser for the streaming chat completion.
//
// Splits the parsing out of client.go so it can be unit-tested without a live
// HTTP server. The format is the OpenAI Server-Sent Events convention:
//
//   data: {"choices":[{"delta":{"content":"Hello"},"finish_reason":null}]}
//   data: {"choices":[{"delta":{"content":" world"},"finish_reason":null}]}
//   data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{...}}
//   data: [DONE]
//
// Usage appears only in the final data frame (when stream_options.include_usage
// is set). We surface it via the returned *StreamDelta.

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
)

// parseSSEStream reads the SSE body line by line and invokes fn for each
// delta. Returns the aggregated usage from the final frame (if any).
func parseSSEStream(body io.Reader, fn func(StreamDelta) bool) (*StreamDelta, error) {
	scanner := bufio.NewScanner(body)
	// LLM gateway chunks can be large (tool-call args); bump past 64KB default.
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var finalUsage *StreamDelta

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || !strings.HasPrefix(line, "data: ") {
			continue // SSE comments / keepalives
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var frame struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
				TotalTokens      int `json:"total_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &frame); err != nil {
			// Skip malformed frames rather than killing the whole stream —
			// a single bad chunk shouldn't lose the whole completion.
			continue
		}

		d := StreamDelta{Done: false}
		if len(frame.Choices) > 0 {
			d.Content = frame.Choices[0].Delta.Content
			d.FinishReason = frame.Choices[0].FinishReason
		}
		if frame.Usage != nil {
			d.PromptTokens = frame.Usage.PromptTokens
			d.CompletionTokens = frame.Usage.CompletionTokens
			d.TotalTokens = frame.Usage.TotalTokens
			finalUsage = &StreamDelta{
				PromptTokens:     d.PromptTokens,
				CompletionTokens: d.CompletionTokens,
				TotalTokens:      d.TotalTokens,
			}
		}

		// Empty content + empty finish_reason + no usage = keepalive, skip fn.
		if d.Content == "" && d.FinishReason == "" && frame.Usage == nil {
			continue
		}
		if !fn(d) {
			break // client requested stop
		}
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		return nil, err
	}
	return finalUsage, nil
}
