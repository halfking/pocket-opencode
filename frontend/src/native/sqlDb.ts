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

/* ═══════════════════════════════════════════════════════════════════════════
 * §878：读「一列 JSON」这件事此前有**三份各自独立的实现、三种口径**：
 *   ① `outboxStore.ts` 裸 `JSON.parse` ⇒ 直接抛，一条坏 payload 能把整个 drain 堵死；
 *   ② `settings-store.ts` / `config-sync/outbox.ts` `catch { payload = {} }` ⇒ 谎报成空；
 *   ③ `approvalStore.ts` 保留空对象（**唯一带理由、非破坏**的那一种）。
 * 三份实现各自漂移 ⇒ 现在把「解码」这一个动作收成**一个落点**，
 * 把「这一处损坏了怎么办」的**决定**留在调用点上（那才是产品口径，§870）。
 * ═══════════════════════════════════════════════════════════════════════════ */

/** 解码结果。`ok:false` 时带原始串与原因，供调用方留证/告警。 */
export type JsonColumn<T> =
  | { ok: true; value: T }
  | { ok: false; raw: string; reason: string }

/**
 * §878：读一行里的 JSON 列，**绝不抛、绝不用 `{}` 冒充**。
 *
 * ⚠ 本函数**只负责解码**，不负责「这一行要不要」—— 那是调用点的决定：
 *   · 批量读（`listReady` / `listLocalSettings`）⇒ 单行损坏应当**跳过**，
 *     否则一条坏行会连累同批的其它行（§878.2 就是这么堵死的）；
 *   · 点查（`get(id)`）⇒ 单行损坏**必须抛**，因为把损坏报成「没有这一行」
 *     正是 §876/D45 那个形态（谎报成空）。
 *   · 降级成 `{}` 只有在**证明降级非破坏**时才允许（见 `approvalStore.ts`）。
 *
 * @param where 表名/用途，写进告警文本，让排查的人知道是哪个队列/哪张表。
 */
export function readJsonColumn<T>(
  row: SqlRow,
  column: string,
  where: string,
): JsonColumn<T> {
  const raw = String(row[column] ?? 'null')
  try {
    return { ok: true, value: JSON.parse(raw) as T }
  } catch (e) {
    return {
      ok: false,
      raw,
      reason: e instanceof Error ? e.message : String(e),
    }
  }
}

/**
 * §878：损坏的 JSON 列的统一告警。
 *
 * ⚠ 为什么不在这里调 `preserveCorruptRaw`：那个函数把原始串**复制到 localStorage**，
 *   而这些行的原始字节**本来就在 SQLite 里**，且本节的原则是**损坏不许销毁**
 *   （§876/D45）—— 那行留在表里本身就是证据，再复制一份只会多一个累加器
 *   （§874 刚把那一族收干净）。
 */
export function warnCorruptColumn(where: string, rowId: string, reason: string): void {
  console.warn(
    `[sqlite-json] ${where} 的行 ${rowId} 存在损坏的 JSON 列，已跳过该行（原始字节保留在表里，未改写）。原因：${reason}`,
  )
}
