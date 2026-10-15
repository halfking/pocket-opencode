import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

// 客户端兜底摘要解析的「合法但空」守卫。
//
// ⚠ 这类纯逻辑无法行为断言：parseSummaryJson 是 **non-exported**，
// 而 `src/api/meetings.ts` 自身用了无扩展名 import（'./http'），
// 项目 runner 是裸 `node --test`（无 tsx loader）—— 那个模块在测试里
// import 不了。所以这里只能源码断言，局限写在下面。
//
// 能抓住：守卫被删 / 调用点不再传 transcript / catch 分支丢了兜底。
// 抓不住：守卫写成但条件恒假（例如误写成 `=== undefined`）。
// 要彻底封住需要把 parseSummaryJson 拆到无 import 依赖的纯模块里。
const here = dirname(fileURLToPath(import.meta.url))
const api = readFileSync(join(here, './meetings.ts'), 'utf8')

const fnStart = api.indexOf('function parseSummaryJson')
const fnEnd = api.indexOf('\nfunction ', fnStart + 10)
const fn = api.slice(fnStart, fnEnd > 0 ? fnEnd : fnStart + 2000)

describe('客户端兜底摘要解析', () => {
  it('函数存在且带 fallbackText 形参', () => {
    assert.ok(fnStart > 0, '找不到 parseSummaryJson')
    assert.match(
      fn,
      /function parseSummaryJson\(content:\s*string,\s*fallbackText\s*=\s*''\)/,
      'parseSummaryJson 没有 fallbackText 形参：无法在摘要不可用时回落到转写',
    )
  })

  it('「合法但空」必须走回落，而不是返回空摘要', () => {
    // 服务端 parseSummaryJSON 已在 2026-10-06 修过同一类缺陷
    // （真网关实测：滚动摘要第 3 轮返回 {"summary":"", …}）。
    assert.match(
      fn,
      /if\s*\(\s*!String\(raw\)\.trim\(\)\s*\)/,
      '没有对空摘要做 trim 检查 ⇒ 合法但空的 JSON 会变成空白摘要面板',
    )
    assert.match(
      fn,
      /emptySummary\(fallbackText/,
      '空摘要时没有回落到 fallbackText',
    )
  })

  it('非法 JSON 的 catch 分支同样要有兜底', () => {
    const catchAt = fn.indexOf('catch')
    assert.ok(catchAt > 0, '找不到 catch 分支')
    assert.match(
      fn.slice(catchAt),
      /emptySummary\(/,
      'catch 分支没有兜底：云端返回非 JSON 时会丢掉转写',
    )
  })

  it('兜底调用点必须把已算好的 transcript 传进去', () => {
    assert.match(
      api,
      /parseSummaryJson\(res\.content,\s*transcript\)/,
      'fallbackSummarize 没有把 transcript 传进解析器 ⇒ 兜底拿不到内容',
    )
  })

  it('提示词不再要求没人读的 tldr / topics', () => {
    // 这两个字段解析器仍容忍（兼容老响应），但**要求**模型写它们会
    // 让内容被拆到两个字段里，而只有 summary 被界面渲染。
    //
    // ⚠ §513：锚点自证是**必需**的，不是保险。
    //   下面这条例外的方向是「负向」的 —— 断言「窗口里没有某个串」。
    //   起点锚一旦消失，`indexOf` 返回 -1，单参 `slice(-1)` 交出的是
    //   **源串最后一个字符**（实测 1 字符，非空串），于是
    //   `includes(某串)` 必假、`!` 之后必真 ⇒ **判据自动通过**。
    //   阴性验证：把那个函数改名后本条仍报 ok（见设计文档 §513），
    //   而同文件的另一条会红 —— 也就是说本条对锚点消失是**全盲**的。
    //   注意「非空」救不了它：退化读数恰好是非空的。
    const fbAt = api.indexOf('function fallbackSummarize')
    assert.ok(fbAt > 0, '兜底摘要函数改名/搬走了 —— 本条是负向判据，锚点缺失会让它恒真')
    const prompts = api.slice(fbAt)
    assert.ok(
      !prompts.slice(0, 1600).includes('"tldr"'),
      '兜底提示词仍要求 tldr：内容会被拆走，且没有消费方',
    )
  })

  it('§220 降级链自己的 catch 分支必须显式声明「这不是摘要」', () => {
    // ⚠ 为什么断言的是 catch 分支，而**不是**整个 fallbackSummarize：
    //   成功路径 return parseSummaryJson(...) 由它自己决定要不要置位
    //   （拿到空摘要时经 emptySummary 置位）⇒ 那条路上不该硬写一个标记。
    //   只有 catch 分支是**手写的对象字面量**，它绕过 emptySummary，
    //   不显式写就永远不会被标记。
    const fbStart = api.indexOf('async function fallbackSummarize')
    assert.ok(fbStart > 0, '找不到 fallbackSummarize')
    const fbEnd = api.indexOf('\n/**', fbStart + 10)
    const fb = api.slice(fbStart, fbEnd > 0 ? fbEnd : fbStart + 3000)
    const catchAt = fb.indexOf('catch')
    assert.ok(catchAt > 0, '找不到 fallbackSummarize 的 catch 分支')
    assert.match(
      fb.slice(catchAt),
      /summaryFallback:\s*true/,
      '降级链自己的 catch 分支没置 summaryFallback：这条路上连降级都失败了，' +
      '而界面会按「没有 agent」解释、说出「已回落到一次性摘要」——' +
      '那个一次性的正是失败者',
    )
  })
})
