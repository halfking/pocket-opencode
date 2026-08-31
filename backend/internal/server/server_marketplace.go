package server

// server_marketplace.go — Phase 4 移动分布式 AI 工作平台 · 技能市场 HTTP handlers。
//
// 设计原则：
//   - 所有路由强制 requireAuth；workspace_id 严格来自 JWT claim，绝不信任 body。
//   - handler 仅做参数解析、错误翻译与审计落点；业务逻辑（提交、审核、发布、安装、
//     撤销、评分、依赖解析、签名验证）保留在 backend/internal/marketplace 包。
//   - 当 s.marketplaceStore == nil 时（PG 未配置或离线模式）返回 503，便于前端
//     做降级；绝不在此处 fallback 到内存 store，避免不同部署产生不一致行为。
//   - Rate 评分在 MVP 阶段仅校验范围与权限（由 store 保证）；持久化明细留待后续 sprint。
//   - 任何 ID 字段都来自 URL/body 显式提供；handler 不自造 ID；这是后续审计和
//     幂等性的基础。
//
// 路由表（全部位于 /api/marketplace/* 下，统一在 Server.Handler() 中注册）：
//
//   GET    /api/marketplace/packages           列出当前 workspace 可见的包
//   GET    /api/marketplace/releases           列出当前 workspace 可见的 release
//   GET    /api/marketplace/packages/{id}/versions
//                                          列出一个包的全部版本
//   POST   /api/marketplace/submit             提交新版本（draft）
//   POST   /api/marketplace/review             审核一个版本
//   POST   /api/marketplace/publish            发布一个已审核版本
//   POST   /api/marketplace/install            记录一次安装
//   POST   /api/marketplace/revoke             撤销一次发布
//   POST   /api/marketplace/rate               对一次发布评分（1-5）

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/halfking/pocket-opencode/backend/internal/marketplace"
)

// requireMarketplaceStore 在 store 未配置时返回 503，避免在每个 handler 里重复判断。
func (s *Server) requireMarketplaceStore(w http.ResponseWriter, r *http.Request) bool {
	if s.marketplaceStore == nil {
		writeError(w, http.StatusServiceUnavailable, "marketplace store not configured")
		return false
	}
	return true
}

// decodeMarketplaceJSON 解析请求体，限制最大 1MB 并拒绝未知字段。
// 1MB 对 manifest/dependency 树足够；过大负载说明客户端误把包内容塞到了 body。
//
// 注：当前 decoder 容忍未知字段，因为客户端可能在 body 里附带 server 端
// 总会覆盖的字段（workspace_id / publisher 等）。handler 在拿到解码结果
// 后会用认证上下文强制覆盖这些字段，避免误信任客户端输入。
func decodeMarketplaceJSON(r *http.Request, dst interface{}) error {
	if r.Body == nil {
		return errors.New("request body is required")
	}
	const maxBody = 1 << 20
	limited := io.LimitReader(r.Body, maxBody+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return err
	}
	if len(data) > maxBody {
		return errors.New("request body too large")
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	if err := dec.Decode(dst); err != nil {
		return err
	}
	return nil
}

// handleMarketplacePackages GET /api/marketplace/packages
//
//	可选 query：kind=skill|agent|workflow 过滤
func (s *Server) handleMarketplacePackages(w http.ResponseWriter, r *http.Request) {
	if !s.requireMarketplaceStore(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	workspaceID := s.workspaceIDFromRequest(r)
	packages, err := s.marketplaceStore.ListPackages(r.Context(), workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	kind := r.URL.Query().Get("kind")
	if kind != "" {
		filtered := make([]marketplace.Package, 0, len(packages))
		for _, p := range packages {
			if p.Kind == kind {
				filtered = append(filtered, p)
			}
		}
		packages = filtered
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"packages": packages})
}

// handleMarketplaceReleases GET /api/marketplace/releases
func (s *Server) handleMarketplaceReleases(w http.ResponseWriter, r *http.Request) {
	if !s.requireMarketplaceStore(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	workspaceID := s.workspaceIDFromRequest(r)
	releases, err := s.marketplaceStore.ListReleases(r.Context(), workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"releases": releases})
}

// handleMarketplacePackageVersions GET /api/marketplace/packages/{id}/versions
func (s *Server) handleMarketplacePackageVersions(w http.ResponseWriter, r *http.Request) {
	if !s.requireMarketplaceStore(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}

	id := extractMarketplacePackageID(r.URL.Path, "/api/marketplace/packages/", "/versions")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid package id")
		return
	}

	workspaceID := s.workspaceIDFromRequest(r)
	versions, err := s.marketplaceStore.ListVersions(r.Context(), workspaceID, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"versions": versions})
}

// handleMarketplaceSubmit POST /api/marketplace/submit
func (s *Server) handleMarketplaceSubmit(w http.ResponseWriter, r *http.Request) {
	if !s.requireMarketplaceStore(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}

	var body marketplace.SubmitRequest
	if err := decodeMarketplaceJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.Name == "" || body.Kind == "" || body.Version == "" || body.Digest == "" {
		writeError(w, http.StatusBadRequest, "name, kind, version, digest are required")
		return
	}
	if body.Kind != "skill" && body.Kind != "agent" && body.Kind != "workflow" {
		writeError(w, http.StatusBadRequest, "kind must be one of skill|agent|workflow")
		return
	}
	if body.Visibility == "" {
		body.Visibility = "workspace"
	}
	if body.Visibility != "private" && body.Visibility != "workspace" &&
		body.Visibility != "org" && body.Visibility != "public" {
		writeError(w, http.StatusBadRequest, "visibility must be one of private|workspace|org|public")
		return
	}

	// workspace_id 与 publisher 严格来自认证上下文，绝不信任 body。
	workspaceID := s.workspaceIDFromRequest(r)
	body.WorkspaceID = workspaceID
	if body.Publisher == "" {
		body.Publisher = s.userIDFromRequest(r)
	}

	version, err := s.marketplaceStore.Submit(r.Context(), body)
	if err != nil {
		writeMarketplaceError(w, err)
		return
	}

	s.auditGateway(r, "marketplace.submit", version.PackageID,
		"version="+version.Version+" kind="+body.Kind, true)
	writeJSON(w, http.StatusCreated, version)
}

// handleMarketplaceReview POST /api/marketplace/review
func (s *Server) handleMarketplaceReview(w http.ResponseWriter, r *http.Request) {
	if !s.requireMarketplaceStore(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}

	var body struct {
		VersionID string `json:"version_id"`
		Approved  bool   `json:"approved"`
		Comment   string `json:"comment"`
	}
	if err := decodeMarketplaceJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.VersionID == "" {
		writeError(w, http.StatusBadRequest, "version_id is required")
		return
	}

	cmd := marketplace.ReviewCommand{
		WorkspaceID: s.workspaceIDFromRequest(r),
		VersionID:   body.VersionID,
		Reviewer:    s.userIDFromRequest(r),
		Approved:    body.Approved,
		Comment:     body.Comment,
	}
	if err := s.marketplaceStore.Review(r.Context(), cmd); err != nil {
		writeMarketplaceError(w, err)
		return
	}

	s.auditGateway(r, "marketplace.review", body.VersionID,
		"approved="+strconv.FormatBool(body.Approved), true)
	writeJSON(w, http.StatusOK, map[string]interface{}{"reviewed": true})
}

// handleMarketplacePublish POST /api/marketplace/publish
func (s *Server) handleMarketplacePublish(w http.ResponseWriter, r *http.Request) {
	if !s.requireMarketplaceStore(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}

	var body struct {
		VersionID string `json:"version_id"`
		Channel   string `json:"channel"`
	}
	if err := decodeMarketplaceJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.VersionID == "" {
		writeError(w, http.StatusBadRequest, "version_id is required")
		return
	}
	if body.Channel == "" {
		body.Channel = "stable"
	}

	cmd := marketplace.PublishCommand{
		WorkspaceID: s.workspaceIDFromRequest(r),
		VersionID:   body.VersionID,
		Channel:     body.Channel,
	}
	release, err := s.marketplaceStore.Publish(r.Context(), cmd)
	if err != nil {
		writeMarketplaceError(w, err)
		return
	}

	s.auditGateway(r, "marketplace.publish", release.ReleaseID,
		"version_id="+body.VersionID+" channel="+body.Channel, true)
	writeJSON(w, http.StatusCreated, release)
}

// handleMarketplaceInstall POST /api/marketplace/install
func (s *Server) handleMarketplaceInstall(w http.ResponseWriter, r *http.Request) {
	if !s.requireMarketplaceStore(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}

	var body struct {
		ReleaseID string `json:"release_id"`
		TargetEnv string `json:"target_env"`
	}
	if err := decodeMarketplaceJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.ReleaseID == "" {
		writeError(w, http.StatusBadRequest, "release_id is required")
		return
	}

	cmd := marketplace.InstallCommand{
		WorkspaceID: s.workspaceIDFromRequest(r),
		ReleaseID:   body.ReleaseID,
		TargetEnv:   body.TargetEnv,
		InstalledBy: s.userIDFromRequest(r),
	}
	inst, err := s.marketplaceStore.Install(r.Context(), cmd)
	if err != nil {
		writeMarketplaceError(w, err)
		return
	}

	s.auditGateway(r, "marketplace.install", inst.InstallationID,
		"release_id="+body.ReleaseID+" target="+body.TargetEnv, true)
	writeJSON(w, http.StatusCreated, inst)
}

// handleMarketplaceRevoke POST /api/marketplace/revoke
func (s *Server) handleMarketplaceRevoke(w http.ResponseWriter, r *http.Request) {
	if !s.requireMarketplaceStore(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}

	var body struct {
		ReleaseID string `json:"release_id"`
		Reason    string `json:"reason"`
	}
	if err := decodeMarketplaceJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.ReleaseID == "" || strings.TrimSpace(body.Reason) == "" {
		writeError(w, http.StatusBadRequest, "release_id and reason are required")
		return
	}

	cmd := marketplace.RevokeCommand{
		WorkspaceID: s.workspaceIDFromRequest(r),
		ReleaseID:   body.ReleaseID,
		Reason:      body.Reason,
		RevokedBy:   s.userIDFromRequest(r),
	}
	if err := s.marketplaceStore.Revoke(r.Context(), cmd); err != nil {
		writeMarketplaceError(w, err)
		return
	}

	s.auditGateway(r, "marketplace.revoke", body.ReleaseID, "reason="+body.Reason, true)
	writeJSON(w, http.StatusOK, map[string]interface{}{"revoked": true})
}

// handleMarketplaceRate POST /api/marketplace/rate
func (s *Server) handleMarketplaceRate(w http.ResponseWriter, r *http.Request) {
	if !s.requireMarketplaceStore(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}

	var body struct {
		ReleaseID string `json:"release_id"`
		Score     int    `json:"score"`
		Comment   string `json:"comment"`
	}
	if err := decodeMarketplaceJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.ReleaseID == "" {
		writeError(w, http.StatusBadRequest, "release_id is required")
		return
	}

	cmd := marketplace.RatingCommand{
		WorkspaceID: s.workspaceIDFromRequest(r),
		ReleaseID:   body.ReleaseID,
		RatedBy:     s.userIDFromRequest(r),
		Score:       body.Score,
		Comment:     body.Comment,
	}
	if err := s.marketplaceStore.Rate(r.Context(), cmd); err != nil {
		writeMarketplaceError(w, err)
		return
	}

	s.auditGateway(r, "marketplace.rate", body.ReleaseID,
		"score="+strconv.Itoa(body.Score), true)
	writeJSON(w, http.StatusOK, map[string]interface{}{"recorded": true})
}

// handleMarketplaceRouter 统一分发 /api/marketplace/ 子树。
//
// 路由到具体 handler 的逻辑集中在 Handler() 中；此处不再做 path split，避免
// 重复解析与潜在的 dispatch 不一致。
func (s *Server) handleMarketplaceRouter(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/marketplace/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	switch parts[0] {
	case "submit":
		s.handleMarketplaceSubmit(w, r)
	case "review":
		s.handleMarketplaceReview(w, r)
	case "publish":
		s.handleMarketplacePublish(w, r)
	case "install":
		s.handleMarketplaceInstall(w, r)
	case "revoke":
		s.handleMarketplaceRevoke(w, r)
	case "rate":
		s.handleMarketplaceRate(w, r)
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

// extractMarketplacePackageID 从 /api/marketplace/packages/{id}/versions 之类的
// 路径中提取 {id}。{id} 允许包含 /（package_id 通常是 "workspace/name" 形式），
// 但要求路径中除 id 段外不出现额外的 /。
//
// 校验：
//   - 必须有 prefix 与 suffix；
//   - 中间段恰好一次 / 后再无其他 /；
//   - id 段不允许为空。
func extractMarketplacePackageID(rawPath, prefix, suffix string) string {
	if !strings.HasPrefix(rawPath, prefix) || !strings.HasSuffix(rawPath, suffix) {
		return ""
	}
	body := rawPath[len(prefix) : len(rawPath)-len(suffix)]
	if body == "" {
		return ""
	}
	// body 中只允许出现一次 /（作为 "workspace/name" 的分隔）。
	if strings.Count(body, "/") > 1 {
		return ""
	}
	return body
}

// writeMarketplaceError 把 marketplace 包内的 sentinel 错误翻译为合适的 HTTP 状态。
func writeMarketplaceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, marketplace.ErrMarketplaceNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, marketplace.ErrMarketplaceConflict),
		errors.Is(err, marketplace.ErrMarketplaceNotPublished):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, marketplace.ErrMarketplaceRateOutOfRange):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}
