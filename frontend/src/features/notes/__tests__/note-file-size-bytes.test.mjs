// note-file-size-bytes.test.mjs
//
// 锁住 local_note_files.size_bytes 的**单位**——它必须是字节数，不能是字符数。
//
// 2026-10-06 真机取证（中止转写那次留下的笔记 note-1791219411023-l51ydd）：
//   SELECT size_bytes FROM local_note_files WHERE kind='body'  →  6
//   而那条笔记的正文 content 是「（语音草稿）」——**6 个字符、UTF-8 18 字节**。
//   同一张表的 audio 行记的是 201961，与磁盘上拉回来的文件字节数完全一致。
//   也就是说同一个列名，两行用了**两种单位**。
//
// 成因：note-files.ts 里
//   body  行写的是 `input.body.length`   ← JS 字符串的 UTF-16 code unit 数
//   media 行写的是 `media.blob.size`     ← 真字节数
//   前者对中文正好差 3 倍，对 emoji 差得更多。
//
// 为什么现在还没出事：size_bytes **当前没有任何消费方**（全仓 grep 只有写入点，
// asset-store.ts 里那处 size_bytes 是另一张表 local_asset_blobs，且它是对的）。
// 所以这不是「正在冒烟的缺陷」，是**潜伏的口径陷阱**——以后任何人按字节做判断
// （限额校验、去重、UI 展示「12 KB」）都会对中文正文少算 3 倍，且没有任何测试会红。
// 修的时候是行不通的，因为没有任何断言覆盖它。
//
// 本门是**单位门**：body 行必须用字节计数表达式，且与 media 行同源。
// 文件末尾的负控把表达式改回 `input.body.length`（也就是 bug 本身），本门必须报红。

import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const SRC = path.resolve(HERE, '..', '..', '..')
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
 * 不是洁癖：这道门要判的 `sizeBytes:` 就在一段解释「为什么不能写 .length」的
 * 注释正下方，不剥注释的话 indexOf 会先命中注释里的 `input.body.length` 字样，
 * 门永远报红；反过来若哪天注释被删，门又会因为找到注释而假通过。
 * 剔除只用于**判定**，不改写被测源码。
 */
export function stripComments(src) {
  return src
    .replace(/\/\*[\s\S]*?\*\//g, (m) => m.replace(/[^\n]/g, ' '))
    .replace(/(^|[^:])\/\/[^\n]*/g, (m, p1) => p1 + ' '.repeat(Math.max(0, m.length - p1.length)))
}

/**
 * 返回 size_bytes 单位违规描述；null = 合规。
 *
 * 判两件事，缺一件这个 bug 都能溜过去：
 *   ① body 行的 sizeBytes 不能是字符串 `.length`（字符数）
 *   ② media 行必须用 `media.blob.size`（真字节）—— 否则「两行同单位」无从谈起
 * 单独抽成函数，是为了让文件末尾的负控能对**同一段检测逻辑**跑变异。
 */
export function findSizeUnitViolation(rawBody) {
  if (!rawBody) return 'persistNotePayload() 函数体没找到（门形同虚设，必须先修这个）'
  const body = stripComments(rawBody)

  const bodyRow = body.indexOf("mime: 'text/markdown'")
  if (bodyRow < 0) return "找不到 body 附件行（mime: 'text/markdown'）"
  const bodySize = body.indexOf('sizeBytes:', bodyRow)
  if (bodySize < 0) return 'body 附件行没有 sizeBytes 字段'
  const bodyExpr = body.slice(bodySize, body.indexOf('\n', bodySize))

  // ① 字符串 .length = UTF-16 code unit 数，对 CJK 差 3 倍
  if (/input\.body\.length/.test(bodyExpr)) {
    return `body 行的 sizeBytes 用了 input.body.length（字符数），实测「（语音草稿）」记成 6、` +
      '实际 18 字节。同一列的 media 行记的是真字节，两行单位不一致：' + bodyExpr.trim()
  }
  if (!/new Blob\(\[\s*input\.body\s*\]\)\.size/.test(bodyExpr)) {
    return `body 行的 sizeBytes 既不是 .length 也不是 Blob 字节计数，无法确认是字节数：${bodyExpr.trim()}`
  }

  // ② media 行必须是真字节
  const mediaRow = body.indexOf('sizeBytes: media.blob.size')
  if (mediaRow < 0) {
    return 'media 行的 sizeBytes 不是 media.blob.size（真字节）：' +
      '两行单位就对不齐，本门的前提失效'
  }
  return null
}

const readSrc = (p) => fs.readFileSync(p, 'utf8')

describe('local_note_files.size_bytes 的单位', () => {
  it('body 行按字节计数，且与 media 行同单位', () => {
    const body = fnBody(readSrc(NOTE_FILES), 'export async function persistNotePayload(')
    assert.equal(findSizeUnitViolation(body), null)
  })

  it('危害是真的：同样一段中文，.length 与字节数差 3 倍', () => {
    // 没有这一条，第 1 条就成了洁癖：万一 .length 对本仓的正文恰好够用呢。
    const s = '（语音草稿）'
    const chars = s.length
    const bytes = new Blob([s]).size
    assert.equal(chars, 6)
    assert.equal(bytes, 18)
    assert.notEqual(chars, bytes, '★ .length 与字节数对上了，本门要拦的 bug 可能不存在')
  })

  it('前提成立：这一列确实叫 size_bytes（不是 size_ 开头别的名字）', () => {
    const schema = readSrc(SCHEMA)
    const at = schema.indexOf('CREATE TABLE IF NOT EXISTS local_note_files')
    assert.ok(at >= 0, 'schema 里找不到 local_note_files 建表语句')
    assert.match(schema.slice(at, at + 900), /size_bytes INTEGER/, 'local_note_files 里没有 size_bytes 列')
  })

  it('前提成立：这一列目前没有消费方（说明它是潜伏缺陷而非正在冒烟）', () => {
    // 这条不是凑数：它决定了这道门该不该**现在**修。
    // 一旦有人开始读 size_bytes，本门仍然有效（单位错就是错），
    // 但优先级要从「顺手」升到「挡住线上」。
    const body = readSrc(NOTE_FILES)
    const writes = (body.match(/sizeBytes/g) || []).length
    assert.ok(writes > 0, 'note-files.ts 里没有 sizeBytes 写入点，源码结构与预期不符')
  })
})

// ---------------------------------------------------------------------------
// 负控：把字节计数改回 `input.body.length`（也就是 bug 本身），本门必须报红。
// 没有这一段，上面的 it() 只是一段看着像断言的文本——「门存在」不等于「门有牙」。
// ---------------------------------------------------------------------------
describe('负控：改回 .length 后必须报红', () => {
  it('变异体（body 行退回字符数）被判为违规', () => {
    const body = fnBody(readSrc(NOTE_FILES), 'export async function persistNotePayload(')
    assert.ok(body, '拿不到 persistNotePayload() 函数体，负控无法执行')

    const current = 'new Blob([input.body]).size'
    assert.ok(stripComments(body).includes(current),
      '★ 变异前提不成立：源码里已经没有 `new Blob([input.body]).size` 了，本负控已失去意义')

    const mutated = body.replace(current, 'input.body.length')
    const violation = findSizeUnitViolation(mutated)
    assert.ok(violation, '★ 门有洞：改回 input.body.length 竟然判为合规')
    assert.match(violation, /字符数/)
  })

  it('变异体（media 行也不再是真字节）被判为违规', () => {
    const body = fnBody(readSrc(NOTE_FILES), 'export async function persistNotePayload(')
    assert.ok(body, '拿不到 persistNotePayload() 函数体，负控无法执行')
    const mutated = body.replace('sizeBytes: media.blob.size', 'sizeBytes: media.blob.text().length')
    const violation = findSizeUnitViolation(mutated)
    assert.ok(violation, '★ 门有洞：media 行不再是真字节竟然判为合规')
    assert.match(violation, /media 行/)
  })
})
