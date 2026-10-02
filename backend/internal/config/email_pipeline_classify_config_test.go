package config

// email_pipeline_classify_config_test.go —
// POCKET_EMAIL_CLASSIFY_VIA_GATEWAY 的默认值契约。
//
// ## 为什么这条要单独测
//
// 这个开关决定每日流水线**会不会自动发真实 LLM 请求**。默认值等于替用户
// 决定要不要花钱，所以它是一条安全属性，不是普通配置项：
//
//   - 默认必须是 false（关）。写成 `getEnv(...) == "1"` 或把默认值写成
//     "true"，后果是**开机即产生持续费用**，而且不会有任何报错。
//   - 只有精确的 "true" 才开。写成 `!= ""` 或 `!= "false"` 时，
//     任何一次误设（比如写了 "0"）都会变成开启。
//
// 只测「true 能开」是不够的：那正是上面两条错误实现都能通过的用例。
// 所以下面三条分别钉住「默认关」「只认 true」「误设不误开」。

import "testing"

// t.Setenv 会自动恢复，所以同一个进程里可以连续测多个值。
func TestEmailClassifyViaGateway_DefaultsToOff(t *testing.T) {
	t.Setenv("POCKET_EMAIL_CLASSIFY_VIA_GATEWAY", "")
	cfg := Load()
	if cfg.EmailClassifyViaGateway {
		t.Fatal("POCKET_EMAIL_CLASSIFY_VIA_GATEWAY 未设置时必须是 false —— " +
			"这个开关会每天自动产生真实 LLM 调用与费用，默认不能让替用户做这个决定")
	}
}

func TestEmailClassifyViaGateway_OnlyExactTrueEnables(t *testing.T) {
	t.Setenv("POCKET_EMAIL_CLASSIFY_VIA_GATEWAY", "true")
	if cfg := Load(); !cfg.EmailClassifyViaGateway {
		t.Fatal("显式 \"true\" 必须开启")
	}
}

// 「误设」这一类才是真实事故的形态：运维想关，或者随手写了 0/否/关，
// 结果账单一直涨。这条把它们逐个钉成 false。
func TestEmailClassifyViaGateway_MisconfigurationDoesNotEnable(t *testing.T) {
	for _, v := range []string{"0", "false", "False", "no", "off", "yes", "1", "TRUE", " true"} {
		t.Setenv("POCKET_EMAIL_CLASSIFY_VIA_GATEWAY", v)
		if cfg := Load(); cfg.EmailClassifyViaGateway {
			t.Errorf("POCKET_EMAIL_CLASSIFY_VIA_GATEWAY=%q 时被开启，want false"+
				"（只认精确的 \"true\"）", v)
		}
	}
}
