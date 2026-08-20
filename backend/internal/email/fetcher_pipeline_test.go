package email

// fetcher_pipeline_test.go — end-to-end coverage for the IMAP fetch pipeline:
//
//	IMAP server → Fetcher.Sync → rules evaluation → InsertEmail → scoped reads
//
// This path had no test at all, which is how two defects survived: InsertEmail's
// column/placeholder mismatch (every insert failed) and GetAccountByID not
// selecting workspace_id (every email landed in workspace 'default').
//
// The IMAP side runs in-process via go-imap's imapmemserver over TLS with a
// self-signed cert, so no network egress and no real credentials are involved.
// Needs Postgres; see store_workspace_test.go for the DSN convention.

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

// selfSignedTLS returns a TLS config pair (server, client) trusting a
// throwaway cert for 127.0.0.1.
func selfSignedTLS(t *testing.T) (*tls.Config, *tls.Config) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:         true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	serverCfg := &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}},
	}
	return serverCfg, &tls.Config{RootCAs: pool, ServerName: "127.0.0.1"}
}

// testIMAP holds a running in-memory IMAP server.
type testIMAP struct {
	host string
	port int
	user *imapmemserver.User
}

// startIMAPServer boots an imapmemserver over TLS on a random loopback port and
// returns it along with a dial func wired to trust its cert.
func startIMAPServer(t *testing.T, username, password string) (*testIMAP, func(string, *imapclient.Options) (*imapclient.Client, error)) {
	t.Helper()
	serverTLS, clientTLS := selfSignedTLS(t)

	memServer := imapmemserver.New()
	user := imapmemserver.NewUser(username, password)
	if err := user.Create("INBOX", nil); err != nil {
		t.Fatalf("create INBOX: %v", err)
	}
	memServer.AddUser(user)

	srv := imapserver.New(&imapserver.Options{
		NewSession: func(_ *imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return memServer.NewSession(), nil, nil
		},
		TLSConfig:    serverTLS,
		InsecureAuth: false,
	})

	ln, err := tls.Listen("tcp", "127.0.0.1:0", serverTLS)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		_ = srv.Close()
		_ = ln.Close()
	})

	addr := ln.Addr().(*net.TCPAddr)
	dial := func(a string, opts *imapclient.Options) (*imapclient.Client, error) {
		if opts == nil {
			opts = &imapclient.Options{}
		}
		opts.TLSConfig = clientTLS
		return imapclient.DialTLS(a, opts)
	}
	return &testIMAP{host: addr.IP.String(), port: addr.Port, user: user}, dial
}

// literalReader adapts a byte slice to imap.LiteralReader (io.Reader + Size).
type literalReader struct {
	*strings.Reader
	size int64
}

func (l literalReader) Size() int64 { return l.size }

func newLiteral(s string) literalReader {
	return literalReader{Reader: strings.NewReader(s), size: int64(len(s))}
}

// appendMessage puts a raw RFC 5322 message into the user's INBOX.
func (ti *testIMAP) appendMessage(t *testing.T, from, subject, body string, when time.Time) {
	t.Helper()
	raw := fmt.Sprintf("From: %s\r\n"+
		"To: recipient@example.com\r\n"+
		"Subject: %s\r\n"+
		"Message-ID: <%s@example.com>\r\n"+
		"Date: %s\r\n"+
		"Content-Type: text/plain; charset=utf-8\r\n"+
		"\r\n%s\r\n",
		from, subject, strings.ReplaceAll(subject, " ", "-"), when.Format(time.RFC1123Z), body)

	if _, err := ti.user.Append("INBOX", newLiteral(raw), &imap.AppendOptions{Time: when}); err != nil {
		t.Fatalf("append %q: %v", subject, err)
	}
}

// newPipelineFetcher wires a Fetcher to the throwaway store + IMAP server and
// seeds a matching account row. Returns the account id.
func newPipelineFetcher(t *testing.T, store *Store, ti *testIMAP,
	dial func(string, *imapclient.Options) (*imapclient.Client, error),
	userID, workspaceID, password, rulesJSON string,
) (*Fetcher, string) {
	t.Helper()
	crypto, err := NewCrypto([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatalf("crypto: %v", err)
	}
	enc, err := crypto.EncryptString(password)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	acctID := "acct-pipeline-" + workspaceID
	acc := &Account{
		ID:              acctID,
		UserID:          userID,
		WorkspaceID:     workspaceID,
		DisplayName:     "pipeline",
		EmailAddress:    "recipient@example.com",
		IMAPHost:        ti.host,
		IMAPPort:        ti.port,
		AuthType:        "password",
		SyncIntervalMin: 15,
		Rules:           rulesJSON,
		Enabled:         true,
		CreatedAt:       time.Now().Unix(),
	}
	if err := store.InsertAccount(context.Background(), acc, enc); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	return &Fetcher{store: store, crypto: crypto, dialTLS: dial}, acctID
}

// The whole happy path: two messages on the server land in the DB and come back
// through the scoped read used by GET /api/emails.
func TestSyncPersistsFetchedEmails(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const password = "app-specific-pw"
	ti, dial := startIMAPServer(t, "recipient@example.com", password)

	now := time.Now().UTC().Truncate(time.Second)
	ti.appendMessage(t, "boss@corp.example", "Quarterly review", "Please send the numbers.", now.Add(-2*time.Hour))
	ti.appendMessage(t, "news@shop.example", "50% off everything", "Sale ends tonight.", now.Add(-time.Hour))

	fetcher, acctID := newPipelineFetcher(t, store, ti, dial, "user-1", "ws-a", password, "")

	saved, err := fetcher.Sync(ctx, acctID)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if saved != 2 {
		t.Fatalf("want 2 emails saved, got %d", saved)
	}

	list, err := store.ListEmailsScoped(ctx, ListFilter{}, "user-1", "ws-a")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2 emails readable, got %d", len(list))
	}

	bySubject := map[string]Email{}
	for _, e := range list {
		bySubject[e.Subject] = e
	}
	got, ok := bySubject["Quarterly review"]
	if !ok {
		t.Fatalf("missing expected subject; got %v", bySubject)
	}
	if got.FromAddress != "boss@corp.example" {
		t.Errorf("from address = %q", got.FromAddress)
	}
	if !strings.Contains(got.Snippet, "send the numbers") {
		t.Errorf("snippet missing body text: %q", got.Snippet)
	}

	// Sync state must advance so the next run doesn't refetch.
	status, err := store.GetSyncStatusScoped(ctx, "user-1", "ws-a")
	if err != nil {
		t.Fatalf("sync status: %v", err)
	}
	if len(status) != 1 {
		t.Fatalf("want 1 account status, got %d", len(status))
	}
	if status[0].LastSyncedUID == 0 {
		t.Error("last_synced_uid did not advance")
	}
	// PendingCount used to be computed with e.workspace_id=a.workspace_id,
	// which returned 0 for any workspace other than 'default'.
	if status[0].PendingCount != 2 {
		t.Errorf("pending (unread) count = %d, want 2", status[0].PendingCount)
	}

	// Re-syncing must not duplicate: UID search starts past last_synced_uid and
	// InsertEmail carries ON CONFLICT DO NOTHING.
	again, err := fetcher.Sync(ctx, acctID)
	if err != nil {
		t.Fatalf("resync: %v", err)
	}
	if again != 0 {
		t.Errorf("resync saved %d, want 0", again)
	}
	list, err = store.ListEmailsScoped(ctx, ListFilter{}, "user-1", "ws-a")
	if err != nil {
		t.Fatalf("list after resync: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("resync duplicated rows: %d", len(list))
	}
}

// Fetched mail must be stamped with the account's workspace, not 'default'.
// GetAccountByID never selected workspace_id, so Account.WorkspaceID was always
// "" and InsertEmail's defaultWorkspace() fallback wrote 'default' for everyone.
func TestSyncStampsAccountWorkspace(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const password = "pw"
	ti, dial := startIMAPServer(t, "recipient@example.com", password)
	ti.appendMessage(t, "someone@example.com", "Scoped mail", "body", time.Now().UTC())

	fetcher, acctID := newPipelineFetcher(t, store, ti, dial, "user-1", "ws-non-default", password, "")
	if _, err := fetcher.Sync(ctx, acctID); err != nil {
		t.Fatalf("sync: %v", err)
	}

	var ws string
	if err := store.pool.QueryRow(ctx,
		`SELECT workspace_id FROM emails WHERE account_id = $1`, acctID).Scan(&ws); err != nil {
		t.Fatalf("read workspace_id: %v", err)
	}
	if ws != "ws-non-default" {
		t.Fatalf("emails.workspace_id = %q, want %q", ws, "ws-non-default")
	}

	// And the email must not be visible from a different workspace.
	other, err := store.ListEmailsScoped(ctx, ListFilter{}, "user-1", "ws-other")
	if err != nil {
		t.Fatalf("list other workspace: %v", err)
	}
	if len(other) != 0 {
		t.Fatalf("email leaked into another workspace: %d rows", len(other))
	}
}

// Account rules must be applied during fetch, before persistence.
func TestSyncAppliesAccountRules(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const password = "pw"
	ti, dial := startIMAPServer(t, "recipient@example.com", password)
	ti.appendMessage(t, "boss@corp.example", "Urgent: sign off", "Need this today.", time.Now().UTC())
	ti.appendMessage(t, "random@other.example", "Just saying hi", "No action needed.", time.Now().UTC())

	// Shape per rules.ParseRules: {"rules":[{type,pattern,actions}]}.
	rulesJSON := `{"rules":[{
		"type": "sender-whitelist",
		"pattern": "boss@corp.example",
		"actions": [{"name": "mark-important"}, {"name": "label-category", "category": "work"}]
	}]}`

	fetcher, acctID := newPipelineFetcher(t, store, ti, dial, "user-1", "ws-a", password, rulesJSON)
	if _, err := fetcher.Sync(ctx, acctID); err != nil {
		t.Fatalf("sync: %v", err)
	}

	list, err := store.ListEmailsScoped(ctx, ListFilter{}, "user-1", "ws-a")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var matched, unmatched *Email
	for i := range list {
		switch list[i].FromAddress {
		case "boss@corp.example":
			matched = &list[i]
		case "random@other.example":
			unmatched = &list[i]
		}
	}
	if matched == nil || unmatched == nil {
		t.Fatalf("expected both emails, got %d", len(list))
	}
	if matched.Importance != "high" {
		t.Errorf("rule did not set importance: %q", matched.Importance)
	}
	if matched.Category != "work" {
		t.Errorf("rule did not set category: %q", matched.Category)
	}
	if unmatched.Importance == "high" {
		t.Error("rule applied to a non-matching email")
	}

	// The category filter on the scoped read must see the persisted value.
	work, err := store.ListEmailsScoped(ctx, ListFilter{Category: "work"}, "user-1", "ws-a")
	if err != nil {
		t.Fatalf("filtered list: %v", err)
	}
	if len(work) != 1 {
		t.Fatalf("category filter returned %d rows, want 1", len(work))
	}
}

// The migration must realign emails.workspace_id with the owning account for
// rows written before GetAccountByID selected workspace_id (all of which landed
// on 'default'). migrate() is idempotent, so re-running it performs the repair.
func TestMigrationBackfillsEmailWorkspace(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedAccount(t, store, "acct-legacy", "user-1", "ws-a")

	// Simulate a legacy row: correct account, but workspace_id stuck on 'default'.
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO emails (id, account_id, workspace_id, from_address, subject, snippet, date,
		                    is_read, is_starred, has_attachments, created_at)
		VALUES ('em-legacy', 'acct-legacy', 'default', 'a@example.com', 's', 'body', $1,
		        FALSE, FALSE, FALSE, $1)
	`, time.Now().Unix()); err != nil {
		t.Fatalf("seed legacy email: %v", err)
	}

	if err := store.migrate(); err != nil {
		t.Fatalf("re-run migrate: %v", err)
	}

	var ws string
	if err := store.pool.QueryRow(ctx,
		`SELECT workspace_id FROM emails WHERE id = 'em-legacy'`).Scan(&ws); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if ws != "ws-a" {
		t.Fatalf("backfill did not repair row: workspace_id = %q, want %q", ws, "ws-a")
	}

	// Idempotent: a second run changes nothing.
	if err := store.migrate(); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if err := store.pool.QueryRow(ctx,
		`SELECT workspace_id FROM emails WHERE id = 'em-legacy'`).Scan(&ws); err != nil {
		t.Fatalf("read back after second run: %v", err)
	}
	if ws != "ws-a" {
		t.Fatalf("second run drifted: %q", ws)
	}
}

// The daily-summary source query must see freshly fetched mail, scoped per
// workspace. This is the input to Scheduler.runDailySummary.
func TestFetchedEmailsReachDailySummaryQuery(t *testing.T) {
	store, cleanup := newWorkspaceTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const password = "pw"
	ti, dial := startIMAPServer(t, "recipient@example.com", password)

	// Anchor to midday UTC so the ±24h day window can't straddle a boundary.
	day := time.Now().UTC().Truncate(24 * time.Hour).Add(12 * time.Hour)
	ti.appendMessage(t, "a@example.com", "Today one", "body one", day)
	ti.appendMessage(t, "b@example.com", "Today two", "body two", day.Add(time.Minute))

	fetcher, acctID := newPipelineFetcher(t, store, ti, dial, "user-1", "ws-a", password, "")
	if _, err := fetcher.Sync(ctx, acctID); err != nil {
		t.Fatalf("sync: %v", err)
	}

	dateStr := day.Format("2006-01-02")
	got, err := store.ListEmailsByDayScoped(ctx, "user-1", "ws-a", dateStr, 0)
	if err != nil {
		t.Fatalf("list by day: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 emails for %s, got %d", dateStr, len(got))
	}

	// A different workspace must produce an empty summary input.
	other, err := store.ListEmailsByDayScoped(ctx, "user-1", "ws-b", dateStr, 0)
	if err != nil {
		t.Fatalf("list by day other ws: %v", err)
	}
	if len(other) != 0 {
		t.Fatalf("summary input leaked across workspace: %d rows", len(other))
	}
}
