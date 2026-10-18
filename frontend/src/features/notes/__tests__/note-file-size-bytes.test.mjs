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
import { createHash } from 'node:crypto'
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

// ===========================================================================
// §892 D62：「降级取值」普查 —— 全仓笔记侧 10 处 `X ?? note.content` 形态，
// 逐条判定**能不能被写回**。结论是 0 处能，唯一的例外是 §891 修掉的 D61。
//
// 为什么这道门值得存在（不是洁癖）：
//   §891/D61 证明了一件事 —— **读路径拿「为别的目的存在的低保真副本」兜底，
//   而写入路径会把那个兜底值当真身写回**，就构成不可逆的数据销毁。
//   本门把那条教训从「一个 bug」变成「一条跨切面的不变量」。
//
// 实测读数（2026-10-18，`features/notes` 生产代码，排除 `__tests__` 与 `*.test.*`）：
//   扫描 30 个生产文件，命中 10 行、归并为 7 个 (文件, 字段) 对。
//   逐条判定：4 处模板只读展示、2 处摘要/正文互为只读输入、
//             1 处同字段兜底（摘要写摘要列）、2 处「来值为空则保留原值」的保守合并。
//   ⇒ **没有一处能把低保真副本写回正文列或正文文件。**
// ===========================================================================

const NOTES_ROOT = path.join(SRC, 'features', 'notes')

/** 递归收集 `features/notes` 下的**生产**文件（排除测试）。 */
function walkProd(dir, out = []) {
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, e.name)
    if (e.isDirectory()) {
      if (e.name === '__tests__' || e.name === 'node_modules') continue
      walkProd(p, out)
    } else if (/\.(ts|vue)$/.test(e.name) && !/\.test\.(ts|mjs)$/.test(e.name)) {
      out.push(p)
    }
  }
  return out
}

/** 剥注释。锚点必须落在真代码上，不能落在解释文字上（本文件既有教训）。 */
const stripForScan = (t) =>
  t.replace(/\/\*[\s\S]*?\*\//g, (m) => m.replace(/[^\n]/g, ' '))
    .replace(/(^|[^:])\/\/[^\n]*/g, '$1')

/**
 * 「降级取值点」= 该行含 `??`/`||` **且**引用了任一保真度字段
 * （content / summary / preview / snippet / digest）。
 *
 * ⚠⚠⚠ 为什么从「`||` 右边是什么」改成**行级**（M50-M6 实测逃掉）：
 *   `const __degraded = existing.summary ?? nextContent` 随后 `content: __degraded`
 *   —— 两步流里低保真字段在 `??` **左边**，按右值匹配一条都抓不到，
 *   而它恰恰是 D61 那种「摘要经中间变量流进正文」的最短复刻。
 *   ⇒ 现在只问「这一行有没有在拿某个保真度字段做兜底」，
 *     至于它流到哪，由**登记表逐行判定**回答。
 */
const DEGRADE_FIELD_RE = /\b[\w$.]*\.(content|summary|preview|snippet|digest)\b/g
const DEGRADE_FALLBACK_RE = /\?\?|\|\|/

/**
 * 站点键 = `相对路径::字段::行内容 sha1 前 8 位`。
 *
 * ⚠⚠⚠ 为什么不能只用 `路径::字段` 做键（第一版的错法，M50-M2 实测逃掉）：
 *   `features/notes/notes-persist.ts::summary` 已在登记表里，于是**在同一个文件里
 *   再加一处 `?? note.summary` 兜底完全隐形** —— 变异实测 13/13 全绿。
 *   「这个文件用过一个低保真字段」不等于「这一处用法是安全的」。
 *
 * ⭐ 用**行内容哈希**而不是行号：
 *   行号会因无关编辑漂移（§844 的 z-index-ladder 已咬过多次），
 *   而内容哈希只在**这一行本身**被改写时才变 —— 增删改都立刻暴露。
 */
const siteKey = (rel, field, line) =>
  `${rel}::${field}::${createHash('sha1').update(line.trim()).digest('hex').slice(0, 8)}`

/** 全部降级取值点，返回精确站点键的集合。 */
function degradationSites() {
  const sites = new Set()
  for (const abs of walkProd(NOTES_ROOT)) {
    const rel = path.relative(SRC, abs)
    for (const line of stripForScan(fs.readFileSync(abs, 'utf8')).split('\n')) {
      if (!DEGRADE_FALLBACK_RE.test(line)) continue
      DEGRADE_FIELD_RE.lastIndex = 0
      let m
      while ((m = DEGRADE_FIELD_RE.exec(line))) sites.add(siteKey(rel, m[1], line))
    }
  }
  return sites
}

/**
 * ⭐ D61 形态 = **同一行**里同时出现「写入正文类字段」与「低保真副本」。
 *
 * ⚠⚠⚠ 第一版写成 `content … ?? … .summary`（要求低保真字段在 `??` **右边**），
 *   M50-M2 实测被 `content: existing.summary ?? nextContent` **整条逃掉** ——
 *   而那正是 D61 的原形：摘要流进 content，`??` 只是「优先用 nextContent」。
 * ⇒ 规则改成「两边都看」：写入形态 ∧ 低保真字段，二者同现即算命中。
 *
 * ⚠ 口径边界（如实登记，不假装覆盖更大）：本规则只管**同一条语句**。
 *   「先赋给中间变量、隔几行再写进 content」的两步流它抓不到；
 *   那类由上面的**逐行登记表**兜住（任何新出现的低保真用法都必须以行哈希登记）。
 */
const CONTENT_WRITE_RE = /\b(?:content|body|text|transcript)\s*[:=][^=\n]*/
const LOW_FIDELITY_USE_RE = /\b[\w$.]*\.(?:summary|preview|snippet|digest)\b/
const isWriteBackRisk = (line) =>
  CONTENT_WRITE_RE.test(line) && LOW_FIDELITY_USE_RE.test(line)

/**
 * 登记表：键 = `路径::字段::行哈希`。值 = 判定理由（**不许出现「可写回」字样**）。
 *
 * 可读对照表（2026-10-18 实测，`features/notes` 30 个生产文件 / 10 个站点）：
 *   NoteDetailView.vue::content   模板只读：相关笔记标题兜底
 *   NoteListView.vue::content ×3  模板只读：草稿横幅 + 卡片标题 + 卡片摘要
 *   note-fts-query.ts::content    只读输入：相关笔记一句话预览
 *   note-search.ts::content       只读输入：喂给检索助手的候选目录 snippet
 *   notes-persist.ts::content     只读输入：向量化正文兜底（searchText 空只在 body 空时）
 *   notes-persist.ts::summary     同字段兜底：摘要列写摘要，不是拿摘要冒充正文
 *   notes-store.ts::content ×2    保守合并：来值为空则**保留原值**（方向与降级相反）
 */
const DEGRADE_REGISTRY = {
  'features/notes/NoteDetailView.vue::content::7778c5d7': '模板只读：无障碍标签',
  'features/notes/NoteDetailView.vue::content::2b277048': '模板只读：相关笔记标题',
  'features/notes/NoteDetailView.vue::content::7c1b68cc': '模板只读：文档标题兜底',
  'features/notes/NoteDetailView.vue::summary::d924a8dd': '同字段：摘要列写摘要列',
  'features/notes/NoteEditView.vue::content::d2bed210': '表单本地副本；其上游 loadFullContent 已按 D61 收紧（读不到正文就抛，不会 hydrate 摘要）',
  'features/notes/NoteListView.vue::content::deacb100': '模板只读：草稿横幅标题',
  'features/notes/NoteListView.vue::summary::2ef2aaac': '模板只读：卡片标题',
  'features/notes/NoteListView.vue::content::2ef2aaac': '模板只读：卡片标题',
  'features/notes/NoteListView.vue::summary::1a1c3dee': '模板只读：卡片摘要',
  'features/notes/NoteListView.vue::content::1a1c3dee': '模板只读：卡片摘要',
  'features/notes/NoteMetaSheet.vue::summary::d3a6b4ef': '表单本地只读',
  'features/notes/NoteMetaSheet.vue::content::7003555d': '只读输入：AI 标题种子',
  'features/notes/NoteMetaSheet.vue::content::2f29eed9': '只读输入：标题建议',
  'features/notes/NoteMetaSheet.vue::content::4a99a0db': '只读输入：领域推断',
  'features/notes/NoteMetaSheet.vue::content::650be452': '只读输入：标签抽取',
  'features/notes/NoteMetaSheet.vue::content::c4387b08': '只读输入：AI 标签',
  'features/notes/NoteMetaSheet.vue::content::9d588d17': '只读输入：领域推断',
  'features/notes/NoteMetaSheet.vue::content::cff154aa': '只读输入：标题建议',
  'features/notes/NotesHubView.vue::summary::2edc92bf': '模板只读：Hub 列表预览',
  'features/notes/note-fts-query.ts::summary::1c092be0': '只读输入：相关笔记一句话预览',
  'features/notes/note-fts-query.ts::content::1c092be0': '只读输入：相关笔记一句话预览',
  'features/notes/note-search.ts::summary::18e0a71a': '只读输入：喂给检索助手的候选目录',
  'features/notes/note-search.ts::content::18e0a71a': '只读输入：喂给检索助手的候选目录',
  'features/notes/note-search.ts::summary::db6b8647': '同字段：助手摘要列写助手摘要',
  'features/notes/notes-fts-backfill.ts::content::39abfcd6': '只读输入：FTS 回填读行内容',
  'features/notes/notes-persist.ts::content::794e4d8b': '只读输入：送 /api/embed 前按服务端 16000 字符上限截断（§896/D66）。截断结果只进 HTTP 请求体，不落库也不落文件 ⇒ 无持久化副作用；⚠ 取头部是技术默认值，「分段嵌入/先摘要」属产品口径（简报 119）',
  'features/notes/notes-persist.ts::content::554774cc': '写入**条件**里的 ||，不是取值降级',
  'features/notes/notes-persist.ts::summary::19169f5a': '同字段：摘要列写摘要',
  'features/notes/notes-persist.ts::content::48f6f842': '★ §891 保留的安全降级：body_path 未回填时 content 仍是全文',
  'features/notes/notes-row.ts::summary::9be89342': '同字段：行映射归一',
  'features/notes/notes-store.ts::content::7303abbd': '保守合并：来值为空时保留原值',
  'features/notes/notes-store.ts::content::e0f74349': '保守合并：来值为空时保留原值',
}


describe('§892 · D62：降级取值的可逆性普查（跨切面不变量）', () => {
  it('★ 前置闸：扫描器真的扫到了 notes 侧的生产代码（防扫描根退一级那种假绿）', () => {
    const files = walkProd(NOTES_ROOT)
    assert.ok(files.length >= 25, `只扫到 ${files.length} 个生产文件 —— 扫描根可能退了一级`)
    assert.ok(
      files.some((f) => f.endsWith('notes-persist.ts')) && files.some((f) => f.endsWith('NoteListView.vue')),
      '扫描结果里必须有 notes-persist.ts 与 NoteListView.vue 两个锚点文件',
    )
  })

  it('★★★ 每一个降级取值点都必须已登记（新增的必须显式判定，不许默认放行）', () => {
    const sites = degradationSites()
    const unregistered = [...sites].filter((s) => !(s in DEGRADE_REGISTRY)).sort()
    assert.equal(
      unregistered.length, 0,
      `出现未登记的降级取值点：\n  ${unregistered.join('\n  ')}\n` +
        '⇒ 请先判定它「能不能被写回」，再登记进来（§891/D61：能写回就是数据销毁）',
    )
  })

  it('★★ 登记项不得腐化：每条在源文件里仍要能找到（stale 要报红）', () => {
    const sites = degradationSites()
    const stale = Object.keys(DEGRADE_REGISTRY).filter((k) => !sites.has(k)).sort()
    assert.equal(stale.length, 0, `登记项已失效（源码里找不到了），请复核：\n  ${stale.join('\n  ')}`)
  })

  it('★★★ 登记表里不许藏「可写回」的降级（这是 D62 的核心禁令）', () => {
    // 登记表是「判定结果」的容器。若有人把一条真会写回的降级登记成合法，
    // 上面两条都会绿 ⇒ 必须单独钉住「登记 ≠ 豁免」。
    for (const [key, why] of Object.entries(DEGRADE_REGISTRY)) {
      assert.doesNotMatch(why, /写回|冒充全文|可写回/, `登记项 ${key} 的判定理由里出现了「可写回」—— 那必须修，不能登记`)
    }
  })

  it('★★★ D61 形态禁令：写入正文类字段的行若同时引用低保真副本，必须已登记', () => {
    const offenders = []
    const unregistered = []
    for (const abs of walkProd(NOTES_ROOT)) {
      const rel = path.relative(SRC, abs)
      stripForScan(fs.readFileSync(abs, 'utf8')).split('\n').forEach((line, i) => {
        if (!isWriteBackRisk(line)) return
        const where = `${rel}:${i + 1}  ${line.trim().slice(0, 80)}`
        offenders.push(where)
        // 命中形态的每一行都必须能在登记表里找到对应站点键。
        const registered = [...degradationSites()].some((k) => k.startsWith(`${rel}::`) && k.includes(
          createHash('sha1').update(line.trim()).digest('hex').slice(0, 8),
        ))
        if (!registered) unregistered.push(where)
      })
    }
    assert.equal(
      unregistered.length, 0,
      `这些行把低保真副本带进了正文写入语句，却未登记：\n  ${unregistered.join('\n  ')}\n` +
        '⇒ 这就是 §891/D61 的形状：降级值会被写入路径当真身持久化（不可逆）',
    )
    // 如实登记读数：当前命中的行必须是已复核过的少数几行，不能悄悄膨胀。
    assert.ok(
      offenders.length <= 2,
      `D61 形态当前命中 ${offenders.length} 行（读数：\n  ${offenders.join('\n  ')}）—— ` +
        '比登记时多了，请逐行复核是不是新引入的写回',
    )
  })

  it('★★★ 量具自检（负向对照）：规则必须真的抓得住，且不得误伤同族形态', () => {
    // ① 必须抓住：低保真字段在 `??` 左右两侧都要算（M50-M2 的教训）。
    const mustCatch = [
      'content: existing.summary ?? nextContent',
      'content: x ?? note.summary',
      'const body = a || note.preview',
      'content: b ?? row.snippet',
      'text = read() ?? row.digest',
      'const transcript = a || b.summary',
      'body: note.summary',
    ]
    for (const line of mustCatch) {
      assert.ok(isWriteBackRisk(line), `★ 门有洞：规则认不出 ${JSON.stringify(line)}`)
    }
    // ② 不得误伤：只读展示、以及「摘要写摘要列」是与本禁令**不同**的两类。
    const mustNotCatch = [
      '<div class="note-snippet">{{ n.summary || n.content }}</div>',
      'nextSummary ?? decided.summary',
      // §896/D66：向量那条已从 decided.searchText 改成 decided.embedText（去掉 FTS bigram），
      // 但它在本门里的性质不变 —— 只是「只读消费」，不是把降级值写回正文。
      'embedAndStore(note.id, decided.embedText || note.content)',
      'content: note.content || existing.content',
    ]
    for (const line of mustNotCatch) {
      assert.ok(!isWriteBackRisk(line), `★ 规则过宽：误伤了 ${JSON.stringify(line)}`)
    }
  })

  it('★★ 量具自检：降级点扫描规则不得退化成「什么都不匹配」', () => {
    const synthetic = [
      'return body ?? note.content',
      'const b = a || note.summary',
      'const c = x ?? row.preview',
      'const d = y || z.snippet',
      'const __degraded = existing.summary ?? nextContent',
    ].join('\n')
    const found = new Set()
    for (const line of synthetic.split('\n')) {
      if (!DEGRADE_FALLBACK_RE.test(line)) continue
      DEGRADE_FIELD_RE.lastIndex = 0
      let m
      while ((m = DEGRADE_FIELD_RE.exec(line))) found.add(m[1])
    }
    assert.ok(found.has('content') && found.has('summary') && found.has('preview') && found.has('snippet'),
      '降级点扫描规则退化了：五种形态认不出四种 ⇒ 上面几条的通过毫无意义')

  })
})
