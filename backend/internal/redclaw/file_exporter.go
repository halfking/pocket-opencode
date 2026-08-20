package redclaw

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// FileExporter 把内存审计日志增量落盘为 JSONL 文件（P1 遗留：审计导出落盘轮转）。
//
// 语义（对接外部 SIEM 的本地过渡方案）：
//   - 增量：游标持久化在 Dir/state.json，每轮只导出上次之后的新条目；
//     游标在每页写入后立即落盘，崩溃重启最多重复一页（at-least-once）。
//   - 轮转：文件按条目时间戳的 UTC 日期命名（audit-YYYYMMDD.jsonl），
//     跨天自然滚动；RetainDays 天之前的文件在每轮导出后清理。
//   - 范围：导出全部租户（运维侧文件由文件系统权限保护）；条目内容
//     遵循既有约定——敏感值不进入 Detail。
//
// 未启用时不产生任何 IO；由宿主（cmd/pocketd）通过 AUDIT_EXPORT_DIR 打开。
type FileExporter struct {
	store interface {
		QueryRange(query AuditQuery) (*AuditPage, error)
	}
	cfg FileExporterConfig

	mu    sync.Mutex
	state exporterState
}

type FileExporterConfig struct {
	Dir string
	// 导出轮询间隔；<=0 用默认 1 分钟。
	Interval time.Duration
	// 保留天数；<=0 用默认 7 天。文件按 mtime 判断过期。
	RetainDays int
	// 单页条数；<=0 用默认 1000（QueryRange 上限）。
	PageSize int
}

type exporterState struct {
	Cursor string `json:"cursor"`
}

func NewFileExporter(store interface {
	QueryRange(query AuditQuery) (*AuditPage, error)
}, cfg FileExporterConfig) *FileExporter {
	if cfg.Interval <= 0 {
		cfg.Interval = time.Minute
	}
	if cfg.RetainDays <= 0 {
		cfg.RetainDays = 7
	}
	if cfg.PageSize <= 0 {
		cfg.PageSize = 1000
	}
	return &FileExporter{store: store, cfg: cfg}
}

func queryAuditRangeContext(ctx context.Context, store interface {
	QueryRange(AuditQuery) (*AuditPage, error)
}, query AuditQuery) (*AuditPage, error) {
	if contextual, ok := store.(interface {
		QueryRangeContext(context.Context, AuditQuery) (*AuditPage, error)
	}); ok {
		return contextual.QueryRangeContext(ctx, query)
	}
	return store.QueryRange(query)
}

// Run 阻塞轮询直到 ctx 取消。单轮失败只记日志语义（返回被吞掉），
// 下一轮重试；游标推进保证不会重复导出大段数据。
func (f *FileExporter) Run(ctx context.Context) {
	ticker := time.NewTicker(f.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = f.ExportOnce(ctx)
		}
	}
}

// ExportOnce 导出所有新条目，返回写入的条数。
func (f *FileExporter) ExportOnce(ctx context.Context) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	// 目录不存在时自动创建（宿主只需给 AUDIT_EXPORT_DIR）。
	if err := os.MkdirAll(f.cfg.Dir, 0o700); err != nil {
		return 0, fmt.Errorf("audit exporter: mkdir: %w", err)
	}
	if err := f.loadStateLocked(); err != nil {
		return 0, fmt.Errorf("audit exporter: load state: %w", err)
	}

	written := 0
	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		page, err := queryAuditRangeContext(ctx, f.store, AuditQuery{AfterCursor: f.state.Cursor, Limit: f.cfg.PageSize})
		if err != nil {
			return written, fmt.Errorf("audit exporter: query: %w", err)
		}
		if len(page.Entries) == 0 && page.NextCursor == "" {
			break // 没有新条目
		}
		n, err := f.appendLocked(page.Entries)
		if err != nil {
			return written, err
		}
		written += n

		// 游标必须推进到本页最后一条（末页 NextCursor 为空，否则重启后会重复导出末页）。
		next := page.NextCursor
		if next == "" && len(page.Entries) > 0 {
			next = encodeAuditCursor(page.Entries[len(page.Entries)-1])
		}
		if next == "" || next == f.state.Cursor {
			break // 无进展保护
		}
		f.state.Cursor = next
		if err := f.saveStateLocked(); err != nil {
			return written, fmt.Errorf("audit exporter: save state: %w", err)
		}
		if page.NextCursor == "" {
			break // 末页
		}
	}

	if err := f.cleanupLocked(time.Now()); err != nil {
		return written, fmt.Errorf("audit exporter: cleanup: %w", err)
	}
	return written, nil
}

// appendLocked 按条目日期分文件追加写；同一天复用同一句柄连续写。
func (f *FileExporter) appendLocked(entries []*AuditEntry) (int, error) {
	byDate := make(map[string][]byte)
	for _, e := range entries {
		line, err := json.Marshal(e)
		if err != nil {
			return 0, fmt.Errorf("audit exporter: marshal %s: %w", e.ID, err)
		}
		name := "audit-" + e.Timestamp.UTC().Format("20060102") + ".jsonl"
		byDate[name] = append(byDate[name], append(line, '\n')...)
	}
	// 稳定顺序写出，便于测试与人工核对。
	names := make([]string, 0, len(byDate))
	for name := range byDate {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		path := filepath.Join(f.cfg.Dir, name)
		file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return 0, fmt.Errorf("audit exporter: open %s: %w", path, err)
		}
		if _, err := file.Write(byDate[name]); err != nil {
			file.Close()
			return 0, fmt.Errorf("audit exporter: write %s: %w", path, err)
		}
		if err := file.Close(); err != nil {
			return 0, fmt.Errorf("audit exporter: close %s: %w", path, err)
		}
	}
	return len(entries), nil
}

func (f *FileExporter) statePath() string {
	return filepath.Join(f.cfg.Dir, "state.json")
}

func (f *FileExporter) loadStateLocked() error {
	raw, err := os.ReadFile(f.statePath())
	if os.IsNotExist(err) {
		f.state.Cursor = ""
		return nil
	}
	if err != nil {
		return err
	}
	var st exporterState
	if err := json.Unmarshal(raw, &st); err != nil {
		return err
	}
	f.state = st
	return nil
}

func (f *FileExporter) saveStateLocked() error {
	raw, err := json.Marshal(f.state)
	if err != nil {
		return err
	}
	tmp := f.statePath() + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, f.statePath())
}

// cleanupLocked 删除保留期之外的 audit-*.jsonl（按文件修改时间）。
func (f *FileExporter) cleanupLocked(now time.Time) error {
	entries, err := os.ReadDir(f.cfg.Dir)
	if err != nil {
		return err
	}
	cutoff := now.AddDate(0, 0, -f.cfg.RetainDays)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, "audit-") || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			if err := os.Remove(filepath.Join(f.cfg.Dir, name)); err != nil {
				return err
			}
		}
	}
	return nil
}
