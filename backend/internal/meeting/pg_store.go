// internal/meeting/pg_store.go
package meeting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGStore 会议记录存储的 PostgreSQL 实现。
//
// 为什么需要它：`Store`（内存实现）是 `server.New` 里的默认值，**进程一重启
// 全部会议记录归零**。会议记录里有逐字稿、摘要、待办清单与关键决策，是这个
// 产品里「丢了就找不回来」代价最高的一类数据，却和聊天摘要一样只活在内存里。
// 本文件按 `finance.NewPGStore` 的既有范式补上 PG 版，`cmd/pocketd` 在 pool
// 就绪时经 `Server.SetMeetingStore` 注入；未注入时仍退回内存版，测试环境零依赖。
type PGStore struct {
	pool *pgxpool.Pool
}

// Compile-time proof that the PG implementation structurally satisfies the
// same interface the in-memory Store does. Without this, dropping a method on
// either side would only surface at the injection site in main.go — and only
// when Postgres happened to be configured in that build.
var (
	_ MeetingStore = (*PGStore)(nil)
	_ MeetingStore = (*Store)(nil)
)

// NewPGStore 创建 PostgreSQL 会议存储实例，并自动执行 migration。
func NewPGStore(ctx context.Context, pool *pgxpool.Pool) (*PGStore, error) {
	if pool == nil {
		return nil, fmt.Errorf("meeting store: postgres pool is nil")
	}
	s := &PGStore{pool: pool}
	if err := s.migrate(ctx); err != nil {
		return nil, fmt.Errorf("meeting migration failed: %w", err)
	}
	return s, nil
}

func (s *PGStore) migrate(ctx context.Context) error {
	schema := `
CREATE TABLE IF NOT EXISTS meetings (
  id TEXT PRIMARY KEY,
  owner_id TEXT NOT NULL,
  workspace_id TEXT NOT NULL,
  title TEXT NOT NULL,
  duration INTEGER NOT NULL DEFAULT 0,
  recording_url TEXT NOT NULL DEFAULT '',
  transcript TEXT NOT NULL DEFAULT '',
  summary TEXT NOT NULL DEFAULT '',
  key_decisions JSONB NOT NULL DEFAULT '[]'::jsonb,
  action_items JSONB NOT NULL DEFAULT '[]'::jsonb,
  tags JSONB NOT NULL DEFAULT '[]'::jsonb,
  project_id TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'recording',
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_meetings_owner_ws ON meetings(owner_id, workspace_id);
CREATE INDEX IF NOT EXISTS idx_meetings_created ON meetings(created_at DESC);
-- 墓碑表：删除也要能跨重启参与增量同步，否则被删的会议会在下一次
-- 增量拉取时被客户端当成"服务器上还在"重新显示出来。
CREATE TABLE IF NOT EXISTS meeting_tombstones (
  id TEXT PRIMARY KEY,
  owner_id TEXT NOT NULL,
  workspace_id TEXT NOT NULL,
  deleted_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_meeting_tombstones_owner_ws
  ON meeting_tombstones(owner_id, workspace_id, deleted_at DESC);
	`
	if _, err := s.pool.Exec(ctx, schema); err != nil {
		return err
	}
	// ★ 2026-10-07：CREATE TABLE IF NOT EXISTS **不会**给已存在的表补列，
	// 所以这三列必须单独 ALTER。加 IF NOT EXISTS 让它对新建/旧库都幂等。
	// participants 用 JSONB，与 key_decisions/action_items/tags 一致。
	const extraCols = `
ALTER TABLE meetings ADD COLUMN IF NOT EXISTS location     TEXT NOT NULL DEFAULT '';
ALTER TABLE meetings ADD COLUMN IF NOT EXISTS participants JSONB NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE meetings ADD COLUMN IF NOT EXISTS note_id      TEXT NOT NULL DEFAULT '';
`
	_, err := s.pool.Exec(ctx, extraCols)
	return err
}

// nextMeetingID 生成会议 ID。
//
// 与内存版共用 `mtg_<nano>_<seq>` 格式和同一个进程内序号，理由见 store.go 里
// meetingIDSeq 的注释（Windows 上 time.Now() 没有纳秒精度，纯 nano 会撞号）。
// 内存版撞号是"静默覆盖前一条"，PG 版则会被 PRIMARY KEY 挡住；为了让 ID 仍然
// 是稳定的主键而不是偶发 500，这里对唯一冲突做有限次重试。
func nextMeetingID() string {
	return fmt.Sprintf("mtg_%d_%d", time.Now().UnixNano(), meetingIDSeq.Add(1))
}

// pgTruncate 把一个时刻**向下**截断到微秒。
//
// ⚠⚠ §964 为什么需要它（这是 §963 之后剩下的那个洞）：
//
//	PG 的 `timestamptz` **物理上只能存微秒** ⇒ 存进去的亚微秒部分会被
//	**四舍五入**掉（PG 用 round-half-to-even：实测 .7µs 进位 / .3µs 舍去）。
//	⇒ 若把 `time.Now()` 的**纳秒**值原样写进去，
//	  「内存里返回给调用方的时刻」与「库里真正存的那一刻」**不相等**
//	  ⇒ 判据一比就红（实测 Linux CI 上 `TestPGUpdateMetaScopedLeavesTranscriptUntouched` 红）。
//
// ★ 关键在「**向下**截断」而不是「四舍五入」：
//
//	截断后的值**本身没有亚微秒部分** ⇒ PG 再存它时**不产生任何取整**
//	⇒ 内存值 == 库值 ⇒ **判据可以保持严格等值**（不必放宽）。
//	若改成四舍五入，截断后的值仍可能落在 .5µs 边界上 ⇒ PG 会再动它一次
//	⇒ 严格等值立刻又不成立。
//
// ⚠ 截断会丢掉 <1µs ⇒ 对业务毫无影响（会议时间点的精度需求是分钟级）。
// ⚠ `Truncate` 以**零时刻**为基准向下取整 ⇒ 对 1970 年之后的时间戳
//
//	就是「按 epoch 向下取整到微秒」⇒ 与 PG 的取整方向一致。
func pgTruncate(t time.Time) time.Time {
	return t.Truncate(time.Microsecond)
}

// pgNow 取「写进 PG 的那一刻」的时间戳，**截断到微秒**。
//
// ⇒ 凡是**写进 PG 的时间戳**都必须走它（别再直接调 `time.Now()`），
//   否则「写进去的值」与「返回给调用方的值」会差一个亚微秒量。
// ⚠ `nextMeetingID()` 里的 `time.Now().UnixNano()` **不归它管**
//
//	—— 那是 ID 唯一性用途，与时间戳精度无关（且 UnixNano 取的是 epoch 纳秒，
//	  截断它反而会削弱唯一性）。
func pgNow() time.Time {
	return pgTruncate(time.Now())
}

const meetingSelectCols = `id, owner_id, workspace_id, title, duration,
	recording_url, transcript, summary,
	COALESCE(key_decisions, '[]'::jsonb), COALESCE(action_items, '[]'::jsonb), COALESCE(tags, '[]'::jsonb),
	project_id, status,
	location, COALESCE(participants, '[]'::jsonb), note_id,
	created_at, updated_at`

// listScopedQuery 是 PG 版的列表 SQL（§936）。
//
// ⚠⚠ **子句顺序是语义**（§936 的 D2）：`updated_at > $3` 必须在 `LIMIT` **之前**。
//
//	修复前：SELECT … ORDER BY created_at DESC LIMIT 500   ← 先截断
//	        （since 过滤在 Go 里、查完之后才做）
//	⇒ ⇒ 一场**创建于 500 名之外**但**刚被更新**的会议不在 newest-500 里
//	⇒ ⇒ 它永远同步不到更新（而且 `ORDER BY created_at` ⇒ 被更新的老会议
//	排序不变 ⇒ 用户端「已精校」徽章永远不亮）
//
//	修复后：先过滤 → 再排序 → 再 LIMIT ⇒ 增量只省响应体，**也省 DB 读**。
//
// ⚠ 排序带 `id DESC` 的 tie-break：created_at 精度到微秒，同批写入会撞时刻
// ⇒ 只按 created_at 排的话顺序不确定 ⇒ 与内存版的逐字一致就做不到。
const listScopedQuery = `SELECT ` + meetingSelectCols + `
	FROM meetings
	WHERE owner_id=$1 AND workspace_id=$2
	  AND ($3::timestamptz IS NULL OR updated_at > $3)
	ORDER BY created_at DESC, id DESC
	LIMIT $4 OFFSET $5`

// listScopedCountQuery 是同一次查询的**总条数**（§936 的 D1）。
//
// ⚠ 它必须与 listScopedQuery 用**同一个**过滤条件（同一个 $3 口径）⇒ ⇒
// 否则 total 与 meetings 数组对不上（分页器会算错页数）。
// ⚠ 它**不含** LIMIT/OFFSET ⇒ total 恒为「过滤后的全部条数」。
const listScopedCountQuery = `SELECT COUNT(*)
	FROM meetings
	WHERE owner_id=$1 AND workspace_id=$2
	  AND ($3::timestamptz IS NULL OR updated_at > $3)`

func scanMeeting(row pgx.Row) (*Meeting, error) {
	var (
		m           Meeting
		rawDecision []byte
		rawActions  []byte
		rawTags     []byte
		rawParts    []byte
	)
	if err := row.Scan(
		&m.ID, &m.OwnerID, &m.WorkspaceID, &m.Title, &m.Duration,
		&m.RecordingURL, &m.Transcript, &m.Summary,
		&rawDecision, &rawActions, &rawTags,
		&m.ProjectID, &m.Status,
		&m.Location, &rawParts, &m.NoteID,
		&m.CreatedAt, &m.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if len(rawParts) > 0 {
		if err := json.Unmarshal(rawParts, &m.Participants); err != nil {
			return nil, fmt.Errorf("decode participants: %w", err)
		}
	}
	// 三个 JSONB 列的 COALESCE 已保证非 NULL，但历史行或手工插入仍可能是
	// SQL NULL；空字节切片会让 json.Unmarshal 直接报错，所以先挡一层。
	if len(rawDecision) > 0 {
		if err := json.Unmarshal(rawDecision, &m.KeyDecisions); err != nil {
			return nil, fmt.Errorf("decode key_decisions: %w", err)
		}
	}
	if len(rawActions) > 0 {
		if err := json.Unmarshal(rawActions, &m.ActionItems); err != nil {
			return nil, fmt.Errorf("decode action_items: %w", err)
		}
	}
	if len(rawTags) > 0 {
		if err := json.Unmarshal(rawTags, &m.Tags); err != nil {
			return nil, fmt.Errorf("decode tags: %w", err)
		}
	}
	return &m, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if ok := asPgError(err, &pgErr); !ok {
		return false
	}
	return pgErr.Code == "23505"
}

func asPgError(err error, target **pgconn.PgError) bool {
	for err != nil {
		if pgErr, ok := err.(*pgconn.PgError); ok {
			*target = pgErr
			return true
		}
		unwrapped, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapped.Unwrap()
	}
	return false
}

func (s *PGStore) CreateScoped(req CreateMeetingRequest, ownerID, workspaceID string) (*Meeting, error) {
	if strings.TrimSpace(req.Title) == "" {
		return nil, fmt.Errorf("title cannot be empty")
	}
	if ownerID == "" {
		return nil, fmt.Errorf("owner_id is required")
	}
	if workspaceID == "" {
		return nil, fmt.Errorf("workspace_id is required")
	}

	decisions, err := json.Marshal([]string{})
	if err != nil {
		return nil, err
	}
	actions, err := json.Marshal([]ActionItem{})
	if err != nil {
		return nil, err
	}
	tags, err := json.Marshal([]string{})
	if err != nil {
		return nil, err
	}

	participants, err := json.Marshal(req.Participants)
	if err != nil {
		return nil, err
	}
	if req.Participants == nil {
		participants = []byte("[]")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// ★ 2026-10-07：客户端带的 id 现在被尊重，且**重复 POST 变成幂等更新**。
	// 改这一条的同时解决了「同一个会议重复建行」：旧实现无条件 nextMeetingID()，
	// 客户端重试一次就多一行，而 Maestro 判 visible 只看节点在不在树里 ⇒ 重复行
	// 会让列表断言假通过。ON CONFLICT (id) DO UPDATE 让重试收敛到同一行。
	//
	// created_at 用客户端的 startedAt（Unix 毫秒），没有就用 now；
	// duration 由毫秒换算成秒；refinedTranscript 落到 transcript 列。
	createdAt := pgNow()
	if req.StartedAt > 0 {
		createdAt = time.UnixMilli(req.StartedAt)
	}
	durationSec := int(req.DurationMs / 1000)
	transcript := req.RefinedTranscript
	status := req.Status
	if status == "" {
		status = "recording"
	}

	const q = `INSERT INTO meetings
		(id, owner_id, workspace_id, title, duration, recording_url, transcript, summary,
		 key_decisions, action_items, tags, project_id, status,
		 location, participants, note_id, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,'',$6,$7,$8,$9,$10,'',$11,$12,$13,$14,$15,$16)
		ON CONFLICT (id) DO UPDATE SET
			title=EXCLUDED.title, duration=EXCLUDED.duration,
			transcript=CASE WHEN EXCLUDED.transcript<>'' THEN EXCLUDED.transcript ELSE meetings.transcript END,
			summary=CASE WHEN EXCLUDED.summary<>'' THEN EXCLUDED.summary ELSE meetings.summary END,
			status=EXCLUDED.status, location=EXCLUDED.location,
			participants=EXCLUDED.participants, note_id=EXCLUDED.note_id,
			updated_at=EXCLUDED.updated_at
		WHERE meetings.owner_id = EXCLUDED.owner_id
		  AND meetings.workspace_id = EXCLUDED.workspace_id
		RETURNING ` + meetingSelectCols

	// ⚠⚠ §943：ON CONFLICT 的 **归属守卫**（缺一不可，缺了就是越权写入）。
	//
	//	WHERE meetings.owner_id = EXCLUDED.owner_id
	//	  AND meetings.workspace_id = EXCLUDED.workspace_id
	//
	//	修复前这条 ON CONFLICT **不带任何谓词** ⇒ 撞上一个**别的作用域**的
	//	id 时，它把**那一行整个改掉**（title / transcript / summary / status /
	//	location / participants / note_id 全部换成提交者的内容，而 owner_id 与
	//	workspace_id **不在 SET 列表里** ⇒ 那行仍然属于原主人，**正文却已经是
	//	别人的了**）。
	//
	//	⇒ ⇒ ⇒ **修复前实测**（§943 当场量的，探针用完即删）：
	//	    用户 A 建会议「A的会议 / 正文A」
	//	    ⇒ 用户 B POST **同一个 id**（「B的会议 / 正文B」）⇒ **201**
	//	    ⇒ 库里那一行变成「B的会议 / 正文B」，而 owner 仍是 A
	//	    ⇒ ⇒ ⇒ **A 自己读回来看到的是 B 的正文** ⇒ 篡改**静默**发生，用户无感
	//	    ⇒ ⇒ ⇒ 而且 B 的响应里带回了 **A 的 owner_id 与 workspace_id**
	//	    （跨用户信息泄露）
	//
	//	⇒ 守卫不成立时 PG **一行都不返回**（ErrNoRows）⇒ 调用方必须**换号**
	//	（与内存版同形，见 store.go 的 CreateScoped）⇒ ⇒ **绝不覆盖**别人的行。
	//
	//	⚠ 为什么**不**把 workspace_id 加进 SET 列表（notes 的 Upsert 加了）：
	//	notes 那边加它是为了**修复存量**落错租户的行（§939 的 D1 留下的），
	//	而 meetings 的落点一直取自 JWT（**没有**落错租户的存量）
	//	⇒ ⇒ ⇒ 在这里加它会**新造**一次「会议跨租户搬家」⇒ ⇒ ⇒ 不加。
	//
	// ⚠ 作用域冲突的口径（全仓**一份**，内存版见 store.go 的 CreateScoped）：
	//
	//	「客户端带来的 id 已被**别的作用域**占用」⇒ 退回服务端发号
	//	（新建**自己**的一行），**绝不覆盖**别人的行。
	//
	//	⚠ 这条口径**两个实现必须逐字一致** ⇒ ⇒ §943 补了一条**同时跑两个实现**
	//	的判据：此前那条「跨作用域撞号」的承重门**只跑在内存版上**
	//	⇒ ⇒ ⇒ 而生产跑的是 PG 版 ⇒ ⇒ ⇒⇒ 两个实现分叉时门禁是绿的。
	//
	//	⚠ 修复前这里写的是「客户端 id 不该被改 ⇒ 直接报错，**绝不允许**退回
	//	  nextMeetingID 静默换号：换号 = 偷换别人的行 = 数据越权」
	//	—— 那句话把「换号」与「越权」画上了等号：
	//	  **换号新建的是自己的一行（不越权），越权的是覆盖别人的行。**
	//	  而当时 PG 版的行为恰恰是**覆盖**（守卫缺失）
	//	⇒ ⇒ ⇒⇒ 两边的注释都指错了对方（内存版说「PG 靠重试换号挡住了」，
	//	  PG 说「客户端 id 不该被改」）⇒ ⇒⇒ 这一节把它们统一成上面那一份口径。
	var lastErr error
	// clientID 是「客户端带来的 id」；一旦它被守卫挡掉（作用域冲突），
	// 就置空 ⇒ 下一轮退回服务端发号（换号），**不覆盖**别人的行。
	clientID := strings.TrimSpace(req.ID)
	for attempt := 0; attempt < 5; attempt++ {
		now := pgNow()
		// 客户端带了 id 就用它（并由 ON CONFLICT 保证重复 POST 收敛到同一行）；
		// 没带才走服务端生成。撞号重试时必须换新 id，否则重试没有意义。
		id := clientID
		if id == "" {
			id = nextMeetingID()
		}
		row := s.pool.QueryRow(ctx, q,
			id, ownerID, workspaceID, req.Title,
			durationSec, transcript, req.Summary,
			decisions, actions, tags, status,
			req.Location, participants, req.NoteID,
			createdAt, now)
		m, err := scanMeeting(row)
		if err == nil {
			return m, nil
		}
		// ★ §943：归属守卫把「撞上别的作用域」挡掉时，PG **一行都不返回**
		//   ⇒ ErrNoRows ⇒ 这不是数据库坏了，是「这个 id 不是我的」
		//   ⇒ ⇒ ⇒ 与内存版同形：换号，绝不覆盖。
		if errors.Is(err, pgx.ErrNoRows) {
			if clientID == "" {
				// 服务端发号还能撞上 ⇒ 理论上不该发生（纳秒 + 进程内自增）。
				return nil, fmt.Errorf("create meeting: generated id conflicted with an existing meeting: %w", err)
			}
			clientID = ""
			continue
		}
		if !isUniqueViolation(err) {
			return nil, err
		}
		// 主键冲突已被 ON CONFLICT 处理（要么更新成功、要么被守卫挡掉）
		// ⇒ ⇒ 走到这里说明是**别的**唯一约束 ⇒ 那张表将来若加了新唯一索引，
		// 这里就是它的兜底。
		if clientID != "" {
			return nil, fmt.Errorf("create meeting: client id %q conflicts with an existing meeting: %w", req.ID, err)
		}
		lastErr = err
	}
	return nil, fmt.Errorf("create meeting: exhausted id retries: %w", lastErr)
}

func (s *PGStore) GetScoped(id, ownerID, workspaceID string) (*Meeting, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("meeting ID cannot be empty")
	}
	if ownerID == "" {
		return nil, fmt.Errorf("owner_id is required")
	}
	if workspaceID == "" {
		return nil, fmt.Errorf("workspace_id is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	q := `SELECT ` + meetingSelectCols + `
		FROM meetings WHERE id=$1 AND owner_id=$2 AND workspace_id=$3`
	m, err := scanMeeting(s.pool.QueryRow(ctx, q, id, ownerID, workspaceID))
	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("meeting not found")
	}
	if err != nil {
		return nil, err
	}
	return m, nil
}

// ListScoped 返回一页会议 + 过滤后的总条数（§936）。
//
// ⚠ 修复前它返回 `LIMIT 500` 截断后的列表，而 handler 把 len(list) 写成
// `total` ⇒ 用户有 600 场会议时 total 恒为 500 ⇒ 分页器显示「共 500 场」，
// 第 501 场之后**永远取不到**（当时**根本没有** limit/offset 参数）。
func (s *PGStore) ListScoped(ownerID, workspaceID string, opts ListOptions) ([]*Meeting, int, error) {
	if ownerID == "" {
		return nil, 0, fmt.Errorf("owner_id is required")
	}
	if workspaceID == "" {
		return nil, 0, fmt.Errorf("workspace_id is required")
	}

	opts = NormalizeListOptions(opts)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// $3 传 *time.Time：nil ⇒ SQL NULL ⇒ `IS NULL` 分支成立（不过滤）。
	// 用指针而不是 any(nil)，是为了让 pgx 明确知道这个参数是 timestamptz。
	var since *time.Time
	if !opts.Since.IsZero() {
		since = &opts.Since
	}

	var total int
	if err := s.pool.QueryRow(ctx, listScopedCountQuery, ownerID, workspaceID, since).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := s.pool.Query(ctx, listScopedQuery, ownerID, workspaceID, since, opts.Limit, opts.Offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	// 与内存版一致返回非 nil 空切片：handler 直接把长度写进 JSON 的 total，
	// nil 切片编码成 null 会让前端拿到"字段存在但不是数组"的形状。
	result := make([]*Meeting, 0)
	for rows.Next() {
		m, err := scanMeeting(rows)
		if err != nil {
			return nil, 0, err
		}
		result = append(result, m)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return result, total, nil
}

func (s *PGStore) UpdateScoped(m *Meeting, ownerID, workspaceID string) error {
	if m == nil {
		return fmt.Errorf("meeting cannot be nil")
	}
	if strings.TrimSpace(m.ID) == "" {
		return fmt.Errorf("meeting ID cannot be empty")
	}
	if ownerID == "" {
		return fmt.Errorf("owner_id is required")
	}
	if workspaceID == "" {
		return fmt.Errorf("workspace_id is required")
	}

	decisions, err := json.Marshal(nonNilStrings(m.KeyDecisions))
	if err != nil {
		return err
	}
	actions, err := json.Marshal(nonNilActions(m.ActionItems))
	if err != nil {
		return err
	}
	tags, err := json.Marshal(nonNilStrings(m.Tags))
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 与内存版一致：UpdatedAt 由存储层盖时间戳，不接受调用方传入的值。
	now := pgNow()
	const q = `UPDATE meetings SET
		title=$4, duration=$5, recording_url=$6, transcript=$7, summary=$8,
		key_decisions=$9, action_items=$10, tags=$11, project_id=$12, status=$13, updated_at=$14
		WHERE id=$1 AND owner_id=$2 AND workspace_id=$3`
	tag, err := s.pool.Exec(ctx, q,
		m.ID, ownerID, workspaceID, m.Title, m.Duration, m.RecordingURL,
		m.Transcript, m.Summary, decisions, actions, tags, m.ProjectID, m.Status, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("meeting not found")
	}
	m.UpdatedAt = now
	return nil
}

// transcriptTailQuery 是「只取转写尾部」的 SQL（§937 的 D1）。
//
// ⚠⚠ **它绝不能退化成 `SELECT … transcript`**：整读 ⇒ 每轮轮询都把整份转写
//
//	从库里搬到进程里 ⇒ 一场 4 小时的会议按 30 秒轮询 ⇒ 累计搬运量 ∝ 时长²。
//
// ⚠ substr(transcript, $4) 按**字符**定位（PG 的 substr 起始位置是 1）⇒
//
//	与 Go 侧 rune 口径同义（transcript.go 的 TranscriptTail）。
//
// ⚠ char_length(transcript) = 全文字符数 = 旧实现的 utf8.RuneCountInString。
//
//	⇒ ⚠ 这两个表达式在 DB 进程内是 O(全文) 的**扫描**，但它们**不产生进程间搬运**
//	⇒ ⇒ 省掉的是「网络 + 应用内存 + 分配」，不是「DB 内的字符计数」。
const transcriptTailQuery = `SELECT substr(transcript, $4), char_length(transcript)
	FROM meetings WHERE id=$1 AND owner_id=$2 AND workspace_id=$3`

// TranscriptTailScoped 返回转写尾部与全文字符数（§937 的 D1）。
//
// ⚠ 与内存版**逐字一致**（口径见 transcript.go）⇒ 跨 store 一致性由
//
//	transcript_test.go 钉住。
func (s *PGStore) TranscriptTailScoped(id, ownerID, workspaceID string, fromRune int) (string, int, error) {
	if strings.TrimSpace(id) == "" {
		return "", 0, fmt.Errorf("meeting ID cannot be empty")
	}
	if ownerID == "" {
		return "", 0, fmt.Errorf("owner_id is required")
	}
	if workspaceID == "" {
		return "", 0, fmt.Errorf("workspace_id is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// ⚠ PG 的 substr 起始位置是 **1** ⇒ Go 侧 0 基游标要 +1。
	//   游标越过末尾（start > 字符数）⇒ PG 返回空串（不是报错）⇒
	//   ⇒ 「没有新增」与「会议不存在」在形状上分得开。
	start := fromRune + 1
	if start < 1 {
		start = 1
	}
	var tail string
	var total int
	err := s.pool.QueryRow(ctx, transcriptTailQuery, id, ownerID, workspaceID, start).Scan(&tail, &total)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", 0, ErrMeetingNotFound
		}
		return "", 0, err
	}
	return tail, total, nil
}

// UpdateMetaScoped 只更新 title / status 两列（nil = 该列不动），
// **不碰 transcript**（§937 的 D2）。
//
// ⚠ SQL 由 buildMetaUpdateSQL 生成（唯一口径）⇒ 那条「SQL 里绝不出现
//
//	transcript」的不变式可以被判据直接验。
func (s *PGStore) UpdateMetaScoped(id, ownerID, workspaceID string, title, status *string) (time.Time, error) {
	if strings.TrimSpace(id) == "" {
		return time.Time{}, fmt.Errorf("meeting ID cannot be empty")
	}
	if ownerID == "" {
		return time.Time{}, fmt.Errorf("owner_id is required")
	}
	if workspaceID == "" {
		return time.Time{}, fmt.Errorf("workspace_id is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	now := pgNow()
	q, args := buildMetaUpdateSQL(id, ownerID, workspaceID, title, status, now)
	tag, err := s.pool.Exec(ctx, q, args...)
	if err != nil {
		return time.Time{}, err
	}
	if tag.RowsAffected() == 0 {
		return time.Time{}, ErrMeetingNotFound
	}
	return now, nil
}

func (s *PGStore) DeleteScoped(id, ownerID, workspaceID string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("meeting ID cannot be empty")
	}
	if ownerID == "" {
		return fmt.Errorf("owner_id is required")
	}
	if workspaceID == "" {
		return fmt.Errorf("workspace_id is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 删除与写墓碑必须在同一事务里：只有墓碑没有删除（或反过来）都会让
	// 增量同步把状态算错，而这里单条 SQL 失败后无从判断到底落没落。
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `DELETE FROM meetings WHERE id=$1 AND owner_id=$2 AND workspace_id=$3`,
		id, ownerID, workspaceID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("meeting not found")
	}
	// 墓碑按主键去重：同一 id 重复删除（已删过又被人重放）不会炸，
	// 也不会把 deleted_at 往回拨。
	//
	// ⚠⚠ §943 D3：这条 ON CONFLICT **必须带归属守卫**。
	//
	//	修复前它的 SET 列表里**有** owner_id 与 workspace_id
	//	⇒ ⇒ ⇒ 撞上一个**别的作用域**的 id 时，它会把**那一行墓碑整个搬走**
	//	（连 owner_id / workspace_id 一起换成提交者的）⇒ ⇒ ⇒⇒ 后果是：
	//	· 受害者**自己**的墓碑清单里**不再有**那条 id
	//	  （墓碑清单按 owner_id + workspace_id 过滤）
	//	  ⇒ ⇒ ⇒ **已删的会议在增量同步里「复活」**（客户端以为服务器上还有它）
	//	· 攻击者的租户里**多出一行**别人的墓碑
	//	  ⇒ ⇒ ⇒⇒ 跨用户的「删除记录」被注入
	//	· deleted_at 也被改 ⇒ 墓碑的时间窗可以任意调
	//
	//	⇒ 守卫不成立时 PG **一行都不改**（ErrNoRows）⇒ 这里必须**报错**。
	//
	//	⚠ 为什么这里**不**像会议本体那样「换号」：
	//	墓碑的 id **必须等于被删会议的 id** ⇒ 换号会写出一条**指向不存在会议**
	//	的墓碑 ⇒ ⇒ ⇒ 那是错的 ⇒ ⇒⇒ 「绝不覆盖」这条**不变量**是共通的，
	//	但**不成立之后怎么办**要按表的语义分别定（本体换号、墓碑报错）。
	//
	//	⚠ 这道守卫在**当前调用链上不可达**（上面那句 DELETE 已按
	//	owner_id + workspace_id 限定 ⇒ 走不到这里时，那条墓碑必然是**同一个
	//	归属**的）⇒ ⇒⇒ 它守的是**不变量**，不是**分支**：
	//	⇒ 一旦将来有人改了上面那句 DELETE 的作用域逻辑，它会立刻变成一道真守卫。
	//
	//	⚠⚠ **必须用 QueryRow + RETURNING，不能用 Exec**：
	//	守卫不成立时 PG **不报错**，只是**不返回行**（`Exec` 拿到的是一个
	//	成功的命令标签）⇒ ⇒ ⇒ 用 Exec 的话这道守卫就是**哑弹**
	//	（存在、但无声 ⇒ 等于没有 ⇒ §942 在 notes 上刚踩过同族坑）。
	const tomb = `INSERT INTO meeting_tombstones (id, owner_id, workspace_id, deleted_at)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (id) DO UPDATE SET owner_id=EXCLUDED.owner_id,
			workspace_id=EXCLUDED.workspace_id, deleted_at=EXCLUDED.deleted_at
		WHERE meeting_tombstones.owner_id = EXCLUDED.owner_id
		  AND meeting_tombstones.workspace_id = EXCLUDED.workspace_id
		RETURNING id`
	var tombID string
	if err := tx.QueryRow(ctx, tomb, id, ownerID, workspaceID, pgNow()).Scan(&tombID); err != nil {
		// ErrNoRows ⇒ 守卫把「这条墓碑属于别的作用域」挡掉了
		// ⇒ ⇒ ⇒ 绝不搬走别人的墓碑（宁可整笔回滚，也不静默改归属）。
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("delete meeting: tombstone %q belongs to another scope: %w",
				id, ErrMeetingTombstoneScopeConflict)
		}
		return err
	}
	return tx.Commit(ctx)
}

// DeletedIDsSince 返回 since 之后被删除的会议 id（增量同步墓碑清单）。
//
// 注意：与内存版签名一致，**查询失败时返回 nil 而不返回 error**。这是接口
// 形状带来的取舍——内存版不可能失败，PG 版可能。失败时调用方会拿到空的
// deletedIds，增量同步退化成"只下发还活着的记录"，客户端要靠全量对账兜底
// 才能发现被删的那几条。真要严格对齐，应当把 error 提到接口上，那会连带改
// server_meeting_ingest.go 与内存版签名；此处先记录，不静默掩盖。
func (s *PGStore) DeletedIDsSince(ownerID, workspaceID string, since time.Time) []string {
	if ownerID == "" || workspaceID == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	q := `SELECT id FROM meeting_tombstones
		WHERE owner_id=$1 AND workspace_id=$2 AND ($3::timestamptz IS NULL OR deleted_at > $3)
		ORDER BY deleted_at DESC LIMIT 500`
	rows, err := s.pool.Query(ctx, q, ownerID, workspaceID, since)
	if err != nil {
		return nil
	}
	defer rows.Close()

	out := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return out
		}
		out = append(out, id)
	}
	if rows.Err() != nil {
		return out
	}
	return out
}

func nonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

func nonNilActions(in []ActionItem) []ActionItem {
	if in == nil {
		return []ActionItem{}
	}
	return in
}
