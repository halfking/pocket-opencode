// app_version_compare.go — 更新检查用的版本号比较。
//
// 背景（2026-10-03 真机走查时发现）：handleCheckUpdate 原本直接对版本号做
// 字符串比较（server.go:2521 的 `req.CurrentVersion < latestVersion.Version`），
// 两个操作数都是 string，Go 走的是**字节字典序**。字典序只在「每个分量都是
// 一位数」时才碰巧等于版本序；从 1.10 / 1.10.10 开始就全错。
//
// 用与 server.go:2521 完全相同的表达式实测（两边 build 号相等以隔离变量）：
//
//	1.2.0    vs 1.2.0     -> false   ✓
//	1.2.0    vs 1.3.0     -> true    ✓
//	1.9.0    vs 1.10.0    -> false   ✗  1.9 的设备永远收不到 1.10
//	1.10.0   vs 1.9.0     -> true    ✗  比服务端新却被通知降级
//	1.10.2   vs 1.10.10   -> false   ✗  补丁级更新漏掉
//
// 最后一个症状最隐蔽：它不报错、不崩溃，只是**永远不推送**，没有任何告警。
//
// 这也正是本项目已经吃过亏的形态——「设备上的 APK 是旧的」在
// docs/handoff/2026-09-30-android-e2e-bug-d-e-f.md §4.74.2 记过一次，
// 代价是整轮验收在测一个过时产物却毫无察觉。更新通道少一次推送，
// 比那次更安静。
package server

import (
	"strconv"
	"strings"
)

// hasUpdateAvailable 是 handleCheckUpdate 的判定本身，抽成纯函数只为可测。
//
// 抽出来不是为了好看：只测 versionLess 的话，把 server.go 的调用点退回裸字符串
// 比较，测试**照样全绿** —— 那是一条装饰性护栏，比没有更糟（下一个维护者
// 会以为更新通道有人守着）。让测试直接跑这条判定，回退调用点才会红。
func hasUpdateAvailable(currentVersion string, currentBuild int, latestVersion string, latestBuild int) bool {
	return versionLess(currentVersion, latestVersion) || currentBuild < latestBuild
}

// versionLess 报告版本 a 是否**严格早于** b。
//
// 规则：
//   - 去掉开头的 v/V；
//   - 截断预发布/构建后缀（`-openpocket`、`+abc`）——构建变体不该改变版本序，
//     否则「1.2.0-openpocket」会被当成比「1.2.0」更新的东西；
//   - 按 `.` 拆成数字分量，逐位比较，缺位当 0（1.2 == 1.2.0）；
//   - 某一位不是纯数字时，规定「数字分量比非数字的新」。非数字版本号
//     （"1.x.0"）没有「正确答案」可言，这里只保证函数对任意输入都有定义、
//     且顺序反对称，不会 panic、也不会静默把两边判成相等。
//
// 「数字更新」这个方向不是随便挑的，它决定了一个真实的生产后果。
// 方向若反过来（把无法解析的版本号当成更新的），那么**服务端 latest 版本号
// 只要有一个字符是脏的**（手写错一个字母、CI 里读了个空串再拼上后缀），
// 全体客户端就会收到「有更新」，去下载一个谁也解析不了的版本。
// 2026-10-03 round29 实测：反向实现下
//
//	hasUpdateAvailable("1.10.0", 5, "1.x.0", 5) == true
//
// 改成「数字更新」后同一条为 false —— 推不出去，方向是安全的那一侧。
//
// 刻意不做的事：不做 semver 的 pre-release 优先级（1.0.0-alpha < 1.0.0）。
// 本项目发布号从不带 alpha/beta，带上的话按上面的规则会截断后缀，
// 结果是 1.0.0-alpha == 1.0.0；真要区分再说，别在没有用例时先写进去。
func versionLess(a, b string) bool {
	av, apre := splitVersion(a)
	bv, bpre := splitVersion(b)
	n := len(av)
	if len(bv) > n {
		n = len(bv)
	}
	for i := 0; i < n; i++ {
		an, aok := component(av, i)
		bn, bok := component(bv, i)
		switch {
		case aok && bok:
			if an != bn {
				return an < bn
			}
		case aok != bok:
			// 一边是纯数字一边不是：数字分量更「新」（1.2.0 新于 1.2.x）。
			// 所以 a 是数字 => a 更新 => a 不比 b 旧 => false；
			//        a 解析失败   => a 更旧 => true。
			// 写成 `return aok` 会把整个方向反过来——round29 实测过那个版本，
			// 后果见本函数注释里「数字更新」那一段。
			return !aok
		default:
			as, bs := rawComponent(av, i), rawComponent(bv, i)
			if as != bs {
				return as < bs
			}
		}
	}
	// 数字分量完全相同。预发布后缀的优先级规则：1.2.0 > 1.2.0-rc1（正式版比预发布新）。
	// 注意不能用 `apre < bpre`：空串在任何串之前，那会把「正式版」判成比
	// 「预发布版」更旧，正好反了。
	if apre == bpre {
		return false
	}
	if apre == "" {
		return false // a 是正式版，b 才是预发布 => a 不比 b 旧
	}
	if bpre == "" {
		return true // a 是预发布，b 是正式版 => a 更旧
	}
	return apre < bpre
}

// splitVersion 去掉 v 前缀与预发布后缀，剩下按 '.' 拆好的分量。
//
// `+build`（构建元数据）与 `-pre`（预发布）分开处理：前者按 semver 规范
// **完全不影响**优先级，直接丢掉；后者保留在返回值里参与比较。
// 「1.2.0+openpocket」和「1.2.0」必须是同一个版本，而不是一个新版本。
func splitVersion(s string) ([]string, string) {
	s = strings.TrimSpace(s)
	if len(s) > 0 && (s[0] == 'v' || s[0] == 'V') {
		s = s[1:]
	}
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	pre := ""
	if i := strings.IndexByte(s, '-'); i >= 0 {
		pre, s = s[i+1:], s[:i]
	}
	if s == "" {
		return nil, pre
	}
	return strings.Split(s, "."), pre
}

func component(parts []string, i int) (int, bool) {
	if i >= len(parts) {
		return 0, true // 缺位当 0
	}
	n, err := strconv.Atoi(parts[i])
	if err != nil {
		return 0, false
	}
	return n, true
}

func rawComponent(parts []string, i int) string {
	if i >= len(parts) {
		return ""
	}
	return parts[i]
}
