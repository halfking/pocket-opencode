// Package wecom 处理 m.kxpms.cn/callback/weixin 企业微信事件回调。
//
// 平台：**企业微信（WeCom）自建应用**（2026-10-02 用户拍板，不是微信公众号）。
// 这两者不是一回事，回调返回格式也不同，别混：
//
//	企业微信 WeCom      GET  → 验签 + 解密 echostr，**原样返回明文**（不加引号、不带 BOM、不带换行）
//	                   POST → 5 秒内返回 "success" 或空串
//	微信公众号（公号）   GET  → 同上（安全模式）；明文模式下直接原样返回 echostr
//	                   POST → 消息需返回**加密 XML** 被动回复，事件才可返回 "success"
//
// 本包只实现企业微信那一侧（POST 统一回 "success"），不做公号的加密 XML 被动回复。
//
// ---- 协议要点（依据企业微信官方「接收消息」/「URL 验证」说明）----
//
// 签名：sha1(字典序排序后的 token、timestamp、nonce、echostr 四者拼接)
//   - 公众号用 sha1(排序后的 token、timestamp、nonce 三者) 且参数名叫 signature；
//     企业微信多带上密文本身、参数名叫 msg_signature。**两者不通用**。
//
// 加解密：AES-256-CBC，密钥 = base64decode(EncodingAESKey 补一个 '=')，IV = 密钥前 16 字节。
//
//	Padding = PKCS#7。
//
// 解密后明文的结构（这不是普通 JSON/XML，是手工拼的二进制包）：
//
//		random(16 字节) + msg_len(4 字节，大端) + msg + receiveid
//
//	  - random：随机前缀，同一条明文每次加密后密文都不同，防密文比对推测内容。
//	  - msg_len：网络字节序（大端）的明文长度，用来切出 msg 段。
//	  - msg：真正的消息内容（企业微信侧通常是 XML）。
//	  - receiveid：CorpID（企业 ID）。**不校验它等于「密文没被改过」**——
//	    密钥对了但 receiveid 不符，说明这条消息不是发给本企业的，直接拒。
//
// 本包对签名比对与 AppID/CorpID 比对都用 constant-time 比较。
package wecom

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1" // #nosec G505 —— 企业微信协议规定用 SHA-1，不可替换
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// 协议固定尺寸：random 前缀 16 字节 + msg_len 4 字节。
const (
	randomPrefixLen = 16
	msgLenFieldLen  = 4
	// minPlainLen = random + msg_len，低于这个长度一定不是合法明文包。
	minPlainLen = randomPrefixLen + msgLenFieldLen
)

var (
	// ErrSignature 表示 msg_signature 校验不通过。
	ErrSignature = errors.New("wecom: signature mismatch")
	// ErrDecrypt 表示 AES 解密失败（通常是 EncodingAESKey 不对）。
	ErrDecrypt = errors.New("wecom: decrypt failed")
	// ErrMalformedPlain 表示解密成功但明文包结构不合法。
	ErrMalformedPlain = errors.New("wecom: malformed plaintext")
	// ErrReceiveID 表示解密出的 receiveid 与配置不符。
	ErrReceiveID = errors.New("wecom: receiveid mismatch")
)

// aesKey 把 EncodingAESKey（43 个字符的 base64，不含补位）还原成 32 字节密钥。
//
// 官方给的是 43 字符的 base64，去掉末尾 '=' 后解码只有 31 字节，必须补回一个 '='
// 才能得到完整的 32 字节。**这里不做补位会让 AES 直接报 key size 错**，
// 而错误信息是 "invalid key size 31"，看不出根因。
func aesKey(encodingAESKey string) ([]byte, error) {
	k := strings.TrimSpace(encodingAESKey)
	if k == "" {
		return nil, fmt.Errorf("%w: EncodingAESKey 未配置", ErrDecrypt)
	}
	// 已经有补位就去掉，统一由下面补一个 '='。
	k = strings.TrimRight(k, "=")
	if len(k) != 43 {
		return nil, fmt.Errorf("%w: EncodingAESKey 长度应为 43（补 '=' 前），实际 %d",
			ErrDecrypt, len(k))
	}
	raw, err := base64.StdEncoding.DecodeString(k + "=")
	if err != nil {
		return nil, fmt.Errorf("%w: EncodingAESKey 不是合法 base64: %v", ErrDecrypt, err)
	}
	if len(raw) != 32 {
		return nil, fmt.Errorf("%w: 解码后密钥应为 32 字节，实际 %d", ErrDecrypt, len(raw))
	}
	return raw, nil
}

// Sign 计算企业微信要求的 msg_signature。
//
// 四个值（token、timestamp、nonce、密文本身）**按字典序排序后拼接**，再取 sha1 十六进制小写。
// 注意是排序「字符串」而不是按固定顺序 —— token 可能排在最前也可能排在最后，
// 写死顺序是最常见的实现错误，而且它在自造 token 时往往碰巧能过，线上才炸。
func Sign(token, timestamp, nonce, payload string) string {
	parts := []string{token, timestamp, nonce, payload}
	sort.Strings(parts)
	sum := sha1.Sum([]byte(strings.Join(parts, ""))) // #nosec G401 —— 协议规定
	return fmt.Sprintf("%x", sum)
}

// VerifySignature 校验 msg_signature。expected 为空时跳过（dev 模式）。
func VerifySignature(token, timestamp, nonce, payload, got string, configured bool) error {
	if !configured {
		return nil
	}
	if subtle.ConstantTimeCompare([]byte(Sign(token, timestamp, nonce, payload)), []byte(got)) != 1 {
		return fmt.Errorf("%w: 期望 %s", ErrSignature, Sign(token, timestamp, nonce, payload))
	}
	return nil
}

// Decrypt 解开企业微信的密文包，返回消息正文（msg 段）。
//
// wantReceiveID 非空时会校验尾部 receiveid；留空则跳过该校验（配置缺失时用）。
func Decrypt(encodingAESKey, ciphertextB64, wantReceiveID string) (string, error) {
	key, err := aesKey(encodingAESKey)
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(ciphertextB64))
	if err != nil {
		return "", fmt.Errorf("%w: 密文不是合法 base64: %v", ErrDecrypt, err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrDecrypt, err)
	}
	if len(raw) == 0 || len(raw)%aes.BlockSize != 0 {
		return "", fmt.Errorf("%w: 密文长度 %d 不是 16 的整数倍", ErrDecrypt, len(raw))
	}
	plain := make([]byte, len(raw))
	cipher.NewCBCDecrypter(block, key[:aes.BlockSize]).CryptBlocks(plain, raw)

	// 去掉 PKCS#7 填充。padding 值在 [1, blocksize]，0 或越界说明密钥不对。
	// 不校验就 unpad 的话，错误密钥会解出「看起来合法」的长度然后 panic 在切片上。
	pad := int(plain[len(plain)-1])
	if pad < 1 || pad > aes.BlockSize || pad > len(plain) {
		return "", fmt.Errorf("%w: PKCS#7 填充字节非法（通常是 EncodingAESKey 不对）", ErrDecrypt)
	}
	plain = plain[:len(plain)-pad]

	if len(plain) < minPlainLen {
		return "", fmt.Errorf("%w: 明文仅 %d 字节，不足 random+msg_len", ErrMalformedPlain, len(plain))
	}
	body := plain[randomPrefixLen+msgLenFieldLen:]
	msgLen := int(binary.BigEndian.Uint32(plain[randomPrefixLen : randomPrefixLen+msgLenFieldLen]))
	if msgLen < 0 || msgLen > len(body) {
		return "", fmt.Errorf("%w: msg_len=%d 超出可用明文长度 %d", ErrMalformedPlain, msgLen, len(body))
	}
	msg := string(body[:msgLen])
	receiveID := string(body[msgLen:])

	if wantReceiveID != "" &&
		subtle.ConstantTimeCompare([]byte(receiveID), []byte(wantReceiveID)) != 1 {
		return "", fmt.Errorf("%w: 密文尾部 receiveid=%q，期望 %q", ErrReceiveID, receiveID, wantReceiveID)
	}
	return msg, nil
}

// Encrypt 是 Decrypt 的逆运算，用于加密被动回复。
//
// 保留它不是为了对称美观：企业微信的**消息**（区别于事件）要求回加密包，
// 没有它这个包就只能做事件入口；也是 round-trip 测试能钉住 msg_len/receiveid
// 拼装顺序的前提——拼反了 Decrypt 自己查不出来，得靠 Encrypt↔Decrypt 互验。
func Encrypt(encodingAESKey, msg, receiveID string) (string, error) {
	key, err := aesKey(encodingAESKey)
	if err != nil {
		return "", err
	}
	// 拼装顺序必须是 random(16) || msg_len(4,BE) || msg || receiveid。
	//
	// 这里第一版写成「先把 msg 接到 random 后面，再把长度 prepend 上去」，
	// 得到 len||random||msg||receiveid。Encrypt↔Decrypt 往返测试当场报出
	// msg_len=2816517225 越界——**正是因为往返能测出顺序反了**，
	// 所以 Encrypt 值得实现：只测 Decrypt 的话这个错根本不存在。
	nonce := deriveNonce(receiveID)
	lenField := make([]byte, msgLenFieldLen)
	binary.BigEndian.PutUint32(lenField, uint32(len(msg)))

	full := make([]byte, 0, len(nonce)+msgLenFieldLen+len(msg)+len(receiveID))
	full = append(full, nonce...)
	full = append(full, lenField...)
	full = append(full, msg...)
	full = append(full, receiveID...)

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrDecrypt, err)
	}
	pad := aes.BlockSize - len(full)%aes.BlockSize
	full = append(full, bytesRepeat(byte(pad), pad)...)

	out := make([]byte, len(full))
	cipher.NewCBCEncrypter(block, key[:aes.BlockSize]).CryptBlocks(out, full)
	return base64.StdEncoding.EncodeToString(out), nil
}

func bytesRepeat(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}
