package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/usersetting"
)

// RedClaw 门户同步策略端点（2026-11-01，本仓内实现）。
//
// 背景：前端把「存储与同步策略」（syncPolicyContract.ts 的 SyncPolicy）统一存
// RedClaw 后台（frontend/src/config/redclaw-portal.ts），portal base 的兜底链
// 最后一跳就是 pocketd 自身（resolveRuntimeApiBase）。此前 pocketd 没有这对
// 路由 ⇒ GET 404 ⇒ 前端按「无法确认策略」降级：system_default=local、
// portal 模块（email/rss/notes/meetings）一律 local-only —— 本地 rig（没有
// RedClaw 部署）上收件箱永远读不到服务端邮件，email-browse 真机流红在
// 「列表没有相对时间」（4c308e2e 实测）。
//
// 这里按前端既有契约补齐：
//
//	GET /api/v1/portal/sync-policy  → {system_default, modules, updatedAt}
//	PUT /api/v1/portal/sync-policy  ← {modules:{email|rss|notes|meetings: cloud|local}}
//
// 持久化复用 usersetting（namespace=sync_policy, id=default，LWW 归属已有）；
// userSettings 未注入（remote-only/单测）时 GET 回默认值、PUT 接受但不落库，
// 两者都不 5xx —— 策略是增强能力，前端对 404/失败本来就降级。
//
// 归属：userID/workspaceID 一律取 JWT（与 /api/notes 同一条不变量），策略是
// 每用户每工作区一份。

type syncPolicyState struct {
	SystemDefault string            `json:"system_default"`
	Modules       map[string]string `json:"modules"`
	UpdatedAt     int64             `json:"updatedAt"`
}

var portalSyncModules = []string{"email", "rss", "notes", "meetings"}

func normalizeSyncMode(v string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "cloud":
		return "cloud", true
	case "local":
		return "local", true
	}
	return "", false
}

func (s *Server) defaultSyncPolicyState() syncPolicyState {
	// system_default 默认 cloud（与前端 defaultSyncPolicy 一致）；
	// 部署方可用 POCKET_SYNC_SYSTEM_DEFAULT=local 一键回到保守态。
	def := "cloud"
	if v, ok := normalizeSyncMode(envOr("POCKET_SYNC_SYSTEM_DEFAULT", "")); ok {
		def = v
	}
	modules := make(map[string]string, len(portalSyncModules))
	for _, m := range portalSyncModules {
		modules[m] = def
	}
	return syncPolicyState{SystemDefault: def, Modules: modules, UpdatedAt: 0}
}

func (s *Server) loadSyncPolicyState(userID, workspaceID string) syncPolicyState {
	def := s.defaultSyncPolicyState()
	if s.userSettings == nil || strings.TrimSpace(userID) == "" {
		return def
	}
	rec, err := s.userSettings.Get(userID, workspaceID, "sync_policy", "default")
	if err != nil || rec == nil || len(rec.Payload) == 0 {
		return def
	}
	var saved struct {
		SystemDefault string            `json:"system_default"`
		Modules       map[string]string `json:"modules"`
	}
	if err := json.Unmarshal(rec.Payload, &saved); err != nil {
		return def
	}
	if v, ok := normalizeSyncMode(saved.SystemDefault); ok {
		def.SystemDefault = v
	}
	for _, m := range portalSyncModules {
		if v, ok := normalizeSyncMode(saved.Modules[m]); ok {
			def.Modules[m] = v
		}
	}
	def.UpdatedAt = rec.UpdatedAt
	return def
}

func (s *Server) handlePortalSyncPolicy(w http.ResponseWriter, r *http.Request) {
	uid := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)
	if wsID == "" {
		wsID = "default"
	}
	switch r.Method {
	case http.MethodGet:
		st := s.loadSyncPolicyState(uid, wsID)
		writeJSON(w, http.StatusOK, st)
	case http.MethodPut:
		var req struct {
			Modules map[string]string `json:"modules"`
		}
		if err := decodeJSONBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid body")
			return
		}
		st := s.loadSyncPolicyState(uid, wsID)
		changed := false
		for m, v := range req.Modules {
			known := false
			for _, k := range portalSyncModules {
				if k == m {
					known = true
					break
				}
			}
			if !known {
				continue // 未知模块键宽容忽略（与前端 normalizeSyncPolicy 同口径）
			}
			nv, ok := normalizeSyncMode(v)
			if !ok {
				writeError(w, http.StatusBadRequest, "invalid mode for "+m)
				return
			}
			if st.Modules[m] != nv {
				st.Modules[m] = nv
				changed = true
			}
		}
		if changed && s.userSettings != nil && strings.TrimSpace(uid) != "" {
			payload, _ := json.Marshal(map[string]any{
				"system_default": st.SystemDefault,
				"modules":        st.Modules,
			})
			if _, err := s.userSettings.Put(usersetting.Record{
				UserID: uid, WorkspaceID: wsID, Namespace: "sync_policy", ID: "default",
				Payload: payload, UpdatedAt: time.Now().UnixMilli(),
			}); err != nil {
				writeError(w, http.StatusInternalServerError, "persist sync policy: "+err.Error())
				return
			}
			st.UpdatedAt = time.Now().UnixMilli()
		}
		writeJSON(w, http.StatusOK, st)
	default:
		writeError(w, http.StatusMethodNotAllowed, "GET/PUT only")
	}
}
