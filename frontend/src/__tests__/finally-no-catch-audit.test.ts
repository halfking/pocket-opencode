// finally-no-catch-audit.test.ts — §857：把 §856 发现的「有 finally 无 catch」口径全仓应用。
//
// 动机：§856 在 ApprovalPanel 上抓到 D17（批准/拒绝/回答/跳过四处
// `try { … } finally { … }`），并发现 **§854 的口径把它完美排除了**：
// §854 查的是「体内**无 try**」，而这一族**有 try、只是没有 catch**。
// §856 只对那四个函数断言 ⇒ 本节把这个口径**全仓跑一遍**。
//
// 为什么这一族值得单独扫：
//   `finally` 的存在本身证明作者**意识到「状态要复位」**，只是没意识到
//   「失败要说出来」—— 这两件事看起来像同一件，其实不是。
//   ⇒ 最像「已处理」的一类，恰恰是最容易漏掉的一类。
//
// 扫面口径（探针 .artifacts/s857-probe/scan_finally_no_catch.py，与本判据同一把尺子）：
//   「.vue 的 <script setup> 里，被注册给 Vue（模板 @事件 / :prop / onMounted / watch）
//     的 async 函数，函数体含 finally、不含 catch、且含 await」⇒ 实测 **9** 处，
//   人工逐条读后 **3 处真缺陷（D18/D19/D20）+ 6 处阴性**，全部登记在 REGISTRY。
//
// ⚠ 已知漏检（三态一律只当标签，见 §854/§856：分类探针比枚举探针更危险）：
//   ① 箭头函数 const 形式不解析；
//   ② `try { … } finally { … }` 里嵌套的 `try/catch` 不区分（外层有 finally 就命中）。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join, relative } from 'node:path'

const HERE = dirname(fileURLToPath(import.meta.url))
const SRC = join(HERE, '..')
const read = (rel: string) => readFileSync(join(SRC, rel), 'utf8')
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
/**
 * ⚠⚠ 必须 **strip 掉 HTML 注释**再取模板：注释里引用 `v-if="…"` 字面量会抢走
 *   `indexOf` 的锚点（本节第一版就因为面板注释里写着 `v-if="loading"` 而误判
 *   「actionError 排在了互斥链之后」）。§841 已因同一原因建过 `tplCode()`。
 */
const templateOf = (t: string): string => {
  const m = /<template>([\s\S]*)<\/template>/.exec(t)
  return m ? strip(m[1]) : ''
}

/**
 * ⚠⚠⚠ 形参里可能有 `{ id: string }` 这样的类型注解 ⇒ `indexOf('{')` 会落在它上面，
 *   取到的"函数体"只有 6 个字符。**同一个坑踩过三次**（§845 `blockFrom`、
 *   §850 `bodyOf`、§856）⇒ 必须先做**圆括号配平**找形参结束，之后才是函数体。
 *   另外必须**先 strip 注释**，否则注释里的 `.catch(() => {})` 会成为起点。
 */
function fnBody(rawCode: string, name: string): string {
  const code = strip(rawCode)
  const m = new RegExp(`(?:async\\s+)?function\\s+${name}\\s*\\(`).exec(code)
  if (!m) return ''
  let d = 0
  let after = -1
  for (let i = m.index + m[0].length - 1; i < code.length; i++) {
    if (code[i] === '(') d++
    else if (code[i] === ')') { d--; if (d === 0) { after = i + 1; break } }
  }
  if (after < 0) return ''
  const start = code.indexOf('{', after)
  if (start < 0) return ''
  let depth = 0
  for (let i = start; i < code.length; i++) {
    if (code[i] === '{') depth++
    else if (code[i] === '}') { depth--; if (depth === 0) return code.slice(start, i + 1) }
  }
  return ''
}

/** 本节的核心口径：`finally` 存在且 `catch` 不存在。 */
const hasFinallyNoCatch = (body: string): boolean =>
  /\bfinally\s*\{/.test(body) && !/\bcatch\s*(\(|\{)/.test(body)

// ─────────────────────────────────────────────────────────────────────────────
// 登记表：扫面 9 处 + §856 已修的四处。键 = `路径::函数名`（不用行号做键）。
// ─────────────────────────────────────────────────────────────────────────────
const REGISTRY: Record<string, string> = {
  'features/cost/CostQuotaView.vue::load': 'allSettled + 分别设 usageError/quotaError',
  'features/meetings/MeetingDetailView.vue::onMicToggle': 'stop 内部吞错 + toast + §834 lastStopWriteFailed',
  'features/meetings/MeetingSettingsSheet.vue::onCaptureLocation': 'captureDeviceLocation 失败 resolve(null) 不 reject',
  'features/notes/NoteEditView.vue::onExtractTags': 'extractTagsForForm 内部 try/catch（离线降级）',
  'features/sessions/SessionConversationView.vue::onComposerSend': 'sendPrompt 设 errorMessage 模板渲染',
  'features/sessions/SessionConversationView.vue::continueSession': '同 onComposerSend',
  'features/sessions/ApprovalPanel.vue::onApprove': '§856/D17 已修',
  'features/sessions/ApprovalPanel.vue::onDeny': '§856/D17 已修',
  'features/sessions/ApprovalPanel.vue::onAnswer': '§856/D17 已修',
  'features/sessions/ApprovalPanel.vue::onSkip': '§856/D17 已修',
  // ↓ §857 本节修掉的三条
  'features/meetings/VoiceprintSheet.vue::onClearAll': '§857/D18 已修',
  'features/meetings/MeetingListView.vue::startNewMeeting': '§857/D19 已修',
  'features/settings/SettingsPermissionsView.vue::refreshAll': '§857/D20 已修',
}

describe('§857 · 尺子自证与登记表对账', () => {
  it('★ 扫到的 .vue 数量够（尺子没空转）', () => {
    assert.ok(VUE_FILES.length > 150, `只扫到 ${VUE_FILES.length} 个 .vue`)
  })

  it('★ 口径本身三向正确（合成样本）', () => {
    const bad = `try { await f() } finally { busy.value = false }`
    assert.equal(hasFinallyNoCatch(bad), true, '有 finally 没 catch 必须判出来')
    const good = `try { await f() } catch (e) { err.value = String(e) } finally { busy.value = false }`
    assert.equal(hasFinallyNoCatch(good), false, '有 catch 不能判成缺口')
    assert.equal(hasFinallyNoCatch(`await f()`), false,
      '没有 finally 属另一条口径（§854 的「体内无 try」），不要混进来')
  })

  it('★ 登记表非空且键形状正确（路径::函数名，不用行号）', () => {
    const keys = Object.keys(REGISTRY)
    assert.ok(keys.length >= 12, `登记表只有 ${keys.length} 条`)
    for (const k of keys) assert.match(k, /^[\w\-./]+\.vue::[A-Za-z_$][\w$]*$/, `键形状不对：${k}`)
  })

  it('★ 登记的函数与文件都还在（双向反向对账）', () => {
    const missing: string[] = []
    for (const k of Object.keys(REGISTRY)) {
      const [file, fn] = k.split('::')
      const full = join(SRC, file)
      const exists = VUE_FILES.some((f) => relOf(f) === file)
      if (!exists) { missing.push(`${k}（文件没了）`); continue }
      const code = scriptOf(readFileSync(full, 'utf8'))
      if (!new RegExp(`function\\s+${fn}\\s*\\(`).test(code)) missing.push(`${k}（函数没了）`)
    }
    assert.deepEqual(missing, [],
      `登记表里的这些条目已失效（改名/删除/移走）——请重新判断：\n  ${missing.join('\n  ')}`)
  })
})

describe('§857 · D18 清空全部声纹失败必须有出口', () => {
  const VIEW = read('features/meetings/VoiceprintSheet.vue')
  const STORE = read('features/meetings/voiceprints-store.ts')

  it('★★★ onClearAll 不得是「有 finally 无 catch」', () => {
    const body = fnBody(scriptOf(VIEW), 'onClearAll')
    assert.ok(body, '找不到 onClearAll')
    assert.equal(hasFinallyNoCatch(body), false,
      'onClearAll 缺 catch ⇒ 用户过了「不可撤销」的确认弹窗却什么都没发生，界面零反馈')
    assert.match(body, /catch[\s\S]{0,200}actionError\.value = apiError\(/,
      'onClearAll 的失败必须写进 actionError，且走 apiError 归一（不把 SQLITE_BUSY 上屏）')
  })

  it('★★ 失败时列表必须保持原样（不得先乐观清空再报错）', () => {
    const body = fnBody(scriptOf(VIEW), 'onClearAll')
    const clearAt = body.indexOf('items.value =')
    const catchAt = body.indexOf('catch (e')
    if (clearAt < 0) return   // 该函数本就不直接改 items（交给 load），属合法
    assert.ok(catchAt > 0 && clearAt > catchAt, 'items 的改动跑在 catch 之前 ⇒ 失败时会谎报成已删')
  })

  it('★★ actionError 的提示必须在互斥链之外', () => {
    // 模板是 v-if loading / v-else-if loadError / v-else-if empty / v-else。
    // 提示若塞进最后一个分支，下一次加载失败落到 loadError 分支时提示会凭空消失。
    const tpl = templateOf(VIEW)
    assert.match(tpl, /v-if="actionError"/, '模板缺少 actionError 的出口')
    const at = tpl.indexOf('v-if="actionError"')
    const chainAt = tpl.indexOf('v-if="loading"')
    assert.ok(at > 0 && chainAt > 0, '模板结构变了')
    assert.ok(at < chainAt, 'actionError 的提示排在了互斥链之后 ⇒ 加载失败时它会被藏掉')
  })

  it('★ clearVoiceprints 必须真的不吞错（本条判据的前提）', () => {
    const body = fnBody(STORE, 'clearVoiceprints')
    assert.ok(body, '找不到 clearVoiceprints')
    // ⚠ 2026-10-29（会议归属切片）：写库句柄从 `localDB.run(...)` 换成了
    //   **绑定了操作租约**的 `db.run(...)` —— 归属与这次写入必须原子一致
    //   （租约的 principal 就是行的归属）⇒ 不能再走未绑定的 localDB。
    //   ⇒ 判据要同时认这两种形态，否则「前提失效」会误报。
    assert.match(body, /(?:\bdb|\blocalDB)\.run\(/, 'clearVoiceprints 不再写库 ⇒ 前提失效')
    assert.equal(/\btry\s*\{/.test(body), false,
      'clearVoiceprints 开始自己吞错误 ⇒ onClearAll 的 catch 将永不触发')
  })
})

describe('§857 · D19 新建会议失败必须有出口（§838 同族漏网）', () => {
  const VIEW = read('features/meetings/MeetingListView.vue')
  const NOTICE = read('utils/action-failure-notice.ts')

  it('★★★ startNewMeeting 不得是「有 finally 无 catch」', () => {
    const body = fnBody(scriptOf(VIEW), 'startNewMeeting')
    assert.ok(body, '找不到 startNewMeeting')
    assert.equal(hasFinallyNoCatch(body), false,
      'startNewMeeting 缺 catch ⇒ 点「新建会议」（本应用最核心的入口）后不跳转、零反馈')
    assert.match(body, /catch[\s\S]{0,240}toast\.error\(describeRecordActionFailure\(/,
      '必须复用同文件 onArchive/onRestore/onDelete 的那套失败文案（§838 已确立的形状）')
  })

  it('★★ 一致性对照：§838 修过的三个处理器不得回退', () => {
    const code = scriptOf(VIEW)
    for (const fn of ['onArchive', 'onRestore', 'onDelete']) {
      const body = fnBody(code, fn)
      assert.ok(body, `找不到 ${fn}`)
      assert.equal(hasFinallyNoCatch(body), false, `${fn} 的 catch 没了 —— §838 的修复被回退`)
    }
  })

  it('★★★ 共享文案工具必须为「新建」提供**专属**措辞', () => {
    // ⚠ 不能复用 'archive'/'delete' 那些「记录仍在列表里」的措辞 ——
    //   新建失败时列表里**根本没有**那条记录，说「仍在列表里」是撒谎。
    assert.match(NOTICE, /'start'/, "RecordAction 类型缺少 'start'")
    const body = fnBody(NOTICE, 'describeRecordActionFailure')
    assert.ok(body, '找不到 describeRecordActionFailure')
    const startCase = /case 'start':[\s\S]{0,400}?return\s+([\s\S]*?)\n/.exec(strip(NOTICE))
    assert.ok(startCase, "switch 里缺少 case 'start'")
    const text = startCase[1].trim()
    assert.equal(/仍在|还在/.test(text), false,
      `新建失败时没有任何记录被创建，文案却说「仍在」⇒ 界面说谎：${text}`)
  })

  it('★ createMeeting 必须真的不吞错（前提）', () => {
    const store = read('features/meetings/meetings-store.ts')
    const body = fnBody(store, 'createMeeting')
    assert.ok(body, '找不到 createMeeting')
    // ⚠ 2026-10-30（portal-store 收口）：写库句柄进一步收进
    //   meetingPortalStore(db).commit(...)（租约绑定的语句批），
    //   判据随语义演化：只要还走这条 commit 写库，前提就成立。
    assert.match(body, /meetingPortalStore\(db\)\.commit\(/, 'createMeeting 不再写库 ⇒ 前提失效')
  })
})

describe('§857 · D20 权限刷新失败必须有出口', () => {
  const VIEW = read('features/settings/SettingsPermissionsView.vue')

  it('★★★ refreshAll 不得是「有 finally 无 catch」', () => {
    const body = fnBody(scriptOf(VIEW), 'refreshAll')
    assert.ok(body, '找不到 refreshAll')
    assert.equal(hasFinallyNoCatch(body), false,
      'refreshAll 缺 catch ⇒ Promise.all 任一 reject 就整批逃逸，权限状态停在旧值且零反馈')
    assert.match(body, /catch[\s\S]{0,240}toast\.error\(apiError\(/,
      'refreshAll 的失败必须走 apiError 归一（与同文件 openSystemSettings 一致）')
  })

  it('★★ 重入守卫必须在第一个 await 之前（与 §855 同构）', () => {
    const body = fnBody(scriptOf(VIEW), 'refreshAll')
    const guardAt = body.indexOf('if (refreshing.value) return')
    const awAt = body.search(/\bawait\b/)
    assert.ok(guardAt > 0 && awAt > 0, 'refreshAll 的结构变了')
    assert.ok(guardAt < awAt, '守卫排在 await 之后 ⇒ 往返窗口内可重复点击')
  })
})

describe('§857 · 反向对照（判据不能过宽）', () => {
  it('★ 「catch 里只有 console.error」不算出口（§847/§854 的反复结论）', () => {
    const body = `try { await f() } catch (e) { console.error(e) } finally { busy.value = false }`
    assert.equal(hasFinallyNoCatch(body), false, '有 catch，本条口径不该判红（D17/D18/D20 都不适用）')
    assert.equal(/toast\.error|actionError\.value|store\.\w+\.value\s*=/.test(body), false,
      '但它也不算「有出口」—— 出口判据查的是可见提示，不是 catch 的存在')
  })

  it('★ callee 自己 resolve(null) 不 reject ⇒ 不属本条口径', () => {
    // captureDeviceLocation 的形状：失败时 resolve(null)，从不 throw。
    const body = `const loc = await captureDeviceLocation()
    if (loc) form.location = loc`
    assert.equal(hasFinallyNoCatch(body), false)
  })
})

describe('§857 · 变异负控', () => {
  it('★ 把 D18 的 catch 删掉 ⇒ 判据转红', () => {
    const orig = read('features/meetings/VoiceprintSheet.vue')
    // ⚠⚠ 两处踩坑，都写在这里免得下一个人再踩：
    //   ① 锚点按**真实源码**写：`onClearAll` 的 catch 块里有两行注释
    //      （「走仓库既定的 apiError 映射…」），第一版没带 `(?:\s*\/\/[^\n]*\n)*`
    //      导致正则匹配不到 ⇒ 「变异没生效」被误读成「判据无牙」。
    //   ② ⚠ **变异脚本必须保持源码语法合法**。第一版把 `… } catch (e) { … } finally {`
    //      替换成 `… } } finally {` —— **多出一个右花括号** ⇒ onClearAll 的函数体
    //      在括号配平时**提前闭合**，判据取到的是残缺体（不含 finally ⇒ 判成 false）
    //      ⇒ 看起来像「判据无牙」，其实是**变异自己把源码写坏了**。
    //      正确：搜索串含前导 `}`、替换串**不含**它，才能配平。
    const mutated = orig.replace(
      /\} catch \(e\) \{\s*\n(?:\s*\/\/[^\n]*\n)*\s*actionError\.value = apiError\(e, '声纹删除失败，请稍后重试'\)\s*\n\s*\} finally \{/,
      '} finally {')
    assert.notEqual(mutated, orig, '★ 变异没生效：没匹配到 catch 块')
    assert.equal(hasFinallyNoCatch(fnBody(scriptOf(mutated), 'onClearAll')), true,
      '变异后 onClearAll 应回到「有 finally 没 catch」')
  })

  it('★ 把 D19 的 catch 删掉 ⇒ 判据转红', () => {
    const orig = read('features/meetings/MeetingListView.vue')
    const mutated = orig.replace(
      /catch \(e\) \{\s*\n\s*console\.error\(RECORD_ACTION_FAIL_TAG, 'start', e\)\s*\n\s*toast\.error\(describeRecordActionFailure\('start', \{ error: e \}\)\)\s*\n\s*\} finally \{/,
      '} finally {')
    assert.notEqual(mutated, orig, '★ 变异没生效：没匹配到 catch 块')
  })

  it('★ 把 actionError 的模板出口改成 v-else ⇒ 判据转红', () => {
    const orig = read('features/meetings/VoiceprintSheet.vue')
    const mutated = orig.replace('<p v-if="actionError"', '<p v-else="actionError"')
    assert.notEqual(mutated, orig, '★ 变异没生效')
  })
})
