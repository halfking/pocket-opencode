package llmgateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// refine/analyze 客户端的往返与错误映射（网关契约锁，2026-10-06 轮）。
func TestRefineAndAnalyzeClient(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/v1/audio/refine":
			_, _ = w.Write([]byte(`{"refined":"大家好，讨论预算，50%要砍。","corrections":[{"from":"百分之五十","to":"50%","reason":"ITN"}],"ignored_hotwords":["达摩院"],"llm_model":"m"}`))
		case "/v1/audio/analyze":
			_, _ = w.Write([]byte(`{"summary":"滚动摘要","key_points":["a"],"hints":["h"],"action_items":[{"text":"t","owner":"张三"}],"topics":["预算"]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "sk-test")

	rr, err := c.RefineTranscription(context.Background(), RefineRequest{Model: "m", Text: "原文", Hotwords: []string{"达摩院"}})
	if err != nil {
		t.Fatalf("refine: %v", err)
	}
	if gotPath != "/v1/audio/refine" || gotAuth != "Bearer sk-test" {
		t.Fatalf("path=%s auth=%s", gotPath, gotAuth)
	}
	if rr.Refined == "" || len(rr.Corrections) != 1 || len(rr.IgnoredWords) != 1 || rr.IgnoredWords[0] != "达摩院" {
		t.Fatalf("refine out = %+v", rr)
	}

	ar, err := c.AnalyzeTranscription(context.Background(), AnalyzeRequest{Model: "m", Transcript: "片段", PriorSummary: "旧摘要", Style: "meeting"})
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if ar.Summary != "滚动摘要" || len(ar.Hints) != 1 || len(ar.ActionItems) != 1 || ar.ActionItems[0].Owner != "张三" {
		t.Fatalf("analyze out = %+v", ar)
	}

	if _, err := c.RefineTranscription(context.Background(), RefineRequest{Model: "m"}); err == nil {
		t.Fatal("empty text must error")
	}
	if _, err := c.AnalyzeTranscription(context.Background(), AnalyzeRequest{Transcript: "x"}); err == nil {
		t.Fatal("empty model must error")
	}
}

// 网关 4xx（如智谱余额 1113）按错误码透出，调用方可按 status 分流。
func TestTransformClientSurfacesGatewayError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":"upstream_rate_limited","message":"Upstream audio provider rate limited: 余额不足"}}`))
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "sk-test")
	_, err := c.RefineTranscription(context.Background(), RefineRequest{Model: "m", Text: "x"})
	if err == nil {
		t.Fatal("429 must surface as error")
	}
	if want := "upstream_rate_limited"; !contains(err.Error(), want) {
		t.Fatalf("err = %v, want contains %q", err, want)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
