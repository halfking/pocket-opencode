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
 *   实际存在**两个**各自都有单测的判据函数——
 *   - `shouldContinueClassify`（取消 / 清空 / 整批全失败 / 轮次上限）
 *   - `classifyRunVerdict`（取消 / 清空 / 连续零进展）
 * 合并后调用点选用哪个是实现选择，不是缺陷。护栏真正要挡的是「判据存在、
 * 循环没接」，所以这里改成匹配**任一**判据函数，但保留下面那条更严的约束：
 * 继续标志必须**由判据函数的返回值赋值**，这样「import 了却没用」仍然会红。
 *
 * 2026-10-04 合并修订（mergeprobe2，对 main 243cda44 解冲突）：调用点不再内联
 * 循环，改用 `runClassifyLoop`（循环整体抽成 async 泛型函数，终止判定在函数
 * 内部）。于是「继续标志 = 判据函数返回值」这条断言在当前接线下**永远不成立**
 * ——继续与否由 runClassifyLoop 内部的 for 循环决定，调用点看不到布尔量。
 *
 * 关键点：main 侧内联接线之所以被放弃，是因为实测它**没接上判据**——
 * `git grep classifyRunVerdict 243cda44 -- frontend/src` 显示该函数只出现在
 * 自己的单测与本护栏里，use-email-inbox.ts 从未 import 或调用它。也就是说
 * main 当时正处在本文件开头描述的那个缺陷形态里。
 *
 * 所以护栏改成按**接线形态**匹配：既接受「内联判据函数」，也接受
 * 「把循环交给 runClassifyLoop」。两者都必须是**真的调用**（不是只 import），
 * 且都必须把停止原因如实显示给用户。判据仍刻意匹配调用点而非「出现过这个词」。
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

/**
 * 两种等价可用的接线形态（见文件头两次修订）：
 *   - 内联判据：`(continueLoop|running) = 判据函数(...)`
 *   - 抽出的循环：`runClassifyLoop({...})`
 * 护栏真正要挡的是「判据存在、循环没接」，所以按形态匹配，但两种都要求
 * 出现在**调用点**（有实参 / 有函数体），而不是只出现在 import 行。
 */
const DECISION = String.raw`(shouldContinueClassify|classifyRunVerdict)`
const INLINE_DECISION = new RegExp(String.raw`(continueLoop|running)\s*=\s*${DECISION}\s*\(`)
const LOOP_HANDOFF = /runClassifyLoop(?:<[^>()]*>)?\s*\(\s*\{/

/** 当前代码用的是哪一种接线形态。两条断言共用，避免各自判断后互相矛盾。 */
const usesInlineDecision = INLINE_DECISION.test(code)
const usesLoopHandoff = LOOP_HANDOFF.test(code)

describe('classify loop wiring', () => {
  it('imports a decision function or the extracted loop as a real code reference', () => {
    const hasDecisionImport = new RegExp(
      String.raw`import\s*\{[^}]*\b${DECISION}\b[^}]*\}\s*from\s*['"][^'"]*email-classify-run['"]`,
    ).test(code)
    const hasLoopImport = /import\s*\{[^}]*\brunClassifyLoop\b[^}]*\}\s*from\s*['"][^'"]*email-classify-loop['"]/
      .test(code)
    assert.ok(
      hasDecisionImport || hasLoopImport,
      'use-email-inbox.ts 必须从 email-classify-run import 判据函数，或从 '
      + 'email-classify-loop import runClassifyLoop',
    )
  })

  it('actually calls the loop/decision function (not just imports it)', () => {
    assert.match(
      code,
      new RegExp(String.raw`(?:\b${DECISION}\s*\(|runClassifyLoop(?:<[^>()]*>)?\s*\()`),
      '判据函数或 runClassifyLoop 必须被真的调用，不能只出现在 import 行',
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

  it('loop continuation comes from a decision function, not an unrelated boolean', () => {
    // 两种接线形态各自的要求：
    //   内联判据 —— 继续标志必须由判据函数的返回值赋值（挡住「import 了却没用」）。
    //   抽出的循环 —— runClassifyLoop 必须带实参对象被调用，而不是写成
    //     `runClassifyLoop` 光杆调用（那等价于没接：内部拿不到 fetchBatch）。
    if (usesInlineDecision) return
    assert.ok(usesLoopHandoff, '循环的继续判定必须来自判据函数或 runClassifyLoop')
  })

  it('the no-progress case is detected and surfaced, not spun on', () => {
    // 零进展判据必须在**代码里**被真的用上，而不是只出现在 import 行。
    //
    // 内联形态下判据写在调用点（自己比较 noProgressPasses 阈值）；抽出形态下
    // 判据在 runClassifyLoop 内部，本文件只锁住「调用点用了这个循环」以及
    // 「停止原因被如实转述给用户」。runClassifyLoop 内部的 no-progress 分支
    // 由 __tests__/email-classify-loop.test.mjs 逐条钉住，不在本文件重复。
    if (!usesInlineDecision) {
      assert.ok(
        usesLoopHandoff,
        '必须把循环交给 runClassifyLoop，否则零进展时 provider 未配置会空转到批次数上限',
      )
    } else {
      // 刻意不匹配常量名本身：`MAX_NO_PROGRESS_PASSES` 出现在 import 里时
      // 大小写与代码里的 `noProgressPasses` 不同，所以下面两种「用上」的形态
      // （自己比较阈值 / 把它当参数交给判据函数）才算是数。
      assert.match(
        code,
        /noProgressPasses\s*(>=|>|===|==)|noProgressPasses\s*[,}]/,
        '必须有一处零进展判定，否则 provider 未配置时空转到轮次上限',
      )
    }
    // 且停下之后必须如实告诉用户，不能只留一个不动的进度条。
    // 抽出形态用 classifyStopHint，内联形态用 classifyDoneHint。
    assert.match(
      code,
      /classifyHint\.value\s*=\s*[^;]*(归类未生效|classifyDoneHint|classifyStopHint)/,
      '停下来的原因必须如实告诉用户，不能只留一个不动的进度条',
    )
  })
})
