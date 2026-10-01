package server

// server_email_pipeline_adapters_test.go — 需求 1/2/3/4 的**手动触发入口**与
// 三个外部依赖适配器（飞书推送 / 飞书台账 / 重要邮件提醒）。
//
// ## 为什么测这里
//
// `handleEmailPipelineRun` 是「每天定时或手工进行邮件接收，然后进行处理」这条
// 需求里**手工**那一半的入口，此前 0% 覆盖。而它身上挂着一个安全性不变量：
//
//	// 覆盖只作用于本轮：跑完立刻恢复成配置值，否则一次 dryRunSpam:false
//	// 会永久改掉后续每日定时任务的行为。
//
// 一次 `dryRunSpam:false` 就是**对真实邮箱执行不可逆的 IMAP MOVE**。
// 如果这个覆盖泄漏到下一轮，用户在某次调试时按了「真实执行」，之后每天 06:00
// 的定时任务都会真的搬邮件，而且没人会去看配置项是否被改过。这条不变量值得
// 有测试守着，而不是只靠一行注释。
//
// 三个适配器的 `Available()` 则是另一类：它们决定「这条腿到底跑没跑」。
// 全仓没有任何测试覆盖过它们，于是「飞书没配 → 发票推送这一步静默跳过」
// 这件事在报告里看不出来（见 TestPipeline_FeishuSkipIsInvisibleInReport）。
//
// ## 负控（实测过，见 handoff §7df）
//
// 把 `defer func() { p.SpamDryRun = prev }()` 改成恢复成 `*spamOverride`
// → TestRunPipeline_DryRunOverrideDoesNotLeak 转红（两条断言）。
//
// 第一版负控直接删掉那一行，结果 `prev` 变成未使用变量、**编译失败** ——
// 负控必须能编译，否则「build failed」会被误当成测试通过或护栏问题。
//
// ## 一条**没有**负控的用例，及原因
//
// `TestPipeline_FeishuSkipIsInvisibleInReport` 断言的是「飞书未配置时报告里
// FeishuPushed=0 / FeishuFailed=0 / Errors 为空」。我原计划的负控是去掉
// `pushInvoiceSet` 的 `!p.Pusher.Available()` 短路 —— 但隔离 schema 里**没有
// 任何发票**，`pushInvoiceSet` 的循环体一次都不执行，于是无论短路在不在，
// 报告都是那三个 0。**这条负控在当前夹具下不可能转红**，不声称它有效。
// 要给它配负控，需要先在隔离 schema 里放一条 status=downloaded、feishu_sent_at=0
// 且 FilePath 指向真实文件的发票；那会让流水线去真的发网络请求，属于另一轮的工作。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/config"
	"github.com/halfking/pocket-opencode/backend/internal/email"
	"github.com/halfking/pocket-opencode/backend/internal/feishu"
	"github.com/halfking/pocket-opencode/backend/internal/notifycenter"
)

// ---- 手动触发入口 ----

func TestPipelineRunHandler_Guards(t *testing.T) {
	cases := []struct {
		name, method, body string
		want               int
		msg                string
	}{
		{"拒非 POST", http.MethodGet, "", 405, "POST only"},
		{"无库", http.MethodPost, "", 503, "email store not configured"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &Server{}
			r := httptest.NewRequest(c.method, "/api/emails/pipeline/run", strings.NewReader(c.body))
			w := httptest.NewRecorder()
			s.handleEmailPipelineRun(w, r)
			if got := errOf(w); got.code != c.want || got.msg != c.msg {
				t.Fatalf("=> (%d, %q), want (%d, %q)", got.code, got.msg, c.want, c.msg)
			}
		})
	}
}

// 什么都没配时，端点回的是 **HTTP 200**，错误只藏在 body 的 errors 数组里。
//
// 这不是 bug（流水线本身就是「跑完把报告给你」），但它是个陷阱：只判状态码的
// 调用方会以为「跑成功了」。所以把「200 + errors 非空」这个组合钉住，
// 免得哪天有人改成 500 让两边判据打架，或反过来。
func TestPipelineRunHandler_UnconfiguredIs200WithErrorInBody(t *testing.T) {
	store, cleanup := newInvoiceScopedStore(t)
	defer cleanup()
	// 有 store、无 fetcher、无 dataDir → ensurePipeline 返回 nil。
	s := &Server{emailStore: store}
	r := httptest.NewRequest(http.MethodPost, "/api/emails/pipeline/run", nil)
	w := httptest.NewRecorder()
	s.handleEmailPipelineRun(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200（错误在 body 里）", w.Code)
	}
	var rep email.PipelineReport
	if err := json.Unmarshal(w.Body.Bytes(), &rep); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rep.Errors) != 1 || !strings.Contains(rep.Errors[0], "email pipeline not configured") {
		t.Fatalf("errors = %v, want 一条 'email pipeline not configured'", rep.Errors)
	}
}

// 核心安全不变量：dryRunSpam 覆盖**只作用于本轮**。
func TestRunPipeline_DryRunOverrideDoesNotLeak(t *testing.T) {
	store, cleanup := newInvoiceScopedStore(t)
	defer cleanup()
	ctx := context.Background()
	dataDir := t.TempDir()

	// 配置值 true（预演）。fetcher 不连网 —— 隔离 schema 里没有任何账户，
	// Run 的账户循环无事可做，几毫秒返回。
	s := &Server{
		emailStore:   store,
		emailFetcher: email.NewFetcher(store, nil),
		dataDir:      dataDir,
		cfg:          config.Config{EmailSpamDryRun: true},
	}
	p := s.ensurePipeline()
	if p == nil {
		t.Fatal("ensurePipeline 返回 nil，测试前提不成立")
	}
	if !p.SpamDryRun {
		t.Fatalf("初始 SpamDryRun = %v, want true（应取自配置）", p.SpamDryRun)
	}

	// 手动触发一次「真实 MOVE」。
	override := false
	rep := s.runEmailPipeline(ctx, &override)
	if rep == nil {
		t.Fatal("runEmailPipeline 返回 nil report")
	}
	// 关键断言：跑完必须**恢复成配置值 true**。若为 false，说明一次手动
	// dryRunSpam:false 泄漏到了下一轮，之后每日 06:00 的定时任务会真的
	// 对真实邮箱执行 IMAP MOVE（不可逆），且没人会去看配置项是否被改过。
	if !p.SpamDryRun {
		t.Errorf("跑完之后 SpamDryRun = false：一次手动 dryRunSpam:false 泄漏到了下一轮！" +
			" 之后的每日定时任务会真的对真实邮箱执行 IMAP MOVE（不可逆）")
	}

	// 再跑一次不带覆盖的，必须仍是配置值 true。
	s.runEmailPipeline(ctx, nil)
	if !p.SpamDryRun {
		t.Errorf("第二轮后 SpamDryRun = %v, want true（配置值）", p.SpamDryRun)
	}
}

// override 传 nil（不传 body）时不得改动任何东西。
func TestRunPipeline_NoOverrideLeavesConfigValue(t *testing.T) {
	store, cleanup := newInvoiceScopedStore(t)
	defer cleanup()
	s := &Server{
		emailStore:   store,
		emailFetcher: email.NewFetcher(store, nil),
		dataDir:      t.TempDir(),
		cfg:          config.Config{EmailSpamDryRun: true},
	}
	p := s.ensurePipeline()
	if p == nil {
		t.Fatal("ensurePipeline 返回 nil")
	}
	s.runEmailPipeline(context.Background(), nil)
	if !p.SpamDryRun {
		t.Errorf("未传覆盖时 SpamDryRun 被改成了 false")
	}
}

// 【已查清的行为，非缺陷】飞书没配时，发票推送**静默跳过**：报告里
// FeishuPushed=0、FeishuFailed=0、Errors 为空 —— 与「有发票但都推成功了」
// 的数字**完全一样**。唯一的线索是启动时那一行 log。
//
// 影响：需求 3「发送到飞书」在当前部署（`POCKET_FEISHU_INVOICE_CHAT_ID`
// 未提供）里是**结构上跑不起来的**，而报告看不出来。验收时不能只看报告数字。
//
// 需求允许「发不出去就建共享文档兜底」，所以降级本身是设计内的；缺的是
// 「跳过」这件事在报告里可见。改法（加一个 skipped 计数 / 一个 Reason 字段）
// 是产品语义，留待拍板 —— 这里只把现状钉住。
func TestPipeline_FeishuSkipIsInvisibleInReport(t *testing.T) {
	store, cleanup := newInvoiceScopedStore(t)
	defer cleanup()
	// 完全没有飞书凭据 → pusher.Available() == false。
	s := &Server{
		emailStore:   store,
		emailFetcher: email.NewFetcher(store, nil),
		dataDir:      t.TempDir(),
		cfg:          config.Config{EmailSpamDryRun: true},
	}
	p := s.ensurePipeline()
	if p == nil {
		t.Fatal("ensurePipeline 返回 nil")
	}
	if p.Pusher == nil {
		t.Fatal("Pusher 不该为 nil（ensurePipeline 总会构造一个）")
	}
	if p.Pusher.Available() {
		t.Skip("本机配了飞书凭据，跳过「未配置」这一支")
	}

	rep := s.runEmailPipeline(context.Background(), nil)
	if rep == nil {
		t.Fatal("nil report")
	}
	// 这就是问题所在：一个「什么都没做」的报告和一个「全都推成功了」的报告
	// 在这几个字段上无法区分。若哪天给 PipelineReport 加了跳过原因，
	// 本用例会红，届时请更新这里的断言与 handoff §7df。
	if len(rep.Errors) != 0 {
		t.Errorf("飞书未配置时报告里却有 errors = %v", rep.Errors)
	}
	if rep.FeishuPushed != 0 || rep.FeishuFailed != 0 {
		t.Errorf("FeishuPushed=%d FeishuFailed=%d, want 0/0", rep.FeishuPushed, rep.FeishuFailed)
	}
}

// ---- 三个适配器：Available / 记忆 / nil 守卫 ----

// feishuInvoicePusher.Available 是需求 3 的总开关。四个条件缺一不可，
// 其中 chatID 最容易被忘（app_id/secret 都配了就是不发）。
func TestFeishuInvoicePusher_AvailableRequiresChatID(t *testing.T) {
	client := feishu.New("app-id", "app-secret")
	cases := []struct {
		name string
		p    *feishuInvoicePusher
		want bool
	}{
		{"nil 接收者", nil, false},
		{"缺 chatID", &feishuInvoicePusher{client: client}, false},
		{"缺 client", &feishuInvoicePusher{chatID: "oc_x"}, false},
		{"两者都有且凭据非空", &feishuInvoicePusher{client: client, chatID: "oc_x"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.p.Available(); got != c.want {
				t.Fatalf("Available() = %v, want %v", got, c.want)
			}
		})
	}
}

// 台账链接必须按 (workspace,user) 隔离，且**不同 scope 互不串** ——
// 这条是需求 8「配置归谁管」的同一类不变量。
func TestFeishuLedgerPublisher_RecallIsScopedPerWorkspaceAndUser(t *testing.T) {
	p := &feishuLedgerPublisher{}
	if got := p.PublishedURL("ws-a", "u-1"); got != "" {
		t.Fatalf("未记录时应为空，得到 %q", got)
	}
	p.RememberPublished("ws-a", "u-1", "https://feishu.cn/sheet/1")
	p.RememberPublished("ws-b", "u-1", "https://feishu.cn/sheet/2")
	p.RememberPublished("ws-a", "u-2", "https://feishu.cn/sheet/3")

	want := map[[2]string]string{
		{"ws-a", "u-1"}: "https://feishu.cn/sheet/1",
		{"ws-b", "u-1"}: "https://feishu.cn/sheet/2",
		{"ws-a", "u-2"}: "https://feishu.cn/sheet/3",
		{"ws-c", "u-1"}: "",
	}
	for k, v := range want {
		if got := p.PublishedURL(k[0], k[1]); got != v {
			t.Errorf("PublishedURL(%s,%s) = %q, want %q", k[0], k[1], got, v)
		}
	}

	// 空 URL 不得被记进去（否则会「记住」一个空链接并短路掉真正的发布）。
	p.RememberPublished("ws-a", "u-1", "")
	if got := p.PublishedURL("ws-a", "u-1"); got != "https://feishu.cn/sheet/1" {
		t.Errorf("空 URL 覆盖了已有链接: %q", got)
	}
	// nil 接收者必须安全。
	var np *feishuLedgerPublisher
	np.RememberPublished("ws", "u", "x")
	if got := np.PublishedURL("ws", "u"); got != "" {
		t.Errorf("nil 接收者返回了 %q", got)
	}
}

// 提醒器未配置时必须**返回错误**而不是静默成功 —— 需求 4「重要邮件提醒」
// 在 notifycenter 缺失时如果静默返回 nil，报告会显示「已提醒」。
func TestNotifycenterEmailNotifier_UnconfiguredReturnsError(t *testing.T) {
	var n *notifycenterEmailNotifier
	if err := n.NotifyImportantEmail(context.Background(), email.Email{Subject: "x"}); err == nil {
		t.Error("nil notifier 返回了 nil error —— 需求 4 会被记成「已提醒」")
	}
	empty := &notifycenterEmailNotifier{}
	if err := empty.NotifyImportantEmail(context.Background(), email.Email{Subject: "x"}); err == nil {
		t.Error("svc 为 nil 时返回了 nil error")
	}
	// 真实 service 的构造代价较高（需要 store），本轮只钉 nil 守卫这一支；
	// Dispatch 的行为由 notifycenter 自己的测试负责。
	var _ *notifycenter.Service = empty.svc
}

// RunEmailPipeline 是 **scheduler 定时任务**调用的那个入口（runEmailPipeline 的
// nil 覆盖版）。它此前 0% 覆盖，而它是需求 1「每天定时」那条腿的唯一入口 ——
// 手工入口（handleEmailPipelineRun）有测试了，定时这条腿却没有。
// 这里同时钉住它**不传覆盖**：定时任务若能带 dryRunSpam:false，就等于让
// 定时路径绕过了预演开关。
func TestRunEmailPipeline_SchedulerPathPassesNoOverride(t *testing.T) {
	store, cleanup := newInvoiceScopedStore(t)
	defer cleanup()
	s := &Server{
		emailStore:   store,
		emailFetcher: email.NewFetcher(store, nil),
		dataDir:      t.TempDir(),
		cfg:          config.Config{EmailSpamDryRun: true},
	}
	p := s.ensurePipeline()
	if p == nil {
		t.Fatal("ensurePipeline 返回 nil")
	}
	rep := s.RunEmailPipeline(context.Background())
	if rep == nil {
		t.Fatal("RunEmailPipeline 返回 nil report")
	}
	if !p.SpamDryRun {
		t.Errorf("定时入口跑完之后 SpamDryRun = false：定时路径不应能改预演开关")
	}
}

// 台账发布器的 Available 判据比 pusher 少一条 —— 它**不需要 chatID**
// （建表只用到 app 凭据，chat_id 是发消息才需要的）。这个差异是有意的，
// 但很容易在重构时被「统一」掉，所以分开钉。
func TestFeishuLedgerPublisher_AvailableDoesNotNeedChatID(t *testing.T) {
	client := feishu.New("app-id", "app-secret")
	var np *feishuLedgerPublisher
	if np.Available() {
		t.Error("nil 接收者 Available() 应为 false")
	}
	if (&feishuLedgerPublisher{}).Available() {
		t.Error("缺 client 时应为 false")
	}
	if !(&feishuLedgerPublisher{client: client}).Available() {
		t.Error("有 client 且凭据非空时应为 true（台账不需要 chatID）")
	}
	// 未配置时 PublishLedger 必须返回错误而不是空串+nil，
	// 否则调用方会把「没配」当成「发布成功但没链接」。
	if _, err := (&feishuLedgerPublisher{}).PublishLedger(context.Background(), "t", nil); err == nil {
		t.Error("未配置时 PublishLedger 返回了 nil error")
	}
}

// 否则采集器会在运行时报 nil 解引用而不是在构造点就明确降级。
func TestEnsureInvoiceHarvester_RequiresAllThreeDeps(t *testing.T) {
	store, cleanup := newInvoiceScopedStore(t)
	defer cleanup()
	fetcher := email.NewFetcher(store, nil)
	dir := t.TempDir()

	if got := (&Server{}).ensureInvoiceHarvester(); got != nil {
		t.Error("全空时应返回 nil")
	}
	if got := (&Server{emailStore: store}).ensureInvoiceHarvester(); got != nil {
		t.Error("缺 fetcher 时应返回 nil")
	}
	if got := (&Server{emailStore: store, emailFetcher: fetcher}).ensureInvoiceHarvester(); got != nil {
		t.Error("缺 dataDir 时应返回 nil")
	}
	if got := (&Server{emailFetcher: fetcher, dataDir: dir}).ensureInvoiceHarvester(); got != nil {
		t.Error("缺 store 时应返回 nil")
	}
	full := &Server{emailStore: store, emailFetcher: fetcher, dataDir: dir}
	h := full.ensureInvoiceHarvester()
	if h == nil {
		t.Fatal("三件齐备时不应返回 nil")
	}
	// 每次调用都新建一个实例（它无内部状态，安全）；真正被单例缓存的是
	// Pipeline 而不是 harvester。断言两次拿到的 Store 指向同一个即可 ——
	// 「共用」指的是共用构造函数与同一份配置，不是共用对象。
	h2 := full.ensureInvoiceHarvester()
	if h2 == nil {
		t.Fatal("第二次调用返回 nil")
	}
	if h2.Store != h.Store || h2.DataDir != h.DataDir {
		t.Error("两次构造出的 harvester 配置不一致")
	}
	// 15 分钟上限在 runEmailPipeline 里，这里只确认 Run 不会因为无账户而挂死。
	done := make(chan struct{})
	go func() {
		defer close(done)
		rep := full.runEmailPipeline(context.Background(), nil)
		if rep == nil {
			t.Error("nil report")
		}
	}()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("无账户的 Run 超过 60s 未返回")
	}
}
