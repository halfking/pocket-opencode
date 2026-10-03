package main

// bootstrap_first_user.go — 空 users 表时「要不要建首个管理员、用什么口令」的判定。
//
// 抽成纯函数是为了能测：这段逻辑原先内联在 main.go 里，用一个**注定失败**的
// 内置口令（"admin"，6 字符 < validatePassword 要求的 8 字符）去建号，于是
// 唯一能救活"users 表被清空"的路径永远失败，表就一直空着——而表空着在非
// DevAuth 部署下等于**谁都登不进去**。
//
// 现规则：口令只能由部署方通过 POCKET_AUTH_PASS 提供；没给就不建，并把
// 后果说清楚。DevAuth 打开时 dev 旁路不查 users 表，不受影响。

// bootstrapDecision 返回：是否创建、使用哪个用户名、使用哪个口令、以及一条
// 面向运维的说明（成功时为空串）。
func bootstrapDecision(devUser, devPass string) (create bool, user, pass, note string) {
	user = devUser
	if user == "" {
		user = "admin"
	}
	if devPass != "" {
		return true, user, devPass, ""
	}
	return false, user, "", "users table empty; refusing to auto-create " + user +
		" with a built-in default password. " +
		"Set POCKET_AUTH_PASS (>= 8 chars) to bootstrap the first admin, " +
		"or rely on POCKET_DEV_AUTH. " +
		"NOTE: without either, every login will fail with 401."
}
