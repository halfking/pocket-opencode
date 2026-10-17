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
const INBOX = read('../../features/email/EmailInboxView.vue')
const FOLDERS = read('../../features/email/EmailFolderListView.vue')
const TASKS = read('../../features/tasks/TasksView.vue')
const OC_STORE = read('../../stores/opencode.ts')
const OC_LIST = read('../../features/opencode/SessionListView.vue')

/**
 * ★★ loader 的 catch 里**不得把已加载的行清空**。
 *
 * §843 的核心：`loadTasks()` / `loadSessions()` 原本各有一句
 *   `tasks.value = []` / `sessions.value = []`
 * ⇒ 一次失败整屏行消失，并显示「暂无运行中的任务」+ 引导新建。
 * 那正是 `list-error-failpath` 为 UI-07e 建立时要防的形态，而它**看不见**这处 ——
 * 因为没有「错误分支」可查，只有一次主动清空。
 *
 * ⚠ 口径：这里查的是「catch 块体内是否出现把某个列表 ref 赋成空数组」，
 *   它同样会命中「确实想清空」的合法写法（目前本仓没有这种写法）。
 *   若将来有，必须在这里显式登记例外，而不是把判据放宽。
 */
const catchClearsRows = (src: string, fn: string): boolean => {
  const body = codeOnly(blockFrom(codeOnly(src), fn))
  const c = /catch\s*\([^)]*\)\s*\{[\s\S]*?\n {2}\}/.exec(body)
  if (!c) return false
  return /\w+(?:\.value)?\s*=\s*\[\]/.test(c[0])
}

/**
 * 三个走 `useContinuousList` 的视图（仓库里**全部**三个，`use-list-sentinel` 已无生产消费方）。
 *
 * ⚠ 三处的「捕获锚点」不同：会议/笔记在 `fetchPage`，收件箱在它的 `readPage` 依赖
 *   （`fetchPage` 由共享的 `makeInboxFetchPage` 生成 ⇒ 那里没有视图状态可写）。
 */
const VIEWS = [
  {
    name: 'MeetingListView', src: MEETINGS, rows: 'meetings',
    emptyCond: 'v-else-if="meetings.length === 0"',
    anchor: 'fetchPage: async', retry: '@action="retry"',
  },
  {
    name: 'NoteListView', src: NOTES, rows: 'filteredNotes',
    emptyCond: 'v-else-if="filteredNotes.length === 0"',
    anchor: 'fetchPage: async', retry: '@action="retry"',
  },
  {
    name: 'EmailInboxView', src: INBOX, rows: 'shownEmails',
    emptyCond: 'v-else-if="shownEmails.length === 0"',
    anchor: 'readPage: async', retry: '@action="load"',
  },
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
 * 模板区，**剥掉 HTML 注释**。
 *
 * ⚠ 为什么需要：`indexOf('暂无运行中的任务')` 会先命中我自己写在注释里的那句
 *   「并显示「暂无运行中的任务」+…」（§843 第一次跑就假红了）。
 *   ⇒ 凡是用「界面上的某段文案」当锚点的判据，必须先剥注释，
 *     否则锚点会被注释抢走，而**注释恰好是为了解释这段代码才写的**。
 */
const tplCode = (src: string): string => templateOf(src).replace(/<!--[\s\S]*?-->/g, '')

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
 * ⚠ 第三版：阳性对照写成 `at > 0`，而**锚点恰好在位置 0** 时 `indexOf` 返回 `0`
 *   ⇒ 被误报成「找不到锚点」。凡是「用 indexOf 定位」的断言，阳性对照都该是 `!== -1`。
 * ⚠⚠ 第四版最隐蔽：它**只会**处理箭头函数。遇到 `async function loadTasks() {`
 *   这种普通函数声明时找不到 `=>`，就一路找到**后面某个** `=>` 再取它的 `{`
 *   ⇒ 返回一段完全无关的切片，判据在那段垃圾上跑。
 *   ⚠ 更糟的是它可能**碰巧**包含 try/catch 而给出假绿（§843 第一次跑就撞上）。
 *   ⇒ 必须同时支持两种形状：锚点自带形参的普通函数，以及 `xxx: (…) => {` 箭头函数。
 */
const blockFrom = (code: string, anchor: string, max = 4000): string => {
  const at = code.indexOf(anchor)
  assert.ok(at !== -1, `★ 阳性对照失败：找不到锚点 ${anchor}`)
  let i = at + anchor.length
  const skipWs = () => { while (i < code.length && /\s/.test(code[i])) i += 1 }
  skipWs()
  if (code[i] === '(') {                        // 跳过形参列表（按括号配平）
    let depth = 0
    for (; i < code.length; i += 1) {
      if (code[i] === '(') depth += 1
      else if (code[i] === ')') { depth -= 1; if (depth === 0) { i += 1; break } }
    }
  }
  skipWs()
  if (code.startsWith('=>', i)) { i += 2; skipWs() }
  assert.ok(code[i] === '{',
    `★ 阳性对照失败：锚点 ${anchor} 之后的函数体不是 '{'（拿到 ${JSON.stringify(code.slice(i, i + 12))}）`)
  const open = i
  let depth = 0
  for (let k = open; k < code.length && k < open + max; k += 1) {
    if (code[k] === '{') depth += 1
    else if (code[k] === '}') {
      depth -= 1
      if (depth === 0) return code.slice(at, k + 1)
    }
  }
  return code.slice(at, at + max)
}

/** 捕获块：fetchPage（会议/笔记）或 readPage（收件箱）的函数体，已剥整行注释。 */
const catchBlockOf = (src: string, anchor: string): string => codeOnly(blockFrom(src, anchor))

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
const hasRetryEntry = (tpl: string, retry: string): boolean => {
  const at = tpl.indexOf('readError &&')
  if (at < 0) return false
  const close = tpl.indexOf('>', at)
  if (close < 0) return false
  const branch = tpl.slice(at, close)
  return branch.includes(retry) && /:action-label="/.test(branch)
}

/** 捕获块里能看到异常对象。 */
const catchesError = (fp: string): boolean => /catch\s*\(e(?:\s*:\s*(?:unknown|any))?\)/.test(fp)

/**
 * 判出「本地库未就绪」。两种写法都接受：字面量 `msg.includes(...)`
 * 或共享判据 `isLocalDbUnavailable(e)`；但**必须有一种**。
 * （收件箱走共享判据，会议/笔记走字面量 —— §837 的钉死用例要求后者保持不变。）
 */
const marksDbUnavailable = (fp: string): boolean =>
  /includes\('LocalDB 未初始化'\)/.test(fp) || /isLocalDbUnavailable\(\s*e\s*\)/.test(fp)

/** 捕获块让错误继续往上抛（返回空结果会被当成成功）。 */
const rethrows = (fp: string): boolean =>
  /throw\s+e\s*;?\s*$/m.test(fp) && !/catch[\s\S]*?return\s*\{\s*rows:\s*\[\]/.test(fp)

/** readError 接在共享判定上，而不是各页自己写一套。 */
const usesSharedPredicate = (src: string): boolean =>
  /isListReadError\(listStatus\.value,/.test(codeOnly(src))

/**
 * ★ 收件箱专属：读失败标志必须**不只**来自 `loadError`。
 *
 * 这是本节最要紧的一条：改之前 `loadError` 恒为 `''`（§841 取证），
 * 于是 `v-else-if="loadError && shownEmails.length === 0"` 恒假 ⇒
 * 门禁 `list-error-failpath` 的 enforced 登记（守卫在）是**空洞满足**。
 * ⇒ 「守卫在」只是必要条件；还要断言「错误标志真能被设上」。
 */
const errorFlagNotOnlyFromLoadError = (src: string): boolean =>
  /readError\s*=\s*computed\([\s\S]{0,160}loadError\.value\s*!==\s*''[\s\S]{0,160}isListReadError\(/.test(codeOnly(src))

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

  it('★★★ 有行与无行必须是两句不同的话（§841：混成一句会让有行场景以为数据是全的）', () => {
    const none = listReadFailureNotice('邮件')
    const some = listReadFailureNotice('邮件', { hasRows: true })
    assert.notEqual(some.title, none.title)
    assert.notEqual(some.hint, none.hint)
  })

  it('★★★ 有行时必须说明「屏幕上的这些可能是旧的」（不说明等于让用户误信数据是全的）', () => {
    const some = listReadFailureNotice('邮件', { hasRows: true })
    assert.match(some.hint, /上次读到的|不是最新/,
      `有行时的文案没提「这些可能是旧的」：${some.hint}`)
    assert.match(some.hint, /重试/, '有行时也要给可执行动作')
  })

  it('★★ hasRows 缺省必须等于「无行」，且显式 false 也要走无行那一支', () => {
    assert.deepEqual(listReadFailureNotice('邮件'), listReadFailureNotice('邮件', {}))
    assert.deepEqual(listReadFailureNotice('邮件'), listReadFailureNotice('邮件', { hasRows: false }))
  })

  it('★★ 两句文案都不得断言数据状态（§838 教训：读失败时并不知道记录还在不在）', () => {
    for (const opts of [{}, { hasRows: true }]) {
      const n = listReadFailureNotice('邮件', opts)
      for (const forbidden of ['没有丢', '已丢失', '数据丢失']) {
        assert.ok(!n.hint.includes(forbidden), `文案断言了数据状态：「${forbidden}」`)
      }
    }
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

/**
 * ★ `reload()` 必须**自己兜住**，不得向外抛。
 *
 * §842 的关键因果链就在这里：`onMounted` 是 `void reload()`（未处理 rejection），
 * 而新建/删除两条路径 `await reload()` ⇒ reload 一抛，
 * ① 新建：错误写进**已关闭**的面板，用户看不到；
 * ② 删除：先弹 `toast.success('已删除')` 再弹 `toast.error('删除失败')`，同一次点击两条矛盾的话。
 * ⇒ 让 reload 不抛，这两条**自动**消失（而不是给每个调用方各打一个补丁）。
 *
 * ⚠ 这里必须先剥整行注释再做括号配平：注释里出现过成对的 `{}`（举例说明那个空回调），
 *   而 `blockFrom` 不认识注释 ⇒ 会把注释里的括号算进深度、提前截断。
 */
const reloadSwallows = (src: string): boolean => noUnguardedTry(src, 'async function reload()')

/**
 * 函数体里 `catch` 的个数不得少于 `try` 的个数 —— 即「没有不设防的 try」。
 *
 * ⚠ 前两版都太松/太脆：
 *   ① 「有没有 catch」太松：reload 有两层 try，删掉**外层** catch 后内层的还在；
 *   ② 按 try/catch/finally 配平维护栈太脆：catch 之后无法在不数括号的前提下弹栈
 *      （而括号在字符串与注释里都有）。
 * ⚠ 而 `finally` **不会吞异常**（它只是必跑）⇒ 判据不能是「有 finally 就行」。
 *   缺陷的准确形状是「try 后面直接跟 finally，异常逃出去」。
 *
 * 口径：这是**近似**判据（catch 数 ≥ try 数），不是逐个 try 配平。
 * 它能咬住本节那个真缺陷与本节用的两个变异；理论上「3 try / 3 catch 但有一个没兜」
 * 这种错配它抓不到 —— 那需要在 JS 里真正解析，这里如实标注而不是假装精确。
 */
const noUnguardedTry = (src: string, fn: string): boolean => {
  const body = codeOnly(blockFrom(codeOnly(src), fn))
  const tries = (body.match(/\btry\s*\{/g) || []).length
  const catches = (body.match(/\bcatch\s*\(/g) || []).length
  return tries > 0 && catches >= tries
}

/** 不得有「完全空且无说明」的 catch（§838 禁的形状）。 */
const hasEmptySwallow = (src: string): boolean => {
  const code = codeOnly(src)
  return /\.catch\(\s*\(\s*\)\s*=>\s*\{\s*\}\s*\)/.test(code)
}

/**
 * 失败分支（**空态那条**）的开标签属性区间。
 *
 * ⚠ 不能用「第一个出现 readError」：横幅 `v-if="readError && folders.length > 0"`
 *   排在它上面，取到的会是横幅那个 div —— 它的开标签在同一行就闭合了，
 *   后面的 `@action` 全被切掉 ⇒ 一条**永远红的假阴性**（这个坑踩了两次）。
 *   ⇒ 条件正则必须精确到 `folders.length === 0`。
 */
const FOLDER_ERROR_BRANCH = /v-(?:else-)?if="readError && folders\.length === 0"/

const branchTagOf = (tplRaw: string): string => {
  const tpl = tplCode(tplRaw)
  const m = FOLDER_ERROR_BRANCH.exec(tpl)
  assert.ok(m, '★ 量具自证失败：模板里找不到「readError && folders.length === 0」这条分支')
  const at = m.index
  const close = tpl.indexOf('>', at)
  assert.ok(close > at, '★ 量具自证失败：失败分支的开标签没有闭合')
  return tpl.slice(at, close)
}

describe('§843 · 任务与会话（TasksView）', () => {
  for (const [fn, name, notice] of [
    ['async function loadTasks()', 'loadTasks', 'tasksError'],
    ['async function loadSessions()', 'loadSessions', 'sessionsError'],
  ] as const) {
    it(`★★★ ${name}() 的 catch 不得清空已加载的行（§843 的真缺陷）`, () => {
      assert.equal(catchClearsRows(TASKS, fn), false,
        `${name} 的 catch 里有「某 ref = []」⇒ 一次失败整屏行消失（UI-07e 的同一形态），`
        + '而仓库门禁看不见它（没有错误分支可查，只有一次主动清空）')
    })

    it(`★★ ${name}() 的 catch 必须挂一条用户可见的失败说明`, () => {
      const body = codeOnly(blockFrom(codeOnly(TASKS), fn))
      assert.match(body, new RegExp(`${notice}\\.value = listReadFailureNotice\\(`),
        `${name} 的 catch 没有写 ${notice} ⇒ 失败路径上零用户可见说明`)
    })

    it(`★ ${name}() 成功路径必须清掉上一次的失败说明`, () => {
      const body = codeOnly(blockFrom(codeOnly(TASKS), fn))
      assert.match(body, new RegExp(`${notice}\\.value = null`),
        `${name} 没有重置 ${notice} ⇒ 读成功后失败提示还挂着`)
    })
  }

  it('★★★ 两个失败分支都必须带「无行」守卫且排在真空态之前', () => {
    const tpl = tplCode(TASKS)
    for (const [flag, rows, emptyBind] of [
      ['tasksError', 'visibleActiveTasks', ':title="activeTasks.length > 0'],
      ['sessionsError', 'visibleSessions', 'icon="💬"'],
    ] as const) {
      const re = new RegExp(`v-(?:else-)?if="${flag} && ${rows}\\.length === 0"`)
      assert.match(tpl, re, `没有「${flag} && ${rows}.length === 0」分支`)
      const at = tpl.search(re)
      assert.ok(at > 0, `量具自证失败：找不到 ${flag} 分支`)
      // ⚠ 锚点用空态分支自己的 title **绑定表达式**，不用文案字面量 ——
      //   文案会出现在我自己写的注释里（§843 踩过）。
      const atEmpty = tpl.indexOf(emptyBind)
      assert.ok(atEmpty > at, `${flag} 分支排在真空态分支之后 ⇒ 永远轮不到（v-else-if 链只认第一个为真的）`)
    }
  })

  it('★★ 两个失败分支都要带可点的重试入口', () => {
    const tpl = tplCode(TASKS)
    assert.match(tpl, /@action="loadTasks"/, '任务区失败分支没有 @action="loadTasks"')
    assert.match(tpl, /@action="loadSessions"/, '会话区失败分支没有 @action="loadSessions"')
  })

  it('★★ 有行时也必须挂非破坏性横幅（两档契约的后一档）', () => {
    const tpl = tplCode(TASKS)
    for (const flag of ['tasksError', 'sessionsError']) {
      // ⚠ 属性顺序是 `v-if` 在前、`class` 在后（照抄本仓既有横幅写法）
      assert.match(tpl, new RegExp(`v-if="${flag}"[\\s\\S]{0,80}class="list-error-banner"`),
        `有行时 ${flag} 没有任何提示 ⇒ 刷新失败后用户以为看到的是最新的`)
    }
  })

  it('★ 变异负控：把清空那一行放回去 ⇒ 尺子必须转红', () => {
    const before = codeOnly(blockFrom(codeOnly(TASKS), 'async function loadTasks()'))
    const mutated = before.replace('console.error(\'Failed to load tasks:\', e)',
      "console.error('Failed to load tasks:', e)\n    tasks.value = []")
    assert.notEqual(mutated, before, '★ 变异没生效：没匹配到 console.error 那一行')
    assert.equal(catchClearsRows(mutated, 'async function loadTasks()'), true, '尺子没咬住清空')
  })

  it('★ 变异负控：把失败分支的守卫拿掉 ⇒ 尺子与仓库门禁都必须转红', () => {
    const mutated = tplCode(TASKS)
      .replace(/v-else-if="tasksError && visibleActiveTasks\.length === 0"/, 'v-else-if="tasksError"')
    assert.notEqual(mutated, tplCode(TASKS), '★ 变异没生效')
    assert.equal(/v-else-if="tasksError && visibleActiveTasks\.length === 0"/.test(mutated), false,
      '拿掉守卫后尺子居然还通过')
  })
})

describe('§844 · OpenCode 会话（store 层清空 + 视图从不渲染 error）', () => {
  it('★★★ store 的 catch 不得无条件清空 sessions（刷新失败会抹掉正在看的列表）', () => {
    const body = codeOnly(blockFrom(codeOnly(OC_STORE), 'async loadSessions(instanceId: string)'))
    assert.equal(/\n\s*this\.sessions\s*=\s*\[\]/.test(body), false,
      'store 里仍有无条件的 `this.sessions = []` ⇒ 刷新同一个实例时一次抖动就抹掉列表')
  })

  it('★★★ 清空必须按**归属**判断（切实例失败时行属于旧实例，必须清）', () => {
    const body = codeOnly(blockFrom(codeOnly(OC_STORE), 'async loadSessions(instanceId: string)'))
    assert.match(body, /if\s*\(this\.sessionsOwnerId !== instanceId\)\s*this\.sessions = \[\]/,
      '清空条件不是按 sessionsOwnerId 判断 ⇒ 切实例失败时会在新实例标题下显示旧实例的会话')
    assert.match(codeOnly(OC_STORE), /this\.sessionsOwnerId = instanceId/,
      '成功路径没有记录 sessionsOwnerId ⇒ 归属永远判不出来')
  })

  it('★★★ 视图必须把 store 的 error 翻成用户可见的说明（此前一次都没引用过）', () => {
    assert.match(codeOnly(OC_LIST), /openCodeStore\.error/,
      'features/opencode/SessionListView.vue 仍然从不读 store.error ⇒ 读失败对用户完全不可见')
  })

  it('★★ 视图不得把 store 里的技术串直接甩给用户', () => {
    const m = /const readError = computed[\s\S]*?\n\}\)/.exec(codeOnly(OC_LIST))
    assert.ok(m, '量具自证失败：找不到 readError 的定义')
    const block = m[0]
    // ⚠ 第一版判据查的是字面量（`:title="…error…"` / `readError.title =`），
    //   端到端变异 M5「把 title 换成 msg」时它**0 红** —— 判据无牙。
    // ⇒ 改成查**结构**：notice 必须来自共享文案函数，且 msg 不得进用户可见字段。
    assert.match(block, /return listReadFailureNotice\(/,
      'readError 没有走共享文案函数 ⇒ 可能在手搓对象时把技术串带出去')
    assert.ok(!/\btitle\s*:\s*[^,\n]*\bmsg\b/.test(block),
      '把 store.error 的原始字符串（msg）当标题了 ⇒ 可能含 URL / 状态码')
    assert.ok(!/\$\{[^}]*\bmsg\b[^}]*\}/.test(block), '把 msg 插值进了用户可见文案')
  })

  it('★★★ 视图的失败分支必须带「无行」守卫且排在真空态之前', () => {
    const tpl = tplCode(OC_LIST)
    assert.match(tpl, /v-else-if="readError && sessions\.length === 0"/, '缺少带守卫的失败分支')
    const atError = tpl.indexOf('v-else-if="readError &&')
    const atEmpty = tpl.indexOf('class="empty-state"')
    assert.ok(atError > 0 && atEmpty > atError, '真空态排在失败分支之前 ⇒ 后者永远轮不到')
  })

  it('★★ 有行时必须挂非破坏性提示，且给可点的重试', () => {
    const tpl = tplCode(OC_LIST)
    assert.match(tpl, /v-if="readError"[\s\S]{0,80}class="list-error-banner"/, '有行时没有提示')
    assert.match(tpl, /class="banner-retry"[\s\S]{0,80}@click="refreshSessions"/, '横幅没有重试按钮')
  })

  it('★ 变异负控：把归属判断改回无条件清空 ⇒ 尺子必须转红', () => {
    const before = codeOnly(blockFrom(codeOnly(OC_STORE), 'async loadSessions(instanceId: string)'))
    const mutated = before.replace(
      'if (this.sessionsOwnerId !== instanceId) this.sessions = []',
      'this.sessions = []',
    )
    assert.notEqual(mutated, before, '★ 变异没生效：没匹配到归属判断')
    assert.equal(/\n\s*this\.sessions\s*=\s*\[\]/.test(mutated), true,
      '改回无条件清空后尺子居然还通过 ⇒ 无牙')
  })

  it('★ 变异负控：视图不再读 store.error ⇒ 尺子必须转红（还原真缺陷）', () => {
    const mutated = codeOnly(OC_LIST).replace(/const msg = openCodeStore\.error/, 'const msg = null')
    assert.notEqual(mutated, codeOnly(OC_LIST), '★ 变异没生效')
    assert.equal(/openCodeStore\.error/.test(mutated), false,
      '不再读 error 后尺子居然还通过 ⇒ 无牙')
  })
})

describe('§842 · 邮件目录（EmailFolderListView）', () => {
  it('★★★ reload() 必须自己兜住（否则调用方会给出与真实结果相反的提示）', () => {
    assert.equal(reloadSwallows(FOLDERS), true,
      'reload() 没有 catch ⇒ void reload() 是未处理 rejection，且新建/删除两条路径会误报')
  })

  it('★★ reload() 里不得有「完全空且无说明」的 catch（§838 禁的形状）', () => {
    assert.equal(hasEmptySwallow(FOLDERS), false,
      'EmailFolderListView 里仍有完全空的 catch ⇒ 失败零痕迹')
  })

  it('★★★ 失败必须落日志（本地库未就绪是预期状态，但不能无声）', () => {
    const body = blockFrom(codeOnly(FOLDERS), 'async function reload()')
    assert.match(body, /console\.error\(/, 'reload() 的 catch 没有 console.error ⇒ 失败无痕迹')
    assert.match(body, /console\.warn\(/, '服务器同步失败那一路没有 console.warn ⇒ 失败零痕迹')
  })

  it('★★★ 模板必须有带「无行」守卫的读失败分支，且带重试入口', () => {
    assert.match(templateOf(FOLDERS), FOLDER_ERROR_BRANCH,
      '没有「readError && folders.length === 0」分支 ⇒ 读失败仍会显示「还没有自定义目录」')
    const branch = branchTagOf(templateOf(FOLDERS))
    assert.match(branch, /@action="reload"/, '失败分支没有可点的重试入口')
    // ⚠ 这里认 `readError.action` **或** `readErrorNotice.action`：vue-tsc 不用
    //   `v-else-if="readError && …"` 收窄后续属性 ⇒ 必须绑一个永远非空的来源，
    //   所以真实代码走的是后者（§842 落地时由 vue-tsc TS18047 逼出来的）。
    assert.match(branch, /:action-label="readError(?:Notice)?\.action"/,
      '失败分支没有绑定动作文案')
    assert.match(branch, /:title="readError(?:Notice)?\.title"/, '失败分支没有绑定标题')
  })

  it('★★ 失败分支必须排在真空态分支之前', () => {
    const tpl = templateOf(FOLDERS)
    const atError = FOLDER_ERROR_BRANCH.exec(tpl)?.index ?? -1
    const atEmpty = tpl.indexOf('v-else-if="folders.length === 0 && !loading"')
    assert.ok(atError > 0 && atEmpty > atError, '真空态分支排在失败分支前面 ⇒ 后者永远轮不到')
  })

  it('★★ 有行时也要有非破坏性提示（不把已加载的目录换成整页错误态）', () => {
    assert.match(templateOf(FOLDERS), /v-if="readError && folders\.length > 0"/,
      '有行时没有任何失败提示 ⇒ 刷新失败后用户以为目录是最新的')
  })

  it('★★ 标志名里必须含 Error（门禁靠 /error/i 认它，否则分支被整体跳过）', () => {
    // §840/§842 各踩过一次：叫 readFailure 时门禁的枚举器在 /error/i 那一步 continue 掉了。
    assert.match(codeOnly(FOLDERS), /const readError = ref</, '标志不是 readError')
    assert.equal(/\breadFailure\b/.test(codeOnly(FOLDERS)), false,
      '还有 readFailure 残留 ⇒ 门禁认不出这条分支')
  })

  it('★ 变异负控：把 reload 的 catch 删掉 ⇒ 尺子必须转红', () => {
    const before = blockFrom(codeOnly(FOLDERS), 'async function reload()')
    // ⚠ 源码是 `} catch (e: unknown) { … } finally {` —— `}` 与 `finally` **同一行**。
    //   第一版正则写成 `\n  }\n  finally`，一个都没匹配上（变异没生效却报了别的错）。
    const mutated = before.replace(
      /\}\s*catch\s*\(e:\s*unknown\)\s*\{[\s\S]*?\}\s*finally\s*\{/,
      '}\n  finally {',
    )
    assert.notEqual(mutated, before, '★ 变异没生效：没匹配到 reload 的 catch')
    assert.equal(reloadSwallows(mutated), false,
      '删掉 catch 后尺子居然还通过 ⇒ 无牙')
  })

  it('★ 变异负控：还原成空回调 ⇒ 尺子必须转红（且自己的注释不算命中）', () => {
    assert.equal(hasEmptySwallow('await x.catch(() => {})'), true)
    assert.equal(hasEmptySwallow(FOLDERS), false)
    // 负控的反面：注释里写同一段字面量**不得**被判成命中
    assert.equal(hasEmptySwallow('// 原来是一句空回调：.catch(() => {})\nconst a = 1\n'), false,
      '注释里的字面量被判成真代码 ⇒ 以后没法在注释里引用坏写法')
  })
})

describe('§840 · 视图接线（源码结构）', () => {
  for (const v of VIEWS) {
    it(`★★ ${v.name}：捕获块内部必须能看见 e.message（错误只在这里可见）`, () => {
      const cb = catchBlockOf(v.src, v.anchor)
      assert.equal(catchesError(cb), true,
        `${v.name} 的 ${v.anchor} 没有 catch ⇒ e.message 到不了任何地方，dbNotReady 恒为 false`)
      assert.equal(marksDbUnavailable(cb), true,
        `${v.name} 没有判「本地库未就绪」⇒ DbLockedState 不可达`)
    })

    it(`★★★ ${v.name}：catch 里必须 rethrow（返回空结果会被控制器当成成功）`, () => {
      assert.equal(rethrows(catchBlockOf(v.src, v.anchor)), true,
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
      assert.equal(hasRetryEntry(templateOf(v.src), v.retry), true,
        `${v.name} 的失败分支没有 ${v.retry} ⇒ 用户点不到重试`)
    })

    it(`★★ ${v.name}：readError 必须接在共享判定上（不能自己另写一套）`, () => {
      assert.equal(usesSharedPredicate(v.src), true,
        `${v.name} 的 readError 没走 isListReadError ⇒ 三页会各自漂移`)
    })
  }

  it('★★★ 收件箱：读失败标志不得只来自 loadError（§841：那个 ref 恒为空，守卫因此是空洞满足）', () => {
    assert.equal(errorFlagNotOnlyFromLoadError(INBOX), true,
      'EmailInboxView 的 readError 没有把 isListReadError 并进来 ⇒ 又退回「只有 loadError」，而它永远设不上')
  })

  it('★★★ 收件箱：有行时也要有失败提示（横幅），不能只在无行时才有反应', () => {
    const tpl = templateOf(INBOX)
    const bannerAt = tpl.indexOf('list-error-banner')
    assert.ok(bannerAt > 0, '收件箱的失败横幅不见了')
    assert.match(tpl.slice(bannerAt - 160, bannerAt), /v-if="readError"/,
      '横幅仍只认 loadError ⇒ 有行时的刷新失败没有任何提示')
  })

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
      const cb = catchBlockOf(v.src, v.anchor)
      assert.equal(rethrows(cb), true, '基线就得通过，否则本变异无效')
      const mutated = cb.replace(/throw\s+e\s*;?\s*$/m, 'return { rows: [], total: 0, hasMore: false }')
      assert.notEqual(mutated, cb, '★ 变异没生效：没匹配到 throw e')
      assert.equal(rethrows(mutated), false, '吞错的 catch 竟然判成合法 ⇒ 判据无牙')
    })

    it(`变异 D · ${v.name}：捕获块不再判「本地库未就绪」`, () => {
      const cb = catchBlockOf(v.src, v.anchor)
      assert.equal(marksDbUnavailable(cb), true, '基线就得通过，否则本变异无效')
      const mutated = cb
        .replace(/if\s*\(msg\.includes\('LocalDB 未初始化'\)\)\s*dbNotReady\.value = true/, '/* 删掉了 */')
        .replace(/if\s*\(isLocalDbUnavailable\(e\)\)\s*dbNotReady\.value = true/, '/* 删掉了 */')
      assert.notEqual(mutated, cb, '★ 变异没生效：没匹配到 dbNotReady 判定')
      assert.equal(marksDbUnavailable(mutated), false, '不再判本地库未就绪竟然判成合法 ⇒ 判据无牙')
    })

    it(`变异 E · ${v.name}：失败分支丢掉重试入口`, () => {
      const tpl = templateOf(v.src)
      assert.equal(hasRetryEntry(tpl, v.retry), true, '基线就得通过，否则本变异无效')
      const mutated = tpl.replace(v.retry, '')
      assert.notEqual(mutated, tpl, `★ 变异没生效：没匹配到 ${v.retry}`)
      assert.equal(hasRetryEntry(mutated, v.retry), false, '没有重试入口竟然判成合法 ⇒ 判据无牙')
    })

    it(`变异 F · ${v.name}：捕获块把 catch 整个删掉`, () => {
      const cb = catchBlockOf(v.src, v.anchor)
      assert.equal(catchesError(cb), true, '基线就得通过，否则本变异无效')
      assert.equal(marksDbUnavailable(cb), true, '基线就得通过，否则本变异无效')
    })
  }

  it('变异 G · 收件箱：把 readError 退回「只认 loadError」（还原 §841 的真缺陷）', () => {
    assert.equal(errorFlagNotOnlyFromLoadError(INBOX), true, '基线就得通过，否则本变异无效')
    const mutated = codeOnly(INBOX).replace(
      /readError\s*=\s*computed\(\s*\(\)\s*=>\s*loadError\.value !== '' \|\| isListReadError\([\s\S]*?\),\s*\)/,
      'readError = computed(() => loadError.value !== \'\')',
    )
    assert.notEqual(mutated, codeOnly(INBOX), '★ 变异没生效：没匹配到 readError 的定义')
    assert.equal(errorFlagNotOnlyFromLoadError(mutated), false,
      '退回「只认 loadError」竟然判成合法 ⇒ 这条判据没有牙')
  })
})
