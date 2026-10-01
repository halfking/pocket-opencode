package feishu

// client_send_test.go — 消息发送与文件上传这条链路此前零覆盖。
//
// sheet.go（建表 / 取 sheet_id / 写值）有 sheet_test.go 覆盖，handler.go 的
// 日志脱敏有 handler_log_test.go 覆盖，但 client.go 里的
// TenantAccessToken / UploadFile / SendMessage / SendText / SendFile /
// SendInvoiceFile **一个测试都没有**。
//
// 而 SendInvoiceFile 就是需求「发送到我们的飞书上」的核心动作。飞书凭证至今
// 未提供，于是这条路径在真实环境里一次都没跑过——出问题时只能等交出凭证的
// 那一刻才暴露。
//
// 本文件把**线上格式**钉死：飞书 im/v1/messages 的 content 字段是
// 「一个字符串，内容是被序列化的消息体」：
//
//	{"receive_id":"oc_x","msg_type":"text","content":"{\"text\":\"hi\"}"}
//
// 也就是说 content 内部应当直接是 {"text":...}，不能再多包一层。

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// feishuStub 记录收到的请求，并按路径返回可编程的响应。
type feishuStub struct {
	srv *httptest.Server

	tokenCalls   int
	tokenServed  int
	uploadCalls  int
	messageCalls int

	// 最后一次消息请求的 body（已解析两层）。
	lastMsg map[string]any
	// 最后一次 upload 的 multipart 字段与文件名。
	uploadFileName string
	uploadFileData []byte
	uploadFileType string

	// 响应覆盖（0 = 用默认成功响应）
	codeMsg    int
	codeUpload int
	codeSend   int
	msgText    string
}

func newStub(t *testing.T) *feishuStub {
	t.Helper()
	st := &feishuStub{}
	st.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal"):
			st.tokenCalls++
			code := st.codeMsg
			msg := st.msgText
			if st.tokenServed > 0 {
				// 第二次起返回失败，用于验证 token 缓存。
				code, msg = 99991663, "token cache should have been used"
			} else {
				st.tokenServed++
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": code, "msg": msg,
				"tenant_access_token": "t-abc123", "expire": 7200,
			})

		case strings.HasSuffix(r.URL.Path, "/im/v1/files"):
			st.uploadCalls++
			mr, err := multipartReader(r)
			if err == nil {
				st.uploadFileName, st.uploadFileData, st.uploadFileType = mr.name, mr.data, mr.fileType
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": st.codeUpload, "msg": st.msgText,
				"data": map[string]any{"file_key": "file_v3_key42"},
			})

		case strings.Contains(r.URL.Path, "/im/v1/messages"):
			st.messageCalls++
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &st.lastMsg)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": st.codeSend, "msg": st.msgText,
				"data": map[string]any{"message_id": "om_1"},
			})

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(st.srv.Close)
	return st
}

// uploadedFile 是从 multipart 里解出来的上传内容。
type uploadedFile struct {
	name     string
	data     []byte
	fileType string
}

func multipartReader(r *http.Request) (uploadedFile, error) {
	_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return uploadedFile{}, err
	}
	out := uploadedFile{}
	mr := multipart.NewReader(r.Body, params["boundary"])
	for {
		part, err := mr.NextPart()
		if err != nil {
			return out, nil
		}
		b, _ := io.ReadAll(part)
		switch part.FormName() {
		case "file":
			out.name, out.data = part.FileName(), b
		case "file_type":
			out.fileType = string(b)
		}
	}
}

func (s *feishuStub) client() *Client {
	return &Client{BaseURL: s.srv.URL, AppID: "cli_x", AppSecret: "secret", HTTP: s.srv.Client()}
}

// 文本消息的 content 必须是 {"text":"..."} 本身，不能再包一层 {"content":...}。
func TestSendText_ContentIsNotDoubleWrapped(t *testing.T) {
	st := newStub(t)
	if err := st.client().SendText(context.Background(), "chat_id", "oc_chat1", "发票 3500.00 已下载"); err != nil {
		t.Fatalf("SendText: %v", err)
	}
	if st.lastMsg == nil {
		t.Fatal("没有收到消息请求")
	}
	if got := st.lastMsg["msg_type"]; got != "text" {
		t.Errorf("msg_type=%v, want text", got)
	}
	if got := st.lastMsg["receive_id"]; got != "oc_chat1" {
		t.Errorf("receive_id=%v, want oc_chat1", got)
	}
	content, ok := st.lastMsg["content"].(string)
	if !ok {
		t.Fatalf("content 必须是字符串（飞书约定：内容是被序列化的消息体），实际类型 %T", st.lastMsg["content"])
	}
	// content 内部必须直接是 {"text":...}
	var inner map[string]any
	if err := json.Unmarshal([]byte(content), &inner); err != nil {
		t.Fatalf("content 不是合法 JSON 字符串: %q", content)
	}
	text, ok := inner["text"]
	if !ok {
		t.Errorf("content 内部缺少 text 键，实际=%v —— 多包了一层：%q", inner, content)
	} else if text != "发票 3500.00 已下载" {
		t.Errorf("text=%v", text)
	}
}

// SendInvoiceFile：先上传拿 file_key，再以 msg_type=file 发出去。
func TestSendInvoiceFile_UploadsThenSendsFileKey(t *testing.T) {
	st := newStub(t)
	pdf := []byte("%PDF-1.4\n1 0 obj\n<<>>\nendobj\ntrailer<<>>\n%%EOF")
	if err := st.client().SendInvoiceFile(context.Background(), "oc_chat1", "其他-某公司-3500.00-2026-09-24.pdf", pdf); err != nil {
		t.Fatalf("SendInvoiceFile: %v", err)
	}
	if st.uploadCalls != 1 || st.messageCalls != 1 {
		t.Fatalf("upload=%d message=%d, want 各 1", st.uploadCalls, st.messageCalls)
	}
	if st.uploadFileName != "其他-某公司-3500.00-2026-09-24.pdf" {
		t.Errorf("上传文件名=%q", st.uploadFileName)
	}
	if string(st.uploadFileData) != string(pdf) {
		t.Errorf("上传内容与传入不一致（%d vs %d 字节）", len(st.uploadFileData), len(pdf))
	}
	if st.uploadFileType != "pdf" {
		t.Errorf("file_type=%q, want pdf", st.uploadFileType)
	}
	if got := st.lastMsg["msg_type"]; got != "file" {
		t.Errorf("msg_type=%v, want file", got)
	}
	var inner map[string]any
	if err := json.Unmarshal([]byte(st.lastMsg["content"].(string)), &inner); err != nil {
		t.Fatalf("content 不是合法 JSON 字符串: %q", st.lastMsg["content"])
	}
	if inner["file_key"] != "file_v3_key42" {
		t.Errorf("content 内部=%v, want file_key=file_v3_key42", inner)
	}
}

// token 必须被缓存：连续两次发送只取一次 token。
func TestTenantAccessToken_IsCachedAcrossCalls(t *testing.T) {
	st := newStub(t)
	c := st.client()
	for i := 0; i < 2; i++ {
		if err := c.SendText(context.Background(), "chat_id", "oc_chat1", "x"); err != nil {
			t.Fatalf("第 %d 次 SendText: %v", i+1, err)
		}
	}
	if st.tokenCalls != 1 {
		t.Errorf("token 接口被调用 %d 次，want 1（没缓存住）", st.tokenCalls)
	}
}

// 飞书业务错误码必须被如实带出。1310213 是「没有电子表格权限」，
// 用户拿到凭证后最可能撞上的就是它——报成别的就查不下去。
func TestFeishuErrors_SurfaceBusinessCode(t *testing.T) {
	st := newStub(t)
	st.codeSend = 1310213
	st.msgText = "no permission"
	err := st.client().SendText(context.Background(), "chat_id", "oc_chat1", "x")
	if err == nil {
		t.Fatal("业务错误码非 0 时必须报错")
	}
	if !strings.Contains(err.Error(), "1310213") {
		t.Errorf("错误=%q，必须包含业务码 1310213", err)
	}

	st2 := newStub(t)
	st2.codeUpload = 230002
	st2.msgText = "upload denied"
	err = st2.client().SendFile(context.Background(), "chat_id", "oc_chat1", "a.pdf", []byte("%PDF"))
	if err == nil || !strings.Contains(err.Error(), "230002") {
		t.Errorf("上传错误=%v，必须包含业务码 230002", err)
	}
}

// 未配置凭证时必须明确失败，而不是拿空 token 去请求。
func TestFeishu_NotConfiguredFailsFast(t *testing.T) {
	c := &Client{BaseURL: "http://127.0.0.1:1", HTTP: http.DefaultClient}
	if _, err := c.TenantAccessToken(context.Background()); err == nil {
		t.Fatal("未配置 app_id/app_secret 时必须报错")
	}
	if err := c.SendInvoiceFile(context.Background(), "oc_x", "a.pdf", []byte("%PDF")); err == nil {
		t.Fatal("未配置时发文件必须报错")
	}
}
