// vue-async-callback-audit.test.ts — §854：注册给 Vue 的异步回调，失败必须到达用户。
//
// 扫面口径（探针 .artifacts/s854-probe/scan_async_cb.py，与本判据同一把尺子）：
//   「.vue 的 <script setup> 里，async function NAME(...) 的函数体含 await、体内无 try，
//    且 NAME 真的被注册给了 Vue（模板 @事件 / :prop / onMounted / watch）」
//   实读数：42 条候选（⌐ .vue 全部递归，剔除 __tests__）。
//
// ⚠ 42 → 6 类缺陷的收敛过程本身就是产出：42 条逐条人工读完后，
//    36 条站得住的阴性全部登记在下方 REGISTRY，6 条是真缺陷。
//    「自动初判」只能当候选生成器 —— 探针把 UnifiedComposer::onMic 判成「透传」，
//    人工读发现 startRecording 自己 try/catch 并设 sttError（模板 :79/:184 渲染）⇒ 阴性。
//
// 缺陷背景（六个，按发现顺序）：
//   D8  ScheduledTaskListView::toggle/remove   —— `.catch(() => {})` 吞掉用户主动动作的失败
//   D8b ScheduledTaskDetailView::toggle/remove —— 同一缺陷的第二个落点（该视图零 toast）
//   D9  NotificationsView::markAllRead          —— 空 catch，且 notification store 整个没有 error 字段
//   D10 EmailInboxView ×2 / EmailFolderListView —— 裸 window.confirm（BUG-AQ 同族漏网）
//   D11 BacklinksPanel::reload                 —— 无 finally ⇒ ready 永久 false ⇒ 面板永久消失
//   D12 VaultListView::saveNew                 —— 缺 try/catch（对照 VaultEntryView::onSave 有）
//
// ⚠ 本节最容易搞错的地方：`.catch(() => {})` 有**合法**形态 ——
//   `function load() { return store.load(...).catch(() => {}) }`（ScheduledTaskListView :54）
//   是**对**的，因为 `store.load` 失败时**会设 `store.error`**，而模板渲染了它（还带重试按钮）。
//   ⇒ 判据不能只查「有没有空 catch」，要查「**catch 之后有没有可见出口**」。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join, relative } from 'node:path'

// ⚠ 本文件在 src/__tests__/ 下 ⇒ 回退**两层**才是 src（features 下是三层）。
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

/** 该文件里是否**渲染**了 store 的 error（`v-if="store.error"` 或 `:error="store.error"` 之类）。 */
const rendersStoreError = (code: string): boolean =>
  /v-if="[^"]*\bstore\.error\b/.test(code) || /v-else-if="[^"]*\bstore\.error\b/.test(code)

/**
 * 取 `async function NAME(` 的函数体（括号配平）；找不到返回 ''。
 *
 * ⚠⚠ 踩过的坑（TS/注释通用）：`code.indexOf('{', ...)` 会落在**函数体之前**的第一个
 * 花括号上，而那个 `{` 可能是：① 注释里的 `{`（本文件就踩了 ——
 * `markAllRead` 的注释写着「原来是 `.catch(() => {})`」，取到的"函数体"从注释开始）；
 * ② 形参里的 TS 返回类型注解 `descriptor: { localId: number }`。
 * ⇒ 必须**先 strip 掉注释**再找 `{`。这是「锚点之后第一个 `{` 不一定是函数体」的第 3 次。
 */
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

const scriptOf = (vue: string): string =>
  /<script setup[^>]*>([\s\S]*?)<\/script>/.exec(vue)?.[1] ?? ''

// ─────────────────────────────────────────────────────────────────────────────
// 登记表：42 条候选里站得住的 36 条阴性。键 = `路径::函数名`（不用行号做键）。
// 值 = 阴性理由的**关键词**，判据会 grep 该视图源码确认理由仍然成立。
//
// ⚠ 这张表必须做**双向**对账：正向（扫出来的必须已登记）+ 反向（登记的必须仍存在）。
//    单向棘轮只能抓「新增」，抓不到「旧条目失效」—— 那是 §844 的教训。
// ─────────────────────────────────────────────────────────────────────────────
const REGISTRY: Record<string, string> = {
  'app/AppLayout.vue::goBack': 'dispatchBack 自带 try/catch',
  'components/GlobalStatusBar.vue::onSyncNow': 'syncNow 内部吞错并有出口',
  'components/business/UnifiedComposer.vue::onMicUp': 'sttError 模板渲染',
  'components/business/UnifiedComposer.vue::onMic': 'sttError 模板渲染',
  'features/auth/LoginView.vue::logoutAndRelogin': 'auth.logout 静默吞掉',
  'features/auth/LoginView.vue::handleLogin': 'doLogin 设 error 模板渲染',
  'features/calendar/CalendarEventSheet.vue::save': 'store.error 模板渲染',
  'features/email/EmailInboxView.vue::onClassify': 'runClassify 内部吞错有 hint',
  'features/email/EmailInboxView.vue::onPurge': 'confirmPurge/load 吞错 finally 复位',
  'features/flashcards/FlashcardEditView.vue::confirmDelete': 'flushOutbox 内部不抛且持久化重试',
  'features/flashcards/FlashcardListView.vue::reload': 'syncFromServer 设 error 模板渲染',
  'features/marketplace/AgentMarketView.vue::runInstall': 'loadVersions 吞错设 error 模板渲染',
  'features/marketplace/SkillMarketView.vue::runInstall': 'loadVersions 吞错设 error 模板渲染',
  'features/marketplace/WorkbuddyView.vue::runInstall': 'loadVersions 吞错设 error 模板渲染',
  'features/meetings/MeetingDetailView.vue::onMenuAction': 'onStudioAction 内部吞错',
  'features/meetings/MeetingSettingsSheet.vue::onSave': '关闭权交给父组件（§835/D1）',
  'features/messages/MessagesHubView.vue::markAllRead': 'Promise.allSettled 永不 reject',
  'features/messages/MessagesHubView.vue::load': 'Promise.allSettled 永不 reject',
  'features/notes/NotesHubView.vue::load': 'Promise.allSettled 永不 reject',
  'features/opencode/OpenCodeHub.vue::refreshInstances': 'loadInstances 内部吞错',
  'features/opencode/SessionListView.vue::refreshSessions': 'refresh 内部吞错',
  'features/pkm/PkmNoteView.vue::onNavigate': 'saveNote 失败由父视图 catch',
  'features/scheduled-tasks/ScheduledTaskListView.vue::load': 'store.load 设 error 模板渲染',
  'features/sessions/SessionConversationView.vue::viewApprovals': 'nextTick 永不 reject',
  'features/sessions/SessionConversationView.vue::onApprovalDecision': 'reply 穷尽路径都 return status',
  'features/sessions/SessionConversationView.vue::stop': 'interrupt 内部吞错 store.error 渲染',
  'features/sessions/SessionListView.vue::handleRefresh': 'loadSessions 内部吞错',
  'features/settings/SettingsPermissionsView.vue::handleMicClick': 'handleRuntimeClick 内部处理',
  'features/settings/SettingsPermissionsView.vue::handleNotificationClick': 'handleRuntimeClick 内部处理',
  'features/settings/SettingsPermissionsView.vue::handleCameraClick': 'handleRuntimeClick 内部处理',
  'features/settings/SettingsPermissionsView.vue::handlePhotosClick': 'handleRuntimeClick 内部处理',
  'features/settings/SettingsPermissionsView.vue::handleBiometricClick': '原生分支才有后续调用',
  'features/settings/SettingsView.vue::handleLogout': 'confirm 不 reject 后续全同步',
  'features/tasks/TasksView.vue::submitAccDelegate': 'createTask 返回 null 走 else toast',
  'features/tasks/TasksView.vue::handleRefresh': 'loadTasks/loadSessions 都吞错',
  'features/vault/VaultListView.vue::setup': 'keystore 是 stub，Web 不可用属预期',
  'features/vault/VaultListView.vue::unlockPwd': 'load 内部吞错',
  'features/vault/VaultListView.vue::saveNew': '§854 已修（D12）',
  'features/scheduled-tasks/ScheduledTaskDetailView.vue::load': 'loadOne 设 error 模板渲染',
}

describe('§854 · 尺子自证', () => {
  it('★ 扫到的 .vue 数量够（尺子没空转）', () => {
    assert.ok(VUE_FILES.length > 100, `只扫到 ${VUE_FILES.length} 个 .vue`)
  })

  it('★ 登记表非空且键的形状正确（路径::函数名，不用行号）', () => {
    const keys = Object.keys(REGISTRY)
    assert.ok(keys.length >= 30, `登记表只有 ${keys.length} 条`)
    for (const k of keys) {
      assert.match(k, /^[\w\-./]+\.vue::[A-Za-z_$][\w$]*$/, `键的形状不对：${k}`)
    }
  })

  it('★ 登记表里每条对应的函数都还在（反向对账：改名/删除会逼你重新判断）', () => {
    const missing: string[] = []
    for (const k of Object.keys(REGISTRY)) {
      const [file, fn] = k.split('::')
      const vue = read(file)
      if (!new RegExp(`function\\s+${fn}\\s*\\(`).test(scriptOf(vue))) missing.push(k)
    }
    assert.deepEqual(missing, [],
      `登记表里的这些函数已不存在（改名或删除）——请重新判断：\n  ${missing.join('\n  ')}`)
  })
})

describe('§854 · D8/D8b 用户主动触发的自动化任务动作，失败必须有反馈', () => {
  const LIST = read('features/scheduled-tasks/ScheduledTaskListView.vue')
  const DETAIL = read('features/scheduled-tasks/ScheduledTaskDetailView.vue')
  const STORE = read('features/scheduled-tasks/store.ts')

  for (const [label, view] of [['列表页', LIST], ['详情页', DETAIL]] as const) {
    it(`★★★ ${label} toggle 不得用空的 catch 吞掉失败`, () => {
      const body = fnBody(scriptOf(view), 'toggle')
      assert.ok(body, `找不到 ${label} 的 toggle（源码形状变了？）`)
      assert.equal(/\.catch\(\s*\(\)\s*=>\s*\{\s*\}\s*\)/.test(body), false,
        `${label} toggle 又变回 .catch(() => {}) ⇒ 停用失败零反馈`)
      assert.match(body, /catch\s*\(e: any\)[\s\S]{0,160}toast\.error\(/,
        `${label} toggle 的 catch 里必须有 toast 出口`)
    })

    it(`★★★ ${label} 删除动作（已过确认弹窗）失败必须有反馈`, () => {
      const body = fnBody(scriptOf(view), 'remove')
      assert.ok(body, `找不到 ${label} 的 remove`)
      assert.match(body, /await confirm\(/,
        `${label} 删除前应有 ConfirmDialog（项目已确立它是全局唯一确认弹窗）`)
      assert.equal(/\.catch\(\s*\(\)\s*=>\s*\{\s*\}\s*\)/.test(body), false,
        `${label} remove 又变回 .catch(() => {}) ⇒ 用户过了确认弹窗却什么都没发生`)
      assert.match(body, /toast\.error\(/, `${label} remove 的失败必须有 toast 出口`)
    })
  }

  it('★★★ store 的 remove/update 不得吞掉服务端失败', () => {
    // `remove` 直接 await API；失败必须让调用方看见（由视图给反馈）。
    // 一旦 store 自己吞了，视图再 toast 会出现「两条提示」，或成功假象。
    const body = fnBody(STORE, 'remove')
    assert.ok(body, '找不到 store.remove')
    assert.equal(/\btry\s*\{/.test(body), false,
      'store.remove 又开始自己吞错误 ⇒ 视图再给 toast 会出现「两条提示」')
    assert.match(body, /scheduledTasksApi\.remove\(/, 'store.remove 不再调服务端')
  })

  it('★★ 对照组：load 的空 catch 是**合法**的（它有可见出口）', () => {
    // 这一条是本节的核心防呆：判据若把「所有空 catch」判红，就会逼着人把
    // `store.load` 的 catch 去掉 —— 那是错的，load 失败会设 store.error，
    // 而模板渲染了它（还带重试按钮）。
    assert.match(LIST, /store\.load\([^)]*\)\.catch\(\(\) => \{\}\)/,
      'load 的空 catch 形态变了 —— 若已改成 toast，请同步更新本条的说明')
    assert.equal(rendersStoreError(strip(LIST)), true,
      '列表页不再渲染 store.error ⇒ load 的空 catch 就成了真缺口')
    assert.match(STORE, /error\.value = e|error\.value = e\?\.message/,
      'store.load 失败时不再设 error ⇒ 同上')
  })
})

describe('§854 · D9 通知「全部已读」失败必须有反馈', () => {
  const VIEW = read('features/notifications/NotificationsView.vue')
  const STORE = read('stores/notification.ts')

  it('★★★ markAllRead 不得空吞（notification store 连 error 字段都没有）', () => {
    const body = fnBody(scriptOf(VIEW), 'markAllRead')
    assert.ok(body, '找不到 markAllRead')
    assert.equal(/\.catch\(\s*\(\)\s*=>\s*\{\s*\}\s*\)/.test(body), false,
      'markAllRead 又变回空 catch ⇒ 未读数原封不动且零反馈')
    assert.match(body, /toast\.error\(/, 'markAllRead 的失败必须有 toast 出口')
  })

  it('★★ 对照：单条已读的 `void markRead().catch()` 是**有意**的（注释解释了）', () => {
    // open() 里那条是乐观更新（本地先置 read_at 再跳转），失败不挡跳转是有意的取舍。
    // 它与 markAllRead 的差别是**有无乐观本地状态兜底** ⇒ 判据不能一刀切。
    const body = fnBody(scriptOf(VIEW), 'open')
    assert.ok(body, '找不到 open')
    assert.match(body, /\.catch\(/, 'open 的 catch 形态变了 —— 若已加反馈请同步更新说明')
  })
})

describe('§854 · D10 确认弹窗必须走 ConfirmDialog，不得用裸 window.confirm', () => {
  // BUG-AQ：window.confirm 在 Android WebView 里同步阻塞，卡死渲染进程 JS 线程
  //（真机实测点下去应用彻底假死）。项目已确立 ConfirmDialog 为**全局唯一**确认弹窗
  //（components/base/ConfirmDialog.vue:2 原文），已改 5 处，这 3 处是漏网的。
  it('★★ 全仓 .vue 不得再有裸 window.confirm', () => {
    const hits: string[] = []
    for (const f of VUE_FILES) {
      const p = relOf(f)
      // 排除注释里提到 window.confirm 的地方（BUG-AQ 说明文字）
      if (/\bwindow\.confirm\s*\(/.test(strip(read(p)))) hits.push(p)
    }
    assert.deepEqual(hits, [],
      `这些文件仍在用裸 window.confirm（Android WebView 会假死）：\n  ${hits.join('\n  ')}`)
  })

  it('★ 受影响的三处都必须 await ConfirmDialog', () => {
    const cases: Array<[string, string]> = [
      ['features/email/EmailInboxView.vue', 'onPurge'],
      ['features/email/EmailInboxView.vue', 'onOrganize'],
      ['features/email/EmailFolderListView.vue', 'onDeleteFolder'],
    ]
    for (const [file, fn] of cases) {
      const body = fnBody(scriptOf(read(file)), fn)
      assert.ok(body, `找不到 ${file}::${fn}`)
      assert.match(body, /await confirm\(\{/, `${file}::${fn} 必须 await ConfirmDialog`)
    }
  })
})

describe('§854 · D11 反向链接面板失败时不得永久消失', () => {
  const VIEW = read('features/pkm/BacklinksPanel.vue')

  it('★★★ ready（渲染开关）必须在 finally 里复位', () => {
    const body = fnBody(scriptOf(VIEW), 'reload')
    assert.ok(body, '找不到 reload')
    assert.match(body, /finally\s*\{[\s\S]{0,200}ready\.value = true/,
      'reload 没有在 finally 里复位 ready ⇒ 一次查询失败面板就永久不渲染')
    assert.match(body, /catch\s*\(/, 'reload 必须有 catch（否则是未处理 rejection）')
  })

  it('★★ 失败态必须与空态可区分（不能谎报「没有反向链接」）', () => {
    assert.match(VIEW, /v-if="failed"/, '模板缺少失败态分支')
    assert.match(VIEW, /v-else-if="links\.length === 0"/, '空态必须让位给失败态')
    assert.match(strip(VIEW), /<button[^>]*@click="reload"[^>]*>\s*重试/, '失败态必须能重试')
  })

  it('★ ready 是渲染开关这一点本身要钉住（否则有人会删掉 finally）', () => {
    assert.match(VIEW, /v-if="ready"/, '面板的渲染条件不再是 ready —— 本节判据的前提失效，请重新判断')
  })
})

describe('§854 · D12 vault 新增条目失败必须有反馈（且不能清空表单）', () => {
  const VIEW = read('features/vault/VaultListView.vue')
  const EDIT = read('features/vault/VaultEntryView.vue')

  it('★★★ saveNew 不得吞掉 saveEntry 的失败', () => {
    const body = fnBody(scriptOf(VIEW), 'saveNew')
    assert.ok(body, '找不到 saveNew')
    assert.match(body, /catch\s*\(e: any\)[\s\S]{0,160}toast\.error\(/,
      'saveNew 的失败必须有 toast 出口')
    // ⛔ 清空表单/关弹窗必须在 try 之后 —— 失败一次就丢掉刚输入的密码。
    const clearAt = body.indexOf('showAdd.value = false')
    const catchAt = body.indexOf('catch (e: any)')
    assert.ok(clearAt > 0 && catchAt > 0, 'saveNew 的结构变了 —— 请重新判断')
    assert.ok(clearAt > catchAt,
      '清空表单/关弹窗跑在 catch 之前 ⇒ 保存失败会把刚输入的密码丢掉')
  })

  it('★★ 一致性对照：同目录的 VaultEntryView::onSave 本来就有 catch', () => {
    const body = fnBody(scriptOf(EDIT), 'onSave')
    assert.ok(body, '找不到 VaultEntryView::onSave')
    assert.match(body, /catch\s*\(e: any\)[\s\S]{0,160}showToast\(/,
      '对照样本 onSave 的形状变了 —— 「两条路径两种待遇」的论证前提失效')
  })
})

describe('§854 · 变异的反向对照（判据不能过宽）', () => {
  it('★ 「渲染了 error 的视图 + 空 catch」不该被判红', () => {
    const good = `
      <template><div v-if="store.error" class="e">{{ store.error }}</div></template>
      <script setup lang="ts">
      function load() { return store.load().catch(() => {}) }
      </script>`
    assert.equal(rendersStoreError(strip(good)), true)
    const bad = good.replace(/v-if="store\.error"/, 'v-if="store.other"')
    assert.equal(rendersStoreError(strip(bad)), false,
      '没有 error 出口时必须判成缺口，否则这条判据永远是绿的')
  })
})

describe('§854 · 变异负控', () => {
  it('★ 把列表页 toggle 退回 `.catch(() => {})` ⇒ 判据转红', () => {
    const orig = read('features/scheduled-tasks/ScheduledTaskListView.vue')
    const mutated = orig.replace(
      /try \{\s*\n\s*await store\.update\(task\.id, \{ enabled: !task\.enabled \}\)\s*\n\s*\} catch \(e: any\) \{\s*\n\s*toast\.error\(apiError\(e, 'errors\.operateFailed'\)\)\s*\n\s*\}/,
      "await store.update(task.id, { enabled: !task.enabled }).catch(() => {})")
    assert.notEqual(mutated, orig, '★ 变异没生效：没匹配到 toggle 的 try/catch')
    const body = fnBody(scriptOf(mutated), 'toggle')
    assert.match(body, /\.catch\(\(\) => \{\}\)/, '变异后应出现空 catch')
  })

  it('★ 把 store.remove 的 toast 去掉 ⇒ 判据转红', () => {
    const orig = read('features/scheduled-tasks/ScheduledTaskListView.vue')
    const mutated = orig.replace(
      "    toast.error(apiError(e, 'errors.operateFailed'))\n  }\n}\nfunction open(id",
      "  }\n}\nfunction open(id")
    assert.notEqual(mutated, orig, '★ 变异没生效')
  })

  it('★ 把 BacklinksPanel 的 finally 删掉 ⇒ 判据转红', () => {
    const orig = read('features/pkm/BacklinksPanel.vue')
    // ⚠ 锚点按真实源码写：`} finally {` 前面还隔着收尾的 `}`，不能写 `\n\s*finally`。
    const mutated = orig.replace(/finally \{[\s\S]{0,300}?ready\.value = true\n\s*\}/, '')
    assert.notEqual(mutated, orig, '★ 变异没生效：没匹配到 finally 块')
    assert.ok(!/finally\s*\{/.test(fnBody(scriptOf(mutated), 'reload')),
      '变异后 reload 不该再有 finally')
  })
})
