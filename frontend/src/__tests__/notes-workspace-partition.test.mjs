// notes-workspace-partition.test.mjs
//
// 调本地库笔记/PKM 读写函数时必须显式带上 workspaceId（或等价表达式）。
//
// ── 这条规则是怎么来的（真机实测，不是推测）──
//
// 2026-10-03 在 vivo V2436A / Android 16 上用 CDP 驱动真机 UI 新建笔记：
// 点「✓ 创建」→ 路由跳到 `#/notes` → 列表显示「还没有笔记」。控制台无异常、
// 无 unhandledrejection、INSERT 明明返回 `{"changes":1}`。
//
// 库是 SQLCipher 加密的，宿主侧 `run-as` 拉出来也读不了（file is not a
// database），所以把 Capacitor 的 JS→native 桥 `Capacitor.nativePromise`
// 挂钩，**抓到了应用真实发给 SQLite 的 SQL**，两条并排就是全部答案：
//
//   [run]   INSERT INTO local_notes … VALUES (?,?,…)
//           values: ["note-1790979672366-x9favk", "ws_user-admin", …]
//   [query] SELECT * FROM local_notes WHERE workspace_id = ? …
//           values: ["default", 50, 0]
//
// 写进 `ws_user-admin`，从 `default` 查——两个分区永远不 intersect，
// 于是「保存成功但列表里没有」。这跟抛异常没有任何关系，UI 上一切正常。
//
// 根因：`/notes` 这个**底部主 tab 根路由**挂的是 NotesHubView，它此前
// 完全没有 workspaceId 这个概念（文件里 0 处引用），listNotes / listPkmNotes
// 于是回退到 notes-persist 里的字面量默认值 'default'。写入方 NoteEditView
// 用的却是 auth.workspaceId（真机实测 ws_user-admin）。
//
// 同一次事故里 fcbd82e4 已经修过同一类问题（NoteListView 的 8 个调用点），
// 但那次是**人工扫仓库**做的，漏了本文件——因为 NoteListView 只挂在
// /notes/voice 下，不是用户实际点得到的那个页面。人工扫的东西必然漏，
// 所以把判据固化成静态卡口。
//
// ── 为什么必须解析 import，不能只按函数名匹配 ──
//
// 第一版只按名字扫，`services/flashcards.ts` 的 `createNote`/`deleteNote`
// 和 `stores/flashcards.ts` 的同名 dispatch 全被报出来——那是
// **flashcard 笔记**（`POST /api/flashcards/notes`），跟 local_notes 毫无
// 关系；`pkm-store.getNote(id)` 也被误伤，它压根不带 workspace 条件
// （`assetStore.get(id)` 全局按 id 查），不是「漏传」而是「本就不分区」。
// 一条会误伤的规则只有两种下场：被人加白名单，或者被人整个关掉。
// 所以这里按 **import 来源**判定：只有从 notes-persist/notes-store/pkm-store
// 引进来的同名函数才算数。
//
// 范围只收**确实按 workspace_id 分区**的函数：
//   · notes-persist / notes-store：createNote / updateNote / deleteNote /
//     getNote / listNotes / listDraftNotes / searchSemantic / searchHybrid
//   · pkm-store：listNotes / findByTitle / deleteNote
// **不收** emails-store.listEmails（按 account_id 分区，不是 workspace_id）、
// meetings-store.listMeetings（该表根本没有 workspace_id 列）、
// pkm-store.getNote（按 id 全局查，本就不分区）。把不分区的东西一并要求，
// 是把规则变成噪音。
//
// 判据不是「参数名必须叫 workspaceId」：updateNote / deleteNote / getNote 的
// workspaceId 是**第三个位置参数**（`updateNote(id, patch, workspaceId)`），
// 写成 `currentWorkspaceId()` 同样正确。所以只要求「整个调用表达式里出现
// 了 workspaceId 相关的标识符」——既拦住 `listNotes({ limit: 30 })` 这种
// 静默回退，又不误伤位置参数写法。

import assert from 'node:assert/strict'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { dirname, extname, join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..', '..')
const SRC = join(ROOT, 'src')
const SKIP = new Set(['node_modules', '.git', 'dist', '__tests__', 'android', 'ios'])

/** 本地库按 workspace_id 分区的模块 → 该模块里受约束的导出名。 */
const GUARDED_MODULES = {
  'notes-persist': new Set([
    'createNote', 'updateNote', 'deleteNote', 'getNote',
    'listNotes', 'listDraftNotes', 'searchSemantic', 'searchHybrid',
  ]),
  'notes-store': new Set([
    'createNote', 'updateNote', 'deleteNote', 'getNote',
    'listNotes', 'listDraftNotes', 'searchSemantic', 'searchHybrid',
  ]),
  'pkm-store': new Set(['listNotes', 'findByTitle', 'deleteNote']),
}

// 定义/再导出的本体不在被扫范围内：notes-store 只是 re-export，
// pkm-store/notes-persist 内部的相互调用是实现细节（那里 'default' 是
// 有意设的兜底默认值，不是「忘了传」）。
const DEFS = new Set([
  'features/notes/notes-persist.ts',
  'features/notes/notes-store.ts',
  'features/notes/notes-search.ts',
  'features/notes/notes-row.ts',
  'features/pkm/pkm-store.ts',
])

/** 调用里出现这些标识符就算「带上了 workspace」。 */
const WS_RE = /workspace_?[Ii]d|currentWorkspaceId/

function collect(dir, out = []) {
  for (const name of readdirSync(dir)) {
    if (SKIP.has(name)) continue
    const full = join(dir, name)
    if (statSync(full).isDirectory()) collect(full, out)
    else if (['.ts', '.vue'].includes(extname(name))) out.push(full)
  }
  return out
}

/**
 * 注释用等长空白替换（不是删除）。
 *
 * 判据跑在源码上，注释里的示例代码（useRealtimeList.ts 的 JSDoc 就写着
 * `await listNotes().then(setNotes)`）不该被当成真实调用点。但**必须等长**：
 * 删掉字符会让后面所有偏移前移，报出来的行号指到别处——一个指错位置的
 * 护栏比没有护栏更糟，因为它让人以为已经修好了。
 */
function stripComments(src) {
  const out = src.split('')
  let i = 0
  let q = null
  const blank = (from, to) => {
    for (let k = from; k < to; k++) if (out[k] !== '\n') out[k] = ' '
  }
  while (i < src.length) {
    const c = src[i]
    if (q) {
      if (c === '\\') i += 2
      else { if (c === q) q = null; i++ }
      continue
    }
    if (c === '"' || c === "'" || c === '`') { q = c; i++; continue }
    if (c === '/' && src[i + 1] === '/') {
      const from = i
      while (i < src.length && src[i] !== '\n') i++
      blank(from, i)
      continue
    }
    if (c === '/' && src[i + 1] === '*') {
      const from = i
      i += 2
      while (i < src.length && !(src[i] === '*' && src[i + 1] === '/')) i++
      i = Math.min(i + 2, src.length)
      blank(from, i)
      continue
    }
    i++
  }
  return out.join('')
}

/**
 * 建立「本地名 → (模块, 是否受约束)」表。
 *
 * `import { getNote, saveNote } from './pkm-store'` ⇒ getNote←pkm-store
 * `import { listNotes as listPkmNotes } from '../pkm/pkm-store'` ⇒ 别名也认。
 * `import * as notesStore from './notes-store'` ⇒ 前缀调用交给 `qualifierOf`。
 */
function importMap(src) {
  const map = new Map()
  const re = /import\s+(?:type\s+)?(?:(\*\s+as\s+\w+)|\{([^}]*)\}|(\w+))\s+from\s+['"]([^'"]+)['"]/g
  let m
  while ((m = re.exec(src)) !== null) {
    const [, ns, named, def, spec] = m
    const mod = spec.split('/').pop()
    const guarded = GUARDED_MODULES[mod]
    if (!guarded) continue
    if (ns) {
      map.set(ns.replace(/\*\s+as\s+/, '').trim(), { mod, qualified: true, guarded, exported: null })
    } else if (named) {
      for (const part of named.split(',')) {
        const t = part.replace(/\btype\b/g, '').trim()
        if (!t) continue
        const as = t.split(/\s+as\s+/)
        const exported = as[0].trim()
        const local = (as[1] ?? as[0]).trim()
        // 别名导入（listNotes as listPkmNotes）必须记**导出名**：
        // 受约束的判据是「从哪个模块导出了什么」，不是「本地叫什么」。
        if (exported) map.set(local, { mod, qualified: false, guarded, exported })
      }
    } else if (def) {
      map.set(def, { mod, qualified: false, guarded, exported: def })
    }
  }
  return map
}

/** `notesStore.listNotes(` ⇒ 'notesStore'；`listNotes(` ⇒ ''。 */
function qualifierOf(src, nameEnd) {
  let i = nameEnd
  while (i > 0 && /\s/.test(src[i - 1])) i--
  if (src[i - 1] === '.') {
    let j = i - 2
    while (j >= 0 && /[\w$]/.test(src[j])) j--
    return src.slice(j + 1, i - 1)
  }
  return ''
}

/** 从 `name(` 的左括号起做深度配对，取出整个调用表达式。 */
function callExpr(src, from) {
  let depth = 0
  for (let i = from; i < src.length; i++) {
    const ch = src[i]
    if (ch === '(') depth++
    else if (ch === ')') {
      depth--
      if (depth === 0) return src.slice(from, i + 1)
    } else if (ch === '`') {
      i++
      while (i < src.length && src[i] !== '`') i += src[i] === '\\' ? 2 : 1
    }
  }
  return null
}

/**
 * 单一标识符实参时，追一层它的声明。
 *
 * `await notesStore.createNote(payload)` 里 payload 是上面
 * `const payload = { …, workspaceId: currentWorkspaceId() }` 组出来的——
 * 这是完全正确且更易读的写法（NoteEditView 就是这么写的）。只看调用表达式
 * 会把它误报。追一层声明既放过这种写法，又不会放过
 * `createNote({ title })` 这种真漏传（声明里确实没有 workspaceId）。
 */
function declaredWithWorkspace(src, ident) {
  const re = new RegExp(`(?:const|let|var)\\s+${ident}\\s*(?::[^=]*)?=\\s*\\{`, 'g')
  let m
  while ((m = re.exec(src)) !== null) {
    const obj = braceObject(src, m.index + m[0].length - 1)
    if (obj && WS_RE.test(obj)) return true
  }
  return false
}

/** 从 `{` 起配对取出一个对象字面量。 */
function braceObject(src, from) {
  let depth = 0
  for (let i = from; i < src.length; i++) {
    if (src[i] === '{') depth++
    else if (src[i] === '}') {
      depth--
      if (depth === 0) return src.slice(from, i + 1)
    }
  }
  return null
}

/** 判断这一个调用是否「带上了 workspace」（直接写在实参里，或在实参变量的声明里）。 */
function carriesWorkspace(expr, src) {
  if (WS_RE.test(expr)) return true
  const inner = expr.replace(/^\(/, '').replace(/\)$/, '').trim()
  return /^[A-Za-z_$][\w$]*$/.test(inner) && declaredWithWorkspace(src, inner)
}

/** 在一段源码里找出所有「没带 workspaceId」的受约束调用点。 */
function offendersIn(rel, raw) {
  if (DEFS.has(rel)) return []
  const src = stripComments(raw)
  const imports = importMap(src)
  const out = []
  for (const [local, info] of imports) {
    for (const exported of info.qualified ? [...info.guarded] : [info.exported]) {
      if (!info.guarded.has(exported)) continue
      // 匹配用**本地名**（源码里实际写的那个），受约束判定用**导出名**。
      // 两者在 `import { listNotes as listPkmNotes }` 时不同：拿导出名去匹配
      // 会把 notes-store 那个裸 listNotes( 也算到 pkm-store 头上。
      const pattern = info.qualified ? `${local}\\.${exported}` : local
      const re = new RegExp(`(?<![\\w$])${pattern.replace(/\$/g, '\\$')}\\s*(?=\\()`, 'g')
      let m
      while ((m = re.exec(src)) !== null) {
        const open = src.indexOf('(', m.index)
        const expr = callExpr(src, open)
        if (expr === null) continue
        if (carriesWorkspace(expr, src)) continue
        const line = raw.slice(0, m.index).split('\n').length
        out.push(`${rel}:${line}  ${src.slice(m.index, m.index + pattern.length)} 未带 workspaceId（来自 ${info.mod} 的 ${exported}）`)
      }
    }
  }
  return out
}

function findOffenders(files) {
  return files.flatMap((file) =>
    offendersIn(relative(SRC, file).split('\\').join('/'), readFileSync(file, 'utf8')))
}

describe('按 workspace 分区的笔记读写必须显式带 workspaceId', () => {
  it('自查：检测器能抓出缺陷样本、放过正常样本与同名异域样本', () => {
    const at = (s) => offendersIn('probe.ts', s)

    // 缺陷样本 1：真机上出问题的那几行（NotesHubView 原文）
    const bad = `import { listNotes } from '../notes/notes-store'
import { listNotes as listPkmNotes } from '../pkm/pkm-store'
  const settled = await Promise.allSettled([
    listNotes({ limit: PAGE }),
    listPkmNotes({ limit: PAGE }),
  ])`
    // 缺陷样本 2：位置参数写法漏传第三参
    const bad2 = `import { deleteNote } from './notes-store'\nawait deleteNote(metaNote.value.id)`

    assert.equal(at(bad).length, 2,
      `缺陷样本必须被抓出 2 处，否则这条规则是空跑（实际：${JSON.stringify(at(bad))}）`)
    assert.equal(at(bad2).length, 1, '位置参数漏传也要被抓')

    // 正常样本 1：options 里带 workspaceId
    const good = `import { listNotes } from './notes-store'
const page = await listNotes({
  limit: 30,
  offset: 0,
  workspaceId: currentWorkspaceId(),
})`
    // 正常样本 2：位置参数写法
    const good2 = `import { updateNote } from './notes-store'
await updateNote(id, { summary }, currentWorkspaceId())`
    // 正常样本 3：命名空间导入同样受约束、同样能放行
    const good3 = `import * as notesStore from './notes-store'
await notesStore.listNotes({ workspaceId: wsId, limit: 30 })`
    // 正常样本 4：注释里出现不算数
    const inComment = `import { listNotes } from './notes-store'
/** 用法：refresh: async () => { await listNotes().then(setNotes) } */`
    // 正常样本 5：实参是上面组好的变量（NoteEditView 的真实写法）
    const viaVar = `import { createNote } from './notes-store'
const payload = {
  title: form.title.trim() || undefined,
  content: form.content.trim(),
  workspaceId: currentWorkspaceId(),
}
await createNote(payload)`
    // 缺陷样本 3：实参是变量，但变量里确实没有 workspaceId
    const varNoWs = `import { createNote } from './notes-store'
const payload = { title: 'x', content: 'y' }
await createNote(payload)`

    assert.deepEqual(at(good), [], '带 workspaceId 的不该被抓')
    assert.deepEqual(at(good2), [], '位置参数带上的不该被抓')
    assert.deepEqual(at(good3), [], '命名空间调用带上了就不该被抓')
    assert.deepEqual(at(inComment), [], '注释里的示例代码不该被抓')
    assert.deepEqual(at(viaVar), [], 'workspaceId 在实参变量声明里的不该被抓')
    assert.equal(at(varNoWs).length, 1, '追一层变量后仍漏传的要被抓')
  })

  it('同名但不同域的 flashcard 笔记不受本规则约束', () => {
    // services/flashcards.ts 的 createNote/deleteNote 打的是
    // POST /api/flashcards/notes，与 local_notes 无关；pkm-store.getNote
    // 按 id 全局查、本就不分区。第一版按名字扫时把它们全报了出来。
    const fs = `import { createNote, deleteNote } from './flashcards'
export async function createNote(i: FlashcardNoteInput) { return http(\`\${BASE}/notes\`, {}) }
deleteNote(id)`
    const pkm = `import { getNote } from './pkm-store'
async function load(id: string) { const note = await getNote(id); return note }`

    assert.deepEqual(offendersIn('services/flashcards.ts', fs), [], 'flashcard 笔记不是 workspace 分区')
    assert.deepEqual(offendersIn('features/pkm/PkmEditor.vue', pkm), [], 'pkm.getNote 本就不分区')
  })

  it('src 下没有漏传 workspaceId 的笔记/PKM 调用点', () => {
    const offenders = findOffenders(collect(SRC))
    assert.deepEqual(
      offenders,
      [],
      '这些调用会静默回退到字面量 \'default\' 分区。写入方用的是 auth.workspaceId，'
        + '两边不 intersect ⇒ 真机表现是「保存成功但列表永远为空」，且不报任何错：\n  '
        + offenders.join('\n  '),
    )
  })
})
