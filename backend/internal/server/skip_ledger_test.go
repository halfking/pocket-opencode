// skip_ledger_test.go —— 本包「被跳过的测试」逐条台账（§467 纪律的续包）。
//
// 为什么有这道门
// ----------------
// `go test` 把 SKIP 报成 ok。§467 为 internal/stt 建了第一道逐条台账，
// internal/notes 是第二个；internal/server 是**全仓跳过点最密的包之一**
// （36 个测试 / 39 个跳过点），在它建这道门之前是**零登记**。
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
// ⛔ 本文件**不含**跳过调用字面量，因此**不需要任何豁免**。
//
//	§467 的 stt 台账必须豁免它自己（负控样本里有一段原始字符串写着
//	一个假测试函数，函数体里调跳过）—— 而豁免本身是一种
//	长期风险（§467 为此专门写了一条自证）。改用 AST 后这一类豁免可以
//	整体消失：豁免没了，「豁免烂成通行证」的路也就没了。
//
// 本包的形状与 notes 不同的地方（说清楚，免得下一个人以为是抄漏了）
// ------------------------------------------------------------------
// 本包 39 个跳过点里，**17 个是同一批「真网关探针」**
// （守卫一律是 `os.Getenv("POCKET_LIVE_GATEWAY") != "1"`，靠它跑会消耗
// 真实额度）。它们是取证工具而不是门禁，所以逐条登记、逐条写清解锁
// 条件，而不是给一个通配豁免 —— 通配豁免正是 §467 要治的病。
//
// 另外本包有 **13 条 CatPremiseFailed**，占比远超其它包：8 条是
// `if srv.<store> != nil { 跳过 }` 这一族 —— 测试服务器一旦挂上真实
// store，要断言的「nil-store 默认形态」就永远观察不到，用例**再也不会失败**。
// 这一族的代价不是「暂时没跑」，是「永远不会红」，所以单独立类并钉住计数。
package server

import (
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/skipaudit"
)

// knownSkips 是本包**全部**拥有跳过点的函数（owner）。
// ⛔ 不在表内的一律红。
//
// ⚠ **登记单位是 owner（拥有跳过点的函数），不是「测试」** —— 2026-10-21 改的：
//
//	本包的跳过点有一部分在 **helper**（夹具函数）里，而 helper 被 N 个测试共用。
//	若登记单位是「测试」，那 N 个测试就要写 N 条内容相同的条目 ——
//	而「为什么没跑」「需要什么才能跑」**只取决于那个 helper**。
//	⇒ 台账会烂成 N 份互相抄写的副本，抄错一份没人发现；
//	  而新增一个调用该 helper 的测试就会让门转红（纯粹的无意义抖动）。
//
//	每个跳过点**有且只有一个 owner** ⇒ 「每个 owner 都登记了」
//	⇒「每个跳过点都被登记了」⇒「运行时跳过的每一条都有台账可查」。
//
// 本包 2026-10-21 实测：57 个跳过点 / 48 个 owner / 186 个测试受影响。
//
// 三条纪律之外的第四条（台账自身的纪律）：**Unlock 必须可复制粘贴**。
// 写「配置一下」「需要凭据」等于没写 —— §464 的教训是下一个会话会把
// 「跳过」重新登记成「没查」，而校验器看不出这两者的区别。
// 所以下面每条 Unlock 都落到具体的 env 键名，或具体到「要改哪个构造路径」。
var knownSkips = []skipaudit.Entry{
	skipaudit.EP("TestBodyCacheAssertionsDoNotAllUseZeroUID", skipaudit.CatHostCapability, "它普查本目录的 .go 源码、数 readCachedEmailBody 的调用点里有多少个还在传字面量 0；"+
		"glob 不到任何 .go 文件时无从普查，跳过。", 1, "无 env 开关：在 backend/internal/server 目录下执行 `go test ./internal/server/ -run TestBodyCacheAssertionsDoNotAllUseZeroUID -v`", "守卫是 `filepath.Glob(filepath.Join(\".\", \"*.go\"))` 返回空 —— 用 `go test -c` 出的二进制在别处跑、或把工作目录换掉就会命中"),

	skipaudit.EP("TestCollaborationRoutesAreRegistered", skipaudit.CatDefensiveGuard, "它要钉的是「协作路由已挂载」：无 store 时该报 503、漏挂才报 404。"+
		"而 newMobileRouteServer 造出的 Server 已带真实 taskStore，这个前提恒不成立 ⇒ 用例永远不会失败。", 1, "无 env 可设：需改构造路径 —— 让 newMobileRouteServer（mobile_session_security_test.go:60 调 New(...) 的那处）传 nil taskStore，或本用例改用一个显式无 store 的构造器", "在那之前，它在 CI 上恒跳过 ⇒ 绿色读数与「跑过并通过」无法区分"),

	{
		Owner:  "TestDiagICBCStatementAttachment",
		Cat:    skipaudit.CatDiagProbe,
		Reason: "只读诊断探针：核对工行对账单附件在加密缓存里的头部 UID 与算法号是否与库里一致；" + "两个守卫分别是显式 opt-in 与数据目录。",
		Unlock: []string{"POCKET_DIAG_ICBC_ATTACH=1", "POCKET_DIAG_DATA_DIR=<含 email_master.key 的数据目录>（缺它时守卫拒绝猜目录）", "go test ./internal/server -run TestDiagICBCStatementAttachment -v"},
		Points: 2,
	},

	skipaudit.EP("TestDiagStreamZeroFrames", skipaudit.CatCredential, "诊断「流式响应零帧」：要真发一次网关请求才知道有没有帧；消耗真实额度，故默认不跑。", 1, "POCKET_LIVE_GATEWAY=1", "POCKET_LLM_GATEWAY_URL=<网关地址> 与 POCKET_LLM_GATEWAY_API_KEY=<key>（缺任一个它直接 Fatal）", "/tmp/real-refine-prompt.txt（提示词文件，由 live_refine_prompt_dump_test.go 导出）"),

	skipaudit.EP("TestEmailClassifyModel_PrefersGatewayOverCfg", skipaudit.CatDefensiveGuard, "它钉的是「分类模型 = 网关 preferredModels 首项，cfg.LLMModel 只是兜底」；"+
		"默认列表为空时兜底真的会生效，作者自己在消息里写了「语义已变，请更新本用例」。", 1, "无 env 可设：需重新登记 opencode.DefaultLLMGatewayPreferredModels（非空硬编码默认）后重写本用例的判据", "在那之前本用例恒跳过，而它要钉的「配置页的 LLMModel 对分类无效」无人验证"),

	skipaudit.EP("TestRefineRejectedCarriesItsReason", skipaudit.CatDefensiveGuard, "它的第二个子用例要钉的是「参会人名单泄漏 ⇒ 精校被拦下并带出 participant_leak 成因」；"+
		"而夹具（把「林岚」同时放进名单和正文）没触发那条 ⇒ 断言形同虚设 ⇒ 停下来说明。", 1, "无 env 可设：需修夹具 —— 让 guardRefineResult 在该夹具上真的判定 participantLeak（否则这条子用例永远不成立）", "在那之前本用例的这半个断言在 CI 上恒跳过 ⇒ 「读侧拦住了名单泄漏」无人验证"),

	{
		Owner:  "TestExportRefinePrompt",
		Cat:    skipaudit.CatShortMode,
		Reason: "把生产同款精校提示词导出成文件供延迟测量脚本用；是工具而非断言，且要指定输出文件。",
		Unlock: []string{"不加 -short 运行（第一个守卫是 testing.Short()）", "POCKET_EXPORT_REFINE_PROMPT=<输出文件路径>（第二个守卫：为空则跳过）", "可选输入：POCKET_EXPORT_REFINE_TRANSCRIPT=<长转写文件> / POCKET_EXPORT_REFINE_TITLE= / POCKET_EXPORT_REFINE_LOCATION="},
		Points: 2,
	},

	{
		Owner:  "TestExportSummaryPrompt",
		Cat:    skipaudit.CatShortMode,
		Reason: "把滚动摘要提示词导出成文件供延迟测量用；直接调被测的 buildSummaryPrompt，避免手抄漂移。",
		Unlock: []string{"不加 -short 运行（第一个守卫是 testing.Short()）", "POCKET_EXPORT_SUMMARY_PROMPT=<输出文件路径>（第二个守卫：为空则跳过）", "可选输入：POCKET_EXPORT_SUMMARY_TRANSCRIPT=<转写文件> / POCKET_EXPORT_SUMMARY_PREV_FILE=<已有摘要文件>"},
		Points: 2,
	},

	skipaudit.EP("TestExportTruncateUnitFixture", skipaudit.CatFixtureRegen, "它是**重新生成**已提交的截断向量夹具（testdata/truncate_unit_vectors.json）；"+
		"平时跑的是读夹具做对照的 TestTruncateUnitFixtureMatchesRealTruncateStr，不需要这一步。", 1, "UPDATE_TRUNCATE_FIXTURE=1", "cd backend && UPDATE_TRUNCATE_FIXTURE=1 go test ./internal/server/ -run TestExportTruncateUnitFixture -v", "⚠ 它会覆写 testdata/truncate_unit_vectors.json，diff 需人眼确认后再提交"),

	skipaudit.EP("TestFlashcardsCreateDeckRouteExists", skipaudit.CatDefensiveGuard, "要断言的是「nil-store 时建卡组路由返回 503 而非 404」；"+
		"测试服务器已带真实 flashcards store ⇒ 前提恒不成立 ⇒ 永远不会失败。", 1, "无 env 可设：需改 newMobileRouteServer 的构造路径，让 srv.flashcardStore 保持 nil（flashcards_deck_route_test.go:25 的守卫）", "在那之前，这条「路由有没有挂上」的锁在 CI 上不存在"),

	skipaudit.EP("TestFlashcardsCreateNoteRouteOnNotesSubpath", skipaudit.CatDefensiveGuard, "BUG-L 的回归锁：flashcard store 为 nil 时该路由应落在 /notes 子路径上；"+
		"store 一旦存在，这个「nil-store 默认」前提就不成立。", 1, "无 env 可设：需改 newMobileRouteServer 的构造路径让 srv.flashcardStore 为 nil（flashcards_create_note_route_test.go:33）", "在那之前，BUG-L 是否复发无人验证"),

	skipaudit.EP("TestFlashcardsReviewRejectsInvalidRatingAsBadRequest", skipaudit.CatDefensiveGuard, "要断言非法评分在 flashcard store 缺失时被拒；测试服务器已带真实 store ⇒ 前提恒不成立。", 1, "无 env 可设：需改 newMobileRouteServer 的构造路径让 srv.flashcardStore 为 nil（flashcards_review_rating_test.go:30）", "该用例自身随后会 SetFlashcardStore(&flashcards.Store{})，所以它要的是**构造期**的 nil"),

	skipaudit.EP("TestLearningRoutesAreRegistered", skipaudit.CatDefensiveGuard, "要断言学习路由已挂载（无 service 时 503、漏挂 404）；"+
		"测试服务器已注入真实 learningService ⇒ 前提恒不成立。", 1, "无 env 可设：需改 newMobileRouteServer 的构造路径让 srv.learningService 为 nil（learning_route_test.go:24）", "在那之前，UI 上学习页整块不可达也不会有人看见"),

	skipaudit.EP("TestLiveCompanionSessionInventory", skipaudit.CatCredential, "对**真跑起来的** agent-companion 守护进程做会话清单实测；"+
		"守护进程不在本机时无法对话，且这是它当初漏接 CompanionURL 导致整条读路径静默失效的回归锁。", 1, "AC_E2E_COMPANION_URL=http://127.0.0.1:8095", "AC_E2E_COMPANION_SECRET=<AC_API_SECRET>（鉴权用）", "go test ./internal/server/ -run TestLiveCompanion -v"),

	skipaudit.EP("TestLiveGatewayAgentReferencesAreReal", skipaudit.CatCredential, "真网关探针：验 agent 给出的引用 id 是不是真的来自上游检索结果；依赖外网并消耗真实额度。", 1, "POCKET_LIVE_GATEWAY=1", "POCKET_LLM_GATEWAY_URL=<网关地址> 与 POCKET_LLM_GATEWAY_API_KEY=<key>", "go test ./internal/server -run TestLiveGateway -v"),

	skipaudit.EP("TestLiveGatewayAgentToolCall", skipaudit.CatCredential, "真网关探针：验真实网关到底吐不吐 tool_calls —— 单测验不到这一段。", 1, "POCKET_LIVE_GATEWAY=1", "POCKET_LLM_GATEWAY_URL=<网关地址> 与 POCKET_LLM_GATEWAY_API_KEY=<key>", "可选 POCKET_LLM_GATEWAY_FORMAT / POCKET_LLM_GATEWAY_MODEL（anthropic 形态不接受 \"auto\"，必须给具体模型名）", "可选 POCKET_REFINE_GLOSSARY=（术语表档位，all 或具体词表路径）"),

	skipaudit.EP("TestLiveGatewayMeetingRecommend", skipaudit.CatCredential, "真网关探针：验会议建议链在真实模型下的产出形态；消耗真实额度、依赖外网。", 1, "POCKET_LIVE_GATEWAY=1", "POCKET_LLM_GATEWAY_URL=<网关地址> 与 POCKET_LLM_GATEWAY_API_KEY=<key>", "可选 POCKET_LLM_GATEWAY_FORMAT / POCKET_LLM_GATEWAY_MODEL / POCKET_REFINE_GLOSSARY"),

	skipaudit.EP("TestLiveGatewayMeetingRefine", skipaudit.CatCredential, "真网关探针：验精校链在真实模型下改不改、同音错改不改对。", 1, "POCKET_LIVE_GATEWAY=1", "POCKET_LLM_GATEWAY_URL=<网关地址> 与 POCKET_LLM_GATEWAY_API_KEY=<key>", "可选 POCKET_LLM_GATEWAY_FORMAT / POCKET_LLM_GATEWAY_MODEL / POCKET_REFINE_GLOSSARY"),

	skipaudit.EP("TestLiveGatewayMeetingRefineLongTranscript", skipaudit.CatCredential, "真网关探针：长转写下的精校行为与首字节预算（§44.8 的慢模型口径）。", 1, "POCKET_LIVE_GATEWAY=1", "POCKET_LLM_GATEWAY_URL=<网关地址> 与 POCKET_LLM_GATEWAY_API_KEY=<key>", "可选 POCKET_LLM_GATEWAY_FORMAT / POCKET_LLM_GATEWAY_MODEL / POCKET_REFINE_GLOSSARY"),

	skipaudit.EP("TestLiveGatewayNoteSummaryJSON", skipaudit.CatCredential, "真网关探针：验笔记总结的 JSON 有没有被真实模型遵守（schema 约束只在真模型上验得到）。", 1, "POCKET_LIVE_GATEWAY=1", "POCKET_LLM_GATEWAY_URL=<网关地址> 与 POCKET_LLM_GATEWAY_API_KEY=<key>", "可选 POCKET_LLM_GATEWAY_FORMAT / POCKET_LLM_GATEWAY_MODEL / POCKET_REFINE_GLOSSARY"),

	skipaudit.EP("TestLiveGatewayRefineFixesASRErrors", skipaudit.CatCredential, "真网关精校质量探针：验「同音错被改对」这条收益是否真实存在。", 1, "POCKET_LIVE_GATEWAY=1", "POCKET_LLM_GATEWAY_URL=<网关地址> 与 POCKET_LLM_GATEWAY_API_KEY=<key>", "可选 POCKET_LLM_GATEWAY_FORMAT / POCKET_LLM_GATEWAY_MODEL / POCKET_REFINE_GLOSSARY"),

	skipaudit.EP("TestLiveGatewayRefineTermHintTopic", skipaudit.CatCredential, "主题术语表配对实测：验术语表配对能不能在真实模型上把错词改对。", 1, "POCKET_LIVE_GATEWAY=1", "POCKET_LLM_GATEWAY_URL=<网关地址> 与 POCKET_LLM_GATEWAY_API_KEY=<key>"),

	skipaudit.EP("TestLiveGatewayRefineTopicVsTitle", skipaudit.CatCredential, "主题 vs 标题的 A/B 对照：验零成本的「靠会议标题修术语」修复路径是否真的成立。", 1, "POCKET_LIVE_GATEWAY=1", "POCKET_LLM_GATEWAY_URL=<网关地址> 与 POCKET_LLM_GATEWAY_API_KEY=<key>"),

	skipaudit.EP("TestLiveGatewayRefsFabricationRate", skipaudit.CatCredential, "引用捏造率实测（§328/§329）：验 agent 有没有编造检索结果里的引用。", 1, "POCKET_LIVE_GATEWAY=1", "POCKET_LLM_GATEWAY_URL=<网关地址> 与 POCKET_LLM_GATEWAY_API_KEY=<key>", "可选 POCKET_FAB_N=<每臂采样数，默认 6> / POCKET_FAB_OUT=<落盘目录，默认 /tmp/opstt/328> / POCKET_FAB_ARMS=<臂选择>"),

	skipaudit.EP("TestLiveGatewayRollingSummary", skipaudit.CatCredential, "真网关探针：验滚动摘要链（summaryJSONSchema、45s+30s 预算）在真实模型下的形态。", 1, "POCKET_LIVE_GATEWAY=1", "POCKET_LLM_GATEWAY_URL=<网关地址> 与 POCKET_LLM_GATEWAY_API_KEY=<key>", "可选 POCKET_LLM_GATEWAY_FORMAT / POCKET_LLM_GATEWAY_MODEL / POCKET_REFINE_GLOSSARY"),

	skipaudit.EP("TestLiveLongStream", skipaudit.CatCredential, "长转写下的流式真网关探测；消耗真实额度，且提示词须事先从本包导出。", 1, "POCKET_LIVE_GATEWAY=1", "POCKET_LLM_GATEWAY_URL=<网关地址> 与 POCKET_LLM_GATEWAY_API_KEY=<key>", "/tmp/real-refine-prompt-long.txt（长样本提示词，先跑 live_refine_prompt_dump 的导出用例生成）", "go test ./internal/server -run TestLiveLongStream -v"),

	skipaudit.EP("TestLiveNextMeetingRaw", skipaudit.CatCredential, "下一场会议推荐的**协议形态 A/B** 真跑：单轮预算 240s、整体要 -timeout 60m。", 1, "POCKET_LIVE_GATEWAY=1", "POCKET_LLM_GATEWAY_URL=<网关地址> 与 POCKET_LLM_GATEWAY_API_KEY=<key>", "可选 POCKET_LLM_GATEWAY_FORMAT / POCKET_LIVE_NEXT_MEETING_MODELS=<逗号分隔模型名单> / POCKET_LIVE_NEXT_MEETING_ROUNDS=<轮数，默认 2> / POCKET_LIVE_NEXT_MEETING_ARMS=<臂> / POCKET_LIVE_NEXT_MEETING_OUT=<落盘 JSON，默认 /tmp/opstt/next-meeting-live.json>", "go test ./internal/server -run TestLiveNextMeetingRaw -v -timeout 60m"),

	skipaudit.EP("TestLiveRefineChain", skipaudit.CatShortMode, "真网关探针（短模式守卫）：验真实精校输出里有没有带期限的行动项；消耗真实额度。", 1, "不加 -short 运行（唯一守卫是 testing.Short()）", "另需 POCKET_LIVE_GATEWAY=1 / POCKET_LLM_GATEWAY_URL / POCKET_LLM_GATEWAY_API_KEY，否则内层 liveProbeService 会跳过", "可选 POCKET_LIVE_REFINE_MODEL=<模型名，默认 glm-5.2>"),

	skipaudit.EP("TestLiveRefineRawOutput", skipaudit.CatShortMode, "真网关探针（短模式守卫）：落盘原始输出以区分「模型把同音错改对」与「模型引入外人」。", 1, "不加 -short 运行（唯一守卫是 testing.Short()）", "另需 POCKET_LIVE_GATEWAY=1 / POCKET_LLM_GATEWAY_URL / POCKET_LLM_GATEWAY_API_KEY，否则内层 liveProbeService 会跳过", "可选 POCKET_LIVE_REFINE_MODEL=<模型名，默认 glm-5.2>"),

	skipaudit.EP("TestMeetingIngestDriftGuardHasTeeth", skipaudit.CatDefensiveGuard, "它是漂移门的**负控**（证明 diffStrings 真有牙）；"+
		"knownDroppedTags 已被 §712 收窄成空 ⇒「少一个键」这一臂无键可少、且 mutate 会 panic ⇒ 本臂不适用。", 1, "无 env 可设：需让 knownDroppedTags 重新非空（meeting_ingest_fields_test.go:69），即前端与 CreateMeetingRequest 的 tag 差集再次出现键", "或改写该臂：在空 base 下也能造出「少一个键」的对照（当前 s[:len(s)-1] 遇 len=0 会 panic）"),

	skipaudit.EP("TestNoteSearcherScopeProbe", skipaudit.CatDiagProbe, "作用域探针：验证检索是否强制按 userID/workspaceID 限定作用域；"+
		"作者刻意同时保留红（复现缺陷）与绿（判据）两种状态，所以默认不跑。", 1, "POCKET_PROBE_SCOPE=1", "go test ./internal/server -run TestNoteSearcherScopeProbe -v", "⚠ 它**在缺陷未修时是红的** —— 这是刻意的，跑它前先知道自己在验什么"),

	skipaudit.EP("TestNotesUpdateRouteAcceptsPutAndPatch", skipaudit.CatDefensiveGuard, "要断言 notes 更新路由同时接受 PUT 与 PATCH（nil-store 时 503、漏挂 404）；"+
		"测试服务器已带真实 notesStore ⇒ 前提恒不成立。", 1, "无 env 可设：需改 newMobileRouteServer 的构造路径让 srv.notesStore 为 nil（notes_update_route_test.go:28）", "在那之前，前端二选一改法是否被后端接受无人验证"),

	skipaudit.EP("TestPOP3RawCache_RealCorpusRoundTrip", skipaudit.CatCredential, "用**真实 POP3 语料**做加密缓存往返；真实语料不进仓库，没有数据目录就没有被测对象。", 1, "POCKET_DIAG_POP3_RAW=<含 email_master.key 的数据目录>", "go test ./internal/server -run TestPOP3RawCache_RealCorpusRoundTrip -v"),

	skipaudit.EP("TestPipeline_FeishuSkipIsInvisibleInReport", skipaudit.CatDefensiveGuard, "它钉的是「飞书未配置时发票推送静默跳过、报告里看不出来」；"+
		"本机一旦配了飞书凭据，要断言的那一支就永远不存在 ⇒ 用例永远不会失败。", 1, "无 env 可设：需让 Pusher.Available() 为假 —— 即清空 POCKET_FEISHU_APP_ID / POCKET_FEISHU_APP_SECRET / POCKET_FEISHU_INVOICE_CHAT_ID（config.go:306/307/354）", "或在无飞书凭据的机器/CI 上跑；这正是需求 3「发送到飞书」在当前部署里结构上跑不起来的那一支"),

	skipaudit.EP("TestRunEventsPathIsNotHijackedByActivityStream", skipaudit.CatDefensiveGuard, "要钉 ACC 的 run-event 路由没被协作 activity stream 吞掉路径；"+
		"测试服务器已带真实 taskStore ⇒ 前提恒不成立。", 1, "无 env 可设：需改 newMobileRouteServer 的构造路径让 srv.taskStore 为 nil（task_collaboration_route_test.go:67）", "在那之前，任务详情页的运行历史被吞掉也不会有人看见"),

	skipaudit.EP("TestSTTOutboundDialerHonorsSameSwitchAsValidation", skipaudit.CatDefensiveGuard, "它要真起一个 loopback 上游、比对「校验层与拨号层认同同一个开关」；"+
		"httptest 起的上游不在 127.0.0.1 上时（部分平台解析成 ::1）这组对照就失去意义。", 1, "无 env 可设：需让上游落在 loopback —— 或显式用 httptest.NewUnstartedServer + 自定义 127.0.0.1 Listener 再 Start()", "或把守卫从字符串匹配改成解析 net.SplitHostPort(upstream.URL) 后判 host 是否回环（当前守卫：server_stt_url_test.go:109）"),

	skipaudit.EP("TestTaskFromSourceRouteIsRegistered", skipaudit.CatDefensiveGuard, "要断言 from-source 任务路由已挂载（nil-store 时 503、漏挂 404）；"+
		"测试服务器已带真实 taskStore ⇒ 前提恒不成立。", 1, "无 env 可设：需改 newMobileRouteServer 的构造路径让 srv.taskStore 为 nil（task_from_source_route_test.go:19）", "在那之前，协作整块不可达也不会有人看见"),

	skipaudit.EP("liveProbeService", skipaudit.CatCredential,
		"真网关探针的统一入口：没有 POCKET_LIVE_GATEWAY=1 就跳过（4 个测试受影响）。⚠ 跑它会消耗真实额度。", 1,
		"POCKET_LIVE_GATEWAY=1",
		"POCKET_LLM_GATEWAY_URL=<网关地址> 与 POCKET_LLM_GATEWAY_API_KEY=<key>"),
	skipaudit.EP("mustTestPool", skipaudit.CatCredential,
		"server 的 PG 集成夹具：五道守卫（DSN 没设 / DSN 解析不了 / 连不上 / PG 不可达 / ……）都落到同一个出口。\n\t\t\t⚠ 它罩住 **275 个测试** —— 本包最大的一处跳过面。", 5,
		"POCKET_TEST_POSTGRES_DSN=<可写的测试库 DSN>",
		"DSN 必须是 pgx 能解析的 URL",
		"目标库必须允许建 schema（隔离 schema 用随机后缀）"),
	skipaudit.EP("newACCGateTaskStore", skipaudit.CatCredential,
		"移动端审批 ACC 闸的 PG 集成夹具：没有 DSN 就跳过（27 个测试受影响）。", 1,
		"POCKET_TEST_POSTGRES_DSN=<可写的测试库 DSN>"),
	skipaudit.EP("newInvoiceScopedStore", skipaudit.CatCredential,
		"发票派发的 PG 集成夹具：没有 DSN 就跳过（34 个测试受影响）。", 1,
		"POCKET_TEST_POSTGRES_DSN=<可写的测试库 DSN>"),
	skipaudit.EP("newIsolatedSchedTaskPool", skipaudit.CatCredential,
		"计划任务隔离 schema 的 PG 夹具：三道守卫（DSN 没设 / 连不上 / PG 不可达）都落到同一个出口；没有 DSN 就跳过（6 个测试受影响）。", 3,
		"POCKET_TEST_POSTGRES_DSN=<可写的测试库 DSN>",
		"目标库必须允许建 schema"),
	skipaudit.EP("newNotifyAdapterFixture", skipaudit.CatCredential,
		"邮件通知适配器的 PG 集成夹具：没有 DSN 就跳过（4 个测试受影响）。", 1,
		"POCKET_TEST_POSTGRES_DSN=<可写的测试库 DSN>"),
	skipaudit.EP("authorityDelegationPG", skipaudit.CatCredential,
		"工作区委托的真实 PostgreSQL 成员与 nonce 重放保护夹具；没有测试 DSN 不连接数据库。", 1,
		"POCKET_TEST_POSTGRES_DSN=<允许创建隔离 schema 的可写测试库 DSN>",
		"cd backend && go test ./internal/server -run TestWorkspaceAuthorityDelegation -count=1"),
	skipaudit.EP("newRSSRoutePG", skipaudit.CatCredential,
		"RSS 路由的 PG 集成夹具：没有 DSN 就跳过（3 个测试受影响）。", 1,
		"POCKET_TEST_POSTGRES_DSN=<可写的测试库 DSN>"),
	skipaudit.EP("newTestPGAuditStore", skipaudit.CatCredential,
		"server 审计链的 PG 集成夹具：没有 DSN 就跳过（5 个测试受影响）。", 1,
		"POCKET_TEST_POSTGRES_DSN=<可写的测试库 DSN>"),
	skipaudit.EP("newTestPGEmailStore", skipaudit.CatCredential,
		"发票汇总的 PG 集成夹具：没有 DSN 就跳过（2 个测试受影响）。", 1,
		"POCKET_TEST_POSTGRES_DSN=<可写的测试库 DSN>"),
	skipaudit.EP("readFrontend", skipaudit.CatHostCapability,
		"跨实现契约测试要读前端源码（frontend/src 的去重实现）；读不到就跳过（15 个测试受影响）。", 1,
		"在含前端源码的仓库里运行（夹具按相对路径读 frontend/src）",
		"或让该夹具能定位到 frontend 目录"),
	skipaudit.EP("s945InvoiceStore", skipaudit.CatCredential,
		"发票导出失败路径的 PG 集成夹具：没有 DSN 就跳过（2 个测试受影响）。", 1,
		"POCKET_TEST_POSTGRES_DSN=<可写的测试库 DSN>"),
}

var ledger = skipaudit.Ledger{
	Pkg:      "./internal/server",
	Entries:  knownSkips,
	MinFuncs: 48, // 实测 48 个 owner
}

// ⚠ Points 是**手写**的，不是 init() 里自动补的。
//
// 自动补（skipaudit.Register）会让 TestSkipLedger_双向精确匹配 里
// 「台账点数 == 实际点数」这条交叉校验**恒真** —— 判据变成永远通过。
// 2026-10-17 首版就是自动补的，当场把这条校验废掉了。
// 手写才能让「我以为它是几次」与「AST 数出来是几次」互相校验。
// 本包有 3 个条目是 2 个跳过点，其余 33 个是 1 个 ⇒ 合计 39。

func dir(t *testing.T) string {
	t.Helper()
	d, err := skipaudit.DirOf("internal/server")
	if err != nil {
		t.Fatalf("定位包目录失败: %v", err)
	}
	return d
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
	// 台账登记的点数必须与实际点数一致 —— 这是台账与 census 的交叉口径
	if res.LedgeredPoints != res.Points {
		t.Errorf("台账点数 %d ≠ 实际点数 %d ⇒ Points 字段没跑 Register 补齐",
			res.LedgeredPoints, res.Points)
	}
}

// 类别分布要**显式**钉住：新增一个「哑弹守卫」时，
// 必须有人在这里改一个常量，而不是悄悄混进总量里。
//
// ⚠ 这一类的语义在 2026-10-21 被**实测推翻**过一次：
//
//	它曾经叫 CatPremiseFailed（「前提不成立 ⇒ 永远不会失败」）。
//	实测结果是：那批守卫**全部没有触发** ⇒ 那些测试今天**是在断言的**，
//	不是「永远失败」。名字写错 ⇒ 下一个会话会按错的前提去处理它
//	（去「修好那个永远失败的测试」，而它其实一直是绿的）。
//
// ⇒ 现在叫 CatDefensiveGuard（哑弹守卫），说的是真话：
//
//	守卫的前提**当前恒成立** ⇒ 这条测试今天在跑、在断言；
//	而一旦前提成立，它会**静默退出门外** ——
//	那时绿色读数与「跑过并通过」完全无法区分。
func TestSkipLedger_哑弹守卫计数被钉住(t *testing.T) {
	const wantDefensiveGuard = 13
	got := 0
	for _, e := range knownSkips {
		if e.Cat == skipaudit.CatDefensiveGuard {
			got++
		}
	}
	if got != wantDefensiveGuard {
		t.Errorf("哑弹守卫类实际 %d 条，钉住值是 %d。\n"+
			"  ⇒ 新增一条要**有意识地**改这个常量并说明理由：\n"+
			"    这类的代价不是「暂时没跑」，而是「**哑弹**」——\n"+
			"    守卫的前提当前恒成立 ⇒ 它永不触发 ⇒ 这条测试今天**是在断言的**；\n"+
			"    而一旦前提成立，它会静默退出门外，那时绿色读数与「跑过并通过」无法区分。",
			got, wantDefensiveGuard)
	}
}

func scan(t *testing.T) (skipaudit.Result, error) {
	t.Helper()
	return skipaudit.Check(ledger, dir(t), nil)
}
