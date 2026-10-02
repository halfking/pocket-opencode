package wecom

import (
	"encoding/xml"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/halfking/pocket-opencode/backend/internal/config"
)

// PublicEntry 暴露给 server 调用的 handler 入口（避免 server 反向引用本包）。
//
// broadcast 用于把事件推给 WebSocket Hub，签名 func(string, any)；
// 与 feishu.PublicEntry 保持同一形状，便于两个回调用同一套接线方式。
func PublicEntry(cfg config.Config, broadcast func(msgType string, payload any)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handleVerify(w, r, cfg)
		case http.MethodPost:
			handleEvent(w, r, cfg, broadcast)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

// cryptoConfigured 报告加解密所需的三项是否齐全。
//
// 缺任何一项都不能做真实验签/解密——此时**必须拒绝**而不是放行。
// 反过来（什么都没配就跳过验签）等于把一个公开可写的回调端点送出去，
// 任何人都能伪造事件。
func cryptoConfigured(cfg config.Config) bool {
	return cfg.WeComToken != "" && cfg.WeComEncodingAESKey != "" && cfg.WeComCorpID != ""
}

// handleVerify 处理企业微信的 URL 验证（管理端保存回调配置时发起的 GET）。
//
// 两种模式：
//   - 明文模式：query 只有 echostr，原样返回。
//   - 安全模式：query 有 msg_signature + timestamp + nonce + echostr，
//     验签 → 解密 → **原样返回明文**。
//
// 返回明文时的字节要求（官方明确，且踩了会「明明验签过了却保存失败」）：
// 不加引号、不带 BOM、**不带结尾换行**。所以这里必须用 w.Write 写裸字节，
// 不能走 json.Encoder（会自动加 \n 并转义）也不能走 http.Error。
func handleVerify(w http.ResponseWriter, r *http.Request, cfg config.Config) {
	q := r.URL.Query()
	echostr := q.Get("echostr")
	if echostr == "" {
		http.Error(w, "missing echostr", http.StatusBadRequest)
		return
	}

	// 明文模式：只有 echostr，没有签名参数。
	msgSig := q.Get("msg_signature")
	if msgSig == "" {
		// 明文模式下原样返回是协议规定的行为，不算「跳过校验」。
		writePlain(w, echostr)
		return
	}

	if !cryptoConfigured(cfg) {
		log.Printf("[wecom] 收到签名请求但 token/EncodingAESKey/CorpID 未配置，拒绝")
		http.Error(w, "wecom crypto not configured", http.StatusServiceUnavailable)
		return
	}

	timestamp := q.Get("timestamp")
	nonce := q.Get("nonce")
	if err := VerifySignature(cfg.WeComToken, timestamp, nonce, echostr, msgSig, true); err != nil {
		log.Printf("[wecom] url 验证签名不通过 ts=%s nonce=%s: %v", timestamp, nonce, err)
		// 返回 403 且**不**回显期望值，避免把签名算法当成预言机。
		http.Error(w, "signature invalid", http.StatusForbidden)
		return
	}

	plain, err := Decrypt(cfg.WeComEncodingAESKey, echostr, cfg.WeComCorpID)
	if err != nil {
		log.Printf("[wecom] url 验证 echostr 解密失败: %v", err)
		http.Error(w, "decrypt failed", http.StatusBadRequest)
		return
	}
	writePlain(w, plain)
}

// writePlain 原样写出明文，Content-Type 固定 text/plain。
func writePlain(w http.ResponseWriter, s string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write([]byte(s))
}

// encryptedEnvelope 是 POST body 的外层信封。
//
// 加密模式下企业微信发来的就是这么一个只有 Encrypt 节点的极简 XML；
// 业务内容全在密文里。
type encryptedEnvelope struct {
	XMLName xml.Name `xml:"xml"`
	Encrypt string   `xml:"Encrypt"`
}

// callbackEvent 是解密后 msg 段里我们关心的字段。
//
// 企业微信的消息/事件明文也是 XML，形状随 MsgType/InfoType 变化。
// 这里只解析公共部分，未知字段忽略——不要因为出现了没列出的字段就整条丢弃。
type callbackEvent struct {
	XMLName      xml.Name `xml:"xml"`
	ToUserName   string   `xml:"ToUserName"`
	FromUserName string   `xml:"FromUserName"`
	CreateTime   int64    `xml:"CreateTime"`
	MsgType      string   `xml:"MsgType"`
	AgentID      string   `xml:"AgentID"`
	Event        string   `xml:"Event"`
	EventKey     string   `xml:"EventKey"`
	Content      string   `xml:"Content"`
}

// handleEvent 处理验证通过后的事件推送（POST）。
//
// 响应契约：**5 秒内**返回字符串 "success"（或空串）。企业微信对格式错误或
// 超时会判定失败并重试最多 3 次——所以这里宁可回 success 也不要在业务处理里阻塞。
// 本实现的业务动作只有广播，几乎不会失败。
func handleEvent(w http.ResponseWriter, r *http.Request, cfg config.Config, broadcast func(string, any)) {
	// 上限 2MB。企业微信推送体很小，设限是为了防 body 无限增长。
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("[wecom] 读 body 失败: %v", err)
		writeSuccess(w, http.StatusBadRequest)
		return
	}

	q := r.URL.Query()
	encryptB64 := extractEncrypt(raw)
	if encryptB64 == "" {
		log.Printf("[wecom] POST body 里没有 <Encrypt> 节点: %q", truncate(string(raw), 200))
		writeSuccess(w, http.StatusBadRequest)
		return
	}

	if !cryptoConfigured(cfg) {
		log.Printf("[wecom] 收到加密事件但 token/EncodingAESKey/CorpID 未配置，拒绝")
		writeSuccess(w, http.StatusServiceUnavailable)
		return
	}

	timestamp := q.Get("timestamp")
	nonce := q.Get("nonce")
	msgSig := q.Get("msg_signature")
	if err := VerifySignature(cfg.WeComToken, timestamp, nonce, encryptB64, msgSig, true); err != nil {
		log.Printf("[wecom] 事件签名不通过 ts=%s nonce=%s: %v", timestamp, nonce, err)
		writeSuccess(w, http.StatusForbidden)
		return
	}

	plain, err := Decrypt(cfg.WeComEncodingAESKey, encryptB64, cfg.WeComCorpID)
	if err != nil {
		log.Printf("[wecom] 事件解密失败: %v", err)
		writeSuccess(w, http.StatusBadRequest)
		return
	}

	var ev callbackEvent
	if err := xml.Unmarshal([]byte(plain), &ev); err != nil {
		// 明文不是 XML 不代表是攻击，也可能是企业微信发了新形状。
		// 记日志 + 回 success：不阻塞对方的重试。
		log.Printf("[wecom] 明文不是可解析的 XML（仍回 success）: %v body=%q", err, truncate(plain, 200))
		writeSuccess(w, http.StatusOK)
		return
	}

	log.Printf("[wecom] 事件 ToUser=%s FromUser=%s Agent=%s MsgType=%s Event=%s Key=%s",
		ev.ToUserName, ev.FromUserName, ev.AgentID, ev.MsgType, ev.Event, ev.EventKey)

	if broadcast != nil {
		// 广播不能拖慢响应：企业微信 5 秒超时，Hub 卡住会直接导致重试风暴。
		go broadcast("wecom.event", ev)
	}
	writeSuccess(w, http.StatusOK)
}

// extractEncrypt 从 POST body 里取出 <Encrypt> 节点内容。
//
// 先按 XML 解析；解析失败再退回大小写不敏感的正则兜底。
// 兜底不是为了「容错」，是因为官方文档里明确提过有接入方遇到过
// 「解密抛 IllegalBlockSizeException / 密文被框架层处理过」的坑——
// 标签顺序或 CDATA 形态变化都可能导致严格解析失败。
func extractEncrypt(raw []byte) string {
	var env encryptedEnvelope
	if err := xml.Unmarshal(raw, &env); err == nil && strings.TrimSpace(env.Encrypt) != "" {
		return strings.TrimSpace(env.Encrypt)
	}
	return regexExtractEncrypt(string(raw))
}

func writeSuccess(w http.ResponseWriter, code int) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.Write([]byte("success"))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
