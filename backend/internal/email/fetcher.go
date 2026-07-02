package email

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// Fetcher 通过 IMAP 拉取邮件。
type Fetcher struct {
	store  *Store
	crypto *Crypto
}

// NewFetcher 构造 Fetcher。
func NewFetcher(store *Store, crypto *Crypto) *Fetcher {
	return &Fetcher{store: store, crypto: crypto}
}

// Sync 同步一个账户的新邮件。返回 (新增邮件数, error)。
func (f *Fetcher) Sync(ctx context.Context, accountID string) (int, error) {
	if f.store == nil {
		return 0, fmt.Errorf("email: store not configured")
	}
	acc, encryptedCred, err := f.store.GetAccountByID(ctx, accountID)
	if err != nil {
		return 0, fmt.Errorf("load account: %w", err)
	}
	if !acc.Enabled {
		return 0, nil
	}
	cred, err := f.crypto.DecryptString(encryptedCred)
	if err != nil {
		return 0, fmt.Errorf("decrypt credential: %w", err)
	}
	if cred == "" || cred == "oauth-pending-no-credential" {
		return 0, fmt.Errorf("account has no usable credential")
	}

	addr := fmt.Sprintf("%s:%d", acc.IMAPHost, acc.IMAPPort)
	client, err := imapclient.DialTLS(addr, nil)
	if err != nil {
		return 0, fmt.Errorf("dial %s: %w", addr, err)
	}
	defer client.Close()

	if err := client.Login(acc.EmailAddress, cred).Wait(); err != nil {
		return 0, fmt.Errorf("login %s: %w", acc.EmailAddress, err)
	}

	mbox, err := client.Select("INBOX", nil).Wait()
	if err != nil {
		return 0, fmt.Errorf("select INBOX: %w", err)
	}
	uidNext := mbox.UIDNext
	highestUID := imap.UID(acc.LastSyncedUID)

	criteria := &imap.SearchCriteria{}
	if acc.LastSyncedUID > 0 {
		var uidSet imap.UIDSet
		uidSet.AddRange(imap.UID(acc.LastSyncedUID+1), uidNext)
		criteria.UID = []imap.UIDSet{uidSet}
	}
	searchData, err := client.UIDSearch(criteria, nil).Wait()
	if err != nil {
		return 0, fmt.Errorf("search: %w", err)
	}
	uids := searchData.AllUIDs()
	if len(uids) == 0 {
		_ = f.store.UpdateSyncState(ctx, accountID, int64(uidNext), time.Now().Unix())
		return 0, nil
	}
	if len(uids) > 50 {
		uids = uids[len(uids)-50:]
	}

	var uidSet imap.UIDSet
	for _, u := range uids {
		uidSet.AddNum(u)
	}
	fetchOpts := &imap.FetchOptions{
		Envelope: true,
		UID:      true,
		BodySection: []*imap.FetchItemBodySection{{
			Specifier: imap.PartSpecifierText,
			Peek:      true,
			Partial:   &imap.SectionPartial{Offset: 0, Size: 5 * 1024},
		}},
	}
	messages, err := client.Fetch(&uidSet, fetchOpts).Collect()
	if err != nil {
		return 0, fmt.Errorf("fetch: %w", err)
	}

	saved := 0
	for _, m := range messages {
		if m.Envelope == nil {
			continue
		}
		fromAddr, fromName := "", ""
		if len(m.Envelope.From) > 0 {
			fromAddr = m.Envelope.From[0].Addr()
			fromName = m.Envelope.From[0].Name
		}
		subject := m.Envelope.Subject
		uid := m.UID
		date := m.Envelope.Date.Unix()
		var snippet string
		for _, bs := range m.BodySection {
			snippet = strings.TrimSpace(string(bs.Bytes))
			break
		}
		if len(snippet) > 500 {
			snippet = snippet[:500]
		}
		em := Email{
			ID:          fmt.Sprintf("em-%d-%s", uid, accountID),
			AccountID:   accountID,
			FromAddress: fromAddr,
			FromName:    fromName,
			Subject:     subject,
			Snippet:     snippet,
			Date:        date,
		}
		if err := f.store.InsertEmail(ctx, em); err != nil {
			log.Printf("[email/fetcher] insert email uid=%d: %v", uid, err)
			continue
		}
		saved++
		if uid > highestUID {
			highestUID = uid
		}
	}
	if err := f.store.UpdateSyncState(ctx, accountID, int64(highestUID), time.Now().Unix()); err != nil {
		log.Printf("[email/fetcher] update sync state %s: %v", accountID, err)
	}
	return saved, nil
}
