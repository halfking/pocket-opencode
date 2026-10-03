package learning

// 来源解析：把「一句 sourceKind + 一个 id」变成一条可入库的学习条目。
//
// 为什么需要它（docs/学习muse/05-实施路线图.md §3 / P2）：
// 「一键加入学习」如果要求客户端同时传 title / summary，入口就必须先读一遍
// 来源详情、拼好请求体——既啰嗦，又让客户端能伪造来源的标题。改成由服务端
// 解析来源，客户端只发 {sourceKind, sourceId}：
//
//	- 入口真的变成"一键"（一个按钮一个 id）；
//	- title/summary 一定与来源一致；
//	- 来源被删时返回错误，而不是留下一条空标题的学习条目（ADR-003 的反向保证）。
//
// 解析器是接口而非具体实现：learning 包不认识 notes/email/rss/meeting，
// 由 internal/learning/sources 负责适配，主程序在那里装配。

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrSourceNotFound means the source entity does not exist or is not visible
// to this user. The HTTP layer maps it to 404 — deliberately the same status as
// "you are not allowed to see it", so a probe cannot distinguish the two.
var ErrSourceNotFound = errors.New("learning: source not found")

// ErrNoResolver means no resolver was assembled (remote-only mode). The
// service then falls back to client-provided titles instead of failing.
var ErrNoResolver = errors.New("learning: no source resolver configured")

// ResolvedSource is the minimal projection of a source entity that the learning
// loop needs. It intentionally carries no body text: learning_items stores a
// reference plus a short summary, never a copy (ADR-003).
type ResolvedSource struct {
	Title   string
	Summary string
	// Tags are carried across when the source has them (notes/rss), so the
	// learning item is searchable by the same vocabulary as its origin.
	Tags []string
}

// SourceResolver turns (kind, id) into a title/summary projection.
//
// Implementations MUST scope the lookup by user/workspace. An unscoped getter
// would let one user file another user's email as their own study material,
// which is a data-exfiltration path, not a cosmetic bug.
type SourceResolver interface {
	Resolve(ctx context.Context, kind, sourceID, userID, workspaceID string) (*ResolvedSource, error)
}

// captureTitleFromSource fills the title/summary/tags of req from the resolver
// when the client did not provide them. A client-provided title always wins:
// this is a convenience for the one-click path, not a validation layer.
//
// It returns ErrSourceNotFound when a resolver is present but the source is
// gone, so the caller can answer 404 instead of storing a title-less item.
func (s *Service) captureTitleFromSource(ctx context.Context, wsID, userID string, req *CaptureRequest) error {
	if s == nil || s.resolver == nil {
		if req.Title == "" {
			return fmt.Errorf("title is required")
		}
		return nil
	}
	// A client that already knows the title does not need a source round-trip.
	// Only manual items legitimately have no source row.
	if req.Title != "" && req.SourceKind == string(SourceManual) {
		return nil
	}
	resolved, err := s.resolver.Resolve(ctx, req.SourceKind, req.SourceID, userID, normalizeWorkspace(wsID))
	if err != nil {
		if errors.Is(err, ErrSourceNotFound) {
			return err
		}
		// A resolver failure (PG hiccup) must not lose a user action: fall back
		// to the client title, and only fail when there is none at all.
		if req.Title != "" {
			return nil
		}
		return err
	}
	if req.Title == "" {
		req.Title = resolved.Title
	}
	if req.Summary == "" {
		req.Summary = truncate(resolved.Summary, summaryLimit)
	}
	if len(resolved.Tags) > 0 {
		req.Tags = resolved.Tags
	}
	return nil
}

// summaryLimit caps the stored summary. A 200-char cap matches the notes
// snippet convention (notes.Store writes ~200 chars) and keeps learning_items
// small enough to list without loading bodies.
const summaryLimit = 200

func truncate(s string, limit int) string {
	s = strings.TrimSpace(s)
	if len(s) <= limit {
		return s
	}
	// Cut on a rune boundary: a Chinese summary would otherwise end in a
	// replacement character when the byte cut lands mid-sequence.
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "…"
}
