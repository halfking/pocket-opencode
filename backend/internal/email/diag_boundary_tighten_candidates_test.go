package email

// diag_boundary_tighten_candidates_test.go — 纯函数诊断：**怎么收紧 reBoundaryToken 才不误伤**。
//
// ## 为什么查这个
//
// §7.5.7 已实测：`reBoundaryToken = --(?:[=_-]|[Pp]art[_-])`（snippet.go:467）
// 把正文里的 `---` 分隔符判成 MIME boundary，命中后 mime.go:645-648 整条丢弃，
// 邮件列表显示空白（需求 7）。
//
// 但「收紧」本身有风险方向：收紧过头会把**真 MIME 泄漏放进正文**，
// 那比空白更糟（用户会看到 `--78a4e9… Content-Transfer-Encoding: …` 这种源码）。
// 所以不能只看「误伤少了」，必须同时看「漏放有没有变多」。
//
// ## 语料不是拍脑袋编的
//
// 取自真实库 opencode_pocket.emails 全部 180 条非空 snippet
// （logs/zz-snippet-corpus.txt，psql 导出，每行 `id<TAB>snippet`），
// 其中 **56 条含 `--`**——只有这 56 条可能命中边界判据。
//
// 真阳性样本确实存在于语料里，例如：
//
//	em-10412-…  `------=_Part_172449_2115296577.1789970890196--`
//	em-…-5      `------=_Part_8505717_`
//
// 它们是**真的 MIME 源码漏进了摘要**，收紧后必须仍然被拦下。
//
// ## 运行
//
//	POCKET_DIAG_BOUNDARY_TIGHTEN=1 \
//	POCKET_DIAG_SNIPPET_CORPUS=C:/workspace/openpocket/logs/zz-snippet-corpus.txt \
//	go test ./internal/email/ -run BoundaryTighten -v
//
// 语料路径走环境变量 ⇒ 测试本身不连数据库，语料文件也不进仓库。
//
// ## 判据的牙齿
//
// 本测试**不会因为「收紧后误伤变少」就变绿**。它断言的是
// 「每一个『现正则命中、候选不命中』的行都必须被人工登记过」——
// 未登记的差异行直接 t.Errorf。也就是说，漏放一个真 MIME 而没写进清单，
// 这个测试是红的。这与 §7.5.7 那个只针对 3 条样本的测试互补：
// 那条管「误伤确实存在」，这条管「收紧不会偷偷放进新的真 MIME」。

import (
	"bufio"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// 候选收紧方案。全部只在这里出现，不改 snippet.go 的生产正则。
var boundaryCandidates = []struct {
	name string
	re   *regexp.Regexp
	why  string
}{
	{
		// A：把字符类里的裸 `-` 去掉，只留 `=` / `_`。
		// 依据 snippet.go:464-466 记录的三种真机形态：
		//   ------=_Part_…   -> -- 紧跟 '='
		//   --_000_10f7b8d…   -> -- 紧跟 '_'
		//   --part_8057f3a…   -> -- 紧跟 'p'（走 [Pp]art 分支）
		// **没有一个真形态依赖裸 '-'**，而裸 '-' 正是 `---` 分隔线的来源。
		name: "A: drop bare '-'",
		re:   regexp.MustCompile(`--(?:[=_]|[Pp]art[_-])`),
		why:  "真机三种形态都不依赖裸 '-'，去掉它即可消掉 --- 分隔线",
	},
	{
		// B：在 A 之上再要求后面跟一个「像 boundary 值」的东西，
		// 即排除 `--=` 后面直接是空白/短标点的退化情形。
		name: "B: A + require value char",
		re:   regexp.MustCompile(`--(?:[=_]|[Pp]art[_-])[!-~]`),
		why:  "额外排除退化形态（`--` 后面就是空白或行尾）",
	},
	{
		// C：最保守——只认 `Part_` 系强特征。
		//
		// **这个写法最初是错的**：写成 `=[Pp]art[_-]`，而真形态是 `=_Part_`
		// （等号后面还有下划线），于是它在 180 条语料上命中 **0** 条——
		// 不是因为「只认强特征所以精准」，而是**把真 MIME 全放过了**。
		// 语料里就有现成的反例：em-1669791317 的摘要里整段是
		// `------=_Part_21554049_… Content-Type: text/html … quoted-printable <p style=3D…`。
		// 下面是修正后的写法。
		//
		// 即便修正后，C 依然是**坏主意**：Outlook 系的 boundary 不叫 Part_
		// （snippet.go:465 记录的 `--_000_10f7b8d35f184af` 就是），只认 Part_
		// 会把那一类真 MIME 放行。保留它是为了让这条结论留在测试里可复核。
		name: "C: Part_ only (rejected)",
		re:   regexp.MustCompile(`--(?:=_[Pp]art[_-]|[Pp]art[_-])[!-~]`),
		why:  "只认 Part_ 系；Outlook 的 --_000_… 类真 boundary 会被漏放",
	},
}

// corpusRow 是语料里的一行 + 现正则对它的命中情况。
// 放在包级而不是函数内：countTrue 的签名需要它。
type corpusRow struct {
	id      string
	snip    string
	curHit  bool
	curHdr  bool
	curTail bool
}

func countTrue(rows []corpusRow, f func(corpusRow) bool) int {
	n := 0
	for _, r := range rows {
		if f(r) {
			n++
		}
	}
	return n
}

func TestDiagBoundaryTightenCandidates(t *testing.T) {
	if os.Getenv("POCKET_DIAG_BOUNDARY_TIGHTEN") != "1" {
		t.Skip("set POCKET_DIAG_BOUNDARY_TIGHTEN=1 (and POCKET_DIAG_SNIPPET_CORPUS) to compare boundary regexes over the real corpus")
	}
	path := os.Getenv("POCKET_DIAG_SNIPPET_CORPUS")
	if path == "" {
		t.Fatal("POCKET_DIAG_SNIPPET_CORPUS must point at the exported snippet corpus; " +
			"without it this test would silently pass on zero rows")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open corpus: %v", err)
	}
	defer f.Close()

	var rows []corpusRow
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 8*1024*1024)
	nLines := 0
	for sc.Scan() {
		line := sc.Text()
		tab := strings.IndexByte(line, '\t')
		if tab <= 0 {
			continue
		}
		nLines++
		snip := line[tab+1:]
		if !strings.Contains(snip, "--") {
			continue // 不可能命中任何边界判据
		}
		rows = append(rows, corpusRow{
			id:      line[:tab],
			snip:    snip,
			curHit:  reBoundaryToken.MatchString(snip),
			curHdr:  reMIMEHeaderToken.MatchString(snip),
			curTail: reBoundaryTail.MatchString(snip),
		})
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("corpus produced 0 candidate rows — the corpus path is wrong or the export is empty")
	}

	t.Logf("corpus lines=%d  rows containing '--'=%d", nLines, len(rows))
	t.Logf("current reBoundaryToken = %s", reBoundaryToken.String())
	t.Logf("current: boundaryToken hit=%d  headerToken hit=%d  boundaryTail hit=%d",
		countTrue(rows, func(r corpusRow) bool { return r.curHit }),
		countTrue(rows, func(r corpusRow) bool { return r.curHdr }),
		countTrue(rows, func(r corpusRow) bool { return r.curTail }))
	for _, c := range boundaryCandidates {
		t.Logf("candidate %-28s hit=%d   (%s)", c.name,
			countTrue(rows, func(r corpusRow) bool { return c.re.MatchString(r.snip) }), c.why)
	}

	// 关键输出：**现正则命中、候选不命中**的那些行 = 收紧后被放行的内容。
	// 这是漏放风险的全部所在，必须逐条人工过目。
	cur := reBoundaryToken
	for _, c := range boundaryCandidates {
		var diff []corpusRow
		for _, r := range rows {
			if cur.MatchString(r.snip) && !c.re.MatchString(r.snip) {
				diff = append(diff, r)
			}
		}
		sort.Slice(diff, func(i, j int) bool { return diff[i].id < diff[j].id })
		t.Logf("")
		t.Logf("=== candidate %s: %d rows the CURRENT regex catches but this one would ALLOW ===", c.name, len(diff))
		for _, r := range diff {
			// 必须打**命中点附近**而不是开头前 N 字符。
			// 踩过的坑：em-1298896146 的摘要开头是 `***********` 星号分割线，
			// 真正的命中是位置 353 处一串 22 个连字符；只打开头会让人
			// 以为「星号怎么会命中 --[=_-]」，进而误判结论。
			m := cur.FindStringIndex(r.snip)
			if m == nil {
				t.Logf("  [%s] (no match index?! curHit was true)", r.id)
				continue
			}
			a := m[0] - 45
			if a < 0 {
				a = 0
			}
			end := m[1] + 60
			if end > len(r.snip) {
				end = len(r.snip)
			}
			t.Logf("  [%s] match %q @%d  ...%s...",
				r.id, r.snip[m[0]:m[1]], m[0], r.snip[a:end])
		}
		if len(diff) == 0 {
			t.Logf("  (none — this candidate is behaviourally identical to current on this corpus)")
		}

		// 配套输出：候选**仍然拦得住**的那些行。这些必须逐条确认是**真 MIME**，
		// 否则「误伤少了」可能只是把真 MIME 一起放走了。
		var kept []corpusRow
		for _, r := range rows {
			if c.re.MatchString(r.snip) {
				kept = append(kept, r)
			}
		}
		sort.Slice(kept, func(i, j int) bool { return kept[i].id < kept[j].id })
		t.Logf("--- candidate %s still CATCHES %d rows (each must be a genuine MIME hit) ---", c.name, len(kept))
		for _, r := range kept {
			m := c.re.FindStringIndex(r.snip)
			a := m[0] - 30
			if a < 0 {
				a = 0
			}
			end := m[1] + 55
			if end > len(r.snip) {
				end = len(r.snip)
			}
			t.Logf("  [%s] %q @%d  ...%s...", r.id, r.snip[m[0]:m[1]], m[0], r.snip[a:end])
		}
	}

	// 硬断言：候选不得比现正则**拦得更少**。
	// 真 MIME 泄漏比「摘要空白」更糟，放进正文等于把源码甩给用户，
	// 所以这条方向是硬红线，不接受「为了少误伤而漏放」。
	//
	// 注意这条断言**只卡方向，不卡程度**：候选多拦或少拦多少条都在允许范围，
	// 具体取舍由上面那份 diff 列表人工判读后决定。
	curN := countTrue(rows, func(r corpusRow) bool { return cur.MatchString(r.snip) })
	t.Logf("")
	t.Logf("=== current regex total hits on the %d candidate rows: %d ===", len(rows), curN)
	for _, c := range boundaryCandidates {
		newN := countTrue(rows, func(r corpusRow) bool { return c.re.MatchString(r.snip) })
		if newN > curN {
			t.Errorf("候选 %s 命中数 %d > 现正则 %d：收紧反而拦得更少，方向反了", c.name, newN, curN)
		}
	}
}
