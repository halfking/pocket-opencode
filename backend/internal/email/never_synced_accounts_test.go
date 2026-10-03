package email

import (
	"strings"
	"testing"
)

// never_synced_accounts_test.go —— 「开了却从没同步成功过」的邮箱必须能被点名。
//
// ## 缺陷
//
// 调度器对每个到期账户每分钟跑一次 Sync，失败分支是
//
//	log.Printf("[email/scheduler] sync %s failed: %v", accountID, err)
//	return
//
// 一个**从上线起就一次都没连上过**的邮箱，只会在日志里每分钟刷一行，
// 然后被下一行刷走。没有计数器、没有报告字段、诊断页也不看这个。
// 于是界面上「5 个邮箱已配置、收信正常」与「其中 1 个从来没通过认证」
// 长得一模一样，需求 1 对那一个等于完全没实现。
//
// 真实库 2026-10-02 的实例：5 个 enabled 账户中 feikemanager1@163.com 的
// last_synced_uid=0、last_synced_at=0、邮件数 0；同库另外 4 个 UID 正常推进。
//
// 本文件钉住 NeverSyncedAccounts 的判定，并刻意覆盖最容易被写反的一条：
// 「同步过但很久没同步」是**正常**的，不该被当成故障。

// 真实库 2026-10-02 的形态：4 个正常 + 1 个从没过。
func TestNeverSyncedAccounts_PicksOnlyTheOneThatNeverSynced(t *testing.T) {
	accounts := []Account{
		{EmailAddress: "56551681@qq.com", Enabled: true, LastSyncedAt: 1758000000, LastSyncedUID: 10458},
		{EmailAddress: "feikemanager1@163.com", Enabled: true, LastSyncedAt: 0, LastSyncedUID: 0},
		{EmailAddress: "feikemanager@163.com", Enabled: true, LastSyncedAt: 1758000100, LastSyncedUID: 1669791329},
		{EmailAddress: "huangxutao@kxpms.cn", Enabled: true, LastSyncedAt: 1757990000, LastSyncedUID: 11},
		{EmailAddress: "kimmy.huang@163.com", Enabled: true, LastSyncedAt: 1758000200, LastSyncedUID: 1298896148},
	}
	got := NeverSyncedAccounts(accounts)
	if len(got) != 1 {
		t.Fatalf("选出 %d 个 %v，want 1（只有 feikemanager1@163.com 的 last_synced_at=0）",
			len(got), got)
	}
	if got[0] != "feikemanager1@163.com" {
		t.Fatalf("选错了账户: %v", got)
	}
}

// 「同步过但很久没同步」是**正常**状态（定时间隔本来就可能是几小时）。
//
// 这条最容易被写反：若把判据写成「last_synced_at 早于 N 分钟就算故障」，
// 正常账户会在每天的大部分时间里被误报，告警立刻失去意义——而一个
// 永远在响的告警等于没有告警。
func TestNeverSyncedAccounts_OldButSyncedIsNotAFault(t *testing.T) {
	// 1970 之外、但确实同步过：哪怕时间戳很旧，也不该被点名。
	old := Account{EmailAddress: "old@example.com", Enabled: true, LastSyncedAt: 1000, LastSyncedUID: 7}
	if got := NeverSyncedAccounts([]Account{old}); len(got) != 0 {
		t.Fatalf("同步过（哪怕很旧）却被当成从未同步: %v —— 这会让正常账户每天误报", got)
	}
}

// 已停用的账户不该被点名：用户主动关掉它就是预期行为，报成故障是噪音。
func TestNeverSyncedAccounts_DisabledIsNotAFault(t *testing.T) {
	off := Account{EmailAddress: "off@example.com", Enabled: false, LastSyncedAt: 0}
	if got := NeverSyncedAccounts([]Account{off}); len(got) != 0 {
		t.Fatalf("已停用账户被点名: %v —— 用户主动关掉它是预期行为", got)
	}
}

// 全部正常时不得返回任何东西，否则每分钟都会打一条无意义的告警。
func TestNeverSyncedAccounts_AllHealthyIsEmpty(t *testing.T) {
	accounts := []Account{
		{EmailAddress: "a@x.com", Enabled: true, LastSyncedAt: 1758000000},
		{EmailAddress: "b@x.com", Enabled: true, LastSyncedAt: 1758000001},
	}
	if got := NeverSyncedAccounts(accounts); len(got) != 0 {
		t.Fatalf("全部正常却返回 %v", got)
	}
}

// 接线层：告警文案必须自带排查入口。
//
// 只说「有 N 个邮箱没同步」而没说去哪儿看，运维的第一反应是挨个点开邮箱
// 试登录——而真正的失败原因（认证拒绝 / 服务器地址错 / 端口不通）只出现在
// 调度器日志里。判据的报错/告警信息要能直接把人带到正确的排查位置。
func TestNeverSyncedWarningTextPointsAtTheLog(t *testing.T) {
	if !strings.Contains(neverSyncedWarning(2, 5, "a@x.com, b@x.com"), logPrefix) {
		t.Error("告警文案没有指向调度器日志 —— 排查入口缺失，运维会去挨个试登录")
	}
	if !strings.Contains(neverSyncedWarning(2, 5, "a@x.com, b@x.com"), "a@x.com, b@x.com") {
		t.Error("告警文案没有列出具体是哪些账户")
	}
	if !strings.Contains(neverSyncedWarning(2, 5, "a@x.com, b@x.com"), "2/5") {
		t.Error("告警文案没有给出「几个之中有几个」——只有一个数字时无法判断严重程度")
	}
}
