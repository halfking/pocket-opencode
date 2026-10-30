/**
 * refine-prompt-parity.test.mjs — §54 发现 C 的门：**两份精校提示词不许再漂。**
 *
 * ── 它守的是什么 ──
 *
 * 精校有两条实现：
 *   ① 服务端 `POST /api/meetings/{id}/refine` → Go `buildRefinePrompt`
 *   ② 前端 `fallbackRefine()` → `/api/llm/chat` → TS `buildFallbackRefinePrompt`
 *
 * ② 是 ① 失败时的兜底。① 在 2026-10-07 被真网关实测重写过（§35：
 * 「润色」是根因词、四条禁令、不得改动数字），**② 一字未动**。
 *
 * ⇒ 实测出来的三条后果（全部真网关，见 docs/design/2026-10-06-recording-quality-fixes.md §54）：
 *   - 「请润色」⇒ 把**完全正确**的句子改写掉、把 CI 改成 101、加原文没有的括注
 *   - `\"action_items\":[]` / `\"todos\":[]` **空数组范例** ⇒ 模型照抄返回空
 *     ⇒「时间点自动加入计划日程」在回落路径上没有输入
 *     （① 上实测：改成非空范例后 action_items 从 `[]` 变 3 条，含 due「十一月底之前」）
 *   - 没有术语表 ⇒「林兰→林岚」「页百零一→CI」在这条路径上修不了
 *
 * ⚠ ② 不是边角情况：llmgateway 的 `ResponseHeaderTimeout` 实测 60s，
 *   而网关 `auto` 路由到 deepseek-v4-flash 时首字节 24~142s（§44）
 *   ⇒ 后端先超时 502 ⇒ 前端必走 ②。
 *
 * 与 §53 修掉的 `filenameForMimeType`（两处各写一份、已经漂）同族：
 * **同一份契约写两遍，漂移只是时间问题。**
 *
 * ── 为什么是行为门而不是源码 grep ──
 *
 * 源码 grep 只能证明「提示词写在那」，证不了「**发出去的是那一份**」
 * （§51「实现对 ≠ 被用上」的教训：写对了但调用点没用，一样是坏的）。
 * 所以下面 A/B 两组都**真的跑一遍**，断言网关**实际收到的请求体**。
 *
 * 跨语言没法共享同一份常量（Go 与 TS），所以契约清单放在**本文件**，
 * 由 A 组（TS 行为）与 C 组（Go 源码）**共用同一份**：
 * 加契约项只改这一处，两份提示词一起被点名。
 *
 * ⚠ 清单曾一度导出在 `refine-prompt.ts` 里，被 `check:dead-features`
 *   判成「未被 App 接线的死能力」——那道门是对的，一个只有测试引用的
 *   导出就是没人用的导出。放在测试里不影响防漂（清单与判据仍同源），
 *   且测试文件本身就是规格。
 */

/**
 * 精校提示词的契约清单：两份实现（Go 的 `buildRefinePrompt`、
 * TS 的 `buildFallbackRefinePrompt`）都必须字面包含这些内容。
 *
 * ⚠ 全部来自**真网关实测**，不是预防性设计（§35 / §54）：
 *  - 「润色」是 §35 实测出的根因词（反向断言：不得出现）
 *  - 「不得改动数字」是第一版重写时漏掉、复测发现模型在猜数字后补上的
 *  - `action_items`/`todos` 的**非空**范例是 §54 实测出的（空数组 ⇒ 返回空）
 *  - 「名单里的人名多半正确」堵的是 §35 实测出的**具体误解**：
 *    模型会把转写里的人名「对齐」进名单（张伟/李娜 → 赵敏/孙磊），
 *    只写「不要替换成不对应的词」它会理解成「赵敏在名单里所以是对应的」
 */
// §143：主题里的专有名词。CONTRACT 钉标签，这条钉**内容**。
const TOPIC_TERM = '悬界芯片'

const CONTRACT = [
  { what: '只纠错不改写', literal: '只改错，不改写' },
  { what: '不得增加内容', literal: '不得增加原文没有的内容' },
  { what: '不得删除内容', literal: '不得删除原文有的内容' },
  { what: '不得改动数字', literal: '不得改动数字' },
  { what: '保留说话人标记', literal: '[说话人]' },
  { what: 'action_items 非空范例', literal: '"action_items":[{"text":"","assignee":"","due":""}]' },
  { what: 'todos 非空范例', literal: '"todos":[{"text":"","assignee":"","due":""}]' },
  { what: 'due 保留原话', literal: 'due 保留用户原话' },
  // §65：glm-5.2 实测把 due 填成指代词「那之前」，产品侧解析失败 ⇒ 永远进不了日程。
  { what: 'due 必须能独立成立（禁指代）', literal: 'due 必须能脱离上下文独立成立' },
  { what: '指代要换成具体时间', literal: '「那之前」「到时候」「之后」' },
  { what: '只放真需要人做的事', literal: '需要有人去做' },
  { what: '名单里的人名多半正确', literal: '名单上没有的人名' },
  // ★ §143（主题接入）：「主题」此前是条死路 —— UI 有输入框、store 有字段、
  //   却在**两侧**都没进请求体/提示词。§138.3 实测：ASR 把主题里的产品名
  //   「悬界芯片」转成了「玄介芯片」，而精校拿不到依据就修不了。
  //   加进共用清单 ⇒ TS 回落提示词与 Go 提示词**同时**被点名。
  //
  // ⚠⚠ 这一项**只能钉标签，钉不住内容** —— 主题内容是每次调用传进来的，
  //   静态字面量写不出来。而 D3 实测证明：只钉标签时，
  //   **把内容整段删掉而只留标签，判据照样全绿** ——
  //   那就是「只声明不读」的同款形状（「拼了标签没拼内容」）。
  //   ⇒ 内容由 assertPromptContract 里那条独立断言钉（见 TOPIC_TERM）。
  { what: '会议议题（专有名词来源）', literal: '会议议题' },
  { what: 'next_meeting 的要求', literal: '★ next_meeting 的要求' },
  { what: 'next_meeting 保留原话', literal: '保留用户原话' },
  { what: 'next_meeting 非 null 范例', literal: '"next_meeting":""' },
  // §64.6：location 一直在请求体里却被 buildRefinePrompt 丢掉。
  { what: '会议地点进术语表', literal: '会议地点：' },
]

import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { afterEach, describe, it } from 'node:test'
import { createRequire } from 'node:module'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const API_DIR = path.resolve(HERE, '..')
const ROOT = path.resolve(API_DIR, '..', '..')
const REPO = path.resolve(ROOT, '..')
const PROMPT_SRC = path.join(API_DIR, '..', 'features', 'meetings', 'refine-prompt.ts')
const MEETINGS_API = path.join(API_DIR, 'meetings.ts')
const GO_FILE = path.join(REPO, 'backend', 'internal', 'server', 'server_meeting.go')
const CACHE = path.join(ROOT, 'node_modules', '.cache')

const requireFrom = createRequire(path.join(ROOT, 'package.json'))
const ESBUILD = requireFrom.resolve('esbuild')


/** 恒 cloud 的 syncPolicyStore 桩（与 _wire-helpers.ALWAYS_CLOUD_SYNC_POLICY_STUB 同源语义）。 */
const ALWAYS_CLOUD_SYNC_POLICY_STUB = `
export const SYNC_POLICY_CACHE_KEY = 'op_sync_policy_v1'
export const SYNC_POLICY_LOCAL_INTENTS_KEY = 'op_sync_policy_local_intents_v1'
export const SYNC_REPORT_CACHE_KEY = 'op_sync_report_v1'
export function readCachedModuleMode() { return 'cloud' }
export function recordSyncReportEntry() {}
export function readCachedSyncReport() { return [] }
export function shouldSync() { return true }
export function moduleMode() { return 'cloud' }
export function readCachedSyncPolicy() { return null }
export function installSyncPolicyActivationHost() {}
export function installSyncPolicyStorage() {}
export function installSyncPolicyScopeResolver() {}
export function useSyncPolicyStore() { return {} }
export const SYNC_MODULES = []
export function isSyncModule() { return false }
export function normalizeSyncPolicy(p) { return p }
export function isPortalSyncModule() { return false }
`

const ALWAYS_DIRECT_AUTH_STUB = `
import { defineStore } from 'pinia'
const sessions = new WeakMap()
export function sessionState(auth) {
  let state = sessions.get(auth)
  if (!state) { state = { generation: {}, applyingRefresh: false, disposed: false }; sessions.set(auth, state) }
  return state
}
export function captureAuthAttempt(auth) { return { owner: auth, generation: sessionState(auth).generation } }
export function assertAuthAttempt(auth, session) {
  const state = sessionState(auth)
  if (session.owner === auth && !state.disposed && state.generation === session.generation) return
  throw Object.assign(new Error('Sign-in attempt was superseded.'), { name: 'AbortError' })
}
export function captureAuthSession(auth) {
  if (auth.activationStatus !== 'ready') throw Object.assign(new Error('Session activation is not ready.'), { name: 'AbortError' })
  return captureAuthAttempt(auth)
}
export function isCurrentAuthSession(auth, session) {
  const state = sessionState(auth)
  return auth.activationStatus === 'ready' && session.owner === auth && !state.disposed && state.generation === session.generation
}
export function assertAuthSession(auth, session) {
  if (isCurrentAuthSession(auth, session)) return
  const error = new Error('The session changed before the request completed.')
  error.name = 'AbortError'
  throw error
}
export const useAuthStore = defineStore('auth', {
  state: () => ({ token: '[REDACTED:secret]', user: '[REDACTED:account]', userId: 'stub-user', workspaceId: 'stub-workspace', authMethod: 'password', activationStatus: 'ready' }),
  actions: {
    maybeRefresh() { return Promise.resolve() },
    refreshSession() { return Promise.resolve() },
  },
})
`

/** 预打包再 import：src/api/* 内部用无扩展名 import，Node 的 ESM 解析器要显式扩展名。 */
async function loadBundle(entry, name, stubs = {}) {
  const out = path.join(CACHE, `opk-${name}-${process.pid}.mjs`)
  fs.mkdirSync(CACHE, { recursive: true })
  // ⚠ 2026-10-29：与 _wire-helpers.loadBundle 同款 stubs 参数 ——
  //   8b34e1c3 的 blockedModuleWrite 会在无激活策略的环境里把 meetings POST
  //   判成 module_policy_local，B/D 组判据需要把策略层替成恒 cloud。
  const stubDir = path.join(CACHE, 'stubs')
  const stubManifest = []
  for (const [spec, code] of Object.entries(stubs)) {
    fs.mkdirSync(stubDir, { recursive: true })
    const f = path.join(stubDir, `${name}-${spec.replace(/[^a-zA-Z0-9]/g, '_')}.mjs`)
    fs.writeFileSync(f, code, 'utf8')
    stubManifest.push([spec, f])
  }
  const pluginSrc = stubManifest.length
    ? `
  const stubManifest = ${JSON.stringify(stubManifest)};
  const escapeRe = (s) => s.replace(/[.*+?^'"'"'{}()|[\\]\\\\]/g, String.fromCharCode(92) + '$&');
  const stubPlugin = {
    name: 'refine-parity-stubs',
    setup(b) {
      for (const [spec, file] of stubManifest) {
        b.onResolve({ filter: new RegExp(escapeRe(spec) + '$') }, () => ({ path: file }));
      }
    },
  };`
    : ''
  const pluginArg = stubManifest.length ? ',\n      plugins: [stubPlugin]' : ''
  execFileSync(process.execPath, ['-e', `
    const esbuild = require(${JSON.stringify(ESBUILD)});
    ${pluginSrc}
    esbuild.build({
      entryPoints: [${JSON.stringify(entry)}],
      bundle: true, format: 'esm', platform: 'browser', write: false,
      external: ['pinia', 'vue']${pluginArg},
      outfile: ${JSON.stringify(out)}, logLevel: 'silent',
    }).then(r => require('fs').writeFileSync(${JSON.stringify(out)}, r.outputFiles[0].contents))
      .catch(e => { console.error(e.message); process.exit(1) })
  `], { cwd: ROOT, stdio: ['ignore', 'inherit', 'inherit'] })
  return import('file://' + out)
}

/** ★ 剥注释用**等长空白替换**，不是删除。 */
const blank = (m) => m.replace(/[^\n]/g, ' ')
const stripJsComments = (t) =>
  t.replace(/\/\*[\s\S]*?\*\//g, blank).split('\n').map((l) => l.replace(/\/\/.*$/, '')).join('\n')
const stripGoComments = (t) =>
  t.replace(/\/\*[\s\S]*?\*\//g, blank).split('\n').map((l) => {
    const i = l.indexOf('//')
    return i < 0 ? l : l.slice(0, i)
  }).join('\n')

/** 取 Go 的 `func buildRefinePrompt(` 函数体，并把源码层的 `\"` 还原成 `"`。 */
// ⚠⚠ 2026-10-07（§107.1）：这条抽取器的**作用域**必须覆盖整条提示词产出路径。
//
//   §107 把 `buildRefinePrompt` 体内的术语表那段**逐字提取**成具名函数
//   `metaTermHint`（Go 侧 `server_meeting.go`），理由是要拿**同一份文本**
//   去做摘要链的配对实验。
//   而本抽取器只切 `buildRefinePrompt` 的**大括号范围** ⇒ 契约里的字面量
//   全都搬到了另一个函数体里 ⇒ C 组立刻报「两份实现已经漂了」。
//
//   ⇒ **报红的是门，不是代码**：提示词输出逐字未变
//   （Go 侧改前改后 md5 相同，见 docs §106.1）。
//
//   修法不是把断言放宽，而是**把作用域补到真正的产出路径**上：
//   `buildRefinePrompt` **调用** `metaTermHint`，两者合起来才是那条提示词。
function goFuncBody(code, name) {
  const start = code.indexOf(`func ${name}(`)
  assert.ok(start > 0, `找不到 Go 的 ${name} —— 判据的前提失效`)
  const open = code.indexOf('{', start)
  let depth = 0
  for (let i = open; i < code.length; i++) {
    if (code[i] === '{') depth++
    else if (code[i] === '}') {
      depth--
      if (depth === 0) return code.slice(open, i)
    }
  }
  throw new Error(`${name} 函数体括号不配平`)
}

/** 整条提示词产出路径（buildRefinePrompt + 它调用的 metaTermHint）。 */
const GO_PROMPT_PATH = ['buildRefinePrompt', 'metaTermHint']

function goRefinePrompt() {
  const code = stripGoComments(fs.readFileSync(GO_FILE, 'utf8'))
  return GO_PROMPT_PATH
    .map((n) => goFuncBody(code, n))
    .join('\n')
    .replace(/\\"/g, '"')
}

/**
 * 只取 `buildRefinePrompt` 本体（不含 metaTermHint）。
 * ⚠ 用途只有一个：证明上面那条的作用域**确实比它大** ——
 *   否则「加进 metaTermHint」这件事在门里是无感的，抽取器等于没修。
 */
function goRefinePromptBodyOnly() {
  const code = stripGoComments(fs.readFileSync(GO_FILE, 'utf8'))
  return goFuncBody(code, 'buildRefinePrompt').replace(/\\"/g, '"')
}

const TRANSCRIPT = '[张伟] 下周三下午三点，跟王总还有林兰开个客户评审会。'
const SEGMENTS = [{
  id: 's1', meetingId: 'm1', speakerLabel: '张伟', lang: 'zh',
  text: '下周三下午三点，跟王总还有林兰开个客户评审会。',
  startMs: 0, endMs: 18000,
}]

const realFetch = globalThis.fetch
const realWindow = globalThis.window
const realLocalStorage = globalThis.localStorage
afterEach(() => {
  globalThis.fetch = realFetch
  globalThis.window = realWindow
  globalThis.localStorage = realLocalStorage
})

function stubBrowser() {
  globalThis.localStorage = {
    getItem: () => null,
    setItem: () => {},
    removeItem: () => {},
    key: () => null,
    get length() { return 0 },
  }
  globalThis.window = {
    addEventListener() {}, removeEventListener() {}, removeAllListeners() {},
    location: { href: 'https://localhost/', origin: 'https://localhost' },
    navigator: { userAgent: 'node-test' },
  }
}

/**
 * 从 URL 里取 pathname 做**精确**比较。
 *
 * ⚠ 这里原本写的是 `url.includes('/api/llm/chat')`，被变异 M6 打出来了：
 *   把 URL 改成 `/api/llm/chat-DISABLED` 时 `includes` **照样成立**
 *   ⇒ 请求已经打到别处去了，判据却放行。
 * 这与本仓记过的「子串断言量的是「出现过这几个字」」同族：
 * 子串匹配量的是**形状**，不是**同一个端点**。
 */
function pathnameOf(u) {
  try { return new URL(String(u)).pathname } catch { return String(u) }
}

/** 打桩 fetch：后端精校一律 500（触发回落），抓下 /api/llm/chat 的真实请求体。 */
function captureFallbackPrompt() {
  const seen = []
  globalThis.fetch = async (url, init) => {
    seen.push({ url: String(url), path: pathnameOf(url), body: init?.body ? JSON.parse(String(init.body)) : null })
    return {
      ok: false,
      status: 500,
      headers: { get: (k) => (k.toLowerCase() === 'content-type' ? 'application/json' : null) },
      async json() { return { error: 'boom' } },
    }
  }
  return seen
}

/**
 * A 组的判据提成独立函数，**A 与负控 D1 共用**。
 *
 * 为什么提出来：负控要证明的是「这些判据真的有牙」，办法只有一个 ——
 * **把同一个判据函数喂一个违规输入，看它是否真的抛**。
 * 若 D1 自己另写一遍断言逻辑（这正是本文件第一版的写法），
 * 那它验证的是「D1 自己的判断」，与 A 实际用的判据无关 ——
 * 形态上像变异验证，实质什么都没验。
 */
function assertPromptContract(prompt, contract) {
  assert.ok(!prompt.includes('润色'),
    '提示词里又出现「润色」——它要求模型改写文本，而这条链路职责是纠错（§35 实测会把正确的句子改掉）')

  for (const { what, literal } of contract) {
    assert.ok(prompt.includes(literal), `回落提示词缺「${what}」（字面量 ${literal}）`)
  }
  // ★ 单独钉住**非空**范例：空数组 ⇒ 模型照抄返回空 ⇒ 下游没有输入可消费。
  assert.ok(!/"action_items":\[\]/.test(prompt),
    'action_items 范例又变回空数组了 —— 真网关实测模型会照抄返回空')
  assert.ok(!/"todos":\[\]/.test(prompt), 'todos 范例又变回空数组了')
  // §56：next_meeting 范例曾经是 null，而**没有任何要求** ⇒ 真网关多数轮次返回 null。
  // 「下周三下午三点开会」这个最直观的日程候选因此拿不到。
  assert.ok(!/"next_meeting":null/.test(prompt),
    'next_meeting 范例又是 null 了 —— 实测多数轮次会照抄返回 null，日程拿不到时间点')

  assert.ok(prompt.includes('客户评审会筹备') && prompt.includes('林岚'), '会议元数据没进提示词')
  // ★ §143：主题的**内容**必须逐字在场（标签在不算数，见 CONTRACT 那条注释）。
  assert.ok(prompt.includes(TOPIC_TERM), '会议议题里的专有名词没进提示词（只剩标签没有内容）')
  // §64.6：location 一直在请求体里却曾被 buildRefinePrompt 丢掉。
  assert.ok(prompt.includes('星海科技总部三楼'), '会议地点没进提示词 —— 它在请求体里却没被当成术语')
  assert.ok(prompt.includes('名单上没有的人名'), '缺「名单里的人名多半正确」那条禁令')

  // 没有 meta 时不能留下半截提示词（拼出「以下信息是可信的上下文」却后面没有内容）。
  assert.ok(prompt.includes('undefined') === false, '提示词里出现了 undefined')
}

describe('§54 发现 C：前端回落精校提示词', () => {
  it('A. buildFallbackRefinePrompt 满足契约，且不再出现「润色」', async () => {
    const mod = await loadBundle(PROMPT_SRC, 'refine-prompt')
    // ⚠ 必须三样都给齐：契约清单里的「会议地点：」**只有传了 location 才出现**
    //   （和「会议主题」「参会人」一样是条件性的）。第一版只传了 title/participants，
    //   门立刻报「缺会议地点进术语表」——门是对的，是测试数据不全。
    const prompt = mod.buildFallbackRefinePrompt(TRANSCRIPT, {
      title: '客户评审会筹备', participants: ['张伟', '林岚'], location: '星海科技总部三楼',
      // ★ §143：不给 topic 的话新增的契约项会立刻报红 —— 门是对的，
      //   是测试数据不全（与 §64.6 加 location 时同一形状）。
      topic: '悬界芯片客户评审会',
    })
    assert.ok(prompt.includes('悬界芯片'),
      '主题里的专有名词没逐字进提示词 ⇒ 只拼了标签没拼内容')
    assertPromptContract(prompt, CONTRACT)

    // 没有 meta 时不能留下半截提示词（拼出「以下信息是可信的上下文」却后面没有内容）。
    const bare = mod.buildFallbackRefinePrompt(TRANSCRIPT)
    assert.ok(!bare.includes('可信的上下文'), '没有 meta 时不该拼出术语表')
    assert.ok(!bare.includes('undefined'), '没有 meta 时提示词里出现了 undefined')
    // 空串/纯空格的参会人必须过滤掉（用户手填列表的常态）。
    const dirty = mod.buildFallbackRefinePrompt(TRANSCRIPT, { participants: ['  ', '', '王强'] })
    assert.ok(dirty.includes('王强') && !dirty.includes('参会人：  、'), '空参会人没被过滤')
  })

  it('B. refine() 失败后，发给 /api/llm/chat 的**就是**这份提示词', async () => {
    stubBrowser()
    const { createPinia, setActivePinia } = await import('pinia')
    setActivePinia(createPinia())
    const mod = await loadBundle(MEETINGS_API, 'meetings-api', { '../stores/syncPolicyStore.ts': ALWAYS_CLOUD_SYNC_POLICY_STUB, '../stores/auth': ALWAYS_DIRECT_AUTH_STUB })
    const seen = captureFallbackPrompt()

    const result = await mod.meetingsApi.refine(
      'm1', SEGMENTS, ['en'],
      { title: '客户评审会筹备', participants: ['张伟', '林岚'], topic: '悬界芯片客户评审会' },
    )

    // 后端失败 ⇒ 确实走了回落（这一步本身是接线是否生效的读数）。
    assert.ok(result.fromFallback, '后端 500 之后没有走回落 —— 接线前提失效')
    const chat = seen.find((s) => s.path === '/api/llm/chat')
    assert.ok(chat, `回落没有打 /api/llm/chat（实际打了 ${seen.map((s) => s.path).join(', ')}）` +
      ' —— 判据不能靠「没调用就当没违规」过关')
    const sent = chat.body.messages[0].content

    // ★ 这才是「实现被用上」的证据：断言**真正发出去的那份**。
    assert.ok(!sent.includes('润色'), '发出去的提示词里仍有「润色」')
    assert.ok(sent.includes('"action_items":[{"text":"","assignee":"","due":""}]'),
      '发出去的提示词没有非空 action_items 范例 —— 「时间点自动进日程」没有输入')
    assert.ok(sent.includes('名单上没有的人名'), '发出去的提示词没带「名单里的人名多半正确」')
    // ★ meta 必须真的透传到了回落（旧代码 fallbackRefine(segments, signal) 把 meta 丢了）。
    assert.ok(sent.includes('林岚'), '回落路径没拿到参会人 —— meta 没透传')
    assert.ok(sent.includes(TRANSCRIPT), '回落提示词里没有转写正文')
  })

  it('C. 跨语言：Go 那份必须同时满足同一份契约清单', async () => {
    const mod = await loadBundle(PROMPT_SRC, 'refine-prompt')
    const go = goRefinePrompt()

    for (const { what, literal } of CONTRACT) {
      assert.ok(go.includes(literal),
        `服务端提示词缺「${what}」（字面量 ${literal}）—— 两份实现已经漂了，` +
        `改一处请同步改另一处，并更新本文件的 CONTRACT 清单`)
    }
    assert.ok(!go.includes('润色'), '服务端提示词里又出现「润色」')
    assert.ok(go.includes('"action_items":[{"text":"","assignee":"","due":""}]'),
      '服务端 action_items 范例不是非空形态')
    // 清单本身不能被清空跑路（否则 C 组变成恒真）。
    assert.ok(CONTRACT.length >= 8,
      `契约清单只剩 ${CONTRACT.length} 条 —— 它是本门的不变量，不许被缩成一条`)

    // ★ 作用域自证：契约里必须**至少有一条**字面量不在 buildRefinePrompt 本体里，
    //   否则「抽取器覆盖了 metaTermHint」这件事无从证明 ——
    //   把抽取器改回单函数，门照样全绿。
    const bodyOnly = goRefinePromptBodyOnly()
    const onlyInHelper = CONTRACT.filter(({ literal }) => !bodyOnly.includes(literal))
    assert.ok(onlyInHelper.length > 0,
      '契约字面量全部落在 buildRefinePrompt 本体内 ⇒ 本门测不出抽取器的作用域，' +
        '负控缺失：把 goRefinePrompt 改回只切一个函数，这里会仍然通过')
    assert.ok(go.includes(CONTRACT[0].literal),
      '拼接后的提示词路径必须仍满足契约（自证拼接没把内容弄丢）')
  })
})

/**
 * ★ 负控（实测转红，不是摆设）。
 *
 * 本仓反复记过：门写完不注入违规，就不知道它是恒真还是判别。
 * 这里注入的是**历史上真实发生过的那个缺陷**（§54 发现 C 的原始形态），
 * 而不是随手编的假变异 —— 假变异可能落在够不到的地方，转红了也说明不了什么。
 */
describe('负控：还原 §54 发现 C 的原始形态，判据必须转红', () => {
  it('D1. 提示词退回「润色」+ 空数组 ⇒ A 组的判据函数真的抛', async () => {
    const mod = await loadBundle(PROMPT_SRC, 'refine-prompt')
    // ⚠ 与 A 用**同一份** meta（含 location）：D1 的前提是「基线先过同一个判据」，
    //   基线 meta 少一个字段 ⇒ 契约里的「会议地点：」不出现 ⇒ 变成假红。
    const base = mod.buildFallbackRefinePrompt(TRANSCRIPT, {
      title: '客户评审会筹备', participants: ['林岚'], location: '星海科技总部三楼',
      // ★ §143：契约清单加了「会议议题」之后，这一句**必须**同步补 ——
      //   少给就变成 §64.6 记的「基线 meta 少一个字段 ⇒ 假红」，
      //   而假红的负控证明不了任何东西（它红的原因不是我们要验的那个）。
      topic: '悬界芯片客户评审会',
    })
    // 基线必须先过，否则「变异转红」证明不了任何东西。
    assertPromptContract(base, CONTRACT)

    // ★ 还原 §54 发现 C 的**真实历史形态**（不是随手编的假变异）：
    //   根因词换回「润色」+ action_items 范例退回空数组。
    const mutated = base
      .replace('请**纠正明显的识别错误**', '请**润色**以下转写')
      .replace('"action_items":[{"text":"","assignee":"","due":""}]', '"action_items":[]')

    // 变异必须真的落到字符上（§「变异没落到字符上」不能当成「判据失效」）。
    assert.ok(mutated.includes('润色'), '变异没落到字符上 —— 判据的变异本身失效')
    assert.ok(/"action_items":\[\]/.test(mutated), '空数组变异没生效')
    assert.notEqual(mutated, base, '变异没改变任何东西')

    // ★ 关键：把**同一个判据函数**喂变异体，它必须真的抛。
    assert.throws(
      () => assertPromptContract(mutated, CONTRACT),
      /润色|action_items 范例又变回空数组/,
      '判据函数在真实历史缺陷上仍然放行 —— 本门是恒真的',
    )
  })

  it('D3. 主题里的专有名词被丢掉（§143 的缺陷）⇒ A 组判红在**那条**上', async () => {
    const mod = await loadBundle(PROMPT_SRC, 'refine-prompt')
    const base = mod.buildFallbackRefinePrompt(TRANSCRIPT, {
      title: '客户评审会筹备', participants: ['张伟', '林岚'],
      location: '星海科技总部三楼', topic: '悬界芯片客户评审会',
    })
    assertPromptContract(base, CONTRACT)   // 基线必须先过

    // 还原 §143 的缺陷形态：**标签还在，内容被丢了**（或者反过来）。
    // 只删内容不删标签，才能证明判据抓的是「用户真正需要的那部分」。
    const mutated = base.replace('悬界芯片客户评审会', '')
    assert.notEqual(mutated, base, '变异没改变任何东西')
    assert.ok(mutated.includes('会议议题'), '变异把标签也删了 ⇒ 它验的不是「内容被丢」这一种')
    // ★ 必须匹配**主题那一条**的消息：红在别的理由上等于没验。
    assert.throws(
      () => assertPromptContract(mutated, CONTRACT),
      /会议议题里的专有名词没进提示词/,
      '主题内容被丢掉时判据没有红在主题那一条上（可能红在了别处 ⇒ 白验）',
    )
  })

  it('D4. 主题为空标签时不得污染提示词（对应 Go 侧同款负控）', async () => {
    const mod = await loadBundle(PROMPT_SRC, 'refine-prompt')
    const dirty = mod.buildFallbackRefinePrompt(TRANSCRIPT, {
      title: '客户评审会筹备', topic: '   ',
    })
    assert.ok(!dirty.includes('会议议题'), '空白主题仍拼出了标签：' + dirty.slice(0, 120))
  })

  it('D2. meta 不透传（§54 的真缺陷）⇒ B 组判红', async () => {
    stubBrowser()
    const { createPinia, setActivePinia } = await import('pinia')
    setActivePinia(createPinia())
    const mod = await loadBundle(MEETINGS_API, 'meetings-api', { '../stores/syncPolicyStore.ts': ALWAYS_CLOUD_SYNC_POLICY_STUB, '../stores/auth': ALWAYS_DIRECT_AUTH_STUB })
    const seen = captureFallbackPrompt()
    // ★ 旧实现的真实调用形状：第三个实参只给 signal，meta 被丢掉。
    await mod.meetingsApi.refine('m1', SEGMENTS, ['en'], undefined, undefined)
    const chat = seen.find((s) => s.path === '/api/llm/chat')
    assert.ok(chat, '负控失败：回落根本没被触发')
    const sent = chat.body.messages[0].content
    assert.ok(!sent.includes('林岚'), '负控失败：meta 不传时判据仍然放行')
    // 同一批输入、meta 传了 ⇒ 判据必须转绿（否则就是恒真）。
    await mod.meetingsApi.refine('m1', SEGMENTS, ['en'], { participants: ['林岚'] })
    const chat2 = seen.filter((s) => s.path === '/api/llm/chat').pop()
    assert.ok(chat2.body.messages[0].content.includes('林岚'), 'meta 传了却没进提示词 —— 判据形同虚设')
  })
})