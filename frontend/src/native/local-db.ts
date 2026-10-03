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
]

const NOTES_CAPTURE_V1_COLUMNS = [
  { table: 'local_notes', column: 'status', sql: "ALTER TABLE local_notes ADD COLUMN status TEXT DEFAULT 'saved'" },
  { table: 'local_notes', column: 'storage_tier', sql: "ALTER TABLE local_notes ADD COLUMN storage_tier TEXT DEFAULT 'inline'" },
  { table: 'local_notes', column: 'summary', sql: 'ALTER TABLE local_notes ADD COLUMN summary TEXT' },
  { table: 'local_notes', column: 'search_text', sql: 'ALTER TABLE local_notes ADD COLUMN search_text TEXT' },
  { table: 'local_notes', column: 'body_path', sql: 'ALTER TABLE local_notes ADD COLUMN body_path TEXT' },
  { table: 'local_notes', column: 'media_json', sql: 'ALTER TABLE local_notes ADD COLUMN media_json TEXT' },
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

  constructor() {
    this.sqlite = new SQLiteConnection(CapacitorSQLite)
  }

  /**
   * 初始化本地加密库。dbSecret 是用户主密码（由 Keystore 派生）。
   * 幂等：重复调用安全。
   */
  async init(dbSecret: string): Promise<void> {
    if (!localDbNeedsOpen(this.initialized, this.conn !== null)) return
    this.initialized = false
    if (this.conn) {
      try {
        await this.sqlite.closeConnection(DB_NAME, false)
      } catch {
        // 忽略关闭失败的错误（可能连接已经不存在）
      }
      this.conn = null
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
      try {
        // SQLiteConnection 包装层接受字符串；底层 plugin 才会转成 {secret: passphrase}
        await this.sqlite.setEncryptionSecret(dbSecret)
      } catch (e) {
        // 若 secret 已存，再次设置可能抛错；这种场景下假定密码一致（用户重启 App 时常见）。
        // 真正的改密路径需要走 changeEncryptionSecret(oldPass, newPass)，MVP 暂不实现。
        console.warn('[localDB] setEncryptionSecret 已存或失败，沿用现有 secret:', e)
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
    for (const col of EMAIL_FOLDERS_V1_COLUMNS) {
      try { await this.conn.execute(col.sql, false) } catch { /* 列可能已存在 */ }
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
    await this.conn.execute('CREATE INDEX IF NOT EXISTS idx_email_invoices_email_date ON local_email_invoices(email_date DESC);', false).catch(() => {})
    await this.conn.execute('CREATE INDEX IF NOT EXISTS idx_email_invoices_dirty ON local_email_invoices(dirty);', false).catch(() => {})
    await this.conn.execute('CREATE INDEX IF NOT EXISTS idx_emails_updated ON local_emails(updated_at DESC);', false).catch(() => {})
    await this.conn.execute('CREATE INDEX IF NOT EXISTS idx_meetings_updated ON local_meetings(updated_at DESC);', false).catch(() => {})
    await this.conn.execute(
      'UPDATE local_emails SET updated_at = COALESCE(NULLIF(updated_at, 0), date, created_at) WHERE IFNULL(updated_at, 0) = 0;',
      false,
    ).catch(() => {})
    await this.conn.execute(
      'UPDATE local_meetings SET updated_at = COALESCE(NULLIF(updated_at, 0), started_at, created_at) WHERE IFNULL(updated_at, 0) = 0;',
      false,
    ).catch(() => {})
    await this.conn.execute(`
      CREATE TABLE IF NOT EXISTS local_list_snapshots (
        namespace TEXT NOT NULL,
        id TEXT NOT NULL,
        payload TEXT NOT NULL DEFAULT '{}',
        updated_at INTEGER NOT NULL,
        dirty INTEGER NOT NULL DEFAULT 0,
        PRIMARY KEY (namespace, id)
      );
    `, false).catch(() => {})
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
    `, false).catch(() => {})
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
    `, false).catch(() => {})
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
    // 不用 queryOne：init 期间 initialized=false，requireReady 会抛错，旧库永远补不上列。
    for (const col of EMAIL_INBOX_V1_COLUMNS) {
      try { await this.conn.execute(col.sql, false) } catch { /* 列可能已存在 */ }
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
    await this.conn.execute('CREATE INDEX IF NOT EXISTS idx_notes_status ON local_notes(status) WHERE deleted_at IS NULL;', false).catch(() => {})

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

    await this.conn.execute(
      "INSERT OR IGNORE INTO _schema_migrations (version, description, applied_at) VALUES ('2026-09-08-notes-capture-v1', '笔记草稿/分级存储/附件', strftime('%s', 'now') * 1000);",
      false,
    )
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
    if (this.conn) {
      try {
        await this.conn.close()
      } catch {
        // 忽略
      }
      try {
        await this.sqlite.closeConnection(DB_NAME, false)
      } catch {
        // 忽略
      }
      this.conn = null
    }
    this.initialized = false
  }

  /** 是否已初始化 */
  isReady(): boolean {
    return this.initialized && this.conn !== null
  }

  /**
   * 执行写操作（DDL / 多语句）。返回受影响行数。
   * transaction=true 时整个 statements 作为一个事务提交。
   */
  async execute(statements: string, transaction = false): Promise<number> {
    this.requireReady()
    const res = await this.conn!.execute(statements, transaction)
    return res.changes?.changes ?? 0
  }

  /**
   * 执行单条参数化语句（INSERT/UPDATE/DELETE），values 用 ? 占位。
   */
  async run(sql: string, values: unknown[] = []): Promise<number> {
    this.requireReady()
    const res = await this.conn!.run(sql, values)
    return res.changes?.changes ?? 0
  }

  /**
   * 在单个事务中依次执行多条参数化语句（INSERT/UPDATE/DELETE）。
   *
   * 底层走 conn.executeSet(set, transaction=true)：任意一条失败则整批回滚，
   * 保证原子性。每条语句用 `values` 数组对应 `?` 占位符，避免 SQL 注入。
   *
   * @param statements `{ statement, values }` 列表
   * @returns 受影响的总行数
   */
  async runInTransaction(
    statements: { statement: string; values: unknown[] }[],
  ): Promise<number> {
    this.requireReady()
    if (statements.length === 0) return 0
    const res = await this.conn!.executeSet(statements, true)
    const changes = res.changes?.changes ?? 0
    return typeof changes === 'number' ? changes : 0
  }

  /**
   * 查询返回多行。values 对应 ? 占位。
   */
  async query<T = Record<string, unknown>>(sql: string, values: unknown[] = []): Promise<T[]> {
    this.requireReady()
    const res = await this.conn!.query(sql, values)
    return (res.values ?? []) as T[]
  }

  /** 查询单行，无结果返回 null。 */
  async queryOne<T = Record<string, unknown>>(sql: string, values: unknown[] = []): Promise<T | null> {
    const rows = await this.query<T>(sql, values)
    return rows.length > 0 ? rows[0] : null
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

  /**
   * 尝试加载 sqlite-vec 扩展（Android 原生）。
   * 若 SQLCipher 构建禁用了 load_extension 或文件不存在，静默失败——
   * 向量检索回退到 JS 余弦（见 vector.ts）。iOS 同理。
   */
  async tryLoadVecExtension(_soPath: string): Promise<boolean> {
    try {
      await this.conn?.loadExtension(_soPath)
      return true
    } catch {
      return false
    }
  }

  private requireReady() {
    if (!this.initialized || !this.conn) {
      throw new Error('LocalDB 未初始化，请先调用 init(dbSecret)')
    }
  }

  // applySchemaBestEffort 已删除（2026-08-27 真机验证）：朴素 split(/;\s*\n/)
  // 同样会截断 FTS 触发体；建表统一走 open() 里的 splitSqlStatements 逐条执行。
}

/** Web/sql.js 不含 FTS5：去掉虚拟表与相关触发器，避免整段 schema 失败 */
function stripFts5ForWeb(sql: string): string {
  return sql
    .replace(/CREATE VIRTUAL TABLE[\s\S]*?;/gi, '-- FTS5 skipped on web\n')
    .replace(/CREATE TRIGGER IF NOT EXISTS local_notes_a[idu][\s\S]*?END;/gi, '-- FTS trigger skipped\n')
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
