package server

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/redclaw"
)

// handleAuditExport GET /api/audit/export — 审计批量导出（P1）。
//
// 参数：
//
//	format   jsonl（默认）| csv
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

	query := redclaw.AuditQuery{
		TenantID:    claims.WorkspaceID, // 强制租户范围，忽略客户端参数
		Action:      r.URL.Query().Get("action"),
		AfterCursor: r.URL.Query().Get("cursor"),
	}

	if raw := r.URL.Query().Get("start"); raw != "" {
		ts, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			http.Error(w, "start must be RFC3339", http.StatusBadRequest)
			return
		}
		query.StartTime = ts
	}
	if raw := r.URL.Query().Get("end"); raw != "" {
		ts, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			http.Error(w, "end must be RFC3339", http.StatusBadRequest)
			return
		}
		query.EndTime = ts
	}
	if !query.StartTime.IsZero() && !query.EndTime.IsZero() && query.StartTime.After(query.EndTime) {
		http.Error(w, "start must not be after end", http.StatusBadRequest)
		return
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 1000 {
			http.Error(w, "limit must be an integer between 1 and 1000", http.StatusBadRequest)
			return
		}
		query.Limit = parsed
	}

	format := r.URL.Query().Get("format")
	if format == "" {
		format = "jsonl"
	}
	if format != "jsonl" && format != "csv" {
		http.Error(w, "format must be jsonl or csv", http.StatusBadRequest)
		return
	}

	page, err := s.auditStore.QueryRange(query)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if page.NextCursor != "" {
		w.Header().Set("X-Audit-Next-Cursor", page.NextCursor)
	}
	w.Header().Set("X-Audit-Count", strconv.Itoa(len(page.Entries)))
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=audit-export.%s", format))

	if format == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		cw := csv.NewWriter(w)
		if err := cw.Write([]string{"id", "timestamp", "action", "user_id", "tenant_id", "resource", "success", "duration_ms", "ip", "detail"}); err != nil {
			return
		}
		for _, e := range page.Entries {
			record := []string{
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
			}
			if err := cw.Write(record); err != nil {
				return
			}
		}
		cw.Flush()
		return
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	enc := json.NewEncoder(w)
	for _, e := range page.Entries {
		if err := enc.Encode(e); err != nil {
			return
		}
	}
}
