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
import { SCHEMA_SQL, splitSqlStatements } from './schema'

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
const EMAIL_SYNC_V1_COLUMNS = [
  { table: 'local_email_accounts', column: 'updated_at', sql: 'ALTER TABLE local_email_accounts ADD COLUMN updated_at INTEGER DEFAULT 0' },
  { table: 'local_email_invoices', column: 'file_name', sql: "ALTER TABLE local_email_invoices ADD COLUMN file_name TEXT DEFAULT ''" },
  { table: 'local_email_invoices', column: 'file_source', sql: "ALTER TABLE local_email_invoices ADD COLUMN file_source TEXT DEFAULT ''" },
  { table: 'local_email_invoices', column: 'attempts', sql: 'ALTER TABLE local_email_invoices ADD COLUMN attempts INTEGER DEFAULT 0' },
  { table: 'local_email_invoices', column: 'last_error', sql: "ALTER TABLE local_email_invoices ADD COLUMN last_error TEXT DEFAULT ''" },
  { table: 'local_email_invoices', column: 'feishu_sent_at', sql: 'ALTER TABLE local_email_invoices ADD COLUMN feishu_sent_at INTEGER DEFAULT 0' },
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
    // 关键修复：如果已有连接，先关闭它，防止重复createConnection报错
    if (this.conn) {
      try {
        await this.sqlite.closeConnection(DB_NAME, false)
      } catch {
        // 忽略关闭失败的错误（可能连接已经不存在）
      }
      this.conn = null
    }

    if (this.initialized) return

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
      const one = stmt.endsWith(';') ? stmt : `${stmt};`
      try {
        await this.conn.execute(one, false)
        applied++
      } catch (e) {
        failed++
        console.warn('[localDB] skip schema stmt:', one.slice(0, 60), e)
      }
    }
    if (failed > 0) {
      console.warn(`[localDB] schema applied ${applied}/${statements.length} statements (${failed} skipped)`)
    }

    // 增量迁移（已有库补列）— 此阶段 initialized 尚未置位，直接用 conn
    this.initialized = true
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
    const done = await this.queryOne<{ version: string }>(
      "SELECT version FROM _schema_migrations WHERE version = '2026-07-15-meetings-v2'",
    )
    if (done) return

    for (const col of MEETINGS_V2_COLUMNS) {
      const exists = await this.queryOne<{ cnt: number }>(
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
    const done = await this.queryOne<{ version: string }>(
      "SELECT version FROM _schema_migrations WHERE version = '2026-09-07-email-sync-v1'",
    )
    if (done) return

    for (const col of EMAIL_SYNC_V1_COLUMNS) {
      const exists = await this.queryOne<{ cnt: number }>(
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
