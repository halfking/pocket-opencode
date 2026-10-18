// local-storage-corruption-audit.test.ts — §858：「读失败」被谎报成「从来没有过」。
//
// 缺陷背景（aiChatStore）：
//   function loadConversations(): Conversation[] | null {
//     try { … return raw ? migrateConversations(JSON.parse(raw)) : null }
//     catch { return null }          // ⚠ 读失败 ⇒ 与「没有对话」同一个值
//   }
//   function init() {
//     const saved = loadConversations()
//     if (saved && saved.length > 0) { … } else { createConversation() }
//   }
//   // createConversation() 最后调 persist()，persist() 做 setItem(同一个 KEY)
// ⇒ localStorage 里的 JSON 一旦损坏，界面显示「开始你的 AI 对话」（用户型），
//   并且**唯一的排查证据（那串损坏内容）会被 persist 立刻覆盖掉**。
//
// ⚠⚠ 本节有一处**实测推翻了我动手前的判断**，必须原样记账：
//   我最初写「一次读失败 = 全部 AI 对话历史被永久覆盖（不可逆）」。
//   用 esbuild 打包后真跑 init()（.artifacts/s858-probe/repro-d21-entry.mjs）的读数是：
//     · 种入 2 条历史 → 改写成半截内容 → init() 后 conversations = 1，
//       原 KEY **确实被覆盖**（`persist()` 干的），但
//     · **留证里存的是「已经损坏的那份串」，不是可解析的历史**；
//   ⇒ 数据不是在 init() 那一刻丢的（存储里本来就已经坏了），
//     init() 造成的是 ①谎报成「从来没有过」②销毁唯一的排查证据。
//   ⇒ 界面文案因此**不能说「可在 corrupt 键下找回」** —— 那是我第一版写的假话。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const HERE = dirname(fileURLToPath(import.meta.url))
const SRC = join(HERE, '..')
const read = (rel: string) => readFileSync(join(SRC, rel), 'utf8')

const strip = (t: string): string =>
  t.replace(/\/\*[\s\S]*?\*\//g, (m) => m.replace(/[^\n]/g, ' '))
   .replace(/(^|[^:])\/\/[^\n]*/g, '$1')
   .replace(/<!--[\s\S]*?-->/g, (m) => m.replace(/[^\n]/g, ' '))

const STORE = read('features/ai-chat/aiChatStore.ts')
const VIEW = read('features/ai-chat/AIChatView.vue')
const TPL = strip(/<template>([\s\S]*)<\/template>/.exec(VIEW)?.[1] ?? '')

/** 取函数体：先 strip 注释，再从 `(` 起做**圆括号配平**（形参里可能有 `{ id: string }`）。 */
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

describe('§858 · 尺子自证', () => {
  it('★ 文件读到了（路径没写错，否则后面全是绿灯假象）', () => {
    assert.ok(STORE.includes('loadConversations'), '没读到 aiChatStore 的 loadConversations')
    assert.ok(TPL.includes('recovered-notice'), '没读到 AIChatView 的模板')
  })
})

describe('§858 · D21 读失败必须与「没有数据」区分开', () => {
  it('★★★ loadConversations 的 catch 不得静默返回 null', () => {
    const body = fnBody(STORE, 'loadConversations')
    assert.ok(body, '找不到 loadConversations')
    const catchAt = body.indexOf('catch')
    assert.ok(catchAt > 0, 'loadConversations 里没有 catch —— 若它已改成抛错，本节前提需重判')
    const tail = body.slice(catchAt)
    assert.match(tail, /preserveCorrupt\(/,
      'catch 里没有留证 ⇒ persist() 覆盖后，损坏内容这个唯一的排查证据就没了（实测已确认它会被覆盖）')
    assert.match(tail, /conversationsRecovered\.value = true/,
      'catch 里没有置位 ⇒ 界面分不清「损坏」与「从来没有过对话」')
  })

  it('★★ settings 是同族，也必须留证 + 置位', () => {
    const body = fnBody(STORE, 'loadSettings')
    assert.ok(body, '找不到 loadSettings')
    const catchAt = body.indexOf('catch')
    assert.ok(catchAt > 0)
    const tail = body.slice(catchAt)
    assert.match(tail, /preserveCorrupt\('settings'/, 'settings 的读失败也必须留证')
    assert.match(tail, /settingsRecovered\.value = true/, 'settings 的读失败也必须置位')
  })

  it('★★ 两个标志必须导出（否则视图无从判断）', () => {
    // ⚠ 锚点要明确：store 文件里有多个 `return {`（DEFAULT_SETTINGS、loadSettings…），
    //   不锚定的话会取到更早的那个 ⇒ 断言在**错的块**上跑（第一次跑就是这样红的）。
    const ret = /useAIChatStore[\s\S]*?\n  return \{[\s\S]*?\n  \}/.exec(strip(STORE))?.[0] ?? ''
    assert.match(ret, /conversationsRecovered/, 'store 的返回值里缺 conversationsRecovered')
    assert.match(ret, /settingsRecovered/, 'store 的返回值里缺 settingsRecovered')
  })

  it('★★★ init() 仍会在读失败时新建对话（这是刻意保留的行为）', () => {
    // ⚠ 判据要钉住**为什么保留**：改掉它会让 AIChatView 整个空掉、连输入框都没有。
    // 真正的修复不是「不新建」，而是「新建之前先留证 + 如实告知」。
    const body = fnBody(STORE, 'init')
    assert.ok(body, '找不到 init')
    assert.match(body, /else\s*\{\s*createConversation\(\)\s*\}/,
      'init() 不再在读失败时新建对话 ⇒ 界面会彻底空掉；请重新判断这个改动的后果')
  })
})

describe('§858 · 界面必须说**实话**（文案不得吹牛）', () => {
  it('★★★ 提示必须渲染，且在所有空态分支之外', () => {
    assert.match(TPL, /v-if="store\.conversationsRecovered \|\| store\.settingsRecovered"/,
      '模板缺少读失败提示的出口')
    const at = TPL.indexOf('conversationsRecovered')
    const chainAt = TPL.indexOf('turns.length === 0')
    assert.ok(at > 0 && chainAt > 0, '模板结构变了')
    assert.ok(at < chainAt, '提示排在空态之后 —— 而读失败时正会落进那个空态，等于什么都看不到')
  })

  it('★★★ 文案不得承诺「可以找回」', () => {
    // ⚠ 这一条来自本节的实测：第一版文案写着「原始数据已另存（未被覆盖），
    //   可在 corrupt 键下找回」，而实测读数是「留证里存的是**已经损坏**的串」。
    //   ⇒ 那是一句**界面说谎**，与 §856 修的是同一族。
    const banner = /class="recovered-notice"[\s\S]{0,600}?<\/div>/.exec(TPL)?.[0] ?? ''
    assert.ok(banner, '找不到提示区块')
    assert.equal(/找回|已备份|可以恢复|未丢失/.test(banner), false,
      '文案承诺了可恢复性 —— 实测证明留证里只是损坏内容，说这话是骗用户')
    assert.match(banner, /无法自动恢复|不能自动恢复/, '文案必须说清「应用无法自动恢复」')
    assert.match(banner, /供排查/, '文案必须说清留证的用途是排查')
  })

  it('★ 提示里必须点出留证键的位置（否则用户找不到）', () => {
    const banner = /class="recovered-notice"[\s\S]{0,600}?<\/div>/.exec(TPL)?.[0] ?? ''
    assert.match(banner, /pocket:ai-chat:corrupt/, '提示必须写明留证键名')
  })
})

describe('§858 · 变异的反向对照（判据不能过宽）', () => {
  it('★ 「catch 里 throw」是另一种合规修法，不该被本判据判红', () => {
    // 判据查的是「留证 + 置位」这一**具体形状**（它能保住排查证据）；
    // 若属主将来改成「读失败就抛错让上层兜住」，本判据会红 ⇒ 那时要重新判断
    // 证据是否还保得住，而不是直接放宽断言。
    const alt = `try { return JSON.parse(raw) } catch (e) { throw new Error('corrupt') }`
    assert.equal(/preserveCorrupt\(/.test(alt), false, '抛错方案里没有留证，本判据会红（这是预期的）')
  })
})

describe('§858 · 变异负控', () => {
  it('★ 把留证与置位退回修复前 ⇒ 判据转红', () => {
    const mutated = STORE
      .replace(/    preserveCorrupt\('conversations', safeGetItem\(storageKey\(STORAGE_KEY_PREFIX\)\)\)\n\s*conversationsRecovered\.value = true\n/, '')
    assert.notEqual(mutated, STORE, '★ 变异没生效')
    const body = fnBody(mutated, 'loadConversations')
    assert.equal(/preserveCorrupt\(/.test(body), false, '变异后不该再有留证')
  })

  it('★ 把文案改回「可找回」⇒ 判据转红', () => {
    const mutated = VIEW.replace('应用<strong>无法自动恢复</strong>它', '原始数据已备份，可在 corrupt 键下找回')
    assert.notEqual(mutated, VIEW, '★ 变异没生效')
    const banner = /class="recovered-notice"[\s\S]{0,600}?<\/div>/.exec(
      strip(/<template>([\s\S]*)<\/template>/.exec(mutated)?.[1] ?? ''))?.[0] ?? ''
    assert.match(banner, /找回/, '变异后文案确实承诺了找回')
    assert.equal(/无法自动恢复|不能自动恢复/.test(banner), false, '而变异后丢掉了「无法自动恢复」')
  })
})
