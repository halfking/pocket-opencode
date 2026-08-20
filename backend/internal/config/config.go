package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// Config holds all application configuration loaded from environment variables.
// It supports multiple deployment phases including personal assistant features,
// AI gateway integration, email processing, and enterprise backend connectivity.
type Config struct {
	Environment              string
	HTTPPort                 string
	DBPath                   string // 保留兼容；Postgres 迁移后仅用于 data 目录定位
	PostgresDSN              string // Phase 0: pocket 后端统一数据层
	PostgresSchema           string // pocket 私有的 PG schema 名（隔离共享 PG 上的其他模块表）
	NPSBaseURL               string
	NPSAuthKey               string
	NPSAuthCryptKey          string
	OpenCodeTimeoutMS        string
	WSHeartbeatMS            string
	ReminderCheckIntervalSec string
	AndroidAppID             string
	UseAndroidShell          string
	OpenCodeInstancesJSON    string
	// 飞书事件回调（m.kxpms.cn/callback/feishu）
	FeishuAppID        string
	FeishuAppSecret    string
	FeishuVerifyToken  string // url_verification.token 匹配（可选）
	FeishuVerifySecret string // X-Lark-Signature 验签密钥（留空 = dev 模式跳过）
	FeishuEncryptKey   string // V1 加密事件解密用（V2 不加密，留空即可）

	// ---- Phase 0: 个人助理模块新增配置 ----
	// AI/STT 后端
	GroqAPIKey                string // POCKET_GROQ_API_KEY：云端 Whisper Large v3 Turbo 兜底
	KxMemoryBaseURL           string // POCKET_KXMEMORY_BASE_URL：kxmemory FastAPI（笔记/分类/SSOT/总结）
	KxMemoryNoteClassifyPath  string // POCKET_KXMEMORY_NOTE_CLASSIFY_PATH（默认 /v1/notes/classify）
	KxMemoryEmailClassifyPath string // POCKET_KXMEMORY_EMAIL_CLASSIFY_PATH（默认 /v1/emails/classify）
	KxMemoryDailySummaryPath  string // POCKET_KXMEMORY_DAILY_SUMMARY_PATH（默认 /v1/emails/daily-summary）
	// 邮箱模块
	EmailMasterKey string // POCKET_EMAIL_MASTER_KEY：AES-GCM 加密 IMAP 凭证
	// 任务系统整合（Phase 5）
	MCPBaseURL     string // POCKET_MCP_BASE_URL：ACC 系统 MCP 端点（mcp.kxpms.cn/acc/mcp）
	MCPAPIKey      string // POCKET_MCP_API_KEY：ACC MCP 的 HMAC 密钥（ACC 的 AUTH_TOKEN_SECRET）
	MCPInsecureTLS bool   // POCKET_MCP_INSECURE_TLS：跳过 TLS 验证（仅 dev/自签证书，生产必须 false）
	// ACC MCP 写操作鉴权（HMAC 内部 JWT，见 internal/mcp/client.go）：
	// pocketd 以内部 JWT（HS256）调用 acc_create_task / acc_task_claim /
	// acc_task_complete / acc_report_session，claims 需含非空 tenant_id 与
	// scopes（默认 tasks,sessions）。
	MCPTenantID    string // POCKET_MCP_TENANT_ID：ACC JWT 的 tenant_id claim（非空）
	MCPScopeString string // POCKET_MCP_SCOPES：逗号分隔，默认 "tasks,sessions"
	// 认证（Phase 0）
	JWTSecret   string // POCKET_JWT_SECRET：签发/校验 app JWT
	DevAuth     bool   // POCKET_DEV_AUTH：允许 admin/admin 开发登录（生产必须不设或 false）
	DevAuthUser string // POCKET_AUTH_USER：首用户 bootstrap 用户名（缺省 admin）
	DevAuthPass string // POCKET_AUTH_PASS：首用户 bootstrap 密码（缺省 admin；仅 POCKET_DEV_AUTH=true 时生效）

	// 邮箱 OAuth + IMAP fetch
	EmailGoogleClientID        string // POCKET_EMAIL_GOOGLE_CLIENT_ID
	EmailGoogleClientSecret    string // POCKET_EMAIL_GOOGLE_CLIENT_SECRET
	EmailMicrosoftClientID     string // POCKET_EMAIL_MICROSOFT_CLIENT_ID
	EmailMicrosoftClientSecret string // POCKET_EMAIL_MICROSOFT_CLIENT_SECRET
	EmailOAuthRedirectURL      string // POCKET_EMAIL_OAUTH_REDIRECT_URL（默认 http://localhost:8088/callback/email/oauth）
	EmailFetchEnabled          bool   // POCKET_EMAIL_FETCH_ENABLED（默认 true；CI/dev 可关闭）
	TimezoneOffsetSec          int    // POCKET_TIMEZONE_OFFSET_SEC：用户时区偏移秒（默认 28800 = UTC+8）

	// ---- Phase C: 龙虾无状态 AI 网关 ----
	// pocketd 作为无状态代理：只转发嵌入/LLM 请求，不存任何用户数据。
	// 客户端（龙虾）只发必要文本片段，pocketd 转发给 AI 提供商。
	EmbedBaseURL string // POCKET_EMBED_BASE_URL：嵌入 API 地址（默认 OpenAI）
	EmbedAPIKey  string // POCKET_EMBED_API_KEY：嵌入 API 密钥
	EmbedModel   string // POCKET_EMBED_MODEL：嵌入模型（默认 text-embedding-3-small）
	LLMBaseURL   string // POCKET_LLM_BASE_URL：LLM API 地址（默认 Groq）
	LLMAPIKey    string // POCKET_LLM_API_KEY：LLM API 密钥
	LLMModel     string // POCKET_LLM_MODEL：默认 LLM 模型

	// 后端集成：可选代理到 llm-gateway-go 企业网关（享受流量治理）
	LLMGatewayURL    string // POCKET_LLM_GATEWAY_URL：llm-gateway-go 地址
	LLMGatewayAPIKey string // POCKET_LLM_GATEWAY_API_KEY：llm-gateway 租户 key

	// WebSocket 安全
	AllowedOrigins string // POCKET_ALLOWED_ORIGINS：逗号分隔的允许 origin 列表（空=dev 模式允许 localhost）

	// —— 磁盘会话聚合（internal/adapter/disk，Wake 范式移植）——
	// 读本机 Claude Code / Codex 落盘的会话转录，注册为只读实例
	// （disk-claude / disk-codex）。严格只读；默认关闭，需显式开启。
	DiskSessionsEnabled   bool   // POCKET_DISK_SESSIONS_ENABLED=true 启用
	DiskSessionsWorkspace string // POCKET_DISK_SESSIONS_WORKSPACE：留空=运维共享只读；填 workspace id=限定该租户

	// —— 会话迁移方案：实例感知增强配置 ——
	DiscoveryFullSubnet bool     // POCKET_DISCOVERY_FULL_SUBNET：true=扫描完整 /24（默认 false 仅本机+网关）
	DiscoveryPorts      []int    // POCKET_DISCOVERY_PORTS：自定义扫描端口（逗号分隔，空=默认 14096-14100）
	DiscoveryExtraHosts []string // POCKET_DISCOVERY_EXTRA_HOSTS：追加扫描主机（逗号分隔，如 ACC/NPS 内网穿透目标）

	// RedClaw 企业后端配置（可选）
	RedClawBaseURL    string // POCKET_REDCLAW_BASE_URL：RedClaw Gateway 地址
	RedClawSecret     string // POCKET_REDCLAW_SECRET：共享密钥
	RedClawTenantID   string // POCKET_REDCLAW_TENANT_ID：当前租户 ID（默认 default）
	RedClawTimeoutSec int    // POCKET_REDCLAW_TIMEOUT_SEC：HTTP 超时秒数（默认 30）
}

// Load reads all configuration from environment variables and returns a Config instance.
// It applies sensible defaults for development environments. For production deployments,
// call Validate() on the returned Config to ensure all required fields are properly set.
func Load() Config {
	environment := strings.ToLower(strings.TrimSpace(getEnv("POCKET_ENV", "development")))
	if environment == "prod" {
		environment = "production"
	}

	return Config{
		Environment:              environment,
		HTTPPort:                 getEnv("POCKET_HTTP_PORT", "8088"),
		DBPath:                   getEnv("POCKET_DB_PATH", "./data/pocket.sqlite"),
		NPSBaseURL:               getFirstEnv([]string{"POCKET_INSTANCE_DISCOVERY_BASE_URL", "POCKET_NPS_BASE_URL"}, ""),
		NPSAuthKey:               getFirstEnv([]string{"POCKET_INSTANCE_DISCOVERY_AUTH_TOKEN", "POCKET_NPS_AUTH_KEY"}, ""),
		NPSAuthCryptKey:          getFirstEnv([]string{"POCKET_INSTANCE_DISCOVERY_AUTH_SECRET", "POCKET_NPS_AUTH_CRYPT_KEY"}, ""),
		OpenCodeTimeoutMS:        getEnv("POCKET_OPENCODE_TIMEOUT_MS", "5000"),
		WSHeartbeatMS:            getEnv("POCKET_WS_HEARTBEAT_MS", "15000"),
		ReminderCheckIntervalSec: getEnv("POCKET_REMINDER_CHECK_INTERVAL_SEC", "60"),
		AndroidAppID:             getEnv("POCKET_ANDROID_APP_ID", "com.kaixuan.opencode.pocket"),
		UseAndroidShell:          getEnv("POCKET_ANDROID_USE_CAPACITOR", "true"),
		OpenCodeInstancesJSON:    getFirstEnv([]string{"POCKET_INSTANCE_CATALOG_JSON", "POCKET_OPENCODE_INSTANCES"}, ""),
		FeishuAppID:              getEnv("POCKET_FEISHU_APP_ID", ""),
		FeishuAppSecret:          getEnv("POCKET_FEISHU_APP_SECRET", ""),
		FeishuVerifyToken:        getEnv("POCKET_FEISHU_VERIFY_TOKEN", ""),
		FeishuVerifySecret:       getEnv("POCKET_FEISHU_VERIFY_SECRET", ""),
		FeishuEncryptKey:         getEnv("POCKET_FEISHU_ENCRYPT_KEY", ""),
		// Phase 0 个人助理模块
		PostgresDSN:                getFirstEnv([]string{"POCKET_POSTGRES_DSN", "DATABASE_URL"}, ""),
		PostgresSchema:             getEnv("POCKET_PG_SCHEMA", "opencode_pocket"),
		GroqAPIKey:                 getEnv("POCKET_GROQ_API_KEY", ""),
		KxMemoryBaseURL:            getEnv("POCKET_KXMEMORY_BASE_URL", ""),
		KxMemoryNoteClassifyPath:   getEnv("POCKET_KXMEMORY_NOTE_CLASSIFY_PATH", "/v1/notes/classify"),
		KxMemoryEmailClassifyPath:  getEnv("POCKET_KXMEMORY_EMAIL_CLASSIFY_PATH", "/v1/emails/classify"),
		KxMemoryDailySummaryPath:   getEnv("POCKET_KXMEMORY_DAILY_SUMMARY_PATH", "/v1/emails/daily-summary"),
		EmailMasterKey:             getEnv("POCKET_EMAIL_MASTER_KEY", ""),
		MCPBaseURL:                 getEnv("POCKET_MCP_BASE_URL", ""),
		MCPAPIKey:                  getEnv("POCKET_MCP_API_KEY", ""),
		MCPInsecureTLS:             getEnv("POCKET_MCP_INSECURE_TLS", "") == "true",
		MCPTenantID:                getEnv("POCKET_MCP_TENANT_ID", ""),
		MCPScopeString:             getEnv("POCKET_MCP_SCOPES", "tasks,sessions"),
		JWTSecret:                  getEnv("POCKET_JWT_SECRET", "pocket-dev-insecure-secret"),
		DevAuth:                    getEnv("POCKET_DEV_AUTH", "") == "true",
		DevAuthUser:                getEnv("POCKET_AUTH_USER", ""),
		DevAuthPass:                getEnv("POCKET_AUTH_PASS", ""),
		EmailGoogleClientID:        getEnv("POCKET_EMAIL_GOOGLE_CLIENT_ID", ""),
		EmailGoogleClientSecret:    getEnv("POCKET_EMAIL_GOOGLE_CLIENT_SECRET", ""),
		EmailMicrosoftClientID:     getEnv("POCKET_EMAIL_MICROSOFT_CLIENT_ID", ""),
		EmailMicrosoftClientSecret: getEnv("POCKET_EMAIL_MICROSOFT_CLIENT_SECRET", ""),
		EmailOAuthRedirectURL:      getEnv("POCKET_EMAIL_OAUTH_REDIRECT_URL", "http://localhost:8088/callback/email/oauth"),
		EmailFetchEnabled:          getEnv("POCKET_EMAIL_FETCH_ENABLED", "true") == "true",
		TimezoneOffsetSec:          getEnvInt("POCKET_TIMEZONE_OFFSET_SEC", 28800),
		// Phase C 无状态 AI 网关
		EmbedBaseURL: getEnv("POCKET_EMBED_BASE_URL", ""),
		EmbedAPIKey:  getFirstEnv([]string{"POCKET_EMBED_API_KEY", "POCKET_OPENAI_API_KEY"}, ""),
		EmbedModel:   getEnv("POCKET_EMBED_MODEL", "text-embedding-3-small"),
		LLMBaseURL:   getEnv("POCKET_LLM_BASE_URL", ""),
		LLMAPIKey:    getFirstEnv([]string{"POCKET_LLM_API_KEY", "POCKET_GROQ_API_KEY"}, ""),
		LLMModel:     getEnv("POCKET_LLM_MODEL", ""),
		// 后端集成：llm-gateway-go 企业网关（可选）
		LLMGatewayURL:    getEnv("POCKET_LLM_GATEWAY_URL", ""),
		LLMGatewayAPIKey: getEnv("POCKET_LLM_GATEWAY_API_KEY", ""),
		// WebSocket 安全
		AllowedOrigins: getEnv("POCKET_ALLOWED_ORIGINS", ""),
		// 磁盘会话聚合（只读）
		DiskSessionsEnabled:   getEnv("POCKET_DISK_SESSIONS_ENABLED", "") == "true",
		DiskSessionsWorkspace: getEnv("POCKET_DISK_SESSIONS_WORKSPACE", ""),
		// 会话迁移方案：实例感知增强
		DiscoveryFullSubnet: getEnv("POCKET_DISCOVERY_FULL_SUBNET", "") == "true",
		DiscoveryPorts:      parseIntList(getEnv("POCKET_DISCOVERY_PORTS", "")),
		DiscoveryExtraHosts: parseStringList(getEnv("POCKET_DISCOVERY_EXTRA_HOSTS", "")),
		// RedClaw 企业后端
		RedClawBaseURL:    getEnv("POCKET_REDCLAW_BASE_URL", ""),
		RedClawSecret:     getEnv("POCKET_REDCLAW_SECRET", ""),
		RedClawTenantID:   getEnv("POCKET_REDCLAW_TENANT_ID", "default"),
		RedClawTimeoutSec: getEnvInt("POCKET_REDCLAW_TIMEOUT_SEC", 30),
	}
}

// Validate checks that all required configuration is present and valid for production use.
// It enforces security requirements like strong JWT secrets, disabled development flags,
// and proper TLS configuration. Returns an error if any validation fails.
func (c Config) Validate() error {
	if c.Environment != "production" {
		return nil
	}

	// Security: JWT secret must be strong and not use development default
	if len([]byte(c.JWTSecret)) < 32 || c.JWTSecret == "pocket-dev-insecure-secret" {
		return fmt.Errorf("POCKET_JWT_SECRET must be at least 32 bytes and must not use the development default")
	}

	// Security: Development features must be disabled in production
	if c.DevAuth {
		return fmt.Errorf("POCKET_DEV_AUTH must be false in production")
	}

	// Security: TLS verification must be enabled in production
	if c.MCPInsecureTLS {
		return fmt.Errorf("POCKET_MCP_INSECURE_TLS must be false in production")
	}

	// Security: ACC JWT tenant_id must be set when MCP is active in production,
	// so the internal JWT carries a non-empty tenant_id claim (ACC rejects empty).
	if strings.TrimSpace(c.MCPBaseURL) != "" && strings.TrimSpace(c.MCPTenantID) == "" {
		return fmt.Errorf("POCKET_MCP_TENANT_ID must be configured in production when POCKET_MCP_BASE_URL is set")
	}

	// PK-3.1: direct LLM provider access is forbidden in production (fail-closed).
	// All LLM traffic must go through the enterprise gateway
	// (POCKET_LLM_GATEWAY_URL/POCKET_LLM_GATEWAY_API_KEY). Reject the startup
	// if a direct endpoint or key is configured, even when a gateway is also
	// configured, so no bypass path can exist.
	if strings.TrimSpace(c.LLMBaseURL) != "" || strings.TrimSpace(c.LLMAPIKey) != "" {
		return fmt.Errorf("POCKET_LLM_BASE_URL/POCKET_LLM_API_KEY (direct LLM provider access) must not be configured in production; route LLM traffic through POCKET_LLM_GATEWAY_URL instead")
	}

	// Database: PostgreSQL is required for production
	if strings.TrimSpace(c.PostgresDSN) == "" {
		return fmt.Errorf("POCKET_POSTGRES_DSN must be configured in production")
	}

	// Security: CORS origins must be explicitly configured
	if strings.TrimSpace(c.AllowedOrigins) == "" {
		return fmt.Errorf("POCKET_ALLOWED_ORIGINS must be configured in production")
	}
	if err := validateOrigins(c.AllowedOrigins); err != nil {
		return fmt.Errorf("invalid POCKET_ALLOWED_ORIGINS: %w", err)
	}

	// Network: HTTP port must be valid
	if err := validatePort(c.HTTPPort); err != nil {
		return fmt.Errorf("invalid POCKET_HTTP_PORT: %w", err)
	}

	// Timeouts: Must be reasonable positive values
	if err := validateTimeout(c.OpenCodeTimeoutMS, "POCKET_OPENCODE_TIMEOUT_MS"); err != nil {
		return err
	}
	if err := validateTimeout(c.WSHeartbeatMS, "POCKET_WS_HEARTBEAT_MS"); err != nil {
		return err
	}
	if err := validateTimeout(c.ReminderCheckIntervalSec, "POCKET_REMINDER_CHECK_INTERVAL_SEC"); err != nil {
		return err
	}

	// Timezone: Must be within reasonable bounds (-43200 to +50400 seconds, covering UTC-12 to UTC+14)
	if c.TimezoneOffsetSec < -43200 || c.TimezoneOffsetSec > 50400 {
		return fmt.Errorf("POCKET_TIMEZONE_OFFSET_SEC must be between -43200 and 50400 (UTC-12 to UTC+14), got %d", c.TimezoneOffsetSec)
	}

	// RedClaw: Timeout must be positive if configured
	if c.RedClawTimeoutSec <= 0 {
		return fmt.Errorf("POCKET_REDCLAW_TIMEOUT_SEC must be positive, got %d", c.RedClawTimeoutSec)
	}

	// Email: Master key must be set if email fetch is enabled
	if c.EmailFetchEnabled && strings.TrimSpace(c.EmailMasterKey) == "" {
		return fmt.Errorf("POCKET_EMAIL_MASTER_KEY must be configured when POCKET_EMAIL_FETCH_ENABLED is true")
	}

	return nil
}

// validatePort checks that a port string is a valid port number (1-65535).
func validatePort(port string) error {
	p, err := strconv.Atoi(port)
	if err != nil {
		return fmt.Errorf("must be a valid integer: %w", err)
	}
	if p < 1 || p > 65535 {
		return fmt.Errorf("must be between 1 and 65535, got %d", p)
	}
	return nil
}

// validateTimeout checks that a timeout string is a positive integer.
func validateTimeout(timeout string, fieldName string) error {
	t, err := strconv.Atoi(timeout)
	if err != nil {
		return fmt.Errorf("%s must be a valid integer: %w", fieldName, err)
	}
	if t <= 0 {
		return fmt.Errorf("%s must be positive, got %d", fieldName, t)
	}
	return nil
}

// validateOrigins checks that all origins in a comma-separated list are valid HTTP/HTTPS URLs.
// validateOrigins checks that all origins in a comma-separated list are valid HTTP/HTTPS URLs.
func validateOrigins(raw string) error {
	for _, item := range strings.Split(raw, ",") {
		origin := strings.TrimSpace(item)
		if origin == "" {
			continue
		}
		u, err := url.Parse(origin)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("invalid origin %q in POCKET_ALLOWED_ORIGINS", origin)
		}
	}
	return nil
}

// getEnv retrieves an environment variable or returns a fallback value if not set.
// getEnv retrieves an environment variable or returns a fallback value if not set.
func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

// getEnvInt retrieves an environment variable as an integer or returns a fallback if not set or invalid.
func getEnvInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

// getFirstEnv tries multiple environment variable keys in order and returns the first non-empty value.
// Returns fallback if all keys are unset or empty.
// getFirstEnv tries multiple environment variable keys in order and returns the first non-empty value.
// Returns fallback if all keys are unset or empty.
func getFirstEnv(keys []string, fallback string) string {
	for _, key := range keys {
		if value := os.Getenv(key); value != "" {
			return value
		}
	}
	return fallback
}

// parseIntList parses a comma-separated list of positive integers (e.g., "14096,14097,8080").
// Returns nil for empty input. Invalid or non-positive integers are silently skipped.
// parseIntList parses a comma-separated list of positive integers (e.g., "14096,14097,8080").
// Returns nil for empty input. Invalid or non-positive integers are silently skipped.
func parseIntList(s string) []int {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	var result []int
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			result = append(result, n)
		}
	}
	return result
}

// parseStringList parses a comma-separated list of strings, trimming whitespace.
// Returns nil for empty input. Empty entries after trimming are skipped.
func parseStringList(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	var result []string
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			result = append(result, v)
		}
	}
	return result
}
