package server

// server_diagnostics.go — kxmemory 诊断端点
//
// 提供 /api/diagnostics/kxmemory 端点，返回 kxmemory client 的运行统计。
// 之前在 kxmemory_test.go 中已有 handleDiagnosticsKxmemory 定义但丢失。
// 这里重新实现：直接用 kxmemory 包的 Stats 类型 + errors.As 探测。

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/halfking/pocket-opencode/backend/internal/kxmemory"
)

// kxmemoryDiagnostics 是诊断端点的响应结构。
type kxmemoryDiagnostics struct {
	Configured bool            `json:"configured"`
	Stats      *kxmemory.Stats `json:"stats"`
}

// handleDiagnosticsKxmemory 返回 kxmemory client 的运行统计。
//
// 响应示例（kxmemory 配置时）：
//
//	{
//	  "configured": true,
//	  "stats": {
//	    "success_count": 42, "retry_count": 3, "failure_count": 1,
//	    "breaker_state": "closed", "breaker_failures": 0,
//	    "last_error": "kxmemory /v1/notes/classify returned 502: ..."
//	  }
//	}
//
// 响应（kxmemory 未配置时）：
//
//	{ "configured": false, "stats": null }
func (s *Server) handleDiagnosticsKxmemory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	if s.kxmemory == nil {
		writeJSON(w, http.StatusOK, kxmemoryDiagnostics{Configured: false, Stats: nil})
		return
	}
	// 探测 kxmem 是否实现了 Stats()（HTTPClient 实现，Mock 不一定）
	var stats kxmemory.Stats
	type statsProvider interface{ Stats() kxmemory.Stats }
	if sp, ok := s.kxmemory.(statsProvider); ok {
		stats = sp.Stats()
	} else {
		writeJSON(w, http.StatusOK, kxmemoryDiagnostics{
			Configured: true,
			Stats:      &kxmemory.Stats{},
		})
		return
	}
	writeJSON(w, http.StatusOK, kxmemoryDiagnostics{
		Configured: true,
		Stats:      &stats,
	})
}

// 编译期检查
var _ = json.Marshal
var _ = errors.Is
