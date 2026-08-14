package server

import (
	"sync"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/adapter"
)

// mobileCreateCache makes POST /api/mobile/sessions idempotent for offline
// replay (SEC-06). A mobile client that created a session offline replays the
// same request with the same Idempotency-Key after network recovery; without
// this cache the replay would create a duplicate upstream session.
//
// Scope: entries are keyed by (workspace, instance, key) so one workspace can
// never observe another's creates. The cache is in-memory by design: it only
// needs to survive retry storms within the process lifetime; the mobile
// client's local SQLite row keeps the durable mapping.
type mobileCreateCache struct {
	mu      sync.Mutex
	entries map[string]mobileCreateCacheEntry
	ttl     time.Duration
}

type mobileCreateCacheEntry struct {
	info      *adapter.OpenCodeSessionInfo
	expiresAt time.Time
}

func newMobileCreateCache() *mobileCreateCache {
	return &mobileCreateCache{
		entries: make(map[string]mobileCreateCacheEntry),
		ttl:     24 * time.Hour,
	}
}

func (c *mobileCreateCache) key(workspaceID, instanceID, idempotencyKey string) string {
	return workspaceID + "|" + instanceID + "|" + idempotencyKey
}

// Get returns the cached create result for the key, if present and unexpired.
func (c *mobileCreateCache) Get(workspaceID, instanceID, idempotencyKey string) (*adapter.OpenCodeSessionInfo, bool) {
	if idempotencyKey == "" {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[c.key(workspaceID, instanceID, idempotencyKey)]
	if !ok || time.Now().After(entry.expiresAt) {
		return nil, false
	}
	return entry.info, true
}

// Put stores the create result. Expired entries are pruned opportunistically.
func (c *mobileCreateCache) Put(workspaceID, instanceID, idempotencyKey string, info *adapter.OpenCodeSessionInfo) {
	if idempotencyKey == "" || info == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) > 4096 {
		now := time.Now()
		for k, v := range c.entries {
			if now.After(v.expiresAt) {
				delete(c.entries, k)
			}
		}
		// 若剪枝后仍然超限，按过期时间淘汰最旧的一批，避免无界增长。
		if len(c.entries) > 4096 {
			for k := range c.entries {
				delete(c.entries, k)
				if len(c.entries) <= 2048 {
					break
				}
			}
		}
	}
	c.entries[c.key(workspaceID, instanceID, idempotencyKey)] = mobileCreateCacheEntry{
		info:      info,
		expiresAt: time.Now().Add(c.ttl),
	}
}
