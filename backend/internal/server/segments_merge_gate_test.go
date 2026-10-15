// segments_merge_gate_test.go — §100：`segmentsToText` 与界面**零耦合**的反证门。
//
// ── 这道门为什么存在 ────────────────────────────────────────────────
//
// 「把不同段放在一起校对合并」（`segmentsToText` 合并同说话人相邻段）
// 曾长期挂着一条待拍板理由：**与气泡行数耦合**。§100 用三条机械证据把那条
// 理由否掉了，并把否证本身钉成门 —— 一旦将来有人把服务端拼的转写接到界面上，
// 本条立刻转红，届时「改它不影响界面行数」这个前提就要重新评估。
//
// ── 那次改动本身已经撤回（§100.3 有真网关 A/B 读数）────────────────
//
// 这里**不**保留「合并后长什么样」的断言：那些断言测的是一个**没有落地**的行为，
// 留着就成了给一个不存在的行为背书。撤回的理由与数据写在
// server_meeting.go 的 `segmentsToText` 注释与设计文档 §100.3。
//
// ★ 这道门的方法论要点：**否证也要配门**。否则下一个人会重新把
//
//	「与气泡行数耦合」当成一条真实约束搬出来，而它既没有证据也没被验证过。
//
// ── ⚠⚠ 跑本门必须带 `-count=1`，否则你可能读到的是缓存里的旧绿（§430 实测）──
//
//	本门在**运行时**去读 `frontend/src`，而 `frontend/` 在 Go 模块
//	（`backend/`）**之外**。只改前端、不动 Go 包时，Go 的测试缓存不会失效：
//
//	$ printf '\nconst _p = segmentsToText(1)\n' >> frontend/src/features/meetings/meeting-dedup.ts
//	$ go test ./internal/server -run TestSegmentsToTextIsNotAUIRenderSource
//	ok  …/internal/server  (cached)      ← 门根本没跑，却报了绿
//	$ go test … -count=1
//	--- FAIL: 出现了 1 处 segmentsToText 引用
//
//	⇒ **结论只认 `-count=1` 的读数。** CI 已经这么做了
//	（`.github/workflows/backend.yml` 是 `go test -race ./... -count=1`），
//	所以 CI 不受影响；受影响的是本地重复跑。
//	⚠ `t.Setenv` **不能**让这道门免于缓存（实测：加了照样返回 cached）。
//	同形的门见 docs §430.5 清单（8 个读 frontend/ 的 Go 门）。
package server

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSegmentsToTextIsNotAUIRenderSource(t *testing.T) {
	const frontendRoot = "../../../frontend/src"
	const backendRoot = "../.."

	// ⚠ 负控就在同一条里：**先证明扫描器能咬住**。
	//   一个什么都扫不出来的扫描器满足「零引用」是**恒真**的 ——
	//   那样这道门会在最需要它的那天（有人真的接上了）保持全绿。
	//   toApiSegments 是前端**确实有**的（api/meetings.ts）。
	if n := len(countFilesWith(t, frontendRoot, "toApiSegments", false)); n == 0 {
		t.Fatalf("扫描器坏了：frontend/src 里连 toApiSegments 都找不到 ⇒ " +
			"后面「零引用」的结论会是恒真的")
	}

	// ① 前端零引用。
	if n := len(countFilesWith(t, frontendRoot, "segmentsToText", false)); n != 0 {
		t.Fatalf("frontend/src 出现了 %d 处 segmentsToText 引用 —— "+
			"服务端拼的转写一旦被界面消费，「改它不影响界面行数」这个前提就不成立了，"+
			"请重读 §100 的结论", n)
	}

	// ② 后端非测试引用只应存在于 server_meeting.go（定义 + 4 个拼提示词的调用点：
	//    summary agent / recommend / 一次性摘要 / 精校）。
	files := countFilesWith(t, backendRoot, "segmentsToText", false)
	if len(files) != 1 || filepath.Base(files[0]) != "server_meeting.go" {
		t.Fatalf("segmentsToText 的非测试引用应只在 server_meeting.go，实际 %v —— "+
			"新增调用点时确认它是拼提示词、不是渲染源", files)
	}
}

// ── 供本门复用的机械扫描 ────────────────────────────────────────────

// isSourceFile 把扫描范围限定在**源码**上。
//
// ⚠ 第一版没做这个过滤，于是后端扫描把编译产物 `backend/pocketd`
// 也算成了「一个引用 segmentsToText 的文件」，门当场转红。
// 那不是缺陷，是扫描器太宽 —— 但它顺带证明了这道门不是恒真的：
// 它确实会拒绝一个与被测性质不符的读数，而不是照单全收。
func isSourceFile(path string) bool {
	switch filepath.Ext(path) {
	case ".go", ".ts", ".tsx", ".vue", ".js", ".mjs":
		return true
	}
	return false
}

// countFilesWith 返回一棵目录树里（非测试文件）出现 needle 的文件路径。
//
// ⚠ 匹配前先剥注释（2026-10-11 补，见 stripComments 的说明）：
//
//	本门要证明的是「前端**消费**服务端的 segmentsToText」，
//	而 §427 为了说明「兜底词沿用另外两处已经在用的那个」，
//	在 meeting-dedup.ts 的**注释**里写了 `segmentsToText` ——
//	一个纯注释里的引用把门打红了，而那不是缺陷，是扫描器太宽。
//	同族的 dedup_threshold_sync_test.go 早就踩过同一个坑（见该文件的说明）。
func countFilesWith(t *testing.T, root, needle string, includeTests bool) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.Contains(path, "node_modules") || !isSourceFile(path) {
			return nil
		}
		isTest := strings.HasSuffix(path, "_test.go") ||
			strings.Contains(path, ".test.") || strings.Contains(path, "__tests__")
		if isTest && !includeTests {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil || !strings.Contains(stripComments(string(b)), needle) {
			return nil
		}
		out = append(out, path)
		return nil
	})
	if err != nil {
		t.Fatalf("遍历 %s: %v", root, err)
	}
	return out
}

// stripComments 把**注释内容**换成等长空格（换行与字符位置全部保留）。
//
// ⚠ 为什么要它（2026-10-11，§429 期间踩到）：
//
//	本门要证明的是「前端**消费**服务端拼的转写」，不是「前端提到过它」。
//	而 §427 在 meeting-dedup.ts 的注释里写了 `segmentsToText`
//	（说明兜底词沿用哪两处已有的），于是一处**纯注释**把门打红了。
//	⭐ 同族的 dedup_threshold_sync_test.go 早就踩过同一个坑，结论逐字是：
//	「TS 侧『已删除 MIN_ANCHOR』的那段**解释性注释里就写着 MIN_ANCHOR**。
//	  直接 grep 全文会把注释当成代码，判据立刻变成假红。」
//	那道门自带 stripGoComments；这道门一直没装，于是同一个坑等着下一个人。
//
// 与那道门的 stripGoCommentsKeepLines 有三点**有意**的差异：
//
//	① 跳过字符串/字符/反引号 ⇒ 文件里的 URL（https://）与模板串不会被截断；
//	   那版靠「`//` 前面是 `:`」这个启发式绕开，遇到别的形态仍会截断整行。
//	② 同样**等长替换** ⇒ 行数与下标不变，报错能指到真正的文件。
//	③ 不复用那版实现是因为它在 internal/stt，本门在 internal/server，
//	   Go 测试辅助函数无法跨包复用（本仓已有同源副本的先例）。
func stripComments(src string) string {
	out := []byte(src)
	i, n := 0, len(src)
	for i < n {
		switch c := src[i]; c {
		case '/':
			if i+1 < n && src[i+1] == '/' {
				for i < n && src[i] != '\n' {
					out[i] = ' '
					i++
				}
				continue
			}
			if i+1 < n && src[i+1] == '*' {
				for i < n && !(src[i] == '*' && i+1 < n && src[i+1] == '/') {
					if src[i] != '\n' {
						out[i] = ' '
					}
					i++
				}
				if i < n { // 吃掉收尾的 */
					out[i] = ' '
					if i+1 < n {
						out[i+1] = ' '
					}
					i += 2
				}
				continue
			}
			i++
		case '"', '\'', '`':
			q := c
			i++
			for i < n {
				if src[i] == '\\' && q != '`' {
					i += 2
					continue
				}
				if src[i] == q {
					i++
					break
				}
				// 未闭合（不该出现在源码里）：按行兜底，绝不把后面整份文件吃掉。
				if src[i] == '\n' && q != '`' {
					break
				}
				i++
			}
		default:
			i++
		}
	}
	return string(out)
}
