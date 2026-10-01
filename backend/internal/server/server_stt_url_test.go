package server

// STT 外部服务地址的 SSRF 校验（2026-10-01）。
//
// 缺陷背景：STT 的 externalBaseURL 原本调用的是 validateGatewayURL，而那个
// 函数受 POCKET_LLM_GATEWAY_ALLOW_PRIVATE 影响。于是任何为了「连上内网 LLM
// 网关」而打开网关开关的部署，**STT 的 SSRF 防护被静默关掉**：用户可以把
// 地址填成 http://127.0.0.1:<本机端口>/v1，后端就把录音 POST 过去。
//
// 这次是黑盒重跑时撞出来的：verify-stt.ps1 的「危险地址应被拒绝」用例在
// 设了 POCKET_LLM_GATEWAY_ALLOW_PRIVATE 的环境里从 PASS 变 FAIL。之前记的
// 19/0 是真的，但只在**没设该开关**的默认环境下成立 —— 断言是环境相关的。
//
// 修法：独立开关 POCKET_STT_ALLOW_PRIVATE。语义是「允许指向自建（内网）ASR」，
// 与网关那个「连内网网关」不是一回事，默认严格。

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestSTTURLRejectsLoopbackEvenWhenGatewaySwitchOn 是这次缺陷的正面用例。
// 网关开关必须**不**影响 STT 地址校验。
func TestSTTURLRejectsLoopbackEvenWhenGatewaySwitchOn(t *testing.T) {
	t.Setenv("POCKET_LLM_GATEWAY_ALLOW_PRIVATE", "true")
	t.Setenv("POCKET_STT_ALLOW_PRIVATE", "")

	for _, u := range []string{
		"http://127.0.0.1:8080/v1",
		"http://localhost:8080/v1",
		"http://10.0.0.5:9000/v1",
	} {
		if err := validateSTTOutboundURL(u); err == nil {
			t.Errorf("网关开关打开时 STT 地址仍必须拒绝私网/loopback：%s", u)
		}
	}
	// 对照：同一个开关下，网关地址本身是允许私网的 —— 证明开关确实生效，
	// 上面三条转红不是因为开关没读到。
	t.Setenv("POCKET_LLM_GATEWAY_ALLOW_PRIVATE", "true")
	if err := validateGatewayURL("http://127.0.0.1:8080/v1"); err != nil {
		t.Errorf("对照：网关地址在开关打开时应当放行，实际被拒：%v", err)
	}
}

// TestSTTURLDedicatedSwitchAllowsSelfHosted 确认自建 ASR 仍然可用，
// 但只认自己的开关。
func TestSTTURLDedicatedSwitchAllowsSelfHosted(t *testing.T) {
	t.Setenv("POCKET_LLM_GATEWAY_ALLOW_PRIVATE", "")
	t.Setenv("POCKET_STT_ALLOW_PRIVATE", "true")

	for _, u := range []string{
		"http://127.0.0.1:9000/v1",
		"http://192.168.1.50:9000/v1",
	} {
		if err := validateSTTOutboundURL(u); err != nil {
			t.Errorf("POCKET_STT_ALLOW_PRIVATE 打开后应放行自建 ASR 地址 %s：%v", u, err)
		}
	}
	// 云元数据端点无论开关与否都必须拒绝。
	for _, u := range []string{
		"http://169.254.169.254/v1",
		"http://metadata.google.internal/v1",
	} {
		if err := validateSTTOutboundURL(u); err == nil {
			t.Errorf("云元数据端点必须始终被拒：%s", u)
		}
	}
}

// TestSTTURLDefaultRejectsPrivate 钉住默认行为。
func TestSTTURLDefaultRejectsPrivate(t *testing.T) {
	t.Setenv("POCKET_LLM_GATEWAY_ALLOW_PRIVATE", "")
	t.Setenv("POCKET_STT_ALLOW_PRIVATE", "")

	if err := validateSTTOutboundURL("https://api.openai.com/v1"); err != nil {
		t.Errorf("公网 https 地址必须放行：%v", err)
	}
	if err := validateSTTOutboundURL("http://127.0.0.1:8080/v1"); err == nil {
		t.Error("默认必须拒绝 loopback")
	}
	// 错误信息要指向正确的开关名，不能把用户导向网关那个。
	err := validateSTTOutboundURL("http://127.0.0.1:8080/v1")
	if err == nil || !strings.Contains(err.Error(), "POCKET_STT_ALLOW_PRIVATE") {
		t.Errorf("错误信息应提示 POCKET_STT_ALLOW_PRIVATE，实际：%v", err)
	}
	if err != nil && strings.Contains(err.Error(), "POCKET_LLM_GATEWAY_ALLOW_PRIVATE") {
		t.Errorf("错误信息不应把用户导向网关开关（那是另一个语义），实际：%v", err)
	}
}

// TestSTTOutboundDialerHonorsSameSwitchAsValidation 守住「校验层与拨号层认同一个开关」。
//
// 这是上一个缺陷留下的**同一个洞的第二层**：POCKET_STT_ALLOW_PRIVATE 只被
// validateSTTOutboundURL 认，而运行时的出站 client 走 gatewayHTTPClient，
// 只认 POCKET_LLM_GATEWAY_ALLOW_PRIVATE。后果是设置页能存进去、
// 一转写就报 "resolved address is not allowed" —— 自建 ASR 彻底不可用。
//
// 上面几个用例全是**函数级**的，挡不住「调用点接错了 client」。
// 这个用例真起一个 loopback 服务并真的去拨它。
func TestSTTOutboundDialerHonorsSameSwitchAsValidation(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"text":"ok"}`))
	}))
	defer upstream.Close()
	if !strings.Contains(upstream.URL, "127.0.0.1") {
		t.Skipf("上游不在 loopback 上（%s），本用例失去意义", upstream.URL)
	}

	cases := []struct {
		name       string
		sttSwitch  string
		gwSwitch   string
		wantDialOK bool
	}{
		{"默认：两个开关都关", "", "", false},
		{"只有 STT 开关开", "true", "", true},
		// 关键一行：网关开关**不**放行 STT 拨号。
		// 曾经在这里用「两个开关的并集」，结果配置层拒绝、拨号层放行 ——
		// 拨号层变成绕过设置页校验的后门，而网关开关在「连内网网关」这种
		// 极常见部署里本就会开，等于把 STT 的 SSRF 防护在最后一层静默关掉。
		{"只有网关开关开：STT 拨号仍须被拒", "", "true", false},
		{"两个都开", "true", "true", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("POCKET_STT_ALLOW_PRIVATE", c.sttSwitch)
			t.Setenv("POCKET_LLM_GATEWAY_ALLOW_PRIVATE", c.gwSwitch)

			// 1) 配置校验层的判定
			validateErr := validateSTTOutboundURL(upstream.URL + "/v1")

			// 2) 实际拨号层的判定
			//
			// 必须走 **s.sttClient() 这个调用点**，不能直接调
			// sttOutboundHTTPClient()：本缺陷的性质就是「函数本身是对的、
			// 调用点接错了 client」，只测函数的话负控**不会转红**
			// （2026-10-01 实测踩过：注入 gatewayHTTPClient 后用例依然全绿）。
			// 这里用一个没有注入 client 的裸 Server，走生产路径。
			srv := &Server{}
			dialErr := func() error {
				resp, err := srv.sttClient(5 * time.Second).Get(upstream.URL + "/v1")
				if err != nil {
					return err
				}
				defer resp.Body.Close()
				_, _ = io.Copy(io.Discard, resp.Body)
				return nil
			}()

			// 核心不变量：两层判定必须一致。
			// 不一致 = 用户能保存一个永远连不上的地址。
			if (validateErr == nil) != (dialErr == nil) {
				t.Fatalf("校验层与拨号层判定不一致：validate=%v  dial=%v\n"+
					"这意味着用户能保存一个转写时必然失败的地址（自建 ASR 不可用）",
					validateErr, dialErr)
			}
			if (dialErr == nil) != c.wantDialOK {
				t.Errorf("拨号结果 = %v（dialErr=%v），期望可达=%v", dialErr == nil, dialErr, c.wantDialOK)
			}
		})
	}
}

// TestSTTOutboundDialerAlwaysBlocksCloudMetadata 确认放行私网时元数据端点仍被拒。
func TestSTTOutboundDialerAlwaysBlocksCloudMetadata(t *testing.T) {
	t.Setenv("POCKET_STT_ALLOW_PRIVATE", "true")
	t.Setenv("POCKET_LLM_GATEWAY_ALLOW_PRIVATE", "true")

	c := sttOutboundHTTPClient(2 * time.Second)
	for _, host := range []string{"169.254.169.254", "[fd00:ec2::254]"} {
		if _, err := c.Get("http://" + host + "/latest/meta-data/"); err == nil {
			t.Errorf("云元数据端点 %s 必须始终被拒", host)
		} else if !strings.Contains(err.Error(), "not allowed") {
			t.Errorf("元数据端点 %s 应被 SSRF 守卫拒绝，实际错误：%v", host, err)
		}
	}
}

// TestSTTConfigRejectsLoopbackUnderGatewaySwitch 是端到端那层：
// 直接打 PUT /api/stt/config，确认「网关开关打开时用户也存不进 loopback 地址」。
// 函数级用例挡不住「调用点接错函数」—— 这次的缺陷正是函数本身没错、调用点错了。
func TestSTTConfigRejectsLoopbackUnderGatewaySwitch(t *testing.T) {
	srv, store := sttTestServer(t, "sk-gateway-test")
	store.Put(seedSTTSetting("shared-user", "ws-a", `{"channel":"auto"}`, ""))

	t.Setenv("POCKET_LLM_GATEWAY_ALLOW_PRIVATE", "true")
	t.Setenv("POCKET_STT_ALLOW_PRIVATE", "")
	h := srv.Handler()
	token := wsAToken(t)

	body := `{"channel":"external","externalBaseURL":"http://127.0.0.1:9/v1",` +
		`"externalModel":"gpt-4o-mini-transcribe","externalApiKey":"sk-placeholder"}`
	req := httptest.NewRequest(http.MethodPut, "/api/stt/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code == http.StatusOK {
		t.Fatalf("网关开关打开时也不得接受 loopback 外部地址，实际 %d：%s",
			rr.Code, rr.Body.String())
	}
}
