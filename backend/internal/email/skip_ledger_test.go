// skip_ledger_test.go —— 本包「被跳过的测试」逐条台账（§467 纪律的续包）。
//
// 为什么有这道门
// ----------------
// `go test` 把 SKIP 报成 ok。§467 为 internal/stt 建了第一道逐条台账，
// internal/notes 是第二份；本文件是第三份，也是**目前最大的一份**：
// 2026-10-17 用 go/ast 普查，internal/email 里有 **91 个测试 / 140 个跳过点**
// 在跳过 —— 独占全仓跳过点的一大半，而它们此前**一条都没登记**。
//
// 本仓的规矩（`docs/design/…-recording-quality-fixes.md` 的 §467/§775）：
//
//	一道门必须**双向**精确匹配 —— 漏登记红、陈旧也红。
//	只增不减的台账会烂成一张永久通行证。
//
// 三条纪律与本文件的关系
// ----------------------
// ① 双向精确匹配          → TestSkipLedger_双向精确匹配
// ② 剥注释但保留字面量    → 由 internal/skipaudit 的 go/parser 实现，
//
//	AST 里根本没有注释，也把字面量里的
//	「假跳过调用」排除在外。
//
// ③ 量具先自证            → TestSkipLedger_量具自证
//
// ⛔ 本文件**不含任何跳过调用字面量**（注释里也没有），因此**不需要任何豁免**。
//
//	§467 的 stt 台账必须豁免它自己（负控样本里有一段原始字符串写着
//	一个自带跳过调用的假测试）—— 而豁免本身是一种长期风险
//	（§467 为此专门写了一条自证）。改用 AST 后这一类豁免可以
//	整体消失：豁免没了，「豁免烂成通行证」的路也就没了。
//
// 本包台账最刺眼的一栏
// --------------------
// 91 条里有 **11 条**属于 CatPremiseFailed：守卫是**用例自己的前提不成立**
// （夹具没被规则命中、负控的对照没搭起来、非导出字段居然落库了…）。
// 这一类的代价不是「暂时没跑」，而是「**永远不会失败**」——
// 前提一不成立，测试自己退出门外，绿色读数与「跑过并通过」完全无法区分。
// 它的数量被 TestSkipLedger_前提不成立类计数被钉住 钉死。
package email

import (
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/skipaudit"
)

// knownSkips 是本包**全部**允许被跳过的测试（91 条）。
//
// ⛔ 不在表内的一律红。
//
// 分类的规矩（照 §467，按**守卫条件**分，不按感觉分）：
//   - CatDiagProbe：守卫是 `POCKET_DIAG_*=1` 这类显式 opt-in，跑起来可能写真实数据；
//   - CatCredential：需要真 DSN / 真 IMAP 账密 / 真语料；
//   - CatHostCapability：需要宿主有中文字体 / 不是 root；
//   - CatFixtureRegen：守卫是「重新生成已提交夹具」的开关；
//   - CatPremiseFailed：守卫是**用例自己**的前提不成立。
//
// ⛔ Unlock 绝不含「配置一下」这类空话：每条都写实测的 env 键名，
//
//	没有 env 可设的（前提不成立那 11 条）就写清楚**要让哪个条件变成真**。
var knownSkips = []skipaudit.Entry{
	skipaudit.E("TestA4InkOverflowOnRealExports", skipaudit.CatDiagProbe, "真实导出件上的 A4 墨迹溢出诊断；跑它要遍历真实导出目录，故默认 opt-in。", "POCKET_DIAG_A4_INK=1", "POCKET_DIAG_EXPORT_DIR=<真实导出件目录>"),
	skipaudit.E("TestA4ReexportWithCurrentCodeIsClean", skipaudit.CatDiagProbe, "用当前代码对既有导出件重导一遍看结果是否还干净；会覆写导出目录。", "POCKET_DIAG_REEXPORT=1", "POCKET_DIAG_INVOICE_DIR=<发票导出目录>"),
	skipaudit.E("TestBuildAuthURLIncludesClientID", skipaudit.CatPremiseFailed, "循环第一项 google 在 providers.go 里压根没登记 ⇒ 整条用例在第一轮就退出门外，"+
		"后半分之一个 outlook 的覆盖根本没机会发生。", "把用例里的 \"google\" 换成 providers.go 登记的 \"gmail\"（或给 providers 追加一条 ID 为 google 的条目）"),
	skipaudit.E("TestBuildAuthURLRejectsEmptyClientID", skipaudit.CatPremiseFailed, "同上：LookupProviderByID(\"google\") 恒为 false，本用例永远走不到断言。", "把用例里的 \"google\" 换成 providers.go 登记的 \"gmail\"（或给 providers 追加一条 ID 为 google 的条目）"),
	skipaudit.E("TestCleanSpam_DryRunNeverMoves", skipaudit.CatPremiseFailed, "夹具邮件没被 LooksLikeSpam 命中（规则集已变），预演路径压根没进 ⇒ 用例自己的前提没立起来。", "让 LooksLikeSpam 对夹具 promo@ads.example.com / 主题「恭喜您获得大奖！点击领取」至少命中一条规则"),
	skipaudit.E("TestDailyPipelineLock_DifferentSchemasAreIndependent", skipaudit.CatCredential, "advisory lock 的 schema 作用域隔离必须真发一次锁才知道；本机没有测试库。", "POCKET_TEST_POSTGRES_DSN=<可写的测试库 DSN>"),
	skipaudit.E("TestDailyPipelineLock_SameSchemaStillExcludes", skipaudit.CatCredential, "同 schema 的互斥排除同样要真库里的两张真表来验。", "POCKET_TEST_POSTGRES_DSN=<可写的测试库 DSN>"),
	skipaudit.E("TestDailyPipelineLock_TestHarnessIsActuallyIsolated", skipaudit.CatCredential, "本条是本文件其余用例的自证：没有真库就无从判断隔离是真的还是假的。", "POCKET_TEST_POSTGRES_DSN=<可写的测试库 DSN>"),
	skipaudit.E("TestDiagA4AutoPreview", skipaudit.CatDiagProbe, "A4 自动缩放预演的只读诊断，按惯例默认 opt-in。", "POCKET_DIAG_A4_AUTO=1"),
	skipaudit.E("TestDiagA4CellFit", skipaudit.CatDiagProbe, "逐格比对单元格文本与导出件是否贴合；要真读导出目录，故 opt-in。", "POCKET_DIAG_CELL_FIT=1", "POCKET_DIAG_EXPORT_DIR=<导出目录> 与 POCKET_DIAG_INVOICE_DIR=<发票目录>"),
	skipaudit.E("TestDiagA4RealExport", skipaudit.CatDiagProbe, "对真实导出件跑一遍 A4 渲染诊断。", "POCKET_DIAG_A4_REAL=1"),
	skipaudit.E("TestDiagAdvisoryReentrant", skipaudit.CatDiagProbe, "advisory lock 重入行为的诊断探针，会连真库发锁。", "POCKET_DIAG_ADVISORY_REENTRANT=1"),
	skipaudit.E("TestDiagAmountProvenance", skipaudit.CatDiagProbe, "金额来源逐跳溯源的只读诊断。", "POCKET_DIAG_AMOUNT_PROV=1"),
	{
		Test:   "TestDiagBackfillAlignment",
		Cat:    skipaudit.CatDiagProbe,
		Reason: "回填与抽取结果对齐的只读诊断；要连真库并解凭据。",
		Unlock: []string{"POCKET_DIAG_ALIGN=1", "POCKET_REAL_MAIL_DSN=<真实库 DSN> + POCKET_REAL_MAIL_SCHEMA=<schema>", "可解开该库凭据的 email_master.key（否则第三点 kerr != nil 也会跳过）"},
		Points: 3,
	},
	{
		Test:   "TestDiagBodyTextPipelineWalk",
		Cat:    skipaudit.CatDiagProbe,
		Reason: "把一封真实邮件的正文管线逐段走一遍并 dump 原始字节。",
		Unlock: []string{"POCKET_DIAG_RAWBYTES_DUMP=1", "POCKET_DIAG_RAWBYTES_OUT=<dump 输出目录>"},
		Points: 2,
	},
	skipaudit.E("TestDiagBoundaryTightenCandidates", skipaudit.CatDiagProbe, "拿真实语料比较 boundary 正则的收紧候选；语料不在仓内，必须 opt-in 带语料。", "POCKET_DIAG_BOUNDARY_TIGHTEN=1", "POCKET_DIAG_SNIPPET_CORPUS=<真实语料目录>"),
	skipaudit.E("TestDiagCleanupPreview", skipaudit.CatDiagProbe, "清垃圾预演的只读诊断。", "POCKET_DIAG_CLEANUP_PREVIEW=1"),
	{
		Test:   "TestDiagCredentialHealthRealDB",
		Cat:    skipaudit.CatCredential,
		Reason: "真库凭据健康诊断；没有真 DSN 与主密钥文件就毫无意义。",
		Unlock: []string{"POCKET_REAL_MAIL_DSN=<真实库 DSN>", "POCKET_REAL_DATA_DIR=<含 email_master.key 的数据目录>"},
		Points: 2,
	},
	skipaudit.E("TestDiagDebtNoticeCandidatesWouldBeFiled", skipaudit.CatDiagProbe, "债务通知类幽灵行的只读诊断：要真库才能看见那些行。", "POCKET_DIAG_DEBT_SHAPE=1", "POCKET_REAL_MAIL_DSN=<真实库 DSN> + POCKET_REAL_MAIL_SCHEMA=<schema>"),
	{
		Test:   "TestDiagDuplicateReport",
		Cat:    skipaudit.CatDiagProbe,
		Reason: "重复邮件报告的只读诊断。",
		Unlock: []string{"POCKET_DIAG_DUP_REPORT=1", "POCKET_REAL_MAIL_DSN=<真实库 DSN>"},
		Points: 2,
	},
	skipaudit.E("TestDiagEUIXMLShape", skipaudit.CatDiagProbe, "EUI 通行费 XML 结构的形状诊断，读的是真实缓存正文。", "POCKET_DIAG_TOLL_ATT=1", "POCKET_DIAG_QP_DATADIR=<QP 缓存正文目录>"),
	skipaudit.E("TestDiagHardDeadlineWithProductionConstants", skipaudit.CatDiagProbe, "按生产常量跑 IMAP 硬截止（约 80s），要占真连接。", "POCKET_DIAG_HARDDEADLINE=1"),
	{
		Test:   "TestDiagIMAPStepTiming",
		Cat:    skipaudit.CatDiagProbe,
		Reason: "活体 IMAP 分步计时探针：会连真账户发只读查询，且作者刻意要第二个开关二次确认。",
		Unlock: []string{"POCKET_DIAG_ACCOUNT=<要诊断的账户>", "POCKET_DIAG_ALLOW=1（活体只读探针的二次确认开关）", "POCKET_REAL_MAIL_DSN=<真实库 DSN>", "可解开真实库凭据的 email_master.key，并从仓库根目录运行"},
		Points: 4,
	},
	{
		Test:   "TestDiagInvoiceBacklog",
		Cat:    skipaudit.CatDiagProbe,
		Reason: "发票积压情况的只读诊断。",
		Unlock: []string{"POCKET_DIAG_INVOICE_BACKLOG=1", "POCKET_REAL_MAIL_DSN=<真实库 DSN>", "POCKET_REAL_MAIL_SCHEMA=<schema>"},
		Points: 3,
	},
	skipaudit.E("TestDiagInvoiceCandidateFalseNegative", skipaudit.CatDiagProbe, "候选漏召回复盘要拿真库导出的 TSV 语料对；语料不入仓。", "POCKET_DIAG_CANDIDATE=1", "POCKET_DIAG_CANDIDATE_CORPUS=<psql 导出的 TSV 路径>"),
	skipaudit.E("TestDiagInvoiceHandoffIntegrity", skipaudit.CatDiagProbe, "交接前发票完整性的只读诊断。", "POCKET_DIAG_HANDOFF=1", "POCKET_REAL_MAIL_DSN=<真实库 DSN> + POCKET_REAL_MAIL_SCHEMA=<schema>", "POCKET_DIAG_INVOICE_DIR=<发票目录>"),
	skipaudit.E("TestDiagLedgerTable", skipaudit.CatDiagProbe, "发票台账表的只读结构诊断。", "POCKET_DIAG_LEDGER_TABLE=1"),
	skipaudit.E("TestDiagLiveSnippetReplay", skipaudit.CatDiagProbe, "真库 snippet 的重放；沿用 rawbytes dump 的同一组开关。", "POCKET_DIAG_RAWBYTES_DUMP=1", "POCKET_DIAG_RAWBYTES_OUT=<dump 输出目录>"),
	skipaudit.E("TestDiagMarkFalseLedgerRows", skipaudit.CatDiagProbe, "标注误判台账行的诊断；默认只读，真写要再加一个执行开关。", "POCKET_DIAG_ANNOTATE=1", "POCKET_REAL_MAIL_DSN=<真实库 DSN> + POCKET_REAL_MAIL_SCHEMA=<schema>", "POCKET_DIAG_ANNOTATE_EXEC=1（真落标注才需要）"),
	{
		Test:   "TestDiagMarketingSpamScoreDistribution",
		Cat:    skipaudit.CatDiagProbe,
		Reason: "营销垃圾分值分布的只读诊断。",
		Unlock: []string{"POCKET_DIAG_SPAM_SCORE=1", "POCKET_REAL_MAIL_DSN=<真实库 DSN> + POCKET_REAL_MAIL_SCHEMA=<schema>"},
		Points: 3,
	},
	{
		Test:   "TestDiagMergeExec",
		Cat:    skipaudit.CatDiagProbe,
		Reason: "⚠ 这是**写**操作诊断（真在库里 merge），所以守卫比读诊断多一层心理成本，默认关着是对的。",
		Unlock: []string{"POCKET_DIAG_MERGE_EXEC=1（写操作开关）", "POCKET_REAL_MAIL_DSN=<真实库 DSN> + POCKET_REAL_MAIL_SCHEMA=<schema>", "可解开该库凭据的 email_master.key"},
		Points: 3,
	},
	{
		Test:   "TestDiagMergePlan",
		Cat:    skipaudit.CatDiagProbe,
		Reason: "合并计划的只读预演诊断。",
		Unlock: []string{"POCKET_DIAG_MERGE_PLAN=1", "POCKET_REAL_MAIL_DSN=<真实库 DSN>", "可解开该库凭据的 email_master.key"},
		Points: 3,
	},
	skipaudit.E("TestDiagNUpScaleRepro", skipaudit.CatDiagProbe, "n-up 版面缩放缺陷的离线复现探针。", "POCKET_DIAG_NUP_SCALE=1"),
	{
		Test:   "TestDiagPOP3BackfillMessageIDs",
		Cat:    skipaudit.CatDiagProbe,
		Reason: "POP3 MessageID 回填诊断；要真库且库里要有可回填的行。",
		Unlock: []string{"POCKET_DIAG_POP3_BACKFILL=1", "POCKET_REAL_MAIL_DSN=<真实库 DSN>", "可解开该库凭据的 email_master.key"},
		Points: 4,
	},
	{
		Test:   "TestDiagPOP3RefetchInvoice",
		Cat:    skipaudit.CatDiagProbe,
		Reason: "对 failed 的 POP3 发票重取票面的只读诊断。",
		Unlock: []string{"POCKET_DIAG_POP3=1", "POCKET_REAL_MAIL_DSN=<真实库 DSN>", "可解开该库凭据的 email_master.key，且真库里已有失败的 POP3 发票行"},
		Points: 5,
	},
	skipaudit.E("TestDiagPurgeInjectedInvoices", skipaudit.CatDiagProbe, "清理由注入产生的假发票；真删要再显式加一个执行开关。", "POCKET_DIAG_PURGE=1", "POCKET_REAL_MAIL_DSN=<真实库 DSN> + POCKET_REAL_MAIL_SCHEMA=<schema>", "POCKET_DIAG_PURGE_EXEC=1（真删才需要）"),
	skipaudit.E("TestDiagQPReplay", skipaudit.CatDiagProbe, "QP 正文重放；样本是仓外的 raw .bin，必须点名指路。", "POCKET_DIAG_QP_REPLAY=1", "POCKET_DIAG_QP_BODY=<raw .bin 文件路径或目录>"),
	skipaudit.E("TestDiagRawBytesDump", skipaudit.CatDiagProbe, "真库邮件的原始字节 dump；产物落在指定目录，默认不跑。", "POCKET_DIAG_RAWBYTES_DUMP=1", "POCKET_DIAG_RAWBYTES_OUT=<dump 输出目录>"),
	{
		Test:   "TestDiagRealExportGridGeometry",
		Cat:    skipaudit.CatDiagProbe,
		Reason: "对真实导出件量网格几何。",
		Unlock: []string{"POCKET_DIAG_REAL_EXPORTS=1", "POCKET_DIAG_EXPORT_DIR=<真实导出件目录>"},
		Points: 2,
	},
	{
		Test:   "TestDiagRealFetchSnippetStages",
		Cat:    skipaudit.CatCredential,
		Reason: "真库 snippet 在四段管线里各自丢在哪；DSN 必须自带 search_path，且要有能解凭据的 key。",
		Unlock: []string{"PG_DSN=<真库 DSN，且 query 里带 search_path=...>", "POCKET_REAL_KEYS=<能解开真实库凭据的 key 列表>"},
		Points: 4,
	},
	skipaudit.E("TestDiagRealInvoiceAdmissionGate", skipaudit.CatCredential, "真实发票准入门诊断；作者刻意不给 DSN 缺省值（缺省会误打生产库）。", "POCKET_REAL_MAIL_DSN=<真实库 DSN>"),
	{
		Test:   "TestDiagRealInvoiceExtraction",
		Cat:    skipaudit.CatCredential,
		Reason: "真实发票抽取诊断；要真库正文与解密用的主密钥目录。",
		Unlock: []string{"POCKET_REAL_MAIL_DSN=<真实库 DSN>", "POCKET_REAL_DATA_DIR=<含 email_master.key 的数据目录>"},
		Points: 2,
	},
	skipaudit.E("TestDiagRealInvoiceExtractionBlastRadius", skipaudit.CatCredential, "抽取缺陷的影响面统计，只能在真库上数。", "POCKET_REAL_MAIL_DSN=<真实库 DSN>"),
	skipaudit.E("TestDiagRealVoucherGeometry", skipaudit.CatDiagProbe, "真实票面页面几何测量（只读）。", "POCKET_DIAG_VOUCHER_GEOM=1", "POCKET_REAL_INVOICE_DIR=<真实发票目录>"),
	skipaudit.E("TestDiagRealXMLAttachmentScan", skipaudit.CatDiagProbe, "扫真实缓存正文里的 XML 附件。", "POCKET_DIAG_XML_SCAN=1", "POCKET_DIAG_QP_DATADIR=<QP 缓存正文目录>"),
	{
		Test:   "TestDiagReminderBacklog",
		Cat:    skipaudit.CatDiagProbe,
		Reason: "提醒积压情况的只读诊断。",
		Unlock: []string{"POCKET_DIAG_REMINDER_BACKLOG=1", "POCKET_REAL_MAIL_DSN=<真实库 DSN>", "POCKET_REAL_MAIL_SCHEMA=<schema>"},
		Points: 3,
	},
	{
		Test:   "TestDiagRestDupes",
		Cat:    skipaudit.CatDiagProbe,
		Reason: "REST 链路重复项的只读诊断。",
		Unlock: []string{"POCKET_DIAG_REST_DUPES=1", "POCKET_REAL_MAIL_DSN=<真实库 DSN> + POCKET_REAL_MAIL_SCHEMA=<schema>", "可解开该库凭据的 email_master.key"},
		Points: 3,
	},
	skipaudit.E("TestDiagSpamPreview", skipaudit.CatDiagProbe, "垃圾判定的预演诊断，连的是 POCKET_DIAG_PG 那条 DSN。", "POCKET_DIAG_SPAM_PREVIEW=1", "POCKET_DIAG_PG=<dsn>"),
	{
		Test:   "TestDiagSpamVerdict",
		Cat:    skipaudit.CatDiagProbe,
		Reason: "垃圾判定结论的只读诊断。",
		Unlock: []string{"POCKET_DIAG_SPAM_VERDICT=1", "POCKET_REAL_MAIL_DSN=<真实库 DSN>", "POCKET_REAL_MAIL_SCHEMA=<schema>"},
		Points: 3,
	},
	{
		Test:   "TestDiagStaleDebtNoticeRow",
		Cat:    skipaudit.CatDiagProbe,
		Reason: "陈旧债务通知行的只读诊断。",
		Unlock: []string{"POCKET_DIAG_STALE_ROW=1", "POCKET_REAL_MAIL_DSN=<真实库 DSN>", "POCKET_REAL_MAIL_SCHEMA=<schema>"},
		Points: 3,
	},
	skipaudit.E("TestDiagTencentIMAPBareLogin", skipaudit.CatCredential, "腾讯 IMAP 裸登录诊断要真账密；没有就退出门外。", "KXPMS_DIAG_USER=<IMAP 账号>", "KXPMS_DIAG_PASS=<IMAP 密码>"),
	{
		Test:   "TestDiagTencentIMAPPerNodeColdStart",
		Cat:    skipaudit.CatCredential,
		Reason: "各节点冷启动耗时诊断要真账号，还要求本机能解析目标主机名。",
		Unlock: []string{"KXPMS_DIAG_USER=<IMAP 账号>", "KXPMS_DIAG_PASS=<IMAP 密码>", "本机 /etc/hosts 或 DNS 能解析目标 IMAP 主机名（否则 err != nil 那一点也会跳过）"},
		Points: 2,
	},
	skipaudit.E("TestDiagTencentIMAPStackAB", skipaudit.CatCredential, "腾讯 IMAP 两套链路 A/B 对比要真账密。", "KXPMS_DIAG_USER=<IMAP 账号>", "KXPMS_DIAG_PASS=<IMAP 密码>"),
	skipaudit.E("TestDiagTollAttachmentXMLHasInvoiceNo", skipaudit.CatDiagProbe, "通行费附件里是否带发票号的诊断。", "POCKET_DIAG_TOLL_ATT=1"),
	skipaudit.E("TestDiagTollAttachments", skipaudit.CatDiagProbe, "看真实通行费附件长什么样，要指到 QP 缓存目录。", "POCKET_DIAG_TOLL_ATT=1", "POCKET_DIAG_QP_DATADIR=<QP 缓存正文目录>"),
	skipaudit.E("TestDiagTollEndToEndOffline", skipaudit.CatDiagProbe, "真实数据 + 离线渲染的端到端复现（不连网但读真语料）。", "POCKET_DIAG_TOLL_E2E=1", "POCKET_DIAG_QP_DATADIR=<QP 缓存正文目录>"),
	skipaudit.E("TestDiagTollInvoiceA4AndLedgerOffline", skipaudit.CatDiagProbe, "真实数据下 A4 版面与台账的离线端到端复现。", "POCKET_DIAG_TOLL_E2E=1", "POCKET_DIAG_QP_DATADIR=<QP 缓存正文目录>"),
	skipaudit.E("TestDiagTollInvoiceReplay", skipaudit.CatDiagProbe, "从磁盘重放两张通行费发票。", "POCKET_DIAG_TOLL_REPLAY=1"),
	skipaudit.E("TestDiagTollReplaySawTheRealBody", skipaudit.CatDiagProbe, "确认重放真的读到了真实正文（空 body 也能蒙混过关，故单独一条）。", "POCKET_DIAG_TOLL_REPLAY=1"),
	{
		Test:   "TestDiagWhichSubPredicate",
		Cat:    skipaudit.CatDiagProbe,
		Reason: "定位空 snippet 是哪个子判据丢的，沿用 rawbytes 的开关与输出目录。",
		Unlock: []string{"POCKET_DIAG_RAWBYTES_DUMP=1", "POCKET_DIAG_RAWBYTES_OUT=<dump 输出目录>"},
		Points: 3,
	},
	{
		Test:   "TestDiagWhyEmptySnippet",
		Cat:    skipaudit.CatCredential,
		Reason: "空 snippet 的成因只能在真库 + 真 key 上定位。",
		Unlock: []string{"PG_DSN=<真库 DSN，且 query 里带 search_path=...>", "POCKET_REAL_KEYS=<能解开真实库凭据的 key 列表>"},
		Points: 4,
	},
	skipaudit.E("TestDiagXMLRender", skipaudit.CatDiagProbe, "XML 渲染的只读诊断。", "POCKET_DIAG_XML_RENDER=1"),
	skipaudit.E("TestDiagnoseLedgerTotalOnRealData", skipaudit.CatCredential, "台账合计的真库诊断，没有 DSN 无从对数。", "POCKET_REAL_MAIL_DSN=<真实库 DSN>"),
	skipaudit.E("TestDiagnosePocketdSchema", skipaudit.CatCredential, "生产 schema 的诊断，必须显式指路。", "POCKET_REAL_MAIL_DSN=<真实库 DSN>"),
	{
		Test:   "TestDiagnoseReminderNotifiedAt",
		Cat:    skipaudit.CatCredential,
		Reason: "提醒 notified_at 的诊断；作者坚持 schema 也要点名（「不知道在读哪个 schema」）。",
		Unlock: []string{"POCKET_REAL_MAIL_DSN=<真实库 DSN>", "POCKET_REAL_MAIL_SCHEMA=<schema>"},
		Points: 2,
	},
	skipaudit.E("TestDiagnoseSnippetLeak", skipaudit.CatCredential, "snippet 泄漏诊断要在真库上找活着的泄漏。", "POCKET_REAL_MAIL_DSN=<真实库 DSN>"),
	skipaudit.E("TestExportInvoiceGrid_WebPReachesWebPDecoder", skipaudit.CatPremiseFailed, "故意损坏的 RIFF/WEBP 夹具竟被成功解码 ⇒ 本该走的解码器分支没被触发，负控失效。", "让 ExportInvoiceGrid 对损坏的 RIFF/WEBP 样本返回非 nil 错误（当前 err == nil）"),
	skipaudit.E("TestExtractInvoice_ForeignCurrencyViaFallbackPath", skipaudit.CatPremiseFailed, "外币兜底路径要靠关键词门放行才进得去；这组夹具当前 !ok ⇒ 用例的前提没立起来。", "让 ExtractInvoice 的关键词门对夹具「Receipt for your subscription」/「USD 45.90」放行（当前返回 !ok）"),
	skipaudit.E("TestFindChineseFont_AcceptsRealCJKFont", skipaudit.CatHostCapability, "要本机真有一款含中文字形的字体夹具才能验正向；无字体的宿主上这条恒跳过。", "宿主装一套含中文字形的 .ttf（macOS 如 /System/Library/Fonts/Supplemental/AppleGothic.ttf），"+
		"或让 FindChineseFont 的三级回退目录里有一款 CJK 字体"),
	skipaudit.E("TestFindChineseFont_RejectsExplicitNonCJKFont", skipaudit.CatHostCapability, "负控要用一款**确实不含中文**的字体夹具；宿主只有中文字体时这条恒跳过。", "宿主准备一款可用的非中文字体夹具（如 /System/Library/Fonts/Supplemental/Arial.ttf）并让用例指到它"),
	skipaudit.E("TestFindChineseFont_ThenRenderXMLInvoice", skipaudit.CatHostCapability, "XML→PDF 要真有中文字体才画得出中文；宿主无 CJK 字体时整条路径不可用。", "宿主装一套含中文字形的 .ttf，让 FindChineseFont(\"\") 能命中"),
	skipaudit.E("TestGenerateFixtureInvoicePDFBase64", skipaudit.CatFixtureRegen, "它是**重新生成**已提交的 IMAP 夹具票面，不是断言；平时跑的是已生成的那份。", "POCKET_EMAIL_GEN_FIXTURE=1", "go test ./internal/email -run TestGenerateFixtureInvoicePDFBase64（产物会被写回仓内）"),
	skipaudit.E("TestHarvestOne_XMLAttachmentRendersThroughHarvester", skipaudit.CatHostCapability, "探不到中文字体时 XML→PDF 在本机不可用（生产同样会记 failed）⇒ 断言在无字体宿主上无意义。", "宿主装一套含中文字形的 .ttf，让 FindChineseFont(t.TempDir()) 的回退能命中"),
	skipaudit.E("TestHarvestOne_ZipInvoiceBeatsSummaryPDF", skipaudit.CatPremiseFailed, "刚由 harvestOne 写出来的文件两处路径都读不到 ⇒ 落盘这一步自己没成功，内容断言无从下手。", "让 harvestOne 落盘后的 inv.FilePath 能被 os.ReadFile 直接读到（DataDir 路径形态已变）"),
	skipaudit.E("TestInvoiceSource_XMLAttachment_RendersPDF", skipaudit.CatHostCapability, "XML 附件渲染 PDF 依赖中文字体；宿主没有就是设计上不可用。", "宿主装一套含中文字形的 .ttf，让 FindChineseFont 能命中"),
	skipaudit.E("TestLastLeakSampleDerivesEmpty", skipaudit.CatCredential, "判据钉的是**真库导出的那封样本**，样本文件缺席就只能退出门外（该文件目前已随仓提交，这条守卫是休眠的）。", "补回 testdata/last_leak_snippet.txt（真库导出的原始邮件样本，压平的 boundary + MIME 头 + 正文同一行）"),
	{
		Test:   "TestMoveToJunkGreenmail",
		Cat:    skipaudit.CatCredential,
		Reason: "真实 UID MOVE 链路验证（-tags=greenmail）；要真 IMAP 服务与真测试库，且收件箱得先有 3 封以上。",
		Unlock: []string{"POCKET_TEST_POSTGRES_DSN=<可写的测试库 DSN>", "docker run -d --rm --name greenmail-test -p 3025:3025 -p 3993:3993 greenmail/standalone:latest " +
			"-Dgreenmail.setup.test.all -Dgreenmail.users=huangxutao@kxmail.local:h8pass", "经 3025 SMTP 往 huangxutao@kxmail.local 投 3 封以上邮件", "go test -tags=greenmail ./internal/email/ -run TestMoveToJunkGreenmail -v"},
		Points: 2,
	},
	{
		Test:   "TestPickCJKFont_NeverReturnsNonCJKFont",
		Cat:    skipaudit.CatHostCapability,
		Reason: "两个守卫都是宿主能力：机器上一个 .ttf 都没有、或没有一个含中文字形，判据就没有可判对象。",
		Unlock: []string{"宿主至少装一款含中文字形的 .ttf（如 /System/Library/Fonts/Supplemental/AppleGothic.ttf）"},
		Points: 2,
	},
	skipaudit.E("TestPickCJKFont_SkipsLatinOnlyEntry", skipaudit.CatHostCapability, "这条要一台**同时**有拉丁字体与中文字体的机器才判得了「跳过纯拉丁项」。", "宿主同时具备一款纯拉丁 .ttf 与一款含中文字形的 .ttf"),
	skipaudit.E("TestPickFreeInvoicePath_TwoInvoicesWithoutParsedNumberDoNotCollide", skipaudit.CatPremiseFailed, "作者本用例要证明「两张都解析不出票号时会撞名」，而 InvoiceFileName 现在已经能区分二者 ⇒ 前提不成立。", "让 PickFreeInvoicePath 在两张发票号都为空时返回同名（当前 na != nb），或改判据"),
	skipaudit.E("TestRawEncodedWordSubject_MissesKeywords", skipaudit.CatPremiseFailed, "本用例钉「未解码的 =?…?= 原文必然命中不了关键词」，而关键词表竟意外命中了它 ⇒ 前提不成立。", "把 InvoiceCandidate 的 ASCII 关键词表（invoice.go 的 invoiceKeywordASCIIRegexes）收敛到不再命中未解码的 =?GB2312?B?…=? 原文"),
	{
		Test:   "TestRealImapPrimaryPath",
		Cat:    skipaudit.CatCredential,
		Reason: "主链路探针按设计就指真 schema；DSN 必带 search_path，且要一把能解真实库凭据的 key。",
		Unlock: []string{"PG_DSN=<真库 DSN，且 query 里带 search_path=...>", "POCKET_REAL_KEYS=<能解开真实库凭据的 key 列表>"},
		Points: 4,
	},
	skipaudit.E("TestRenderInvoiceXMLPDF", skipaudit.CatHostCapability, "渲染正文的字形来自中文字体；宿主无 CJK 字体时这条断言在设计上就无法成立。", "宿主装一套含中文字形的 .ttf，让 FindChineseFont(\"\") 能命中"),
	skipaudit.E("TestRenderInvoiceXMLPDF_LatinFontOutputHasNoCJGGlyphs", skipaudit.CatPremiseFailed, "负控要求产物里能解出 /FontFile2；解不出 0 个字体程序 ⇒ 对照没搭起来，这条不构成有效负控。", "让 renderProbePDF（拉丁字体）的产物能被 embeddedFontPrograms 解出至少一个 /FontFile2"),
	skipaudit.E("TestRenderInvoiceXMLPDF_OutputEmbedsCJGGlyphs", skipaudit.CatHostCapability, "正向控制要真嵌入中文字形；宿主没有 CJK 字体就无从验。", "宿主装一套含中文字形的 .ttf，让 FindChineseFont(\"\") 能命中"),
	skipaudit.E("TestSaveInvoiceFile_UnwritableDirIsNotReportedAsDownloaded", skipaudit.CatHostCapability, "以 root 运行时权限位不生效 ⇒ 「不可写目录」这个前提根本造不出来。", "以非 root 身份运行 go test（os.Geteuid() != 0），例如 sudo -u <普通用户> 或容器内以 uid>=1000 跑"),
	skipaudit.E("TestSellerFallbackSurvivesDatabaseRoundTrip", skipaudit.CatPremiseFailed, "作者当时就写明了：一个非导出字段居然落了库 ⇒ 本用例的前提不成立，判据要重新设计。", "让非导出的 sellerIsFallback 字段不再随 Invoice 落库（或按作者原话重新设计判据）"),
	{
		Test:   "TestSpamRuleOnRealMailboxData",
		Cat:    skipaudit.CatCredential,
		Reason: "垃圾规则的真实表现只能在真 schema 的真邮件上看；空库也判不了（len(all) == 0 那一点）。",
		Unlock: []string{"POCKET_REAL_MAIL_DSN=<真实 schema 的 DSN>", "该 schema 里确有邮件可数（否则 len(all) == 0 那一点也会跳过）"},
		Points: 2,
	},
	skipaudit.E("TestSyncGreenmail", skipaudit.CatCredential, "真实 IMAP 同步链路验证（-tags=greenmail）；要真 IMAP 服务与真测试库。", "POCKET_TEST_POSTGRES_DSN=<可写的测试库 DSN>", "docker run -d --rm --name greenmail-test -p 3025:3025 -p 3993:3993 greenmail/standalone:latest "+
		"-Dgreenmail.setup.test.all -Dgreenmail.users=huangxutao@kxmail.local:h8pass", "经 3025 SMTP 往 huangxutao@kxmail.local 投递若干封带 PDF 附件的发票邮件", "go test -tags=greenmail ./internal/email/ -run TestSyncGreenmail -v"),
	skipaudit.E("TestSyncReleasesInflightOnReturn", skipaudit.CatPremiseFailed, "本用例要「Sync 返回错误后 inflight 被释放」；而 NewFetcher(nil, nil) 下它提前 return 且 err == nil ⇒ 前提不成立。", "让 NewFetcher(nil, nil).Sync(ctx, \"acct-2\") 返回非 nil 错误（当前 store 为 nil 时直接返回 nil）"),
	skipaudit.E("TestXMLInvoiceEndToEnd_RenderPDF", skipaudit.CatHostCapability, "XML 渲染端到端要真有中文字体；三级回退全落空时该路径在本机不可用。", "宿主装一套含中文字形的 .ttf，让 FindChineseFont 的三级回退能命中"),
}

var ledger = skipaudit.Ledger{
	Pkg:      "./internal/email",
	Entries:  knownSkips,
	MinFuncs: 70, // 实测 91 个。留 21 的余量，但不许掉到七成以下
}

// ③ 量具自证：先确认抽取器真的看得见东西，再谈台账对不对。
func TestSkipLedger_量具自证(t *testing.T) {
	res, err := scan(t)
	if err != nil {
		t.Fatalf("扫描失败: %v", err)
	}
	if err := skipaudit.SelfTestOK(ledger, res); err != nil {
		t.Fatal(err)
	}
	t.Logf("%s：跳过测试 %d 个 / 跳过点 %d 个，台账登记 %d 条", ledger.Pkg, res.Found, res.Points, len(ledger.Entries))
}

// ① 双向精确匹配。
func TestSkipLedger_双向精确匹配(t *testing.T) {
	res, err := scan(t)
	if err != nil {
		t.Fatalf("扫描失败: %v", err)
	}
	if !res.OK() {
		t.Error(res.String())
	}
	// 台账登记的点数必须与实际点数一致 —— 这是台账与 census 的交叉口径。
	// ⚠ Points 是**手写**的：只有手写，这条校验才不是恒真的。
	if res.LedgeredPoints != res.Points {
		multi := 0
		for _, e := range knownSkips {
			if e.Points > 1 {
				multi++
			}
		}
		t.Errorf("台账点数 %d ≠ 实际点数 %d ⇒ 某条目的 Points 字段写错了"+
			"（本包 %d 条里有 %d 条是多跳过点；单跳过点的条目 Points 留 0 即可）",
			res.LedgeredPoints, res.Points, len(knownSkips), multi)
	}
}

// 类别分布要**显式**钉住：新增一个「前提不成立就跳过」的测试时，
// 必须有人在这里改一个常量，而不是悄悄混进总量里。
func TestSkipLedger_前提不成立类计数被钉住(t *testing.T) {
	const wantPremiseFailed = 11
	got := 0
	for _, e := range knownSkips {
		if e.Cat == skipaudit.CatPremiseFailed {
			got++
		}
	}
	if got != wantPremiseFailed {
		t.Errorf("「前提不成立就跳过」类实际 %d 条，钉住值是 %d。\n"+
			"  ⇒ 新增一条要**有意识地**改这个常量并说明理由：\n"+
			"    这类的代价不是「暂时没跑」，而是「**永远不会失败**」。", got, wantPremiseFailed)
	}
}

func dir(t *testing.T) string {
	t.Helper()
	d, err := skipaudit.DirOf("internal/email")
	if err != nil {
		t.Fatalf("定位包目录失败: %v", err)
	}
	return d
}

// scan 跑一次核对。
//
// ⚠ 本包有 140 个跳过点、91 个跳过函数，同一函数体内有多处跳过很常见
// （例如 TestDiagIMAPStepTiming 有 4 处）。Points 是**机械字段**，
// ⚠ Points 是**手写**的，不是在这里用 Register 自动补的。
//
// 首版（2026-10-17）在这里跑了 Register 自动补齐，结果
// 「台账点数 == 实际点数」这条交叉校验**恒真** —— 判据变成永远通过。
// 那正是本仓反复记录的形态：**判据看起来在咬，其实已经不咬了。**
// 手写才能让「我以为它是几次」与「AST 数出来是几次」真正互相校验。
func scan(t *testing.T) (skipaudit.Result, error) {
	t.Helper()
	return skipaudit.Check(ledger, dir(t), nil)
}
