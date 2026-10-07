// note-persist-fk-order.test.mjs
//
// 锁住 createNote() 里的**写入顺序**，因为它由外键约束决定，不是风格问题。
//
// 2026-10-05 真机复现（Redmi 2411DRN47C / HyperOS / sttdev）：
//   录音 → 转写成功（后端出了「请提前。提前十分钟到场。今天下午三点，会议。」）
//   → 但 local_notes 恒为 0 行，页面一直显示「还没有笔记」。
// 抓到的真实报错是 `Run: FOREIGN KEY constraint failed (code 787)`。
//
// 成因链（三处都要对，缺一处这个 bug 就不成立）：
//   1. schema.ts:80   local_note_files.note_id REFERENCES local_notes(id)
//   2. note-files.ts  insertFileRow() 往 local_note_files 插**子行**
//   3. notes-persist  createNote() 原本**先**调 persistNotePayload()，
//      父行 local_notes 要到它之后才 INSERT —— 父行不存在时插子行必炸。
//
// 触发条件是 persistBodyFile || media.length > 0，也就是**每一条语音笔记**
// （必带 audioBlob）以及长文本笔记；纯短文本笔记走不到这段，所以之前一直没炸。
//
// 本门是**顺序门**：INSERT 必须早于 persistNotePayload，回填 UPDATE 必须晚于它。
// 文件末尾的负控证明这扇门在真代码上会红——否则它只是一段看着像断言的文本。

import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const SRC = path.resolve(HERE, '..', '..', '..')
const PERSIST = path.join(SRC, 'features', 'notes', 'notes-persist.ts')
const NOTE_FILES = path.join(SRC, 'features', 'notes', 'note-files.ts')
const SCHEMA = path.join(SRC, 'native', 'schema.ts')

/** 取 `function <name>(...) { … }` 的函数体（大括号配平），null = 没找到。 */
export function fnBody(src, decl) {
  const start = src.indexOf(decl)
  if (start < 0) return null
  const open = src.indexOf('{', start)
  if (open < 0) return null
  let depth = 0
  for (let i = open; i < src.length; i++) {
    if (src[i] === '{') depth++
    else if (src[i] === '}') {
      depth--
      if (depth === 0) return src.slice(open, i + 1)
    }
  }
  return null
}

/**
 * 去掉行注释与块注释。
 *
 * 这不是洁癖：2026-10-05 第一版这道门直接 `indexOf('persistNotePayload(')`，
 * 结果命中的是**解释成因的注释**（"persistNotePayload() 会先往 local_note_files
 * 插子行"）而不是调用点，于是门在修复后依然报红。同一个坑本会话已经踩过一次：
 * 数某文件的 v1 FROM/JOIN 时没剔注释，数出 10 而实际是 5。
 *
 * 剔除只用于**判定**，不改写被测源码。
 */
export function stripComments(src) {
  return src
    .replace(/\/\*[\s\S]*?\*\//g, (m) => m.replace(/[^\n]/g, ' '))
    .replace(/(^|[^:])\/\/[^\n]*/g, (m, p1) => p1 + ' '.repeat(Math.max(0, m.length - p1.length)))
}

/**
 * 返回 createNote() 的外键顺序违规描述；null = 合规。
 *
 * 单独抽成函数而不是写成一个大 it()，是为了让文件末尾的负控能对**同一段
 * 检测逻辑**跑变异——否则「门会红」这件事就没有被真正验证过。
 */
export function findFkOrderViolation(rawBody) {
  if (!rawBody) return 'createNote() 函数体没找到（门形同虚设，必须先修这个）'
  const body = stripComments(rawBody)
  const insert = body.indexOf('INSERT INTO local_notes')
  const persist = body.indexOf('persistNotePayload(')
  if (insert < 0) return 'createNote() 里没有 INSERT INTO local_notes'
  if (persist < 0) return 'createNote() 里没有调用 persistNotePayload('
  if (insert > persist) {
    return `local_notes 父行插入(${insert}) 晚于 persistNotePayload(${persist})：` +
      '写 local_note_files 子行时父行还不存在，SQLite 报 787 FOREIGN KEY constraint failed'
  }
  const backfill = body.indexOf('UPDATE local_notes', persist)
  if (backfill < 0) {
    return 'persistNotePayload() 之后没有 UPDATE local_notes 回填 body_path/media_json/audio_path：' +
      '附件路径会永远停在 NULL，读回来时 loadFullContent() 只能退回正文'
  }
  return null
}

const readSrc = (p) => fs.readFileSync(p, 'utf8')

describe('createNote 写入顺序（外键约束）', () => {
  it('父行 INSERT 早于 persistNotePayload，且其后有回填 UPDATE', () => {
    const body = fnBody(readSrc(PERSIST), 'export async function createNote(')
    assert.equal(findFkOrderViolation(body), null)
  })

  it('前提成立：子表确实外键指向 local_notes', () => {
    const schema = readSrc(SCHEMA)
    const filesTable = schema.slice(
      schema.indexOf('CREATE TABLE IF NOT EXISTS local_note_files'),
      schema.indexOf('CREATE TABLE IF NOT EXISTS local_note_files') + 900,
    )
    assert.match(filesTable, /FOREIGN KEY \(note_id\) REFERENCES local_notes\(id\)/,
      'schema 若改成不引用 local_notes，本门的前提失效，应重新评估而不是继续跑')
  })

  it('前提成立：insertFileRow 写的确实是子表 local_note_files', () => {
    const body = fnBody(readSrc(NOTE_FILES), 'async function insertFileRow(')
    assert.ok(body, 'insertFileRow() 没找到')
    assert.match(body, /INSERT OR REPLACE INTO local_note_files/)
  })
})

// ---------------------------------------------------------------------------
// 负控：把顺序改回「先写附件、后插父行」（也就是 bug 本身），本门必须报红。
// 没有这一段，上面的 it() 只是一段看着像断言的文本——「门存在」不等于「门有牙」。
// ---------------------------------------------------------------------------
describe('负控：顺序改回 bug 版本后必须报红', () => {
  const body = fnBody(readSrc(PERSIST), 'export async function createNote(')

  it('变异体（附件先于父行）被判为违规', () => {
    assert.ok(body, '拿不到 createNote() 函数体，负控无法执行')
    const blockStart = body.indexOf('if (decided.persistBodyFile || media.length > 0) {')
    const insertStart = body.indexOf('await localDB.run(\n    `INSERT INTO local_notes')
    assert.ok(blockStart >= 0, '定位失败：找不到 persistNotePayload 块，源码结构与预期不符')
    assert.ok(insertStart >= 0, '定位失败：找不到 INSERT INTO local_notes，源码结构与预期不符')
    assert.ok(insertStart < blockStart,
      '★ 变异前提不成立：源码里 INSERT 已经不在 persistNotePayload 之前了，本负控已失去意义')

    // 用括号配对取整块，避免靠缩进猜边界
    const open = body.indexOf('{', blockStart)
    let depth = 0
    let blockEnd = -1
    for (let i = open; i < body.length; i++) {
      if (body[i] === '{') depth++
      else if (body[i] === '}') {
        depth--
        if (depth === 0) { blockEnd = i + 1; break }
      }
    }
    assert.ok(blockEnd > 0, '定位失败：persistNotePayload 块括号不配对')

    const block = body.slice(blockStart, blockEnd)
    let mutated = body.slice(0, blockStart) + body.slice(blockEnd)
    const at = mutated.indexOf('await localDB.run(\n    `INSERT INTO local_notes')
    mutated = mutated.slice(0, at) + block + '\n\n  ' + mutated.slice(at)

    const violation = findFkOrderViolation(mutated)
    assert.ok(violation, '★ 门有洞：把顺序改回 bug 版本竟然判为合规')
    assert.match(violation, /FOREIGN KEY constraint failed/)
  })

  it('变异体（删掉回填 UPDATE）被判为违规', () => {
    assert.ok(body, '拿不到 createNote() 函数体，负控无法执行')
    // 注意：不能替换成 `UPDATE local_notes_DISABLED`——那仍然以
    // `UPDATE local_notes` 开头，indexOf 照样命中，门测不到东西。
    const mutated = body.replace('UPDATE local_notes', 'UPDATE_XX')
    const violation = findFkOrderViolation(mutated)
    assert.ok(violation, '★ 门有洞：回填 UPDATE 没了竟然判为合规')
    assert.match(violation, /回填/)
  })
})
