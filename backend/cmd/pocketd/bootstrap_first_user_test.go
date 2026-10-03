package main

import (
	"strings"
	"testing"
)

func TestBootstrapDecision(t *testing.T) {
	t.Run("没有口令就不建（旧实现会拿 6 字符的 admin 去撞 8 字符下限，必然失败）", func(t *testing.T) {
		create, user, pass, note := bootstrapDecision("", "")
		if create {
			t.Fatalf("create = true with a built-in default password; " +
				"它只有 6 字符，必然被 validatePassword（>= 8）拒掉，" +
				"等于留下一条注定失败的恢复路径")
		}
		if pass != "" {
			t.Errorf("pass = %q, want empty", pass)
		}
		if user != "admin" {
			t.Errorf("user = %q, want admin", user)
		}
		// 说明必须讲清后果，否则运维会以为已经建好了。
		for _, must := range []string{"POCKET_AUTH_PASS", "401"} {
			if !strings.Contains(note, must) {
				t.Errorf("note 缺少 %q：运维需要知道怎么修、以及不修会怎样。note=%q", must, note)
			}
		}
	})

	t.Run("给了合规口令就建", func(t *testing.T) {
		// 刻意用一个中性占位串，而不是任何真实部署口令：这个文件会进 git，
		// 断言只需要「长度 >= 8」这一性质，不需要知道部署方用了什么。
		const pass = "unit-test-pass"
		create, user, got, note := bootstrapDecision("", pass)
		if !create || user != "admin" || got != pass || note != "" {
			t.Fatalf("create=%v user=%q pass=%q note=%q", create, user, got, note)
		}
	})

	t.Run("自定义用户名被尊重", func(t *testing.T) {
		_, user, _, _ := bootstrapDecision("alice", "unit-test-pass")
		if user != "alice" {
			t.Errorf("user = %q, want alice", user)
		}
	})
}
