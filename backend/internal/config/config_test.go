package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	for _, key := range []string{
		"POCKET_ENV",
		"POCKET_JWT_SECRET",
		"POCKET_DEV_AUTH",
		"POCKET_MCP_INSECURE_TLS",
		"POCKET_ALLOWED_ORIGINS",
		"POCKET_POSTGRES_DSN",
		"DATABASE_URL",
		"POCKET_PG_SCHEMA",
	} {
		t.Setenv(key, "")
	}

	cfg := Load()
	if cfg.Environment != "development" {
		t.Fatalf("expected development environment, got %q", cfg.Environment)
	}
	if cfg.JWTSecret != DevDefaultJWTSecret {
		t.Fatalf("unexpected default JWT secret: %q", cfg.JWTSecret)
	}
	if cfg.HTTPPort != "8088" {
		t.Fatalf("unexpected default HTTP port: %q", cfg.HTTPPort)
	}
	if cfg.PostgresSchema != "opencode_pocket" {
		t.Fatalf("unexpected default postgres schema: %q", cfg.PostgresSchema)
	}
	if !cfg.EmailFetchEnabled {
		t.Fatal("expected email fetch to be enabled by default")
	}
}

func TestLoadProductionAlias(t *testing.T) {
	t.Setenv("POCKET_ENV", "prod")
	t.Setenv("POCKET_JWT_SECRET", "01234567890123456789012345678901")
	t.Setenv("POCKET_ALLOWED_ORIGINS", "https://app.example.com")
	t.Setenv("POCKET_POSTGRES_DSN", "postgres://user:pass@localhost/pocket")
	t.Setenv("POCKET_PG_SCHEMA", "tenant_schema")
	t.Setenv("POCKET_EMAIL_FETCH_ENABLED", "false")
	// RedClaw auth authority: production requires it unless legacy-only.
	t.Setenv("POCKET_REDCLAW_ADMIN_URL", "http://redclaw-admin.internal:28081")
	t.Setenv("POCKET_REDCLAW_ADMIN_SECRET", "0123456789abcdef0123456789abcdef-redclaw")
	// PK-3.1: direct LLM env must be cleared so this test does not depend on
	// the host machine's environment.
	t.Setenv("POCKET_LLM_BASE_URL", "")
	t.Setenv("POCKET_LLM_API_KEY", "")
	t.Setenv("POCKET_GROQ_API_KEY", "")

	cfg := Load()
	if cfg.Environment != "production" {
		t.Fatalf("expected prod to normalize to production, got %q", cfg.Environment)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected valid production config, got %v", err)
	}
}

func TestProductionConfigValidation(t *testing.T) {
	// ⚠ §692：这张表此前是**恒真的**。base 没有设 AuthLegacyOnly，
	// 于是 base 自己就过不了 RedClaw 校验（`POCKET_REDCLAW_ADMIN_URL must be
	// configured in production`）—— 而 RedClaw 排在所有被测检查**之后**。
	// 结果：8 个用例全部因为**同一条无关检查**而失败，每一格都是绿的，
	// 表格看上去在验证「默认密钥 / 短密钥 / DevAuth / TLS / PG / origins 都会被拒」，
	// 实际上它一次都没证明过是**哪条**校验拒的。
	// ⇒ 现在 base 补上 AuthLegacyOnly，并逐格断言错误来自**预期的那条**校验。
	base := Config{
		Environment:    "production",
		JWTSecret:      "01234567890123456789012345678901",
		PostgresDSN:    "postgres://user:pass@localhost/pocket",
		AllowedOrigins: "https://app.example.com",
		AuthLegacyOnly: true,

		// 下面几项与 `Load()` 的默认值一致：Validate() 会解析它们，
		// 留空会先在解析处报错，把后面所有用例的失败原因都盖掉
		// —— 这正是本节要根治的「归因被前置失败吃掉」。
		HTTPPort:                 "8088",
		OpenCodeTimeoutMS:        "5000",
		WSHeartbeatMS:            "15000",
		ReminderCheckIntervalSec: "60",
		RedClawAdminTimeoutSec:   10,
		RedClawTimeoutSec:        30,
		RSS: RSSConfig{
			HTTPTimeout:    15 * time.Second,
			MaxConcurrency: 4,
			DigestHour:     9,
			DigestMinute:   0,
		},
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("前提失效：base 必须是合法生产配置，否则下面的用例无法归因（%v）", err)
	}

	cases := []struct {
		name string
		// wantErrContains：失败必须来自**这一格想验证的那条**校验。
		wantErrContains string
		mutate          func(*Config)
	}{
		{name: "default secret", wantErrContains: "POCKET_JWT_SECRET",
			mutate: func(c *Config) { c.JWTSecret = DevDefaultJWTSecret }},
		{name: "legacy dead default secret", wantErrContains: "POCKET_JWT_SECRET",
			mutate: func(c *Config) { c.JWTSecret = "pocket-dev-insecure-secret" }},
		{name: "short secret", wantErrContains: "POCKET_JWT_SECRET",
			mutate: func(c *Config) { c.JWTSecret = "too-short" }},
		// §692：32 个空格是 32 **字节**，`len([]byte(secret)) < 32` 不成立，
		// 且不等于任何 dev 默认值 ⇒ 修复前这一格会被放行，即生产用一把
		// 纯空白的密钥签发 JWT。长度校验必须按 trim 后的有效字符算。
		{name: "whitespace-only secret", wantErrContains: "POCKET_JWT_SECRET",
			mutate: func(c *Config) { c.JWTSecret = strings.Repeat(" ", 32) }},
		{name: "dev auth", wantErrContains: "POCKET_DEV_AUTH",
			mutate: func(c *Config) { c.DevAuth = true }},
		{name: "insecure MCP TLS", wantErrContains: "POCKET_MCP_INSECURE_TLS",
			mutate: func(c *Config) { c.MCPInsecureTLS = true }},
		{name: "missing postgres", wantErrContains: "POCKET_POSTGRES_DSN",
			mutate: func(c *Config) { c.PostgresDSN = "" }},
		{name: "missing origins", wantErrContains: "POCKET_ALLOWED_ORIGINS must be configured",
			mutate: func(c *Config) { c.AllowedOrigins = "" }},
		{name: "invalid origin", wantErrContains: "invalid POCKET_ALLOWED_ORIGINS",
			mutate: func(c *Config) { c.AllowedOrigins = "https://app.example.com/path" }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base
			tc.mutate(&cfg)
			err := cfg.Validate()
			if err == nil {
				t.Fatal("expected production config validation to fail")
			}
			if !strings.Contains(err.Error(), tc.wantErrContains) {
				t.Fatalf("失败的不是本用例想验证的那条校验：got %q，want 包含 %q",
					err.Error(), tc.wantErrContains)
			}
		})
	}
}

// TestValidateProductionLLMDirectAccessRejected covers PK-3.1: production
// must fail-closed when a direct LLM endpoint/key is configured, gateway
// routing must pass, and development keeps direct access allowed.
func TestValidateProductionLLMDirectAccessRejected(t *testing.T) {
	base := Config{
		Environment:              "production",
		HTTPPort:                 "8088",
		OpenCodeTimeoutMS:        "5000",
		WSHeartbeatMS:            "15000",
		ReminderCheckIntervalSec: "60",
		RedClawAdminURL:          "http://redclaw-admin.internal:28081",
		RedClawAdminSecret:       "0123456789abcdef0123456789abcdef-redclaw",
		JWTSecret:                "01234567890123456789012345678901",
		PostgresDSN:              "postgres://user:pass@localhost/pocket",
		AllowedOrigins:           "https://app.example.com",
		RedClawTimeoutSec:        30,
		RSS:                      RSSConfig{HTTPTimeout: 15 * time.Second, MaxConcurrency: 4},
	}

	// ⚠ §693：这张表的 base 是齐的（RedClaw + RSS 都设了），所以它**不是**恒真的
	// （两条正向子用例能过就是证明）。但负向子用例只断言「出错了」，
	// 钉不住错误来自哪条 ⇒ 这里统一按契约断言。
	const wantDirectLLMErr = "POCKET_LLM_BASE_URL/POCKET_LLM_API_KEY"

	t.Run("production with direct LLM endpoint rejected", func(t *testing.T) {
		cfg := base
		cfg.LLMBaseURL = "https://api.groq.com/openai/v1"
		err := cfg.Validate()
		if err == nil {
			t.Fatal("expected production config with direct LLM base URL to be rejected")
		}
		if !strings.Contains(err.Error(), wantDirectLLMErr) {
			t.Fatalf("失败的不是直连 LLM 那条校验：got %q", err.Error())
		}
	})

	t.Run("production with direct LLM key rejected", func(t *testing.T) {
		cfg := base
		cfg.LLMAPIKey = "gsk_direct_key"
		err := cfg.Validate()
		if err == nil {
			t.Fatal("expected production config with direct LLM API key to be rejected")
		}
		if !strings.Contains(err.Error(), wantDirectLLMErr) {
			t.Fatalf("失败的不是直连 LLM 那条校验：got %q", err.Error())
		}
	})

	t.Run("production with gateway passes", func(t *testing.T) {
		cfg := base
		cfg.LLMGatewayURL = "https://llm-gateway.internal"
		cfg.LLMGatewayAPIKey = "tenant-key"
		if err := cfg.Validate(); err != nil {
			t.Fatalf("expected production gateway config to pass, got %v", err)
		}
	})

	t.Run("production with direct LLM alongside gateway still rejected", func(t *testing.T) {
		cfg := base
		cfg.LLMGatewayURL = "https://llm-gateway.internal"
		cfg.LLMGatewayAPIKey = "tenant-key"
		cfg.LLMBaseURL = "https://api.groq.com/openai/v1"
		err := cfg.Validate()
		if err == nil {
			t.Fatal("expected production config with any direct LLM setting to be rejected even when gateway is configured")
		}
		if !strings.Contains(err.Error(), wantDirectLLMErr) {
			t.Fatalf("失败的不是直连 LLM 那条校验：got %q", err.Error())
		}
	})

	t.Run("development with direct LLM passes", func(t *testing.T) {
		cfg := base
		cfg.Environment = "development"
		cfg.LLMBaseURL = "https://api.groq.com/openai/v1"
		cfg.LLMAPIKey = "gsk_direct_key"
		if err := cfg.Validate(); err != nil {
			t.Fatalf("expected development config with direct LLM to pass, got %v", err)
		}
	})
}

func TestValidateOrigins(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		ok   bool
	}{
		{name: "multiple origins", raw: "https://app.example.com, http://localhost:3000", ok: true},
		{name: "path", raw: "https://app.example.com/path", ok: false},
		{name: "query", raw: "https://app.example.com?x=1", ok: false},
		{name: "unsupported scheme", raw: "ws://app.example.com", ok: false},
		{name: "empty entries", raw: "https://app.example.com, ,", ok: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateOrigins(tc.raw)
			if (err == nil) != tc.ok {
				t.Fatalf("validateOrigins(%q) error = %v, want success = %v", tc.raw, err, tc.ok)
			}
		})
	}
}

func TestParseIntList(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		expected []int
	}{
		{name: "empty string", input: "", expected: nil},
		{name: "whitespace only", input: "   ", expected: nil},
		{name: "single port", input: "8080", expected: []int{8080}},
		{name: "multiple ports", input: "14096,14097,14098", expected: []int{14096, 14097, 14098}},
		{name: "with spaces", input: "8080, 9090, 3000", expected: []int{8080, 9090, 3000}},
		{name: "invalid mixed", input: "8080,invalid,9090", expected: []int{8080, 9090}},
		{name: "negative filtered", input: "8080,-1,9090", expected: []int{8080, 9090}},
		{name: "zero filtered", input: "8080,0,9090", expected: []int{8080, 9090}},
		{name: "all invalid", input: "invalid,bad,wrong", expected: nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := parseIntList(tc.input)
			if len(result) != len(tc.expected) {
				t.Fatalf("parseIntList(%q) = %v, want %v", tc.input, result, tc.expected)
			}
			for i := range result {
				if result[i] != tc.expected[i] {
					t.Fatalf("parseIntList(%q) = %v, want %v", tc.input, result, tc.expected)
				}
			}
		})
	}
}

func TestParseStringList(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		expected []string
	}{
		{name: "empty string", input: "", expected: nil},
		{name: "whitespace only", input: "   ", expected: nil},
		{name: "single item", input: "host1", expected: []string{"host1"}},
		{name: "multiple items", input: "host1,host2,host3", expected: []string{"host1", "host2", "host3"}},
		{name: "with spaces", input: "host1, host2, host3", expected: []string{"host1", "host2", "host3"}},
		{name: "empty entries", input: "host1,,host2", expected: []string{"host1", "host2"}},
		{name: "trailing comma", input: "host1,host2,", expected: []string{"host1", "host2"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := parseStringList(tc.input)
			if len(result) != len(tc.expected) {
				t.Fatalf("parseStringList(%q) = %v, want %v", tc.input, result, tc.expected)
			}
			for i := range result {
				if result[i] != tc.expected[i] {
					t.Fatalf("parseStringList(%q) = %v, want %v", tc.input, result, tc.expected)
				}
			}
		})
	}
}

func TestValidatePort(t *testing.T) {
	cases := []struct {
		name  string
		port  string
		valid bool
	}{
		{name: "valid port", port: "8080", valid: true},
		{name: "min port", port: "1", valid: true},
		{name: "max port", port: "65535", valid: true},
		{name: "zero port", port: "0", valid: false},
		{name: "negative port", port: "-1", valid: false},
		{name: "too large", port: "65536", valid: false},
		{name: "non-numeric", port: "abc", valid: false},
		{name: "empty", port: "", valid: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePort(tc.port)
			if (err == nil) != tc.valid {
				t.Fatalf("validatePort(%q) error = %v, want valid = %v", tc.port, err, tc.valid)
			}
		})
	}
}

func TestValidateTimeout(t *testing.T) {
	cases := []struct {
		name    string
		timeout string
		valid   bool
	}{
		{name: "valid timeout", timeout: "5000", valid: true},
		{name: "min timeout", timeout: "1", valid: true},
		{name: "zero timeout", timeout: "0", valid: false},
		{name: "negative timeout", timeout: "-1", valid: false},
		{name: "non-numeric", timeout: "abc", valid: false},
		{name: "empty", timeout: "", valid: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateTimeout(tc.timeout, "TEST_TIMEOUT")
			if (err == nil) != tc.valid {
				t.Fatalf("validateTimeout(%q) error = %v, want valid = %v", tc.timeout, err, tc.valid)
			}
		})
	}
}

func TestProductionValidationEnhanced(t *testing.T) {
	// ⚠ §693：这张 13 格表此前也是**恒真的**，机制与 §692.6 那张完全一样：
	// base 缺 `RedClawAdminURL`/`RedClawAdminSecret`（且 `AuthLegacyOnly` 为零值 false）
	// 与整个 `RSS` ⇒ **base 自己就过不了校验**，而 RedClaw 检查排在端口/超时/
	// 时区/RSS 这些被测检查**之前** ⇒ 实测 **13/13 格报的是同一个错误**：
	//   `POCKET_REDCLAW_ADMIN_URL must be configured in production`
	// 也就是说这 13 格从未验证过「端口非法」「超时非正」「时区越界」中的任何一条。
	base := Config{
		Environment:              "production",
		JWTSecret:                "01234567890123456789012345678901",
		PostgresDSN:              "postgres://user:pass@localhost/pocket",
		AllowedOrigins:           "https://app.example.com",
		HTTPPort:                 "8088",
		OpenCodeTimeoutMS:        "5000",
		WSHeartbeatMS:            "15000",
		ReminderCheckIntervalSec: "60",
		TimezoneOffsetSec:        28800,
		RedClawTimeoutSec:        30,
		RedClawAdminURL:          "http://redclaw-admin.internal:28081",
		RedClawAdminSecret:       "0123456789abcdef0123456789abcdef-redclaw",
		EmailFetchEnabled:        false,
		RSS:                      RSSConfig{HTTPTimeout: 15 * time.Second, MaxConcurrency: 4, DigestHour: 9, DigestMinute: 0},
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("前提失效：base 必须是合法生产配置，否则 13 格都无法归因（%v）", err)
	}

	cases := []struct {
		name            string
		wantErrContains string
		mutate          func(*Config)
	}{
		{name: "invalid http port - zero", wantErrContains: "invalid POCKET_HTTP_PORT",
			mutate: func(c *Config) { c.HTTPPort = "0" }},
		{name: "invalid http port - negative", wantErrContains: "invalid POCKET_HTTP_PORT",
			mutate: func(c *Config) { c.HTTPPort = "-1" }},
		{name: "invalid http port - too large", wantErrContains: "invalid POCKET_HTTP_PORT",
			mutate: func(c *Config) { c.HTTPPort = "65536" }},
		{name: "invalid http port - non-numeric", wantErrContains: "invalid POCKET_HTTP_PORT",
			mutate: func(c *Config) { c.HTTPPort = "abc" }},
		{name: "invalid timeout - zero", wantErrContains: "POCKET_OPENCODE_TIMEOUT_MS",
			mutate: func(c *Config) { c.OpenCodeTimeoutMS = "0" }},
		{name: "invalid timeout - negative", wantErrContains: "POCKET_OPENCODE_TIMEOUT_MS",
			mutate: func(c *Config) { c.OpenCodeTimeoutMS = "-1" }},
		{name: "invalid ws heartbeat", wantErrContains: "POCKET_WS_HEARTBEAT_MS",
			mutate: func(c *Config) { c.WSHeartbeatMS = "0" }},
		{name: "invalid reminder interval", wantErrContains: "POCKET_REMINDER_CHECK_INTERVAL_SEC",
			mutate: func(c *Config) { c.ReminderCheckIntervalSec = "-1" }},
		{name: "timezone too low", wantErrContains: "POCKET_TIMEZONE_OFFSET_SEC",
			mutate: func(c *Config) { c.TimezoneOffsetSec = -50000 }},
		{name: "timezone too high", wantErrContains: "POCKET_TIMEZONE_OFFSET_SEC",
			mutate: func(c *Config) { c.TimezoneOffsetSec = 60000 }},
		{name: "redclaw timeout zero", wantErrContains: "POCKET_REDCLAW_TIMEOUT_SEC",
			mutate: func(c *Config) { c.RedClawTimeoutSec = 0 }},
		{name: "redclaw timeout negative", wantErrContains: "POCKET_REDCLAW_TIMEOUT_SEC",
			mutate: func(c *Config) { c.RedClawTimeoutSec = -1 }},
		{name: "email enabled without master key", wantErrContains: "POCKET_EMAIL_MASTER_KEY",
			mutate: func(c *Config) {
				c.EmailFetchEnabled = true
				c.EmailMasterKey = ""
			}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base
			tc.mutate(&cfg)
			err := cfg.Validate()
			if err == nil {
				t.Fatal("expected production config validation to fail")
			}
			if !strings.Contains(err.Error(), tc.wantErrContains) {
				t.Fatalf("失败的不是本用例想验证的那条校验：got %q，want 包含 %q",
					err.Error(), tc.wantErrContains)
			}
		})
	}
}

func TestProductionValidationSuccess(t *testing.T) {
	cfg := Config{
		Environment:              "production",
		JWTSecret:                "01234567890123456789012345678901",
		PostgresDSN:              "postgres://user:pass@localhost/pocket",
		AllowedOrigins:           "https://app.example.com",
		HTTPPort:                 "8088",
		OpenCodeTimeoutMS:        "5000",
		WSHeartbeatMS:            "15000",
		ReminderCheckIntervalSec: "60",
		TimezoneOffsetSec:        28800,
		RedClawTimeoutSec:        30,
		RedClawAdminURL:          "http://redclaw-admin.internal:28081",
		RedClawAdminSecret:       "0123456789abcdef0123456789abcdef-redclaw",
		EmailFetchEnabled:        false,
		RSS:                      RSSConfig{HTTPTimeout: 15 * time.Second, MaxConcurrency: 4},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected valid production config, got error: %v", err)
	}
}

func TestProductionValidationWithEmailEnabled(t *testing.T) {
	cfg := Config{
		Environment:              "production",
		JWTSecret:                "01234567890123456789012345678901",
		PostgresDSN:              "postgres://user:pass@localhost/pocket",
		AllowedOrigins:           "https://app.example.com",
		HTTPPort:                 "8088",
		OpenCodeTimeoutMS:        "5000",
		WSHeartbeatMS:            "15000",
		ReminderCheckIntervalSec: "60",
		TimezoneOffsetSec:        28800,
		RedClawTimeoutSec:        30,
		RedClawAdminURL:          "http://redclaw-admin.internal:28081",
		RedClawAdminSecret:       "0123456789abcdef0123456789abcdef-redclaw",
		EmailFetchEnabled:        true,
		EmailMasterKey:           "secure-master-key-for-encryption",
		RSS:                      RSSConfig{HTTPTimeout: 15 * time.Second, MaxConcurrency: 4},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected valid production config with email enabled, got error: %v", err)
	}
}

func TestGetFirstEnv(t *testing.T) {
	t.Setenv("TEST_KEY_1", "value1")
	t.Setenv("TEST_KEY_2", "value2")

	cases := []struct {
		name     string
		keys     []string
		fallback string
		expected string
	}{
		{name: "first key set", keys: []string{"TEST_KEY_1", "TEST_KEY_2"}, fallback: "default", expected: "value1"},
		{name: "second key set", keys: []string{"TEST_KEY_MISSING", "TEST_KEY_2"}, fallback: "default", expected: "value2"},
		{name: "no keys set", keys: []string{"TEST_KEY_MISSING_1", "TEST_KEY_MISSING_2"}, fallback: "default", expected: "default"},
		{name: "empty keys", keys: []string{}, fallback: "default", expected: "default"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := getFirstEnv(tc.keys, tc.fallback)
			if result != tc.expected {
				t.Fatalf("getFirstEnv(%v, %q) = %q, want %q", tc.keys, tc.fallback, result, tc.expected)
			}
		})
	}
}

// ── §774 补齐三兄弟的覆盖（getEnv / getEnvInt / getEnvDuration）──
//
// 为什么要补（全部可核对）：
//
//	① `6ec9dda3b`（2026-07-26，message 声称 "fix missing godoc ... and test coverage"）
//	   补了 `TestGetFirstEnv`，**却没补 `getEnv` / `getEnvInt`**；
//	② 而 `getEnv` 是这三个里调用最多的（`config.go` 内 98 处），
//	   `getEnvInt` 15 处 —— 两个都**零直接测试**；
//	③ 同一批改动还在 `getEnv` 上留下**两行逐字重复的 godoc**
//	   （本轮已删；`gofmt`/`go vet` 都不管这个，所以活了约 50 天）。
//
// ⚠⚠⚠ **下面这三条是「固化当前行为」，不是「断言它对」**：
//
//	其中标 ⭐ 的那几条是**缺陷**，不是期望行为。
//	它们在这里的作用是：**把缺陷变成可核对的读数**，
//	而不是让它继续躲在「没测试所以没人知道」里。
//
// ⛔ **若属主在 #63 上拍板「全仓 trim」，上面标 ⭐ 的用例会转红** ——
//
//	**那是预期的**：行为变更必须由人显式看见，不能悄悄改掉。
//	同族：§771 的「门红不是自行消红的理由（顺序不能反）」。
func TestGetEnvCurrentBehavior(t *testing.T) {
	cases := []struct{ name, set, fallback, want string }{
		{"未设置（空串）⇒ 走 fallback", "", "default", "default"},
		{"正常值原样返回", "https://llmgo.example/v1", "default", "https://llmgo.example/v1"},
		{"⭐ 纯空白被当成「已设置」原样返回（#63：getEnv 不 trim）", "   ", "default", "   "},
		{"⭐ 单个空格同理（⇒ 上层拿到的是不可用但非空的 key）", " ", "default", " "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TEST_GETENV_KEY", tc.set)
			if got := getEnv("TEST_GETENV_KEY", tc.fallback); got != tc.want {
				t.Fatalf("getEnv(set=%q) = %q, want %q", tc.set, got, tc.want)
			}
		})
	}
}

// ⭐⭐ TestGetEnvIntCurrentBehavior 里的两条 ⭐ 是**静默行为错误**，不是「容错」：
//
//	`POCKET_SMTP_PORT=" 587"`（复制粘贴带空格）⇒ 解析失败 ⇒ **静默变 0**
//	`POCKET_TIMEZONE_OFFSET_SEC=" 28800"` ⇒ 静默变 0 ⇒ 时区整个错掉
//
// 而调用方**拿不到任何提示**（没有 error、没有日志、没有 panic）。
// ⛔ 是否要 log / 报错 / 宽容解析，属**工程口径**，本轮只把它测出来，**不代拍**。
func TestGetEnvIntCurrentBehavior(t *testing.T) {
	cases := []struct {
		name     string
		set      string
		fallback int
		want     int
	}{
		{"未设置（空串）⇒ 走 fallback", "", 42, 42},
		{"正常整数字符串", "7", 42, 7},
		{"负数", "-3", 42, -3},
		{"零", "0", 42, 0},
		{"⭐ 非数字 ⇒ 静默回落，无任何提示", "abc", 42, 42},
		{"⭐⭐ 前后空格 ⇒ Atoi 失败 ⇒ 静默回落（getEnvDuration 有 TrimSpace，它没有）", " 7 ", 42, 42},
		{"⭐⭐ 小数 ⇒ 静默回落（int 型配置给小数同样无声吞掉）", "7.5", 42, 42},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TEST_GETENVINT_KEY", tc.set)
			if got := getEnvInt("TEST_GETENVINT_KEY", tc.fallback); got != tc.want {
				t.Fatalf("getEnvInt(set=%q, fb=%d) = %d, want %d", tc.set, tc.fallback, got, tc.want)
			}
		})
	}
}

// TestGetEnvDurationCurrentBehavior —— 与上一条**故意对照**：
// 同样的空白值，这里因为有 `strings.TrimSpace` 而**能正常解析**。
func TestGetEnvDurationCurrentBehavior(t *testing.T) {
	cases := []struct {
		name     string
		set      string
		fallback time.Duration
		want     time.Duration
	}{
		{"未设置（空串）⇒ 走 fallback", "", 5 * time.Second, 5 * time.Second},
		{"标准 duration 语法", "1m30s", time.Second, 90 * time.Second},
		{"裸整数按秒解释", "42", time.Second, 42 * time.Second},
		{"⭐ 前后空格 ⇒ 有 TrimSpace ⇒ 正常解析（与 getEnvInt 相反）", " 5s ", time.Second, 5 * time.Second},
		{"解析失败 ⇒ 静默回落（与 getEnvInt 同样无声）", "abc", 5 * time.Second, 5 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TEST_GETENVDURATION_KEY", tc.set)
			if got := getEnvDuration("TEST_GETENVDURATION_KEY", tc.fallback); got != tc.want {
				t.Fatalf("getEnvDuration(set=%q) = %v, want %v", tc.set, got, tc.want)
			}
		})
	}
}

// TestEnvTrimInconsistencyAcrossSiblings 把上面那条**不一致**单独钉住并命名：
//
//	同一个带空格的输入，`getEnvDuration` 解析成功、`getEnvInt` 静默回落。
//
// ⇒ 它不是「三兄弟风格不统一」这种审美问题，而是
//
//	**同一个运维动作（env 里多打一个空格）在两个函数上得到相反结果**，
//	且其中一边**完全无声**。
//
// ⚠ 这条测试现在是**绿的** —— 因为缺陷还在。
// ⛔ **它绿不代表缺陷已修**，只代表「缺陷一旦被修就会红」。
func TestEnvTrimInconsistencyAcrossSiblings(t *testing.T) {
	t.Setenv("TEST_TRIM_DURATION_KEY", " 5s ")
	t.Setenv("TEST_TRIM_INT_KEY", " 7 ")

	if got := getEnvDuration("TEST_TRIM_DURATION_KEY", time.Second); got != 5*time.Second {
		t.Fatalf("getEnvDuration(\" 5s \") = %v, want 5s（它有 TrimSpace，应当正常解析）", got)
	}
	// ⚠ 这里断言的是**当前的缺陷行为**：静默回落，而不是 7。
	// 若将来给 getEnvInt 补上 TrimSpace，这一条会红 —— 那是修复生效的信号。
	if got := getEnvInt("TEST_TRIM_INT_KEY", 42); got != 42 {
		t.Fatalf("getEnvInt(\" 7 \") = %d, want 42（静默回落 = 缺陷仍在）；"+
			"若本条转红，说明 getEnvInt 已补 TrimSpace ⇒ 请同步更新上面两条 ⭐ 用例", got)
	}
}
