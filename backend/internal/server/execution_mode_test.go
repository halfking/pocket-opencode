package server

// execution_mode_test.go — 需求 6「默认放在设备本地进行」的判定契约。
//
// 这条判定原先埋在 runEmailPipeline 里、没有任何测试守护。风险很实：把
// `mode == "server"` 改成 `mode != ""` 就会让**默认**变成委托，而被委托的
// 是带邮箱权限的整条流水线（会被 POST 到远端编排服务）。这类改动不会让任何
// 现有测试变红。
//
// 负控对照：把判定改成「非空即委托」，DefaultModeIsLocal / MissingURLFallsBack
// / WhitespaceOnly 两个用例必须转红。

import "testing"

func TestShouldDelegatePipeline_DefaultModeIsLocal(t *testing.T) {
	// 默认配置：POCKET_EMAIL_EXECUTION_MODE 默认 "local"，URL 为空。
	if shouldDelegatePipeline("local", "") {
		t.Fatal("local mode must never delegate")
	}
	if shouldDelegatePipeline("", "") {
		t.Fatal("empty mode must default to local (requirement 6: 默认放在设备本地)")
	}
}

func TestShouldDelegatePipeline_ServerWithURLDelegates(t *testing.T) {
	// 唯一会委托的组合：显式 server + 配了远端地址。
	if !shouldDelegatePipeline("server", "http://pipeline.internal/run") {
		t.Fatal("server mode with a URL must delegate")
	}
}

func TestShouldDelegatePipeline_MissingURLFallsBackToLocal(t *testing.T) {
	// 配了 server 却没给 URL：没法委托，落回**本地**而不是报错。
	// 这里绝不能委托——delegatePipeline 会因空 URL 直接返回错误，
	// 等于本轮流水线什么都没跑。
	if shouldDelegatePipeline("server", "") {
		t.Fatal("server mode without a URL must fall back to local, not delegate into an error")
	}
	if shouldDelegatePipeline("server", "   ") {
		t.Fatal("whitespace-only URL must count as missing")
	}
}

func TestShouldDelegatePipeline_CaseAndSpaceTolerant(t *testing.T) {
	// 环境变量值常带大小写/空白差异：' Server ' 应被认作 server 并委托。
	if !shouldDelegatePipeline(" Server ", "http://pipeline.internal/run") {
		t.Fatal("mode should be trimmed and case-insensitive")
	}
	// 反过来，'local' 的各种写法都不该委托。
	for _, m := range []string{"LOCAL", " local ", "Local", "device", "standalone"} {
		if shouldDelegatePipeline(m, "http://pipeline.internal/run") {
			t.Fatalf("mode %q must not delegate", m)
		}
	}
}
