package wecom

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/config"
)

// 测试用凭据。EncodingAESKey 是 43 字符（不含 '='），解码后恰好 32 字节。
const (
	testToken    = "QDG6eK" // 企业微信官方文档示例 token
	testCorpID   = "ww5823bf96d3bd5549"
	testAESKey   = "jWmYm7qr5nMoAUwZRjGtBxmz3KA1tkAJHu8vY6NnH2k"
	testAESKeyNo = testAESKey
)

// buildCiphertext **独立于本包的 Encrypt** 手工拼装密文。
//
// 为什么不用 Encrypt 造输入：那样 Encrypt 和 Decrypt 会互为对方的
// 「正确性证明」，两者把 msg_len 和 receiveid 的顺序同时拼反也照样全绿。
// 这里按协议原文（random(16) + len(4,BE) + msg + receiveid）另写一遍，
// 两者只有在协议被正确实现时才对得上。
func buildCiphertext(t *testing.T, aesKeyB64, msg, receiveID string) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(aesKeyB64 + "=")
	if err != nil || len(raw) != 32 {
		t.Fatalf("测试用 AESKey 非法: %v len=%d", err, len(raw))
	}
	random := []byte("0123456789abcdef")
	lenField := make([]byte, 4)
	binary.BigEndian.PutUint32(lenField, uint32(len(msg)))
	full := append([]byte{}, random...)
	full = append(full, lenField...)
	full = append(full, msg...)
	full = append(full, receiveID...)

	block, err := aes.NewCipher(raw)
	if err != nil {
		t.Fatalf("aes.NewCipher: %v", err)
	}
	pad := aes.BlockSize - len(full)%aes.BlockSize
	for i := 0; i < pad; i++ {
		full = append(full, byte(pad))
	}
	out := make([]byte, len(full))
	cipher.NewCBCEncrypter(block, raw[:aes.BlockSize]).CryptBlocks(out, full)
	return base64.StdEncoding.EncodeToString(out)
}

func TestDecryptAgainstHandBuiltCiphertext(t *testing.T) {
	msg := "<xml><ToUserName>ww1</ToUserName><FromUserName>u2</FromUserName><MsgType>text</MsgType><Content>hi</Content></xml>"
	ct := buildCiphertext(t, testAESKey, msg, testCorpID)

	got, err := Decrypt(testAESKey, ct, testCorpID)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if got != msg {
		t.Fatalf("明文 = %q, want %q", got, msg)
	}
}

// Encrypt↔Decrypt 往返：钉住 msg_len / receiveid 的**拼装顺序**。
// 顺序反了的话，手工拼的那条会红而这条会绿——两条一起才算覆盖到。
func TestEncryptDecryptRoundTrip(t *testing.T) {
	msg := "<xml><Event>change_contact</Event></xml>"
	ct, err := Encrypt(testAESKey, msg, testCorpID)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	got, err := Decrypt(testAESKey, ct, testCorpID)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if got != msg {
		t.Fatalf("往返后明文 = %q, want %q", got, msg)
	}
}

// receiveid 不符必须拒：密钥对但收件企业不对，说明这条不是发给本企业的。
func TestDecryptRejectsForeignReceiveID(t *testing.T) {
	ct := buildCiphertext(t, testAESKey, "<xml/>", "ww_someone_else")
	_, err := Decrypt(testAESKey, ct, testCorpID)
	if !errors.Is(err, ErrReceiveID) {
		t.Fatalf("err = %v, want ErrReceiveID", err)
	}
}

// 错误的 EncodingAESKey 必须报错而不是解出乱码。
// 这是最危险的失败模式：不报错的解密密文会一路流到 XML 解析。
func TestDecryptRejectsWrongKey(t *testing.T) {
	ct := buildCiphertext(t, testAESKey, "<xml/>", testCorpID)
	wrong := strings.Repeat(testAESKey[:42], 1) + "X"
	_, err := Decrypt(wrong, ct, testCorpID)
	if err == nil {
		t.Fatal("错误的 EncodingAESKey 竟然解密成功了 —— 必须在填充/长度校验处报错")
	}
}

// Sign 必须**按字典序排序**四个值，而不是按固定顺序拼接。
// token 排在最前和排在最后都要成立；写死顺序是最常见的实现错误。
func TestSignSortsAllFourValues(t *testing.T) {
	ts, nonce, payload := "1409659813", "1372623149", "encrypted-echostr"

	// token 字典序最小 → 排序后 token 在最前。
	if !strings.HasPrefix(payload, "zzz") {
		// 让 token 排到末尾：token = "zzzz"
		tok := "zzzz"
		sorted := assertSortedStringsIsDeterministic([]string{tok, ts, nonce, payload})
		if sorted[0] != payload && sorted[3] != tok {
			t.Fatalf("排序自检失效: %v", sorted)
		}
	}
	// 显式断言：交换 token 与 timestamp 的输入位置不应改变结果。
	a := Sign("aaa", ts, nonce, payload)
	b := Sign(ts, "aaa", nonce, payload)
	if a != b {
		t.Fatalf("输入顺序影响了结果: %s != %s —— Sign 必须先排序", a, b)
	}
	// 改任何一个值都必须改变签名。
	if Sign("aaa", ts, nonce, payload) == Sign("aab", ts, nonce, payload) {
		t.Fatal("改 token 未改变签名")
	}
	if Sign("aaa", ts, nonce, payload) == Sign("aaa", ts, nonce, payload+"x") {
		t.Fatal("改密文未改变签名 —— 说明密文没被纳入签名")
	}
}

func testCfg() config.Config {
	return config.Config{
		WeComToken:          testToken,
		WeComEncodingAESKey: testAESKey,
		WeComCorpID:         testCorpID,
	}
}

func getVerifyURL(msgSig, ts, nonce, echostr string) string {
	return "/callback/weixin?msg_signature=" + msgSig +
		"&timestamp=" + ts + "&nonce=" + nonce + "&echostr=" + echostr
}

// GET 验签成功 → 返回解密明文。
//
// 断言「不带引号 / 不带结尾换行」是有实据的：企业微信要求原样返回明文，
// 带了引号或换行管理端会判定验证失败。这两件事在测试里最容易被 json.Encoder 破坏。
func TestHandleVerifyReturnsPlaintext(t *testing.T) {
	ts, nonce := "1409659813", "1372623149"
	echostr := buildCiphertext(t, testAESKey, "1616140317555161061", testCorpID)
	sig := Sign(testToken, ts, nonce, echostr)

	req := httptest.NewRequest(http.MethodGet, getVerifyURL(sig, ts, nonce, echostr), nil)
	rec := httptest.NewRecorder()
	PublicEntry(testCfg(), nil)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%q)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if body != "1616140317555161061" {
		t.Fatalf("body = %q, want 明文原样返回", body)
	}
	if strings.HasSuffix(body, "\n") {
		t.Fatal("明文结尾带换行 —— 企业微信会判定验证失败")
	}
	if strings.HasPrefix(body, `"`) {
		t.Fatal("明文被 JSON 化了（带引号）—— 必须原样返回")
	}
}

func TestHandleVerifyRejectsBadSignature(t *testing.T) {
	ts, nonce := "1409659813", "1372623149"
	echostr := buildCiphertext(t, testAESKey, "1616140317555161061", testCorpID)
	bad := strings.Repeat("0", 40) // 合法长度但算错的签名

	req := httptest.NewRequest(http.MethodGet, getVerifyURL(bad, ts, nonce, echostr), nil)
	rec := httptest.NewRecorder()
	PublicEntry(testCfg(), nil)(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403（伪造签名必须被拒）", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "sha") {
		t.Fatalf("403 响应体回显了期望签名，等于把算法变成预言机: %q", rec.Body.String())
	}
}

// 三项凭据任一缺失都必须拒绝，绝不能因为「没配就跳过验签」而放行。
func TestHandleVerifyRefusesWhenNotConfigured(t *testing.T) {
	ts, nonce := "1409659813", "1372623149"
	echostr := buildCiphertext(t, testAESKey, "x", testCorpID)
	sig := Sign(testToken, ts, nonce, echostr)

	cases := map[string]config.Config{
		"缺 Token":  {WeComEncodingAESKey: testAESKey, WeComCorpID: testCorpID},
		"缺 AESKey": {WeComToken: testToken, WeComCorpID: testCorpID},
		"缺 CorpID": {WeComToken: testToken, WeComEncodingAESKey: testAESKey},
		"全缺":       {},
	}
	for name, cfg := range cases {
		req := httptest.NewRequest(http.MethodGet, getVerifyURL(sig, ts, nonce, echostr), nil)
		rec := httptest.NewRecorder()
		PublicEntry(cfg, nil)(rec, req)
		if rec.Code == http.StatusOK {
			t.Fatalf("%s：未配置却返回 200 —— 回调端点会变成任何人可伪造的公开入口", name)
		}
	}
}

// 明文模式：只带 echostr、不带签名 → 原样返回，不做加解密。
func TestHandleVerifyPlaintextModeEchoesEchostr(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/callback/weixin?echostr=hello-plain", nil)
	rec := httptest.NewRecorder()
	PublicEntry(config.Config{}, nil)(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "hello-plain" {
		t.Fatalf("明文模式 status=%d body=%q", rec.Code, rec.Body.String())
	}
}

func postEvent(t *testing.T, cfg config.Config, msg string) *httptest.ResponseRecorder {
	t.Helper()
	ct := buildCiphertext(t, testAESKey, msg, testCorpID)
	ts, nonce := "1409659813", "1372623149"
	sig := Sign(testToken, ts, nonce, ct)
	body := "<xml><ToUserName><![CDATA[" + testCorpID + "]]></ToUserName>" +
		"<Encrypt><![CDATA[" + ct + "]]></Encrypt></xml>"

	req := httptest.NewRequest(http.MethodPost, getVerifyURL(sig, ts, nonce, "ignored"), strings.NewReader(body))
	rec := httptest.NewRecorder()
	PublicEntry(cfg, func(string, any) {})(rec, req)
	return rec
}

func TestHandleEventAcceptsAndReturnsSuccess(t *testing.T) {
	got := make(chan callbackEvent, 1)
	cfg := testCfg()
	ct := buildCiphertext(t, cfg.WeComEncodingAESKey,
		"<xml><ToUserName>ww1</ToUserName><MsgType>event</MsgType><Event>change_contact</Event></xml>",
		testCorpID)
	ts, nonce := "1409659813", "1372623149"
	sig := Sign(testToken, ts, nonce, ct)
	body := "<xml><Encrypt><![CDATA[" + ct + "]]></Encrypt></xml>"
	req := httptest.NewRequest(http.MethodPost, getVerifyURL(sig, ts, nonce, "ignored"), strings.NewReader(body))
	rec := httptest.NewRecorder()
	PublicEntry(cfg, func(_ string, p any) { got <- p.(callbackEvent) })(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	// 企业微信要求事件推送返回字符串 success（5 秒内）。
	if rec.Body.String() != "success" {
		t.Fatalf("body = %q, want \"success\"", rec.Body.String())
	}
	select {
	case ev := <-got:
		if ev.Event != "change_contact" {
			t.Fatalf("广播事件 Event = %q", ev.Event)
		}
	case <-time.After(3 * time.Second):
		// 广播是 go 出去的（企业微信 5 秒窗口不允许被 Hub 拖住），
		// 所以这里必须等，不能用 default 立刻判失败。
		t.Fatal("3 秒内没有广播出事件")
	}
}

func TestHandleEventRejectsBadSignature(t *testing.T) {
	ct := buildCiphertext(t, testAESKey, "<xml/>", testCorpID)
	ts, nonce := "1409659813", "1372623149"
	body := "<xml><Encrypt><![CDATA[" + ct + "]]></Encrypt></xml>"
	req := httptest.NewRequest(http.MethodPost,
		getVerifyURL(strings.Repeat("0", 40), ts, nonce, "ignored"), strings.NewReader(body))
	rec := httptest.NewRecorder()
	PublicEntry(testCfg(), nil)(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

// 严格 XML 解析失败时，正则兜底仍要能取出 Encrypt。
// 官方文档确实提到过「解密抛 IllegalBlockSizeException / 密文被框架层处理过」。
func TestExtractEncryptRegexFallback(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"标准 CDATA", "<xml><Encrypt><![CDATA[ABCDEF]]></Encrypt></xml>"},
		{"无 CDATA", "<xml><Encrypt>ABCDEF</Encrypt></xml>"},
		{"多换行缩进", "<xml>\n  <Encrypt>\n    ABCDEF\n  </Encrypt>\n</xml>"},
	}
	for _, c := range cases {
		if got := extractEncrypt([]byte(c.body)); got != "ABCDEF" {
			t.Fatalf("%s: extractEncrypt = %q, want ABCDEF", c.name, got)
		}
	}
	if got := extractEncrypt([]byte("<xml><Other>x</Other></xml>")); got != "" {
		t.Fatalf("没有 Encrypt 节点时返回了 %q，应为空", got)
	}
}

// EncodingAESKey 的补位是这类实现最常见的静默失败点：43 字符解码只有 31 字节。
func TestAesKeyRejectsWrongLength(t *testing.T) {
	for _, k := range []string{"", "short", strings.Repeat("A", 44), "!!!not-base64!!!"} {
		if _, err := aesKey(k); err == nil {
			t.Fatalf("aesKey(%q) 未报错", k)
		}
	}
	if _, err := aesKey(testAESKey); err != nil {
		t.Fatalf("aesKey(合法 43 字符) 报错: %v", err)
	}
	// 带 '=' 的写法也要能吃下。
	if _, err := aesKey(testAESKey + "="); err != nil {
		t.Fatalf("aesKey(带补位) 报错: %v", err)
	}
}
