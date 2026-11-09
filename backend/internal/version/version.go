// Package version — openpocket 后端的构建期版本信息
//
// -----------------------------------------------------------------------------
// Purpose:        为 rule 71 统一日志标准提供必填的 version 字段。
//
// 服务的 version 必须是**构建期注入**的，禁止硬编码在源码里——硬编码的
// 版本号必然与实际发布物脱节，AI 归因时会把不同版本的同一故障混成一类。
// Replaces:       (无)
// Replaced-by:    (无)
// Status:         active
//
// Changelog:
//
// 2026-11-09  v1.0  首版。随 vibe-coding rule 71 落地。
//
// -----------------------------------------------------------------------------
//
// 构建期注入：
//
//	go build -ldflags "-X github.com/halfking/pocket-opencode/backend/internal/version.BuildVersion=1.2.3" ./cmd/pocketd
//
// 未注入时回落到 "dev"——仍是一个合法非空值，可让门禁通过，
// 同时明确表示「这不是发布构建」。
package version

// BuildVersion 由构建期通过 -ldflags -X 注入。
var BuildVersion = "dev"

// BuildCommit 由构建期通过 -ldflags -X 注入（可选，用于日志溯源）。
var BuildCommit = ""
