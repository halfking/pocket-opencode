package server

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/usersetting"
)

func (s *Server) SetUserSettingsStore(store usersetting.Repository) {
	s.userSettings = store
}

func (s *Server) handleUserSettings(w http.ResponseWriter, r *http.Request) {
	if s.userSettings == nil {
		writeError(w, http.StatusServiceUnavailable, "user settings store unavailable")
		return
	}
	userID := s.userIDFromRequest(r)
	workspaceID := s.workspaceIDFromRequest(r)

	switch r.Method {
	case http.MethodGet:
		list, err := s.userSettings.List(userID, workspaceID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		public := make([]usersetting.Record, 0, len(list))
		for _, rec := range list {
			rec.Secret = ""
			public = append(public, rec)
		}
		writeJSON(w, http.StatusOK, map[string]any{"settings": public})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleUserSettingItem(w http.ResponseWriter, r *http.Request) {
	if s.userSettings == nil {
		writeError(w, http.StatusServiceUnavailable, "user settings store unavailable")
		return
	}
	namespace, id := parseUserSettingPath(r.URL.Path)
	if namespace == "" || id == "" {
		http.Error(w, "namespace and id required", http.StatusBadRequest)
		return
	}
	userID := s.userIDFromRequest(r)
	workspaceID := s.workspaceIDFromRequest(r)

	switch r.Method {
	case http.MethodGet:
		rec, err := s.userSettings.Get(userID, workspaceID, namespace, id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if rec == nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		rec.Secret = ""
		writeJSON(w, http.StatusOK, rec)
	case http.MethodPut:
		var body struct {
			Payload   json.RawMessage `json:"payload"`
			UpdatedAt int64           `json:"updatedAt"`
			Secret    string          `json:"secret"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
			return
		}
		result, err := s.userSettings.Put(usersetting.Record{
			UserID:      userID,
			WorkspaceID: workspaceID,
			Namespace:   namespace,
			ID:          id,
			Payload:     body.Payload,
			UpdatedAt:   body.UpdatedAt,
			Secret:      body.Secret,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		result.Record.Secret = ""
		if result.Conflict {
			writeJSON(w, http.StatusConflict, result)
			return
		}
		if namespace == "llm_gateway" && result.Applied {
			s.applyGatewayFromSetting(r, usersetting.Record{
				Namespace: namespace, ID: id, Payload: body.Payload,
				Secret: body.Secret, UpdatedAt: body.UpdatedAt,
			})
		}
		writeJSON(w, http.StatusOK, result)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func parseUserSettingPath(path string) (namespace, id string) {
	const prefix = "/api/user-settings/"
	rest := strings.TrimPrefix(path, prefix)
	rest = strings.Trim(rest, "/")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return parts[0], parts[1]
}

func settingHasObsoleteGateway(payload json.RawMessage) bool {
	var body struct {
		BaseURL string `json:"baseURL"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return false
	}
	return obsoleteLocalGatewayURL(body.BaseURL)
}

func (s *Server) seedAdminGatewaySetting(def llmGatewayState) {
	if s.userSettings == nil {
		return
	}
	existing, err := s.userSettings.Get("user-admin", "default", "llm_gateway", "default")
	if err != nil {
		return
	}
	if existing != nil && !settingHasObsoleteGateway(existing.Payload) {
		return
	}
	payload, err := json.Marshal(map[string]any{
		"baseURL": def.BaseURL, "format": def.Format,
		"models": def.Models, "preferredModels": def.PreferredModels,
	})
	if err != nil {
		return
	}
	if _, err := s.userSettings.Put(usersetting.Record{
		UserID: "user-admin", WorkspaceID: "default",
		Namespace: "llm_gateway", ID: "default",
		Payload: payload, Secret: def.APIKey, UpdatedAt: time.Now().Unix(),
	}); err != nil {
		log.Printf("[user-settings] seed admin llm_gateway: %v", err)
	}
}

func (s *Server) applyGatewayFromSetting(r *http.Request, rec usersetting.Record) {
	var payload struct {
		BaseURL         string   `json:"baseURL"`
		Format          string   `json:"format"`
		Models          []string `json:"models"`
		PreferredModels []string `json:"preferredModels"`
	}
	if err := json.Unmarshal(rec.Payload, &payload); err != nil || payload.BaseURL == "" {
		return
	}
	workspaceID := s.workspaceIDFromRequest(r)
	current := s.gatewaySnapshot(workspaceID)
	if rec.Secret != "" {
		current.APIKey = rec.Secret
	}
	current.BaseURL = payload.BaseURL
	current.Format = normalizeGatewayFormat(payload.Format)
	if payload.Models != nil {
		current.Models = append([]string(nil), payload.Models...)
	}
	if payload.PreferredModels != nil {
		current.PreferredModels = append([]string(nil), payload.PreferredModels...)
	}
	current.UpdatedAt = rec.UpdatedAt
	if s.llmGWStore != nil {
		if err := s.llmGWStore.SaveConfig(r.Context(), workspaceID, current); err != nil {
			log.Printf("[user-settings] persist gateway: %v", err)
		}
	}
	if err := s.pushConfigToOpenCode(r, workspaceID, current); err != nil {
		log.Printf("[user-settings] push gateway to opencode (non-fatal): %v", err)
	}
	if s.llmGWCache != nil {
		s.llmGWCache.replace(workspaceID, current)
	}
}
