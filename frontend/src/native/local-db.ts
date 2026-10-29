/**
 * local-db.ts — 🦞 龙虾硬壳：本地加密数据库抽象层
 *
 * 所有用户数据（笔记/邮件/密码/会议/聊天）默认只存在手机本地，经
 * SQLCipher AES-256 加密。服务端零知识。
 *
 * 数据库密码（dbSecret）由 Keystore 保护的主密钥派生，App 首次启动时
 * setupMasterPassword 生成并写入 AndroidKeyStore，此处只读取明文密码供
 * SQLite 加密用（密码本身不落盘明文，由 keystore plugin 管理）。
 *
 * 架构定位：见 docs/2026-07-02-lobster-local-storage-design.md
 */
import { Capacitor } from '@capacitor/core'
import { CapacitorSQLite, SQLiteConnection, SQLiteDBConnection } from '@capacitor-community/sqlite'
import { initSqliteWeb } from './sqlite-web-init'
import { isWebFallbackRuntime } from './runtime-platform'
import type { SqlDb, SqlRow } from './sqlDb'
import { SCHEMA_SQL, splitSqlStatements, normalizeTriggerForPluginExecute } from './schema'
import { localDbNeedsOpen } from './local-db-init'
import { assertNoTransactionControl, assertReadOnlyQuery } from './local-db-sql-guard'
import type { SyncModule, SyncMode } from '../api/syncPolicyContract'
import { LocalDbLeases, type OperationLease, type OperationOptions, type OwnerIdentity, type ActivationPublisher, type PolicySnapshot, type PolicySnapshotPublisher } from './local-db-leases'

export type { OperationLease, OperationOptions, OwnerIdentity, OwnerLease, ActivationPublisher, PolicySnapshot, PolicySnapshotPublisher } from './local-db-leases'

const MEETINGS_V2_COLUMNS = [
  { table: 'local_meetings', column: 'location', sql: 'ALTER TABLE local_meetings ADD COLUMN location TEXT' },
  { table: 'local_meetings', column: 'participants', sql: 'ALTER TABLE local_meetings ADD COLUMN participants TEXT' },
  { table: 'local_meetings', column: 'live_summary', sql: 'ALTER TABLE local_meetings ADD COLUMN live_summary TEXT' },
  { table: 'local_meetings', column: 'refined_transcript', sql: 'ALTER TABLE local_meetings ADD COLUMN refined_transcript TEXT' },
  { table: 'local_meetings', column: 'recommendations', sql: 'ALTER TABLE local_meetings ADD COLUMN recommendations TEXT' },
  { table: 'local_meetings', column: 'status', sql: "ALTER TABLE local_meetings ADD COLUMN status TEXT DEFAULT 'completed'" },
  { table: 'local_meeting_segments', column: 'lang', sql: "ALTER TABLE local_meeting_segments ADD COLUMN lang TEXT DEFAULT 'zh'" },
  { table: 'local_meeting_segments', column: 'confidence', sql: 'ALTER TABLE local_meeting_segments ADD COLUMN confidence REAL DEFAULT 1.0' },
  { table: 'local_meetings', column: 'note_id', sql: 'ALTER TABLE local_meetings ADD COLUMN note_id TEXT' },
]

// 邮箱配置 LWW 同步 + 发票文件采集字段（2026-09-07）：
// 服务端 email_accounts.updated_at 是 SSOT 时间锚，本地镜像用它做
// last-write-wins；发票镜像补文件状态，离线时也能看到采集进度。
const LIVE_RECORD_V1_COLUMNS = [
  { table: 'local_meetings', column: 'session_id', sql: 'ALTER TABLE local_meetings ADD COLUMN session_id TEXT' },
  { table: 'local_meeting_segments', column: 'translation', sql: 'ALTER TABLE local_meeting_segments ADD COLUMN translation TEXT' },
  { table: 'local_meeting_audio_parts', column: 'file_path', sql: 'ALTER TABLE local_meeting_audio_parts ADD COLUMN file_path TEXT' },
]

const MEETINGS_STUDIO_V1_COLUMNS = [
  { table: 'local_meetings', column: 'archived_at', sql: 'ALTER TABLE local_meetings ADD COLUMN archived_at INTEGER' },
  { table: 'local_meetings', column: 'tags', sql: 'ALTER TABLE local_meetings ADD COLUMN tags TEXT' },
  { table: 'local_meetings', column: 'topic', sql: 'ALTER TABLE local_meetings ADD COLUMN topic TEXT' },
  { table: 'local_meetings', column: 'summary_skill', sql: "ALTER TABLE local_meetings ADD COLUMN summary_skill TEXT DEFAULT 'meeting-minutes'" },
  { table: 'local_todos', column: 'meeting_id', sql: 'ALTER TABLE local_todos ADD COLUMN meeting_id TEXT' },
  // §132：段来源标记。存量行一律回填 'asr'（与 DEFAULT 同值），
  // 即升级后去重行为与升级前**逐字相同**，只有新写入的人工段才被豁免。
  { table: 'local_meeting_segments', column: 'origin', sql: "ALTER TABLE local_meeting_segments ADD COLUMN origin TEXT DEFAULT 'asr'" },
]

const NOTES_CAPTURE_V1_COLUMNS = [
  { table: 'local_notes', column: 'status', sql: "ALTER TABLE local_notes ADD COLUMN status TEXT DEFAULT 'saved'" },
  { table: 'local_notes', column: 'storage_tier', sql: "ALTER TABLE local_notes ADD COLUMN storage_tier TEXT DEFAULT 'inline'" },
  { table: 'local_notes', column: 'summary', sql: 'ALTER TABLE local_notes ADD COLUMN summary TEXT' },
  { table: 'local_notes', column: 'search_text', sql: 'ALTER TABLE local_notes ADD COLUMN search_text TEXT' },
  { table: 'local_notes', column: 'body_path', sql: 'ALTER TABLE local_notes ADD COLUMN body_path TEXT' },
  { table: 'local_notes', column: 'media_json', sql: 'ALTER TABLE local_notes ADD COLUMN media_json TEXT' },
]

/**
 * 配置表归属列（2026-10-29）。
 *
 * ⚠ 为什么**只加列、不回填**：local_user_settings / local_config_outbox 在加列前
 * 没有账号或工作区字段 ⇒ 存量行的归属**无法从数据本身证明**。按已确认的边界
 * （未知归属的历史数据必须保持未认领、不得自动认领或上传），给存量行回填
 * 当前账号等于**凭空替它认领**——那正是要避免的事。
 *
 * ⇒ 存量行的两列保持 NULL = 归属未知：不上传、不被 B 账号捡走、不删除。
 *   只有加列之后、由**已验证会话**新写入的行才带归属。
 */
const CONFIG_OWNERSHIP_V1_COLUMNS = [
  { table: 'local_user_settings', column: 'owner_user_id', sql: 'ALTER TABLE local_user_settings ADD COLUMN owner_user_id TEXT' },
  { table: 'local_user_settings', column: 'owner_workspace_id', sql: 'ALTER TABLE local_user_settings ADD COLUMN owner_workspace_id TEXT' },
  { table: 'local_config_outbox', column: 'owner_user_id', sql: 'ALTER TABLE local_config_outbox ADD COLUMN owner_user_id TEXT' },
  { table: 'local_config_outbox', column: 'owner_workspace_id', sql: 'ALTER TABLE local_config_outbox ADD COLUMN owner_workspace_id TEXT' },
]

/**
 * 会议/录音/待办表的归属列与**带归属的主键**（2026-10-29）。
 *
 * ⚠ 与配置表（CONFIG_OWNERSHIP_V1_*）的**关键差异**：这里**必须保留旧行**。
 *
 *   配置表是**云端镜像** —— 归属不明的旧行可以重新拉回来，丢掉不算数据损失。
 *   会议/录音**不可从云端恢复**：录音分片是原始音频、转写是本地 ASR 结果、
 *   声纹是本地采的 embedding ⇒ 丢掉就是**真的丢了用户的原始数据**。
 *
 *   ⇒ 旧行迁移到新表，归属列置为**空串**（= 归属未知 = 未认领）：
 *     数据保住，但**任何账号都读不到、也永远不会被上传**。
 *   这正是用户确认的边界：「未知归属的历史数据保持未认领」。
 *
 * ⚠ 为什么用空串而不是 NULL：SQLite 的 NULL 不参与主键唯一性 ⇒
 *   用 NULL 会让同一个 id 无限插新行。空串是普通的键分量。
 */
const MEETING_OWNERSHIP_V1_COLUMNS = [
  { table: 'local_todos', column: 'owner_user_id', sql: 'ALTER TABLE local_todos ADD COLUMN owner_user_id TEXT' },
  { table: 'local_todos', column: 'owner_workspace_id', sql: 'ALTER TABLE local_todos ADD COLUMN owner_workspace_id TEXT' },
  { table: 'local_meetings', column: 'owner_user_id', sql: 'ALTER TABLE local_meetings ADD COLUMN owner_user_id TEXT' },
  { table: 'local_meetings', column: 'owner_workspace_id', sql: 'ALTER TABLE local_meetings ADD COLUMN owner_workspace_id TEXT' },
  { table: 'local_meeting_segments', column: 'owner_user_id', sql: 'ALTER TABLE local_meeting_segments ADD COLUMN owner_user_id TEXT' },
  { table: 'local_meeting_segments', column: 'owner_workspace_id', sql: 'ALTER TABLE local_meeting_segments ADD COLUMN owner_workspace_id TEXT' },
  { table: 'local_voiceprints', column: 'owner_user_id', sql: 'ALTER TABLE local_voiceprints ADD COLUMN owner_user_id TEXT' },
  { table: 'local_voiceprints', column: 'owner_workspace_id', sql: 'ALTER TABLE local_voiceprints ADD COLUMN owner_workspace_id TEXT' },
  { table: 'local_meeting_audio_parts', column: 'owner_user_id', sql: 'ALTER TABLE local_meeting_audio_parts ADD COLUMN owner_user_id TEXT' },
  { table: 'local_meeting_audio_parts', column: 'owner_workspace_id', sql: 'ALTER TABLE local_meeting_audio_parts ADD COLUMN owner_workspace_id TEXT' },
]

/**
 * 会议/录音/待办表重建为带归属的主键（2026-10-29）。
 *
 * ⚠ 重建时**搬运旧行**，但归属列置空串（未认领）—— 见上面的说明。
 *
 * ⚠⚠ 三条 SQLite 硬约束（都是**实测**出来的，不是推测）：
 *
 *  1. **子表外键必须与父表主键同形**。父表主键是
 *     `(id, owner_user_id, owner_workspace_id)` 三列 ⇒ 子表外键也必须三列。
 *     写成单列 `REFERENCES local_meetings(id)` 时，SQLite 报
 *     「foreign key mismatch」⇒ **每一次插入段落/分片都会失败**
 *     （Android 端 `setForeignKeyConstraintsEnabled(true)` ⇒ 外键真的生效）。
 *
 *  2. **`ALTER TABLE ... RENAME` 会改写子表的外键子句**。把父表改名成
 *     `*_legacy_v1` 时，仍在的子表外键会被改写成指向 `local_meetings_legacy_v1`
 *     ⇒ 重建后子表的外键指向一张**已被删掉的表** ⇒ 级联删除静默失效。
 *     ⇒ 顺序必须是：**先删旧子表 → 再改父表名 → 再让 v2 表改名到位**。
 *
 *  3. **`INSERT OR IGNORE` 会把外键违规**静默吞掉**。重建期必须用普通
 *     `INSERT`（违规即抛错）—— 否则子表整批搬运会「零行成功、零报错」。
 */
interface MeetingOwnershipRebuildSpec {
  readonly table: string
  readonly create: string
  readonly copy: string
  readonly drop: string
  readonly rename: string
  /**
   * 新表的**完整列清单**（顺序与 create 一致）。
   *
   * ⚠ 为什么需要它：历史库的表形状**各不相同**（有的库没有 `location`、
   *   有的没有 `session_id` —— 它们是历次迁移陆续补上去的）。
   *   搬运语句直接 SELECT 全部列 ⇒ 在缺列的旧库上会报
   *   「no such column」⇒ 迁移**整张表失败**，行留在旧表里没搬过来。
   *   ⇒ 搬运前先把旧表缺的列**补齐**（缺列补 NULL/默认值），再搬。
   */
  readonly columns: readonly { readonly name: string; readonly ddl: string }[]
  /**
   * 旧表**缺这些列**时，搬运要填的占位值。
   *
   * ⚠ 为什么需要：历史库的表形状各不相同 —— 有的旧库根本没有 `start_ms`
   *   （它是后来的迁移补上去的）⇒ 搬运时该列是 NULL，而新表那一列是
   *   `NOT NULL` ⇒ 搬运**整批失败**（报「NOT NULL constraint failed」），
   *   行留在旧表里没搬过来。
   *   ⇒ 缺列用占位值（0 / ''）补齐，**不是** NULL。
   */
  readonly defaults?: Readonly<Record<string, string>>
  /** 本表是**父表**（有子表引用它）⇒ 改名让位必须排在子表之后。 */
  readonly parent?: boolean
  /** 本表是**子表**（引用 parent 表）⇒ 改名让位必须排在父表之前。 */
  readonly childOf?: string
  /** 本表没有父子关系 ⇒ 改名让位顺序无所谓。 */
  readonly standalone?: boolean
}

const MEETING_OWNERSHIP_V1_REBUILD: readonly MeetingOwnershipRebuildSpec[] = [
  {
    table: 'local_todos',
    create: `CREATE TABLE IF NOT EXISTS local_todos_ownership_v1 (
      id TEXT NOT NULL,
      note_id TEXT,
      title TEXT NOT NULL,
      description TEXT,
      status TEXT DEFAULT 'pending',
      priority TEXT DEFAULT 'medium',
      due_at INTEGER,
      completed_at INTEGER,
      tags TEXT,
      extracted_from_voice INTEGER DEFAULT 0,
      meeting_id TEXT,
      created_at INTEGER NOT NULL,
      updated_at INTEGER NOT NULL,
      owner_user_id TEXT NOT NULL DEFAULT '',
      owner_workspace_id TEXT NOT NULL DEFAULT '',
      PRIMARY KEY (id, owner_user_id, owner_workspace_id),
      FOREIGN KEY (note_id) REFERENCES local_notes(id) ON DELETE SET NULL
    )`,
    copy: `INSERT INTO local_todos_ownership_v1 (
      id, note_id, title, description, status, priority, due_at, completed_at,
      tags, extracted_from_voice, meeting_id, created_at, updated_at,
      owner_user_id, owner_workspace_id
    )
    SELECT id,
      note_id,
      COALESCE(title, ''),
      description,
      COALESCE(status, 'pending'),
      COALESCE(priority, 'medium'),
      due_at,
      completed_at,
      tags,
      extracted_from_voice,
      meeting_id,
      COALESCE(created_at, 0),
      COALESCE(updated_at, 0),
      '',
      ''
    FROM local_todos_legacy_v1`,
    drop: 'DROP TABLE IF EXISTS local_todos_ownership_v1',
    rename: 'ALTER TABLE local_todos_ownership_v1 RENAME TO local_todos',
    columns: [
      { name: 'id', ddl: 'TEXT' },
      { name: 'note_id', ddl: 'TEXT' },
      { name: 'title', ddl: 'TEXT NOT NULL' },
      { name: 'description', ddl: 'TEXT' },
      { name: 'status', ddl: "TEXT DEFAULT 'pending'" },
      { name: 'priority', ddl: "TEXT DEFAULT 'medium'" },
      { name: 'due_at', ddl: 'INTEGER' },
      { name: 'completed_at', ddl: 'INTEGER' },
      { name: 'tags', ddl: 'TEXT' },
      { name: 'extracted_from_voice', ddl: 'INTEGER DEFAULT 0' },
      { name: 'meeting_id', ddl: 'TEXT' },
      { name: 'created_at', ddl: 'INTEGER NOT NULL' },
      { name: 'updated_at', ddl: 'INTEGER NOT NULL' },
    ],
    defaults: {
      title: '',
      created_at: '0',
      updated_at: '0',
      status: 'pending',
      priority: 'medium',
    },
    /** 本表没有子表 ⇒ 不需要处理「先删子表再改父表名」的顺序。 */
    standalone: true,
  },
  {
    table: 'local_meetings',
    create: `CREATE TABLE IF NOT EXISTS local_meetings_ownership_v1 (
      id TEXT NOT NULL,
      title TEXT,
      location TEXT,
      participants TEXT,
      audio_path TEXT,
      duration_ms INTEGER DEFAULT 0,
      transcript TEXT,
      summary TEXT,
      live_summary TEXT,
      refined_transcript TEXT,
      recommendations TEXT,
      note_id TEXT,
      session_id TEXT,
      status TEXT DEFAULT 'recording',
      started_at INTEGER NOT NULL,
      created_at INTEGER NOT NULL,
      updated_at INTEGER NOT NULL DEFAULT 0,
      deleted_at INTEGER,
      archived_at INTEGER,
      tags TEXT,
      topic TEXT,
      summary_skill TEXT DEFAULT 'meeting-minutes',
      owner_user_id TEXT NOT NULL DEFAULT '',
      owner_workspace_id TEXT NOT NULL DEFAULT '',
      PRIMARY KEY (id, owner_user_id, owner_workspace_id)
    )`,
    copy: `INSERT INTO local_meetings_ownership_v1 (
      id, title, location, participants, audio_path, duration_ms, transcript, summary,
      live_summary, refined_transcript, recommendations, note_id, session_id, status,
      started_at, created_at, updated_at, deleted_at, archived_at, tags, topic,
      summary_skill, owner_user_id, owner_workspace_id
    )
    SELECT id,
      COALESCE(title, ''),
      location,
      participants,
      audio_path,
      duration_ms,
      transcript,
      summary,
      live_summary,
      refined_transcript,
      recommendations,
      note_id,
      session_id,
      COALESCE(status, 'recording'),
      COALESCE(started_at, 0),
      COALESCE(created_at, 0),
      COALESCE(updated_at, 0),
      deleted_at,
      archived_at,
      tags,
      topic,
      summary_skill,
      '',
      ''
    FROM local_meetings_legacy_v1`,
    drop: 'DROP TABLE IF EXISTS local_meetings_ownership_v1',
    rename: 'ALTER TABLE local_meetings_ownership_v1 RENAME TO local_meetings',
    columns: [
      { name: 'id', ddl: 'TEXT' },
      { name: 'title', ddl: 'TEXT' },
      { name: 'location', ddl: 'TEXT' },
      { name: 'participants', ddl: 'TEXT' },
      { name: 'audio_path', ddl: 'TEXT' },
      { name: 'duration_ms', ddl: 'INTEGER DEFAULT 0' },
      { name: 'transcript', ddl: 'TEXT' },
      { name: 'summary', ddl: 'TEXT' },
      { name: 'live_summary', ddl: 'TEXT' },
      { name: 'refined_transcript', ddl: 'TEXT' },
      { name: 'recommendations', ddl: 'TEXT' },
      { name: 'note_id', ddl: 'TEXT' },
      { name: 'session_id', ddl: 'TEXT' },
      { name: 'status', ddl: "TEXT DEFAULT 'recording'" },
      { name: 'started_at', ddl: 'INTEGER NOT NULL' },
      { name: 'created_at', ddl: 'INTEGER NOT NULL' },
      { name: 'updated_at', ddl: 'INTEGER NOT NULL DEFAULT 0' },
      { name: 'deleted_at', ddl: 'INTEGER' },
      { name: 'archived_at', ddl: 'INTEGER' },
      { name: 'tags', ddl: 'TEXT' },
      { name: 'topic', ddl: 'TEXT' },
      { name: 'summary_skill', ddl: "TEXT DEFAULT 'meeting-minutes'" },
    ],
    defaults: {
      started_at: '0',
      created_at: '0',
      updated_at: '0',
      title: '',
      status: 'recording',
    },
    /**
     * ⚠ 本表是**父表** ⇒ 必须先处理子表（见 MEETING_OWNERSHIP_V1_REBUILD 的头注
     *   第 2 条：`ALTER TABLE RENAME` 会改写子表外键子句）。
     */
    parent: true,
  },
  {
    table: 'local_meeting_segments',
    create: `CREATE TABLE IF NOT EXISTS local_meeting_segments_ownership_v1 (
      id TEXT NOT NULL,
      meeting_id TEXT NOT NULL,
      speaker_label TEXT,
      lang TEXT DEFAULT 'zh',
      confidence REAL DEFAULT 1.0,
      start_ms INTEGER NOT NULL,
      end_ms INTEGER NOT NULL,
      text TEXT NOT NULL,
      translation TEXT,
      origin TEXT DEFAULT 'asr',
      owner_user_id TEXT NOT NULL DEFAULT '',
      owner_workspace_id TEXT NOT NULL DEFAULT '',
      PRIMARY KEY (id, owner_user_id, owner_workspace_id),
      FOREIGN KEY (meeting_id, owner_user_id, owner_workspace_id)
        REFERENCES local_meetings_ownership_v1(id, owner_user_id, owner_workspace_id) ON DELETE CASCADE
    )`,
    copy: `INSERT INTO local_meeting_segments_ownership_v1 (
      id, meeting_id, speaker_label, lang, confidence, start_ms, end_ms, text,
      translation, origin, owner_user_id, owner_workspace_id
    )
    SELECT id,
      COALESCE(meeting_id, ''),
      speaker_label,
      COALESCE(lang, 'zh'),
      COALESCE(confidence, 1.0),
      COALESCE(start_ms, 0),
      COALESCE(end_ms, 0),
      COALESCE(text, ''),
      translation,
      COALESCE(origin, 'asr'),
      '',
      ''
    FROM local_meeting_segments_legacy_v1`,
    drop: 'DROP TABLE IF EXISTS local_meeting_segments_ownership_v1',
    rename: 'ALTER TABLE local_meeting_segments_ownership_v1 RENAME TO local_meeting_segments',
    columns: [
      { name: 'id', ddl: 'TEXT' },
      { name: 'meeting_id', ddl: 'TEXT NOT NULL' },
      { name: 'speaker_label', ddl: 'TEXT' },
      { name: 'lang', ddl: "TEXT DEFAULT 'zh'" },
      { name: 'confidence', ddl: 'REAL DEFAULT 1.0' },
      { name: 'start_ms', ddl: 'INTEGER NOT NULL' },
      { name: 'end_ms', ddl: 'INTEGER NOT NULL' },
      { name: 'text', ddl: 'TEXT NOT NULL' },
      { name: 'translation', ddl: 'TEXT' },
      { name: 'origin', ddl: "TEXT DEFAULT 'asr'" },
    ],
    defaults: {
      meeting_id: '',
      start_ms: '0',
      end_ms: '0',
      text: '',
      lang: 'zh',
      confidence: '1.0',
      origin: 'asr',
    },
    /** 子表：外键指向**最终名**的父表。 */
    childOf: 'local_meetings',
  },
  {
    table: 'local_voiceprints',
    create: `CREATE TABLE IF NOT EXISTS local_voiceprints_ownership_v1 (
      id TEXT NOT NULL,
      display_name TEXT,
      embedding BLOB,
      sample_count INTEGER DEFAULT 1,
      created_at INTEGER NOT NULL,
      owner_user_id TEXT NOT NULL DEFAULT '',
      owner_workspace_id TEXT NOT NULL DEFAULT '',
      PRIMARY KEY (id, owner_user_id, owner_workspace_id)
    )`,
    copy: `INSERT INTO local_voiceprints_ownership_v1 (
      id, display_name, embedding, sample_count, created_at, owner_user_id, owner_workspace_id
    )
    SELECT id,
      display_name,
      embedding,
      COALESCE(sample_count, 1),
      COALESCE(created_at, 0),
      '',
      ''
    FROM local_voiceprints_legacy_v1`,
    drop: 'DROP TABLE IF EXISTS local_voiceprints_ownership_v1',
    rename: 'ALTER TABLE local_voiceprints_ownership_v1 RENAME TO local_voiceprints',
    columns: [
      { name: 'id', ddl: 'TEXT' },
      { name: 'display_name', ddl: 'TEXT' },
      { name: 'embedding', ddl: 'BLOB' },
      { name: 'sample_count', ddl: 'INTEGER DEFAULT 1' },
      { name: 'created_at', ddl: 'INTEGER NOT NULL' },
    ],
    defaults: {
      created_at: '0',
      sample_count: '1',
    },
    standalone: true,
  },
  {
    table: 'local_meeting_audio_parts',
    create: `CREATE TABLE IF NOT EXISTS local_meeting_audio_parts_ownership_v1 (
      id TEXT NOT NULL,
      meeting_id TEXT NOT NULL,
      seq INTEGER NOT NULL,
      mime_type TEXT NOT NULL,
      data_base64 TEXT NOT NULL,
      file_path TEXT,
      created_at INTEGER NOT NULL,
      owner_user_id TEXT NOT NULL DEFAULT '',
      owner_workspace_id TEXT NOT NULL DEFAULT '',
      PRIMARY KEY (id, owner_user_id, owner_workspace_id),
      FOREIGN KEY (meeting_id, owner_user_id, owner_workspace_id)
        REFERENCES local_meetings_ownership_v1(id, owner_user_id, owner_workspace_id) ON DELETE CASCADE
    )`,
    copy: `INSERT INTO local_meeting_audio_parts_ownership_v1 (
      id, meeting_id, seq, mime_type, data_base64, file_path, created_at,
      owner_user_id, owner_workspace_id
    )
    SELECT id,
      COALESCE(meeting_id, ''),
      COALESCE(seq, 0),
      COALESCE(mime_type, ''),
      COALESCE(data_base64, ''),
      file_path,
      COALESCE(created_at, 0),
      '',
      ''
    FROM local_meeting_audio_parts_legacy_v1`,
    drop: 'DROP TABLE IF EXISTS local_meeting_audio_parts_ownership_v1',
    rename: 'ALTER TABLE local_meeting_audio_parts_ownership_v1 RENAME TO local_meeting_audio_parts',
    columns: [
      { name: 'id', ddl: 'TEXT' },
      { name: 'meeting_id', ddl: 'TEXT NOT NULL' },
      { name: 'seq', ddl: 'INTEGER NOT NULL' },
      { name: 'mime_type', ddl: 'TEXT NOT NULL' },
      { name: 'data_base64', ddl: 'TEXT NOT NULL' },
      { name: 'file_path', ddl: 'TEXT' },
      { name: 'created_at', ddl: 'INTEGER NOT NULL' },
    ],
    defaults: {
      meeting_id: '',
      seq: '0',
      mime_type: '',
      data_base64: '',
      created_at: '0',
    },
    childOf: 'local_meetings',
  },
]

/**
 * 配置表重建为**带归属的主键**（2026-10-29）。
 *
 * ⚠ 为什么必须重建表（而不是只加列）：旧主键是 (namespace, id)，两个账号的
 *   同一项配置会**互相覆盖** —— 既是数据丢失，也是跨账号串号。
 *   服务端 user_settings 的主键本来就是 (user_id, workspace_id, namespace, id)，
 *   本地镜像必须与它一致。
 *
 * ⚠ 重建时**不迁移任何行**：旧行的归属无从证明（迁移时能拿到的只有当前账号，
 *   那等于凭空替历史行认领）。⇒ 旧行留在旧表里被丢弃，新表从空表开始。
 *   这正是用户确认的边界：未知归属的历史数据保持未认领，不得自动认领或上传。
 *
 * ⚠ 为什么用**新表名**而不是 DELETE+INSERT：SQLite 的 ALTER TABLE 不能改主键；
 *   建新表 + 改名是标准做法，且旧表在改名完成前一直存在（中途失败不丢数据）。
 */
const CONFIG_OWNERSHIP_V1_REBUILD = [
  {
    table: 'local_user_settings',
    create: `CREATE TABLE IF NOT EXISTS local_user_settings_v2 (
      namespace TEXT NOT NULL,
      id TEXT NOT NULL,
      payload TEXT NOT NULL DEFAULT '{}',
      secret_encrypted TEXT NOT NULL DEFAULT '',
      updated_at INTEGER NOT NULL,
      dirty INTEGER NOT NULL DEFAULT 0,
      owner_user_id TEXT NOT NULL DEFAULT '',
      owner_workspace_id TEXT NOT NULL DEFAULT '',
      PRIMARY KEY (namespace, id, owner_user_id, owner_workspace_id)
    )`,
    drop: 'DROP TABLE IF EXISTS local_user_settings_v2',
    rename: 'ALTER TABLE local_user_settings_v2 RENAME TO local_user_settings',
  },
]

/**
 * §898：向量表加一列 `chunks`（一行装几个段向量）。
 *
 * ⚠ 为什么**只加列**、不改主键：本地库的迁移机制只支持 `ALTER TABLE ADD COLUMN`
 *   （见上面每一组 `*_COLUMNS` 的写法），**不支持重建表**。
 *   而复合主键 `(note_id, chunk_idx)` 必须重建表 ⇒ 得另造一套迁移基建，风险远高于收益。
 *   ⇒ 形态改成「一行仍是 note_id，embedding BLOB 里连续放 chunks×dim 个 float」。
 *   ⇒ 旧数据 `chunks=1`，语义与从前**逐字一致**，迁移是**纯加列**、零数据风险。
 */
const NOTE_VECTORS_V1_COLUMNS = [
  { table: 'local_note_vectors', column: 'chunks', sql: 'ALTER TABLE local_note_vectors ADD COLUMN chunks INTEGER NOT NULL DEFAULT 1' },
  // §899：段文本哈希（增量重嵌用）。默认 '' ⇒ 旧数据没有哈希 ⇒ 一律重嵌（安全侧）。
  { table: 'local_note_vectors', column: 'chunk_hashes', sql: "ALTER TABLE local_note_vectors ADD COLUMN chunk_hashes TEXT NOT NULL DEFAULT ''" },
]

const EMAIL_SYNC_V1_COLUMNS = [
  { table: 'local_email_accounts', column: 'updated_at', sql: 'ALTER TABLE local_email_accounts ADD COLUMN updated_at INTEGER DEFAULT 0' },
  { table: 'local_email_invoices', column: 'file_name', sql: "ALTER TABLE local_email_invoices ADD COLUMN file_name TEXT DEFAULT ''" },
  { table: 'local_email_invoices', column: 'file_source', sql: "ALTER TABLE local_email_invoices ADD COLUMN file_source TEXT DEFAULT ''" },
  { table: 'local_email_invoices', column: 'attempts', sql: 'ALTER TABLE local_email_invoices ADD COLUMN attempts INTEGER DEFAULT 0' },
  { table: 'local_email_invoices', column: 'last_error', sql: "ALTER TABLE local_email_invoices ADD COLUMN last_error TEXT DEFAULT ''" },
  { table: 'local_email_invoices', column: 'feishu_sent_at', sql: 'ALTER TABLE local_email_invoices ADD COLUMN feishu_sent_at INTEGER DEFAULT 0' },
]

const EMAIL_INBOX_V1_COLUMNS = [
  { table: 'local_emails', column: 'deleted_at', sql: 'ALTER TABLE local_emails ADD COLUMN deleted_at INTEGER DEFAULT 0' },
  { table: 'local_emails', column: 'body_purged', sql: 'ALTER TABLE local_emails ADD COLUMN body_purged INTEGER DEFAULT 0' },
]

// 自定义邮件目录 + 本地迁移操作日志（2026-10-01）：
//   - local_emails.folder 记录邮件所在目录（空 = INBOX）；
//   - local_email_folders 是服务端目录的本地镜像；
//   - local_email_ops 是本地移动/删除操作的离线队列，同步按钮把 pending 推给
//     服务端 /api/emails/ops（幂等键去重），由服务端经 IMAP 真正迁移。
const EMAIL_FOLDERS_V1_COLUMNS = [
  { table: 'local_emails', column: 'folder', sql: "ALTER TABLE local_emails ADD COLUMN folder TEXT DEFAULT ''" },
  // q2：AI 判定重要度的依据。服务端早就写进 emails.action_reason 了，但读路径
  // 从没读过它，于是「为什么这封被判为重要」在列表页拿不到 —— 提醒不可信。
  // 已存在的本地库不会因为改了 CREATE TABLE 而补列（SQLite 的
  // CREATE TABLE IF NOT EXISTS 对老库是 no-op），所以必须有这条迁移。
  { table: 'local_emails', column: 'action_reason', sql: 'ALTER TABLE local_emails ADD COLUMN action_reason TEXT' },
]

const LIST_SYNC_V1_COLUMNS = [
  { table: 'local_email_invoices', column: 'email_date', sql: 'ALTER TABLE local_email_invoices ADD COLUMN email_date INTEGER DEFAULT 0' },
  { table: 'local_email_invoices', column: 'dirty', sql: 'ALTER TABLE local_email_invoices ADD COLUMN dirty INTEGER DEFAULT 0' },
  { table: 'local_email_invoices', column: 'client_id', sql: "ALTER TABLE local_email_invoices ADD COLUMN client_id TEXT DEFAULT ''" },
  { table: 'local_emails', column: 'updated_at', sql: 'ALTER TABLE local_emails ADD COLUMN updated_at INTEGER DEFAULT 0' },
  { table: 'local_meetings', column: 'updated_at', sql: 'ALTER TABLE local_meetings ADD COLUMN updated_at INTEGER DEFAULT 0' },
  { table: 'local_chat_conversations', column: 'updated_at', sql: 'ALTER TABLE local_chat_conversations ADD COLUMN updated_at INTEGER DEFAULT 0' },
  { table: 'local_chat_messages', column: 'updated_at', sql: 'ALTER TABLE local_chat_messages ADD COLUMN updated_at INTEGER DEFAULT 0' },
]

const DB_NAME = 'lobster'
const DB_VERSION = 1

/**
 * LocalDB 是前端唯一访问本地数据库的入口。所有 feature store（notes/emails/...）
 * 都通过 LocalDB.instance 获取 connection，避免多处 createConnection。
 */
class LocalDB {
  private sqlite: SQLiteConnection
  private conn: SQLiteDBConnection | null = null
  private initialized = false
  private transactionUncertain = false
  private activationBlocked = false
  private leases = new LocalDbLeases()
  // One JS instance only: other tabs/WebViews and account ownership need their own boundary.
  private pending: Promise<unknown> = Promise.resolve()

  private enqueue<T>(operation: () => Promise<T>): Promise<T> {
    const result = this.pending.then(operation)
    this.pending = result.catch(() => {})
    return result
  }

  captureOperation(options: OperationOptions): OperationLease {
    return this.leases.capture(options)
  }

  assertOperationCurrent(lease: OperationLease): void {
    this.requireActivationHealthy()
    this.leases.assertCurrent(lease)
  }

  bindOperation(lease: OperationLease) {
    return Object.freeze({
      assertCurrent: () => this.assertOperationCurrent(lease),
      /**
       * 这个句柄所属的**归属**（`null` = 归属未知）。
       *
       * ⚠ 为什么挂在句柄上：写入方需要「用**哪个归属**写这一行」，而这个归属
       *   必须与**授权这次写入的租约**是同一代会话 —— 否则会出现
       *   「租约是 A 的、归属却是 B 的」的撕裂（切账号瞬间）。
       *   ⇒ 归属只能从租约上取，不能另行读 auth store。
       */
      owner: lease.owner.principal
        ? Object.freeze({ userId: lease.owner.principal.userId, workspaceId: lease.owner.principal.workspaceId })
        : null,
      run: (sql: string, values: unknown[] = []) => this.run(sql, values, lease),
      query: <T = Record<string, unknown>>(sql: string, values: unknown[] = []) => this.query<T>(sql, values, lease),
      queryOne: <T = Record<string, unknown>>(sql: string, values: unknown[] = []) => this.queryOne<T>(sql, values, lease),
      execute: (sql: string) => this.execute(sql, true, lease),
      runInTransaction: (statements: { statement: string; values: unknown[] }[]) => this.runInTransaction(statements, lease),
    })
  }

  /** Additive kernel only: production auth/policy publishers are not wired here. */
  async requestOwnerActivation(next: OwnerIdentity | null, publish: ActivationPublisher): Promise<void> {
    const ticket = this.leases.prepareOwner(next)
    return this.enqueue(async () => {
      this.requireActivationHealthy()
      this.leases.activateOwner(ticket, publish)
    })
  }

  async requestPolicyActivation(module: SyncModule, mode: SyncMode, publish: ActivationPublisher): Promise<void> {
    const ticket = this.leases.preparePolicy(module, mode)
    return this.enqueue(async () => {
      this.requireActivationHealthy()
      this.leases.activatePolicy(ticket, publish)
    })
  }

  /** Complete snapshots publish once. Unchanged, nonpending modules retain their leases. */
  async requestPolicySnapshotActivation(modes: PolicySnapshot, publish: PolicySnapshotPublisher): Promise<void> {
    const ticket = this.leases.prepareSnapshot(modes)
    return this.enqueue(async () => {
      this.requireActivationHealthy()
      this.leases.activateSnapshot(ticket, publish)
    })
  }

  private requireActivationHealthy(): void {
    if (this.transactionUncertain || this.activationBlocked) throw new Error('LocalDB 状态不确定，需关闭连接并重新初始化')
  }

  // Called only inside the queue; no public callbacks can re-enter it.
  private async inTransaction<T>(conn: SQLiteDBConnection, lease: OperationLease, operation: () => Promise<T>): Promise<T> {
    let result: T
    let began = false
    try {
      await conn.beginTransaction()
      began = true
      this.leases.assertCurrent(lease)
      result = await operation()
      // No await between this check and dispatch. Once commit is dispatched,
      // activation stays queued until its reply and Web persistence settle.
      this.leases.assertCurrent(lease)
      await conn.commitTransaction()
    } catch (error) {
      if (!began) {
        // A rejected begin may refer to an already active transaction owned elsewhere.
        this.transactionUncertain = true
        this.initialized = false
        throw error
      }
      try {
        await conn.rollbackTransaction()
      } catch (rollbackError) {
        this.transactionUncertain = true
        this.initialized = false
        throw Object.assign(new Error('LocalDB 回滚失败，需关闭连接并重新初始化'), { cause: error, rollbackError })
      }
      throw error
    }
    // jeep's explicit commit omits its autoSave finally. An empty nontransactional
    // execute runs that finally after commit, respecting the element's autoSave setting.
    // A persistence failure occurs after commit and cannot be rolled back.
    if (isWebFallbackRuntime()) {
      try {
        await conn.execute('', false)
      } catch (error) {
        this.initialized = false
        this.activationBlocked = true
        throw Object.assign(error instanceof Error ? error : new Error(String(error)), { outcome: 'committed' })
      }
    }
    this.leases.assertCurrent(lease, 'committed')
    return result
  }

  constructor() {
    this.sqlite = new SQLiteConnection(CapacitorSQLite)
  }

  /**
   * 只校验主密码，不开库、不建表、不动连接。
   *
   * 为什么需要单独一个入口（2026-10-06 安全修复）：
   *   `initLobster` 的顺序是 initAppCrypto → localDB.init。若只在 init 里校验，
   *   错口令会**先**用错口令派生出 AES key 并写进模块级 `cryptoKey`，
   *   然后才在 init 抛错 —— crypto 状态已被污染。
   *   把校验提前到 crypto 之前，错口令就完全碰不到加密层。
   *
   * 口径：库还没有 secret（首次设置）时返回 true —— 那时没有「对不对」可比，
   * 正确性由随后的 setEncryptionSecret 建立。
   */
  async verifyMasterPassword(dbSecret: string): Promise<boolean> {
    return this.enqueue(async () => {
      if (isWebFallbackRuntime()) return true
      if (!dbSecret) return false
      if (!(await this.isSecretStoredSafely())) return true
      return this.checkEncryptionSecretSafely(dbSecret)
    })
  }

  /**
   * secure store 里是否已存过 secret。
   *
   * ⚠️ 探针失败必须与「没存」区分开：若插件不支持（如 Web fallback / 老版本插件），
   * 这里返回 false ⇒ 走「首次设置 secret」分支 ⇒ setEncryptionSecret 若真失败会抛出，
   * 不会被静默吞掉。**宁可失败也不要静默放行**——那正是本次修掉的洞。
   */
  private async isSecretStoredSafely(): Promise<boolean> {
    try {
      const r = await this.sqlite.isSecretStored()
      return r?.result === true
    } catch (e) {
      console.warn('[localDB] isSecretStored 探针失败，按「未存」处理:', e)
      return false
    }
  }

  /**
   * 比对本次传入的 secret 与 secure store 里已存的是否一致。
   *
   * 插件原生实现是 `savedPassphrase.equals(passphrase)`（UtilsSecret.java:148），
   * 官方文档标注 since 4.6.1；本仓锁 @capacitor-community/sqlite 8.1.0。
   * 探针失败一律按「不一致」处理 ⇒ 拒绝开库（fail-closed）。
   * 这是本修复的核心：**宁可让用户重输一次，也不拿错密码开库**。
   */
  private async checkEncryptionSecretSafely(dbSecret: string): Promise<boolean> {
    try {
      const r = await this.sqlite.checkEncryptionSecret(dbSecret)
      return r?.result === true
    } catch (e) {
      console.warn('[localDB] checkEncryptionSecret 失败，按「口令不一致」拒绝开库:', e)
      return false
    }
  }

  /**
   * 初始化本地加密库。dbSecret 是用户主密码（由 Keystore 派生）。
   * 幂等：重复调用安全。
   */
  async init(dbSecret: string): Promise<void> {
    return this.enqueue(async () => {
      if (!localDbNeedsOpen(this.initialized, this.conn !== null)) return
      this.initialized = false
      if (this.conn) {
        await this.closeConnection()
      }

      // Web（jeep-sqlite / sql.js）不支持 SQLCipher 加密库；HarmonyOS Phase A
      // 同样固定走这条路径，直到 ArkTS RDB bridge 经真机验证后才可启用原生加密库。
      // 原生 Android/iOS 保持 secret 模式。
      const isWeb = isWebFallbackRuntime()
      const encrypted = !isWeb && dbSecret.length > 0
      if (isWeb) {
        console.info('[localDB] web fallback runtime: no-encryption (browser/HarmonyOS Phase A)')
        await initSqliteWeb()
        // Web 端必须显式 initWebStore：插件的 jeepSqliteElement 引用只在
        // initWebStore() 里捕获，漏掉这一步则后续所有操作都抛
        // "The jeep-sqlite element is not present in the DOM!"，浏览器上
        // 本地库永远无法建立（ISSUES #19 深度遍历排障中定位）。
        await this.sqlite.initWebStore()
      }

      // ✅ 关键修复：在 createConnection 之前先调用 setEncryptionSecret
      // 官方 API 文档：setEncryptionSecret "Only to be used once if you wish to encrypt database"
      // open() 内部会从 secure store 取密码作为 SQLCipher PRAGMA key
      // 如果在 open() 之后调用，SQLCipher 已经在未设 key 的情况下尝试读 db header，必然失败
      // （"Open: No Passphrase stored"）。
      if (encrypted) {
        // 2026-10-06 安全修复（真机实锤，见 __tests__/unlock-secret-verification.test.mjs 头注）：
        //
        // 原形状是「setEncryptionSecret 抛不抛来判断」——catch 里只 console.warn 就继续往下。
        // 但插件 Database.java:245 的 `password = _uSecret.getPassphrase()` 意味着
        // **open() 用的是 secure store 里已存的那个 secret，不是本次传入的**。
        // 加上 crypto.ts 的 initAppCrypto 只做 PBKDF2 派生、对任何字符串都成功，
        // 三者合成 ⇒ 用户在解锁页输**任意非空错误口令**也会开库成功。
        //
        // 真机 A/B/C 三轮对照（emulator-5554）：
        //   正确口令 → 离开解锁页；错误口令 → **同样**离开解锁页；
        //   空口令 → 被 unlockSubmitMode 挡住（证明判据非恒真）。
        //
        // 为什么不能改成「抛错就 fail-fast」：第二次及以后启动时
        // setEncryptionSecret **必然**抛 "a passphrase has already been set"（原生
        // UtilsSecret.java:42），那是正常路径不是错误 ⇒ 直接抛会让所有老用户重启即崩。
        // 正确做法是用插件提供的 checkEncryptionSecret 做**真正的比对**：
        // 已存 → 比对，不一致就拒绝；未存 → 写入新 secret。
        const stored = await this.isSecretStoredSafely()
        if (stored) {
          const ok = await this.checkEncryptionSecretSafely(dbSecret)
          if (!ok) {
            // 口令与库内 secret 不一致 ⇒ 绝不能用已存的 secret 继续开库，
            // 否则就是「拿错密码开人家库」。抛给调用方，由 LoginView 显示失败。
            throw new Error('主密码与本地加密库的密钥不一致')
          }
        } else {
          await this.sqlite.setEncryptionSecret(dbSecret)
        }
      }

      const mode = encrypted ? 'secret' : 'no-encryption'

      // 官方推荐：先 checkConnectionsConsistency，再 isConnection / create
      await this.sqlite.checkConnectionsConsistency()
      const already = (await this.sqlite.isConnection(DB_NAME, false)).result === true
      if (already) {
        this.conn = await this.sqlite.retrieveConnection(DB_NAME, false)
      } else {
        this.conn = await this.sqlite.createConnection(
          DB_NAME,
          encrypted,
          mode,
          DB_VERSION,
          false,
        )
      }
      await this.conn.open()

      // 建表（幂等 CREATE IF NOT EXISTS）。Web/sql.js 通常无 FTS5，先跳过 FTS 段。
      //
      // 必须用 splitSqlStatements 逐条执行（2026-08-27 真机验证实测）：Android 端
      // 插件对整段 SQL 按 `;` 机械切分，FTS 触发体 BEGIN...END 内的分号会把语句
      // 截断，原生批次在首个残缺片段处中止且不向 JS 抛错——SCHEMA_SQL 中位于
      // FTS 段之后的表（local_outbox / local_drafts / local_todos 等）全部静默
      // 缺失。schema.ts 的 splitSqlStatements 正是为此而写（触发体整体保留），
      // 此前没有任何生产消费方。逐条执行 + 单条失败跳过告警，保证后续语句继续。
      const schemaSql = isWeb ? stripFts5ForWeb(SCHEMA_SQL) : SCHEMA_SQL
      const statements = splitSqlStatements(schemaSql)
      let applied = 0
      let failed = 0
      for (const stmt of statements) {
        // BUG-AH：切分正确还不够 —— 插件的 Android execute 会按字面量 `;\n` 再切一次，
        // 触发体里的「分号 + 换行」会把 CREATE TRIGGER 截断成半条语句。
        // 真机实测：三个 FTS 触发器一个都没建成，而 FTS 虚表建出来了
        // （虚表体内没有分号，所以幸存）。详见 schema.ts 里
        // normalizeTriggerForPluginExecute 的实验表。
        const one = normalizeTriggerForPluginExecute(stmt)
        const withSemi = one.endsWith(';') ? one : `${one};`
        try {
          await this.conn.execute(withSemi, false)
          applied++
        } catch (e) {
          failed++
          console.warn('[localDB] skip schema stmt:', withSemi.slice(0, 60), e)
        }
      }
      if (failed > 0) {
        console.warn(`[localDB] schema applied ${applied}/${statements.length} statements (${failed} skipped)`)
      }

      // 增量迁移（已有库补列）— initialized 在全部迁移完成后才置位，
      // 避免旧库在补列完成前被 requireReady() 放行、查询撞 no such column
      try {
        await this.runMeetingsV2Migration()
      } catch (e) {
        console.warn('[localDB] meetings v2 migration failed:', e)
      }
      try {
        await this.runEmailSyncV1Migration()
      } catch (e) {
        console.warn('[localDB] email sync v1 migration failed:', e)
      }
      try {
        await this.runLiveRecordV1Migration()
      } catch (e) {
        console.warn('[localDB] live record v1 migration failed:', e)
      }
      try {
        await this.runNotesCaptureV1Migration()
      } catch (e) {
        console.warn('[localDB] notes capture v1 migration failed:', e)
      }
      try {
        await this.runMeetingsStudioV1Migration()
      } catch (e) {
        console.warn('[localDB] meetings studio v1 migration failed:', e)
      }
      try {
        // §898：向量表加 chunks 列。必须排在 `initialized` 置位之前 ——
        //   `vectorIndex.load()` 会 SELECT chunks 列，列不存在就是一次查询失败。
        await this.runNoteVectorsV1Migration()
      } catch (e) {
        console.warn('[localDB] note vectors v1 migration failed:', e)
      }
      try {
        // 配置表归属列。必须排在 `initialized` 置位之前 —— config-sync 会按
        // 归属列过滤待上传行，列不存在就是一次查询失败。
        await this.runConfigOwnershipV1Migration()
      } catch (e) {
        console.warn('[localDB] config ownership v1 migration failed:', e)
      }
      try {
        // 配置表重建为带归属的主键。必须在补列之后、initialized 之前。
        await this.runConfigOwnershipV1Rebuild()
      } catch (e) {
        console.warn('[localDB] config ownership v1 rebuild failed:', e)
      }
      try {
        // 会议/录音/待办/声纹表补归属列。必须在 initialized 之前 ——
        //   会议链路按归属列过滤读写，列不存在就是一次查询失败。
        await this.runMeetingOwnershipV1Migration()
      } catch (e) {
        console.warn('[localDB] meeting ownership v1 migration failed:', e)
      }
      try {
        // 会议/录音/待办/声纹表重建为带归属的主键。必须在补列之后、initialized 之前。
        await this.runMeetingOwnershipV1Rebuild()
      } catch (e) {
        console.warn('[localDB] meeting ownership v1 rebuild failed:', e)
      }
      try {
        await this.runListSyncV1Migration()
      } catch (e) {
        console.warn('[localDB] list sync v1 migration failed:', e)
      }
      try {
        await this.runEmailInboxV1Migration()
      } catch (e) {
        console.warn('[localDB] email inbox v1 migration failed:', e)
      }
      try {
        await this.runEmailFoldersV1Migration()
      } catch (e) {
        console.warn('[localDB] email folders v1 migration failed:', e)
      }
      this.initialized = true
    })
  }

  /** 自定义邮件目录 + 本地迁移操作日志（2026-10-01）。 */
  private async runEmailFoldersV1Migration(): Promise<void> {
    if (!this.conn) return
    await this.conn.execute(`
      CREATE TABLE IF NOT EXISTS _schema_migrations (
        version TEXT PRIMARY KEY,
        description TEXT,
        applied_at INTEGER NOT NULL
      );
    `, false)
    // 2026-10-06 修正：本方法与 runEmailInboxV1Migration 是**同一个缺陷的第二个实例**。
    // 设备每次冷启动打出的 4 条 `Execute: duplicate column name` 里，
    // `deleted_at` / `body_purged` 来自 inbox 那个方法，
    // 而 **`folder` / `action_reason` 来自这里** —— 两者都是「裸 execute 补列 +
    // 版本号只写不读」。只修一个 ⇒ 冷启动仍然打 2 条，真错误仍被埋。
    // （「同一形状的缺陷修完必须扫面」：我是靠比对各 *_COLUMNS 数组的元素归属才发现的。）
    const done = await this.queryForMigration<{ version: string }>(
      "SELECT version FROM _schema_migrations WHERE version = '2026-10-01-email-folders-v1'",
    )
    if (done) return
    for (const col of EMAIL_FOLDERS_V1_COLUMNS) {
      const exists = await this.queryForMigration<{ cnt: number }>(
        `SELECT COUNT(*) AS cnt FROM pragma_table_info('${col.table}') WHERE name = ?`,
        [col.column],
      )
      if (exists && exists.cnt > 0) continue
      try {
        await this.conn.execute(col.sql, false)
      } catch {
        // 列可能已存在，忽略
      }
    }
    await this.conn.execute(`
      CREATE TABLE IF NOT EXISTS local_email_folders (
        id TEXT PRIMARY KEY,
        account_id TEXT NOT NULL,
        name TEXT NOT NULL,
        display_name TEXT NOT NULL DEFAULT '',
        special TEXT NOT NULL DEFAULT '',
        source TEXT NOT NULL DEFAULT 'user',
        server_synced INTEGER NOT NULL DEFAULT 0,
        email_count INTEGER NOT NULL DEFAULT 0,
        created_at INTEGER NOT NULL,
        updated_at INTEGER NOT NULL,
        UNIQUE(account_id, name)
      );
    `, false)
    await this.conn.execute(`
      CREATE TABLE IF NOT EXISTS local_email_ops (
        id TEXT PRIMARY KEY,
        account_id TEXT NOT NULL,
        email_id TEXT NOT NULL,
        uid INTEGER NOT NULL DEFAULT 0,
        action TEXT NOT NULL,
        target_folder TEXT NOT NULL DEFAULT '',
        subject TEXT NOT NULL DEFAULT '',
        status TEXT NOT NULL DEFAULT 'pending',
        error TEXT NOT NULL DEFAULT '',
        created_at INTEGER NOT NULL,
        updated_at INTEGER NOT NULL
      );
      CREATE INDEX IF NOT EXISTS idx_email_ops_status ON local_email_ops(status);
    `, false)
    await this.conn.execute(
      "INSERT OR IGNORE INTO _schema_migrations (version, description, applied_at) VALUES ('2026-10-01-email-folders-v1', '自定义目录/本地迁移操作日志', strftime('%s', 'now') * 1000);",
      false,
    )
  }

  /** 会议模块 v2：为旧库补列，列已存在则跳过 */
  private async runMeetingsV2Migration(): Promise<void> {
    if (!this.conn) return
    await this.conn.execute(`
      CREATE TABLE IF NOT EXISTS _schema_migrations (
        version TEXT PRIMARY KEY,
        description TEXT,
        applied_at INTEGER NOT NULL
      );
    `, false)
    const done = await this.queryForMigration<{ version: string }>(
      "SELECT version FROM _schema_migrations WHERE version = '2026-07-15-meetings-v2'",
    )
    if (done) return

    for (const col of MEETINGS_V2_COLUMNS) {
      const exists = await this.queryForMigration<{ cnt: number }>(
        `SELECT COUNT(*) AS cnt FROM pragma_table_info('${col.table}') WHERE name = ?`,
        [col.column],
      )
      if (exists && exists.cnt > 0) continue
      try {
        await this.conn.execute(col.sql, false)
      } catch {
        // 列可能已存在，忽略
      }
    }
    // 声纹表
    await this.conn.execute(`
      CREATE TABLE IF NOT EXISTS local_voiceprints (
        id TEXT PRIMARY KEY,
        display_name TEXT,
        embedding BLOB,
        sample_count INTEGER DEFAULT 1,
        created_at INTEGER NOT NULL
      );
      INSERT OR IGNORE INTO _schema_migrations (version, description, applied_at)
      VALUES ('2026-07-15-meetings-v2', '会议模块扩展字段', strftime('%s', 'now') * 1000);
    `, false)
  }

  /** 邮箱配置 LWW 同步 + 发票文件字段（2026-09-07）：旧库补列。 */
  private async runEmailSyncV1Migration(): Promise<void> {
    if (!this.conn) return
    await this.conn.execute(`
      CREATE TABLE IF NOT EXISTS _schema_migrations (
        version TEXT PRIMARY KEY,
        description TEXT,
        applied_at INTEGER NOT NULL
      );
    `, false)
    const done = await this.queryForMigration<{ version: string }>(
      "SELECT version FROM _schema_migrations WHERE version = '2026-09-07-email-sync-v1'",
    )
    if (done) return

    for (const col of EMAIL_SYNC_V1_COLUMNS) {
      const exists = await this.queryForMigration<{ cnt: number }>(
        `SELECT COUNT(*) AS cnt FROM pragma_table_info('${col.table}') WHERE name = ?`,
        [col.column],
      )
      if (exists && exists.cnt > 0) continue
      try {
        await this.conn.execute(col.sql, false)
      } catch {
        // 列可能已存在
      }
    }
    await this.conn.execute(
      "INSERT OR IGNORE INTO _schema_migrations (version, description, applied_at) VALUES ('2026-09-07-email-sync-v1', '邮箱配置 LWW + 发票文件字段', strftime('%s', 'now') * 1000);",
      false,
    )
  }

  /** 列表本地优先：发票 email_date / dirty / client_id。 */
  private async runListSyncV1Migration(): Promise<void> {
    if (!this.conn) return
    await this.conn.execute(`
      CREATE TABLE IF NOT EXISTS _schema_migrations (
        version TEXT PRIMARY KEY,
        description TEXT,
        applied_at INTEGER NOT NULL
      );
    `, false)
    for (const col of LIST_SYNC_V1_COLUMNS) {
      const exists = await this.queryForMigration<{ cnt: number }>(
        `SELECT COUNT(*) AS cnt FROM pragma_table_info('${col.table}') WHERE name = ?`,
        [col.column],
      )
      if (exists && exists.cnt > 0) continue
      try {
        await this.conn.execute(col.sql, false)
      } catch { /* 列可能已存在 */ }
    }
    await this.conn.execute('CREATE INDEX IF NOT EXISTS idx_email_invoices_email_date ON local_email_invoices(email_date DESC);', false).catch((e) => {
      // §948：DDL / 回填失败不致命，但必须留痕 —— 索引没建出来 ⇒ 列表查询变慢；
      // 表没建出来 ⇒ 对应功能直接不可用；回填没跑 ⇒ updated_at 不收敛。
      console.warn('[local-db] DDL 执行失败（本地库已退化）：', e)
    })
    await this.conn.execute('CREATE INDEX IF NOT EXISTS idx_email_invoices_dirty ON local_email_invoices(dirty);', false).catch((e) => {
      // §948：DDL / 回填失败不致命，但必须留痕 —— 索引没建出来 ⇒ 列表查询变慢；
      // 表没建出来 ⇒ 对应功能直接不可用；回填没跑 ⇒ updated_at 不收敛。
      console.warn('[local-db] DDL 执行失败（本地库已退化）：', e)
    })
    await this.conn.execute('CREATE INDEX IF NOT EXISTS idx_emails_updated ON local_emails(updated_at DESC);', false).catch((e) => {
      // §948：DDL / 回填失败不致命，但必须留痕 —— 索引没建出来 ⇒ 列表查询变慢；
      // 表没建出来 ⇒ 对应功能直接不可用；回填没跑 ⇒ updated_at 不收敛。
      console.warn('[local-db] DDL 执行失败（本地库已退化）：', e)
    })
    await this.conn.execute('CREATE INDEX IF NOT EXISTS idx_meetings_updated ON local_meetings(updated_at DESC);', false).catch((e) => {
      // §948：DDL / 回填失败不致命，但必须留痕 —— 索引没建出来 ⇒ 列表查询变慢；
      // 表没建出来 ⇒ 对应功能直接不可用；回填没跑 ⇒ updated_at 不收敛。
      console.warn('[local-db] DDL 执行失败（本地库已退化）：', e)
    })
    await this.conn.execute(
      'UPDATE local_emails SET updated_at = COALESCE(NULLIF(updated_at, 0), date, created_at) WHERE IFNULL(updated_at, 0) = 0;',
      false,
    ).catch((e) => {
      // §948：DDL / 回填失败不致命，但必须留痕 —— 索引没建出来 ⇒ 列表查询变慢；
      // 表没建出来 ⇒ 对应功能直接不可用；回填没跑 ⇒ updated_at 不收敛。
      console.warn('[local-db] DDL 执行失败（本地库已退化）：', e)
    })
    await this.conn.execute(
      'UPDATE local_meetings SET updated_at = COALESCE(NULLIF(updated_at, 0), started_at, created_at) WHERE IFNULL(updated_at, 0) = 0;',
      false,
    ).catch((e) => {
      // §948：DDL / 回填失败不致命，但必须留痕 —— 索引没建出来 ⇒ 列表查询变慢；
      // 表没建出来 ⇒ 对应功能直接不可用；回填没跑 ⇒ updated_at 不收敛。
      console.warn('[local-db] DDL 执行失败（本地库已退化）：', e)
    })
    await this.conn.execute(`
      CREATE TABLE IF NOT EXISTS local_list_snapshots (
        namespace TEXT NOT NULL,
        id TEXT NOT NULL,
        payload TEXT NOT NULL DEFAULT '{}',
        updated_at INTEGER NOT NULL,
        dirty INTEGER NOT NULL DEFAULT 0,
        PRIMARY KEY (namespace, id)
      );
    `, false).catch((e) => {
      // §948：DDL / 回填失败不致命，但必须留痕 —— 索引没建出来 ⇒ 列表查询变慢；
      // 表没建出来 ⇒ 对应功能直接不可用；回填没跑 ⇒ updated_at 不收敛。
      console.warn('[local-db] DDL 执行失败（本地库已退化）：', e)
    })
    await this.conn.execute(`
      CREATE TABLE IF NOT EXISTS local_ai_conversations (
        id TEXT PRIMARY KEY,
        title TEXT NOT NULL DEFAULT '',
        model TEXT NOT NULL DEFAULT 'auto',
        mode TEXT NOT NULL DEFAULT 'single',
        agent_id TEXT,
        custom_system_prompt TEXT,
        archived_at INTEGER,
        created_at INTEGER NOT NULL,
        updated_at INTEGER NOT NULL
      );
    `, false).catch((e) => {
      // §948：DDL / 回填失败不致命，但必须留痕 —— 索引没建出来 ⇒ 列表查询变慢；
      // 表没建出来 ⇒ 对应功能直接不可用；回填没跑 ⇒ updated_at 不收敛。
      console.warn('[local-db] DDL 执行失败（本地库已退化）：', e)
    })
    await this.conn.execute(`
      CREATE TABLE IF NOT EXISTS local_ai_messages (
        id TEXT PRIMARY KEY,
        conversation_id TEXT NOT NULL,
        role TEXT NOT NULL,
        content TEXT NOT NULL DEFAULT '',
        payload TEXT NOT NULL DEFAULT '{}',
        created_at INTEGER NOT NULL,
        updated_at INTEGER NOT NULL
      );
    `, false).catch((e) => {
      // §948：DDL / 回填失败不致命，但必须留痕 —— 索引没建出来 ⇒ 列表查询变慢；
      // 表没建出来 ⇒ 对应功能直接不可用；回填没跑 ⇒ updated_at 不收敛。
      console.warn('[local-db] DDL 执行失败（本地库已退化）：', e)
    })
    await this.conn.execute(
      "INSERT OR IGNORE INTO _schema_migrations (version, description, applied_at) VALUES ('2026-09-08-list-sync-v1', '列表本地优先 dirty/email_date/client_id', strftime('%s', 'now') * 1000);",
      false,
    )
  }

  private async runEmailInboxV1Migration(): Promise<void> {
    if (!this.conn) return
    await this.conn.execute(`
      CREATE TABLE IF NOT EXISTS _schema_migrations (
        version TEXT PRIMARY KEY,
        description TEXT,
        applied_at INTEGER NOT NULL
      );
    `, false)
    // 2026-10-06 修正：本方法曾是**唯一**还绕开守卫的那个，代价是每次冷启动
    // 打 4 条 `Execute: duplicate column name`（deleted_at / body_purged / folder /
    // action_reason）。真机捕获里它们每次启动固定出现 ⇒ **真正的错误会被埋在里面**
    // （本轮那张 ReferenceError 就夹在它们中间差点被当成噪音放过）。
    //
    // 根因不是「init 期间不能查」——`queryForMigration` 只要 `this.conn`，init 期间可用
    // （见本文件 queryForMigration 上方的注释：当时坏的是**另一个** requireReady 助手 queryOne）。
    // 真正的原因是：本方法先改用 `this.conn.execute` 绕开，随后 `queryForMigration`
    // 被引入并修了另外 6 个迁移（「修一处不够」），**唯独这里一直没转换过来**。
    // 于是版本号只写不读（下面第 464 行一直在 INSERT，却从没有过 if (done) return），
    // 补列循环也就一直裸奔。
    //
    // 现在与三个兄弟迁移完全同形：先版本早退，再逐列查 pragma_table_info。
    const done = await this.queryForMigration<{ version: string }>(
      "SELECT version FROM _schema_migrations WHERE version = '2026-09-08-email-inbox-v1'",
    )
    if (done) return
    for (const col of EMAIL_INBOX_V1_COLUMNS) {
      const exists = await this.queryForMigration<{ cnt: number }>(
        `SELECT COUNT(*) AS cnt FROM pragma_table_info('${col.table}') WHERE name = ?`,
        [col.column],
      )
      if (exists && exists.cnt > 0) continue
      try {
        await this.conn.execute(col.sql, false)
      } catch {
        // 列可能已存在，忽略
      }
    }
    await this.conn.execute(
      "INSERT OR IGNORE INTO _schema_migrations (version, description, applied_at) VALUES ('2026-09-08-email-inbox-v1', '邮件伪删除 deleted_at/body_purged', strftime('%s', 'now') * 1000);",
      false,
    )
  }

  /** 会话听见式录音：session_id / 句级译文 / 分片路径。 */
  private async runLiveRecordV1Migration(): Promise<void> {
    if (!this.conn) return
    await this.conn.execute(`
      CREATE TABLE IF NOT EXISTS _schema_migrations (
        version TEXT PRIMARY KEY,
        description TEXT,
        applied_at INTEGER NOT NULL
      );
    `, false)
    await this.conn.execute(`
      CREATE TABLE IF NOT EXISTS local_meeting_audio_parts (
        id TEXT PRIMARY KEY,
        meeting_id TEXT NOT NULL,
        seq INTEGER NOT NULL,
        mime_type TEXT NOT NULL,
        data_base64 TEXT NOT NULL,
        file_path TEXT,
        created_at INTEGER NOT NULL
      );
    `, false)
    await this.conn.execute(
      'CREATE INDEX IF NOT EXISTS idx_meetings_session ON local_meetings(session_id);',
      false,
    )
    for (const col of LIVE_RECORD_V1_COLUMNS) {
      const exists = await this.queryForMigration<{ cnt: number }>(
        `SELECT COUNT(*) AS cnt FROM pragma_table_info('${col.table}') WHERE name = ?`,
        [col.column],
      )
      if (exists && exists.cnt > 0) continue
      try {
        await this.conn.execute(col.sql, false)
      } catch { /* 列可能已存在 */ }
    }
    await this.conn.execute(
      "INSERT OR IGNORE INTO _schema_migrations (version, description, applied_at) VALUES ('2026-09-08-live-record-v1', '会话录音 session_id/译文/分片路径', strftime('%s', 'now') * 1000);",
      false,
    )
  }

  /** 笔记捕捉：草稿/分级存储列 + 附件表 + FTS 改索引 search_text。 */
  private async runNotesCaptureV1Migration(): Promise<void> {
    if (!this.conn) return
    await this.conn.execute(`
      CREATE TABLE IF NOT EXISTS _schema_migrations (
        version TEXT PRIMARY KEY,
        description TEXT,
        applied_at INTEGER NOT NULL
      );
    `, false)
    const done = await this.queryForMigration<{ version: string }>(
      "SELECT version FROM _schema_migrations WHERE version = '2026-09-08-notes-capture-v1'",
    )
    if (done) return

    for (const col of NOTES_CAPTURE_V1_COLUMNS) {
      const exists = await this.queryForMigration<{ cnt: number }>(
        `SELECT COUNT(*) AS cnt FROM pragma_table_info('${col.table}') WHERE name = ?`,
        [col.column],
      )
      if (exists && exists.cnt > 0) continue
      try {
        await this.conn.execute(col.sql, false)
      } catch { /* 列可能已存在 */ }
    }

    await this.conn.execute(`
      CREATE TABLE IF NOT EXISTS local_note_files (
        id TEXT PRIMARY KEY,
        note_id TEXT NOT NULL,
        kind TEXT NOT NULL,
        rel_path TEXT,
        mime TEXT,
        size_bytes INTEGER DEFAULT 0,
        duration_ms INTEGER DEFAULT 0,
        data_base64 TEXT,
        created_at INTEGER NOT NULL
      );
    `, false)
    await this.conn.execute(
      'CREATE INDEX IF NOT EXISTS idx_note_files_note ON local_note_files(note_id);',
      false,
    )
    await this.conn.execute('CREATE INDEX IF NOT EXISTS idx_notes_status ON local_notes(status) WHERE deleted_at IS NULL;', false).catch((e) => {
      // §948：DDL / 回填失败不致命，但必须留痕 —— 索引没建出来 ⇒ 列表查询变慢；
      // 表没建出来 ⇒ 对应功能直接不可用；回填没跑 ⇒ updated_at 不收敛。
      console.warn('[local-db] DDL 执行失败（本地库已退化）：', e)
    })

    // BUG-AH：三个 FTS 触发器原来是把**多语句字符串**整块丢给
    // `this.conn.execute()`。触发器体里本来就带分号（`local_notes_ad` / `_au`
    // 体内各有 1~2 条以 `;` 结尾的语句），插件按「单条语句」解析，
    // 于是从第一个分号处截断 → `incomplete input (code 1)`，
    // **三个触发器一个都没建成**（真机 sqlite_master 实测：只有 FTS 虚表与
    // 它的影子表，零个 trigger）。
    //
    // 后果不是报错而是**索引悄悄失同步**：搜索走
    // `local_notes_fts MATCH`（notes-search.ts），而 ad/au 缺失意味着
    // 删改笔记不会从索引里摘掉旧行 → 搜到已删除或旧内容的笔记。
    // 之前 `notes-fts-ready.ts` 的全量回灌让行数一度对得上，掩盖了这一点。
    //
    // 修法与 SCHEMA_SQL 路径保持一致：交给 splitSqlStatements 切分后逐条执行
    // （那条路已被证明有效 —— FTS 虚表就是它建出来的）。
    // 回归/证伪：scripts/check-fts-triggers-device.mjs 直接查设备上的 sqlite_master。
    const ftsTriggerDdl = `
      CREATE TRIGGER IF NOT EXISTS local_notes_ai AFTER INSERT ON local_notes BEGIN
        INSERT INTO local_notes_fts(rowid, title, content)
        VALUES (new.rowid, new.title, COALESCE(NULLIF(new.search_text, ''), new.content));
      END;
      CREATE TRIGGER IF NOT EXISTS local_notes_ad AFTER DELETE ON local_notes BEGIN
        INSERT INTO local_notes_fts(local_notes_fts, rowid, title, content)
        VALUES ('delete', old.rowid, old.title, COALESCE(NULLIF(old.search_text, ''), old.content));
      END;
      CREATE TRIGGER IF NOT EXISTS local_notes_au AFTER UPDATE ON local_notes BEGIN
        INSERT INTO local_notes_fts(local_notes_fts, rowid, title, content)
        VALUES ('delete', old.rowid, old.title, COALESCE(NULLIF(old.search_text, ''), old.content));
        INSERT INTO local_notes_fts(rowid, title, content)
        VALUES (new.rowid, new.title, COALESCE(NULLIF(new.search_text, ''), new.content));
      END;
    `
    const ftsTriggerStmts = splitSqlStatements(ftsTriggerDdl)
    if (ftsTriggerStmts.length !== 3) {
      // 切分数量不对说明 DDL 写坏了，宁可报错也不要静默建一半
      throw new Error(`[localDB] FTS trigger DDL split into ${ftsTriggerStmts.length} statements, expected 3`)
    }

    // ⚠⚠ Web / sql.js 回退没有 FTS5 ⇒ `local_notes_fts` 虚表**不存在**。
    //   但上面这段 DDL 会**重建三个触发器**（触发器体引用该虚表）。
    //   ⇒ 触发器在、虚表不在 ⇒ 之后任何一条 `INSERT INTO local_notes`
    //     都会报 `no such table: main.local_notes_fts`（实测 Web 全新建库）。
    //   ⇒ 这里必须**先确认虚表存在**，否则一个触发器都不建。
    //   旧触发器也要删：它们可能是在「虚表还在」的旧库里留下的，
    //   留着会让同样的 INSERT 继续失败。
    const notesFtsExists = await this.queryForMigration<{ cnt: number }>(
      "SELECT COUNT(*) AS cnt FROM sqlite_master WHERE type = 'table' AND name = 'local_notes_fts'",
    )
    if (!notesFtsExists || notesFtsExists.cnt === 0) {
      try {
        await this.conn.execute('DROP TRIGGER IF EXISTS local_notes_ai;', false)
        await this.conn.execute('DROP TRIGGER IF EXISTS local_notes_ad;', false)
        await this.conn.execute('DROP TRIGGER IF EXISTS local_notes_au;', false)
      } catch (e) {
        console.warn('[localDB] notes FTS trigger cleanup skipped:', e)
      }
    } else {
      try {
        await this.conn.execute('DROP TRIGGER IF EXISTS local_notes_ai;', false)
        await this.conn.execute('DROP TRIGGER IF EXISTS local_notes_ad;', false)
        await this.conn.execute('DROP TRIGGER IF EXISTS local_notes_au;', false)
        for (const one of ftsTriggerStmts) {
          await this.conn.execute(normalizeTriggerForPluginExecute(one), false)
        }
      } catch (e) {
        console.warn('[localDB] notes FTS trigger rebuild skipped:', e)
      }
    }

    await this.conn.execute(
      "INSERT OR IGNORE INTO _schema_migrations (version, description, applied_at) VALUES ('2026-09-08-notes-capture-v1', '笔记草稿/分级存储/附件', strftime('%s', 'now') * 1000);",
      false,
    )
  }

  /**
   * §898：向量表加 `chunks` 列（一行装几个段向量）。
   *
   * ⚠ 与别的迁移不同，这里**不用 `_schema_migrations` 的版本标记**：
   *   本仓库的迁移标记是「跑一次就不再跑」，而加列是**幂等且必须每次核对**的
   *   （旧库可能是在别处建的、也可能上次加到一半失败）。
   *   ⇒ 每次开库都查 `pragma_table_info`，缺就补 —— 这与既有 `*_COLUMNS` 的写法一致，
   *     也天然覆盖「表刚被 CREATE TABLE IF NOT EXISTS 建出来、列已在 DDL 里」的情况。
   */
  private async runNoteVectorsV1Migration(): Promise<void> {
    if (!this.conn) return
    for (const col of NOTE_VECTORS_V1_COLUMNS) {
      const exists = await this.queryForMigration<{ cnt: number }>(
        `SELECT COUNT(*) AS cnt FROM pragma_table_info('${col.table}') WHERE name = ?`,
        [col.column],
      )
      if (exists && exists.cnt > 0) continue
      try {
        await this.conn.execute(col.sql, false)
      } catch {
        // 列可能已存在（并发开库），忽略
      }
    }
  }

  /**
   * 配置表归属列（2026-10-29）：给 local_user_settings / local_config_outbox
   * 补 owner_user_id / owner_workspace_id。
   *
   * ⚠ 与 §898 的向量列迁移同样**不用版本标记**、每次开库都核对：
   *   加列是幂等的，而归属列直接决定「哪些行允许被上传」⇒ 缺列就必须补，
   *   绝不能因为「上次跑过了」就假定列还在。
   *
   * ⚠ **不回填存量行**：见 CONFIG_OWNERSHIP_V1_COLUMNS 的说明。
   */
  private async runConfigOwnershipV1Migration(): Promise<void> {
    if (!this.conn) return
    for (const col of CONFIG_OWNERSHIP_V1_COLUMNS) {
      const exists = await this.queryForMigration<{ cnt: number }>(
        `SELECT COUNT(*) AS cnt FROM pragma_table_info('${col.table}') WHERE name = ?`,
        [col.column],
      )
      if (exists && exists.cnt > 0) continue
      try {
        await this.conn.execute(col.sql, false)
      } catch {
        // 列可能已存在（并发开库），忽略
      }
    }
  }

  /**
   * 把配置表重建成带归属的主键（2026-10-29）。
   *
 * ⚠ 只在**主键还不是**带归属的版本时才重建：每次开库都查 pragma_table_info，
   *   确认主键列已含 owner_user_id/owner_workspace_id 就跳过 —— 与 §898 的
   *   向量列迁移同思路（幂等、必须每次核对）。
   *
 * ⚠ 重建**不搬任何旧行**：旧行归属无从证明，搬过去等于替它认领。
   */
  private async runConfigOwnershipV1Rebuild(): Promise<void> {
    if (!this.conn) return
    for (const spec of CONFIG_OWNERSHIP_V1_REBUILD) {
      const columns = await this.queryAllForMigration<{ name: string; pk: number }>(
        `SELECT name, pk FROM pragma_table_info('${spec.table}')`,
      )
      if (!columns || columns.length === 0) continue
      const primaryKey = columns.filter(c => c.pk > 0).map(c => c.name).sort().join(',')
      const wanted = 'id,namespace,owner_user_id,owner_workspace_id'
      if (primaryKey === wanted) continue
      try {
        await this.conn.execute(spec.drop, false)
        await this.conn.execute(spec.create, false)
        // ⚠ 旧表先改名让位，再把新表改名到位 —— 顺序不能反，
        //   否则新表会被旧表的名字挡住。
        await this.conn.execute(`ALTER TABLE ${spec.table} RENAME TO ${spec.table}_legacy_v1`, false)
        await this.conn.execute(spec.rename, false)
        await this.conn.execute(`DROP TABLE IF EXISTS ${spec.table}_legacy_v1`, false)
      } catch (error) {
        // 重建失败 ⇒ 旧表仍在（还没被改名），功能退回旧行为，不丢数据。
        console.warn(`[local-db] ${spec.table} 归属重建失败：`, error)
      }
    }
  }

  /**
   * 会议/录音/待办/声纹表补归属列（2026-10-29）。
   *
   * ⚠ 与 §898 的向量列迁移同样**不用版本标记**、每次开库都核对：
   *   加列是幂等的，而归属列直接决定「哪些行允许被读出/上传」⇒ 缺列就必须补。
   */
  private async runMeetingOwnershipV1Migration(): Promise<void> {
    if (!this.conn) return
    for (const col of MEETING_OWNERSHIP_V1_COLUMNS) {
      const exists = await this.queryForMigration<{ cnt: number }>(
        `SELECT COUNT(*) AS cnt FROM pragma_table_info('${col.table}') WHERE name = ?`,
        [col.column],
      )
      if (exists && exists.cnt > 0) continue
      try {
        await this.conn.execute(col.sql, false)
      } catch {
        // 列可能已存在（并发开库），忽略
      }
    }
  }

  /**
   * 把会议/录音/待办/声纹表重建成带归属的主键（2026-10-29）。
   *
   * ⚠ 与 config 重建的**关键差异**：这里**搬运旧行**（归属置空串 = 未认领），
     而不是丢弃 —— 会议录音不可从云端恢复（见 MEETING_OWNERSHIP_V1_COLUMNS 的说明）。
   *
   * ⚠ 顺序是**硬约束**（实测 SQLite 3.51 得到）：
   *   1. **先改子表名**（segments / audio_parts）—— 它们是叶子表，没人引用它们；
   *   2. **再改父表名**（meetings）；
   *   3. 建最终名的新表、搬运、丢旧表。
   *
   *   为什么不能反过来：`ALTER TABLE ... RENAME` 会**改写子表的外键子句** ⇒
   *   先改父表名会让子表外键指向 `local_meetings_legacy_v1`（一张即将被删的表）
   *   ⇒ 级联删除静默失效。
   *
   * ⚠ 搬运用普通 `INSERT`（不是 `INSERT OR IGNORE`）：`OR IGNORE` 会把外键违规
   *   **静默吞掉** ⇒ 子表整批搬运会「零行成功、零报错」，看起来迁移成功、数据却没了。
   */
  private async runMeetingOwnershipV1Rebuild(): Promise<void> {
    if (!this.conn) return
    const specs = MEETING_OWNERSHIP_V1_REBUILD
    // 只处理**还没重建**的表（主键已含归属列 ⇒ 跳过）。
    const pending: MeetingOwnershipRebuildSpec[] = []
    for (const spec of specs) {
      const columns = await this.queryAllForMigration<{ name: string; pk: number }>(
        `SELECT name, pk FROM pragma_table_info('${spec.table}')`,
      )
      if (!columns || columns.length === 0) continue
      const primaryKey = columns.filter(c => c.pk > 0).map(c => c.name).sort().join(',')
      const wanted = 'id,owner_user_id,owner_workspace_id'
      if (primaryKey === wanted) continue
      pending.push(spec)
    }
    if (pending.length === 0) return

    // ⚠ 第 1 步：先把**子表**改名让位（叶子表）。
    for (const spec of pending) {
      if (!spec.childOf) continue
      await this.conn.execute(`ALTER TABLE ${spec.table} RENAME TO ${spec.table}_legacy_v1`, false)
    }
    // ⚠ 第 2 步：再把**父表**改名让位。
    for (const spec of pending) {
      if (!spec.parent) continue
      await this.conn.execute(`ALTER TABLE ${spec.table} RENAME TO ${spec.table}_legacy_v1`, false)
    }
    // ⚠ 第 2b 步：**独立表**（没有父子关系）也要改名让位 —— 否则第 3 步的
    //   建表会撞上同名（CREATE TABLE IF NOT EXISTS 静默跳过 ⇒ 新表根本没建）
    //   而搬运语句却从**旧表**读（数据搬完看起来对，但旧表的行永远还在）。
    for (const spec of pending) {
      if (!spec.standalone) continue
      await this.conn.execute(`ALTER TABLE ${spec.table} RENAME TO ${spec.table}_legacy_v1`, false)
    }
    // ⚠ 第 2c 步：把**旧表缺的列补齐**。
    //   历史库的表形状**各不相同**（有的没有 `location`、有的没有 `session_id` ——
    //   它们是历次迁移陆续补上去的）⇒ 搬运语句 SELECT 全部列时会在缺列的库上
    //   报「no such column」⇒ **整张表迁移失败**，行留在旧表里没搬过来。
    //   ⇒ 先给旧表补上缺的列（补 NULL / 默认值），再搬。
    for (const spec of pending) {
      const present = new Set((await this.queryAllForMigration<{ name: string }>(
        `SELECT name FROM pragma_table_info('${spec.table}_legacy_v1')`,
      )).map(c => c.name))
      for (const column of spec.columns) {
        if (present.has(column.name)) continue
        try {
          await this.conn.execute(
            // ⚠ 补列一律用**可空**类型：SQLite 不允许给 NOT NULL 且无默认值的
            //   列加到**已有行**的表上（报「NOT NULL constraint failed」）⇒
            //   用 NOT NULL 的 DDL 补列，旧库一有行就会失败。
            //   新表那一侧才是带约束的形状（create 语句里写死）。
            `ALTER TABLE ${spec.table}_legacy_v1 ADD COLUMN ${column.name} ${column.ddl.replace(/ NOT NULL/i, '')}`, false,
          )
        } catch {
          // 列可能已被并发补上，忽略
        }
      }
    }
    // ⚠ 第 3 步：**先建**所有新表（父表要先于子表 —— 子表的外键指向父表的
    //   最终名，父表还没就位时子表建不出来）。
    //   ⚠ 表名用临时的 `*_ownership_v1` ⇒ 父表建好之后**子表的外键才指向
    //   一个存在的表** ⇒ 不会留下「外键指向不存在表」的隐患。
    for (const spec of pending) {
      try {
        await this.conn.execute(spec.drop, false)
        await this.conn.execute(spec.create, false)
      } catch (error) {
        console.warn(`[local-db] ${spec.table} 新表创建失败：`, error)
      }
    }
    // ⚠ 第 4 步：搬运旧行（父表先搬 —— 子表的外键要求父行**已经存在**）。
    //   ⚠ 顺序不能反：先搬子表会因外键失败（父行还没搬过去）。
    for (const spec of pending) {
      try {
        await this.conn.execute(spec.copy, false)
      } catch (error) {
        console.warn(`[local-db] ${spec.table} 归属搬运失败：`, error)
      }
    }
    // ⚠ 第 5 步：把新表**改名到位**（**父表先**，子表后），再丢旧表。
    //   ⚠ 父表先改名是关键：子表的外键此刻指向 `local_meetings_ownership_v1`
    //   ⇒ 父表改名时 SQLite 会**自动改写**子表的外键子句 ⇒ 指向最终名。
    //   ⚠ 顺序反了（先子表后父表）也一样能work，但父表先更安全：
    //   子表改名到位时父表已是最终名，外键引用保持有效。
    //   ⚠ 改名到位前旧表已改名为 `*_legacy_v1` ⇒ 最终名是空的 ⇒ 不会撞名。
    for (const spec of pending) {
      try {
        await this.conn.execute(spec.rename, false)
      } catch (error) {
        console.warn(`[local-db] ${spec.table} 归属改名失败：`, error)
      }
    }
    for (const spec of pending) {
      try {
        await this.conn.execute(`DROP TABLE IF EXISTS ${spec.table}_legacy_v1`, false)
      } catch (error) {
        // 丢旧表失败 ⇒ 数据在新表里，旧表是冗余副本；下次开库会再试一次。
        console.warn(`[local-db] ${spec.table} 旧表清理失败：`, error)
      }
    }
  }

  /** 会议工作台：归档 / 标签 / 主题 / 总结技能 / 待办关联。 */
  private async runMeetingsStudioV1Migration(): Promise<void> {
    if (!this.conn) return
    await this.conn.execute(`
      CREATE TABLE IF NOT EXISTS _schema_migrations (
        version TEXT PRIMARY KEY,
        description TEXT,
        applied_at INTEGER NOT NULL
      );
    `, false)
    for (const col of MEETINGS_STUDIO_V1_COLUMNS) {
      const exists = await this.queryForMigration<{ cnt: number }>(
        `SELECT COUNT(*) AS cnt FROM pragma_table_info('${col.table}') WHERE name = ?`,
        [col.column],
      )
      if (exists && exists.cnt > 0) continue
      try {
        await this.conn.execute(col.sql, false)
      } catch { /* 列可能已存在 */ }
    }
    await this.conn.execute(
      "INSERT OR IGNORE INTO _schema_migrations (version, description, applied_at) VALUES ('2026-09-08-meetings-studio-v1', '会议归档/标签/主题/技能/待办关联', strftime('%s', 'now') * 1000);",
      false,
    )
  }

  /** 关闭并清理连接，允许重新初始化 */
  async close(): Promise<void> {
    return this.enqueue(() => this.closeConnection())
  }

  private async closeConnection(): Promise<void> {
    this.initialized = false
    this.activationBlocked = true
    this.leases.invalidateConnection()
    if (this.conn) {
      if (this.transactionUncertain) {
        // jeep close exports the database: never persist an unresolved transaction.
        if ((await this.conn.isTransactionActive()).result !== false) {
          await this.conn.rollbackTransaction()
        }
        this.transactionUncertain = false
      }
      // The manager closes an open handle and removes an unopened/closed one.
      // Calling conn.close first is not retry-safe: Android rejects closed handles.
      await this.sqlite.closeConnection(DB_NAME, false)
      this.conn = null
    }
    this.activationBlocked = false
  }

  /** 是否已初始化 */
  isReady(): boolean {
    return this.initialized && this.conn !== null
  }

  /**
   * 执行写操作（DDL / 多语句）。返回受影响行数。
   * 整个 statements 作为一个事务提交；旧 transaction 参数保留位置兼容。
   * 不接受 BEGIN/COMMIT/SAVEPOINT 等事务控制；使用 runInTransaction 管理批次。
   */
  async execute(statements: string, _transaction = false, lease?: OperationLease): Promise<number> {
    lease ??= this.captureOperation({ kind: 'local-edit' })
    return this.enqueue(async () => {
      this.leases.assertCurrent(lease)
      this.requireReady()
      assertNoTransactionControl(statements)
      const conn = this.conn!
      const res = await this.inTransaction(conn, lease, () => conn.execute(statements, false))
      this.leases.assertCurrent(lease, 'committed')
      return res.changes?.changes ?? 0
    })
  }

  /**
   * 执行单条参数化语句（INSERT/UPDATE/DELETE），values 用 ? 占位。
   */
  async run(sql: string, values: unknown[] = [], lease?: OperationLease): Promise<number> {
    lease ??= this.captureOperation({ kind: 'local-edit' })
    const params = [...values]
    return this.enqueue(async () => {
      this.leases.assertCurrent(lease)
      this.requireReady()
      assertNoTransactionControl(sql)
      const conn = this.conn!
      const res = await this.inTransaction(conn, lease, () => conn.run(sql, params, false))
      this.leases.assertCurrent(lease, 'committed')
      return res.changes?.changes ?? 0
    })
  }

  /**
   * 在单个事务中依次执行多条参数化语句（INSERT/UPDATE/DELETE）。
   *
   * 连接队列内显式管理事务：任意一条失败则整批回滚，
   * 保证原子性。每条语句用 `values` 数组对应 `?` 占位符，避免 SQL 注入。
   *
   * @param statements `{ statement, values }` 列表
   * @returns 受影响的总行数
   */
  async runInTransaction(
    statements: { statement: string; values: unknown[] }[],
    lease?: OperationLease,
  ): Promise<number> {
    lease ??= this.captureOperation({ kind: 'local-edit' })
    const batch = statements.map(({ statement, values }) => ({ statement, values: [...values] }))
    return this.enqueue(async () => {
      this.leases.assertCurrent(lease)
      this.requireReady()
      for (const { statement } of batch) assertNoTransactionControl(statement)
      if (batch.length === 0) return 0
      const conn = this.conn!
      const res = await this.inTransaction(conn, lease, () => conn.executeSet(batch, false))
      this.leases.assertCurrent(lease, 'committed')
      const changes = res.changes?.changes ?? 0
      return typeof changes === 'number' ? changes : 0
    })
  }

  /**
   * 查询返回多行。values 对应 ? 占位。
   */
  async query<T = Record<string, unknown>>(sql: string, values: unknown[] = [], lease?: OperationLease): Promise<T[]> {
    lease ??= this.captureOperation({ kind: 'local-edit' })
    const params = [...values]
    return this.enqueue(() => this.queryRows<T>(sql, params, lease))
  }

  private async queryRows<T>(sql: string, params: unknown[], lease: OperationLease): Promise<T[]> {
    this.leases.assertCurrent(lease)
    this.requireReady()
    assertNoTransactionControl(sql)
    assertReadOnlyQuery(sql)
    const res = await this.conn!.query(sql, params)
    this.leases.assertCurrent(lease)
    return (res.values ?? []) as T[]
  }

  /** 查询单行，无结果返回 null。 */
  async queryOne<T = Record<string, unknown>>(sql: string, values: unknown[] = [], lease?: OperationLease): Promise<T | null> {
    lease ??= this.captureOperation({ kind: 'local-edit' })
    const params = [...values]
    return this.enqueue(async () => {
      const rows = await this.queryRows<T>(sql, params, lease)
      this.leases.assertCurrent(lease)
      return rows.length > 0 ? rows[0] : null
    })
  }

  /**
   * 迁移专用的免守卫查询（BUG-AI）。
   *
   * 背景：`init()` 在开头把 `initialized = false`，直到**所有迁移跑完**才置 true；
   * 而 `query/execute/run` 都先 `requireReady()`，于是 init 期间调用必抛
   * 「LocalDB 未初始化，请先调用 init(dbSecret)」。
   *
   * 真机实测（https 包，`diag-finance-view-vanish.mjs` 抓的 console）：
   * 7 个迁移里有 6 个整条挂掉，只剩 console.warn：
   *   [localDB] meetings v2 migration failed: LocalDB 未初始化…
   *   [localDB] email sync v1 / live record v1 / notes capture v1
   *   [localDB] meetings studio v1 / list sync v1 migration failed: …
   * 全新安装看不出问题（SCHEMA_SQL 已经建全表），
   * 但**增量迁移要补的那些列，老库永远补不上** —— 典型升级期才爆的坑。
   *
   * 为什么之前只坏一半：`runEmailInboxV1Migration` 早就发现了这件事
   * （388-391 行的注释写着「init 期间 initialized=false，requireReady 会抛错，
   * 旧库永远补不上列」），并在那一个方法里改用 `this.conn.execute` 绕开 ——
   * 但另外 6 个方法还在走带守卫的助手。修一处不够。
   *
   * 这里只放行「有连接」这一条必要条件，不放宽任何 SQL 校验。
   */
  private async queryForMigration<T = Record<string, unknown>>(
    sql: string,
    values: unknown[] = [],
  ): Promise<T | null> {
    if (!this.conn) throw new Error('LocalDB 未初始化：连接不存在')
    const res = await this.conn.query(sql, values)
    const rows = (res.values ?? []) as T[]
    return rows.length > 0 ? rows[0] : null
  }

  /** 迁移期用的**多行**查询（queryForMigration 只返回第一行）。 */
  private async queryAllForMigration<T = Record<string, unknown>>(
    sql: string,
    values: unknown[] = [],
  ): Promise<T[]> {
    if (!this.conn) throw new Error('LocalDB 未初始化：连接不存在')
    const res = await this.conn.query(sql, values)
    return (res.values ?? []) as T[]
  }

  /**
   * 尝试加载 sqlite-vec 扩展（Android 原生）。
   * 若 SQLCipher 构建禁用了 load_extension 或文件不存在，静默失败——
   * 向量检索回退到 JS 余弦（见 vector.ts）。iOS 同理。
   */
  async tryLoadVecExtension(_soPath: string): Promise<boolean> {
    return this.enqueue(async () => {
      try {
        this.requireReady()
        await this.conn!.loadExtension(_soPath)
        return true
      } catch {
        return false
      }
    })
  }

  private requireReady() {
    if (this.transactionUncertain) {
      throw new Error('LocalDB 事务状态不确定，需关闭连接并重新初始化')
    }
    if (!this.initialized || !this.conn) {
      throw new Error('LocalDB 未初始化，请先调用 init(dbSecret)')
    }
  }

  // applySchemaBestEffort 已删除（2026-08-27 真机验证）：朴素 split(/;\s*\n/)
  // 同样会截断 FTS 触发体；建表统一走 open() 里的 splitSqlStatements 逐条执行。
}

/**
 * Web/sql.js 不含 FTS5：去掉虚拟表与相关触发器，避免整段 schema 失败。
 *
 * ⚠⚠ 审计修复（2026-10-29）：原来的正则只去掉 `local_notes_a[idu]` 三个触发器，
 *   **assets 的三个触发器留在了库里** ⇒ 触发器引用不存在的 `local_assets_fts`
 *   ⇒ Web 上任何一条 `INSERT INTO local_assets` 都报 `no such table: main.local_assets_fts`
 *   （实测：全新 Web 建库后插入资产直接失败）。
 *   ⇒ 现在按「FTS 触发器」整体去掉：凡是触发器体引用某个 `*_fts` 虚表的，
 *   一并去掉——留下任何一个都会让对应表的普通 INSERT 失败。
 */
function stripFts5ForWeb(sql: string): string {
  return sql
    .replace(/CREATE VIRTUAL TABLE[\s\S]*?;/gi, '-- FTS5 skipped on web\n')
    .replace(/CREATE TRIGGER IF NOT EXISTS \w+_a[idu][\s\S]*?END;/gi, '-- FTS trigger skipped on web\n')
}

/**
 * 把 LocalDB 适配成 SqlDb 接口，供离线持久化层（SqliteOutboxStore /
 * SqliteApprovalStore / MobileSyncRuntime）使用。LocalDB.run 已是写操作，
 * LocalDB.query 返回行数组，对应 SqlDb.all。
 */
export function localDbAsSql(localDBInstance: LocalDB): SqlDb {
  return {
    run: (sql, params) => localDBInstance.run(sql, params ?? []),
    all: <T extends SqlRow = SqlRow>(sql: string, params?: unknown[]) =>
      localDBInstance.query<T>(sql, params ?? []),
  }
}

/** 单例。全 App 共享一个本地加密库连接。 */
export const localDB = new LocalDB()
