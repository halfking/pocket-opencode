package server

// server_lobster.go — S0-C Lobster Vault 加密镜像同步的 HTTP handler。
//
// 单端点 /api/assets/sync 同时承担 push（上传本地 dirty 改动）和 pull（拉取
// 其他设备的改动），避免来回 RTT。请求体：
//
//   {
//     "since": 0,                     // 客户端已知的最高 server_rev（pull 增量）
//     "pushes": [                     // 本地 dirty 的 assets（已加密）
//       {
//         "id": "ast_xxx",
//         "kind": "note",
//         "client_rev": 3,
//         "cipher_title": "...",      // 可选，加密的标题
//         "cipher_blob": "...",       // 加密的 (body + blobs + meta) 包
//         "deleted_at": 0,
//         "updated_at": 1719...
//       }
//     ]
//   }
//
// 响应：
//   {
//     "latest_server_rev": 42,        // 服务端当前最高 rev
//     "pulled": [ ...asset mirrors ], // server_rev > since 的增量
//     "push_results": [
//       { "asset_id": "ast_xxx", "server_rev": 4, "conflict": false }
//     ]
//   }
//
// 安全：服务端永远不见 cipher_blob 明文（spec §3.2 决策 3 的 e2ee_local_first
// 承诺）。workspace_id 来自 JWT，客户端不可越权。

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/halfking/pocket-opencode/backend/internal/lobster"
)

// assetSyncRequest 是 /api/assets/sync 的请求体。
type assetSyncRequest struct {
	Since  int                  `json:"since"`
	Pushes []lobster.AssetMirror `json:"pushes"`
}

// assetSyncResponse 是响应体。
type assetSyncResponse struct {
	LatestServerRev int                   `json:"latest_server_rev"`
	Pulled          []lobster.AssetMirror `json:"pulled"`
	PushResults     []lobster.PushResult  `json:"push_results"`
}

func (s *Server) handleAssetSync(w http.ResponseWriter, r *http.Request) {
	if s.lobsterSync == nil {
		writeError(w, http.StatusServiceUnavailable, "lobster sync not configured")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}

	var body assetSyncRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}

	wsID := s.workspaceIDFromRequest(r)
	ctx := r.Context()

	// 1. Push：依次处理本地 dirty 的 assets。
	var pushResults []lobster.PushResult
	for i := range body.Pushes {
		m := body.Pushes[i]
		m.WorkspaceID = wsID // 强制绑定 caller workspace，防越权
		res, err := s.lobsterSync.Push(ctx, &m)
		if err != nil {
			// 单条 push 失败不中断整批；记录错误结果。
			pushResults = append(pushResults, lobster.PushResult{
				AssetID: m.ID,
			})
			continue
		}
		pushResults = append(pushResults, *res)
	}

	// 2. Pull：拉取 server_rev > since 的增量。
	pulled, err := s.lobsterSync.Pull(ctx, wsID, body.Since, 200)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "pull: "+err.Error())
		return
	}

	// 3. 返回最新 server_rev 供客户端下次 since 用。
	latestRev, err := s.lobsterSync.LatestServerRev(ctx, wsID)
	if err != nil {
		latestRev = body.Since // 降级
	}

	writeJSON(w, http.StatusOK, assetSyncResponse{
		LatestServerRev: latestRev,
		Pulled:          pulled,
		PushResults:     pushResults,
	})
}

// （未使用，留作未来 GET /api/assets/{id} 用）
var _ = strconv.Atoi
