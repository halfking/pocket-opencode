package email

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// diag_credential_health_test.go —— 判断**库里存的凭据本身是否完好**。
//
// 不碰真实邮箱、不发起任何 IMAP 连接，纯粹是本地解密检查。
//
// ## 为什么需要它
//
// 真实库 2026-10-02：5 个 enabled 账户中 feikemanager1@163.com 的
// last_synced_uid=0、last_synced_at=0、邮件数 0 —— 一次都没成功同步过。
// 「认证被拒」的原因至少分三类，必须先分开再排查，否则很容易一路查到
// 错的地方：
//
//	(a) 凭据在库里就坏了（解密失败 / 被截断 / 存的是明文而非密文）
//	(b) 凭据完好，但服务端拒绝（密码错、账号需激活、IP 未放行）
//	(c) 客户端侧（163 要求的 ID 头没发）
//
// (c) 基本可排除：同库另外两个 163 账户（feikemanager@163.com、
// kimmy.huang@163.com）都在正常同步，说明这条客户端路径已经处理过了。
//
// (a) 是**本地可判定**的，而 (b) 必须真实登录才知道。把这道便宜的检查
// 放在前面，能避免把「库里的密文坏了」误当成「服务端在拒绝」——后者会
// 让人反复换密码、反复查 IP 白名单，而真正的解法只是重新写一次凭据。
//
// ## 隐私
//
// **绝不打印明文**。只报「能否解密 / 明文长度 / 是否符合专用密码形态」。
// 长度与形态足以区分「存的是完整密码」和「存了一半 / 存成了别的字段」，
// 而不需要看到内容本身。
//
// ## 2026-10-02 实跑结论
//
//	56551681@qq.com        解密 ok  16 位  uid=10458
//	feikemanager1@163.com  解密 ok  16 位  uid=0       ← 凭据完好
//	feikemanager@163.com   解密 ok  16 位  uid=1669791329
//	huangxutao@kxpms.cn    解密 ok  16 位  uid=11
//	kimmy.huang@163.com    解密 ok  16 位  uid=1298896148
//
// 5 个全部解密成功且都是 16 位（163/QQ 专用密码形态），(a) 被排除。
// feikemanager1@163.com 的失败在 (b)：需要一次真实 IMAP 登录才能确定是
// 密码错、账号需激活还是 IP 未放行。

func TestDiagCredentialHealthRealDB(t *testing.T) {
	dsn := os.Getenv("POCKET_REAL_MAIL_DSN")
	if dsn == "" {
		t.Skip("POCKET_REAL_MAIL_DSN not set; 跳过真实凭据健康诊断")
	}
	schema := os.Getenv("POCKET_REAL_MAIL_SCHEMA")
	if schema == "" {
		schema = "opencode_pocket"
	}
	dataDir := os.Getenv("POCKET_REAL_DATA_DIR")
	if dataDir == "" {
		t.Skip("POCKET_REAL_DATA_DIR not set; 跳过（需要 email_master.key 才能解密）")
	}

	key, err := EnsureMasterKey(os.Getenv("POCKET_EMAIL_MASTER_KEY"), dataDir)
	if err != nil {
		t.Fatalf("取主密钥失败: %v", err)
	}
	cr, err := NewCrypto(key)
	if err != nil {
		t.Fatalf("构造 Crypto 失败: %v", err)
	}

	ctx := context.Background()
	cfg, perr := pgxpool.ParseConfig(dsn)
	if perr != nil {
		t.Fatalf("parse dsn: %v", perr)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	// 2026-10-02 补：当场验证 search_path 确实落在目标 schema 上。
	//
	// 覆盖式设置（RuntimeParams）只有单一来源，不像 DSN 拼接那样有
	// 「pgx 取第一个同名参数」的歧义；但「以为钉住了」正是本仓库
	// search_path 缺陷家族的特征（见 diag_merge_exec_test.go 与
	// reminder_notified_diag_test.go 的注释），所以读回来确认一次。
	//
	// 代价是每次诊断多一个 round trip；收益是打错库时**立刻**报出
	// schema 名，而不是扫到空集后输出误导性结论。
	var resolvedSchema string
	if err := pool.QueryRow(ctx, `SELECT current_schema()`).Scan(&resolvedSchema); err != nil {
		t.Fatalf("verify search_path: %v", err)
	}
	if resolvedSchema != schema {
		t.Fatalf("search_path 未生效：期望 %q，连接实际落在 %q。**拒绝继续**"+
			"——本诊断的全部价值在于「和实现看到同一批数据」，"+
			"打到别的库会输出误导性结论。", schema, resolvedSchema)
	}
	t.Logf("search_path verified: current_schema() = %q", resolvedSchema)
	rows, qerr := pool.Query(ctx, `
		SELECT email_address, COALESCE(credential_encrypted,''), COALESCE(auth_type,''),
		       COALESCE(last_synced_uid,0), COALESCE(last_synced_at,0)
		FROM email_accounts ORDER BY email_address`)
	if qerr != nil {
		t.Fatalf("query: %v", qerr)
	}
	defer rows.Close()

	bad, total := 0, 0
	for rows.Next() {
		total++
		var addr, enc, auth string
		var uid, lastAt int64
		if serr := rows.Scan(&addr, &enc, &auth, &uid, &lastAt); serr != nil {
			t.Fatalf("scan: %v", serr)
		}
		status, note := zzCredStatus(cr, enc, uid, lastAt)
		if status != "ok" {
			bad++
		}
		t.Logf("%-24s %-6s auth=%-8s uid=%-11d %s", addr, status, auth, uid, note)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	// 不断言必须为 0：真实环境里凭据坏了是需要人来处理的，测试不该替人
	// 决定「坏了就算失败」。这里只保证——它一定会被看见。
	t.Logf("凭据健康度汇总：%d 个账户，其中 %d 个有问题（详见逐行输出）", total, bad)
}

// zzCredStatus 只回「能否解密」与**不含明文**的说明。
func zzCredStatus(cr *Crypto, enc string, uid, lastAt int64) (string, string) {
	if strings.TrimSpace(enc) == "" {
		return "空", "⚠ 凭据为空 —— 从未配置"
	}
	plain, err := cr.DecryptString(enc)
	if err != nil {
		return "FAIL", "⚠ 解密失败（主密钥不匹配或密文损坏）: " + err.Error()
	}
	if plain == "" {
		return "空", "⚠ 解密后为空串"
	}
	shape := "非常规"
	switch len(plain) {
	case 16:
		shape = "16 位（符合 163/QQ 专用密码形态）"
	case 8:
		shape = "8 位"
	}
	note := "明文长度 " + strconv.Itoa(len(plain)) + "，" + shape
	if uid == 0 && lastAt == 0 {
		note += "；⚠ 但从未同步成功过 → 凭据完好，问题在服务端侧（密码错/需激活/IP 放行）"
	}
	return "ok", note
}

func zzTrunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

var _ = zzTrunc

