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

// ─────────────────────────────────────────────────────────────────────────────
// 空 key 的 active 行：既不能造出来，造出来了也必须能被补上。
//
// 前两支守卫只管"解不开的行"，而 api_key_encrypted='' 是**另一个**状态：
// decryptString 对空串直接返回 ("", nil)，LoadConfig 既不报错、existing 又非 nil，
// 于是 EnsureLLMGatewayDefaults 直接 continue。结果是**永久**不可用——
// 哪怕运维后来把 POCKET_LLM_GATEWAY_API_KEY 配好，也没有任何启动路径会碰它。
//
// 2026-10-02 真机实测坐实：21:08 那次启动明确设了
// POCKET_LLM_GATEWAY_API_KEY，日志对 workspace=default 打的仍是
// `loaded config from DB`，而 llm_gateway_configs id=1 至今 is_active=true
// 且 api_key_encrypted=''。该 workspace 的 chat/embed 全程硬 503，App 内无提示。
//
// 判别点：HTTP 入口 POST /api/llm-gateway/config 本身就拒绝把首份配置存成空 key
//（"apiKey required for first configuration"），启动播种不该绕过它造同样的状态。
// ─────────────────────────────────────────────────────────────────────────────

// 首份播种：env 无 key 时不许写出一条永久不可用的 active 行。
func TestEnsureLLMGatewayDefaults_FirstSeedSkipsWhenEnvEmpty(t *testing.T) {
	t.Setenv("POCKET_LLM_GATEWAY_API_KEY", "")

	// (nil, nil)：该 workspace 还没有 active 行 —— 走首份播种分支
	store := &fakeGWStore{loaded: nil}
	s := &Server{llmGWStore: store}

	s.EnsureLLMGatewayDefaults("ws-first-seed")

	if len(store.saveCalls) != 0 {
		t.Fatalf("SaveConfig called %d time(s) while seeding a brand-new workspace with no env key; "+
			"会写出一条 api_key_encrypted='' 的 active 行，而它永远不会被修复（"+
			"decryptString 对空串不报错 ⇒ 自愈分支不触发；existing != nil ⇒ 直接 continue）",
			len(store.saveCalls))
	}
}

// 首份播种在 env 有 key 时**必须**照常播种，否则新 workspace 会永远配不上网关。
func TestEnsureLLMGatewayDefaults_FirstSeedStillRunsWhenEnvHasKey(t *testing.T) {
	t.Setenv("POCKET_LLM_GATEWAY_API_KEY", "sk-first-seed-key")

	store := &fakeGWStore{loaded: nil}
	s := &Server{llmGWStore: store}

	s.EnsureLLMGatewayDefaults("ws-first-seed")

	if len(store.saveCalls) != 1 {
		t.Fatalf("SaveConfig called %d time(s), want 1 (a brand-new workspace must still be seeded)", len(store.saveCalls))
	}
	if got := store.saveCalls[0].APIKey; got != "sk-first-seed-key" {
		t.Errorf("seeded APIKey = %q, want the env value", got)
	}
}

// 修复路径：已有 active 行但 key 是空的，且 env 现在有 key → 只补 key。
func TestEnsureLLMGatewayDefaults_BackfillsEmptyKeyFromEnv(t *testing.T) {
	t.Setenv("POCKET_LLM_GATEWAY_API_KEY", "sk-backfilled-from-env")

	store := &fakeGWStore{loaded: &llmGatewayState{
		BaseURL:         "https://llm.kxpms.cn/v1",
		APIKey:          "", // <- 就是这条永久死路
		Models:          []string{"m-keep-1", "m-keep-2"},
		PreferredModels: []string{"m-keep-2"},
		Format:          "anthropic-messages",
	}}
	s := &Server{llmGWStore: store}

	s.EnsureLLMGatewayDefaults("ws-empty-key")

	if len(store.saveCalls) != 1 {
		t.Fatalf("SaveConfig called %d time(s), want 1 (an active row with an empty key is otherwise永久不可修复)", len(store.saveCalls))
	}
	got := store.saveCalls[0]
	if got.APIKey != "sk-backfilled-from-env" {
		t.Errorf("backfilled APIKey = %q, want the env value", got.APIKey)
	}
	// 只改 key：用户自己设过的东西一律不许被 def 覆盖
	if got.BaseURL != "https://llm.kxpms.cn/v1" {
		t.Errorf("backfill changed BaseURL to %q; 只有 key 该被改", got.BaseURL)
	}
	if len(got.Models) != 2 || len(got.PreferredModels) != 1 {
		t.Errorf("backfill dropped the model lists: models=%v preferred=%v", got.Models, got.PreferredModels)
	}
	if got.Format != "anthropic-messages" {
		t.Errorf("backfill changed Format to %q; 只有 key 该被改", got.Format)
	}
}

// 反向：env 也没 key 时不能为了"修"而反复重写同一行（否则每次启动都换一行，
// 就是本轮查实的那条 churn）。此时应当保持静默跳过。
func TestEnsureLLMGatewayDefaults_EmptyKeyNotRewrittenWhenEnvAlsoEmpty(t *testing.T) {
	t.Setenv("POCKET_LLM_GATEWAY_API_KEY", "")

	store := &fakeGWStore{loaded: &llmGatewayState{BaseURL: "https://llm.kxpms.cn/v1", APIKey: ""}}
	s := &Server{llmGWStore: store}

	s.EnsureLLMGatewayDefaults("ws-empty-both")

	if len(store.saveCalls) != 0 {
		t.Fatalf("SaveConfig called %d time(s) with nothing to backfill; "+
			"每次启动换一条新行正是 llm_gateway_configs 无限累积的成因", len(store.saveCalls))
	}
}

// 反向：已有可用 key 的行绝不能被这条新分支碰到（幂等性不能被破坏）。
func TestEnsureLLMGatewayDefaults_BackfillDoesNotTouchHealthyRow(t *testing.T) {
	t.Setenv("POCKET_LLM_GATEWAY_API_KEY", "sk-env-key")

	store := &fakeGWStore{loaded: &llmGatewayState{BaseURL: "https://llm.kxpms.cn/v1", APIKey: "sk-tenant-real-key"}}
	s := &Server{llmGWStore: store}

	s.EnsureLLMGatewayDefaults("ws-healthy")

	if len(store.saveCalls) != 0 {
		t.Fatalf("SaveConfig called %d time(s) for a healthy active row; want 0 (must stay idempotent)", len(store.saveCalls))
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
