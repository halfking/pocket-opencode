package server

import (
	"crypto/rand"
	"crypto/subtle"
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
	// ssoTxnPerIPCap 单来源并发 pending 上限：login 每请求铸一个 nonce，
	// 无 per-IP 限额时单 IP 可在 TTL 内灌满全局表，让所有 SSO 登录 500。
	// 32 足以覆盖单个 NAT 出口的正常并发登录流。
	ssoTxnPerIPCap = 32
	ssoExchangeCap = 1024
)

var errSSOStoreFull = errors.New("sso state store full")

// ssoTxnStore 保存待消费的登录绑定 nonce（login 签发 → callback 单次消费）。
type ssoTxnStore struct {
	mu  sync.Mutex
	ttl time.Duration
	cap int
	// perIPCap 限制同一来源的并发 pending 数（防灌表 DoS）；来源取
	// clientIP(r)（XFF 感知）。直连暴露时可被 XFF 伪造绕过，此时仍有
	// 全局 cap 兜底内存。
	perIPCap int
	m        map[string]ssoTxnEntry
}

type ssoTxnEntry struct {
	ip  string
	exp time.Time
	// verifier 仅 Casdoor 直连 OIDC 分支使用（IssueOIDC 落入，ConsumeOIDC
	// 取回，换取 token 时作 PKCE code_verifier）。RedClaw 分支为空。
	verifier string
	// appChallenge binds a RedClaw result to the initiating native app.
	appChallenge string
}

func newSSOTxnStore(ttl time.Duration, cap int) *ssoTxnStore {
	return &ssoTxnStore{ttl: ttl, cap: cap, perIPCap: ssoTxnPerIPCap, m: make(map[string]ssoTxnEntry)}
}

// Issue 签发新 nonce。全局表满或单来源 pending 超 perIPCap 时先清一遍
// 过期项，仍超则报错（正常流量下不可达）。
func (st *ssoTxnStore) issue(ip string, entry ssoTxnEntry) (string, error) {
	nonce, err := randomHex(32)
	if err != nil {
		return "", err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.m) >= st.cap || st.countByIPLocked(ip) >= st.perIPCap {
		st.sweepLocked()
		if len(st.m) >= st.cap || st.countByIPLocked(ip) >= st.perIPCap {
			return "", errSSOStoreFull
		}
	}
	entry.ip = ip
	entry.exp = time.Now().Add(st.ttl)
	st.m[nonce] = entry
	return nonce, nil
}

// Consume 单次校验并删除 nonce：不存在或已过期都返回 false。
func (st *ssoTxnStore) consume(nonce string) (ssoTxnEntry, bool) {
	if nonce == "" {
		return ssoTxnEntry{}, false
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	e, ok := st.m[nonce]
	if !ok {
		return ssoTxnEntry{}, false
	}
	delete(st.m, nonce) // 无论是否过期都移除：过期 nonce 没有复活的价值
	return e, !time.Now().After(e.exp)
}

// IssueOIDC 签发带 PKCE verifier 的 OIDC 登录事务。nonce 语义与 Issue
// 完全一致（同一张表、同一 TTL/cap），额外携带 verifier 供 callback 换
// token 时回传 code_verifier。
func (st *ssoTxnStore) IssueOIDC(ip, verifier string) (string, error) {
	return st.issue(ip, ssoTxnEntry{verifier: verifier})
}

// ConsumeOIDC 单次校验并删除 nonce，返回其 PKCE verifier。
func (st *ssoTxnStore) ConsumeOIDC(nonce string) (string, bool) {
	e, ok := st.consume(nonce)
	if !ok || e.verifier == "" {
		return "", false
	}
	return e.verifier, true
}

// ssoHandoff 一次性 code 换回的登录结果（等价旧 302 query 载荷）。
type ssoHandoff struct {
	Token       string `json:"token"`
	User        string `json:"user"`
	UserID      string `json:"user_id"`
	WorkspaceID string `json:"workspace_id"`
	AuthMethod  string `json:"auth_method"`
}

const authMethodCasdoorOIDC = "casdoor-oidc"

type ssoExchangeEntry struct {
	h            ssoHandoff
	exp          time.Time
	appChallenge string
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
func (st *ssoExchangeStore) Put(h ssoHandoff, appChallenge string) (string, error) {
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
	st.m[code] = ssoExchangeEntry{h: h, exp: time.Now().Add(st.ttl), appChallenge: appChallenge}
	return code, nil
}

// Take consumes a live code only after its app binding matches.
func (st *ssoExchangeStore) Take(code, appChallenge string) (ssoHandoff, bool) {
	if code == "" {
		return ssoHandoff{}, false
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	e, ok := st.m[code]
	if !ok {
		return ssoHandoff{}, false
	}
	if time.Now().After(e.exp) {
		delete(st.m, code)
		return ssoHandoff{}, false
	}
	if subtle.ConstantTimeCompare([]byte(e.appChallenge), []byte(appChallenge)) != 1 {
		return ssoHandoff{}, false
	}
	delete(st.m, code)
	return e.h, true
}

func (st *ssoTxnStore) sweepLocked() {
	now := time.Now()
	for k, e := range st.m {
		if now.After(e.exp) {
			delete(st.m, k)
		}
	}
}

func (st *ssoTxnStore) countByIPLocked(ip string) int {
	if ip == "" {
		return 0 // 无来源信息时不做 per-IP 限制（退回全局 cap 兜底）
	}
	n := 0
	for _, e := range st.m {
		if e.ip == ip {
			n++
		}
	}
	return n
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
