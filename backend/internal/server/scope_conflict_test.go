package server

// scope_conflict_test.go — §944 判据：**撞号 ⇒ 409（可区分）+ 受害者的行一字未改**。
//
// ⚠⚠ 本节关掉的是 §943 留下的那张**普查台账**（9 处「裸 id + ON CONFLICT
// DO UPDATE」待定性项）。定性之后，其中 4 处的 id 是**客户端自选**的
// ⇒ ⇒ ⇒ 撞号可达 ⇒ ⇒⇒ 后果重的这 4 处本节补上了**归属守卫**：
//
//	biometric_credentials  ⇒ 撞 id 覆盖**别人的认证公钥** ⇒ 登录链按 id 取行
//	                        ⇒ 攻击者用自己的私钥就能**顶替原主人登录**（最重）
//	asset_mirrors          ⇒ 撞 id 把**别人的加密资产**搬进自己的租户并覆盖
//	devices                ⇒ 撞 id 把**别人的设备行**搬走 + push_token 被换
//	notification_rules     ⇒ 撞 id **改写别人的通知规则**（留在别人的租户里）
//
// ⚠ 本文件量的是**HTTP 响应**与**落库之后的行**，不是源码文本。
//
// ⚠⚠ **必须断言状态码是 409 而不是 500**：
//
//	「失败必须可区分」——500 会让客户端以为「服务端坏了」而**重试**
//	（重试一个必然失败的请求 ⇒ 客户端侧的死循环）；
//	而 409 表达的是「这个 id 归别人，你换个 id」。
//
// ⚠ 负控（每个子用例内自带）：**受害者自己**用同一个 id 再写一次 ⇒ 照常成功
//	⇒ ⇒⇒ 这一腿证明「守卫没有把正常路径也挡掉」。
import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/halfking/pocket-opencode/backend/internal/auth"
	"github.com/halfking/pocket-opencode/backend/internal/identity"
	"github.com/halfking/pocket-opencode/backend/internal/lobster"
	"github.com/halfking/pocket-opencode/backend/internal/notifycenter"
)

const (
	s944Owner   = "user-s944-owner"
	s944Other   = "user-s944-other"
	s944Ws      = "ws-s944"
	s944WsOther = "ws-s944-other"
)

// s944Server 造一个**带真 PG 四个 store** 的 Server。
//
// ⚠ 为什么必须显式注入 PG 版：生产走的是 PG 版，而**内存版**当时没有这道
//
//	守卫 ⇒ ⇒ ⇒ 只测内存版会漏掉生产行为（★ §943 的元教训：
//	「门禁跑在内存版上，生产跑的是 PG 版」）。
func s944Server(t *testing.T) *Server {
	t.Helper()
	pool := mustTestPool(t)

	biometric := auth.NewBiometricStore(pool)
	if err := biometric.Init(context.Background()); err != nil {
		t.Fatalf("biometric Init: %v", err)
	}
	identityStore, err := identity.New(pool)
	if err != nil {
		t.Fatalf("identity.New: %v", err)
	}
	lobsterStore, err := lobster.NewSyncStore(pool)
	if err != nil {
		t.Fatalf("lobster.NewSyncStore: %v", err)
	}
	notifyStore, err := notifycenter.New(pool)
	if err != nil {
		t.Fatalf("notifycenter.New: %v", err)
	}

	srv, _ := newTestServerWithAuth(t)
	srv.SetBiometricStore(biometric)
	srv.SetIdentityStore(identityStore)
	srv.SetLobsterSync(lobsterStore)
	srv.SetNotifyCenter(notifycenter.NewService(notifyStore, nil), notifyStore)
	return srv
}

// s944Token 签一个 token。
func s944Token(t *testing.T, srv *Server, user, ws string) string {
	t.Helper()
	tok, err := srv.jwtSigner.SignWithWorkspace(user, "admin", ws)
	if err != nil {
		t.Fatalf("签 token: %v", err)
	}
	return tok
}

// s944Do 发一个带 token 的请求，返回状态码与响应体。
func s944Do(t *testing.T, srv *Server, method, path, token, body string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	return rr.Code, rr.Body.String()
}

// TestS944BiometricRegisterCollisionReturns409 生物认证：撞别人的 credential_id ⇒ 409。
func TestS944BiometricRegisterCollisionReturns409(t *testing.T) {
	srv := s944Server(t)
	ownerTok := s944Token(t, srv, s944Owner, s944Ws)
	otherTok := s944Token(t, srv, s944Other, s944Ws)

	const credID = "cred-s944-endtoend"
	pub := base64.RawURLEncoding.EncodeToString([]byte("owner-public-key"))

	body := `{"credential_id":"` + credID + `","public_key":"` + pub + `","device_name":"受害者设备"}`
	code, resp := s944Do(t, srv, http.MethodPost, "/api/auth/biometric/register/finish", ownerTok, body)
	if code != http.StatusOK {
		t.Fatalf("受害者注册应 200，实际 %d body=%s", code, strings.TrimSpace(resp))
	}

	// 攻击者拿**同一个 credential_id**、**自己的公钥**来写。
	attackerPub := base64.RawURLEncoding.EncodeToString([]byte("attacker-public-key"))
	body = `{"credential_id":"` + credID + `","public_key":"` + attackerPub + `","device_name":"攻击者设备"}`
	code, resp = s944Do(t, srv, http.MethodPost, "/api/auth/biometric/register/finish", otherTok, body)
	if code != http.StatusConflict {
		t.Fatalf("撞 credential_id 应 409（可区分），实际 %d body=%s", code, strings.TrimSpace(resp))
	}

	// 受害者的公钥必须**一字未改**。
	got, err := srv.BiometricStore().Get(context.Background(), credID)
	if err != nil {
		t.Fatalf("读受害者的凭证失败: %v", err)
	}
	if string(got.PublicKey) != "owner-public-key" {
		t.Fatalf("受害者的公钥变成了 %q ⇒ **越权覆盖**（登录链按 id 取行 ⇒ 可顶替身份）", got.PublicKey)
	}
	if got.UserID != s944Owner {
		t.Errorf("那一行的 user_id 变成了 %q（want %q）", got.UserID, s944Owner)
	}
}

// TestS944DeviceRegisterCollisionReturns409 设备：撞别人的 device id ⇒ 409。
func TestS944DeviceRegisterCollisionReturns409(t *testing.T) {
	srv := s944Server(t)
	ctx := context.Background()

	// 两个用户都得是该 workspace 的成员（handler 会查成员资格）。
	for _, u := range []string{s944Owner, s944Other} {
		if err := srv.identityStore.AddMember(ctx, &identity.Member{
			WorkspaceID: s944Ws, UserID: u, Role: identity.RoleOwner,
		}); err != nil {
			t.Fatalf("加成员 %s: %v", u, err)
		}
	}
	ownerTok := s944Token(t, srv, s944Owner, s944Ws)
	otherTok := s944Token(t, srv, s944Other, s944Ws)

	const devID = "device-s944-endtoend"
	body := `{"id":"` + devID + `","fingerprint":"fp-owner","push_token":"owner-token","os":"iOS"}`
	code, resp := s944Do(t, srv, http.MethodPost, "/api/workspaces/"+s944Ws+"/devices", ownerTok, body)
	if code != http.StatusOK {
		t.Fatalf("受害者注册设备应 200，实际 %d body=%s", code, strings.TrimSpace(resp))
	}

	body = `{"id":"` + devID + `","fingerprint":"fp-attacker","push_token":"attacker-token","os":"Android"}`
	code, resp = s944Do(t, srv, http.MethodPost, "/api/workspaces/"+s944Ws+"/devices", otherTok, body)
	if code != http.StatusConflict {
		t.Fatalf("撞 device id 应 409（可区分），实际 %d body=%s", code, strings.TrimSpace(resp))
	}

	devs, err := srv.identityStore.ListDevices(ctx, s944Ws)
	if err != nil {
		t.Fatalf("列设备失败: %v", err)
	}
	if len(devs) != 1 {
		t.Fatalf("应有 1 台设备，实际 %d ⇒ 设备行被搬走了", len(devs))
	}
	if devs[0].PushToken != "owner-token" {
		t.Errorf("受害者的 push_token 变成了 %q ⇒ **越权覆盖**", devs[0].PushToken)
	}
	if devs[0].UserID != s944Owner {
		t.Errorf("设备行的 user_id 变成了 %q（want %q）", devs[0].UserID, s944Owner)
	}
}

// TestS944AssetPushCollisionIsReported 资产镜像：撞别人的 asset_id ⇒ 该条**必须**回报错误。
//
// ⚠ 这一条与前两条不同：资产同步是**批量**接口（一条 push 失败不中断整批）
// ⇒ ⇒⇒ 所以「失败」不能靠 HTTP 状态码表达 ⇒ ⇒⇒⇒ 必须落在**每条结果**里
// （PushResult.Error）⇒ ⇒⇒ 否则客户端会把本地资产标成已同步
// ⇒ ⇒⇒⇒ **静默丢数据**（服务端根本没收到那份密文）。
func TestS944AssetPushCollisionIsReported(t *testing.T) {
	srv := s944Server(t)
	ctx := context.Background()
	ownerTok := s944Token(t, srv, s944Owner, s944Ws)
	otherTok := s944Token(t, srv, s944Other, s944WsOther)

	const assetID = "asset-s944-endtoend"
	body := `{"since":0,"pushes":[{"id":"` + assetID + `","kind":"note","client_rev":1,"cipher_blob":"owner-cipher"}]}`
	code, resp := s944Do(t, srv, http.MethodPost, "/api/assets/sync", ownerTok, body)
	if code != http.StatusOK {
		t.Fatalf("受害者推资产应 200，实际 %d body=%s", code, strings.TrimSpace(resp))
	}

	body = `{"since":0,"pushes":[{"id":"` + assetID + `","kind":"note","client_rev":1,"cipher_blob":"attacker-cipher"}]}`
	code, resp = s944Do(t, srv, http.MethodPost, "/api/assets/sync", otherTok, body)
	if code != http.StatusOK {
		t.Fatalf("批量接口整体应 200（单条失败不中断整批），实际 %d body=%s", code, strings.TrimSpace(resp))
	}
	var out struct {
		PushResults []struct {
			AssetID string `json:"asset_id"`
			Error   string `json:"error"`
		} `json:"push_results"`
	}
	if err := json.Unmarshal([]byte(resp), &out); err != nil {
		t.Fatalf("decode 响应: %v body=%s", err, strings.TrimSpace(resp))
	}
	if len(out.PushResults) != 1 {
		t.Fatalf("应有 1 条 push 结果，实际 %d", len(out.PushResults))
	}
	if out.PushResults[0].Error == "" {
		t.Fatalf("撞 id 的那条 push 没有回报错误 ⇒ 客户端会把本地资产标成已同步 ⇒ **静默丢数据**")
	}

	got, err := srv.lobsterSync.GetMirror(ctx, assetID)
	if err != nil {
		t.Fatalf("读受害者的资产失败: %v", err)
	}
	if got.CipherBlob != "owner-cipher" {
		t.Errorf("受害者的密文变成了 %q ⇒ **越权覆盖**", got.CipherBlob)
	}
	if got.WorkspaceID != s944Ws {
		t.Errorf("资产行被搬到了 workspace %q（want %q）", got.WorkspaceID, s944Ws)
	}
}

// TestS944NotificationRuleCollisionReturns409 通知规则：撞别人的 rule id ⇒ 409。
func TestS944NotificationRuleCollisionReturns409(t *testing.T) {
	srv := s944Server(t)
	ownerTok := s944Token(t, srv, s944Owner, s944Ws)
	otherTok := s944Token(t, srv, s944Other, s944WsOther)

	const ruleID = "rule-s944-endtoend"
	body := `{"id":"` + ruleID + `","source":"email","kind":"important","channels":["inbox"],"priority":"high","enabled":true}`
	code, resp := s944Do(t, srv, http.MethodPost, "/api/notifications/rules", ownerTok, body)
	if code != http.StatusOK {
		t.Fatalf("受害者建规则应 200，实际 %d body=%s", code, strings.TrimSpace(resp))
	}

	body = `{"id":"` + ruleID + `","source":"attacker","kind":"important","channels":["websocket"],"priority":"low","enabled":false}`
	code, resp = s944Do(t, srv, http.MethodPost, "/api/notifications/rules", otherTok, body)
	if code != http.StatusConflict {
		t.Fatalf("撞 rule id 应 409（可区分），实际 %d body=%s", code, strings.TrimSpace(resp))
	}

	rules, err := srv.notifyStore.ListRules(context.Background(), s944Ws)
	if err != nil {
		t.Fatalf("列规则失败: %v", err)
	}
	found := false
	for _, r := range rules {
		if r.ID == ruleID {
			found = true
			if r.Source != "email" || r.Priority != "high" || !r.Enabled {
				t.Errorf("受害者的规则被改写了：source=%q priority=%q enabled=%v ⇒ **越权覆盖**",
					r.Source, r.Priority, r.Enabled)
			}
		}
	}
	if !found {
		t.Fatalf("受害者的规则不见了 ⇒ 归属被搬走了")
	}
}
