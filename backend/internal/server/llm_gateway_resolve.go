package server

import (
	"context"
	"encoding/json"
	"log"
	"strings"

	"github.com/halfking/pocket-opencode/backend/internal/opencode"
	"github.com/halfking/pocket-opencode/backend/internal/usersetting"
)

// canonicalGatewayURL 把本机 docker 旧默认（8782）改写成 kaixuan 网关。
// 对话/会话必须走设置或此默认，不能再打 llm-gateway-local-8782。
func canonicalGatewayURL(u string) string {
	u = strings.TrimSpace(u)
	if u == "" || obsoleteLocalGatewayURL(u) {
		return opencode.DefaultLLMGatewayBaseURL
	}
	return u
}

func rewriteObsoleteGateway(st llmGatewayState) llmGatewayState {
	st.BaseURL = canonicalGatewayURL(st.BaseURL)
	st.Format = normalizeGatewayFormat(st.Format)
	return st
}

func overlayGatewaySetting(st llmGatewayState, rec *usersetting.Record) llmGatewayState {
	if rec == nil {
		return st
	}
	var payload struct {
		BaseURL         string   `json:"baseURL"`
		Format          string   `json:"format"`
		Models          []string `json:"models"`
		PreferredModels []string `json:"preferredModels"`
	}
	if err := json.Unmarshal(rec.Payload, &payload); err != nil || strings.TrimSpace(payload.BaseURL) == "" {
		return st
	}
	st.BaseURL = payload.BaseURL
	st.Format = normalizeGatewayFormat(payload.Format)
	if payload.Models != nil {
		st.Models = append([]string(nil), payload.Models...)
	}
	if payload.PreferredModels != nil {
		st.PreferredModels = append([]string(nil), payload.PreferredModels...)
	}
	if rec.Secret != "" {
		st.APIKey = rec.Secret
	}
	if rec.UpdatedAt > 0 {
		st.UpdatedAt = rec.UpdatedAt
	}
	return st
}

func (s *Server) loadUserGatewaySetting(userID, workspaceID string) *usersetting.Record {
	if s == nil || s.userSettings == nil || strings.TrimSpace(userID) == "" {
		return nil
	}
	rec, err := s.userSettings.Get(userID, workspaceID, "llm_gateway", "default")
	if err != nil || rec == nil {
		return nil
	}
	return rec
}

// pickGatewayState 选出某工作区当前生效的网关状态。两种借用 default 工作区
// 快照的情况必须分开处理：
//   - 地址是废弃的本机 8782：只把地址换回默认网关，**保留本工作区自己的 key**。
//     早先这两件事混在一个条件里；而 2026-09-30 起 default 快照自带内置 key，
//     混着判会让「8782 + 自有 key」的工作区整个配置被 default 快照顶掉，
//     用户在设置页配的 key 被静默换成内置那把。
//   - 完全没有 key：这时才向 default 工作区借一整套（地址+key+模型）。
func (s *Server) pickGatewayState(workspaceID string) llmGatewayState {
	st := s.gatewaySnapshot(workspaceID)
	if workspaceID == "default" {
		return st
	}
	if obsoleteLocalGatewayURL(st.BaseURL) {
		st.BaseURL = opencode.DefaultLLMGatewayBaseURL
		return st
	}
	if st.APIKey == "" {
		if fb := s.gatewaySnapshot("default"); !obsoleteLocalGatewayURL(fb.BaseURL) && fb.APIKey != "" {
			return fb
		}
	}
	return st
}

func (s *Server) effectiveGatewayState(userID, workspaceID string) llmGatewayState {
	if workspaceID == "" {
		workspaceID = "default"
	}
	st := s.pickGatewayState(workspaceID)
	if rec := s.loadUserGatewaySetting(userID, workspaceID); rec != nil {
		st = overlayGatewaySetting(st, rec)
	}
	// 用户级设置可能把地址覆盖成废弃的 8782：同样只换地址、保留 key。
	if obsoleteLocalGatewayURL(st.BaseURL) {
		st.BaseURL = opencode.DefaultLLMGatewayBaseURL
		return st
	}
	// 仍然没有任何 key：这时才向 default 工作区借。
	if st.APIKey == "" && workspaceID != "default" {
		fb := s.pickGatewayState("default")
		if rec := s.loadUserGatewaySetting(userID, "default"); rec != nil {
			fb = overlayGatewaySetting(fb, rec)
		}
		if !obsoleteLocalGatewayURL(fb.BaseURL) && fb.APIKey != "" {
			st = fb
		}
	}
	return rewriteObsoleteGateway(st)
}

// ResolveGatewayForUser 给对话/模型目录用：用户设置优先于缓存/env，
// 且永不返回 llm-gateway-local-8782。
func (s *Server) ResolveGatewayForUser(userID, workspaceID string) GatewayConfig {
	st := s.effectiveGatewayState(userID, workspaceID)
	return GatewayConfig{
		BaseURL: st.BaseURL, APIKey: st.APIKey, Models: st.Models,
		Format: normalizeGatewayFormat(st.Format), PreferredModels: st.PreferredModels,
	}
}

func (s *Server) syncGatewayToInstance(ctx context.Context, apiBaseURL, userID, workspaceID string) {
	if s == nil || strings.TrimSpace(apiBaseURL) == "" {
		return
	}
	st := s.effectiveGatewayState(userID, workspaceID)
	if st.BaseURL == "" || st.APIKey == "" {
		return
	}
	if err := s.pushGatewayToInstance(ctx, apiBaseURL, st); err != nil {
		log.Printf("[llm-gateway] sync to instance failed (non-fatal): %v", err)
	}
}
