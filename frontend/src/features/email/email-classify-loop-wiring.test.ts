/**
 * 归类循环的**接线**护栏。
 *
 * Run: node --experimental-strip-types --test src/features/email/email-classify-loop-wiring.test.ts
 *
 * 为什么需要它：`email-classify-run.test.ts` 只测纯函数
 * `classifyRunVerdict`，而纯函数绿着**不能证明** `use-email-inbox.ts`
 * 真的在用它。2026-10-02 那次缺陷的原形态恰恰是「判据存在、循环没接」
 * ——终止条件写死成 `remaining <= 0`，空转时永远退不出来。
 *
 * 所以这里做源码级断言，锁住三件事：
 *   1. 调用点确实 import 了 classifyRunVerdict 并真的调用了它；
 *   2. 旧的「只认 remaining<=0」的 do-while 终止写法已经不在；
 *   3. 循环的继续条件来自 verdict，而不是某个跟判据无关的布尔量。
 *
 * 判据刻意匹配**调用点**而不是「出现过这个词」——否则把接线整行注释掉
 * 也能满足。下面的负控就是按「删掉接线只留注释」这一形态设计的。
 */
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { describe, it } from 'node:test'

const src = readFileSync(new URL('./use-email-inbox.ts', import.meta.url), 'utf8')

/** 去掉注释后再匹配，避免「把接线注释掉」也能满足断言。 */
const stripComments = (t: string): string =>
  t
    .replace(/\/\*[\s\S]*?\*\//g, ' ')
    .replace(/(^|[^:])\/\/[^\n]*/g, '$1 ')

const code = stripComments(src)

describe('classify loop wiring', () => {
  it('imports classifyRunVerdict as a real code reference', () => {
    assert.match(
      code,
      /import\s*\{[^}]*\bclassifyRunVerdict\b[^}]*\}\s*from\s*['"][^'"]*email-classify-run['"]/,
      'use-email-inbox.ts 必须 import classifyRunVerdict',
    )
  })

  it('actually calls the verdict (not just imports it)', () => {
    assert.match(code, /classifyRunVerdict\s*\(/, 'classifyRunVerdict 必须被真的调用')
  })

  it('the old remaining<=0-only do-while termination is gone', () => {
    assert.doesNotMatch(
      code,
      /while\s*\(\s*!classifyCancel\.value\s*\)/,
      '旧的 `while (!classifyCancel.value)` 循环还在 —— 零进展时退不出来',
    )
    assert.doesNotMatch(
      code,
      /if\s*\(\s*classifyCancel\.value\s*\|\|\s*remain\s*<=\s*0\s*\)\s*break/,
      '旧的 `if (cancel || remain <= 0) break` 终止条件还在',
    )
  })

  it('loop continuation is driven by the verdict', () => {
    assert.match(
      code,
      /verdict\.kind\s*===\s*['"]continue['"]/,
      '循环的继续条件必须来自 verdict.kind === continue',
    )
  })

  it('the stalled case is surfaced to the user instead of spinning silently', () => {
    assert.match(code, /verdict\.kind\s*===\s*['"]stalled['"]/, '必须识别 stalled')
    assert.match(
      code,
      /classifyHint\.value\s*=\s*[^;]*归类未生效/,
      '停下来的原因必须如实告诉用户，不能只留一个不动的进度条',
    )
  })
})
