package server

import (
	"encoding/json"
	"net/http"

	"github.com/halfking/pocket-opencode/backend/internal/migration"
)

// handleMigration 会话跨主机迁移主入口。
//
//	POST /api/migration          发起迁移
//	Body: {fromInstanceId, sessionId, toInstanceId?, taskId?, promptTemplates?, workingDirectory?}
//	→ MigrationResult（成功下发迁移命令到目标实例）
func (s *Server) handleMigration(w http.ResponseWriter, r *http.Request) {
	if s.migrationSvc == nil {
		writeMigrationError(w, http.StatusServiceUnavailable, "migration service not configured (registry/opencode/pluginHub required)")
		return
	}
	if r.Method != http.MethodPost {
		// GET 迁移服务状态
		writeMigrationJSON(w, http.StatusOK, map[string]any{
			"enabled": true,
			"endpoints": map[string]string{
				"migrate":   "POST /api/migration",
				"preview":   "POST /api/migration/preview (预览迁移包与提示词，不实际执行)",
			},
		})
		return
	}

	var req migration.MigrationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeMigrationError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.FromInstanceID == "" || req.SessionID == "" {
		writeMigrationError(w, http.StatusBadRequest, "fromInstanceId and sessionId are required")
		return
	}

	result, err := s.migrationSvc.Migrate(r.Context(), req)
	if err != nil && result != nil && result.Error != "" {
		// 迁移失败但有结构化结果，返回 result（含 error 字段）
		writeMigrationJSON(w, http.StatusOK, result)
		return
	}
	if err != nil {
		writeMigrationError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeMigrationJSON(w, http.StatusOK, result)
}

// handleMigrationPreview 预览迁移：拉取源会话组装迁移包 + 拼接提示词，但不实际下发命令。
// 用于迁移向导第一步"选择内容"时展示将要迁移什么。
//
//	POST /api/migration/preview
//	Body: {fromInstanceId, sessionId, promptTemplates?}
//	→ {pack: SessionResumeBrief, prompt: string, turnsMigrated: int}
func (s *Server) handleMigrationPreview(w http.ResponseWriter, r *http.Request) {
	if s.migrationSvc == nil {
		writeMigrationError(w, http.StatusServiceUnavailable, "migration service not configured")
		return
	}
	var req struct {
		FromInstanceID  string   `json:"fromInstanceId"`
		SessionID       string   `json:"sessionId"`
		PromptTemplates []string `json:"promptTemplates,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeMigrationError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.FromInstanceID == "" || req.SessionID == "" {
		writeMigrationError(w, http.StatusBadRequest, "fromInstanceId and sessionId are required")
		return
	}

	pack, prompt, err := s.migrationSvc.Preview(r.Context(), req.FromInstanceID, req.SessionID, req.PromptTemplates)
	if err != nil {
		writeMigrationError(w, http.StatusInternalServerError, "preview failed: "+err.Error())
		return
	}

	writeMigrationJSON(w, http.StatusOK, map[string]any{
		"pack":          pack,
		"prompt":        prompt,
		"turnsMigrated": pack.TurnCount,
	})
}

func writeMigrationJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeMigrationError(w http.ResponseWriter, status int, msg string) {
	writeMigrationJSON(w, status, map[string]string{"error": msg})
}
