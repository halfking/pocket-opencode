// pending-brief-index-coverage.test.mjs — §574：把决策简报的「一页清单」变成**可核的断言**。
//
// ── 这道门为什么存在 ──────────────────────────────────────────────────
//
// `2026-10-11-pending-decisions-brief.md` 开头那页「今天能动什么」清单，
// 自己写着「⚠ **计数口径**（可核）：本页编号覆盖 #4–#51 共 48 项，无遗漏、无重复登记」。
//
// ⛔ 实测（2026-10-14，§574）这句话**三处都不成立**：
//   ① **#4 有正文条目，却不在一页清单里** —— 清单声称覆盖 #4，实际最小编号是 #5；
//   ② **#31 / #32 / #33 在清单里有行，正文却没有任何条目** —— 三个「待拍板项」只有一行摘要；
//   ③ **D 组表头写「（20 件）」，该表实际有 25 行**。
//
// ⭐ 三处都是同一族病：**「有几条」「覆盖到哪」从来没被写成断言，只被当作共识传下去**。
//   ⇒ 门只能守「被写成断言的东西」；没写成断言的计数/范围，一代一代错下去，
//   而且**每传一次都显得更可信**（有编号、有小节标题、语气笃定）。
//   ⇒ 可执行的修法只有一个：**把它变成断言**，让下一次数错的人当场看见那个数。
//
// ── 三条判据（对称 + 各自带负控）────────────────────────────────────
//
//   ① 正文有条目的编号 **⊆** 一页清单（漏登记 → 红）
//   ② 一页清单的编号 **⊆** 正文条目 ∪ `NO_BODY_ENTRY`（幽灵条目 → 红）
//   ③ 每个分组表头声明的件数 **==** 该表实际行数
//
// ⚠ ② 特意留了一个**有界**的 `NO_BODY_ENTRY` 白名单：承认「有些项确实只有一行摘要」
//   是现实，但要求它**逐项写明理由**，并且**白名单本身被断言不许超过 5 条**
//   —— 否则它会变成垃圾桶，把缺口重新藏起来。

import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const BRIEF = path.resolve(HERE, '..', '..', '..', '..', 'docs', 'design', '2026-10-11-pending-decisions-brief.md')
const raw = readFileSync(BRIEF, 'utf8')

/**
 * 只有一行摘要、正文没有条目的编号。**必须逐条写理由**，且不许超过 5 条。
 * ⚠ 这不是「允许漏」——是把一个不可见的缺口变成可见且有上限的欠账。
 */
const NO_BODY_ENTRY = {
  '14b': '§574 实测：它是 #14 的子项，**没有独立 ### 标题**；编号独立登记是为了能被单独检索到',
  '30': '§574 实测：它是 #36 正文里的子项（标题写作「#36 … #30 ASR 热词表…」），**没有独立 ### 标题**',
  '31': '§574 实测：只有 C 组一行摘要 + §484.4 指针，正文未开条目；「空文本算不算失败」需先补读数',
  '32': '§574 实测：只有 C 组一行摘要，正文未开条目；这是产品方向（见 §542 的对外账）',
  '33': '§574 实测：只有 C 组一行摘要 + §491–§493 指针，正文未开条目；剩余动作待写',
}

/** 一页清单的边界：从第一张分组表到 `## 〇` 速览之前。 */
const LIST_START = raw.indexOf('### A. ')
const LIST_END = raw.indexOf('## 〇、本轮')
const listRegion = LIST_START >= 0 && LIST_END > LIST_START ? raw.slice(LIST_START, LIST_END) : ''

/** 正文条目：任意层级的标题里**第一个** `#N`（`#36` 的标题里提到 `#30` 不算另开条目）。 */
const bodyIds = new Set()
for (const line of raw.split('\n')) {
  const m = /^#{2,4} .*?(#\d+[a-z]?)/.exec(line)
  if (m) bodyIds.add(m[1].slice(1))
}

/**
 * 一页清单里**表格行**上出现的编号。
 *
 * ⚠⚠ 匹配面必须与被检面**同粒度**：只扫「表格行」，不扫整个区域。
 *   第一版扫的是整个 `listRegion`，结果连**说明散文**里的 `**#4** 有正文条目却不在本页`
 *   都被算成「已登记」⇒ 判据① **恒绿**（一个看起来完全正常的门）。
 *   「登记进一页清单」的语义是「**有一行表格**」，散文里的提及不算数。
 */
const listRows = listRegion.split('\n').filter((l) => /^\| (?!\s*--)/.test(l))
const listIds = new Set()
for (const row of listRows) {
  for (const m of row.matchAll(/\*\*#(\d+[a-z]?(?: B 格)?)\*\*/g)) {
    listIds.add(m[1].replace(' B 格', ''))
  }
}

/**
 * 分组表：表头声明的件数 vs 该表实际行数。
 *
 * ⚠ 行计数必须**按表的实际形态**数，不能只认 `| **#`：
 *   A 组的行是 `| **A2** |` 与 `| ~~**A1**~~ ✅ … |`（外部输入，不是编号项），
 *   另有交叉引用行 `| **#30 词源** / **#31** / … |`。
 *   ⛔ 只数 `| **#` 会让 A 组实测 0 行、让 D 组少算一行 —— 两个都是**量具的错**，
 *      不是文档的错。（§574 第一版就是这么错的，当场被量具自证抓住。）
 */
const TABLE_ROW = /^\| (?!\s*--)(?!\s*#\s*\|)/gm

function groupTables(src) {
  const out = []
  const re = /^### ([A-D])\. .*?（(\d+) 件）\s*$/gm
  let m
  while ((m = re.exec(src)) !== null) {
    const start = m.index + m[0].length
    const rest = src.slice(start)
    const stop = rest.search(/^### |^---$/m)
    const body = stop < 0 ? rest : rest.slice(0, stop)
    out.push({ id: m[1], declared: Number(m[2]), rows: (body.match(TABLE_ROW) || []).length })
  }
  return out
}

const tables = groupTables(listRegion)

describe('§574 · 决策简报的一页清单必须自己站得住', () => {
  it('量具自证：边界与集合都抓到了东西', () => {
    assert.ok(listRegion.length > 500, `一页清单区域没抓到（长度 ${listRegion.length}）⇒ 量具失效，先修抓取`)
    assert.ok(bodyIds.size >= 40, `正文只抓到 ${bodyIds.size} 个条目 ⇒ 量具失效`)
    assert.ok(listIds.size >= 40, `清单只抓到 ${listIds.size} 个编号 ⇒ 量具失效`)
    assert.ok(tables.length >= 3, `只抓到 ${tables.length} 张分组表 ⇒ 量具失效`)
  })

  it('★ 判据①：正文有独立条目的编号，必须都登记进一页清单', () => {
    const missing = [...bodyIds].filter((id) => !listIds.has(id)).sort()
    assert.deepEqual(missing, [],
      `这些编号正文里开了一整条，却没进一页清单：${missing.join(' / ')} —— ` +
      `清单开头那句「本页编号覆盖 #N 起」就是假的`)
  })

  it('★ 判据②：一页清单不得出现「正文根本没有」的幽灵条目', () => {
    const ghosts = [...listIds].filter((id) => !bodyIds.has(id) && !(id in NO_BODY_ENTRY)).sort()
    assert.deepEqual(ghosts, [],
      `这些编号只有清单行、没有正文条目：${ghosts.join(' / ')} —— ` +
      `要么补正文条目，要么把它登记进 NO_BODY_ENTRY 并写明理由`)
  })

  it('★ 判据③：每张分组表声明的件数必须等于它的实际行数', () => {
    for (const t of tables) {
      assert.equal(t.rows, t.declared,
        `${t.id} 组表头写「（${t.declared} 件）」，实测 ${t.rows} 行`)
    }
  })

  it('★ 白名单必须逐项写理由，且**不许超过 5 条**', () => {
    const n = Object.keys(NO_BODY_ENTRY).length
    assert.ok(n <= 5, `NO_BODY_ENTRY 有 ${n} 条（上限 5）⇒ 它开始变成垃圾桶了`)
    for (const [id, why] of Object.entries(NO_BODY_ENTRY)) {
      assert.ok(why && why.trim().length >= 10, `#${id} 没写理由（或太短）`)
      assert.ok(listIds.has(id), `白名单里的 #${id} 根本不在一页清单里 ⇒ 该从白名单删掉`)
    }
  })

  // ── 负控：三条判据必须真的会红，否则上面三条是装饰 ──────────────────
  it('★★ 负控①：把 #4 从清单里删掉（还原 §574 查出的缺口）⇒ 判据① 必须红', () => {
    const mutated = raw.replace('| **#4** |', '| **#4x** |')
    assert.notEqual(mutated, raw, '负控样本没插进去（#4 的清单行没了？）')
    const mutatedRows = mutated
      .slice(mutated.indexOf('### A. '), mutated.indexOf('## 〇、本轮'))
      .split('\n').filter((l) => /^\| (?!\s*--)/.test(l))
    const mutatedList = new Set(
      mutatedRows.flatMap((row) => [...row.matchAll(/\*\*#(\d+[a-z]?(?: B 格)?)\*\*/g)].map((m) => m[1].replace(' B 格', ''))),
    )
    assert.equal(mutatedList.has('4'), false, '负控前提失败：#4 仍能从清单行里解析出来')
    // 判据① 在同一套口径下必须报出 #4
    const missing = [...bodyIds].filter((id) => !mutatedList.has(id))
    assert.ok(missing.includes('4'), `负控失效：判据① 没把 #4 报出来（实报 ${missing.join('/')}）`)
  })

  it('★★ 负控②：往清单里塞一个正文没有的编号 ⇒ 判据② 必须红', () => {
    const ghost = '999'
    const mutatedList = new Set([...listIds, ghost])
    const ghosts = [...mutatedList].filter((id) => !bodyIds.has(id) && !(id in NO_BODY_ENTRY))
    assert.ok(ghosts.includes(ghost), '负控失效：幽灵编号没被抓出来 ⇒ 判据② 是恒真的')
  })

  it('★★ 负控④：散文里的 `#N` 提及**不算**登记（匹配面必须同粒度）', () => {
    // §574 量具第一版扫整个区域 ⇒ 说明文字里的 `**#N**` 被当成已登记，
    // 判据① 恒绿。这里把 #5 的**清单行**删掉、但在散文里留一个 `**#5**` 提及，
    // 判据① 仍必须把它报成「漏登记」。
    const mutated = raw
      .replace(/^\| \*\*#5\*\* \|.*$/m, '')
      .replace('⚠ 若日后', '**#5** 若日后')
    assert.notEqual(mutated, raw, '负控样本没插进去')
    const mutatedRows = mutated
      .slice(mutated.indexOf('### A. '), mutated.indexOf('## 〇、本轮'))
      .split('\n').filter((l) => /^\| (?!\s*--)/.test(l))
    const mutatedList = new Set(
      mutatedRows.flatMap((row) => [...row.matchAll(/\*\*#(\d+[a-z]?(?: B 格)?)\*\*/g)].map((m) => m[1].replace(' B 格', ''))),
    )
    assert.equal(mutatedList.has('5'), false, '负控前提失败：#5 的清单行没能删掉')
    const missing = [...bodyIds].filter((id) => !mutatedList.has(id))
    assert.ok(missing.includes('5'),
      `负控失效：散文里的 **#5** 提及被当成了登记（判据① 实报 ${missing.join('/')}）⇒ 匹配面与被检面不同粒度`)
  })

  it('★★ 负控③：把 D 组表头的件数改错 ⇒ 判据③ 必须红', () => {
    const target = tables.find((t) => t.id === 'D') ?? tables[0]
    const mutated = listRegion.replace(
      new RegExp(`(### ${target.id}\\. .*?)（${target.declared} 件）`),
      `$1（${target.declared + 1} 件）`,
    )
    assert.notEqual(mutated, listRegion, '负控样本没插进去')
    const after = groupTables(mutated).find((t) => t.id === target.id)
    assert.ok(after, `负控失效：改完抓不到 ${target.id} 组`)
    assert.notEqual(after.rows, after.declared,
      '负控失效：表头件数改了仍判绿 ⇒ 判据③ 根本没在比这两者')
  })
})