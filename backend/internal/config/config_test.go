package config

import "testing"

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
	if cfg.JWTSecret != "pocket-dev-insecure-secret" {
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
	base := Config{
		Environment:    "production",
		JWTSecret:      "01234567890123456789012345678901",
		PostgresDSN:    "postgres://user:pass@localhost/pocket",
		AllowedOrigins: "https://app.example.com",
	}

	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "default secret", mutate: func(c *Config) { c.JWTSecret = "pocket-dev-insecure-secret" }},
		{name: "short secret", mutate: func(c *Config) { c.JWTSecret = "too-short" }},
		{name: "dev auth", mutate: func(c *Config) { c.DevAuth = true }},
		{name: "insecure MCP TLS", mutate: func(c *Config) { c.MCPInsecureTLS = true }},
		{name: "missing postgres", mutate: func(c *Config) { c.PostgresDSN = "" }},
		{name: "missing origins", mutate: func(c *Config) { c.AllowedOrigins = "" }},
		{name: "invalid origin", mutate: func(c *Config) { c.AllowedOrigins = "https://app.example.com/path" }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base
			tc.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("expected production config validation to fail")
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
	}

	t.Run("production with direct LLM endpoint rejected", func(t *testing.T) {
		cfg := base
		cfg.LLMBaseURL = "https://api.groq.com/openai/v1"
		if err := cfg.Validate(); err == nil {
			t.Fatal("expected production config with direct LLM base URL to be rejected")
		}
	})

	t.Run("production with direct LLM key rejected", func(t *testing.T) {
		cfg := base
		cfg.LLMAPIKey = "gsk_direct_key"
		if err := cfg.Validate(); err == nil {
			t.Fatal("expected production config with direct LLM API key to be rejected")
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
		if err := cfg.Validate(); err == nil {
			t.Fatal("expected production config with any direct LLM setting to be rejected even when gateway is configured")
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
		EmailFetchEnabled:        false,
	}

	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "invalid http port - zero", mutate: func(c *Config) { c.HTTPPort = "0" }},
		{name: "invalid http port - negative", mutate: func(c *Config) { c.HTTPPort = "-1" }},
		{name: "invalid http port - too large", mutate: func(c *Config) { c.HTTPPort = "65536" }},
		{name: "invalid http port - non-numeric", mutate: func(c *Config) { c.HTTPPort = "abc" }},
		{name: "invalid timeout - zero", mutate: func(c *Config) { c.OpenCodeTimeoutMS = "0" }},
		{name: "invalid timeout - negative", mutate: func(c *Config) { c.OpenCodeTimeoutMS = "-1" }},
		{name: "invalid ws heartbeat", mutate: func(c *Config) { c.WSHeartbeatMS = "0" }},
		{name: "invalid reminder interval", mutate: func(c *Config) { c.ReminderCheckIntervalSec = "-1" }},
		{name: "timezone too low", mutate: func(c *Config) { c.TimezoneOffsetSec = -50000 }},
		{name: "timezone too high", mutate: func(c *Config) { c.TimezoneOffsetSec = 60000 }},
		{name: "redclaw timeout zero", mutate: func(c *Config) { c.RedClawTimeoutSec = 0 }},
		{name: "redclaw timeout negative", mutate: func(c *Config) { c.RedClawTimeoutSec = -1 }},
		{name: "email enabled without master key", mutate: func(c *Config) {
			c.EmailFetchEnabled = true
			c.EmailMasterKey = ""
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base
			tc.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("expected production config validation to fail")
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
