// note-ai-mirror.test.ts — 门禁：「录音后的 AI 总结必须真的能拿到笔记」。
//
// 这道门守的是一条**整条链路恒 404**的缺陷（2026-10-06 实测读盘发现）：
//
//   createNote() 只写本地 SQLite（localDB），id 由本地 newNoteId() 生成，
//   全仓**没有任何一处** POST /api/notes
//   （notesApi.create / update / list / get / remove / classify / search
//     的真实调用方数量逐个查过，全是 0）
//   ⇒ 后端 notes 表永远没有这条行
//   ⇒ handleNoteSummarize / handleNoteClassify 的第一步都是
//      GetByIDScoped(id) → 「404 note not found」
//
// 后果有两层，第二层才致命：
//   1. 旧的 `http('/api/notes/{id}/classify').catch(()=>{})` 把 404 吞掉，
//      分类静默失效，无人察觉；
//   2. **录音停止后的 AI 总结因此从来没成功过** —— 需求
//      「录音时即时总结」在随手记侧一直只是界面上的一个动作。
//
// 判据分三层：接线（必须先镜像再 summarize）、顺序（依赖关系不能反）、
// 负控（证明这两条不是恒真）。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const HERE = dirname(fileURLToPath(import.meta.url))
// 本文件在 features/notes/__tests__/ ⇒ 同级源码在上溯一级。
const read = (f) => readFileSync(join(HERE, '..', f), 'utf8')
// 仓库根：__tests__ → notes → features → src → frontend → <repo>
const readRepo = (f) =>
  readFileSync(join(HERE, '..', '..', '..', '..', '..', f), 'utf8')

// notesDecodeChain 取出「notes POST 的解码链」源码：
//   调用点（server_assistant.go 的 POST 分支）+ 实现（server_note_ingest.go 的
//   decodeNoteJSON）⇒ ⇒ **两段都要** ⇒ ⇒ 否则「解码搬到共用那一份」之后，
//   判据只看得到调用点、看不见**真解码** ⇒ ⇒⇒ 下面那条负控会**失去牙**。
const notesDecodeChain = () => {
  const goSrc = readRepo('backend/internal/server/server_assistant.go')
  const helperSrc = readRepo('backend/internal/server/server_note_ingest.go')
  const start = goSrc.indexOf('case http.MethodPost:')
  const callSite = goSrc.slice(start, start + 400)
  const i = helperSrc.indexOf('func decodeNoteJSON')
  const helper = i >= 0 ? helperSrc.slice(i) : ''
  return `${callSite}\n${helper}`
}

// notesDecodeCallRE 认「普通 Decode」的两种形态：
//   ① §941 之前：解码直接写在 POST 分支里（json.NewDecoder(r.Body).Decode(&n)）
//   ② §941 之后：分支里调**共用那一份** decodeNoteJSON(raw, &n)
// ⇒ ⇒ ⇒ 两种都认 ⇒ ⇒ ⇒ 将来实现再换写法，本门不该误报。
const notesDecodeCallRE =
  /(json\.NewDecoder\(r\.Body\)\.Decode\(&n\)|decodeNoteJSON\(raw, &n\))/

// notesDecodeIsPlainDecode 是这条前提判据的**可测函数**（供下面的负控调用）：
//   notes 的解码链上**没有** DisallowUnknownFields ⇒ ⇒ 字段名拼错会被**静默丢弃**
//   ⇒ ⇒ 所以前端那条「逐字段对齐」的断言才有必要。
const notesDecodeIsPlainDecode = (chain) => !/DisallowUnknownFields/.test(chain)

describe('A · 接线层：笔记必须先镜像到后端，AI 能力才有得可查', () => {
  it('createNote 必须 POST /api/notes 落行，且把 promise 登记给消费者', () => {
    const src = read('notes-persist.ts')
    assert.match(
      src, /http[<(][^\n]*['"`]\/api\/notes['"`]/,
      'createNote 路径上没有把笔记 POST 给后端 ⇒ 后端 notes 表永远没有这条行',
    )
    // 镜像体必须带上 id（后端据此才能被 GetByIDScoped 查到）与正文。
    assert.match(src, /id: note\.id/, '镜像体缺 id ⇒ 后端会用 randomID 另造一条，客户端 id 查不到')
    assert.match(src, /snippet: note\.content/, '镜像体缺 snippet ⇒ 总结与分类拿不到正文')
    // ★§168：上面那条只证明 snippet **存在**，没证明 2026-10-06 那次修复本身还在。
    //   `content` 与 `snippet` 是**两列**（backend store.go:107 `content := n.Content`、
    //   :259「正文与 snippet 是两列」），`Note.BodyForLLM` 优先读 Content。
    //   谁删掉 `content:` 这一行，本门**原本全绿** ⇒ 长语音笔记的后半段再也到不了
    //   后端的行动项抽取与 agent 检索，而没有任何报错。
    assert.match(
      src, /content: note\.content,/,
      '镜像体缺 content ⇒ 长语音笔记的后半段到不了后端（2026-10-06 的修复被回退了）',
    )
    // 负控：content 必须**不截断**，snippet 才截断。
    // 两者写反（content 也 slice）同样是静默缺陷，所以断言的不是「有两个字段」，
    // 而是「**哪些字段被截断**」。
    assert.doesNotMatch(
      src, /content: note\.content\.slice/,
      'content 被截断了 ⇒ 后端拿到的是摘要而非正文',
    )
    assert.match(
      src, /snippet: note\.content\.slice\(0, NOTE_MIRROR_SNIPPET_RUNES\)/,
      'snippet 的截断写法变了 ⇒ 与服务端 snippetRunes=200 的对齐失效',
    )
    // ★ 关键：**登记**而不只是「镜像函数存在」。
    //   只断言 `mirrorNoteToBackend(` 出现过，会被一个「调用了但没登记」
    //   的变异体蒙混过关 —— 那时 summarize 的 await 等的是 undefined，
    //   仍然 404，缺陷原样保留（实测 M1 变异最初就是从这条漏过去的）。
    const createNote = src.slice(src.indexOf('export async function createNote'),
                                 src.indexOf('export async function updateNote'))
    assert.match(
      createNote, /noteMirrorReady\.set\(/,
      'createNote 没有登记镜像 promise ⇒ awaitNoteMirror 拿到 undefined，/summarize 仍然 404',
    )
  })

  it('awaitNoteMirror 必须被 summarize 的调用方等待', () => {
    const src = read('NoteListView.vue')
    assert.match(src, /await awaitNoteMirror\(/, '录音收尾没有等镜像完成 ⇒ /summarize 必然 404')
    // 顺序：await 必须在 summarize 之前
    const iAwait = src.indexOf('await awaitNoteMirror(')
    const iSummarize = src.indexOf('notesApi.summarize(')
    assert.ok(iAwait >= 0 && iSummarize > iAwait,
      `awaitNoteMirror(${iAwait}) 必须在 summarize(${iSummarize}) 之前`)
  })

  it('镜像必须登记进 noteMirrorReady（否则 await 是空操作）', () => {
    const src = read('notes-persist.ts')
    assert.match(src, /noteMirrorReady\.set\(/, '镜像没有被登记 ⇒ awaitNoteMirror 拿不到 promise')
    assert.match(src, /export async function awaitNoteMirror/, 'awaitNoteMirror 未导出，调用方 import 不到')
  })

  it('classify 必须在镜像之后（顺序有依赖）', () => {
    const src = read('notes-persist.ts')
    const iMirror = src.indexOf('await http<{ id?: string }>(\'/api/notes\'')
    const iClassify = src.indexOf('/classify`')
    assert.ok(iMirror >= 0, '找不到镜像调用')
    assert.ok(iClassify > iMirror, 'classify 跑在镜像之前 ⇒ 拿到的仍是 404')
  })
})

describe('B · 后端契约：镜像的字段名必须与 notes.Note 对得上', () => {
  it('镜像体字段名必须逐个命中后端 Note 的 json tag', () => {
    const goStruct = readRepo('backend/internal/notes/note.go')
    const src = read('notes-persist.ts')
    // 后端 Note 只有 Snippet 一个内容字段（note.go:18 的注释就是这么写的）
    assert.match(goStruct, /Snippet\s+string\s+`json:"snippet"`/, '后端 Note 结构变了')
    for (const field of ['id', 'snippet', 'contentType', 'domain', 'tags', 'audioPath']) {
      // `json:"domain,omitempty" // 注释` —— struct tag 后可能跟行尾注释，
      // 所以只匹配到第一个引号，不锚定行尾。
      assert.match(goStruct, new RegExp(`json:"${field}[^"]*"`), `后端 Note 缺 json:"${field}"`)
      assert.ok(new RegExp(`\\b${field}:`).test(src), `镜像体缺字段 ${field}（Go 侧会静默丢弃未知字段）`)
    }
  })

  it('负控：Go 侧 DisallowUnknownFields 会让字段名拼错直接报错（证明字段名有牙）', () => {
    // decodeCalendarJSON 用了 DisallowUnknownFields；notes 侧是普通 Decode，
    // 所以「字段名拼错」在这条链路上**不会**报错而是静默丢失 —— 这正是
    // 必须逐字段对齐的原因。把这条前提钉成事实。
    //
    // ⚠ §941：notes POST 的解码**搬到了共用那一份**
    //   （backend/internal/server/server_note_ingest.go 的 decodeNoteJSON）
    //   ⇒ ⇒ 断言的**指向**跟着搬，但**语义一个字不改**：
    //   「notes 侧仍是普通 Decode（不是 DisallowUnknownFields）」。
    //   ⇒ ⇒ ⇒ 而且解码链必须**同时**包含调用点**与**实现 ——
    //   ⇒ ⇒ ⇒ 只看调用点的话，实现搬走之后这条负控会**失去牙**
    //   （它就看不见「真解码」里有没有 DisallowUnknownFields 了）。
    const chain = notesDecodeChain()
    assert.match(chain, notesDecodeCallRE,
      'notes POST 仍是普通 Decode（§941 起解码搬到了共用那一份 ⇒ 两种形态都认）')
    assert.ok(notesDecodeIsPlainDecode(chain),
      '若哪天 notes 改用 DisallowUnknownFields，本门这条负控需要重写（拼错会直接报错而不是静默丢失）')
  })
})

describe('C · 负控：证明判据不是恒真', () => {
  it('去掉镜像调用 ⇒ 接线层必须转红', () => {
    const withMirror = "await http('/api/notes', { method: 'POST', body: JSON.stringify({ id: note.id }) })"
    const withoutMirror = "await http(`/api/notes/${note.id}/classify`, { method: 'POST' })"
    assert.match(withMirror, /['"`]\/api\/notes['"`]/)
    assert.ok(!/['"`]\/api\/notes['"`]/.test(withoutMirror),
      '负控：只有 classify 的旧形态不该被接线判据匹配')
  })

  it('顺序反了（summarize 在 await 之前）必须被顺序判据抓住', () => {
    const correct = 'await awaitNoteMirror(id)\nconst r = await notesApi.summarize(id)'
    const reversed = 'const r = await notesApi.summarize(id)\nawait awaitNoteMirror(id)'
    const pos = (s, needle) => s.indexOf(needle)
    assert.ok(pos(correct, 'awaitNoteMirror') < pos(correct, 'summarize'), '正确顺序应被判过')
    assert.ok(pos(reversed, 'awaitNoteMirror') > pos(reversed, 'summarize'),
      '负控：顺序反了仍满足「await 存在」这条弱判据 —— 顺序判据必须独立承重')
  })

  // ★§168：下面三条以**真源码**为基线，而不是另写一份样本 ——
  //   样本与真源码不同形的话，负控证明的就不是「这条判据有牙」，
  //   而是「我另写的那个串有牙」（§149「负控必须与真源码同形」）。
  it('负控：删掉 content 那一行 ⇒ 正文契约判据必须转红', () => {
    const src = read('notes-persist.ts')
    assert.match(src, /content: note\.content,/, '前提：真源码此刻应当有 content 行')
    const mutated = src.replace(/^\s*content: note\.content,\s*$/m, '')
    assert.notEqual(mutated, src, '变异没落上 —— 锚点已过期，本负控失去意义')
    assert.ok(
      !/content: note\.content,/.test(mutated) && !/content: note\.content\.slice/.test(mutated),
      '负控：删掉 content 行后仍被判过 ⇒ 正文契约判据恒真',
    )
  })

  it('负控：content 也被截断 ⇒ 「不得截断」判据必须转红', () => {
    const src = read('notes-persist.ts')
    assert.doesNotMatch(src, /content: note\.content\.slice/)
    const mutated = src.replace(/content: note\.content,/, 'content: note.content.slice(0, 200),')
    assert.notEqual(mutated, src, '变异没落上 —— 锚点已过期')
    assert.match(mutated, /content: note\.content\.slice/,
      '负控：截断变异没造出来 ⇒ 本负控无效')
  })

  it('负控：snippet 不再截断 ⇒ snippet 契约判据必须转红', () => {
    const src = read('notes-persist.ts')
    assert.match(src, /snippet: note\.content\.slice\(0, NOTE_MIRROR_SNIPPET_RUNES\)/)
    const mutated = src.replace(
      /snippet: note\.content\.slice\(0, NOTE_MIRROR_SNIPPET_RUNES\)/, 'snippet: note.content,')
    assert.notEqual(mutated, src, '变异没落上 —— 锚点已过期')
    assert.ok(!/snippet: note\.content\.slice\(0, NOTE_MIRROR_SNIPPET_RUNES\)/.test(mutated),
      '负控：去掉 snippet 截断后仍被判过 ⇒ 与服务端 snippetRunes=200 的对齐没人守')
  })

  // ★§941：上面那条「Go 侧是普通 Decode」的前提判据，**指向**跟着解码搬家改了
  //   ⇒ ⇒ ⇒ 「改了指向」的判据必须**证明它仍有牙** ⇒ ⇒⇒
  //   ⇒ ⇒⇒ 否则就是「把门指向新位置」而不是「守住同一件事」
  //   ⇒ ⇒⇒ ⇒ 负控：给解码加上 DisallowUnknownFields ⇒ 前提判据必须转红。
  it('负控：给 notes 的解码加上 DisallowUnknownFields ⇒ 本门那条前提判据必须转红', () => {
    const chain = notesDecodeChain()
    assert.ok(notesDecodeIsPlainDecode(chain),
      '前提：真源码此刻应当**没有** DisallowUnknownFields')
    const mutated = chain.replace(
      'json.NewDecoder(bytes.NewReader(raw))',
      'json.NewDecoder(bytes.NewReader(raw))\n\tdec.DisallowUnknownFields()',
    )
    assert.notEqual(mutated, chain, '变异没落上 —— 锚点已过期，本负控失去意义')
    assert.match(mutated, /DisallowUnknownFields/, '负控：变异没造出来 ⇒ 本负控无效')
    assert.ok(!notesDecodeIsPlainDecode(mutated),
      '负控：加了 DisallowUnknownFields 后前提判据仍判过 ⇒ 本门那条前提恒真')
  })
})
