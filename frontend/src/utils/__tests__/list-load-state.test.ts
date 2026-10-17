// list-load-state.test.ts — §840：列表页**首屏读取失败**不得被显示成「还没有记录」。
//
// 旧模板（会议 / 笔记同形）：
//     <div v-if="loading"><Skeleton/></div>
//     <EmptyState v-else-if="meetings.length === 0" title="还没有会议记录"
//                 hint="点击右下角麦克风开始第一次会议" />
//     <div v-else> …列表… <div v-if="meetings.length > 0"> …重试按钮… </div> </div>
//
// `loading` 是 `status==='idle' && 行数===0`。首屏读失败时 `status` 变 `'failed'`
// ⇒ loading=false、行数 0 ⇒ **正好落进真空态**，而重试按钮在 `行数>0` 分支里永不渲染。
// ⇒ 用户已有 10 场会议，看到「一场都没有，请开始第一次」，并被引导去新建一场。
//
// ⚠ 更深一层：`dbNotReady → DbLockedState`（本地库未就绪 ⇒ 重新登录）**根本不可达** ——
//   `resetQuery()` 返回 void 不抛，控制器链上自带 `.catch` 吞掉 fetch 错误，
//   所以视图层的 try/catch 永远收不到 e.message。两种失败在界面上完全同形。
//   取证读数：`.artifacts/s840-probe/probe.mjs`（两种失败 status/行数/dbNotReady 三项全同）。
//   ⇒ 错误**只能在视图自己的 fetchPage 内部**拦截，那里才看得见 e.message。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import {
  isListReadError,
  isListSkeleton,
  listReadFailureNotice,
  isLocalDbUnavailable,
  LIST_READ_FAIL_TAG,
} from '../list-load-state.ts'
import { isLocalDbUnavailable as reexportedFromMeetings } from '../../features/meetings/meeting-load-notice.ts'

const HERE = dirname(fileURLToPath(import.meta.url))
const read = (rel: string) => readFileSync(join(HERE, rel), 'utf8')
const MEETINGS = read('../../features/meetings/MeetingListView.vue')
const NOTES = read('../../features/notes/NoteListView.vue')

const VIEWS = [
  { name: 'MeetingListView', src: MEETINGS, rows: 'meetings', emptyCond: 'v-else-if="meetings.length === 0"' },
  { name: 'NoteListView', src: NOTES, rows: 'filteredNotes', emptyCond: 'v-else-if="filteredNotes.length === 0"' },
] as const

/** 剥掉整行注释。⚠ 只挡整行；行尾注释仍可能命中（§833 起累计多次翻车）。 */
const codeOnly = (text: string): string =>
  text.split('\n').filter((l) => {
    const t = l.trim()
    return !(t.startsWith('//') || t.startsWith('*') || t.startsWith('/*'))
  }).join('\n')

/** 模板区：取 <script 之前的部分。 */
const templateOf = (src: string): string => {
  const i = src.search(/<script\b/)
  return i === -1 ? src : src.slice(0, i)
}

/**
 * 从锚点起取到**括号配平**为止的整块。
 *
 * ⚠ 第一版用 `/^\s*\}\)/m` 找边界，结果它先撞上 `listMeetings(size, { … })` 的收尾
 *   `})` ⇒ 取到的块在 `catch` 之前就断了 ⇒ 后面 8 条判据一起假红。
 *   ⇒ **边界必须由语法（括号配平）界定，不能用正则猜形状。**
 *
 * ⚠ 第二版仍错：锚点后**第一个** `{` 是参数解构 `({ page })` 的括号，
 *   配平立刻归零 ⇒ 只取到 26 个字符。
 *   ⇒ 必须先越过 `=>` 再取函数体的 `{`。
 */
const blockFrom = (code: string, anchor: string, max = 4000): string => {
  const at = code.indexOf(anchor)
  assert.ok(at > 0, `★ 阳性对照失败：找不到锚点 ${anchor}`)
  const arrow = code.indexOf('=>', at + anchor.length)
  assert.ok(arrow > 0, `★ 阳性对照失败：锚点 ${anchor} 之后没有 '=>'`)
  const open = code.indexOf('{', arrow)
  assert.ok(open > 0, `★ 阳性对照失败：锚点 ${anchor} 的箭头函数没有函数体`)
  let depth = 0
  for (let i = open; i < code.length && i < open + max; i += 1) {
    if (code[i] === '{') depth += 1
    else if (code[i] === '}') {
      depth -= 1
      if (depth === 0) return code.slice(at, i + 1)
    }
  }
  return code.slice(at, at + max)
}

/** fetchPage 的函数体（已剥整行注释）。 */
const fetchPageOf = (src: string): string => codeOnly(blockFrom(src, 'fetchPage: async'))

// ── 判据本体：断言与变异**共用同一把尺子** ────────────────────────────────
// （变异去测「另一份写法的正则」等于没测；那样 0 红无法区分「判据有洞」与「变异无效」。）

/** 模板里有「读失败 + 无行守卫」这条分支。 */
const hasGuardedBranch = (tpl: string): boolean =>
  /v-else-if="readError && [^"]*\.length === 0"/.test(tpl)

/** 读失败分支排在真空态分支之前（v-else-if 链只认第一个为真的）。 */
const orderOk = (tpl: string, emptyCond: string): boolean => {
  const atError = tpl.indexOf('readError &&')
  const atEmpty = tpl.indexOf(emptyCond)
  return atError > 0 && atEmpty > atError
}

/** 失败分支带可点重试入口。 */
const hasRetryEntry = (tpl: string): boolean => {
  const at = tpl.indexOf('readError &&')
  if (at < 0) return false
  const close = tpl.indexOf('>', at)
  if (close < 0) return false
  const branch = tpl.slice(at, close)
  return /@action="retry"/.test(branch) && /:action-label="readFailure\.action"/.test(branch)
}

/** fetchPage 看得见 e.message，且能判出「本地库未就绪」。 */
const seesErrorObject = (fp: string): boolean =>
  /catch\s*\(e:\s*unknown\)/.test(fp) && /msg\.includes\('LocalDB 未初始化'\)/.test(fp)

/** fetchPage 的 catch 让错误继续往上抛（返回空结果会被当成成功）。 */
const rethrows = (fp: string): boolean =>
  /throw\s+e\s*;?\s*$/m.test(fp) && !/catch[\s\S]*?return\s*\{\s*rows:\s*\[\]/.test(fp)

/** readError 接在共享判定上，而不是各页自己写一套。 */
const usesSharedPredicate = (src: string): boolean =>
  /isListReadError\(listStatus\.value,/.test(codeOnly(src))

describe('§840 · 判定：什么算「读失败」', () => {
  it('★★ 无行 + failed ⇒ 是读失败（这正是被修掉的那个落点）', () => {
    assert.equal(isListReadError('failed', 0), true)
  })

  it('★★★ 有行 + failed ⇒ **不是**：控制器在失败时保留已加载行，有行时只做行内重试', () => {
    assert.equal(isListReadError('failed', 12), false,
      '误判成整页错误态会把已经显示的行换掉 —— 那是 list-error-failpath 那道门管的反面')
  })

  it('★★ 其它状态 + 无行 ⇒ 不是（真空态必须是真空态）', () => {
    for (const s of ['idle', 'exhausted', 'paused'] as const) {
      assert.equal(isListReadError(s, 0), false, `${s}/0 被误判成读失败`)
    }
  })

  it('★ 负控：畸形入参不得抛（它是判据，不是断言）', () => {
    assert.doesNotThrow(() => isListReadError(undefined as never, 0))
    assert.doesNotThrow(() => isListReadError('failed' as never, undefined as never))
  })
})

describe('§840 · 判定：什么算「还在加载」', () => {
  it('★★★ refreshing/loadingNext + 无行 ⇒ 仍是骨架屏（旧模板在这里会闪一下真空态）', () => {
    // 首屏 load() 会同步把 status 从 idle 翻成 refreshing ⇒ 旧 loading 立刻为 false，
    // 行数仍 0 ⇒ 「还没有会议记录」在请求发出前就闪出来。
    assert.equal(isListSkeleton('refreshing', 0, false), true)
    assert.equal(isListSkeleton('loadingNext', 0, false), true)
  })

  it('★★ 有行 / 本地库未就绪 ⇒ 不是骨架屏（那两态各自有更贴切的界面）', () => {
    assert.equal(isListSkeleton('idle', 3, false), false)
    assert.equal(isListSkeleton('idle', 0, true), false)
  })

  it('★★ 读完且为空 ⇒ 不是骨架屏（否则真空态永远轮不到）', () => {
    assert.equal(isListSkeleton('exhausted', 0, false), false)
    assert.equal(isListSkeleton('paused', 0, false), false)
  })
})

describe('§840 · 文案', () => {
  it('★★★ 不得断言数据状态（读失败时我们并不知道记录还在不在）', () => {
    const n = listReadFailureNotice('会议记录')
    for (const forbidden of ['没有丢', '已丢失', '数据丢失', '不存在']) {
      assert.ok(!n.hint.includes(forbidden), `文案断言了数据状态：「${forbidden}」`)
    }
  })

  it('★★★ 不得漏技术串给用户', () => {
    const n = listReadFailureNotice('会议记录')
    for (const tech of ['SQLITE', 'undefined', 'null', 'fetchPage', 'Exception']) {
      assert.ok(!n.title.includes(tech) && !n.hint.includes(tech), `技术串漏给用户：${tech}`)
    }
  })

  it('★★ 必须给可执行动作，且动作文案指向重试', () => {
    const n = listReadFailureNotice('会议记录')
    assert.match(n.action, /重试|重新/, `失败态没有可执行动作：${JSON.stringify(n.action)}`)
  })

  it('★★ 两个页面的名词各自正确（文案由 subject 决定，不写死）', () => {
    assert.equal(listReadFailureNotice('会议记录').title, '会议记录读取失败')
    assert.equal(listReadFailureNotice('笔记').title, '笔记读取失败')
  })
})

describe('§840 · 「本地库未就绪」只有一份实现', () => {
  it('★★ §837 的再导出必须与 SSOT 是同一个函数（两份同义判据必然漂移）', () => {
    assert.equal(reexportedFromMeetings, isLocalDbUnavailable,
      'meeting-load-notice 不再导出同一个函数 ⇒ 出现了第二份判据')
  })

  it('★★ 判据本身仍然认得出那两种真实抛错', () => {
    assert.equal(isLocalDbUnavailable(new Error('LocalDB 未初始化：连接不存在')), true)
    assert.equal(isLocalDbUnavailable(new Error('LocalDB 未初始化，请先调用 init(dbSecret)')), true)
    assert.equal(isLocalDbUnavailable(new Error('SQLITE_BUSY')), false)
  })

  it('★ 诊断 tag 稳定（两处视图靠它 grep 自己的失败日志）', () => {
    assert.equal(LIST_READ_FAIL_TAG, '[list] 读取失败：')
  })
})

describe('§840 · 视图接线（源码结构）', () => {
  for (const v of VIEWS) {
    it(`★★ ${v.name}：fetchPage 内部必须能看见 e.message（错误只在这里可见）`, () => {
      assert.equal(seesErrorObject(fetchPageOf(v.src)), true,
        `${v.name} 的 fetchPage 收不到 e.message ⇒ dbNotReady 恒为 false，DbLockedState 不可达`)
    })

    it(`★★★ ${v.name}：catch 里必须 rethrow（返回空结果会被控制器当成成功）`, () => {
      assert.equal(rethrows(fetchPageOf(v.src)), true,
        `${v.name} 的 catch 吞掉了错误 ⇒ 控制器 commitPage → 'exhausted'，失败分支成了死代码`)
    })

    it(`★★★ ${v.name}：模板必须有带「无行」守卫的读失败分支`, () => {
      assert.equal(hasGuardedBranch(templateOf(v.src)), true,
        `${v.name} 的模板没有「readError && 行数 === 0」分支 ⇒ 首屏失败仍会落进真空态`)
    })

    it(`★★★ ${v.name}：读失败分支必须排在真空态分支**之前**`, () => {
      assert.equal(orderOk(templateOf(v.src), v.emptyCond), true,
        `${v.name} 的真空态分支排在读失败分支前面 ⇒ 后者永远轮不到（v-else-if 链只认第一个为真的）`)
    })

    it(`★★ ${v.name}：失败分支必须带可点的重试入口`, () => {
      assert.equal(hasRetryEntry(templateOf(v.src)), true,
        `${v.name} 的失败分支没有 @action="retry" ⇒ 用户点不到重试`)
    })

    it(`★★ ${v.name}：readError 必须接在共享判定上（不能自己另写一套）`, () => {
      assert.equal(usesSharedPredicate(v.src), true,
        `${v.name} 的 readError 没走 isListReadError ⇒ 两页会各自漂移`)
    })
  }

  it('★★ 笔记页必须把搜索态排除在失败分支之外（搜索 0 结果是合法结论）', () => {
    assert.match(codeOnly(NOTES), /searchResults\.value === null && isListReadError\(/,
      '搜索态没被排除 ⇒ 用户搜不到东西时会被告知「读取失败」')
  })
})

describe('§840 · 变异负控（每条都必须让上面那把尺子转红）', () => {
  for (const v of VIEWS) {
    it(`变异 A · ${v.name}：拿掉「无行」守卫`, () => {
      const tpl = templateOf(v.src)
      assert.equal(hasGuardedBranch(tpl), true, '基线就得通过，否则本变异无效')
      const mutated = tpl.replace(/v-else-if="readError && [^"]*"/, 'v-else-if="readError"')
      assert.notEqual(mutated, tpl, '★ 变异没生效：正则没匹配到')
      assert.equal(hasGuardedBranch(mutated), false, '拿掉守卫后尺子居然还通过 ⇒ 判据无牙')
    })

    it(`变异 B · ${v.name}：把失败分支挪到真空态之后`, () => {
      const tpl = templateOf(v.src)
      assert.equal(orderOk(tpl, v.emptyCond), true, '基线就得通过，否则本变异无效')
      // 合成一个顺序颠倒的模板：判据必须认出来（v-else-if 链只认第一个为真的）
      const synth = `<template>\n  <div v-if="loading" />\n`
        + `  <EmptyState ${v.emptyCond} title="还没有" />\n`
        + `  <EmptyState v-else-if="readError && ${v.rows}.length === 0" title="失败" />\n`
        + `</template>\n<script setup lang="ts"></script>`
      assert.equal(orderOk(synth, v.emptyCond), false, '顺序颠倒的模板竟然判成合法 ⇒ 顺序判据无牙')
    })

    it(`变异 C · ${v.name}：catch 不 rethrow（改成返回空结果）`, () => {
      const fp = fetchPageOf(v.src)
      assert.equal(rethrows(fp), true, '基线就得通过，否则本变异无效')
      const mutated = fp.replace(/throw\s+e\s*;?\s*$/m, 'return { rows: [], total: 0, hasMore: false }')
      assert.notEqual(mutated, fp, '★ 变异没生效：没匹配到 throw e')
      assert.equal(rethrows(mutated), false, '吞错的 catch 竟然判成合法 ⇒ 判据无牙')
    })

    it(`变异 D · ${v.name}：fetchPage 不再判「本地库未就绪」`, () => {
      const fp = fetchPageOf(v.src)
      assert.equal(seesErrorObject(fp), true, '基线就得通过，否则本变异无效')
      const mutated = fp.replace(/if\s*\(msg\.includes\('LocalDB 未初始化'\)\)\s*dbNotReady\.value = true/, '/* 删掉了 */')
      assert.notEqual(mutated, fp, '★ 变异没生效：没匹配到 dbNotReady 判定')
      assert.equal(seesErrorObject(mutated), false, '不再判本地库未就绪竟然判成合法 ⇒ 判据无牙')
    })

    it(`变异 E · ${v.name}：失败分支丢掉 @action="retry"`, () => {
      const tpl = templateOf(v.src)
      assert.equal(hasRetryEntry(tpl), true, '基线就得通过，否则本变异无效')
      const mutated = tpl.replace('@action="retry"', '')
      assert.notEqual(mutated, tpl, '★ 变异没生效：没匹配到 @action')
      assert.equal(hasRetryEntry(mutated), false, '没有重试入口竟然判成合法 ⇒ 判据无牙')
    })
  }
})
