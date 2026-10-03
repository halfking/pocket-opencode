package feishu

// decrypt_event_test.go — 事件解密是否与**飞书官方算法**一致。
//
// 官方「事件订阅 - 事件解密」算法：
//
//	key  = sha256(encrypt_key)
//	raw  = base64decode(encrypt)
//	iv   = raw[:16]                     // IV 内嵌在密文头部
//	body = AES-256-CBC-decrypt(raw[16:])  -> PKCS#7 unpad
//
// 2026-10-01 补：上一轮修了验签（§7y），但**没实现解密**。
// 后果：配了 Encrypt Key 后虽能过验签，body 仍是密文 -> 事件解析必然失败。
//
// 本测试用**官方文档自带的测试向量**做真值锚点（不是自己造的密文）：
//
//	encrypt_key = "test key"
//	encrypt     = "P37w+VZImNgPEO1RBhJ6RtKl7n6zymIbEG1pReEzghk="
//	明文         = "hello world"
//
// 该向量在官方 Python/Java 示例中均出现，来源可追溯。
// 官方参考：
// https://open.feishu.cn/document/uAjLw4CM/ukTMukTMukTM/event-subscription-guide/event-subscription-configure-/request-url-configuration-case
//
// 负控对照：把 IV 取成后 16 字节 / 把 key 改成直接用 encrypt_key（不 sha256）
//          -> TestDecryptEvent_OfficialTestVector 转红。

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/config"
)

// strconvNow 返回当前 Unix 秒的字符串形式（验签要求 5 分钟内的新鲜时间戳）
func strconvNow() string {
	return strconv.FormatInt(time.Now().Unix(), 10)
}

// 官方文档测试向量
const (
	officialTestKey     = "test key"
	officialTestEncrypt = "P37w+VZImNgPEO1RBhJ6RtKl7n6zymIbEG1pReEzghk="
	officialTestPlain   = "hello world"
)

func TestDecryptEvent_OfficialTestVector(t *testing.T) {
	got, err := decryptEvent(officialTestKey, officialTestEncrypt)
	if err != nil {
		t.Fatalf("decryptEvent must handle the official doc vector: %v", err)
	}
	if string(got) != officialTestPlain {
		t.Fatalf("official vector: got %q want %q", string(got), officialTestPlain)
	}
}

// encrypter 用来构造测试密文（生产代码只解密，不需要加密）
func encrypter(t *testing.T, key, plaintext string) string {
	t.Helper()
	k := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(k[:])
	if err != nil {
		t.Fatal(err)
	}
	// PKCS#7 填充
	pad := aes.BlockSize - len(plaintext)%aes.BlockSize
	padded := append([]byte(plaintext), bytesRepeat(byte(pad), pad)...)
	// IV 用固定 16 字节，便于复现
	iv := []byte("0123456789abcdef")
	out := make([]byte, len(iv)+len(padded))
	copy(out, iv)
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out[aes.BlockSize:], padded)
	return base64.StdEncoding.EncodeToString(out)
}

func bytesRepeat(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}

func TestDecryptEvent_RoundTrip(t *testing.T) {
	plain := `{"schema":"2.0","header":{"event_id":"e1"},"event":{"type":"im.message.receive_v1"}}`
	got, err := decryptEvent("k-123", encrypter(t, "k-123", plain))
	if err != nil {
		t.Fatalf("round trip failed: %v", err)
	}
	if string(got) != plain {
		t.Fatalf("round trip mismatch:\n got %q\nwant %q", string(got), plain)
	}
}

// TestDecryptEvent_RejectsMalformed 畸形输入必须报错而不是 panic 或返回垃圾。
func TestDecryptEvent_RejectsMalformed(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		input string
	}{
		{"empty key", "", officialTestEncrypt},
		{"not base64", "k", "!!!not-base64!!!"},
		{"too short", "k", base64.StdEncoding.EncodeToString([]byte("short"))},
		{"iv only, no ciphertext", "k", base64.StdEncoding.EncodeToString(bytesRepeat(0x41, 16))},
		{"ciphertext not block aligned", "k", base64.StdEncoding.EncodeToString(append(bytesRepeat(0x41, 16), []byte("xyz")...))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if out, err := decryptEvent(tc.key, tc.input); err == nil {
				t.Fatalf("malformed input must be rejected, got %q", string(out))
			}
		})
	}
}

func TestUnpadPKCS7_RejectsInvalidPadding(t *testing.T) {
	// 填充字节为 0（非法）
	if _, err := unpadPKCS7([]byte{1, 2, 3, 0}, 16); err == nil {
		t.Fatal("padding byte 0 must be rejected")
	}
	// 填充字节超过块大小
	if _, err := unpadPKCS7([]byte{1, 2, 3, 17}, 16); err == nil {
		t.Fatal("padding byte > block size must be rejected")
	}
	// 填充字节超过数据长度
	if _, err := unpadPKCS7([]byte{9}, 16); err == nil {
		t.Fatal("padding byte > len must be rejected")
	}
	// 内容不一致
	if _, err := unpadPKCS7([]byte{1, 2, 3, 3, 2}, 16); err == nil {
		t.Fatal("inconsistent padding must be rejected")
	}
	// 空输入
	if _, err := unpadPKCS7(nil, 16); err == nil {
		t.Fatal("empty plaintext must be rejected")
	}
	// 合法
	got, err := unpadPKCS7([]byte{'h', 'i', 4, 4, 4, 4}, 16)
	if err != nil {
		t.Fatalf("valid padding must be accepted: %v", err)
	}
	if string(got) != "hi" {
		t.Fatalf("got %q want %q", string(got), "hi")
	}
}

// TestHandler_EncryptedEventEndToEnd 端到端：加密 body -> handler -> 派发成功。
// 这是本轮修复的真正目标：配了 Encrypt Key 后事件能被正确处理。
func TestHandler_EncryptedEventEndToEnd(t *testing.T) {
	key := "e2e-encrypt-key"
	plain := `{"schema":"2.0","header":{"event_id":"e1"},"event":{"type":"im.message.receive_v1"}}`
	body, err := json.Marshal(map[string]string{"encrypt": encrypter(t, key, plain)})
	if err != nil {
		t.Fatal(err)
	}

	var dispatched string
	h := PublicEntry(config.Config{FeishuEncryptKey: key}, func(msgType string, payload interface{}) {
		dispatched = msgType
	})

	// 构造合法签名（对**原始加密 body**签名）
	ts := "1700000000"
	nonce := "n-once"
	req := httptest.NewRequest(http.MethodPost, "/callback/feishu", strings.NewReader(string(body)))
	req.Header.Set("X-Lark-Request-Timestamp", ts)
	req.Header.Set("X-Lark-Request-Nonce", nonce)
	req.Header.Set("X-Lark-Signature", officialSign(ts, nonce, key, string(body)))
	// 时间戳窗口用当前时间重新签名
	now := strconvNow()
	req.Header.Set("X-Lark-Request-Timestamp", now)
	req.Header.Set("X-Lark-Signature", officialSign(now, nonce, key, string(body)))

	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("encrypted event must be accepted, got %d body=%s", rec.Code, rec.Body.String())
	}
	// broadcast 收到的是 handleMessageEvent 转换后的类型 "feishu.message"
	// （见 handler.go handleMessageEvent），说明解密后的明文被正确解析并派发。
	if dispatched != "feishu.message" {
		t.Fatalf("decrypted event must be dispatched as feishu.message, got %q", dispatched)
	}
}

// TestHandler_EncryptedEventRejectsBadSignature 加密事件同样必须验签，
// 防止绕过（解密分支不能成为验签的旁路）。
func TestHandler_EncryptedEventRejectsBadSignature(t *testing.T) {
	key := "e2e-encrypt-key"
	body, err := json.Marshal(map[string]string{"encrypt": encrypter(t, key, `{"schema":"2.0","event":{"type":"x"}}`)})
	if err != nil {
		t.Fatal(err)
	}
	h := PublicEntry(config.Config{FeishuEncryptKey: key}, func(string, interface{}) {
		t.Fatal("must not dispatch an unsigned event")
	})
	req := httptest.NewRequest(http.MethodPost, "/callback/feishu", strings.NewReader(string(body)))
	now := strconvNow()
	req.Header.Set("X-Lark-Request-Timestamp", now)
	req.Header.Set("X-Lark-Request-Nonce", "n")
	req.Header.Set("X-Lark-Signature", "deadbeef") // 错误签名
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad signature on encrypted event must be 401, got %d", rec.Code)
	}
}

// TestHandler_EncryptedEventWithoutKeyIsRejected 未配密钥时收到加密事件必须拒绝，
// 不能静默当明文解析。
func TestHandler_EncryptedEventWithoutKeyIsRejected(t *testing.T) {
	body := `{"encrypt":"P37w+VZImNgPEO1RBhJ6RtKl7n6zymIbEG1pReEzghk="}`
	h := PublicEntry(config.Config{}, func(string, interface{}) {
		t.Fatal("must not dispatch when no key configured")
	})
	req := httptest.NewRequest(http.MethodPost, "/callback/feishu", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("encrypted event without key must be 401, got %d", rec.Code)
	}
}

// TestHandler_ProductionRejectsUnsignedWhenKeysEmpty 2026-10-03 安全回归。
//
// 修复前：signatureKey 为空时只打一行 WARNING 就放行，攻击者构造一条未签名的
// im.message.receive_v1 就能把内容广播进所有已连接客户端的实时通道（端到端实测）。
// 修复后：production 一律 503 拒绝，与企业微信侧 config.go:72-73 的自述策略一致。
func TestHandler_ProductionRejectsUnsignedWhenKeysEmpty(t *testing.T) {
	body, err := json.Marshal(map[string]any{
		"schema": "2.0",
		"header": map[string]any{"event_id": "e-unsigned", "event_type": "im.message.receive_v1"},
		"event": map[string]any{
			"type":    "im.message.receive_v1",
			"app_id":  "cli_evil",
			"message": map[string]any{"message_id": "om_evil", "chat_id": "oc_evil", "msg_type": "text", "content": "{\"text\":\"INJECTED\"}"},
			"sender":  map[string]any{"sender_id": map[string]any{"open_id": "ou_evil"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	broadcasts := 0
	h := PublicEntry(config.Config{Environment: "production"}, func(string, interface{}) {
		broadcasts++
	})
	// 刻意不带任何 X-Lark-* 头：这就是攻击者能发出的请求
	req := httptest.NewRequest(http.MethodPost, "/callback/feishu", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("production + 无密钥 + 未签名事件应 503，实际 %d body=%s", rec.Code, rec.Body.String())
	}
	// 关键：不只看状态码。"返回 503 但事件已经广播出去"这种改动骗得过上面的断言
	if broadcasts != 0 {
		t.Fatalf("未验签事件在拒绝之后仍被广播了 %d 次——封堵必须在 dispatch 之前", broadcasts)
	}
}

// TestHandler_DevStillSkipsWhenKeysEmpty 负控：守卫是**按环境**生效的。
// 把守卫写成无条件的实现会让本条转红，从而证明上一条不是因为"总是拒绝"而绿。
func TestHandler_DevStillSkipsWhenKeysEmpty(t *testing.T) {
	body, err := json.Marshal(map[string]any{
		"schema": "2.0",
		"header": map[string]any{"event_id": "e-dev"},
		"event":  map[string]any{"type": "im.message.receive_v1", "app_id": "cli_dev"},
	})
	if err != nil {
		t.Fatal(err)
	}

	broadcasts := 0
	h := PublicEntry(config.Config{Environment: "development"}, func(string, interface{}) {
		broadcasts++
	})
	req := httptest.NewRequest(http.MethodPost, "/callback/feishu", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("development + 无密钥 + 未签名事件应保持 200（dev 流程不受影响），实际 %d body=%s", rec.Code, rec.Body.String())
	}
	if broadcasts == 0 {
		t.Fatal("development 下应仍然派发——若这里也 0，说明守卫被写成了无条件的")
	}
}

// TestHandler_ProductionRejectsUnsignedProdAlias 同一条守卫对 legacy 别名
// "prod" 也要生效：IsProduction() 接受两种写法，只测 "production" 会漏掉一半。
func TestHandler_ProductionRejectsUnsignedProdAlias(t *testing.T) {
	body, _ := json.Marshal(map[string]any{
		"schema": "2.0",
		"header": map[string]any{"event_id": "e-prod-alias"},
		"event":  map[string]any{"type": "im.message.receive_v1"},
	})
	broadcasts := 0
	h := PublicEntry(config.Config{Environment: "prod"}, func(string, interface{}) { broadcasts++ })
	req := httptest.NewRequest(http.MethodPost, "/callback/feishu", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("Environment=\"prod\" 同样应 503，实际 %d", rec.Code)
	}
	if broadcasts != 0 {
		t.Fatalf("prod 别名下未验签事件仍被广播 %d 次", broadcasts)
	}
}
