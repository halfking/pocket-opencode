// 门禁：迁移方法里的「裸补列循环」——`for (const col of X_COLUMNS)` 循环内
// **没有**先查 `pragma_table_info` 就直接 `execute(col.sql)`。
//
// 为什么要有这道门（2026-10-06 真机实证）：
//   `runEmailInboxV1Migration` 与 `runEmailFoldersV1Migration` 都是这个形状。
//   后果是**每次冷启动固定打 4 条** `Execute: duplicate column name`
//   （deleted_at / body_purged / folder / action_reason），因为
//   Capacitor 的 SQLite 插件在把错误抛给 catch **之前**就 console.error 了。
//   ⇒ 真正的错误会被埋在里面。本轮就差点栽在这儿：
//   一次真实缺陷的 ReferenceError 夹在 4 条噪音中间，只看「有报错」就会把它当噪音放过。
//
// 判据为什么**只**禁这一条、不要求「版本早退」：
//   另有 3 个迁移是「只写版本不读」（ListSync / LiveRecord / MeetingsStudio）。
//   它们每次 init 会重跑，但循环里有 `pragma_table_info` 守卫 ⇒ 每列都被 `continue`
//   跳过，**无害**（只是多几次查询）。若把「必须有版本早退」也写进来，
//   门禁会长期红 3 条 ⇒ 逼人加 ALLOWLIST ⇒ 门禁失去牙齿。
//   **门禁要红到真问题为止，不能红到需要 ALLOWLIST 为止。**
//
// Run: node --test src/native/__tests__/migration-guards.test.mjs
import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import { readFileSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const SRC = readFileSync(join(here, '..', 'local-db.ts'), 'utf8')

/** 按方法切块：从 `private async run*Migration(` 到下一个方法（或文件尾）。 */
function migrationMethods(src) {
  const heads = [...src.matchAll(/private async (run\w*Migration)\(\)/g)]
  return heads.map((h, i) => ({
    name: h[1],
    line: src.slice(0, h.index).split('\n').length,
    body: src.slice(h.index, i + 1 < heads.length ? heads[i + 1].index : src.length),
  }))
}

/**
 * 找出方法内所有「遍历 *COLUMNS 的补列循环」及其**完整**循环体。
 *
 * ⚠️ 必须用**括号配平**取循环体，不能用「下一个固定缩进的 }」——
 * 补列循环里通常嵌着 `try { … } catch { … }`，按固定缩进收尾会在**内层** } 截断，
 * 从而看不见循环后半段有没有守卫（写窄了就会漏）。
 */
function columnLoops(src) {
  const out = []
  const re = /for \(const col of (\w+_COLUMNS)\)\s*\{/g
  for (const m of src.matchAll(re)) {
    const open = m.index + m[0].length - 1 // 指向 '{'
    let depth = 0
    let i = open
    for (; i < src.length; i++) {
      if (src[i] === '{') depth++
      else if (src[i] === '}') {
        depth--
        if (depth === 0) break
      }
    }
    out.push({ array: m[1], body: src.slice(open + 1, i) })
  }
  return out
}

const EXPECTED_METHODS = 8

test('前提自证·扫到的方法数与源码一致（否则下面几条可能全在「没扫到」上假绿）', () => {
  const methods = migrationMethods(SRC)
  assert.equal(
    methods.length,
    EXPECTED_METHODS,
    `扫到 ${methods.length} 个 run*Migration，期望 ${EXPECTED_METHODS} 个。` +
      `若是解析器坏了，先修解析器 —— 这条会变成「空对空恒真」。`,
  )
  const arrays = [...new Set(columnLoops(SRC).map((l) => l.array))]
  assert.equal(arrays.length, 8, `扫到 ${arrays.length} 个 *_COLUMNS 数组，期望 8 个：${arrays.join(', ')}`)
})

test('前提自证·分类器能区分「有守卫」与「无守卫」（在**合成输入**上自证，不依赖仓里还有缺陷）', () => {
  // ⚠️ 不能拿真实仓当量具的前提：那正是「修复之后前提就不成立」的类型
  // （第一版就栽在这里：它要求「仓里至少还有一条裸循环」，而我修完缺陷后必然为 0）。
  const bare = `for (const col of DEMO_COLUMNS) {
      try { await this.conn.execute(col.sql, false) } catch {}
    }`
  const guarded = `for (const col of DEMO_COLUMNS) {
      const exists = await this.queryForMigration("SELECT COUNT(*) FROM pragma_table_info('t')")
      if (exists) continue
      try { await this.conn.execute(col.sql, false) } catch {}
    }`
  // 守卫写在**嵌套块之后**也要能看见 —— 这正是按固定缩进收尾会漏的情形
  const guardAfterNested = `for (const col of DEMO_COLUMNS) {
      if (a) { try { x() } catch {} }
      const exists = await this.queryForMigration("pragma_table_info('t')")
      if (exists) continue
    }`
  assert.equal(columnLoops(bare).length, 1, '合成裸循环都没被解析出来')
  assert.equal(/pragma_table_info/.test(columnLoops(bare)[0].body), false, '裸循环被判成了有守卫')
  assert.equal(/pragma_table_info/.test(columnLoops(guarded)[0].body), true, '有守卫的循环被判成裸')
  assert.equal(
    /pragma_table_info/.test(columnLoops(guardAfterNested)[0].body),
    true,
    '守卫在嵌套块之后时看不见 ⇒ 解析按固定缩进收尾，写窄了',
  )
})

test('主判据·任何补列循环都必须先查 pragma_table_info', () => {
  const bare = []
  for (const m of migrationMethods(SRC)) {
    for (const loop of columnLoops(m.body)) {
      if (!/pragma_table_info/.test(loop.body)) {
        bare.push(`${m.name}（${loop.array}）`)
      }
    }
  }
  assert.deepEqual(
    bare,
    [],
    `这些迁移方法在「列已存在」时仍会硬发 ALTER，每次冷启动打 duplicate column name：\n  ` +
      bare.join('\n  ') +
      '\n修法：循环前先 queryForMigration(`SELECT COUNT(*) AS cnt FROM pragma_table_info(...)`)，' +
      'cnt>0 就 continue（与另外几个迁移同形）。',
  )
})

test('负对照·把真实文件里一个带守卫的循环换成裸的，主判据必须转红', () => {
  // 对**真实文件**做变异（不是合成字符串）——证明判据对真文件失灵过
  const mutated = SRC.replace(
    /for \(const col of (\w+_COLUMNS)\)\s*\{/,
    (full) => full, // 先确认至少有一处可变异
  )
  assert.ok(columnLoops(SRC).length > 0, '源码里一条补列循环都没有，变异无从谈起')
  // 把**第一个**带守卫的循环整体替换成裸的
  const withGuard = columnLoops(SRC).find((l) => /pragma_table_info/.test(l.body))
  assert.ok(withGuard, '源码里没有带守卫的补列循环 ⇒ 负对照无效')
  const start = SRC.indexOf(withGuard.body)
  assert.ok(start > 0, '定位循环体失败')
  const mutatedSrc =
    SRC.slice(0, start) +
    `\n        await this.conn.execute(col.sql, false)\n      ` +
    SRC.slice(start + withGuard.body.length)
  assert.notEqual(mutatedSrc, SRC, '变异没生效 ⇒ 负对照无效')
  const bare = columnLoops(mutatedSrc).filter((l) => !/pragma_table_info/.test(l.body))
  assert.ok(bare.length > 0, '真文件被换成裸循环，判据却没抓到 ⇒ 主判据对真文件恒绿')
})

/* ═══════════════════════════════════════════════════════════════════════════
 * §879 **行为层**：FTS 重灌失败时，不得把这次迁移记成「已完成」。
 *
 * 上面那道门是源码扫描；这一段跑**真 SQLite（含真 FTS5）+ 故障注入**，
 * 因为本条性质的核心是**时序**（失败 → 版本号），文本扫不出来。
 *
 * ⚠ 本文件导入的是 `notes-fts-backfill.ts` 而不是 `notes-fts-ready.ts`：
 *   后者顶层 import `localDB` 与 `native/crypto`，那条链在 node 里
 *   加载不了（无扩展名 import 那一族，简报第 94 条）。
 *   可测核心单独成模块是 §820「决策放纯函数」的同款手法。
 * ═══════════════════════════════════════════════════════════════════════════ */
import { DatabaseSync } from 'node:sqlite'
import { runNotesIndexBackfill, NOTES_FTS_MIGRATION } from '../../features/notes/notes-fts-backfill.ts'

/** 真 SQLite + 真 FTS5 的最小夹具，与生产 schema 对齐到够用的程度。 */
function makeNotesFixture(opts = {}) {
  const sqlite = new DatabaseSync(':memory:')
  // ⚠⚠ 必须是**外部内容表**（`content='local_notes'`），与 `native/schema.ts:49` 一致。
  //   第一版写成普通 `fts5(title, content)`，于是 `'delete-all'` 抛
  //   「may only be used with a contentless or external content fts5 table」
  //   —— 读上去像**生产代码一直在失败**，实际是夹具没复刻关键那一列。
  //   ⇒ 替身不复刻关键性质时，它测的是量具，不是代码。
  sqlite.exec(`
    CREATE TABLE local_notes (
      rowid INTEGER PRIMARY KEY,
      id TEXT NOT NULL,
      title TEXT,
      content TEXT,
      tags TEXT,
      search_text TEXT,
      encrypted_content INTEGER DEFAULT 0,
      storage_tier TEXT,
      deleted_at INTEGER,
      workspace_id TEXT DEFAULT 'default'
    );
    CREATE VIRTUAL TABLE local_notes_fts USING fts5(
      title, content, content='local_notes', content_rowid='rowid',
      tokenize='unicode61 remove_diacritics 2'
    );
    CREATE TABLE _schema_migrations (version TEXT PRIMARY KEY, description TEXT, applied_at INTEGER);
  `)
  for (const n of opts.notes ?? ['alpha', 'beta', 'gamma']) {
    sqlite.prepare(
      `INSERT INTO local_notes (id, title, content, search_text) VALUES (?,?,?,NULL)`,
    ).run(n, `标题 ${n}`, `正文 ${n} 的内容`)
  }
  const db = {
    isReady: () => true,
    queryOne: async (sql, values = []) => sqlite.prepare(sql).get(...values) ?? null,
    query: async (sql, values = []) => sqlite.prepare(sql).all(...values),
    run: async (sql, values = []) => Number(sqlite.prepare(sql).run(...values).changes),
    execute: async (sql) => {
      // ⚠ 故障注入点：让「delete-all 成功、重灌失败」成为可能 ——
      //   这正是真实世界里最危险的形状（清空已生效，补回没生效）。
      if (opts.failRepopulate && /INSERT INTO local_notes_fts\(rowid/.test(sql)) {
        throw new Error('injected: repopulate failed (disk full)')
      }
      sqlite.exec(sql)
      return 0
    },
  }
  return { sqlite, db }
}

const decryptOk = async (s) => s
/**
 * ⚠⚠⚠ **必须用 MATCH 量，不能用 `COUNT(*)`。**
 *   `local_notes_fts` 是**外部内容表**，`SELECT COUNT(*) FROM local_notes_fts`
 *   数的是**内容表 `local_notes` 的行数** —— 与索引里有没有词**完全无关**。
 *   第一版就栽在这：明明索引是空的，读出来却是 3。
 *   ⇒ 这与 §877「替身要造出真实语义」是同一条：量具测的必须是**被测的那件事**。
 *   ⚠ 还必须用**拉丁词**去 MATCH：生产用 `unicode61`，它把连续汉字当成**一词**
 *   （`正文 alpha 的内容` → 词元 `正文` / `alpha` / `的内容`）
 *   ⇒ `MATCH '内容'` 永远 0 命中。这也是 `notes-search.ts` 要并一层 LIKE 的原因。
 */
const ftsRowCount = (sqlite) =>
  Number(sqlite.prepare('SELECT COUNT(*) AS n FROM local_notes_fts WHERE local_notes_fts MATCH ?')
    .get('alpha OR beta OR gamma').n)
const migrationStamped = (sqlite) =>
  Number(sqlite.prepare('SELECT COUNT(*) AS n FROM _schema_migrations WHERE version = ?')
    .get(NOTES_FTS_MIGRATION).n)

test('§878/879 正控：FTS5 可用时回填成功，索引条数与笔记数一致', async () => {
  const { sqlite, db } = makeNotesFixture()
  const r = await runNotesIndexBackfill(db, decryptOk)
  assert.equal(r.changed, 3, '三条笔记的 search_text 都该被回填')
  assert.equal(r.ftsOk, true)
  assert.equal(r.stamped, true)
  assert.equal(ftsRowCount(sqlite), 3, '重灌后索引条数必须等于笔记数')
  assert.equal(migrationStamped(sqlite), 1)
})

test('§879 ★ 重灌失败 ⇒ 迁移版本号不得写入（否则这次回填永不重跑）', async () => {
  const { sqlite, db } = makeNotesFixture({ failRepopulate: true })
  const r = await runNotesIndexBackfill(db, decryptOk)
  assert.equal(r.ftsOk, false, '注入的失败没有被认出来')
  assert.equal(r.stamped, false,
    '重灌失败却把迁移记成已完成 ⇒ 本次回填再也不会重跑，索引永久残缺')
  assert.equal(migrationStamped(sqlite), 0,
    '迁移表里不该出现这条记录')
})

test('§879 ★ 重灌失败后下一次调用会重试（不是记成「已完成」就跳过）', async () => {
  const { sqlite, db } = makeNotesFixture({ failRepopulate: true })
  await runNotesIndexBackfill(db, decryptOk)
  // ⚠ 同一个 db（故障仍在注入中）⇒ 第二轮仍应尝试重跑，而不是读到版本号就 return。
  const second = await runNotesIndexBackfill(db, decryptOk)
  assert.equal(second.changed, 0,
    '第二轮没重跑回填（search_text 已回填过 ⇒ changed 应为 0）但它**必须跑到这里**')
  assert.equal(second.stamped, false, '故障仍在 ⇒ 第二轮同样不该记成完成')
  assert.equal(migrationStamped(sqlite), 0)
})

test('§879 对照组：故障撤掉后，下一轮能把索引补回来并记成完成', async () => {
  // ⚠ 没有这一条，「不写版本号」可能只是把问题推迟；必须证明它**真的会自愈**。
  const sqlite0 = new DatabaseSync(':memory:')
  const fixtureA = makeNotesFixture({ failRepopulate: true })
  await runNotesIndexBackfill(fixtureA.db, decryptOk)
  assert.equal(ftsRowCount(fixtureA.sqlite), 0, '清空已生效、重灌没生效 ⇒ 索引此刻是空的')

  // 换一个**不再注入故障**的 db 连到同一个 sqlite 上（即故障撤除）。
  const healed = {
    ...fixtureA.db,
    execute: async (sql) => { fixtureA.sqlite.exec(sql); return 0 },
  }
  const r = await runNotesIndexBackfill(healed, decryptOk)
  assert.equal(r.ftsOk, true)
  assert.equal(ftsRowCount(fixtureA.sqlite), 3, '索引应被补回 3 条')
  assert.equal(migrationStamped(fixtureA.sqlite), 1, '这次才该记成已完成')
  assert.ok(sqlite0)
})

test('§879 Web 降级不是失败：虚表不存在时仍应记成完成', async () => {
  // ⚠ 旧代码对「web 无 FTS5」和「Android 真失败」一视同仁地静默；
  //   但两者不能一刀切 —— web 上没有索引这件事是**预期**的。
  const { sqlite, db } = makeNotesFixture()
  const noFts = { ...db, execute: async (sql) => {
    if (/local_notes_fts/.test(sql)) throw new Error('no such table: local_notes_fts')
    return 0
  } }
  const r = await runNotesIndexBackfill(noFts, decryptOk)
  assert.equal(r.ftsOk, false, '虚表不存在仍算「没重灌」')
  assert.equal(r.stamped, true,
    'web 上没有 FTS 是预期降级 ⇒ 不该每次搜索都重跑整轮回填')
})
