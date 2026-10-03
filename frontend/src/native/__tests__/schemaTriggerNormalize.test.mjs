/**
 * normalizeTriggerForPluginExecute tests — BUG-AH 的回归锁。
 *
 * 背景：splitSqlStatements 能把触发体完整交给插件，但插件的 Android `execute`
 * 还会按**字面量 `;` + LF** 再切一次。`CREATE TRIGGER ... BEGIN <stmt>;\nEND;`
 * 因此被从 `;\n` 处截断 → `Execute: incomplete input (code 1)` →
 * 真机上三个笔记 FTS 触发器一个都没建出来（虚表建出来了，因为它体内没有分号）。
 *
 * 真机判别实验（scripts/exp-trigger-bisect.mjs，7 组）：
 *   多行但「;」后跟空格 → ✅     单行但体内含 «;» + LF → ❌
 *   ⇒ 变量是「分号后面是不是 LF」，与多不多行、有没有内部分号都无关。
 *
 * 这里用 node:sqlite（真 SQLite）**实际执行**归一化后的 DDL 来判定，
 * 而不是只比对字符串 —— 字符串判据证明不了 SQLite 认不认。
 */
import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import { DatabaseSync } from 'node:sqlite'

import { SCHEMA_SQL, splitSqlStatements, normalizeTriggerForPluginExecute } from '../schema.ts'

/** 造一个与生产同构的最小库：普通表 + FTS5 虚表。 */
function makeDb() {
  const db = new DatabaseSync(':memory:')
  db.exec(`CREATE TABLE local_notes (rowid INTEGER PRIMARY KEY, title TEXT, content TEXT, search_text TEXT);`)
  db.exec(`CREATE VIRTUAL TABLE local_notes_fts USING fts5(title, content, content='local_notes', content_rowid='rowid');`)
  return db
}

function triggerNames(db) {
  return db
    .prepare(`SELECT name FROM sqlite_master WHERE type='trigger' ORDER BY name`)
    .all()
    .map((r) => r.name)
}

test('BUG-AH：未归一化的触发器在「分号紧跟 LF」时无法被单条执行', () => {
  // 这条是判据的自证：如果哪天插件不再按 «;»+LF 切分，这会失败，
  // 那时本文件的存在前提需要重新评估（而不是默默通过）。
  const db = makeDb()
  const raw = `CREATE TRIGGER zz_raw AFTER INSERT ON local_notes BEGIN
  INSERT INTO local_notes_fts(rowid, title, content)
  VALUES (new.rowid, new.title, COALESCE(NULLIF(new.search_text, ''), new.content));
END;`
  // 用 sqlite 自己的 prepare 模拟「被从 ;\n 处截断」：只送前半截
  let threw = false
  try {
    db.prepare(raw.slice(0, raw.indexOf(';\n') + 1)).run()
  } catch {
    threw = true
  }
  assert.ok(threw, '截断后的半条语句本应编译失败；若不再失败，说明前提变了')
  assert.deepEqual(triggerNames(db), [], '截断的那次不应建出任何触发器')
})

/** 只取笔记 FTS 那三个触发器：SCHEMA_SQL 里还有别的模块的触发器，
 *  它们引用本测试最小库里不存在的表（local_assets 等），会掩盖真正的结论。 */
function notesTriggers() {
  return splitSqlStatements(SCHEMA_SQL)
    .map(normalizeTriggerForPluginExecute)
    .filter((s) => /^CREATE\s+TRIGGER[^;]*local_notes_a[iud]\b/i.test(s))
}

test('BUG-AH：归一化后，三个笔记 FTS 触发器都能真正建出来', () => {
  const db = makeDb()
  const triggers = notesTriggers()

  assert.equal(triggers.length, 3, `应切出 3 个笔记 FTS 触发器，实际 ${triggers.length}`)
  for (const t of triggers) {
    // 归一化后：体内不能再出现 «分号 + LF»
    assert.ok(!/;[ \t]*\r?\n/.test(t), `仍存在 «分号+换行»，插件会截断：${t.slice(0, 60)}`)
    // 触发体整体保留
    assert.match(t, /BEGIN/)
    assert.match(t, /END;?$/)
    // 真 SQLite 能编译
    db.exec(t)
  }
  const names = triggerNames(db)
  for (const want of ['local_notes_ai', 'local_notes_ad', 'local_notes_au']) {
    assert.ok(names.includes(want), `缺少触发器 ${want}，实际：${names.join(',')}`)
  }
})

test('BUG-AH：归一化后触发器**真的生效**（插入/更新/删除都同步索引）', () => {
  // 只验「建出来了」不够 —— 建出来但不生效的触发器同样是坏的。
  //
  // ⚠️ 但这条**判别力有限**，说清楚免得日后被当成证伪过的判据：
  // node:sqlite 是真 SQLite，它本来就接受 «分号 + 换行» 的触发体。
  // 截断是 **Capacitor 插件**的毛病，不是 SQLite 的。所以把归一化去掉后
  // 这条照样全绿（实测）。真正判别 BUG-AH 的是第 2 条（断言归一化后的语句里
  // 不再有 «;»+LF）以及真机上的 check-fts-triggers-device.mjs。
  const db = makeDb()
  for (const t of notesTriggers()) db.exec(t)

  db.exec(`INSERT INTO local_notes(rowid, title, content, search_text) VALUES (1, 'alpha', 'ALPHATOKEN', '')`)
  let rows = db.prepare(`SELECT count(*) AS c FROM local_notes_fts WHERE local_notes_fts MATCH 'ALPHATOKEN'`).all()
  assert.equal(rows[0].c, 1, '插入后应能搜到')

  // 更新：旧词必须消失，新词必须出现（靠 _au 触发器）
  db.exec(`UPDATE local_notes SET content = 'BETATOKEN' WHERE rowid = 1`)
  rows = db.prepare(`SELECT count(*) AS c FROM local_notes_fts WHERE local_notes_fts MATCH 'ALPHATOKEN'`).all()
  assert.equal(rows[0].c, 0, '更新后旧词应从索引消失（缺 _au 就会残留）')
  rows = db.prepare(`SELECT count(*) AS c FROM local_notes_fts WHERE local_notes_fts MATCH 'BETATOKEN'`).all()
  assert.equal(rows[0].c, 1, '更新后新词应可搜到')

  // 删除：整条必须从索引消失（靠 _ad 触发器）
  db.exec(`DELETE FROM local_notes WHERE rowid = 1`)
  rows = db.prepare(`SELECT count(*) AS c FROM local_notes_fts WHERE local_notes_fts MATCH 'BETATOKEN'`).all()
  assert.equal(rows[0].c, 0, '删除后索引里不应残留（缺 _ad 就会搜到已删笔记）')
})

test('非触发器语句不被归一化（避免误伤字符串字面量里的换行）', () => {
  const withNewlineInLiteral = "INSERT INTO t(a) VALUES ('line1\nline2');"
  assert.equal(normalizeTriggerForPluginExecute(withNewlineInLiteral), withNewlineInLiteral)
  const createTable = `CREATE TABLE x (\n  a TEXT\n);`
  assert.equal(normalizeTriggerForPluginExecute(createTable), createTable)
})
