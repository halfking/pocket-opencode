// Package logging — rule 71 Go 参考实现的单元测试
//
// -----------------------------------------------------------------------------
// Purpose:        证明参考实现确实满足 rules/71-log-standard.md 的关键约束：
//                 必填字段齐全、扁平单层、级别数值符合 OTel 表、敏感信息脱敏、
//                 非法参数 fail-loud。
// Replaces:       (无)
// Replaced-by:    (无)
// Status:         active
//
// Changelog:
//   2026-11-09  v1.0  首版。
// -----------------------------------------------------------------------------

package logging

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// captureInit 初始化日志到临时文件并返回解析函数。
func captureInit(t *testing.T, opts Options) func() map[string]any {
	t.Helper()
	if opts.Service == "" {
		opts.Service = "test-service"
	}
	if opts.Version == "" {
		opts.Version = "0.0.1"
	}
	if opts.Env == "" {
		opts.Env = "test"
	}
	dir := t.TempDir()
	opts.LogFile = filepath.Join(dir, "svc.log")
	if err := Init(opts); err != nil {
		t.Fatalf("Init 失败: %v", err)
	}
	t.Cleanup(func() { _ = Shutdown() })

	return func() map[string]any {
		data, err := os.ReadFile(opts.LogFile)
		if err != nil {
			t.Fatalf("读取日志失败: %v", err)
		}
		lines := strings.Split(strings.TrimSpace(string(data)), "\n")
		if len(lines) == 0 {
			t.Fatalf("日志为空")
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(lines[len(lines)-1]), &obj); err != nil {
			t.Fatalf("日志不是合法 JSON: %v (line=%s)", err, lines[len(lines)-1])
		}
		return obj
	}
}

// TestInitInjectsRequiredFields 验证 8 个必填字段全部自动注入。
func TestInitInjectsRequiredFields(t *testing.T) {
	last := captureInit(t, Options{})
	slog.Info("user profile loaded", "user_id", "u_a3f9")

	obj := last()
	required := []string{
		"timestamp", "severity_text", "severity_number",
		"msg", "service", "version", "env", "instance_id",
	}
	for _, key := range required {
		if _, ok := obj[key]; !ok {
			t.Errorf("缺必填字段 %q，实际字段=%v", key, keysOf(obj))
		}
	}
}

// keysOf 返回对象的键列表（测试辅助）。
func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestLogRecordIsFlat 验证输出为扁平单层（无嵌套对象）。
func TestLogRecordIsFlat(t *testing.T) {
	last := captureInit(t, Options{})
	slog.Info("flat check", "request_id", "req_1")

	obj := last()
	for key, val := range obj {
		switch v := val.(type) {
		case map[string]any:
			t.Errorf("字段 %q 是嵌套对象，违反扁平约束", key)
		case []any:
			t.Errorf("字段 %q 是数组，违反扁平约束", key)
		default:
			_ = v
		}
	}
}

// TestSeverityTextAndAlias 验证 severity_text 与 ECS 别名 level 同时存在且一致。
func TestSeverityTextAndAlias(t *testing.T) {
	last := captureInit(t, Options{})
	slog.Info("alias check")

	obj := last()
	sev, ok := obj["severity_text"].(string)
	if !ok {
		t.Fatalf("severity_text 缺失或非字符串")
	}
	if sev != "INFO" {
		t.Errorf("severity_text=%q，期望 INFO", sev)
	}
	// level 是小写别名（ECS log.level 惯例），与 severity_text 大小写不同。
	alias, ok := obj["level"].(string)
	if !ok {
		t.Fatalf("level 别名缺失")
	}
	if alias != strings.ToLower(sev) {
		t.Errorf("level 别名=%v，期望 %s（小写 ECS 形态）", alias, strings.ToLower(sev))
	}
}

// TestSeverityNumberFollowsOTelTable 验证 severity_number 符合 OTel 数值表。
func TestSeverityNumberFollowsOTelTable(t *testing.T) {
	cases := []struct {
		level slog.Level
		want  float64
	}{
		{slog.LevelDebug, 5},
		{slog.LevelInfo, 9},
		{slog.LevelWarn, 13},
		{slog.LevelError, 17},
	}
	for _, tc := range cases {
		last := captureInit(t, Options{Level: tc.level})
		slog.Log(context.Background(), tc.level, "level check")
		obj := last()
		got, ok := obj["severity_number"].(float64)
		if !ok {
			t.Fatalf("severity_number 缺失")
		}
		if got != tc.want {
			t.Errorf("level %v → severity_number=%v，期望 %v", tc.level, got, tc.want)
		}
	}
}

// TestSensitiveKeyIsRedacted 验证敏感键的值被替换为占位符。
func TestSensitiveKeyIsRedacted(t *testing.T) {
	last := captureInit(t, Options{})
	slog.Info("secret check", "password", "hunter2", "api_key", "sk-abcdefghijklmnop")

	obj := last()
	if got := obj["password"]; got != redactedPlaceholder {
		t.Errorf("password=%v，期望 %q", got, redactedPlaceholder)
	}
	if got := obj["api_key"]; got != redactedPlaceholder {
		t.Errorf("api_key=%v，期望 %q", got, redactedPlaceholder)
	}
}

// TestSensitiveValuePatternIsRedacted 验证敏感**值**模式被脱敏（即使键名无害）。
func TestSensitiveValuePatternIsRedacted(t *testing.T) {
	last := captureInit(t, Options{})
	slog.Info("value check", "note", "token is Bearer abcdef123456")

	obj := last()
	if got := obj["note"]; got != redactedPlaceholder {
		t.Errorf("note=%v，期望整体脱敏为 %q", got, redactedPlaceholder)
	}
}

// TestMsgIsRedacted 验证 msg 字段也走脱敏（不能绕过）。
func TestMsgIsRedacted(t *testing.T) {
	last := captureInit(t, Options{})
	slog.Info("AKIAIOSFODNN7EXAMPLE in message")

	obj := last()
	if got := obj["msg"]; got != redactedPlaceholder {
		t.Errorf("msg=%v，期望脱敏", got)
	}
}

// TestLongValueIsTruncated 验证超长值被截断，防止日志爆炸。
//
// 用 'z' 重复而非十六进制字符：长十六进制串会先命中敏感模式被整体脱敏
// （见 TestLongHexLikeValueIsRedacted），那是另一条独立的保护路径。
func TestLongValueIsTruncated(t *testing.T) {
	last := captureInit(t, Options{})
	long := strings.Repeat("z", maxValueLength+100)
	slog.Info("truncation check", "payload", long)

	obj := last()
	got, ok := obj["payload"].(string)
	if !ok {
		t.Fatalf("payload 缺失")
	}
	if len(got) > maxValueLength+len("…[truncated]") {
		t.Errorf("payload 长度 %d 未被截断", len(got))
	}
	if !strings.HasSuffix(got, "…[truncated]") {
		t.Errorf("payload 未带截断标记，实际=%q", got[len(got)-20:])
	}
}

// TestLongHexLikeValueIsRedacted 验证长十六进制串（疑似密钥）被整体脱敏，
// 而不是仅截断——这是与截断独立的另一条保护路径。
func TestLongHexLikeValueIsRedacted(t *testing.T) {
	last := captureInit(t, Options{})
	slog.Info("hex check", "blob", strings.Repeat("a", 40))

	obj := last()
	if got := obj["blob"]; got != redactedPlaceholder {
		t.Errorf("blob=%v，期望脱敏为 %q", got, redactedPlaceholder)
	}
}

// TestInitRejectsInvalidParams 验证非法参数 fail-loud（不静默降级）。
func TestInitRejectsInvalidParams(t *testing.T) {
	cases := []struct {
		name string
		opts Options
	}{
		{"缺 Service", Options{Version: "1.0", Env: "test"}},
		{"缺 Version", Options{Service: "svc", Env: "test"}},
		{"缺 Env", Options{Service: "svc", Version: "1.0"}},
		// 注意：没有「Env 非法」这一档——Init 会先把环境名归一到
		// local/dev/test/staging/prod，因此任何非空输入都是合法的。
		// 归一表见 TestNormalizeEnv。
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := Init(tc.opts); err == nil {
				t.Errorf("期望返回错误，实际 nil")
			}
		})
	}
}

// TestResourceFieldsAreConstant 验证资源字段在多次日志间保持不变
// （它们必须为常量才能升为 Loki 标签）。
func TestResourceFieldsAreConstant(t *testing.T) {
	last := captureInit(t, Options{})
	slog.Info("first")
	first := last()
	slog.Info("second")
	second := last()

	for _, key := range []string{"service", "version", "env", "instance_id"} {
		if first[key] != second[key] {
			t.Errorf("资源字段 %q 不稳定: %v → %v", key, first[key], second[key])
		}
	}
}

// TestTimestampIsRFC3339UTC 验证时间戳是 RFC3339 UTC。
func TestTimestampIsRFC3339UTC(t *testing.T) {
	last := captureInit(t, Options{})
	slog.Info("ts check")

	obj := last()
	ts, ok := obj["timestamp"].(string)
	if !ok {
		t.Fatalf("timestamp 缺失")
	}
	if !strings.HasSuffix(ts, "Z") {
		t.Errorf("timestamp=%q 期望以 Z 结尾（UTC）", ts)
	}
	if _, err := time.Parse(time.RFC3339Nano, ts); err != nil {
		t.Errorf("timestamp=%q 不是合法 RFC3339: %v", ts, err)
	}
}

// --- 环境名归一 ---

func TestNormalizeEnv(t *testing.T) {
	cases := map[string]string{
		"production":  "prod",
		"PROD":        "prod",
		"development": "dev",
		"staging":     "staging",
		"testing":     "test",
		"local":       "local",
		"":            "local",
		"unknown-xyz": "local",
	}
	for in, want := range cases {
		if got := NormalizeEnv(in); got != want {
			t.Errorf("NormalizeEnv(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestInit_NormalizesEnvBeforeUse(t *testing.T) {
	// Init 接受 "development" 这类历史取值，归一后写入日志的必须是 "dev"——
	// 否则 AI 按 env 分组时会同时看到 dev 与 development 两个桶。
	dir := t.TempDir()
	if err := Init(Options{
		Service: "svc",
		Version: "1.0.0",
		Env:     "development",
		Level:   slog.LevelInfo,
		LogFile: filepath.Join(dir, "svc.log"),
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer func() { _ = Shutdown() }()
	_, _, env, _ := ActiveResource()
	if env != "dev" {
		t.Errorf("env = %q, want dev", env)
	}
}

// --- 级别化 printf 辅助函数 ---

func TestErrorfWarnfCarryRealLevel(t *testing.T) {
	// 存量 stdlib log 调用点迁移到 Errorf/Warnf 后，
	// level 字段必须是真的 error/warn——否则 level=~"error|fatal" 会漏掉全部错误事件。
	dir := t.TempDir()
	if err := Init(Options{
		Service: "svc",
		Version: "1.0.0",
		Env:     "test",
		Level:   slog.LevelInfo,
		LogFile: filepath.Join(dir, "svc.log"),
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer func() { _ = Shutdown() }()

	Errorf("ERROR: boom %s", "x")
	Warnf("WARN: careful %d", 1)

	data, err := os.ReadFile(filepath.Join(dir, "svc.log"))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 3 {
		t.Fatalf("日志行数不足: %d", len(lines))
	}
	var errLine, warnLine map[string]any
	for _, ln := range lines {
		var obj map[string]any
		if err := json.Unmarshal([]byte(ln), &obj); err != nil {
			continue
		}
		switch obj["severity_text"] {
		case "ERROR":
			errLine = obj
		case "WARN":
			warnLine = obj
		}
	}
	if errLine == nil {
		t.Fatal("没有 ERROR 级别的行")
	}
	if errLine["level"] != "error" || errLine["severity_number"] != float64(17) {
		t.Errorf("ERROR 行级别字段不对: %v", errLine)
	}
	if !strings.Contains(errLine["msg"].(string), "boom x") {
		t.Errorf("ERROR 行消息不对: %v", errLine["msg"])
	}
	if warnLine == nil {
		t.Fatal("没有 WARN 级别的行")
	}
	if warnLine["level"] != "warn" || warnLine["severity_number"] != float64(13) {
		t.Errorf("WARN 行级别字段不对: %v", warnLine)
	}
}

func TestFatalfLevelIsFatal(t *testing.T) {
	// Fatalf 的级别必须是 FATAL（severity_number 21），否则
	// 「进程即将退出」这类不可恢复事件抓不到。
	if got := severityNumber(slog.LevelError + 4); got != 21 {
		t.Errorf("FATAL severity_number = %d, want 21", got)
	}
	if got := severityText(slog.LevelError + 4); got != "FATAL" {
		t.Errorf("severity_text = %q, want FATAL", got)
	}
}
