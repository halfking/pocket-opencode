package email

// store_importance_constraint_test.go — emails.importance 的 DB 级 CHECK 兜底。
//
// 背景（2026-10-01）：§7af 发现 splitReminderCandidates 用 `case "high"` 精确
// 匹配，而上游 kxmemory 返回的是 "High"/"HIGH"/"高"。脏值既不触发提醒、也不
// 计入 unclassified——报告里 remindersSent=0 看起来一切正常。NormalizeImportance
// 修了写入侧，但那是**代码**约定：任何人绕过它直接写库、或老库里已有的历史
// 脏值，约束都拦不住。同文件的 email_accounts.auth_type 一直有 CHECK，emails
// 没有——这里补齐。
//
// 为什么这类护栏必须用真库测：CHECK 是否生效取决于**表上有没有这个约束**，
// 而约束是 migrate() 在建表时才落的。纯 Go 单测跑不到这一步，只能自己断言
// 字符串常量，那是自证欺人。所以这里真连 PostgreSQL（无 POCKET_TEST_POSTGRES_DSN
// 时 skip，与其它 email 集成测试一致）。
//
// 负控对照：'High'/'高'/'1'/'URGENT' 四个上游真实出现过的脏值必须全部被拒。
// 若把 migrate() 里的 CHECK 去掉（本文件的负控做法），这四条断言会从
// "被拒" 变成 "写入成功"，测试转红。

import (
	"context"
	"testing"
	"time"
)

// 写一行带指定 importance 的 email，返回是否成功。
func tryWriteImportance(t *testing.T, store *Store, accountID, id, importance string) error {
	t.Helper()
	return store.InsertEmail(context.Background(), Email{
		ID:          id,
		AccountID:   accountID,
		MessageID:   id + "@example.com",
		Subject:     "constraint probe " + importance,
		FromAddress: "sender@example.com",
		Date:        time.Now().Unix(),
		Importance:  importance,
	})
}

func TestEmailsImportanceCheckRejectsDirtyValues(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	seedAccount(t, store, "acct-imp", "owner", "ws-imp")

	// 上游 kxmemory 与规则引擎真实返回过的形态（§7af 列举），每一个都必须被
	// DB 挡住，而不是等到 splitReminderCandidates 静默漏掉。
	dirty := []string{"High", "HIGH", "高", "1", "URGENT", "Medium", "普通", "3"}
	for _, v := range dirty {
		t.Run("reject/"+v, func(t *testing.T) {
			if err := tryWriteImportance(t, store, "acct-imp", "mail-dirty-"+v, v); err == nil {
				t.Fatalf("importance=%q was accepted by the DB; the CHECK constraint is not doing its job", v)
			}
		})
	}
}

func TestEmailsImportanceCheckAcceptsNormalizedValues(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	seedAccount(t, store, "acct-imp", "owner", "ws-imp")

	// NormalizeImportance 的输出域。空串是合法值，语义「未分类」——
	// 把它当非法会逼着代码去写 NULL 或跳过写入，反而更糟。
	for _, v := range []string{"high", "medium", "low", ""} {
		t.Run("accept/"+v, func(t *testing.T) {
			if err := tryWriteImportance(t, store, "acct-imp", "mail-ok-"+v, v); err != nil {
				t.Fatalf("importance=%q must be accepted, got: %v", v, err)
			}
		})
	}
}

// 列必须保持可空：NULL 与空串一样表示「未分类」。若把 CHECK 写成
//
//	importance <> ''
//
// 或加 NOT NULL，「未分类」的表示方式就被掐死了一条。
func TestEmailsImportanceColumnStaysNullable(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	seedAccount(t, store, "acct-imp", "owner", "ws-imp")

	if err := tryWriteImportance(t, store, "acct-imp", "mail-null", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// 直接写 NULL，绕过 Go 侧结构体的零值语义
	if _, err := store.pool.Exec(context.Background(),
		`UPDATE emails SET importance = NULL WHERE id = $1`, "mail-null"); err != nil {
		t.Fatalf("NULL importance must remain writable (it means 'unclassified'), got: %v", err)
	}
}
