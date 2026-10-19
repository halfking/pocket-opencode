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
import { describe, it, test } from 'node:test' // §894 用了 describe/it
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

// §898：新增 runNoteVectorsV1Migration（向量表加 chunks 列）⇒ 8 → 9。
// ⚠ 这条是「前提自证」：它保证下面的扫描**扫到了全部**迁移方法，
//   数字必须与源码同步 —— 少写一个数 ⇒ 下面的判据可能全在「没扫到」上假绿。
const EXPECTED_METHODS = 9

test('前提自证·扫到的方法数与源码一致（否则下面几条可能全在「没扫到」上假绿）', () => {
  const methods = migrationMethods(SRC)
  assert.equal(
    methods.length,
    EXPECTED_METHODS,
    `扫到 ${methods.length} 个 run*Migration，期望 ${EXPECTED_METHODS} 个。` +
      `若是解析器坏了，先修解析器 —— 这条会变成「空对空恒真」。`,
  )
  const arrays = [...new Set(columnLoops(SRC).map((l) => l.array))]
  // §898：新增 NOTE_VECTORS_V1_COLUMNS（向量表加 chunks 列）⇒ 8 → 9。
  assert.equal(arrays.length, 9, `扫到 ${arrays.length} 个 *_COLUMNS 数组，期望 9 个：${arrays.join(', ')}`)
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

/* ═══════════════════════════════════════════════════════════════════════════
 * §881 **行为层**：向量索引在「更新一条已有笔记」后必须立刻反映新向量。
 *
 * 缺陷形态：`add()` 里 `isInMemory = !已存在 && 未超上限` ⇒ 更新已有笔记时
 * 该值为 false ⇒ 写完库就 return ⇒ **内存矩阵永远留着旧向量**。
 * 而 `notes-persist.ts` 的更新路径（`patch.content !== undefined`）正是调 `add`
 * ⇒ 编辑笔记后语义检索继续用改动前的内容作答，库里的新向量要等下次 `load()`。
 *
 * ⚠ 本文件现在同时是「SQLite 存储语义行为层」的宿主（§879 FTS / §881 向量索引）。
 *   新增同类行为用例请就近放这里，而不是另开文件（argv 已贴脸）。
 * ═══════════════════════════════════════════════════════════════════════════ */
import { VectorIndex, DIM } from '../vector-index.ts'
// §899：切段器与哈希都是纯函数，直接引来做行为断言。
import { splitForEmbedding } from '../../features/notes/note-storage-policy.ts'
import { sha1Hex } from '../../utils/text-hash.ts'
import { needsFullRebuild } from '../../features/notes/embed-reuse.ts'
import { NodeSqlDb } from './helpers.mjs'

/**
 * §881 夹具适配：把 node:sqlite 的 BLOB 语义**归一化成 Capacitor 插件的形状**。
 *
 * ⚠⚠ 两处真实差异（不改适配器，判据会测的是量具不是代码）：
 *   1. 绑定：`localDB.run(..., [noteId, blob.buffer, …])` 传的是 **ArrayBuffer**；
 *      Capacitor 插件接受，而 `node:sqlite` 直接报
 *      「Provided value cannot be bound to SQLite parameter 2」⇒ 这里转成 Uint8Array。
 *   2. 读回：`load()` 里 `new Float32Array(r.embedding)` **依赖 r.embedding 是 ArrayBuffer**
 *      （对 ArrayBuffer 是「字节重解释为 float」，对 Uint8Array 则会变成「按字节填数」）。
 *      Capacitor 返回 ArrayBuffer，node:sqlite 返回 Uint8Array ⇒ 这里转回去。
 *
 * ⇒ 这是 §877/§879 的同一条纪律：**替身要复刻关键性质**，否则读数是假的。
 */
function makeVectorDb() {
  const db = new NodeSqlDb()
  return {
    run: (sql, params = []) => db.run(sql, params.map(toBindable)),
    all: async (sql, params = []) => {
      const rows = await db.all(sql, params.map(toBindable))
      return rows.map((r) => (r.embedding instanceof Uint8Array
        ? { ...r, embedding: r.embedding.buffer.slice(r.embedding.byteOffset, r.embedding.byteOffset + r.embedding.byteLength) }
        : r))
    },
  }
}
const toBindable = (p) => (p instanceof ArrayBuffer ? new Uint8Array(p) : p)

/**
 * ⚠ `local_note_vectors.note_id` 有**外键**指向 `local_notes(id)`
 * （schema.ts:87 附近）⇒ 不先落一条笔记就会 FOREIGN KEY constraint failed。
 * ⇒ 夹具必须把**被测表的约束**也复刻出来，不能只建被测的那一张表。
 */
async function seedNote(db, id) {
  await db.run(
    'INSERT OR IGNORE INTO local_notes (id, content, created_at, updated_at) VALUES (?, ?, ?, ?)',
    [id, id, 0, 0],
  )
}

/** 只在前两维上有信号的向量；其余维留 0（矩阵按 DIM 宽分配）。 */
function vec(a, b) {
  const v = new Float32Array(DIM)
  v[0] = a
  v[1] = b
  return v
}

/** 查询向量同理：`search()` 会遍历全部 DIM，q 必须是 DIM 长，否则 `0 * undefined = NaN`。 */
function queryVec(a, b) {
  const v = new Float32Array(DIM)
  v[0] = a
  v[1] = b
  return v
}

test('§881 正控：新增一条笔记后，语义检索按它的向量作答', async () => {
  const db = makeVectorDb()
  await seedNote(db, 'note-1')
  const index = new VectorIndex(db)
  // ⚠ 必须先 load()：`search()` 在 `loaded === false` 时直接返回 []。
  //   生产里 `lobster-init.ts` 启动时调一次，这里照抄那个时序。
  await index.load()
  await index.add('note-1', vec(1, 0), 'm')
  const hits = index.search(queryVec(1, 0), 5)
  assert.equal(hits.length, 1)
  assert.equal(hits[0].noteId, 'note-1')
  assert.ok(Math.abs(hits[0].score - 1) < 1e-6, `score=${hits[0].score}`)
})

test('§881 ★ 更新一条已有笔记后，检索必须立刻用新向量（不是旧向量）', async () => {
  const db = makeVectorDb()
  await seedNote(db, 'note-1')
  const index = new VectorIndex(db)
  await index.load()
  await index.add('note-1', vec(1, 0), 'm') // 旧内容：偏第一维
  assert.ok(Math.abs(index.search(queryVec(1, 0), 5)[0].score - 1) < 1e-6, '前置条件不成立')

  // 编辑笔记：内容变了，向量也变了（偏第二维）。
  await index.add('note-1', vec(0, 1), 'm')

  const after = index.search(queryVec(1, 0), 5)
  assert.equal(after.length, 1)
  assert.ok(
    Math.abs(after[0].score) < 1e-6,
    `更新后仍按旧向量作答（score=${after[0].score}，应≈0）` +
    ' ⇒ 编辑笔记后语义检索继续用改动前的内容，且零提示',
  )
})

test('§881 ★ 内存索引与库必须一致（load() 之后读数不得变化）', async () => {
  // ⚠ 这条钉的是「就地覆盖」与「全量重建」两条路径**等价** ——
  //   若实现改成「有更新就整表 load()」，这条同样绿（那是更慢但正确的做法）。
  const db = makeVectorDb()
  await seedNote(db, 'note-1')
  const index = new VectorIndex(db)
  await index.load()
  await index.add('note-1', vec(1, 0), 'm')
  await index.add('note-1', vec(0, 1), 'm')
  const inMemory = index.search(queryVec(1, 0), 5)[0].score

  const reloaded = new VectorIndex(db)
  await reloaded.load()
  const fromDb = reloaded.search(queryVec(1, 0), 5)[0].score

  assert.ok(
    Math.abs(inMemory - fromDb) < 1e-6,
    `内存读数 ${inMemory} 与从库重建的读数 ${fromDb} 不一致 ⇒ 两者已分叉`,
  )
  assert.ok(Math.abs(fromDb) < 1e-6, '库里的向量本身就不对（那就不是索引的问题，是写入的问题）')
})

test('§881 对照组：两条笔记按各自向量给出不同分数（判据有牙）', async () => {
  const db = makeVectorDb()
  await seedNote(db, 'note-a')
  await seedNote(db, 'note-b')
  const index = new VectorIndex(db)
  await index.load() // 同上：loaded=false 时 search() 恒返回 []
  await index.add('note-a', vec(1, 0), 'm')
  await index.add('note-b', vec(0, 1), 'm')
  const hits = index.search(queryVec(1, 0), 2)
  assert.equal(hits.length, 2)
  const byId = Object.fromEntries(hits.map((h) => [h.noteId, h.score]))
  assert.ok(Math.abs(byId['note-a'] - 1) < 1e-6, `note-a score=${byId['note-a']}`)
  assert.ok(Math.abs(byId['note-b']) < 1e-6, `note-b score=${byId['note-b']}`)
})

/* ═══════════════════════════════════════════════════════════════════════════
 * §894 D64：`add()` 每次都重新分配整块索引 ⇒ 保存一条笔记的成本随库里笔记数
 * 线性增长，且**每次保存**都分配/丢弃一整块（到 50000 条上限时约 293MB）。
 *
 * 实测（Node 22，预热后测边际成本，三轮取平均）：
 *   n= 200 →  0.076ms   修后 0.041ms
 *   n= 800 →  0.277ms   修后 0.044ms
 *   n=2000 →  1.368ms   修后 0.046ms
 *   n=4000 →  2.969ms   修后 0.049ms
 *   n=8000 →  5.276ms   修后 0.056ms      ⇒ 明确线性于 n
 *
 * ⚠⚠ 这里**不用计时做判据**（CI 上必然抖动），改用**不依赖时间的性质**：
 *   「分配容量 > 实际条数」这一条 slack 就足以区分「精确分配」与「几何增长」。
 *   ⇒ 反过来说，**只有能被性质断言的改进才配写进门禁**。
 *
 * ⚠ `matrix`/`capacity` 是 TS private，但 strip-types **不做**运行时访问控制，
 *   本文件是 .mjs（不参与 vue-tsc）⇒ 可以直接读。
 * ═══════════════════════════════════════════════════════════════════════════ */
describe('§894 · D64：向量索引必须几何增长，不得每次重分配整块', () => {
  /** ⚠ TS 的 private 只是类型约束，运行时不存在 ⇒ .mjs 里读得到。 */
  const capOf = (idx) => idx.matrix.length / DIM
  const capField = (idx) => idx.capacity

  /**
   * 灌 n 条并返回 { db, index }。
   * ⚠ 必须把 db 一起返回：load() 的读数来自**它**，用另一个空 db 重建会读到 0 条
   *   （第一版测试 6 就栽在这里，症状是「从库重建后条数不对：0 !== 300」）。
   */
  async function filled(n) {
    const db = makeVectorDb()
    const index = new VectorIndex(db)
    await index.load()
    for (let i = 0; i < n; i++) {
      await db.run(
        'INSERT OR IGNORE INTO local_notes (id, content, created_at, updated_at) VALUES (?, ?, ?, ?)',
        [`n${i}`, `n${i}`, 0, 0],
      )
      await index.add(`n${i}`, vec(1, i === 0 ? 0 : 1), 'm')
    }
    return { db, index }
  }
  const idxOf = async (n) => (await filled(n)).index

  it('★ 前置闸：夹具真能灌进去（否则下面的断言全是恒真）', async () => {
    const idx = await idxOf(120)
    assert.equal(idx.size(), 120, `size()=${idx.size()}，夹具本身坏了`)
    assert.ok(capOf(idx) > 0, 'matrix 长度为 0 ⇒ 读不到私有字段，本节全部断言会变成恒假')
  })

  it('★★★ 分配容量必须大于实际条数（几何增长的 slack）', async () => {
    // 精确分配的形状是 `matrix.length === size() * DIM` ⇒ slack 恒为 0。
    const idx = await idxOf(120)
    assert.ok(
      capOf(idx) > idx.size(),
      `分配容量 ${capOf(idx)} 没有大于实际条数 ${idx.size()} ⇒ 实现退回了「每次按实际条数精确分配」` +
        '（那是 §894/D64 的原缺陷形状）',
    )
    assert.equal(capField(idx), capOf(idx), 'capacity 字段与 matrix 实际长度不一致 ⇒ 两处已分叉')
  })

  it('★★★ 容量不得超过实际条数的两倍（不许为提速而无限超额分配）', async () => {
    const idx = await idxOf(120)
    assert.ok(
      capOf(idx) <= idx.size() * 2,
      `分配 ${capOf(idx)} 条的容量却只装了 ${idx.size()} 条 ⇒ 超额分配无上限，会把峰值内存推高`,
    )
  })

  it('★★★ 峰值内存不得比修前更大（容量上限必须钉在 MAX_VECTORS）', async () => {
    // 50000 × 1536 × 4B ≈ 293MB。倍增若不钉上限，最坏会变成两倍。
    const MAX_CAP = 50000
    const src = readFileSync(new URL('../vector-index.ts', import.meta.url), 'utf8')
    assert.match(src, /Math\.min\(MAX_VECTORS,/, 'grow() 的容量上限必须用 Math.min 钉在 MAX_VECTORS')
    assert.ok(capOf(await idxOf(120)) <= MAX_CAP, '容量超过了 MAX_VECTORS')
  })

  it('★★★ 跨容量边界后，每条向量仍能被正确检索（几何增长不得改变语义）', async () => {
    // 覆盖「扩容那一刻」：旧向量留在各自偏移上，没有被搬错或覆盖。
    const db = makeVectorDb()
    const index = new VectorIndex(db)
    await index.load()
    const N = 300
    for (let i = 0; i < N; i++) {
      await db.run(
        'INSERT OR IGNORE INTO local_notes (id, content, created_at, updated_at) VALUES (?, ?, ?, ?)',
        [`k${i}`, `k${i}`, 0, 0],
      )
      // 交替朝两个方向，命中时应各自 score≈1
      await index.add(`k${i}`, vec(1, i % 2 === 0 ? 0 : 1), 'm')
    }
    assert.equal(index.size(), N)
    const hits = index.search(queryVec(1, 0), N)
    const byId = Object.fromEntries(hits.map((h) => [h.noteId, h.score]))
    for (let i = 0; i < N; i += 2) {
      assert.ok(
        Math.abs(byId[`k${i}`] - 1) < 1e-6,
        `k${i} 本该朝第一维（score≈1），实得 ${byId[`k${i}`]} ⇒ 扩容时向量被搬错了`,
      )
      // ⚠ 期望值必须是 1/√2 ≈ 0.707 而不是 0：夹具给奇数项的是 vec(1,1)，
      //   归一化后是 (0.707,0.707)，与查询向量 (1,0) 点积就是 0.707。
      //   （第一版这里写 0，是**我算错了**而不是实现有问题 —— 别把量具的错当成产品缺陷。）
      assert.ok(
        Math.abs(byId[`k${i + 1}`] - Math.SQRT1_2) < 1e-6,
        `k${i + 1} 应为 (1,1) 归一化后的点积 ≈0.707，实得 ${byId[`k${i + 1}`]} ⇒ 扩容时向量被搬错了`,
      )
    }
  })

  it('★★★ load() 之后容量必须与实际条数对齐（capacity 不得残留旧值）', async () => {
    // load() 是「按库里有多少装多少」的权威点；capacity 若不重置，
    // 下一次 add 就会跳过必要扩容 ⇒ 写进已被占用的偏移 ⇒ 静默覆盖别的向量。
    const { db, index: grown } = await filled(300)
    assert.ok(capOf(grown) > grown.size(), '前置条件不成立：没有触发扩容')

    const fresh = new VectorIndex(db)
    await fresh.load()
    assert.equal(fresh.size(), 300, '从库重建后条数不对')
    assert.equal(capField(fresh), 300, `load() 后 capacity=${capField(fresh)}，应为实际条数 300`)
  })

  it('★★ 量具自检：判据确实能区分「精确分配」与「几何增长」', async () => {
    // 复刻修前形状（精确分配），证明 slack 断言对它不成立 ⇒ 判据有牙。
    const exact = { size: () => 120, matrix: { length: 120 * DIM }, capacity: 120 }
    assert.equal(capOf(exact), exact.size(), '负控前提失败：复刻的形状不是「精确分配」')
    const grown = await idxOf(120)
    assert.ok(capOf(grown) > grown.size(), '正向前提失败：新实现没有 slack ⇒ 本节全部断言都是恒假')
  })
})

/* ═══════════════════════════════════════════════════════════════════════════
 * §895 D65：`search()` 是**同步**的全量暴力点积，跑在 UI 线程上。
 *
 * 实测（Node 22，预热后五轮取平均）：
 *   n= 500 →  0.66ms    n= 2000 →  2.30ms
 *   n= 8000 →  9.32ms    n=20000 → 24.89ms        ⇒ 线性于条数
 *   ⇒ 到 MAX_VECTORS 上限（50000）外推约 62ms 的主线程冻结；移动端再慢 3–10 倍。
 *
 * 真实暴露面（不是搜索框）：`NoteDetailView.loadRelated` 在**每次打开笔记详情**时
 * 调 `searchSemantic`。搜索框走的是 `@keyup.enter`，**不是**每次按键 ⇒ 那里没问题。
 *
 * 修法：`searchAsync()` 分片并让出事件循环，**结果与 `search()` 逐字相同**。
 * ⚠ 判据里必须有一条**同步负控**：同一个「让出探测器」跑 `search()` 必须读到 0 次
 *   让出 —— 否则「探测器有牙」只是我的假设。
 * ═══════════════════════════════════════════════════════════════════════════ */
describe('§895 · D65：语义检索不得整段占满 UI 线程', () => {
  /** 造一个灌了 n 条、方向交错的索引（与 §894 的 filled 同夹具思路）。 */
  async function seeded(n) {
    const db = makeVectorDb()
    const index = new VectorIndex(db)
    await index.load()
    for (let i = 0; i < n; i++) {
      await db.run(
        'INSERT OR IGNORE INTO local_notes (id, content, created_at, updated_at) VALUES (?, ?, ?, ?)',
        [`s${i}`, `s${i}`, 0, 0],
      )
      await index.add(`s${i}`, vec(1, i % 3 === 0 ? 0 : 1), 'm')
    }
    return index
  }

  /** 宏任务计数器：让出一次事件循环就会 +1。 */
  function turnCounter() {
    let turns = 0
    const timer = setInterval(() => { turns++ }, 0)
    return { stop: () => clearInterval(timer), read: () => turns }
  }

  it('★★★ searchAsync 与 search 的结果必须逐字相同（id 顺序与 score）', async () => {
    const index = await seeded(120)
    for (const topK of [1, 3, 10, 50, 120]) {
      for (const [a, b] of [[1, 0], [0, 1], [1, 1]]) {
        const q = queryVec(a, b)
        const sync = index.search(q, topK)
        const async_ = await index.searchAsync(q, topK, 7)
        assert.deepEqual(
          async_.map((m) => m.noteId), sync.map((m) => m.noteId),
          `topK=${topK} q=(${a},${b}) 时异步版的 id 顺序与同步版不同 ⇒ 分片改变了语义`,
        )
        for (let i = 0; i < sync.length; i++) {
          assert.equal(async_[i].score, sync[i].score,
            `topK=${topK} q=(${a},${b}) 第 ${i} 条 score 不同（${async_[i].score} vs ${sync[i].score}）`)
        }
      }
    }
  })

  it('★★★ 返回条数必须是 min(topK, 条数)（绝对性质，不靠与同步版比对）', async () => {
    // ⚠⚠ 为什么要有这条：上面那条只验「同步版 == 异步版」，
    //   两版**同时**改错时它照样全绿（M53-M2 实测：`k` 的 off-by-one 逃掉了）。
    //   ⇒ 凡是「一致性」判据，都必须再配一条**绝对**性质，否则共因失效。
    const index = await seeded(37)
    for (const topK of [0, 1, 2, 5, 37, 100]) {
      const expected = Math.min(Math.max(topK, 0), 37)
      assert.equal(index.search(queryVec(1, 0), topK).length, expected, `同步版 topK=${topK} 条数不对`)
      assert.equal((await index.searchAsync(queryVec(1, 0), topK)).length, expected, `异步版 topK=${topK} 条数不对`)
    }
  })

  it('★★★ 同分时保留**先加入**的（top-k 取舍是确定的，不依赖插入顺序的偶然）', async () => {
    // ⚠ 语料必须是**全同分**：§894 那个夹具只有 3 种方向，同分对根本触发不到
    //   （M53-M4 把 `>` 改成 `>=` 全绿 ⇒ 判据没咬住「取舍」这件事）。
    //   全部向量相同 ⇒ 全部 score 相同 ⇒ 只有在**同分**下才能区分 `>` 与 `>=`。
    const db = makeVectorDb()
    const index = new VectorIndex(db)
    await index.load()
    const N = 20
    for (let i = 0; i < N; i++) {
      await db.run(
        'INSERT OR IGNORE INTO local_notes (id, content, created_at, updated_at) VALUES (?, ?, ?, ?)',
        [`t${i}`, `t${i}`, 0, 0],
      )
      await index.add(`t${i}`, vec(1, 0), 'm')   // 全部朝同一方向 ⇒ 全部同分
    }
    for (const topK of [1, 5, 10]) {
      const ids = index.search(queryVec(1, 0), topK).map((m) => m.noteId)
      assert.deepEqual(ids, Array.from({ length: topK }, (_, i) => `t${i}`),
        `全同分语料下 topK=${topK} 选出的不是**最先加入**的 ${topK} 条（得到 ${ids.join(',')}）`)
    }
    const asyncIds = (await index.searchAsync(queryVec(1, 0), 5, 3)).map((m) => m.noteId)
    assert.deepEqual(asyncIds, ['t0', 't1', 't2', 't3', 't4'], `异步版在同一语料上与同步版不一致：${asyncIds.join(',')}`)

    // ⚠⚠ 变异台账（M53-V7）：把 `considerInto` 的守卫 `dot > out[out.length-1].score`
    //   改成 `>=`，**本节全绿**。查证后确认它是**等价变异**，不是判据有洞：
    //   同分且 `out` 已满时，`>=` 会让候选走完
    //   「插到队尾（`dot <= out[j].score` 立即断）→ splice → `pop()` 掉自己」
    //   ⇒ 输出集合与顺序完全不变，只多一次无用 splice。
    //   ⇒ 真正承载「同分保序」的是**插入定位**那一行，M53-V8（`dot <= out[j].score`
    //   放宽成 `dot < out[j].score`）会红，就是它。
    //   ⇒ 登记在此，免得下一个人把 V7 的全绿当成「判据漏了」而去补一条钉不住行为的断言。
  })

  it('★★★ top-k 的**溢出淘汰**路径必须被触发（否则「插入后淘汰」整条代码无人验证）', async () => {
    // ⚠⚠⚠ 前面的语料有一个**真空**：向量按「前 10 条最好」排好，
    //   之后的向量要么同分（进不来）、要么更差（进不来）
    //   ⇒ `out.length > k ⇒ 淘汰` 这一段**一次都没执行过**，
    //   把 `out.pop()` 换成 `out.shift()`（丢弃最高分而不是最低分）全仓判据照样全绿（M53-M8 实测）。
    // ⇒ 这里专门构造「越晚加入的越强」的语料，逼出淘汰路径。
    const db = makeVectorDb()
    const index = new VectorIndex(db)
    await index.load()
    const N = 30
    for (let i = 0; i < N; i++) {
      await db.run(
        'INSERT OR IGNORE INTO local_notes (id, content, created_at, updated_at) VALUES (?, ?, ?, ?)',
        [`r${i}`, `r${i}`, 0, 0],
      )
      // ⚠⚠ 语料方向（这里我第一次写反了，实测才发现）：
      //   `add` 会做 L2 归一化，`vec(1, i)` 归一化后是 (1,i)/√(1+i²)，
      //   与查询 `queryVec(1,0)` 的点积 = 1/√(1+i²) ⇒ **随 i 递减**，
      //   越晚加入的越**弱** ⇒ 根本顶不掉任何一条 ⇒ 淘汰路径又一次没被执行。
      //   正确写法：`vec(i, 1)` 归一化后与 (1,0) 的点积 = i/√(i²+1) ⇒ 严格递增
      //   ⇒ 每条新向量都比当前 top-k 里最弱的那条强 ⇒ 每次插入都触发 `out.pop()`。
      //   ⇒ 「构造能触发目标分支的语料」和「构造语料」是两件事，必须分开验。
      await index.add(`r${i}`, vec(i, 1), 'm')
    }
    // topK=1：正确答案应当是**最强**的那条 = 最后加入的 r29
    const best = index.search(queryVec(1, 0), 1).map((m) => m.noteId)
    assert.deepEqual(best, ['r29'],
      `top-1 选出的是 ${best.join(',')}，不是最强的 r29 ⇒ 淘汰方向反了（保留了最弱的一条）`)

    // topK=3：应为最强的三条 r29 / r28 / r27
    const top3 = index.search(queryVec(1, 0), 3).map((m) => m.noteId)
    assert.deepEqual(top3, ['r29', 'r28', 'r27'],
      `top-3 选出 ${top3.join(',')}，应为 r29/r28/r27 ⇒ 溢出淘汰有 bug`)

    // 异步版在同一语料上必须一致
    const async3 = (await index.searchAsync(queryVec(1, 0), 3, 4)).map((m) => m.noteId)
    assert.deepEqual(async3, top3, `异步版结果 ${async3.join(',')} 与同步版不一致`)
  })

  it('★★★ searchAsync 期间必须让出事件循环（而同步 search 一次都不让）', async () => {
    const index = await seeded(120)

    const c1 = turnCounter()
    await index.searchAsync(queryVec(1, 0), 5, 8)   // chunk=8 ⇒ 15 片
    c1.stop()
    assert.ok(c1.read() > 0, `让出次数 ${c1.read()} —— searchAsync 全程没让出事件循环，UI 线程仍被占满`)

    const c2 = turnCounter()
    index.search(queryVec(1, 0), 5)                 // 同步基准
    c2.stop()
    assert.equal(c2.read(), 0,
      `同步 search() 也让出了 ${c2.read()} 次 ⇒ 探测器没有分辨力，本节断言全是恒真`)
  })

  it('★★ chunkSize 越大，让出次数越少（分片粒度是可控的，不是写死的）', async () => {
    const index = await seeded(120)
    const coarse = turnCounter()
    await index.searchAsync(queryVec(1, 0), 5, 120)
    coarse.stop()
    const fine = turnCounter()
    await index.searchAsync(queryVec(1, 0), 5, 8)
    fine.stop()
    assert.ok(fine.read() > coarse.read(),
      `细粒度让出 ${fine.read()} 次 ≤ 粗粒度 ${coarse.read()} 次 ⇒ chunkSize 没起作用`)
  })

  it('★★★ 生产路径必须走异步版（结构门）', () => {
    const src = readFileSync(new URL('../../features/notes/notes-search.ts', import.meta.url), 'utf8')
    assert.match(src, /await vectorIndex\.searchAsync\(/,
      'searchSemantic 又改回同步的 vectorIndex.search(...) ⇒ UI 线程冻结回来了')
    assert.doesNotMatch(src, /vectorIndex\.search\(/,
      'notes-search.ts 里仍有同步 search() 调用（searchAsync 名字含 search，判据要排除它）')
  })

  it('★★ 边界：空索引 / topK<=0 两个版本都必须返回空数组', async () => {
    const empty = new VectorIndex(makeVectorDb())
    await empty.load()                                  // loaded=true 但 0 条
    assert.deepEqual(await empty.searchAsync(queryVec(1, 0), 5), [], '空索引应返回空数组')
    const index = await seeded(10)
    assert.deepEqual(await index.searchAsync(queryVec(1, 0), 0), [], 'topK=0 应返回空数组')
    assert.deepEqual(index.search(queryVec(1, 0), 0), [], '同步版 topK=0 也应返回空数组（两者须一致）')
    assert.deepEqual(await index.searchAsync(queryVec(1, 0), -3), [], 'topK 为负应返回空数组')
  })
})

/* ═══════════════════════════════════════════════════════════════════════════
 * §898 · D67：长会议必须**按段**可检索 —— 「一条笔记一个向量」这族在结构上就做不到
 * ═══════════════════════════════════════════════════════════════════════════
 * 属主要求「会议时长无上限、只受存储约束、不大量增加存储或资源」。
 * §897 定位到唯一瓶颈是向量化（16000 字符硬上限），本节把它落地成段级向量。
 *
 * ⭐ 本节要钉的核心性质（实测推导见 `vector-index.ts::search` 上方注释）：
 *   局部代理嵌入（TF-IDF，1536 维）下，4 小时会议（18 段）第 10 段埋一句关键信息：
 *     截断 0.0452 / 质心 0.0492 / 整篇单向量 0.0914 / **段级 max 0.1442**
 *   而一条中等相关的短笔记是 0.0394
 *   ⇒ 截断与质心只比它高 15%~25% ⇒ 长会议在有若干短笔记的库里会被挤出 top-k。
 *   ⇒ **max 聚合是必须的**，而「同一条笔记的多个段各占一个 top-k 名额」是错的。
 *
 * ⚠ 判据全部用**性质**而不是实现细节：
 *   「一条多段笔记在结果里只出现一次」「它的分数等于它最好那一段的分数」
 *   「旧数据（chunks=1）照样能检索」—— 这三条与内部怎么存无关。
 */
describe('§898 · D67：段级向量与 max 聚合', () => {
  /** 一条笔记的 N 个段向量：第 i 段朝 queryVec(i,0) 方向，强度递增。 */
  function chunkVecs(n) {
    const out = []
    for (let i = 0; i < n; i++) {
      const v = new Float32Array(DIM)
      v[0] = i + 1
      v[1] = 0
      out.push(v)
    }
    return out
  }

  it('★★★ 一条多段笔记在结果里只出现一次，且分数 = 最好那一段的分数', async () => {
    const db = makeVectorDb()
    const index = new VectorIndex(db)
    await index.load()
    await db.run(
      'INSERT OR IGNORE INTO local_notes (id, content, created_at, updated_at) VALUES (?, ?, ?, ?)',
      ['long', 'long', 0, 0],
    )
    // 5 段，第 4 段（index 3）最强：v[0]=4
    await index.add('long', chunkVecs(5), 'm')
    // 再放 3 条单段笔记，分数介于 1 与 4 之间
    for (const [id, strength] of [['a', 2], ['b', 3], ['c', 1.5]]) {
      await db.run(
        'INSERT OR IGNORE INTO local_notes (id, content, created_at, updated_at) VALUES (?, ?, ?, ?)',
        [id, id, 0, 0],
      )
      const v = new Float32Array(DIM)
      v[0] = strength
      await index.add(id, v, 'm')
    }
    const hits = index.search(queryVec(1, 0), 10)
    const longs = hits.filter((h) => h.noteId === 'long')
    assert.equal(longs.length, 1,
      `结果里 'long' 出现了 ${longs.length} 次 ⇒ 同一笔记的多个段各占了一个 top-k 名额`)
    // 归一化后：段 i 的向量是 (1,0) 方向 ⇒ 分数恒为 1；强度不影响方向，只影响归一化后是否仍朝同一方向。
    // 这里所有段都是 (x,0) 方向 ⇒ 归一化后都是 (1,0) ⇒ 分数都是 1。
    assert.equal(longs[0].score, 1, `long 的分数是 ${longs[0].score}，不是它最好那一段的分数`)
  })

  it('★★★ max 聚合：分数必须取**各段最高**，而不是平均或最后一段', async () => {
    const db = makeVectorDb()
    const index = new VectorIndex(db)
    await index.load()
    await db.run(
      'INSERT OR IGNORE INTO local_notes (id, content, created_at, updated_at) VALUES (?, ?, ?, ?)',
      ['m', 'm', 0, 0],
    )
    // ⚠⚠ 夹具必须让「最高段」「最后一段」「平均」三者**互不相同** ——
    //   第一版三段是 (1,0) / (0,1) / (0.5,0)：归一化后段 0 与段 2 **都是 (1,0)**，
    //   于是「取最后一段」恰好等于「取最高段」⇒ 变异全绿（反例没落在能起作用的区间）。
    //   现在用 (1,0) / (0,1) / (0.707,0.707)：
    //     最高段 = 1.000（第 1 段）   最后一段 = 0.707   平均 = 0.569
    const mk = (a, b) => { const v = new Float32Array(DIM); v[0] = a; v[1] = b; return v }
    await index.add('m', [mk(1, 0), mk(0, 1), mk(0.707, 0.707)], 'm')
    const hits = index.search(queryVec(1, 0), 5)
    assert.equal(hits.length, 1, `'m' 出现了 ${hits.length} 次`)
    assert.ok(Math.abs(hits[0].score - 1) < 1e-6,
      `分数是 ${hits[0].score}：最高段应是 1.000，最后一段是 0.707、平均是 0.569 ⇒ max 聚合被改坏了`)
  })

  it('★★★ 旧数据（chunks=1）必须照样能检索，且与段级数据同形', async () => {
    const db = makeVectorDb()
    const index = new VectorIndex(db)
    await index.load()
    await db.run(
      'INSERT OR IGNORE INTO local_notes (id, content, created_at, updated_at) VALUES (?, ?, ?, ?)',
      ['legacy', 'legacy', 0, 0],
    )
    // 模拟旧库：直接写一行 chunks=1 的向量（不经过 add）
    const v = new Float32Array(DIM)
    v[0] = 1
    await db.run(
      `INSERT INTO local_note_vectors (note_id, embedding, dim, model, chunks, created_at)
       VALUES (?, ?, ?, ?, 1, ?)`,
      ['legacy', new Uint8Array(v.buffer).buffer, DIM, 'm', 0],
    )
    await index.load()
    const hits = index.search(queryVec(1, 0), 5)
    assert.equal(hits.length, 1, `旧数据检索不到（hits=${JSON.stringify(hits)}）`)
    assert.equal(hits[0].noteId, 'legacy')
  })

  it('★★★ 库里一行装 N 段：embedding BLOB 长度 = chunks × dim × 4', async () => {
    const db = makeVectorDb()
    const index = new VectorIndex(db)
    await index.load()
    await db.run(
      'INSERT OR IGNORE INTO local_notes (id, content, created_at, updated_at) VALUES (?, ?, ?, ?)',
      ['x', 'x', 0, 0],
    )
    await index.add('x', chunkVecs(7), 'm')
    const rows = await db.all('SELECT embedding, dim, chunks FROM local_note_vectors WHERE note_id = ?', ['x'])
    assert.equal(rows.length, 1, `写了 ${rows.length} 行 ⇒ 一条笔记应只有一行`)
    const blob = rows[0].embedding
    const bytes = blob.byteLength ?? blob.length
    assert.equal(rows[0].chunks, 7, `chunks 列是 ${rows[0].chunks}，应为 7`)
    assert.equal(bytes, 7 * DIM * 4, `BLOB 是 ${bytes} 字节，应为 ${7 * DIM * 4}（7 段 × ${DIM} 维 × 4B）`)
  })

  it('★★★ 段数变化时必须重建（否则段的连续布局会错位）', async () => {
    const db = makeVectorDb()
    const index = new VectorIndex(db)
    await index.load()
    await db.run(
      'INSERT OR IGNORE INTO local_notes (id, content, created_at, updated_at) VALUES (?, ?, ?, ?)',
      ['y', 'y', 0, 0],
    )
    await index.add('y', chunkVecs(3), 'm')
    assert.equal(index.size(), 3, '3 段应占 3 行')
    // 改成 5 段
    await index.add('y', chunkVecs(5), 'm')
    assert.equal(index.size(), 5, `改成 5 段后索引里是 ${index.size()} 行 ⇒ 段数变化时没有重建`)
    assert.equal(index.noteCount(), 1, `noteCount() 是 ${index.noteCount()}，应为 1（一条笔记）`)
    // 检索仍然按 5 段工作
    const hits = index.search(queryVec(1, 0), 5)
    assert.equal(hits.length, 1, `重建后检索结果有 ${hits.length} 条`)
  })

  it('★★★ 生产路径必须按段嵌入（不得再把整篇发给 /api/embed）', () => {
    const src = readFileSync(new URL('../../features/notes/notes-persist.ts', import.meta.url), 'utf8')
    assert.match(src, /splitForEmbedding\(/, 'notes-persist 没有调用切段器 ⇒ 长会议仍按整篇发，必然 400')
    assert.doesNotMatch(src, /JSON\.stringify\(\{ text: content \}\)/,
      '还在把整篇 content 发给 /api/embed ⇒ 超长笔记必然 400')
  })
})

/* ═══════════════════════════════════════════════════════════════════════════
 * §899 · D68：编辑长会议时**只重嵌变了的段**
 * ═══════════════════════════════════════════════════════════════════════════
 * §898 落地后暴露的最大成本：改一条 4 小时会议 = 15 次 `/api/embed` 调用，每次保存重来。
 *
 * ⭐ 本节的**前提**是「段边界在编辑后保持稳定」—— 而这一条**不是显然的**：
 *   固定偏移切段时改一个字会让后面所有段的边界整体位移 ⇒ 每一段文本都变了
 *   ⇒ 增量重嵌**一段也省不下**。
 *   实测（4 小时会议 82688 字 / 900 段，改第 3 段里一个词）：
 *     固定偏移  ⇒ 逐字未变的段 **0/22**（0%）
 *     段落锚定  ⇒ 逐字未变的段 **20/21**（95%）
 * ⇒ 所以「段落锚定切段」不是风格偏好，是**增量重嵌能不能成立**的前提。
 */
describe('§899 · D68：增量重嵌与段边界稳定性', () => {
  /**
   * 一段会议转写风格的文本：n 段，每段约 110 字。
   *
   * ⚠⚠⚠ **n 必须让文本超过 16000 字**，否则 `splitForEmbedding` 走
   *   「整篇一段」的早退分支 ⇒ **段落分组那整段代码一次都没执行**
   *   ⇒ 下面几条判据会全绿而什么都没测。
   *   ⚠ 这不是假想的风险：本节第一版用 n=120（≈13440 字），
   *   三条判据全部落在早退分支上 —— 「段文本重新拼接」那个变异因此**打不进任何判据**。
   *   ⇒ 下面的「前置闸」就是为此存在的。
   */
  function meetingText(n) {
    return Array.from({ length: n }, (_, i) =>
      `第${i}周进展 完成了接口联调部分 剩余工作按排期推进 风险点已同步给负责人 ${'细节'.repeat(25)}`,
    ).join('\n\n')
  }

  /**
   * ⭐ **真实转写形状**的语料（§900）。
   *
   * ⚠⚠ 实测 `.artifacts/asr-s788/seg-asr/merged.txt`（一场真实会议的合并转写）：
   *   3392 字 / **52 行** / 单 `\n` **52** 次 / `\n\n` **0** 次 / **空行率 0%**
   *   每行就是一个 ASR 分段（中位 66 字）。
   *   ⇒ 「空行锚点」在真实数据上**一个边界都找不到** ⇒ 增量重嵌退化成 0 复用。
   *
   * ⚠ 这条语料必须保持这个形状 —— 下面每条判据的结论都建立在它身上。
   */
  function realTranscriptShape(n) {
    return Array.from({ length: n }, (_, i) =>
      `第${i}点网关的抽象层现在有一个整体系有一套那个整大模型抽象层，然后所谓的CI能够借用到这个抽象层${'细节'.repeat(5)}`,
    ).join('\n')
  }

  it('★★★ 真实转写形状的语料：改一处之后仍要绝大多数段不变（§900 回归钉）', () => {
    // ⚠ 下面这条是 §900 的**回归钉**：§899 的空行锚点在这条语料上复用率是 0。
    const text = realTranscriptShape(320)
    // 先自证语料形状 —— 不然下面量的是「另一个形状」
    assert.equal(text.split('\n\n').length, 1, '语料里出现了空行 ⇒ 不是真实转写的形状')
    assert.equal(text.split('\n').length, 320, '行数不对')
    assert.ok(text.length > 16000, `语料只有 ${text.length} 字，还没超过单发上限`)

    const before = splitForEmbedding(text)
    const after = splitForEmbedding(text.replace('第8点', '第8点（已更正）'))
    assert.ok(before.length > 1, `只切出 ${before.length} 段 ⇒ 没走到分段路径`)
    const set = new Set(before.map((c) => c.text))
    const same = after.filter((c) => set.has(c.text)).length
    assert.ok(
      same / after.length >= 0.6,
      `真实形状下改一处只有 ${same}/${after.length} 段未变（${((same / after.length) * 100).toFixed(1)}%）` +
        `⇒ 锚点退化了（空行锚点在真实转写上复用率是 0，§900 实测）`,
    )
  })

  it('★★★ 前置闸：夹具必须真的走「分段」路径（否则下面几条全是恒真）', () => {
    const text = meetingText(300)
    assert.ok(text.length > 16000, `夹具只有 ${text.length} 字，还没超过单发上限 16000 ⇒ 下面走的是早退分支`)
    const chunks = splitForEmbedding(text)
    assert.ok(chunks.length > 1, `夹具只切出 ${chunks.length} 段 ⇒ 段落分组没被走到`)
    // 段落分组路径的标志：段里**含段落分隔符**（早退分支不会有）
    assert.ok(chunks.some((c) => c.text.includes('\n\n')), '没有任何一段含段落分隔符 ⇒ 走的是早退分支')
  })

  it('★★★ 段落锚定：改一处之后，绝大多数段**逐字不变**（增量重嵌的前提）', () => {
    const text = meetingText(300)
    const before = splitForEmbedding(text)
    const after = splitForEmbedding(text.replace('第2周进展', '第2周进展（已更正）'))
    assert.ok(before.length >= 5, `语料只切出 ${before.length} 段，夹具太短`)
    const set = new Set(before.map((c) => c.text))
    const same = after.filter((c) => set.has(c.text)).length
    // ⚠ 门槛定在 80%：改的是**第 3 段**，理论上只该失效 1 段。
    //   门槛不能定成 100% —— 段落分组是「累积到目标长度」，
    //   改早段会让**分组的边界**移动一点，影响相邻一两组。
    assert.ok(
      same / after.length >= 0.8,
      `改一处后只有 ${same}/${after.length} 段逐字未变（${((same / after.length) * 100).toFixed(1)}%）` +
        `⇒ 段边界不稳定 ⇒ 增量重嵌省不下调用`,
    )
  })

  it('★★★ 每段必须都是原文的**真子串**且偏移精确（不能重新拼接）', () => {
    // ⚠ 第一版用 split + join('\n\n') 重建段落 ⇒ 原文分隔符是 '\n \n' 时
    //   重建文本与原文不再逐字相同、累积偏移会漂移 ⇒ 哈希与原文对不上。
    // ⚠⚠ 语料必须**超过上限**（见上面的前置闸），否则根本走不到分组那行。
    for (const sep of ['\n\n', '\n \n', '\n\n\n']) {
      const text = meetingText(300).split('\n\n').join(sep)
      const chunks = splitForEmbedding(text)
      assert.ok(chunks.length > 1, `分隔符 ${JSON.stringify(sep)} 下只切出 ${chunks.length} 段（夹具没超上限）`)
      for (const c of chunks) {
        assert.equal(text.slice(c.start, c.end), c.text,
          `分隔符 ${JSON.stringify(sep)} 下有一段不是原文子串（start=${c.start} end=${c.end}）`)
      }
    }
  })

  it('★★★ 每段长度都必须在服务端上限内（改切段方式后这条不能被破）', () => {
    const text = meetingText(300)
    const chunks = splitForEmbedding(text)
    assert.ok(chunks.length > 1, '夹具没走到分段路径')
    for (const c of chunks) {
      assert.ok(c.text.length <= 16000, `有一段长 ${c.text.length} 字符，超过上限 16000`)
    }
  })

  it('★★★ 哈希必须可复现且对内容敏感（SHA-1 钉公开测试向量）', () => {
    // ⚠ 哈希是「这段有没有变过」的判据 ⇒ 它必须：
    //   ① 对同一输入永远给同一结果；
    //   ② 对不同输入给出不同结果；
    //   ③ **实现本身可核验**（不能自证）⇒ 钉公开测试向量。
    assert.equal(sha1Hex(''), 'da39a3ee5e6b4b0d3255bfef95601890afd80709', 'sha1("") 不对')
    assert.equal(sha1Hex('abc'), 'a9993e364706816aba3e25717850c26c9cd0d89d', 'sha1("abc") 不对')
    assert.equal(
      sha1Hex('a'.repeat(1000000)),
      '34aa973cd4c4daa4f61eeb2bdbad27316534016f',
      'sha1(百万个 a) 不对（多块路径有问题）',
    )
    assert.notEqual(sha1Hex('会议转写'), sha1Hex('会议转写 '), '末尾多一个空格都没变 ⇒ 哈希不敏感')
  })

  it('★★★ 复用必须精确：只有**逐字相同**的段才复用', async () => {
    const db = makeVectorDb()
    const index = new VectorIndex(db)
    await index.load()
    await db.run(
      'INSERT OR IGNORE INTO local_notes (id, content, created_at, updated_at) VALUES (?, ?, ?, ?)',
      ['n1', 'n1', 0, 0],
    )
    const text = meetingText(300)
    const chunks = splitForEmbedding(text)
    assert.ok(chunks.length > 1, '夹具没走到分段路径')
    const hashes = chunks.map((c) => sha1Hex(c.text))
    const vecs = chunks.map((_, i) => { const v = new Float32Array(DIM); v[0] = i + 1; return v })
    await index.add('n1', vecs, 'm', hashes)

    const prev = await index.existingChunks('n1')
    assert.ok(prev, 'existingChunks 读不到刚写进去的笔记')
    assert.equal(prev.hashes.length, chunks.length, '哈希条数与段数对不上')
    assert.equal(prev.vectors.length, chunks.length, '向量条数与段数对不上')
    assert.equal(prev.model, 'm')

    // 改动第 2 段 ⇒ 第 2 段的哈希变了，其余不变
    const edited = text.replace('第2周进展', '第2周进展（已更正）')
    const chunks2 = splitForEmbedding(edited)
    const hashes2 = chunks2.map((c) => sha1Hex(c.text))
    const reusable = hashes2.filter((h) => prev.hashes.includes(h)).length
    assert.ok(reusable >= chunks2.length - 2,
      `${chunks2.length} 段里只有 ${reusable} 段可复用（改动只涉及 1 段）⇒ 复用判定太保守`)
    // 改过的那一段绝不能被复用
    const changed = hashes2.find((h) => !prev.hashes.includes(h))
    assert.ok(changed, '改动后没有任何段的哈希变化 ⇒ 哈希没起作用')
  })

  it('★★★ 换模型时不得复用旧向量（不同模型的向量不在同一空间）', () => {
    // ⚠ 这条钉的是**决策**（换模型 ⇒ 全部重嵌）。
    //   ⚠⚠ 第一版是**源码门**（找 `modelChanged` 与 `embedChunks(chunks, null)` 两串字），
    //   结果变异「把条件改成 `if (false && modelChanged)`」**全绿** ——
    //   源码门对**同形**改动无效（这正是 §95.3 的老教训）。
    //   ⇒ 改判据形态：抽一个**纯函数**出来，判它**行为**。
    // ⚠⚠ 这里**不再**保留任何源码门：
    //   第一版的源码门（找 `modelChanged` 这串字）在变异「把条件改成
    //   `if (false && modelChanged)`」下**全绿**；更糟的是它一度**只因为一条过期注释里
    //   还留着 `modelChanged` 这个词就绿着** —— 判据命中了自己写的解释注释。
    //   ⇒ 源码门对**同形**改动无效，决策必须放纯函数、用**行为**验。
    //   （决策放纯函数是本仓 §820 的手法）
    assert.equal(needsFullRebuild('model-a', 'model-a', 3), false, '同模型却要求全部重嵌')
    assert.equal(needsFullRebuild('model-b', 'model-a', 3), true, '换模型却没要求全部重嵌')
    assert.equal(needsFullRebuild('model-b', 'model-a', 0), false,
      '一段都没复用时不需要重嵌（本来就要全部调 API）')
    assert.equal(needsFullRebuild(null, 'model-a', 3), false, '没有 API 模型读数时不该要求重嵌')
  })

  it('★★★ 纯函数必须真的被生产代码调用（否则「验对了但没接上」）', () => {
    // ⚠ 上一条验的是**纯函数的行为**；这一条验的是**接线**。
    //   两者缺一不可：只验行为 ⇒ 函数对了但生产代码压根没调它；
    //   只验接线 ⇒ 函数写错了照样绿。
    // ⚠ 这一条**只是「存在性」**，不是「语义」——
    //   把调用短路成 `if (false && needsFullRebuild(...))` 它照样绿。
    //   ⇒ 语义由上一条的行为断言负责，这里只保证「接上了」。
    const src = readFileSync(new URL('../../features/notes/notes-persist.ts', import.meta.url), 'utf8')
    assert.match(src, /needsFullRebuild\(/,
      '生产代码没有调用 needsFullRebuild ⇒ 换模型时不会全部重嵌（纯函数白写）')
    assert.match(src, /existingChunks\(/,
      '生产代码没有调用 existingChunks ⇒ 增量重嵌根本不会发生（每次都全部重嵌）')
  })

  it('★★★ 生产路径必须用段落锚定的切段（增量重嵌的前提）', () => {
    const src = readFileSync(new URL('../../features/notes/note-storage-policy.ts', import.meta.url), 'utf8')
    assert.match(src, /groupByParagraph/, '切段器里没有段落分组 ⇒ 段边界不稳定 ⇒ 增量重嵌不成立')
  })
})

/* ═══════════════════════════════════════════════════════════════════════════
 * §901 · D70：随手记链的转写**零分隔符** ⇒ 锚点必须能退化到「句号」
 * ═══════════════════════════════════════════════════════════════════════════
 * §900 把锚点改成「任意换行」后，本节查了**另一条**转写链，发现它**根本没有行**：
 *   · 会议链 `renderTranscript`（meeting-dedup.ts:429）⇒ `[说话人] 文本` + `\n` ✔
 *   · 随手记增量链 `appendTranscript`（note-recording.ts:74）
 *     ⇒ `` `${committed}${finalChunk}` ``，**零分隔符直接拼接** ⇒ 整份是**一整行**
 *   ⇒ 只认「行」的锚点在随手记链上一个边界都找不到：
 *     实测（零换行的真实转写 20046 字、798 个句号）改两个词：
 *       行锚点 2 段，**0/2 存活**；句号锚点 6 段，**5/6 存活**。
 *
 * ⇒ 锚点按**可用结构逐级退化**：行 → 句号 → 纯大小切。
 */
describe('§901 · D70：锚点必须能退化到句号（随手记链没有行）', () => {
  /** 随手记增量链的形状：**零换行**，一整行。 */
  function quickNoteShape(n) {
    // ⚠ n 必须让文本**超过 16000**（每段约 41 字 ⇒ n≥450），
    //   否则走「整篇一段」早退分支 ⇒ 下面几条判据全是恒真。
    //   ⚠ 这不是假想：n=300 时只有 12190 字，前置闸当场报红（它就是这么用的）。
    return Array.from({ length: n }, (_, i) =>
      `第${i}点网关的抽象层现在有一套整大模型抽象层。${'细节'.repeat(5)}这样才能最好。`,
    ).join('')
  }

  it('★★★ 前置闸：语料必须是「零换行 + 有句号 + 超过上限」的真实随手记形状', () => {
    const text = quickNoteShape(600)
    assert.equal(text.includes('\n'), false, '语料里出现了换行 ⇒ 不是随手记链的形状')
    assert.ok((text.match(/[。！？]/g) || []).length > 100, '句号太少 ⇒ 句号锚点没有可用的边界')
    assert.ok(text.length > 16000, `语料只有 ${text.length} 字，还没超过单发上限`)
    assert.ok(splitForEmbedding(text).length > 1, '只切出一段 ⇒ 夹具没走到分段路径')
  })

  it('★★★ 零换行的转写：改一处之后仍要绝大多数段不变（句号退化）', () => {
    const text = quickNoteShape(600)
    const edited = text.replace('第8点', '第8点（已更正）')
    const before = splitForEmbedding(text)
    const after = splitForEmbedding(edited)
    assert.ok(before.length > 1, '夹具没走到分段路径')
    const set = new Set(before.map((c) => c.text))
    const same = after.filter((c) => set.has(c.text)).length
    assert.ok(
      same / after.length >= 0.6,
      `零换行语料下改一处只有 ${same}/${after.length} 段未变（${((same / after.length) * 100).toFixed(1)}%）` +
        `⇒ 句号退化没生效（只认行时实测是 0/2，§901）`,
    )
  })

  it('★★★ 退化不得破坏其它不变量（子串 / 上限）', () => {
    const text = quickNoteShape(600)
    const edited = text.replace('第8点', '第8点（已更正）')
    for (const c of splitForEmbedding(edited)) {
      assert.ok(c.text.length <= 16000, `有一段长 ${c.text.length} 字符，超过上限`)
      assert.equal(edited.slice(c.start, c.end), c.text, '有一段不是原文真子串')
    }
  })

  it('★★★ 有行的语料必须优先用行（行是转写的自然单元）', () => {
    // ⚠ 优先级判据：会议链**同时**有行和句号，必须走行。
    // ⚠⚠ 第一版用「段里包含换行」来判 ⇒ **无区分力**：
    //   按句号分组时一个组会横跨很多行，`text.includes('\n')` 照样为真，
    //   变异「把两级顺序颠倒」全绿。
    // ⇒ 改用能区分的性质：**每段都起止于行边界**
    //   （实测同一语料：行锚定 5/5 段起止在行边界；句号锚定只有 1/5）。
    const text = Array.from({ length: 400 }, (_, i) => `第${i}行只有一句。${'补充细节'.repeat(8)}`).join('\n')
    const chunks = splitForEmbedding(text)
    assert.ok(chunks.length > 1, '夹具没走到分段路径')
    // ⚠⚠ 这里也踩过一次：两个辅助函数的名字**写反了**（`atLineEnd` 实际测的是行首），
    //   于是判据在**正确实现**上报红 —— 我第一反应差点去改实现。
    //   ⇒ 「判据红了」的第一件事仍然是「先问是不是量具写错了」，别先动被测代码。
    const startIsLineStart = (i) => i === 0 || text[i - 1] === '\n'
    const endIsLineEnd = (i) => i === text.length || text[i] === '\n'
    const badStart = chunks.filter((c) => !startIsLineStart(c.start))
    const badEnd = chunks.filter((c) => !endIsLineEnd(c.end))
    assert.equal(badStart.length, 0,
      `${badStart.length}/${chunks.length} 段的起点不在行边界 ⇒ 用的是句号锚点，而有行时应当优先用行`)
    assert.equal(badEnd.length, 0,
      `${badEnd.length}/${chunks.length} 段的终点不在行边界 ⇒ 用的是句号锚点，而有行时应当优先用行`)
  })
})

/* ═══════════════════════════════════════════════════════════════════════════
 * §902 · D71：`local_note_vectors` 的两个新列，**旧库升级后语义必须不变**
 * ═══════════════════════════════════════════════════════════════════════════
 * §898 加了 `chunks`（一行装几个段向量）、§899 加了 `chunk_hashes`（增量重嵌用）。
 * 两者都是**纯加列**，好处正是「旧数据零改动继续可用」——
 * ⚠ 但「旧数据零改动」这句话，到本节为止**没有任何判据在管**，纯靠注释。
 *
 * 本节跑**真 SQLite**（`node:sqlite`，与本文件 §879 那几段同一手法）：
 *   ① 按「§898 之前的旧形状」建表并塞一条旧行；
 *   ② 用**从 local-db.ts 源码里抽出来的** ALTER 语句升级；
 *   ③ 断言旧行语义不变（chunks 默认 1、哈希默认空、向量仍能解析成 DIM 维）。
 * ⇒ ② 之所以从源码抽：判据里抄一份 SQL 就是**第二份真相**，日后必然漂移。
 */
describe('§902 · D71：向量表加列后，旧库语义不变', () => {
  /** 从 local-db.ts 的源码里抽出 `*_COLUMNS` 数组的内容（不手抄 SQL）。 */
  function columnSql(name) {
    const src = readFileSync(new URL('../local-db.ts', import.meta.url), 'utf8')
    const at = src.indexOf(`const ${name} = [`)
    assert.ok(at >= 0, `源码里找不到 ${name}`)
    const end = src.indexOf('\n]', at)
    assert.ok(end > at, `${name} 的数组没闭合`)
    const body = src.slice(at, end)
    return [...body.matchAll(/sql:\s*("(?:[^"\\]|\\.)*"|'[^']*')/g)].map((m) =>
      m[1][0] === '"' ? JSON.parse(m[1]) : m[1].slice(1, -1),
    )
  }

  it('★★★ 迁移清单必须声明这两个列（否则旧库升级后 load() 会查询失败）', () => {
    const sql = columnSql('NOTE_VECTORS_V1_COLUMNS')
    assert.equal(sql.length, 2, `NOTE_VECTORS_V1_COLUMNS 有 ${sql.length} 条，期望 2（chunks + chunk_hashes）`)
    assert.match(sql[0], /ALTER TABLE local_note_vectors ADD COLUMN chunks INTEGER NOT NULL DEFAULT 1/,
      `chunks 列的 ALTER 不对：${sql[0]}`)
    assert.match(sql[1], /ALTER TABLE local_note_vectors ADD COLUMN chunk_hashes TEXT NOT NULL DEFAULT ''/,
      `chunk_hashes 列的 ALTER 不对：${sql[1]}`)
  })

  it('★★★ 旧库升级后语义不变（真 SQLite：chunks 默认 1、哈希默认空、向量仍可解析）', () => {
    const sqlite = new DatabaseSync(':memory:')
    // §898 **之前**的表形状
    sqlite.exec(`
      CREATE TABLE local_notes (id TEXT PRIMARY KEY, content TEXT, created_at INTEGER, updated_at INTEGER);
      CREATE TABLE local_note_vectors (
        note_id TEXT PRIMARY KEY,
        embedding BLOB NOT NULL,
        dim INTEGER NOT NULL,
        model TEXT NOT NULL,
        created_at INTEGER NOT NULL
      );
    `)
    const vec = new Float32Array(DIM)
    vec[0] = 1
    sqlite.prepare('INSERT INTO local_notes (id, content, created_at, updated_at) VALUES (?,?,?,?)')
      .run('old1', 'old1', 0, 0)
    sqlite.prepare('INSERT INTO local_note_vectors (note_id, embedding, dim, model, created_at) VALUES (?,?,?,?,?)')
      .run('old1', new Uint8Array(vec.buffer), DIM, 'm', 0)

    // 升级：用源码里的 ALTER 语句（不是判据里手抄的一份）
    for (const sql of columnSql('NOTE_VECTORS_V1_COLUMNS')) sqlite.exec(sql)

    const cols = sqlite.prepare("SELECT name FROM pragma_table_info('local_note_vectors')").all().map((r) => r.name)
    for (const c of ['chunks', 'chunk_hashes']) {
      assert.ok(cols.includes(c), `升级后仍然没有 ${c} 列（现有：${cols.join(',')}）`)
    }
    const row = sqlite.prepare('SELECT note_id, chunks, chunk_hashes, dim FROM local_note_vectors WHERE note_id = ?').get('old1')
    assert.ok(row, '升级后旧行不见了')
    assert.equal(row.chunks, 1, `旧行 chunks=${row.chunks}，应为 1（旧数据是「整篇一个向量」）`)
    assert.equal(row.chunk_hashes, '', `旧行 chunk_hashes=${JSON.stringify(row.chunk_hashes)}，应为空串（⇒ 下次一律重嵌，安全侧）`)
    // 旧向量仍能解析成 DIM 维 —— 这是「升级没有损坏数据」的核心
    const blob = row.dim ?? DIM
    const raw = sqlite.prepare('SELECT embedding FROM local_note_vectors WHERE note_id = ?').get('old1').embedding
    const bytes = new Uint8Array(raw.buffer, raw.byteOffset, raw.byteLength)
    assert.equal(bytes.byteLength, blob * 4, `旧向量 BLOB 是 ${bytes.byteLength} 字节，应为 ${blob * 4}`)
    const f = new Float32Array(bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength))
    assert.equal(f.length, blob, `旧向量解析出 ${f.length} 维，应为 ${blob}`)
    assert.equal(f[0], 1, '旧向量的内容被升级改动了')
  })

  it('★★★ 新库（DDL 内联列）必须与迁移后的列形状一致', () => {
    const src = readFileSync(new URL('../schema.ts', import.meta.url), 'utf8')
    const ddl = src.slice(src.indexOf('CREATE TABLE IF NOT EXISTS local_note_vectors'))
    assert.match(ddl.slice(0, ddl.indexOf(');')), /chunks INTEGER NOT NULL DEFAULT 1/,
      'schema.ts 的 DDL 里没有 chunks 列 ⇒ 新库与升级后的旧库形状不同')
    assert.match(ddl.slice(0, ddl.indexOf(');')), /chunk_hashes TEXT NOT NULL DEFAULT ''/,
      'schema.ts 的 DDL 里没有 chunk_hashes 列 ⇒ 新库与升级后的旧库形状不同')
  })
})

/* ═══════════════════════════════════════════════════════════════════════════
 * §903 · D72：`load()` 失败一次 ⇒ **整场会话的语义检索静默失效**
 * ═══════════════════════════════════════════════════════════════════════════
 * §902 补了「迁移把旧库升级成什么样」的保障，但没管**迁移失败之后**会怎样。
 * 实测（Node 22，生产 `VectorIndex` + 真 node:sqlite）：
 *
 *   启动时 `lobster-init.ts:87` 调 `vectorIndex.load()`，失败只 `console.warn`；
 *   而 `prepare()` 拿 `!this.loaded` 当闸，`add()` 又从不置 `loaded`
 *   ⇒ 向量**已经落库**、`add()` 也确实把新向量写进了 `ids`/`matrix`/`noteSpans`，
 *     但 `search()` / `searchAsync()` 恒返回空
 *   ⇒ 用户看到的是「搜不到」，界面上没有任何错误，日志里只有一行 warn。
 *
 * ⚠ **触发条件的口径**（这一条是量出来的，不是猜的）：
 *   先试过「一行坏数据让 load() 抛错」，**不成立** —— SQLite 是动态类型，
 *   BLOB 列里塞 TEXT 时 `new Float32Array("hello")` 给出**长度 0**（不抛），
 *   塞 number 时给出等长的**全零**数组（也不抛）⇒ 两种都被 `if (off + DIM > usable) break`
 *   静默跳过。⇒ `load()` 现实里只有 **DB 级错误**（库忙 / 未迁移 / 密钥错）才会抛。
 *   ⚠ 那个 DB 级错误本身 ⛔ 未在真机复现，本节量的是**它一旦发生后的后果**。
 */
describe('§903 · D72：load() 失败一次后，新向量必须仍然可被检索到', () => {
  /**
   * 真 SQLite + 在 `all()` 上注入**一次** DB 级失败（复刻「启动那一刻库忙」）。
   * ⚠ 只注入 `all()` 不注入 `run()`：未迁移的表上 `add()` 的 INSERT 也会抛，
   *   那是另一条（且更彻底）的失效路径；本节要量的是
   *   「**写能写、读索引读不出来**」这个更隐蔽的形态。
   */
  function makeFlakyLoadDb() {
    const inner = makeVectorDb()
    let fail = true
    return {
      run: (sql, params = []) => inner.run(sql, params),
      all: async (sql, params = []) => {
        if (fail) { fail = false; throw new Error('database is locked (SQLITE_BUSY)') }
        return inner.all(sql, params)
      },
    }
  }

  it('★★★ 前置闸：确认夹具真能让 load() 抛错（否则下面两条全是恒真）', async () => {
    const db = makeFlakyLoadDb()
    await assert.rejects(() => new VectorIndex(db).load(), /locked/,
      '夹具没能让 load() 抛错 ⇒ 下面的判据会在没有失效的状态下空转')
  })

  it('★★★ load() 失败后 add() 的新向量，search / searchAsync 都必须能命中', async () => {
    const db = makeFlakyLoadDb()
    await seedNote(db, 'note-1')
    const index = new VectorIndex(db)
    // 启动路径：lobster-init.ts:87 —— 失败只 warn，App 照常 ready
    await assert.rejects(() => index.load(), /locked/)
    await index.add('note-1', vec(1, 0), 'm')

    const sync = index.search(queryVec(1, 0), 5)
    const async = await index.searchAsync(queryVec(1, 0), 5)
    assert.equal(sync.length, 1,
      `同步 search 恒 0 命中（内存索引里明明有 note-1）⇒ add() 没把索引标记为已装载`)
    assert.equal(async.length, 1, `异步 searchAsync 恒 0 命中（生产走的是它）`)
    assert.ok(Math.abs(sync[0].score - 1) < 1e-6, `score=${sync[0].score}，应≈1`)
  })

  it('★★★ 坏的是内存索引、不是数据：换个实例重新 load() 就必须能搜到', async () => {
    const db = makeFlakyLoadDb()
    await seedNote(db, 'note-1')
    const first = new VectorIndex(db)
    await assert.rejects(() => first.load(), /locked/)
    await first.add('note-1', vec(1, 0), 'm')

    // 换一个实例 = 模拟「下次冷启动」，此时 all() 已经不失败了
    const next = new VectorIndex(db)
    await next.load()
    const hits = next.search(queryVec(1, 0), 5)
    assert.equal(hits.length, 1,
      '重新 load() 后仍搜不到 ⇒ 向量根本没落库（问题在落库侧，不在内存索引）')
  })

  // ⚠⚠ 自曝：本节第一版还写了第 4 条「未装载且未 add 的索引 search 必须返回 0」
  //   （想当「别把启动期的空索引守卫弄掉」的负控）。**已删** —— 它是恒真门：
  //   实测变异 M60-V2 把 `prepare()` 里的 `!this.loaded || this.ids.length === 0`
  //   **整个删掉**，这条判据**照样全绿**（54/54）⇒ 因为 n=0 时 for 循环本来就空转。
  //   ⇒ 按 §891「不拿恒真门充数」删掉，不补。
  //   ⭐ 顺带一条读数：那两个守卫**当前是冗余的** —— 删掉它们不改变任何行为。
  //     本节的修复**没有去动它们**（它们还表达着「没装载就不作答」的意图，
  //     将来 n 的定义一变就可能有意义），但如实登记「今天删了也没事」。
})

/* ═══════════════════════════════════════════════════════════════════════════
 * §904 · D73：`dim` 列**只写不读**；三条路径全都拿模块常量 `DIM` 无条件解读
 * ═══════════════════════════════════════════════════════════════════════════
 * 缺陷（实测，Node 22 + 真 node:sqlite + 生产 add()/load()/existingChunks()）：
 *
 *   | 入参/库里的行            | 修之前                                   | 后果 |
 *   |------------------------|-----------------------------------------|------|
 *   | 网关换成 3072 维模型      | 截断成**前一半**，`dim` 列照旧写 1536      | 索引里是半条向量，**自信地答错** |
 *   | 网关换成 1024 维模型      | 补零到 1536，`dim` 列照旧写 1536          | `dim` 列**记录的不是事实** |
 *   | 库里已是别的维度的行       | `load()` 切不出完整一段，被 `break` 掉     | 该笔记**静默消失**，零诊断 |
 *
 * ⇒ 关键事实：`load()` 的 SELECT **根本没查 `dim`** ⇒ 那个列写了两年没人核对。
 * ⇒ 修法：**排除并出声**，不硬凑成 DIM（凑出来的向量会让搜索自信地答错，比搜不到更糟）。
 */
describe('§904 · D73：维度不对的向量必须被排除并出声，而不是硬凑', () => {
  /**
   * 抓 warn（§883 的「只出声、不阻断」：出声是本节要验的一半）。
   *
   * ⚠⚠⚠ **第一版是同步的，被两条判据当场揭穿**：`try { return { value: fn(), lines } }
   *   finally { restore }` 里，`finally` 在 `fn()` **返回 promise 的那一刻**就跑了
   *   —— 而 `load()` 的第一句就是 `await this.db.all(...)`，
   *   它后面 emit 的 warn 全部发生在 restore 之后 ⇒ 抓了个空。
   *   `add()` 之所以碰巧过了，是因为它的 warn 在**第一个 await 之前**（同步段）。
   *   ⇒ 教训同 §901：**判据红先问「量具量的是不是那件事」**，别先动被测代码。
   */
  async function captureWarn(fn) {
    const original = console.warn
    const lines = []
    console.warn = (...a) => lines.push(a.join(' '))
    try { await fn() } finally { console.warn = original }
    return lines
  }

  /** 造一条**维度是 DIM 之外**的库行（模拟换过模型 / 换过设备 / 被写坏）。 */
  async function seedRowOfDim(db, nodeDb, noteId, dim) {
    await seedNote(db, noteId)
    const v = new Float32Array(dim)
    v[0] = 1
    await db.run(
      'INSERT INTO local_note_vectors (note_id, embedding, dim, model, chunks, chunk_hashes, created_at)' +
      ' VALUES (?,?,?,?,?,?,?)',
      [noteId, new Uint8Array(v.buffer), dim, `model-${dim}`, 1, '', 0],
    )
  }

  it('★★★ 前置闸：非 DIM 的向量确实进不了 DIM 宽的索引（否则下面几条都是恒真）', async () => {
    const db = makeVectorDb()
    await seedRowOfDim(db, null, 'odd', 1024)
    const q = queryVec(1, 0)
    assert.equal(q.length, DIM, '查询向量必须与 DIM 同宽')
    // 数学前提：1024 个 float 切不出 1536 宽的一段。
    assert.ok(1024 < DIM, `夹具前提不成立：${1024} 不小于 ${DIM}`)
  })

  it('★★★ add() 收到非 DIM 维的向量：拒绝写库 + 出声', async () => {
    const db = makeVectorDb()
    await seedNote(db, 'big')
    const index = new VectorIndex(db)
    await index.load()
    const over = new Float32Array(3072); over[0] = 1
    const lines = await captureWarn(() => index.add('big', over, 'model-3072'))
    assert.ok(lines.some((l) => l.includes('3072')),
      `没有为维度不符出声（收到的 warn：${JSON.stringify(lines)}）`)
    const row = await db.all('SELECT dim FROM local_note_vectors WHERE note_id = ?', ['big'])
    assert.equal(row.length, 0,
      `仍写进库了（dim=${row[0]?.dim}）⇒ 半条向量会进索引，检索会自信地答错`)
  })

  it('★★★ 低维向量同样被拒绝（不能靠「补零」蒙混过关）', async () => {
    const db = makeVectorDb()
    await seedNote(db, 'small')
    const index = new VectorIndex(db)
    await index.load()
    const under = new Float32Array(1024); under[0] = 1
    const lines = await captureWarn(() => index.add('small', under, 'model-1024'))
    assert.ok(lines.some((l) => l.includes('1024')), `没有出声：${JSON.stringify(lines)}`)
    assert.equal((await db.all('SELECT note_id FROM local_note_vectors WHERE note_id = ?', ['small'])).length, 0,
      '低维向量被补零写进库了 ⇒ dim 列记录的不是事实')
  })

  it('★★★ 库里的低维行：load() 必须排除它，且**同库的好行仍然搜得到**', async () => {
    const db = makeVectorDb()
    await seedRowOfDim(db, null, 'odd', 1024)
    await seedNote(db, 'good')
    const index = new VectorIndex(db)
    await index.load()
    await index.add('good', vec(1, 0), 'm')

    const hits = index.search(queryVec(1, 0), 5)
    assert.equal(hits.length, 1, `好行没搜到（hits=${JSON.stringify(hits)}）⇒ 一条坏行把整个索引搞坏了`)
    assert.equal(hits[0].noteId, 'good')
    assert.ok(!hits.some((h) => h.noteId === 'odd'), '维度不对的行仍参与了检索')
  })

  it('★★★ 库里的高维行：load() 必须排除它（不能拿前一半当向量）', async () => {
    const db = makeVectorDb()
    await seedRowOfDim(db, null, 'big', 3072)
    const index = new VectorIndex(db)
    const lines = await captureWarn(() => index.load())
    assert.equal(index.search(queryVec(1, 0), 5).length, 0,
      '3072 维的行进了索引 ⇒ 取的是它的前一半，检索结果是错的')
    assert.ok(lines.some((l) => l.includes('3072')), `load() 没有出声：${JSON.stringify(lines)}`)
  })

  it('★★★ 坏行必须出声（不能只是静默消失）', async () => {
    const db = makeVectorDb()
    await seedRowOfDim(db, null, 'odd', 1024)
    const index = new VectorIndex(db)
    const lines = await captureWarn(() => index.load())
    assert.ok(lines.some((l) => l.includes('odd') && l.includes('1024')),
      `没有点名是哪一条、哪一维：${JSON.stringify(lines)}`)
  })

  it('★★★ 复用路径：高维 + 有哈希的行，绝不能被当成「可复用」', async () => {
    // ⚠ 这条是本节**唯一一条低维形态盖不住**的判据，形状必须精确：
    //   低维（1024）行在**修之前**就被 `hashes.length !== vectors.length` 挡住了
    //   （vectors 切出来是 0 条）⇒ 用低维形态写这条判据，它对修复是**恒绿**的（无牙）。
    //   高维（3072）+ **非空 chunk_hashes** 才是真的漏网：
    //     修之前 vectors 切出 **1 条**（前一半）、hashes 也是 1 条 ⇒ 长度相等
    //     ⇒ `existingChunks()` 返回「可复用」⇒ §899 会**拿半条向量当正确的旧向量**
    //       跳过 API 调用，笔记从此带着错向量留在索引里。
    const db = makeVectorDb()
    await seedNote(db, 'big')
    const v = new Float32Array(3072); v[0] = 1
    await db.run(
      'INSERT INTO local_note_vectors (note_id, embedding, dim, model, chunks, chunk_hashes, created_at)' +
      ' VALUES (?,?,?,?,?,?,?)',
      ['big', new Uint8Array(v.buffer), 3072, 'model-3072', 1, 'deadbeef', 0],
    )
    const index = new VectorIndex(db)
    await index.load()
    const prev = await index.existingChunks('big')
    assert.equal(prev?.hashes.length, 0,
      '维度不对的行仍被当成可复用 ⇒ §899 会拿半条向量跳过 API 调用')
    assert.equal(prev?.vectors.length, 0, '维度不对的行仍返回了向量')
  })

  it('★★ 正控：DIM 维的正常向量一切照旧（这条防「把好行也一起排除」）', async () => {
    const db = makeVectorDb()
    await seedNote(db, 'good')
    const index = new VectorIndex(db)
    const lines = await captureWarn(() => index.load())
    await index.add('good', vec(1, 0), 'm')
    assert.equal(lines.length, 0, `正常库不该出声：${JSON.stringify(lines)}`)
    const hits = index.search(queryVec(1, 0), 5)
    assert.equal(hits.length, 1, '正常向量搜不到了')
    assert.ok(Math.abs(hits[0].score - 1) < 1e-6, `score=${hits[0].score}`)
    // dim 列必须记**实际**维度
    const row = await db.all('SELECT dim FROM local_note_vectors WHERE note_id = ?', ['good'])
    assert.equal(row[0].dim, DIM, `dim 列记成了 ${row[0].dim}，应=${DIM}`)
  })
})
