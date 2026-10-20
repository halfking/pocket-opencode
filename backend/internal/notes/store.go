package notes

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// maxListResults is the maximum number of notes returned by List to prevent
	// unbounded result sets in offline list rendering.
	maxListResults = 200
)

// Store is the PostgreSQL-backed local cache of voice-note metadata.
// AI processing (classification, SSOT, graph) happens in kxmemory; pocketd
// only caches metadata for offline list rendering. Migrated from SQLite in
// Phase 0 alongside the other module stores.
//
// The actual `notes` table in PG was created by a separate migration
// (docs/appendix-a-pg-migration.sql) and uses different types from what
// this store originally assumed: created_at / updated_at are
// `timestamp without time zone`, `tags` is
// `jsonb DEFAULT '[]'::jsonb`, and there are extra columns (`content`,
// `ai_summary`, `confidence_score`, `deleted_at`). This store now adapts
// to that schema: timestamps are converted at the boundary, tags are
// marshalled to / unmarshalled from jsonb arrays, and the `content`
// column is filled from `Note.Snippet` on insert (the Go-side Note model
// only carries Snippet today; full content lives in kxmemory).
//
// ⚠ §940：上面那句「`timestamp without time zone`」是**修复前**的状态 ——
// 三个时间列现在被 migrate() 统一转成 **timestamptz**（绝对时刻）。
// 原因与实测读数见 ensureTimestampColumns 的注释；一句话版本：
// 裸墙钟在「写按会话时区渲染 / 读按 UTC 解释」下往返偏一个时区差
// （实测 +8 小时）⇒ ⇒ 增量同步每轮都重推最近 8 小时的改动 ⇒ ⇒
// 成本 ∝ 8 小时改动量，而不是 ∝ 新增量。
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) (*Store, error) {
	s := &Store{pool: pool}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("notes migrate: %w", err)
	}
	return s, nil
}

func (s *Store) migrate() error {
	// Idempotent: table already exists in the DB (from appendix-a), so
	// CREATE TABLE IF NOT EXISTS is a no-op. ADD COLUMN IF NOT EXISTS
	// covers the (rare) fresh-install case.
	//
	// ⚠ §940：三个时间列现在直接声明成 **TIMESTAMPTZ**（新库一步到位）。
	// 已存在的旧库（列是 `timestamp without time zone`）由下面的
	// ensureTimestampColumns 转换 —— 见那里的完整说明。
	_, err := s.pool.Exec(context.Background(), `
	CREATE TABLE IF NOT EXISTS notes (
		id TEXT PRIMARY KEY,
		user_id TEXT NOT NULL,
		workspace_id TEXT DEFAULT 'default',
		title TEXT,
		content TEXT NOT NULL DEFAULT '',
		snippet TEXT,
		content_type TEXT DEFAULT 'voice',
		domain TEXT,
		tags JSONB DEFAULT '[]'::jsonb,
		audio_path TEXT,
		audio_duration INTEGER DEFAULT 0,
		created_by_voice BOOLEAN DEFAULT TRUE,
		ai_summary TEXT,
		confidence_score REAL,
		created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
		deleted_at TIMESTAMPTZ
	);
	ALTER TABLE notes ADD COLUMN IF NOT EXISTS content TEXT NOT NULL DEFAULT '';
	ALTER TABLE notes ADD COLUMN IF NOT EXISTS snippet TEXT;
	ALTER TABLE notes ADD COLUMN IF NOT EXISTS tags JSONB DEFAULT '[]'::jsonb;
	ALTER TABLE notes ADD COLUMN IF NOT EXISTS ai_summary TEXT;
	ALTER TABLE notes ADD COLUMN IF NOT EXISTS confidence_score REAL;
	ALTER TABLE notes ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;
	CREATE INDEX IF NOT EXISTS idx_notes_user_domain ON notes(user_id, domain);
	CREATE INDEX IF NOT EXISTS idx_notes_updated ON notes(updated_at DESC);
	`)
	if err != nil {
		return fmt.Errorf("notes store migration failed: %w", err)
	}
	// ⚠ §940：旧库的时间列是 `timestamp without time zone` ⇒ 必须转。
	if err := s.ensureTimestampColumns(context.Background()); err != nil {
		return fmt.Errorf("notes store timestamp migration failed: %w", err)
	}
	return nil
}

// notesTimestampColumns 是 notes 表里必须保持 timestamptz 的三个时间列。
//
// ⚠ 为什么是这三个：created_at/updated_at 决定**增量同步的水位线**
// （`GET /api/notes?since=` 按 updated_at 过滤），deleted_at 决定**墓碑**
// （`ListDeletedIDsScoped` 按 deleted_at 比对）⇒ ⇒ 三个都直接参与
// 「客户端能不能把改动同步到」这件事 ⇒ ⇒ 三个都必须是**绝对时刻**。
var notesTimestampColumns = []string{"created_at", "updated_at", "deleted_at"}

// notesTimestampAdvisoryLock 是 ensureTimestampColumns 用的事务级 advisory lock。
//
// ⚠⚠ §940：**这个锁的理由被实测推翻过一次** —— 原先的理由是错的，
//
//	记录在案，免得后人照着那个错故事改代码。
//
//	原说法：「类型转换**不是幂等**的 —— 第二个实例在第一个转完之后**再转一次**
//	⇒ ⇒ 会把已经正确的 instant 当成墙钟再解释一遍 ⇒ ⇒ 数据被**静默**平移
//	一个时区差。」
//
//	⇒ ⇒ **实测否掉了这个说法**：对**已经是 timestamptz** 的列再执行一次
//	`ALTER ... TYPE timestamptz USING col AT TIME ZONE current_setting('TimeZone')`
//	⇒ ⇒ epoch **一字不变**（把会话时区换成 America/New_York 再转一次也不变）
//	⇒ ⇒ ⇒ **值上是幂等的** ⇒ ⇒ ⇒ 双实例重复转换**不会**平移数据。
//	⇒ ⇒ ⇒ ⇒ 所以这个锁**不防漂移**。
//
// ⚠ 但**成本上不幂等** —— 这才是这个锁的真实理由（实测）：
//
//	每一次 `ALTER COLUMN ... TYPE` 都会**重写整张表**（实测 relfilenode 变）
//	⇒ ⇒ ACCESS EXCLUSIVE + 全表拷贝 + WAL ⇒ ⇒⇒ 成本 ∝ 表大小。
//	⇒ ⇒⇒ 多个实例同时启动 ⇒ 各自排一次全表重写 ⇒ ⇒⇒ 而它们转的是**同一张表**
//	⇒ ⇒⇒ 于是一张表被重写 N 次 ⇒ ⇒⇒ 成本 ∝ N × 表大小。
//
// ⇒ ⇒⇒ 用 advisory lock 把「查类型 + 转换」整段串行化 ⇒ ⇒ 第二个实例拿到锁时
// 看到的已经是**已转换**的类型 ⇒ ⇒ **直接跳过** ⇒ ⇒⇒ 全表只重写**一次**。
const notesTimestampAdvisoryLock = 94020261020

// notesColumnNeedsConversion 判定一个时间列**要不要**做类型转换。
//
// ⚠⚠ 它是**纯函数** ⇒ ⇒ ⇒ 好处有三个（都不是「为了写测试」才抽的）：
//
//	① **口径只有一份**：判断「这一列是不是已经是绝对时刻」这件事只有这一处
//	   ⇒ ⇒ 将来有人加第四个时间列，判据自动跟着走；
//	② **不依赖 PG 就能测** ⇒ ⇒ ⇒ CI 的 backend job（**没有 PG**）里
//	   这一段逻辑**仍然有门**（不是「整段只能靠真 PG 才验得了」）；
//	③ 变异脚本的锚点是**单行** ⇒ ⇒ 不会因为「中间多了一行注释」而失效
//	   （★ 本轮踩过：锚点凭记忆写 ⇒ 与真实文件差一行注释 ⇒ 0 命中）。
//
// ⚠ 类型名只认 PG 的 `format_type` 输出形态（实测：`timestamptz` 输出的是
// **"timestamp with time zone"** 而不是 "timestamptz"）⇒ ⇒ 别的类型**不猜**，
// 直接报错 ⇒ ⇒ 宁可停下来说明，也不要默默按错的类型解释。
func notesColumnNeedsConversion(typ string) (bool, error) {
	switch typ {
	case "timestamp with time zone":
		return false, nil // ✅ 已经是绝对时刻
	case "timestamp without time zone":
		return true, nil // 裸墙钟 ⇒ 必须转
	default:
		return false, fmt.Errorf("unexpected column type %s", typ)
	}
}

// ensureTimestampColumns 把 notes 表的时间列统一成 timestamptz。
//
// ⚠⚠ §940 的 D1（这是本函数存在的全部理由）：
//
// `timestamp without time zone` 在 PG 里是**裸墙钟**（naive wall clock）——
// 它**不携带时区**，PG 对它的处理是：
//
//	写：参数是 timestamptz（instant）⇒ PG 用**会话时区**把这个 instant
//	    **渲染成墙钟**再存进去；
//	    `CURRENT_TIMESTAMP` 同样是会话时区的墙钟。
//	读：pgx 把裸墙钟**按 UTC 解释** ⇒ ⇒ 往返误差 = 会话时区差。
//
// 实测（本机 PG 会话时区 Asia/Shanghai）：写进去再读回来，epoch 偏
// **+28800 秒（+8 小时）** ⇒ ⇒ 笔记的 updated_at 一律落在**未来 8 小时**
// ⇒ ⇒ `since` 过滤**永远命中** ⇒ ⇒ **每一轮增量轮询都把最近 8 小时的
// 改动重推一遍** ⇒ ⇒ 成本 ∝ **8 小时的改动量**，而不是 ∝ **新增量**
// ⇒ ⇒⇒ 这正是属主要求（「成本只取决于存储/新增量」）的反面。
//
// ⇒ ⇒ ⇒ 为什么是 timestamptz：它是**绝对时刻**，与会话时区无关；
// **全仓其余 store 用的都是它**（meeting / redclaw / lobster / auth /
// quota / finance / llm_gateway_nodes …）⇒ ⇒ notes 是**唯一的例外** ⇒
// ⇒ ⇒ ⇒ 这一步同时把 notes 拉回全仓口径。
//
// ⚠ 存量行的还原：`timestamp without time zone` 里的值是**写它那一刻的会话
// 墙钟** ⇒ ⇒ 要还原成 instant，必须用**当时的会话时区**去解释 ⇒ ⇒
// 这里用 `AT TIME ZONE current_setting('TimeZone')` ⇒ ⇒ **不硬编码 '-8 hours'**
// ⇒ ⇒ 换到别的部署时区上也不会错得更多。
// ⚠⚠ **如实登记的残余**：如果 PG 的会话时区在「写入」与「本次迁移」之间
// **被改过**，那批行会被还原成「按新时区解释的墙钟」⇒ ⇒ 仍偏一个时区差。
// ⇒ ⇒ 本机/生产的会话时区在观测期内**未变**（⛔ 未实测生产），故按当前
// 会话时区还原是**当下最准**的还原；真正的根治是让 PG 的时区**不再变动**。
//
// ⚠ 幂等：**值上**幂等（实测：重复转换 epoch 不变），**成本上**不幂等
// （实测：每次转换都重写整表）⇒ ⇒⇒ 所以「先查列类型、已是 timestamptz 就
// 直接跳过」这一步不是可有可无的优化：它保证**每次进程启动对 notes 表零 DDL**
// ——既不拿 ACCESS EXCLUSIVE，也不重写表。
// ⇒ ⇒⇒ 这一条由 store_timestamp_test.go 的
// 「第二次创建 store 不得重写 notes 表」判据钉住。
//
// ⚠⚠ DEFAULT **不需要**额外处理（实测，§940）：
//
//	PG 在转换列类型时会**自己**把默认值表达式适配到新类型：实测一个
//	`TIMESTAMP WITHOUT TIME ZONE DEFAULT CURRENT_TIMESTAMP` 的列转成
//	timestamptz 之后，`pg_get_expr` 仍然是 `CURRENT_TIMESTAMP`，且转换后
//	新插入的行拿到的是**正确的 instant**（epoch 与 `now()` 一致）。
//	⇒ ⇒⇒ 进一步实测：旧默认值若是 `now()::timestamp` 或 `localtimestamp`
//	（转换后仍保留裸墙钟形态），新插入的行**同样**是正确的 instant
//	——因为往 timestamptz 列赋值时那次转换**按会话时区**解释那个墙钟。
//	⇒ ⇒⇒ ⇒ 所以这里**不需要**再发一条 `SET DEFAULT CURRENT_TIMESTAMP`
//	⇒ ⇒⇒ ⇒ 那条 DDL 是「每次启动都要再拿一次 ACCESS EXCLUSIVE」的纯开销，
//	而且它当年被写进来时的理由（「PG 会保留那个 cast ⇒ 得显式重设」）
//	—— 经实测是**错的**（保留 cast 也不会让 instant 变错）。
func (s *Store) ensureTimestampColumns(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// ⚠ 并发护栏：见 notesTimestampAdvisoryLock 的说明。
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", int64(notesTimestampAdvisoryLock)); err != nil {
		return fmt.Errorf("advisory lock: %w", err)
	}

	// to_regclass('notes') 走 search_path 解析 ⇒ 命中的就是**本连接真正
	// 使用的那张表**（测试里是隔离 schema，生产是 public）。
	rows, err := tx.Query(ctx, `
		SELECT a.attname, format_type(a.atttypid, a.atttypmod)
		FROM pg_attribute a
		WHERE a.attrelid = to_regclass('notes')
		  AND a.attnum > 0 AND NOT a.attisdropped
		  AND a.attname = ANY($1)`, notesTimestampColumns)
	if err != nil {
		return fmt.Errorf("read column types: %w", err)
	}
	types := map[string]string{}
	for rows.Next() {
		var name, typ string
		if err := rows.Scan(&name, &typ); err != nil {
			rows.Close()
			return fmt.Errorf("scan column type: %w", err)
		}
		types[name] = typ
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate column types: %w", err)
	}

	for _, col := range notesTimestampColumns {
		typ, ok := types[col]
		if !ok {
			// 列不存在（不该发生：migrate() 的 ADD COLUMN IF NOT EXISTS 保证它在）
			continue
		}
		needs, err := notesColumnNeedsConversion(typ)
		if err != nil {
			return fmt.Errorf("notes.%s: %w", col, err)
		}
		if !needs {
			continue // ✅ 已经是绝对时刻
		}
		// ⚠ 列名来自上面的常量表，不是用户输入 ⇒ 拼进 DDL 是安全的。
		alter := fmt.Sprintf(
			"ALTER TABLE notes ALTER COLUMN %s TYPE timestamptz USING %s AT TIME ZONE current_setting('TimeZone')",
			col, col)
		if _, err := tx.Exec(ctx, alter); err != nil {
			return fmt.Errorf("alter %s: %w", col, err)
		}
	}
	// ⚠ 这里**故意没有**「SET DEFAULT CURRENT_TIMESTAMP」这一段 ——
	// 理由见本函数的文档注释（实测：PG 自己会适配默认值表达式；即便旧
	// 默认值仍是裸墙钟形态，往 timestamptz 列赋值时那次转换也按会话时区
	// 解释 ⇒ instant 仍然正确）⇒ ⇒⇒ 而那条 DDL 会让**每次启动**都再拿一次
	// ACCESS EXCLUSIVE ⇒ ⇒⇒ ⇒ 删掉它，「启动时对 notes 表零 DDL」才成立。
	return tx.Commit(ctx)
}

// Upsert caches or updates a note's metadata after kxmemory confirms it.
// allowedDomains mirrors the schema's CHECK (domain IN (...)).
// Package-level because both Upsert and UpdateNoteScoped must agree; a second
// copy in UpdateNoteScoped is exactly the kind of drift that lets an invalid
// domain through one path and not the other.
var allowedDomains = map[string]bool{"work": true, "study": true, "life": true, "idea": true}

// ErrStoreUnavailableNotes is returned when the store has no pool, i.e. the
// handler is wired but the backing store is not configured.
var ErrStoreUnavailableNotes = errors.New("notes: store not configured")

// ErrNoteOwnershipConflict 表示「这个 id 已经存在，但**不属于**调用方」。
//
// ⚠ §939：Upsert 的 ON CONFLICT 带了归属守卫（WHERE notes.user_id = $2），
// 撞上一个不属于自己的 id 时**一行都不会改**。这必须是一个**错误**
// 而不是静默成功 —— 静默成功的话，调用方会以为「笔记写好了」，
// 而用户的笔记其实一个字都没被保存（而且对方那行也被保住了）。
var ErrNoteOwnershipConflict = errors.New("notes: note id belongs to another user")

func (s *Store) Upsert(ctx context.Context, n *Note) error {
	// `content` 列是 TEXT NOT NULL，没有长度限制。2026-10-06 起真正写正文
	// （此前这里只写 n.Snippet，等于把整篇正文截成 200 字存进 content 列）。
	//
	// 回落到 Snippet 的两个原因：
	//   1. 旧调用方（会议 ingest 等）只填 Snippet，content 必须有值，
	//      否则撞 NOT NULL 约束；
	//   2. 存量行里 content 可能就是 snippet 拷过去的。
	content := n.Content
	if strings.TrimSpace(content) == "" {
		content = n.Snippet
	}

	// Convert epoch-second timestamps (Note.CreatedAt / UpdatedAt are int64
	// seconds) to time.Time for PG `timestamptz` columns.
	// ⚠ §940：列现在是 timestamptz（绝对时刻）⇒ ⇒ pgx 直接送 instant ⇒
	// 往返**精确**（修复前是裸墙钟 ⇒ 往返偏一个会话时区差，实测 +8h）。
	// CreatedAt == 0 means "not set" → let DB default kick in.
	var createdAt, updatedAt any
	if n.CreatedAt > 0 {
		createdAt = time.Unix(n.CreatedAt, 0).UTC()
	} else {
		createdAt = nil // NULL → DEFAULT CURRENT_TIMESTAMP
	}
	if n.UpdatedAt > 0 {
		updatedAt = time.Unix(n.UpdatedAt, 0).UTC()
	} else {
		updatedAt = nil
	}

	// tags: Note model holds a JSON-encoded array string. Actual column is
	// jsonb. Pass the []string form via pgx (it knows how to encode []string
	// into jsonb). If the JSON is malformed, fall back to empty array.
	var tagsVal any = []string{}
	if n.Tags != "" {
		var arr []string
		if err := json.Unmarshal([]byte(n.Tags), &arr); err == nil {
			tagsVal = arr
		}
	}

	// domain: schema has CHECK (domain IN ('work','study','life','idea')).
	// The Go Note.Domain defaults to "" when not set, which the CHECK
	// rejects. Only pass domain when it matches one of the allowed values;
	// otherwise pass NULL.
	var domainVal any
	if allowedDomains[n.Domain] {
		domainVal = n.Domain
	} else {
		domainVal = nil
	}

	// content_type: same — schema has CHECK (content_type IN
	// ('voice','text','mixed')). Default to "voice" if unspecified so
	// the column's NOT NULL + CHECK constraints both pass.
	contentType := n.ContentType
	if contentType != "voice" && contentType != "text" && contentType != "mixed" {
		contentType = "voice"
	}
	var contentTypeVal any = contentType

	// ⚠ §939：ON CONFLICT 的两个变化，缺一不可。
	//
	//	① WHERE notes.user_id = $2 —— **归属守卫**。
	//	  修复前 ON CONFLICT (id) DO UPDATE 不带任何谓词 ⇒ 撞上**别人的**
	//	  id 就会把那一行的 title/content/snippet **整个覆盖掉**
	//	  （user_id 列不在 SET 列表里 ⇒ 那行仍然属于原主人，但正文已经是
	//	  攻击者的了）⇒ ⇒ ⇒ 任何登录用户 POST /api/notes 带别人的 id
	//	  就能改别人的笔记。
	//	  §939 把会议笔记的 id 改成了**派生 id**（note_mtg_<meetingID>），
	//	  id 从「随机猜不到」变成「**可推算**」⇒ ⇒ ⇒ 这条攻击面必须
	//	  在同一节里关掉，否则等于自己开一个门。
	//	  ⇒ 守卫不成立时**一行都不改**（而不是改一行别人的）。
	//
	//	② workspace_id = EXCLUDED.workspace_id —— **可修复**。
	//	  修复前 SET 列表里没有 workspace_id ⇒ 一条**已经**落错租户的行
	//	  （比如 §939 之前落进 workspace_id='' 的那些）**永远改不回来**：
	//	  同一个 id 再写一次也只改别的列 ⇒ ⇒ ⇒ D1 的修法对存量行无效。
	//	  ⇒ 现在重精校一次就能把它搬回正确的租户。
	//	  ⚠ 守卫只判 user_id、不判 workspace_id ⇒ 「同一个用户的笔记在
	//	  另一个租户下」会被**搬回来**（这是修复，不是越权：行本来就属于
	//	  这个人）；「别人的行」则一行都动不了。
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO notes (id, user_id, workspace_id, title, content, snippet, content_type, domain, tags, audio_path, audio_duration, created_by_voice, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, COALESCE($13, CURRENT_TIMESTAMP), COALESCE($14, CURRENT_TIMESTAMP))
		ON CONFLICT (id) DO UPDATE SET
			title         = EXCLUDED.title,
			content       = EXCLUDED.content,
			snippet       = EXCLUDED.snippet,
			content_type  = EXCLUDED.content_type,
			domain        = EXCLUDED.domain,
			tags          = EXCLUDED.tags,
			workspace_id  = EXCLUDED.workspace_id,
			updated_at    = COALESCE($14, CURRENT_TIMESTAMP)
		WHERE notes.user_id = $2
	`,
		n.ID, n.UserID, n.WorkspaceID, n.Title, content, n.Snippet, contentTypeVal, domainVal, tagsVal, n.AudioPath, n.AudioDuration, n.CreatedByVoice, createdAt, updatedAt)
	if err != nil {
		return fmt.Errorf("upsert note %s: %w", n.ID, err)
	}
	// ⚠ 守卫不成立 ⇒ 一行都没动（撞 id 但不是同一个用户的行）⇒
	// 必须**报错**，否则调用方会以为「写成功了」。
	if tag.RowsAffected() == 0 {
		return ErrNoteOwnershipConflict
	}
	return nil
}

// ListScoped returns notes for one (user, workspace) pair. Use this instead of
// List on request paths: List filters on user_id only, so the same user in two
// workspaces would see both workspaces' notes.
func (s *Store) ListScoped(ctx context.Context, userID, workspaceID, domain string) ([]Note, error) {
	if workspaceID == "" {
		workspaceID = "default"
	}
	q := `
		SELECT id, user_id, workspace_id, title, content, snippet,
		       content_type, domain, tags, audio_path, audio_duration,
		       created_by_voice, created_at, updated_at
		FROM notes WHERE user_id = $1 AND workspace_id = $2 AND deleted_at IS NULL`
	args := []any{userID, workspaceID}
	if domain != "" {
		q += " AND domain = $3"
		args = append(args, domain)
	}
	q += " ORDER BY updated_at DESC LIMIT " + fmt.Sprintf("%d", maxListResults)

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query notes for user %s workspace %s: %w", userID, workspaceID, err)
	}
	defer rows.Close()
	return scanNoteRows(rows)
}

// Deprecated: List ignores workspace_id. Use ListScoped on any path that serves
// an authenticated request.
func (s *Store) List(ctx context.Context, userID, domain string) ([]Note, error) {
	// SELECT 现在包含 content 和 snippet — 修复 v1.0 期间遗留的字段缺失
	// bug（sync classify 路径曾因此拿到空 snippet 让真实 kxmemory 返回 400）。
	// 这两个列都加 NOT NULL DEFAULT '' 在 migrate() 里，所以向后兼容旧行。
	q := `
		SELECT id, user_id, workspace_id, title, content, snippet,
		       content_type, domain, tags, audio_path, audio_duration,
		       created_by_voice, created_at, updated_at
		FROM notes WHERE user_id = $1 AND deleted_at IS NULL`
	args := []any{userID}
	if domain != "" {
		q += " AND domain = $2"
		args = append(args, domain)
	}
	q += " ORDER BY updated_at DESC LIMIT " + fmt.Sprintf("%d", maxListResults)

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query notes for user %s: %w", userID, err)
	}
	defer rows.Close()
	return scanNoteRows(rows)
}

// scanNoteRows decodes the shared note SELECT column list. List and ListScoped
// both use it so the nullable-column handling stays in one place.
func scanNoteRows(rows pgx.Rows) ([]Note, error) {
	var out []Note
	for rows.Next() {
		var (
			n           Note
			workspaceID sql.NullString
			title       sql.NullString
			content     sql.NullString
			snippet     sql.NullString
			domain      sql.NullString
			tags        []byte // raw jsonb
			audioPath   sql.NullString
			createdAt   sql.NullTime
			updatedAt   sql.NullTime
		)
		if err := rows.Scan(&n.ID, &n.UserID, &workspaceID, &title, &content, &snippet,
			&n.ContentType, &domain, &tags, &audioPath, &n.AudioDuration, &n.CreatedByVoice,
			&createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan note row: %w", err)
		}
		if workspaceID.Valid {
			n.WorkspaceID = workspaceID.String
		}
		if title.Valid {
			n.Title = title.String
		}
		if content.Valid {
			n.Content = content.String // 正文与 snippet 是两列（2026-10-06）
		}
		if snippet.Valid {
			n.Snippet = snippet.String
		}
		if domain.Valid {
			n.Domain = domain.String
		}
		if audioPath.Valid {
			n.AudioPath = audioPath.String
		}
		if createdAt.Valid {
			n.CreatedAt = createdAt.Time.Unix()
		}
		if updatedAt.Valid {
			n.UpdatedAt = updatedAt.Time.Unix()
		}
		// tags: jsonb array → JSON string (matches Note model convention)
		if len(tags) > 0 {
			var arr []string
			if err := json.Unmarshal(tags, &arr); err == nil {
				b, _ := json.Marshal(arr)
				n.Tags = string(b)
			}
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate note rows: %w", err)
	}
	return out, nil
}

// GetByID 按 ID 查找单条笔记（不含软删除）。
//
// 返回 (nil, nil) 表示不存在（与 email.Store.GetEmailByID 行为一致），让
// handler 用 `if found == nil` 判断 404 而非依赖 error 类型。
//
// 用于替换 handleNoteClassify / handleNoteOperations 的 O(N) List + linear
// scan 反模式，避免每次 sync classify 都扫整张 notes 表。
// Deprecated: Use GetByIDScoped for production code with ownership checks.
func (s *Store) GetByID(ctx context.Context, id string) (*Note, error) {
	var (
		n           Note
		workspaceID sql.NullString
		title       sql.NullString
		content     sql.NullString
		snippet     sql.NullString
		domain      sql.NullString
		tags        []byte
		audioPath   sql.NullString
		createdAt   sql.NullTime
		updatedAt   sql.NullTime
	)
	err := s.pool.QueryRow(ctx, `
		SELECT id, user_id, workspace_id, title, content, snippet,
		       content_type, domain, tags, audio_path, audio_duration,
		       created_by_voice, created_at, updated_at
		FROM notes WHERE id = $1 AND deleted_at IS NULL
	`, id).Scan(
		&n.ID, &n.UserID, &workspaceID, &title, &content, &snippet,
		&n.ContentType, &domain, &tags, &audioPath, &n.AudioDuration, &n.CreatedByVoice,
		&createdAt, &updatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get note by id %s: %w", id, err)
	}
	if workspaceID.Valid {
		n.WorkspaceID = workspaceID.String
	}
	if title.Valid {
		n.Title = title.String
	}
	if content.Valid {
		n.Content = content.String // 正文与 snippet 是两列（2026-10-06）
	}
	if snippet.Valid {
		n.Snippet = snippet.String
	}
	if domain.Valid {
		n.Domain = domain.String
	}
	if audioPath.Valid {
		n.AudioPath = audioPath.String
	}
	if createdAt.Valid {
		n.CreatedAt = createdAt.Time.Unix()
	}
	if updatedAt.Valid {
		n.UpdatedAt = updatedAt.Time.Unix()
	}
	if len(tags) > 0 {
		var arr []string
		if err := json.Unmarshal(tags, &arr); err == nil {
			b, _ := json.Marshal(arr)
			n.Tags = string(b)
		}
	}
	return &n, nil
}

// GetByIDScoped 按 ID 和 workspace 查找单条笔记（不含软删除）。
func (s *Store) GetByIDScoped(ctx context.Context, id, userID, workspaceID string) (*Note, error) {
	var (
		n         Note
		wsID      sql.NullString
		title     sql.NullString
		content   sql.NullString
		snippet   sql.NullString
		domain    sql.NullString
		tags      []byte
		audioPath sql.NullString
		createdAt sql.NullTime
		updatedAt sql.NullTime
	)
	err := s.pool.QueryRow(ctx, `
		SELECT id, user_id, workspace_id, title, content, snippet,
		       content_type, domain, tags, audio_path, audio_duration,
		       created_by_voice, created_at, updated_at
		FROM notes WHERE id = $1 AND user_id = $2 AND workspace_id = $3 AND deleted_at IS NULL
	`, id, userID, workspaceID).Scan(
		&n.ID, &n.UserID, &wsID, &title, &content, &snippet,
		&n.ContentType, &domain, &tags, &audioPath, &n.AudioDuration, &n.CreatedByVoice,
		&createdAt, &updatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get note by id %s: %w", id, err)
	}
	if wsID.Valid {
		n.WorkspaceID = wsID.String
	}
	if title.Valid {
		n.Title = title.String
	}
	// content 与 snippet 是**两列**，不是同一份数据的两个读法（2026-10-06）：
	// 此前两行都往 n.Snippet 写，content 被 snippet 覆盖掉，于是全仓
	// 拿不到超过 200 字的正文。现在 content 进它自己的字段。
	if content.Valid {
		n.Content = content.String
	}
	if snippet.Valid {
		n.Snippet = snippet.String
	}
	if domain.Valid {
		n.Domain = domain.String
	}
	if audioPath.Valid {
		n.AudioPath = audioPath.String
	}
	if createdAt.Valid {
		n.CreatedAt = createdAt.Time.Unix()
	}
	if updatedAt.Valid {
		n.UpdatedAt = updatedAt.Time.Unix()
	}
	if len(tags) > 0 {
		var arr []string
		if err := json.Unmarshal(tags, &arr); err == nil {
			b, _ := json.Marshal(arr)
			n.Tags = string(b)
		}
	}
	return &n, nil
}

// NotePatch is a partial update for a note. Only non-nil fields are written,
// so an explicit empty string clears a value while nil means "leave as is".
// Mirrors scheduledtask.TaskInput's convention.
type NotePatch struct {
	Title         *string
	Content       *string // also refreshes Snippet
	ContentType   *string
	Domain        *string
	Tags          *string // JSON array string
	AudioPath     *string
	AudioDuration *int
}

// ErrNoteNotFound is returned by UpdateNoteScoped when no row matches the
// (id, user, workspace, not-deleted) tuple. Distinct from a DB error so the
// HTTP layer can answer 404 instead of 500.
var ErrNoteNotFound = errors.New("notes: note not found")

// snippetRunes is the snippet length the local cache uses for list rendering.
const snippetRunes = 200

// UpdateNoteScoped applies a partial update to one note, constrained to the
// (id, user, workspace) tuple and to non-deleted rows.
//
// BUG-N（2026-09-30 由 scripts/probe-write-methods.mjs 的 method 级探测发现）：
// 前端 frontend/src/api/notes.ts 的 `update()` 打 PUT /api/notes/:id，但后端
// handleNoteOperations 此前只有 GET/DELETE，notes.Store 也没有任何更新方法。
// 结果是**编辑笔记在真机上恒 405**，功能完全不可用。
//
// 两个刻意的行为约定：
//   - Content 变更时同步重算 Snippet。列表摘要读的是 snippet；不同步的话
//     "编辑后标题还在列表"这种断言会恒真（标题没动），掩盖正文根本没存上。
//     断言必须能看见新正文。
//   - 所有权进 UPDATE 谓词，不用先查后写。跨用户/跨 workspace 改不动。
func (s *Store) UpdateNoteScoped(ctx context.Context, id, userID, workspaceID string, patch NotePatch) (*Note, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreUnavailableNotes
	}

	sets := []string{}
	args := []any{id, userID, workspaceID}
	add := func(col string, val any) {
		args = append(args, val)
		sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)))
	}

	if patch.Title != nil {
		add("title", *patch.Title)
	}
	if patch.Content != nil {
		add("content", *patch.Content)
		// Snippet mirrors the head of content; DB stores seconds, we store the
		// same truncated preview the list view renders.
		snip := []rune(*patch.Content)
		if len(snip) > snippetRunes {
			snip = snip[:snippetRunes]
		}
		add("snippet", string(snip))
	}
	if patch.ContentType != nil {
		ct := *patch.ContentType
		// schema CHECK (content_type IN ('voice','text','mixed'))
		if ct != "voice" && ct != "text" && ct != "mixed" {
			return nil, fmt.Errorf("notes: invalid contentType %q", ct)
		}
		add("content_type", ct)
	}
	if patch.Domain != nil {
		d := *patch.Domain
		// schema CHECK (domain IN ('work','study','life','idea'))
		if d == "" {
			add("domain", nil)
		} else if allowedDomains[d] {
			add("domain", d)
		} else {
			return nil, fmt.Errorf("notes: invalid domain %q", d)
		}
	}
	if patch.Tags != nil {
		// column is jsonb; pass []string so pgx encodes it natively.
		arr := []string{}
		if *patch.Tags != "" {
			if err := json.Unmarshal([]byte(*patch.Tags), &arr); err != nil {
				return nil, fmt.Errorf("notes: tags must be a JSON array string: %w", err)
			}
		}
		add("tags", arr)
	}
	if patch.AudioPath != nil {
		add("audio_path", *patch.AudioPath)
	}
	if patch.AudioDuration != nil {
		add("audio_duration", *patch.AudioDuration)
	}

	if len(sets) == 0 {
		// Nothing to change: return the current row rather than erroring, so
		// an empty PATCH is a harmless no-op instead of a 4xx the UI can't
		// distinguish from a real failure.
		return s.GetByIDScoped(ctx, id, userID, workspaceID)
	}

	sets = append(sets, "updated_at = CURRENT_TIMESTAMP")
	sql := fmt.Sprintf(`
		UPDATE notes SET %s
		WHERE id = $1 AND user_id = $2 AND workspace_id = $3 AND deleted_at IS NULL
	`, strings.Join(sets, ", "))

	tag, err := s.pool.Exec(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("update note %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNoteNotFound
	}
	return s.GetByIDScoped(ctx, id, userID, workspaceID)
}

func (s *Store) Delete(ctx context.Context, id string) error {
	// Soft-delete: keep the row, set deleted_at. Avoids breaking FK
	// relationships in other tables that may reference notes.id in the
	// future, and matches the actual schema's idx_notes_* `WHERE
	// deleted_at IS NULL` partial-index design.
	// Deprecated: Use DeleteScoped for production code with ownership checks.
	_, err := s.pool.Exec(ctx, `UPDATE notes SET deleted_at = CURRENT_TIMESTAMP WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete note %s: %w", id, err)
	}
	return nil
}

// DeleteScoped soft-deletes a note with ownership verification
func (s *Store) DeleteScoped(ctx context.Context, id, userID, workspaceID string) error {
	result, err := s.pool.Exec(ctx, `
		UPDATE notes SET deleted_at = CURRENT_TIMESTAMP 
		WHERE id = $1 AND user_id = $2 AND workspace_id = $3 AND deleted_at IS NULL
	`, id, userID, workspaceID)
	if err != nil {
		return fmt.Errorf("delete note %s: %w", id, err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("note not found or already deleted")
	}
	return nil
}

func (s *Store) Close() error { return nil }

// ListDeletedIDsScoped 返回 deleted_at 晚于 sinceSec（epoch 秒）的软删除
// 笔记 id（墓碑清单），供客户端把「其他端已删除」的行从本地缓存移除。
// 见 docs/2026-09-09-list-sync-rules.md §3.2。
func (s *Store) ListDeletedIDsScoped(ctx context.Context, userID, workspaceID string, sinceSec int64, limit int) ([]string, error) {
	if sinceSec <= 0 {
		return nil, nil
	}
	if workspaceID == "" {
		workspaceID = "default"
	}
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id FROM notes
		WHERE user_id = $1 AND workspace_id = $2
		  AND deleted_at IS NOT NULL AND deleted_at > to_timestamp($3)
		ORDER BY deleted_at ASC LIMIT $4`,
		userID, workspaceID, sinceSec, limit)
	if err != nil {
		return nil, fmt.Errorf("query deleted note ids: %w", err)
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
