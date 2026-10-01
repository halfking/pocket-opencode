package server

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// EnsureLLMGatewayDefaults 的自愈分支绝不能变成"毁配置"。
//
// 背景：`LoadConfig` 对"密文解不开"返回 error，这条分支会用
// `defaultLLMGatewayState()` 覆写。而该 default 的 APIKey 只来自
// `POCKET_LLM_GATEWAY_API_KEY`，**没有内置默认值**——env 没配时就是空串。
//
// SaveConfig 的语义是：先把该 workspace 全部行 `is_active = false`，
// 再插一条新的 active 行。于是 env 也为空时，一次"自愈"会把
// 「只是暂时解不开」的一行，连同它本来可用的旧 active 行一起废掉，
// 换成一条**永久没有 key** 的行——配置从"解不开但还在"变成"永久丢失"。
//
// 正确行为：env 无 key 时只告警、不落库。宁可保持"读不出来"让用户去设置页
// 重新填 key，也不要静默销毁已有配置。

type fakeGWStore struct {
	loadErr   error
	loaded    *llmGatewayState
	saveCalls []llmGatewayState
}

func (f *fakeGWStore) SaveConfig(ctx context.Context, workspaceID string, st llmGatewayState) error {
	f.saveCalls = append(f.saveCalls, st)
	return nil
}

func (f *fakeGWStore) LoadConfig(ctx context.Context, workspaceID string) (*llmGatewayState, error) {
	if f.loadErr != nil {
		return nil, f.loadErr
	}
	return f.loaded, nil
}

func TestEnsureLLMGatewayDefaults_SelfHealDoesNotWipeKeyWhenEnvEmpty(t *testing.T) {
	t.Setenv("POCKET_LLM_GATEWAY_API_KEY", "")

	// 模拟"行存在但解不开"（如 JWT secret 轮换后 cipher 校验失败）
	store := &fakeGWStore{loadErr: errors.New("decrypt api key: cipher: message authentication failed")}
	s := &Server{llmGWStore: store}

	s.EnsureLLMGatewayDefaults("ws-selfheal")

	if len(store.saveCalls) != 0 {
		t.Fatalf("SaveConfig called %d time(s) with an empty env key; "+
			"这会把该 workspace 的 active 行置空并换上一条无 key 的行，配置不可逆地丢失",
			len(store.saveCalls))
	}
}

func TestEnsureLLMGatewayDefaults_SelfHealStillRunsWhenEnvHasKey(t *testing.T) {
	t.Setenv("POCKET_LLM_GATEWAY_API_KEY", "sk-test-selfheal-key")

	store := &fakeGWStore{loadErr: errors.New("decrypt api key: corrupted row")}
	s := &Server{llmGWStore: store}

	s.EnsureLLMGatewayDefaults("ws-selfheal")

	// env 有 key 时，自愈是**期望行为**，不能被上面的守卫误伤——
	// 否则就退回成"每次启动都解密失败并静默回退，配置再也救不回来"。
	if len(store.saveCalls) != 1 {
		t.Fatalf("SaveConfig called %d time(s), want 1 (self-heal must still work when env provides a key)", len(store.saveCalls))
	}
	if got := store.saveCalls[0].APIKey; got != "sk-test-selfheal-key" {
		t.Errorf("self-healed APIKey = %q, want the env value", got)
	}
}

func TestEnsureLLMGatewayDefaults_ExistingRowIsNeverOverwritten(t *testing.T) {
	// env 空 + 现有 active 行可正常读取 → 幂等跳过，一条都不该写。
	t.Setenv("POCKET_LLM_GATEWAY_API_KEY", "")

	store := &fakeGWStore{loaded: &llmGatewayState{BaseURL: "https://llm.kxpms.cn/v1", APIKey: "sk-existing"}}
	s := &Server{llmGWStore: store}

	s.EnsureLLMGatewayDefaults("ws-idem")

	if len(store.saveCalls) != 0 {
		t.Fatalf("SaveConfig called %d time(s) for a workspace that already has an active row; want 0 (idempotent)", len(store.saveCalls))
	}
}

// 遗留本机网关地址的迁移只该改 URL，不该换掉整份配置。
//
// 这条路径**不需要任何解密失败**：配置读得好好的（key 在手），只因为
// base_url 里带 llm-gateway-local-8782 就走迁移分支。原实现传的是 `def`
// 而不是读出来的 `existing`，而 def 的 APIKey 只来自 env 且无内置默认值——
// env 没配时就是空串，于是租户刚存好的 key 连同 models/preferred_models
// 一起被抹成一条无 key 的行。
func TestEnsureLLMGatewayDefaults_ObsoleteURLMigrationKeepsKey(t *testing.T) {
	t.Setenv("POCKET_LLM_GATEWAY_API_KEY", "")

	store := &fakeGWStore{loaded: &llmGatewayState{
		BaseURL:         "http://llm-gateway-local-8782/v1",
		APIKey:          "sk-tenant-real-key",
		Models:          []string{"m-keep-1", "m-keep-2"},
		PreferredModels: []string{"m-keep-1"},
	}}
	s := &Server{llmGWStore: store}

	s.EnsureLLMGatewayDefaults("ws-legacy")

	if len(store.saveCalls) != 1 {
		t.Fatalf("SaveConfig called %d time(s), want 1 (the obsolete URL still has to be migrated)", len(store.saveCalls))
	}
	got := store.saveCalls[0]
	if got.APIKey != "sk-tenant-real-key" {
		t.Errorf("migrated APIKey = %q, want the stored key; "+
			"用 def 覆盖会把租户的 key 换成空串，且不可逆", got.APIKey)
	}
	if strings.Contains(got.BaseURL, "llm-gateway-local-8782") {
		t.Errorf("migrated BaseURL = %q, still points at the obsolete local gateway", got.BaseURL)
	}
	if len(got.Models) != 2 || len(got.PreferredModels) != 1 {
		t.Errorf("migration dropped the model lists: models=%v preferred=%v", got.Models, got.PreferredModels)
	}
}

// LoadLLMGatewayFromDB 的自愈分支与 EnsureLLMGatewayDefaults 是同一个陷阱，
// 但危害更大：cmd/pocketd/main.go 启动时对**每个 workspace** 调它。
//
// 判定依据是 LoadConfig 的契约——没有 active 行时它返回 (nil, nil) 而**不报错**
// （见 llm_gateway_store.go），所以 err != nil 只可能意味着"有行但解不开"，
// 绝不可能是"新 workspace 首次 seed"。因此 env 无 key 时落库就是纯粹的破坏。
func TestLoadLLMGatewayFromDB_SelfHealDoesNotWipeKeyWhenEnvEmpty(t *testing.T) {
	t.Setenv("POCKET_LLM_GATEWAY_API_KEY", "")

	store := &fakeGWStore{loadErr: errors.New("decrypt api key: cipher: message authentication failed")}
	s := &Server{llmGWStore: store}

	s.LoadLLMGatewayFromDB("ws-load-guard")

	if len(store.saveCalls) != 0 {
		t.Fatalf("SaveConfig called %d time(s) with an empty env key; "+
			"这会把该 workspace 的 active 行置空并换上一条无 key 的行，配置不可逆地丢失",
			len(store.saveCalls))
	}
}

func TestLoadLLMGatewayFromDB_SelfHealStillRunsWhenEnvHasKey(t *testing.T) {
	t.Setenv("POCKET_LLM_GATEWAY_API_KEY", "sk-env-recovery-key")

	store := &fakeGWStore{loadErr: errors.New("decrypt api key: corrupted row")}
	s := &Server{llmGWStore: store}

	s.LoadLLMGatewayFromDB("ws-load-recover")

	// env 有 key 时自愈是期望行为：拿 env 的 key 把毒化行换掉，配置才救得回来。
	// 守卫不能把它一并禁掉，否则就退化成"每次启动都失败且永远不恢复"。
	if len(store.saveCalls) != 1 {
		t.Fatalf("SaveConfig called %d time(s), want 1 (self-heal must still work when env provides a key)", len(store.saveCalls))
	}
	if got := store.saveCalls[0].APIKey; got != "sk-env-recovery-key" {
		t.Errorf("self-healed APIKey = %q, want the env value", got)
	}
}

// 没有 active 行是 LoadConfig 的 (nil, nil) 情形，不是错误：此时不应有任何写入，
// 也不应因为 st == nil 而崩。env 无 key 时更不能凭空造一条无 key 的 active 行。
func TestLoadLLMGatewayFromDB_NoRowIsNotAnErrorAndWritesNothing(t *testing.T) {
	t.Setenv("POCKET_LLM_GATEWAY_API_KEY", "")

	store := &fakeGWStore{loaded: nil} // (nil, nil)：库里没有该 workspace 的行
	s := &Server{llmGWStore: store}

	s.LoadLLMGatewayFromDB("ws-fresh")

	if len(store.saveCalls) != 0 {
		t.Fatalf("SaveConfig called %d time(s) for a workspace with no stored row; want 0", len(store.saveCalls))
	}
}

// nilCipher 用来造一个"装着 nil 指针的 interface"：interface 变量本身非 nil，
// 但任何方法调用都会在 nil receiver 上崩。
type nilCipher struct{}

func (*nilCipher) EncryptString(string) (string, error) { return "", nil }
func (*nilCipher) DecryptString(string) (string, error) { return "", nil }

// NewLLMGatewayStore 必须挡住 typed-nil cipher。
//
// 调用方（cmd/pocketd/main.go）传的是 *email.Crypto 这个**具体指针类型**。
// 当 POCKET_EMAIL_MASTER_KEY 长度不对时 emailCrypto 为 nil，装进接口后
// `cipher == nil` 判断会放行、构造"成功"，直到第一次 SaveConfig 才 panic——
// 进程启动即崩，而不是报"master key 配错了"。2026-10-01 实测到的栈：
// email.(*Crypto).EncryptString(0x0, ...) <- encryptAPIKey <- SaveConfig。
func TestNewLLMGatewayStore_RejectsTypedNilCipher(t *testing.T) {
	var c apiKeyCipher = (*nilCipher)(nil)
	if c == nil {
		t.Fatal("precondition: 一个装着 nil 指针的 interface 必须非 nil，" +
			"否则本用例复现不了被绕过的那个检查")
	}
	if _, err := NewLLMGatewayStore(nil, c); err == nil {
		t.Fatal("NewLLMGatewayStore 接受了 typed-nil cipher；" +
			"nil 检查被绕过，第一次 encrypt 就会在 nil receiver 上 panic")
	}
}
