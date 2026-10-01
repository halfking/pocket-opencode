package stt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// scriptedASR 按调用顺序返回预设文本，用于精确控制多段拼接结果。
type scriptedASR struct {
	mu    sync.Mutex
	texts []string
	idx   int
}

func (s *scriptedASR) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseMultipartForm(32 << 20)
	s.mu.Lock()
	defer s.mu.Unlock()
	txt := "fallback"
	if s.idx < len(s.texts) {
		txt = s.texts[s.idx]
	}
	s.idx++
	_, _ = w.Write([]byte(`{"text":"` + txt + `"}`))
}

func newIncremental(srvURL string) *IncrementalTranscriber {
	tr := NewResolver(func(context.Context, Scope) (*Target, error) {
		return &Target{
			BaseURL: srvURL, APIKey: "k", Model: "stub-model",
			Transport: TransportTranscriptions, Channel: ChannelExternal,
		}, nil
	})
	return NewIncrementalTranscriber(tr)
}

func toneChunk(start, end float64) IncrementalChunk {
	return IncrementalChunk{
		Audio: continuousWAV(16000, 1), StartSec: start, EndSec: end, SilenceCut: true,
	}
}

func TestIncrementalProducesDeltaNotFullText(t *testing.T) {
	// scriptedASR 每段返回固定文本，delta 应为「新出现的那部分」而不是全量。
	stub := &scriptedASR{texts: []string{"甲", "乙"}}
	srv := httptest.NewServer(stub)
	defer srv.Close()

	it := newIncremental(srv.URL)
	ctx := context.Background()

	r1, err := it.TranscribeChunk(ctx, Scope{}, toneChunk(0, 1), false)
	if err != nil {
		t.Fatalf("第 1 段失败: %v", err)
	}
	if r1.Text != "甲" || r1.Delta != "甲" {
		t.Errorf("第 1 段 text=%q delta=%q，期望都是「甲」", r1.Text, r1.Delta)
	}

	r2, err := it.TranscribeChunk(ctx, Scope{}, toneChunk(1, 2), false)
	if err != nil {
		t.Fatalf("第 2 段失败: %v", err)
	}
	// Text 是累计文本（前端覆盖显示用），Delta 是相对上次的增量。
	// 「甲」与「乙」无字符重叠，所以累计为「甲乙」，增量为「乙」。
	if r2.Text != "甲乙" {
		t.Errorf("第 2 段 text=%q，期望累计「甲乙」", r2.Text)
	}
	if r2.Delta != "乙" {
		t.Errorf("第 2 段 delta=%q，期望只有新增的「乙」（前端据此只追加不重排）", r2.Delta)
	}
}

func TestIncrementalResetsBetweenSessions(t *testing.T) {
	stub := &scriptedASR{texts: []string{"甲", "乙", "丙"}}
	srv := httptest.NewServer(stub)
	defer srv.Close()

	it := newIncremental(srv.URL)
	ctx := context.Background()
	first, err := it.TranscribeChunk(ctx, Scope{}, toneChunk(0, 1), false)
	if err != nil {
		t.Fatal(err)
	}
	if first.Text == "" {
		t.Fatal("第 1 段应出字")
	}
	// 新会话必须清空累积，否则第二场会议的文字会接着第一场显示
	it.Reset()
	r, err := it.TranscribeChunk(ctx, Scope{}, toneChunk(0, 1), false)
	if err != nil {
		t.Fatal(err)
	}
	if r.Text != "乙" {
		t.Errorf("Reset 后应从零开始（只含本段文本），实际 %q", r.Text)
	}
	if strings.Contains(r.Text, first.Text) {
		t.Errorf("Reset 后仍含上一会话文本 %q，说明累积未清空", first.Text)
	}
}

func TestIncrementalSegmentFailureKeepsPreviousText(t *testing.T) {
	// 关键契约：一段失败不能让此前已经出字的即时文本消失。
	var hit int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit++
		if hit == 1 {
			_, _ = w.Write([]byte(`{"text":"已经转写好的内容"}`))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"boom"}}`))
	}))
	defer srv.Close()

	it := newIncremental(srv.URL)
	ctx := context.Background()
	r1, _ := it.TranscribeChunk(ctx, Scope{}, toneChunk(0, 1), false)
	if r1.Text == "" {
		t.Fatal("第 1 段应出字")
	}

	r2, err := it.TranscribeChunk(ctx, Scope{}, toneChunk(1, 2), false)
	if err != nil {
		t.Fatalf("单段失败不应让整个调用返回错误: %v", err)
	}
	if r2.Error == "" {
		t.Error("单段失败应在结果里带 Error，供前端提示")
	}
	if r2.Text != r1.Text {
		t.Errorf("段失败后已产出文本必须保留: 之前=%q 现在=%q", r1.Text, r2.Text)
	}
}

func TestIncrementalEmptyChunkIsNotAnError(t *testing.T) {
	stub := &scriptedASR{texts: []string{}}
	srv := httptest.NewServer(stub)
	defer srv.Close()

	it := newIncremental(srv.URL)
	r, err := it.TranscribeChunk(context.Background(), Scope{}, IncrementalChunk{}, false)
	if err != nil {
		t.Fatalf("纯静音产生的空切片不应报错: %v", err)
	}
	if r.Text != "" {
		t.Errorf("空切片不应产出文本，实际 %q", r.Text)
	}
}

func TestIncrementalFinalFlagPropagates(t *testing.T) {
	stub := &scriptedASR{texts: []string{"甲"}}
	srv := httptest.NewServer(stub)
	defer srv.Close()

	it := newIncremental(srv.URL)
	r, _ := it.TranscribeChunk(context.Background(), Scope{}, toneChunk(0, 1), true)
	if !r.IsFinal {
		t.Error("IsFinal 未透传到结果")
	}
}

func TestIncrementalRejectsUnconfiguredEngine(t *testing.T) {
	it := NewIncrementalTranscriber(nil)
	if _, err := it.TranscribeChunk(context.Background(), Scope{}, toneChunk(0, 1), false); err == nil {
		t.Error("未配置的引擎应报错")
	}
	// nil 接收者也不能 panic
	var nilIt *IncrementalTranscriber
	if _, err := nilIt.TranscribeChunk(context.Background(), Scope{}, toneChunk(0, 1), false); err == nil {
		t.Error("nil 接收者应报错而不是 panic")
	}
}

func TestMergeIncrementalRemovesOverlap(t *testing.T) {
	// 这是即时转写最容易出错的地方：切片有 1 秒重叠，直接拼接会出现
	//「今天今天下午三点」这种明显错误。
	cases := []struct {
		name            string
		committed, next string
		want            string
	}{
		{"空历史直接取新段", "", "今天下午三点开会", "今天下午三点开会"},
		{"新段为空保持不变", "今天下午三点开会", "", "今天下午三点开会"},
		{"完全重复不叠加", "今天下午三点", "今天下午三点", "今天下午三点"},
		{"重叠部分去重", "今天下午三点开项目评审会", "三点开项目评审会请准备报告", "今天下午三点开项目评审会请准备报告"},
		{"无重叠原样拼接", "今天下午三点", "请准备进度报告", "今天下午三点请准备进度报告"},
		{"短于阈值不误判", "预算", "算完成", "预算算完成"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mergeIncremental(tc.committed, tc.next); got != tc.want {
				t.Errorf("mergeIncremental(%q, %q) = %q，期望 %q",
					tc.committed, tc.next, got, tc.want)
			}
		})
	}
}

func TestMergeIncrementalDoesNotDropRealContent(t *testing.T) {
	// 误去重的代价是丢掉真实内容，代价高于多几个重复字。
	//
	// 契约是「重叠判定上限 40 rune」：更短的重复视为切片重叠（去重），
	// 超过上限的重复视为真实重复说话（保留两份）。
	short := strings.Repeat("甲", 30)
	if got := mergeIncremental(short, short); got != short {
		t.Errorf("30 rune 重复应判为切片重叠并去重，实际长度 %d，期望 %d", len([]rune(got)), len([]rune(short)))
	}

	long := strings.Repeat("甲", 60)
	if got := mergeIncremental(long, long); len([]rune(got)) != 120 {
		t.Errorf("60 rune 重复应视为真实重复说话并保留两份，实际长度 %d，期望 120", len([]rune(got)))
	}
}

func TestMergeIncrementalHandlesChineseRuneBoundaries(t *testing.T) {
	// 按字节切会劈开 UTF-8 字符，表现为「甲」接「乙」丢掉「乙」。
	// 这条锁住按 rune 切的实现：单字不构成重叠，两个相同字才构成。
	cs := []rune("今天下午三点开项目评审会")
	ns := []rune("三点开项目评审会请准备进度报告")
	if got := mergeIncremental(string(cs), string(ns)); got != "今天下午三点开项目评审会请准备进度报告" {
		t.Errorf("中文重叠去重失败: %q", got)
	}
	// 无重叠的相邻中文不应被误判
	if got := mergeIncremental("我", "好"); got != "我好" {
		t.Errorf("单字相邻不应被误判成重叠: %q", got)
	}
}
