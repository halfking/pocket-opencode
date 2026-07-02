package config

import "os"

type Config struct {
	HTTPPort                 string
	DBPath                   string // 保留兼容；Postgres 迁移后仅用于 data 目录定位
	PostgresDSN              string // Phase 0: pocket 后端统一数据层
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
	FeishuAppID       string
	FeishuAppSecret   string
	FeishuVerifyToken string // url_verification.token 匹配（可选）
	FeishuVerifySecret string // X-Lark-Signature 验签密钥（留空 = dev 模式跳过）
	FeishuEncryptKey  string // V1 加密事件解密用（V2 不加密，留空即可）

	// ---- Phase 0: 个人助理模块新增配置 ----
	// AI/STT 后端
	GroqAPIKey     string // POCKET_GROQ_API_KEY：云端 Whisper Large v3 Turbo 兜底
	KxMemoryBaseURL string // POCKET_KXMEMORY_BASE_URL：kxmemory FastAPI（笔记/分类/SSOT/总结）
	// 邮箱模块
	EmailMasterKey string // POCKET_EMAIL_MASTER_KEY：AES-GCM 加密 IMAP 凭证
	// 任务系统整合（Phase 5）
	MCPBaseURL string // POCKET_MCP_BASE_URL：ACC 系统 MCP 端点（mcp.kxpms.cn/acc/mcp）
	MCPAPIKey  string // POCKET_MCP_API_KEY：ACC MCP Bearer token
	MCPInsecureTLS bool // POCKET_MCP_INSECURE_TLS：跳过 TLS 验证（仅 dev/自签证书，生产必须 false）
	// 认证（Phase 0）
	JWTSecret   string // POCKET_JWT_SECRET：签发/校验 app JWT
	DevAuth     bool   // POCKET_DEV_AUTH：允许 admin/admin 开发登录（生产必须不设或 false）
	DevAuthUser string // POCKET_AUTH_USER：首用户 bootstrap 用户名（缺省 admin）
	DevAuthPass string // POCKET_AUTH_PASS：首用户 bootstrap 密码（缺省 admin；仅 POCKET_DEV_AUTH=true 时生效）

	// 邮箱 OAuth + IMAP fetch
	EmailGoogleClientID       string // POCKET_EMAIL_GOOGLE_CLIENT_ID
	EmailGoogleClientSecret   string // POCKET_EMAIL_GOOGLE_CLIENT_SECRET
	EmailMicrosoftClientID    string // POCKET_EMAIL_MICROSOFT_CLIENT_ID
	EmailMicrosoftClientSecret string // POCKET_EMAIL_MICROSOFT_CLIENT_SECRET
	EmailOAuthRedirectURL     string // POCKET_EMAIL_OAUTH_REDIRECT_URL（默认 http://localhost:8088/callback/email/oauth）
	EmailFetchEnabled         bool   // POCKET_EMAIL_FETCH_ENABLED（默认 true；CI/dev 可关闭）

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
}

func Load() Config {
	return Config{
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
		PostgresDSN:    getFirstEnv([]string{"POCKET_POSTGRES_DSN", "DATABASE_URL"}, ""),
		GroqAPIKey:     getEnv("POCKET_GROQ_API_KEY", ""),
		KxMemoryBaseURL: getEnv("POCKET_KXMEMORY_BASE_URL", ""),
		EmailMasterKey: getEnv("POCKET_EMAIL_MASTER_KEY", ""),
		MCPBaseURL:     getEnv("POCKET_MCP_BASE_URL", ""),
		MCPAPIKey:      getEnv("POCKET_MCP_API_KEY", ""),
		MCPInsecureTLS: getEnv("POCKET_MCP_INSECURE_TLS", "") == "true",
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
	}
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func getFirstEnv(keys []string, fallback string) string {
	for _, key := range keys {
		if value := os.Getenv(key); value != "" {
			return value
		}
	}
	return fallback
}
