package server

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

// ============================================================================
// SSO 服务端状态存储（2026-09-05 state 合约修复的 pocket 侧载体）
//
// 背景见 docs/handoff/2026-09-05-sso-state-contract-mismatch.md：
// RedClaw auth-agent 忽略外部 state 并自行生成，pocket 前端的
// sessionStorage 严格比对永远失败。修复后 CSRF 绑定收敛到 pocket 服务端：
//
//   ssoTxnStore     — /api/auth/sso/login 签发一次性 nonce，callback 必须
//                     携带同值 HttpOnly cookie 并消费（防冷启动/重放回调）；
//   ssoExchangeStore — callback 成功后签发一次性短时 code，前端用它 POST
//                     /api/auth/sso/exchange 换 token（P1-2：token 不再进 URL）。
//
// 两者都是进程内存表：数据天然短命（分钟级）、丢失的最坏后果是用户重试
// 一次登录，不值得引入 PG 依赖；容量上限防内存放大。
// ============================================================================

const (
	// ssoTxnCookie 承载登录绑定 nonce。SameSite=Lax 允许 IdP 顶层 GET 导航带回；
	// HttpOnly 防 JS 读取；Path 收窄到 SSO 端点族。
	ssoTxnCookie   = "pocket_sso_txn"
	ssoTxnTTL      = 10 * time.Minute
	ssoExchangeTTL = 90 * time.Second
	ssoTxnCap      = 4096
	ssoExchangeCap = 1024
)

var errSSOStoreFull = errors.New("sso state store full")

// ssoTxnStore 保存待消费的登录绑定 nonce（login 签发 → callback 单次消费）。
type ssoTxnStore struct {
	mu  sync.Mutex
	ttl time.Duration
	cap int
	m   map[string]time.Time
}

func newSSOTxnStore(ttl time.Duration, cap int) *ssoTxnStore {
	return &ssoTxnStore{ttl: ttl, cap: cap, m: make(map[string]time.Time)}
}

// Issue 签发新 nonce。表满时先清一遍过期项，仍满则报错（正常流量下不可达）。
func (st *ssoTxnStore) Issue() (string, error) {
	nonce, err := randomHex(32)
	if err != nil {
		return "", err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.m) >= st.cap {
		st.sweepLocked()
		if len(st.m) >= st.cap {
			return "", errSSOStoreFull
		}
	}
	st.m[nonce] = time.Now().Add(st.ttl)
	return nonce, nil
}

// Consume 单次校验并删除 nonce：不存在或已过期都返回 false。
func (st *ssoTxnStore) Consume(nonce string) bool {
	if nonce == "" {
		return false
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	exp, ok := st.m[nonce]
	if !ok {
		return false
	}
	delete(st.m, nonce) // 无论是否过期都移除：过期 nonce 没有复活的价值
	return !time.Now().After(exp)
}

// ssoHandoff 一次性 code 换回的登录结果（等价旧 302 query 载荷）。
type ssoHandoff struct {
	Token       string `json:"token"`
	User        string `json:"user"`
	UserID      string `json:"user_id"`
	WorkspaceID string `json:"workspace_id"`
}

type ssoExchangeEntry struct {
	h   ssoHandoff
	exp time.Time
}

// ssoExchangeStore 保存待交换的一次性 code（callback 签发 → exchange 单次消费）。
type ssoExchangeStore struct {
	mu  sync.Mutex
	ttl time.Duration
	cap int
	m   map[string]ssoExchangeEntry
}

func newSSOExchangeStore(ttl time.Duration, cap int) *ssoExchangeStore {
	return &ssoExchangeStore{ttl: ttl, cap: cap, m: make(map[string]ssoExchangeEntry)}
}

// Put 登记登录结果并返回一次性 code。
func (st *ssoExchangeStore) Put(h ssoHandoff) (string, error) {
	code, err := randomHex(32)
	if err != nil {
		return "", err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.m) >= st.cap {
		st.sweepLocked()
		if len(st.m) >= st.cap {
			return "", errSSOStoreFull
		}
	}
	st.m[code] = ssoExchangeEntry{h: h, exp: time.Now().Add(st.ttl)}
	return code, nil
}

// Take 单次取出：命中即删除，二次使用同一 code 返回 false。
func (st *ssoExchangeStore) Take(code string) (ssoHandoff, bool) {
	if code == "" {
		return ssoHandoff{}, false
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	e, ok := st.m[code]
	if !ok {
		return ssoHandoff{}, false
	}
	delete(st.m, code)
	if time.Now().After(e.exp) {
		return ssoHandoff{}, false
	}
	return e.h, true
}

func (st *ssoTxnStore) sweepLocked() {
	now := time.Now()
	for k, exp := range st.m {
		if now.After(exp) {
			delete(st.m, k)
		}
	}
}

func (st *ssoExchangeStore) sweepLocked() {
	now := time.Now()
	for k, e := range st.m {
		if now.After(e.exp) {
			delete(st.m, k)
		}
	}
}

// randomHex 返回 n 字节 CSPRNG 熵的 hex 串（2n 字符）。
func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
