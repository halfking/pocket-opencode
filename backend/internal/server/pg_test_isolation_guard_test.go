package server

// 仓库级护栏：PG 测试不得直接落到调用方 DSN 指向的 schema 上。
//
// 背景（2026-10-02 审计实测）：本仓库的惯例是**同一个 DSN 既喂服务也喂测试**，
// 所以 `POCKET_TEST_POSTGRES_DSN` 的 search_path 完全可能就是生产 schema。
// 当时有 5 个测试文件没有任何 schema 隔离，其中
// internal/chatagent 的夹具还对**活表**执行 DELETE。
//
// 献祭 schema 对照实验（DSN 的 search_path 指向 sacrificial_prod）：
//
//	修复前：测试报告 ok，而 sacrificial_prod 里
//	        customer-success-manager 被 DELETE 掉、
//	        新建了 finance_transactions / scheduled_tasks /
//	        scheduled_task_runs / scheduled_task_tombstones 四张表、
//	        留下 1 行 finance_transactions；一个测试 schema 都没建。
//	修复后：canary 存活、四张表一张没建、残留测试 schema 0 个。
//
// 这个护栏把该结论固化成**结构性质**，而不是靠人记住逐个文件检查：
//
//	1. 任何 _test.go 都不得读 POCKET_POSTGRES_DSN（服务自己的生产连接串）。
//	   这是最危险的一行，且对测试没有任何正当用途——`436af42` 之后
//	   internal/scheduledtask 与 internal/server/scheduled_task_integration
//	   正是靠它零配置地打到生产库。
//	2. 任何 _test.go 若打开 PostgreSQL 连接，就必须把 search_path 钉到
//	   自己生成的 `*_test_` schema 上；否则必须在下面的 allowlist 里，
//	   并写明它为什么安全。
//
// 新增 PG 测试助手时若忘了隔离，本护栏会在 CI 里直接失败。

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// pgOpenRe 匹配「打开 PostgreSQL 连接」的写法。
var pgOpenRe = regexp.MustCompile(`pgxpool\.New|pgx\.Connect|sql\.Open\(\s*"postgres`)

// isolatedSchemaRe 要求文件里存在自建测试 schema 的字面量前缀。
var isolatedSchemaRe = regexp.MustCompile(`"(\w*_test_)`)

// productionDSNRe 是被禁止出现在测试里的生产连接串环境变量。
var productionDSNRe = regexp.MustCompile(`os\.Getenv\(\s*"POCKET_POSTGRES_DSN"`)

// dsnSearchPathAppendRe 匹配「往 DSN 字符串后面拼 search_path 参数」。
//
// 这条规则来自一个真实的漏网：internal/quota 的助手写的是
// pgxpool.New(ctx, dsn+"&search_path="+schema)。它的 schema 名是对的，
// 规则 2 因此放行；但本仓库的 DSN 往往**已经**带 search_path（生产
// schema），拼接后出现两个同名参数、pgx 取第一个，隔离静默失效。
// 献祭 schema 实测：quota_budgets 被建到了 sacrificial_prod 里，且
// TestPGStore_BudgetsFor_AcceptsZeroPeriod 随之失败。
//
// 正确写法是覆盖式设置，见 pgxpool.ParseConfig 后的
// RuntimeParams["search_path"] = schema + ",public"。
var dsnSearchPathAppendRe = regexp.MustCompile(`dsn\s*\+\s*"&search_path=|dsn\s*\+\s*` + "`" + `&search_path=`)

// pgSafeWithoutIsolation 逐个列出「打开 PG 连接但不隔离 schema 仍然安全」的
// 文件，并写明理由。新增条目必须给出可核查的理由，不能写成"应该没事"。
var pgSafeWithoutIsolation = map[string]string{
	// 需要两个显式开关才会运行：POCKET_DIAG_*=1 且 POCKET_REAL_MAIL_DSN。
	// 第三个开关的测试文件永远 skip。
	"internal/email/diag_pop3_backfill_test.go": "需 POCKET_DIAG_POP3_BACKFILL=1 + POCKET_REAL_MAIL_DSN 双重开关",
	"internal/email/diag_pop3_invoice_test.go":   "需 POCKET_DIAG_POP3=1 + POCKET_REAL_MAIL_DSN 双重开关",

	// 需要 build tag `greenmail`，`go test ./...` 永远不会编译它；
	// 且只删自己 acctID 名下的行。
	"internal/email/fetcher_greenmail_test.go": "需 build tag greenmail + PG_DSN，go test ./... 不编译",

	// 下面两个是**有意**指向真实 schema 的只读诊断探针——指向真实库正是
	// 它们的目的，所以不能要求它们自建隔离 schema。两者均无任何写语句
	// （INSERT/UPDATE/DELETE/DROP/CREATE/TRUNCATE 一个都没有），且门控极严。
	"internal/email/diag_kxpms_test.go": "只读真实库探针：无写语句；需 POCKET_DIAG_ACCOUNT + POCKET_DIAG_ALLOW=1 + POCKET_REAL_MAIL_DSN + POCKET_DIAG_DATA_DIR",
	"internal/email/spam_realdata_test.go": "只读真实库探针：无写语句；需 POCKET_REAL_MAIL_DSN + POCKET_REAL_MAIL_SCHEMA（目的就是读真实 schema）",

	// vendored 第三方代码，需 -tags=integration + IDENTITY_SHADOW_DSN。
	"third_party/identity-go/shadow/dao_test.go": "vendored 第三方，需 -tags=integration + IDENTITY_SHADOW_DSN",
}

func TestPGTestsNeverTargetTheProductionSchema(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test file")
	}
	// backend/internal/server/xxx_test.go -> backend
	backendRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))

	var checked int
	err := filepath.Walk(backendRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			base := info.Name()
			// 跳过依赖与构建产物：它们不是本仓库的测试助手。
			if base == "node_modules" || base == "testdata" || base == "vendor" || strings.HasPrefix(base, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, rerr := filepath.Rel(backendRoot, path)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		// 本文件自身必然含规则 3 的反例字面量（那是它的文档注释），跳过。
		if rel == "internal/server/pg_test_isolation_guard_test.go" {
			return nil
		}

		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		src := string(raw)
		checked++

		// 规则只对**可执行代码**生效：注释里出现的反例（说明"不要这么写"）
		// 不该把文件判红。按行剥掉纯注释行（// 开头、或块注释的 * 开头）。
		var codeLines []string
		for _, ln := range strings.Split(src, "\n") {
			t := strings.TrimSpace(ln)
			if strings.HasPrefix(t, "//") || strings.HasPrefix(t, "*") || t == "/*" {
				continue
			}
			codeLines = append(codeLines, ln)
		}
		code := strings.Join(codeLines, "\n")

		// 规则 1：任何测试都不得读生产 DSN 变量。
		if productionDSNRe.MatchString(code) {
			t.Errorf("%s: 测试读取了 POCKET_POSTGRES_DSN（服务自己的生产连接串）。\n"+
				"  测试必须只认 POCKET_TEST_POSTGRES_DSN：同一个 DSN 既喂服务也喂测试时，\n"+
				"  读生产变量等于零配置地把测试打到生产库。", rel)
		}

		// 规则 3：不得靠拼接 DSN 字符串来设置 search_path（见上面的注释）。
		if dsnSearchPathAppendRe.MatchString(code) {
			t.Errorf("%s: 通过拼接 DSN 字符串（dsn+\"&search_path=\"）来隔离 schema。\n"+
				"  本仓库的 DSN 往往已经带 search_path，拼接会产生两个同名参数、pgx 取第一个，\n"+
				"  隔离静默失效而测试照样报告 ok。改用 ParseConfig 后的\n"+
				"  RuntimeParams[\"search_path\"] = schema + \",public\"。", rel)
		}

		// 规则 2：打开 PG 连接就必须隔离 schema。
		if !pgOpenRe.MatchString(code) {
			return nil
		}
		if isolatedSchemaRe.MatchString(code) {
			return nil
		}
		if reason, ok := pgSafeWithoutIsolation[rel]; ok {
			t.Logf("allowlist: %s — %s", rel, reason)
			return nil
		}
		t.Errorf("%s: 打开了 PostgreSQL 连接但没有把 search_path 钉到自建的 `*_test_` schema。\n"+
			"  没有隔离时，测试的建表迁移与 DELETE 会落到 DSN search_path 指向的 schema\n"+
			"  （在本仓库的惯例下那就是生产库），而测试会报告 ok。\n"+
			"  修法见 internal/task/store_test.go 等 20 处已隔离的助手；\n"+
			"  若本文件确实安全，请连同理由加入 pgSafeWithoutIsolation。", rel)
		return nil
	})
	if err != nil {
		t.Fatalf("walk backend: %v", err)
	}
	if checked < 20 {
		t.Errorf("只扫描到 %d 个测试文件，路径推导可能不对（本护栏会因此失明）", checked)
	}
	t.Logf("已扫描 %d 个 _test.go", checked)
}
