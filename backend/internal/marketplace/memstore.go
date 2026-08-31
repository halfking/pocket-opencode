package marketplace

// memstore.go — 进程内 InMemory 实现 marketplace.Service。
//
// 用途：
//   - 单元测试 / 端到端 handler 测试的 backend；
//   - 本地开发环境的 mock(避免依赖真实 PG)；
//   - 不适用于生产部署 — 数据非持久化、跨实例不同步。
//
// 并发：使用 sync.RWMutex 保护内部 map；所有公开方法线程安全。

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// MemoryStore 是 marketplace.Service 的内存实现。
type MemoryStore struct {
	mu          sync.RWMutex
	packages    map[string]*Package        // package_id -> Package
	versions    map[string]*PackageVersion // version_id -> PackageVersion
	releases    map[string]*ReleaseRef
	installs    map[string]*InstallationRef
	reviewState map[string]bool  // version_id -> approved
	ratings     map[string][]int // release_id -> []score
}

// NewMemoryStore 创建并初始化一个空 store。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		packages:    make(map[string]*Package),
		versions:    make(map[string]*PackageVersion),
		releases:    make(map[string]*ReleaseRef),
		installs:    make(map[string]*InstallationRef),
		reviewState: make(map[string]bool),
		ratings:     make(map[string][]int),
	}
}

// Init noop：内存 store 无需初始化表。
func (s *MemoryStore) Init(_ context.Context) error { return nil }

// Submit 提交新版本。
func (s *MemoryStore) Submit(ctx context.Context, req SubmitRequest) (PackageVersion, error) {
	if req.WorkspaceID == "" || req.Name == "" || req.Kind == "" || req.Version == "" || req.Digest == "" {
		return PackageVersion{}, errors.New("marketplace: workspace_id, name, kind, version, digest required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	pkgID := req.PackageID
	if pkgID == "" {
		pkgID = fmt.Sprintf("%s/%s", req.WorkspaceID, req.Name)
	}
	if _, ok := s.packages[pkgID]; !ok {
		visibility := req.Visibility
		if visibility == "" {
			visibility = "workspace"
		}
		s.packages[pkgID] = &Package{
			PackageID:   pkgID,
			WorkspaceID: req.WorkspaceID,
			Name:        req.Name,
			Kind:        req.Kind,
			Publisher:   req.Publisher,
			Visibility:  visibility,
			CreatedAt:   time.Now(),
		}
	}

	versionID := fmt.Sprintf("%s@%s", pkgID, req.Version)
	if _, ok := s.versions[versionID]; ok {
		return PackageVersion{}, fmt.Errorf("%w: version %s already exists", ErrMarketplaceConflict, req.Version)
	}
	v := &PackageVersion{
		VersionID:   versionID,
		PackageID:   pkgID,
		WorkspaceID: req.WorkspaceID,
		Version:     req.Version,
		Digest:      req.Digest,
		Manifest:    req.Manifest,
		Status:      VersionDraft,
		Signature:   req.Signature,
		SubmittedAt: time.Now(),
	}
	s.versions[versionID] = v
	return *v, nil
}

// Review 审核。
func (s *MemoryStore) Review(ctx context.Context, cmd ReviewCommand) error {
	if cmd.WorkspaceID == "" || cmd.VersionID == "" {
		return errors.New("marketplace: workspace_id and version_id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.versions[cmd.VersionID]
	if !ok {
		return ErrMarketplaceNotFound
	}
	if v.Status != VersionDraft && v.Status != VersionSubmitted && v.Status != VersionReviewing {
		return fmt.Errorf("%w: cannot review version in status %s", ErrMarketplaceConflict, v.Status)
	}
	if cmd.Approved {
		v.Status = VersionApproved
	} else {
		v.Status = VersionRejected
	}
	v.Reviewer = cmd.Reviewer
	s.reviewState[cmd.VersionID] = cmd.Approved
	return nil
}

// Publish 发布已审核版本。
func (s *MemoryStore) Publish(ctx context.Context, cmd PublishCommand) (ReleaseRef, error) {
	channel := cmd.Channel
	if channel == "" {
		channel = "stable"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.versions[cmd.VersionID]
	if !ok {
		return ReleaseRef{}, ErrMarketplaceNotFound
	}
	if v.Status != VersionApproved {
		return ReleaseRef{}, fmt.Errorf("%w: version must be approved to publish, got %s", ErrMarketplaceConflict, v.Status)
	}
	releaseID := fmt.Sprintf("%s-%s-%d", cmd.VersionID, channel, time.Now().UnixNano())
	rel := &ReleaseRef{
		ReleaseID:   releaseID,
		VersionID:   cmd.VersionID,
		Channel:     channel,
		PublishedAt: time.Now(),
	}
	s.releases[releaseID] = rel
	v.Status = VersionPublished
	v.PublishedAt = &rel.PublishedAt
	return *rel, nil
}

// Install 记录一次安装。
func (s *MemoryStore) Install(ctx context.Context, cmd InstallCommand) (InstallationRef, error) {
	if cmd.WorkspaceID == "" || cmd.ReleaseID == "" {
		return InstallationRef{}, errors.New("marketplace: workspace_id and release_id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.releases[cmd.ReleaseID]; !ok {
		return InstallationRef{}, ErrMarketplaceNotFound
	}
	instID := fmt.Sprintf("%s-%s-%d", cmd.WorkspaceID, cmd.ReleaseID, time.Now().UnixNano())
	inst := &InstallationRef{
		InstallationID: instID,
		ReleaseID:      cmd.ReleaseID,
		InstalledAt:    time.Now(),
	}
	s.installs[instID] = inst
	return *inst, nil
}

// Revoke 撤销发布。
func (s *MemoryStore) Revoke(ctx context.Context, cmd RevokeCommand) error {
	if cmd.WorkspaceID == "" || cmd.ReleaseID == "" {
		return errors.New("marketplace: workspace_id and release_id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rel, ok := s.releases[cmd.ReleaseID]
	if !ok {
		return ErrMarketplaceNotFound
	}
	v, ok := s.versions[rel.VersionID]
	if !ok {
		return ErrMarketplaceNotFound
	}
	v.Status = VersionRevoked
	return nil
}

// Rate 评分(仅校验范围,持久化明细)。
func (s *MemoryStore) Rate(ctx context.Context, cmd RatingCommand) error {
	if cmd.WorkspaceID == "" || cmd.ReleaseID == "" || cmd.RatedBy == "" {
		return errors.New("marketplace: workspace_id, release_id, rated_by required")
	}
	if cmd.Score < 1 || cmd.Score > 5 {
		return ErrMarketplaceRateOutOfRange
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.releases[cmd.ReleaseID]; !ok {
		return ErrMarketplaceNotFound
	}
	s.ratings[cmd.ReleaseID] = append(s.ratings[cmd.ReleaseID], cmd.Score)
	return nil
}

// ListPackages 列出 workspace 的所有包。
func (s *MemoryStore) ListPackages(ctx context.Context, workspaceID string) ([]Package, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Package, 0)
	for _, p := range s.packages {
		if p.WorkspaceID == workspaceID {
			out = append(out, *p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// ListReleases 列出 workspace 关联的所有 release。
func (s *MemoryStore) ListReleases(ctx context.Context, workspaceID string) ([]ReleaseRef, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ReleaseRef, 0)
	for _, rel := range s.releases {
		if v, ok := s.versions[rel.VersionID]; ok && v.WorkspaceID == workspaceID {
			out = append(out, *rel)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PublishedAt.After(out[j].PublishedAt) })
	return out, nil
}

// ListVersions 列出一个包的所有版本。
func (s *MemoryStore) ListVersions(ctx context.Context, workspaceID, packageID string) ([]PackageVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]PackageVersion, 0)
	for _, v := range s.versions {
		if v.WorkspaceID == workspaceID && v.PackageID == packageID {
			out = append(out, *v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SubmittedAt.After(out[j].SubmittedAt) })
	return out, nil
}

// Compile-time 接口断言。
var _ Service = (*MemoryStore)(nil)
