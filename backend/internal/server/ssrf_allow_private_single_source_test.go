package server

// ssrf_allow_private_single_source_test.go —— §691：同一 env 变量的两处判定必须是同一条规则。
//
// ── 缺陷形态 ──────────────────────────────────────────────────────
//
// `POCKET_LLM_GATEWAY_ALLOW_PRIVATE`（安全审计 SEC-08 记为「**高影响放宽开关**」）
// 曾被**两个地方各判一次**：
//
//	ssrf.go 的闸门        strings.TrimSpace(...) 后只认 "1" / "true" / "yes"
//	cmd/pocketd 启动日志   os.Getenv(...) != ""            ← 任何非空字符串都算 true
//
// 而 `backend/.env.example:298` 写的是：
//
//	POCKET_LLM_GATEWAY_ALLOW_PRIVATE=false
//
// **"false" 是非空字符串** ⇒ 照示例文件部署的每个实例，
// 启动日志都会打 `allow_private_hosts=true`，而实际闸门是**关**的。
// ⇒ 报错的方向尤其恶劣：运维想确认「放宽是关的」时，日志恰恰说它是开的。
//
// 修法不是「把日志改对」，而是**让日志复用闸门那条规则的唯一实现**
// （`server.LLMGatewayAllowPrivate()`）——否则下一次改规则还会漂。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLLMGatewayAllowPrivateTruthTable(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{"1", true}, {"true", true}, {"TRUE", true}, {"True", true},
		{"yes", true}, {"YES", true}, {" yes ", true}, {"1 ", true},
		{"", false},
		{"0", false}, {"false", false}, {"FALSE", false}, {"no", false},
		{"off", false}, {"   ", false}, {"\t\n", false}, {"true1", false},
	}
	for _, c := range cases {
		t.Setenv("POCKET_LLM_GATEWAY_ALLOW_PRIVATE", c.raw)
		if got := LLMGatewayAllowPrivate(); got != c.want {
			t.Errorf("LLMGatewayAllowPrivate(%q) = %v, want %v", c.raw, got, c.want)
		}
		// 未导出的那条必须与导出的**完全一致**（它已被三处调用）
		if got := gatewayAllowPrivate(); got != c.want {
			t.Errorf("gatewayAllowPrivate(%q) = %v, want %v（两处必须同源）", c.raw, got, c.want)
		}
	}
}

// 反向：.env.example 里的那个值必须判为「关」——
// 这是本缺陷在**真实默认值**下的那一条，不是构造出来的边角。
func TestEnvExampleValueIsNotEnabled(t *testing.T) {
	p := filepath.Join("..", "..", ".env.example")
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读不到 .env.example：%v", err)
	}
	var line string
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "POCKET_LLM_GATEWAY_ALLOW_PRIVATE=") {
			line = strings.TrimSpace(l)
			break
		}
	}
	if line == "" {
		t.Fatalf(".env.example 里没有 POCKET_LLM_GATEWAY_ALLOW_PRIVATE 那一行")
	}
	val := strings.TrimSpace(strings.SplitN(line, "=", 2)[1])
	t.Setenv("POCKET_LLM_GATEWAY_ALLOW_PRIVATE", val)
	if LLMGatewayAllowPrivate() {
		t.Fatalf(".env.example 的值 %q 被判成**开启**放宽 —— "+
			"这正是 §691 那个缺陷：日志会据此打出 allow_private_hosts=true", val)
	}
}

// 防回归的**形状**判据：启动日志那行不得再出现裸的 `!= ""` 判定。
//
// ⚠ 这一条不是「断言一个字符串在不在」，而是断言**唯一实现**这件事：
// 只要有人把第二份判定写回 main.go，它就红。
func TestPocketdStartupLogHasNoSecondJudge(t *testing.T) {
	p := filepath.Join("..", "..", "cmd", "pocketd", "main.go")
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读不到 main.go：%v", err)
	}
	for i, l := range strings.Split(string(raw), "\n") {
		if !strings.Contains(l, "POCKET_LLM_GATEWAY_ALLOW_PRIVATE") {
			continue
		}
		if strings.Contains(l, `!= ""`) {
			t.Errorf("cmd/pocketd/main.go:%d 又出现了一份 ALLOW_PRIVATE 判定：%s\n"+
				"必须复用 server.LLMGatewayAllowPrivate()，否则两处规则会再次漂",
				i+1, strings.TrimSpace(l))
		}
	}
}
