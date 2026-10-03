package server

// server_email_notify_adapter_test.go — 重要邮件提醒的最后一环：
// notifycenterEmailNotifier 把 email.Email 翻成 notifycenter.Event 并派发。
//
// 这一层此前零覆盖（grep NotifyImportantEmail 在 server 包测试里无命中）。
// 它是「对其它重要邮件进行提醒」这条需求里**唯一**决定「提醒发给了谁」的
// 地方，而它的两种失效都是无声的：
//
//  1. 收件人解析失败被吞掉（`if err == nil` 才取 userID）→ Event.UserID 为空
//     → notifycenter 的 WebsocketSender.Send 退化成**工作区全体广播**
//     （它自己的注释写明「无 user_id 退化为全局广播」）→ 别人邮箱里的重要
//     邮件推给同 workspace 所有在线用户；
//  2. 同一次吞错还让 Dispatch 成功返回 → 流水线的 notifyImportant 把这封
//     记进 notified_at（「已提醒」）→ 提醒**永久**丢失，且报告上一切正常。
//
// 上一轮我判断这层「跨包做不了假 store、必须改 notifycenter 的导出口子才能测」
// 是错的：notifycenter.New(pool)、NewService(store, sender) 与 Sender 接口
// 都是导出的，用真 store 跑在临时 schema 上即可，不需要任何设计改动。
import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/email"
	"github.com/halfking/pocket-opencode/backend/internal/notifycenter"
	"github.com/jackc/pgx/v5/pgxpool"
)

type notifySendCall struct {
	ch    notifycenter.Channel
	n     *notifycenter.Notification
	token string
}

// recordingNotifySender 实现 notifycenter.Sender（导出接口），记录每次投递。
type recordingNotifySender struct {
	mu    sync.Mutex
	calls []notifySendCall
}

func (s *recordingNotifySender) Send(_ context.Context, ch notifycenter.Channel, n *notifycenter.Notification, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, notifySendCall{ch: ch, n: n, token: token})
	return nil
}

func (s *recordingNotifySender) snapshot() []notifySendCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]notifySendCall(nil), s.calls...)
}

func (s *recordingNotifySender) onChannel(ch notifycenter.Channel) *notifycenter.Notification {
	for _, c := range s.snapshot() {
		if c.ch == ch {
			return c.n
		}
	}
	return nil
}

// newNotifyAdapterFixture 起一个临时 schema，同时装好 email.Store 与
// notifycenter.Store —— 两者共用一个 pool，各自跑自己的幂等迁移。
func newNotifyAdapterFixture(t *testing.T) (*notifycenterEmailNotifier, *recordingNotifySender, *pgxpool.Pool, func()) {
	t.Helper()
	dsn := os.Getenv("POCKET_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POCKET_TEST_POSTGRES_DSN not set; skipping email notifycenter adapter integration test")
	}
	ctx := context.Background()
	rootPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		rootPool.Close()
		t.Fatalf("rand: %v", err)
	}
	schema := "email_notify_test_" + hex.EncodeToString(suffix)
	if _, err := rootPool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		rootPool.Close()
		t.Fatalf("create schema: %v", err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("scoped pool: %v", err)
	}
	cleanup := func() {
		pool.Close()
		_, _ = rootPool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		rootPool.Close()
	}

	emailStore, err := email.NewStore(pool)
	if err != nil {
		cleanup()
		t.Fatalf("email.NewStore: %v", err)
	}
	ncStore, err := notifycenter.New(pool)
	if err != nil {
		cleanup()
		t.Fatalf("notifycenter.New: %v", err)
	}
	sender := &recordingNotifySender{}
	svc := notifycenter.NewService(ncStore, sender)
	return &notifycenterEmailNotifier{svc: svc, store: emailStore}, sender, pool, cleanup
}

func seedNotifyAccount(t *testing.T, store *email.Store, id, userID, workspaceID string) {
	t.Helper()
	acc := &email.Account{
		ID: id, UserID: userID, WorkspaceID: workspaceID,
		DisplayName: "notify " + id, EmailAddress: id + "@example.com",
		IMAPHost: "imap.example.com", IMAPPort: 993, AuthType: "password",
		SyncIntervalMin: 15, Enabled: true, CreatedAt: time.Now().Unix(),
	}
	if err := store.InsertAccount(context.Background(), acc, "enc-cred"); err != nil {
		t.Fatalf("insert account %s: %v", id, err)
	}
}

// 正常路径：收件人必须解析成账户的归属用户，并且**落进 notifications 表**。
// 只断言 sender 收到是不够的 —— 真正决定用户能不能在通知中心看到这封提醒的
// 是那一行的 user_id。
func TestNotifyImportantEmailRoutesToAccountOwner(t *testing.T) {
	notifier, sender, pool, cleanup := newNotifyAdapterFixture(t)
	defer cleanup()
	ctx := context.Background()

	seedNotifyAccount(t, notifier.store, "acct-owner", "user-owner", "ws-a")

	e := email.Email{
		ID: "em-1", AccountID: "acct-owner", WorkspaceID: "ws-a",
		Subject: "合同到期提醒", Snippet: "请尽快处理",
	}
	if err := notifier.NotifyImportantEmail(ctx, e); err != nil {
		t.Fatalf("NotifyImportantEmail: %v", err)
	}

	ws := sender.onChannel(notifycenter.ChannelWebsocket)
	if ws == nil {
		t.Fatalf("websocket 通道没有收到投递，实际通道: %v", sender.snapshot())
	}
	if ws.UserID != "user-owner" {
		t.Fatalf("通知归属 user_id=%q，want user-owner（空值会退化成工作区全体广播）", ws.UserID)
	}
	if ws.WorkspaceID != "ws-a" {
		t.Fatalf("workspace_id=%q，want ws-a", ws.WorkspaceID)
	}
	if ws.Source != "email" || ws.Kind != "email.important" {
		t.Fatalf("source/kind=%q/%q，want email/email.important", ws.Source, ws.Kind)
	}
	if ws.Title != "重要邮件：合同到期提醒" {
		t.Fatalf("title=%q", ws.Title)
	}
	if ws.Body != "请尽快处理" {
		t.Fatalf("body=%q，want 邮件摘要", ws.Body)
	}
	if ws.Priority != "high" {
		t.Fatalf("priority=%q，want high", ws.Priority)
	}

	var userID, title, kind, priority string
	err := pool.QueryRow(ctx,
		`SELECT user_id, title, kind, priority FROM notifications WHERE id=$1`, ws.ID).
		Scan(&userID, &title, &kind, &priority)
	if err != nil {
		t.Fatalf("通知中心里没有这行（用户将看不到提醒）: %v", err)
	}
	if userID != "user-owner" || title != "重要邮件：合同到期提醒" || kind != "email.important" || priority != "high" {
		t.Fatalf("notifications 行 = user_id=%q title=%q kind=%q priority=%q", userID, title, kind, priority)
	}
}

// 主题为空时标题不能变成「重要邮件：重要邮件」。
func TestNotifyImportantEmailWithoutSubjectHasCleanTitle(t *testing.T) {
	notifier, sender, _, cleanup := newNotifyAdapterFixture(t)
	defer cleanup()

	seedNotifyAccount(t, notifier.store, "acct-nosub", "user-owner", "ws-a")

	if err := notifier.NotifyImportantEmail(context.Background(), email.Email{
		ID: "em-nosub", AccountID: "acct-nosub", WorkspaceID: "ws-a", Subject: "   ",
	}); err != nil {
		t.Fatalf("NotifyImportantEmail: %v", err)
	}
	ws := sender.onChannel(notifycenter.ChannelWebsocket)
	if ws == nil {
		t.Fatal("websocket 通道没有收到投递")
	}
	if ws.Title != "重要邮件" {
		t.Fatalf("title=%q，want 重要邮件（不得重复前缀）", ws.Title)
	}
}

// 解析不出归属账户时必须返回错误，且**不得**发出任何通知。
// 静默派发等于两种坏结局叠加：工作区全体广播 + 被记成「已提醒」。
func TestNotifyImportantEmailUnknownAccountFailsWithoutSending(t *testing.T) {
	notifier, sender, pool, cleanup := newNotifyAdapterFixture(t)
	defer cleanup()
	ctx := context.Background()

	// 库里**没有**这个账户（模拟邮件行还在、账户已被删除）。
	err := notifier.NotifyImportantEmail(ctx, email.Email{
		ID: "em-orphan", AccountID: "acct-deleted", WorkspaceID: "ws-a", Subject: "孤儿邮件",
	})
	if err == nil {
		t.Fatal("归属账户不存在时必须返回错误，否则这封会被记成已提醒而永久丢失")
	}
	if !strings.Contains(err.Error(), "acct-deleted") {
		t.Fatalf("错误信息应带上账户 id: %v", err)
	}
	if calls := sender.snapshot(); len(calls) != 0 {
		t.Fatalf("失败了却仍派发了 %d 次: %v", len(calls), calls)
	}
	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM notifications`).Scan(&rows); err != nil {
		t.Fatalf("count notifications: %v", err)
	}
	if rows != 0 {
		t.Fatalf("notifications 表多了 %d 行", rows)
	}
}

// 投递失败必须上抛：流水线的契约是「err → 不写 notified_at → 下轮重试」，
// 这里吞掉就等于告诉流水线「已提醒」。
func TestNotifyImportantEmailPropagatesDispatchFailure(t *testing.T) {
	notifier, sender, pool, cleanup := newNotifyAdapterFixture(t)
	defer cleanup()
	ctx := context.Background()

	seedNotifyAccount(t, notifier.store, "acct-broken", "user-owner", "ws-a")
	// 拆掉通知中心的落库表，让 Dispatch 的 InsertNotification 失败。
	if _, err := pool.Exec(ctx, `DROP TABLE notifications`); err != nil {
		t.Fatalf("drop notifications: %v", err)
	}

	err := notifier.NotifyImportantEmail(ctx, email.Email{
		ID: "em-broken", AccountID: "acct-broken", WorkspaceID: "ws-a", Subject: "会失败的一封",
	})
	if err == nil {
		t.Fatal("Dispatch 失败必须上抛，否则流水线会把它记成已提醒")
	}
	if calls := sender.snapshot(); len(calls) != 0 {
		t.Fatalf("落库失败却仍推了 %d 次", len(calls))
	}
}

// 没接通知中心时返回明确错误（而不是 panic 或静默成功）。
func TestNotifyImportantEmailWithoutServiceFails(t *testing.T) {
	n := &notifycenterEmailNotifier{}
	if err := n.NotifyImportantEmail(context.Background(), email.Email{ID: "em-1"}); err == nil {
		t.Fatal("svc 为 nil 时必须返回错误")
	}
	var nilNotifier *notifycenterEmailNotifier
	if err := nilNotifier.NotifyImportantEmail(context.Background(), email.Email{ID: "em-1"}); err == nil {
		t.Fatal("notifier 本身为 nil 时必须返回错误而不是 panic")
	}
}
