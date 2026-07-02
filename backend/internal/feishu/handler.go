// Package feishu 处理 m.kxpms.cn/callback/feishu 飞书事件回调。
//
// 协议版本：V2（schema 2.0）。
// 端点说明：
//   - URL 验证：飞书后台首次订阅时发送 {"type":"url_verification",...}，必须回 {"challenge":...}
//   - 事件回调：{"schema":"2.0","header":{...},"event":{...}}，必须在 3s 内返回 {"code":0}，否则飞书会重试
//
// 签名验证：V2 使用 HMAC-SHA256（消息体不加密）。
//   X-Lark-Signature = base64(hmac_sha256(timestamp + nonce + secret, body))
//   其中 timestamp 来自 X-Lark-Request-Timestamp header，nonce 来自 X-Lark-Request-Nonce。
//
// dev 模式：若 POCKET_FEISHU_VERIFY_SECRET 留空则跳过签名校验（生产前必须配置）。
package feishu

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
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

		// 4) 分支 1: URL 验证（飞书首次订阅时的 challenge）
		if env.Type == "url_verification" {
			handleURLVerification(w, cfg, env)
			return
		}

		// 5) 分支 2: 事件回调 —— 验签（仅当配置了 verify_secret 才验）
		if cfg.FeishuVerifySecret != "" {
			timestamp := r.Header.Get("X-Lark-Request-Timestamp")
			nonce := r.Header.Get("X-Lark-Request-Nonce")
			signature := r.Header.Get("X-Lark-Signature")
			if !verifySignature(timestamp, nonce, cfg.FeishuVerifySecret, string(raw), signature) {
				log.Printf("[feishu] signature verification failed ts=%s nonce=%s sig=%q", timestamp, nonce, signature)
				writeJSON(w, http.StatusUnauthorized, map[string]any{"code": -1, "msg": "signature invalid"})
				return
			}
		} else {
			log.Printf("[feishu] WARNING: POCKET_FEISHU_VERIFY_SECRET empty, signature check SKIPPED (dev mode)")
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
}

// eventEnvelope V2 事件结构（仅取 type 字段做派发）
type eventEnvelope struct {
	Schema string          `json:"schema"`
	Header json.RawMessage `json:"header"`
	Event  struct {
		Type     string          `json:"type"`
		AppID    string          `json:"app_id"`
		TenantKey string          `json:"tenant_key"`
		Message  json.RawMessage `json:"message,omitempty"`
		Sender   json.RawMessage `json:"sender,omitempty"`
		File     json.RawMessage `json:"file,omitempty"`
		Document json.RawMessage `json:"document,omitempty"`
		Wiki     json.RawMessage `json:"wiki,omitempty"`
	} `json:"event"`
}

func handleURLVerification(w http.ResponseWriter, cfg config.Config, env envelope) {
	// 若配置了 verify_token，强制匹配
	if cfg.FeishuVerifyToken != "" && env.Token != cfg.FeishuVerifyToken {
		log.Printf("[feishu] url_verification token mismatch: got=%q want=%q", env.Token, cfg.FeishuVerifyToken)
		writeJSON(w, http.StatusUnauthorized, map[string]any{"code": -1, "msg": "token mismatch"})
		return
	}
	log.Printf("[feishu] url_verification OK challenge=%q", env.Challenge)
	writeJSON(w, http.StatusOK, map[string]any{"challenge": env.Challenge})
}

// verifySignature 飞书 V2 HMAC-SHA256 验签 + 时间戳新鲜度校验
// 签名公式：hmac_sha256(timestamp + nonce + secret, body) → base64
// 时间戳校验：防止重放攻击，要求 timestamp 在 5 分钟内
func verifySignature(timestamp, nonce, secret, body, signature string) bool {
	if secret == "" {
		return true // dev 模式（无 secret 时跳过验签和时间戳校验）
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

	// HMAC 签名验证
	stringToSign := timestamp + nonce + secret
	h := hmac.New(sha256.New, []byte(stringToSign))
	h.Write([]byte(body))
	expected := base64.StdEncoding.EncodeToString(h.Sum(nil))
	return subtle.ConstantTimeCompare([]byte(expected), []byte(signature)) == 1
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
	Type     string          `json:"type"`
	AppID    string          `json:"app_id"`
	TenantKey string          `json:"tenant_key"`
	Message  json.RawMessage `json:"message,omitempty"`
	Sender   json.RawMessage `json:"sender,omitempty"`
	File     json.RawMessage `json:"file,omitempty"`
	Document json.RawMessage `json:"document,omitempty"`
	Wiki     json.RawMessage `json:"wiki,omitempty"`
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
	Type     string          `json:"type"`
	AppID    string          `json:"app_id"`
	TenantKey string          `json:"tenant_key"`
	Message  json.RawMessage `json:"message,omitempty"`
	Sender   json.RawMessage `json:"sender,omitempty"`
	File     json.RawMessage `json:"file,omitempty"`
	Document json.RawMessage `json:"document,omitempty"`
	Wiki     json.RawMessage `json:"wiki,omitempty"`
}, broadcast func(msgType string, payload interface{}), isRead bool) {
	// MVP: 解析关键字段，记日志 + 推 WebSocket
	var msg struct {
		ChatID       string `json:"chat_id"`
		ChatType     string `json:"chat_type"`
		MessageID    string `json:"message_id"`
		MessageType  string `json:"message_type"`
		Content      string `json:"content"`
		CreateTime   string `json:"create_time"`
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
	Type     string          `json:"type"`
	AppID    string          `json:"app_id"`
	TenantKey string          `json:"tenant_key"`
	Message  json.RawMessage `json:"message,omitempty"`
	Sender   json.RawMessage `json:"sender,omitempty"`
	File     json.RawMessage `json:"file,omitempty"`
	Document json.RawMessage `json:"document,omitempty"`
	Wiki     json.RawMessage `json:"wiki,omitempty"`
}, broadcast func(msgType string, payload interface{}), eventType string) {
	// MVP: 提取文件/文档 token + name
	var file struct {
		FileToken   string `json:"file_token"`
		FileName    string `json:"file_name"`
		FileType    string `json:"file_type"`
		ActionList  []string `json:"action_list"`
	}
	_ = json.Unmarshal(ev.File, &file)

	var doc struct {
		DocID  string `json:"doc_id"`
		DocType string `json:"doc_type"`
		Title  string `json:"title"`
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
