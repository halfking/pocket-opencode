package server

import (
	"context"
	"errors"
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
