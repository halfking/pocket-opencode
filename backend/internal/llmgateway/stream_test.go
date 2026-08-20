package llmgateway

import (
	"strings"
	"testing"
)

func TestParseSSEStream(t *testing.T) {
	// Simulated OpenAI-style SSE body with usage in the final frame.
	body := strings.NewReader(`data: {"choices":[{"delta":{"content":"Hello"},"finish_reason":null}]}

data: {"choices":[{"delta":{"content":" world"},"finish_reason":null}]}

data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}

data: [DONE]
`)

	var got []string
	finalUsage, err := parseSSEStream(body, func(d StreamDelta) bool {
		if d.Content != "" {
			got = append(got, d.Content)
		}
		return true
	})
	if err != nil {
		t.Fatalf("parseSSEStream: %v", err)
	}

	if strings.Join(got, "") != "Hello world" {
		t.Errorf("content = %q, want %q", strings.Join(got, ""), "Hello world")
	}
	if finalUsage == nil || finalUsage.TotalTokens != 12 {
		t.Errorf("usage = %+v, want 12 tokens", finalUsage)
	}
}

func TestParseSSEStream_EarlyStop(t *testing.T) {
	// fn returns false after first chunk — parser should stop.
	body := strings.NewReader(`data: {"choices":[{"delta":{"content":"A"}}]}

data: {"choices":[{"delta":{"content":"B"}}]}

`)
	count := 0
	_, err := parseSSEStream(body, func(d StreamDelta) bool {
		count++
		return false // stop immediately
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if count != 1 {
		t.Errorf("invocations = %d, want 1 (early stop)", count)
	}
}

func TestParseSSEStream_MalformedSkipped(t *testing.T) {
	// A malformed line should not abort the stream.
	body := strings.NewReader(`data: {not json}

data: {"choices":[{"delta":{"content":"OK"}}]}

data: [DONE]
`)
	var got string
	_, err := parseSSEStream(body, func(d StreamDelta) bool {
		got += d.Content
		return true
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "OK" {
		t.Errorf("got %q, want OK (malformed line should be skipped)", got)
	}
}
