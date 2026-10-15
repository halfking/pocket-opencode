// internal/meeting/pg_store.go
package meeting

import (
	"context"
	"encoding/json"
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

const meetingSelectCols = `id, owner_id, workspace_id, title, duration,
	recording_url, transcript, summary,
	COALESCE(key_decisions, '[]'::jsonb), COALESCE(action_items, '[]'::jsonb), COALESCE(tags, '[]'::jsonb),
	project_id, status,
	location, COALESCE(participants, '[]'::jsonb), note_id,
	created_at, updated_at`

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
	createdAt := time.Now()
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
		RETURNING ` + meetingSelectCols

	// 唯一冲突只可能来自 ID 撞号（客户端没带 id 时走 nextMeetingID），重试即换一个新 ID。
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		now := time.Now()
		// 客户端带了 id 就用它（并由 ON CONFLICT 保证重复 POST 收敛到同一行）；
		// 没带才走服务端生成。撞号重试时必须换新 id，否则重试没有意义。
		id := strings.TrimSpace(req.ID)
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
		if !isUniqueViolation(err) {
			return nil, err
		}
		// 客户端 id 撞上已有行时 ON CONFLICT 已处理，走到这里说明是真的主键冲突，
		// 而客户端 id 不该被改 ⇒ 直接返回错误，别偷偷换成别人的行。
		if strings.TrimSpace(req.ID) != "" {
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

func (s *PGStore) ListScoped(ownerID, workspaceID string) ([]*Meeting, error) {
	if ownerID == "" {
		return nil, fmt.Errorf("owner_id is required")
	}
	if workspaceID == "" {
		return nil, fmt.Errorf("workspace_id is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	q := `SELECT ` + meetingSelectCols + `
		FROM meetings WHERE owner_id=$1 AND workspace_id=$2
		ORDER BY created_at DESC LIMIT 500`
	rows, err := s.pool.Query(ctx, q, ownerID, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// 与内存版一致返回非 nil 空切片：handler 直接把长度写进 JSON 的 total，
	// nil 切片编码成 null 会让前端拿到"字段存在但不是数组"的形状。
	result := make([]*Meeting, 0)
	for rows.Next() {
		m, err := scanMeeting(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, m)
	}
	return result, rows.Err()
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
	now := time.Now()
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
	const tomb = `INSERT INTO meeting_tombstones (id, owner_id, workspace_id, deleted_at)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (id) DO UPDATE SET owner_id=EXCLUDED.owner_id,
			workspace_id=EXCLUDED.workspace_id, deleted_at=EXCLUDED.deleted_at`
	if _, err := tx.Exec(ctx, tomb, id, ownerID, workspaceID, time.Now()); err != nil {
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
