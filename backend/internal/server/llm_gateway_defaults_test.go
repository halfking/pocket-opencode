package server

import "testing"

func TestObsoleteLocalGatewayURL(t *testing.T) {
	t.Parallel()
	if !obsoleteLocalGatewayURL("http://llm-gateway-local-8782:8782") {
		t.Fatal("expected old local docker URL to be obsolete")
	}
	// 2026-09-30: llm.kxpms.cn 是当前正式默认网关，不再算 obsolete。
	// 回归护栏：2026-09-21 曾把它当老域名强制改写到 llmgo，
	// 结果设置页填 llm.kxpms.cn 会被悄悄打回，配置形同虚设。
	if obsoleteLocalGatewayURL("https://llm.kxpms.cn/v1") {
		t.Fatal("llm.kxpms.cn is the current default gateway and must not be obsolete")
	}
	if obsoleteLocalGatewayURL("https://llmgo.kxpms.cn/v1") {
		t.Fatal("llmgo.kxpms.cn URL must not be obsolete")
	}
	if obsoleteLocalGatewayURL("") {
		t.Fatal("empty URL is not obsolete")
	}
}

func TestSettingHasObsoleteGateway(t *testing.T) {
	t.Parallel()
	if !settingHasObsoleteGateway([]byte(`{"baseURL":"http://llm-gateway-local-8782:8782"}`)) {
		t.Fatal("expected obsolete payload")
	}
	// 2026-09-30: llm.kxpms.cn 是合法配置，必须原样保留
	if settingHasObsoleteGateway([]byte(`{"baseURL":"https://llm.kxpms.cn/v1"}`)) {
		t.Fatal("current default gateway payload must not be treated as obsolete")
	}
	if settingHasObsoleteGateway([]byte(`{`)) {
		t.Fatal("invalid json is not obsolete")
	}
}
