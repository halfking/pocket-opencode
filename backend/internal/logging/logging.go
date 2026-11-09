// Package logging — rule 71 统一日志标准的 Go 参考实现
//
// -----------------------------------------------------------------------------
// Purpose:        提供符合 rules/71-log-standard.md 的结构化日志初始化：
//
//	① 8 个必填字段（timestamp/severity_text/severity_number/msg/
//	   service/version/env/instance_id）自动注入；
//	② 扁平 JSONL 输出（单层，无嵌套）；
//	③ 敏感信息在 **handler 层**统一脱敏（调用点不会漏）；
//	④ OTel SeverityNumber 数值映射；
//	⑤ 可选 lumberjack 轮转落盘。
//
// Replaces:       (无)
// Replaced-by:    (无)
// Status:         active
//
// Changelog:
//
//	2026-11-09  v1.0  首版。随 rule 71 落地。
//	2026-11-09  v1.1  修：级别字段改由 standardHandler 注入，不再用 slog.Group
//	                     （Group 会产生嵌套对象，违反扁平单层约束）。
//
// -----------------------------------------------------------------------------
//
// 依赖：
//
//	gopkg.in/natefinch/lumberjack.v2   // 轮转（可选，文件落盘时才需要）
//
// 用法（服务 main.go 启动时调用一次）：
//
//	logging.Init(logging.Options{
//	    Service:   "llm-gateway",
//	    Version:   version.BuildVersion,   // 构建期注入，勿硬编码
//	    Env:       cfg.Env,                // local/dev/test/staging/prod
//	    Level:     slog.LevelInfo,
//	    LogFile:   cfg.LogFile,            // 空 = 只输出 stdout
//	})
//	defer logging.Shutdown()
//
// 之后全仓直接用标准库 slog：
//
//	slog.Info("user profile loaded", "user_id", "u_a3f9", "duration_ms", 42.5)
//	slog.Error("load user profile failed: context deadline exceeded",
//	    "error_code", "upstream.timeout", "error_type", "context.DeadlineExceeded")
//
// 字段名约定见 rules/71-log-standard.md §4；脱敏键见
// spec/logging/log-schema.yaml 的 redaction 段。
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

// Version 是本参考实现的版本，与 rule 71 同步演进。
const Version = "1.1"

// Options 是日志初始化参数。
type Options struct {
	// Service 是服务标识，须与 deploy/platform/services.json 的键一致（kebab-case）。
	Service string
	// Version 是服务版本，必须由构建期注入（如 ldflags -X），禁止硬编码。
	Version string
	// Env 是部署环境：local / dev / test / staging / prod。
	Env string
	// Level 是最低日志级别，默认 slog.LevelInfo。
	Level slog.Level
	// LogFile 是落盘路径。空表示只输出 stdout（容器化场景推荐）。
	LogFile string
	// MaxSizeMB / MaxBackups / MaxAgeDays / Compress 是轮转参数，
	// 取值遵循 rule 71 §6.1（默认 100MB × 10 份 / gzip / 7-30 天）。
	MaxSizeMB  int
	MaxBackups int
	MaxAgeDays int
	Compress   bool
	// DisableRedact 关闭脱敏。**仅允许在测试中使用**；生产开启会泄露密钥。
	DisableRedact bool
}

// validEnvs 是允许的环境取值（rule 71 §4.1）。
var validEnvs = map[string]bool{
	"local": true, "dev": true, "test": true, "staging": true, "prod": true,
}

// envNames 返回允许的环境取值（错误信息用）。
func envNames() []string {
	return []string{"local", "dev", "test", "staging", "prod"}
}

// NormalizeEnv 把部署环境归一到 rule 71 的允许集合。
//
// 各仓历史取值不一（"development" / "production" / "PROD" …），
// 不归一会让 AI 按 env 分组时出现 dev 与 development 两个桶。
// 无法识别时回落到 "local"——写入非法值会让日志被门禁判为不合规，
// 而「服务起不来」比「环境标记不准」严重得多。
//
// 与 templates/logging/go-zerolog/ 的同名函数保持一致（两个模板同源）。
func NormalizeEnv(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "production", "prod":
		return "prod"
	case "staging", "stage":
		return "staging"
	case "development", "dev":
		return "dev"
	case "testing", "test":
		return "test"
	case "local":
		return "local"
	default:
		return "local"
	}
}

// ---------------------------------------------------------------------------
// OTel SeverityNumber 数值表（rule 71 §2 / §4.1）
// TRACE 1-4 | DEBUG 5-8 | INFO 9-12 | WARN 13-16 | ERROR 17-20 | FATAL 21-24
// 实现取区间下界，便于数值比较（severity_number >= 17 即 ERROR 及以上）。
// ---------------------------------------------------------------------------

// severityNumber 把 slog 级别映射为 OTel SeverityNumber。
func severityNumber(l slog.Level) int {
	switch {
	case l < slog.LevelDebug:
		return 1 // TRACE
	case l < slog.LevelInfo:
		return 5 // DEBUG
	case l < slog.LevelWarn:
		return 9 // INFO
	case l < slog.LevelError:
		return 13 // WARN
	case l < slog.LevelError+4:
		return 17 // ERROR
	default:
		return 21 // FATAL
	}
}

// severityText 把 slog 级别映射为规范级别文本。
func severityText(l slog.Level) string {
	switch {
	case l < slog.LevelDebug:
		return "TRACE"
	case l < slog.LevelInfo:
		return "DEBUG"
	case l < slog.LevelWarn:
		return "INFO"
	case l < slog.LevelError:
		return "WARN"
	case l < slog.LevelError+4:
		return "ERROR"
	default:
		return "FATAL"
	}
}

// ---------------------------------------------------------------------------
// 脱敏（rule 71 §5）
//
// 关键设计：脱敏在 **handler 层**做，不在调用点做——做在调用点迟早有新人漏一处。
// ---------------------------------------------------------------------------

// redactedPlaceholder 是敏感值的统一替换文本。
const redactedPlaceholder = "[REDACTED]"

// sensitiveKeys 是需要脱敏的键名（小写比较）。
// 注意：**不要**把 "env" 列入——本规范的 env 是部署环境名，不是环境变量。
var sensitiveKeys = map[string]bool{
	"password": true, "passwd": true, "pwd": true,
	"secret": true, "token": true, "access_token": true, "refresh_token": true,
	"api_key": true, "apikey": true, "authorization": true,
	"cookie": true, "session_id": true, "private_key": true,
	"credential": true, "credential_id": true, "handoff_token": true,
}

// sensitivePatterns 是按**值**匹配的敏感模式（命中即替换整个值）。
var sensitivePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._\-]+`),                           // Bearer token
	regexp.MustCompile(`eyJ[A-Za-z0-9_\-]+\.eyJ[A-Za-z0-9_\-]+\.[A-Za-z0-9_\-]+`), // JWT
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),                                        // AWS access key
	regexp.MustCompile(`sk-[A-Za-z0-9_\-]{16,}`),                                  // OpenAI 风格 key
	regexp.MustCompile(`\b[A-Fa-f0-9]{32,}\b`),                                    // 长十六进制（疑似密钥）
}

// maxValueLength 是单字段值的长度上限（rule 71 §5.1）。
const maxValueLength = 2048

// truncate 截断超长值，防止日志爆炸。
func truncate(s string) string {
	if len(s) <= maxValueLength {
		return s
	}
	return s[:maxValueLength] + "…[truncated]"
}

// redactString 对字符串值做模式脱敏 + 截断。
func redactString(s string) string {
	for _, re := range sensitivePatterns {
		if re.MatchString(s) {
			return redactedPlaceholder
		}
	}
	return truncate(s)
}

// isSensitiveKey 判断键名是否需要脱敏。
func isSensitiveKey(key string) bool {
	return sensitiveKeys[strings.ToLower(key)]
}

// ---------------------------------------------------------------------------
// standardHandler：在 JSON handler 之上注入规范字段
//
// 为什么不用 slog.HandlerOptions.ReplaceAttr 注入多个字段？
// ReplaceAttr 每次只能返回**一个** Attr，无法同时产出
// severity_text + level + severity_number 三个顶层字段。
// 用 slog.Group 会产生嵌套对象，违反「扁平单层」约束。
// 因此这里包装一个 handler，在 Handle() 里向 record 追加属性。
// ---------------------------------------------------------------------------

// standardHandler 包装内层 handler，注入资源字段与级别字段。
type standardHandler struct {
	inner    slog.Handler
	res      *resource
	redact   bool
	inflight sync.Map // 防止重复注入（WithAttrs 已带资源字段时）
}

// resource 持有四个进程级常量字段（rule 71 §4.1：必须为常量，
// 才能升为 Loki 标签）。
type resource struct {
	service    string
	version    string
	env        string
	instanceID string
}

// buildInstanceID 生成实例唯一标识：hostname-pid-启动时刻。
func buildInstanceID() string {
	host, _ := os.Hostname()
	return fmt.Sprintf("%s-%d-%d", host, os.Getpid(), time.Now().Unix())
}

// Enabled 透传内层 handler 的级别判断。
func (h *standardHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.inner.Enabled(ctx, l)
}

// Handle 注入规范字段后交给内层 handler。
func (h *standardHandler) Handle(ctx context.Context, r slog.Record) error {
	// 级别三件套：severity_text（OTel 原生名）+ level（ECS/习惯别名）
	// + severity_number（数值）。两套查询风格命中同一行（rule 71 §3.2）。
	text := severityText(r.Level)
	r.AddAttrs(
		slog.String("severity_text", text),
		// level 是**小写**别名（ECS log.level 惯例）。
		// 与 severity_text 大小写不同是刻意的：两者各取所长，
		// 避免下游 LogQL（level=~"error|fatal"）因大小写不匹配漏数据。
		slog.String("level", strings.ToLower(text)),
		slog.Int("severity_number", severityNumber(r.Level)),
	)

	// 资源字段：仅当调用点未显式提供时注入。
	if !h.hasAttr(r, "service") {
		r.AddAttrs(slog.String("service", h.res.service))
	}
	if !h.hasAttr(r, "version") {
		r.AddAttrs(slog.String("version", h.res.version))
	}
	if !h.hasAttr(r, "env") {
		r.AddAttrs(slog.String("env", h.res.env))
	}
	if !h.hasAttr(r, "instance_id") {
		r.AddAttrs(slog.String("instance_id", h.res.instanceID))
	}

	// 脱敏在 handler 层做（调用点不会漏）。
	if h.redact {
		r = redactRecord(r)
	}

	return h.inner.Handle(ctx, r)
}

// hasAttr 判断 record 是否已含某键。
func (h *standardHandler) hasAttr(r slog.Record, key string) bool {
	found := false
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			found = true
			return false
		}
		return true
	})
	return found
}

// WithAttrs 透传并保持资源字段注入能力。
func (h *standardHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &standardHandler{
		inner:  h.inner.WithAttrs(attrs),
		res:    h.res,
		redact: h.redact,
	}
}

// WithGroup 透传。规范要求扁平单层，group 一律拍平——这里直接返回自身，
// 丢弃分组边界（内层 handler 不会产生嵌套）。
func (h *standardHandler) WithGroup(name string) slog.Handler {
	return h
}

// redactRecord 对 record 的所有属性做脱敏（敏感键 + 敏感值模式）。
func redactRecord(r slog.Record) slog.Record {
	out := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(redactAttr(a))
		return true
	})
	return out
}

// redactAttr 脱敏单个属性。
func redactAttr(a slog.Attr) slog.Attr {
	if isSensitiveKey(a.Key) {
		return slog.String(a.Key, redactedPlaceholder)
	}
	if a.Value.Kind() == slog.KindString {
		return slog.String(a.Key, redactString(a.Value.String()))
	}
	return a
}

// replaceAttr 把 slog 默认字段名换成规范名，并做值脱敏。
//
// 注意：级别字段由 standardHandler 注入（需要产出多个顶层字段），
// 这里只负责 time → timestamp 与 msg 的改名/脱敏。
func replaceAttr(groups []string, a slog.Attr) slog.Attr {
	// 只处理顶层属性；嵌套 group 一律拍平（规范要求扁平单层）。
	if len(groups) > 0 {
		return slog.Attr{}
	}
	switch a.Key {
	case slog.TimeKey:
		if t, ok := a.Value.Any().(time.Time); ok {
			return slog.String("timestamp", t.UTC().Format(time.RFC3339Nano))
		}
	case slog.LevelKey:
		// 级别由 standardHandler 产出（需要同时给出 severity_text / level /
		// severity_number 三个顶层字段，ReplaceAttr 做不到）。
		//
		// 注意：slog.LevelKey 的字面值就是 "level"，与我们要补的 ECS 别名
		// **同名**。因此不能只看 key——必须同时确认值的类型是 slog.Level
		// （slog 内建属性的形态）。standardHandler 补的别名值是 string，
		// 会正常通过。
		if _, isLevel := a.Value.Any().(slog.Level); isLevel {
			return slog.Attr{}
		}
	case slog.MessageKey:
		return slog.String("msg", redactString(a.Value.String()))
	}
	if isSensitiveKey(a.Key) {
		return slog.String(a.Key, redactedPlaceholder)
	}
	if a.Value.Kind() == slog.KindString {
		return slog.String(a.Key, redactString(a.Value.String()))
	}
	return a
}

// replaceAttrNoRedact 是关闭脱敏时的变体（仅测试用）。
func replaceAttrNoRedact(groups []string, a slog.Attr) slog.Attr {
	if len(groups) > 0 {
		return slog.Attr{}
	}
	switch a.Key {
	case slog.TimeKey:
		if t, ok := a.Value.Any().(time.Time); ok {
			return slog.String("timestamp", t.UTC().Format(time.RFC3339Nano))
		}
	case slog.LevelKey:
		return slog.Attr{}
	case slog.MessageKey:
		return slog.String("msg", a.Value.String())
	}
	return a
}

// ---------------------------------------------------------------------------
// 初始化
// ---------------------------------------------------------------------------

var (
	shutdownMu sync.Mutex
	activeFile *lumberjack.Logger
	activeRes  *resource
)

// Init 初始化全局 slog 记录器。必须且只需在服务启动时调用一次。
//
// 失败（参数非法 / 目录不可写）时返回 error，且**不改变** slog 当前 handler，
// 以免服务起不来；调用方应把错误显式打出来。
func Init(opts Options) error {
	if opts.Service == "" {
		return fmt.Errorf("logging: Service 不能为空")
	}
	if opts.Version == "" {
		return fmt.Errorf("logging: Version 不能为空（须构建期注入）")
	}
	if opts.Env == "" {
		return fmt.Errorf("logging: Env 不能为空")
	}
	// 环境名先归一再校验：各仓历史取值（development / production / PROD…）
	// 归一后都落在允许集合里，无需调用方自己翻译。
	opts.Env = NormalizeEnv(opts.Env)
	if !validEnvs[opts.Env] {
		return fmt.Errorf("logging: Env=%q 非法，允许 %v", opts.Env, envNames())
	}
	if opts.MaxSizeMB <= 0 {
		opts.MaxSizeMB = 100
	}
	if opts.MaxBackups <= 0 {
		opts.MaxBackups = 10
	}
	if opts.MaxAgeDays <= 0 {
		opts.MaxAgeDays = 7
	}

	res := &resource{
		service:    opts.Service,
		version:    opts.Version,
		env:        opts.Env,
		instanceID: buildInstanceID(),
	}

	var out io.Writer = os.Stdout
	var lj *lumberjack.Logger

	if opts.LogFile != "" {
		if err := os.MkdirAll(filepath.Dir(opts.LogFile), 0o755); err != nil {
			return fmt.Errorf("logging: create log dir: %w", err)
		}
		lj = &lumberjack.Logger{
			Filename:   opts.LogFile,
			MaxSize:    opts.MaxSizeMB,
			MaxBackups: opts.MaxBackups,
			MaxAge:     opts.MaxAgeDays,
			Compress:   opts.Compress,
			LocalTime:  true,
		}
		// 镜像到 stderr：日志文件配错时日志不丢（与 rule 71 §6 一致）。
		out = io.MultiWriter(lj, os.Stderr)
	}

	attrFn := replaceAttr
	if opts.DisableRedact {
		attrFn = replaceAttrNoRedact
	}

	inner := slog.NewJSONHandler(out, &slog.HandlerOptions{
		Level:       opts.Level,
		ReplaceAttr: attrFn,
	})
	slog.SetDefault(slog.New(&standardHandler{
		inner:  inner,
		res:    res,
		redact: !opts.DisableRedact,
	}))

	shutdownMu.Lock()
	activeFile = lj
	activeRes = res
	shutdownMu.Unlock()

	slog.Info("logging initialized",
		"service", res.service,
		"version", res.version,
		"env", res.env,
		"instance_id", res.instanceID,
		"log_file", opts.LogFile,
	)
	return nil
}

// Shutdown 关闭轮转文件。应在进程退出前调用。
func Shutdown() error {
	shutdownMu.Lock()
	lj := activeFile
	activeFile = nil
	activeRes = nil
	shutdownMu.Unlock()
	if lj == nil {
		return nil
	}
	// lumberjack.Logger 只提供 Close（无 Sync）；关闭即完成 flush。
	if err := lj.Close(); err != nil {
		return err
	}
	return nil
}

// ActiveResource 返回当前生效的资源字段（测试与诊断用）。
func ActiveResource() (service, version, env, instanceID string) {
	shutdownMu.Lock()
	defer shutdownMu.Unlock()
	if activeRes == nil {
		return "", "", "", ""
	}
	return activeRes.service, activeRes.version, activeRes.env, activeRes.instanceID
}

// ---------------------------------------------------------------------------
// 链路关联
//
// 约定：middleware 用 logging.WithTrace(ctx, traceID, requestID) 写入，
// 由调用方在打点时用 slog.InfoContext(ctx, ...) 携带，实现跨服务链路关联。
// ---------------------------------------------------------------------------

type traceKey struct{}
type requestKey struct{}

// WithTrace 把 trace_id / request_id 放进 context。
func WithTrace(ctx context.Context, traceID, requestID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if traceID != "" {
		ctx = context.WithValue(ctx, traceKey{}, traceID)
	}
	if requestID != "" {
		ctx = context.WithValue(ctx, requestKey{}, requestID)
	}
	return ctx
}

// TraceFrom 从 context 取 trace_id。
func TraceFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(traceKey{}).(string); ok {
		return v
	}
	return ""
}

// RequestFrom 从 context 取 request_id。
func RequestFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(requestKey{}).(string); ok {
		return v
	}
	return ""
}
