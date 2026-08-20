/**
 * sqlDb.ts — P1 移动离线持久化的 SQL 执行抽象。
 *
 * 目标：让离线持久化 / outbox / 同步引擎的 SQL 可以在 Node 测试里跑真实
 * SQLite（node:sqlite DatabaseSync），在 App 里跑 LocalDB（Capacitor
 * SQLite + SQLCipher）。LocalDB 的 run/query 方法已结构化满足本接口，
 * 生产侧直接传 localDB 实例即可；测试侧用 node:sqlite 实现同一接口。
 *
 *   - run(sql, params)  → 写操作（INSERT/UPDATE/DELETE），返回受影响行数
 *   - all(sql, params)  → 读操作，返回行对象数组（值为 JS 基础类型）
 *
 * 注意：SQLite 动态类型里 INTEGER 列可能返回 number 或 bigint，使用方
 * （mobileSyncStore / outboxStore）统一用 Number(row.x) 归一。
 */

export interface SqlRow {
  [column: string]: unknown
}

export interface SqlDb {
  run(sql: string, params?: unknown[]): Promise<number>
  all(sql: string, params?: unknown[]): Promise<SqlRow[]>
}

/** 判定错误是否为 SQLite UNIQUE 约束冲突（幂等重放时用于去重判断）。 */
export function isUniqueViolation(err: unknown): boolean {
  const msg = err instanceof Error ? err.message : String(err)
  return /UNIQUE constraint failed/i.test(msg)
}
