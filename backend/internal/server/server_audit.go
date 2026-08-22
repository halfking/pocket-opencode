package server

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/redclaw"
)

// handleAuditLogs GET /api/audit/logs — admin 视角下的工作区审计分页查询。
//
// 与 /api/audit/export 共享同一套 query 解析、tenant 强校验与分页语义；
// 区别仅在于默认 format=json、默认 limit=100，且响应不带 attachment 头。
//
// 参数：
//
//	format   json（默认）| jsonl | csv
//	start    起始时间（RFC3339，闭区间；缺省 = 最早）
//	end      结束时间（RFC3339，开区间；缺省 = 至今）
//	cursor   上一页返回的 X-Audit-Next-Cursor（增量续传）
//	limit    每页条数，1-1000，默认 100
//	action   可选 action 过滤
//
// 权限：仅 admin，租户范围强制取自 JWT claims（忽略客户端参数）。
func (s *Server) handleAuditLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.auditStore == nil {
		http.Error(w, "audit not configured", http.StatusServiceUnavailable)
		return
	}
	claims := s.claimsFromContext(r)
	if claims == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if claims.Role != "admin" {
		http.Error(w, "admin role required", http.StatusForbidden)
		return
	}

	query, format, err := parseAuditQuery(r, false)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	page, err := queryAuditRange(ctx, s.auditStore, query)
	if err != nil {
		status, message := auditQueryError(err)
		if status >= http.StatusInternalServerError {
			log.Printf("[audit] query logs failed: %v", err)
		}
		http.Error(w, message, status)
		return
	}

	writeAuditPage(w, page, format, false)
}

func auditQueryError(err error) (int, string) {
	switch {
	case errors.Is(err, redclaw.ErrInvalidAuditCursor):
		return http.StatusBadRequest, "invalid audit cursor"
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout, "audit query timed out"
	case errors.Is(err, context.Canceled):
		return http.StatusRequestTimeout, "audit query canceled"
	default:
		return http.StatusInternalServerError, "internal server error"
	}
}

func queryAuditRange(ctx context.Context, store redclaw.RangeStore, query redclaw.AuditQuery) (*redclaw.AuditPage, error) {
	if contextual, ok := store.(redclaw.RangeStoreContext); ok {
		return contextual.QueryRangeContext(ctx, query)
	}
	return store.QueryRange(query)
}
