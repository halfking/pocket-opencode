package marketplace

// blob.go — 内容寻址（sha256 hex）blob 存取。
//
// 设计见 docs/handoff/2026-09-05-marketplace-signing-chain-design.md §5：
//   - blob 表 marketplace_blobs 以 digest（裸 64 位小写 hex）为主键，
//     同 digest 重复上传天然去重（PutBlob 幂等返回首次的 meta）；
//   - 入参 digest 大小写不敏感、可选 "sha256:" 前缀，存储一律小写裸 hex；
//   - 单 blob 上限 MaxBlobSize（防御性）；
//   - 版本 digest 约定：marketplace_versions.digest 可为 "sha256:<hex>"
//     形式，GetBlobByRelease 负责规范化后定位 blob。
//
// MVP 后端为 PG bytea（整体存取）；S3/MinIO 等真流式后端留待后续 sprint。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// MaxBlobSize 是单个 blob 的防御性大小上限（64 MiB）。
const MaxBlobSize int64 = 64 << 20

// BlobMeta 描述一个内容寻址 blob 的元数据。
type BlobMeta struct {
	Digest      string    `json:"digest"`       // 规范化后的 sha256 裸 hex（64 字符小写）
	Size        int64     `json:"size"`         // 字节数
	ContentType string    `json:"content_type"` // MIME 类型；空入参落库为 application/octet-stream
	CreatedAt   time.Time `json:"created_at"`   // 首次上传时间
}

// Blob 专属 sentinel 错误。NotFound 语义复用包内 ErrMarketplaceNotFound。
var (
	// ErrBlobDigestMismatch：内容 sha256 与声明 digest 不符，或 digest 格式非法。
	ErrBlobDigestMismatch = errors.New("marketplace: blob digest mismatch")
	// ErrBlobTooLarge：内容超过 MaxBlobSize。
	ErrBlobTooLarge = errors.New("marketplace: blob too large")
)

// blobDigestKey 把任意合法 digest 入参规范化为 blob 表主键格式：
// 去除可选 "sha256:" 前缀、转小写，且必须恰好 64 位 hex。
// 不满足 → ErrBlobDigestMismatch。
func blobDigestKey(digest string) (string, error) {
	d := strings.ToLower(digest)
	d = strings.TrimPrefix(d, "sha256:")
	if len(d) != 64 {
		return "", fmt.Errorf("%w: digest must be 64 hex chars, got %d", ErrBlobDigestMismatch, len(d))
	}
	if _, err := hex.DecodeString(d); err != nil {
		return "", fmt.Errorf("%w: digest must be hex: %v", ErrBlobDigestMismatch, err)
	}
	return d, nil
}

// PutBlob 上传（或幂等复用）一个内容寻址 blob。
//
// 校验顺序（ADR §5）：digest 规范化 → sha256(content) 必须与声明 digest 一致
// （不符 ErrBlobDigestMismatch）→ 大小上限（超限 ErrBlobTooLarge）。
// contentType 为空时落库 "application/octet-stream"。
//
// 幂等：INSERT ... ON CONFLICT (digest) DO NOTHING 之后无条件回查，
// 同 digest 重复上传返回首次的 meta（不比较内容——digest 即内容标识）。
func (s *Store) PutBlob(ctx context.Context, digest string, content []byte, contentType string) (BlobMeta, error) {
	if s.pool == nil {
		return BlobMeta{}, fmt.Errorf("marketplace: pool not configured")
	}
	key, err := blobDigestKey(digest)
	if err != nil {
		return BlobMeta{}, err
	}
	sum := sha256.Sum256(content)
	if hex.EncodeToString(sum[:]) != key {
		return BlobMeta{}, fmt.Errorf("%w: content sha256 %s != declared %s",
			ErrBlobDigestMismatch, hex.EncodeToString(sum[:]), key)
	}
	if int64(len(content)) > MaxBlobSize {
		return BlobMeta{}, fmt.Errorf("%w: %d bytes exceeds limit %d", ErrBlobTooLarge, len(content), MaxBlobSize)
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	if _, err := s.pool.Exec(ctx, `
		INSERT INTO marketplace_blobs (digest, content, size, content_type, created_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (digest) DO NOTHING
	`, key, content, len(content), contentType, time.Now().Unix()); err != nil {
		return BlobMeta{}, err
	}

	// 无条件回查：新插入返回本次行，冲突时返回既有行（幂等语义）。
	var meta BlobMeta
	var createdAt int64
	err = s.pool.QueryRow(ctx, `
		SELECT digest, size, content_type, created_at FROM marketplace_blobs WHERE digest = $1
	`, key).Scan(&meta.Digest, &meta.Size, &meta.ContentType, &createdAt)
	if errors.Is(err, pgx.ErrNoRows) {
		// 理论不可达：INSERT 已在本连接语义内完成或命中既有行。
		return BlobMeta{}, ErrMarketplaceNotFound
	}
	if err != nil {
		return BlobMeta{}, err
	}
	meta.CreatedAt = time.Unix(createdAt, 0)
	return meta, nil
}

// GetBlob 按 digest 读取 blob。无行 → ErrMarketplaceNotFound。
func (s *Store) GetBlob(ctx context.Context, digest string) (BlobMeta, []byte, error) {
	if s.pool == nil {
		return BlobMeta{}, nil, fmt.Errorf("marketplace: pool not configured")
	}
	key, err := blobDigestKey(digest)
	if err != nil {
		return BlobMeta{}, nil, err
	}
	return s.getBlobByKey(ctx, key)
}

// getBlobByKey 按 blob 表主键（裸小写 hex）读回元数据与内容。
// MVP 将 bytea 整体读入内存（受 MaxBlobSize 约束）；真流式留待 S3 后端。
func (s *Store) getBlobByKey(ctx context.Context, key string) (BlobMeta, []byte, error) {
	var meta BlobMeta
	var createdAt int64
	var content []byte
	err := s.pool.QueryRow(ctx, `
		SELECT digest, size, content_type, created_at, content
		FROM marketplace_blobs WHERE digest = $1
	`, key).Scan(&meta.Digest, &meta.Size, &meta.ContentType, &createdAt, &content)
	if errors.Is(err, pgx.ErrNoRows) {
		return BlobMeta{}, nil, ErrMarketplaceNotFound
	}
	if err != nil {
		return BlobMeta{}, nil, err
	}
	meta.CreatedAt = time.Unix(createdAt, 0)
	return meta, content, nil
}

// GetBlobByRelease 读取一个 release 对应版本的内容 blob。
//
// 可见性与 Install 完全一致（同 workspace，或 package visibility='public'）；
// 不可见/不存在一律 ErrMarketplaceNotFound，不泄露存在性。
// release 的版本行存在但其 blob 尚未上传 → ErrMarketplaceNotFound。
func (s *Store) GetBlobByRelease(ctx context.Context, workspaceID, releaseID string) (BlobMeta, []byte, error) {
	if s.pool == nil {
		return BlobMeta{}, nil, fmt.Errorf("marketplace: pool not configured")
	}
	if workspaceID == "" || releaseID == "" {
		return BlobMeta{}, nil, errors.New("marketplace: workspace_id and release_id required")
	}

	// 一条 JOIN 定位 release → version → package，并校验可见性。
	// SQL 写法与 Install 的可见性判定保持一致。
	var versionDigest string
	err := s.pool.QueryRow(ctx, `
		SELECT v.digest
		FROM marketplace_releases r
		JOIN marketplace_versions v ON r.version_id = v.version_id
		JOIN marketplace_packages p ON v.package_id = p.package_id
		WHERE r.release_id = $1
		  AND (p.workspace_id = $2 OR p.visibility = 'public')
	`, releaseID, workspaceID).Scan(&versionDigest)
	if errors.Is(err, pgx.ErrNoRows) {
		return BlobMeta{}, nil, ErrMarketplaceNotFound
	}
	if err != nil {
		return BlobMeta{}, nil, err
	}

	// 版本 digest 允许带 "sha256:" 前缀/大写（blob 协议约定）；规范化后定位 blob。
	// 规范化失败说明版本行是 blob 协议落地之前的旧数据，按原样返回该错误。
	key, err := blobDigestKey(versionDigest)
	if err != nil {
		return BlobMeta{}, nil, err
	}
	return s.getBlobByKey(ctx, key)
}
