package server

import "testing"

func TestObsoleteLocalGatewayURL(t *testing.T) {
	t.Parallel()
	if !obsoleteLocalGatewayURL("http://llm-gateway-local-8782:8782") {
		t.Fatal("expected old local docker URL to be obsolete")
	}
	// 2026-09-21: llm.kxpms.cn 切到 llmgo.kxpms.cn，老域名行也算 obsolete
	if !obsoleteLocalGatewayURL("https://llm.kxpms.cn/v1") {
		t.Fatal("legacy llm.kxpms.cn URL must be treated as obsolete")
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
	// 2026-09-21: 老域名行也算 obsolete
	if !settingHasObsoleteGateway([]byte(`{"baseURL":"https://llm.kxpms.cn/v1"}`)) {
		t.Fatal("legacy kaixuan payload must be obsolete after llmgo switch")
	}
	if settingHasObsoleteGateway([]byte(`{`)) {
		t.Fatal("invalid json is not obsolete")
	}
}
