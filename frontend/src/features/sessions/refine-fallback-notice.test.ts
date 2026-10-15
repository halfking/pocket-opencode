import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

// 精校「静默伪装成成功」的门禁。
//
// 缺陷（2026-10-06 真网关复测时发现）：服务端 parseRefineJSON 失败会回落成
// 原始转写并照常返回 200，结构完整 ⇒ 前端照常渲染 ⇒ 而 toast 是**无条件**的
// `toast.success('录音已结束，精翻完成')`。用户被告知精翻完成，
// 屏幕上那份文本实际是他刚说过的原话。
//
// 这道门分两部分，因为两部分的**可测性不同**：
//   · API 层（normalizeRefine）—— 纯数据变换，**行为断言**；
//   · 服务端与 composable —— 跨语言 / 需组件运行时，只能源码断言。
//     后者的局限写在注释里：它能抓住「判断被删/被短路」，
//     但抓不住「判断在、结论算错」。
const here = dirname(fileURLToPath(import.meta.url))
const read = (p: string) => readFileSync(join(here, p), 'utf8')

/** ★ 剥注释（等长替换，保持下标可比）——注释里的 `meetingsApi.refine(...)`
 *  说明文字会让「refine 调用点在哪」这类判据命中假位置（§49 实测踩到）。 */
const blank = (m: string) => m.replace(/[^\n]/g, ' ')
const stripComments = (t: string) => t
  .replace(/\/\*[\s\S]*?\*\//g, blank)
  .split('\n').map((l) => { const i = l.indexOf('//'); return i < 0 ? l : l.slice(0, i) }).join('\n')

const segs: MeetingSegment[] = [
  {
    id: 's1',
    meetingId: 'm1',
    speakerLabel: '张伟',
    lang: 'zh',
    confidence: 1,
    startMs: 0,
    endMs: 3000,
    text: '今天下午三点开评审会',
  },
]

describe('精校降级信号必须端到端透出', () => {
  // ---- API 层：源码断言（**且必须收紧**）----
  //
  // ⚠ 这里**不能**写成行为断言：`src/api/meetings.ts` 自身用了无扩展名
  // import（'./http'），项目 runner 是裸 `node --test`（无 tsx loader），
  // 所以那个模块在测试里根本 import 不了。这不是本轮引入的 ——
  // run-mjs-tests.mjs 的豁免清单里已记着同类问题（修它属于另一个 PR）。
  //
  // 因此断言必须**具体到表达式**。第一版写成 /refine_fallback|refineFallback/
  // 结果变异「只去掉 snake_case 分支」后仍然绿（camelCase 那行还在，文本照样
  // 匹配）——判据量的是「代码写在那」，不是「会执行」。
  // 现在**两个字段名都必须出现**，那个变异随即转红。
  it('normalizeRefine 同时认 snake_case 与 camelCase 两种降级字段', () => {
    const api = read('../../api/meetings.ts')
    const i = api.indexOf('export function normalizeRefine')
    assert.ok(i > 0, '找不到 normalizeRefine')
    // ⚠⚠ 2026-10-11（§426 顺带修）：这里原先是 `api.slice(i, i + 2000)` ——
    // 一个**魔法窗口**。§426 只是给 normalizeRefine 加了一段解释输入规模的注释，
    // 函数体从 ~1900 字涨到 2373 字 ⇒ 窗口把 return 块截掉了 ⇒
    // **一道判据形状的断言被一次正当改动打成红**。
    //
    // 这与本文件 §2026-10-06 那次「字符距离判据被正当重构作废」是**同一个形状**
    // （注释里已记：它量的是某段文本的形状，不是不变量）。
    // ⇒ 处置按 §410 的纪律：**只放开形式**，不降判据 ——
    //   改用本文件已有的 braceBlockAfter 按**花括号配平**取函数体，
    //   插多少行无关代码都不再影响它，而判据本身（降级信号必须写进返回值）一字未动。
    const fn = braceBlockAfter(api, i)
    assert.ok(
      fn !== null && fn.length > 500,
      `normalizeRefine 的函数体没取到（实测 ${fn?.length ?? 0} 字）—— 取体逻辑坏了`,
    )
    const fnBody = fn!
    const returnAt = fnBody.indexOf('return {')
    assert.ok(returnAt > 0, 'normalizeRefine 体内找不到 return { —— 取体逻辑坏了')
    const init = fnBody.slice(0, returnAt)
    assert.ok(
      init.includes('raw.refine_fallback === true'),
      'normalizeRefine 没认 snake_case 的 refine_fallback',
    )
    assert.ok(
      init.includes('raw.refineFallback === true'),
      'normalizeRefine 没认 camelCase 的 refineFallback',
    )
    assert.ok(
      /serverRefined === undefined/.test(init) && /serverRefined === null/.test(init),
      'normalizeRefine 没把「服务端压根没回 refined_transcript」识别为降级',
    )
    assert.ok(
      /fromFallback\s*\?\s*\{\s*fromFallback:\s*true/.test(fnBody) ||
        /fromFallback,\s*fromFallback/.test(fnBody) ||
        fnBody.includes('fromFallback: true'),
      'normalizeRefine 算出了 fromFallback 却没有写进返回值 ⇒ 信号在最后一跳丢掉',
    )
  })

  // ---- 源码断言：跨语言的那一段 ----
  //
  // ⚠⚠ 2026-10-06 **判据自身被一次正当重构作废过一次**，这里记下改法。
  //
  // 原判据：从调用点往后取 900 字符，看里面有没有 `"refine_fallback"`。
  // 它在两个 unrelated 的编辑后**误报红**，而产品是好的：
  //   1. 服务端把回落载荷抽成了独立函数 `refineFallbackPayload()`，
  //      `"refine_fallback": true` 合法地搬进了那个函数；
  //   2. 另一个会话在调用点与回落之间插了约 2900 字符的提示词改动，
  //      距离从 <900 变成 2935。
  // ⇒ **判据量的是「字符距离」，不是「降级有没有带上信号」。**
  //
  // 改法：把「距离」换成「**结构**」——
  //   ① 从调用点做**括号配对**取出 `if err != nil { … }` 分支本身；
  //   ② 分支里若直接出现标记，判定通过；
  //   ③ 否则顺着分支里**调用到的具名函数**去找标记（本次重构的情形）。
  // 这样插入多少行无关代码都不影响，而标记被摘掉仍然会红（见变异验证）。
  const braceBlockAfter = (src: string, from: number): string | null => {
    const open = src.indexOf('{', from)
    if (open < 0) return null
    let depth = 0
    for (let k = open; k < src.length; k++) {
      if (src[k] === '{') depth++
      else if (src[k] === '}') {
        depth--
        if (depth === 0) return src.slice(open, k + 1)
      }
    }
    return null
  }

  const bodyOfFunc = (src: string, name: string): string | null => {
    const at = src.indexOf(`func ${name}(`)
    return at < 0 ? null : braceBlockAfter(src, at)
  }

  it('服务端在解析失败时标记 refine_fallback', () => {
    const go = readFileSync(
      join(here, '../../../../backend/internal/server/server_meeting.go'),
      'utf8',
    )
    const i = go.indexOf('parsed, err := parseRefineJSON(content)')
    assert.ok(i > 0, '找不到 parseRefineJSON 的调用点')

    const branch = braceBlockAfter(go, go.indexOf('if err != nil', i))
    assert.ok(branch, 'parseRefineJSON 调用点之后没有 err != nil 分支')
    assert.ok(branch!.length > 2, 'err != nil 分支是空的 ⇒ 解析失败不会回落')

    if (branch!.includes('"refine_fallback"')) return

    // 标记被抽进了分支里调用的具名函数（本次重构：refineFallbackPayload）。
    const called = [...branch!.matchAll(/\b([a-z][A-Za-z0-9_]*)\s*\(/g)]
      .map((m) => m[1])
      .filter((n) => n !== 'if' && n !== 'for' && n !== 'switch' && n !== 'return')
    const viaHelper = called.some((n) => (bodyOfFunc(go, n) ?? '').includes('"refine_fallback"'))
    assert.ok(
      viaHelper,
      `parseRefineJSON 失败的回落分支既没直接设 refine_fallback，` +
        `调用的函数（${[...new Set(called)].join(',') || '无'}）里也没有 ⇒ 前端无从知道降级发生`,
    )
  })

  // ---- 负控：判据必须对「标记被摘掉」转红 ----
  //
  // 不写这条，上面那个「顺着调用找函数」就可能退化成恒真
  // （只要分支里还有任何函数调用就去找，找到什么都算过）。
  it('负控：标记被摘掉时上面那条判据必须报红', () => {
    const go = readFileSync(
      join(here, '../../../../backend/internal/server/server_meeting.go'),
      'utf8',
    )
    const i = go.indexOf('parsed, err := parseRefineJSON(content)')
    const branch = braceBlockAfter(go, go.indexOf('if err != nil', i))!
    const called = [...branch.matchAll(/\b([a-z][A-Za-z0-9_]*)\s*\(/g)]
      .map((m) => m[1])
      .filter((n) => !['if', 'for', 'switch', 'return'].includes(n))
    // 模拟「标记被删掉」：分支本身与它调到的函数体里都不含标记。
    const mutant = go.replace(/\s*"refine_fallback":\s*true,?/g, '')
    const mutatedBranch = braceBlockAfter(mutant, mutant.indexOf('if err != nil', i))!
    const mutantHit =
      mutatedBranch.includes('"refine_fallback"') ||
      called.some((n) => (bodyOfFunc(mutant, n) ?? '').includes('"refine_fallback"'))
    assert.equal(mutantHit, false, '负控失败：标记删掉后判据仍然放行')
    // 同时确认基线确实是绿的，否则这条负控证明不了什么。
    const baselineHit =
      branch.includes('"refine_fallback"') ||
      called.some((n) => (bodyOfFunc(go, n) ?? '').includes('"refine_fallback"'))
    assert.equal(baselineHit, true, '基线就不绿：用例前提失效')
  })

  it('toast 在降级时不说「精翻完成」', () => {
    const src = stripComments(read('../meetings/meeting-recording-finalize.ts'))
    const i = src.indexOf('meetingsApi.refine(')
    assert.ok(i > 0, '找不到 refine 的调用点')
    const tail = src.slice(i, i + 3000)

    // ⚠ 2026-10-07：降级分支被**抽成了纯函数** `buildRefineNotice`
    // （features/sessions/refine-outcome-notice.ts），调用点不再内联
    // `if (result.fromFallback)`。原判据因此误报红 —— 与本文件上面那条
    // 「字符距离」判据同型：它量的是**某段文本的形状**，不是不变量。
    //
    // 这里改为**认两种结构**，但只保一条不变量：
    // 「降级判定必须发生在成功 toast 之前」。
    //
    // `buildRefineNotice` 自身的行为断言在
    //   __tests__/refine-rejected-notice.test.ts
    // （覆盖 fromFallback / rejected 两种降级各自该说什么话）。
    // 本条**不重复**那份，只钉接线：降级判定确实接上了，且接在成功提示之前。
    const inline = tail.indexOf('if (result.fromFallback)')
    const helper = tail.indexOf('buildRefineNotice(')
    const decisionAt = inline > 0 ? inline : helper
    assert.ok(
      decisionAt > 0,
      'refine 之后既没有内联 fromFallback 分支，也没有 buildRefineNotice ⇒ 降级时仍会报「精翻完成」',
    )
    // 走 helper 时，喂给它的必须是**真的降级信号**。
    // ⚠ 只查键名 `fromFallback` 在不在是不够的：把它改成 `fromFallback: false`
    //   一样过 —— 而那正是「降级信号在这一跳被丢掉」的形态（变异 M2 实测打不红）。
    // ⇒ 钉的是**值来自 result**，不是「有个这个键」。
    if (helper > 0 && inline < 0) {
      const callEnd = tail.indexOf(')', helper)
      const args = tail.slice(helper, callEnd)
      assert.match(
        args,
        /fromFallback\s*:\s*result\.fromFallback/,
        '调用了 buildRefineNotice，却没有把 result.fromFallback 原样传进去（写成常量即为信号丢失）',
      )
    }
    // ⚠ 锚点要精确到 toast 调用这一句，不能只搜「精翻完成」——
    // 否则会命中**注释里**那句引述，而它在判断之前。
    const ok = tail.indexOf("toast.success('录音已结束，精翻完成')")
    assert.ok(
      ok === -1 || ok > decisionAt,
      '成功 toast 出现在降级判断之前 ⇒ 降级分支永远走不到',
    )
  })
})
