package server

// server_email_classify_progress_test.go —— 把「客户端归类循环会不会停」这件事
// 从「读代码觉得会停」变成**可复现的服务端事实**。
//
// ## 要证明的事
//
// 前端 `use-email-inbox.ts` 的归类循环是：
//
//	for {
//	  report = await classifyInbox(20)      // POST /api/emails/classify
//	  ...
//	  if (classifyCancel || report.remaining <= 0) break
//	}
//
// 它唯一的自然退出条件是 `remaining <= 0`。于是问题变成：
//
//	**服务端有没有一种情况，反复调用 classify 都返回 classified:0
//	  而 remaining 恒大于 0？**
//
// 有，而且是最普通的两类**真实运行故障**，不是构造出来的边角：
//
//   A. 网关调用失败（`s.llm.Chat` 返回 error）—— 网关 503 / key 失效 /
//      限流 / 模型名错。kxmemory 未配的部署（本部署就是，
//      `POCKET_KXMEMORY_BASE_URL` 未设）全靠网关这条腿。
//   B. 网关返回**无法解析**的内容（`parseGatewayClassification` 失败）——
//      模型没按约定格式输出。这是 LLM 链路上极常见的一类失败。
//
// 两种情况下 `classified` 恒 0（因为它要求 `Category != "" && Error == ""`，
// 见 server_email_classify.go:82-87），而 `remaining` 一封没少
// （失败时根本没写库）。
//
// ## 一条被否掉的假机制（留档以免有人重走）
//
// 初稿假设「网关对象配了但没选模型」会导致同样的结果。**实测不成立**：
// `emailClassifyModel` 在 `cfg.LLMModel` 为空时仍从
// `ResolveGatewayForUser` 拿到非空 PreferredModels，所以
// `classifyViaGateway` 的 `model == ""` 早退分支（:126-129）没被触发，
// 日志显示实际走的是「调用了模型但输出无法解析」。机制说错了，但**结论**
// （存在 classified:0 / remaining>0 且不收敛的响应）由下面两条用例独立证明，
// 不依赖那个假机制。
//
// ## 为什么这条要钉住
//
// 客户端循环没有「本轮零进展就停」的判断，也没有批次数上限。上面任一故障
// 一旦出现，用户点一次「归类」，前端就会**无限次**打这个端点，而每次调用
// 服务端都要逐封跑分类 —— 一次点击换一场对已经故障的网关的持续压测，
// 而且没有任何退避。
//
// 本文件只钉**服务端事实**。客户端的终止修法与负控在
// frontend/src/features/email/__tests__/email-classify-loop.test.mjs。

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/config"
)

type classifyProgressReport struct {
	Classified int                  `json:"classified"`
	Remaining  int                  `json:"remaining"`
	Results    []classifyResultJSON `json:"results"`
}

func postClassify(t *testing.T, s *Server) classifyProgressReport {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/emails/classify", strings.NewReader(`{"limit":20}`))
	w := httptest.NewRecorder()
	s.handleEmailClassify(w, r)
	if w.Code != 200 {
		t.Fatalf("classify => (%d, %q)", w.Code, errOf(w).msg)
	}
	var rep classifyProgressReport
	if err := json.Unmarshal(w.Body.Bytes(), &rep); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, w.Body.String())
	}
	return rep
}

// newNoProgressServer 造一个「分类必然逐封失败」的 server：kxmemory 未配
// （生产上就是 POCKET_KXMEMORY_BASE_URL 未设），只走网关，而网关按 llm 的
// 设定必然失败（调用报错 或 返回无法解析的内容）。
func newNoProgressServer(t *testing.T, llm *fakeLLM) *Server {
	t.Helper()
	store, cleanup := newInvoiceScopedStore(t)
	t.Cleanup(cleanup)
	return &Server{
		emailStore: store,
		cfg:        config.Config{LLMModel: "some-model"},
		llm:        llm,
		kxmemory:   nil,
	}
}

// assertZeroProgress 断言这一批「零进展」：classified=0、remaining=want、
// 且逐封都带 Error（客户端靠 Error 非空才跳过写库，这个字段必须非空）。
func assertZeroProgress(t *testing.T, rep classifyProgressReport, wantRemaining int) {
	t.Helper()
	if rep.Classified != 0 {
		t.Errorf("classified = %d, want 0（逐封都失败）", rep.Classified)
	}
	if rep.Remaining != wantRemaining {
		t.Errorf("remaining = %d, want %d（一封都没归类，remaining 必须原样不动）", rep.Remaining, wantRemaining)
	}
	if len(rep.Results) == 0 {
		t.Fatal("results 为空，无法逐条检查 Error")
	}
	for i, row := range rep.Results {
		if row.Error == "" {
			t.Errorf("results[%d].Error 为空，客户端会误以为分类成功并写库", i)
		}
		if row.Category != "" {
			t.Errorf("results[%d].Category = %q, want \"\"", i, row.Category)
		}
	}
}

// TestClassify_GatewayCallFails_ReturnsZeroClassifiedWithRemaining
//
// 场景 A：网关调用直接报错（503 / key 失效 / 限流）。
func TestClassify_GatewayCallFails_ReturnsZeroClassifiedWithRemaining(t *testing.T) {
	store, cleanup := newInvoiceScopedStore(t)
	defer cleanup()
	seedAccountEmail(t, store, "acc-1", "e-nm-1", 1767225600)
	seedAccountEmail(t, store, "acc-1", "e-nm-2", 1767225700)
	seedAccountEmail(t, store, "acc-1", "e-nm-3", 1767225800)

	llm := &fakeLLM{err: errors.New("gateway unavailable: 503")}
	s := &Server{emailStore: store, cfg: config.Config{LLMModel: "m"}, llm: llm, kxmemory: nil}

	assertZeroProgress(t, postClassify(t, s), 3)
	if llm.calls != 3 {
		t.Errorf("网关被调用 %d 次, want 3（每封一次）", llm.calls)
	}
}

// TestClassify_GatewayOutputUnparseable_ReturnsZeroClassifiedWithRemaining
//
// 场景 B：网关通了，但返回的内容不符合约定格式。
//
// 留档价值：初稿误以为「模型名没配好」才是主因，实测这条才是 fakeLLM
// 默认配置下真实走到的分支（见文件头「被否掉的假机制」）。
func TestClassify_GatewayOutputUnparseable_ReturnsZeroClassifiedWithRemaining(t *testing.T) {
	store, cleanup := newInvoiceScopedStore(t)
	defer cleanup()
	seedAccountEmail(t, store, "acc-1", "e-up-1", 1767225600)
	seedAccountEmail(t, store, "acc-1", "e-up-2", 1767225700)

	// reply 是一段散文：模型没按 JSON 输出，parseGatewayClassification 失败。
	llm := &fakeLLM{reply: "这封邮件看起来是账单，建议您留意一下。"}
	s := &Server{emailStore: store, cfg: config.Config{LLMModel: "m"}, llm: llm, kxmemory: nil}

	assertZeroProgress(t, postClassify(t, s), 2)
}

// TestClassify_RepeatedCallsNeverConverge_Key 两条都断在「客户端循环没有出口」
// 这一条上。关键在于**反复调用不收敛**：只断言「一次返回 remaining>0」不够，
// 有人会说「下一次就好了」。这里连打三次，证明 remaining 恒定不动，
// 于是 `while (!cancel) { if (remaining<=0) break }` **没有任何出口**。
func TestClassify_RepeatedCallsNeverConverge_Key(t *testing.T) {
	for _, tc := range []struct {
		name string
		llm  *fakeLLM
	}{
		{"网关报错", &fakeLLM{err: errors.New("gateway unavailable: 503")}},
		{"输出无法解析", &fakeLLM{reply: "这封邮件看起来是账单。"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newNoProgressServer(t, tc.llm)
			for _, id := range []string{"e-c-1", "e-c-2", "e-c-3", "e-c-4", "e-c-5"} {
				seedAccountEmail(t, s.emailStore, "acc-1", id, 1767225600)
			}

			for i := 0; i < 3; i++ {
				rep := postClassify(t, s)
				if rep.Classified != 0 {
					t.Fatalf("第 %d 次调用 classified = %d, want 0", i+1, rep.Classified)
				}
				if rep.Remaining != 5 {
					t.Errorf("第 %d 次调用 remaining = %d, want 5（故障不变就不该有任何进展）", i+1, rep.Remaining)
				}
				if rep.Remaining <= 0 {
					t.Fatalf("第 %d 次 remaining=%d <= 0，客户端会退出 —— 本用例的前提（不收敛）已不成立，请重读",
						i+1, rep.Remaining)
				}
			}
		})
	}
}
