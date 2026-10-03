package email

import (
	"strings"
	"testing"
)

// classify_skip_reason_test.go —— 「同步成功了但自动分类根本没跑」必须有话说。
//
// ## 缺陷
//
// scheduler.go 原本是
//
//	if s.kxmem == nil || userID == "" || !ShouldProcessAfterFetch(1, n) {
//	    return
//	}
//
// 三个条件任一成立就 return，**不记日志、不报错、不计数**。而
// `POCKET_KXMEMORY_BASE_URL` 未配置时 s.kxmem 恒为 nil，也就是
// 每次同步之后自动分类都被静默跳过：邮件进来了、importance 永远空、
// 需求 4 永远不提醒。
//
// ## 为什么单看诊断页看不出来
//
// integration_status.go 会分别报 kxmemory disabled 与 llm-gateway enabled。
// 两条**单看都准确**，合起来却误导：看到网关在跑，自然以为自动分类有
// 兜底。而兜底只接在 HTTP 端点（server 包的 classifyViaGateway）上，
// Scheduler 在 internal/email 包里拿不到它。
//
// 本文件钉住「跳过时必须能说出原因」。

func TestClassifySkipReason_ExplainsMissingKxmemory(t *testing.T) {
	got := ClassifySkipReason(false, "user-admin")
	if got == "" {
		t.Fatal("kxmemory 未配置却返回空串 —— 定时路径会静默跳过自动分类，" +
			"而这个死路径在日志和报告里都没有任何痕迹")
	}
	// 文案必须点出两件最关键的事：为什么跳过、以及手动路径不受影响。
	// 只说「跳过」而不说「手动能跑」的话，运维会得出「整个分类功能坏了」
	// 的错误结论，转而去查一个其实正常的手动路径。
	if !strings.Contains(got, "POCKET_KXMEMORY_BASE_URL") {
		t.Errorf("文案没有点出环境变量名，运维不知道该查什么: %q", got)
	}
	if !strings.Contains(got, "/api/emails/classify") {
		t.Errorf("文案没有说明手动路径不受影响，会误导成「分类功能整体不可用」: %q", got)
	}
}

func TestClassifySkipReason_ExplainsMissingUserOwner(t *testing.T) {
	got := ClassifySkipReason(true, "")
	if got == "" {
		t.Fatal("账户无归属却返回空串 —— 这同样是一条静默跳过")
	}
	// 这个原因必须与 kxmemory 未配置**区分开**：两者的处置完全不同
	// （一个是环境变量没配，一个是账户数据脏），混成一句话就没法分流。
	if strings.Contains(got, "POCKET_KXMEMORY_BASE_URL") {
		t.Errorf("无归属的原因被说成了 kxmemory 问题，处置方向会跑偏: %q", got)
	}
	if !strings.Contains(got, "user") {
		t.Errorf("文案没有点出是 user 归属问题: %q", got)
	}
}

// 对照组：一切正常时**不得**返回原因。
//
// 这是防过度修复的那一半：若让 ClassifySkipReason 在正常路径也返回非空，
// 每轮都会打告警，真正出事时反而被淹没。
func TestClassifySkipReason_NormalPathIsSilent(t *testing.T) {
	if got := ClassifySkipReason(true, "user-admin"); got != "" {
		t.Fatalf("kxmemory 已配且 userID 非空，却返回 %q —— 正常路径不该被判为跳过", got)
	}
}
