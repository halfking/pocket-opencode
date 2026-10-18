// submit-guard-audit.test.ts — §855：会创建数据的动作，第二次点击必须被拦住。
//
// 起点：§854 扫的是「用户点了、失败了，看得见吗」；本节扫它的邻居
// 「用户点了一下，结果做了两次」。
//
// ─────────────────────────────────────────────────────────────────────────────
// ⭐ 本节的核心是一条**机制**，不是一张名单：
//
//   「模板 :disabled 能不能拦住第二次点击」取决于**它的条件在 handler 的第一个
//   await 之前是否同步变真**。
//
//   理由：Vue 的响应式 DOM 更新在 **nextTick（微任务）** 里；而两个独立的
//   click 事件之间，微任务队列**必然排空**。⇒ 只要 disabled 的条件在第一个
//   await 之前已经变真，第二击落到按钮上时它已是 disabled，浏览器不派发事件。
//
//   两个方向都实测过（不是推理而已）：
//     ✅ 拦得住：`CalendarEventSheet::save` 的 :disabled 绑 `store.saving`，
//        而 `store.createEvent` 的**第一行**就是 `saving.value = true`（await 之前同步）；
//        `TasksView::submitAccDelegate` 的 `accStore.submitting` 同理。
//     ❌ 拦不住：`TasksView::handleCreate` 原来的 :disabled 绑 `!newTask.title`，
//        而 `newTask.title` 要 **await api.createTask 之后**才被清空
//        ⇒ 整个网络往返窗口都是可点的；`FinanceView::quickConfirm` 的确认按钮
//        **连 :disabled 都没有**。
//
//   ⇒ 所以本判据查的是「**第一个 await 之前**有没有东西变真」，不是「有没有 disabled」。
//      「模板里写了 disabled」不等于有保护 —— 这正是本节两处真缺陷的共同形态。
//
// 扫面读数（探针 .artifacts/s855-probe/scan_dblclick.py，与本判据同一把尺子）：
//   口径 = .vue 里「注册给了模板 + 函数体含 await + 体内有创建类动作
//   （create/add/send/insert/submit/install/upload/publish/start/begin/register）
//   + 既无 `X.value=true…finally X.value=false` 的结构化守卫、也无 `if (X) return`」
//   实测：宽口径 48 → 收窄到创建类 **14** → 人工逐条读后 **3 条真缺陷 + 11 条阴性**。
//   ⛔ 收窄的理由写在探针注释里：`deleteEvent`/`remove`/`rename` 的第二次调用通常是
//      noop（id 已不存在），后果远轻于「多出两条记录」。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join, relative } from 'node:path'

// ⚠ 本文件在 src/__tests__/ 下 ⇒ 回退**两层**才是 src（features 下是三层）。
const HERE = dirname(fileURLToPath(import.meta.url))
const SRC = join(HERE, '..')
const read = (rel: string) => readFileSync(join(SRC, rel), 'utf8')

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
const relOf = (p: string) => relative(SRC, p).split('\\').join('/')

const strip = (t: string): string =>
  t.replace(/\/\*[\s\S]*?\*\//g, (m) => m.replace(/[^\n]/g, ' '))
   .replace(/(^|[^:])\/\/[^\n]*/g, '$1')
   .replace(/<!--[\s\S]*?-->/g, (m) => m.replace(/[^\n]/g, ' '))

const templateOf = (t: string): string => {
  const m = /<template>([\s\S]*)<\/template>/.exec(t)
  return m ? m[1] : ''
}
const scriptOf = (t: string): string => {
  const m = /<script setup[^>]*>([\s\S]*?)<\/script>/.exec(t)
  return m ? m[1] : ''
}

/** ⚠ 必须先 strip 注释再找 `{`：函数上方的注释里可能有 `.catch(() => {})` 这类花括号
 *  （§854 的 D9 判据就是这样在自己修好的源码上假红的）。 */
function fnBody(rawCode: string, name: string): string {
  const code = strip(rawCode)
  const m = new RegExp(`(?:async\\s+)?function\\s+${name}\\s*\\(`).exec(code)
  if (!m) return ''
  const start = code.indexOf('{', m.index + m[0].length)
  if (start < 0) return ''
  let d = 0
  for (let i = start; i < code.length; i++) {
    if (code[i] === '{') d++
    else if (code[i] === '}') { d--; if (d === 0) return code.slice(start, i + 1) }
  }
  return ''
}

/** 第一个 `await` 之前的位置（-1 表示函数体里没有 await）。 */
const firstAwait = (body: string): number => body.search(/\bawait\b/)

/**
 * 结构化的「正在提交」守卫：某个 ref 置 true，并在 finally 里复位为 false。
 * ⚠ 刻意**不用名字词表**（submitting/loading/busy/…）：词表是穷举不完的，
 * 探针 v5 第一版就是漏了 `deckCreating`/`creating`/`confirming` 而假阴。
 */
const busyStructure = (body: string): boolean =>
  /([A-Za-z_$][\w$.]*)\.value\s*=\s*true[\s\S]{0,2000}?finally\s*\{[\s\S]{0,400}?\1\.value\s*=\s*false/
    .test(body)

// ─────────────────────────────────────────────────────────────────────────────
// 登记表：扫面 14 条候选 + 本节修掉的 3 条。键 = `路径::函数名`（不用行号做键，
// §844 的 z-index-ladder 已咬了六次）。
// 值 = 阴性理由关键词 / 已修编号。
// ─────────────────────────────────────────────────────────────────────────────
const REGISTRY: Record<string, string> = {
  'components/business/UnifiedComposer.vue::onMicUp': '不写数据：停录音/发事件（假阳）',
  'components/business/UnifiedComposer.vue::onCamera': '不写数据：只加附件（假阳）',
  'features/calendar/CalendarEventSheet.vue::save': '模板 disabled 绑 store.saving，createEvent 第一行即置 true',
  'features/email/EmailAccountAddView.vue::saveAndVerify': '模板 disabled 绑 busy，函数首行置 true',
  'features/email/EmailAccountSetup.vue::testAndSave': '模板 disabled 绑 testing，函数首行置 true',
  'features/marketplace/AgentMarketView.vue::runInstall': 'installTarget 末行置 null 即重入守卫',
  'features/marketplace/SkillMarketView.vue::runInstall': 'installTarget 末行置 null 即重入守卫',
  'features/marketplace/WorkbuddyView.vue::runInstall': 'installTarget 末行置 null 即重入守卫',
  'features/meetings/MeetingDetailView.vue::onSummarize': '模板 disabled 绑 summarizing，函数首行置 true',
  'features/notes/NoteDetailView.vue::summarize': '函数体显式 if (summarizing.value) return',
  'features/notes/NoteEditView.vue::onSave': '函数体显式 if (saving.value) return',
  'features/notes/NoteListView.vue::onMicToggle': 'toggleRecording 内部 isRecording 守卫（toggle 语义）',
  'features/settings/SettingsSTT.vue::onRecordProbe': 'if (recording) stop —— toggle 语义即重入守卫',
  'features/tasks/TasksView.vue::submitAccDelegate': '模板 disabled 绑 accStore.submitting，createTask 首行置 true',
  // ↓ §855 本节修掉的三条
  'features/tasks/TasksView.vue::handleCreate': '§855/D13 已修',
  'features/finance/FinanceView.vue::quickConfirm': '§855/D14 已修',
  'features/finance/FinanceView.vue::remove': '§855/D15 已修',
}

describe('§855 · 尺子自证与登记表对账', () => {
  it('★ 扫到的 .vue 数量够（尺子没空转）', () => {
    assert.ok(VUE_FILES.length > 150, `只扫到 ${VUE_FILES.length} 个 .vue`)
  })

  it('★ 登记表非空且键形状正确（路径::函数名，不用行号）', () => {
    const keys = Object.keys(REGISTRY)
    assert.ok(keys.length >= 15, `登记表只有 ${keys.length} 条`)
    for (const k of keys) assert.match(k, /^[\w\-./]+\.vue::[A-Za-z_$][\w$]*$/, `键形状不对：${k}`)
  })

  it('★ 登记的函数都还在（反向对账：改名/删除会逼你重新判断）', () => {
    const missing: string[] = []
    for (const k of Object.keys(REGISTRY)) {
      const [file, fn] = k.split('::')
      const vue = read(file)
      const code = scriptOf(vue)
      const found = new RegExp(`(?:async\\s+)?function\\s+${fn}\\s*\\(`).test(code)
        || new RegExp(`(?:const|let)\\s+${fn}\\s*=\\s*(?:async\\s*)?(?:function\\b|\\()`).test(code)
      if (!found) missing.push(k)
    }
    assert.deepEqual(missing, [],
      `登记表里的这些函数已不存在（改名或删除）——请重新判断：\n  ${missing.join('\n  ')}`)
  })

  it('★ 登记表里的 .vue 都还在（反向对账：文件被删/改名同样要重判）', () => {
    const gone: string[] = []
    for (const k of Object.keys(REGISTRY)) {
      const file = k.split('::')[0]
      const exists = VUE_FILES.some((f) => relOf(f) === file)
      if (!exists) gone.push(file)
    }
    assert.deepEqual(gone, [], `登记表里的这些文件已不存在：\n  ${gone.join('\n  ')}`)
  })
})

describe('§855 · D13 新建任务：第二个点击必须被拦住', () => {
  const VIEW = read('features/tasks/TasksView.vue')

  it('★★★ handleCreate 必须有重入守卫', () => {
    const body = fnBody(scriptOf(VIEW), 'handleCreate')
    assert.ok(body, '找不到 handleCreate')
    assert.match(body, /if\s*\([^)]*creating\.value\s*\)\s*return/,
      'handleCreate 缺 `if (…creating.value) return` ⇒ 一次网络往返窗口内双击会创建两条任务')
  })

  it('★★★ creating 必须在第一个 await 之前置 true、并在 finally 里复位', () => {
    const body = fnBody(scriptOf(VIEW), 'handleCreate')
    const setAt = body.indexOf('creating.value = true')
    const awAt = firstAwait(body)
    assert.ok(setAt > 0 && awAt > 0, 'handleCreate 的结构变了 —— 请重新判断')
    assert.ok(setAt < awAt, 'creating 置 true 排在第一个 await 之后 ⇒ 守卫本身就有窗口')
    assert.match(body, /finally\s*\{[^}]*creating\.value = false/,
      'creating 没有在 finally 里复位 ⇒ 失败一次后按钮永久禁用')
  })

  it('★★★ 按钮的 disabled 必须绑 creating（不能只绑 !title）', () => {
    // 这条是本节的**机制锚点**：`:disabled="!newTask.title"` 的条件要等
    // await 之后才清空 ⇒ 拦不住往返窗口。判据要钉住「绑的是 creating」。
    const tpl = templateOf(VIEW)
    const btn = /<button[^>]*@click="handleCreate"[^>]*>/.exec(tpl)?.[0]
    assert.ok(btn, '找不到 handleCreate 的按钮')
    assert.match(btn, /:disabled="[^"]*creating[^"]*"/,
      '「创建」按钮的 :disabled 没绑 creating ⇒ 只有一个要等 await 之后才变真的条件')
  })

  it('★★ 创建失败必须有用户可见提示（原来只有 console.error）', () => {
    const body = fnBody(scriptOf(VIEW), 'handleCreate')
    assert.match(body, /catch[\s\S]{0,200}toast\.error\(/,
      'handleCreate 的失败只有 console.error ⇒ 弹窗留着但零提示，用户只会当成没点到')
  })
})

describe('§855 · D14/D15 财务「确认入账」与「删除」', () => {
  const VIEW = read('features/finance/FinanceView.vue')

  it('★★★ quickConfirm 必须有重入守卫 + finally 复位', () => {
    const body = fnBody(scriptOf(VIEW), 'quickConfirm')
    assert.ok(body, '找不到 quickConfirm')
    assert.match(body, /if\s*\([^)]*confirming\.value\s*\)\s*return/,
      'quickConfirm 缺重入守卫 ⇒ 双击会入账两条（两条真实财务流水）')
    assert.match(body, /finally\s*\{[^}]*confirming\.value = false/,
      'confirming 没有在 finally 里复位')
  })

  it('★★★ 「确认入账」按钮必须绑 confirming', () => {
    const tpl = templateOf(VIEW)
    const btn = /<button[^>]*@click="quickConfirm"[^>]*>/.exec(tpl)?.[0]
    assert.ok(btn, '找不到「确认入账」按钮')
    assert.match(btn, /:disabled="[^"]*confirming[^"]*"/,
      '「确认入账」按钮没有 :disabled —— 这是本节最直接的形态')
  })

  it('★★★ remove 必须按 id 守卫（不是布尔：列表里多行同时可见）', () => {
    const body = fnBody(scriptOf(VIEW), 'remove')
    assert.ok(body, '找不到 remove')
    assert.match(body, /if\s*\(\s*removingId\.value === tx\.id\s*\)\s*return/,
      'remove 缺按 id 的守卫 ⇒ 双击时第二次对已删 id 发请求，404 后界面报「操作失败」而流水确实消失了（界面说谎）')
    assert.match(body, /finally\s*\{[\s\S]{0,160}?removingId\.value = null/,
      'removingId 没有在 finally 里复位')
  })

  it('★★ 同文件的「记账」按钮本来就有守卫（一致性对照样本不能变）', () => {
    const tpl = templateOf(VIEW)
    const btn = /<button[^>]*@click="quickParse"[^>]*>/.exec(tpl)?.[0]
    assert.ok(btn, '找不到「记账」按钮')
    assert.match(btn, /:disabled="[^"]*quickBusy[^"]*"/,
      '「记账」按钮的 quickBusy 守卫没了 —— D14 的「同文件两种待遇」论证前提失效')
  })
})

describe('§855 · 机制的���向对照（判据不能过宽，也不能只认「写了 disabled」）', () => {
  it('★ disabled 条件在第一个 await 之前变真 ⇒ 算有保护', () => {
    const guarded = `
      function save() {
        busy.value = true            // 第一个 await 之前就变真
        try { await api.create(x); } finally { busy.value = false }
      }`
    assert.equal(busyStructure(guarded), true)
    assert.ok(firstAwait(guarded) > guarded.indexOf('busy.value = true'),
      '置位必须排在 await 之前')
  })

  it('★ disabled 条件要等 await 之后才变 ⇒ 不算有保护', () => {
    // 这就是 D13 的原形态：模板有 :disabled，但它绑的东西在 await 之后才清空。
    const window = `
      function handleCreate() {
        if (!form.title) return
        try {
          await api.createTask({ id: 'task-' + Date.now(), title: form.title })
          form.title = ''            // ⛔ 按钮要等这一行才变灰
        } finally { }
      }`
    assert.equal(busyStructure(window), false,
      '这条必须判成缺口，否则 D13 那类缺陷永远是绿的')
    assert.equal(/if\s*\(\s*creating\.value\s*\)\s*return/.test(window), false)
  })

  it('★ 置位在 finally 之外 ⇒ 失败一次后按钮永久禁用，也算缺口', () => {
    const noFinally = `
      function save() {
        busy.value = true
        await api.create(x)
        busy.value = false
      }`
    assert.equal(busyStructure(noFinally), false,
      '没有 finally 复位 ⇒ 一次异常就让这个动作永久不可用')
  })
})

describe('§855 · 变异负控', () => {
  it('★ 把 D13 的守卫删掉 ⇒ 判据转红', () => {
    const orig = read('features/tasks/TasksView.vue')
    const mutated = orig.replace('if (!newTask.value.title || creating.value) return',
      'if (!newTask.value.title) return')
    assert.notEqual(mutated, orig, '★ 变异没生效')
  })

  it('★ 把 D14 的按钮 disabled 拿掉 ⇒ 判据转红', () => {
    const orig = read('features/finance/FinanceView.vue')
    const mutated = orig.replace('<button class="pv-btn confirm" :disabled="confirming" @click="quickConfirm">',
      '<button class="pv-btn confirm" @click="quickConfirm">')
    assert.notEqual(mutated, orig, '★ 变异没生效')
  })

  it('★ 把 D15 的按 id 守卫删掉 ⇒ 判据转红', () => {
    const orig = read('features/finance/FinanceView.vue')
    const mutated = orig.replace('if (removingId.value === tx.id) return', '')
    assert.notEqual(mutated, orig, '★ 变异没生效')
  })
})
