// approval-silent-truncation.test.ts — §856：审批链路上的「用户输入被静默丢弃」与
// 「用户动作失败零反馈」。
//
// 缺陷背景（ApprovalPanel，human-in-the-loop 审批面板）：
//   D16  提交前对 >500 字的审批附言做 `permMessage[p.id] = permMessage[p.id].slice(0, 500)`
//        而输入框**没有 maxlength、没有字数提示、没有任何提示消息**
//        ⇒ 用户写的追加限制条件（「可以读，但绝对不要改 package.json」）被无声丢掉，
//          **Agent 收到的是不完整的指令**，可能做出用户明确禁止的事。
//        ⛔ 后端**没有**这个限制：`PermissionReplyRequest{Reply, Message string}`
//          （backend/internal/adapter/opencode_http.go:722）无长度校验，直接透传上游
//          ⇒ 那个 500 是前端凭空加的。
//   D17  批准/拒绝/回答/跳过四处的失败出口为零：
//        `doReplyPermission` / `answerQuestion` / `skipQuestion` 都**不设** `store.error`
//        （它只由 `fetchPending` 写，stores/approval.ts:64/71）
//        ⇒ 面板那条 `v-if="store.error"` 的横幅不会亮（文案还写着「审批状态拉取失败」），
//          rejection 直接逃逸，点「批准」之后界面什么都不发生。
//
// ⚠⚠ 本节最值得记的是**判据口径的盲区**，它同时解释了 D17 为什么躲过 §854：
//
//   §854 的扫面口径是「async function 体内含 await 且**体内无 try**」。
//   而 `onApprove` 写的是 `try { … } finally { submitting = false }`
//   —— **有 try、但没有 catch**。
//   ⇒ 它被 §854 的口径**完美地排除在外**：有 try 就算「作者处理了」。
//   而事实上「有 finally 却没有 catch」恰恰是最隐蔽的一类：
//   **finally 的存在证明作者意识到「状态要复位」，只是没意识到「失败要说出来」**。
//   本判据因此用另一条口径：**找 `finally` 却找不到 `catch` 的函数体**。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join, relative } from 'node:path'

// ⚠ 本文件在 src/__tests__/ 下 ⇒ 回退**两层**才是 src。
const HERE = dirname(fileURLToPath(import.meta.url))
const SRC = join(HERE, '..')
// ★ 读入口归一 CRLF → LF（2026-10-22 Windows 实测修掉）
//
//   本仓的 .ts/.vue 在 Windows 检出是 CRLF（core.autocrlf=true），
//   而下面这些用例的「变异锚点」正则里写死了 \n ⇒ 锚点匹配不上
//   ⇒ 变异根本没发生 ⇒ 断言报「变异没生效（锚点与真源码不匹配）」
//   ⇒ 该报红的那条反而报红成「锚点没命中」，看起来像源码变了，其实没变。
//   ⇒ Linux 上源文件是 LF ⇒ 锚点命中 ⇒ **Linux 一直绿**。
//
//   归一在**读入口**（一处盖住全部锚点），不是逐条改正则
//   —— 与 D6–D10、§88 同族纪律，也与仓内既有先例一致：
//   src/features/email/__tests__/account-read-path-normalization.test.mjs:46-48。

const read = (rel: string) => readFileSync(join(SRC, rel), 'utf8').replace(/\r\n/g, '\n')
const relOf = (p: string) => relative(SRC, p).split('\\').join('/')

function collectVue(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(join(SRC, dir))) {
    if (name === '__tests__' || name === 'node_modules') continue
    const full = join(SRC, dir, name)
    let st
    try { st = statSync(full) } catch (err: any) {
      if (err?.code !== 'ENOENT') throw err
      continue
    }
    if (st.isDirectory()) collectVue(relative(SRC, full), out)
    else if (name.endsWith('.vue')) out.push(full)
  }
  return out
}
const VUE_FILES = collectVue('.')

const strip = (t: string): string =>
  t.replace(/\/\*[\s\S]*?\*\//g, (m) => m.replace(/[^\n]/g, ' '))
   .replace(/(^|[^:])\/\/[^\n]*/g, '$1')
   .replace(/<!--[\s\S]*?-->/g, (m) => m.replace(/[^\n]/g, ' '))

const scriptOf = (t: string): string => {
  const m = /<script setup[^>]*>([\s\S]*?)<\/script>/.exec(t)
  return m ? m[1] : ''
}
const templateOf = (t: string): string => {
  const m = /<template>([\s\S]*)<\/template>/.exec(t)
  return m ? m[1] : ''
}

/**
 * ⚠⚠⚠ 这段查找逻辑踩过**三次**同一个坑，必须保留这两行注释：
 *
 *   坑：`code.indexOf('{', 形参之后)` 会落在**形参的类型注解**上。
 *     `async function onApprove(p: { id: string }) { … }`
 *     ⇒ 第一个 `{` 是 `{ id: string }` 里的那个，配平立刻归零，
 *       取到的"函数体"是 `{ id: string }` —— **6 个字符**。
 *     历史：§845 的 `blockFrom`（TS 返回类型注解）、§850 的 `bodyOf`
 *     （含花括号类型注解）、**本节第 3 次**（形参类型注解）。
 *
 *   另一个坑（§854 D9）：必须**先 strip 注释**，否则注释里的
 *     `.catch(() => {})` 的花括号会成为"函数体"的起点。
 *
 * ⇒ 正确做法：① strip 注释；② 从 `(` 起做**圆括号配平**找到形参结束；
 *    ③ 再从那里找 `{`。
 */
function fnBody(rawCode: string, name: string): string {
  const code = strip(rawCode)
  const m = new RegExp(`(?:async\\s+)?function\\s+${name}\\s*\\(`).exec(code)
  if (!m) return ''
  // ② 形参：从 ( 起做圆括号配平（花括号在这段里要**跳过**，只数圆括号）
  let paren = 0
  let afterParams = -1
  for (let i = m.index + m[0].length - 1; i < code.length; i++) {
    if (code[i] === '(') paren++
    else if (code[i] === ')') {
      paren--
      if (paren === 0) { afterParams = i + 1; break }
    }
  }
  if (afterParams < 0) return ''
  // ③ 返回类型注解之后才是函数体
  const start = code.indexOf('{', afterParams)
  if (start < 0) return ''
  let d = 0
  for (let i = start; i < code.length; i++) {
    if (code[i] === '{') d++
    else if (code[i] === '}') { d--; if (d === 0) return code.slice(start, i + 1) }
  }
  return ''
}

// ─────────────────────────────────────────────────────────────────────────────
// §856 的新口径：`finally` 存在但 `catch` 不存在 ⇒ 失败无出口。
//
// ⚠ 这**不能**做成「全仓零容忍」—— 纯读取函数（`try { … } finally { … }` 且
//    不与用户动作绑定）是合法的。所以本判据只对**审批链路**这四个具体动作断言，
//    并用一个合成样本证明口径的方向是对的。
// ─────────────────────────────────────────────────────────────────────────────
const hasFinallyNoCatch = (body: string): boolean =>
  /\bfinally\s*\{/.test(body) && !/\bcatch\s*(\(|\{)/.test(body)

describe('§856 · 尺子自证', () => {
  it('★ 扫到的 .vue 数量够（尺子没空转）', () => {
    assert.ok(VUE_FILES.length > 150, `只扫到 ${VUE_FILES.length} 个 .vue`)
  })

  it('★ `finally` 无 `catch` 这条口径本身是对的（合成样本双向）', () => {
    const bad = `
      async function onApprove(p) {
        if (submitting[p.id]) return
        submitting[p.id] = true
        try {
          await store.approvePermission(p.id, msg)
        } finally {
          submitting[p.id] = false
        }
      }`
    assert.equal(hasFinallyNoCatch(bad), true, '有 finally 没 catch 必须判出来')
    const good = `
      async function onApprove(p) {
        submitting[p.id] = true
        try {
          await store.approvePermission(p.id, msg)
        } catch (e) {
          actionError.value = String(e)
        } finally {
          submitting[p.id] = false
        }
      }`
    assert.equal(hasFinallyNoCatch(good), false, '有 catch 就不能判成缺口')
    const noFinally = `
      async function onApprove(p) {
        await store.approvePermission(p.id, msg)
      }`
    assert.equal(hasFinallyNoCatch(noFinally), false,
      '没有 finally 属于另一条口径（§854 的「体内无 try」），不要混进来')
  })
})

describe('§856 · D16 审批附言不得被静默截断', () => {
  const VIEW = read('features/sessions/ApprovalPanel.vue')
  const API = read('api/approvals.ts')

  it('★★★ 提交路径上不得再有对 permMessage 的 slice 赋值', () => {
    // ⚠⚠ 这条正则的第一版写成 `permMessage\[\w+\]`，**匹配不到 `permMessage[p.id]`** ——
    //   `[]` 里带点号（M13-a 实测：判据 14/0 全绿，缺陷原样放回去一声不响）。
    //   ⇒ `[]` 里必须允许点：`[\w.]+`。
    //   ⚠ 这条是本文件里**唯一**靠正则的判据，其余都靠 fnBody 取到的真实结构 ——
    //     能用结构就别用正则（§855：名字词表穷举不完）。
    for (const fn of ['onApprove', 'onDeny']) {
      const body = fnBody(scriptOf(VIEW), fn)
      assert.ok(body, `找不到 ${fn}`)
      assert.equal(/permMessage\[[\w.]+\]\s*=\s*[\w.[\]]*\.slice\(/.test(body), false,
        `${fn} 又变回「截断用户输入再提交」⇒ 审批限制条件被无声丢弃`)
    }
  })

  it('★★★ 超限必须有可见告知（不能只是不截断就完事）', () => {
    const body = strip(scriptOf(VIEW))
    assert.match(body, /PERM_MESSAGE_MAX/, '缺少上限常量/告知逻辑的锚点')
    assert.match(body, /toast\.warning\(/,
      '超限时必须有可见告知 —— 静默丢弃与静默放过都不是「修好了」')
    assert.match(body, /noticeIfTruncated/, '两个提交路径都必须走告知函数')
  })

  it('★★ 输入框不得加 maxlength 硬截（会引入后端并不存在的新限制）', () => {
    // ⛔ 后端 PermissionReplyRequest 对 message 无长度校验 ⇒ 硬截等于前端自造限制。
    // 若属主将来决定「要有上限且不可提交」，请同步改这一条与文档的待拍板项。
    const tpl = templateOf(VIEW)
    const ta = /<textarea[^>]*v-model="permMessage\[[^\]]*\]"[\s\S]{0,400}?\/>/.exec(tpl)?.[0]
    assert.ok(ta, '找不到附言输入框')
    assert.equal(/maxlength\s*=/.test(ta), false,
      '附言框加了 maxlength ⇒ 前端硬截，与「后端无限制」的事实不符；真要限制请先拍板')
  })

  it('★ API 层不得自己截 message（防止换个入口又截一次）', () => {
    assert.equal(/\bmessage\b[^\n]*\.slice\(/.test(strip(API)), false,
      'api/approvals.ts 对 message 做了 slice —— 那是绕过视图的第二道截断')
  })
})

describe('§856 · D17 审批动作的失败必须有出口', () => {
  const VIEW = read('features/sessions/ApprovalPanel.vue')
  const STORE = read('stores/approval.ts')

  it('★★★ 批准/拒绝/回答/跳过四处的失败都不得零反馈', () => {
    for (const fn of ['onApprove', 'onDeny', 'onAnswer', 'onSkip']) {
      const body = fnBody(scriptOf(VIEW), fn)
      assert.ok(body, `找不到 ${fn}`)
      assert.equal(hasFinallyNoCatch(body), false,
        `${fn} 有 finally 没 catch ⇒ 失败时 rejection 逃逸且界面零反馈`)
      assert.match(body, /catch[\s\S]{0,200}actionError\.value\s*=/,
        `${fn} 的失败必须写进 actionError`)
    }
  })

  it('★★★ 四个动作开头都要清掉上一条的失败提示', () => {
    const code = scriptOf(VIEW)
    for (const fn of ['onApprove', 'onDeny', 'onAnswer', 'onSkip']) {
      const body = fnBody(code, fn)
      const firstAwait = body.search(/\bawait\b/)
      const clearAt = body.indexOf('actionError.value =')
      assert.ok(clearAt > 0 && firstAwait > 0, `${fn} 结构变了`)
      assert.ok(clearAt < firstAwait,
        `${fn} 的清空排在 await 之后 ⇒ 上一次失败提示会一直挂到下次成功`)
    }
  })

  it('★★ 失败提示必须渲染，且不与 hasPending 互斥', () => {
    const tpl = templateOf(VIEW)
    assert.match(tpl, /v-if="actionError"/, '模板缺少 actionError 的出口')
    assert.match(tpl, /v-if="store\.error"/, '拉取失败横幅不应消失')
    assert.match(tpl, /\{\{\s*actionError\s*\}\}/, 'actionError 必须被渲染出来')
    // 批准成功后该审批项会从 store 消失 ⇒ hasPending 转 false
    // ⇒ 提示若在 hasPending 分支里就等于「成功了才显示失败」。
    const panelIdx = tpl.indexOf('v-if="store.hasPending"')
    const actionIdx = tpl.indexOf('v-if="actionError"')
    assert.ok(actionIdx > 0 && panelIdx > 0, '模板结构变了')
    assert.notEqual(/v-else-if="store\.hasPending"/.test(tpl), true,
      'hasPending 改成了 v-else-if ⇒ 必须重新核对失败提示是否还会渲染')
  })

  it('★★ 四个动作共用的那条 store 方法不得吞错（否则 catch 永远不触发）', () => {
    // `doReplyPermission` / `answerQuestion` / `skipQuestion` 都直接 await，
    // **不设 store.error** ⇒ 复用 store.error 等于「批准失败但横幅写着拉取失败」。
    const body = fnBody(STORE, 'doReplyPermission')
    assert.ok(body, '找不到 doReplyPermission')
    assert.equal(/\berror\.value\s*=/.test(body), false,
      'doReplyPermission 开始自己设 store.error ⇒ actionError 与它会各说各话')
  })

  it('★ store 的 error 只由 fetchPending 写（这是本节结论的前提）', () => {
    const body = fnBody(STORE, 'fetchPending')
    assert.ok(body, '找不到 fetchPending')
    assert.match(body, /error\.value\s*=/, 'fetchPending 不再设 error ⇒ 前提失效')
  })
})

describe('§856 · 变异的反向对照（判据不能过宽）', () => {
  it('★ 「catch 里只是 console.error」不算出口', () => {
    // §847/§854 的反复结论：只有 console.error 等于没有出口。
    const body = `
      try { await store.approvePermission(p.id, msg) }
      catch (e) { console.error('failed', e) }
      finally { submitting[p.id] = false }`
    assert.equal(hasFinallyNoCatch(body), false, '有 catch，不该被本条口径判红（D17 不适用）')
    assert.equal(/actionError\.value\s*=/.test(body), false,
      '但它也不该被算作「有出口」——出口判据查的是 actionError，不是 catch 的存在')
  })
})

describe('§856 · 变异负控', () => {
  it('★ 把 D16 的截断放回去 ⇒ 判据转红', () => {
    const orig = read('features/sessions/ApprovalPanel.vue')
    const mutated = orig.replace(
      '  noticeIfTruncated(permMessage[p.id])\n  submitting[p.id] = true',
      "  if (permMessage[p.id].length > 500) permMessage[p.id] = permMessage[p.id].slice(0, 500)\n  submitting[p.id] = true")
    assert.notEqual(mutated, orig, '★ 变异没生效')
    const body = fnBody(scriptOf(mutated), 'onApprove')
    assert.match(body, /permMessage\[p\.id\]\s*=\s*permMessage\[p\.id\]\.slice\(/, '变异后应出现截断赋值')
  })

  it('★ 把 D17 的 catch 删掉 ⇒ 判据转红', () => {
    const orig = read('features/sessions/ApprovalPanel.vue')
    const mutated = orig.replace(
      /catch \(e: any\) \{\s*\n(\s*\/\/[^\n]*\n)*\s*actionError\.value = apiError\(e, 'errors\.operateFailed'\)\s*\n\s*\}/g, '')
    assert.notEqual(mutated, orig, '★ 变异没生效：没匹配到 catch 块')
    const body = fnBody(scriptOf(mutated), 'onApprove')
    assert.equal(hasFinallyNoCatch(body), true, '变异后 onApprove 应回到「有 finally 没 catch」')
  })
})
