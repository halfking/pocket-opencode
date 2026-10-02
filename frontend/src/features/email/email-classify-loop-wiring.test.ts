/**
 * 归类循环的**接线**护栏。
 *
 * Run: node --experimental-strip-types --test src/features/email/email-classify-loop-wiring.test.ts
 *
 * 为什么需要它：纯函数测绿**不能证明** `use-email-inbox.ts` 真的在用它。
 * 2026-10-02 那次缺陷的原形态恰恰是「判据存在、循环没接」——终止条件写死成
 * `remaining <= 0`，空转时永远退不出来。
 *
 * 所以这里做源码级断言，锁住三件事：
 *   1. 调用点确实 import 了某个判据函数并真的调用了它；
 *   2. 旧的「只认 remaining<=0」的 do-while 终止写法已经不在；
 *   3. 循环的继续条件**来自那个判据函数的返回值**，而不是某个跟判据无关的
 *      布尔量。
 *
 * 2026-10-02 合并修订：判据函数此前写死成 `classifyRunVerdict`。但同模块里
 * 实际存在**两个**各自都有单测的判据函数——
 *   - `shouldContinueClassify`（取消 / 清空 / 整批全失败 / 轮次上限）
 *   - `classifyRunVerdict`（取消 / 清空 / 连续零进展）
 * 合并后调用点选用哪个是实现选择，不是缺陷。护栏真正要挡的是「判据存在、
 * 循环没接」，所以这里改成匹配**任一**判据函数，但保留下面那条更严的约束：
 * 继续标志必须**由判据函数的返回值赋值**，这样「import 了却没用」仍然会红。
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

/** 两个等价可用的判据函数，见文件头 2026-10-02 合并修订。 */
const DECISION = String.raw`(shouldContinueClassify|classifyRunVerdict)`

describe('classify loop wiring', () => {
  it('imports a decision function as a real code reference', () => {
    assert.match(
      code,
      new RegExp(
        String.raw`import\s*\{[^}]*\b${DECISION}\b[^}]*\}\s*from\s*['"][^'"]*email-classify-run['"]`,
      ),
      'use-email-inbox.ts 必须从 email-classify-run import 判据函数',
    )
  })

  it('actually calls the decision function (not just imports it)', () => {
    assert.match(
      code,
      new RegExp(String.raw`\b${DECISION}\s*\(`),
      '判据函数必须被真的调用',
    )
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

  it('loop continuation is assigned FROM the decision function, not an unrelated boolean', () => {
    assert.match(
      code,
      new RegExp(String.raw`(continueLoop|running)\s*=\s*${DECISION}\s*\(`),
      '循环的继续标志必须由判据函数的返回值赋值',
    )
  })

  it('the no-progress case is detected and surfaced, not spun on', () => {
    // 零进展判据必须在**代码里**被真的用上，而不是只出现在 import 行。
    // 这里刻意不匹配常量名本身：`MAX_NO_PROGRESS_PASSES` 出现在 import 里时
    // 大小写与代码里的 `noProgressPasses` 不同，所以下面两种「用上」的形态
    // （自己比较阈值 / 把它当参数交给判据函数）才算是数。
    // 负控：把判定整段删掉、只留 import 行，本条必须转红。
    assert.match(
      code,
      /noProgressPasses\s*(>=|>|===|==)|noProgressPasses\s*[,}]/,
      '必须有一处零进展判定，否则 provider 未配置时空转到轮次上限',
    )
    // 且停下之后必须如实告诉用户，不能只留一个不动的进度条。
    assert.match(
      code,
      /classifyHint\.value\s*=\s*[^;]*(归类未生效|classifyDoneHint)/,
      '停下来的原因必须如实告诉用户，不能只留一个不动的进度条',
    )
  })
})
