package server

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/opencode"
)

// 前端 `frontend/src/constants/llm-gateway.ts` 顶部写着「与后端
// DefaultLLMGatewayBaseURL / DefaultLLMGatewayPreferredModels 同源」。
//
// 在这个测试出现之前，那句话**只是一句注释，没有任何东西盯着它**。
// 2026-10-02 实测漂移就是这样发生的：用户把首选模型从 glm-5.2 改成 glm-5.3，
// 只改了后端常量的场景里，前端那份兜底预填仍然是 glm-5.2 —— 于是
// 「后端没起来时的兜底预填」和「后端起来了的 seed」给出两个不同的首选项，
// 而两侧都不会报错。离线/首次安装的用户看到的默认模型与在线用户不同。
//
// 这道测试把「同源」变成可判定的：两份列表必须逐项相同（顺序也算）。
const frontendLLMGatewayConst = "../../../frontend/src/constants/llm-gateway.ts"

// 单引号字符串字面量，够用且不会误吃注释里的内容。
var frontendModelRe = regexp.MustCompile(`'([a-z0-9][a-z0-9._-]*)'`)

// 解析 DEFAULT_GATEWAY_PREFERRED_MODELS 数组字面量里的模型 id。
func parseFrontendPreferredModels(t *testing.T, src string) []string {
	t.Helper()
	start := bytes.Index([]byte(src), []byte("DEFAULT_GATEWAY_PREFERRED_MODELS"))
	if start < 0 {
		t.Fatalf("前端常量里找不到 DEFAULT_GATEWAY_PREFERRED_MODELS —— " +
			"要么它被改名/删除（那 frontend/src/constants/llm-gateway.ts 的导出契约就破了），" +
			"要么本测试的解析该跟进了")
	}
	rest := src[start:]
	// 必须从 `= [` 起算，不能从名字之后找第一个 `[`。
	// 声明形如 `export const X: readonly string[] = [ ... ]`，
	// 名字之后第一个 `[` 落在**类型标注** `string[]` 上，取到的区间是 `[]`。
	// 这个坑真的踩过：解析出 0 个模型，是下面那道自检把它抓出来的。
	assign := bytes.Index([]byte(rest), []byte("= ["))
	if assign < 0 {
		t.Fatalf("找不到 `= [` 赋值，本测试的解析器跟不上了（常量可能改成了别的写法）")
	}
	rest = rest[assign+len("= ["):]
	open, closing := 0, bytes.IndexByte([]byte(rest), ']')
	if closing < 0 {
		t.Fatalf("数组没有闭合的 `]`")
	}
	var out []string
	for _, m := range frontendModelRe.FindAllStringSubmatch(rest[open:closing+1], -1) {
		out = append(out, m[1])
	}
	if len(out) == 0 {
		t.Fatalf("从前端常量里解析出 0 个模型，解析器多半已经失效（" +
			"判据恒真/恒假时最容易被读成「通过了」）")
	}
	return out
}

func TestFrontendLLMGatewayDefaultsMatchBackend(t *testing.T) {
	raw, err := os.ReadFile(filepath.FromSlash(frontendLLMGatewayConst))
	if err != nil {
		t.Fatalf("读前端常量失败：%v", err)
	}
	src := string(raw)

	got := parseFrontendPreferredModels(t, src)
	want := opencode.DefaultLLMGatewayPreferredModels
	if !slices.Equal(got, want) {
		t.Fatalf("前端兜底预填与后端 seed 不一致：\n  前端 %s\n  后端 %s\n"+
			"前端这份是「后端没起来时的兜底预填」（见该文件头注释），" +
			"两边不一致意味着离线用户和在线用户看到的默认模型不同，且不会有任何报错。",
			got, want)
	}

	// baseURL 同样同源，一并钉住。
	if !bytes.Contains(raw, []byte(opencode.DefaultLLMGatewayBaseURL)) {
		t.Fatalf("前端常量里没有后端的默认 baseURL %q —— 同源约定对地址也成立",
			opencode.DefaultLLMGatewayBaseURL)
	}
}
