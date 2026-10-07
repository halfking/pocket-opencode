package stt

// JSON 契约护栏（§39）：`FullResult.Segments` 的 wire 形状 ↔ 前端
// `SttFullSegment` / `attributeFullTranscript`。
//
// ── 为什么这道门必要：错法是**静默**的 ──
// Go 的 json tag 与前端的 TS 字段是分别手写的，没有工具检查两边一致。
// 这次不是「界面少个字段」那么轻 —— §36 修的正是「把逐段回执挂回说话人」，
// 而它靠的是 `startSec` / `endSec`：
//
//	若 Go 改名而前端没跟 → 前端拿到 undefined
//	→ attributeFullTranscript 里的 toMs(undefined) = Number.isFinite(undefined) ? … : 0
//	→ 两条判据都归到 0，**每一条回执都变成 [0, 0]**
//	→ 不抛错、不告警，只是归属全部退化成「都挂到第一段」
//
// 也就是说：改一个 json tag 的名字，整条 §36 的修复会**安静地变成摆设**，
// 而 `go test` 与 `npm run gates` 全绿。
//
// ── 与本仓既有约定的关系 ──
// `internal/calendar/json_contract_test.go` 的做法是「对着 TS 抄一遍字段名」。
// 本文件多走一步：**fixture 由本测试真实 marshal 产出**，前端测试读同一个文件。
// ⇒ 不存在「手抄漂移」这一类问题；手抄能抄错的只有期望值，而期望值是本文件
// 里那两行白名单，改它需要同时改测试与本文件，diff 里看得见。
//
// 更新 fixture（**只在有意改动 wire 形状时**）：
//	UPDATE_FIXTURE=1 go test ./internal/stt/ -run TestFullSegmentsWireFixture
import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// wireFixturePath 指向前端测试读的同一个文件。
//
// 路径是**从本包目录**数起（`backend/internal/stt` → 仓库根三级）。
// 跨仓引用在本仓已有先例（internal/calendar/json_contract_test.go 抄 TS 接口、
// 前端 refine-fallback-notice.test.ts 反过来读 Go 源码），两边都在一个仓里。
const wireFixturePath = "../../../frontend/src/api/__tests__/fixtures/transcribe-full-segments.json"

// wireSample 是 fixture 的内容来源：**形态贴近真实**——
// 两条正常段 + 一条失败段（失败段没有 text、只有 error），
// 时间范围用非整数（11.5 秒），因为整数的秒最能掩盖「秒→毫秒」换算被改成别的。
//
// ★ 失败段是**必须**进 fixture 的：`text` 带 omitempty，
// 失败段的 JSON 里根本没有这个键。前端若假设 text 恒存在，
// 这里就会露出「text 为空时拿右双引号顶上」那个 .trim() 兜底是不是真的兜住了。
//
//	⚠ 被引的那一行原文用的是 U+201D，本注释**不复现该字符**：本文件一旦入库，
//	  check-smart-quotes（backend.yml 的 build-gate 硬步骤，continue-on-error: false）
//	  会把它当成落单弯引号而当场转红 —— 而那个形态在真实代码里已经不存在了。
func wireSample() FullResult {
	return FullResult{
		Text: "这个季度的续费方案基本上已经定了［第 2 段转写失败：upstream 503］没签的那两个走线下",
		Segments: []SegmentResult{
			{Index: 0, StartSec: 0, EndSec: 11.5, Text: "这个季度的续费方案基本上已经定了"},
			{Index: 1, StartSec: 11.5, EndSec: 22, Error: "upstream 503"},
			{Index: 2, StartSec: 22, EndSec: 28, Text: "没签的那两个走线下"},
		},
		Failed:     1,
		Succeeded:  2,
		DurationMS: 28000,
	}
}

// TestFullSegmentsWireFixture 是本文件的主门：真实 marshal → 比对 fixture。
func TestFullSegmentsWireFixture(t *testing.T) {
	raw, err := json.MarshalIndent(wireSample(), "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	abs := filepath.Clean(wireFixturePath)
	if os.Getenv("UPDATE_FIXTURE") == "1" {
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(abs, append(raw, '\n'), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		t.Logf("fixture 已更新：%s", abs)
		return
	}
	want, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("读不到 fixture（首次请跑 UPDATE_FIXTURE=1）：%v", err)
	}
	if string(raw)+"\n" != string(want) {
		t.Fatalf("wire 形状与前端共用的 fixture 不一致。\n--- 实际 marshal ---\n%s\n--- fixture 文件 ---\n%s\n"+
			"前端 attributeFullTranscript 靠 startSec/endSec 归属；"+
			"若这里红了而你没改前端，说明两端已经对不上，§36 的修复会静默退化成「全挂第一段」。",
			raw, want)
	}
}

// TestFullSegmentKeyWhitelist 钉字段名白名单，并额外检查**值的类型**。
//
// 为什么要单独查类型：`omitempty` + float64 的组合下，
// `StartSec: 0` 会被整个丢掉（"omitempty 对 0 值生效"），
// 于是第一段的 start 在 JSON 里**根本不存在**。这与「字段名写错」是同一种
// 静默失效，但读数长得完全不一样。
func TestFullSegmentKeyWhitelist(t *testing.T) {
	// 刻意把 StartSec/EndSec 取非零值，确保它们一定会被序列化。
	s := SegmentResult{Index: 0, StartSec: 0.5, EndSec: 11.5, Text: "x"}
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// 与 frontend/src/api/stt-settings.ts 的 SttFullSegment 逐字对应。
	want := map[string]bool{"index": true, "startSec": true, "endSec": true, "text": true}
	for k := range want {
		if _, ok := m[k]; !ok {
			t.Errorf("SegmentResult 的 JSON 缺字段 %q；前端 SttFullSegment 声明了它。实际键：%v", k, m)
		}
	}
	for k := range m {
		if !want[k] {
			t.Errorf("SegmentResult 的 JSON 多出前端没有的字段 %q", k)
		}
	}
	// 秒必须是**数字**：若某次重构把它改成 string（例如为了避开浮点显示），
	// 前端 toMs 会拿到字符串，`Number.isFinite("11.5")` 为 false → 归 0。
	for _, k := range []string{"startSec", "endSec"} {
		switch m[k].(type) {
		case float64:
		default:
			t.Errorf("%s 应序列化为 number，实际 %T（前端 toMs 遇到非 number 会静默归 0）", k, m[k])
		}
	}
}
