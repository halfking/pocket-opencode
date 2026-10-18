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
