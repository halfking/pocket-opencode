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
//	3. 不得靠拼接 DSN 字符串（dsn+"&search_path="）来隔离 schema——
//	   本仓库的 DSN 常已带 search_path，拼接出两个同名参数、pgx 取第一个，
//	   隔离静默失效。
//	4. **在 allowlist 里豁免「不隔离 schema」，不等于豁免「写」。**
//	   规则 1-3 全部通过 allowlist 一次性放行，那个设计让 allowlist 变成
//	   完全的盲区：负控实测（2026-10-02）往一个已登记的只读探针注入
//	   `pool.Exec(ctx, "DELETE FROM email_accounts")`，护栏**依然绿**。
//	   规则 4 独立生效：allowlist 内的文件出现 SQL 写语句时，
//	   必须把写语句登记进 pgAllowlistedWrites 并说明它为什么安全，否则判红。
//	   豁免的语义从此是「不豁免写」。
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

// productionDSNRe 匹配测试**代码**里对生产 DSN 变量字面量的引用。
//
// 2026-10-02 实测的漏网（这条规则此前等于不存在）：13 个助手写成
//
//	for _, k := range []string{"POCKET_TEST_POSTGRES_DSN", "POCKET_POSTGRES_DSN"} {
//	    if v := os.Getenv(k); v != "" { ... }
//
// 字面量在切片里、传给 Getenv 的是变量 k，所以旧判据
// `os\.Getenv\(\s*"POCKET_POSTGRES_DSN"` 一条都匹配不到。实测后果不是理论上的：
// 生产库里至今躺着 2 个 `meeting_test_*` schema（各带 2 张表）——有人只带了
// POCKET_POSTGRES_DSN 就跑了 `go test ./internal/meeting/`，测试在生产**数据库**里
// 建了 schema。护栏写了、CI 也在跑，却一条都没拦住。
//
// 现在改为「字面量作为实参出现」（前面是 `(` 或 `,`）：
//   - `os.Getenv("POCKET_POSTGRES_DSN"`      → 命中（直接读）
//   - `[]string{"..._TEST_...", "POCKET_..."}` → 命中（切片回退）
//   - `t.Skip("... or POCKET_POSTGRES_DSN not set")` → 不命中（嵌在更长的提示文本里）
var productionDSNRe = regexp.MustCompile(`(?:\(\s*|,\s*)"POCKET_POSTGRES_DSN"`)

// productionDSNWriteRe 匹配**写入**该变量的合法用法：测试用假值覆盖环境变量。
//
// 必须以 `\($` 结尾锚定：规则 1 的回看窗口是 `code[from:h[0]]`，而 h[0] 指向
// 匹配串的**第一个字符**，也就是那个 `(`。所以窗口的末尾正好是 `...Setenv`
// 而**不含** `(` 与引号——早先写成 `(?:t|os)\.Setenv\(\s*"POCKET_POSTGRES_DSN"`
// 时它永远匹配不上，排除逻辑形同虚设（config_test.go 被误判就是它暴露的）。
var productionDSNWriteRe = regexp.MustCompile(`(?:t|os)\.Setenv\(\s*$`)

// pgProductionDSNWriteOnly 列出「出现生产 DSN 字面量但只写不读」的文件。
// 判据是"字面量作为实参出现"，它分不清读与写，所以这类文件必须显式登记理由。
var pgProductionDSNWriteOnly = map[string]string{
	// 两处都是写：TestLoadDefaults 把一批 env key（含 POCKET_POSTGRES_DSN）
	// 逐个 t.Setenv(key, "") 清空以验证默认值；TestLoadProductionAlias 用
	// t.Setenv 写入假 DSN "postgres://user:pass@localhost/pocket" 验证 prod 别名。
	// 这个文件不打开任何 PG 连接（无 pgxpool.New / pg.Conn / sql.Open）。
	"internal/config/config_test.go": "只用 t.Setenv 写空值/假值来验证配置默认值与别名，不读也不连库",
}

// stripGoComments 在匹配前剥掉注释。
//
// 旧实现只剥「整行是注释」的行，剥不掉两件事，而这两件都能让违规代码隐身：
//   - 行尾注释：`schema := x // 顺便说一句 POCKET_POSTGRES_DSN`
//   - 块注释的中间行（不以 * 开头）
// 另外 `//` 必须要求前面不是 `:`，否则 `"https://..."` 会被当成注释起点
// 把整行截断——那是「因为判据太宽而漏报」，方向同样危险。
func stripGoComments(src string) string {
	var b strings.Builder
	b.Grow(len(src))
	inBlock := false
	for _, ln := range strings.Split(src, "\n") {
		if inBlock {
			i := strings.Index(ln, "*/")
			if i < 0 {
				continue
			}
			ln = " " + ln[i+2:]
			inBlock = false
		}
		if i := strings.Index(ln, "/*"); i >= 0 {
			if j := strings.Index(ln[i+2:], "*/"); j >= 0 {
				ln = ln[:i] + " " + ln[i+2+j+2:]
			} else {
				ln = ln[:i]
				inBlock = true
			}
		}
		for i := 0; i < len(ln); i++ {
			if ln[i] == '/' && i+1 < len(ln) && ln[i+1] == '/' && (i == 0 || ln[i-1] != ':') {
				ln = ln[:i]
				break
			}
		}
		b.WriteString(ln)
		b.WriteByte('\n')
	}
	return b.String()
}

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
	"internal/email/diag_pop3_invoice_test.go":  "需 POCKET_DIAG_POP3=1 + POCKET_REAL_MAIL_DSN 双重开关",

	// 需要 build tag `greenmail`，`go test ./...` 永远不会编译它；
	// 且只删自己 acctID 名下的行。
	"internal/email/fetcher_greenmail_test.go": "需 build tag greenmail + PG_DSN，go test ./... 不编译",

	// diag_snippet_leak_test.go 是**唯一**带写语句的诊断探针，因此不能和上面
	// 那些「无写语句」的条目混为一谈。逐条核过：
	//   · 全文只有 1 条写语句（UPDATE email_accounts SET last_synced_uid=0,
	//     last_synced_at=0），没有 INSERT/DELETE/DROP/CREATE/TRUNCATE/ALTER；
	//   · 该写语句被**三重开关**挡住：POCKET_REAL_MAIL_DSN +
	//     POCKET_DIAG_RESET_ACCOUNT + POCKET_DIAG_ALLOW_RESET=1；
	//   · 写路径额外拒绝 schema 缺省值——本文件 schema 缺省是 opencode_pocket
	//     （生产 schema），而 SQL 支持 who='ALL' 改写全部账户。2026-10-02 已补
	//     这道闸：此前只要设 DSN + RESET_ACCOUNT 就能把生产库所有账户的同步
	//     进度归零，触发全量重拉；
	//   · 读路径（统计 rawMIME 摘要数）仍只需 POCKET_REAL_MAIL_DSN，且只用
	//     pool.Query。
	// 它要的就是读真实 schema 并（可选）改真实同步进度，自建隔离 schema 反而
	// 会让诊断查了个空库、输出「数据没了」的假结论。
	"internal/email/diag_snippet_leak_test.go": "诊断探针：1 条 UPDATE（重置 last_synced_uid 以便用当前二进制重写摘要），被 POCKET_REAL_MAIL_DSN + POCKET_DIAG_RESET_ACCOUNT + POCKET_DIAG_ALLOW_RESET=1 三重开关挡住，且写路径拒绝 schema 缺省值（缺省=生产库 opencode_pocket 且 who='ALL' 可改全部账户）；读路径只读",

	// 下面两个是**有意**指向真实 schema 的只读诊断探针——指向真实库正是
	// 它们的目的，所以不能要求它们自建隔离 schema。两者均无任何写语句
	// （INSERT/UPDATE/DELETE/DROP/CREATE/TRUNCATE 一个都没有），且门控极严。
	"internal/email/diag_kxpms_test.go":    "只读真实库探针：无写语句；需 POCKET_DIAG_ACCOUNT + POCKET_DIAG_ALLOW=1 + POCKET_REAL_MAIL_DSN + POCKET_DIAG_DATA_DIR",
	"internal/email/spam_realdata_test.go": "只读真实库探针：无写语句；需 POCKET_REAL_MAIL_DSN + POCKET_REAL_MAIL_SCHEMA（目的就是读真实 schema）",

	// internal/email/diag_credential_health_test.go（2026-10-02 新增，被本护栏判红后逐项核对）：
	//   · 只用 pool.Query，**一次 Exec 都没有**（全文件扫
	//     INSERT|UPDATE|DELETE|DROP|TRUNCATE|ALTER|CREATE|GRANT：0 命中），
	//     唯一的 SQL 是一条 SELECT ... FROM email_accounts ORDER BY email_address；
	//   · 门控是 POCKET_REAL_MAIL_DSN + POCKET_REAL_DATA_DIR，**不认**
	//     POCKET_TEST_POSTGRES_DSN，因此本地 `go test ./...` 不会踩到；
	//   · 它的目的就是读生产 schema 里 email_accounts 的凭据密文并用本地
	//     dataDir 里的 email_master.key 解密——自建隔离 schema 会让「查真实库
	//     的凭据」这件事失去意义（隔离库里根本没有真实账户）。这与
	//     spam_realdata_test.go / ledger_realdata_diag_test.go 同理；
	//   · **绝不打印明文**：只输出「能否解密 / 明文长度 / 是否符合专用密码形态」，
	//     长度与形态足以区分「完整密码」与「截断/存错字段」，无需看到内容。
	//
	// 【盲区已于 2026-10-02 补上：规则 4】上面「0 写语句」此前只是**一次性观察**，
	// 列入本表后护栏**完全跳过**该文件——是「不看」，不是「检查后放行」。
	// 当时的负控实测：往该文件注入 `pool.Exec(ctx, "DELETE FROM email_accounts")`，
	// 护栏**依然绿**。因此「无写语句」没有任何机器维持。
	//
	// 现在的机制：规则 4 独立于规则 2 生效——在 pgSafeWithoutIsolation 里的文件
	// 仍必须把写语句登记进 pgAllowlistedWrites，否则判红。负控复测（2026-10-02
	// 第二轮，同一处注入）：护栏如期转红并点名本文件。
	// 语义从此是「不豁免写」，而不是「不豁免一切」。
	"internal/email/diag_credential_health_test.go": "只读真实库探针：无写语句（仅 SELECT）；需 POCKET_REAL_MAIL_DSN + POCKET_REAL_MAIL_SCHEMA + POCKET_REAL_DATA_DIR（判定库中凭据密文是否完好，隔离库无真实账户可查）；绝不打印明文。**规则 4 会在本文件出现写语句时判红（负控实测会转红），但豁免表本身不检查 schema 隔离是否真的到位**",

	// 2026-10-02 新增的发票抽取诊断。逐项核对过：
	//   · 4 处 DB 调用全是 pool.Query / pool.QueryRow，**0 写语句**
	//     （扫 INSERT|UPDATE|DELETE|DROP|TRUNCATE|ALTER|CREATE：0 命中；
	//     唯一的 "DELETE" 字样是 L133 `COALESCE(deleted_at,0)=0` 这个读条件，
	//     不是 DELETE 语句——这类误报是本护栏要求人写理由的原因之一）；
	//   · 双开关门控：POCKET_REAL_MAIL_DSN + POCKET_REAL_DATA_DIR，
	//     缺任一即 t.Skip，且 L47 显式把 search_path 指向
	//     POCKET_REAL_MAIL_SCHEMA —— 它要读的就是真实库里的真实发票，
	//     自建隔离 schema 会让它「查了个空库」并输出「发票都没了」的假结论。
	// 同上：**规则 4 会检查本文件的写语句**（2026-10-02 补上，见
	// diag_credential_health_test.go 条目的说明）。豁免的只是「自建隔离 schema」。
	"internal/email/diag_real_invoice_extract_test.go": "只读真实库诊断：0 写语句，4 处 pool.Query；需 POCKET_REAL_MAIL_DSN + POCKET_REAL_DATA_DIR 双重开关（读真实库里的真实发票，隔离库会让它输出「发票都没了」的假结论）。**规则 4 会在本文件出现写语句时判红**",

	// 2026-10-02 新增（发票准入门取证）：
	//   · 0 写语句，1 处 pool.Query（一条 SELECT），.Exec( 出现 0 次；
	//   · 门控只有 POCKET_REAL_MAIL_DSN（schema 缺省 opencode_pocket），
	//     与 CI 设的 POCKET_TEST_POSTGRES_DSN 是两个不同变量，CI 里恒 skip；
	//   · 显式把 search_path 指向 POCKET_REAL_MAIL_SCHEMA——它要读的就是
	//     真实 schema，自建隔离 schema 会让它输出「发票都没了」的假结论。
	// 同上：**规则 4 会检查本文件的写语句**。
	"internal/email/diag_real_invoice_gate_test.go": "只读真实库诊断：0 写语句，1 处 pool.Query（单条 SELECT）；需 POCKET_REAL_MAIL_DSN（schema 缺省 opencode_pocket）。产出是准入门交叉表，不落库。**规则 4 会在本文件出现写语句时判红**",

	// 下面三个是 2026-10-02 合入 feat/mail-config-deploy 时被本护栏判红的，
	// 逐个核过后确认安全，理由可核查：
	//   · 只用 pool.Query，**一次 Exec 都没有**（2026-10-02 全文件扫
	//     INSERT|UPDATE|DELETE|DROP|TRUNCATE|ALTER|CREATE|GRANT：0 命中）；
	//   · 门控变量是 POCKET_REAL_MAIL_DSN，与测试用的 POCKET_TEST_POSTGRES_DSN
	//     是**两个不同的变量**，而 CI（backend-pg.yml）只设后者，
	//     所以这三个文件在任何 CI 与常规 go test 中都 skip；
	//   · 它们要的就是「读真实 schema」，所以显式把 search_path 指向
	//     POCKET_REAL_MAIL_SCHEMA（缺省 opencode_pocket），自建隔离 schema
	//     反而会让诊断「查了个空库」并输出「数据没了」的假结论
	//     ——ledger_realdata_diag_test.go 的注释记的就是这个坑。
	"internal/email/diag_schema_present_test.go":    "只读真实库探针：无写语句；需 POCKET_REAL_MAIL_DSN + POCKET_DIAG_SCHEMA（目的是查真实 schema 在不在）",
	"internal/email/ledger_realdata_diag_test.go":   "只读真实库探针：无写语句；需 POCKET_REAL_MAIL_DSN + POCKET_REAL_MAIL_SCHEMA（核对台账合计口径在真实数据上的变化）",
	"internal/email/reminder_notified_diag_test.go": "只读真实库探针：无写语句；需 POCKET_REAL_MAIL_DSN（核对 remindersSent 计数在真实数据上的来源）",

	// ===== 2026-10-02 合并 email 分支时本护栏新增判红的 7 个，逐个核过 =====
	//
	// 背景：email 分支上有一批 2026-10-01 的只读诊断探针，此前 main 的护栏
	// 没跑到它们身上；合并后护栏与探针第一次同处一个包，于是报出。
	// 下面 7 个**性质各不相同**，不能一刀切，理由逐个写清。
	//
	// 第 1 组：纯只读，无任何写语句（INSERT|UPDATE|DELETE|DROP|CREATE|TRUNCATE
	// 全文件 0 命中），且都要显式开关才运行。
	"internal/email/diag_backfill_align_test.go": "只读真实库诊断：全文件 0 写语句；需显式 diag 开关 + POCKET_REAL_MAIL_DSN（核对 backfill 补采与对齐口径的差异）",
	"internal/email/diag_dup_report_test.go":      "只读真实库诊断：全文件 0 写语句；需显式 diag 开关 + POCKET_REAL_MAIL_DSN（重复副本预演报表，产出为报告不落库）",
	"internal/email/diag_merge_plan_test.go":       "只读真实库诊断：全文件 0 写语句；需显式 diag 开关 + POCKET_REAL_MAIL_DSN（合并迁移**预演**，只出计划不执行）",
	"internal/email/diag_rest_dupes_test.go":      "只读真实库诊断：全文件 0 写语句；需显式 diag 开关 + POCKET_REAL_MAIL_DSN（剩余重复候选的定性排查）",
	"internal/email/realprobe_test.go":            "只读真实库探针：0 写语句；search_path 显式指向 POCKET_REAL_MAIL_SCHEMA（目的就是读真实 schema，自建隔离 schema 反而会查出「数据没了」的假结论）",
	//
	// 第 2 组：**这个文件本身是隔离助手**，它实现隔离而不是违反隔离。
	// pgscope_test.go 提供 newScopedPool（search_path 只指向调用方建好的
	// schema）与 dropScopedSchema（只删调用方传进来的那个 schema 名）。
	// 护栏的判据是文本扫写语句，扫到 `DROP SCHEMA IF EXISTS ... CASCADE`
	// 就判红 —— 而这恰恰是「收尾只 DROP 自己那一个 schema」的正确模式，
	// 与 chatagent/store_test.go 里的同一模式一样。属误报。
	"internal/email/pgscope_test.go": "**隔离助手本身**：提供 newScopedPool（search_path 只指向调用方建好的 schema）与 dropScopedSchema（只删调用方传进来的 schema 名）。文本扫到 DROP SCHEMA 属误报——那正是「只删自己建的」的安全收尾模式",
	//
	// 第 3 组：**会在真实库执行写操作**，所以理由必须说清三道闸门，
	// 不能套用「只读」那套话术。三道闸门都在代码里可查：
	//   1. POCKET_DIAG_MERGE_EXEC=1 显式开关，不设直接 return（diag_merge_exec_test.go:48）；
	//   2. POCKET_REAL_MAIL_DSN + POCKET_REAL_MAIL_SCHEMA 显式指定目标库；
	//   3. 备份表必须**存在且非空**，否则 t.Fatal 拒绝执行任何写操作
	//      （:73-81，「没有回滚路径就不执行写操作」）。
	// 三道之外它只打墓碑（deleted_at）保留数据，recover 语句在文件里以
	// t.Logf 形式给出（pool.Exec 出现 0 次）。合并重复副本的**执行**仍
	// 待单独授权 —— 本条 allowlist 只表示「护栏不该因它未自建 schema 而报红」，
	// 不构成对该写操作的授权。
	"internal/email/diag_merge_exec_test.go": "**会在真实库写**（合并重复副本，只打墓碑）：三道闸门——需 POCKET_DIAG_MERGE_EXEC=1 显式开关；需 POCKET_REAL_MAIL_DSN + POCKET_REAL_MAIL_SCHEMA；备份表不存在或为空时 t.Fatal 拒绝执行（无回滚路径不写）。pool.Exec 出现 0 次，recover 语句以 t.Logf 形式给出。**待单独授权执行**",

	// vendored 第三方代码，需 -tags=integration + IDENTITY_SHADOW_DSN。
	"third_party/identity-go/shadow/dao_test.go": "vendored 第三方，需 -tags=integration + IDENTITY_SHADOW_DSN",
}

// sqlWriteRe 匹配 SQL 写语句动词，用于**规则 4**（见下）。
//
// 【正则陷阱，勿改回单字符类】早期版本写的是
//
//	\bUPDATE\s+[A-Za-z_]\b
//
// `[A-Za-z_]` 只吃**一个**字符 'e'，而尾部的 \b 要求 'e' 后面是**非单词字符**
// ——实际是 "email_accounts" 里 'mail_accounts' 的 'm'，属于单词字符，于是 \b
// 恒不成立。**该分支恒假**。同族的 INSERT\s+INTO / DELETE\s+FROM /
// CREATE\s+(TABLE|SCHEMA|INDEX) 一并恒假。
//
// 后果不是理论上的：internal/email/diag_snippet_leak_test.go 里那条真实存在的
// `UPDATE email_accounts SET last_synced_uid = 0` 就在判据眼皮底下，而普查
// 报它「0 写语句」——与 allowlist 里写的「全文只有 1 条写语句」直接矛盾。
// 静默失明，没有任何报错。
//
// 正确写法是 \w+（贪婪吃掉整个标识符）。TestCensusSQLWriteReIsNotVacuous
// 钉住这一点。
var sqlWriteRe = regexp.MustCompile(`(?i)\b(INSERT\s+INTO|UPDATE\s+\w+|DELETE\s+FROM|DROP\s+(?:TABLE|SCHEMA|INDEX)\b|CREATE\s+(?:TABLE|SCHEMA|INDEX)\b|TRUNCATE\b|ALTER\s+TABLE\b|GRANT\b)`)

// pgAllowlistedWrites 逐个登记「在 pgSafeWithoutIsolation 里、且确实含写语句」
// 的文件，连同它为什么安全。与 pgSafeWithoutIsolation 的区别是**方向**：
//
//	pgSafeWithoutIsolation 回答「不隔离 schema 为什么安全」（规则 2）
//	pgAllowlistedWrites   回答「有写语句为什么安全」（规则 4）
//
// 两个 map 独立，意味着**豁免不豁免写**是分开裁定的：一个文件可以
// 「不豁免隔离」的同时被要求把写语句列进来。旧设计里「在 allowlist 里」
// 一次性放行了所有检查，那正是盲区的来源。
var pgAllowlistedWrites = map[string]string{
	// 需 build tag greenmail + PG_DSN，`go test ./...` 永不编译；
	// 写的对象是自己刚 CREATE 的 schema，且只删自己 acctID 名下的行。
	"internal/email/fetcher_greenmail_test.go": "build tag greenmail（go test ./... 不编译）；CREATE SCHEMA 的是自己刚建的隔离 schema，DELETE 只删自己 acctID 名下的行",

	// 1 条 UPDATE（重置 last_synced_uid 以便用当前二进制重写摘要），三重开关：
	// POCKET_REAL_MAIL_DSN + POCKET_DIAG_RESET_ACCOUNT + POCKET_DIAG_ALLOW_RESET=1。
	// 写路径额外拒绝 schema 缺省值（缺省=生产库 opencode_pocket，且 who='ALL'
	// 可改写全部账户的同步进度）。
	"internal/email/diag_snippet_leak_test.go": "1 条 UPDATE（重置 last_synced_uid），被 POCKET_REAL_MAIL_DSN + POCKET_DIAG_RESET_ACCOUNT + POCKET_DIAG_ALLOW_RESET=1 三重开关挡住；写路径拒绝 schema 缺省值（缺省=生产库 opencode_pocket 且 who='ALL' 可改全部账户）",

	// 隔离助手本身：dropScopedSchema 只 DROP 调用方传进来的那个 schema 名。
	"internal/email/pgscope_test.go": "隔离助手本身：dropScopedSchema 只 DROP 调用方传进来的 schema 名（'DROP SCHEMA IF EXISTS '+schema+' CASCADE'），是「只删自己建的」的安全收尾模式",

	// 只读探针，**可执行代码里 0 写语句**。但它的输出模板里含 CREATE SCHEMA
	// 字样（t.Logf「重启 pocketd 会重新 CREATE SCHEMA 并跑迁移」），
	// 规则 4 的正则分不出字符串是 SQL 还是提示文本，所以登记在册。
	// 登记的是「这条 CREATE SCHEMA 是提示文本」这个事实，不是豁免写权限。
	"internal/email/diag_schema_present_test.go": "只读真实库探针，可执行代码 0 写语句；登记原因是输出模板 t.Logf 里含「重启 pocketd 会重新 CREATE SCHEMA 并跑迁移」这句**提示文本**（非可执行 SQL），规则 4 的正则无法区分字符串用途",

	// 合并重复副本的执行探针，三道闸门：POCKET_DIAG_MERGE_EXEC=1 显式开关；
	// POCKET_REAL_MAIL_DSN + POCKET_REAL_MAIL_SCHEMA 显式指定目标库；
	// 备份表不存在或为空时 t.Fatal 拒绝执行（无回滚路径不写）。
	// 它只打墓碑（deleted_at）保留数据；文件里的 UPDATE 是**回滚语句**，
	// 以 t.Logf 形式给出（pool.Exec 出现 0 次），不是可执行写操作。
	// **待单独授权执行**。
	"internal/email/diag_merge_exec_test.go": "三道闸门——POCKET_DIAG_MERGE_EXEC=1 显式开关 + POCKET_REAL_MAIL_DSN/POCKET_REAL_MAIL_SCHEMA 显式指定 + 备份表不存在或为空时 t.Fatal 拒绝执行；只打墓碑（deleted_at）保留数据，文件里的 UPDATE 是以 t.Logf 给出的回滚语句而非可执行写操作。**待单独授权执行**",

	// vendored 第三方，需 -tags=integration + IDENTITY_SHADOW_DSN。
	"third_party/identity-go/shadow/dao_test.go": "vendored 第三方；需 -tags=integration + IDENTITY_SHADOW_DSN，默认不编译",
}

// hasSQLWrite 判断剥注释后的代码里是否有 SQL 写语句。
//
// 【有意偏向误报】正则分不出「这个字符串是 SQL，会被 Exec 执行」与
// 「这个字符串是给人看的提示文本」——诊断探针里两者都长这样：
//
//	pool.Exec(ctx, "DELETE FROM emails WHERE id=$1")   ← 真语句
//	t.Logf("重启 pocketd 会重新 CREATE SCHEMA 并跑迁移")  ← 提示文本
//
// 剥掉字符串字面量可以消掉后者，但也会消掉前者。所以这里**不剥**，
// 宁可误报，让人写一行理由进 pgAllowlistedWrites。
// 取舍的依据是代价不对称：护栏漏报 = 在生产库上删表且测试报告 ok；
// 护栏误报 = 补一行可核查的理由。
func hasSQLWrite(code string) bool {
	return sqlWriteRe.MatchString(code)
}

// TestCensusSQLWriteReIsNotVacuous 钉住 sqlWriteRe 的**每一条**分支都有承重能力。
//
// 这条测试存在是因为一次真实事故：原判据写
//
//	\bUPDATE\s+[A-Za-z_]\b
//
// `[A-Za-z_]` 只吃**一个**字符 'e'，而尾部的 \b 要求 'e' 后面是**非单词字符**
// ——实际是 "email_accounts" 里 'mail_accounts' 的 'm'，属于单词字符，于是 \b
// 恒不成立。**该分支恒假**，而且不报任何错。
//
// 后果不是理论上的：internal/email/diag_snippet_leak_test.go 里真实存在的
// `UPDATE email_accounts SET last_synced_uid = 0` 被普查报成「0 写语句」，
// 与 allowlist 里手写的「全文只有 1 条写语句」直接矛盾——而没人发现，
// 因为**两侧看起来都是对的**：手写注释不会被机器核对，恒假的判据也不会喊。
//
// 负控：把任一分支改回单字符类 `[A-Za-z_]\b`，本测试必须转红。
func TestCensusSQLWriteReIsNotVacuous(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"UPDATE", "pool.Exec(ctx, `UPDATE email_accounts SET x=0`)"},
		{"INSERT", "pool.Exec(ctx, \"INSERT INTO emails (a) VALUES (1)\")"},
		{"DELETE", "pool.Exec(ctx, `DELETE FROM emails WHERE id=$1`)"},
		{"DROP SCHEMA", "pool.Exec(ctx, \"DROP SCHEMA IF EXISTS \"+schema+\" CASCADE\")"},
		{"DROP TABLE", "pool.Exec(ctx, `DROP TABLE emails`)"},
		{"DROP INDEX", "pool.Exec(ctx, `DROP INDEX idx_emails`)"},
		{"CREATE SCHEMA", "pool.Exec(ctx, \"CREATE SCHEMA \"+schema)"},
		{"CREATE TABLE", "pool.Exec(ctx, `CREATE TABLE emails (id int)`)"},
		{"CREATE INDEX", "pool.Exec(ctx, `CREATE INDEX idx ON emails (a)`)"},
		{"TRUNCATE", "pool.Exec(ctx, `TRUNCATE emails`)"},
		{"ALTER TABLE", "pool.Exec(ctx, `ALTER TABLE emails ADD COLUMN b int`)"},
		{"GRANT", "pool.Exec(ctx, `GRANT SELECT ON emails TO app`)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !hasSQLWrite(c.in) {
				t.Errorf("判据对 %q 返回 false——该分支恒假。\n"+
					"  典型成因：动词后的标识符写成单字符类 [A-Za-z_]，\n"+
					"  而尾部的 \\b 要求其后是非单词字符，于是永不成立。\n"+
					"  正确写法是 \\w+。", c.name)
			}
		})
	}

	// 反向：纯读语句不得被误判，否则这条规则会退化成「什么都红」而被忽略。
	for _, s := range []string{
		"pool.Query(ctx, `SELECT id FROM emails WHERE deleted_at IS NULL`)",
		"pool.QueryRow(ctx, `SELECT count(*) FROM email_accounts`)",
	} {
		if hasSQLWrite(s) {
			t.Errorf("只读语句被误判为含写语句：%q", s)
		}
	}

	// 反向：注释里的写动词必须被 stripGoComments 剥掉后再判。
	if hasSQLWrite(stripGoComments("// 历史版本用过 DELETE FROM emails\npool.Query(ctx, `SELECT 1`)")) {
		t.Errorf("注释里的 DELETE 被误判为可执行写语句")
	}

	// 反向：COALESCE(deleted_at,0)=0 这类含 "DELETE" 字样的**读条件**必须放过。
	// diag_real_invoice_extract_test.go 就是这一形态，它当初让该文件
	// 判红、需要人写手写理由——那条误报是真实存在的，不该被这条规则继承。
	const readCond = "rows, err := pool.Query(ctx, `SELECT id FROM emails WHERE COALESCE(deleted_at,0)=0`)"
	if hasSQLWrite(readCond) {
		t.Errorf("读条件 COALESCE(deleted_at,0) 被误判为 DELETE 语句：%q", readCond)
	}

	// 【已知且接受的假阳性方向】sqlWriteRe 里的 TRUNCATE / GRANT 无限定词，
	// 且 `UPDATE\s+\w+` 会撞上普通英文短语，于是会命中这些**不是 SQL** 的行：
	//
	//	time.Now().UTC().Truncate(time.Second)     ← TRUNCATE 撞 Go 方法名
	//	t.Fatal("oversized response/status update missing")  ← UPDATE 撞英文短语
	//	grant := computeGrant(user)                ← GRANT 撞 Go 标识符
	//
	// 首次实现时把规则 4 做成全仓扫描，这三类让判红 77 个文件。
	// 结论是**收窄作用域**而不是给正则加限定词：加限定词必然让某些真 SQL
	// 漏掉，而漏掉的代价是在生产库上删表；误报的代价是补一行理由。
	// 规则 4 因此只作用于豁免表内的文件——盲区本来就只在那里。
	//
	// 这三条断言把这个取舍钉住：它们会命中，是**已知的、有意的**，
	// 不是待修的缺陷。若哪天有人想「让判据更精确」，先读这段。
	//
	// （写这几条时我一度把 TestCaptureTruncatesLongSummary 当成假阳性样例，
	//  实际它不命中——"Truncates" 的词尾 s 挡住了 \bTRUNCATE\b。
	//  假阳性样例必须实测，不能凭印象写。）
	for _, s := range []string{
		"now := time.Now().UTC().Truncate(time.Second)",
		`t.Fatal("oversized response/status update missing")`,
		"grant := computeGrant(user)",
	} {
		if !hasSQLWrite(s) {
			t.Errorf("预期这条已知假阳性不再命中，说明 sqlWriteRe 变了，需重估作用域取舍：%q", s)
		}
	}
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
		// 不该把文件判红。必须剥掉行尾注释与块注释，只看代码。
		code := stripGoComments(src)

		// 规则 1：任何测试都不得**读**生产 DSN 变量（Setenv 写假值不算）。
		if hits := productionDSNRe.FindAllStringIndex(code, -1); len(hits) > 0 {
			reads := 0
			for _, h := range hits {
				from := h[0] - 64
				if from < 0 {
					from = 0
				}
				if productionDSNWriteRe.MatchString(code[from:h[0]]) {
					continue
				}
				reads++
			}
			if reads > 0 {
				if reason, ok := pgProductionDSNWriteOnly[rel]; ok {
					t.Logf("allowlist(只写不读): %s — %s", rel, reason)
				} else {
					t.Errorf("%s: 测试读取了 POCKET_POSTGRES_DSN（服务自己的生产连接串）%d 处。\n"+
					"  测试必须只认 POCKET_TEST_POSTGRES_DSN：同一个 DSN 既喂服务也喂测试时，\n"+
					"  读生产变量等于零配置地把测试打到生产库——CI 只设 TEST 变量，所以本地\n"+
					"  `go test ./...` 才会踩到，而它会在生产库里建表/建 schema 并报告 ok。\n"+
					"  切片回退（[]string{\"POCKET_TEST_POSTGRES_DSN\", \"POCKET_POSTGRES_DSN\"}）\n"+
					"  同样算读：字面量在切片里、传给 Getenv 的是变量。", rel, reads)
				}
			}
		}

		// 规则 3：不得靠拼接 DSN 字符串来设置 search_path（见上面的注释）。
		if dsnSearchPathAppendRe.MatchString(code) {
			t.Errorf("%s: 通过拼接 DSN 字符串（dsn+\"&search_path=\"）来隔离 schema。\n"+
				"  本仓库的 DSN 往往已经带 search_path，拼接会产生两个同名参数、pgx 取第一个，\n"+
				"  隔离静默失效而测试照样报告 ok。改用 ParseConfig 后的\n"+
				"  RuntimeParams[\"search_path\"] = schema + \",public\"。", rel)
		}

		// 规则 4：**豁免表内**的文件仍要登记它有哪些写语句。
		//
		// 这条规则存在的唯一理由：旧设计里「在 pgSafeWithoutIsolation 里」
		// 一次性放行了**全部**检查，于是 allowlist 变成完全的盲区——
		// 负控实测（2026-10-02）往 diag_credential_health_test.go 注入一段
		// `pool.Exec(ctx, "DELETE FROM email_accounts")`，护栏依然绿。
		// 豁免的语义应当是「不豁免写」。
		//
		// 【作用域限定在豁免表内，这是踩过坑才定下来的】
		// 最初把规则 4 写成全仓扫描，结果判红 77 个文件，而其中绝大多数是
		// 假阳性，分两类：
		//   · **已正确隔离**的文件（internal/task/store_test.go 等自建
		//     `*_test_` schema 的助手）——它们本来就清楚自己写在隔离库里，
		//     登记「我写了什么」纯属仪式；
		//   · **Go 标识符撞上 SQL 动词**：time.Now().Truncate(...)、
		//     t.Fatal("status update missing")、TestCaptureTruncatesLongSummary。
		//     正则分不出 SQL 动词和普通函数/字符串。sqlWriteRe 里的
		//     TRUNCATE / GRANT 无限定词，正是这一类。
		// 盲区从来只存在于**豁免表内**——表外的文件要么被规则 2 强制隔离，
		// 要么根本不开 PG 连接。所以作用域收窄到豁免表是准确的，不是妥协。
		//
		// 收窄后仍保留方向正确性：豁免「不隔离 schema」不再等于豁免「写」。
		if _, exempt := pgSafeWithoutIsolation[rel]; exempt && hasSQLWrite(code) {
			if reason, ok := pgAllowlistedWrites[rel]; ok {
				t.Logf("allowlist(含写语句): %s — %s", rel, reason)
			} else {
				t.Errorf("%s: 在 pgSafeWithoutIsolation 里被豁免，且出现了 SQL 写语句，\n"+
					"  但没有登记到 pgAllowlistedWrites。\n"+
					"  护栏不检查豁免表的写语句——负控实测（2026-10-02）往\n"+
					"  diag_credential_health_test.go 注入 `pool.Exec(ctx, \"DELETE FROM\n"+
					"  email_accounts\")` 时护栏依然绿。所以「有写语句」是**一次性观察**，\n"+
					"  不是机器维持的不变式。\n"+
					"  修法二选一：\n"+
					"    a) 若它该写隔离 schema：让文件自建 `*_test_` schema 并把 search_path 钉上去，\n"+
					"       然后从 pgSafeWithoutIsolation 里删掉它；\n"+
					"    b) 若它确实要写真实库（如诊断探针）：把文件与可核查的理由加进\n"+
					"       pgAllowlistedWrites——理由要写清：写什么、被哪几道开关挡住、\n"+
					"       schema 缺省指向哪里。\n"+
					"  不要靠把 pgSafeWithoutIsolation 的理由写宽松来绕过——那是让冲突变沉默。", rel)
			}
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

// TestStripGoCommentsHandlesTheThreeWaysToHideCode 锁住 stripGoComments 自己的
// 语义，而不是它的实现细节。
//
// 为什么必须有这条：判据是「剥注释后再匹配」。判据一坏，两边都错——
// 剥得太狠会把真违规连同代码一起删掉（永远绿），剥得太弱会让注释冒充代码
// （误报）。这两种都只能靠对**输入**的直接断言发现，光看护栏整体是绿的没用。
//
// 负控：把 trailing / block / URL 三个子用例里的任意一个实现改回旧行为
// （只剥整行注释），本测试必须转红。
func TestStripGoCommentsHandlesTheThreeWaysToHideCode(t *testing.T) {
	const lit = `"POCKET_POSTGRES_DSN"`

	cases := []struct {
		name string
		in   string
		// wantGone: 剥完后不应再出现该字面量（它只存在于注释里）
		wantGone bool
		// wantKept: 剥完后必须仍然出现（它在代码里，剥注释不能误伤）
		wantKept string
	}{
		{
			name:     "行尾注释必须被剥掉",
			in:       "\tdsn := os.Getenv(\"POCKET_TEST_POSTGRES_DSN\") // 以前还会回退读 " + lit + "\n",
			wantGone: true,
		},
		{
			name: "多行块注释（中间行不以 * 开头）必须被剥掉",
			in: "/* 旧实现：\n" + lit + " 是回退项\n后来删掉了 */\ndsn := os.Getenv(\"POCKET_TEST_POSTGRES_DSN\")\n",
			wantGone: true,
		},
		{
			name:     "字符串里的 https:// 不能被当成注释起点",
			in:       "\tu := \"https://example.com\" // 真注释\n",
			wantKept: `"https://example.com"`,
		},
		{
			name:     "代码里的字面量必须保留（剥注释不能误伤真违规）",
			in:       "\tfor _, k := range []string{\"POCKET_TEST_POSTGRES_DSN\", " + lit + "} {\n",
			wantKept: lit,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := stripGoComments(c.in)
			if c.wantGone && strings.Contains(got, lit) {
				t.Errorf("剥注释后字面量仍在，注释会冒充代码：\n%s", got)
			}
			if c.wantKept != "" && !strings.Contains(got, c.wantKept) {
				t.Errorf("剥注释把代码误伤了，%q 消失：\n%s", c.wantKept, got)
			}
		})
	}
}
