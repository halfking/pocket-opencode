import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

// spam-cleanup-move-safety.test.mjs — 需求 1「清理广告与垃圾邮件，将它们移到
// 垃圾邮件箱」在**客户端**的安全接线。
//
// ## 为什么这条要单独判
//
// 需求 1 的后端有 `POCKET_EMAIL_SPAM_DRYRUN` 安全阀（默认预演、不 MOVE），
// 而客户端这一侧的结构是「预览 → 用户确认 → 真移」两步。两条都成立时，
// 用户点「预览」绝不会动他的邮箱；任何一处接反，用户**点一次预览就把真实
// 邮件搬进垃圾箱**，而后端日志与报告一切正常——这比直接失败危险得多。
//
// 现状：`cleanup-filter.ts`（匹配纯函数）有覆盖，但
// 「视图点哪个按钮 → 调哪个函数 → 发什么 dryRun」这段**零覆盖**。
//
// ## 第 3 条判据是本文件最要紧的一条：展开顺序
//
//   ✅ { ...filter, dryRun: true }   —— 后者胜出，调用方**无法**覆盖
//   ❌ { dryRun: true, ...filter }   —— 前者被覆盖，一个 dryRun:false 就能
//                                     让「预览」变成真搬
//
// 两种写法读起来几乎一样，IDE 也不提示，编译也过。所以必须判**顺序**，
// 而不是只判「文件里出现过 dryRun: true」。

const HERE = path.dirname(fileURLToPath(import.meta.url)) // src/features/email/__tests__
const FEAT = path.resolve(HERE, '..') // src/features/email
const APIDIR = path.resolve(HERE, '..', '..', '..', 'api') // src/api

const readSrc = p => fs.readFileSync(p, 'utf8')
const apiSrc = () => readSrc(path.join(APIDIR, 'email-cleanup.ts'))
const viewSrc = () => readSrc(path.join(FEAT, 'EmailSpamCleanupView.vue'))

/** 取某个导出函数体里的 JSON.stringify 实参原文（按大括号配平）。 */
function bodyArg(src, fnName) {
  const i = src.indexOf(`export function ${fnName}(`)
  assert.notEqual(i, -1, `email-cleanup.ts 里找不到 ${fnName} —— 判据可能已失效`)
  const open = src.indexOf('JSON.stringify(', i)
  assert.notEqual(open, -1, `${fnName} 没有 JSON.stringify 请求体`)
  let depth = 0
  for (let k = open + 'JSON.stringify('.length - 1; k < src.length; k++) {
    const ch = src[k]
    if (ch === '(' || ch === '{') depth++
    else if (ch === ')' || ch === '}') {
      depth--
      if (depth === 0) return src.slice(open + 'JSON.stringify('.length, k)
    }
  }
  throw new Error(`${fnName} 的 JSON.stringify 括号不配平`)
}

test('预览发 dryRun:true，真移发 dryRun:false', () => {
  const src = apiSrc()
  assert.match(bodyArg(src, 'previewEmailCleanup'), /dryRun:\s*true/,
    'previewEmailCleanup 没有发 dryRun:true —— 「预览」会变成真搬邮件')
  assert.match(bodyArg(src, 'runEmailCleanup'), /dryRun:\s*false/,
    'runEmailCleanup 没有发 dryRun:false —— 「确认移到垃圾箱」会变成只预演')
})

test('dryRun 写在展开之后：调用方无法覆盖这个安全值', () => {
  const src = apiSrc()
  for (const fn of ['previewEmailCleanup', 'runEmailCleanup']) {
    const arg = bodyArg(src, fn).replace(/\s+/g, ' ').trim()
    const spreadAt = arg.indexOf('...filter')
    const dryAt = arg.search(/dryRun\s*:/)
    assert.notEqual(spreadAt, -1, `${fn} 的请求体里没有 ...filter（判据可能已失效）`)
    assert.notEqual(dryAt, -1, `${fn} 的请求体里没有 dryRun（判据可能已失效）`)
    assert.ok(
      dryAt > spreadAt,
      `${fn} 的请求体是 { dryRun, ...filter } 形态：展开在后 ⇒ 调用方传一个\n` +
        '  dryRun 就能覆盖掉这个安全值，看起来和现在一样、行为完全相反。\n' +
        `  实际写法：${arg}`,
    )
  }
})

test('视图里「预览匹配」走 previewEmailCleanup，「确认移到垃圾箱」走 runEmailCleanup', () => {
  const src = viewSrc()
  // 按钮文案与调用的对应关系：@click 与按钮文字必须落在同一块模板里，
  // 否则「预览」按钮可能真的在发真移请求。
  assert.match(
    src,
    /@click="preview"[\s\S]{0,200}?预览匹配/,
    '「预览匹配」按钮不再调用 preview() —— 判据可能已失效',
  )
  assert.match(
    src,
    /async function preview\(\)[\s\S]{0,900}?previewEmailCleanup\(/,
    'preview() 没有调 previewEmailCleanup —— 预览路径可能变成了真移',
  )
  assert.match(
    src,
    /async function confirmDelete\(\)[\s\S]{0,900}?runEmailCleanup\(/,
    'confirmDelete() 没有调 runEmailCleanup —— 确认后什么都不会发生',
  )
  // 按钮图标（调用）在文案之前还是之后都行，但「确认移到垃圾箱」这个文案
  // 必须在**真正执行的函数**上，不能是另一个只更新本地状态的函数。
  assert.match(src, /确认移到垃圾箱/)
})

test('真移前必须先预览过、且有匹配项，并经过一次确认', () => {
  const src = viewSrc()
  // 没预览过 / 没匹配项时，确认按钮必须禁用
  assert.match(
    src,
    /:disabled="busy \|\| !previewed \|\| matched === 0"/,
    '确认按钮不再要求「已预览且有匹配项」—— 用户可以在没看过清单时直接搬邮件',
  )
  // 真移前要有一次用户确认，不能一键到底
  assert.match(
    src,
    /const ok = await confirm[\s\S]{0,200}?if \(!ok\) return/,
    'confirmDelete 在真移之前没有要用户确认 —— 需求 1 的破坏性操作变成了一键',
  )
})
