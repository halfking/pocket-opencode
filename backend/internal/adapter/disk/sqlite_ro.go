package disk

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"

	// modernc.org/sqlite 是纯 Go 实现（无 cgo），保持 pocketd 可交叉编译到 linux。
	_ "modernc.org/sqlite"
)

// 本文件是 Wake crates/wake-core/src/adapters/sqlite_ro.rs 的 Go 移植。
//
// 读别家 agent 的 SQLite 有两条铁律：
//  1. 绝不写：一律 mode=ro 打开，也绝不发 DDL/DML；agent 正在跑时写入会污染
//     它的状态。
//  2. 绝不 immutable=1：WAL 并发写下 immutable 会读到撕裂页。
//
// 因此策略是「只读直开 → 探测查询验证可用 → 失败则拷 db/-wal/-shm 到临时目录
// 再只读打开 → 放弃」。临时目录由返回的 cleanup 负责删除。

// sqliteRO 是一个只读 SQLite 句柄；tmpDir 非空表示走了拷贝降级路径。
type sqliteRO struct {
	db     *sql.DB
	tmpDir string
}

// close 关闭连接并清理拷贝降级留下的临时目录。
func (s *sqliteRO) close() {
	if s == nil {
		return
	}
	if s.db != nil {
		_ = s.db.Close()
	}
	if s.tmpDir != "" {
		_ = os.RemoveAll(s.tmpDir)
	}
}

// roDSN 构造只读 DSN。busy_timeout 让 agent 正在写时短暂等待而不是立刻失败。
func roDSN(path string) string {
	return "file:" + url.PathEscape(path) + "?mode=ro&_pragma=busy_timeout(2000)"
}

// openSQLiteRO 只读打开 dbPath。tag 只用于临时目录命名，便于排查残留。
// 返回的句柄必须由调用方 close()。
func openSQLiteRO(ctx context.Context, dbPath, tag string) (*sqliteRO, error) {
	info, err := os.Stat(dbPath)
	if err != nil || info.IsDir() {
		return nil, fmt.Errorf("sqlite %s not available: %s", tag, dbPath)
	}

	// 1) 只读直开 + 探测查询（能列出 schema 才算真的可用）。
	if db, derr := sql.Open("sqlite", roDSN(dbPath)); derr == nil {
		if probeSQLite(ctx, db) == nil {
			return &sqliteRO{db: db}, nil
		}
		_ = db.Close()
	}

	// 2) 拷贝降级：db 三件套一起拷（缺 -wal 会丢最近的提交）。
	tmpDir, err := os.MkdirTemp("", "pocketd-disk-"+tag+"-")
	if err != nil {
		return nil, fmt.Errorf("sqlite %s: create temp dir: %w", tag, err)
	}
	copyPath := filepath.Join(tmpDir, "db.sqlite")
	if err := copyFile(dbPath, copyPath); err != nil {
		_ = os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("sqlite %s: copy db: %w", tag, err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		// 副档缺失是正常情况（非 WAL 模式），忽略错误。
		_ = copyFile(dbPath+suffix, copyPath+suffix)
	}

	db, err := sql.Open("sqlite", roDSN(copyPath))
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("sqlite %s: open copy: %w", tag, err)
	}
	if err := probeSQLite(ctx, db); err != nil {
		_ = db.Close()
		_ = os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("sqlite %s: probe copy: %w", tag, err)
	}
	return &sqliteRO{db: db, tmpDir: tmpDir}, nil
}

// probeSQLite 用一次 sqlite_master 计数验证连接真的可读。
func probeSQLite(ctx context.Context, db *sql.DB) error {
	var n int64
	return db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master").Scan(&n)
}

// copyFile 以只读方式读取 src、以 0600 写入 dst（dst 一定在临时目录内）。
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// virtualPath 给 SQLite 型数据源的会话造一个 `<db路径>#<会话id>` 虚拟路径：
// 磁盘上不存在，只用于展示与去重（与 Wake sqlite_ro::virtual_path 一致）。
func virtualPath(dbPath, id string) string {
	return dbPath + "#" + id
}
