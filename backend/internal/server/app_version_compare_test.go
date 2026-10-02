package server

import "testing"

// 这些用例里的 a/b 取值不是随手编的：它们逐条对应 2026-10-03 用与
// server.go:2521 **完全相同**的表达式实测出来的错误结果
// （两边 build 号相等以隔离出字符串比较这一项）。
// 旧实现的真实输出记在 oldLexicographic 注释里。
func TestVersionLess_RejectsTheLexicographicTraps(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want bool
		// 旧实现（直接 `a < b`）算出来的值，仅作文档。
		oldLexicographic bool
	}{
		{"相同版本不算有更新", "1.2.0", "1.2.0", false, false},
		{"正常升级", "1.2.0", "1.3.0", true, true},
		{"本机比服务端新", "1.3.0", "1.2.0", false, false},

		// ↓ 下面三条旧实现全错，是这个测试存在的理由
		{"1.9 的设备必须收到 1.10", "1.9.0", "1.10.0", true, false},
		{"比服务端新时不得提示降级", "1.10.0", "1.9.0", false, true},
		{"补丁号进位也必须被识别", "1.10.2", "1.10.10", true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := versionLess(c.a, c.b); got != c.want {
				t.Fatalf("versionLess(%q, %q) = %v, want %v（旧实现给的是 %v）",
					c.a, c.b, got, c.want, c.oldLexicographic)
			}
		})
	}
}

func TestVersionLess_Normalisation(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{"缺位补零后相等", "1.2", "1.2.0", false},
		{"v 前缀", "v1.3.0", "1.2.0", false},
		{"v 前缀仍能比较", "v1.2.0", "v1.3.0", true},
		{"构建元数据不影响优先级", "1.2.0+openpocket", "1.2.0", false},
		{"预发布比正式版旧", "1.2.0-rc1", "1.2.0", true},
		{"正式版不比预发布旧", "1.2.0", "1.2.0-rc1", false},
		{"两个预发布按字典序", "1.2.0-alpha", "1.2.0-beta", true},
		{"空串不得被当成最小", "", "1.0.0", true},
		{"两侧都空视为相等", "", "", false},
		// 非数字分量没有「正确答案」可言，函数只保证**有定义且反对称**。
		// 实际规则是「数字分量比非数字的新」（app_version_compare.go 里有说明）。
		{"非数字分量有定义", "1.x.0", "1.y.0", true},
		// ↓ round29 更正：下面三条原本断言的是**反的方向**。
		// 实现写的是 `return aok`，即「a 是数字 => a 更旧」，与它自己上面两行
		// 注释、以及这条用例的名字全都相反：名字叫「数字分量优先于非数字」，
		// 断言却是 versionLess("1.10.0","1.x.0")==true，也就是 1.10.0 更旧。
		// 代码与用例彼此自洽，所以从来没红过——错的是方向本身。
		{"数字分量新于非数字", "1.10.0", "1.x.0", false},
		{"非数字分量更旧", "1.x.0", "1.10.0", true},
		{"数字/非数字顺序必须反对称", "1.2.0", "1.2.x", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := versionLess(c.a, c.b); got != c.want {
				t.Fatalf("versionLess(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
			}
		})
	}
}

// 这一组直接跑**生产判定本身**（hasUpdateAvailable），而不是只测 versionLess。
// 理由：只测 versionLess 的话，把 server.go 的调用点退回裸字符串比较，
// 这些用例会照样全绿 —— 一条装饰性护栏比没有护栏更糟。
// build 号在两条用例里都取相同值，把变量隔离到版本字符串这一项。
func TestHasUpdateAvailable_VersionStringIsTheDecidingTerm(t *testing.T) {
	cases := []struct {
		name           string
		cur, latest    string
		want           bool
		oldLexicograph bool
	}{
		{"同版本同 build：无更新", "1.2.0", "1.2.0", false, false},
		{"版本落后：有更新", "1.2.0", "1.3.0", true, true},
		{"版本领先：无更新", "1.10.0", "1.9.0", false, true},
		{"小数进位必须有更新", "1.9.0", "1.10.0", true, false},
		{"补丁进位必须有更新", "1.10.2", "1.10.10", true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := hasUpdateAvailable(c.cur, 5, c.latest, 5) // build 相等 => 只剩版本比较
			if got != c.want {
				t.Fatalf("hasUpdateAvailable(%q, 5, %q, 5) = %v, want %v（旧实现给 %v）",
					c.cur, c.latest, got, c.want, c.oldLexicograph)
			}
		})
	}
}

// build 号是独立的第二判据：版本字符串完全相同，build 落后仍然要提示更新。
// 真机上就是这个形态（客户端 version.ts 的 buildNumber 硬编码成 2）。
func TestHasUpdateAvailable_BuildNumberStillDecidesOnItsOwn(t *testing.T) {
	if !hasUpdateAvailable("1.2.0", 2, "1.2.0", 3) {
		t.Fatal("版本相同但 build 落后时必须提示更新")
	}
	if hasUpdateAvailable("1.2.0", 3, "1.2.0", 3) {
		t.Fatal("版本与 build 都相同时不得提示更新")
	}
}

// 解析不了的 latest 版本号**不得**被当成升级推出去。
//
// 这条直接跑生产判定而不是 versionLess，理由与上面那条一样：判据必须落在
// handleCheckUpdate 真正调用的那个函数上，否则把调用点换掉它照样绿。
//
// 2026-10-03 round29 实测：修正前 hasUpdateAvailable("1.10.0",5,"1.x.0",5)
// 返回 true —— 服务端版本号里只要有一个脏字符，全体客户端就被告知
// 「有新版本」，去拉一个谁也解析不了的包，而且没有任何告警。
func TestHasUpdateAvailable_MalformedLatestIsNeverOfferedAsUpgrade(t *testing.T) {
	cases := []struct {
		name        string
		cur, latest string
	}{
		{"字母分量", "1.10.0", "1.x.0"},
		{"尾部字母分量", "1.2.0", "1.2.x"},
		{"首段就是字母", "1.10.0", "x.10.0"},
		{"夹在中间", "1.10.0", "1.10.beta0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// build 号两边取相同值，把变量隔离到版本字符串这一项。
			// 不隔离的话，latestBuild 一旦更大，|| 的右半边会把它盖成 true，
			// 这条判据就永远绿不了、也永远红不了——等于没写。
			if hasUpdateAvailable(c.cur, 5, c.latest, 5) {
				t.Fatalf("latest=%q 无法解析，hasUpdateAvailable(%q,5,%q,5) 仍为 true："+
					"不得把解析不了的版本当成升级推给客户端", c.latest, c.cur, c.latest)
			}
		})
	}
}
