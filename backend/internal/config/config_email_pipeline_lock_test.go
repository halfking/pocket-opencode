package config

// config_email_pipeline_lock_test.go — 每日定时流水线的跨进程锁开关。
//
// ## 为什么单独立一条
//
// 这条开关是整个多实例修复的**总闸**。它的失效模式是静默的：把它默认改成
// false，或者某个部署的 .env 里留着 POCKET_EMAIL_PIPELINE_ADVISORY_LOCK=false，
// 代码路径完全不变、测试（除本文件）全绿，只有到 08:00 才发现提醒又被推了
// 三份。所以默认值与解析规则都要有判据。
//
// 参照：POCKET_SCHEDULER_ADVISORY_LOCK 也存在于 config，但全仓**零消费方**
// （只有 config.go:195 的声明与 :340 的赋值）。那是一个声称存在、实际不生效
// 的开关。本文件顺带钉住"新开关真的被读"，避免重蹈覆辙。

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEmailPipelineAdvisoryLock_DefaultsToTrue(t *testing.T) {
	for _, key := range []string{
		"POCKET_EMAIL_PIPELINE_ADVISORY_LOCK",
		// LoadDefaults 会读这两个，别让外部环境污染默认值断言。
		"POCKET_ENV",
		"POCKET_JWT_SECRET",
		"POCKET_DEV_AUTH",
	} {
		t.Setenv(key, "")
		_ = os.Unsetenv(key)
	}
	cfg := Load()
	if !cfg.EmailPipelineAdvisoryLock {
		t.Fatal("POCKET_EMAIL_PIPELINE_ADVISORY_LOCK must default to true: " +
			"多个 pocketd 共享一个数据库时，进程内互斥拦不住它们，默认关等于" +
			"让重复推送默认开启")
	}
}

// 解析规则与本仓库既有惯例一致（见 POCKET_EMAIL_SPAM_DRYRUN）：
// **只有精确的 "true" 才开**，其余一切值都关。
//
// 这里如实记下一个风险，而不是把它写成一条我擅自发明的"更安全"的规则：
// 因为默认值是 true（安全方向），任何拼错的取值——"TRUE"、"1"、" true "、
// "yes"——都会**关掉**多实例保护，而代码路径完全不变、其它测试全绿。
// 判据锁住的是当前真实契约：改解析规则会红，届时要同步改这里并更新部署文档。
func TestEmailPipelineAdvisoryLock_OnlyExactTrueEnables(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{"true", true},
		{"false", false},
		{"", true}, // 未设置 => 走默认 "true"
		// 以下全部关闭（与 POCKET_EMAIL_SPAM_DRYRUN 同一套 getEnv 语义）。
		{"0", false},
		{"no", false},
		{"off", false},
		{"FALSE", false},
		{"False", false},
		{" true ", false},
	}
	for _, c := range cases {
		t.Setenv("POCKET_EMAIL_PIPELINE_ADVISORY_LOCK", c.raw)
		got := Load().EmailPipelineAdvisoryLock
		if got != c.want {
			t.Errorf("POCKET_EMAIL_PIPELINE_ADVISORY_LOCK=%q -> %v, want %v", c.raw, got, c.want)
		}
	}
}

// 逃生门必须真的能关掉锁，否则无 PG 的本地部署没有出路。
func TestEmailPipelineAdvisoryLock_CanBeDisabledExplicitly(t *testing.T) {
	t.Setenv("POCKET_EMAIL_PIPELINE_ADVISORY_LOCK", "false")
	if Load().EmailPipelineAdvisoryLock {
		t.Fatal("explicit \"false\" must disable the lock (escape hatch for " +
			"deployments without PostgreSQL)")
	}
}

// 反向护栏：POCKET_SCHEDULER_ADVISORY_LOCK 是个**死配置**——全仓只有声明与
// 赋值，没有消费方。这条用例把「它当前确实是死的」记成一个会主动失败的事实，
// 这样哪天有人真的把它接上了（那是好事），本文件会立刻转红提醒更新注释，
// 而不是让「scheduledtask 也缺跨进程锁」这个结论被时间冲淡。
func TestSchedulerAdvisoryLock_IsStillUnconsumed(t *testing.T) {
	repoRoot := repoRootForTest(t)
	consumers := 0
	err := filepath.Walk(repoRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			base := info.Name()
			if base == ".git" || base == "node_modules" || base == "vendor" || base == ".wt-e2e" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		if filepath.Base(path) == "config.go" {
			return nil // 声明与赋值本身不算消费方
		}
		// 本文件自己就写了这个标识符（注释里解释它为什么是死配置），
		// 不跳过就会自己扫到自己、永远判定"已有消费方"。
		if filepath.Base(path) == "config_email_pipeline_lock_test.go" {
			return nil
		}
		if filepath.Base(path) == filepath.Base(os.Args[0]) {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if containsAnyBytes(b, "SchedulerAdvisoryLock") {
			consumers++
			t.Logf("consumer found: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Skipf("cannot walk repo root %s: %v", repoRoot, err)
	}
	if consumers > 0 {
		t.Fatalf("SchedulerAdvisoryLock now has %d consumer(s) — it is no longer a dead "+
			"config. Update the comment in this file and re-audit whether the "+
			"scheduledtask dispatcher still lacks cross-process locking", consumers)
	}
}

func containsAnyBytes(b []byte, needle string) bool {
	n := []byte(needle)
	if len(n) == 0 || len(b) < len(n) {
		return false
	}
	for i := 0; i+len(n) <= len(b); i++ {
		match := true
		for j := range n {
			if b[i+j] != n[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// repoRootForTest 从本文件位置向上找 go.mod。
func repoRootForTest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Skip("cannot locate go.mod; skipping dead-config audit")
	return ""
}
