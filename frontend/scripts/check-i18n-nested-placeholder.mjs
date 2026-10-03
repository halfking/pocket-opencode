// 负控 + 修法验证：用仓库自带的 vue-i18n 实测
// `SyntaxError: Not allowed nest placeholder` 的触发与消除。
//
// 现场（2026-10-04 00:15 真机）：flashcards-write 失败截图是**纯空白页**，
// `<main id="main">` 的 children.length = 0，textarea/input/button 全为 0，
// 控制台反复抛 Not allowed nest placeholder；重新导航一次后又正常渲染
// ⇒ 闪卡建卡页**间歇性**渲染失败。
//
// 根因：flashcards.edit.clozePlaceholder / clozeHint 这两个词条用
// `{{c1::答案}}` 给用户演示挖空语法，而 vue-i18n 的消息编译器把 `{`
// 当占位符开始符，解析到嵌套 `{` 直接抛错。FlashcardEditView.vue:110/115
// 在**渲染期**就调用 t()，即使当前是「正面·反面」模板、那个分支没显示。
import { createI18n } from 'vue-i18n'

// ⚠️ 判据落点找了三次才对，走过的两条弯路都记在这里：
//   ① 判「t() 会不会抛错」        → 实测不抛，还返回了正常字符串（失明）
//   ② 判「createI18n 时会不会打 console」→ 实测词条是**懒编译**，不打（失明）
//   ③ 正解：抓 **t() 调用期间**的 console.error —— 实测那里稳定打出 8 条
//      `Message compilation error: …`。
//
// 机理：vue-i18n 把编译错误只打到 console、**t() 本身不抛**，于是调用方
// 拿到的是「原样返回的字面量」，看不出出错。真正崩的是**组件渲染期**：
// 探针 probe-i18n-err-locus.mjs 实测
//     [Vue warn]: Unhandled error during execution of setup function
// ⇒ 渲染中断 ⇒ 真机上表现为**空白页**（`<main>` 的 children.length = 0），
//   而不是抛异常堆栈，极难定位。
const compileMsg = (msg) => {
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: { k: msg } } })
  const hits = []
  const orig = console.error
  console.error = (...a) => hits.push(a.map(String).join(' ').split('\n')[0])
  let out = ''
  try {
    out = i18n.global.t('k')
  } catch (e) {
    return { ok: false, out: `throw: ${e.message}`, rendered: '' }
  } finally {
    console.error = orig
  }
  const bad = hits.filter((l) => /compilation error/i.test(l))
  return { ok: bad.length === 0, out: bad.join(' | '), rendered: out }
}

const BAD = "Use {{c1::answer}} or {{c1::answer::hint}} to mark occlusions."
const FIXED = "Use {'{'}c1::answer{'}'} or {'{'}c1::answer::hint{'}'} to mark occlusions."

// 转义写法必须真的还原出字面量，不能只是「不报错」
const literalOf = (msg) => {
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: { k: msg } } })
  try {
    return i18n.global.t('k')
  } catch {
    return ''
  }
}

const bad = compileMsg(BAD)
const fixed = compileMsg(FIXED)

console.log('=== 负控：修前的词条（抓 t() 期间的 console.error）===')
console.log('输入:', BAD)
console.log('结果:', bad.ok ? '无编译错误（判据失明！）' : `报编译错误 → ${bad.out}`)
console.log('t() 返回:', JSON.stringify(bad.rendered))
if (bad.ok) {
  console.log('❌ 预期它报错，实际没报 —— 判据失明，不能用它当负控')
  process.exit(1)
}

console.log('\n=== 修法：花括号字面量转义 ===')
console.log('输入:', FIXED)
console.log('结果:', fixed.ok ? '无编译错误' : `报编译错误 → ${fixed.out}`)
console.log('t() 返回:', JSON.stringify(fixed.rendered))
if (!fixed.ok) {
  console.log('❌ 转义写法无效')
  process.exit(1)
}
if (!fixed.rendered.includes('{c1::answer}') || !fixed.rendered.includes('{c1::answer::hint}')) {
  console.log('❌ 输出里没有还原出挖空语法字面量，转义破坏了内容')
  process.exit(1)
}

console.log('\n✅ 判据有牙齿（坏写法在 t() 期间报错），且转义修法有效且保真')

// ---------- 扫全部 locale 文件 ----------
console.log('\n=== 扫描全部 locale 词条 ===')
import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'

const LOCALES_DIR = new URL('../src/locales/', import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, '$1')
const files = readdirSync(LOCALES_DIR).filter((f) => f.endsWith('.json'))

const walk = (obj, path, out) => {
  for (const [k, v] of Object.entries(obj || {})) {
    const p = path ? `${path}.${k}` : k
    if (typeof v === 'string') out.push([p, v])
    else if (v && typeof v === 'object') walk(v, p, out)
  }
}

let total = 0
for (const f of files) {
  const json = JSON.parse(readFileSync(join(LOCALES_DIR, f), 'utf8'))
  const entries = []
  walk(json, '', entries)
  const problems = []
  for (const [key, value] of entries) {
    if (value.includes('{') || value.includes('}')) {
      const r = compileMsg(value)
      if (!r.ok) problems.push({ key, value, err: r.out.split(' | ')[0] })
    }
  }
  if (problems.length) {
    console.log(`\n❌ ${f} —— ${problems.length} 条`)
    for (const p of problems) console.log(`   ${p.key}\n     ${p.value}\n     → ${p.err}`)
    total += problems.length
  }
}

console.log(`\n扫描 ${files.length} 个 locale 文件，问题词条 ${total} 条`)
if (total > 0) {
  console.log('❌ 存在会让组件渲染中断的词条（真机表现为空白页）')
  process.exit(1)
}
console.log('✅ 全部词条在 t() 期间均无编译错误')
