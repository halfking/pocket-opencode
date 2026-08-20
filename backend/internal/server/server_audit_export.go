package server

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/redclaw"
)

// auditFormat 描述 /api/audit/logs 与 /api/audit/export 共享的输出格式。
type auditFormat string

const (
	auditFormatJSON  auditFormat = "json"
	auditFormatJSONL auditFormat = "jsonl"
	auditFormatCSV   auditFormat = "csv"

	auditLogsMaxLimit     = 1000
	auditLogsDefaultLimit = 100
	auditExportMaxLimit   = 1000
	auditExportDefault    = 500
)

// parseAuditQuery 把 query string 解析成 redclaw.AuditQuery；attachment
// 为 true 时（export 端点）默认 format=jsonl / limit=500；为 false（logs
// 端点）时默认 format=json / limit=100。两个端点的参数校验、错误码、
// limit/cursor 行为完全一致；区别只在于默认 format、默认 limit 与
// 响应头。
func parseAuditQuery(r *http.Request, attachment bool) (redclaw.AuditQuery, auditFormat, error) {
	claims := sClaimsFromRequest(r)
	if claims == nil {
		return redclaw.AuditQuery{}, "", fmt.Errorf("unauthorized")
	}
	q := redclaw.AuditQuery{
		TenantID:    strings.TrimSpace(claims.WorkspaceID),
		Action:      r.URL.Query().Get("action"),
		AfterCursor: r.URL.Query().Get("cursor"),
	}
	if q.TenantID == "" {
		return redclaw.AuditQuery{}, "", fmt.Errorf("workspace is required")
	}
	if err := redclaw.ValidateAuditCursor(q.AfterCursor); err != nil {
		return redclaw.AuditQuery{}, "", err
	}
	if raw := r.URL.Query().Get("start"); raw != "" {
		ts, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return redclaw.AuditQuery{}, "", fmt.Errorf("start must be RFC3339")
		}
		q.StartTime = ts
	}
	if raw := r.URL.Query().Get("end"); raw != "" {
		ts, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return redclaw.AuditQuery{}, "", fmt.Errorf("end must be RFC3339")
		}
		q.EndTime = ts
	}
	if !q.StartTime.IsZero() && !q.EndTime.IsZero() && q.StartTime.After(q.EndTime) {
		return redclaw.AuditQuery{}, "", fmt.Errorf("start must not be after end")
	}

	maxLimit := auditLogsMaxLimit
	defaultLimit := auditLogsDefaultLimit
	if attachment {
		maxLimit = auditExportMaxLimit
		defaultLimit = auditExportDefault
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > maxLimit {
			return redclaw.AuditQuery{}, "", fmt.Errorf("limit must be an integer between 1 and %d", maxLimit)
		}
		q.Limit = parsed
	} else {
		q.Limit = defaultLimit
	}

	format := r.URL.Query().Get("format")
	if format == "" {
		if attachment {
			format = string(auditFormatJSONL)
		} else {
			format = string(auditFormatJSON)
		}
	}
	switch auditFormat(format) {
	case auditFormatJSON, auditFormatJSONL, auditFormatCSV:
		return q, auditFormat(format), nil
	}
	return redclaw.AuditQuery{}, "", fmt.Errorf("format must be json, jsonl, or csv")
}

// writeAuditPage 把一页 audit 渲染成对应格式并写入 w。attachment=true
// 时携带 Content-Disposition 与下载文件名（export 端点）；attachment=
// false 时只设 Content-Type（logs 端点）。
func writeAuditPage(w http.ResponseWriter, page *redclaw.AuditPage, format auditFormat, attachment bool) {
	if page == nil {
		page = &redclaw.AuditPage{}
	}
	if page.NextCursor != "" {
		w.Header().Set("X-Audit-Next-Cursor", page.NextCursor)
	}
	w.Header().Set("X-Audit-Count", strconv.Itoa(len(page.Entries)))

	switch format {
	case auditFormatCSV:
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		if attachment {
			w.Header().Set("Content-Disposition", "attachment; filename=audit-export.csv")
		}
		cw := csv.NewWriter(w)
		_ = cw.Write([]string{"id", "timestamp", "action", "user_id", "tenant_id", "resource", "success", "duration_ms", "ip", "detail"})
		for _, e := range page.Entries {
			_ = cw.Write([]string{
				e.ID,
				e.Timestamp.UTC().Format(time.RFC3339Nano),
				e.Action,
				e.UserID,
				e.TenantID,
				e.Resource,
				strconv.FormatBool(e.Success),
				strconv.FormatInt(e.DurationMs, 10),
				e.IP,
				e.Detail,
			})
		}
		cw.Flush()
	case auditFormatJSONL:
		w.Header().Set("Content-Type", "application/x-ndjson")
		if attachment {
			w.Header().Set("Content-Disposition", "attachment; filename=audit-export.jsonl")
		}
		enc := json.NewEncoder(w)
		for _, e := range page.Entries {
			_ = enc.Encode(e)
		}
	case auditFormatJSON:
		w.Header().Set("Content-Type", "application/json")
		if attachment {
			w.Header().Set("Content-Disposition", "attachment; filename=audit-export.json")
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"entries": page.Entries,
			"total":   len(page.Entries),
		})
	default:
	}
}

// handleAuditExport GET /api/audit/export — 审计批量导出（P1）。
//
// 参数（与 /api/audit/logs 一致）：
//
//	format   jsonl（默认）| json | csv
//	start    起始时间（RFC3339，闭区间；缺省 = 最早）
//	end      结束时间（RFC3339，开区间；缺省 = 至今）
//	cursor   上一页返回的 X-Audit-Next-Cursor（增量续传）
//	limit    每页条数，1-1000，默认 500
//	action   可选 action 过滤
//
// 权限：仅 admin，租户范围强制取自 JWT claims（忽略客户端 tenant_id）。
// 增量语义：QueryRange 二分定位起始时间，游标编码 (timestamp, id)，
// 导出方持久化游标即可避免全量重扫。
func (s *Server) handleAuditExport(w http.ResponseWriter, r *http.Request) {
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

	query, format, err := parseAuditQuery(r, true)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if query.EndTime.IsZero() {
		query.EndTime = time.Now()
	}
	query.ExcludeAction = "audit.export"

	page, err := s.auditStore.QueryRange(query)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// 自审计：记录「谁导了什么」，便于合规追溯。注意要写到当前 admin
	// 的 workspace，避免跨租户观测。
	s.Write(r, "audit.export", "audit",
		AuditFields{Success: true, Detail: fmt.Sprintf(
			"format=%s action=%s start=%s end=%s count=%d",
			format, query.Action,
			formatTimeRFC3339(query.StartTime), formatTimeRFC3339(query.EndTime),
			len(page.Entries),
		)})

	writeAuditPage(w, page, format, true)
}

func formatTimeRFC3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// sClaimsFromRequest 抽出当前请求的 claims；与 s.claimsFromContext 等价，
// 但显式接收 *Server 用于 parseAuditQuery 这种无 Server receiver 的 helper
// （避免在 query 解析阶段漏掉 tenant 强校验）。
//
// 注意：调用方必须确保 r 已经过 requireAuth；这里仅是访问 context 的便利
// 函数。
func sClaimsFromRequest(r *http.Request) *authClaims {
	return sClaimsFromContext(r.Context())
}

// sClaimsFromContext 抽出 context 里的 claims，与 Server.claimsFromContext
// 行为一致；保留独立函数是为了让 parseAuditQuery 不依赖 *Server receiver。
func sClaimsFromContext(ctx interface{ Value(any) any }) *authClaims {
	v := ctx.Value(authClaimsContextKey{})
	if v == nil {
		return nil
	}
	c, ok := v.(*authClaims)
	if !ok {
		return nil
	}
	return c
}
