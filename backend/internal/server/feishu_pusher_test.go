package server

// feishu_pusher_test.go — feishuInvoicePusher 这一层此前零覆盖。
//
// 8bd951eb 修好了 feishu.Client 的消息格式，6820dcbb 补上了流水线侧的
// 筛选与记账，但中间这层仍然没有任何测试：
//
//	PushInvoice: 读盘 → 取文件名（空则退回 basename）→ 发文件 → 再发一条文字说明
//
// 它是「采集器产出的文件」到「飞书群里那条消息」之间唯一的一环，
// 而整条链路因为飞书凭证未提供从未跑过。

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/email"
	"github.com/halfking/pocket-opencode/backend/internal/feishu"
)

// feishuCall 记录一次飞书调用。
type feishuCall struct {
	path    string
	msgType string
	content map[string]any
}

type pushStub struct {
	srv     *httptest.Server
	calls   []feishuCall
	uploads int
}

func newPushStub(t *testing.T) *pushStub {
	t.Helper()
	ps := &pushStub{}
	ps.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0, "msg": "ok", "tenant_access_token": "t-1", "expire": 7200,
			})
		case strings.HasSuffix(r.URL.Path, "/im/v1/files"):
			ps.uploads++
			_, _ = io.ReadAll(r.Body)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0, "msg": "ok", "data": map[string]any{"file_key": "fk-1"},
			})
		case strings.Contains(r.URL.Path, "/im/v1/messages"):
			var body struct {
				MsgType string `json:"msg_type"`
				Content string `json:"content"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			inner := map[string]any{}
			_ = json.Unmarshal([]byte(body.Content), &inner)
			ps.calls = append(ps.calls, feishuCall{
				path: r.URL.Path, msgType: body.MsgType, content: inner,
			})
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "ok"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(ps.srv.Close)
	return ps
}

func (ps *pushStub) pusher(chatID string) *feishuInvoicePusher {
	return &feishuInvoicePusher{
		client: &feishu.Client{
			BaseURL: ps.srv.URL, AppID: "cli", AppSecret: "s", HTTP: ps.srv.Client(),
		},
		chatID: chatID,
	}
}

func writePDF(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// 正常路径：先发文件，再发一条含金额/对方单位/来源邮件的文字说明。
func TestFeishuInvoicePusher_SendsFileThenNote(t *testing.T) {
	ps := newPushStub(t)
	dir := t.TempDir()
	abs := writePDF(t, dir, "其他-某公司-3500.00-2026-09-24.pdf", "%PDF-1.4 data")

	err := ps.pusher("oc_chat1").PushInvoice(context.Background(), email.Invoice{
		FileName: "其他-某公司-3500.00-2026-09-24.pdf",
		Amount:   3500, Currency: "CNY", Seller: "某公司", Subject: "电子发票开具通知",
	}, abs)
	if err != nil {
		t.Fatalf("PushInvoice: %v", err)
	}
	if ps.uploads != 1 {
		t.Errorf("上传次数=%d, want 1", ps.uploads)
	}
	if len(ps.calls) != 2 {
		t.Fatalf("消息条数=%d, want 2（文件 + 文字说明）", len(ps.calls))
	}
	if ps.calls[0].msgType != "file" || ps.calls[0].content["file_key"] != "fk-1" {
		t.Errorf("第一条应是 file 消息且带 file_key，实际=%+v", ps.calls[0])
	}
	note, _ := ps.calls[1].content["text"].(string)
	if ps.calls[1].msgType != "text" {
		t.Errorf("第二条 msg_type=%q, want text", ps.calls[1].msgType)
	}
	for _, want := range []string{"其他-某公司-3500.00-2026-09-24.pdf", "3500.00", "CNY", "某公司", "电子发票开具通知"} {
		if !strings.Contains(note, want) {
			t.Errorf("文字说明缺少 %q，实际=%q", want, note)
		}
	}
}

// 币种为空要兜底成 CNY：真实库里 Currency 可能是空的（历史行）。
func TestFeishuInvoicePusher_DefaultsCurrencyToCNY(t *testing.T) {
	ps := newPushStub(t)
	dir := t.TempDir()
	abs := writePDF(t, dir, "a-100.00.pdf", "%PDF")
	if err := ps.pusher("oc_chat1").PushInvoice(context.Background(), email.Invoice{
		FileName: "a-100.00.pdf", Amount: 100, Seller: "甲", Subject: "发票",
	}, abs); err != nil {
		t.Fatalf("PushInvoice: %v", err)
	}
	note, _ := ps.calls[1].content["text"].(string)
	if !strings.Contains(note, "CNY") {
		t.Errorf("币种为空时应兜底 CNY，实际=%q", note)
	}
}

// FileName 为空时退回磁盘上的 basename，否则群里的文件名会是空的。
func TestFeishuInvoicePusher_FallsBackToBasename(t *testing.T) {
	ps := newPushStub(t)
	dir := t.TempDir()
	abs := writePDF(t, dir, "发票兜底-88.00.pdf", "%PDF")
	if err := ps.pusher("oc_chat1").PushInvoice(context.Background(), email.Invoice{
		Amount: 88, Seller: "甲", Subject: "发票", FileName: "", // 故意留空
	}, abs); err != nil {
		t.Fatalf("PushInvoice: %v", err)
	}
	note, _ := ps.calls[1].content["text"].(string)
	if !strings.Contains(note, "发票兜底-88.00.pdf") {
		t.Errorf("FileName 为空时应退回 basename，实际=%q", note)
	}
}

// 文件读不到必须报错，且**一条消息都不能发**——否则群里会收到「已归档发票：
// 」这种没有附件的半截消息。
func TestFeishuInvoicePusher_MissingFileSendsNothing(t *testing.T) {
	ps := newPushStub(t)
	err := ps.pusher("oc_chat1").PushInvoice(context.Background(), email.Invoice{
		FileName: "不存在.pdf", Amount: 1,
	}, filepath.Join(t.TempDir(), "不存在.pdf"))
	if err == nil {
		t.Fatal("文件读不到必须报错")
	}
	if !strings.Contains(err.Error(), "不存在.pdf") {
		t.Errorf("错误=%q，应点名文件名（只报 read 目录名看不出是哪张票）", err)
	}
	if ps.uploads != 0 || len(ps.calls) != 0 {
		t.Errorf("读盘失败却仍发了请求：uploads=%d calls=%d", ps.uploads, len(ps.calls))
	}
}

// Available 的三个条件缺一不可：client 为空、chatID 为空、client 未配置凭证。
func TestFeishuInvoicePusher_AvailableRequiresAllThree(t *testing.T) {
	ps := newPushStub(t)
	if !ps.pusher("oc_chat1").Available() {
		t.Error("三件齐备时必须 Available")
	}
	if (&feishuInvoicePusher{client: &feishu.Client{}, chatID: "oc_x"}).Available() {
		t.Error("未配置 app_id/app_secret 时不该 Available")
	}
	if (&feishuInvoicePusher{client: &feishu.Client{AppID: "a", AppSecret: "b"}}).Available() {
		t.Error("chatID 为空时不该 Available")
	}
	var nilPusher *feishuInvoicePusher
	if nilPusher.Available() {
		t.Error("nil 接收者必须安全返回 false")
	}
}
