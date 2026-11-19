package server

// acc_ecosystem_test.go — /api/acc/agents 的超时/降级/缓存判据。
//
// 背景（2026-11-20，211 生产实测）：ACC runtime-control 面抖动 22~67s，
// 本端点原样透传 r.Context() 导致网关 30s 超时、前端拿到空。判据：
//
//	① 运行时清单挂住 ⇒ 在时限内降级读注册表，source=acc-registry-fallback，
//	   整个请求必须**远早于**网关 30s 返回；
//	② 成功结果 60s 内复用缓存（cached:true），不再打 ACC；
//	③ TTL 过期后重新打 ACC；
//	④ 两面都不可达 ⇒ 502（通道故障要如实报错，不能渲染成空生态）；
//	⑤ 运行时清单健康 ⇒ 原合并语义不变（source=acc，推送/拉取/注册表互补）。
//
// 超时/缓存的时长是包级 var：测试收窄到毫秒级，用完必须还原。

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/acchttp"
	"github.com/halfking/pocket-opencode/backend/internal/accruntime"
)

// newACCEcosystemServer 组一个只带 ACC runtime 客户端的 Server。
// handleACCAgents 只依赖 s.accRuntime 与 s.accAgentsCache（后者懒建）。
func newACCEcosystemServer(t *testing.T, stubURL string) *Server {
	t.Helper()
	accClient, err := accruntime.New(acchttp.Config{
		BaseURL: stubURL,
		APIKey:  "test-key",
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("accruntime.New: %v", err)
	}
	srv := &Server{}
	srv.SetACCRuntime(accClient)
	return srv
}

// narrowACCAgentTimings 把超时/缓存时长收窄到毫秒级并注册还原。
func narrowACCAgentTimings(t *testing.T, live time.Duration, ttl time.Duration) {
	t.Helper()
	oldLive, oldTTL := accAgentsLiveTimeout, accAgentsCacheTTL
	accAgentsLiveTimeout, accAgentsCacheTTL = live, ttl
	t.Cleanup(func() {
		accAgentsLiveTimeout, accAgentsCacheTTL = oldLive, oldTTL
	})
}

// countingStub 是一个记录 /api/v2 命中次数的 ACC 桩。
type countingStub struct {
	mu       sync.Mutex
	runtimes int
	registry int
	// slowRuntimes>0 时 runtimes 端点睡这么久再答（模拟回连读面挂住）。
	slowRuntimes time.Duration
	// runtimesCode / registryCode 覆盖状态码（0 = 200 正常应答）。
	runtimesCode int
	registryCode int
}

func (s *countingStub) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/runtime/runtimes", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.runtimes++
		slow, code := s.slowRuntimes, s.runtimesCode
		s.mu.Unlock()
		if slow > 0 {
			time.Sleep(slow)
		}
		if code != 0 {
			w.WriteHeader(code)
			_, _ = w.Write([]byte(`{"error":"boom"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"runtime_id":"rt-1","agent_inventory":[{"agent_id":"ag-push","kind":"zcode","state":"running"}]}]}`))
	})
	mux.HandleFunc("/api/v2/runtime/runtimes/rt-1/agents", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"agents":[{"agent_id":"ag-push","kind":"zcode","status":"idle"}]}`))
	})
	mux.HandleFunc("/api/v2/agents", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.registry++
		code := s.registryCode
		s.mu.Unlock()
		if code != 0 {
			w.WriteHeader(code)
			_, _ = w.Write([]byte(`{"error":"boom"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"agents":[{"id":"ag-registry-only","name":"reg","status":"online"}]}`))
	})
	return mux
}

func (s *countingStub) calls() (runtimes, registry int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runtimes, s.registry
}

func decodeAgentsResponse(t *testing.T, rr *httptest.ResponseRecorder) (source string, cached bool, count int) {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v body=%q", err, rr.Body.String())
	}
	source, _ = body["source"].(string)
	cached, _ = body["cached"].(bool)
	count = int(body["count"].(float64))
	return source, cached, count
}

// 判据①：运行时清单挂住（超过收窄后的时限）⇒ 降级读注册表，source=
// acc-registry-fallback，且整个请求在时限内返回（不会拖到网关 30s）。
func TestACCAgents_FallsBackToRegistryWhenLiveFaceHangs(t *testing.T) {
	narrowACCAgentTimings(t, 100*time.Millisecond, time.Minute)
	stub := &countingStub{slowRuntimes: 800 * time.Millisecond}
	ts := httptest.NewServer(stub.handler())
	defer ts.Close()
	srv := newACCEcosystemServer(t, ts.URL)

	start := time.Now()
	rr := httptest.NewRecorder()
	srv.handleACCAgents(rr, httptest.NewRequest(http.MethodGet, "/api/acc/agents", nil))
	elapsed := time.Since(start)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
	if elapsed > 2*time.Second {
		t.Fatalf("elapsed = %v, want well under 2s (degradation must not hang)", elapsed)
	}
	source, _, count := decodeAgentsResponse(t, rr)
	if source != "acc-registry-fallback" {
		t.Errorf("source = %q, want acc-registry-fallback", source)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1 (registry-only agent)", count)
	}
}

// 判据②：成功结果进缓存 ⇒ 第二次请求 cached:true 且不再打 ACC。
func TestACCAgents_CachesSuccessWithinTTL(t *testing.T) {
	narrowACCAgentTimings(t, time.Second, time.Minute)
	stub := &countingStub{}
	ts := httptest.NewServer(stub.handler())
	defer ts.Close()
	srv := newACCEcosystemServer(t, ts.URL)

	rr1 := httptest.NewRecorder()
	srv.handleACCAgents(rr1, httptest.NewRequest(http.MethodGet, "/api/acc/agents", nil))
	if rr1.Code != http.StatusOK {
		t.Fatalf("first status = %d", rr1.Code)
	}
	source1, cached1, _ := decodeAgentsResponse(t, rr1)
	if source1 != "acc" || cached1 {
		t.Errorf("first response source=%q cached=%v, want acc/false", source1, cached1)
	}

	rr2 := httptest.NewRecorder()
	srv.handleACCAgents(rr2, httptest.NewRequest(http.MethodGet, "/api/acc/agents", nil))
	if rr2.Code != http.StatusOK {
		t.Fatalf("second status = %d", rr2.Code)
	}
	source2, cached2, count2 := decodeAgentsResponse(t, rr2)
	if !cached2 {
		t.Error("second response not marked cached")
	}
	if source2 != "acc" || count2 == 0 {
		t.Errorf("second response source=%q count=%d, want acc/non-zero", source2, count2)
	}
	runtimes, registry := stub.calls()
	if runtimes != 1 || registry != 1 {
		t.Errorf("ACC hits after two requests = (runtimes %d, registry %d), want (1, 1)", runtimes, registry)
	}
}

// 判据③：TTL 过期 ⇒ 缓存失效，重新打 ACC。
func TestACCAgents_CacheExpiresAfterTTL(t *testing.T) {
	narrowACCAgentTimings(t, time.Second, 40*time.Millisecond)
	stub := &countingStub{}
	ts := httptest.NewServer(stub.handler())
	defer ts.Close()
	srv := newACCEcosystemServer(t, ts.URL)

	rr1 := httptest.NewRecorder()
	srv.handleACCAgents(rr1, httptest.NewRequest(http.MethodGet, "/api/acc/agents", nil))
	if rr1.Code != http.StatusOK {
		t.Fatalf("first status = %d", rr1.Code)
	}
	time.Sleep(80 * time.Millisecond)

	rr2 := httptest.NewRecorder()
	srv.handleACCAgents(rr2, httptest.NewRequest(http.MethodGet, "/api/acc/agents", nil))
	if rr2.Code != http.StatusOK {
		t.Fatalf("second status = %d", rr2.Code)
	}
	_, cached2, _ := decodeAgentsResponse(t, rr2)
	if cached2 {
		t.Error("second response served from expired cache")
	}
	runtimes, _ := stub.calls()
	if runtimes != 2 {
		t.Errorf("runtimes hits = %d, want 2 (cache expired, refetched)", runtimes)
	}
}

// 判据④：两面都不可达 ⇒ 502。通道故障必须如实报错，不能渲染成空生态。
func TestACCAgents_Reports502WhenBothFacesFail(t *testing.T) {
	narrowACCAgentTimings(t, time.Second, time.Minute)
	stub := &countingStub{runtimesCode: http.StatusBadGateway, registryCode: http.StatusBadGateway}
	ts := httptest.NewServer(stub.handler())
	defer ts.Close()
	srv := newACCEcosystemServer(t, ts.URL)

	rr := httptest.NewRecorder()
	srv.handleACCAgents(rr, httptest.NewRequest(http.MethodGet, "/api/acc/agents", nil))
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (body=%s)", rr.Code, rr.Body.String())
	}
}

// 判据⑤：运行时清单健康 ⇒ 原合并语义不变：推送面提供 ag-push，
// 拉取面更新其状态（idle 覆盖 running），注册表补 ag-registry-only。
func TestACCAgents_MergesFacesWhenHealthy(t *testing.T) {
	narrowACCAgentTimings(t, time.Second, time.Minute)
	stub := &countingStub{}
	ts := httptest.NewServer(stub.handler())
	defer ts.Close()
	srv := newACCEcosystemServer(t, ts.URL)

	rr := httptest.NewRecorder()
	srv.handleACCAgents(rr, httptest.NewRequest(http.MethodGet, "/api/acc/agents", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d (body=%s)", rr.Code, rr.Body.String())
	}
	var body struct {
		Source string `json:"source"`
		Agents []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v body=%q", err, rr.Body.String())
	}
	if body.Source != "acc" {
		t.Errorf("source = %q, want acc", body.Source)
	}
	if len(body.Agents) != 2 {
		t.Fatalf("agents = %d, want 2", len(body.Agents))
	}
	byID := map[string]string{}
	for _, a := range body.Agents {
		byID[a.ID] = a.Status
	}
	if byID["ag-push"] != "idle" {
		t.Errorf("ag-push status = %q, want idle (read face wins)", byID["ag-push"])
	}
	if byID["ag-registry-only"] != "online" {
		t.Errorf("ag-registry-only status = %q, want online", byID["ag-registry-only"])
	}
}
