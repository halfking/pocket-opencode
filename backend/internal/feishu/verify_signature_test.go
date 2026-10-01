package feishu

// verify_signature_test.go — 事件回调验签是否与**飞书官方算法**一致。
//
// 官方「事件订阅 - 签名校验」（请求地址配置文档）给出的公式是：
//
//	b1 = (timestamp + nonce + encrypt_key).encode('utf-8')
//	b  = b1 + body
//	s  = sha256(b)            # 裸 SHA-256，不是 HMAC
//	校验 s（小写 hex）== 请求头 X-Lark-Signature
//
// 2026-10-01 实测发现本仓原实现三处全错（handler.go 旧版）：
//
//	① 用 HMAC-SHA256 而非裸 SHA-256
//	② 密钥串位置错：官方是 ts+nonce+key+body 全部参与**哈希**，旧版把 body 当 HMAC 的 data
//	③ 输出用 base64 而非小写 hex
//
// 三者任一不符都会导致飞书真实回调**恒 401**。本测试按官方算法构造签名来锁死实现。
// 官方参考（含 Python/Java/Golang/Node/PHP 五语言示例）：
// https://open.feishu.cn/document/uAjLw4CM/ukTMukTMukTM/event-subscription-guide/event-subscription-configure-/request-url-configuration-case
//
// 负控对照：把实现改回 HMAC/base64/data-分离 -> TestVerifySignature_AcceptsFeishuOfficialSignature 转红。

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strconv"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/config"
)

// officialSign 按飞书官方算法计算签名：hex(sha256(timestamp + nonce + encryptKey + body))
func officialSign(timestamp, nonce, encryptKey, body string) string {
	sum := sha256.Sum256([]byte(timestamp + nonce + encryptKey + body))
	return hex.EncodeToString(sum[:])
}

func TestVerifySignature_AcceptsFeishuOfficialSignature(t *testing.T) {
	key := "test-encrypt-key"
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := "abc123nonce"
	body := `{"schema":"2.0","header":{"event_id":"e1"},"event":{"type":"im.message.receive_v1"}}`

	if !verifySignature(ts, nonce, key, body, officialSign(ts, nonce, key, body)) {
		t.Fatal("handler must accept the OFFICIAL feishu signature: hex(sha256(ts+nonce+encryptKey+body))")
	}
}

// TestVerifySignature_BodyParticipatesInSignature 锁死「body 参与哈希」这一语义。
// 官方公式里 body 是被哈希内容的一部分——把它去掉会算出完全不同的签名。
// 这条同时反证了旧实现（body 当 HMAC data、密钥串不含 body）是错的。
func TestVerifySignature_BodyParticipatesInSignature(t *testing.T) {
	key := "k3y"
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	body := `{"a":1}`

	// 用 A 的 body 算出的签名，不能验过 B 的 body
	if verifySignature(ts, "n", key, `{"b":2}`, officialSign(ts, "n", key, body)) {
		t.Fatal("signature computed over body A must NOT verify body B (body participates in the hash)")
	}
	// 且正确 body 必须通过
	if !verifySignature(ts, "n", key, body, officialSign(ts, "n", key, body)) {
		t.Fatal("signature over the correct body must verify")
	}
}

// TestVerifySignature_RejectsLegacyHmacBase64Variant 负控对照：
// 旧实现（HMAC-SHA256，key=ts+nonce+secret，data=body，base64 输出）必须被拒绝。
func TestVerifySignature_RejectsLegacyHmacBase64Variant(t *testing.T) {
	key := "k3y"
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := "n"
	body := `{"a":1}`

	h := hmac.New(sha256.New, []byte(ts+nonce+key))
	h.Write([]byte(body))
	legacy := base64.StdEncoding.EncodeToString(h.Sum(nil))

	if verifySignature(ts, nonce, key, body, legacy) {
		t.Fatal("legacy HMAC+base64 signature must be rejected; it is NOT the feishu algorithm")
	}
}

func TestVerifySignature_RejectsStaleTimestamp(t *testing.T) {
	key := "s3cret"
	old := strconv.FormatInt(time.Now().Add(-10*time.Minute).Unix(), 10)
	if verifySignature(old, "n", key, "{}", officialSign(old, "n", key, "{}")) {
		t.Fatal("stale timestamp must be rejected (replay protection)")
	}
	future := strconv.FormatInt(time.Now().Add(10*time.Minute).Unix(), 10)
	if verifySignature(future, "n", key, "{}", officialSign(future, "n", key, "{}")) {
		t.Fatal("far-future timestamp must be rejected")
	}
}

func TestVerifySignature_RejectsMissingHeader(t *testing.T) {
	if verifySignature("", "n", "key", "{}", "") {
		t.Fatal("missing timestamp/signature must be rejected when key is configured")
	}
	if verifySignature("not-a-number", "n", "key", "{}", "abc") {
		t.Fatal("non-numeric timestamp must be rejected")
	}
}

func TestVerifySignature_DevModeSkipsWhenKeyEmpty(t *testing.T) {
	// 未配置密钥时跳过验签（dev 模式），这是既有行为，锁住它。
	if !verifySignature("", "", "", "{}", "") {
		t.Fatal("empty key must skip verification (dev mode)")
	}
}

func TestVerifySignature_RejectsWrongSignature(t *testing.T) {
	key := "s3cret"
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	if verifySignature(ts, "n", key, "{}", "deadbeef") {
		t.Fatal("wrong signature must be rejected")
	}
	// 换了 body 就失效
	if verifySignature(ts, "n", key, `{"x":9}`, officialSign(ts, "n", key, "{}")) {
		t.Fatal("signature must be bound to the exact body")
	}
}

// TestSignatureKey_PrefersEncryptKey 锁死密钥选取顺序：
// 官方算法用的是 Encrypt Key，Verify Secret 仅为历史回退。
func TestSignatureKey_PrefersEncryptKey(t *testing.T) {
	if got := signatureKey(config.Config{FeishuEncryptKey: "ek", FeishuVerifySecret: "vs"}); got != "ek" {
		t.Fatalf("Encrypt Key must win, got %q", got)
	}
	if got := signatureKey(config.Config{FeishuVerifySecret: "vs"}); got != "vs" {
		t.Fatalf("must fall back to Verify Secret, got %q", got)
	}
	if got := signatureKey(config.Config{}); got != "" {
		t.Fatalf("both empty = dev mode, got %q", got)
	}
}
