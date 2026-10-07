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
