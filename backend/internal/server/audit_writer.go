package server

import (
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/redclaw"
)

// Audit 写入侧规约（P3 多租户治理）：
//
//   - 任何调用方都不应自行构造 redclaw.AuditEntry；统一通过 Write /
//     WriteWithClaims 写入，便于在集中位置做脱敏与长度截断。
//   - Detail 字段在写入前由 redactDetail 处理：识别常见敏感键名并把值
//     替换为 auditRedactedValue，避免 API key / OAuth token / SMTP
//     密码 / vault blob 误进入审计；并截断到 maxAuditDetailBytes 防止
//     写入超长 detail（LLM gateway body 仍由 caller 截断到 512B）。
//   - TenantID 始终取自 auth.Claims.WorkspaceID（system tenant 走常量
//     auditSystemTenant）；不接受客户端参数。
const (
	maxAuditDetailBytes      = 1024
	auditSystemTenant        = "system:acc"
	auditRedactedValue       = "[REDACTED]"
	auditDetailTruncatedTail = "...[truncated]"
)

// auditSensitiveKeys 是 detail 字符串里需要做值掩码的键名（小写、不含
// 冒号/等号）。匹配时按大小写不敏感；匹配范围包含 JSON 风格 "key":"v"
// 与 query/form 风格 key=v。
var auditSensitiveKeys = []string{
	"password",
	"passwd",
	"secret",
	"api_key",
	"apikey",
	"token",
	"access_token",
	"refresh_token",
	"authorization",
	"cookie",
	"session_id",
	"smtp_password",
	"client_secret",
	"private_key",
}

// AuditFields 是 audit 写入 helper 的可选字段。Success 的零值是 false；调用方
// 必须显式传入 Success: true 表示成功。Detail 不传则为空；Timestamp / IP 由 helper 自动
// 填充（Timestamp 非零时保留调用方提供的事件时间）。
//
// TenantID 为非空时强制覆盖（用于 system:acc 等 system 级事件）；空值
// 时默认取 auth.Claims.WorkspaceID。
type AuditFields struct {
	Detail    string
	Success   bool
	Timestamp time.Time
	Duration  time.Duration
	TenantID  string
}

// Write 是 server 内 HTTP handler 调用的 audit 写入入口。
//
// action / resource 由调用方按业务命名（如 "vault.sync.upload"、
// "mobile.approval.permission_once"）；TenantID 与 UserID 由 helper 从
// requireAuth 注入的 authClaims 派生；IP 由 helper 自动从
// X-Forwarded-For / RemoteAddr 取得。详情字段 Detail 会经过 redact +
// 长度截断。
func (s *Server) Write(r *http.Request, action, resource string, fields AuditFields) {
	if r == nil {
		return
	}
	s.writeAuditEntry(action, resource, fields, s.claimsFromContext(r), auditClientIP(r))
}

// WriteWithClaims 是非 HTTP 入口（scheduler、后台任务、worker）调用的
// audit 写入入口。claims 可为 nil（system tenant 场景）。
func (s *Server) WriteWithClaims(claims *authClaims, action, resource string, fields AuditFields) {
	s.writeAuditEntry(action, resource, fields, claims, "")
}

// writeAuditEntry 是核心实现。所有 writer 都收敛到这一处，确保 redact
// / 长度截断 / nil-store 守卫只在一处维护。
func (s *Server) writeAuditEntry(action, resource string, fields AuditFields, claims *authClaims, ip string) {
	if s.auditStore == nil {
		return
	}
	if action == "" {
		// 没有 action 的审计事件没有检索意义；忽略而不是写空 action。
		return
	}

	userID := ""
	if claims != nil {
		userID = claims.UserID
	}
	tenantID := ""
	switch {
	case fields.TenantID != "":
		// caller 显式声明 tenant（如 system 任务），优先级最高。
		tenantID = fields.TenantID
	case claims != nil:
		tenantID = claims.WorkspaceID
	}

	detail := redactDetail(fields.Detail)
	if len(detail) > maxAuditDetailBytes {
		tail := auditDetailTruncatedTail
		if maxAuditDetailBytes <= len(tail) {
			detail = tail[:maxAuditDetailBytes]
		} else {
			detail = detail[:maxAuditDetailBytes-len(tail)] + tail
		}
	}

	entry := &redclaw.AuditEntry{
		Action:     action,
		UserID:     userID,
		TenantID:   tenantID,
		Resource:   resource,
		Detail:     detail,
		Success:    fields.Success,
		DurationMs: fields.Duration.Milliseconds(),
		IP:         ip,
		Timestamp:  fields.Timestamp,
	}
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}

	if err := s.auditStore.Record(entry); err != nil {
		log.Printf("[audit] record %s failed: %v", action, err)
	}
}

// redactDetail 把字符串里以常见敏感键名开头的键值替换为占位符。
//
// 支持的样式（大小写不敏感）：
//
//   - JSON 风格  "key":"value"
//   - query/form 风格  key=value / key = value（边界由空白/&/,/}/]
//     /"/' 终止）
//
// 不解析完整 JSON/URL 以保持低开销；只对已知键名做值替换，未知键名不
// 动作。已替换为 auditRedactedValue 的内容再次扫描也不会出错。
func redactDetail(s string) string {
	if s == "" {
		return s
	}
	for _, key := range auditSensitiveKeys {
		s = redactOne(s, strings.ToLower(key))
	}
	return s
}

// redactOne 在 src 上扫描，把 key 后面的**所有**值替换为
// auditRedactedValue。返回新字符串。
//
// 同一键多次出现时全部掩码——只替换第一处会让
// {"a":1,"password":"x","b":{"password":"y"}} 里的 y 幸存。
func redactOne(src, key string) string {
	if key == "" {
		return src
	}

	// 1) JSON 风格 "key":"value"——循环替换直到无匹配。
	jsonPrefix := `"` + key + `":`
	for {
		lower := strings.ToLower(src)
		replaced := false
		for i := 0; i+len(jsonPrefix) <= len(lower); i++ {
			if lower[i:i+len(jsonPrefix)] != jsonPrefix {
				continue
			}
			j := i + len(jsonPrefix)
			if j >= len(src) || src[j] != '"' {
				break
			}
			j++ // 跳过开引号
			end := j
			for end < len(src) && src[end] != '"' {
				end++
			}
			if end >= len(src) {
				return src
			}
			// 值已是占位符则跳过：否则重扫会把 [REDACTED] 当作新值
			// 无限替换自身，循环永不终止。用 HasPrefix 而非全等——
			// query 风格的值终止符包含 ']'，会把占位符截成 "[REDACTED"。
			if strings.HasPrefix(src[j:], auditRedactedValue) {
				continue
			}
			src = src[:j] + auditRedactedValue + src[end:]
			replaced = true
			break
		}
		if !replaced {
			break
		}
	}

	// 2) query/form 风格 key=value——同样循环到无匹配。
	for {
		lower := strings.ToLower(src)
		replaced := false
		for i := 0; i+len(key)+1 <= len(lower); i++ {
			if lower[i:i+len(key)] != key || lower[i+len(key)] != '=' {
				continue
			}
			if i > 0 {
				prev := lower[i-1]
				if !isTokenBoundary(prev) {
					continue
				}
			}
			j := i + len(key) + 1
			for j < len(src) && (src[j] == ' ' || src[j] == '\t') {
				j++
			}
			start := j
			for j < len(src) && !isValueTerminator(src[j]) {
				j++
			}
			if j == start {
				continue
			}
			// 同上：已是占位符的值不再触发替换，避免死循环（HasPrefix
			// 兼容 ']' 截断的情况）。
			if strings.HasPrefix(src[start:], auditRedactedValue) {
				continue
			}
			src = src[:start] + auditRedactedValue + src[j:]
			replaced = true
			break
		}
		if !replaced {
			break
		}
	}

	return src
}

func isTokenBoundary(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', ',', '&', '"', '\'', '{', '[', '(':
		return true
	}
	return false
}

func isValueTerminator(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', '&', ',', '"', '\'', '}', ']':
		return true
	}
	return false
}

// AuditSystemTenant 返回 system 事件所用的固定 tenant id，供其它包
// （email / tasksync）写入 system 级审计时复用，避免重复字面量。
func AuditSystemTenant() string { return auditSystemTenant }

// claimsUserFromContextOrEmpty 是 handler 没经过 requireAuth（如
// /api/llm-gateway/config 直挂 mux）但仍想从 context 拿 user 的兜底：
// claims 为 nil 时返空字符串，避免 audit 写入 panic。
func claimsUserFromContextOrEmpty(r *http.Request) string {
	if r == nil {
		return ""
	}
	if c := sClaimsFromRequest(r); c != nil {
		return c.UserID
	}
	return ""
}

// baseURLHostOnly 从 URL 字符串里只提取 scheme + host，去掉 path /
// query，避免 baseURL 中的潜在敏感串进入 detail。
func baseURLHostOnly(raw string) string {
	if raw == "" {
		return ""
	}
	idx := strings.Index(raw, "://")
	if idx < 0 {
		return raw
	}
	rest := raw[idx+3:]
	if j := strings.IndexAny(rest, "/?#"); j >= 0 {
		return raw[:idx+3] + rest[:j]
	}
	return raw
}

// auditClientIP 取客户端 IP；优先 X-Forwarded-For，与
// llm_gateway_nodes_handler.go 的 clientIPFromRequest 保持一致行为。
// 这里独立实现而不是直接复用，是为了避免 audit_writer.go 依赖 gateway
// 包（保持 helper 自包含），并便于将来迁出。
func auditClientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if idx := strings.IndexByte(xff, ','); idx > 0 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	host := r.RemoteAddr
	if idx := strings.LastIndexByte(host, ':'); idx > 0 {
		host = host[:idx]
	}
	return host
}
