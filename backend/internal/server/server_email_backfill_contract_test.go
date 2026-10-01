package server

// server_email_backfill_contract_test.go — /api/email/backfill 的**跨语言**契约。
//
// 这个 handler 需要活的 Postgres（emailStore 是具体类型，不是接口，没法
// 打桩）加真 IMAP，端到端测试在这台机器上跑不起来，于是它的响应契约就没有
// 任何自动化保护。
//
// 而契约真的错过：handler 一度返回 `{"reports": [...]}`，前端
// emailApi.backfill 读的是 `res.accounts`，取不到就静默当成「0 封、也没
// 错误」——用户看到的是「回补完成」，实际什么都没发生。这类 bug 靠人工读
// 两侧代码能发现，但下一次改名就没人拦了。
//
// 所以这里直接把两边的源码钉在一起：前端读的键名必须出现在 handler 的
// 响应里，且 BackfillReport 的 JSON 字段名必须与前端类型声明逐字一致。

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/email"
)

// 测试的工作目录是 backend/internal/server，仓库根要往上三层。
const frontendAPIRel = "../../../frontend/src/api/email.ts"

func readFrontend(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.FromSlash(rel))
	if err != nil {
		t.Skipf("读不到前端源码 %s：%v（契约测试随前端一起跑）", rel, err)
	}
	return string(b)
}

// 前端 backfill() 声明的返回类型里，accounts 的元素类型有哪些字段；
// 这些字段必须与后端 BackfillReport 的 JSON 键**同名**。
func TestBackfillResponseMatchesFrontendContract(t *testing.T) {
	src := readFrontend(t, frontendAPIRel)

	// 取出 `accounts: Array<{ ... }>` 里的字段名。
	re := regexp.MustCompile(`accounts:\s*Array<\s*\{([^}]*)\}>`)
	m := re.FindStringSubmatch(src)
	if m == nil {
		t.Fatalf("前端 %s 里找不到 `accounts: Array<{...}>` 声明；契约变了，请同步更新本测试", frontendAPIRel)
	}
	// 字段用 `;` 分隔（`{ accountId: string; saved: number }`），
	// 按标识符 + 可选 `?` + `:` 来抓，不要按逗号切。
	frontFields := map[string]bool{}
	for _, f := range regexp.MustCompile(`(\w+)\s*\??\s*:`).FindAllStringSubmatch(m[1], -1) {
		frontFields[f[1]] = true
	}
	if len(frontFields) == 0 {
		t.Fatalf("没能从 %s 解析出 accounts 的字段", frontendAPIRel)
	}

	// 后端 BackfillReport 的 JSON 键。
	//
	// 用反射读 struct tag，而不是 marshal 一个空值再反序列化：
	// Error 带 `json:"error,omitempty"`，空值序列化后这个键根本不存在，
	// 会把「后端有、前端也声明了」误报成「后端没有这个字段」。
	backFields := map[string]bool{}
	rt := reflect.TypeOf(email.BackfillReport{})
	for i := 0; i < rt.NumField(); i++ {
		tag := rt.Field(i).Tag.Get("json")
		name := strings.Split(tag, ",")[0]
		if name == "" || name == "-" {
			name = rt.Field(i).Name
		}
		backFields[name] = true
	}

	for f := range frontFields {
		if !backFields[f] {
			t.Errorf("前端 accounts.%s 在后端 BackfillReport 的 JSON 里不存在；后端实际字段：%v", f, keys(backFields))
		}
	}
	// 反向：后端有而前端没声明的字段也要报，否则前端会静默丢掉错误信息。
	for k := range backFields {
		if !frontFields[k] {
			t.Errorf("后端 BackfillReport.%s 前端未声明，该字段不会被读取", k)
		}
	}
}

// handler 响应体的顶层键必须叫 "accounts"。
//
// 负控：把 handler 里的 "accounts" 改回 "reports"，本测试必须转红。
func TestBackfillHandlerWritesAccountsKey(t *testing.T) {
	src, err := os.ReadFile("server_email_backfill.go")
	if err != nil {
		t.Fatalf("读不到 server_email_backfill.go：%v", err)
	}
	// 剥注释，避免护栏被自己的说明文字满足。
	var b strings.Builder
	for _, line := range strings.Split(string(src), "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	code := b.String()

	if !strings.Contains(code, `"accounts":`) {
		t.Error(`handleEmailBackfill 的响应里没有 "accounts" 键；前端 emailApi.backfill 读的是 res.accounts，改名会让回补失败被静默吞掉`)
	}
	if regexp.MustCompile(`"reports"\s*:`).MatchString(code) {
		t.Error(`handleEmailBackfill 仍在写 "reports" 键`)
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
