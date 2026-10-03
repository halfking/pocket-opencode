package email

import (
	"context"
	"fmt"
	"strings"
)

// credential_check.go — 启动自检：这把 master key 到底解不解得开库里的凭据。
//
// ## 为什么需要它
//
// `config.Validate()` 只检查 `POCKET_EMAIL_MASTER_KEY` **非空**
// （config.go:441）。「非空」和「对」是两件事：填了一把**错**的 key，进程
// 会照常启动、界面照常打开、调度器照常打印 `Email scheduler started`，然后
// 每一个账户的每一次同步都在 `fetcher.go:338` 撞 `decrypt credential: …`。
// 从外面看「服务在跑」，实际一封新邮件都收不到。
//
// 这不是假想。2026-10-01 在本机实测（§7bf）：磁盘上有 4 处
// `email_master.key`，其中只有 1 把能解开真实库里 5 个账户的凭据，另外 3 把
// （包括当前 `pocketd.exe` 所在目录那把）一把都解不开 —— 而用错的那把启动
// 时**没有任何报错**。
//
// ## 2026-10-02 定案：是哪一把
//
// 逐个 data dir 跑 `diag_credential_health_test.go`（只 SELECT、本地解密、
// 不碰 IMAP、不打印明文）实测 4 把：
//
//	C:\workspace\openpocket\data\email_master.key            5/5 解开 ← 权威
//	C:\workspace\openpocket\backend\data\email_master.key    0/5
//	C:\workspace\openpocket\.wt-e2e\backend\data\…           0/5
//	C:\workspace\openpocket\.scratch-sttdev\data\…            0/5
//
// 4 把的 SHA-256 互不相同，确认是 4 把不同的 key 而非同一把的副本。
// 权威那把 = `openpocket\data` 那把，与正文缓存 `data/email-bodies` 同目录，
// 两个独立信号一致。
//
// **同一把 key 还封着 LLM 网关的 API key**（llm_gateway_store.go:198 用的是
// 同一个 cipher），而这层在自检里原本没被点名：key 拿错时日志表现为「邮件
// 解不开」，而网关那边只是安静退回 env、env 未设就是没有 key，于是
// 归类/总结/发票提取/语音转写全都不工作会被当成另一件事另开一轮排查。
// 18100 实例（data dir 指到 .wt-e2e）实测同时命中两边，见 main.go 那条
// ERROR 的补充说明。
//
// ## 边界（别过度承诺）
//
// - 它只验「能不能解密」，**不验**密码是否仍有效、IMAP 是否可达。凭据正确但
//   服务端改了密码，同样过不了这一关。
// - 它**不修**任何东西，只把一件原本静默的事变成启动日志里的一行 ERROR。
//
// ## 三个计数，不是一个
//
// 账户分三类，混在一起算就会出现两种误判：
//
//   1. 能解开        —— 成功。
//   2. 空凭据 / oauth-pending —— **不是** key 错了，是这个账户还没配完。
//      全新部署的库就是这样。若把它算成失败，「一把都解不开」会被误报。
//   3. 解不开        —— key 错了，或密文损坏。
//
// 所以 Decryptable / Skipped / Failed 必须分开，且判「全失败」时必须把
// Skipped 排除掉。

// CredentialCheck 是一次启动自检的结果。
type CredentialCheck struct {
	// Accounts 是启用的账户总数。
	Accounts int
	// Decryptable 是凭据能被当前 key 解开、且确有可用凭据的账户数。
	Decryptable int
	// Skipped 是凭据为空或 oauth-pending 的账户数（还没配完，不算失败）。
	Skipped int
	// Failed 是有凭据却解不开的账户数 —— 这才是「key 拿错了」的证据。
	Failed int
	// FirstAccountID / FirstEmailAddress / FirstError 是第一个解不开的账户。
	FirstAccountID    string
	FirstEmailAddress string
	FirstError        error
}

// AllDecryptable 报告是否没有任何账户解密失败。
func (c CredentialCheck) AllDecryptable() bool {
	return c.Failed == 0
}

// AllFailed 是最危险的那一格：**有实际凭据，且一把都解不开**。这几乎必然
// 意味着 master key 拿错了（而不是「所有密码都错了」），所以调用方应该按这
// 个级别报警。注意它必须排除 Skipped，否则全新部署会被误报。
func (c CredentialCheck) AllFailed() bool {
	return c.Failed > 0 && c.Decryptable == 0
}

// Summary 给出可直接进日志的一行人类可读描述。
func (c CredentialCheck) Summary() string {
	switch {
	case c.Accounts == 0:
		return "no enabled email accounts; nothing to verify"
	case c.Skipped == c.Accounts:
		return fmt.Sprintf("all %d enabled email account(s) have no credential yet; nothing to verify",
			c.Accounts)
	case c.AllDecryptable():
		return fmt.Sprintf("all %d enabled email account(s) decrypt with the current master key (%d still unconfigured)",
			c.Accounts, c.Skipped)
	case c.AllFailed():
		return fmt.Sprintf(
			"MASTER KEY LOOKS WRONG: none of the %d configured email account(s) decrypt "+
				"(first: account=%s %s: %v)",
			c.Accounts-c.Skipped, c.FirstAccountID, c.FirstEmailAddress, c.FirstError)
	default:
		return fmt.Sprintf(
			"partial: %d/%d configured email account(s) decrypt with the current master key "+
				"(first failure: account=%s %s: %v)",
			c.Decryptable, c.Accounts-c.Skipped, c.FirstAccountID, c.FirstEmailAddress, c.FirstError)
	}
}

// CheckCredentials 用当前 key 逐个试解启用账户的凭据。
//
// 它**不会**把明文写回任何地方，只在内存里过一遍。查库失败时返回 error；
// 查库成功但有账户解不开时，**不**返回 error —— 那不是调用失败，而是「key
// 是错的」这个需要被报告的**事实**，由 AllFailed()/Summary() 表达。这样调用
// 方不会把一次成功的自检误当成启动失败而退出。
func CheckCredentials(ctx context.Context, store *Store, c *Crypto) (CredentialCheck, error) {
	var res CredentialCheck
	if store == nil || c == nil {
		return res, nil
	}
	rows, err := store.ListEnabledAccountCredentials(ctx)
	if err != nil {
		return res, fmt.Errorf("list account credentials: %w", err)
	}
	res.Accounts = len(rows)
	for _, r := range rows {
		// 空密文要**在解密之前**判掉。credential_encrypted 是 TEXT NOT NULL，
		// 但值可以是 ''（新建账户尚未配置凭据），而 DecryptString("") 直接返回
		// `ciphertext too short` —— 那样它会落进 Failed，全新部署被误报成
		// 「master key 拿错了」。这是写测试时真抓出来的：判「解出来是不是空」
		// 放在解密之后是不够的。
		if strings.TrimSpace(r.CredentialCipher) == "" {
			res.Skipped++
			continue
		}
		plain, derr := c.DecryptString(r.CredentialCipher)
		if derr == nil {
			if plain == "" || plain == "oauth-pending-no-credential" {
				res.Skipped++
			} else {
				res.Decryptable++
			}
			continue
		}
		res.Failed++
		if res.FirstError == nil {
			res.FirstError = derr
			res.FirstAccountID = r.ID
			res.FirstEmailAddress = r.EmailAddress
		}
	}
	return res, nil
}
