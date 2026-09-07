package server

import "testing"

func TestObsoleteLocalGatewayURL(t *testing.T) {
	t.Parallel()
	if !obsoleteLocalGatewayURL("http://llm-gateway-local-8782:8782") {
		t.Fatal("expected old local docker URL to be obsolete")
	}
	if obsoleteLocalGatewayURL("https://llm.kxpms.cn/v1") {
		t.Fatal("kaixuan URL must not be treated as obsolete")
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
	if settingHasObsoleteGateway([]byte(`{"baseURL":"https://llm.kxpms.cn/v1"}`)) {
		t.Fatal("kaixuan payload must not be obsolete")
	}
	if settingHasObsoleteGateway([]byte(`{`)) {
		t.Fatal("invalid json is not obsolete")
	}
}
