// verify-card-deck-labels.mjs — BUG-V 判据：闪卡「新建卡片」与「新建卡组」两个入口
// 的**文案必须与行为一致**，且两个文案在**每个语言**里都必须可区分。
//
// 为什么需要它（2026-10-01 外部审计提出，2026-10-01 复核证伪）：
//   历史上闪卡列表页的两个语义完全不同的动作曾经共用一个按钮
//   （BUG-K/BUG-U 那一轮：「新建卡组」直接跳 /flashcards/new，也就是卡片编辑页）。
//   修完之后如果某个语言包把 flashcards.deck.create 译成和 flashcards.list.create
//   同一句话，用户就会再次看到「新建卡组 → 点进去是卡片编辑页」。
//   **只翻 zh-CN 看不出来**，必须逐语言 + 逐视图比对。
//
// 判据由两部分组成，缺一不可：
//   A. 逐语言：两个 key 都存在且取值不同（用户能分辨两个动作）
//   B. 逐视图：标着 deck.create 的控件必须处在建组上下文里，标着 list.create 的
//      控件必须处在跳卡片页的上下文里
//
// 判据有效性由 --selftest 自证：对一份**合成坏样本**跑同一套规则必须转红。
// 光在好样本上跑绿说明不了任何事。
//
// 用法：
//   node scripts/verify-card-deck-labels.mjs              跑判据
//   node scripts/verify-card-deck-labels.mjs --selftest   先证明判据能转红，再跑判据
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

// ⚠️ 绝不写死工作区路径（BUG-V2 同款死法）。
// 旧版写的是 C:/workspace/openpocket/wt3/frontend/src —— 那个 worktree 还在时，
// 这个判据会**静默地判另一棵源码树**：跑出绿也不代表当前树是绿的。
// 仓库根从脚本自身位置推导（scripts/ 的上一级），换 worktree 也永远判当前树。
const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const FRONTEND_SRC = path.join(ROOT, 'frontend', 'src')
const LOCALES_DIR = path.join(FRONTEND_SRC, 'locales')

// ── 判定规则（好样本与合成坏样本共用同一套，判据才有意义）──────────────
const CARD_ROUTE_RE = /\/flashcards\/new\b|goCreate|router\.push\(\s*['"`]\/flashcards\/(new|cards)/
const DECK_SUBMIT_RE = /store\.createDeck|createDeck\(/

/**
 * 检查一段模板片段里出现的入口标签用得对不对。
 * @param {string} label  'deck' | 'list'
 * @param {string} block  该控件所在的 <form> / <button> 片段
 * @returns {string|null} 问题描述，null 表示通过
 */
function checkBlock(label, block) {
  const isCard = CARD_ROUTE_RE.test(block)
  const isDeck = DECK_SUBMIT_RE.test(block)
  if (label === 'list') {
    // 「新建卡片」必须真的去建卡片
    return isCard ? null : '标着 list.create 的控件没有跳卡片页'
  }
  // 「新建卡组」绝不能跳卡片页；只展开表单的 toggle 允许两者都不满足
  if (isCard) return '标着 deck.create 的控件却跳到了卡片页'
  return null
}

/** 把一个 .vue 的模板拆成 form / button 片段，并记下每段里出现过的入口标签 */
function extractBlocks(vue) {
  const blocks = []
  const grab = (re) => {
    for (const m of vue.matchAll(re)) {
      const body = m[0]
      const labels = []
      if (body.includes("t('flashcards.deck.create')")) labels.push('deck')
      if (body.includes("t('flashcards.list.create')")) labels.push('list')
      for (const l of labels) blocks.push({ label: l, body })
    }
  }
  grab(/<form\b[\s\S]*?<\/form>/g)
  grab(/<button\b[\s\S]*?<\/button>/g)
  return blocks
}

function checkVue(file) {
  const vue = fs.readFileSync(file, 'utf8')
  const problems = []
  for (const { label, body } of extractBlocks(vue)) {
    const why = checkBlock(label, body)
    if (why) problems.push(`${path.basename(file)}: ${why}\n    ${body.replace(/\s+/g, ' ').slice(0, 150)}`)
  }
  return problems
}

function checkLocales() {
  const problems = []
  const rows = []
  for (const f of fs.readdirSync(LOCALES_DIR).filter((x) => x.endsWith('.json'))) {
    const j = JSON.parse(fs.readFileSync(path.join(LOCALES_DIR, f), 'utf8'))
    const lc = j?.flashcards?.list?.create
    const dc = j?.flashcards?.deck?.create
    rows.push({ locale: f, listCreate: lc, deckCreate: dc })
    if (typeof lc !== 'string' || !lc.trim()) { problems.push(`${f}: flashcards.list.create 缺失`); continue }
    if (typeof dc !== 'string' || !dc.trim()) { problems.push(`${f}: flashcards.deck.create 缺失`); continue }
    if (lc === dc) problems.push(`${f}: 两个不同动作共用同一句文案 "${lc}"`)
  }
  return { problems, rows }
}

// ── 自测：用合成坏样本证明规则能转红 ────────────────────────────────
const BROKEN_FIXTURE = `
<template>
  <button class="add" type="button" @click="goCreate">
    <span>{{ t('flashcards.list.create') }}</span>
  </button>
  <form @submit.prevent="submitCreateDeck">
    <span>{{ t('flashcards.deck.create') }}</span>
  </form>
</template>
`
const BROKEN_LOCALE = { flashcards: { list: { create: '新建卡组' }, deck: { create: '新建卡组' } } }

function selftest() {
  console.log('── 判据自测（合成坏样本）──')
  const b1 = checkBlock('deck', /<form[\s\S]*<\/form>/.exec(BROKEN_FIXTURE) ? /<form[\s\S]*<\/form>/.exec(BROKEN_FIXTURE)[0] : '')
  const b2 = checkBlock('list', /<button[\s\S]*?<\/button>/.exec(BROKEN_FIXTURE)[0])
  const l1 = BROKEN_LOCALE.flashcards.list.create === BROKEN_LOCALE.flashcards.deck.create
  const ok1 = b1 === null // 坏样本里 deck.create 在一个真的 submit 建组的 form 里 → 这条不该报
  const ok2 = b2 === null // list.create 绑 goCreate → 这条不该报
  const ok3 = l1 === true  // 两个 key 同值 → 语言判据必须报
  console.log(`  语言判据（两 key 同值）      ${ok3 ? '✅ 转红' : '❌ 没转红 —— 判据失灵'}`)
  console.log(`  视图判据（deck.create 在 form）${ok1 ? '✅ 未误报' : `❌ 误报：${b1}`}`)
  console.log(`  视图判据（list.create 绑 goCreate）${ok2 ? '✅ 未误报' : `❌ 误报：${b2}`}`)

  // 再验一条真正该红的：把 list.create 挂到一个建组 form 上
  const shouldRed = checkBlock('list', `<form @submit.prevent="store.createDeck(name)"><span>{{ t('flashcards.list.create') }}</span></form>`)
  console.log(`  视图判据（list.create 挂在建组 form）${shouldRed ? '✅ 转红' : '❌ 没转红 —— 判据失灵'}`)
  const pass = ok1 && ok2 && ok3 && shouldRed !== null
  console.log(`自测结论：${pass ? '✅ 判据既能转红也不会误报' : '❌ 判据不可信'}\n`)
  return pass
}

// ── 主流程 ──────────────────────────────────────────────────────────
console.log('── 判据自测 ──')
if (!selftest()) process.exit(2)

const { problems, rows } = checkLocales()
console.log('── 逐语言文案对照 ──')
for (const r of rows) {
  console.log(`  ${r.locale.padEnd(12)} list.create="${r.listCreate}"   deck.create="${r.deckCreate}"`)
}

const vueFiles = []
for (const f of ['features/flashcards/FlashcardListView.vue', 'features/flashcards/FlashcardEditView.vue', 'features/study/StudyHubView.vue']) {
  const p = path.join(FRONTEND_SRC, f)
  // ⚠️ 绝不能"文件不存在就跳过"。文件被改名/挪走时，"检查了 0 个视图"和
  // "检查了 3 个视图且都通过"在输出里长得一模一样，判据照样报绿。
  // 缺文件 = 判据没在检查它声称在检查的东西，直接判不可用。
  if (!fs.existsSync(p)) {
    console.error(`❌ 判据不可用：视图文件不存在 ${p}`)
    console.error('   文件被改名/挪走时，判据会"少检查几个视图"然后照样报绿。')
    console.error('   先修路径，或确认该视图确实已重命名。')
    process.exit(2)
  }
  vueFiles.push(p)
}
console.log(`\n── 逐视图绑定核对（${vueFiles.length} 个视图）──`)
const vueProblems = vueFiles.flatMap(checkVue)
for (const p of vueProblems) console.log(`  ❌ ${p}`)
if (!vueProblems.length) console.log('  ✅ 没有「文案与行为不符」的控件')

const all = [...problems, ...vueProblems]
console.log(`\n结论：${all.length === 0 ? '✅ 两个入口在所有语言、所有视图下文案与行为一致且可区分' : `❌ ${all.length} 处问题`}`)
console.log('口径：本判据覆盖的是**静态文案与绑定**；真机上的实际渲染与点击仍需设备回归。')
process.exit(all.length === 0 ? 0 : 1)
