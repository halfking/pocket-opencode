// meeting-load-notice.test.ts — §837：会议详情页**读库失败**不得变成「永久骨架屏」。
//
// `load()` 此前是线性无保护的：
//     loading.value = true
//     const data = await getMeetingWithSegments(meetingId.value)   // ← 抛错
//     …
//     loading.value = false                                        // ← 永远到不了
// ⇒ ① `loading` 永久为 true ⇒ 整页永久骨架屏；② 一个未处理的 rejection；
//    ③ 零反馈（没有错误行、没有重试入口）。
//
// 而这不是罕见分支：local-db.ts 明确在「锁屏后回来 / 启动早于 init(dbSecret) /
// 切换工作区导致密钥不可用」时抛 `LocalDB 未初始化：…`。**同仓的 MeetingListView
// 对同一个失败已有出口**（dbNotReady → DbLockedState → 重新登录）⇒ 详情页是孤例。
//
// ⚠ 刻意不做的那件事：「catch 里写 loading.value = false」会把一次**读取失败**
//   变成模板第二层的**「会议不存在」** ⇒ 那是从「永久骨架屏」换成「谎报」。
//   ⇒ 必须有自己的分支，且位置排在 `!meeting` **之前**。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import {
  describeMeetingLoadFailure,
  isLocalDbUnavailable,
  MEETING_LOAD_FAIL_TAG,
} from '../meeting-load-notice.ts'

const HERE = dirname(fileURLToPath(import.meta.url))
const read = (rel: string) => readFileSync(join(HERE, rel), 'utf8')
const DETAIL = read('../MeetingDetailView.vue')
const LIST = read('../MeetingListView.vue')

const codeOnly = (text: string): string =>
  text.split('\n').filter((l) => {
    const t = l.trim()
    return !(t.startsWith('//') || t.startsWith('*') || t.startsWith('/*'))
  }).join('\n')

const fnBody = (code: string, name: string, max = 2600): string => {
  const at = code.indexOf(name)
  assert.ok(at > 0, `★ 阳性对照失败：找不到 ${name}`)
  const rest = code.slice(at + name.length, at + name.length + max)
  const next = rest.search(/\n(?:export )?(?:async )?function [A-Za-z_$]/)
  return next > 0 ? code.slice(at, at + name.length + next) : code.slice(at, at + name.length + max)
}

/** 模板区：取 <script 之前的部分（视图里到处是 <template v-else> 块，不能用 </template>）。 */
const templateOf = (src: string): string => {
  const i = src.search(/<script\b/)
  return i === -1 ? src : src.slice(0, i)
}

describe('§837 D1 · 判定（行为，纯函数）', () => {
  it('★★ 必须认得出本地库未就绪（那是「重新登录」而不是「重试」）', () => {
    assert.equal(isLocalDbUnavailable(new Error('LocalDB 未初始化：连接不存在')), true)
    assert.equal(isLocalDbUnavailable(new Error('LocalDB 未初始化，请先调用 init(dbSecret)')), true)
  })

  it('★★ 其它失败不得被误判成本地库不可用（否则用户会被送去重新登录却没用）', () => {
    for (const e of [new Error('SQLITE_BUSY'), new Error('disk full'), new Error('')]) {
      assert.equal(isLocalDbUnavailable(e), false, `误判：${e.message}`)
    }
  })

  it('★ 负控：null / undefined / 非 Error 都不得抛（它是判据，不是断言）', () => {
    for (const e of [null, undefined, 42, {}]) {
      assert.equal(typeof isLocalDbUnavailable(e), 'boolean', `形态 ${String(e)} 产出了非布尔值`)
    }
  })

  it('⚠ 与列表页保持同一判据（两处若分叉，会出现「列表能进、详情进不去」）', () => {
    assert.match(codeOnly(LIST), /msg\.includes\('LocalDB 未初始化'\)/,
      '列表页的判据形状变了 ⇒ 请同步 meeting-load-notice.ts 的 isLocalDbUnavailable')
  })
})

describe('§837 D1 · 文案（行为，纯函数）', () => {
  it('★★★ 两种失败必须是两句不同的话（用户动作不同：重登 vs 重试）', () => {
    const db = describeMeetingLoadFailure({ error: new Error('LocalDB 未初始化：连接不存在') })
    const other = describeMeetingLoadFailure({ error: new Error('SQLITE_BUSY') })
    assert.notEqual(db, other, '两种失败给了同一句文案')
    assert.match(db, /重新登录/, '本地库不可用时没让用户重新登录（那是唯一有效的动作）')
    assert.match(other, /重试/, '一般读取失败没给重试入口')
  })

  it('★★★ 本地库不可用时必须说清「数据没丢」（否则用户会以为会议丢了而重录）', () => {
    const db = describeMeetingLoadFailure({ error: new Error('LocalDB 未初始化：连接不存在') })
    assert.match(db, /没有丢|没丢/, '没说清会议数据仍在本地')
  })

  it('★★ 不得谎报「会议不存在」（那正是只写 loading=false 会造成的误导）', () => {
    for (const e of [new Error('LocalDB 未初始化：连接不存在'), new Error('SQLITE_BUSY'), null]) {
      const t = describeMeetingLoadFailure({ error: e })
      assert.doesNotMatch(t, /会议不存在|找不到会议/, `把读取失败说成了会议不存在：${t}`)
    }
  })

  it('负控：任意异常形态不得产出 undefined / [object Object] / NaN', () => {
    for (const e of [null, undefined, 42, {}, { message: 'boom' }]) {
      const t = describeMeetingLoadFailure({ error: e })
      assert.doesNotMatch(t, /undefined|\[object Object\]|NaN/, `异常形态 ${String(e)} 产出假读数：${t}`)
    }
  })

  it('★ 诊断前缀必须点名「读取会议失败」，与写入类前缀区分', () => {
    assert.match(MEETING_LOAD_FAIL_TAG, /读取会议失败/, '')
  })
})

// ── 接线（源码扫描，⚠ 对「信号变死代码」零牙，只保证真的接上了）──
describe('§837 D1 · 接线（扫描层，零牙已如实标注）', () => {
  const loadBody = (): string => fnBody(codeOnly(DETAIL), 'async function load()')

  it('★★★ loading 归零必须在 finally（否则抛错时那行永远到不了 ⇒ 永久骨架屏）', () => {
    const b = loadBody()
    assert.match(b, /finally \{/, 'load() 没有 finally ⇒ 抛错时 loading 永远为 true')
    // ⚠ 正控必须**以 finally 块的闭合为界**。第一版取「finally { 起 200 字符」，
    //   而那 200 字符越过了块尾，吃到了函数后面那一行 `loading.value = false`
    //   ⇒ 变异「把 finally 里那行删掉」照样全绿（实测 0 红）。
    //   （本会话第 4 次「窗口必须以语法边界为界」：§835 相邻函数、§836 块结构、此处块尾。）
    const at = b.indexOf('finally {')
    const close = b.indexOf('\n  }', at)
    assert.ok(close > at, '★ 阳性对照失败：找不到 finally 块的闭合')
    assert.match(b.slice(at, close), /loading\.value = false/,
      'finally 里没有 loading = false ⇒ 骨架屏仍会永久挂住')
  })

  it('★★★ 失败时不得清空已显示的会议（一次刷新失败不该抹掉正在看的正文）', () => {
    const b = loadBody()
    const catchAt = b.indexOf('catch (e)')
    assert.ok(catchAt > 0, '★ 阳性对照失败：load() 没有 catch')
    const tail = b.slice(catchAt)
    // 清空发生在 catch **之后**（成功路径）才是对的；catch 体内不得有它。
    const catchBody = tail.slice(0, tail.indexOf('} finally'))
    assert.doesNotMatch(catchBody, /meeting\.value\s*=|storedSegments\.value\s*=/,
      'catch 里仍在改 meeting/storedSegments ⇒ 失败一次就把用户正在看的内容清空')
  })

  it('★★★ 错误分支必须排在「会议不存在」之前（否则失败被谎报成会议没了）', () => {
    const tpl = templateOf(DETAIL)
    const atErr = tpl.indexOf('v-else-if="loadError && !meeting"')
    const atMissing = tpl.indexOf('v-else-if="!meeting"')
    assert.ok(atErr > 0, '★ 阳性对照失败：模板里没有读取失败的独立分支')
    assert.ok(atErr < atMissing, '错误分支排在「会议不存在」之后 ⇒ 读取失败会被显示成会议没了')
  })

  it('★★ 已有会议时失败必须降级为非破坏性横幅（不得接管整页）', () => {
    const tpl = templateOf(DETAIL)
    // 两档契约来自 list-error-failpath 那道门：无行可显才接管整页。
    assert.match(tpl, /v-else-if="loadError && !meeting"/, '整页接管没有「无行」守卫')
    assert.match(tpl, /<p v-if="loadError" class="err" role="alert">/,
      '没有行内横幅 ⇒ 已有会议时一旦读取失败，用户会直接失去正在看的内容')
  })

  it('★ 成功一次必须清掉上一次的错误（否则错误会一直挂着）', () => {
    assert.match(loadBody(), /loadError\.value = ''/, '成功后没有清掉 loadError')
  })
})

describe('§837 D2 · 标题/地点富化不得挡住自动开录（扫描层）', () => {
  it('★★★ captureContext 整体必须有兜住（它在 load 与自动开录之间）', () => {
    const b = fnBody(codeOnly(DETAIL), 'async function captureContext(')
    assert.match(b, /try \{/, 'captureContext 没有 try ⇒ 一次写库失败会中断 onMounted')
    assert.match(b, /catch/, 'captureContext 没有 catch')
  })

  it('★★ 富化失败必须继续往下走（不得把自动开录挡在门外）', () => {
    const mounted = fnBody(codeOnly(DETAIL), 'onMounted(async () => {', 2000)
    const atCapture = mounted.indexOf('await captureContext(m)')
    const atAuto = mounted.indexOf('if (auto) await onMicToggle()')
    assert.ok(atCapture > 0 && atAuto > atCapture,
      '★ 阳性对照失败：onMounted 里的顺序与前提不符（captureContext 在前、自动开录在后）')
    // 前提是「captureContext 自己吞掉失败」—— 由上一条钉住；这里钉住它确实是 async await。
    assert.match(mounted, /await captureContext\(m\)/, 'onMounted 不再等 captureContext ⇒ 顺序前提变了')
  })

  it('⚠ 前提登记：地理定位本身永不抛错（否则本节的前提就不成立）', () => {
    const meta = read('../meeting-meta.ts')
    const body = fnBody(codeOnly(meta), 'export async function captureDeviceLocation(')
    // 错误分支走 resolve(null) 而不是 reject / throw。
    assert.match(body, /\(\) => resolve\(null\)/, 'captureDeviceLocation 的失败分支不再 resolve(null) ⇒ 可能开始 reject，本节的前提失效')
    assert.doesNotMatch(body, /throw |reject\(/, 'captureDeviceLocation 变成了会抛错的实现 ⇒ 请重新判断 D2')
  })
})
