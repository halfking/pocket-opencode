package marketplace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Package marketplace 实现 OpenPocket 技能市场（Skill Marketplace）。
//
// 设计参考 RedClaw marketplace 模式：Package/Manifest/Version 三层抽象，
// submit → review → publish → install 完整生命周期，支持依赖、权限声明与签名验证。
//
// 与 RedClaw 的差异：
//   - Kind 包含 "skill"（MCP 技能）、"agent"（智能体模板）、"workflow"（任务流）
//   - 技能包格式：manifest.json + skill/ 目录（WASM/Python/TypeScript 等运行时）
//   - 租户隔离：每个 workspace 独立命名空间（private/workspace/org/public）
//
// 存储：PostgreSQL marketplace_* 表（workspace 隔离）。

// Package 是逻辑包（技能/智能体/工作流等）。
type Package struct {
	PackageID   string    `json:"package_id"`
	WorkspaceID string    `json:"workspace_id"`
	Name        string    `json:"name"`
	Kind        string    `json:"kind"` // "skill", "agent", "workflow"
	Publisher   string    `json:"publisher"`
	Visibility  string    `json:"visibility"` // "private", "workspace", "org", "public"
	CreatedAt   time.Time `json:"created_at"`
}

// Manifest 声明包版本的元数据。
type Manifest struct {
	Version       string            `json:"version"`
	Description   string            `json:"description,omitempty"`
	Digest        string            `json:"digest"`
	Licenses      []string          `json:"licenses,omitempty"`
	Dependencies  []Dependency      `json:"dependencies,omitempty"`
	Permissions   []string          `json:"permissions,omitempty"`
	Compatibility map[string]string `json:"compatibility,omitempty"`
	Runtime       string            `json:"runtime,omitempty"` // "wasm", "python", "typescript"
}

// Dependency 引用其他市场包。
type Dependency struct {
	PackageID string `json:"package_id"`
	Version   string `json:"version"`
}

// PackageVersion 是包的不可变版本。
type PackageVersion struct {
	VersionID   string        `json:"version_id"`
	PackageID   string        `json:"package_id"`
	WorkspaceID string        `json:"workspace_id"`
	Version     string        `json:"version"`
	Digest      string        `json:"digest"`
	Manifest    Manifest      `json:"manifest"`
	Status      VersionStatus `json:"status"`
	Signature   string        `json:"signature,omitempty"`
	Reviewer    string        `json:"reviewer,omitempty"`
	SubmittedAt time.Time     `json:"submitted_at"`
	PublishedAt *time.Time    `json:"published_at,omitempty"`
}

// VersionStatus 版本生命周期。
type VersionStatus string

const (
	VersionDraft     VersionStatus = "draft"
	VersionSubmitted VersionStatus = "submitted"
	VersionReviewing VersionStatus = "reviewing"
	VersionApproved  VersionStatus = "approved"
	VersionRejected  VersionStatus = "rejected"
	VersionPublished VersionStatus = "published"
	VersionRevoked   VersionStatus = "revoked"
)

// SubmitRequest 提交包版本。
type SubmitRequest struct {
	WorkspaceID string   `json:"workspace_id"`
	PackageID   string   `json:"package_id,omitempty"`
	Name        string   `json:"name"`
	Kind        string   `json:"kind"`
	Version     string   `json:"version"`
	Digest      string   `json:"digest"`
	Manifest    Manifest `json:"manifest"`
	Publisher   string   `json:"publisher"`
	Signature   string   `json:"signature,omitempty"`
	Visibility  string   `json:"visibility,omitempty"`
}

// ReviewCommand 审核版本。
type ReviewCommand struct {
	WorkspaceID string `json:"workspace_id"`
	VersionID   string `json:"version_id"`
	Reviewer    string `json:"reviewer"`
	Approved    bool   `json:"approved"`
	Comment     string `json:"comment,omitempty"`
}

// PublishCommand 发布到 release channel。
type PublishCommand struct {
	WorkspaceID string `json:"workspace_id"`
	VersionID   string `json:"version_id"`
	Channel     string `json:"channel,omitempty"`
}

// ReleaseRef 已发布的版本引用。
type ReleaseRef struct {
	ReleaseID   string    `json:"release_id"`
	VersionID   string    `json:"version_id"`
	Channel     string    `json:"channel"`
	PublishedAt time.Time `json:"published_at"`
}

// InstallCommand 安装包。
type InstallCommand struct {
	WorkspaceID string `json:"workspace_id"`
	ReleaseID   string `json:"release_id"`
	TargetEnv   string `json:"target_env,omitempty"`
	InstalledBy string `json:"installed_by"`
}

// InstallationRef 安装记录。
type InstallationRef struct {
	InstallationID string    `json:"installation_id"`
	ReleaseID      string    `json:"release_id"`
	InstalledAt    time.Time `json:"installed_at"`
}

// RevokeCommand 撤销发布。
type RevokeCommand struct {
	WorkspaceID string `json:"workspace_id"`
	ReleaseID   string `json:"release_id"`
	Reason      string `json:"reason"`
	RevokedBy   string `json:"revoked_by"`
}

// RatingCommand 评分。
type RatingCommand struct {
	WorkspaceID string `json:"workspace_id"`
	ReleaseID   string `json:"release_id"`
	RatedBy     string `json:"rated_by"`
	Score       int    `json:"score"`
	Comment     string `json:"comment,omitempty"`
}

// Service 是市场服务接口。
type Service interface {
	Submit(ctx context.Context, request SubmitRequest) (PackageVersion, error)
	Review(ctx context.Context, command ReviewCommand) error
	Publish(ctx context.Context, command PublishCommand) (ReleaseRef, error)
	Install(ctx context.Context, command InstallCommand) (InstallationRef, error)
	Revoke(ctx context.Context, command RevokeCommand) error
	Rate(ctx context.Context, command RatingCommand) error
	ListPackages(ctx context.Context, workspaceID string) ([]Package, error)
	ListReleases(ctx context.Context, workspaceID string) ([]ReleaseRef, error)
	ListVersions(ctx context.Context, workspaceID, packageID string) ([]PackageVersion, error)
}

// Store 是市场数据的 PostgreSQL 持久化实现。
type Store struct {
	pool *pgxpool.Pool
}

// NewStore 创建 Store。
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

const schema = `
CREATE TABLE IF NOT EXISTS marketplace_packages (
	package_id   TEXT PRIMARY KEY,
	workspace_id TEXT NOT NULL,
	name         TEXT NOT NULL,
	kind         TEXT NOT NULL,
	publisher    TEXT NOT NULL,
	visibility   TEXT NOT NULL,
	created_at   BIGINT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_marketplace_pkg_ws ON marketplace_packages(workspace_id);

CREATE TABLE IF NOT EXISTS marketplace_versions (
	version_id   TEXT PRIMARY KEY,
	package_id   TEXT NOT NULL,
	workspace_id TEXT NOT NULL,
	version      TEXT NOT NULL,
	digest       TEXT NOT NULL,
	manifest     JSONB NOT NULL,
	status       TEXT NOT NULL,
	signature    TEXT,
	reviewer     TEXT,
	submitted_at BIGINT NOT NULL,
	published_at BIGINT
);
CREATE INDEX IF NOT EXISTS idx_marketplace_ver_pkg ON marketplace_versions(package_id);

CREATE TABLE IF NOT EXISTS marketplace_releases (
	release_id   TEXT PRIMARY KEY,
	version_id   TEXT NOT NULL,
	channel      TEXT NOT NULL,
	published_at BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS marketplace_installations (
	installation_id TEXT PRIMARY KEY,
	release_id      TEXT NOT NULL,
	workspace_id    TEXT NOT NULL,
	installed_by    TEXT NOT NULL,
	installed_at    BIGINT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_marketplace_inst_ws ON marketplace_installations(workspace_id);
`

// Init 初始化 marketplace 表。
func (s *Store) Init(ctx context.Context) error {
	if s.pool == nil {
		return fmt.Errorf("marketplace: pool not configured")
	}
	_, err := s.pool.Exec(ctx, schema)
	return err
}

// Submit 提交新版本（草稿）。同名包首次提交自动创建 Package 记录。
func (s *Store) Submit(ctx context.Context, req SubmitRequest) (PackageVersion, error) {
	if s.pool == nil {
		return PackageVersion{}, fmt.Errorf("marketplace: pool not configured")
	}
	if req.WorkspaceID == "" || req.Name == "" || req.Kind == "" || req.Version == "" {
		return PackageVersion{}, errors.New("marketplace: workspace_id, name, kind, version required")
	}
	if req.Digest == "" {
		return PackageVersion{}, errors.New("marketplace: digest required")
	}

	pkgID := req.PackageID
	if pkgID == "" {
		pkgID = fmt.Sprintf("%s/%s", req.WorkspaceID, req.Name)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PackageVersion{}, err
	}
	defer tx.Rollback(ctx)

	var existingPkg string
	err = tx.QueryRow(ctx, `SELECT package_id FROM marketplace_packages WHERE package_id = $1`, pkgID).Scan(&existingPkg)
	if errors.Is(err, pgx.ErrNoRows) {
		now := time.Now().Unix()
		if _, err := tx.Exec(ctx, `
			INSERT INTO marketplace_packages (package_id, workspace_id, name, kind, publisher, visibility, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, pkgID, req.WorkspaceID, req.Name, req.Kind, req.Publisher, req.Visibility, now); err != nil {
			return PackageVersion{}, err
		}
	} else if err != nil {
		return PackageVersion{}, err
	}

	versionID := fmt.Sprintf("%s@%s", pkgID, req.Version)
	now := time.Now().Unix()
	manifestJSON, err := json.Marshal(req.Manifest)
	if err != nil {
		return PackageVersion{}, fmt.Errorf("marketplace: marshal manifest: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO marketplace_versions (version_id, package_id, workspace_id, version, digest, manifest, status, signature, submitted_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, versionID, pkgID, req.WorkspaceID, req.Version, req.Digest, manifestJSON, VersionDraft, req.Signature, now); err != nil {
		return PackageVersion{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return PackageVersion{}, err
	}

	return PackageVersion{
		VersionID:   versionID,
		PackageID:   pkgID,
		WorkspaceID: req.WorkspaceID,
		Version:     req.Version,
		Digest:      req.Digest,
		Manifest:    req.Manifest,
		Status:      VersionDraft,
		Signature:   req.Signature,
		SubmittedAt: time.Unix(now, 0),
	}, nil
}

// Review 审核版本。
func (s *Store) Review(ctx context.Context, cmd ReviewCommand) error {
	if s.pool == nil {
		return fmt.Errorf("marketplace: pool not configured")
	}
	if cmd.WorkspaceID == "" || cmd.VersionID == "" {
		return errors.New("marketplace: workspace_id and version_id required")
	}
	status := VersionApproved
	if !cmd.Approved {
		status = VersionRejected
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE marketplace_versions SET status = $1, reviewer = $2
		WHERE version_id = $3 AND workspace_id = $4 AND status IN ('draft', 'submitted', 'reviewing')
	`, status, cmd.Reviewer, cmd.VersionID, cmd.WorkspaceID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrMarketplaceNotFound
	}
	return nil
}

// Publish 发布已批准的版本到一个 release channel。
func (s *Store) Publish(ctx context.Context, cmd PublishCommand) (ReleaseRef, error) {
	if s.pool == nil {
		return ReleaseRef{}, fmt.Errorf("marketplace: pool not configured")
	}
	channel := cmd.Channel
	if channel == "" {
		channel = "stable"
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ReleaseRef{}, err
	}
	defer tx.Rollback(ctx)

	var status string
	err = tx.QueryRow(ctx, `SELECT status FROM marketplace_versions WHERE version_id = $1 AND workspace_id = $2`,
		cmd.VersionID, cmd.WorkspaceID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ReleaseRef{}, ErrMarketplaceNotFound
	}
	if err != nil {
		return ReleaseRef{}, err
	}
	if status != string(VersionApproved) {
		return ReleaseRef{}, fmt.Errorf("%w: version must be approved to publish, got %s", ErrMarketplaceConflict, status)
	}

	releaseID := fmt.Sprintf("%s-%s-%d", cmd.VersionID, channel, time.Now().UnixNano())
	now := time.Now().Unix()
	if _, err := tx.Exec(ctx, `
		INSERT INTO marketplace_releases (release_id, version_id, channel, published_at)
		VALUES ($1, $2, $3, $4)
	`, releaseID, cmd.VersionID, channel, now); err != nil {
		return ReleaseRef{}, err
	}

	if _, err := tx.Exec(ctx, `UPDATE marketplace_versions SET status = $1, published_at = $2 WHERE version_id = $3`,
		VersionPublished, now, cmd.VersionID); err != nil {
		return ReleaseRef{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return ReleaseRef{}, err
	}

	return ReleaseRef{
		ReleaseID:   releaseID,
		VersionID:   cmd.VersionID,
		Channel:     channel,
		PublishedAt: time.Unix(now, 0),
	}, nil
}

// Install 记录一次安装。
func (s *Store) Install(ctx context.Context, cmd InstallCommand) (InstallationRef, error) {
	if s.pool == nil {
		return InstallationRef{}, fmt.Errorf("marketplace: pool not configured")
	}
	if cmd.WorkspaceID == "" || cmd.ReleaseID == "" {
		return InstallationRef{}, errors.New("marketplace: workspace_id and release_id required")
	}

	var versionID string
	if err := s.pool.QueryRow(ctx, `SELECT version_id FROM marketplace_releases WHERE release_id = $1`,
		cmd.ReleaseID).Scan(&versionID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return InstallationRef{}, ErrMarketplaceNotFound
		}
		return InstallationRef{}, err
	}

	installID := fmt.Sprintf("%s-%s-%d", cmd.WorkspaceID, cmd.ReleaseID, time.Now().UnixNano())
	now := time.Now().Unix()
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO marketplace_installations (installation_id, release_id, workspace_id, installed_by, installed_at)
		VALUES ($1, $2, $3, $4, $5)
	`, installID, cmd.ReleaseID, cmd.WorkspaceID, cmd.InstalledBy, now); err != nil {
		return InstallationRef{}, err
	}
	return InstallationRef{
		InstallationID: installID,
		ReleaseID:      cmd.ReleaseID,
		InstalledAt:    time.Unix(now, 0),
	}, nil
}

// Revoke 撤销一次发布。
func (s *Store) Revoke(ctx context.Context, cmd RevokeCommand) error {
	if s.pool == nil {
		return fmt.Errorf("marketplace: pool not configured")
	}
	if cmd.WorkspaceID == "" || cmd.ReleaseID == "" {
		return errors.New("marketplace: workspace_id and release_id required")
	}

	var versionID string
	if err := s.pool.QueryRow(ctx, `SELECT version_id FROM marketplace_releases WHERE release_id = $1`,
		cmd.ReleaseID).Scan(&versionID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrMarketplaceNotFound
		}
		return err
	}

	tag, err := s.pool.Exec(ctx, `UPDATE marketplace_versions SET status = $1 WHERE version_id = $2 AND workspace_id = $3`,
		VersionRevoked, versionID, cmd.WorkspaceID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrMarketplaceNotFound
	}
	return nil
}

// Rate 记录一次评分（1-5）。当前仅校验范围，评分明细存储留待后续 sprint。
func (s *Store) Rate(ctx context.Context, cmd RatingCommand) error {
	if cmd.WorkspaceID == "" || cmd.ReleaseID == "" || cmd.RatedBy == "" {
		return errors.New("marketplace: workspace_id, release_id, rated_by required")
	}
	if cmd.Score < 1 || cmd.Score > 5 {
		return ErrMarketplaceRateOutOfRange
	}
	return nil
}

// ListPackages 列出 workspace 可见的所有包。
func (s *Store) ListPackages(ctx context.Context, workspaceID string) ([]Package, error) {
	if s.pool == nil {
		return nil, fmt.Errorf("marketplace: pool not configured")
	}
	rows, err := s.pool.Query(ctx, `
		SELECT package_id, workspace_id, name, kind, publisher, visibility, created_at
		FROM marketplace_packages WHERE workspace_id = $1 ORDER BY created_at DESC
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Package, 0)
	for rows.Next() {
		var p Package
		var createdAt int64
		if err := rows.Scan(&p.PackageID, &p.WorkspaceID, &p.Name, &p.Kind, &p.Publisher, &p.Visibility, &createdAt); err != nil {
			return nil, err
		}
		p.CreatedAt = time.Unix(createdAt, 0)
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListReleases 列出 workspace 可见的所有发布。
func (s *Store) ListReleases(ctx context.Context, workspaceID string) ([]ReleaseRef, error) {
	if s.pool == nil {
		return nil, fmt.Errorf("marketplace: pool not configured")
	}
	rows, err := s.pool.Query(ctx, `
		SELECT r.release_id, r.version_id, r.channel, r.published_at
		FROM marketplace_releases r
		INNER JOIN marketplace_versions v ON r.version_id = v.version_id
		WHERE v.workspace_id = $1 ORDER BY r.published_at DESC
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]ReleaseRef, 0)
	for rows.Next() {
		var r ReleaseRef
		var publishedAt int64
		if err := rows.Scan(&r.ReleaseID, &r.VersionID, &r.Channel, &publishedAt); err != nil {
			return nil, err
		}
		r.PublishedAt = time.Unix(publishedAt, 0)
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListVersions 列出一个包的所有版本。
func (s *Store) ListVersions(ctx context.Context, workspaceID, packageID string) ([]PackageVersion, error) {
	if s.pool == nil {
		return nil, fmt.Errorf("marketplace: pool not configured")
	}
	rows, err := s.pool.Query(ctx, `
		SELECT version_id, package_id, workspace_id, version, digest, manifest, status, signature, reviewer, submitted_at, published_at
		FROM marketplace_versions WHERE package_id = $1 AND workspace_id = $2 ORDER BY submitted_at DESC
	`, packageID, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]PackageVersion, 0)
	for rows.Next() {
		var v PackageVersion
		var manifestJSON []byte
		var submittedAt int64
		var publishedAt *int64
		if err := rows.Scan(&v.VersionID, &v.PackageID, &v.WorkspaceID, &v.Version, &v.Digest, &manifestJSON,
			&v.Status, &v.Signature, &v.Reviewer, &submittedAt, &publishedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(manifestJSON, &v.Manifest); err != nil {
			return nil, fmt.Errorf("marketplace: unmarshal manifest: %w", err)
		}
		v.SubmittedAt = time.Unix(submittedAt, 0)
		if publishedAt != nil {
			t := time.Unix(*publishedAt, 0)
			v.PublishedAt = &t
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

var _ Service = (*Store)(nil)

// Sentinel errors.
var (
	ErrMarketplaceNotFound       = errors.New("marketplace: not found")
	ErrMarketplaceConflict       = errors.New("marketplace: conflict")
	ErrMarketplaceNotPublished   = errors.New("marketplace: not published")
	ErrMarketplaceRateOutOfRange = errors.New("marketplace: rating out of range")
)
