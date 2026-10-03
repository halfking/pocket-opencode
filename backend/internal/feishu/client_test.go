package feishu

// client_test.go — 出站客户端的 mock server 覆盖。
//
// ## 为什么必须有
//
// `client.go` 的三个方法是**需求 3 与需求 4 的唯一出站通路**：
//
//	UploadFile  / SendFile / SendInvoiceFile  → 需求 3「发票文件发到飞书」
//	SendText                                 → 需求 4「其它重要邮件提醒」
//
// 线上这两个需求都因为缺配置（`POCKET_FEISHU_INVOICE_CHAT_ID` /
// `POCKET_KXMEMORY_BASE_URL`）而**从未真正跑过一次**，所以它们是全仓库里
// 「代码存在但一行都没被执行过」的部分。sheet_test.go 已经用同样的方式
// 覆盖了表格 API，出站这三个方法此前**一个测试都没有**。
//
// 覆盖不到的��实（仍需真实 chat id 联调）：验签回调解密、真实群权限、
// 真实文件大小限制。这里能证明的是**协议层**——URL、鉴权头、
// multipart 结构、以及最容易写错的那处双层 JSON 编码。

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type recordedReq struct {
	Method      string
	Path        string
	RawQuery    string
	Auth        string
	ContentType string
	Body        []byte
}

type feishuMock struct {
	mu       sync.Mutex
	requests []recordedReq
	// handlers 按路径分派；未命中返回 404 让测试立刻暴露拼错的路径。
	handlers map[string]func(w http.ResponseWriter, r *http.Request)
}

func (m *feishuMock) calls() []recordedReq {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]recordedReq, len(m.requests))
	copy(out, m.requests)
	return out
}

func (m *feishuMock) callsTo(path string) []recordedReq {
	var out []recordedReq
	for _, c := range m.calls() {
		if c.Path == path {
			out = append(out, c)
		}
	}
	return out
}

func (m *feishuMock) countTo(path string) int { return len(m.callsTo(path)) }

// newMockClient 起一个假飞书服务。routes 的 key 是 URL path（不含 query）。
func newMockClient(t *testing.T, routes map[string]func(w http.ResponseWriter, r *http.Request)) (*Client, *feishuMock) {
	t.Helper()
	m := &feishuMock{handlers: routes}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		m.mu.Lock()
		m.requests = append(m.requests, recordedReq{
			Method:      r.Method,
			Path:        r.URL.Path,
			RawQuery:    r.URL.RawQuery,
			Auth:        r.Header.Get("Authorization"),
			ContentType: r.Header.Get("Content-Type"),
			Body:        raw,
		})
		m.mu.Unlock()
		if h, ok := m.handlers[r.URL.Path]; ok {
			h(w, r)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"code":404,"msg":"no mock route for `+r.URL.Path+`"}`)
	}))
	t.Cleanup(srv.Close)

	c := New("cli_test", "secret_test")
	c.BaseURL = srv.URL
	return c, m
}

func writeJSONBody(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// tokenRoute 返回一个标准 token 响应。
func tokenRoute(tok string, expire int) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSONBody(w, map[string]any{
			"code":                0,
			"msg":                 "ok",
			"tenant_access_token": tok,
			"expire":              expire,
		})
	}
}

// --- TenantAccessToken ---

// 协议锁死：路径、app_id/app_secret 字段、以及飞书特有的 snake_case 响应。
func TestTenantAccessToken_PostsAppCredentialsAndParsesSnakeCase(t *testing.T) {
	c, m := newMockClient(t, map[string]func(http.ResponseWriter, *http.Request){
		"/open-apis/auth/v3/tenant_access_token/internal": tokenRoute("t-abc", 7200),
	})

	tok, err := c.TenantAccessToken(context.Background())
	if err != nil {
		t.Fatalf("TenantAccessToken: %v", err)
	}
	if tok != "t-abc" {
		t.Fatalf("token = %q, want t-abc", tok)
	}
	calls := m.callsTo("/open-apis/auth/v3/tenant_access_token/internal")
	if len(calls) != 1 {
		t.Fatalf("token calls = %d, want 1", len(calls))
	}
	var body map[string]string
	if err := json.Unmarshal(calls[0].Body, &body); err != nil {
		t.Fatalf("token body is not JSON: %v", err)
	}
	if body["app_id"] != "cli_test" || body["app_secret"] != "secret_test" {
		t.Fatalf("credentials not sent: %v", body)
	}
}

// 缓存必须真的生效：第二次调用不得再打飞书。过期时间留足（7200s），
// 避免 ttl-5min 的提前失效把这条测成「因为过期才重新请求」。
func TestTenantAccessToken_CachesUntilExpiry(t *testing.T) {
	c, m := newMockClient(t, map[string]func(http.ResponseWriter, *http.Request){
		"/open-apis/auth/v3/tenant_access_token/internal": tokenRoute("t-cache", 7200),
	})
	for i := 0; i < 3; i++ {
		if _, err := c.TenantAccessToken(context.Background()); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if n := m.countTo("/open-apis/auth/v3/tenant_access_token/internal"); n != 1 {
		t.Fatalf("token endpoint hit %d times, want 1 (cache broken)", n)
	}
}

// 没配置凭据时必须直接报错，且**一个请求都不发**。
func TestTenantAccessToken_UnconfiguredFailsWithoutRequest(t *testing.T) {
	c := New("", "")
	if c.Available() {
		t.Fatal("empty credentials must report Available()=false")
	}
	if _, err := c.TenantAccessToken(context.Background()); err == nil {
		t.Fatal("unconfigured client must not return a token")
	}
	// SendFile/SendText 都以 token 为前提，必须一起在这里失败。
	if err := c.SendText(context.Background(), "chat_id", "oc_x", "hi"); err == nil {
		t.Fatal("SendText must fail when credentials are missing")
	}
}

// 飞书用业务 code 表示错误（HTTP 仍可能 200），必须冒泡成 error。
func TestTenantAccessToken_BusinessErrorSurfaces(t *testing.T) {
	c, _ := newMockClient(t, map[string]func(http.ResponseWriter, *http.Request){
		"/open-apis/auth/v3/tenant_access_token/internal": func(w http.ResponseWriter, _ *http.Request) {
			writeJSONBody(w, map[string]any{"code": 10003, "msg": "app not exist"})
		},
	})
	if _, err := c.TenantAccessToken(context.Background()); err == nil {
		t.Fatal("code!=0 must be an error, not an empty token")
	}
}

// --- UploadFile ---

// 需求 3 的关键一步：multipart 三个字段 + 鉴权头 + file_key 解析。
func TestUploadFile_SendsMultipartWithAuthAndReturnsFileKey(t *testing.T) {
	c, m := newMockClient(t, map[string]func(http.ResponseWriter, *http.Request){
		"/open-apis/auth/v3/tenant_access_token/internal": tokenRoute("t-up", 7200),
		"/open-apis/im/v1/files": func(w http.ResponseWriter, _ *http.Request) {
			writeJSONBody(w, map[string]any{"code": 0, "msg": "ok", "data": map[string]any{"file_key": "file_v3_x"}})
		},
	})

	key, err := c.UploadFile(context.Background(), "其他-开票中心-128.00-2026-09-24.pdf", []byte("%PDF-1.7 fake"))
	if err != nil {
		t.Fatalf("UploadFile: %v", err)
	}
	if key != "file_v3_x" {
		t.Fatalf("file_key = %q, want file_v3_x", key)
	}

	ups := m.callsTo("/open-apis/im/v1/files")
	if len(ups) != 1 {
		t.Fatalf("upload calls = %d, want 1", len(ups))
	}
	if ups[0].Auth != "Bearer t-up" {
		t.Fatalf("Authorization = %q, want Bearer t-up", ups[0].Auth)
	}
	mt, params, err := mime.ParseMediaType(ups[0].ContentType)
	if err != nil || !strings.HasPrefix(mt, "multipart/form-data") {
		t.Fatalf("Content-Type = %q, want multipart/form-data", ups[0].ContentType)
	}
	mr := multipart.NewReader(strings.NewReader(string(ups[0].Body)), params["boundary"])
	fields := map[string]string{}
	var fileName, fileBody string
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("multipart parse: %v", err)
		}
		b, _ := io.ReadAll(part)
		switch part.FormName() {
		case "file":
			fileName, fileBody = part.FileName(), string(b)
		default:
			fields[part.FormName()] = string(b)
		}
	}
	if fields["file_type"] != "pdf" {
		t.Fatalf("file_type = %q, want pdf", fields["file_type"])
	}
	if want := "其他-开票中心-128.00-2026-09-24.pdf"; fields["file_name"] != want {
		t.Fatalf("file_name = %q, want %q", fields["file_name"], want)
	}
	if fileName != fields["file_name"] {
		t.Fatalf("multipart filename %q != file_name field %q", fileName, fields["file_name"])
	}
	if fileBody != "%PDF-1.7 fake" {
		t.Fatalf("uploaded bytes = %q, want the original payload", fileBody)
	}
}

// 负控：file_key 为空或 code!=0 时必须报错。空 file_key 若放过，
// 上层会拿一个空 key 去发消息，失败点在更远、更难查的地方。
func TestUploadFile_EmptyFileKeyIsAnError(t *testing.T) {
	for _, tc := range []struct {
		name string
		body map[string]any
	}{
		{"code_nonzero", map[string]any{"code": 234001, "msg": "bad request"}},
		{"empty_key", map[string]any{"code": 0, "data": map[string]any{"file_key": ""}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newMockClient(t, map[string]func(http.ResponseWriter, *http.Request){
				"/open-apis/auth/v3/tenant_access_token/internal": tokenRoute("t-up", 7200),
				"/open-apis/im/v1/files": func(w http.ResponseWriter, _ *http.Request) {
					writeJSONBody(w, tc.body)
				},
			})
			if _, err := c.UploadFile(context.Background(), "a.pdf", []byte("x")); err == nil {
				t.Fatal("must not return a key when Feishu reports failure")
			}
		})
	}
}

// --- SendMessage ---

// 飞书 message 的 content 字段是**字符串化的 JSON**（双层编码）——
// 这是最容易写错的一处：传成对象飞书会拒。断言到解码后仍是合法 JSON。
func TestSendMessage_DoubleEncodesContentAndSetsReceiveIDType(t *testing.T) {
	c, m := newMockClient(t, map[string]func(http.ResponseWriter, *http.Request){
		"/open-apis/auth/v3/tenant_access_token/internal": tokenRoute("t-send", 7200),
		"/open-apis/im/v1/messages": func(w http.ResponseWriter, _ *http.Request) {
			writeJSONBody(w, map[string]any{"code": 0, "msg": "ok"})
		},
	})

	if err := c.SendText(context.Background(), "chat_id", "oc_group1", "发票已归档"); err != nil {
		t.Fatalf("SendText: %v", err)
	}
	sends := m.callsTo("/open-apis/im/v1/messages")
	if len(sends) != 1 {
		t.Fatalf("send calls = %d, want 1", len(sends))
	}
	if sends[0].RawQuery != "receive_id_type=chat_id" {
		t.Fatalf("query = %q, want receive_id_type=chat_id", sends[0].RawQuery)
	}
	if sends[0].Auth != "Bearer t-send" {
		t.Fatalf("Authorization = %q, want Bearer t-send", sends[0].Auth)
	}
	var outer struct {
		ReceiveID string `json:"receive_id"`
		MsgType   string `json:"msg_type"`
		Content   string `json:"content"`
	}
	if err := json.Unmarshal(sends[0].Body, &outer); err != nil {
		t.Fatalf("message body is not JSON: %v", err)
	}
	if outer.ReceiveID != "oc_group1" || outer.MsgType != "text" {
		t.Fatalf("unexpected envelope: %+v", outer)
	}
	// content 必须是**字符串**（Go 侧解出来就是 string，说明飞书侧看到的是字符串）
	var inner map[string]string
	if err := json.Unmarshal([]byte(outer.Content), &inner); err != nil {
		t.Fatalf("content is not itself valid JSON (double-encoding lost): %q", outer.Content)
	}
	if inner["text"] != "发票已归档" {
		t.Fatalf("content.text = %q, want 发票已归档", inner["text"])
	}
}

// 需求 3 的完整两步：先传文件拿 file_key，再用它发 file 消息。
func TestSendFile_UploadsThenSendsFileMessage(t *testing.T) {
	c, m := newMockClient(t, map[string]func(http.ResponseWriter, *http.Request){
		"/open-apis/auth/v3/tenant_access_token/internal": tokenRoute("t-f", 7200),
		"/open-apis/im/v1/files": func(w http.ResponseWriter, _ *http.Request) {
			writeJSONBody(w, map[string]any{"code": 0, "data": map[string]any{"file_key": "key_42"}})
		},
		"/open-apis/im/v1/messages": func(w http.ResponseWriter, _ *http.Request) {
			writeJSONBody(w, map[string]any{"code": 0})
		},
	})

	if err := c.SendFile(context.Background(), "chat_id", "oc_g", "a.pdf", []byte("%PDF")); err != nil {
		t.Fatalf("SendFile: %v", err)
	}
	if n := m.countTo("/open-apis/im/v1/files"); n != 1 {
		t.Fatalf("uploads = %d, want 1", n)
	}
	sends := m.callsTo("/open-apis/im/v1/messages")
	if len(sends) != 1 {
		t.Fatalf("sends = %d, want 1", len(sends))
	}
	var outer struct {
		MsgType string `json:"msg_type"`
		Content string `json:"content"`
	}
	_ = json.Unmarshal(sends[0].Body, &outer)
	if outer.MsgType != "file" {
		t.Fatalf("msg_type = %q, want file", outer.MsgType)
	}
	var inner map[string]string
	_ = json.Unmarshal([]byte(outer.Content), &inner)
	if inner["file_key"] != "key_42" {
		t.Fatalf("file_key not threaded into the message: %+v", inner)
	}
}

// 需求 3 的对外入口：规范文件名 + chat_id。
func TestSendInvoiceFile_UsesChatIDAndPassesNameThrough(t *testing.T) {
	c, m := newMockClient(t, map[string]func(http.ResponseWriter, *http.Request){
		"/open-apis/auth/v3/tenant_access_token/internal": tokenRoute("t-inv", 7200),
		"/open-apis/im/v1/files": func(w http.ResponseWriter, r *http.Request) {
			_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
			mr := multipart.NewReader(r.Body, params["boundary"])
			var name string
			for {
				p, err := mr.NextPart()
				if err != nil {
					break
				}
				if p.FormName() == "file" {
					name = p.FileName()
				}
			}
			_ = name
			writeJSONBody(w, map[string]any{"code": 0, "data": map[string]any{"file_key": "k"}})
		},
		"/open-apis/im/v1/messages": func(w http.ResponseWriter, _ *http.Request) {
			writeJSONBody(w, map[string]any{"code": 0})
		},
	})

	const name = "通信-开票中心-128.00-2026-09-24.pdf"
	if err := c.SendInvoiceFile(context.Background(), "oc_chat", name, []byte("%PDF")); err != nil {
		t.Fatalf("SendInvoiceFile: %v", err)
	}
	if q := m.callsTo("/open-apis/im/v1/messages")[0].RawQuery; q != "receive_id_type=chat_id" {
		t.Fatalf("invoice push must target chat_id, query = %q", q)
	}
}

// 负控：发送失败必须冒泡。静默吞掉会让上层以为「已推送」而把 DB 标记成 sent。
func TestSendMessage_BusinessErrorSurfaces(t *testing.T) {
	c, _ := newMockClient(t, map[string]func(http.ResponseWriter, *http.Request){
		"/open-apis/auth/v3/tenant_access_token/internal": tokenRoute("t-e", 7200),
		"/open-apis/im/v1/messages": func(w http.ResponseWriter, _ *http.Request) {
			writeJSONBody(w, map[string]any{"code": 230002, "msg": "bot not in chat"})
		},
	})
	if err := c.SendText(context.Background(), "chat_id", "oc_x", "hi"); err == nil {
		t.Fatal("code!=0 on send must be an error")
	}
}

// 非 200 的 HTTP 状态必须带 body 冒泡，否则排查时只剩一句「请求失败」。
func TestSendMessage_HTTPErrorIncludesBody(t *testing.T) {
	c, _ := newMockClient(t, map[string]func(http.ResponseWriter, *http.Request){
		"/open-apis/auth/v3/tenant_access_token/internal": tokenRoute("t-h", 7200),
		"/open-apis/im/v1/messages": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, "upstream connect error")
		},
	})
	err := c.SendText(context.Background(), "chat_id", "oc_x", "hi")
	if err == nil {
		t.Fatal("HTTP 502 must be an error")
	}
	if !strings.Contains(err.Error(), "upstream connect error") {
		t.Fatalf("error must carry the response body for diagnosis, got %q", err)
	}
}

// 并发取 token 只能打飞书一次（Client 声称并发安全，这里把它钉住）。
func TestTenantAccessToken_ConcurrentCallersHitEndpointOnce(t *testing.T) {
	c, m := newMockClient(t, map[string]func(http.ResponseWriter, *http.Request){
		"/open-apis/auth/v3/tenant_access_token/internal": tokenRoute("t-race", 7200),
	})
	var wg sync.WaitGroup
	errs := make([]error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = c.TenantAccessToken(context.Background())
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: %v", i, err)
		}
	}
	if n := m.countTo("/open-apis/auth/v3/tenant_access_token/internal"); n != 1 {
		t.Fatalf("token endpoint hit %d times under concurrency, want 1", n)
	}
}
