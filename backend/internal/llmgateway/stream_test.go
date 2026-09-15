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

func TestParseSSEStream_ModelOnlyFrame(t *testing.T) {
	body := strings.NewReader(`data: {"model":"kimi-k2","choices":[{"delta":{}}]}

data: {"model":"kimi-k2","choices":[{"delta":{"content":"hi"}}]}

data: [DONE]
`)
	var models []string
	var content string
	_, err := parseSSEStream(body, func(d StreamDelta) bool {
		if d.Model != "" {
			models = append(models, d.Model)
		}
		content += d.Content
		return true
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if content != "hi" {
		t.Errorf("content=%q want hi", content)
	}
	if len(models) < 1 || models[0] != "kimi-k2" {
		t.Errorf("models=%v want first kimi-k2", models)
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

func TestParseSSEStream_ToolCallsDelta(t *testing.T) {
	// OpenAI streaming tool_calls: index + incremental id/function/arguments
	// Each arguments field is a JSON string that arrives in chunks
	body := strings.NewReader(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_abc","type":"function","function":{"name":"get_weather","arguments":""}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"loc"}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"ation\":\""}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"NYC\"}"}}]}}]}

data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}

data: [DONE]
`)
	var deltas []StreamDelta
	var allDeltas []StreamDelta
	finalUsage, err := parseSSEStream(body, func(d StreamDelta) bool {
		allDeltas = append(allDeltas, d)
		if len(d.ToolCalls) > 0 {
			deltas = append(deltas, d)
		}
		return true
	})
	if err != nil {
		t.Fatalf("parseSSEStream: %v", err)
	}
	t.Logf("Total deltas received: %d, with tool_calls: %d", len(allDeltas), len(deltas))
	for i, d := range allDeltas {
		t.Logf("  Delta %d: content=%q tool_calls=%d finish=%q", i, d.Content, len(d.ToolCalls), d.FinishReason)
		if len(d.ToolCalls) > 0 {
			t.Logf("    ToolCall[0]: id=%q name=%q args=%q", d.ToolCalls[0].ID, d.ToolCalls[0].Function.Name, d.ToolCalls[0].Function.Arguments)
		}
	}
	if len(deltas) != 4 {
		t.Errorf("tool_calls deltas = %d, want 4", len(deltas))
	}
	if len(deltas) > 0 && (deltas[0].ToolCalls[0].ID != "call_abc" || deltas[0].ToolCalls[0].Function.Name != "get_weather") {
		t.Errorf("first delta = %+v, want id=call_abc name=get_weather", deltas[0].ToolCalls[0])
	}
	// Arguments should accumulate: "" + "{\"loc" + "ation\":\"" + "NYC\"}" = "{\"location\":\"NYC\"}"
	if len(deltas) == 4 {
		fullArgs := deltas[0].ToolCalls[0].Function.Arguments +
			deltas[1].ToolCalls[0].Function.Arguments +
			deltas[2].ToolCalls[0].Function.Arguments +
			deltas[3].ToolCalls[0].Function.Arguments
		if fullArgs != `{"location":"NYC"}` {
			t.Errorf("accumulated arguments = %q, want %q", fullArgs, `{"location":"NYC"}`)
		}
	}
	if finalUsage == nil || finalUsage.TotalTokens != 15 {
		t.Errorf("usage = %+v, want 15 tokens", finalUsage)
	}
}

