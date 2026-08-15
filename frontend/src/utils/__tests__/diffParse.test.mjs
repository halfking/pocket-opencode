/**
 * parseUnifiedDiff / extractDiffText tests（P2 E5-S3 分段渲染的解析层）。
 */
import { strict as assert } from 'node:assert'
import { test } from 'node:test'

import {
  extractDiffText,
  looksLikeUnifiedDiff,
  parseUnifiedDiff,
} from '../../utils/diffParse.ts'

const SAMPLE = [
  'diff --git a/src/a.ts b/src/a.ts',
  'index 1a2b3c4..5d6e7f8 100644',
  '--- a/src/a.ts',
  '+++ b/src/a.ts',
  '@@ -10,4 +10,6 @@ function hello()',
  ' context line',
  '-removed line',
  '+added line',
  '+another added line',
  ' context line 2',
  '\\ No newline at end of file',
].join('\n')

test('looksLikeUnifiedDiff：hunk 头是信号', () => {
  assert.equal(looksLikeUnifiedDiff(SAMPLE), true)
  assert.equal(looksLikeUnifiedDiff('+++ b/x\n--- a/x\nno hunk'), false)
  assert.equal(looksLikeUnifiedDiff(''), false)
  assert.equal(looksLikeUnifiedDiff('普通日志输出\n+加号开头也不算'), false)
})

test('parseUnifiedDiff：meta/hunk/行分类与统计', () => {
  const d = parseUnifiedDiff(SAMPLE)
  assert.ok(d)
  assert.equal(d.hunks.length, 1)
  assert.equal(d.meta.length, 4) // diff --git / index / --- / +++
  const h = d.hunks[0]
  assert.equal(h.oldStart, 10)
  assert.equal(h.newStart, 10)
  assert.equal(h.adds, 2)
  assert.equal(h.dels, 1)
  assert.deepEqual(
    h.lines.map((l) => l.type),
    ['context', 'del', 'add', 'add', 'context'],
  )
  assert.deepEqual(h.lines[1], { type: 'del', text: 'removed line' })
  // "\ No newline" 不计入行数
  assert.equal(d.totalLines, 5)
  assert.equal(d.adds, 2)
  assert.equal(d.dels, 1)
})

test('parseUnifiedDiff：无 hunk 头返回 null', () => {
  assert.equal(parseUnifiedDiff('hello world'), null)
})

test('parseUnifiedDiff：多文件 diff 的文件头绑定到首个 hunk，不产生假增删行', () => {
  const multi = [
    ...SAMPLE.split('\n'),
    'diff --git a/src/b.ts b/src/b.ts',
    '--- a/src/b.ts',
    '+++ b/src/b.ts',
    '@@ -1,2 +1,2 @@',
    ' ctx',
    '-old',
    '+new',
  ].join('\n')
  const d = parseUnifiedDiff(multi)
  assert.ok(d)
  assert.equal(d.hunks.length, 2)
  assert.equal(d.adds, 3)
  assert.equal(d.dels, 2)
  // 第二个文件头三行不产生增删
  assert.equal(d.hunks[1].adds, 1)
  assert.equal(d.hunks[1].dels, 1)
  assert.ok(d.hunks[1].fileMeta.some((l) => l.startsWith('diff --git a/src/b.ts')))
})

test('parseUnifiedDiff：hunk 内以 --- 开头的删除内容不误判为文件头', () => {
  const d = parseUnifiedDiff([
    '--- a/x',
    '+++ b/x',
    '@@ -1,1 +1,1 @@',
    '---actual text',
    '+replacement',
  ].join('\n'))
  assert.ok(d)
  assert.equal(d.hunks.length, 1)
  assert.equal(d.hunks[0].dels, 1)
  assert.deepEqual(d.hunks[0].lines[0], { type: 'del', text: '--actual text' })
})

test('parseUnifiedDiff：单 hunk 5,000 行保持完整解析，供视图按批挂载', () => {
  const lines = ['--- a/big.txt', '+++ b/big.txt', '@@ -1,5000 +1,5000 @@']
  for (let i = 0; i < 5000; i++) lines.push(i % 2 === 0 ? `-del ${i}` : `+add ${i}`)
  const d = parseUnifiedDiff(lines.join('\n'))
  assert.ok(d)
  assert.equal(d.hunks.length, 1)
  assert.equal(d.hunks[0].lines.length, 5000)
  assert.equal(d.hunks[0].adds, 2500)
  assert.equal(d.hunks[0].dels, 2500)
})

test('parseUnifiedDiff：5,000 行输入解析正确且 hunk 独立分段', () => {
  const lines = ['--- a/big.txt', '+++ b/big.txt']
  let expectedAdds = 0
  let expectedDels = 0
  for (let h = 0; h < 100; h++) {
    lines.push(`@@ -${h * 50 + 1},50 +${h * 50 + 1},50 @@`)
    for (let i = 0; i < 50; i++) {
      if (i % 5 === 0) {
        lines.push(`-del ${h}-${i}`)
        expectedDels++
      } else if (i % 5 === 1) {
        lines.push(`+add ${h}-${i}`)
        expectedAdds++
      } else {
        lines.push(` ctx ${h}-${i}`)
      }
    }
  }
  const d = parseUnifiedDiff(lines.join('\n'))
  assert.ok(d)
  assert.equal(d.hunks.length, 100)
  assert.equal(d.adds, expectedAdds)
  assert.equal(d.dels, expectedDels)
  assert.equal(d.totalLines, 100 * 50)
  assert.equal(d.hunks[99].adds, 10)
})

test('extractDiffText：字符串直判，对象浅扫常见字段', () => {
  assert.equal(extractDiffText(SAMPLE), SAMPLE)
  assert.equal(extractDiffText('no diff here'), null)
  assert.equal(extractDiffText({ diff: SAMPLE }), SAMPLE)
  assert.equal(extractDiffText({ output: { patch: SAMPLE } }), null) // 只扫一层，刻意保守
  assert.equal(extractDiffText(42), null)
  assert.equal(extractDiffText(null), null)
})
