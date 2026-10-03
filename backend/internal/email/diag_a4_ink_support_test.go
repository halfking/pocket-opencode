package email

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// diag_a4_ink_support.go —— diag_a4_ink_overflow_test.go 的支撑实现。
//
// ## 它做什么
//
// 对一个已经是 A4 网格排版的 PDF，**逐页算出「墨迹落在哪」**，并把每次
// 放置（form XObject）归到它所在的格子里，判定墨迹有没有越过格线。
//
// 与 diag_a4_cell_fit_test.go 的分工：
//
//   - cell_fit 判**页框**（BBox 变换后的矩形）是否 ⊆ 格子。它是「墨迹不越界」
//     的**充分条件**，因为可见墨迹 ⊆ 页框。页框出格时它报红，但报红**不能**
//     证明墨迹真的越过去了。
//   - 本文件判**墨迹本身**。这是必要且充分的那一半：页框出格但墨迹不出格是
//     完全可能的（票面右侧一整条白边），反过来不存在。
//
// 两者合起来才是完整答案：cell_fit 说「有风险」，本文件说「到底有没有撞上」。
//
// ## 边界（写在前面，别过度解读输出）
//
// ① 文字墨迹用**保守外扩**：字形纵向按字号的整倍从基线外扩，横向按解析出的
//    字宽推进。解析不出字宽时**不会**假装宽度为 0，而是把该次绘制记成
//    `widthUnknown` 并单独计数——那种情况下「未越界」的结论强度更弱。
// ② 内联图像（BI/ID/EI）按单位正方形算，是上界。
// ③ 裁切路径（`W n` / `W* n`）不产生墨迹，正确排除；网格裁切线本身画在
//    格线上，判定用 tol 容差并单独报出越界深度，不要把容差内的读数当越界。

const (
	inkMaxDepth  = 12
	inkMaxOps    = 4_000_000
	inkTolPt     = 0.5
	inkInflateFs = 1.0 // 文字纵向按字号整倍外扩（保守上界）
)

// ---------------------------------------------------------------- 矩阵 / 矩形

// inkMat 是 PDF 的 [a b c d e f]，表示
//
//	x' = a·x + c·y + e
//	y' = b·x + d·y + f
type inkMat [6]float64

var inkIdentity = inkMat{1, 0, 0, 1, 0, 0}

func inkMul(m, n inkMat) inkMat { // m × n（先 n 后 m）
	return inkMat{
		m[0]*n[0] + m[1]*n[2],
		m[0]*n[1] + m[1]*n[3],
		m[2]*n[0] + m[3]*n[2],
		m[2]*n[1] + m[3]*n[3],
		m[4]*n[0] + m[5]*n[2] + n[4],
		m[4]*n[1] + m[5]*n[3] + n[5],
	}
}

func inkApply(m inkMat, x, y float64) (float64, float64) {
	return m[0]*x + m[2]*y + m[4], m[1]*x + m[3]*y + m[5]
}

type inkRect struct {
	x0, y0, x1, y1 float64
}

// inkEmpty 是「还没有墨迹」的哨兵：反向的矩形，valid() 为 false。
//
// **不能用零值 inkRect{0,0,0,0} 当空**——它的 valid() 是 true，
// 于是「首次赋值」会走并集分支把结果并成 {0,0,0,0}，
// 判据从此永远读到空墨迹、永远判「未越界」，而它看上去一切正常。
// 这是本文件第一版真实踩到的坑。
func inkEmpty() inkRect {
	return inkRect{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
}

func (r inkRect) valid() bool { return r.x1 >= r.x0 && r.y1 >= r.y0 }

func inkRectOfPoints(pts []inkRect) inkRect {
	out := inkEmpty()
	first := true
	for _, p := range pts {
		if !p.valid() {
			continue
		}
		if first {
			out, first = p, false
			continue
		}
		if p.x0 < out.x0 {
			out.x0 = p.x0
		}
		if p.y0 < out.y0 {
			out.y0 = p.y0
		}
		if p.x1 > out.x1 {
			out.x1 = p.x1
		}
		if p.y1 > out.y1 {
			out.y1 = p.y1
		}
	}
	return out
}

// intersect 返回 r 与 c 的交集（两者都要有效）。
func (r inkRect) intersect(c inkRect) inkRect {
	if !r.valid() || !c.valid() {
		return inkEmpty()
	}
	return inkRect{
		math.Max(r.x0, c.x0), math.Max(r.y0, c.y0),
		math.Min(r.x1, c.x1), math.Min(r.y1, c.y1),
	}
}

// unionInto 把 r 并进 dst（dst 可能是哨兵）。
func (dst *inkRect) unionInto(r inkRect) {
	if !r.valid() {
		return
	}
	if !dst.valid() {
		*dst = r
		return
	}
	if r.x0 < dst.x0 {
		dst.x0 = r.x0
	}
	if r.y0 < dst.y0 {
		dst.y0 = r.y0
	}
	if r.x1 > dst.x1 {
		dst.x1 = r.x1
	}
	if r.y1 > dst.y1 {
		dst.y1 = r.y1
	}
}

func inkRectOfCorners(m inkMat, r inkRect) inkRect {
	pts := make([]inkRect, 0, 4)
	for _, c := range [][2]float64{{r.x0, r.y0}, {r.x1, r.y0}, {r.x1, r.y1}, {r.x0, r.y1}} {
		x, y := inkApply(m, c[0], c[1])
		pts = append(pts, inkRect{x, y, x, y})
	}
	return inkRectOfPoints(pts)
}

// grow 按 d 向四个方向外扩。
func (r inkRect) grow(d float64) inkRect {
	return inkRect{r.x0 - d, r.y0 - d, r.x1 + d, r.y1 + d}
}

// overflowDepth 返回 r 越出 cell 的**最大**距离（pt）；完全在内返回 0。
func (r inkRect) overflowDepth(cell inkRect) float64 {
	if !r.valid() {
		return 0
	}
	d := math.Max(math.Max(cell.x0-r.x0, r.x1-cell.x1), math.Max(cell.y0-r.y0, r.y1-cell.y1))
	if d < 0 {
		return 0
	}
	return d
}

// ---------------------------------------------------------------- 词法

type inkTokKind int

const (
	inkNum inkTokKind = iota
	inkName
	inkStr
	inkArrOpen
	inkArrClose
	inkDictOpen
	inkDictClose
	inkOp
	inkKw // true / false / null
)

type inkTok struct {
	kind inkTokKind
	num  float64
	str  string
	raw  []byte
	op   string
}

func inkIsWS(c byte) bool {
	switch c {
	case 0x00, 0x09, 0x0a, 0x0c, 0x0d, 0x20:
		return true
	}
	return false
}

func inkIsDelim(c byte) bool {
	switch c {
	case '(', ')', '<', '>', '[', ']', '{', '}', '/', '%':
		return true
	}
	return false
}

// inkTokenize 把内容流切成 token 序列。返回 ok=false 表示词法就失败
// （调用方必须**如实报失败**，不能当成「没有墨迹」）。
func inkTokenize(b []byte) ([]inkTok, bool) {
	var out []inkTok
	i := 0
	n := len(b)
	flushNumber := func(s string) bool {
		if s == "" {
			return true
		}
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return false
		}
		out = append(out, inkTok{kind: inkNum, num: v})
		return true
	}
	for i < n {
		c := b[i]
		switch {
		case inkIsWS(c):
			i++
		case c == '%':
			for i < n && b[i] != '\n' && b[i] != '\r' {
				i++
			}
		case c == '(':
			depth, start := 1, i+1
			i++
			for i < n && depth > 0 {
				switch b[i] {
				case '\\':
					i += 2
					continue
				case '(':
					depth++
				case ')':
					depth--
				}
				i++
			}
			if depth != 0 {
				return nil, false
			}
			out = append(out, inkTok{kind: inkStr, raw: unescapePDFString(b[start : i-1])})
		case c == '<':
			if i+1 < n && b[i+1] == '<' {
				out = append(out, inkTok{kind: inkDictOpen})
				i += 2
				continue
			}
			j := i + 1
			for j < n && b[j] != '>' {
				j++
			}
			if j >= n {
				return nil, false
			}
			out = append(out, inkTok{kind: inkStr, raw: decodeHexString(b[i+1 : j])})
			i = j + 1
		case c == '>':
			if i+1 < n && b[i+1] == '>' {
				out = append(out, inkTok{kind: inkDictClose})
				i += 2
				continue
			}
			return nil, false
		case c == '[':
			out = append(out, inkTok{kind: inkArrOpen})
			i++
		case c == ']':
			out = append(out, inkTok{kind: inkArrClose})
			i++
		case c == '{' || c == '}':
			i++
		case c == '/':
			j := i + 1
			var sb strings.Builder
			for j < n && !inkIsWS(b[j]) && !inkIsDelim(b[j]) {
				if b[j] == '#' && j+2 < n {
					if v, err := strconv.ParseUint(string(b[j+1:j+3]), 16, 8); err == nil {
						sb.WriteByte(byte(v))
						j += 3
						continue
					}
				}
				sb.WriteByte(b[j])
				j++
			}
			out = append(out, inkTok{kind: inkName, str: sb.String()})
			i = j
		default:
			j := i
			var sb strings.Builder
			for j < n && !inkIsWS(b[j]) && !inkIsDelim(b[j]) {
				sb.WriteByte(b[j])
				j++
			}
			txt := sb.String()
			if txt == "" {
				return nil, false
			}
			if isNumberToken(txt) {
				if !flushNumber(txt) {
					return nil, false
				}
			} else if txt == "true" || txt == "false" || txt == "null" {
				out = append(out, inkTok{kind: inkKw, str: txt})
			} else {
				out = append(out, inkTok{kind: inkOp, op: txt})
			}
			i = j
		}
		if len(out) > inkMaxOps {
			return nil, false
		}
	}
	return out, true
}

func isNumberToken(s string) bool {
	if s == "" {
		return false
	}
	i := 0
	if s[i] == '+' || s[i] == '-' {
		i++
	}
	digits, dot := 0, false
	for ; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			digits++
			continue
		}
		if s[i] == '.' && !dot {
			dot = true
			continue
		}
		return false
	}
	return digits > 0 || dot
}

func unescapePDFString(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); {
		if b[i] != '\\' {
			out = append(out, b[i])
			i++
			continue
		}
		i++
		if i >= len(b) {
			break
		}
		switch b[i] {
		case 'n':
			out = append(out, '\n')
			i++
		case 'r':
			out = append(out, '\r')
			i++
		case 't':
			out = append(out, '\t')
			i++
		case 'b':
			out = append(out, '\b')
			i++
		case 'f':
			out = append(out, '\f')
			i++
		case '(', ')', '\\':
			out = append(out, b[i])
			i++
		case '\r':
			i++
			if i < len(b) && b[i] == '\n' {
				i++
			}
		case '\n':
			i++
		default:
			if b[i] >= '0' && b[i] <= '7' {
				v := 0
				for k := 0; k < 3 && i < len(b) && b[i] >= '0' && b[i] <= '7'; k++ {
					v = v*8 + int(b[i]-'0')
					i++
				}
				out = append(out, byte(v))
			} else {
				out = append(out, b[i])
				i++
			}
		}
	}
	return out
}

func decodeHexString(b []byte) []byte {
	var sb []byte
	var hi = -1
	for _, c := range b {
		var v = -1
		switch {
		case c >= '0' && c <= '9':
			v = int(c - '0')
		case c >= 'a' && c <= 'f':
			v = int(c-'a') + 10
		case c >= 'A' && c <= 'F':
			v = int(c-'A') + 10
		default:
			continue
		}
		if hi < 0 {
			hi = v
			continue
		}
		sb = append(sb, byte(hi*16+v))
		hi = -1
	}
	if hi >= 0 {
		sb = append(sb, byte(hi*16))
	}
	return sb
}

// ---------------------------------------------------------------- 字体宽度

type inkFont struct {
	twoByte     bool
	firstChar   int
	simpleWidth []float64 // 1/1000 em
	cidWidth    map[int]float64
	defaultW    float64 // 1/1000 em
	hasWidths   bool
	// boundPerCode 是**每码位的宽度上界**（1/1000 em），>0 表示这份
	// 字体的精确宽度拿不到、只能按上界算。
	//
	// 为什么上界够用：判据问的是「墨迹有没有越过裁切线」。把宽度算大
	// 只会让墨迹盒变大 ⇒ 可能**误报**越界，但绝不会**漏报**。
	// 对「这批 A4 能不能直接交财务」这个问题，误报可以逐条复核，
	// 漏报则是直接交付事故。
	//
	// 取 1000（1 em）的前提：标准 14 字体（Helvetica/Times/Courier 系）
	// 的任何字形宽度都不超过 1 em。这是本判据**唯一的字体假设**，
	// 写在字段上，将来若要放宽必须同时改这里。
	boundPerCode float64
}

// glyphWidthOf 返回该字节串的推进宽度（1/1000 em，不含 Tc/Tw/Tfs）。
// exact=false 表示用的是上界而非精确值。
func (f *inkFont) glyphWidthOf(s []byte) (w float64, exact bool) {
	if f == nil {
		return 0, false
	}
	if f.twoByte {
		if f.cidWidth != nil && len(s)%2 == 0 {
			for i := 0; i+1 < len(s); i += 2 {
				cid := int(s[i])<<8 | int(s[i+1])
				gw, ok := f.cidWidth[cid]
				if !ok {
					gw = f.defaultW
				}
				w += gw
			}
			return w, f.hasWidths
		}
		// /W 没解析出来：按每 2 字节 1 个码位算上界。
		return f.boundPerCode * float64(len(s)/2), false
	}
	if f.simpleWidth != nil {
		exact = true
		for _, c := range s {
			idx := int(c) - f.firstChar
			if idx < 0 || idx >= len(f.simpleWidth) {
				// 码位落在 /Widths 覆盖之外：整串退回上界，不能一半精确一半猜。
				return f.boundPerCode * float64(len(s)), false
			}
			w += f.simpleWidth[idx]
		}
		return w, true
	}
	return f.boundPerCode * float64(len(s)), false
}

func resolveInkFont(xt *model.XRefTable, res types.Dict, name string) *inkFont {
	if res == nil {
		return nil
	}
	o, found := res.Find("Font")
	if !found || o == nil {
		return nil
	}
	fd, err := xt.DereferenceDict(o)
	if err != nil || fd == nil {
		return nil
	}
	fo, found := fd.Find(name)
	if !found || fo == nil {
		return nil
	}
	d, err := xt.DereferenceDict(fo)
	if err != nil || d == nil {
		return nil
	}
	f := &inkFont{defaultW: 1000, boundPerCode: 1000}
	if st := d.Type(); st != nil && *st == "Type0" {
		f.twoByte = true
		df, found := d.Find("DescendantFonts")
		if !found || df == nil {
			return f
		}
		arr, err := xt.DereferenceArray(df)
		if err != nil || len(arr) == 0 {
			return f
		}
		cd, err := xt.DereferenceDict(arr[0])
		if err != nil || cd == nil {
			return f
		}
		if dw, err := xt.DereferenceNumber(mustFind(cd, "DW")); err == nil {
			f.defaultW = dw
		}
		wa, found := cd.Find("W")
		if !found || wa == nil {
			return f
		}
		arr, err = xt.DereferenceArray(wa)
		if err != nil {
			return f
		}
		f.cidWidth = parseWArray(xt, arr)
		f.hasWidths = f.cidWidth != nil
		return f
	}
	f.firstChar = 0
	if fc, err := xt.DereferenceNumber(mustFind(d, "FirstChar")); err == nil {
		f.firstChar = int(fc)
	}
	wo, found := d.Find("Widths")
	if !found || wo == nil {
		return f
	}
	arr, err := xt.DereferenceArray(wo)
	if err != nil {
		return f
	}
	for _, e := range arr {
		v, err := xt.DereferenceNumber(e)
		if err != nil {
			return f
		}
		f.simpleWidth = append(f.simpleWidth, v)
	}
	f.hasWidths = f.simpleWidth != nil
	return f
}

func mustFind(d types.Dict, key string) types.Object {
	o, found := d.Find(key)
	if !found {
		return nil
	}
	return o
}

// parseWArray 解析 CIDFont 的 /W：[ c [w1 w2 …] cFirst cLast w … ]
func parseWArray(xt *model.XRefTable, arr types.Array) map[int]float64 {
	m := map[int]float64{}
	num := func(o types.Object) (float64, bool) {
		v, err := xt.DereferenceNumber(o)
		return v, err == nil
	}
	for i := 0; i < len(arr); {
		c, ok := num(arr[i])
		if !ok {
			return nil
		}
		if i+1 >= len(arr) {
			return nil
		}
		if sub, err := xt.DereferenceArray(arr[i+1]); err == nil && isPlainArray(arr[i+1]) {
			for k, e := range sub {
				w, ok := num(e)
				if !ok {
					return nil
				}
				m[int(c)+k] = w
			}
			i += 2
			continue
		}
		if i+2 >= len(arr) {
			return nil
		}
		c2, ok1 := num(arr[i+1])
		w, ok2 := num(arr[i+2])
		if !ok1 || !ok2 {
			return nil
		}
		for cc := int(c); cc <= int(c2) && cc-int(c) < 65536; cc++ {
			m[cc] = w
		}
		i += 3
	}
	return m
}

func isPlainArray(o types.Object) bool {
	_, ok := o.(types.Array)
	return ok
}

// dictSubtype 取 /Subtype 的名字（未定义则返回空串）。
func dictSubtype(d types.Dict) string {
	o, found := d.Find("Subtype")
	if !found || o == nil {
		return ""
	}
	if n, ok := o.(types.Name); ok {
		return string(n)
	}
	return ""
}

// ---------------------------------------------------------------- 解释器

// inkPlacement 是一次 form XObject 放置：它的页框落在 A4 的哪里，
// 以及它自己带出来的墨迹范围。
type inkPlacement struct {
	objNr int
	box   inkRect // 页框（BBox 经完整变换）
	ink   inkRect // 墨迹
	// root 指向「顶层放置」的下标：0 表示自己就是顶层。
	//
	// 为什么必须按顶层归组：pdfcpu NUp 把一张票整体放成一个 form，
	// 而票面自己内部还有嵌套 form（背景条、表格线、文字块）。
	// 实测一页 841.89pt 的 A4 上，顶层只有 4 个放置，嵌套碎片却有 21 个
	// —— 按碎片各自归格会得到一堆 y 为负、无法归到任何格子的小框，
	// 而「这张票被放在哪一格」只由顶层决定。
	root int
	// boundedCnt 是「按 1 em 上界算宽度」的绘制次数：这类绘制的墨迹盒是
	// **上界**，可能误报越界，但不会漏报。
	boundedCnt    int
	strokeUnknown int // 线宽未知的描边次数（未按 lw/2 外扩）
}

// inkPageResult 是一张 A4 输出页的结果。
type inkPageResult struct {
	placements []inkPlacement
	pageInk    inkRect
}

type inkFontState struct {
	font      *inkFont
	size      float64
	charSpace float64
	wordSpace float64
	hScale    float64 // Tz，默认 100
	leading   float64
	rise      float64
}

// wordSpanned 返回这段字节里「空格码」的数量对应的额外推进。
// PDF 规定 Tw 只作用于**单字节**空格码，CID 双字节串不能按字节数算。
func (s *inkFontState) wordSpanned(raw []byte) float64 {
	if s.font == nil || s.wordSpace == 0 {
		return 0
	}
	n := 0
	if s.font.twoByte {
		for i := 0; i+1 < len(raw); i += 2 {
			if raw[i] == 0 && raw[i+1] == 32 {
				n++
			}
		}
	} else {
		for _, c := range raw {
			if c == 32 {
				n++
			}
		}
	}
	return float64(n) * s.wordSpace
}

type inkWalker struct {
	xt   *model.XRefTable
	opN  int
	seen map[int]bool
	// at 记录「解析不下去」的确切位置（算子名 + token 下标）。
	// 只有一句「解析失败」时，人根本不知道该修判据还是该修数据 ——
	// 而这两种情形的处置完全相反。
	at string
}

func (w *inkWalker) failf(format string, args ...any) bool {
	w.at = fmt.Sprintf(format, args...)
	return false
}

func (w *inkWalker) walk(res types.Dict, content []byte, ctm inkMat,
	pl *inkPlacement, page *inkPageResult, depth int, parentRoot int) (ok bool) {
	if depth > inkMaxDepth {
		return false
	}
	toks, ok := inkTokenize(content)
	if !ok {
		return w.failf("词法解析失败（内容流 %d 字节，token 数 %d）", len(content), len(toks))
	}
	lastOp := ""
	defer func() {
		if !ok && w.at == "" {
			w.at = fmt.Sprintf("算子 %q 附近（token #%d）", lastOp, w.opN)
		}
	}()
	var stack []inkMat
	var pts []inkRect
	var cur inkRect
	haveCur := false
	// hScale 必须显式给 1：零值会让**所有**文字推进量算成 0，
	// 于是墨迹右边界恒等于起点 —— 判据看着在跑，实际永远判「不出格」。
	var fs inkFontState
	fs.hScale = 1
	tm, tlm := inkIdentity, inkIdentity

	record := func(r inkRect) {
		if !r.valid() {
			return
		}
		if pl != nil {
			pl.ink.unionInto(r)
		}
		page.pageInk.unionInto(r)
	}

	pushPt := func(x, y float64) {
		dx, dy := inkApply(ctm, x, y)
		r := inkRect{dx, dy, dx, dy}
		if haveCur {
			pts = append(pts, cur)
		}
		cur, haveCur = r, true
	}
	unionPts := func() inkRect { return inkRectOfPoints(append(pts, cur)) }

	// 文字墨迹：基线段 + 纵向按字号整倍外扩（保守上界）
	showText := func(raw []byte) {
		if fs.size == 0 {
			return
		}
		// PDF 是**行向量**约定：Trm = Tm × CTM，即先 CTM 后 Tm。
		// inkMul(m, n) 的语义是「先 n 后 m」，所以这里必须是
		// inkMul(tm, ctm)——写成 inkMul(ctm, tm) 会把 CTM 的平移量
		// 当成 Tm 的平移量再叠一次，坐标直接翻倍。
		//
		// 另外**不要再乘 tlm**：Tm/Td 都已把 tlm 同步成 tm，
		// 再乘一次等于把平移加两遍（第一版真踩了，坐标 200 → 400）。
		full := inkMul(tm, ctm)
		ax, ay := inkApply(full, 0, fs.rise)
		w1000, exact := fs.font.glyphWidthOf(raw)
		adv := (w1000/1000*fs.size + fs.charSpace*float64(len(raw)) + fs.wordSpanned(raw)) * fs.hScale
		bx, by := inkApply(full, adv, fs.rise)
		record(inkRectOfPoints([]inkRect{{ax, ay, ax, ay}, {bx, by, bx, by}}).
			grow(fs.size * inkInflateFs))
		if !exact && pl != nil {
			pl.boundedCnt++
		}
	}

	for idx := 0; idx < len(toks); idx++ {
		w.opN++
		if w.opN > inkMaxOps {
			return false
		}
		t := toks[idx]
		if t.kind != inkOp {
			continue
		}
		lastOp = t.op
		switch t.op {
		case "q":
			stack = append(stack, ctm)
		case "Q":
			if len(stack) == 0 {
				return false
			}
			ctm = stack[len(stack)-1]
			stack = stack[:len(stack)-1]
		case "cm":
			if idx < 6 {
				return false
			}
			a := toks[idx-6]
			b := toks[idx-5]
			c := toks[idx-4]
			d := toks[idx-3]
			e := toks[idx-2]
			f := toks[idx-1]
			if a.kind != inkNum || b.kind != inkNum || c.kind != inkNum ||
				d.kind != inkNum || e.kind != inkNum || f.kind != inkNum {
				return false
			}
			ctm = inkMul(inkMat{a.num, b.num, c.num, d.num, e.num, f.num}, ctm)
		case "m", "l":
			if idx < 2 || toks[idx-2].kind != inkNum || toks[idx-1].kind != inkNum {
				return false
			}
			pushPt(toks[idx-2].num, toks[idx-1].num)
		case "c", "v", "y":
			if idx < 6 {
				return false
			}
			for k := 6; k >= 2; k -= 2 {
				pushPt(toks[idx-k].num, toks[idx-k+1].num)
			}
		case "h":
			// 闭合只连回首点，不新增点。
		case "re":
			if idx < 4 {
				return false
			}
			x, y, w, h := toks[idx-4].num, toks[idx-3].num, toks[idx-2].num, toks[idx-1].num
			corners := [][2]float64{{x, y}, {x + w, y}, {x + w, y + h}, {x, y + h}}
			for _, c := range corners {
				pushPt(c[0], c[1])
			}
		case "f", "F", "f*", "S", "s", "B", "B*", "b", "b*":
			if haveCur {
				pts = append(pts, cur)
			}
			record(unionPts())
			pts, cur, haveCur = nil, inkRect{}, false
		case "n", "W", "W*":
			// 裁切路径不产生墨迹。
			pts, cur, haveCur = nil, inkRect{}, false
		case "BT":
			tm, tlm = inkIdentity, inkIdentity
		case "ET":
		case "Tf":
			if idx < 2 {
				return false
			}
			sz, nam := toks[idx-1], toks[idx-2]
			if sz.kind != inkNum || nam.kind != inkName {
				return false
			}
			fs.size = sz.num
			fs.font = resolveInkFont(w.xt, res, nam.str)
		case "Tm":
			if idx < 6 {
				return false
			}
			tm = inkMat{
				toks[idx-6].num, toks[idx-5].num, toks[idx-4].num,
				toks[idx-3].num, toks[idx-2].num, toks[idx-1].num,
			}
			tlm = tm
		case "TL":
			if idx >= 1 && toks[idx-1].kind == inkNum {
				fs.leading = toks[idx-1].num
			}
		case "Tc":
			if idx >= 1 && toks[idx-1].kind == inkNum {
				fs.charSpace = toks[idx-1].num
			}
		case "Tw":
			if idx >= 1 && toks[idx-1].kind == inkNum {
				fs.wordSpace = toks[idx-1].num
			}
		case "Tz":
			if idx >= 1 && toks[idx-1].kind == inkNum {
				fs.hScale = toks[idx-1].num / 100
			}
		case "Ts":
			if idx >= 1 && toks[idx-1].kind == inkNum {
				fs.rise = toks[idx-1].num
			}
		case "Td", "TD":
			if idx < 2 {
				return false
			}
			tx, ty := toks[idx-2].num, toks[idx-1].num
			if t.op == "TD" {
				fs.leading = -ty
			}
			tlm = inkMul(inkMat{1, 0, 0, 1, tx, ty}, tlm)
			tm = tlm
		case "T*":
			tlm = inkMul(inkMat{1, 0, 0, 1, 0, -fs.leading}, tlm)
			tm = tlm
		case "Tj", "'", "\"":
			var raw []byte
			if t.op == "Tj" {
				if idx < 1 || toks[idx-1].kind != inkStr {
					return false
				}
				raw = toks[idx-1].raw
			} else {
				if t.op == "'" {
					tlm = inkMul(inkMat{1, 0, 0, 1, 0, -fs.leading}, tlm)
					tm = tlm
				}
				if idx < 1 || toks[idx-1].kind != inkStr {
					return false
				}
				raw = toks[idx-1].raw
			}
			showText(raw)
			tm = inkMul(inkMat{1, 0, 0, 1, fs.size*fs.hScale + fs.charSpace + fs.wordSpace, 0}, tm)
		case "TJ":
			// TJ 数组：数字是 1/1000 em 的字距调整（向左）。
			arr, perr := inkTJItems(toks, idx)
			if perr {
				return false
			}
			for _, it := range arr {
				if it.isNum {
					dx := -it.num / 1000 * fs.size * fs.hScale
					tm = inkMul(inkMat{1, 0, 0, 1, dx, 0}, tm)
					continue
				}
				showText(it.raw)
				tm = inkMul(inkMat{1, 0, 0, 1, fs.size*fs.hScale + fs.charSpace + fs.wordSpace, 0}, tm)
			}
		case "Do":
			if idx < 1 || toks[idx-1].kind != inkName {
				return false
			}
			// parentRoot 必须**传下去**：顶层调用传 -1，嵌套调用传本层的 root，
			// 否则票面内部的子 form 全被当成顶层放置（实测一页 4 顶层 + 21 碎片）。
			if !w.walkXObject(res, toks[idx-1].str, ctm, page, depth, record, parentRoot) {
				return false
			}
		case "BI":
			// 内联图像：按单位正方形算（上界）。
			j := idx + 1
			for j < len(toks) && !(toks[j].kind == inkOp && toks[j].op == "EI") {
				j++
			}
			if j >= len(toks) {
				return false
			}
			record(inkRectOfCorners(ctm, inkRect{0, 0, 1, 1}))
			idx = j
		case "d0", "d1":
			// Type3 字体：宽度无法解析，交给 widthUnknown 路径。
		}
	}
	// 未闭合的路径也算墨迹（有些生成器靠路径构造隐式描边，极少见但保守起见）。
	if haveCur {
		pts = append(pts, cur)
	}
	if len(pts) > 0 {
		record(unionPts())
	}
	return true
}

type inkTJItem struct {
	isNum bool
	num   float64
	raw   []byte
}

func inkTJItems(toks []inkTok, idx int) ([]inkTJItem, bool) {
	depth := 0
	var out []inkTJItem
	for j := idx - 1; j >= 0; j-- {
		t := toks[j]
		switch t.kind {
		case inkArrClose:
			depth++
		case inkArrOpen:
			if depth == 0 {
				items := make([]inkTJItem, 0, len(out))
				for k := len(out) - 1; k >= 0; k-- {
					items = append(items, out[k])
				}
				return items, true
			}
			depth--
		case inkNum:
			if depth == 0 {
				out = append(out, inkTJItem{isNum: true, num: t.num})
			}
		case inkStr:
			if depth == 0 {
				out = append(out, inkTJItem{raw: t.raw})
			}
		}
	}
	return nil, false
}

func (w *inkWalker) walkXObject(res types.Dict, name string, ctm inkMat,
	page *inkPageResult, depth int, record func(inkRect), parentRoot int) bool {
	if res == nil {
		return false
	}
	o, found := res.Find("XObject")
	if !found || o == nil {
		return false
	}
	xd, err := w.xt.DereferenceDict(o)
	if err != nil || xd == nil {
		return false
	}
	fo, found := xd.Find(name)
	if !found || fo == nil {
		return false
	}
	sd, _, err := w.xt.DereferenceStreamDict(fo)
	if err != nil || sd == nil {
		return false
	}
	// form 的标志是 /Subtype /Form，**不是** /Type —— 它的 /Type 是 /XObject。
	// 写成查 Type 会让每个 form 都被当成非 form，walk 直接失败。
	if st := dictSubtype(sd.Dict); st != "Form" {
		// 图像之类的非 form XObject 仍是**墨迹**：按单位正方形算（上界）。
		// 直接 return false 会让整份文件解析失败，于是汇总里出现
		// 「0 越界」——而那个 0 只覆盖了能解析的文件，是**误导**。
		if record != nil {
			record(inkRectOfCorners(ctm, inkRect{0, 0, 1, 1}))
		}
		return true
	}
	if depth+1 > inkMaxDepth {
		return false
	}
	m := inkIdentity
	if mo, found := sd.Find("Matrix"); found && mo != nil {
		if arr, err := w.xt.DereferenceArray(mo); err == nil && len(arr) == 6 {
			var v [6]float64
			okAll := true
			for i, e := range arr {
				f, err := w.xt.DereferenceNumber(e)
				if err != nil {
					okAll = false
					break
				}
				v[i] = f
			}
			if okAll {
				m = inkMat(v)
			}
		}
	}
	bb := inkRect{0, 0, 612, 792}
	if bo, found := sd.Find("BBox"); found && bo != nil {
		if arr, err := w.xt.DereferenceArray(bo); err == nil && len(arr) == 4 {
			var v [4]float64
			okAll := true
			for i, e := range arr {
				f, err := w.xt.DereferenceNumber(e)
				if err != nil {
					okAll = false
					break
				}
				v[i] = f
			}
			if okAll {
				bb = inkRect{v[0], v[1], v[2], v[3]}
			}
		}
	}
	ctm2 := inkMul(ctm, m)
	pi := len(page.placements)
	root := pi
	if parentRoot >= 0 {
		root = parentRoot
	}
	pl := inkPlacement{
		objNr: -1,
		box:   inkRectOfCorners(ctm2, bb),
		ink:   inkEmpty(),
		root:  root,
	}
	// BBox 默认就有内容，不递归也会记下「这里放过东西」。
	if !pl.box.valid() {
		return false
	}
	page.placements = append(page.placements, pl)
	fr, found := sd.Find("Resources")
	var fres types.Dict
	if found && fr != nil {
		if d, err := w.xt.DereferenceDict(fr); err == nil {
			fres = d
		}
	}
	if fres == nil {
		fres = res
	}
	// DereferenceStreamDict 只保证 Raw 有内容，Content 可能还没解出来。
	// 不显式 Decode 的话，内层内容流是空的 —— 外面看「解析成功、
	// 零墨迹」，与「确实没有墨迹」完全一样。
	if len(sd.Content) == 0 && len(sd.Raw) > 0 {
		if err := sd.Decode(); err != nil {
			return false
		}
	}
	if !w.walk(fres, sd.Content, ctm2, &page.placements[pi], page, depth+1, root) {
		return false
	}
	// form 内容**必须**按 /BBox 裁剪（PDF 32000-1 8.10.1：BBox 界定 form
	// 的裁剪区域），超出 BBox 的绘制不会被渲染/打印。
	//
	// 这条规则有个重要推论，本判据整个设计就建立在它上面：
	//
	//	可见墨迹 ⊆ 页框（form 的 BBox / 页面的 MediaBox）
	//	⇒ **页框不出格 ⇒ 墨迹必不出格**（定理，不是经验）
	//	⇒ 反过来「墨迹越界 ⇒ 页框越界」恒成立
	//
	// 所以本判据**不可能**发现页框判据发现不了的越界；它的价值是
	// **降级**：把「页框出格」里那些其实只是白边的格子挑出来。
	// 任何声称「页框在格内但墨迹越界」的用例都是在构造不可能的形态 ——
	// 我第一版就构造了一个（文字画到 form BBox 之外），被裁剪逻辑打回。
	//
	// 不裁剪的后果实测过：交通票面内容流里有
	// `118.75 118.75 2243.95 3272.02 re` 这类坐标远大于 841.92 高的页框，
	// 不裁就报出「墨迹越界 1284pt」——而那部分**根本不会印出来**。
	page.placements[pi].ink = page.placements[pi].ink.intersect(pl.box)
	return true
}

var _ = fmt.Sprintf
