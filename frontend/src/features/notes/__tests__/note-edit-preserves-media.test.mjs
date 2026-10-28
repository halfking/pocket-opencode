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
// §898：切段器是纯函数，直接引来做**行为断言**（源码形状断言管不住「换个写法绕过上限」）。
import { splitForEmbedding } from '../note-storage-policy.ts'

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
      /( *)if \(!bodyFileNeeded && existing\.bodyPath\) \{\s*\n\s*await deleteNoteBodyFile\(id, existing\.createdAt, db\)\s*\n\s*\}/,
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
      // ⚠ §948：deleteFile 的 catch 从「`.catch(() => {})`」改成了
      //   「`.catch((e) => { 理由注释 + console.warn })`」⇒ 锚点随之更新
      //   （语义不变：仍然删的是那个文件，只是失败时留了痕）。
      /await Filesystem\.deleteFile\([^)]*\)\.catch\(\(e\) => \{[\s\S]*?\n    \}\)/,
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
// ===========================================================================
// §893 D63：`updateNote` 在 **patch 里没有 content** 时会无条件加载全文。
//
// §891/D61 把 `loadFullContent` 的降级收紧成抛错。本节复核那次改动的**影响面**，
// 结论是：影响面比 §891 记录的「打不开笔记」更宽，而且顺带关掉了一处更早的
// 静默缺陷。两条都钉在这里，免得下一个人重新怀疑一遍。
// ===========================================================================

describe('§893 · D63：updateNote 的派生状态来源（内容没变时也要重建）', () => {
  const u = stripComments(updateNoteBody || '')

  it('★ 量具自证：updateNote 的函数体抽得到、且确实在加载全文', () => {
    assert.ok(u.length > 0, '抽不到 updateNote() 函数体 ⇒ 下面几条会变成恒真')
    assert.match(u, /patch\.content !== undefined\s*\?\s*patch\.content\s*:\s*await loadFullContent\(existing, db\)/,
      'updateNote 已不按「内容没变就沿用」处理 —— 请重新判断本节各条的结论是否还成立')
  })

  it('★★★ 内容缺省时会加载全文（这就是 D63 的根因，也是 §891 抛错面的来源）', () => {
    // ⚠ 这不是缺陷断言，是**事实**断言：它锁住「为什么元信息保存也会被正文问题挡住」。
    //   两个真实调用点都落在这一支（已逐个核对）：
    //     NoteListView.vue:552  onMetaSave  → { ...data, status: 'saved' }（title/domain/tags）
    //     NoteListView.vue:460  摘要落库     → { summary }
    assert.match(u, /await loadFullContent\(existing, db\)/,
      'updateNote 不再加载全文 ⇒ 本节记录的影响面已变，请重核')
  })

  it('★★★ 派生状态（content / search_text / storage_tier）在每次 updateNote 都会被重建', () => {
    // ⚠⭐ 这一条同时记录了 §891 **顺带堵住**的一处静默缺陷：
    //   §891 之前，`loadFullContent` 在正文读不到时返回 note.content（tier='file' 下是
    //   ≤280 字摘要）⇒ `decideNoteStorage({ body: 摘要 })` ⇒ `search_text` 被**用摘要重建**
    //   ⇒ 全文检索无声地退化成摘要检索（不丢正文、但检索质量静默下降，且没有任何提示）。
    //   §891 改成抛错后这条路径不再走到 ⇒ 该降级被一并关闭。
    assert.match(u, /decideNoteStorage\(\{/, 'updateNote 不再重算存储分层 ⇒ 请重核本节结论')

    // ⚠⚠⚠ 必须定位到 **UPDATE 语句的参数数组**，不能只断言「出现过 decided.searchText」。
    //   `decided.searchText` 在 updateNote 里出现**两次**（UPDATE 的值 + 向量写入的实参），
    //   所以 `/decided\.searchText/` 这种「出现过」式的断言在把 UPDATE 那一处
    //   换成 `existing.searchText` 时**照样全绿** —— M51-M3 实测逃掉。
    //   ⇒ 判据要量的是「它被写进 UPDATE 的值里」这个**流向**，不是「源码里有这个词」。
    //
    // ⚠ 第二版又踩了一次定位坑：第一版取 `UPDATE … WHERE id = ?` 之间的窗口，
    //   而 SQL 模板里的 WHERE **排在参数数组之前** ⇒ 那个窗口只有列名列表，没有值。
    //   ⇒ 改为从 `UPDATE` 之后的第一个 `[` 起做**括号配平**（SQL 模板里没有方括号）。
    const setAt = u.indexOf('UPDATE local_notes SET')
    assert.ok(setAt > 0, '定位失败：updateNote 里找不到 UPDATE 语句')
    const br = u.indexOf('[', setAt)
    assert.ok(br > 0, '定位失败：UPDATE 之后找不到参数数组的左括号')
    let depth = 0
    let end = -1
    for (let i = br; i < u.length; i++) {
      if (u[i] === '[') depth++
      else if (u[i] === ']') {
        depth--
        if (depth === 0) { end = i + 1; break }
      }
    }
    assert.ok(end > 0, '参数数组括号不配平 ⇒ 定位失败')
    const updateParams = u.slice(br, end)
    assert.match(updateParams, /decided\.searchText/,
      'search_text 不再由 decided 派生（改成了别的来源）⇒ 派生链已变，请重核本节结论')
    assert.match(updateParams, /decided\.tier/, 'storage_tier 同样应由 decided 派生')
    // 向量那条必须同样来自**本次重建**的 decided（§896/D66 改用 decided.embedText，
    // 它是同一份 decided 里的「纯内容」投影，与 searchText 同源不同形）。
    // ⚠ 不变量没变：向量不得改用 existing.* / 旧值 / 别处重建的文本 ——
    //   旧值在 tier='file' 时是 ≤280 字摘要（§891/D61 同族，会让向量与索引分叉）。
    assert.match(u, /embedAndStore\(id, decided\.embedText, operation\.cloud\)/,
      '向量写入不再用本次重建的 decided.embedText ⇒ 请重核派生链')
    assert.doesNotMatch(u, /embedAndStore\([^)]*existing\./,
      '向量改用了 existing.*（tier=file 时那是 ≤280 字摘要）⇒ 向量会与 FTS 索引分叉')
  })

  it('★★★ 反向：不得改成「内容没变就用 existing.content 当 nextContent」', () => {
    // ⭐ 防矫枉过正：那正是 §891/D61 的原形 —— tier='file' 时 existing.content 是摘要，
    //   而它会被 persistNotePayload 写回正文文件（打开再保存就销毁全文）。
    //   正确的替代方案（内容没变就**完全不重建**派生状态）属设计取舍，见简报 116；
    //   在属主拍板前，这里只钉住「不许退回去」。
    assert.doesNotMatch(
      u,
      /:\s*existing\.content\s*(?:;|,|\n)/,
      'updateNote 把 existing.content 当 nextContent 了 ⇒ D61 原形复活（摘要会被写回正文文件）',
    )
  })

  it('★★ 两个真实调用点都必须在 catch 里（否则抛错是无提示的死路）', () => {
    const list = read(path.join(SRC, 'features', 'notes', 'NoteListView.vue'))
    // onMetaSave：try/catch 就在 updateNote 两侧
    assert.match(list, /async function onMetaSave[\s\S]{0,400}?try \{[\s\S]{0,300}?updateNote\([\s\S]{0,400}?\} catch/,
      'onMetaSave 的 updateNote 不在 try/catch 里 ⇒ 元信息保存失败会变成无提示的死路')
    // 摘要落库那一支在 onMetaSummarize 的 try 内
    assert.match(list, /try \{[\s\S]{0,4000}?updateNote\(noteId, \{ summary \}[\s\S]{0,4000}?\} catch/,
      '摘要落库的 updateNote 不在 try/catch 里 ⇒ 会变成未处理的 rejection')
  })

  it('★★ 如实登记：摘要落库那条的**错误归因是错的**（本节不代拍，只钉住事实）', () => {
    // ⚠ 摘要已经由服务端生成并拿回来了，抛错的只是**本地落库**；
    //   用户看到的却是「总结失败，可稍后在笔记详情页重试」。
    //   修它要改文案（产品口径）⇒ 登记在简报 116，这里只钉住「catch 写的是总结失败」。
    const list = read(path.join(SRC, 'features', 'notes', 'NoteListView.vue'))
    assert.match(list, /summarizeError\.value = apiError\(e, '总结失败/,
      '摘要落库那条的错误文案已改（不再归因成「总结失败」）⇒ 简报 116 第 ① 条可以撤了')
  })
})

/* ═══════════════════════════════════════════════════════════════════════════
 * §896 · D66：送去做向量的那份文本有两个缺陷
 * ═══════════════════════════════════════════════════════════════════════════
 * ① **喂给 embedding 模型的是 FTS 检索文本**，而那份文本的尾巴是
 *    `cjkBigramTokens` 合成出来的机器 bigram（最多 80 个 gram ≈ 239 字符）
 *    —— 它是给 FTS5 做 CJK 子串匹配用的，对 embedding 是纯噪声。
 *    实测：正文 200 字的随手记，实收文本里 **53.2%** 是合成 gram。
 * ② **`/api/embed` 服务端有 16000 字符硬上限**
 *    （`backend/internal/server/server_assistant.go:2853`），
 *    前端此前**不知道**，发的是不截断的整份文本
 *    ⇒ 正文超过约 15753 字（≈63 分钟连续发言）必然 400；
 *    而创建路径只 `console.warn`、编辑保存路径 `.catch(() => {})` **完全静默**，
 *    全仓又没有 embedding 回填 ⇒ **这条笔记永远不会有向量**，语义检索永远搜不到它。
 *
 * 本组判据盯三件事：**去噪**、**长度预算**、**失败不再静默**。
 * 最后一组带负控：把两个 `.catch` 换回原来的形状，门必须报红。
 */
describe('§896 · D66：向量化用的文本不该是 FTS 文本，也不该无上限', () => {
  it('★★★ 向量必须用 decided.embedText（纯内容），不得用 decided.searchText', () => {
    const u = read(path.join(SRC, 'features', 'notes', 'notes-persist.ts'))
    assert.match(u, /embedAndStore\(id, decided\.embedText, operation\.cloud\)/,
      '编辑保存路径的向量又改回 decided.searchText ⇒ 合成 gram 回到 embedding 输入里')
    assert.match(u, /embedAndStore\(note\.id, decided\.embedText \|\| note\.content, operation\.cloud\)/,
      '创建路径的向量没有走 decided.embedText')
    // 反向：searchText 只该出现在 UPDATE 的派生列里，不该再被送去做向量。
    // ⚠ 量具事故（已写进注释）：第一版正则要求实参是 `note.id`，
    //   而编辑保存那条传的是裸 `id` ⇒ 只数到 1 处 ⇒ 判据自己红了。
    //   「数量判据」必须允许两种入参写法，否则它会替漏网背书。
    const embedCalls = [...u.matchAll(/embedAndStore\(\s*[\w.]+\s*,\s*([^)]*)\)/g)].map((m) => m[1].trim())
    assert.ok(embedCalls.length >= 2, `只找到 ${embedCalls.length} 处 embedAndStore 调用（判据该按数量而不是按字面量钉）`)
    for (const arg of embedCalls) {
      assert.doesNotMatch(arg, /searchText/,
        `embedAndStore 的实参是 ${JSON.stringify(arg)} —— 那是 FTS 文本（含合成 bigram），不是 embedding 输入`)
    }
  })

  it('★★★ decideNoteStorage 必须同时给出两个投影，且 embedText 不含 gram', () => {
    const pol = read(path.join(SRC, 'features', 'notes', 'note-storage-policy.ts'))
    assert.match(pol, /export function buildEmbedText\(/, 'note-storage-policy 少了 buildEmbedText')
    assert.match(pol, /embedText,\n?\s*persistBodyFile: false,/, 'inline 分支没有产出 embedText')
    assert.match(pol, /embedText,\n?\s*persistBodyFile: true,/, 'file 分支没有产出 embedText')
    // buildEmbedText 自身不得引用 gram 构造器 —— 这就是「去噪」这件事本身。
    const body = pol.slice(pol.indexOf('export function buildEmbedText'), pol.indexOf('export function buildSearchText'))
    assert.doesNotMatch(body, /cjkBigramTokens/,
      'buildEmbedText 里又出现了 cjkBigramTokens ⇒ 去噪被撤销')
    // 反向负控：buildSearchText 必须仍然带 gram（FTS 侧一个字都不能变）
    assert.match(pol.slice(pol.indexOf('export function buildSearchText')), /cjkBigramTokens/,
      'buildSearchText 不再带 gram ⇒ FTS 的 CJK 子串匹配塌了（§886 的修法被误伤）')
  })

  it('★★★ 发出去的每一段都必须在服务端上限内，且上限值与后端硬编码一致', () => {
    // ⚠⚠ §898：这条门**换了实现**（截断 ⇒ 分段），但**不变量没变**：
    //   「/api/embed 收到的 text 永远不超过服务端上限」才是要守的东西。
    //   ⇒ 而且判据从「源码里有截断」升级成**行为断言**：
    //     直接对切段器喂超长文本，检查**每一段**的长度。
    //   （源码形状断言管不住「换个写法绕过上限」，行为断言管得住。）
    const u = read(path.join(SRC, 'features', 'notes', 'notes-persist.ts'))
    assert.match(u, /const EMBED_TEXT_CHAR_LIMIT = 16000/,
      'EMBED_TEXT_CHAR_LIMIT 不存在或不是 16000 ⇒ /api/embed 会拿到必然被拒的请求体')
    // 请求体必须发的是**段文本**，不是原 content
    // ⚠⚠ §899：段文本的取法从 `c.text` 变成 `chunks[i].text`（增量重嵌那一版），
    //   所以这里**不再钉死变量名**，只钉「发的是某个段的 .text」。
    //   ⇒ 钉变量名会把「换了取法但语义没变」误判成缺陷。
    assert.match(u, /JSON\.stringify\(\{ text: [A-Za-z_\[\]0-9.]+\.text \}\)/,
      '/api/embed 请求体没有用段文本（仍在发整篇 content ⇒ 超长必然 400）')
    assert.doesNotMatch(u, /JSON\.stringify\(\{ text: content \}\)/,
      '/api/embed 又在发整篇 content ⇒ 超长笔记必然 400 且永远拿不到向量')
    // 行为：切段器吐出来的每一段都必须在上限内，且**整篇被覆盖**（不丢尾部）
    //
    // ⚠⚠⚠ §917：这条门原先量的是**字符**（`c.text.length <= LIMIT`），
    //   而**服务端那道闸判的是 UTF-8 字节**（Go 的 `len(string)`）
    //   ⇒ **信息与判定不同形** ⇒ 这道门在 CJK 夹具上**恒绿**：
    //   `'甲'` 是 3 字节 ⇒ 修复前每块 16000 字符 = **48000 字节**
    //   ⇒ 服务端**必然 400**（⇒ 那条笔记 0 向量），而这道门**全绿**。
    //   ⇒ 门**没有变松**：是**测量单位被纠正**成它自己标题里写的那个量
    //   （「在服务端上限内」）。⚠ 纠正之后它当场变红 ⇒ 这正是空气门被识破的样子。
    const LIMIT = 16000
    const long = '甲'.repeat(70000)
    const chunks = splitForEmbedding(long, { limit: LIMIT })
    assert.ok(chunks.length > 1, `70000 字只切出 ${chunks.length} 段 ⇒ 分段没生效`)
    for (const c of chunks) {
      const bytes = Buffer.byteLength(c.text, 'utf8')
      assert.ok(bytes <= LIMIT,
        `有一段长 ${c.text.length} 字符 / ${bytes} 字节，超过服务端上限 ${LIMIT} **字节** ⇒ 必然 400`)
      // 字符数那条**保留**：字节 ≤ 16000 ⇒ 码元必然 ≤ 16000（每码元 ≥ 1 字节）
      assert.ok(c.text.length <= LIMIT,
        `有一段长 ${c.text.length} 字符，超过 ${LIMIT} ⇒ 切段失控`)
    }
    // 整篇必须被覆盖到（这是「不再截断」与「截断」的本质区别）
    const covered = chunks.map((c) => c.text).join('')
    assert.ok(covered.length >= long.length - LIMIT,
      `分段后覆盖的字符数 ${covered.length} 远小于原文 ${long.length} ⇒ 尾部被丢了`)
  })

  it('★★★ 两条嵌入路径的失败必须同样出声（编辑保存那条曾是完全静默的）', () => {
    const u = read(path.join(SRC, 'features', 'notes', 'notes-persist.ts'))
    const catches = [...u.matchAll(/embedAndStore\([^)]*\)\.catch\(\(([^)]*)\)\s*=>\s*\{([^}]*)\}/g)]
    assert.equal(catches.length, 2,
      `找到 ${catches.length} 处 embedAndStore().catch()（期望 2：创建 + 编辑保存）`)
    for (const [, param, body] of catches) {
      assert.match(param, /e|err|error/, `有一处 catch 没接住错误参数：${param}`)
      assert.match(body, /console\.(warn|error)/,
        `有一处 embedAndStore().catch() 是空的 ⇒ 嵌入失败零痕迹（${body.trim()}）`)
    }
    // 反向负控：不得出现「参数接了但什么也不做」
    assert.doesNotMatch(u, /embedAndStore\([^)]*\)\.catch\(\(\)\s*=>\s*\{\s*\}\)/,
      '又有 embedAndStore().catch(() => {}) 空吞了 —— 这正是 §896 修掉的那一行')
  })
})

/**
 * §917：`/api/embed` 的闸是 **UTF-8 字节**，而切段器历史上按**码元**切
 * ⇒ 「按 16000 码元切出来的块」是 **48000 字节** ⇒ **每一次 `/api/embed` 都 400**
 * ⇒ 那条笔记**一个向量都建不出来** ⇒ 语义检索对它 **0 命中**（FTS 仍可用）。
 *
 * ── 缺陷本体（实测，真实 ASR 语料）──
 *   `.artifacts/asr-s788/seg-asr/merged.txt`：3392 字 / **9938 字节** / 51 行
 *   ⇒ **2.93 字节/字** ⇒ 16000 字节的闸只装得下 **5460 个中文字**。
 *   ⇒ **失败带 = 5460 ~ 16000 字**（≈ **20 ~ 59 分钟**发言）：
 *     ① 走「整篇一段」的早退分支 ⇒ **1 块 16000 字节以上** ⇒ 400；
 *     ② 无换行无句号的正文 ⇒ 按 16000 **码元**切 ⇒ 每块 **48000 字节** ⇒ 全 400。
 *   ⇒ ⚠ **1 小时以上反而正常**（按 §900 的行/句锚点切成 ~4000 字的组 ⇒ 11.7KB/块）
 *     ⇒ 所以这是**一条失败带**，不是单调退化 ⇒ 只看长文本会以为没问题。
 *
 * ── 修法 ──
 *   切段器的「整篇一段」早退分支要**同时**满足码元阈值与**字节**闸；
 *   切完再过一道 `capChunksByBytes` 兜住「组内再切」与「末段并回上一段」两条路径。
 *   ⚠ **不动分组逻辑** ⇒ §899 的段边界与既有笔记的块哈希**不变**
 *     （实测：>16000 字的文本**逐段一致**）⇒ 增量重嵌的复用率不受影响。
 */
describe('§917 每块送进 /api/embed 的文本必须过**字节**闸（不是字符闸）', () => {
  /** ⚠ 刻意**不**从生产代码 import 这个数：写死服务端那个值，
   *  前端的镜像常量一旦漂移，本判据立刻红（否则两边一起错就没人知道）。 */
  const GATE = 16000
  const bytes = (s) => Buffer.byteLength(s, 'utf8')

  /** 真实转写的形状：每行一个 ASR 分段（中位 66 字，§900 实测）。 */
  const realTranscriptShape = (n) => Array.from({ length: n }, (_, i) =>
    `第${i}点网关的抽象层现在有一个整体系有一套那个整大模型抽象层，然后所谓的CI能够借用到这个抽象层${'细节'.repeat(5)}`,
  ).join('\n')

  /** 无换行但有句号的形状（随手记增量链 `appendTranscript` 的形状）。 */
  const noNewline = (n) => Array.from({ length: n }, (_, i) =>
    `第${i}点网关的抽象层现在有一个整体系有一套那个整大模型抽象层${'细节'.repeat(5)}。`,
  ).join('')

  /** 既无换行也无句号（纯口述无标点）。 */
  const noStructure = (n) => '会议纪要讨论季度预算与交付里程碑排期以及客户跟进安排事项与负责人确认'.repeat(n)

  /**
   * ⚠ 三条语料各取多少段，**必须先量**（本节第一版取 60 段 ⇒ 字数不够，
   *   三条都**没踩进失败带** ⇒ 量具自证当场把它抓了出来 —— 这条自证存在的意义）。
   *   实测（真实形状，中文约 2.93 字节/字）：
   *     真实转写形状 150 段 = 8889 字 / 25089 字节 ✔ 在带内
   *     无换行有句号 150 段 = 6190 字 / 17890 字节 ✔ 在带内
   *     无结构     200 段 = 6800 字 / 20400 字节 ✔ 在带内
   */
  const BAND_N = { rt: 150, noNewline: 150, noStructure: 200 }

  it('★ 量具自证：三条语料必须真的踩在「字符不超闸、字节超闸」的缝里', () => {
    for (const [name, text] of [
      ['真实转写形状', realTranscriptShape(BAND_N.rt)],
      ['无换行有句号', noNewline(BAND_N.noNewline)],
      ['无结构', noStructure(BAND_N.noStructure)],
    ]) {
      const chars = [...text].length
      const b = bytes(text)
      assert.ok(chars <= GATE,
        `量具失效：${name} 有 ${chars} 个字符 > ${GATE} ⇒ 它量不到单位差异`)
      assert.ok(b > GATE,
        `量具失效：${name} 只有 ${b} 字节 ≤ ${GATE} ⇒ 它量不到单位差异`)
    }
  })

  it('★ 每块送进 /api/embed 的文本，**字节数**必须 ≤ 闸（三种形态 × 多个长度）', () => {
    const shapes = [
      ['真实转写形状', realTranscriptShape],
      ['无换行有句号', noNewline],
      ['无结构', noStructure],
    ]
    for (const [name, mk] of shapes) {
      for (const n of [30, 60, 120, 200, 400, 900]) {
        const text = mk(n)
        const chunks = splitForEmbedding(text)
        assert.ok(chunks.length > 0, `${name}/${n}：切出 0 段`)
        for (const c of chunks) {
          const b = bytes(c.text)
          assert.ok(b <= GATE,
            `${name}/${n}（${text.length} 字）：有一段 ${c.text.length} 字符 / **${b} 字节** ` +
            `> 闸 ${GATE} 字节 ⇒ 这一次 /api/embed **必然 400** ⇒ 这条笔记 0 向量`)
        }
      }
    }
  })

  it('★ 失败带（字符不超闸、字节超闸）必须**被切开**，不能整篇一次发出去', () => {
    // ⚠ 这条是本节的**判别动作**：修复前这一段是**整篇一块** ⇒ 必然 400。
    for (const [name, text] of [
      ['真实转写形状', realTranscriptShape(BAND_N.rt)],
      ['无换行有句号', noNewline(BAND_N.noNewline)],
    ]) {
      assert.ok([...text].length <= GATE && bytes(text) > GATE,
        `量具失效：${name} 不在失败带里`)
      const chunks = splitForEmbedding(text)
      assert.ok(chunks.length > 1,
        `${name}：${text.length} 字 / ${bytes(text)} 字节 被整篇发成 1 块 ⇒ 必然 400`)
    }
  })

  it('★ 切段不能丢内容，且每块必须是原文的**真子串**（偏移精确）', () => {
    const text = realTranscriptShape(200)
    const chunks = splitForEmbedding(text)
    for (const c of chunks) {
      assert.equal(text.slice(c.start, c.end), c.text,
        `有一段不是原文的真子串（start=${c.start} end=${c.end}）⇒ 哈希与原文对不上`)
    }
    // 覆盖度：按顺序把每段文本拼起来，应当覆盖原文去掉分隔符后的全部内容
    const stripped = text.replace(/\n+/g, '')
    const joined = chunks.map((c) => c.text).join('')
    let i = 0
    for (const ch of joined) { if (i < stripped.length && stripped[i] === ch) i++ }
    assert.equal(i, stripped.length,
      `切段后只覆盖了原文的 ${((i / stripped.length) * 100).toFixed(1)}% ⇒ 内容丢了`)
  })

  it('★ 短笔记行为**逐字不变**（低于闸 ⇒ 仍然整篇一块，一个字都不动）', () => {
    const text = '张伟：下周三跟客户过一遍方案。\n李娜：预算按季度复核，缺口走专项。'
    const chunks = splitForEmbedding(text)
    assert.equal(chunks.length, 1, `短笔记被切成了 ${chunks.length} 块 ⇒ 行为变了`)
    assert.equal(chunks[0].text, text, '短笔记的块文本与原文不一致 ⇒ 行为变了')
  })
})
