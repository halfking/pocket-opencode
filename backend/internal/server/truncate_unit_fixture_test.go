package server

// §245.8 截断单位判据的**真值侧**。
//
// 为什么需要这个文件（§245.7 自记的缺口）：
//   前端那道门 `truncate-unit-parity-244.test.mjs` 要量「后端按字节、前端按码元」，
//   但它在 **JS 里复刻**了 Go 的 truncateStr 再去比 ——
//   ⇒ 量的是「我自己写的 JS 实现 vs 我自己写的 JS 实现」。
//   如果 Go 侧语义变了（改成按字符、或加了省略号），
//   **那道门不会知道**，它会继续报一个已经不存在的缺口。
//
// 这里把真身固化成一份 fixture：同一批语料 → 真 Go 的真实输出。
// 前端那道门读这份 fixture 做比对，于是它量的对象变成产品代码本身。
//
// ⚠ 两处刻意的设计：
//   ① 用**同一批语料常量**在两侧各跑一遍。语料写在 Go 里、导出给前端，
//      而不是各写一份 —— 两份语料必然漂（§80：同一逻辑抄两遍，同一缺陷必然两处出现）。
//   ② 导出语料是**产品行为的一部分**，不是测试脚手架：
//      它回答的是「产品现在到底给用户多长的内容」，两端都必须读它。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"unicode/utf8"
)

// truncateUnitVector 是一条「语料 + 字节上限 → 真实输出」的向量。
type truncateUnitVector struct {
	Name   string `json:"name"`
	Input  string `json:"input"`
	Limit  int    `json:"limit"`
	Runes  int    `json:"runes"`  // 输出的字符数
	Bytes  int    `json:"bytes"`  // 输出的字节数
	Output string `json:"output"` // 真实输出（原样）
}

// truncateUnitCorpus 是**唯一一份**语料，两侧共用。
//
// ⚠ 为什么必须是「会议转写的真实形态」而不是纯 ASCII：
//
//	ASCII 下字节数 == 码元数 ⇒ 单位差异恒为 0 ⇒ 这道门会整份假绿。
//	（既有门 `summary-fallback-local-shape.test.ts` 就是这么假绿的。）
//
// ⚠ 为什么必须含星平面字符（emoji）：
//
//	它是「按码元切」与「按字节切」唯一会**产出不同长度**的那类字符
//	（汉字 3B 但 1 码元，emoji 4B 但 2 码元），
//	而且它同时是 §245.2 那条「切在代理对中间」的触发条件。
var truncateUnitCorpus = []struct {
	Name  string
	Input string
	Limit int
}{
	{"纯中文-500", "会议内容摘要需要完整呈现给用户查阅确认", 500},
	{"纯中文-200", "会议内容摘要需要完整呈现给用户查阅确认", 200},
	{"长中文-500", repeatStr("会议内容摘要需要完整呈现给用户查阅确认。", 40), 500},
	{"中英混排-500", "张伟: 这个 budget 我们下周 review 一下。\n李娜: 预算按季度复核，缺口走专项。", 500},
	{"含星平面-500", repeatStr("进度OK🔥交付顺利", 60), 500},
	{"含星平面-200", repeatStr("进度OK🔥交付顺利", 60), 200},
	{"刚好不触发截断-500", "短转写，不该被截断", 500},
}

func repeatStr(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}

// 这道判据把**磁盘上的 fixture**（固化常量）与**当场调用的真函数**逐条比对。
//
// ⚠⚠ 第一版把两侧都算成「当场调用真函数」⇒ 恒等 ⇒ 判据恒绿。
//
//	变异 GM1（真函数改成按字符截断）、GM3（真函数改成不截断）都**没有**被咬住，
//	才暴露出这个自证循环：fixture 和判据来自同一个函数，比对必然成立。
//	⇒ ★ **量具不能由被测对象生成**：被测对象一改，量具跟着改，
//	  于是「通过」永远成立。这与「断言的期望值不要手数」是同一族，
//	  但更隐蔽 —— 手数的期望值会错，这里**自动生成的期望值会跟着跑**。
//
// 正确形态：fixture 是**已提交的文件**，语义变更会让它与真函数不符 ⇒ 红。
// 重新生成的命令写在报错里（语义变更是**有意的**，不是误伤）。
func TestTruncateUnitFixtureMatchesRealTruncateStr(t *testing.T) {
	fixture := loadTruncateFixture(t)

	vecs := buildTruncateVectors()

	if len(fixture) == 0 {
		t.Fatal("fixture 为空")
	}
	if len(fixture) != len(vecs) {
		t.Fatalf("向量条数不一致：fixture=%d 当场生成=%d（语料改了就该重生成 fixture）", len(fixture), len(vecs))
	}

	// 逐条与真 truncateStr 比对。
	byName := make(map[string]truncateUnitVector, len(vecs))
	for _, v := range vecs {
		byName[v.Name] = v
	}
	for _, f := range fixture {
		v, ok := byName[f.Name]
		if !ok {
			t.Errorf("fixture 里的 %q 在当前语料里不存在（fixture 过期，需重生成）", f.Name)
			continue
		}
		got := truncateStr(v.Input, v.Limit)
		if got != f.Output {
			t.Errorf("[%s] truncateStr 语义已变：\n  fixture(固化的期望)=%q\n  真函数(现在)=%q\n"+
				"  这是**有意的**语义变更吗？若是，请显式重新生成：\n"+
				"    cd backend && UPDATE_TRUNCATE_FIXTURE=1 go test ./internal/server/ -run TestExportTruncateUnitFixture",
				f.Name, f.Output, got)
		}
		if n := utf8.RuneCountInString(got); n != f.Runes {
			t.Errorf("[%s] runes 期望值过期：fixture=%d 实测=%d（同样需重生成）", f.Name, f.Runes, n)
		}
		if n := len(got); n != f.Bytes {
			t.Errorf("[%s] bytes 期望值过期：fixture=%d 实测=%d（同样需重生成）", f.Name, f.Bytes, n)
		}
	}

	// ★ 自证：这份 fixture 必须能表达「字节 ≠ 码元」，否则它证明不了任何事。
	sawTruncation := false
	for _, v := range vecs {
		if len(v.Input) > v.Limit {
			sawTruncation = true
		}
	}
	if !sawTruncation {
		t.Fatal("语料里没有任何一条真正触发截断 ⇒ fixture 证明不了「按字节截」这件事")
	}
}

// loadTruncateFixture 读**磁盘上的**固化 fixture（不是当场生成的）。
func loadTruncateFixture(t *testing.T) []truncateUnitVector {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "truncate_unit_vectors.json"))
	if err != nil {
		t.Fatalf("读不到 fixture（它必须是一份**已提交的固化常量**，否则判据会自证循环）：%v", err)
	}
	var vecs []truncateUnitVector
	if err := json.Unmarshal(b, &vecs); err != nil {
		t.Fatal(err)
	}
	return vecs
}

// 导出给前端判据消费（`-update` 写盘）。
//
// ⚠ 这是**产品行为快照**，不是测试脚手架：前端那道门读它，
// 是为了让「后端口径」的读数来自产品代码而不是 JS 里的复刻。
func TestExportTruncateUnitFixture(t *testing.T) {
	path := filepath.Join("testdata", "truncate_unit_vectors.json")
	if os.Getenv("UPDATE_TRUNCATE_FIXTURE") != "1" {
		t.Skip("设 UPDATE_TRUNCATE_FIXTURE=1 才重新生成")
	}
	vecs := buildTruncateVectors()
	b, err := json.MarshalIndent(vecs, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("已写 %s（%d 条）", path, len(vecs))
}

func buildTruncateVectors() []truncateUnitVector {
	vecs := make([]truncateUnitVector, 0, len(truncateUnitCorpus))
	for _, c := range truncateUnitCorpus {
		out := truncateStr(c.Input, c.Limit)
		vecs = append(vecs, truncateUnitVector{
			Name:   c.Name,
			Input:  c.Input,
			Limit:  c.Limit,
			Runes:  utf8.RuneCountInString(out),
			Bytes:  len(out),
			Output: out,
		})
	}
	// 按名字排序 ⇒ 文件内容与写入顺序无关，避免无意义的 diff 抖动。
	sort.Slice(vecs, func(i, j int) bool { return vecs[i].Name < vecs[j].Name })
	return vecs
}
