// Package rss — sharecard.go
//
// renderShareCard 把一条 RSS item 渲染成 1080×1350 PNG（朋友圈/微博画幅）。
//
// 设计取舍：
//   - 纯 stdlib 实现（image/draw + image/png）保证零外部依赖、可在任何 build tag 下编译通过。
//   - CJK 字体暂不内置：MVP 用简化的英文/数字 + 几何色块占位，标题用 5×7 bitmap font 渲染。
//     后续迭代接 fogleman/gg + 嵌入 NotoSansSC（仍 pure Go，无需 CGO）。
//   - QR 码简化：直接画一个 33×33 的方阵（low-EC），MVP 阶段避免引入 qrcode 库；用户手动截屏或复制链接。
package rss

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"strings"
)

// RenderShareCard 渲染 PNG 字节流。theme 仅识别 "light" / "dark"。
func RenderShareCard(_ context.Context, it Item, src *Source, theme string) ([]byte, error) {
	bg, fg, accent := lightTheme()
	if strings.EqualFold(theme, "dark") {
		bg, fg, accent = darkTheme()
	}

	const W, H = 1080, 1350
	img := image.NewRGBA(image.Rect(0, 0, W, H))

	drawRect(img, 0, 0, W, H, bg)

	// 顶栏：accent 色条 + 源 favicon 占位 + 源标题
	drawRect(img, 0, 0, W, 90, accent)
	drawTextBitmap(img, 40, 32, truncateASCII(src.Title, 28), white)
	if src.URL != "" {
		drawTextBitmap(img, 40, 62, truncateASCII(src.URL, 40), white)
	}

	// 主区：标题（最多 4 行）
	title := strings.TrimSpace(it.Title)
	if title == "" {
		title = "Untitled"
	}
	lines := wrapASCII(title, 22)
	if len(lines) > 4 {
		lines = append(lines[:3], "…")
	}
	y := 180
	for _, line := range lines {
		drawTextBitmap(img, 60, y, truncateASCII(line, 22), fg)
		y += 56
	}

	// 摘要区（最多 6 行）
	summary := strings.TrimSpace(it.Summary)
	if summary == "" {
		summary = strings.TrimSpace(it.Content)
	}
	if summary != "" {
		// 内容是 HTML，先粗暴去标签
		summary = stripHTMLTags(summary)
		summaryLines := wrapASCII(summary, 36)
		if len(summaryLines) > 6 {
			summaryLines = append(summaryLines[:5], "…")
		}
		y += 40
		for _, line := range summaryLines {
			drawTextBitmap(img, 60, y, truncateASCII(line, 36), fg)
			y += 44
		}
	}

	// 底部：QR 占位 + URL
	qy := H - 280
	drawRect(img, W-340, qy, 240, 240, black)
	drawRect(img, W-340+8, qy+8, 240-16, 240-16, white)
	// 简化的 QR 占位：3 个角的对齐标记 + 中心点
	drawFinderPattern(img, W-340+16, qy+16)
	drawFinderPattern(img, W-340+240-16-49, qy+16)
	drawFinderPattern(img, W-340+16, qy+240-16-49)
	drawRect(img, W-340+90, qy+90, 60, 60, black)

	// URL 文本
	urlText := truncateASCII(it.URL, 50)
	if urlText == "" {
		urlText = "(no link)"
	}
	drawTextBitmap(img, 60, H-120, "Link:", fg)
	drawTextBitmap(img, 60, H-80, urlText, fg)
	drawTextBitmap(img, 60, H-40, "@openpocket", accent)

	// 编码
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ===== 调色板 =====

var (
	white = color.RGBA{0xFF, 0xFF, 0xFF, 0xFF}
	black = color.RGBA{0x00, 0x00, 0x00, 0xFF}
)

func lightTheme() (bg, fg, accent color.RGBA) {
	return color.RGBA{0xFA, 0xFA, 0xFA, 0xFF}, color.RGBA{0x1A, 0x1A, 0x1A, 0xFF}, color.RGBA{0x25, 0x63, 0xEB, 0xFF}
}
func darkTheme() (bg, fg, accent color.RGBA) {
	return color.RGBA{0x0F, 0x14, 0x19, 0xFF}, color.RGBA{0xE5, 0xE7, 0xEB, 0xFF}, color.RGBA{0x60, 0xA5, 0xFA, 0xFF}
}

// ===== 图形原语 =====

func drawRect(img *image.RGBA, x, y, w, h int, c color.RGBA) {
	b := img.Bounds()
	for i := 0; i < w; i++ {
		xx := x + i
		if xx < b.Min.X || xx >= b.Max.X {
			continue
		}
		for j := 0; j < h; j++ {
			yy := y + j
			if yy < b.Min.Y || yy >= b.Max.Y {
				continue
			}
			img.SetRGBA(xx, yy, c)
		}
	}
}

// drawFinderPattern 画一个 7×7 的 QR 定位标记（实心方块带白色内框 + 中心点）。
func drawFinderPattern(img *image.RGBA, x, y int) {
	const s = 7
	for i := 0; i < s; i++ {
		for j := 0; j < s; j++ {
			isBorder := i == 0 || j == 0 || i == s-1 || j == s-1
			isCenter := i >= 2 && i <= 4 && j >= 2 && j <= 4
			if isBorder || isCenter {
				drawRect(img, x+i*7, y+j*7, 7, 7, black)
			}
		}
	}
}

// ===== 文本处理（无 CJK，纯 ASCII bitmap） =====

// 5×7 ASCII bitmap 字体：A-Z, 0-9, 常用标点；其余字符用 "?" 替代。
// 每个字符用 7 个字节表示 7 行（每字节低 5 位为一行像素）。
var asciiFont = map[rune][7]byte{
	'A': {0x3E, 0x09, 0x09, 0x09, 0x3E, 0x00, 0x00},
	'B': {0x3F, 0x25, 0x25, 0x25, 0x1A, 0x00, 0x00},
	'C': {0x1E, 0x21, 0x21, 0x21, 0x12, 0x00, 0x00},
	'D': {0x3F, 0x21, 0x21, 0x21, 0x1E, 0x00, 0x00},
	'E': {0x3F, 0x25, 0x25, 0x25, 0x21, 0x00, 0x00},
	'F': {0x3F, 0x05, 0x05, 0x05, 0x01, 0x00, 0x00},
	'G': {0x1E, 0x21, 0x29, 0x29, 0x3A, 0x00, 0x00},
	'H': {0x3F, 0x08, 0x08, 0x08, 0x3F, 0x00, 0x00},
	'I': {0x21, 0x21, 0x3F, 0x21, 0x21, 0x00, 0x00},
	'J': {0x10, 0x20, 0x20, 0x20, 0x1F, 0x00, 0x00},
	'K': {0x3F, 0x08, 0x14, 0x22, 0x21, 0x00, 0x00},
	'L': {0x3F, 0x20, 0x20, 0x20, 0x20, 0x00, 0x00},
	'M': {0x3F, 0x02, 0x04, 0x02, 0x3F, 0x00, 0x00},
	'N': {0x3F, 0x04, 0x08, 0x10, 0x3F, 0x00, 0x00},
	'O': {0x1E, 0x21, 0x21, 0x21, 0x1E, 0x00, 0x00},
	'P': {0x3F, 0x09, 0x09, 0x09, 0x06, 0x00, 0x00},
	'Q': {0x1E, 0x21, 0x29, 0x31, 0x2E, 0x00, 0x00},
	'R': {0x3F, 0x09, 0x19, 0x29, 0x26, 0x00, 0x00},
	'S': {0x22, 0x25, 0x25, 0x25, 0x19, 0x00, 0x00},
	'T': {0x01, 0x01, 0x3F, 0x01, 0x01, 0x00, 0x00},
	'U': {0x1F, 0x20, 0x20, 0x20, 0x1F, 0x00, 0x00},
	'V': {0x0F, 0x10, 0x20, 0x10, 0x0F, 0x00, 0x00},
	'W': {0x1F, 0x20, 0x1C, 0x20, 0x1F, 0x00, 0x00},
	'X': {0x21, 0x12, 0x0C, 0x12, 0x21, 0x00, 0x00},
	'Y': {0x07, 0x08, 0x30, 0x08, 0x07, 0x00, 0x00},
	'Z': {0x21, 0x31, 0x29, 0x25, 0x23, 0x00, 0x00},
	'0': {0x1E, 0x29, 0x25, 0x23, 0x1E, 0x00, 0x00},
	'1': {0x22, 0x21, 0x3F, 0x20, 0x20, 0x00, 0x00},
	'2': {0x22, 0x31, 0x29, 0x25, 0x23, 0x00, 0x00},
	'3': {0x12, 0x21, 0x25, 0x25, 0x1A, 0x00, 0x00},
	'4': {0x18, 0x14, 0x12, 0x3F, 0x10, 0x00, 0x00},
	'5': {0x17, 0x25, 0x25, 0x25, 0x19, 0x00, 0x00},
	'6': {0x1E, 0x25, 0x25, 0x25, 0x18, 0x00, 0x00},
	'7': {0x01, 0x01, 0x31, 0x09, 0x07, 0x00, 0x00},
	'8': {0x1A, 0x25, 0x25, 0x25, 0x1A, 0x00, 0x00},
	'9': {0x06, 0x29, 0x29, 0x29, 0x1E, 0x00, 0x00},
	' ': {0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
	'.': {0x00, 0x00, 0x00, 0x00, 0x20, 0x00, 0x00},
	',': {0x00, 0x00, 0x00, 0x20, 0x10, 0x00, 0x00},
	':': {0x00, 0x20, 0x00, 0x20, 0x00, 0x00, 0x00},
	'-': {0x08, 0x08, 0x08, 0x08, 0x08, 0x00, 0x00},
	'_': {0x20, 0x20, 0x20, 0x20, 0x20, 0x00, 0x00},
	'/': {0x10, 0x10, 0x08, 0x04, 0x02, 0x00, 0x00},
	'?': {0x02, 0x01, 0x29, 0x05, 0x02, 0x00, 0x00},
	'@': {0x1E, 0x21, 0x2D, 0x2D, 0x0E, 0x00, 0x00},
	'#': {0x14, 0x3F, 0x14, 0x3F, 0x14, 0x00, 0x00},
	'&': {0x10, 0x2A, 0x25, 0x2A, 0x18, 0x00, 0x00},
	'=': {0x14, 0x14, 0x14, 0x14, 0x14, 0x00, 0x00},
	'+': {0x08, 0x08, 0x3E, 0x08, 0x08, 0x00, 0x00},
	'(': {0x08, 0x10, 0x10, 0x10, 0x08, 0x00, 0x00},
	')': {0x04, 0x02, 0x02, 0x02, 0x04, 0x00, 0x00},
	'!': {0x00, 0x00, 0x3E, 0x00, 0x00, 0x00, 0x00},
	'\'': {0x00, 0x04, 0x02, 0x00, 0x00, 0x00, 0x00},
}

// drawTextBitmap 用 5×7 bitmap 字体绘制 ASCII 文本。CJK 字符替换为 '?'。
// 字符比例放大 6× 字号以匹配 1080×1350 画幅。
func drawTextBitmap(img *image.RGBA, x, y int, text string, c color.RGBA) {
	scale := 6
	px, py := x, y
	for _, r := range text {
		glyph, ok := asciiFont[r]
		if !ok {
			glyph = asciiFont['?']
		}
		for row := 0; row < 7; row++ {
			bits := glyph[row]
			for col := 0; col < 5; col++ {
				if bits&(1<<(4-col)) != 0 {
					drawRect(img, px+col*scale, py+row*scale, scale, scale, c)
				}
			}
		}
		px += 6 * scale // 5 + 1 列间距
		if px > img.Bounds().Max.X-30 {
			return
		}
	}
}

// truncateASCII 截断到 n 个字符；非 ASCII 用 '?' 替换。
func truncateASCII(s string, n int) string {
	if len(s) <= n {
		return s
	}
	out := make([]rune, 0, n+1)
	for _, r := range s {
		if r > 127 {
			r = '?'
		}
		out = append(out, r)
		if len(out) >= n {
			out = append(out, '…')
			break
		}
	}
	return string(out)
}

// wrapASCII 简易换行（按 rune 长度计）；CJK 字符视为 1 列。
func wrapASCII(s string, max int) []string {
	var lines []string
	cur := 0
	var line strings.Builder
	for _, r := range s {
		if r > 127 {
			r = '?'
		}
		if cur >= max {
			lines = append(lines, strings.TrimRight(line.String(), " "))
			line.Reset()
			cur = 0
		}
		line.WriteRune(r)
		cur++
	}
	if line.Len() > 0 {
		lines = append(lines, strings.TrimRight(line.String(), " "))
	}
	return lines
}

// stripHTMLTags 极简去标签；只用于截屏占位。
func stripHTMLTags(s string) string {
	var out strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == '<':
			in = true
		case r == '>':
			in = false
			out.WriteRune(' ')
		case !in:
			out.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(out.String()), " ")
}