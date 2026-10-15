// llm_model_resolution_test.go — §98：**模型解析顺序**的承重门。
//
// ── 为什么这道门值得单独立 ────────────────────────────────────────
//
// `dynamicGatewayBFFProvider.resolveChatModel` 决定了这个产品**每一条** LLM 链
// 实际打到网关的模型名：会议摘要、会议精校、随手记总结、录音实时翻译、
// 邮件分类、agent。全仓没有第二个地方决定模型。
//
// 而在此之前**它没有任何直接测试**：
// `llmbff_provider_adapters_test.go` 覆盖的是 Stream 的回退链、
// `isNoCandidateError` / `pickFallbackModel` / `isModelUnavailableError`，
// **唯独没有覆盖「解析顺序」本身** —— 而顺序错了不报错，只是静默换模型。
//
// 解析链（§98.1 实测读数）：
//
//	req.Model（请求显式，且 ≠ "auto"）
//	  └→ workspace PreferredModels[0]
//	       └→ workspace Models[0]
//	            └→ "auto"（交给网关内置路由）
//
// ★ 关键性质：这是一条**扁平**的链，**没有任何按业务链分派的维度**。
//
//	「把精校换成便宜模型」用现有开关做不到只换精校。
package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/llmbff"
	"github.com/halfking/pocket-opencode/backend/internal/llmgateway"
)

// resolveWith 只按传入的 GatewayConfig 解析，不碰网络。
func resolveWith(cfg GatewayConfig) GatewayResolver {
	return func(_, _ string) GatewayConfig { return cfg }
}

func newResolverProvider(cfg GatewayConfig) *dynamicGatewayBFFProvider {
	return &dynamicGatewayBFFProvider{resolve: resolveWith(cfg)}
}

// ── 承重门 1：显式请求的模型必须原样透传 ────────────────────────────

// TestResolveChatModel_ExplicitRequestWins 钉住「调用方显式指定模型时，
// 部署偏好**不得**覆盖它」。
//
// ⚠ 为什么这条单独钉：`resolveChatModel` 开头那两行
//
//	if req.Model != "" && req.Model != "auto" { return req }
//
// 是整个产品里**唯一**让调用方能点名的机制。设置页的模型选择器、
// 以及将来任何「这条链用哪个模型」的按链覆盖，都要经过它。
// 一旦被人改成「总是用 preferred」，用户选了模型却打另一个模型，
// 表现是「界面选 A、账单/结果来自 B」，没有任何报错。
func TestResolveChatModel_ExplicitRequestWins(t *testing.T) {
	// ★ 故意给一个**与 preferred[0] 完全不同**的模型名：
	//   若优先级写反了，"claude-haiku-4-5" 会被替换成 "glm-5.2"，
	//   而这两者恰好是 §66 实测的两个候选 —— 换成不相干的字符串反而可能
	//   在某些实现下「碰巧正确」。
	p := newResolverProvider(GatewayConfig{PreferredModels: []string{"glm-5.2", "doubao-seed-2-0-mini"}})

	got := p.resolveChatModel(llmbff.ChatRequest{Model: "claude-haiku-4-5"}).Model
	if got != "claude-haiku-4-5" {
		t.Fatalf("显式指定的模型被改成了 %q —— 调用方点名能力失效（preferred=%v）", got, p.resolve("", ""))
	}
}

// ── 承重门 2：空 / "auto" 的三级回落顺序 ───────────────────────────

// TestResolveChatModel_FallbackOrder 钉住回落顺序：
// preferred[0] → Models[0] → "auto"。
//
// ⚠ 三个分支**症状完全不同**，所以必须分三个子用例而不是一个表驱动：
//
//	· preferred 缺失时退到 Models[0]
//	· 两者都缺时退到 "auto"（而不是留空 —— 留空会让网关按空模型处理）
//	· ★ 显式传 "auto" 与**不传**是**同一档**，不是两档。
//	  这条最容易被误改：直觉上「auto」是用户的显式选择，应当优先于 preferred，
//	  于是有人把 `req.Model != "auto"` 删掉 —— 那样显式 auto 就再也打不到 auto 了。
func TestResolveChatModel_FallbackOrder(t *testing.T) {
	t.Run("preferred[0] 优先", func(t *testing.T) {
		p := newResolverProvider(GatewayConfig{
			PreferredModels: []string{"claude-haiku-4-5", "glm-5.2"},
			Models:          []string{"glm-5.2"},
		})
		if got := p.resolveChatModel(llmbff.ChatRequest{Model: ""}).Model; got != "claude-haiku-4-5" {
			t.Fatalf("空模型应取 preferred[0]，实际 %q", got)
		}
	})

	t.Run("preferred 为空则退 Models[0]", func(t *testing.T) {
		p := newResolverProvider(GatewayConfig{Models: []string{"glm-5.2", "doubao-seed-2-0-mini"}})
		if got := p.resolveChatModel(llmbff.ChatRequest{Model: ""}).Model; got != "glm-5.2" {
			t.Fatalf("无 preferred 时应退 Models[0]，实际 %q", got)
		}
	})

	t.Run("两者皆空退 auto 而不是留空", func(t *testing.T) {
		p := newResolverProvider(GatewayConfig{})
		got := p.resolveChatModel(llmbff.ChatRequest{Model: ""}).Model
		if got != "auto" {
			t.Fatalf("两者皆空时应发 \"auto\" 交给网关路由，实际 %q（留空会被网关按空模型处理）", got)
		}
	})

	t.Run("★ 显式 \"auto\" 与不传是同一档", func(t *testing.T) {
		// 负控形状：若有人删掉 `req.Model != "auto"` 这半个条件，
		// 显式 "auto" 就会**跳过** preferred 直接落到 Models[0] 或 "auto"，
		// 于是这一条转红。
		p := newResolverProvider(GatewayConfig{
			PreferredModels: []string{"claude-haiku-4-5"},
			Models:          []string{"glm-5.2"},
		})
		if got := p.resolveChatModel(llmbff.ChatRequest{Model: "auto"}).Model; got != "claude-haiku-4-5" {
			t.Fatalf("显式 \"auto\" 与空模型是同一档，都应取 preferred[0]=\"claude-haiku-4-5\"，实际 %q", got)
		}
	})
}

// ── 承重门 3：走完生产同款装配，钉住发到网关的模型名 ────────────────

// TestLLMChat_EndToEndReachesGatewayWithResolvedModel 是本文件里**唯一一条
// 走完全链**的用例，其余两条只测解析纯函数。
//
// 装配与 `cmd/pocketd/main.go` 一致：
//
//	Server.handleLLMChat → llmChatViaBFF → llmbff.Service.Chat
//	  → dynamicGatewayBFFProvider.Chat（**真的那个**，不是替身）
//	    → resolveChatModel → llmGatewayBFFProvider → HTTP
//
// 它断言的是**最终发到网关的模型名**。只测 resolveChatModel 不够：
// 中间任何一层把 req.Model 覆盖或清空，解析函数的单测照样绿。
//
// ⚠ 我第一版这里注入的是**裸** recordingProvider（绕过 dynamic 适配器），
//
//	读数打出 `provider 收到的 model=""` —— 那条断言只证明了「没被 400 挡掉」，
//	**没有**证明「模型被解析成了非空值」。⇒ 门自己宣称守什么，就得真的守那个；
//	门的注释写得比门本身强，是最隐蔽的一种虚门。
//
// ★ 顺带记一条被代码否掉的猜想：我看到 server_assistant.go:2920 那句
// `writeError(..., "model required (set POCKET_LLM_MODEL or pass in request)")`
// 就断定「所有前端降级路径都是 400」。读下去才发现那句**只在非 BFF 分支**
// （`if s.llmBFF != nil` 之后），而生产走 BFF，llmChatViaBFF 明确注释
// 「不再像旧路径那样 400」。⇒ 两次猜想都被代码否掉才拿到真实形状。
func TestLLMChat_EndToEndReachesGatewayWithResolvedModel(t *testing.T) {
	const wantModel = "claude-haiku-4-5"

	var gotModel string
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req llmgateway.ChatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotModel = req.Model
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"t","model":%q,"choices":[{"index":0,`+
			`"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],`+
			`"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`, wantModel)
	}))
	defer gw.Close()

	srv, tokens := newWorkspaceIsolationServer(t)
	// 显式 preferred[0]：模拟「设置页里用户选了 claude-haiku-4-5」。
	// 部署侧 POCKET_LLM_MODEL 未设置（见 TestLLMModelPinningStatus），
	// 前端也不发 model ⇒ 模型**只能**从这里来，所以这条断言的来源是唯一的。
	srv.SetLLMBFF(llmbff.NewService(NewDynamicLLMGatewayBFFProvider(
		func(string, string) GatewayConfig {
			return GatewayConfig{
				BaseURL:         gw.URL,
				APIKey:          "k",
				Format:          "openai-chat",
				PreferredModels: []string{wantModel, "glm-5.2"},
			}
		}), nil), nil)
	if srv.llmBFF == nil {
		t.Fatal("用例前提失效：SetLLMBFF 之后 llmBFF 仍为 nil")
	}

	// 逐字复制前端 fallbackRefine 的请求体形状：没有 model 键。
	body := `{"messages":[{"role":"user","content":"把下面这段转写精校成通顺中文。"}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/llm/chat", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tokens["ws-a"])
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("空 model 的请求必须放行（BFF 已装配），实际 %d body=%s —— "+
			"前端所有降级路径都会撞上这道门", rr.Code, strings.TrimSpace(rr.Body.String()))
	}
	if gotModel != wantModel {
		t.Fatalf("发到网关的 model=%q，期望 %q（preferred[0]）—— "+
			"「请求无 model 时由工作区偏好补上」这条链在中途断了", gotModel, wantModel)
	}
	t.Logf("请求体无 model 键 → 发到网关的 model=%q ⇒ 解析链端到端打通", gotModel)
}

// ── 现状登记（**不是门**） ──────────────────────────────────────────

// TestLLMModelPinningStatus 只做读数，不判定成败。
//
// ★ 它记的是一条**决定待拍板形状**的事实：模型开关是**全局的**，不是按链的。
//
//	· 部署级 `POCKET_LLM_MODEL`：全仓**没有任何**部署脚本 / 容器 / env 模板设置它
//	  （只有 docs/archive 与 test-evidence 提到过），默认 ""。
//	· 工作区级 `PreferredModels`：设置页维护的**扁平列表**，无按链维度。
//	· 因此「把精校换成 claude-haiku-4-5 而不动摘要」**用现有开关做不到**：
//	  要么两条链一起换，要么新增一条按链覆盖（如 POCKET_LLM_MODEL_REFINE）。
//
// ⚠ 另一个不稳定量：网关侧 `auto` 的落点**会变**。
//
//	llmbff_provider_adapters.go:203-205 的注释写着「网关侧 auto 当前会路由到
//	claude-sonnet-4.5」，而 §66 真网关实测的有效模型是 `glm-5.2`。
//	⇒ 任何「现役模型是 X」的结论都带时间戳，不是常量。
//
// 拍板后请**删掉本函数**并改写成承重断言。
func TestLLMModelPinningStatus(t *testing.T) {
	// 读数 1：解析链里**没有任何**按业务链分派的参数。
	//   resolveChatModel 的入参只有 req（含 Model/WorkspaceID/User）与 resolve，
	//   没有 kind / chain / purpose 之类的维度 ⇒ 结构上就不可能按链选模型。
	src := readFrontend(t, "llmbff_provider_adapters.go")
	body := goFuncBody(t, src, "func (p *dynamicGatewayBFFProvider) resolveChatModel(")
	for _, forbidden := range []string{"Kind", "kind", "Purpose", "Chain", "Scene"} {
		if strings.Contains(body, forbidden) {
			t.Logf("★ resolveChatModel 里出现了 %q —— 可能已支持按链分派，"+
				"本登记已过期，请重读现状并删掉它", forbidden)
			return
		}
	}
	t.Logf("现状：resolveChatModel 的解析输入只有 req.Model + workspace 配置，**无按链维度** ⇒ " +
		"「只给精校换一个模型」需要新增按链覆盖，现有三个开关都做不到")

	// 读数 2：部署侧有没有钉模型。
	// ⚠ 只扫仓库内的部署清单，**不读** /tmp 下的凭据文件。
	root := "../../.."
	hit := ""
	for _, f := range []string{
		"deploy-154.sh", "deploy-245.sh", "deploy-252.sh", "deploy-local.sh",
		"Dockerfile", "Dockerfile.runtime", "LOCAL_CONFIG.md.template",
	} {
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			continue
		}
		if strings.Contains(string(b), "POCKET_LLM_MODEL") {
			hit = f
			break
		}
	}
	if hit != "" {
		t.Logf("★ %s 里设置了 POCKET_LLM_MODEL —— 本登记的「未钉模型」前提已变，请重读", hit)
	} else {
		t.Log("现状：8 个部署入口（deploy-*.sh / Dockerfile / 模板）里**没有一个**设置 " +
			"POCKET_LLM_MODEL ⇒ 部署级缺省为 \"\"，模型完全由工作区 preferred[0] 与网关 auto 决定")
	}
}
