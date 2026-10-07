// note-media-append-not-replace.test.mjs
//
// 锁住「加附件是追加不是替换」这条产品语义。
//
// ── 产品决策（2026-10-06，用户拍板）────────────────────────────────────────
//   跨类型共存：给一条语音笔记补一张图/一个视频，已有录音必须原样还在。
//   同类型替换：一条笔记只该有一条音轨；连加两个视频则后者顶掉前者。
//
// ── 改动前（缺陷6 的语义层，与数据安全层同源）────────────────────────────
//   updateNote 里 `const replacingMedia = media.length > 0`，
//   命中后走「整体替换」：deleteNoteFiles 整目录 rmdir + audio_path 置空。
//   ⇒ 用户只是想给语音笔记补张图，录音就没了。UI 上没有任何提示，
//   因为这是代码当成「替换」语义写出来的，不是异常路径。
//
// ── 为什么这两条语义在磁盘层成立（前提，本门也一并锁住）───────────────────
//   note-paths.ts:24-27 按 kind 分目录：audio/01.webm、videos/01.mp4、
//   images/01.jpg、files/01.bin ⇒ 跨类型不撞名，可以共存。
//   note-files.ts:97 计数器从 0 起、每次 +1 ⇒ 第一次是 01；
//   两次 updateNote 都从 01 起 ⇒ 同类型落到**同一个 rel_path**，
//   且 row id `${noteId}-${kind}-${index}` 相同；
//   schema.ts:71 `id TEXT PRIMARY KEY` ⇒ 必须靠 `INSERT OR REPLACE` 顶掉旧行。
//   前提任一不成立，同类型替换就会变成「越加越多」或「第二次写直接 UNIQUE 报错」。
//
// 末尾四条负控：丢掉跨类型合回、丢掉同类型过滤、把两类 kind 塞进同一目录、
// 把 carriedMedia 重新圈回条件里 —— 每一条门都必须报红。

import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const SRC = path.resolve(HERE, '..', '..', '..')
const PERSIST = path.join(SRC, 'features', 'notes', 'notes-persist.ts')
const NOTE_FILES = path.join(SRC, 'features', 'notes', 'note-files.ts')
const NOTE_PATHS = path.join(SRC, 'features', 'notes', 'note-paths.ts')
const SCHEMA = path.join(SRC, 'native', 'schema.ts')

/** 取函数体（大括号配平，跳过形参表与字符串），null = 没找到。 */
export function fnBody(src, decl) {
  const start = src.indexOf(decl)
  if (start < 0) return null
  let i = start + decl.length
  let paren = 1
  let closed = false
  for (; i < src.length; i++) {
    const c = src[i]
    if (c === "'" || c === '"' || c === '`') {
      const quote = c
      for (i++; i < src.length && src[i] !== quote; i++) if (src[i] === '\\') i++
      continue
    }
    if (c === '(') paren++
    else if (c === ')') { paren--; if (paren === 0) { closed = true; i++; break } }
  }
  if (!closed) return null
  const open = src.indexOf('{', i)
  if (open < 0) return null
  let depth = 0
  for (let k = open; k < src.length; k++) {
    if (src[k] === '{') depth++
    else if (src[k] === '}') {
      depth--
      if (depth === 0) return src.slice(open, k + 1)
    }
  }
  return null
}

/** 去注释，保留长度与换行 —— 本门要按字符下标判「声明处在哪一层」。 */
export function stripComments(src) {
  return src
    .replace(/\/\*[\s\S]*?\*\//g, (m) => m.replace(/[^\n]/g, ' '))
    .replace(/(^|[^:])\/\/[^\n]*/g, (m, p1) => p1 + ' '.repeat(Math.max(0, m.length - p1.length)))
}

/**
 * 在 `src` 的第 `idx` 个字符处，返回它所在的花括号嵌套深度
 * （函数体自身的 `{` 记作 1）。跳过字符串字面量。
 *
 * 用来判「carriedMedia 的声明在不在函数体顶层」：一旦有人把它重新塞进
 * `if (条件) { ... }`，深度就变成 2，声明也随之变成条件性合并 ——
 * 也就是把「追加」悄悄改回「只在带媒体时替换」。纯文本搜索判不出来，
 * 因为两种写法的 token 完全一样。
 */
export function braceDepthAt(src, idx) {
  let depth = 0
  for (let i = 0; i < idx && i < src.length; i++) {
    const c = src[i]
    if (c === "'" || c === '"' || c === '`') {
      const quote = c
      for (i++; i < idx && src[i] !== quote; i++) if (src[i] === '\\') i++
      continue
    }
    if (c === '{') depth++
    else if (c === '}') depth--
  }
  return depth
}

/**
 * 返回「加附件被当成替换 / 跨类型媒体被丢弃」的风险清单；空数组 = 安全。
 *
 * `insertFileRowBody` 与 `persistNotePayloadBody` 分开传：INSERT 语句在
 * `insertFileRow()` 里，不在 persistNotePayload 的函数体内。第一版只传了
 * persistNotePayload 的函数体，于是「用了 INSERT 而不是 INSERT OR REPLACE」
 * 这条判据在自己的正确源码上报红 —— **量具作用域错，不是源码有缺陷**。
 * 同类错本会话已踩多次（fnBody 跳过形参表、行扫描剥注释），凡按「函数体」
 * 取证据的判据都要先确认被查文本真在那个函数体里。
 */
export function findAppendRisks(updateNoteBody, notePathsSrc, noteFilesBody, insertFileRowBody) {
  const risks = []
  const u = stripComments(updateNoteBody || '')
  const p = stripComments(notePathsSrc || '')
  const f = stripComments(noteFilesBody || '')
  const ins = stripComments(insertFileRowBody || '')

  if (!u) return ['updateNote() 函数体没找到（门形同虚设）']
  if (!p) return ['note-paths.ts 没读到（门形同虚设）']
  if (!f) return ['persistNotePayload() 函数体没找到（门形同虚设）']
  if (!ins) return ['insertFileRow() 函数体没找到（门形同虚设）']

  // ── 1) 旧媒体清单要无条件解析出来 ──────────────────────────────────────────
  const declAt = u.search(/\bconst carriedMedia\b/)
  if (declAt < 0) {
    risks.push('updateNote 没有 carriedMedia —— 旧 mediaJson 里的非 body 条目会被本次写入抹掉')
  } else {
    // 声明必须在函数体顶层（深度 1）。被 if/for 圈住 = 条件性合并 = 替换语义。
    const depth = braceDepthAt(u, declAt)
    if (depth !== 1) {
      risks.push(`carriedMedia 的声明处在深度 ${depth}（应为函数体顶层 1）—— 旧媒体合并被条件圈住了`)
    }
    // 解析时必须排除 body：body.md 由 persistBodyFile 单独管，
    // 混进来会出现两条 body 记录。
    if (!/f\.kind\s*!==\s*'body'/.test(u)) {
      risks.push('carriedMedia 没有排除 kind=body —— 正文会在 mediaJson 里出现两条')
    }
    if (!/JSON\.parse\(existing\.mediaJson\)/.test(u)) {
      risks.push('carriedMedia 没有解析 existing.mediaJson —— 旧媒体清单读不到')
    }
  }

  // ── 2) 同类型以本次为准，跨类型合回 ────────────────────────────────────────
  if (!/\bwrittenKinds\b/.test(u)) {
    risks.push('updateNote 没有 writtenKinds —— 同类型媒体无法「后者顶掉前者」，会越加越多')
  }
  const keptAt = u.search(/\bconst kept\b/)
  if (keptAt < 0) {
    risks.push('updateNote 没有 kept —— 本次写的 files 会直接覆盖旧 mediaJson')
  } else {
    const line = u.slice(keptAt, u.indexOf('\n', keptAt) < 0 ? keptAt + 400 : u.indexOf('\n', keptAt))
    if (!/\[\s*\.\.\.written\.files\s*,/.test(line)) {
      risks.push('kept 不是以本次写的 written.files 开头 —— 同类型替换的基准丢了')
    }
    if (!/carriedMedia\.filter\(/.test(line)) {
      risks.push('kept 里没有 carriedMedia.filter(...) —— 跨类型旧媒体（录音）会被丢弃')
    }
    if (!/writtenKinds\.has\(/.test(u)) {
      risks.push('kept 没用 writtenKinds 做同类型过滤 —— 连加两个视频会并存两条')
    }
  }

  // ── 3) 正文是否落文件要单独判定，不能由「有没有带媒体」决定 ────────────────
  if (!/\bconst bodyFileNeeded\b/.test(u)) {
    risks.push('updateNote 没有 bodyFileNeeded 判定 —— 正文落不落文件与媒体混在一起判断')
  } else if (!/persistBodyFile:\s*bodyFileNeeded\b/.test(u)) {
    risks.push('persistNotePayload 收到的不是 bodyFileNeeded —— 正文文件会被无谓重写或误删')
  }

  // ── 前提 A：不同 kind 落在不同目录，否则「跨类型共存」在盘上就是覆盖 ────────
  const folderAt = p.search(/\bconst folder\s*=/)
  if (folderAt < 0) {
    risks.push('note-paths.ts 里的 folder 映射没找到（跨类型共存的前提没了）')
  } else {
    const line = p.slice(folderAt, p.indexOf('\n', folderAt))
    const folders = new Set((line.match(/'([a-z]+)'/g) || []).map((s) => s.replace(/'/g, '')))
    for (const need of ['audio', 'videos', 'images', 'files']) {
      if (!folders.has(need)) risks.push(`note-paths.ts 的 folder 映射里没有 ${need} —— 该类型会写进别人的目录`)
    }
    if (folders.size < 4) risks.push(`folder 映射只有 ${folders.size} 个不同目录（应 4 个）：跨类型附件会互相覆盖`)
  }

  // ── 前提 B：同类型跨次写入落到同一 rel_path + 同一 row id，再靠 REPLACE 顶掉 ──
  if (!/counters:\s*Record<string,\s*number>\s*=\s*\{[^}]*audio:\s*0/.test(f)) {
    risks.push('persistNotePayload 的计数器没从 0 起 —— 第一个附件不是 01')
  }
  if (!/counters\[media\.kind\]\s*=\s*\(counters\[media\.kind\]\s*\|\|\s*0\)\s*\+\s*1/.test(f)) {
    risks.push('persistNotePayload 的计数器没有 +1 递增 —— 同类型第二次会写到 00/新文件而不是覆盖')
  }
  if (!/INSERT OR REPLACE INTO local_note_files/.test(ins)) {
    risks.push('insertFileRow 用的是 INSERT 而不是 INSERT OR REPLACE —— 同类型第二次会撞主键')
  }
  if (!/\$\{input\.noteId\}-\$\{media\.kind\}-\$\{counters\[media\.kind\]\}/.test(f)) {
    risks.push('子行 id 不含 kind 与序号 —— 同类型无法定位到同一条行，替换会变成新增')
  }

  return risks
}

const read = (p) => fs.readFileSync(p, 'utf8')
const persistSrc = read(PERSIST)
const pathsSrc = read(NOTE_PATHS)
const filesSrc = read(NOTE_FILES)
const updateNoteBody = fnBody(persistSrc, 'export async function updateNote(')
const persistPayloadBody = fnBody(filesSrc, 'export async function persistNotePayload(')
// INSERT 语句在 insertFileRow() 里，不在 persistNotePayload() 里。
const insertFileRowBody = fnBody(filesSrc, 'async function insertFileRow(')

describe('加附件是追加不是替换', () => {
  it('updateNote / note-paths / persistNotePayload 无替换语义风险', () => {
    assert.deepEqual(findAppendRisks(updateNoteBody, pathsSrc, persistPayloadBody, insertFileRowBody), [])
  })

  it('前提成立：local_note_files.id 是主键（同类型替换靠 INSERT OR REPLACE）', () => {
    const schema = stripComments(read(SCHEMA))
    const at = schema.indexOf('CREATE TABLE IF NOT EXISTS local_note_files')
    assert.ok(at >= 0, 'local_note_files 建表语句没找到')
    const tail = schema.slice(at, at + 500)
    assert.match(tail, /id\s+TEXT\s+PRIMARY\s+KEY/, 'id 不是主键 ⇒ 同类型替换的判据前提失效')
  })
})

describe('负控：把追加改回替换必须报红', () => {
  it('负控1：kept 里丢掉跨类型合回 → 报红（改回替换语义）', () => {
    assert.ok(updateNoteBody, '拿不到 updateNote() 函数体')
    const mutated = updateNoteBody.replace(
      /carriedMedia\.filter\(\(f\) => !writtenKinds\.has\(f\.kind\)\)/,
      '[]',
    )
    assert.notEqual(mutated, updateNoteBody, '变异没生效：kept 构造与预期不符，先看源码再改门')
    const risks = findAppendRisks(mutated, pathsSrc, persistPayloadBody, insertFileRowBody)
    assert.ok(risks.length, '★ 门有洞：跨类型合回被删掉竟然判为安全')
    assert.ok(
      risks.some((r) => /carriedMedia\.filter|会被丢弃/.test(r)),
      `门红了但红得不对，要的是「跨类型合回」那条，实际：${JSON.stringify(risks)}`,
    )
  })

  it('负控2：去掉同类型过滤（直接 concat）→ 报红', () => {
    assert.ok(updateNoteBody, '拿不到 updateNote() 函数体')
    const mutated = updateNoteBody
      .replace(/\[\s*\.\.\.written\.files\s*,\s*\.\.\.carriedMedia\.filter\([^)]*\)\s*\.\.\.\]/, '')
      .replace(/carriedMedia\.filter\(\(f\) => !writtenKinds\.has\(f\.kind\)\)/, 'carriedMedia')
    assert.notEqual(mutated, updateNoteBody, '变异没生效：kept 构造与预期不符')
    const risks = findAppendRisks(mutated, pathsSrc, persistPayloadBody, insertFileRowBody)
    assert.ok(risks.length, '★ 门有洞：同类型不过滤竟然判为安全（连加两个视频会并存两条）')
    assert.ok(risks.some((r) => /writtenKinds|同类型/.test(r)))
  })

  it('负控3：把两类 kind 塞进同一目录 → 报红（跨类型共存的前提被破坏）', () => {
    const mutated = pathsSrc.replace("kind === 'video' ? 'videos'", "kind === 'video' ? 'audio'")
    assert.notEqual(mutated, pathsSrc, '变异没生效：folder 映射与预期不符')
    const risks = findAppendRisks(updateNoteBody, mutated, persistPayloadBody, insertFileRowBody)
    assert.ok(risks.length, '★ 门有洞：视频与录音共用目录竟然判为安全')
    assert.ok(risks.some((r) => /folder 映射|写进别人的目录/.test(r)))
  })

  it('负控4：把 carriedMedia 重新圈回条件里 → 报红', () => {
    assert.ok(updateNoteBody, '拿不到 updateNote() 函数体')
    // 只在声明前加一个 if 开头。braceDepthAt 在声明位置即定格，
    // 不需要配平右括号就能命中「声明不再在顶层」这条判据。
    const mutated = updateNoteBody.replace(
      /const carriedMedia = \(\(\) => \{/,
      "if (media.length > 0) { const carriedMedia = (() => {",
    )
    assert.notEqual(mutated, updateNoteBody, '变异没生效：carriedMedia 声明与预期不符')
    const risks = findAppendRisks(mutated, pathsSrc, persistPayloadBody, insertFileRowBody)
    assert.ok(risks.length, '★ 门有洞：条件性合并竟然判为安全')
    assert.ok(
      risks.some((r) => /深度/.test(r)),
      `门红了但红得不对，要的是「声明深度」那条，实际：${JSON.stringify(risks)}`,
    )
  })
})