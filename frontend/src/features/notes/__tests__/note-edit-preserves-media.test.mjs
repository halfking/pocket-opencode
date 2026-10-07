// note-edit-preserves-media.test.mjs
//
// 锁住「updateNote 不得让已有媒体消失」这条数据安全约束。
//
// 本门只管**破坏性操作与引用完整性**：删什么、何时删、audio_path 会不会被清空。
// 「加附件是追加不是替换」这套语义由姊妹门 note-media-append-not-replace 管，
// 两门共用同一段 updateNote 源码但判据不重叠，避免一处缺陷被两边同时放过。
//
// ── 缺陷2（2026-10-06 真机复现，Redmi 2411DRN47C / HyperOS / sttdev）────────────
//   在笔记详情页点「编辑」→ 改一个字 → 保存
//     audio_path        非空 → null
//     audio_duration_ms 13968 → 0
//     磁盘              audio/01.webm (199192B) 连同整个笔记目录一起消失
//     UI                零提示
//   成因：updateNote 在「正文变了」时调 deleteNoteFiles —— 它对整个笔记目录
//   `rmdir recursive`。修法：换 deleteNoteBodyFile（只删 body.md + 只删 kind='body' 子行）。
//
// ── 缺陷6（同日真机复现，加视频路径）────────────────────────────────────────
//   给一条带录音的笔记加视频保存：
//     .form-error  「保存失败，请稍后重试」
//     audio 子行消失、盘上只剩 body.md、库行 audio_path 悬空指向不存在的文件
//     异常        [note] 保存失败: ProgressEvent
//                  TypeError: Cannot read properties of null (reading 'split')
//                  （blobToBase64 的 reader.result 为 null，base64.ts 未做 null 防护）
//   成因：`const replacingMedia = media.length > 0` 命中后**先** deleteNoteFiles
//   （整目录 rmdir）**再** persistNotePayload。后者抛错 ⇒ 删除无法回滚，
//   而结尾的 UPDATE local_notes 永不执行 ⇒ 子表与库行同时停在半路。
//
//   ⇒ 与 FK 写入顺序同一条原则：**破坏性操作必须排在成功写之后**。
//     本门判据 3 就是这条顺序不变量，负控1 是它的变异。
//
// 文件末尾五条负控：把删除挪到写之前、删整个目录、audioPath 换成无条件赋值、
// deleteNoteBodyFile 里放回 rmdir、去掉子行 kind 限定 —— 每一条门都必须报红。
// 没有负控的静态门只是一段看着像断言的文本。

import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const SRC = path.resolve(HERE, '..', '..', '..')
const PERSIST = path.join(SRC, 'features', 'notes', 'notes-persist.ts')
const NOTE_FILES = path.join(SRC, 'features', 'notes', 'note-files.ts')

/**
 * 取 `function <name>(...) { … }` 的**函数体**（大括号配平），null = 没找到。
 *
 * 必须先跳过参数表再找 body 的 `{`：形如
 *   export async function updateNote(id: string, patch: Partial<…> & { media?: … }, …) {
 * 的声明里，**参数表内部就有花括号**。第一版直接 indexOf('{')，配平从
 * `& {` 那个左括号开始，数到的是 patch 类型的右括号 —— 于是 updateNote 的
 * 函数体被判成一小段类型声明，门把「修复已在位」误报成五条风险。
 * 症状很有迷惑性：负控反而全绿（它们查的是别的文件），只有主断言红。
 */
export function fnBody(src, decl) {
  const start = src.indexOf(decl)
  if (start < 0) return null
  // 1) 跳过形参表。decl 本身以 '(' 结尾，那个开括号已经被消耗掉了，
  //    所以初始深度是 **1** 而不是 0 —— 取 0 的话扫描器会在函数体里
  //    第一个 `getNote(...)` 的 ')' 上误判成「形参表闭合」。
  let i = start + decl.length
  let paren = 1
  let closed = false
  for (; i < src.length; i++) {
    const c = src[i]
    if (c === "'" || c === '"' || c === '`') { // 跳过字符串字面量
      const quote = c
      for (i++; i < src.length && src[i] !== quote; i++) if (src[i] === '\\') i++
      continue
    }
    if (c === '(') paren++
    else if (c === ')') { paren--; if (paren === 0) { closed = true; i++; break } }
  }
  if (!closed) return null
  // 2) 形参表之后（含可选的返回类型）的第一个 '{' 才是函数体起点
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

/**
 * 去注释，**保留长度与换行**（用等长空格替换）。
 * 保留长度是刻意的：本门要用字符下标比较两个调用的先后顺序，
 * 去注释若缩短了文本，下标就全错了。
 *
 * 为什么必须去：本文件里大量注释在**解释成因**，其中就写着
 * `const replacingMedia = media.length > 0`、`deleteNoteFiles`、
 * `audioPath = written.audioPath` 这些字面量。直接 indexOf 命中的是注释，
 * 修复后门仍报红（同一类错本会话已踩三次）。
 */
export function stripComments(src) {
  return src
    .replace(/\/\*[\s\S]*?\*\//g, (m) => m.replace(/[^\n]/g, ' '))
    .replace(/(^|[^:])\/\/[^\n]*/g, (m, p1) => p1 + ' '.repeat(Math.max(0, m.length - p1.length)))
}

/**
 * 返回「updateNote 会丢媒体 / 会留下悬空引用」的风险清单；空数组 = 安全。
 * 抽成函数是为了让负控能对**同一段检测逻辑**跑变异。
 */
export function findMediaLossRisks(updateNoteBody, deleteBodyFileBody) {
  const risks = []
  const u = stripComments(updateNoteBody || '')
  const d = stripComments(deleteBodyFileBody || '')

  if (!u) return ['updateNote() 函数体没找到（门形同虚设）']
  if (!d) return ['deleteNoteBodyFile() 函数体没找到（门形同虚设）']

  // ── 判据 1：updateNote 一次都不许碰 deleteNoteFiles ──────────────────────────
  // 它对整个笔记目录 rmdir recursive：body + audio + images + videos 一起没。
  // 用词边界匹配 —— `deleteNoteBodyFile(` 不会误命中 `deleteNoteFiles(`，
  // 但裸 indexOf('deleteNoteFiles') 在字符串里出现时也未必，这里按调用点判。
  if (/\bdeleteNoteFiles\s*\(/.test(u)) {
    risks.push('updateNote 调用了 deleteNoteFiles —— 它会 rmdir 整个笔记目录，媒体全部消失')
  }
  // ── 判据 2：改正文只走 deleteNoteBodyFile ──────────────────────────────────
  if (!/\bdeleteNoteBodyFile\s*\(/.test(u)) {
    risks.push('updateNote 没有走 deleteNoteBodyFile（改正文路径没有只删 body 的那一条）')
  }
  // ── 判据 3：★ 破坏性操作必须排在成功写之后 ────────────────────────────────
  // 这条是缺陷6 的根因判据。删除在写之前 ⇒ 写失败就没有回滚路径，
  // 整目录已经没了；而 UPDATE 在最后，写失败时连库行都停在旧值。
  const writeAt = u.search(/\bpersistNotePayload\s*\(/)
  const delAt = u.search(/\bdeleteNoteBodyFile\s*\(/)
  if (writeAt >= 0 && delAt >= 0 && delAt < writeAt) {
    risks.push('★ 删除排在 persistNotePayload 之前 —— 写失败时媒体已被物理删除且无法回滚（缺陷6）')
  }
  if (writeAt < 0) risks.push('updateNote 没有调用 persistNotePayload（附件写盘路径不存在）')
  // ── 判据 4：audio_path 引用只能被「本次真的提交了 audio」换掉 ────────────────
  // 加视频时 written.audioPath 是 null（原样保留）；改正文时是 null（必须保留旧值）。
  if (!/\baudioPath\s*=\s*written\.audioPath\s*\?\?\s*existing\.audioPath\b/.test(u)) {
    risks.push('audioPath 不是 `written.audioPath ?? existing.audioPath` —— 本次没提交 audio 时会把引用清空')
  }
  // ── 判据 5：正文不落文件的那条 else 分支必须留着 audio_path ──────────────────
  // 内容变短到内联时走 else，历史上正是这里写 `audioPath = null`。
  const elseAt = u.indexOf('} else {')
  if (elseAt >= 0) {
    const branch = u.slice(elseAt, delAt >= 0 && delAt > elseAt ? delAt : elseAt + 600)
    if (!/\baudioPath\s*=\s*existing\.audioPath\b/.test(branch)) {
      risks.push('正文不落文件的 else 分支没有保留 `audioPath = existing.audioPath`')
    }
  }
  // ── 判据 6：deleteNoteBodyFile 自身的删除范围 ───────────────────────────────
  if (/rmdir/.test(d)) risks.push('deleteNoteBodyFile 里有 rmdir —— 递归删整个目录会连带删掉媒体')
  if (!/deleteFile/.test(d)) risks.push('deleteNoteBodyFile 没有 deleteFile：正文文件删不掉，重复保存会留陈旧 body.md')
  if (!/DELETE FROM local_note_files[^;]*kind\s*=/.test(d)) {
    risks.push('deleteNoteBodyFile 的 DELETE 没有限定 kind = body，会把音频子行一起删掉')
  }
  return risks
}

const read = (p) => fs.readFileSync(p, 'utf8')
const persistSrc = read(PERSIST)
const filesSrc = read(NOTE_FILES)
const updateNoteBody = fnBody(persistSrc, 'export async function updateNote(')
const deleteBodyFileBody = fnBody(filesSrc, 'export async function deleteNoteBodyFile(')

describe('改正文/加附件不得销毁媒体', () => {
  it('updateNote 与 deleteNoteBodyFile 无媒体丢失风险', () => {
    assert.deepEqual(findMediaLossRisks(updateNoteBody, deleteBodyFileBody), [])
  })

  it('前提成立：deleteNoteFiles 仍是「整体替换」用的那个（rmdir recursive）', () => {
    const whole = fnBody(filesSrc, 'export async function deleteNoteFiles(')
    assert.ok(whole, 'deleteNoteFiles() 没找到')
    assert.match(stripComments(whole), /rmdir/, 'deleteNoteFiles 不再整体删目录 ⇒ 真删笔记时残留文件')
  })

  it('前提成立：updateNote 里对 deleteNoteFiles 的调用为零（判据 1 的独立复核）', () => {
    const u = stripComments(updateNoteBody || '')
    const calls = u.match(/\bdeleteNoteFiles\s*\(/g) || []
    assert.equal(calls.length, 0, `updateNote 里出现了 ${calls.length} 次 deleteNoteFiles 调用`)
  })
})

describe('负控：把行为改回去必须报红', () => {
  it('负控1：★ 删除挪到 persistNotePayload 之前 → 报红（缺陷6 的反向变异）', () => {
    assert.ok(updateNoteBody, '拿不到 updateNote() 函数体')
    // 把清理块整体剪下来贴到写之前。剪贴后的两处各留一个空行占位，
    // 保证 persistNotePayload 的调用文本仍然存在、只是顺序反了。
    const mutated = updateNoteBody.replace(
      /( *)if \(!bodyFileNeeded && existing\.bodyPath\) \{\s*\n\s*await deleteNoteBodyFile\(id, existing\.createdAt\)\s*\n\s*\}/,
      (_m, ind) => `${ind}// moved`,
    )
    assert.notEqual(mutated, updateNoteBody, '变异没生效：清理块结构与预期不符，先看源码再改门')
    const moved = mutated.replace(
      /( *)const written = await persistNotePayload\(\{/,
      (_m, ind) => `${ind}if (!bodyFileNeeded && existing.bodyPath) {\n${ind}  await deleteNoteBodyFile(id, existing.createdAt)\n${ind}}\n${ind}const written = await persistNotePayload({`,
    )
    assert.notEqual(moved, mutated, '变异没生效：persistNotePayload 调用点找不到')
    const risks = findMediaLossRisks(moved, deleteBodyFileBody)
    assert.ok(risks.length, '★ 门有洞：把删除挪到写之前竟然判为安全')
    assert.ok(
      risks.some((r) => /排在 persistNotePayload 之前/.test(r)),
      `门红了但红得不对，要的是「顺序」那条，实际：${JSON.stringify(risks)}`,
    )
  })

  it('负控2：在 updateNote 里放回 deleteNoteFiles → 报红', () => {
    assert.ok(updateNoteBody, '拿不到 updateNote() 函数体')
    const mutated = updateNoteBody.replace(
      /( *)const shouldEncrypt = useCryptoConfig\(\)/,
      (_m, ind) => `${ind}await deleteNoteFiles(id, existing.createdAt)\n${ind}const shouldEncrypt = useCryptoConfig()`,
    )
    assert.notEqual(mutated, updateNoteBody, '变异没生效：注入点找不到')
    const risks = findMediaLossRisks(mutated, deleteBodyFileBody)
    assert.ok(risks.length, '★ 门有洞：整目录删除竟然判为安全')
    assert.ok(risks.some((r) => /deleteNoteFiles/.test(r)))
  })

  it('负控3：audioPath 换成无条件赋值 → 报红', () => {
    assert.ok(updateNoteBody, '拿不到 updateNote() 函数体')
    const mutated = updateNoteBody.replace(
      /audioPath = written\.audioPath \?\? existing\.audioPath/,
      'audioPath = written.audioPath',
    )
    assert.notEqual(mutated, updateNoteBody, '变异没生效：audioPath 赋值行结构与预期不符')
    const risks = findMediaLossRisks(mutated, deleteBodyFileBody)
    assert.ok(risks.length, '★ 门有洞：audio_path 被无条件覆盖竟然判为安全')
    assert.ok(risks.some((r) => /written\.audioPath/.test(r)))
  })

  it('负控4：deleteNoteBodyFile 里放回 rmdir → 报红', () => {
    assert.ok(deleteBodyFileBody, '拿不到 deleteNoteBodyFile() 函数体')
    const mutated = deleteBodyFileBody.replace(
      /await Filesystem\.deleteFile\([^)]*\)\.catch\(\(\) => \{\}\)/,
      "await Filesystem.rmdir({ path: rel, directory: Directory.Data, recursive: true }).catch(() => {})",
    )
    assert.notEqual(mutated, deleteBodyFileBody, '变异没生效：源码结构与预期不符，先看源码再改门')
    const risks = findMediaLossRisks(updateNoteBody, mutated)
    assert.ok(risks.length, '★ 门有洞：deleteNoteBodyFile 里放回整目录删除竟然判为安全')
    assert.ok(risks.some((r) => /rmdir/.test(r)))
  })

  it('负控5：子表 DELETE 不限定 kind → 报红', () => {
    assert.ok(deleteBodyFileBody, '拿不到 deleteNoteBodyFile() 函数体')
    const mutated = deleteBodyFileBody.replace(
      /DELETE FROM local_note_files WHERE note_id = \? AND kind = \?/,
      'DELETE FROM local_note_files WHERE note_id = ?',
    )
    assert.notEqual(mutated, deleteBodyFileBody, '变异没生效：DELETE 语句与预期不符')
    const risks = findMediaLossRisks(updateNoteBody, mutated)
    assert.ok(risks.length, '★ 门有洞：DELETE 不限定 kind 竟然判为安全')
    assert.ok(risks.some((r) => /kind/.test(r)))
  })
})