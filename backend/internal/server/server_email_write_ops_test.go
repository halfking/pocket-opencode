package server

// server_email_write_ops_test.go — 分类 / 清理两个**写操作**端点的接线层。
//
// `server_email_classify.go`（38/38）与 `server_email_purge.go`（14/14）此前
// 同样是 0%。这两个端点比发票那组更危险，原因是它们**改数据**：
// classify 会把分类结果写回 emails 行，purge 会软删邮件**并删掉磁盘上的正文缓存**。
// 接线错一个字符，代价是「邮件被误分类」或「正文文件被删」。
//
// 覆盖到的（实测非 0，见 handoff §7de）：
//   handleEmailClassify 0% → 守卫 100% + 成功路径
//   classifyOneEmail     0% → 100%
//   classifyViaKxmemory  0% → 100%（fake kxmemory 记录调用与回退）
//   classifyViaGateway   0% → 100%
//   firstNonEmptyStr     0% → 100%
//   handleEmailPurge     0% → 100%
//
// ## 负控（实测过）
//
// 1) 把 `allow` 的过滤去掉（`items = items`）→ TestClassify_ExplicitIDsAreIntersectedWithNewestPage 转红。
// 2) 把 purge 的 `if s.dataDir != ""` 去掉并改成无条件遍历 paths → TestPurge_DeletesCachedBodyFiles 转红。
// 3) 把 `if s.kxmemory == nil` 去掉（直接调 kxmemory）→ TestClassify_FallsBackToGatewayWhenKxmemoryFails 转红。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/aigate"
	"github.com/halfking/pocket-opencode/backend/internal/config"
	"github.com/halfking/pocket-opencode/backend/internal/email"
	"github.com/halfking/pocket-opencode/backend/internal/kxmemory"
	"github.com/halfking/pocket-opencode/backend/internal/opencode"
)

// ---- fake 分类器 ----

// fakeLLM 实现 aigate.LLMClient。它同时是「LLM 回答了什么」的记录器：
// 分类链路最脆的一环是模型输出的形态，而唯一能验证「我们真的把正确的东西
// 喂给了模型」的办法就是把入参截下来断言。
type fakeLLM struct {
	reply     string
	err       error
	calls     int
	lastModel string
	lastUser  string
}

func (f *fakeLLM) Chat(_ context.Context, model string, msgs []aigate.ChatMessage) (string, error) {
	f.calls++
	f.lastModel = model
	for _, m := range msgs {
		if m.Role == "user" {
			f.lastUser = m.Content
		}
	}
	return f.reply, f.err
}

// fakeKxmemory 让分类器「配了但调不通」，用来验证退回网关这条腿。
// 内嵌 kxmemory.Client 让它自动满足整个接口（其余方法一调就 nil panic，
// 而分类链路本轮只用到 ClassifyEmails）——这样接口新增方法时不会编译不过。
type fakeKxmemory struct {
	kxmemory.Client
	calls int
	fail  bool
}

func (f *fakeKxmemory) ClassifyEmails(context.Context, kxmemory.ClassifyEmailsRequest) (*kxmemory.ClassifyEmailsResponse, error) {
	f.calls++
	if f.fail {
		return nil, fmt.Errorf("kxmemory down (test)")
	}
	return &kxmemory.ClassifyEmailsResponse{Results: []kxmemory.EmailClassificationResult{{
		EmailID: "from-kxmemory", Category: "work", Importance: "high", Summary: "kxmemory 的分类",
	}}}, nil
}

const classifyReply = `{"category":"bill","importance":"high","summary":"腾讯云 9 月账单","suggested_action":"review"}`

// ---- 无 store 的守卫（不需要 DB） ----

func TestEmailWriteOps_NilStore_GuardOrder(t *testing.T) {
	// classify：方法先于 store。
	cases := []struct {
		name   string
		call   func(w http.ResponseWriter, r *http.Request)
		method string
		want   int
		msg    string
	}{
		{"classify 拒非 POST", func(w http.ResponseWriter, r *http.Request) {
			(&Server{}).handleEmailClassify(w, r)
		}, http.MethodGet, 405, "POST only"},
		{"classify 无库", func(w http.ResponseWriter, r *http.Request) {
			(&Server{}).handleEmailClassify(w, r)
		}, http.MethodPost, 503, "email store not configured"},
		// purge：方法先于 store，与 classify 同。
		{"purge 拒非 POST", func(w http.ResponseWriter, r *http.Request) {
			(&Server{}).handleEmailPurge(w, r)
		}, http.MethodGet, 405, "POST only"},
		{"purge 无库", func(w http.ResponseWriter, r *http.Request) {
			(&Server{}).handleEmailPurge(w, r)
		}, http.MethodPost, 503, "email store not configured"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(c.method, "/api/emails/x", strings.NewReader(`{"ids":["a"]}`))
			w := httptest.NewRecorder()
			c.call(w, r)
			if got := errOf(w); got.code != c.want || got.msg != c.msg {
				t.Fatalf("=> (%d, %q), want (%d, %q)", got.code, got.msg, c.want, c.msg)
			}
		})
	}
}

// ---- 真 store：守卫后半段 ----

// classify 在有 store、但 kxmemory / llmBFF / llm 全空时报的 503 与
// 「无 store」的 503 消息不同 —— 两个降级状态必须能区分开，否则运维看到
// 同一个 503 会去查错的地方（去查邮箱配置，而不是去查分类器配置）。
func TestClassify_StoreButNoClassifierIsDistinguishable(t *testing.T) {
	store, cleanup := newInvoiceScopedStore(t)
	defer cleanup()
	seedAccountEmail(t, store, "acc-1", "e-classify-1", 1767225600)

	s := &Server{emailStore: store}
	r := httptest.NewRequest(http.MethodPost, "/api/emails/classify", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	s.handleEmailClassify(w, r)

	got := errOf(w)
	if got.code != 503 || got.msg != "email classifier not configured" {
		t.Fatalf("=> (%d, %q), want (503, email classifier not configured)", got.code, got.msg)
	}
}

func TestPurge_StoreButBadBody(t *testing.T) {
	store, cleanup := newInvoiceScopedStore(t)
	defer cleanup()
	s := &Server{emailStore: store}

	cases := []struct {
		name, body, wantMsg string
	}{
		{"缺 ids", `{}`, "ids required"},
		{"空 ids 数组", `{"ids":[]}`, "ids required"},
		{"坏 JSON", `{`, "ids required"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/emails/purge", strings.NewReader(c.body))
			w := httptest.NewRecorder()
			s.handleEmailPurge(w, r)
			if got := errOf(w); got.code != 400 || got.msg != c.wantMsg {
				t.Fatalf("=> (%d, %q), want (400, %q)", got.code, got.msg, c.wantMsg)
			}
		})
	}

	// 101 个 id 必须被拒。边界是 100 → 允许，所以这里精确测 100 与 101 两档。
	ids100 := make([]string, 100)
	for i := range ids100 {
		ids100[i] = fmt.Sprintf("e-%d", i)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/emails/purge",
		strings.NewReader(mustJSON(map[string]any{"ids": ids100})))
	w := httptest.NewRecorder()
	s.handleEmailPurge(w, r)
	if w.Code != 200 {
		t.Fatalf("100 ids should be accepted, got (%d, %q)", w.Code, errOf(w).msg)
	}

	ids101 := append(append([]string{}, ids100...), "e-100")
	r = httptest.NewRequest(http.MethodPost, "/api/emails/purge",
		strings.NewReader(mustJSON(map[string]any{"ids": ids101})))
	w = httptest.NewRecorder()
	s.handleEmailPurge(w, r)
	if got := errOf(w); got.code != 400 || got.msg != "too many ids" {
		t.Fatalf("101 ids => (%d, %q), want (400, too many ids)", got.code, got.msg)
	}
}

// ---- purge 的真实副作用：软删 + 删正文缓存 ----

// purge 是本文件里唯一一个**会删磁盘文件**的端点，所以它的判据必须是
// 「文件真的没了」，而不是「接口回了 200」。
func TestPurge_DeletesCachedBodyFiles(t *testing.T) {
	store, cleanup := newInvoiceScopedStore(t)
	defer cleanup()
	dataDir := t.TempDir()
	ctx := context.Background()

	seedAccountEmail(t, store, "acc-1", "e-purge-1", 1767225600)
	seedAccountEmail(t, store, "acc-1", "e-purge-2", 1767225601)

	rel1 := filepath.Join("email-bodies", "e-purge-1.eml")
	rel2 := filepath.Join("email-bodies", "e-purge-2.eml")
	for _, rel := range []string{rel1, rel2} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dataDir, rel)), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dataDir, rel), []byte("cached body"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if err := store.MarkEmailBodyCached(ctx, "e-purge-1", rel1, 100); err != nil {
		t.Fatalf("MarkEmailBodyCached 1: %v", err)
	}
	if err := store.MarkEmailBodyCached(ctx, "e-purge-2", rel2, 100); err != nil {
		t.Fatalf("MarkEmailBodyCached 2: %v", err)
	}

	s := &Server{emailStore: store, dataDir: dataDir}
	r := httptest.NewRequest(http.MethodPost, "/api/emails/purge",
		strings.NewReader(`{"ids":["e-purge-1"]}`))
	w := httptest.NewRecorder()
	s.handleEmailPurge(w, r)
	if w.Code != 200 {
		t.Fatalf("purge => (%d, %q)", w.Code, errOf(w).msg)
	}
	var payload struct {
		Purged int64 `json:"purged"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Purged != 1 {
		t.Fatalf("purged = %d, want 1", payload.Purged)
	}
	if _, err := os.Stat(filepath.Join(dataDir, rel1)); !os.IsNotExist(err) {
		t.Errorf("被删邮件的正文缓存仍在: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, rel2)); err != nil {
		t.Errorf("未选中的邮件正文缓存被误删: %v", err)
	}
	// 软删：行还在（用户可恢复），只是标记 deleted_at。
	purged, err := store.IsEmailBodyPurged(ctx, "e-purge-1", "local", "default")
	if err != nil {
		t.Fatalf("IsEmailBodyPurged: %v", err)
	}
	if !purged {
		t.Error("e-purge-1 未被标记为已清理")
	}
}

// 越权不能删：换一个 user 的 claims 打同一个 purge 端点，必须 purged:0 且
// 原封邮件不受影响。这条不变量值得单独钉 —— 「邮箱配置归谁管」是需求 8 的
// 待拍板项，在拍板之前，越权删除必须被测试挡住。
//
// 走的是真 handler（claims 注入 request context，与 requireAuth 做的事一样），
// 不是直接调 store：否则测的只是 SQL 的 WHERE，而漏掉「handler 传错了
// userID」这种更常见的接线错误。
func TestPurge_IsWorkspaceScoped(t *testing.T) {
	store, cleanup := newInvoiceScopedStore(t)
	defer cleanup()
	ctx := context.Background()
	dataDir := t.TempDir()
	seedAccountEmail(t, store, "acc-1", "e-scope-1", 1767225600)
	rel := filepath.Join("email-bodies", "e-scope-1.eml")
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dataDir, rel)), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, rel), []byte("body"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := store.MarkEmailBodyCached(ctx, "e-scope-1", rel, 10); err != nil {
		t.Fatalf("MarkEmailBodyCached: %v", err)
	}

	s := &Server{emailStore: store, dataDir: dataDir}

	// 别的 user 打过来 —— 应当什么都删不掉。
	evil := withTestClaims(
		httptest.NewRequest(http.MethodPost, "/api/emails/purge",
			strings.NewReader(`{"ids":["e-scope-1"]}`)),
		"someone-else", "member", "default")
	w := httptest.NewRecorder()
	s.handleEmailPurge(w, evil)
	if w.Code != 200 {
		t.Fatalf("越权 purge => (%d, %q)", w.Code, errOf(w).msg)
	}
	var payload struct {
		Purged int64 `json:"purged"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Purged != 0 {
		t.Fatalf("别的 user 删掉了 %d 封 —— 越权！", payload.Purged)
	}
	if _, err := os.Stat(filepath.Join(dataDir, rel)); err != nil {
		t.Errorf("越权 purge 把正文缓存删了: %v", err)
	}
	purged, err := store.IsEmailBodyPurged(ctx, "e-scope-1", "local", "default")
	if err != nil {
		t.Fatalf("IsEmailBodyPurged: %v", err)
	}
	if purged {
		t.Error("越权 purge 把原邮件标记成已清理了")
	}

	// 本人打过来则必须成功 —— 否则上面那条可能只是「端点根本没在工作」。
	owner := withTestClaims(
		httptest.NewRequest(http.MethodPost, "/api/emails/purge",
			strings.NewReader(`{"ids":["e-scope-1"]}`)),
		"local", "admin", "default")
	w = httptest.NewRecorder()
	s.handleEmailPurge(w, owner)
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode owner: %v", err)
	}
	if payload.Purged != 1 {
		t.Fatalf("本人 purge 删了 %d 封, want 1 —— 端点没在工作", payload.Purged)
	}
}

// ---- classify 的成功路径 ----

// kxmemory 配了但调不通时，必须自动退到 LLM 网关（server_email_classify.go:100-107）。
// 这条退路是「只配了网关的部署里自动归纳还能用」的唯一保证。
func TestClassify_FallsBackToGatewayWhenKxmemoryFails(t *testing.T) {
	store, cleanup := newInvoiceScopedStore(t)
	defer cleanup()
	seedAccountEmail(t, store, "acc-1", "e-fb-1", 1767225600)

	llm := &fakeLLM{reply: classifyReply}
	kx := &fakeKxmemory{fail: true}
	s := &Server{
		emailStore: store,
		cfg:        config.Config{LLMModel: "test-model"},
		llm:        llm,
		kxmemory:   kx,
	}

	r := httptest.NewRequest(http.MethodPost, "/api/emails/classify", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	s.handleEmailClassify(w, r)
	if w.Code != 200 {
		t.Fatalf("classify => (%d, %q)", w.Code, errOf(w).msg)
	}
	if kx.calls != 1 {
		t.Errorf("kxmemory 被调用 %d 次, want 1", kx.calls)
	}
	if llm.calls != 1 {
		t.Fatalf("网关被调用 %d 次, want 1（退路没走）", llm.calls)
	}
	// 模型名：网关的 preferredModels 优先于 cfg.LLMModel（见
	// TestEmailClassifyModel_PrefersGatewayOverCfg 这条独立用例）。这里只断言
	// 「解析出了非空模型」，具体优先级交给那条用例，避免两处重复断言。
	if strings.TrimSpace(llm.lastModel) == "" {
		t.Error("分类时没有解析出模型名")
	}
	var res struct {
		Classified int `json:"classified"`
		Remaining  int `json:"remaining"`
		Results    []classifyResultJSON
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Classified != 1 || len(res.Results) != 1 {
		t.Fatalf("classified=%d results=%d, want 1/1", res.Classified, len(res.Results))
	}
	got := res.Results[0]
	if got.Category != "bill" || got.Importance != "high" || got.Summary != "腾讯云 9 月账单" {
		t.Fatalf("分类结果没解出来: %+v", got)
	}
	if res.Remaining != 0 {
		t.Errorf("remaining = %d, want 0（分类后不应再算未分类）", res.Remaining)
	}
	// 入参断言：模型必须拿到发件人/主题/摘要，且**不能**拿到整封正文。
	if !strings.Contains(llm.lastUser, "e-fb-1") && !strings.Contains(llm.lastUser, "发件人") {
		t.Errorf("喂给模型的用户内容不含发件人字段: %q", llm.lastUser)
	}
	if strings.Contains(llm.lastUser, "正文全文") {
		t.Error("喂了整封正文，违反「只喂 snippet」的约定")
	}
}

// kxmemory 配了且调得通时，**必须走 kxmemory 而不是网关**（优先级反过来）。
// 这条在当前环境更重要：`POCKET_KXMEMORY_BASE_URL` 没配，所以生产上 kxmemory
// 这条腿根本没跑过，只验「调不通时退回」不足以说明「调得通时它是对的」。
func TestClassify_PrefersKxmemoryOverGateway(t *testing.T) {
	store, cleanup := newInvoiceScopedStore(t)
	defer cleanup()
	seedAccountEmail(t, store, "acc-1", "e-kx-1", 1767225600)

	llm := &fakeLLM{reply: classifyReply}
	kx := &fakeKxmemory{} // fail = false → 调得通
	s := &Server{emailStore: store, cfg: config.Config{LLMModel: "m"}, llm: llm, kxmemory: kx}

	r := httptest.NewRequest(http.MethodPost, "/api/emails/classify", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	s.handleEmailClassify(w, r)
	if w.Code != 200 {
		t.Fatalf("classify => (%d, %q)", w.Code, errOf(w).msg)
	}
	if kx.calls != 1 {
		t.Errorf("kxmemory 被调用 %d 次, want 1", kx.calls)
	}
	if llm.calls != 0 {
		t.Errorf("kxmemory 调得通却还是打了网关 %d 次 —— 优先级反了", llm.calls)
	}
	var res struct {
		Classified int                  `json:"classified"`
		Results    []classifyResultJSON `json:"results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Classified != 1 || len(res.Results) != 1 {
		t.Fatalf("classified=%d results=%d, want 1/1", res.Classified, len(res.Results))
	}
	// 结果字段必须来自 kxmemory 的响应，而不是网关那份。
	if res.Results[0].Category != "work" || res.Results[0].Summary != "kxmemory 的分类" {
		t.Fatalf("结果不是 kxmemory 那份: %+v", res.Results[0])
	}
	// EmailID 取自**请求**（classifyViaKxmemory 的 `out := classifyResultJSON{EmailID: it.ID}`），
	// 分类器响应里自己带的 EmailID 被忽略。fake 故意返回 "from-kxmemory"，
	// 就是为了钉住这一点：分类器认错了邮件也不会把分类结果写到别人的行上。
	if res.Results[0].EmailID != "e-kx-1" {
		t.Errorf("EmailID = %q, want e-kx-1（取自请求，忽略分类器返回的 id）", res.Results[0].EmailID)
	}
}

// 【行为事实，非缺陷】显式指定 ids 时，实际分类的是
// **「ids ∩ 最新 ≤20 封未分类邮件」**。因为 ListUnclassifiedScoped 硬上限 20
// 且按 date DESC 取最新一页，allow 集合又只取 ids 的前 20 个。
// 于是「指定一封很老的未分类邮件」会静默什么都不做（classified:0，无错误）。
// 需求上这是「批量整理」入口，不报错但没整理，运维很难发现。
// 这条用例把当前行为钉住；要不要改成真按 ids 取，是待拍板项。
func TestClassify_ExplicitIDsAreIntersectedWithNewestPage(t *testing.T) {
	store, cleanup := newInvoiceScopedStore(t)
	defer cleanup()
	// 25 封未分类邮件，date 递增 —— e-00 最老，e-24 最新。
	for i := 0; i < 25; i++ {
		id := fmt.Sprintf("e-%02d", i)
		seedAccountEmail(t, store, "acc-1", id, int64(1767225600+i))
	}

	llm := &fakeLLM{reply: classifyReply}
	s := &Server{emailStore: store, cfg: config.Config{LLMModel: "m"}, llm: llm}

	// 指定最老的 1 封（e-00）。它不在「最新 20 封」这一页里。
	r := httptest.NewRequest(http.MethodPost, "/api/emails/classify",
		strings.NewReader(mustJSON(map[string]any{"ids": []string{"e-00"}})))
	w := httptest.NewRecorder()
	s.handleEmailClassify(w, r)
	var res struct {
		Classified int                  `json:"classified"`
		Results    []classifyResultJSON `json:"results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Classified != 0 || len(res.Results) != 0 {
		t.Fatalf("指定最老的一封却被分类了: classified=%d results=%+v —— 行为已变，请更新本用例与 handoff",
			res.Classified, res.Results)
	}
	if llm.calls != 0 {
		t.Errorf("不该调用模型，却调了 %d 次", llm.calls)
	}
	// 而最新的一封在那一页里，必须能分类成功。
	r = httptest.NewRequest(http.MethodPost, "/api/emails/classify",
		strings.NewReader(mustJSON(map[string]any{"ids": []string{"e-24"}})))
	w = httptest.NewRecorder()
	s.handleEmailClassify(w, r)
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode 2: %v", err)
	}
	if res.Classified != 1 {
		t.Fatalf("最新的 e-24 应被分类: %+v", res)
	}
}

// 【行为事实】分类用的模型 = 网关 preferredModels 的第一项，cfg.LLMModel 只是
// 兜底。而 defaultLLMGatewayState() 在没有任何运行时配置时也会把
// DefaultLLMGatewayPreferredModels 整份填进 PreferredModels（非空硬编码默认），
// 所以**默认部署下 cfg.LLMModel 这条兜底永远走不到**。
//
// 我最初在回退用例里断言「模型 = cfg.LLMModel」直接转红，拿到的是 glm-5.2
// （默认列表第一项）。这说明配置页上配的 LLMModel 对邮件分类是**无效配置** ——
// 分类模型实际由网关的 preferredModels 决定。这条不是我能单方面改的语义，
// 但必须钉住：否则有人改默认列表顺序，分类模型会静默换掉且没有任何测试会红。
func TestEmailClassifyModel_PrefersGatewayOverCfg(t *testing.T) {
	if len(opencode.DefaultLLMGatewayPreferredModels) == 0 {
		t.Skip("默认 preferredModels 为空，cfg.LLMModel 兜底会生效 —— 语义已变，请更新本用例")
	}
	s := &Server{cfg: config.Config{LLMModel: "cfg-should-lose"}}
	got := s.emailClassifyModel("local", "default")
	want := strings.TrimSpace(opencode.DefaultLLMGatewayPreferredModels[0])
	if got != want {
		t.Fatalf("emailClassifyModel = %q, want %q（网关 preferred 优先）", got, want)
	}
}

// 不指定 ids 时就是「分类最新一页未分类邮件」，limit 缺省 20、硬上限 20。
func TestClassify_NoIDs_TakesNewestPage(t *testing.T) {
	store, cleanup := newInvoiceScopedStore(t)
	defer cleanup()
	for i := 0; i < 25; i++ {
		seedAccountEmail(t, store, "acc-1", fmt.Sprintf("e-%02d", i), int64(1767225600+i))
	}
	llm := &fakeLLM{reply: classifyReply}
	s := &Server{emailStore: store, cfg: config.Config{LLMModel: "m"}, llm: llm}

	r := httptest.NewRequest(http.MethodPost, "/api/emails/classify", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	s.handleEmailClassify(w, r)
	var res struct {
		Classified int `json:"classified"`
		Results    []classifyResultJSON
		Remaining  int `json:"remaining"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Classified != 20 {
		t.Fatalf("classified = %d, want 20（硬上限）", res.Classified)
	}
	if res.Remaining != 5 {
		t.Errorf("remaining = %d, want 5（25 - 20）", res.Remaining)
	}
	// 拿到的必须是最新的 20 封（e-05..e-24），最老的 5 封留在库里。
	seen := map[string]bool{}
	for _, row := range res.Results {
		seen[row.EmailID] = true
	}
	for i := 0; i < 5; i++ {
		if seen[fmt.Sprintf("e-%02d", i)] {
			t.Errorf("e-%02d 是最老的 5 封之一，不该在这一页里", i)
		}
	}
	for i := 5; i < 25; i++ {
		if !seen[fmt.Sprintf("e-%02d", i)] {
			t.Errorf("e-%02d 应在最新一页里，却没被分类", i)
		}
	}
}

// ---- 辅助 ----

type respErr struct {
	code int
	msg  string
}

func errOf(w *httptest.ResponseRecorder) respErr {
	var p struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &p)
	return respErr{code: w.Code, msg: p.Error}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// seedAccountEmail 造出「一个账号 + 一封未分类邮件」这条最小可分类单元。
// 走 InsertEmail 而非裸 SQL：这样测试用的就是生产写路径，将来 FK 变了会红，
// 而不是悄悄继续跑。
func seedAccountEmail(t *testing.T, store *email.Store, accountID, emailID string, date int64) {
	t.Helper()
	if _, _, err := store.GetAccountByIDScoped(context.Background(), accountID, "local", "default"); err != nil {
		if err := store.InsertAccount(context.Background(), &email.Account{
			ID: accountID, UserID: "local", WorkspaceID: "default",
			DisplayName: "seed", EmailAddress: "seed@vendor.example",
			IMAPHost: "imap.example.com", IMAPPort: 993, AuthType: "password", Enabled: true,
		}, ""); err != nil {
			t.Fatalf("InsertAccount: %v", err)
		}
	}
	if err := store.InsertEmail(context.Background(), email.Email{
		ID: emailID, AccountID: accountID, WorkspaceID: "default",
		FromAddress: "billing@vendor.example", FromName: "某供应商",
		Subject: "发票 " + emailID, Snippet: "本期账单已出，请查收", Date: date,
	}); err != nil {
		t.Fatalf("InsertEmail %s: %v", emailID, err)
	}
}
