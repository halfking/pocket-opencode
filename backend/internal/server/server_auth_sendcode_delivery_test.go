package server

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/notify"
)

// send-code 必须在响应里声明投递能力（delivery）。
//
// 背景：忘记密码第 1 步在没配 SMTP 的部署上恒返 200（防枚举，刻意如此），
// 但验证码只入库、根本不会发邮件。前端以前只看 status 就推进到第 2 步，
// 于是用户静默卡死，界面上没有任何错误。delivery 让前端能区分
// 「已发出」与「这台机器压根没邮件通道」——它是全局部署事实，与该邮箱是否
// 已注册无关，因此不破坏防枚举。
func TestSendCode_DeclaresDeliveryCapability(t *testing.T) {
	pool := mustTestPool(t)

	t.Run("no SMTP -> delivery=none", func(t *testing.T) {
		srv := newExtendedAuthTestServer(t, pool) // SetAuthExt(cs, nil, nil)
		rr := postJSON(t, srv, "/api/auth/send-code",
			fmt.Sprintf(`{"email":%q,"purpose":"reset"}`, testEmail))
		if rr.Code != http.StatusOK {
			t.Fatalf("send-code: %d %s", rr.Code, rr.Body.String())
		}
		var sent struct {
			Delivery  string `json:"delivery"`
			DebugCode string `json:"debug_code"`
		}
		decodeJSON(t, rr, &sent)
		if sent.Delivery != "none" {
			t.Errorf("delivery = %q, want %q (smtpClient is nil here)", sent.Delivery, "none")
		}
		// 本 helper 开了 SMTPDebugEcho，dev 下仍应回显验证码。
		if sent.DebugCode == "" {
			t.Error("expected debug_code when SMTPDebugEcho is on")
		}
	})

	t.Run("with SMTP -> delivery=smtp", func(t *testing.T) {
		srv := newExtendedAuthTestServer(t, pool)
		// 换成非 nil 的 SMTP 客户端。Client 只有未导出的 cfg 字段，
		// 零值字面量在这里就够用——本用例只关心 delivery 的取值，
		// 真正的投递行为由 notify 包的测试覆盖。
		srv.SetAuthExt(srv.codeStore, &notify.Client{}, nil)
		rr := postJSON(t, srv, "/api/auth/send-code",
			fmt.Sprintf(`{"email":%q,"purpose":"reset"}`, testEmail))
		if rr.Code != http.StatusOK {
			t.Fatalf("send-code: %d %s", rr.Code, rr.Body.String())
		}
		var sent struct {
			Delivery string `json:"delivery"`
		}
		decodeJSON(t, rr, &sent)
		if sent.Delivery != "smtp" {
			t.Errorf("delivery = %q, want %q (smtpClient is non-nil here)", sent.Delivery, "smtp")
		}
	})

	// 防枚举不能因为加了 delivery 而被破坏：非法邮箱同样恒返 200，
	// 而且同样要带上 delivery，否则前端在异常分支上会误判成「没发出去」。
	t.Run("invalid email still returns 200 and still declares delivery", func(t *testing.T) {
		srv := newExtendedAuthTestServer(t, pool)
		rr := postJSON(t, srv, "/api/auth/send-code", `{"email":"not-an-email","purpose":"reset"}`)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200 for anti-enumeration, got %d %s", rr.Code, rr.Body.String())
		}
		var sent struct {
			OK       bool   `json:"ok"`
			Delivery string `json:"delivery"`
		}
		decodeJSON(t, rr, &sent)
		if !sent.OK {
			t.Error("expected ok:true")
		}
		if sent.Delivery == "" {
			t.Error("delivery must be present on the anti-enumeration path too")
		}
	})
}
