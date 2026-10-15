// refine-meta-parity.test.mjs — §85：前端 meta 字段名必须与 Go 的 json tag **逐条对齐**。
//
// 为什么需要这一半：Go 侧 refine_meta_wire_test.go 证明「前端那份 JSON 能解出值」，
// 但那份 JSON 是**手抄**进测试的。若前端把 `title` 改成 `meetingTitle`，
// Go 那半边照样全绿（它测的是抄进去的那份），而真实请求已经发不出这个字段了。
//
// ⇒ 两半各管一侧：Go 半边验「形状能解」，本文件验「名字对得上」。
//
// 同族判据形态见 §66.4 的 summary-prompt-parity.test.mjs（同一套路）。
import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { describe, it } from 'node:test'
import { fileURLToPath } from 'node:url'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const ROOT = path.resolve(HERE, '..', '..', '..')
const read = (rel) => fs.readFileSync(path.resolve(ROOT, rel), 'utf8')

const GO_SRC = read('../backend/internal/server/server_meeting.go')
const TS_API = read('src/api/meetings.ts')

/** Go 侧：meetingMetaIn 的 `json:"xxx"` tag 集合。 */
function goMetaTags() {
  const block = /type meetingMetaIn struct \{([\s\S]*?)\n\}/.exec(GO_SRC)
  assert.ok(block, '解析不到 meetingMetaIn —— 判据失效，先修判据')
  return [...block[1].matchAll(/json:"([^",]+)"/g)].map((m) => m[1]).sort()
}

/**
 * 前端侧：**refine()** 的 meta 参数类型里的键集合。
 *
 * ⚠⚠ 第一版是 `/meta\?:\s*\{([^}]*)\}/.exec(TS_API)` —— 直接取**文件里第一个**
 *   `meta?:`，而那是 **`summarize()`** 的签名（它在 117 行，refine 在 167 行）。
 *   `summarize` 的 meta 三年前就没变过，两边恰好相同，所以这道门一直
 *   **绿在测错的那个签名上**：refine 真少一个字段它照样全绿。
 *   ⇒ 这是又一种「判据恒真」的成因：**测的对象与声明的对象不是同一个**。
 *
 *   ⇒ 现在必须锚在 `async refine(` 之后，且配套一条**自证 + 负控**（见下）。
 *     ⚠ 不要顺手给 `summarize()` 也加 `topic`：`metaTermHint` 全仓只有一个
 *     调用点（服务端 `buildRefinePrompt`），`buildSummaryPrompt(transcript, prev)`
 *     **根本不接 meta** ⇒ 加过去就是「只声明不读」，正是本会话反复修的那个形状。
 */
function tsMetaKeysFrom(src) {
  const at = src.indexOf('async refine(')
  assert.ok(at > 0, '解析不到 refine() —— 判据失效，先修判据')
  const m = /meta\?:\s*\{([^}]*)\}/.exec(src.slice(at))
  assert.ok(m, '解析不到 refine() 的 meta 参数类型 —— 判据失效，先修判据')
  return [...m[1].matchAll(/(\w+)\??\s*:/g)].map((x) => x[1]).sort()
}

function tsMetaKeys() {
  return tsMetaKeysFrom(TS_API)
}

// ⚠ 必须与两侧一致地**排序后**比较：本节两侧都做了 .sort()，
//   常量若按声明顺序写就会恒不相等（§85.4 记的就是这一下）。
// ★ §143（主题接入）：`topic` 是**有意新增**的字段，不是漂移。
//   这张表就是这道门的登记表 —— 加字段必须来改它，
//   改完之后两侧仍要逐条相等（少一个 / 改一个名照样红）。
const FIELDS = ['location', 'participants', 'title', 'topic']

describe('§85 · 跨语言契约：前端 meta 字段名 ↔ Go json tag', () => {
  it('★ 四个字段名两侧逐条相等（少一个 / 改一个名都要红）', () => {
    const go = goMetaTags()
    const ts = tsMetaKeys()
    assert.deepEqual(go, FIELDS, `Go 侧字段变成了 ${JSON.stringify(go)}`)
    assert.deepEqual(ts, FIELDS, `前端字段变成了 ${JSON.stringify(ts)}`)
  })

  it('负控：任一侧少一个字段时，上面那条必须报红', () => {
    const brokenGo = 'type meetingMetaIn struct {\n\tTitle string `json:"title"`\n}\n'
    const got = [...brokenGo.matchAll(/json:"([^",]+)"/g)].map((m) => m[1]).sort()
    assert.notDeepEqual(got, FIELDS, '样本构造失败：少字段却仍判定相等')
  })

  it('量具自证：抽取的确实是 refine() 的 meta，不是文件里第一个 meta?', () => {
    // 负控样本：summarize 的键与 refine 的键**故意不同**。
    // 若抽取器取错签名，这里会取到 summarize 的键并红。
    const fake =
      'async summarize(id, segs, meta?: { alpha?: string }, signal?: AbortSignal) {}\n' +
      'async refine(id, segs, langs, meta?: { beta?: string }, signal?: AbortSignal) {}\n'
    assert.deepEqual(tsMetaKeysFrom(fake), ['beta'], '抽取器取错了签名（拿了 summarize 的）')
    // 正控：真源码里 refine 必须在 summarize 之后，且键集要含 topic。
    assert.ok(TS_API.indexOf('async summarize(') < TS_API.indexOf('async refine('),
      '本仓里 summarize 先于 refine —— 负控样本的前提变了，先看判据')
  })

  it('负控：refine() 的 meta 少一个字段时，上面那条必须报红', () => {
    const fake =
      'async summarize(id, segs, meta?: { title?: string; participants?: string[]; location?: string }, s?: AbortSignal) {}\n' +
      'async refine(id, segs, langs, meta?: { title?: string; participants?: string[] }, s?: AbortSignal) {}\n'
    assert.notDeepEqual(tsMetaKeysFrom(fake), FIELDS,
      '样本构造失败：refine 少了字段却仍判定相等')
  })

  it('前端 refine() 确实把 meta 放进请求体（不是只声明了类型）', () => {
    const body = /body:\s*JSON\.stringify\(\{([\s\S]*?)\}\)/.exec(TS_API)
    assert.ok(body, '解析不到 refine() 的请求体')
    assert.match(body[1], /\bmeta\b/, '请求体里没有 meta ⇒ 声明了类型却没发出去')
  })
})

/** Go 侧：meetingSegmentIn 的 `json:"xxx"` tag 集合（已排序）。 */
function goSegmentTags() {
  const block = /type meetingSegmentIn struct \{([\s\S]*?)\n\}/.exec(GO_SRC)
  assert.ok(block, '解析不到 meetingSegmentIn —— 判据失效，先修判据')
  return [...block[1].matchAll(/json:"([^",]+)"/g)].map((m) => m[1]).sort()
}

/** 前端侧：toApiSegments 映射出的对象字面量的键（已排序）。 */
function tsSegmentKeys() {
  // 取 `return segments.map((s) => ({ … }))` 里那个对象字面量，避开签名上的 `segments:`
  const m = /\(\s*\{([\s\S]*?)\}\s*\)\s*\)/.exec(TS_API.slice(TS_API.indexOf('function toApiSegments')))
  assert.ok(m, '解析不到 toApiSegments 的映射对象 —— 判据失效，先修判据')
  return [...m[1].matchAll(/(\w+)\s*:/g)].map((x) => x[1]).sort()
}

describe('§90 · 跨语言契约：精校请求的 segments 与 target_langs', () => {
  // 与 §85 的 meta 同理：字段名一旦漂移，Go 侧会**安静地**收到空分段
  // （`json` 解码对未知字段不报错、对缺失字段留零值）⇒ 无任何门会响。

  it('★ segments 的五个字段名两侧逐条相等', () => {
    const want = ['end_ms', 'lang', 'speaker', 'start_ms', 'text']
    assert.deepEqual(goSegmentTags(), want, 'Go 侧 meetingSegmentIn 的字段变了')
    assert.deepEqual(tsSegmentKeys(), want, '前端 toApiSegments 的字段变了')
  })

  it('★ speaker 必须真的取自 speakerLabel（不是硬编码字面量）', () => {
    const body = TS_API.slice(TS_API.indexOf('function toApiSegments'))
    assert.match(body, /speaker:\s*s\.speakerLabel/,
      'speaker 没取自 speakerLabel ⇒ 每段都会被标成同一个人，说话人归因失效')
  })

  it('★ target_langs 两侧同名（前端发出、Go 接收）', () => {
    assert.match(TS_API, /\btarget_langs:\s*targetLangs/, '前端请求体里没有 target_langs')
    assert.match(GO_SRC, /TargetLangs\s+\[\]string\s+`json:"target_langs"`/,
      'Go 侧没有 target_langs 字段')
  })

  it('负控：字段改名/少字段时上面几条必须报红', () => {
    const brokenGo = 'type meetingSegmentIn struct {\n\tSpeaker string `json:"speaker"`\n}\n'
    const got = [...brokenGo.matchAll(/json:"([^",]+)"/g)].map((m) => m[1]).sort()
    assert.notDeepEqual(got, ['end_ms', 'lang', 'speaker', 'start_ms', 'text'],
      '样本构造失败：少字段却仍判定相等')

    const mutated = TS_API.replace('target_langs: targetLangs', 'targetLang: targetLangs')
    assert.notEqual(mutated, TS_API, '样本构造失败：锚点没命中')
    assert.ok(!/\btarget_langs:\s*targetLangs/.test(mutated),
      '判据没咬住：已改名却仍判定通过')
  })
})
