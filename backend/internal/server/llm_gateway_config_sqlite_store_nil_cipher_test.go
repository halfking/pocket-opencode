package server

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// SQLite 兜底 store 的 typed-nil cipher 防线必须有直接用例钉死。
//
// 病根：main.go 把 NewGatewayCipher(cfg.JWTSecret) 的返回值直接装进
// llmGatewayCipher 接口——secret 为空时它是 nil *gatewayKeyCipher，接口判空
// `cipher == nil` 挡不住（typed-nil）。旧代码构造"成功"，第一次 encrypt/decrypt
// 才在 nil receiver 上解引用 g.key panic：无 PG 且 POCKET_JWT_SECRET 漏配的
// 实例，启动自愈（EnsureLLMGatewayDefaults → SaveConfig）或用户在设置页保存
// 配置即触发。这与 PG store 在 3ea18409 修掉的是同一类洞（见
// TestNewLLMGatewayStore_RejectsTypedNilCipher）。

func TestNewLLMGatewaySQLiteConfigStore_RejectsTypedNilCipher(t *testing.T) {
	t.Parallel()
	// 真实形态：NewGatewayCipher("") 返回 nil 指针，装进接口后必须非 nil，
	// 否则本用例复现不了被绕过的那个判空。
	var c llmGatewayCipher = NewGatewayCipher("")
	if c == nil {
		t.Fatal("precondition: 装着 nil 指针的 interface 必须非 nil，" +
			"否则本用例复现不了 typed-nil 绕过 `cipher == nil` 判空的路径")
	}
	_, err := NewLLMGatewaySQLiteConfigStore(filepath.Join(t.TempDir(), "gw.sqlite"), c)
	if err == nil {
		t.Fatal("构造器接受了 typed-nil cipher；" +
			"第一次 encrypt/decrypt 就会在 nil receiver 上解引用 g.key panic")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("err = %v，期望提示未配置，让运维能定位到 JWT secret 缺失", err)
	}
}

func TestNewLLMGatewaySQLiteConfigStore_RejectsNilCipher(t *testing.T) {
	t.Parallel()
	// 契约与 PG store 对齐：没有 cipher 就构造失败，而不是把"未配置"留到
	// 第一次落库时才暴露（那时报错点离真因隔了好几跳）。
	if _, err := NewLLMGatewaySQLiteConfigStore(filepath.Join(t.TempDir(), "gw.sqlite"), nil); err == nil {
		t.Fatal("构造器接受了 nil cipher；与 NewLLMGatewayStore 的契约不一致")
	}
}

// 兜底纵深：就算绕过构造器直构 store/直接调 helper，typed-nil 也必须返回
// 显式 error 而不是 panic 或 ("", nil) 伪装成功。
func TestGatewaySQLiteHelpers_TypedNilCipherIsErrorNotPanic(t *testing.T) {
	t.Parallel()
	var c llmGatewayCipher = NewGatewayCipher("")
	if _, err := encryptString(c, "sk-should-not-encrypt"); err == nil {
		t.Fatal("typed-nil cipher 加密返回了 nil error；" +
			"要么 panic 要么把明文/空串当成功传播，两条路都是坏的")
	}
	if _, err := decryptString(c, "Y2lwaGVydGV4dA=="); err == nil {
		t.Fatal("typed-nil cipher 解密返回了 nil error；" +
			"调用方的「未配置」降级分支（err != nil）会因此永远不触发")
	}
}

// 对照组：真 cipher 走完整落库→读回，证明上面的失败判定不是夹具坏了，
// 也锁住 SaveConfig/LoadConfig 的 key 加解密对称性。
func TestGatewaySQLiteStore_RoundTripWithRealCipher(t *testing.T) {
	t.Parallel()
	store, err := NewLLMGatewaySQLiteConfigStore(
		filepath.Join(t.TempDir(), "gw.sqlite"), NewGatewayCipher("unit-test-secret"))
	if err != nil {
		t.Fatalf("构造 store: %v", err)
	}
	defer store.Close()

	st := llmGatewayState{
		BaseURL:         "https://llm.kxpms.cn/v1",
		APIKey:          "sk-roundtrip-check",
		Models:          []string{"m-1", "m-2"},
		PreferredModels: []string{"m-1"},
		Format:          "openai-chat",
	}
	if err := store.SaveConfig(context.Background(), "default", st); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	got, err := store.LoadConfig(context.Background(), "default")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got == nil {
		t.Fatal("LoadConfig 返回 (nil, nil)，但刚写入过一行")
	}
	if got.APIKey != "sk-roundtrip-check" {
		t.Errorf("round-trip APIKey = %q，想要还原明文（加密落库→解密读回必须对称）", got.APIKey)
	}
	if got.BaseURL != st.BaseURL || got.Format != st.Format {
		t.Errorf("round-trip 丢字段: baseURL=%q format=%q", got.BaseURL, got.Format)
	}
	if len(got.Models) != 2 || len(got.PreferredModels) != 1 {
		t.Errorf("round-trip 丢模型列表: models=%v preferred=%v", got.Models, got.PreferredModels)
	}
}
