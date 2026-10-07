// note-draft-persisted-before-transcribe.test.mjs
//
// 锁住「兜底转写之前必须先落草稿」以及「早落库之后不得再建第二条」。
//
// 2026-10-06 真机三臂对照（唯一变量 = 谁终结了 stopping 窗口）：
//   臂1 真实后端自然跑完        local_notes 11→12
//   臂2 桩挂住 + 点「停止转写」  local_notes 12→13，音频 200364B 在盘上
//   臂3 桩挂住 + am force-stop   local_notes 13→13，**盘上零新目录**
// 复现：轮 2、轮 3 均「对照 +1条/1目录、实测 +0条/0目录」，每轮杀之前都读到
//       桩 inflight=1（请求确实在飞行中，不是杀早了）。
//
// 成因（recordingRuntime.ts 的 stop 顺序）：
//   :945 cleanupMedia()  释放麦克风
//   :946 audioBlob = new Blob(this.chunks)   录音此刻只在内存
//   :988 await withTimeout(transcribeFull(...), 10*60_000)   最长 10 分钟
//   :1019 pendingResult = {...}   到这里才交给界面
// ⇒ 整个 stopping 窗口内音频只活在 WebView 堆的一个变量里，进程死即蒸发。
// 而窗口是**最长 10 分钟**：用户没电 / 划掉应用 / 被 MIUI SmartPower 回收
// （这台设备上非常频繁）都很容易落在这个窗口里。
//
// 修法：草稿在**分片收尾之后、兜底转写之前**落库，转写有结果后回填正文。
//   · 放分片之前 ⇒ 正文停在「最后一句还没回来」，而分片出字时兜底**不会**跑，
//     也就没人回填 ⇒ 笔记少了最后一句。
//   · 放兜底之前 ⇒ 10 分钟窗口被消掉。
//
// 早落库的代价：「建草稿」有两个候选点，不加 draftId 判据就会一次录音留两条。
// 所以本门同时锁两件事：顺序 + 不重复建。
//
// 末尾两个负控分别把这两件事各打回一次，本门必须报红。

import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const SRC = path.resolve(HERE, '..', '..', '..')
const RT = path.join(SRC, 'native', 'recordingRuntime.ts')
const VIEW = path.join(SRC, 'features', 'notes', 'NoteListView.vue')
const HOOK = path.join(SRC, 'features', 'notes', 'useNoteRecording.ts')

export function stripComments(src) {
  return src
    .replace(/\/\*[\s\S]*?\*\//g, (m) => m.replace(/[^\n]/g, ' '))
    .replace(/(^|[^:])\/\/[^\n]*/g, (m, p1) => p1 + ' '.repeat(Math.max(0, m.length - p1.length)))
}

export function fnBody(src, decl) {
  const start = src.indexOf(decl)
  if (start < 0) return null
  const open = src.indexOf('{', start)
  if (open < 0) return null
  let depth = 0
  for (let i = open; i < src.length; i++) {
    if (src[i] === '{') depth++
    else if (src[i] === '}') { depth--; if (depth === 0) return src.slice(open, i + 1) }
  }
  return null
}

/**
 * 返回「先落库」顺序违规描述；null = 合规。
 * 抽出成函数，好让负控对**同一段检测逻辑**跑变异。
 */
export function findEarlyPersistViolation(body) {
  if (!body) return 'runStop() 函数体没找到（门形同虚设，必须先修这个）'
  const sliceAt = body.indexOf('this.sliceChain')
  const createAt = body.indexOf('voiceDraftSink.create(')
  const fullAt = body.indexOf('transcribeFull(')
  const updateAt = body.indexOf('update(draftId')

  if (sliceAt < 0) return 'runStop() 里没有分片收尾的 sliceChain 等待，前提变了'
  if (fullAt < 0) return 'runStop() 里找不到 transcribeFull(，前提变了'
  if (createAt < 0) return 'runStop() 里没有 voiceDraftSink.create(：兜底转写期间录音仍然只在内存里'
  if (sliceAt > createAt) {
    return '草稿落库出现在分片收尾之前：正文会停在「最后一句还没回来」的状态，' +
      '而分片出字时兜底整段转写不会跑，也就没人回填 ⇒ 笔记少最后一句'
  }
  if (createAt > fullAt) {
    return '草稿落库晚于 transcribeFull(：兜底转写是 10 分钟级 await，' +
      '落库在它之后就等于没修 —— 进程死在窗口里录音照样蒸发'
  }
  if (updateAt < 0) {
    return '没有把转写结果回填给已落库草稿的 update(draftId …)：' +
      '落库那一刻正文是「（语音草稿）」，用户永远拿不到转写文字'
  }
  if (updateAt < fullAt) {
    return '回填写在转写之前：那是在回填还没产生的结果'
  }
  return null
}

/** 返回「会重复建笔记」的描述；null = 合规。 */
export function findDoubleCreateViolation(fnBodySrc, fnName) {
  if (!fnBodySrc) return `${fnName}() 函数体没找到（门形同虚设）`
  if (!/draftId/.test(fnBodySrc)) {
    return `${fnName}() 没有判 draftId：运行时早落库时它会再建一次，一次录音留两条笔记`
  }
  if (!/adoptVoiceDraft\(/.test(fnBodySrc)) {
    return `${fnName}() 见到 draftId 却没走 adoptVoiceDraft()，草稿被重复创建`
  }
  return null
}

const read = (p) => fs.readFileSync(p, 'utf8')
const runStopBody = () => fnBody(stripComments(read(RT)), 'private async runStop(')
const viewFn = (name) => {
  const raw = read(VIEW)
  const s = raw.indexOf('<script setup')
  const e = raw.lastIndexOf('</script>')
  const scoped = stripComments(s >= 0 && e > s ? raw.slice(s, e) : raw)
    .replace(/import\s[\s\S]*?from\s+['"][^'"]*['"];?/g, (m) => ' '.repeat(m.length))
  return fnBody(scoped, `async function ${name}(`)
}

describe('兜底转写之前先落草稿', () => {
  it('顺序：分片收尾 < 落库 < 兜底转写 < 回填', () => {
    assert.equal(findEarlyPersistViolation(runStopBody()), null)
  })

  it('运行时提供了 sink 注册口，且特性层真的注册了', () => {
    const rt = read(RT)
    assert.match(rt, /registerVoiceDraftSink\s*\(/, '运行时没有 registerVoiceDraftSink()')
    const hook = read(HOOK)
    assert.match(hook, /registerVoiceDraftSink\(/, 'useNoteRecording 没有注册落库出口，早落库永远不发生')
    assert.match(hook, /createNote\(/, 'sink 没用 notesStore.createNote 落库')
    // 回填必须只改文字、不传 media —— 否则会触发 deleteNoteFiles 把音频删掉。
    // 只要求 content 是「带冒号的键」：`{ content: text }` 里 content 与 } 之间
    // 还有 `: text`，写 `\{\s*content\s*\}` 会漏（第一版就这么写，当场误报）。
    assert.match(hook, /updateNote\([^)]*\{\s*content\s*:/s, '回填必须只传 content')
    assert.doesNotMatch(hook, /updateNote\([^)]*\bmedia\b/s, '回填传了 media：会走「换媒体」分支，音频有被删的风险')
  })

  it('页面在场路径不会建第二条笔记', () => {
    assert.equal(findDoubleCreateViolation(viewFn('onMicToggle'), 'onMicToggle'), null)
  })

  it('跨页拾取路径不会建第二条笔记', () => {
    assert.equal(findDoubleCreateViolation(viewFn('pickupPendingRecording'), 'pickupPendingRecording'), null)
  })

  it('前提：兜底转写确实还是 10 分钟级（这条缺陷的窗口才有意义）', () => {
    const body = read(RT)
    assert.match(body, /10\s*\*\s*60_000/, 'transcribeFull 的超时上限不再是 10 分钟，窗口大小变了，需重评')
  })
})

// ---------------------------------------------------------------------------
// 负控：各打回一次，本门必须报红。
// ---------------------------------------------------------------------------
describe('负控：把顺序与去重各打回一次，必须报红', () => {
  it('变异体（落库挪到 transcribeFull 之后）被判为违规', () => {
    const body = runStopBody()
    assert.ok(body, '拿不到 runStop() 函数体，负控无法执行')
    assert.ok(body.includes('voiceDraftSink.create('), '★ 变异前提不成立：源码里已经没有落库调用了')
    // 把整段 create 块搬到兜底转写之后：这就是「没修」的样子
    const at = body.indexOf('voiceDraftSink.create(')
    const openAt = body.lastIndexOf('{', at)
    let depth = 0, end = -1
    for (let i = openAt; i < body.length; i++) {
      if (body[i] === '{') depth++
      else if (body[i] === '}') { depth--; if (depth === 0) { end = i + 1; break } }
    }
    assert.ok(end > 0, '定位失败：create 块括号不配对')
    const block = body.slice(openAt, end)
    const without = body.slice(0, openAt) + body.slice(end)
    const anchor = without.indexOf('this.pendingResult =')
    assert.ok(anchor > 0, '定位失败：找不到 pendingResult 赋值点')
    const mutated = without.slice(0, anchor) + block + '\n      ' + without.slice(anchor)

    const violation = findEarlyPersistViolation(mutated)
    assert.ok(violation, '★ 门有洞：把落库挪到转写之后竟然判为合规')
    assert.match(violation, /晚于 transcribeFull|蒸发/)
  })

  it('变异体（去掉 draftId 去重判据）被判为违规', () => {
    const body = viewFn('onMicToggle')
    assert.ok(body, '拿不到 onMicToggle() 函数体，负控无法执行')
    assert.ok(body.includes('draftId'), '★ 变异前提不成立：源码里已经没有 draftId 判据了')
    const mutated = body
      .replace(/if\s*\(\s*stopped\.draftId\s*\)\s*await\s*adoptVoiceDraft\([^)]*\)\s*\n?\s*else\s*/, '')
    const violation = findDoubleCreateViolation(mutated, 'onMicToggle')
    assert.ok(violation, '★ 门有洞：去掉去重判据竟然判为合规')
    assert.match(violation, /再建一次|重复创建/)
  })
})
