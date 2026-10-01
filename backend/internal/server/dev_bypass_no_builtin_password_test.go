package server

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/config"
)

// exemptionLikeMarker 与 repohygiene 卡口用的是同一个标记名。
// 单独写一个字面量而不是跨包引用：repohygiene 那边是 _test.go 里的包内符号，
// 跨包引用测试符号会让两个包的测试编译耦合在一起。
const exemptionLikeMarker = "secret-scan-ok"

// readOwnSource 读回本文件被测的源文件。
// 用运行时路径而不是编译期常量，是为了让护栏在**任何 worktree / 任何机器**
// 上都能读到真实内容——护栏读不到源文件就必须失败，不能静默跳过。
func readOwnSource(t *testing.T) (string, error) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	p := filepath.Join(wd, "server_assistant.go")
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// hasHardcodedPasswordLiteral 找「引号包裹、>= 8 字符、含字母与数字」的字面量。
// 这正是仓库里那把泄漏口令的形状（Veritrans&9527 = 13 字符、字母+数字+符号）。
var literalRe = regexp.MustCompile(`["']([^"'\n$]{8,})["']`)

func hasHardcodedPasswordLiteral(line string) bool {
	for _, m := range literalRe.FindAllStringSubmatch(line, -1) {
		v := m[1]
		hasLetter := strings.ContainsAny(v, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")
		hasDigit := strings.ContainsAny(v, "0123456789")
		if hasLetter && hasDigit {
			return true
		}
	}
	return false
}

// dev 旁路不得有任何**内置缺省口令**。
//
// 背景（2026-10-02）：devBypassCredentials 曾在 DevAuthPass 为空时回落到一个
// 写死在源码里的口令。那把口令同时明文出现在 8 个受跟踪文件里
// （server_assistant.go、start-dev.sh、verify-stt*.ps1、verify-https-prod.mjs、
// e2e/web/helpers/*、e2e/android/local-agent-cdp.py），任何拿到仓库的人都知道。
// 上一轮 336c883 已经修掉 bootstrap 建号路径的同类问题，dev 旁路是残留入口。
//
// 这条护栏盯的是**形状**而不是某一把具体口令：任何在源码里出现的、看起来
// 像口令的字面量都是回归。写死具体口令会让下一个人换个口令就绕过它。
func TestDevBypassHasNoBuiltinDefaultPassword(t *testing.T) {
	// 没有显式配置口令时，旁路必须直接拒绝。
	s := &Server{cfg: config.Config{DevAuth: true, DevAuthUser: "admin"}}

	if _, ok := s.devBypassCredentials("admin", ""); ok {
		t.Fatal("DevAuthPass 为空时 dev 旁路仍然放行——内置缺省口令没有真正移除")
	}
	// 用一个「看起来像口令」的值也不该被放行：空配置下没有任何口令是合法的。
	for _, candidate := range []string{
		"Veritrans&9527", // 历史值：必须已被彻底移除
		"admin", "password", "12345678", "adminadmin",
	} {
		if _, ok := s.devBypassCredentials("admin", candidate); ok {
			t.Errorf("DevAuthPass 为空时 dev 旁路放行了口令 %q", candidate)
		}
	}
}

// 显式配置口令时旁路必须照常工作——否则这条护栏会把功能一起禁掉，
// 而「一个恒拒绝的旁路」和「一个恒放行的旁路」同样是坏护栏。
func TestDevBypassAcceptsExplicitlyConfiguredPassword(t *testing.T) {
	s := &Server{cfg: config.Config{
		DevAuth:     true,
		DevAuthUser: "admin",
		DevAuthPass: "Correct-Horse-Battery-9",
	}}

	if _, ok := s.devBypassCredentials("admin", "Correct-Horse-Battery-9"); !ok {
		t.Error("显式配置的正确口令被拒绝——本轮修复把 dev 旁路一起禁掉了")
	}
	if _, ok := s.devBypassCredentials("admin", "wrong-password"); ok {
		t.Error("错误口令被放行")
	}
	if _, ok := s.devBypassCredentials("notadmin", "Correct-Horse-Battery-9"); ok {
		t.Error("错误用户名被放行")
	}

	// 用户名留空时仍缺省 admin（这条行为没变，别顺手改掉）。
	s2 := &Server{cfg: config.Config{DevAuthPass: "Correct-Horse-Battery-9"}}
	if _, ok := s2.devBypassCredentials("admin", "Correct-Horse-Battery-9"); !ok {
		t.Error("DevAuthUser 留空时应缺省为 admin")
	}
}

// 卡口（internal/repohygiene）负责扫全仓的明文口令；这条负责钉住
// 「源码里不许再出现内置缺省」这个具体决策。两者都要有：卡口能发现新写的
// 硬编码，但发现不了「把默认值挪进配置文件读取逻辑」这种改写。
func TestDevAuthPassHasNoSourceLevelDefault(t *testing.T) {
	// 读自己的源文件，确认 devBypassCredentials 附近没有口令字面量。
	// 这里刻意不硬编码具体口令串——硬编码了就等于把它又抄了一遍。
	b, err := readOwnSource(t)
	if err != nil {
		t.Fatalf("读不到 server_assistant.go：%v", err)
	}
	const startMarker = "func (s *Server) devBypassCredentials("
	i := strings.Index(b, startMarker)
	if i < 0 {
		t.Fatal("devBypassCredentials 不见了——护栏需要跟着改名更新")
	}
	body := extractGoFuncBody(b, startMarker)

	// 函数体内出现引号包裹、长度 >= 8 的字面量就是回归（口令的最小形状）。
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, exemptionLikeMarker) {
			continue
		}
		if hasHardcodedPasswordLiteral(line) {
			t.Errorf("devBypassCredentials 内出现疑似硬编码口令字面量：%s", strings.TrimSpace(line))
		}
	}
}

// extractGoFuncBody 从 func 声明起，按大括号配平取出函数体。
//
// 不用 `strings.Index(body, "\n}\n")` 截断：第一版那么写，结果匹配到了
// 函数里**第一个**顶格 `}` —— 也就是内层 if 块的收尾，于是把后面整个文件
// 的内容都当成「函数体」来扫，报出一堆 writeError 的错误文案。
// 护栏本身出假阳性，比没有护栏更糟：它会被当成噪声忽略掉。
// 大括号配平不处理字符串/注释里的花括号，但函数体里那种写法极罕见，
// 且真出现了也只会让扫描范围略大，不会漏掉真正的口令。
func extractGoFuncBody(src, startMarker string) string {
	i := strings.Index(src, startMarker)
	if i < 0 {
		return ""
	}
	depth := 0
	started := false
	for j := i; j < len(src); j++ {
		switch src[j] {
		case '{':
			depth++
			started = true
		case '}':
			depth--
			if started && depth == 0 {
				return src[i : j+1]
			}
		}
	}
	return src[i:]
}
