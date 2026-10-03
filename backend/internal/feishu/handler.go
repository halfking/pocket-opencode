// Package feishu 处理 m.kxpms.cn/callback/feishu 飞书事件回调。
//
// 协议版本：V2（schema 2.0）。
// 端点说明：
//   - URL 验证：飞书后台首次订阅时发送 {"type":"url_verification",...}，必须回 {"challenge":...}
//   - 事件回调：{"schema":"2.0","header":{...},"event":{...}}，必须在 3s 内返回 {"code":0}，否则飞书会重试
//
// 签名验证：按飞书官方「签名校验」算法（请求地址配置文档）。
//
//	stringToSign = timestamp + nonce + encryptKey + body
//	X-Lark-Signature = hex(sha256(stringToSign))
//
// 密钥是「事件与回调 > 加密策略」里的 Encrypt Key，即 POCKET_FEISHU_ENCRYPT_KEY；
// 为兼容既有部署，未配置时回退到 POCKET_FEISHU_VERIFY_SECRET。
// 注意：既不是 HMAC 也不是 base64。
//
// dev 模式：若 Encrypt Key 与 Verify Secret 都留空则跳过签名校验（生产前必须配置）。
package feishu

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/config"
)

// PublicEntry 暴露给 server 调用的 handler 入口（避免循环引用）。
// broadcast 由 server 注入一个闭包，转发给 WebSocket Hub。
func PublicEntry(cfg config.Config, broadcast func(msgType string, payload interface{})) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 1) 仅接受 POST（飞书后台发送 url_verification 也是 POST）
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// 2) 读 raw body（验签需要原始字节）
		r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			log.Printf("[feishu] read body failed: %v", err)
			http.Error(w, "read body failed", http.StatusBadRequest)
			return
		}

		// 3) 解析通用 envelope（不分 schema 1.0/2.0，先看顶层 type）
		var env envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			log.Printf("[feishu] parse envelope failed: %v body=%q", err, string(raw))
			writeJSON(w, http.StatusBadRequest, map[string]any{"code": -1, "msg": "invalid json"})
			return
		}

		// 4) 分支 0: 加密事件 —— 配了 Encrypt Key 后飞书只发 {"encrypt":"<base64>"}。
		//    必须先验签（签名基于**原始 body**），再解密，最后才解析事件。
		if env.Encrypt != "" {
			key := signatureKey(cfg)
			if key == "" {
				log.Printf("[feishu] received encrypted event but no encrypt key configured; cannot decrypt")
				writeJSON(w, http.StatusUnauthorized, map[string]any{"code": -1, "msg": "encrypt key not configured"})
				return
			}
			timestamp := r.Header.Get("X-Lark-Request-Timestamp")
			nonce := r.Header.Get("X-Lark-Request-Nonce")
			signature := r.Header.Get("X-Lark-Signature")
			// 签名对原始 body 计算（不是对明文），故此处仍用 raw。
			if !verifySignature(timestamp, nonce, key, string(raw), signature) {
				log.Printf("[feishu] signature verification failed (encrypted event) ts=%s nonce=%s", timestamp, nonce)
				writeJSON(w, http.StatusUnauthorized, map[string]any{"code": -1, "msg": "signature invalid"})
				return
			}
			plain, err := decryptEvent(key, env.Encrypt)
			if err != nil {
				log.Printf("[feishu] decrypt event failed: %v", err)
				writeJSON(w, http.StatusBadRequest, map[string]any{"code": -1, "msg": "decrypt failed"})
				return
			}
			// 明文才是真正的事件体
			var inner eventEnvelope
			if err := json.Unmarshal(plain, &inner); err != nil {
				log.Printf("[feishu] parse decrypted event failed: %v plain=%q", err, string(plain))
				writeJSON(w, http.StatusOK, map[string]any{"code": 0, "msg": "ignored: decrypted payload not event v2"})
				return
			}
			dispatch(inner.Event.Type, inner.Event, broadcast)
			writeJSON(w, http.StatusOK, map[string]any{"code": 0, "msg": "ok"})
			return
		}

		// 5) 分支 1: URL 验证（飞书首次订阅时的 challenge）
		if env.Type == "url_verification" {
			handleURLVerification(w, cfg, env)
			return
		}

		// 5) 分支 2: 事件回调 —— 验签。
		// 官方算法用的是「加密策略」里的 Encrypt Key；未配置时回退到 Verify Secret，
		// 两者都为空才是 dev 模式（跳过验签）。
		if key := signatureKey(cfg); key != "" {
			timestamp := r.Header.Get("X-Lark-Request-Timestamp")
			nonce := r.Header.Get("X-Lark-Request-Nonce")
			signature := r.Header.Get("X-Lark-Signature")
			if !verifySignature(timestamp, nonce, key, string(raw), signature) {
				log.Printf("[feishu] signature verification failed ts=%s nonce=%s sig=%q", timestamp, nonce, signature)
				writeJSON(w, http.StatusUnauthorized, map[string]any{"code": -1, "msg": "signature invalid"})
				return
			}
		} else if cfg.IsProduction() {
			// production 一律 fail-closed。放行等于把回调端点变成任何人可伪造的公开入口：
			// 攻击者构造一条 im.message.receive_v1 就能把内容广播进所有已连接客户端的
			// 实时通道（2026-10-03 端到端实测确认）。
			// 与企业微信侧一致：config.go:72-73 写明「任一为空时一律 503 拒绝，而不是放行」。
			log.Printf("[feishu] ERROR: production 但 POCKET_FEISHU_ENCRYPT_KEY / POCKET_FEISHU_VERIFY_SECRET 皆为空，拒绝未验签事件（type=%s）", env.Type)
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"code": -1, "msg": "signature verification not configured"})
			return
		} else {
			log.Printf("[feishu] WARNING: POCKET_FEISHU_ENCRYPT_KEY and POCKET_FEISHU_VERIFY_SECRET are both unset; signature check SKIPPED (dev mode)")
		}

		// 6) 解析 event 字段
		var ev eventEnvelope
		if err := json.Unmarshal(raw, &ev); err != nil {
			log.Printf("[feishu] parse event envelope failed: %v", err)
			writeJSON(w, http.StatusOK, map[string]any{"code": 0, "msg": "ignored: not event v2"})
			return
		}

		// 7) 派发事件
		dispatch(ev.Event.Type, ev.Event, broadcast)

		// 8) 必须返回 {"code":0}，否则飞书会重试
		writeJSON(w, http.StatusOK, map[string]any{"code": 0, "msg": "ok"})
	}
}

// envelope 飞书回调顶层（覆盖 url_verification + event 两种）
type envelope struct {
	Type      string          `json:"type"`
	Token     string          `json:"token,omitempty"`
	Challenge string          `json:"challenge,omitempty"`
	Schema    string          `json:"schema,omitempty"`
	Header    json.RawMessage `json:"header,omitempty"`
	Event     json.RawMessage `json:"event,omitempty"`
	// Encrypt 加密事件体：配置 Encrypt Key 后事件以 AES-256-CBC 加密，
	// 整个回调退化为 {"encrypt":"<base64>"} 一个字段。
	Encrypt string `json:"encrypt,omitempty"`
}

// eventEnvelope V2 事件结构（仅取 type 字段做派发）
type eventEnvelope struct {
	Schema string          `json:"schema"`
	Header json.RawMessage `json:"header"`
	Event  struct {
		Type      string          `json:"type"`
		AppID     string          `json:"app_id"`
		TenantKey string          `json:"tenant_key"`
		Message   json.RawMessage `json:"message,omitempty"`
		Sender    json.RawMessage `json:"sender,omitempty"`
		File      json.RawMessage `json:"file,omitempty"`
		Document  json.RawMessage `json:"document,omitempty"`
		Wiki      json.RawMessage `json:"wiki,omitempty"`
	} `json:"event"`
}

func handleURLVerification(w http.ResponseWriter, cfg config.Config, env envelope) {
	// 若配置了 verify_token，强制匹配
	if cfg.FeishuVerifyToken != "" && env.Token != cfg.FeishuVerifyToken {
		// Avoid logging raw verify tokens; record the lengths and a short
		// prefix so operators can still distinguish cases without leaking
		// the secret to log aggregators.
		logTokenMismatch(len(env.Token), len(cfg.FeishuVerifyToken), env.Token, cfg.FeishuVerifyToken)
		writeJSON(w, http.StatusUnauthorized, map[string]any{"code": -1, "msg": "token mismatch"})
		return
	}
	log.Printf("[feishu] url_verification OK challenge_len=%d", len(env.Challenge))
	writeJSON(w, http.StatusOK, map[string]any{"challenge": env.Challenge})
}

// logTokenMismatch records a verify-token mismatch without leaking the
// raw token bytes. It is a tiny seam so a unit test can assert the
// redaction policy: the secret string must never appear verbatim in
// log output.
//
// gotPrefix and wantPrefix are passed in already-clipped to <=6 bytes by
// the caller; the helper prints the lengths and the prefixes only.
func logTokenMismatch(gotLen, wantLen int, got, want string) {
	log.Printf("[feishu] url_verification token mismatch: got_len=%d want_len=%d got_prefix=%.6q want_prefix=%.6q",
		gotLen, wantLen, got, want)
}

// verifySignature 飞书事件回调验签 + 时间戳新鲜度校验。
//
// 官方「签名校验」算法（请求地址配置 / Signature verification）：
//
//	stringToSign = timestamp + nonce + encryptKey + body
//	signature    = hex(sha256(stringToSign))     // 小写 hex
//
// 注意既不是 HMAC 也不是 base64 —— 密钥是**被哈希的明文前缀**，输出是 hex。
// timestamp 取自 X-Lark-Request-Timestamp，nonce 取自 X-Lark-Request-Nonce，
// encryptKey 为开发者后台「事件与回调 > 加密策略」中的 Encrypt Key。
// 官方参考（含 Python/Java/Golang/Node/PHP 五语言示例）：
// https://open.feishu.cn/document/uAjLw4CM/ukTMukTMukTM/event-subscription-guide/event-subscription-configure-/request-url-configuration-case
//
// 时间戳额外加 5 分钟窗口防重放（官方未强制，属本实现的加固）。
func verifySignature(timestamp, nonce, encryptKey, body, signature string) bool {
	if encryptKey == "" {
		return true // dev 模式（无 encrypt key 时跳过验签和时间戳校验）
	}
	if timestamp == "" || signature == "" {
		return false
	}

	// 时间戳新鲜度校验（防止重放）
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false // 时间戳格式错误
	}
	now := time.Now().Unix()
	if abs(now-ts) > 5*60 { // 5 分钟窗口
		return false // 时间戳过期或来自未来
	}

	// 官方签名：sha256(timestamp + nonce + encryptKey + body) -> 小写 hex
	sum := sha256.Sum256([]byte(timestamp + nonce + encryptKey + body))
	expected := hex.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(expected), []byte(signature)) == 1
}

// signatureKey 返回用于事件回调验签的密钥。
// 官方「签名校验」用的是「事件与回调 > 加密策略」中的 Encrypt Key；
// FeishuVerifySecret 是本仓历史字段，保留为回退以兼容既有部署。
// 两者都为空 = dev 模式，跳过验签。
func signatureKey(cfg config.Config) string {
	if cfg.FeishuEncryptKey != "" {
		return cfg.FeishuEncryptKey
	}
	return cfg.FeishuVerifySecret
}

// decryptEvent 按飞书官方「事件解密」算法解密加密事件体。
//
// 官方算法（请求地址配置 / 事件解密）：
//
//	key  = sha256(encrypt_key)                    // 32 字节
//	raw  = base64decode(encrypt)
//	iv   = raw[:16]                               // 前 16 字节即 IV
//	body = AES-256-CBC-decrypt(key, iv, raw[16:])
//	body = PKCS#7-unpad(body)
//
// 官方参考（含 Python/Java/Golang/Node/PHP 五语言示例）：
// https://open.feishu.cn/document/uAjLw4CM/ukTMukTMukTM/event-subscription-guide/event-subscription-configure-/request-url-configuration-case
//
// 注意 IV **内嵌在密文头部**（前 16 字节），不是配置项。
func decryptEvent(encryptKey, encrypted string) ([]byte, error) {
	if encryptKey == "" {
		return nil, errors.New("feishu: encrypt key not configured")
	}
	raw, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return nil, fmt.Errorf("feishu: decode encrypt field: %w", err)
	}
	// 需要 IV(16) + 至少一个密文块
	if len(raw) <= aes.BlockSize {
		return nil, fmt.Errorf("feishu: encrypted payload too short: %d bytes", len(raw))
	}
	key := sha256.Sum256([]byte(encryptKey))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("feishu: new cipher: %w", err)
	}
	iv := raw[:aes.BlockSize]
	ciphertext := raw[aes.BlockSize:]
	if len(ciphertext)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("feishu: ciphertext not a multiple of block size: %d", len(ciphertext))
	}
	plain := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, ciphertext)
	return unpadPKCS7(plain, aes.BlockSize)
}

// unpadPKCS7 去除 PKCS#7 填充。blockSize 用于校验填充字节的合法范围，
// 避免畸形输入被静默截断。
func unpadPKCS7(b []byte, blockSize int) ([]byte, error) {
	if len(b) == 0 {
		return nil, errors.New("feishu: empty plaintext after decrypt")
	}
	pad := int(b[len(b)-1])
	if pad == 0 || pad > blockSize || pad > len(b) {
		return nil, fmt.Errorf("feishu: invalid PKCS#7 padding byte %d", pad)
	}
	for _, c := range b[len(b)-pad:] {
		if int(c) != pad {
			return nil, errors.New("feishu: inconsistent PKCS#7 padding")
		}
	}
	return b[:len(b)-pad], nil
}

// abs 返回绝对值
func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

// dispatch 根据 event.type 派发到具体处理函数
func dispatch(eventType string, ev struct {
	Type      string          `json:"type"`
	AppID     string          `json:"app_id"`
	TenantKey string          `json:"tenant_key"`
	Message   json.RawMessage `json:"message,omitempty"`
	Sender    json.RawMessage `json:"sender,omitempty"`
	File      json.RawMessage `json:"file,omitempty"`
	Document  json.RawMessage `json:"document,omitempty"`
	Wiki      json.RawMessage `json:"wiki,omitempty"`
}, broadcast func(msgType string, payload interface{})) {
	// 记录全部事件（便于调试 & 审计）
	payloadBytes, _ := json.Marshal(ev)
	log.Printf("[feishu] event=%s app=%s tenant=%s payload=%s", eventType, ev.AppID, ev.TenantKey, string(payloadBytes))

	switch eventType {
	// 消息类
	case "im.message.receive_v1":
		handleMessageEvent(ev, broadcast, false)
	case "im.message.message_read_v1":
		handleMessageEvent(ev, broadcast, true)
	// 文档类（云文档 / Docx）
	case "docx.document.created_v1",
		"docx.document.edited_v1",
		"docx.document.deleted_v1",
		"drive.file.created_v1",
		"drive.file.edited_v1",
		"drive.file.title_updated_v1",
		"wiki.space.created_v1",
		"wiki.space.edited_v1",
		"wiki.node.created_v1",
		"wiki.node.edited_v1":
		handleDocEvent(ev, broadcast, eventType)
	default:
		log.Printf("[feishu] unhandled event type=%s, acked with code:0", eventType)
		// 飞书要求对所有事件都返回成功，否则会持续重试
	}
}

// handleMessageEvent 消息事件：解析 chat_id / message_id / sender_id
func handleMessageEvent(ev struct {
	Type      string          `json:"type"`
	AppID     string          `json:"app_id"`
	TenantKey string          `json:"tenant_key"`
	Message   json.RawMessage `json:"message,omitempty"`
	Sender    json.RawMessage `json:"sender,omitempty"`
	File      json.RawMessage `json:"file,omitempty"`
	Document  json.RawMessage `json:"document,omitempty"`
	Wiki      json.RawMessage `json:"wiki,omitempty"`
}, broadcast func(msgType string, payload interface{}), isRead bool) {
	// MVP: 解析关键字段，记日志 + 推 WebSocket
	var msg struct {
		ChatID      string `json:"chat_id"`
		ChatType    string `json:"chat_type"`
		MessageID   string `json:"message_id"`
		MessageType string `json:"message_type"`
		Content     string `json:"content"`
		CreateTime  string `json:"create_time"`
	}
	_ = json.Unmarshal(ev.Message, &msg)

	var sender struct {
		SenderID   string `json:"sender_id"`
		SenderType string `json:"sender_type"`
		TenantKey  string `json:"tenant_key"`
	}
	_ = json.Unmarshal(ev.Sender, &sender)

	action := "received"
	if isRead {
		action = "read"
	}
	log.Printf("[feishu] message %s: chat=%s type=%s msg=%s sender=%s", action, msg.ChatID, msg.MessageType, msg.MessageID, sender.SenderID)

	// 推 WebSocket 给前端（MVP 转发原始事件，由前端解析展示）
	if broadcast != nil {
		broadcast("feishu.message", map[string]any{
			"action":  action,
			"chat_id": msg.ChatID,
			"message": msg,
			"sender":  sender,
		})
	}
}

// handleDocEvent 文档/多维表事件
func handleDocEvent(ev struct {
	Type      string          `json:"type"`
	AppID     string          `json:"app_id"`
	TenantKey string          `json:"tenant_key"`
	Message   json.RawMessage `json:"message,omitempty"`
	Sender    json.RawMessage `json:"sender,omitempty"`
	File      json.RawMessage `json:"file,omitempty"`
	Document  json.RawMessage `json:"document,omitempty"`
	Wiki      json.RawMessage `json:"wiki,omitempty"`
}, broadcast func(msgType string, payload interface{}), eventType string) {
	// MVP: 提取文件/文档 token + name
	var file struct {
		FileToken  string   `json:"file_token"`
		FileName   string   `json:"file_name"`
		FileType   string   `json:"file_type"`
		ActionList []string `json:"action_list"`
	}
	_ = json.Unmarshal(ev.File, &file)

	var doc struct {
		DocID   string `json:"doc_id"`
		DocType string `json:"doc_type"`
		Title   string `json:"title"`
	}
	_ = json.Unmarshal(ev.Document, &doc)

	log.Printf("[feishu] doc event=%s file=%s name=%s doc=%s", eventType, file.FileToken, file.FileName, doc.DocID)

	if broadcast != nil {
		broadcast("feishu.doc", map[string]any{
			"event_type": eventType,
			"file":       file,
			"document":   doc,
		})
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
