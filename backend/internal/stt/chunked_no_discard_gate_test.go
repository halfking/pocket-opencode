// chunked_no_discard_gate_test.go — §120：**切块链路不许为丢弃的增强特性付费**。
//
// ── 缺口的事实链 ───────────────────────────────────────────────────
//
// buildVerboseOptions（transcribe.go:240-265）按「模型能力 + 本次音频时长」
// 自动开 verbose_json / 词级时间戳 / provider.diarization。
// 在 `microsoft/mai-transcribe-2` 上，**每一块**都满足开启条件
// （chunk 时长来自 wavDurationSeconds(audio)，见 transcribe.go:295），
//
//	⇒ TranscribeFull         每块都请求分离与词级时间戳
//	⇒ IncrementalTranscriber 每块都请求分离与词级时间戳
//
// 而两个调用方**都只读 Result.Text**：
//
//	SegmentResult（full.go:70）   只有 Index/StartSec/EndSec/Text/Error
//	IncrementalResult            没有任何说话人字段
//
// ⇒ 付了钱（verbose 响应更大、更慢，还多一条 408/500/503 的面）
//
//	拿回来原样扔掉。
//
// ── 为什么是「不请求」而不是「接上去」 ──────────────────────────────
//
// §31.3 已经论证：服务端分离要在**整段音频**上做才有意义（跨段一致），
// 而这两条链路都在送出去之前先切碎了 ⇒ 拼出来的是碎的说话人身份。
// §31.3 还记着一次教训：把 segments 加进前端类型后查消费者，**零个**，
// 于是那次改动被撤回。对称的做法是别为扔掉的东西付钱，而不是再接一遍
// 没人读的数据（否则就是第二次造一个死能力）。
//
// ★ 判据形态：断言落在**假上游真正收到的 multipart 字段**上，
//
//	不做源码文本匹配 —— 与同包 diarization_test.go 同一约定。
package stt

import (
	"context"
	"net/http/httptest"
	"strconv"
	"testing"
)

// plainUpstream 是一个只回纯文本的假上游：它**记录**收到的字段，
// 但不会替任何实现兜底 —— 开不开分离开参全看请求本身。
func plainUpstream() *diarizationUpstream {
	return &diarizationUpstream{replies: []diarizationReply{{
		status: 200,
		body:   `{"text":"张三负责结算，李四跟进排期。"}`,
	}}}
}

// assertPlain 断言这一次请求**没有**要那些调用方读不到的增强特性。
func assertPlain(t *testing.T, got capturedTranscription, where string) {
	t.Helper()
	if got.responseFormat != "json" {
		t.Errorf("%s: response_format = %q，期望 json —— 本路径只读 .Text，"+
			"要 verbose_json 等于为丢弃的字段付费", where, got.responseFormat)
	}
	if got.wordGranularity != "" {
		t.Errorf("%s: 仍发了 timestamp_granularities[] = %q —— "+
			"词级时间戳在本路径没有落点（SegmentResult 无该字段）", where, got.wordGranularity)
	}
	if got.provider != "" {
		t.Errorf("%s: 仍发了 provider = %q —— 本路径不读说话人，"+
			"却要多付一次上游 408/500/503 diarization_unavailable 的面", where, got.provider)
	}
}

// TestTranscribeFullDoesNotRequestDiscardedEnhancements ——
// 收尾全量重转（两阶段架构的第二阶段，最终转写就出自这里）。
func TestTranscribeFullDoesNotRequestDiscardedEnhancements(t *testing.T) {
	up := plainUpstream()
	srv := httptest.NewServer(up.handler(t))
	defer srv.Close()

	eng := engineFor(t, srv, maiTarget(srv))
	// 26 秒连续音频 ⇒ 会被切成 ≥2 块（默认段长 25 秒），
	// 走的正是「逐块 TranscribeFor」那条路径。
	res, err := eng.TranscribeFull(context.Background(), Scope{}, continuousWAV(16000, 26), "m.wav")
	if err != nil {
		t.Fatalf("全量转写失败: %v", err)
	}

	// 夹具自证：块数必须 ≥2，否则下面遍历到的是「不可切分」分支，量具失明。
	if len(up.calls) < 2 {
		t.Fatalf("夹具未生效：26 秒音频应切出 ≥2 块，实际只发了 %d 次请求 —— "+
			"这条门会退化成只测单块路径", len(up.calls))
	}
	for i, got := range up.calls {
		assertPlain(t, got, "TranscribeFull 第 "+strconv.Itoa(i)+" 块")
	}
	// ⚠ 反向自证：文本必须还在。「不请求增强」不是「不转写」。
	if res == nil || res.Text == "" {
		t.Fatal("全量转写结果为空 —— 优化请求参数绝不能以丢文字为代价")
	}
}

// TestIncrementalTranscribeDoesNotRequestDiscardedEnhancements ——
// 实时增量链（VAD 切片后每块约 5~8 秒）。
func TestIncrementalTranscribeDoesNotRequestDiscardedEnhancements(t *testing.T) {
	up := plainUpstream()
	srv := httptest.NewServer(up.handler(t))
	defer srv.Close()

	tr := NewResolver(func(context.Context, Scope) (*Target, error) {
		return maiTarget(srv), nil
	})
	inc := NewIncrementalTranscriber(tr)

	// 喂一块短音频，走「短切片直接单次转写」那条快路径。
	_, err := inc.TranscribeChunk(context.Background(), Scope{}, IncrementalChunk{
		Audio: continuousWAV(16000, 6), StartSec: 0, EndSec: 6, SilenceCut: true,
	}, false)
	if err != nil {
		t.Fatalf("增量转写失败: %v", err)
	}

	if len(up.calls) != 1 {
		t.Fatalf("应只发一次请求，实际 %d 次（夹具可能没走到目标路径）", len(up.calls))
	}
	assertPlain(t, up.calls[0], "IncrementalTranscriber")
}

// TestSingleShotStillRequestsDiarization —— ★ 反向保护。
//
// 上一条门有把刀刃朝内的可能：一刀把「不请求」推广到**所有**调用方，
// 那样 diarization_test.go 的三条断言会红，于是有人「顺手」把
// 单发路径也关掉 —— 而单发路径（server_stt_stream.go:98）是**真的**
// 会把 segments 吐出去的。
//
// 这条测试钉住「只关切块链路，不关单发链路」。
func TestSingleShotStillRequestsDiarization(t *testing.T) {
	up := &diarizationUpstream{replies: []diarizationReply{{
		status: 200,
		body:   `{"text":"ok","segments":[{"speaker":"0","text":"ok","start":0,"end":1}]}`,
	}}}
	srv := httptest.NewServer(up.handler(t))
	defer srv.Close()

	eng := engineFor(t, srv, maiTarget(srv))
	if _, err := eng.Transcribe(context.Background(), continuousWAV(16000, 8), "m.wav"); err != nil {
		t.Fatalf("单发转写失败: %v", err)
	}
	if len(up.calls) != 1 {
		t.Fatalf("应只发一次请求，实际 %d 次", len(up.calls))
	}
	got := up.calls[0]
	if got.responseFormat != "verbose_json" {
		t.Errorf("单发路径的 response_format = %q，期望 verbose_json —— "+
			"server_stt_stream.go 会把 segments 吐给前端，这里关掉就真的没人再开了",
			got.responseFormat)
	}
	if got.provider == "" {
		t.Error("单发路径仍应请求 diarization（它会真的把 segments 透出）")
	}
}

// TestTranscribeFullNonSplittableDoesNotRequestDiscardedEnhancements ——
// 「不可切分」那条分支（webm / mp4 / mp3 / 非 16-bit PCM）。
//
// ★ 这条是被变异逼出来的：第一版只覆盖了可切分的 WAV 分支，
//
//	于是把**不可切分**分支的 forcePlain 改回 false 时门全绿。
//	⇒ 与 §105/§116/§118 同一课：门要覆盖的是**出事的那一处**。
//
// ⚠ 这条分支上 diarization 本来就关着（durationSec=0 → ShouldRequestDiarization
//
//	直接返回 false，见 full.go:566-575），所以修复前它仍然多发的是
//	**词级时间戳 + verbose_json** —— 断言因此必须盯这两项，不能只盯 provider。
func TestTranscribeFullNonSplittableDoesNotRequestDiscardedEnhancements(t *testing.T) {
	up := plainUpstream()
	srv := httptest.NewServer(up.handler(t))
	defer srv.Close()

	eng := engineFor(t, srv, maiTarget(srv))
	// 非 PCM 载荷 ⇒ SplitWAV 判定不可切 ⇒ 走整段单次转写那条分支。
	res, err := eng.TranscribeFull(context.Background(), Scope{}, []byte("not-a-pcm-webm-payload"), "m.webm")
	if err != nil {
		t.Fatalf("全量转写失败: %v", err)
	}
	if len(up.calls) != 1 {
		t.Fatalf("应只发一次请求，实际 %d 次（夹具可能没走到不可切分分支）", len(up.calls))
	}
	assertPlain(t, up.calls[0], "TranscribeFull 不可切分分支")
	if res == nil || res.Text == "" {
		t.Fatal("全量转写结果为空 —— 优化请求参数绝不能以丢文字为代价")
	}
}
